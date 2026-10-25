package controller

import (
	"context"
	"fmt"
	"slices"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// mainHealthStale is after how many intervals without a check the main
// health no longer freezes autopilot, so a watcher that stopped does not hold
// it forever.
const mainHealthStale = 3

// freezeOnMainRed holds autopilot while the default branch is red
// (mainHealth.freezeAutopilot). Checked after the merge gate's run, review
// and CI checks passed, so the active issue's CI loop is final: the issue is
// blocked (main-red) and autopilot pauses instead of merging. While a red
// branch's next pipeline is retried, the freeze holds. Once the branch is
// green, or its health disabled or stale, the block is lifted and autopilot
// continues by itself; the gate runs again on the next poll, after the CI
// loop saw the issue's pipelines.
func (a *autopilot) freezeOnMainRed(ctx context.Context) error {
	i := slices.IndexFunc(a.ap.Items, func(it aiv1.AutopilotItem) bool { return it.State == aiv1.AutopilotItemActive })
	if i < 0 {
		return nil
	}
	it := &a.ap.Items[i]
	is, ok := a.issues[it.IID]
	task := a.tasks[it.IID]
	if !ok || is.State != "opened" || task == nil {
		return nil
	}
	if task.Status.Run.MergeRequestIID != 0 { // as drive does
		it.MergeRequestIID, it.MergeRequestURL = task.Status.Run.MergeRequestIID, task.Status.Run.MergeRequestURL
	}
	blocked := task.Status.Blocked != nil && task.Status.Blocked.Reason == aiv1.BlockMainRed
	mh := a.r.freshMainHealth(a.spec, a.project.Status.MainHealth, mainHealthStale)
	red := mh != nil && *a.spec.MainHealth.FreezeAutopilot &&
		(mh.State == aiv1.MainHealthRed || mh.State == aiv1.MainHealthRetrying && (blocked || mh.IssueIID != 0))
	switch {
	case red && (a.ap.Phase != aiv1.AutopilotRunning || !blocked && a.gateCheck(it, is, task).state != gatePassed),
		!red && !blocked:
		return nil
	}
	base, err := a.base(ctx)
	if err != nil {
		return err
	}
	if red {
		reason := fmt.Sprintf("`%s` is red", base)
		if mh.IssueIID != 0 {
			reason += fmt.Sprintf(" (#%d)", mh.IssueIID)
		}
		reason += ": merging waits until it is green"
		if err := a.r.block(ctx, a.spec.Workflow, is, task, aiv1.BlockMainRed, reason); err != nil {
			return err
		}
		return a.pauseRun(ctx, it, reason)
	}
	if err := a.r.unblock(ctx, a.gl, a.spec, is, task); err != nil {
		return err
	}
	if a.ap.Phase != aiv1.AutopilotPaused || a.ap.PausedFor != it.IID {
		return nil
	}
	why := fmt.Sprintf("`%s` is green again", base)
	if mh == nil {
		why = fmt.Sprintf("the health of `%s` is not watched any more", base)
	}
	a.log.Info("autopilot continues by itself", "issue", it.IID, "why", why)
	if err := a.comment(ctx, fmt.Sprintf("▶️ Autopilot continues: %s.", why)); err != nil {
		return err
	}
	a.ap.Phase, a.ap.PausedReason, a.ap.PausedFor, a.ap.ResumeWhenReady = aiv1.AutopilotRunning, "", 0, false
	return nil
}
