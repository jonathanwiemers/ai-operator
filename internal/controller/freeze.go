package controller

import (
	"context"
	"fmt"
	"slices"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
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
		return a.pauseRun(ctx, it, aiv1.PauseMainRed, reason)
	}
	if err := a.r.unblock(ctx, a.gl, a.spec, is, task); err != nil {
		return err
	}
	if a.ap.Phase != aiv1.AutopilotPaused || a.ap.PausedFor != it.IID {
		return nil
	}
	why := fmt.Sprintf("`%s` is green again", base)
	switch {
	case mh == nil:
		why = fmt.Sprintf("the health of `%s` is not watched any more", base)
	case !*a.spec.MainHealth.FreezeAutopilot:
		why = "the merge freeze is switched off (`mainHealth.freezeAutopilot`)"
	}
	a.log.Info("autopilot continues by itself", "issue", it.IID, "why", why)
	if err := a.comment(ctx, fmt.Sprintf("▶️ Autopilot continues: %s.", why)); err != nil {
		return err
	}
	a.lift()
	return nil
}

// loadMainRedFix keeps the main-red issue as the lane's item while the
// default branch is red, and while autopilot is paused for it; one item per
// red episode. The item starts with mainHealth.autoFix, or once an owner
// handed the issue to the agent (an active workflow label). Its issue is in
// no milestone, so it is read on its own.
func (a *autopilot) loadMainRedFix(ctx context.Context) error {
	if mh := a.r.freshMainHealth(a.spec, a.project.Status.MainHealth, mainHealthStale); a.lane && *a.spec.MainHealth.FreezeAutopilot && mh != nil {
		a.mainRed = mh.IssueIID
	}
	var is *gitlab.Issue
	get := func(iid int64) error {
		var err error
		if is, err = a.gl.GetIssue(ctx, a.spec.GitLab.Project, iid); err != nil && !gitlab.IsNotFound(err) {
			return fmt.Errorf("getting issue #%d: %w", iid, err)
		}
		return nil
	}
	switch fix := a.ap.MainRedFix; {
	case fix != nil && (fix.IID == a.mainRed || fix.IID == a.ap.PausedFor):
	case a.mainRed != 0 && !a.spec.MainHealth.AutoFix:
		if err := get(a.mainRed); err != nil {
			return err
		}
		a.ap.MainRedFix = nil
		if is != nil && hasAny(*is, a.spec.Workflow.ActiveLabels()...) {
			a.ap.MainRedFix = &aiv1.AutopilotItem{IID: a.mainRed, State: aiv1.AutopilotItemActive}
		}
	case a.mainRed != 0:
		a.ap.MainRedFix = &aiv1.AutopilotItem{IID: a.mainRed, State: aiv1.AutopilotItemActive}
	default:
		a.ap.MainRedFix = nil
	}
	fix := a.ap.MainRedFix
	if fix == nil || fix.State != aiv1.AutopilotItemActive {
		return nil
	}
	if is == nil {
		if err := get(fix.IID); err != nil {
			return err
		}
	}
	if is != nil {
		a.issues[fix.IID] = *is
	}
	return nil
}

// mainRedLane drives the main-red issue through the same merge gate as the
// queue and merges it: the only merge while the default branch is red. Not
// while a pause holds autopilot as a whole ("/agent pause", a plan cycle) or
// is about the issue itself (autoResume ends that one), nor once a human
// took the issue from the agent (no workflow label).
func (a *autopilot) mainRedLane(ctx context.Context) error {
	fix := a.ap.MainRedFix
	is, ok := a.issues[a.mainRed]
	switch {
	case fix == nil || fix.IID != a.mainRed || fix.State != aiv1.AutopilotItemActive,
		a.ap.Phase != aiv1.AutopilotRunning && (a.ap.Phase != aiv1.AutopilotPaused || a.ap.PausedFor == 0 || a.ap.PausedFor == fix.IID),
		ok && is.State == "opened" && !hasAny(is, a.spec.Workflow.ActiveLabels()...):
		return nil
	}
	return a.drive(ctx, fix)
}

// about says that a pause is over the fix of the red default branch.
func (a *autopilot) about(it *aiv1.AutopilotItem, reason string) string {
	if it == a.ap.MainRedFix {
		return fmt.Sprintf("the fix of the red default branch (#%d) cannot be merged: %s", it.IID, reason)
	}
	return reason
}
