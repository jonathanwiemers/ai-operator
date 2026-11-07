package controller

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// reviewErrors is the merge gate of a head some reviewers failed on. If all
// of them failed transiently (an outage) and no re-review was asked for this
// head yet, it waits for one (reReview). Failing transiently again pauses
// and blocks the issue: there is nothing in it for a human to fix.
func (a *autopilot) reviewErrors(it *aiv1.AutopilotItem, task *aiv1.AgentTask) gateResult {
	run := task.Status.Run
	g := gateResult{state: gateFailed, kind: aiv1.PauseReviewErrors, byRun: true,
		reason: fmt.Sprintf("%s on #%d, so the change is not fully reviewed", failedReviewers(run), it.IID)}
	switch {
	case !run.ReviewErrorsTransient || run.PushedSHA == "":
	case task.Spec.Source.ReviewFor != run.ReviewKey():
		return gateResult{state: gateReReview}
	default:
		g.reason += ", also when reviewed again (an outage of their service)"
		g.block = aiv1.BlockInfra
	}
	return g
}

// failedReviewers names each failed reviewer and why, sorted so the reason
// does not change with the status order. A resetAt is written by the review
// run, so it stays the same across polls; its date is relative to the run's.
func failedReviewers(run aiv1.RunStatus) string {
	if len(run.ReviewErrorKinds) == 0 {
		return fmt.Sprintf("%d reviewers failed", run.ReviewErrors)
	}
	errs := slices.Clone(run.ReviewErrorKinds)
	slices.SortStableFunc(errs, func(x, y aiv1.ReviewError) int {
		return cmp.Or(strings.Compare(string(x.Reviewer), string(y.Reviewer)), strings.Compare(string(x.Kind), string(y.Kind)))
	})
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		why := []string{string(e.Kind)}
		if e.Engine != "" {
			why = append(why, string(e.Engine))
		}
		if e.ResetAt != nil {
			day := e.ResetAt.Time
			if run.LastActivityTime != nil {
				day = run.LastActivityTime.Time
			}
			why = append(why, "resets "+resetTime(e.ResetAt.Time, day))
		}
		parts = append(parts, fmt.Sprintf("%s failed (%s)", e.Reviewer, strings.Join(why, ", ")))
	}
	return strings.Join(parts, ", ")
}

// reReview asks for the re-review of the pushed head once
// agents.review.retryAfter has passed since the run whose reviewers failed.
func (a *autopilot) reReview(ctx context.Context, it *aiv1.AutopilotItem, task *aiv1.AgentTask) error {
	run := task.Status.Run
	if t := run.LastActivityTime; t != nil && a.r.now().Before(t.Add(a.spec.Agents.Review.RetryAfter.Duration)) {
		return nil
	}
	a.log.Info("autopilot asks for a re-review, the reviewers failed transiently", "issue", it.IID, "sha", run.PushedSHA)
	task.Spec.Source.ReviewFor = run.ReviewKey()
	if err := a.r.requestRun(ctx, a.gl, a.spec, task, it.IID); err != nil {
		return err
	}
	a.askedRun(it, run.PushedSHA)
	return nil
}

// failGate pauses for a failed merge gate; a pause that is no human's to fix
// also blocks the issue.
func (a *autopilot) failGate(ctx context.Context, it *aiv1.AutopilotItem, is gitlab.Issue, task *aiv1.AgentTask, g gateResult) error {
	pause := a.pauseActive
	if g.byRun {
		pause = a.pauseRun
	}
	if err := pause(ctx, it, g.kind, g.reason); err != nil || g.block == "" || a.ap.Phase != aiv1.AutopilotPaused {
		return err
	}
	return a.r.block(ctx, a.spec.Workflow, is, task, g.block, g.reason)
}

// unblockActive lifts the infra block of the active issue, and of the
// main-red lane's, when a pause ends (also one a later "/agent pause" took
// over). A block its failed run asked for stays: only a new run lifts it.
func (a *autopilot) unblockActive(ctx context.Context) error {
	items := a.ap.Items
	if a.ap.MainRedFix != nil {
		items = append(slices.Clip(items), *a.ap.MainRedFix)
	}
	for _, it := range items {
		task := a.tasks[it.IID]
		if it.State != aiv1.AutopilotItemActive || task == nil || task.Status.Blocked == nil || task.Status.Blocked.Reason != aiv1.BlockInfra ||
			task.Status.Run.BlockReason == aiv1.BlockInfra {
			continue
		}
		if err := a.r.unblock(ctx, a.gl, a.spec, a.issues[it.IID], task); err != nil {
			return err
		}
	}
	return nil
}
