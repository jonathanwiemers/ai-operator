package controller

import (
	"context"
	"fmt"
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
func (r *AgentProjectReconciler) syncRevisits(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, st *aiv1.AgentTask) error {
	if st.Spec.Milestone == nil {
		return nil
	}
	now := r.now()
	orig := st.DeepCopy()
	rs := st.Status.Revisits.DeepCopy()
	if rs == nil {
		// Start watching now; comments written before are history.
		st.Status.Revisits = &aiv1.RevisitStatus{Since: metav1.Time{Time: now}, CheckedAt: metav1.Time{Time: now}}
		return r.Status().Patch(ctx, st, client.MergeFrom(orig))
	}
	glProject := spec.GitLab.Project
	after := rs.CheckedAt.Add(-revisitClockSkew)
	closed, err := gl.ListIssues(ctx, glProject, gitlab.ListIssuesOptions{Milestone: st.Spec.Milestone.Title, State: "closed", UpdatedAfter: &after})
	if err != nil {
		return fmt.Errorf("listing closed issues of milestone %q: %w", st.Spec.Milestone.Title, err)
	}
	targets := make([]int64, 0, len(closed)+1)
	for _, is := range closed {
		targets = append(targets, is.IID)
	}
	if ap := st.Status.Autopilot; ap != nil && ap.TrackingIssueIID != 0 && ap.Phase != aiv1.AutopilotOff {
		targets = append(targets, ap.TrackingIssueIID)
	}
	handled := rs.LastNoteID
	for _, iid := range targets {
		notes, err := gl.ListIssueNotes(ctx, glProject, iid)
		if err != nil {
			return fmt.Errorf("listing notes of issue #%d: %w", iid, err)
		}
		for _, n := range notes {
			if n.ID <= rs.LastNoteID || (n.CreatedAt != nil && n.CreatedAt.Before(rs.Since.Time)) ||
				!IsCommand(n, project.Status.BotUserID, spec.Workflow.CommandPrefix) {
				continue
			}
			m := marker.Revisit.FindStringSubmatch(n.Body)
			if m == nil || m[1] != spec.Workflow.CommandPrefix {
				continue
			}
			if err := r.revisitAfterMerge(ctx, gl, spec, st, project.Status.BotUserID, iid, n, strings.ToUpper(m[2]), strings.TrimSpace(m[3])); err != nil {
				return err
			}
			handled = max(handled, n.ID)
		}
	}
	if len(closed) == 0 && handled == rs.LastNoteID {
		return nil // nothing new: keep the window, save a status write
	}
	st.Status.Revisits.LastNoteID = handled
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

	// Find the decision: planner decisions in the description, implementer
	// decisions in the bot's decision comments.
	section := ""
	if strings.Contains(id, "-P") {
		section = marker.DecisionSection(target.Description, id)
	} else {
		notes, err := gl.ListIssueNotes(ctx, glProject, targetIID)
		if err != nil {
			return fmt.Errorf("listing notes of issue #%d: %w", targetIID, err)
		}
		for _, tn := range notes {
			if botID != 0 && tn.Author.ID == botID && !tn.System && strings.HasPrefix(tn.Body, marker.DecisionsHeader) {
				if section = marker.DecisionSection(tn.Body, id); section != "" {
					break
				}
			}
		}
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
	for _, is := range all {
		if m := marker.RevisitOf.FindStringSubmatch(is.Description); m != nil && m[1] == id {
			return reply(fmt.Sprintf("#%d already revisits %s; comment there.", is.IID, id))
		}
	}

	autopilot := st.Status.Autopilot != nil && st.Status.Autopilot.Phase != aiv1.AutopilotOff && st.Spec.Autopilot
	labels := []string{wf.GeneratedLabel, wf.ReadyLabel}
	if autopilot {
		labels[1] = wf.DraftLabel // the queue hands it over
	}
	if profile := spec.ProfileOf(target.Labels); profile != "" {
		labels = append(labels, spec.ProfileLabel(profile))
	}
	title := id
	if h := marker.Decisions(section); len(h) > 0 {
		title = fmt.Sprintf("%s: %s", id, h[0].Title)
	}
	is, err := gl.CreateIssue(ctx, glProject, gitlab.CreateIssueOptions{
		Title:       "Revisit " + title,
		Description: renderFollowUp(id, section, instruction, n, target, wf.CommandPrefix),
		Labels:      strings.Join(labels, ","),
		MilestoneID: st.Spec.Milestone.ID,
	})
	if err != nil {
		return fmt.Errorf("creating the follow-up for %s: %w", id, err)
	}
	log.FromContext(ctx).Info("revisit after merge: follow-up created", "decision", id, "issue", is.IID, "autopilot", autopilot)
	if autopilot {
		orig := st.DeepCopy()
		st.Status.Autopilot.Items = append(st.Status.Autopilot.Items,
			aiv1.AutopilotItem{IID: is.IID, State: aiv1.AutopilotItemPending, RevisitOf: id})
		if err := r.Status().Patch(ctx, st, client.MergeFrom(orig)); err != nil {
			return err
		}
		return reply(fmt.Sprintf("Created #%d to revisit %s. It is queued on autopilot.", is.IID, id))
	}
	return reply(fmt.Sprintf("Created #%d to revisit %s; the agent starts on it now.", is.IID, id))
}

// renderFollowUp renders the description of a follow-up issue.
func renderFollowUp(id, section, instruction string, n gitlab.Note, target *gitlab.Issue, prefix string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Follow-up of #%d to revisit decision **%s**, requested by @%s.\n\n", target.IID, id, n.Author.Username)
	b.WriteString("### Decision to revisit\n\n")
	// The old decision without its marker: this issue must not carry it.
	b.WriteString(strings.TrimSpace(marker.Decision.ReplaceAllString(section, "")) + "\n\n")
	if instruction == "" {
		instruction = "(no instruction given; see the comments on #" + fmt.Sprint(target.IID) + ")"
	}
	fmt.Fprintf(&b, "### Requested change\n\n%s\n\n", instruction)
	fmt.Fprintf(&b, "The change of #%d is merged. Change the code accordingly on top of it, and record the new decision with "+
		"`supersedes` set to `%s`. Comment `%s …` here to steer the work.\n\n", target.IID, id, prefix)
	fmt.Fprintf(&b, "<!-- ai-operator:key=revisit-%s -->\n<!-- ai-operator:revisit=%s -->\n", strings.ToLower(id), id)
	return b.String()
}
