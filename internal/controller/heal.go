package controller

import (
	"context"
	"fmt"
	"slices"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// healBaseFailing rebases the active MR whose CI failed only on jobs that
// also fail on the default branch (BaseFailing), once that branch is green at
// a commit the MR is not based on: nothing reruns its pipeline, and a retry
// would run on the old base. Once per green commit (HealedOn); a ciRed pause
// about the issue ends, so drive hands the rebased head to CI.
func (a *autopilot) healBaseFailing(ctx context.Context) error {
	i := slices.IndexFunc(a.ap.Items, func(it aiv1.AutopilotItem) bool { return it.State == aiv1.AutopilotItemActive })
	if i < 0 {
		return nil
	}
	it := &a.ap.Items[i]
	is, ok := a.issues[it.IID]
	task := a.tasks[it.IID]
	mh := a.r.freshMainHealth(a.spec, a.project.Status.MainHealth, mainHealthStale)
	switch {
	case !ok || is.State != "opened" || task == nil || it.RebasedFrom != "",
		a.ap.Phase != aiv1.AutopilotRunning && (a.ap.Phase != aiv1.AutopilotPaused || a.ap.PausedFor != it.IID || a.ap.PauseKind != aiv1.PauseCIRed),
		mh == nil || mh.State != aiv1.MainHealthGreen || mh.SHA == "" || mh.SHA == it.HealedOn:
		return nil
	}
	// Only the old base's red CI holds the issue, nothing before it.
	if g := a.gateCheck(it, is, task); g.state != gateFailed || g.kind != aiv1.PauseCIRed || task.Status.CI.State != aiv1.CIStateBaseFailing {
		return nil
	}
	mr, err := a.gl.GetMergeRequest(ctx, a.spec.GitLab.Project, it.MergeRequestIID)
	if err != nil {
		return fmt.Errorf("getting merge request !%d: %w", it.MergeRequestIID, err)
	}
	if mr.State != "opened" || mr.SHA != task.Status.Run.PushedSHA || mr.DiffRefs != nil && mr.DiffRefs.BaseSHA == mh.SHA {
		return nil // drive pauses for the MR; or it already has the fix
	}
	base, err := a.base(ctx)
	if err != nil {
		return err
	}
	why := fmt.Sprintf("`%s` is green at %s: rebasing !%d, whose CI failed on jobs that also failed on `%s`", base, short(mh.SHA), mr.IID, base)
	return a.rebaseActive(ctx, it, mr, task, why, func() { it.HealedOn = mh.SHA })
}

// rebaseActive rebases the active issue's MR (or, if a rebase cannot help,
// mergeBase hands it to the agent) and says so on the tracking issue with
// why. A pause about the issue ends, unless the rebase paused anew, so drive
// follows the rebased head to CI. The rebase goes first: if it fails, nothing
// is said and the next poll tries again. Once it was requested, requested
// runs (which records it) and the pause ends before the comment, so a failed
// comment can neither repeat the rebase nor leave the pause in place.
func (a *autopilot) rebaseActive(ctx context.Context, it *aiv1.AutopilotItem, mr *gitlab.MergeRequest, task *aiv1.AgentTask, why string, requested func()) error {
	paused, reason := a.ap.Phase == aiv1.AutopilotPaused && a.ap.PausedFor == it.IID, a.ap.PausedReason
	a.log.Info("autopilot rebases the active merge request", "issue", it.IID, "mr", mr.IID, "why", why)
	if err := a.rebase(ctx, it, mr, task); err != nil {
		return err
	}
	requested()
	line := "🔄 " + why + "."
	if paused && a.ap.Phase == aiv1.AutopilotPaused && a.ap.PausedReason == reason {
		a.lift()
		line = "▶️ Autopilot continues: " + why + "."
	}
	return a.comment(ctx, line)
}
