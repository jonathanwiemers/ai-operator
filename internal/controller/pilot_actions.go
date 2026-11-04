package controller

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

// The pilot acts only through proposals: its runs record them
// (status.pilot.proposed), the operator posts them on the tracking issue
// as "Proposal P<n>", and runs their actions once an owner answers "yes".
// The model never executes anything, and the operator checks every action
// again when it runs it.

// pilotTools are the tools the pilot may propose; merging, closing and
// pushing are none of them.
var pilotTools = []string{"retry", "reReview", "instruct", "openIssue", "fixFlaky", "skip", "pause", "resume", "revisit", "setRule", "forgetRule", "editSpec"}

// queueTools go through the tracking issue's commands.
var queueTools = []string{"skip", "pause", "resume", "revisit"}

// proposalTTL is how long a proposal can be accepted.
const proposalTTL = 24 * time.Hour

// validAction checks an action against the milestone (spec task st): a
// known tool, an issue of the milestone with a task, a kind of
// instruction, an issue title the pilot has not opened yet, an autopilot
// queue the command applies to.
func validAction(a aiv1.PilotAction, st *aiv1.AgentTask, tasks map[int64]*aiv1.AgentTask, opened []string) error {
	ap := st.Status.Autopilot
	running := queueRunning(st)
	switch {
	case !slices.Contains(pilotTools, a.Tool):
		return fmt.Errorf("`%s` is not a tool of the pilot; merging, closing and pushing are not available", inertLine(a.Tool, 40))
	case isRuleTool(a.Tool):
		return validRule(a)
	case a.Tool == "revisit" && marker.DecisionIssue(a.Decision) == 0:
		return fmt.Errorf("`%s` is not a decision", inertLine(a.Decision, 40))
	case a.Tool == "revisit":
		return nil
	case a.Tool == "editSpec" && strings.TrimSpace(a.Spec) == "":
		return errors.New("a spec edit needs the new text")
	case a.Tool == "editSpec" && len(a.Spec) > maxSpec:
		return fmt.Errorf("the new spec is over %d KiB", maxSpec>>10)
	case a.Tool == "editSpec":
		return nil // the milestone itself is checked in GitLab (specEdit)
	case a.Tool == "fixFlaky" && a.IssueIID <= 0:
		return errors.New("fixFlaky needs the flaky issue")
	case a.Tool == "fixFlaky":
		return nil // the issue itself is checked in GitLab (checkFlaky)
	case slices.Contains(queueTools, a.Tool) && !running:
		return errors.New("this milestone has no autopilot queue")
	case a.Tool == "pause" && ap.Phase == aiv1.AutopilotPaused, a.Tool == "resume" && ap.Phase != aiv1.AutopilotPaused:
		return fmt.Errorf("autopilot is %s", strings.ToLower(string(ap.Phase)))
	case a.Tool == "pause" || a.Tool == "resume":
		return nil
	case a.Tool == "skip" && !slices.ContainsFunc(ap.Items, func(it aiv1.AutopilotItem) bool { return it.IID == a.IssueIID && it.State != aiv1.AutopilotItemMerged }):
		return fmt.Errorf("#%d is no planned issue that is not merged yet", a.IssueIID)
	case a.Tool == "skip":
		return nil
	}
	if a.Tool == "openIssue" {
		switch {
		case strings.TrimSpace(a.Title) == "":
			return errors.New("an issue needs a title")
		case slices.Contains(opened, titleKey(a.Title)):
			return errors.New("the pilot opened an issue with this title before")
		}
		return nil
	}
	t := tasks[a.IssueIID]
	switch {
	case t == nil:
		return fmt.Errorf("#%d is not an issue of this milestone with a task", a.IssueIID)
	case a.Tool == "reReview" && t.Status.Run.PushedSHA == "":
		return fmt.Errorf("#%d has no pushed head to review", a.IssueIID)
	case a.Tool == "instruct" && a.Text == "" && !slices.Contains(aiv1.PilotInstructKinds, a.Kind):
		return fmt.Errorf("`%s` is not a kind of instruction", inertLine(a.Kind, 40))
	}
	return nil
}

// issueTasks returns the implement tasks of a milestone's issues.
func (r *AgentProjectReconciler) issueTasks(ctx context.Context, project *aiv1.AgentProject, milestone int64) (map[int64]*aiv1.AgentTask, error) {
	var list aiv1.AgentTaskList
	if err := r.List(ctx, &list, client.InNamespace(project.Namespace), client.MatchingLabels{LabelProject: project.Name}); err != nil {
		return nil, err
	}
	out := map[int64]*aiv1.AgentTask{}
	for i := range list.Items {
		if t := &list.Items[i]; t.Spec.Type == aiv1.TaskTypeImplement && t.Spec.Issue != nil && t.Spec.Milestone != nil && t.Spec.Milestone.IID == milestone {
			out[t.Spec.Issue.IID] = t
		}
	}
	return out, nil
}

// postProposals posts the proposals of the pilot's runs and extra (the
// operator's own, from "/pilot tell") on the tracking issue, each with the
// next ID. A proposal without an action the operator would run is not
// posted. It returns the ID of the last one.
func (r *AgentProjectReconciler) postProposals(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st, coord *aiv1.AgentTask, tracking int64, extra ...aiv1.PilotProposal) (int64, error) {
	ps := coord.Status.Pilot
	if ps == nil {
		ps = &aiv1.PilotStatus{}
	}
	if len(ps.Proposed)+len(extra) == 0 {
		return 0, nil
	}
	tasks, err := r.issueTasks(ctx, project, st.Spec.Milestone.IID)
	if err != nil {
		return 0, err
	}
	now := metav1.NewTime(r.now())
	next := ps.LastProposal
	opened := slices.Clone(ps.OpenedIssues)
	proposed, own, counts := manage(spec, st, coord, tasks, ps.Proposed, &opened)
	var posted []aiv1.PilotProposal
	var bodies []string
	for i, p := range append(slices.Clone(proposed), extra...) {
		var notes []string
		valid := 0
		p.Actions = forgetFirst(p.Actions)
		for j := range p.Actions {
			if i < len(proposed) {
				// Only the operator passes an owner's words on.
				p.Actions[j].Owner, p.Actions[j].Text = "", ""
			}
			p.Actions[j] = withRuleText(p.Actions[j], st)
			err := validAction(p.Actions[j], st, tasks, opened)
			if err == nil && p.Actions[j].Tool == "fixFlaky" {
				_, err = checkFlaky(ctx, gl, spec, project.Status.BotUserID, project.Status.MainHealth, p.Actions[j].IssueIID, st.Spec.Milestone.IID)
			}
			diff := ""
			if err == nil && p.Actions[j].Tool == "editSpec" {
				_, diff, err = specEdit(ctx, gl, spec, st, p.Actions[j])
			}
			note := describeAction(p.Actions[j], err)
			if err == nil {
				valid++
				if p.Actions[j].Tool == "openIssue" {
					opened = append(opened, titleKey(p.Actions[j].Title))
				}
				if diff != "" {
					note += ", as this diff shows:" + showDiff(diff)
				}
			}
			notes = append(notes, note)
		}
		// The pilot manages, yet this needs an owner: its assessment did not
		// ping them.
		ping := ""
		if i < len(proposed) && p.Quiet {
			ping = notify.Mention(st.Spec.Owners)
		}
		if valid == 0 {
			log.FromContext(ctx).Info("the pilot proposed nothing the operator can run", "task", coord.Name, "actions", notes)
			if ping != "" && !slices.ContainsFunc(own, func(q aiv1.PilotProposal) bool { return q.Source == p.Source }) {
				bodies = append(bodies, fmt.Sprintf("🧭 The pilot proposed nothing the operator runs: %s\n\n%s%s", inertLine(p.Summary, 300), strings.Join(notes, "\n"), ping))
			}
			continue
		}
		next++
		p.ID, p.PostedAt = next, &now
		posted = append(posted, p)
		bodies = append(bodies, fmt.Sprintf("🧭 **Proposal P%[1]d:** %[2]s\n\n%[3]s\n\nAn owner accepts it with `yes` as a reply in this thread, or `%[4]s yes P%[1]d` "+
			"anywhere in the milestone; `no` or `%[4]s no P%[1]d` drops it. It no longer applies after 24 h or once the milestone's state changes.\n\n"+
			"<!-- ai-operator:proposal id=%[1]d state=%[5]s -->%[6]s", p.ID, inertLine(p.Summary, 300), strings.Join(notes, "\n"), spec.Workflow.PilotPrefix, p.State, ping))
	}
	// Only the owners of now answer them, whoever takes their names later.
	if len(posted) > 0 {
		owners, err := ownerIDs(ctx, gl, st.Spec.Owners)
		if err != nil {
			return 0, err
		}
		pinned := slices.Sorted(maps.Keys(owners))
		if len(pinned) == 0 {
			pinned = []int64{0} // no user has it: nobody answers, not even who takes a name later
		}
		for i := range posted {
			posted[i].OwnerIDs = pinned
		}
	}
	// Recorded before they are posted: each is posted at most once.
	if err := r.patchProposals(ctx, coord, func(ps *aiv1.PilotStatus) {
		ps.Proposed, ps.LastProposal = nil, next
		ps.Proposals = append(ps.Proposals, posted...)
		if counts != nil {
			ps.Accepted, ps.Interventions = append(ps.Accepted, own...), counts
		}
	}); err != nil {
		return 0, err
	}
	for _, body := range bodies {
		if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, tracking, body); err != nil {
			log.FromContext(ctx).Info("could not post the pilot's proposal", "issue", tracking, "error", err.Error())
		}
	}
	return next, nil
}

// patchProposals changes the pilot status of a Coordinate task, failing
// on a conflict: the runner adds proposals concurrently, and a proposal
// must neither get lost nor run twice. Proposals past their time go.
func (r *AgentProjectReconciler) patchProposals(ctx context.Context, coord *aiv1.AgentTask, mutate func(*aiv1.PilotStatus)) error {
	orig := coord.DeepCopy()
	if coord.Status.Pilot == nil {
		coord.Status.Pilot = &aiv1.PilotStatus{}
	}
	mutate(coord.Status.Pilot)
	now := r.now()
	coord.Status.Pilot.Proposals = slices.DeleteFunc(coord.Status.Pilot.Proposals, func(p aiv1.PilotProposal) bool {
		return p.PostedAt == nil || now.Sub(p.PostedAt.Time) >= proposalTTL
	})
	err := r.Status().Patch(ctx, coord, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{}))
	if err != nil {
		coord.Status = orig.Status // nothing of it holds
	}
	return err
}

// applyPilotChatCommands applies Desk accept/reject on the Coordinate task
// newer than LastChatCommandID. Authority is ownerSubjects; answers and the
// mark are saved on the Coordinate task.
func (r *AgentProjectReconciler) applyPilotChatCommands(ctx context.Context, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st, coord *aiv1.AgentTask) error {
	src := &coord.Spec.Source
	var pending []aiv1.ChatMessage
	for _, m := range src.ChatMessages {
		if m.Kind == "command" && m.ID > src.LastChatCommandID {
			pending = append(pending, m)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	slices.SortFunc(pending, func(a, b aiv1.ChatMessage) int { return cmp.Compare(a.ID, b.ID) })
	logger := log.FromContext(ctx)
	now := metav1.Now()
	// Kept apart until the end: answerProposal's status patch replaces
	// coord with the server's copy, spec included.
	var answers []aiv1.ChatReply
	mark := src.LastChatCommandID
	answer := func(id int64, text string) {
		answers = append(answers, aiv1.ChatReply{MessageID: id, Text: text, At: now})
		mark = id
	}
	for _, m := range pending {
		cmd, err := chatcmd.Parse(m.Text)
		if err != nil {
			answer(m.ID, err.Error())
			logger.Info("chat command skipped", "id", m.ID, "author", m.Author, "error", err.Error())
			continue
		}
		if !cmd.ProposalOnly() {
			answer(m.ID, "not supported on this task yet")
			continue
		}
		if !slices.Contains(coord.Spec.OwnerSubjects, m.Author) {
			answer(m.ID, "Only the milestone's owners may answer proposals.")
			logger.Info("chat command refused", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author, "reason", "not an owner subject")
			continue
		}
		who := m.Author
		if m.AuthorName != "" {
			who = m.AuthorName
		}
		yes := cmd.Cmd == "accept"
		logger.Info("applying chat command", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author, "proposal", cmd.Proposal)
		reply, err := r.answerProposal(ctx, project, spec, st, coord, who, 0, trackingIssue(st, coord), cmd.Proposal, yes, true)
		if err != nil {
			return err
		}
		if reply == "" {
			reply = fmt.Sprintf("Proposal P%d accepted; the outcome follows.", cmd.Proposal)
		}
		answer(m.ID, reply)
	}
	src = &coord.Spec.Source
	src.ChatAnswers = append(src.ChatAnswers, answers...)
	src.LastChatCommandID = mark
	if n := len(src.ChatAnswers); n > maxChatAnswers {
		src.ChatAnswers = append([]aiv1.ChatReply(nil), src.ChatAnswers[n-maxChatAnswers:]...)
	}
	return r.Update(ctx, coord)
}

// answerProposal handles an owner's "yes" or "no", written by user byID
// named by on issue where (0: a merge request), to proposal id (0: none
// named) and returns the reply, "" for none. An answer from someone who was
// no owner when the proposal was posted does nothing; one that expired is
// refused; an accepted one moves to status.pilot.accepted, which
// runAccepted runs at the end of the poll. authorized skips the OwnerIDs
// check: the Desk path already matched Author against ownerSubjects.
func (r *AgentProjectReconciler) answerProposal(ctx context.Context, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st, coord *aiv1.AgentTask, by string, byID, where, id int64, yes, authorized bool) (string, error) {
	var open []aiv1.PilotProposal
	if ps := coord.Status.Pilot; ps != nil {
		open = ps.Proposals
	}
	prefix := spec.Workflow.PilotPrefix
	i := slices.IndexFunc(open, func(p aiv1.PilotProposal) bool { return p.ID == id })
	switch {
	case id == 0 && len(open) == 0:
		return "There is no open proposal.", nil
	case id == 0:
		var b strings.Builder
		fmt.Fprintf(&b, "Which proposal? Answer `%[1]s yes P<n>` or `%[1]s no P<n>`. Open:", prefix)
		for _, p := range open {
			fmt.Fprintf(&b, "\n- P%d: %s", p.ID, inertLine(p.Summary, 200))
		}
		return b.String(), nil
	case i < 0:
		return fmt.Sprintf("Proposal P%d no longer applies.", id), nil
	}
	p := open[i]
	// Without pinned IDs (posted before they were), the caller's check by
	// the owners' names is all there is. Desk callers pass authorized.
	if !authorized && len(p.OwnerIDs) > 0 && !slices.Contains(p.OwnerIDs, byID) {
		return "", nil
	}
	tasks, err := r.issueTasks(ctx, project, st.Spec.Milestone.IID)
	if err != nil {
		return "", err
	}
	expired := p.PostedAt == nil || r.now().Sub(p.PostedAt.Time) >= proposalTTL || aiv1.PilotState(st, tasks, p.Actions) != p.State
	accept := yes && !expired
	p.AcceptedBy = by
	if err := r.patchProposals(ctx, coord, func(ps *aiv1.PilotStatus) {
		ps.Proposals = slices.DeleteFunc(ps.Proposals, func(q aiv1.PilotProposal) bool { return q.ID == id })
		if accept {
			ps.Accepted = append(ps.Accepted, p)
		}
	}); err != nil {
		return "", err
	}
	tracking := trackingIssue(st, coord)
	switch {
	case !yes:
		return fmt.Sprintf("Proposal P%d dropped by `@%s`.", id, by), nil
	case expired:
		return fmt.Sprintf("Proposal P%d no longer applies: the milestone's state changed since it was made, or it is older than 24 h. "+
			"Ask me again (`%s …`) for a fresh one.", id, prefix), nil
	case where == tracking:
		return "", nil // the outcome follows
	}
	return fmt.Sprintf("Proposal P%d accepted; the outcome follows on #%d.", id, tracking), nil
}

// runAccepted runs the accepted proposals of a pilot at the end of the
// poll, when nothing else of it changes the tasks any more. They leave the
// status before any action runs, so each runs once at most; every action
// is checked again first. The outcome goes on the tracking issue.
func (r *AgentProjectReconciler) runAccepted(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st, coord *aiv1.AgentTask, tracking int64) error {
	ps := coord.Status.Pilot
	if ps == nil || len(ps.Accepted) == 0 {
		return nil
	}
	tasks, err := r.issueTasks(ctx, project, st.Spec.Milestone.IID)
	if err != nil {
		return err
	}
	accepted, opened := ps.Accepted, slices.Clone(ps.OpenedIssues)
	errs := make([][]error, len(accepted))
	for i, p := range accepted {
		errs[i] = make([]error, len(p.Actions))
		for j, a := range p.Actions {
			if errs[i][j] = validAction(a, st, tasks, opened); errs[i][j] == nil && a.Tool == "fixFlaky" {
				_, errs[i][j] = checkFlaky(ctx, gl, spec, project.Status.BotUserID, project.Status.MainHealth, a.IssueIID, st.Spec.Milestone.IID)
			}
			if errs[i][j] == nil && p.AcceptedBy == "" && a.Tool == "editSpec" {
				errs[i][j] = errors.New("a spec edit needs an owner's yes")
			} else if errs[i][j] == nil && p.AcceptedBy == "" && spec.Pilot.AutonomyOf(coord) != aiv1.PilotManaged {
				errs[i][j] = errors.New("the pilot proposes now (`/pilot stop` or `pilot.autonomy`)")
			} else if errs[i][j] == nil && a.Tool == "openIssue" {
				opened = append(opened, titleKey(a.Title))
			}
		}
	}
	if err := r.patchProposals(ctx, coord, func(ps *aiv1.PilotStatus) {
		ps.Accepted, ps.OpenedIssues = nil, opened
		refund(ps, tasks, accepted, errs)
	}); err != nil {
		return err
	}
	var failed []string
	for i, p := range accepted {
		var b strings.Builder
		fmt.Fprintf(&b, "🧭 Proposal P%d accepted by `@%s`:", p.ID, p.AcceptedBy)
		var lines []string
		for j, a := range p.Actions {
			if errs[i][j] == nil {
				if errs[i][j] = r.runAction(ctx, gl, project, spec, st, tracking, p.ID, p.AcceptedBy, a, tasks[a.IssueIID]); errs[i][j] != nil && a.Tool == "openIssue" {
					failed = append(failed, titleKey(a.Title)) // may be opened again
				}
			}
			line := describeAction(a, errs[i][j])
			if a.Tool == "editSpec" && errs[i][j] == nil {
				line += ": spec updated; the milestone is planned again"
			}
			lines = append(lines, line)
		}
		log.FromContext(ctx).Info("pilot proposal run", "task", coord.Name, "proposal", p.ID, "by", p.AcceptedBy)
		body := b.String() + "\n" + strings.Join(lines, "\n")
		if p.AcceptedBy == "" {
			body = auditNote(spec, coord, tasks, p, lines)
		}
		if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, tracking, body); err != nil {
			log.FromContext(ctx).Info("could not post the outcome of a proposal", "issue", tracking, "error", err.Error())
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return r.patchProposals(ctx, coord, func(ps *aiv1.PilotStatus) {
		ps.OpenedIssues = slices.DeleteFunc(ps.OpenedIssues, func(k string) bool { return slices.Contains(failed, k) })
	})
}

// runAction executes one validated action of an accepted proposal.
func (r *AgentProjectReconciler) runAction(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, st *aiv1.AgentTask,
	tracking, id int64, by string, a aiv1.PilotAction, t *aiv1.AgentTask) error {
	switch a.Tool {
	case "fixFlaky":
		// Checked again: another pilot may have handed it over meanwhile.
		mh := project.Status.MainHealth
		is, err := checkFlaky(ctx, gl, spec, project.Status.BotUserID, mh, a.IssueIID, st.Spec.Milestone.IID)
		if err != nil {
			return err
		}
		return r.handOverFlaky(ctx, gl, spec, mh, st, is, fmt.Sprintf("the pilot of milestone %q, %s", inertLine(st.Spec.Milestone.Title, 100), pilotHow(id, by)))
	case "skip", "pause", "resume", "revisit":
		return r.queueCommand(ctx, st, aiv1.PilotCommand{Action: a, Owner: by, OnIssue: tracking})
	case "setRule", "forgetRule":
		return r.runRule(ctx, gl, spec, st, a)
	case "editSpec":
		// Read again: the description the owner saw, with its command
		// lines of now; the next poll plans the milestone again.
		desc, _, err := specEdit(ctx, gl, spec, st, a)
		if err != nil {
			return err
		}
		_, err = gl.UpdateMilestoneDescription(ctx, spec.GitLab.Project, st.Spec.Milestone.ID, desc)
		return err
	case "openIssue":
		_, err := gl.CreateIssue(ctx, spec.GitLab.Project, gitlab.CreateIssueOptions{
			Title: inertLine(a.Title, 200),
			Description: fmt.Sprintf("%s\n\n_Opened by the pilot of %s (%s)._\n\n<!-- ai-operator:pilot-issue=%s -->",
				inert(a.Note), inertLine(st.Spec.Milestone.Title, 200), pilotHow(id, by), titleKey(a.Title)),
			Labels: spec.Workflow.GeneratedLabel,
		})
		return err
	}
	// On a fresh copy, tried again on a conflict: the task changes as it runs.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Get(ctx, client.ObjectKeyFromObject(t), t); err != nil {
			return err
		}
		switch a.Tool {
		case "reReview":
			if err := r.askReview(ctx, t, t.Status.Run.PushedSHA); err != nil {
				return err
			}
		case "instruct":
			t.Spec.Source.PilotInstruction = &aiv1.PilotInstruction{Kind: a.Kind, Note: a.Note, Owner: a.Owner, Text: a.Text, Request: t.Spec.RunRequest + 1}
		}
		if a.Text == "" {
			t.Spec.Source.PilotRun = t.Spec.RunRequest + 1 // keeps the CI fix rounds; an owner's words do not
		}
		return r.requestRun(ctx, gl, spec, t, a.IssueIID)
	}); err != nil {
		return err
	}
	if a.Tool == "instruct" {
		// Shown where the implementer works; its prompt has it from the
		// task, not from this comment.
		body := fmt.Sprintf("🧭 Instruction from the pilot (%s) for the next run: %s", pilotHow(id, by), describeAction(a, nil))
		if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, a.IssueIID, body); err != nil {
			log.FromContext(ctx).Info("could not show the pilot's instruction", "issue", a.IssueIID, "error", err.Error())
		}
	}
	return nil
}

// maxPilotCommands bounds the queue commands that wait to be applied.
const maxPilotCommands = 20

// queueCommand hands an accepted queue command to the paths of the
// tracking issue's commands: autopilot applies skip, pause and resume on
// its next poll, the revisits a revisit. Each remembers the last it
// applied, so a command applies once.
func (r *AgentProjectReconciler) queueCommand(ctx context.Context, st *aiv1.AgentTask, c aiv1.PilotCommand) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var t aiv1.AgentTask
		if err := r.Get(ctx, client.ObjectKeyFromObject(st), &t); err != nil {
			return err
		}
		orig := t.DeepCopy()
		if t.Status.Pilot == nil {
			t.Status.Pilot = &aiv1.PilotStatus{}
		}
		cmds := slices.Clone(t.Status.Pilot.Commands)
		c.Seq = 1
		if n := len(cmds); n > 0 {
			c.Seq = cmds[n-1].Seq + 1
		}
		// Those every path applied go; the last keeps the sequence.
		var done int64
		if rs := t.Status.Revisits; rs != nil {
			done = rs.LastPilotCommand
		}
		if ap := t.Status.Autopilot; ap != nil {
			done = min(done, ap.LastPilotCommand)
		}
		cmds = slices.DeleteFunc(cmds, func(x aiv1.PilotCommand) bool { return x.Seq <= done && x.Seq != c.Seq-1 })
		if len(cmds) >= maxPilotCommands {
			return fmt.Errorf("%d commands of the pilot wait to be applied", len(cmds))
		}
		t.Status.Pilot.Commands = append(cmds, c)
		return r.Status().Patch(ctx, &t, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{}))
	})
}

// askReview asks the next run of task to review head again without the
// implementer, also a head it reviewed again before.
func (r *AgentProjectReconciler) askReview(ctx context.Context, task *aiv1.AgentTask, head string) error {
	task.Spec.Source.ReviewFor = head
	if head != task.Status.Run.ReviewedFor {
		return nil
	}
	// Cleared before the run request, so no run sees one without it.
	st := task.DeepCopy()
	st.Status.Run.ReviewedFor = ""
	if err := r.Status().Patch(ctx, st, client.MergeFrom(task)); err != nil {
		return err
	}
	task.Status.Run.ReviewedFor, task.ResourceVersion = "", st.ResourceVersion
	return nil
}

// describeAction renders an action for a comment; err says why it is not
// (or would not be) run.
func describeAction(a aiv1.PilotAction, err error) string {
	var s string
	switch a.Tool {
	case "retry":
		s = fmt.Sprintf("**retry** #%d: a new run", a.IssueIID)
	case "reReview":
		s = fmt.Sprintf("**reReview** #%d: its reviewers run again on the pushed head", a.IssueIID)
	case "instruct":
		if a.Text != "" {
			s = fmt.Sprintf("**instruct** #%d, instruction from `@%s` via pilot: %s", a.IssueIID, a.Owner, inertLine(a.Text, 1000))
		} else {
			s = fmt.Sprintf("**instruct** #%d: `%s`", a.IssueIID, inertLine(a.Kind, 40))
			if a.Note != "" {
				s += ", with the pilot's hint: " + inertLine(a.Note, 2000)
			}
		}
	case "skip":
		s = fmt.Sprintf("**skip** #%d: autopilot leaves it out", a.IssueIID)
	case "pause", "resume":
		s = fmt.Sprintf("**%s** autopilot", a.Tool)
	case "revisit":
		s = fmt.Sprintf("**revisit** %s, asking for: %s", inertLine(a.Decision, 40), inertLine(a.Note, 2000))
	case "openIssue":
		s = fmt.Sprintf("**openIssue** %q, not handed to the agent, with the body: %s", inertLine(a.Title, 200), inertLine(a.Note, 2000))
	case "fixFlaky":
		s = fmt.Sprintf("**fixFlaky** #%d: the flaky issue goes to the agent in this milestone, with the operator's task", a.IssueIID)
	case "setRule", "forgetRule":
		s = describeRule(a)
	case "editSpec":
		s = "**editSpec**: a new spec replaces the milestone description, its command lines kept"
	default:
		s = fmt.Sprintf("**%s**", inertLine(a.Tool, 40))
	}
	if err != nil {
		s += " (not run: " + err.Error() + ")"
	}
	return "- " + s
}

// titleKey identifies an issue the pilot opens by its title.
func titleKey(title string) string {
	return hashString(strings.ToLower(strings.Join(strings.Fields(title), " ")))
}

// inert makes agent text safe for a comment: no mentions, quick actions
// or markers.
func inert(s string) string {
	return notify.Defuse(strings.ReplaceAll(strings.TrimSpace(s), "<!--", "&lt;!--"))
}

// inertLine is inert text on one line, cut to n bytes.
func inertLine(s string, n int) string {
	return inert(textutil.Truncate(strings.Join(strings.Fields(s), " "), n))
}

// threadAnswers handles the owners' "yes" and "no" written as replies in
// the thread of a proposal on the tracking issue, newer than since. The
// threads are read only when there is such an answer.
func (r *AgentProjectReconciler) threadAnswers(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st, coord *aiv1.AgentTask, tracking, since int64, notes []gitlab.Note) error {
	if coord == nil {
		return nil
	}
	bot := project.Status.BotUserID
	var answers []gitlab.Note
	for _, n := range notes {
		if _, id, ok := command.PilotAnswer(n.Body); ok && id == 0 && n.ID > since && !n.System && n.Author.ID != bot {
			answers = append(answers, n)
		}
	}
	if len(answers) == 0 {
		return nil
	}
	owners, err := ownerIDs(ctx, gl, st.Spec.Owners)
	if err != nil {
		return err
	}
	answers = slices.DeleteFunc(answers, func(n gitlab.Note) bool { return !owners[n.Author.ID] })
	if len(answers) == 0 {
		return nil
	}
	threads, err := gl.ListIssueDiscussions(ctx, spec.GitLab.Project, tracking)
	if err != nil {
		return fmt.Errorf("listing the threads of the tracking issue #%d: %w", tracking, err)
	}
	// Which proposal each reply answers: the thread must start with the
	// bot's proposal.
	proposal := map[int64]int64{}
	for _, d := range threads {
		if len(d.Notes) == 0 || d.Notes[0].Author.ID != bot {
			continue
		}
		if m := marker.Proposal.FindStringSubmatch(d.Notes[0].Body); m != nil {
			id, _ := strconv.ParseInt(m[1], 10, 64)
			for _, n := range d.Notes[1:] {
				proposal[n.ID] = id
			}
		}
	}
	for _, n := range answers {
		id, ok := proposal[n.ID]
		if !ok {
			continue
		}
		yes, _, _ := command.PilotAnswer(n.Body)
		reply, err := r.answerProposal(ctx, project, spec, st, coord, n.Author.Username, n.Author.ID, tracking, id, yes, false)
		if err != nil {
			return err
		}
		if reply != "" {
			pilotReply(ctx, gl, spec, pilotNote{Note: n, issue: tracking}, reply)
		}
	}
	return nil
}

// proposeTell turns an owner's "/pilot tell #N: …" into a proposal that
// passes their words on to the implementer of #N verbatim.
func (r *AgentProjectReconciler) proposeTell(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st, coord *aiv1.AgentTask, n pilotNote, iid int64, text string) (string, error) {
	tasks, err := r.issueTasks(ctx, project, st.Spec.Milestone.IID)
	if err != nil {
		return "", err
	}
	a := aiv1.PilotAction{Tool: "instruct", IssueIID: iid, Owner: n.Author.Username, Text: textutil.Truncate(text, maxPilotMessage)}
	if err := validAction(a, st, tasks, nil); err != nil {
		return "Not proposed: " + err.Error() + ".", nil
	}
	tracking := trackingIssue(st, coord)
	p := aiv1.PilotProposal{Summary: fmt.Sprintf("pass @%s's words on to the implementer of #%d", n.Author.Username, iid),
		Actions: []aiv1.PilotAction{a}, State: aiv1.PilotState(st, tasks, []aiv1.PilotAction{a})}
	id, err := r.postProposals(ctx, gl, project, spec, st, coord, tracking, p)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Proposal P%d on #%d: an owner's `yes` passes it on.", id, tracking), nil
}

// ownerIDs resolves the owners' usernames to user IDs: a username can
// change hands.
func ownerIDs(ctx context.Context, gl gitlab.API, owners []string) (map[int64]bool, error) {
	ids := map[int64]bool{}
	for _, o := range owners {
		u, err := gl.FindUser(ctx, o)
		if err != nil {
			return nil, fmt.Errorf("looking up owner %q: %w", o, err)
		}
		if u != nil {
			ids[u.ID] = true
		}
	}
	return ids, nil
}
