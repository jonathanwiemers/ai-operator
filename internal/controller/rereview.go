package controller

import (
	"context"
	"fmt"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// reviewErrors is the merge gate of a head some reviewers failed on. If all
// of them failed transiently (an outage) and no re-review was asked for this
// head yet, it waits for one (reReview). Failing transiently again pauses
// and blocks the issue: there is nothing in it for a human to fix.
func (a *autopilot) reviewErrors(it *aiv1.AutopilotItem, task *aiv1.AgentTask) gateResult {
	run := task.Status.Run
	g := gateResult{state: gateFailed, byRun: true,
		reason: fmt.Sprintf("%d reviewers failed on #%d, so the change is not fully reviewed", run.ReviewErrors, it.IID)}
	switch {
	case !run.ReviewErrorsTransient:
	case task.Spec.Source.ReviewFor != run.PushedSHA:
		return gateResult{state: gateReReview}
	default:
		g.reason += ", also when reviewed again (an outage of their service)"
		g.block = aiv1.BlockInfra
	}
	return g
}

// reReview asks for the re-review of the pushed head once
// agents.review.retryAfter has passed since the run whose reviewers failed.
func (a *autopilot) reReview(ctx context.Context, it *aiv1.AutopilotItem, task *aiv1.AgentTask) error {
	run := task.Status.Run
	if t := run.LastActivityTime; t != nil && a.r.now().Before(t.Add(a.spec.Agents.Review.RetryAfter.Duration)) {
		return nil
	}
	a.log.Info("autopilot asks for a re-review, the reviewers failed transiently", "issue", it.IID, "sha", run.PushedSHA)
	task.Spec.Source.ReviewFor = run.PushedSHA
	return a.r.requestRun(ctx, a.gl, a.spec, task, it.IID)
}

// failGate pauses for a failed merge gate; a pause that is no human's to fix
// also blocks the issue.
func (a *autopilot) failGate(ctx context.Context, it *aiv1.AutopilotItem, is gitlab.Issue, task *aiv1.AgentTask, g gateResult) error {
	pause := a.pauseActive
	if g.byRun {
		pause = a.pauseRun
	}
	if err := pause(ctx, it, g.reason); err != nil || g.block == "" || a.ap.Phase != aiv1.AutopilotPaused {
		return err
	}
	return a.r.block(ctx, a.spec.Workflow, is, task, g.block, g.reason)
}

// unblockPaused lifts the infra block of the issue a pause was about, when
// the pause ends.
func (a *autopilot) unblockPaused(ctx context.Context, iid int64) error {
	task := a.tasks[iid]
	if task == nil || task.Status.Blocked == nil || task.Status.Blocked.Reason != aiv1.BlockInfra {
		return nil
	}
	return a.r.unblock(ctx, a.gl, a.spec, a.issues[iid], task)
}
