package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

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

// tell passes an owner's "/pilot tell #N: …" on to the implementer of #N
// verbatim, accepted by that owner: the owner is the author of their own
// words, so it needs no Accept. It runs at the end of the poll as every
// accepted proposal does (runAccepted), and the reply says where it goes.
func (r *AgentProjectReconciler) tell(ctx context.Context, project *aiv1.AgentProject,
	st, coord *aiv1.AgentTask, n pilotNote, iid int64, text string) (string, error) {
	tasks, err := r.issueTasks(ctx, project, st.Spec.Milestone.IID)
	if err != nil {
		return "", err
	}
	a := aiv1.PilotAction{Tool: "instruct", IssueIID: iid, Owner: n.Author.Username, Text: textutil.Truncate(text, maxPilotMessage)}
	if err := validAction(a, st, tasks, nil); err != nil {
		return "Not passed on: " + err.Error() + ".", nil
	}
	p := aiv1.PilotProposal{Summary: fmt.Sprintf("pass @%s's words on to the implementer of #%d", n.Author.Username, iid),
		Actions: []aiv1.PilotAction{a}, State: aiv1.PilotState(st, tasks, []aiv1.PilotAction{a}), Source: fmt.Sprintf("tell/%d", n.ID),
		AcceptedBy: n.Author.Username, Trigger: iid}
	if err := r.patchProposals(ctx, coord, func(ps *aiv1.PilotStatus) {
		// Once per note, also when the poll that took it is run again.
		if !slices.ContainsFunc(ps.Accepted, func(q aiv1.PilotProposal) bool { return q.Source == p.Source }) {
			ps.Accepted = append(ps.Accepted, p)
		}
	}); err != nil {
		return "", err
	}
	if t := tasks[iid]; t != nil && runInProgress(t) {
		return fmt.Sprintf("Passed on to #%d's running implementer; I'll say here when it is delivered.", iid), nil
	}
	return fmt.Sprintf("Queued for #%d's next run.", iid), nil
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
