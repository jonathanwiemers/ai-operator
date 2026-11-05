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
	"github.com/jonathanwiemers/ai-operator/internal/desklink"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

// The pilot acts only through proposals: its runs record them
// (status.pilot.proposed), the operator puts them to the owners as
// proposal prompts, and runs their actions once an owner chooses "Do it".
// The model never executes anything, and the operator checks every action
// again when it runs it. Proposals "P<n>" posted before prompts
// (status.pilot.proposals) are answered with "yes" until they expire.

// pilotTools are the tools the pilot may propose; merging, closing and
// pushing are none of them.
var pilotTools = []string{"retry", "reReview", "instruct", "openIssue", "fixFlaky", "retryPipeline", "rebase", "mergeBase", "skip", "pause", "resume", "revisit", "setRule", "forgetRule", "editSpec"}

// ownerTools are the operator's own options of budget prompts: an owner
// chooses them, the model never proposes them (checkAction).
var ownerTools = []string{"raiseBudget", "endMilestone"}

// queueTools go through the tracking issue's commands.
var queueTools = []string{"rebase", "mergeBase", "skip", "pause", "resume", "revisit", "endMilestone"}

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
	case !slices.Contains(pilotTools, a.Tool) && !slices.Contains(ownerTools, a.Tool):
		return fmt.Errorf("`%s` is not a tool of the pilot; merging, closing and pushing are not available", inertLine(a.Tool, 40))
	case a.Tool == "raiseBudget":
		return validRaise(a, st)
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
	case a.Tool == "retryPipeline" && a.PipelineID <= 0:
		return errors.New("retryPipeline needs the pipeline")
	case a.Tool == "retryPipeline":
		return nil // checked against main health (checkRetryPipeline)
	case slices.Contains(queueTools, a.Tool) && !running:
		return errors.New("this milestone has no autopilot queue")
	case a.Tool == "pause" && ap.Phase == aiv1.AutopilotPaused, a.Tool == "resume" && ap.Phase != aiv1.AutopilotPaused:
		return fmt.Errorf("autopilot is %s", strings.ToLower(string(ap.Phase)))
	case a.Tool == "pause" || a.Tool == "resume" || a.Tool == "endMilestone":
		return nil
	case a.Tool == "skip" && !slices.ContainsFunc(ap.Items, func(it aiv1.AutopilotItem) bool { return it.IID == a.IssueIID && it.State != aiv1.AutopilotItemMerged }):
		return fmt.Errorf("#%d is no planned issue that is not merged yet", a.IssueIID)
	case a.Tool == "skip":
		return nil
	case a.Tool == "rebase" || a.Tool == "mergeBase":
		return activeMR(ap, a.IssueIID)
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

// activeMR refuses rebase and mergeBase on anything but the active issue of
// the queue (ap) with a merge request; not on the main-red fix. The tracking
// issue's command checks the MR itself when it runs (mrCommand).
func activeMR(ap *aiv1.AutopilotStatus, iid int64) error {
	i := slices.IndexFunc(ap.Items, func(it aiv1.AutopilotItem) bool { return it.IID == iid })
	switch {
	case i < 0 || ap.Items[i].State != aiv1.AutopilotItemActive:
		return fmt.Errorf("#%d is not the active issue of the queue", iid)
	case ap.Items[i].MergeRequestIID == 0:
		return fmt.Errorf("#%d has no merge request yet", iid)
	}
	return nil
}

// issueTasks returns the implement tasks of a milestone's issues, and
// that of the main-red issue, which has no milestone: a main health prompt
// can instruct its agent.
func (r *AgentProjectReconciler) issueTasks(ctx context.Context, project *aiv1.AgentProject, milestone int64) (map[int64]*aiv1.AgentTask, error) {
	var list aiv1.AgentTaskList
	if err := r.List(ctx, &list, client.InNamespace(project.Namespace), client.MatchingLabels{LabelProject: project.Name}); err != nil {
		return nil, err
	}
	var mainRed int64
	if mh := project.Status.MainHealth; mh != nil && mh.State == aiv1.MainHealthRed {
		mainRed = mh.IssueIID
	}
	out := map[int64]*aiv1.AgentTask{}
	for i := range list.Items {
		t := &list.Items[i]
		if t.Spec.Type != aiv1.TaskTypeImplement || t.Spec.Issue == nil {
			continue
		}
		if t.Spec.Milestone != nil && t.Spec.Milestone.IID == milestone || t.Spec.Milestone == nil && mainRed != 0 && t.Spec.Issue.IID == mainRed {
			out[t.Spec.Issue.IID] = t
		}
	}
	return out, nil
}

// postProposals puts the proposals of the pilot's runs and extra (the
// operator's own, from "/pilot tell") to the owners as proposal prompts on
// the milestone's spec task st; their comments go on the tracking issue.
// What a managing pilot runs on its own (manage) is audited there instead.
// A proposal without an action the operator would run, or made on a state
// that is gone, is not put. It returns the id of the last prompt, also of
// one put before for the same source.
func (r *AgentProjectReconciler) postProposals(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st, coord *aiv1.AgentTask, tracking int64, extra ...aiv1.PilotProposal) (string, error) {
	ps := coord.Status.Pilot
	if ps == nil {
		ps = &aiv1.PilotStatus{}
	}
	if len(ps.Proposed)+len(extra) == 0 {
		return "", nil
	}
	tasks, err := r.issueTasks(ctx, project, st.Spec.Milestone.IID)
	if err != nil {
		return "", err
	}
	opened := slices.Clone(ps.OpenedIssues)
	proposed, own, counts := manage(spec, st, coord, tasks, ps.Proposed, &opened)
	var bodies []string
	last := ""
	for i, p := range append(slices.Clone(proposed), extra...) {
		fromRun := i < len(proposed)
		kind := aiv1.PromptProposal
		if len(p.Options) > 0 {
			kind = optionsKind(p.Kind)
		}
		// Put before the pilot's status was written (a conflict, a crash):
		// a proposal is put once.
		if j := slices.IndexFunc(st.Status.Prompts, func(q aiv1.Prompt) bool {
			return p.Source != "" && q.Source.Kind == kind && q.Source.Key == p.Source
		}); j >= 0 {
			last = st.Status.Prompts[j].ID
			continue
		}
		if len(p.Options) > 0 {
			q, err := r.addPrompt(ctx, gl, st, st, r.optionsPrompt(ctx, gl, project, spec, st, tasks, opened, p))
			if err != nil {
				return "", err
			}
			last = q.ID
			continue
		}
		stale := aiv1.PilotState(st, tasks, p.Actions) != p.State
		var notes []string
		var valid []aiv1.PilotAction
		p.Actions = forgetFirst(p.Actions)
		for j := range p.Actions {
			note, err := checkAction(ctx, gl, project, spec, st, tasks, opened, &p.Actions[j], fromRun, false)
			if err == nil {
				valid = append(valid, p.Actions[j])
				if p.Actions[j].Tool == "openIssue" {
					opened = append(opened, titleKey(p.Actions[j].Title))
				}
			}
			notes = append(notes, note)
		}
		if len(valid) == 0 {
			log.FromContext(ctx).Info("the pilot proposed nothing the operator can run", "task", coord.Name, "actions", notes)
			// The pilot manages, yet this needs an owner: its assessment did
			// not ping them.
			if fromRun && p.Quiet && !slices.ContainsFunc(own, func(q aiv1.PilotProposal) bool { return q.Source == p.Source }) {
				bodies = append(bodies, fmt.Sprintf("🧭 The pilot proposed nothing the operator runs: %s\n\n%s%s",
					inertLine(p.Summary, 300), strings.Join(notes, "\n"), notify.Mention(st.Spec.Owners)))
			}
			continue
		}
		if stale {
			// An owner's answer could not run it either.
			log.FromContext(ctx).Info("the pilot's proposal was made on a state that is gone", "task", coord.Name, "source", p.Source)
			continue
		}
		q, err := r.addPrompt(ctx, gl, st, st, proposalPrompt(st, tasks, p, valid, notes, fromRun))
		if err != nil {
			return "", err
		}
		last = q.ID
	}
	if err := r.patchProposals(ctx, coord, func(ps *aiv1.PilotStatus) {
		ps.Proposed = nil
		if counts != nil {
			ps.Accepted, ps.Interventions = append(ps.Accepted, own...), counts
		}
	}); err != nil {
		return "", err
	}
	for _, body := range bodies {
		if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, tracking, body); err != nil {
			log.FromContext(ctx).Info("could not post the pilot's proposal", "issue", tracking, "error", err.Error())
		}
	}
	return last, nil
}

// proposalPrompt is the prompt of proposal p: A runs those of its actions
// the operator runs (valid), B does nothing. Its context is why p was made
// and notes, the lines of all its actions. fromRun: a run of the pilot made
// p and mentioned the owners itself unless p is Quiet; the operator's own
// proposals mention nobody.
func proposalPrompt(st *aiv1.AgentTask, tasks map[int64]*aiv1.AgentTask, p aiv1.PilotProposal, valid []aiv1.PilotAction, notes []string, fromRun bool) aiv1.Prompt {
	summary := strings.Join(strings.Fields(p.Summary), " ")
	if summary == "" {
		summary = "The pilot proposes this."
	}
	evidence := strings.Join(notes, "\n")
	if why := strings.TrimSpace(p.Why); why != "" {
		evidence = "**Why:** " + why + "\n\n" + evidence
	}
	q := aiv1.Prompt{
		Source:   aiv1.PromptSource{Kind: aiv1.PromptProposal, IssueIID: p.Trigger, Key: p.Source},
		Question: textutil.Truncate(summary, aiv1.MaxPromptQuestion-len("…")),
		Context:  textutil.Truncate(evidence, aiv1.MaxPromptContext-len("…")),
		Options: []aiv1.PromptOption{
			{ID: "A", Label: "Do it", Detail: textutil.Truncate(summary, aiv1.MaxPromptOptionDetail-len("…")), Actions: valid, Recommended: true},
			{ID: "B", Label: "Do nothing"},
		},
		Quiet: !fromRun || !p.Quiet,
	}
	q.PilotState = proposalState(st, tasks, q)
	return q
}

// optionsKind is the kind of a prompt with the pilot's own options:
// mainHealth as recorded, anything else an assessment.
func optionsKind(k aiv1.PromptKind) aiv1.PromptKind {
	if k == aiv1.PromptMainHealth {
		return k
	}
	return aiv1.PromptAssessment
}

// optionsPrompt is the prompt of p's own options (an assessment): those
// whose actions the operator runs (validOptions). When none is left, the
// owners still get the question, with "Look into it myself" and "Do
// nothing" and why the pilot's options went. A run never mentioned the
// owners for it, so the comment does.
func (r *AgentProjectReconciler) optionsPrompt(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st *aiv1.AgentTask, tasks map[int64]*aiv1.AgentTask, opened []string, p aiv1.PilotProposal) aiv1.Prompt {
	question := strings.Join(strings.Fields(p.Question), " ")
	if question == "" {
		question = "What should happen next?"
	}
	q := aiv1.Prompt{
		Source:   aiv1.PromptSource{Kind: optionsKind(p.Kind), IssueIID: p.Trigger, Key: p.Source},
		Question: textutil.Truncate(question, aiv1.MaxPromptQuestion-len("…")),
		Context:  textutil.Truncate(p.Context, aiv1.MaxPromptContext-len("…")),
		Options:  slices.Clone(p.Options),
	}
	if _, err := r.validOptions(ctx, gl, project, spec, st, tasks, opened, &q, true); err != nil {
		log.FromContext(ctx).Info("the pilot's options leave nothing the operator runs", "task", st.Name, "source", p.Source, "error", err.Error())
		q.Options = []aiv1.PromptOption{{ID: "A", Label: "Look into it myself", Recommended: true}, {ID: "B", Label: "Do nothing"}}
		q.Context = textutil.Truncate(q.Context+"\n\n_The pilot's options were not offered: "+err.Error()+"._", aiv1.MaxPromptContext-len("…"))
	}
	return q
}

// checkRetryPipeline refuses to retry pipeline id unless it is the red
// pipeline of the default branch that main health (mh) knows: the newest
// one that finished, red after the watcher's own retry or without one.
func checkRetryPipeline(mh *aiv1.MainHealthStatus, id int64) error {
	if mh == nil || mh.State != aiv1.MainHealthRed || mh.PipelineID != id {
		return fmt.Errorf("pipeline %d is not the red pipeline of the default branch", id)
	}
	return nil
}

// checkAction checks an action before it is put to the owners: validAction,
// then the flaky issue and the spec edit in GitLab, the pipeline of a
// retryPipeline against main health. An action the model
// made (fromModel) loses an owner's words: only the operator passes them
// on. byOwner: only an owner's choice runs it (an option that is not
// recommended), so a flaky issue needs one failure, not two. It returns the
// action's line for a comment.
func checkAction(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st *aiv1.AgentTask, tasks map[int64]*aiv1.AgentTask, opened []string, a *aiv1.PilotAction, fromModel, byOwner bool) (string, error) {
	if fromModel {
		a.Owner, a.Text = "", ""
		if slices.Contains(ownerTools, a.Tool) {
			err := fmt.Errorf("`%s` is only offered by the operator, in its budget prompts", a.Tool)
			return describeAction(*a, err), err
		}
	}
	*a = withRuleText(*a, st)
	err := validAction(*a, st, tasks, opened)
	if err == nil && a.Tool == "fixFlaky" {
		_, err = checkFlaky(ctx, gl, spec, project.Status.BotUserID, project.Status.MainHealth, a.IssueIID, st.Spec.Milestone.IID, byOwner)
	}
	if err == nil && a.Tool == "retryPipeline" {
		err = checkRetryPipeline(project.Status.MainHealth, a.PipelineID)
	}
	diff := ""
	if err == nil && a.Tool == "editSpec" {
		_, diff, err = specEdit(ctx, gl, spec, st, *a)
	}
	note := describeAction(*a, err)
	if err == nil && diff != "" {
		note += ", as this diff shows:" + showDiff(diff)
	}
	return note, err
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
	return r.writeFailed(ctx, coord, "patchProposals", err)
}

// applyPilotChatCommands applies Desk accept/reject, answers to the
// milestone's prompts (answerPrompt) and revisits (revisitCommand) on the
// Coordinate task newer than LastChatCommandID. Authority is
// ownerSubjects; answers and the mark are saved on the Coordinate task.
func (r *AgentProjectReconciler) applyPilotChatCommands(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
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
	// No text: the message is answered later (consumeRefined).
	answer := func(id int64, text string) {
		if text != "" {
			answers = append(answers, aiv1.ChatReply{MessageID: id, Text: text, At: now})
		}
		mark = id
	}
	for _, m := range pending {
		cmd, err := chatcmd.Parse(m.Text)
		if err != nil {
			answer(m.ID, err.Error())
			logger.Info("chat command skipped", "id", m.ID, "author", m.Author, "error", err.Error())
			continue
		}
		if !cmd.ProposalOnly() && !cmd.PromptCmd() && !cmd.RevisitCmd() {
			answer(m.ID, "not supported on this task yet")
			continue
		}
		if !slices.Contains(coord.Spec.OwnerSubjects, m.Author) {
			if cmd.PromptCmd() {
				answer(m.ID, "Only the milestone's owners can answer prompts.")
			} else if cmd.RevisitCmd() {
				answer(m.ID, "Only the milestone's owners can revisit decisions.")
			} else {
				answer(m.ID, "Only the milestone's owners may answer proposals.")
			}
			logger.Info("chat command refused", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author, "reason", "not an owner subject")
			continue
		}
		who := m.Author
		if m.AuthorName != "" {
			who = m.AuthorName
		}
		if cmd.PromptCmd() {
			logger.Info("applying chat command", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author, "prompt", cmd.Prompt)
			reply, _, err := r.answerPrompt(ctx, project, spec, st, coord, who, m.ID, cmd, "desk")
			if err != nil {
				return err
			}
			answer(m.ID, reply)
			continue
		}
		if cmd.RevisitCmd() {
			logger.Info("applying chat command", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author, "decision", cmd.Decision)
			reply, err := r.revisitCommand(ctx, gl, project, spec, st, coord, who, cmd)
			if err != nil {
				return err
			}
			answer(m.ID, reply)
			continue
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
	case id == 0:
		return r.whichProposal(prefix, st, open), nil
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

// whichProposal answers a "yes" or "no" that names no proposal: the open
// proposals (open) to name, and the open prompts of the milestone's spec
// task st, which are answered in the Desk.
func (r *AgentProjectReconciler) whichProposal(prefix string, st *aiv1.AgentTask, open []aiv1.PilotProposal) string {
	var b strings.Builder
	if len(open) > 0 {
		fmt.Fprintf(&b, "Which proposal? Answer `%[1]s yes P<n>` or `%[1]s no P<n>`. Open:", prefix)
		for _, p := range open {
			fmt.Fprintf(&b, "\n- P%d: %s", p.ID, inertLine(p.Summary, 200))
		}
	}
	var prompts []string
	for _, p := range st.Status.Prompts {
		if !p.State.Closed() {
			prompts = append(prompts, fmt.Sprintf("\n- %s: %s — %s", p.ID, inertLine(p.Question, 200), deskLine(desklink.Prompt(r.DeskURL, st, p.ID), false)))
		}
	}
	switch {
	case b.Len() == 0 && len(prompts) == 0:
		return "There is no open proposal."
	case b.Len() == 0:
		b.WriteString("There is no open proposal to answer with yes or no.")
	}
	if len(prompts) > 0 {
		b.WriteString("\n\nOpen prompts, answered in the Desk:" + strings.Join(prompts, ""))
	}
	return b.String()
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
				_, errs[i][j] = checkFlaky(ctx, gl, spec, project.Status.BotUserID, project.Status.MainHealth, a.IssueIID, st.Spec.Milestone.IID, p.AcceptedBy != "")
			} else if errs[i][j] == nil && a.Tool == "retryPipeline" {
				errs[i][j] = checkRetryPipeline(project.Status.MainHealth, a.PipelineID)
			}
			if errs[i][j] == nil && p.AcceptedBy == "" && a.Tool == "editSpec" {
				errs[i][j] = errors.New("a spec edit needs an owner's yes")
			} else if errs[i][j] == nil && p.AcceptedBy == "" && slices.Contains(ownerTools, a.Tool) {
				errs[i][j] = fmt.Errorf("`%s` needs an owner's answer", a.Tool)
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
				if errs[i][j] = r.runAction(ctx, gl, project, spec, st, tracking, proposalHow(p), p.AcceptedBy, a, tasks[a.IssueIID]); errs[i][j] != nil && a.Tool == "openIssue" {
					failed = append(failed, titleKey(a.Title)) // may be opened again
				}
			}
			line := describeAction(a, errs[i][j])
			if a.Tool == "editSpec" && errs[i][j] == nil {
				line += ": spec updated; the milestone is planned again"
			}
			lines = append(lines, line)
		}
		log.FromContext(ctx).Info("pilot proposal run", "task", coord.Name, "proposal", p.ID, "prompt", p.Prompt, "by", p.AcceptedBy)
		if p.Prompt != "" {
			// The prompt's GitLab comment shows it with the answer.
			if err := r.promptOutcome(ctx, st, p.Prompt, lines); err != nil {
				log.FromContext(ctx).Info("could not record the outcome of a prompt's answer", "task", st.Name, "prompt", p.Prompt, "error", err.Error())
			}
			continue
		}
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

// runAction executes one validated action of an accepted proposal; how
// says how it came to run (proposalHow), by who accepted it.
func (r *AgentProjectReconciler) runAction(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, st *aiv1.AgentTask,
	tracking int64, how, by string, a aiv1.PilotAction, t *aiv1.AgentTask) error {
	switch a.Tool {
	case "fixFlaky":
		// Checked again: another pilot may have handed it over meanwhile.
		mh := project.Status.MainHealth
		is, err := checkFlaky(ctx, gl, spec, project.Status.BotUserID, mh, a.IssueIID, st.Spec.Milestone.IID, by != "")
		if err != nil {
			return err
		}
		return r.handOverFlaky(ctx, gl, spec, mh, st, is, fmt.Sprintf("the pilot of milestone %q, %s", inertLine(st.Spec.Milestone.Title, 100), how), by != "")
	case "retryPipeline":
		// Checked again: the branch may be green or have a newer pipeline.
		if err := checkRetryPipeline(project.Status.MainHealth, a.PipelineID); err != nil {
			return err
		}
		_, err := gl.RetryPipeline(ctx, spec.GitLab.Project, a.PipelineID)
		return err
	case "rebase", "mergeBase", "skip", "pause", "resume", "revisit", "endMilestone":
		return r.queueCommand(ctx, st, aiv1.PilotCommand{Action: a, Owner: by, OnIssue: tracking})
	case "raiseBudget":
		return r.raiseBudget(ctx, gl, spec, st, a.Amount)
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
				inert(a.Note), inertLine(st.Spec.Milestone.Title, 200), how, titleKey(a.Title)),
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
		body := fmt.Sprintf("🧭 Instruction from the pilot (%s) for the next run: %s", how, describeAction(a, nil))
		if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, a.IssueIID, body); err != nil {
			log.FromContext(ctx).Info("could not show the pilot's instruction", "issue", a.IssueIID, "error", err.Error())
		}
	}
	return nil
}

// maxPilotCommands bounds the queue commands that wait to be applied.
const maxPilotCommands = 20

// commandsWaiting is queueCommand's refusal when maxPilotCommands wait.
type commandsWaiting int

func (n commandsWaiting) Error() string {
	return fmt.Sprintf("%d commands of the pilot wait to be applied", int(n))
}

// queueCommand hands an accepted queue command to the paths of the
// tracking issue's commands: autopilot applies skip, pause and resume on
// its next poll, the revisits a revisit. Each remembers the last it
// applied, so a command applies once.
func (r *AgentProjectReconciler) queueCommand(ctx context.Context, st *aiv1.AgentTask, c aiv1.PilotCommand) error {
	return r.writeFailed(ctx, st, "queueCommand", retry.RetryOnConflict(retry.DefaultRetry, func() error {
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
			return commandsWaiting(len(cmds))
		}
		t.Status.Pilot.Commands = append(cmds, c)
		return r.Status().Patch(ctx, &t, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{}))
	}))
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
	case "rebase":
		s = fmt.Sprintf("**rebase** #%d: GitLab rebases its MR onto the default branch; the review holds if the change is unchanged", a.IssueIID)
	case "mergeBase":
		s = fmt.Sprintf("**mergeBase** #%d: the agent merges the default branch into its MR", a.IssueIID)
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
	case "retryPipeline":
		s = fmt.Sprintf("**retryPipeline** %d: its failed jobs run again", a.PipelineID)
	case "setRule", "forgetRule":
		s = describeRule(a)
	case "editSpec":
		s = "**editSpec**: a new spec replaces the milestone description, its command lines kept"
	case "raiseBudget":
		s = fmt.Sprintf("**raiseBudget** to $%s: the milestone description's budget line", showBudget(a.Amount))
	case "endMilestone":
		s = "**endMilestone**: autopilot picks no further issue and is done after the active one"
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

// proposeTell turns an owner's "/pilot tell #N: …" into a proposal prompt
// that passes their words on to the implementer of #N verbatim.
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
		Actions: []aiv1.PilotAction{a}, State: aiv1.PilotState(st, tasks, []aiv1.PilotAction{a}), Source: fmt.Sprintf("tell/%d", n.ID)}
	id, err := r.postProposals(ctx, gl, project, spec, st, coord, tracking, p)
	if err != nil {
		return "", err
	}
	return proposedReply(id, tracking), nil
}

// proposedReply is the reply to an owner whose command became prompt id
// on the tracking issue ("": none).
func proposedReply(id string, tracking int64) string {
	if id == "" {
		return "Not proposed: the operator would not run it now."
	}
	return fmt.Sprintf("%s on #%d: an owner answers it in the Desk.", id, tracking)
}

// pinnedOwners are the sorted user IDs of owners, pinned on what only they
// answer; [0] when there are none: no user has it, not even who takes a
// name later.
func pinnedOwners(ctx context.Context, gl gitlab.API, owners []string) ([]int64, error) {
	ids, err := ownerIDs(ctx, gl, owners)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []int64{0}, nil
	}
	return slices.Sorted(maps.Keys(ids)), nil
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
