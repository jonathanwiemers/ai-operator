package controller

import (
	"context"
	"fmt"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

// revisitClockSkew widens the "updated after" window, so comments are not
// missed when the operator's and GitLab's clocks disagree. The note ID
// watermark keeps them from being handled twice.
const revisitClockSkew = 5 * time.Minute

// syncRevisits turns "/agent revisit <id> …" comments on closed issues of a
// planned milestone, and on its autopilot tracking issue, into follow-up
// issues. Revisits on open issues are handled by their runner.
//
// A follow-up carries the old decision and the instruction, and a marker
// naming the decision, so its implementer records a decision that supersedes
// it. On autopilot it joins the queue; otherwise it goes straight to ready,
// since a human asked for it.
func (r *AgentProjectReconciler) syncRevisits(ctx context.Context, gl gitlab.API, gate *commandGate, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, st *aiv1.AgentTask) error {
	if st.Spec.Milestone == nil {
		return nil
	}
	now := r.now()
	if st.Status.Revisits == nil {
		// Start watching now; comments written before are history.
		orig := st.DeepCopy()
		st.Status.Revisits = &aiv1.RevisitStatus{Since: metav1.Time{Time: now}, CheckedAt: metav1.Time{Time: now}}
		return r.Status().Patch(ctx, st, client.MergeFrom(orig))
	}
	rs := st.Status.Revisits
	glProject := spec.GitLab.Project
	after := rs.CheckedAt.Add(-revisitClockSkew)
	closed, err := gl.ListIssues(ctx, glProject, gitlab.ListIssuesOptions{Milestone: st.Spec.Milestone.Title, State: "closed", UpdatedAfter: &after})
	if err != nil {
		return fmt.Errorf("listing closed issues of milestone %q: %w", st.Spec.Milestone.Title, err)
	}
	// Closed issues count from when they were closed: a revisit written
	// while the issue was open was its runner's. The tracking issue counts
	// while autopilot is on.
	tracking := int64(0)
	if ap := st.Status.Autopilot; ap != nil && ap.Phase != aiv1.AutopilotOff {
		tracking = ap.TrackingIssueIID
	}
	type target struct {
		iid  int64
		from *time.Time
	}
	var targets []target
	for _, is := range closed {
		if ap := st.Status.Autopilot; ap != nil && is.IID == ap.TrackingIssueIID {
			continue // handled below, or not at all when off
		}
		targets = append(targets, target{is.IID, is.ClosedAt})
	}
	if tracking != 0 {
		targets = append(targets, target{iid: tracking})
	}
	for _, t := range targets {
		notes, err := gl.ListIssueNotes(ctx, glProject, t.iid)
		if err != nil {
			return fmt.Errorf("listing notes of issue #%d: %w", t.iid, err)
		}
		key := strconv.FormatInt(t.iid, 10)
		for _, n := range notes {
			if n.ID <= rs.LastNoteIDs[key] || !IsCommand(n, project.Status.BotUserID, spec.Workflow.CommandPrefix) ||
				n.CreatedAt == nil || n.CreatedAt.Before(rs.Since.Time) || (t.from != nil && n.CreatedAt.Before(*t.from)) {
				continue
			}
			m := marker.Revisit.FindStringSubmatch(n.Body)
			if m == nil || m[1] != spec.Workflow.CommandPrefix {
				continue
			}
			ok, err := gate.counts(ctx, n, st.Spec.Owners)
			switch {
			case err != nil:
				return err
			case !ok:
				err = gate.deny(ctx, st, pilotNote{Note: n, issue: t.iid}, st.Spec.Owners)
			default:
				err = r.revisitAfterMerge(ctx, gl, spec, st, project.Status.BotUserID, t.iid, n, strings.ToUpper(m[2]), strings.TrimSpace(m[3]))
			}
			if err != nil {
				return err
			}
			// Saved per comment, so a later failure does not handle it again.
			orig := st.DeepCopy()
			if st.Status.Revisits.LastNoteIDs == nil {
				st.Status.Revisits.LastNoteIDs = map[string]int64{}
			}
			st.Status.Revisits.LastNoteIDs[key] = n.ID
			if err := r.Status().Patch(ctx, st, client.MergeFrom(orig)); err != nil {
				return err
			}
			rs = st.Status.Revisits
		}
	}
	// Revisits from proposals an owner accepted, once each.
	if ps := st.Status.Pilot; ps != nil {
		for _, c := range ps.Commands {
			if c.Seq <= rs.LastPilotCommand {
				continue
			}
			if c.Action.Tool == "revisit" {
				n := gitlab.Note{Author: gitlab.User{Username: c.Owner + " via the pilot"}}
				if err := r.revisitAfterMerge(ctx, gl, spec, st, project.Status.BotUserID, c.OnIssue, n, c.Action.Decision, inert(c.Action.Note)); err != nil {
					return err
				}
			}
			orig := st.DeepCopy()
			st.Status.Revisits.LastPilotCommand = c.Seq
			if err := r.Status().Patch(ctx, st, client.MergeFrom(orig)); err != nil {
				return err
			}
			rs = st.Status.Revisits
		}
	}
	if len(closed) == 0 {
		return nil // nothing new: keep the window, save a status write
	}
	orig := st.DeepCopy()
	st.Status.Revisits.CheckedAt = metav1.Time{Time: now}
	return r.Status().Patch(ctx, st, client.MergeFrom(orig))
}

// revisitAfterMerge creates the follow-up issue for one revisit comment, or
// replies why it does not.
func (r *AgentProjectReconciler) revisitAfterMerge(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, st *aiv1.AgentTask, botID, onIssue int64, n gitlab.Note, id, instruction string) error {
	glProject := spec.GitLab.Project
	wf := spec.Workflow
	reply := func(body string) error {
		if _, err := gl.CreateIssueNote(ctx, glProject, onIssue, "🤖 "+body); err != nil {
			return fmt.Errorf("replying on issue #%d: %w", onIssue, err)
		}
		return nil
	}
	targetIID := marker.DecisionIssue(id)
	target, err := gl.GetIssue(ctx, glProject, targetIID)
	switch {
	case gitlab.IsNotFound(err):
		return reply(fmt.Sprintf("Decision %s belongs to #%d, which does not exist.", id, targetIID))
	case err != nil:
		return fmt.Errorf("getting issue #%d: %w", targetIID, err)
	case target.Milestone == nil || target.Milestone.ID != st.Spec.Milestone.ID:
		return reply(fmt.Sprintf("Decision %s belongs to #%d, which is not in this milestone.", id, targetIID))
	case target.State == "opened":
		return reply(fmt.Sprintf("#%d is still open: comment `%s revisit %s …` there, and its next run changes the decision.", targetIID, wf.CommandPrefix, id))
	}

	// Find the decision in the bot's decision comments; planning decisions
	// of older issues are in the description.
	notes, err := gl.ListIssueNotes(ctx, glProject, targetIID)
	if err != nil {
		return fmt.Errorf("listing notes of issue #%d: %w", targetIID, err)
	}
	section := ""
	for _, tn := range notes {
		if botID != 0 && tn.Author.ID == botID && !tn.System && strings.HasPrefix(tn.Body, marker.DecisionsHeader) {
			if section = marker.DecisionSection(tn.Body, id); section != "" {
				break
			}
		}
	}
	if section == "" && strings.Contains(id, "-P") {
		section = marker.DecisionSection(target.Description, id)
	}
	switch {
	case section == "":
		return reply(fmt.Sprintf("Decision %s was not found on #%d.", id, targetIID))
	case strings.Contains(section, id+" superseded -->"):
		return reply(fmt.Sprintf("Decision %s was already superseded; revisit the decision that replaced it.", id))
	}

	// One follow-up per decision.
	all, err := gl.ListIssues(ctx, glProject, gitlab.ListIssuesOptions{Milestone: st.Spec.Milestone.Title, State: "opened"})
	if err != nil {
		return fmt.Errorf("listing issues of milestone %q: %w", st.Spec.Milestone.Title, err)
	}
	autopilot := st.Status.Autopilot != nil && st.Status.Autopilot.Phase != aiv1.AutopilotOff && st.Spec.Autopilot
	for _, is := range all {
		if m := marker.RevisitOf.FindStringSubmatch(is.Description); m != nil && m[1] == id && is.Author.ID == botID {
			if autopilot && !onAutopilot(st, is.IID) && is.HasLabel(wf.DraftLabel) {
				// Created on autopilot, but queuing it failed last time.
				return r.queueFollowUp(ctx, st, is.IID, id, reply, "Queued")
			}
			return reply(fmt.Sprintf("#%d already revisits %s; comment there.", is.IID, id))
		}
	}

	labels := []string{wf.GeneratedLabel, wf.ReadyLabel}
	if autopilot {
		labels[1] = wf.DraftLabel // the queue hands it over
	}
	if profile := spec.ProfileOf(target.Labels); profile != "" {
		labels = append(labels, spec.ProfileLabel(profile))
	}
	// The follow-up runs like the issue it revisits.
	for _, l := range target.Labels {
		if strings.HasPrefix(l, wf.ModelLabelPrefix) || strings.HasPrefix(l, wf.EffortLabelPrefix) {
			labels = append(labels, l)
		}
	}
	title := id
	if h := marker.Decisions(section); len(h) > 0 {
		title = fmt.Sprintf("%s: %s", id, h[0].Title)
	}
	is, err := gl.CreateIssue(ctx, glProject, gitlab.CreateIssueOptions{
		Title:       textutil.Truncate("Revisit "+title, 254),
		Description: renderFollowUp(id, section, instruction, n, target, wf.CommandPrefix),
		Labels:      strings.Join(labels, ","),
		MilestoneID: st.Spec.Milestone.ID,
	})
	if code := gitlab.StatusCode(err); code >= 400 && code < 500 && code != 408 && code != 429 {
		// It would fail again on every poll: say so and move on.
		log.FromContext(ctx).Info("revisit after merge: GitLab refused the follow-up", "decision", id, "error", err.Error())
		return reply(fmt.Sprintf("Could not create the follow-up for %s: GitLab refused it (HTTP %d). Open the issue by hand.", id, code))
	}
	if err != nil {
		return fmt.Errorf("creating the follow-up for %s: %w", id, err)
	}
	log.FromContext(ctx).Info("revisit after merge: follow-up created", "decision", id, "issue", is.IID, "autopilot", autopilot)
	if autopilot {
		return r.queueFollowUp(ctx, st, is.IID, id, reply, "Created")
	}
	return reply(fmt.Sprintf("Created #%d to revisit %s; the agent starts on it now.", is.IID, id))
}

// queueFollowUp appends a follow-up to the autopilot queue.
func (r *AgentProjectReconciler) queueFollowUp(ctx context.Context, st *aiv1.AgentTask, iid int64, id string, reply func(string) error, verb string) error {
	orig := st.DeepCopy()
	st.Status.Autopilot.Items = append(st.Status.Autopilot.Items,
		aiv1.AutopilotItem{IID: iid, State: aiv1.AutopilotItemPending, RevisitOf: id})
	if err := r.Status().Patch(ctx, st, client.MergeFrom(orig)); err != nil {
		return err
	}
	return reply(fmt.Sprintf("%s #%d to revisit %s. It is queued on autopilot.", verb, iid, id))
}

// renderFollowUp renders the description of a follow-up issue. The old
// decision and the instruction are cleaned, so they cannot carry markers or
// mentions into the bot's issue; the markers come last.
func renderFollowUp(id, section, instruction string, n gitlab.Note, target *gitlab.Issue, prefix string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Follow-up of #%d to revisit decision **%s**, requested by @%s.\n\n", target.IID, id, n.Author.Username)
	b.WriteString("### Decision to revisit\n\n")
	// The old decision without its marker: this issue must not carry it.
	b.WriteString(marker.Clean(marker.Decision.ReplaceAllString(section, "")) + "\n\n")
	instruction = marker.Clean(instruction)
	if instruction == "" {
		instruction = "(no instruction given; see the comments on #" + fmt.Sprint(target.IID) + ")"
	}
	fmt.Fprintf(&b, "### Requested change\n\n%s\n\n", instruction)
	fmt.Fprintf(&b, "#%d is closed and its change is on the default branch. Change the code accordingly on top of it, and record the new "+
		"decision with `supersedes` set to `%s`. Comment `%s …` here to steer the work.\n\n", target.IID, id, prefix)
	fmt.Fprintf(&b, "<!-- ai-operator:revisit=%s -->\n", id)
	return b.String()
}
