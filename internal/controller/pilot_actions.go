package controller

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
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
	"github.com/jonathanwiemers/ai-operator/internal/desklink"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
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
var pilotTools = []string{"retry", "reReview", "instruct", "openIssue", "fixFlaky", "retryPipeline", "rebase", "mergeBase", "restartRun", "skip", "unskip", "startAnyway", "markDone", "pause", "resume", "revisit", "setRule", "forgetRule", "editSpec", "setTier"}

// ownerTools are the operator's own options of budget prompts: an owner
// chooses them, the model never proposes them (checkAction).
var ownerTools = []string{"raiseBudget", "endMilestone"}

// answerTools are ways out of a stuck pause the pilot only ever proposes:
// they run on an owner's answer, never on its own (runAccepted), and are
// not counted (countedTools).
var answerTools = []string{"unskip", "startAnyway", "markDone"}

// chatTools run when asked for in a problem chat; the rest (spec, rules, decisions, ways out) need an Accept.
var chatTools = append(slices.Clone(countedTools), "skip", "pause", "resume")

// queueTools go through the tracking issue's commands.
var queueTools = []string{"rebase", "mergeBase", "skip", "unskip", "startAnyway", "pause", "resume", "revisit", "endMilestone"}

// proposalTTL is how long a proposal can be accepted.
const proposalTTL = 24 * time.Hour

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
// operator's own, from "/pilot forget") to the owners as proposal prompts on
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
	tasks, err := r.pilotTasks(ctx, project, st)
	if err != nil {
		return "", err
	}
	opened := slices.Clone(ps.OpenedIssues)
	fromChat := slices.DeleteFunc(slices.Clone(ps.Proposed), func(p aiv1.PilotProposal) bool { return p.ChatMessageID == 0 })
	proposed, own, counts := manage(spec, st, coord, tasks, r.gitLabLimitOf(project), slices.DeleteFunc(slices.Clone(ps.Proposed), func(p aiv1.PilotProposal) bool { return p.ChatMessageID != 0 }), &opened)
	// Run as the owner who asked in a problem chat, else put; never by autonomy.
	var chat []aiv1.PilotProposal
	mark := ps.LastChatAccepted
	for _, p := range fromChat {
		if p.ChatMessageID <= mark {
			log.FromContext(ctx).Info("the chat's proposal is dropped: its message or a newer one ran", "task", coord.Name, "message", p.ChatMessageID)
		} else if by, why := chatAccepted(st, coord, tasks, p); by != "" {
			p.AcceptedBy, mark = by, p.ChatMessageID
			chat = append(chat, p)
		} else {
			log.FromContext(ctx).Info("the chat's proposal waits for an owner's answer", "task", coord.Name, "message", p.ChatMessageID, "why", why)
			proposed = append(proposed, p)
		}
	}
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
			note, err := r.checkAction(ctx, gl, project, spec, st, tasks, opened, &p.Actions[j], fromRun, false)
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
		ps.Proposed, ps.Accepted, ps.LastChatAccepted = nil, append(ps.Accepted, chat...), max(ps.LastChatAccepted, mark)
		if counts != nil {
			ps.Accepted, ps.Interventions = append(ps.Accepted, own...), counts
		}
	}); err != nil {
		return "", err
	}
	for _, body := range bodies {
		if err := r.postPilotNote(ctx, gl, project, spec, coord, tracking, body); err != nil {
			log.FromContext(ctx).Info("could not post the pilot's proposal", "issue", tracking, "error", err.Error())
		}
	}
	return last, nil
}

// chatAccepted is the owner whose "problem: #<Trigger> …" asked for chat proposal p,
// all on that issue or the milestone, in a state that holds, or whose own
// words p passes on (ownWords); else why not.
func chatAccepted(st, coord *aiv1.AgentTask, tasks map[int64]*aiv1.AgentTask, p aiv1.PilotProposal) (by, why string) {
	i := slices.IndexFunc(coord.Spec.Source.ChatMessages, func(m aiv1.ChatMessage) bool { return m.ID == p.ChatMessageID })
	if i < 0 {
		return "", "the message is gone"
	}
	m := coord.Spec.Source.ChatMessages[i]
	rest, ok := strings.CutPrefix(strings.TrimSpace(m.Text), fmt.Sprintf("problem: #%d", p.Trigger))
	switch {
	case !slices.Contains(coord.Spec.OwnerSubjects, m.Author):
		return "", "its author is no owner"
	case ownWords(m, p) && aiv1.PilotState(st, tasks, p.Actions) != p.State:
		return "", "the milestone's state changed"
	case ownWords(m, p):
		return cmp.Or(m.AuthorName, m.Author), ""
	case p.Trigger <= 0 || !ok || rest != "" && strings.ContainsRune("0123456789", rune(rest[0])):
		return "", "the message is not about the proposal's issue"
	case len(p.Actions) == 0 || len(p.Options) > 0 || slices.ContainsFunc(p.Actions, func(a aiv1.PilotAction) bool {
		// Words in an owner's name run only as ownWords.
		return a.IssueIID != 0 && a.IssueIID != p.Trigger || !slices.Contains(chatTools, a.Tool) || a.Owner != "" || a.Text != ""
	}):
		return "", "an action is on another issue or needs an owner's Accept"
	case aiv1.PilotState(st, tasks, p.Actions) != p.State:
		return "", "the milestone's state changed"
	}
	return cmp.Or(m.AuthorName, m.Author), ""
}

// ownWords: chat proposal p passes on the words of message m's author
// verbatim, and nothing else: one instruct on its Trigger, by that author,
// whose text is a non-empty part of the message as written. The pilot
// cannot put its own words in an owner's name.
func ownWords(m aiv1.ChatMessage, p aiv1.PilotProposal) bool {
	if len(p.Actions) != 1 || len(p.Options) > 0 {
		return false
	}
	a := p.Actions[0]
	text := strings.TrimSpace(a.Text)
	return a.Tool == "instruct" && a.Kind == "" && a.Note == "" && a.Owner == m.Author && a.IssueIID > 0 && a.IssueIID == p.Trigger &&
		text != "" && strings.Contains(strings.TrimSpace(m.Text), text)
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
	// A pause's assessment belongs to the problem of the issue it holds up.
	if q.Source.IssueIID == 0 && q.Source.Kind == aiv1.PromptAssessment && pauseSource(p.Source) {
		q.Source.IssueIID = aiv1.ProblemIssue(st.Status.Autopilot)
	}
	if _, err := r.validOptions(ctx, gl, project, spec, st, tasks, opened, &q, true); err != nil {
		log.FromContext(ctx).Info("the pilot's options leave nothing the operator runs", "task", st.Name, "source", p.Source, "error", err.Error())
		q.Options = []aiv1.PromptOption{{ID: "A", Label: "Look into it myself", Recommended: true}, {ID: "B", Label: "Do nothing"}}
		q.Context = textutil.Truncate(q.Context+"\n\n_The pilot's options were not offered: "+err.Error()+"._", aiv1.MaxPromptContext-len("…"))
	}
	return q
}

// checkAction checks an action before it is put to the owners: validAction,
// then the flaky issue and the spec edit in GitLab, the pipeline of a
// retryPipeline against main health. An action the model
// made (fromModel) loses an owner's words: only the operator passes them
// on. byOwner: only an owner's choice runs it (an option that is not
// recommended), so a flaky issue needs one failure, not two. It returns the
// action's line for a comment.
func (r *AgentProjectReconciler) checkAction(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
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
	if err == nil {
		err = validRetry(*a, tasks, r.gitLabLimitOf(project))
	}
	if err == nil && a.Tool == "setTier" {
		err = validProfile(spec, a.Profile)
	}
	if err == nil && a.Tool == "fixFlaky" {
		_, err = checkFlaky(ctx, gl, spec, project.Status.BotUserID, project.Status.MainHealth, a.IssueIID, flakyMilestone(spec, st), byOwner)
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
// milestone's prompts (answerPrompt), revisits (revisitCommand) and drafts
// of a follow-up milestone (draftRequest) on the Coordinate task newer than
// LastChatCommandID. Once the autopilot is done only drafts are taken.
// Authority is ownerSubjects; answers, a draft request and the mark are
// saved on the Coordinate task.
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
	// A draft asked for, set with the answers.
	var draft *aiv1.FollowUpDraftRequest
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
		if !cmd.ProposalOnly() && !cmd.PromptCmd() && !cmd.RevisitCmd() && !cmd.DraftCmd() && !cmd.RestartCmd() {
			answer(m.ID, "not supported on this task yet")
			continue
		}
		if autopilotDone(st) && !cmd.DraftCmd() && !cmd.RestartCmd() {
			// The pilot stays for the leftovers only.
			answer(m.ID, "The autopilot of this milestone is done: its pilot only writes drafts of a follow-up milestone now.")
			continue
		}
		if !slices.Contains(coord.Spec.OwnerSubjects, m.Author) {
			if cmd.DraftCmd() {
				answer(m.ID, "Only the milestone's owners can ask for a draft of a follow-up milestone.")
			} else if cmd.PromptCmd() {
				answer(m.ID, "Only the milestone's owners can answer prompts.")
			} else if cmd.RevisitCmd() {
				answer(m.ID, "Only the milestone's owners can revisit decisions.")
			} else if cmd.RestartCmd() {
				answer(m.ID, notOwnerRestart)
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
		if cmd.RestartCmd() {
			logger.Info("applying chat command", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author)
			// Keyed by the command: applied again after a lost update, it
			// finds its note and restarts nothing twice.
			key := fmt.Sprintf("chat/%s/%d", coord.Name, m.ID)
			// Once the autopilot is done (retrospective, drafts) the note
			// still goes to the tracking issue the pilot was on.
			iid := trackingIssue(st, coord)
			if iid == 0 && coord.Spec.Issue != nil {
				iid = coord.Spec.Issue.IID
			}
			if iid == 0 {
				answer(m.ID, "The run cannot be restarted: this milestone has no tracking issue for the note.")
				continue
			}
			reply, err := r.restartRun(ctx, gl, spec, coord, iid, who+" via the Desk", key, project.Status.BotUserID)
			if err != nil {
				return err
			}
			answer(m.ID, reply)
			continue
		}
		if cmd.DraftCmd() {
			logger.Info("applying chat command", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author, "followUps", cmd.FollowUps)
			reply, req := draftRequest(st, coord, draft, who, cmd.FollowUps, r.now())
			if req != nil {
				draft = req
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
	if draft != nil {
		// The run of this request writes the draft.
		coord.Spec.RunRequest++
		draft.Request = coord.Spec.RunRequest
		src.FollowUpDraft = draft
	}
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
	tasks, err := r.pilotTasks(ctx, project, st)
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
	st, coord, queue *aiv1.AgentTask, tracking int64) error {
	ps := coord.Status.Pilot
	if ps == nil || len(ps.Accepted) == 0 {
		return nil
	}
	tasks, err := r.pilotTasks(ctx, project, st)
	if err != nil {
		return err
	}
	accepted, opened := ps.Accepted, slices.Clone(ps.OpenedIssues)
	errs := make([][]error, len(accepted))
	limit := r.gitLabLimitOf(project)
	for i, p := range accepted {
		errs[i] = make([]error, len(p.Actions))
		for j, a := range p.Actions {
			if errs[i][j] = validAction(a, st, tasks, opened); errs[i][j] == nil {
				errs[i][j] = validRetry(a, tasks, limit)
			}
			if errs[i][j] == nil && a.Tool == "fixFlaky" {
				_, errs[i][j] = checkFlaky(ctx, gl, spec, project.Status.BotUserID, project.Status.MainHealth, a.IssueIID, flakyMilestone(spec, st), p.AcceptedBy != "")
			} else if errs[i][j] == nil && a.Tool == "retryPipeline" {
				errs[i][j] = checkRetryPipeline(project.Status.MainHealth, a.PipelineID)
			}
			if errs[i][j] == nil && p.AcceptedBy == "" && a.Tool == "editSpec" {
				errs[i][j] = errors.New("a spec edit needs an owner's yes")
			} else if errs[i][j] == nil && p.AcceptedBy == "" && a.Tool == "setTier" {
				errs[i][j] = errors.New("a tier change needs an owner's yes")
			} else if errs[i][j] == nil && p.AcceptedBy == "" && (slices.Contains(ownerTools, a.Tool) || slices.Contains(answerTools, a.Tool)) {
				errs[i][j] = fmt.Errorf("`%s` needs an owner's answer", a.Tool)
			} else if errs[i][j] == nil && p.AcceptedBy == "" && spec.Pilot.AutonomyOf(coord) != aiv1.PilotManaged {
				errs[i][j] = errors.New("the pilot proposes now (`/pilot stop` or `pilot.autonomy`)")
			} else if errs[i][j] == nil && a.Tool == "openIssue" {
				opened = append(opened, titleKey(a.Title))
			} else if errs[i][j] == nil && (a.Tool == "unskip" || a.Tool == "markDone" || queueIssue(st, a)) {
				errs[i][j] = checkOpenIssue(ctx, gl, spec, st, a.IssueIID)
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
	var acted []aiv1.PilotActed
	for i, p := range accepted {
		var b strings.Builder
		head := fmt.Sprintf("Proposal P%d accepted", p.ID)
		if p.ChatMessageID != 0 {
			head = "Asked for in the Desk chat"
		} else if strings.HasPrefix(p.Source, "tell/") {
			head = "Told with `/pilot tell`"
		}
		fmt.Fprintf(&b, "🧭 %s by `@%s`:", head, p.AcceptedBy)
		var lines []string
		for j, a := range p.Actions {
			var answer string
			if errs[i][j] == nil && a.Tool == "restartRun" {
				answer, errs[i][j] = r.pilotRestart(ctx, gl, spec, tasks[a.IssueIID], a.IssueIID, proposalHow(p), project.Status.BotUserID)
			} else if errs[i][j] == nil {
				if answer, errs[i][j] = r.runAction(ctx, gl, project, spec, st, queue, tracking, proposalHow(p), p.AcceptedBy, ownerMessageID(p), a, tasks[a.IssueIID]); errs[i][j] != nil && a.Tool == "openIssue" {
					failed = append(failed, titleKey(a.Title)) // may be opened again
				}
			}
			line := describeAction(a, errs[i][j])
			if answer != "" && errs[i][j] == nil && a.Tool == "restartRun" {
				line = fmt.Sprintf("- **restartRun** #%d: %s", a.IssueIID, answer)
			} else if answer != "" && errs[i][j] == nil {
				line += "; " + answer
			}
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
		acted = append(acted, pilotActed(r.now(), p, lines, errs[i], tasks))
		body := b.String() + "\n" + strings.Join(lines, "\n")
		if p.AcceptedBy == "" {
			body = auditNote(spec, coord, tasks, p, lines)
		}
		if err := r.postPilotNote(ctx, gl, project, spec, coord, tracking, body); err != nil {
			log.FromContext(ctx).Info("could not post the outcome of a proposal", "issue", tracking, "error", err.Error())
		}
	}
	if len(failed) == 0 && len(acted) == 0 {
		return nil
	}
	return r.patchProposals(ctx, coord, func(ps *aiv1.PilotStatus) {
		ps.OpenedIssues = slices.DeleteFunc(ps.OpenedIssues, func(k string) bool { return slices.Contains(failed, k) })
		ps.Acted = append(ps.Acted, acted...)
		ps.Acted = ps.Acted[max(0, len(ps.Acted)-aiv1.MaxPilotActed):]
	})
}

// pilotActed is the log entry of a proposal runAccepted ran without a
// prompt: its untrusted text raw (the Desk shows it without HTML), cut to
// the bounds; the issues it counted against; whether any action ran.
func pilotActed(now time.Time, p aiv1.PilotProposal, lines []string, errs []error, tasks map[int64]*aiv1.AgentTask) aiv1.PilotActed {
	e := aiv1.PilotActed{
		At:      metav1.NewTime(now),
		By:      p.AcceptedBy,
		Summary: cutText(p.Summary, aiv1.MaxPilotActedSummary),
		Why:     cutText(p.Why, aiv1.MaxPilotActedWhy),
		Ran:     slices.Contains(errs, nil),
	}
	for _, l := range lines[:min(len(lines), aiv1.MaxPilotActedLines)] {
		e.Lines = append(e.Lines, cutText(l, aiv1.MaxPilotActedLine))
	}
	for _, a := range p.Actions {
		key, _ := interventionKey(a, p.Trigger, tasks)
		if iid, err := strconv.ParseInt(key, 10, 64); err == nil && !slices.Contains(e.Issues, iid) {
			e.Issues = append(e.Issues, iid)
		}
	}
	return e
}

// cutText cuts untrusted text to at most n bytes of valid UTF-8.
func cutText(s string, n int) string {
	return textutil.Cut(strings.ToValidUTF8(s, "�"), n)
}

// ownerMessageID is the key of the owner message proposal p passes on: its
// source (tell/<noteID>, chat/<id>), else the prompt or the proposal.
func ownerMessageID(p aiv1.PilotProposal) string {
	switch {
	case p.Source != "":
		return p.Source
	case p.Prompt != "":
		return "prompt/" + p.Prompt
	}
	return fmt.Sprintf("acted/%d", p.ID)
}

// runAction executes one validated action of an accepted proposal; how
// says how it came to run (proposalHow), by who accepted it, source is the
// key of an owner's words (ownerMessageID), queue the Queue task of the
// loose issues queue (nil: none). It returns where an owner's words went.
func (r *AgentProjectReconciler) runAction(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, st, queue *aiv1.AgentTask,
	tracking int64, how, by, source string, a aiv1.PilotAction, t *aiv1.AgentTask) (string, error) {
	switch a.Tool {
	case "fixFlaky":
		// Checked again: another pilot may have handed it over meanwhile.
		mh := project.Status.MainHealth
		is, err := checkFlaky(ctx, gl, spec, project.Status.BotUserID, mh, a.IssueIID, flakyMilestone(spec, st), by != "")
		if err != nil {
			return "", err
		}
		loose, err := looseQueue(spec, queue)
		if err != nil {
			return "", err
		}
		takenBy := "the pilot"
		if by != "" {
			takenBy = "@" + by
		}
		return "", r.handOverFlaky(ctx, gl, spec, mh, st, loose, is, fmt.Sprintf("the pilot of %s, %s", pilotOf(st), how), takenBy, by != "")
	case "retryPipeline":
		// Checked again: the branch may be green or have a newer pipeline.
		if err := checkRetryPipeline(project.Status.MainHealth, a.PipelineID); err != nil {
			return "", err
		}
		_, err := gl.RetryPipeline(ctx, spec.GitLab.Project, a.PipelineID)
		return "", err
	case "rebase", "mergeBase", "skip", "unskip", "startAnyway", "pause", "resume", "revisit", "endMilestone":
		return "", r.queueCommand(ctx, st, aiv1.PilotCommand{Action: a, Owner: by, OnIssue: tracking})
	case "markDone":
		return "", markDone(ctx, gl, spec, st, how, a)
	case "setTier":
		return setTier(ctx, gl, spec, a)
	case "raiseBudget":
		return "", r.raiseBudget(ctx, gl, spec, st, a.Amount)
	case "setRule", "forgetRule":
		return "", r.runRule(ctx, gl, spec, st, a)
	case "editSpec":
		// Read again: the description the owner saw, with its command
		// lines of now; the next poll plans the milestone again.
		desc, _, err := specEdit(ctx, gl, spec, st, a)
		if err != nil {
			return "", err
		}
		_, err = gl.UpdateMilestoneDescription(ctx, spec.GitLab.Project, st.Spec.Milestone.ID, desc)
		return "", err
	case "openIssue":
		of := "the loose issues queue"
		if st.Spec.Type != aiv1.TaskTypeQueue {
			of = inertLine(st.Spec.Milestone.Title, 200)
		}
		is, err := gl.CreateIssue(ctx, spec.GitLab.Project, gitlab.CreateIssueOptions{
			Title: inertLine(a.Title, 200),
			Description: fmt.Sprintf("%s\n\n_Opened by the pilot of %s (%s)._\n\n<!-- ai-operator:pilot-issue=%s -->",
				notify.Inert(a.Note), of, how, titleKey(a.Title)),
			Labels: spec.Workflow.GeneratedLabel,
		})
		if err != nil || st.Spec.Type != aiv1.TaskTypeQueue {
			return "", err
		}
		// Not taken on its own: the owners decide what the queue works on.
		return fmt.Sprintf("opened #%d, not in the queue; an owner takes it with `%s take #%d`", is.IID, spec.Workflow.CommandPrefix, is.IID), nil
	}
	running := false
	// On a fresh copy, tried again on a conflict: the task changes as it runs.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Get(ctx, client.ObjectKeyFromObject(t), t); err != nil {
			return err
		}
		// An owner's words reach the running implementer without a new run.
		if running = a.Tool == "instruct" && a.Text != "" && runInProgress(t); running {
			t.Spec.Source.OwnerMessages = aiv1.AppendOwnerMessage(t.Spec.Source.OwnerMessages,
				aiv1.OwnerMessage{ID: source, Owner: a.Owner, Text: a.Text, At: metav1.NewTime(r.now())})
			return r.Update(ctx, t)
		}
		switch a.Tool {
		case "reReview":
			if err := r.askReview(ctx, t, t.Status.Run.ReviewKey()); err != nil {
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
		return "", err
	}
	if running {
		body := fmt.Sprintf("🧭 Message from `@%s` via the pilot, passed to the running implementer: %s", a.Owner, inertLine(a.Text, aiv1.MaxOwnerMessageText))
		if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, a.IssueIID, body); err != nil {
			log.FromContext(ctx).Info("could not show the owner's message", "issue", a.IssueIID, "error", err.Error())
		}
		return fmt.Sprintf("queued for #%d's running implementer", a.IssueIID), nil
	}
	// A run of the paused issue the pilot (or an owner through it) started.
	r.commandRun(ctx, st, t, pilotBy(by))
	if a.Tool != "instruct" {
		return "", nil
	}
	// Shown where the implementer works; its prompt has it from the
	// task, not from this comment.
	body := fmt.Sprintf("🧭 Instruction from the pilot (%s) for the next run: %s", how, describeAction(a, nil))
	if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, a.IssueIID, body); err != nil {
		log.FromContext(ctx).Info("could not show the pilot's instruction", "issue", a.IssueIID, "error", err.Error())
	}
	if a.Text == "" {
		return "", nil
	}
	return fmt.Sprintf("queued for #%d's next run", a.IssueIID), nil
}

// setTier moves open issue a.IssueIID to tier a.Profile with one update: the
// tier's profile label added, its other profile labels and its model and
// effort labels removed. It requests no run: the next one resolves the tier
// (IssueRole), a running call goes on as it is. It returns the acted line.
func setTier(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, a aiv1.PilotAction) (string, error) {
	p, ok := spec.Profile(a.Profile)
	if !ok {
		return "", validProfile(spec, a.Profile)
	}
	is, err := gl.GetIssue(ctx, spec.GitLab.Project, a.IssueIID)
	switch {
	case err != nil:
		return "", fmt.Errorf("getting issue #%d: %w", a.IssueIID, err)
	case is.State != "opened":
		return "", fmt.Errorf("#%d is closed", a.IssueIID)
	}
	wf, add := spec.Workflow, spec.ProfileLabel(p.Name)
	var remove []string
	for _, l := range is.Labels {
		if l != add && slices.ContainsFunc([]string{wf.ProfileLabelPrefix, wf.ModelLabelPrefix, wf.EffortLabelPrefix},
			func(prefix string) bool { return prefix != "" && strings.HasPrefix(l, prefix) }) {
			remove = append(remove, l)
		}
	}
	if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, a.IssueIID, gitlab.UpdateIssueOptions{AddLabels: []string{add}, RemoveLabels: remove}); err != nil {
		return "", err
	}
	line := fmt.Sprintf("#%d now runs on tier %s (%s)", a.IssueIID, p.Name, p.Setting())
	if len(remove) > 0 {
		line += "; removed " + strings.Join(remove, ", ")
	}
	return line, nil
}

// queueIssue reports whether action a of the pilot of Queue task st is on
// one of its issues, checked in GitLab before it runs (checkOpenIssue); a
// pause is the whole queue's.
func queueIssue(st *aiv1.AgentTask, a aiv1.PilotAction) bool {
	return st.Spec.Type == aiv1.TaskTypeQueue && a.IssueIID != 0 && a.Tool != "fixFlaky" && a.Tool != "pause"
}

// pilotOf names what the pilot of st steers: the milestone of spec task st,
// or the loose issues queue of a Queue task.
func pilotOf(st *aiv1.AgentTask) string {
	if st.Spec.Type == aiv1.TaskTypeQueue {
		return "the loose issues queue"
	}
	return fmt.Sprintf("milestone %q", inertLine(st.Spec.Milestone.Title, 100))
}

// markDone closes issue a.IssueIID of the milestone (or loose issues queue)
// of st as done (the done label, the active labels removed) at an owner's
// answer (how), with the pilot's reason on the issue. Autopilot then counts
// it as done by hand (doneByHand): merged, so the issues that wait for it can
// start.
func markDone(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, st *aiv1.AgentTask, how string, a aiv1.PilotAction) error {
	wf := spec.Workflow
	// The reason first, so the issue never closes without it; an accepted
	// action runs once (runAccepted), so neither repeats.
	reason := "no reason given"
	if strings.TrimSpace(a.Note) != "" {
		reason = inertLine(a.Note, 2000)
	}
	body := fmt.Sprintf("✅ Closed as done by the pilot of %s (%s), not merged by autopilot: %s", pilotOf(st), how, reason)
	if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, a.IssueIID, body); err != nil {
		return fmt.Errorf("commenting on #%d: %w", a.IssueIID, err)
	}
	_, err := gl.UpdateIssue(ctx, spec.GitLab.Project, a.IssueIID, gitlab.UpdateIssueOptions{
		StateEvent: "close", AddLabels: []string{wf.DoneLabel}, RemoveLabels: append(wf.ActiveLabels(), wf.DraftLabel),
	})
	if err != nil {
		return fmt.Errorf("closing #%d: %w", a.IssueIID, err)
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
		switch ap := t.Status.Autopilot; {
		case ap != nil && t.Spec.Type == aiv1.TaskTypeQueue:
			done = ap.LastPilotCommand // the queue has no revisits
		case ap != nil:
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

// askReview asks the next run of task to review head (the review key,
// RunStatus.ReviewKey: with a companion it names both heads) again without the
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
	case "restartRun":
		s = fmt.Sprintf("**restartRun** #%d: its stalled run starts again, counted as an interruption", a.IssueIID)
	case "rebase":
		s = fmt.Sprintf("**rebase** #%d: GitLab rebases its MR onto the default branch; the review holds if the change is unchanged", a.IssueIID)
	case "mergeBase":
		s = fmt.Sprintf("**mergeBase** #%d: the agent merges the default branch into its MR", a.IssueIID)
	case "skip":
		s = fmt.Sprintf("**skip** #%d: autopilot leaves it out", a.IssueIID)
	case "unskip":
		s = fmt.Sprintf("**unskip** #%d: it goes back to pending, and autopilot picks it once it can start", a.IssueIID)
	case "startAnyway":
		s = fmt.Sprintf("**startAnyway** #%d: it may start although an issue it depends on was skipped", a.IssueIID)
	case "markDone":
		s = fmt.Sprintf("**markDone** #%d: closed as done, not merged by autopilot; the issues that wait for it can start", a.IssueIID)
		if a.Note != "" {
			s += ". The reason: " + inertLine(a.Note, 2000)
		}
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
	case "setTier":
		s = fmt.Sprintf("**setTier** #%d to tier %s: its next run uses it, a running call goes on", a.IssueIID, inertLine(a.Profile, 40))
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

// inertLine is inert text on one line, cut to n bytes.
func inertLine(s string, n int) string {
	return notify.Inert(textutil.Truncate(strings.Join(strings.Fields(s), " "), n))
}
