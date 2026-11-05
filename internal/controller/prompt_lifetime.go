package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// A prompt lives until it is answered or its source resolves: main is
// green again, its issue is merged or closed, the agent's run moved on, or
// a newer prompt has the same source (addPrompt). Only proposals expire by
// time, after proposalTTL, as PilotProposals do.

// syncOpenPrompts runs syncPrompts on the spec tasks and the Implement
// tasks (by issue) that have open prompts. byMilestone are the spec tasks
// by milestone. One failing task does not hold up the others.
func (r *AgentProjectReconciler) syncOpenPrompts(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	specTasks []*aiv1.AgentTask, byMilestone map[int64]*aiv1.AgentTask, issues map[int64]gitlab.Issue, implTasks map[int64]*aiv1.AgentTask) error {
	open := func(t *aiv1.AgentTask) bool {
		return slices.ContainsFunc(t.Status.Prompts, func(p aiv1.Prompt) bool { return !p.State.Closed() })
	}
	now, mh, closed := r.now(), project.Status.MainHealth, closedIssues(ctx, gl, spec.GitLab.Project)
	var errs []error
	for _, st := range specTasks {
		if !open(st) {
			continue
		}
		// The tasks answerProposal checks a proposal's state with.
		tasks, err := r.issueTasks(ctx, project, st.Spec.Milestone.IID)
		if err == nil {
			err = r.syncPrompts(ctx, st, promptResolved(now, mh, issues, tasks, st, closed))
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("prompts of milestone %q: %w", st.Spec.Milestone.Title, err))
		}
	}
	for iid, t := range implTasks {
		if !open(t) {
			continue
		}
		var st *aiv1.AgentTask
		if t.Spec.Milestone != nil {
			st = byMilestone[t.Spec.Milestone.IID]
		}
		if err := r.syncPrompts(ctx, t, promptResolved(now, mh, issues, implTasks, st, closed)); err != nil {
			errs = append(errs, fmt.Errorf("prompts of issue #%d: %w", iid, err))
		}
	}
	return errors.Join(errs...)
}

// syncPrompts closes the open prompts of holder that resolved says are
// resolved, in one optimistic-lock patch, and writes only when one is.
func (r *AgentProjectReconciler) syncPrompts(ctx context.Context, holder *aiv1.AgentTask, resolved func(aiv1.Prompt) (aiv1.PromptState, bool)) error {
	due := func(p aiv1.Prompt) bool {
		if p.State.Closed() {
			return false
		}
		_, ok := resolved(p)
		return ok
	}
	if !slices.ContainsFunc(holder.Status.Prompts, due) {
		return nil
	}
	now := metav1.NewTime(r.now())
	var closed []string
	err := r.patchPrompts(ctx, holder, func(list []aiv1.Prompt) []aiv1.Prompt {
		closed = closed[:0]
		for i := range list {
			if q := &list[i]; !q.State.Closed() {
				if state, ok := resolved(*q); ok {
					q.State, q.ClosedAt = state, &now
					closed = append(closed, q.ID+"="+string(state))
				}
			}
		}
		return list
	})
	if err == nil && len(closed) > 0 {
		log.FromContext(ctx).Info("prompts closed: their source resolved", "task", holder.Name, "prompts", closed)
	}
	return err
}

// promptResolved says which open prompts resolved, and how: st is the
// milestone's spec task (nil: none), tasks its issues' tasks, issues the
// open issues of the poll. closed confirms that an issue missing from
// issues is closed; it is asked only for prompts whose issue is missing.
func promptResolved(now time.Time, mh *aiv1.MainHealthStatus, issues map[int64]gitlab.Issue, tasks map[int64]*aiv1.AgentTask, st *aiv1.AgentTask,
	closed func(int64) bool) func(aiv1.Prompt) (aiv1.PromptState, bool) {
	return func(p aiv1.Prompt) (aiv1.PromptState, bool) {
		src := p.Source
		switch src.Kind {
		case aiv1.PromptProposal:
			if p.PostedAt == nil || now.Sub(p.PostedAt.Time) >= proposalTTL {
				return aiv1.PromptExpired, true
			}
			if p.PilotState != "" && st != nil && proposalState(st, tasks, p) != p.PilotState {
				return aiv1.PromptSuperseded, true
			}
		case aiv1.PromptMainHealth:
			if !mainUnhealthy(mh, src.IssueIID) {
				return aiv1.PromptSuperseded, true
			}
		case aiv1.PromptNeedsInput:
			// An answer not shown yet (showAnswers) keeps it.
			if t := tasks[src.IssueIID]; t != nil && src.RunRequest != 0 && t.Spec.RunRequest > src.RunRequest &&
				delivery(t.Spec.Source.PromptAnswers, p.ID) == nil {
				return aiv1.PromptSuperseded, true // the agent ran again without the answer
			}
		case aiv1.PromptBudget:
			if budgetResolved(p, st) {
				return aiv1.PromptSuperseded, true
			}
		case aiv1.PromptRevisit:
			// A revisit is about a merged issue's decision: its issue
			// resolves nothing.
			return "", false
		}
		if iid := src.IssueIID; iid != 0 {
			if _, open := issues[iid]; autopilotItemState(st, iid) == aiv1.AutopilotItemMerged || !open && closed(iid) {
				return aiv1.PromptSuperseded, true
			}
		}
		return "", false
	}
}

// proposalState is PilotState of the milestone and the actions of p's
// options, which a proposal prompt records as PilotState when it is made.
// An option made from an owner's answer later does not count.
func proposalState(st *aiv1.AgentTask, tasks map[int64]*aiv1.AgentTask, p aiv1.Prompt) string {
	var actions []aiv1.PilotAction
	for _, o := range p.Options {
		if !o.FromAnswer {
			actions = append(actions, o.Actions...)
		}
	}
	return aiv1.PilotState(st, tasks, actions)
}

// mainUnhealthy reports whether the default branch is still red (or being
// retried) or flaky for issue iid: the main-red issue, or a flaky issue
// of the last pipeline. Without an issue, whether it is not green at all.
// The watcher switched off counts as green.
func mainUnhealthy(mh *aiv1.MainHealthStatus, iid int64) bool {
	if mh == nil {
		return false
	}
	switch mh.State {
	case aiv1.MainHealthRed, aiv1.MainHealthRetrying:
		return iid == 0 || mh.IssueIID == iid
	case aiv1.MainHealthFlaky:
		return iid == 0 || slices.ContainsFunc(mh.Flaky, func(f aiv1.FlakyJobReport) bool { return f.IssueIID == iid })
	}
	return false
}

// autopilotItemState is the state of issue iid in the autopilot queue of
// st, empty if it is not in it.
func autopilotItemState(st *aiv1.AgentTask, iid int64) aiv1.AutopilotItemState {
	if st == nil || st.Status.Autopilot == nil {
		return ""
	}
	items := st.Status.Autopilot.Items
	if i := slices.IndexFunc(items, func(it aiv1.AutopilotItem) bool { return it.IID == iid }); i >= 0 {
		return items[i].State
	}
	return ""
}

// closedIssues confirms through GetIssue that an issue is closed (or
// gone), once per issue and poll. An error counts as open: the prompt is
// checked again next poll.
func closedIssues(ctx context.Context, gl gitlab.API, project string) func(int64) bool {
	seen := map[int64]bool{}
	return func(iid int64) bool {
		if c, ok := seen[iid]; ok {
			return c
		}
		is, err := gl.GetIssue(ctx, project, iid)
		switch {
		case gitlab.IsNotFound(err):
			seen[iid] = true
		case err != nil:
			log.FromContext(ctx).Info("could not check the issue of a prompt", "issue", iid, "error", err.Error())
			seen[iid] = false
		default:
			seen[iid] = is.State == "closed"
		}
		return seen[iid]
	}
}
