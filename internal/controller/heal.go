package controller

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

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
	return a.rebaseActive(ctx, it, mr, task, why, aiv1.ResolvedBySelfHealed, "", func() { it.HealedOn = mh.SHA })
}

// rebaseActive rebases the active issue's MR (or, if a rebase cannot help,
// mergeBase hands it to the agent) and says so on the tracking issue with
// why; a pause it ends was resolved by 'by' (who, see resumeLine). The rebase goes first: if it fails,
// nothing is said and the next poll tries again. Once it was requested,
// requested runs (which records it).
func (a *autopilot) rebaseActive(ctx context.Context, it *aiv1.AutopilotItem, mr *gitlab.MergeRequest, task *aiv1.AgentTask, why string, by aiv1.PauseResolver, who string, requested func()) error {
	a.log.Info("autopilot rebases the active merge request", "issue", it.IID, "mr", mr.IID, "why", why)
	return a.remedyActive(ctx, it, "rebase", why, by, who, func() error {
		if err := a.rebase(ctx, it, mr, task); err != nil {
			return err
		}
		requested()
		return nil
	})
}

// remedyActive runs act, the remedy exit (aiv1.PauseExits) on the active
// issue, and says so on the tracking issue with why. A pause about the
// issue of a kind the remedy is an exit of ends, unless act paused anew,
// so drive follows the new head, resolved by 'by'. The pause ends before the
// comment, so a failed comment can neither repeat act nor leave the pause in
// place.
func (a *autopilot) remedyActive(ctx context.Context, it *aiv1.AutopilotItem, remedy, why string, by aiv1.PauseResolver, who string, act func() error) error {
	paused := a.ap.Phase == aiv1.AutopilotPaused && a.ap.PausedFor == it.IID &&
		slices.ContainsFunc(aiv1.PauseExits(a.ap.PauseKind), func(e aiv1.PauseExit) bool { return e.ID == remedy })
	reason := a.ap.PausedReason
	if err := act(); err != nil {
		return err
	}
	line := "🔄 " + why + "."
	if paused && a.ap.Phase == aiv1.AutopilotPaused && a.ap.PausedReason == reason {
		a.lift(by)
		line = resumeLine(by, who, why)
	}
	return a.comment(ctx, line)
}

// mrCommand applies "rebase #N" and "mergeBase #N" (by an owner on the
// tracking issue, or the pilot's tools): GitLab rebases the active issue's
// MR (rebaseActive), or the agent merges the default branch into it
// (mergeBase). Anything else, or a head either cannot help, is answered.
func (a *autopilot) mrCommand(ctx context.Context, word, arg, by string) (string, bool, error) {
	prefix := a.spec.Workflow.CommandPrefix
	iid, _ := strconv.ParseInt(strings.TrimPrefix(arg, "#"), 10, 64)
	it, task := a.item(iid), a.tasks[iid]
	refuse := func(format string, args ...any) (string, bool, error) {
		return fmt.Sprintf("`%s` needs the active issue with a merge request, e.g. `%s %s #12`: ", word, prefix, word) + fmt.Sprintf(format, args...) + ".", false, nil
	}
	switch {
	case it == nil || it.State != aiv1.AutopilotItemActive || it == a.ap.MainRedFix:
		return refuse("#%d is not the active issue of the queue", iid)
	case it.MergeRequestIID == 0 || task == nil || task.Status.Run.PushedSHA == "":
		return refuse("#%d has no pushed merge request", iid)
	case task.RunPending():
		return refuse("a run of #%d is pending, wait for it", iid)
	case it.RebasedFrom != "":
		return refuse("a rebase of !%d is in progress", it.MergeRequestIID)
	}
	mr, err := a.gl.GetMergeRequest(ctx, a.spec.GitLab.Project, it.MergeRequestIID)
	if err != nil {
		return "", false, fmt.Errorf("getting merge request !%d: %w", it.MergeRequestIID, err)
	}
	base, err := a.base(ctx)
	if err != nil {
		return "", false, err
	}
	switch {
	case mr.State != "opened":
		return refuse("!%d is %s", mr.IID, mr.State)
	case word == "rebase" && mr.SHA != task.Status.Run.PushedSHA:
		// A rebase would carry someone's push past the review.
		return refuse("the head of !%d (%s) is not the commit the agent pushed (%s)", mr.IID, short(mr.SHA), short(task.Status.Run.PushedSHA))
	case word == "mergeBase" && !*a.spec.Workflow.ResolveConflicts:
		return refuse("the agent does not merge the default branch here (`workflow.resolveConflicts` is off)")
	case word == "mergeBase" && task.Spec.Source.MergeBaseFor == mr.SHA:
		return refuse("the agent merged `%s` into this head of !%d before", base, mr.IID)
	case word == "rebase":
		return fmt.Sprintf("Rebasing !%d.", mr.IID), true, a.rebaseActive(ctx, it, mr, task, fmt.Sprintf("%s asked to rebase !%d onto `%s`", by, mr.IID, base), resolverOf(by), by, func() {})
	}
	a.log.Info("autopilot asks the agent to merge the default branch", "issue", it.IID, "mr", mr.IID, "by", by)
	return fmt.Sprintf("The agent merges `%s` into !%d.", base, mr.IID), true, a.remedyActive(ctx, it, "mergeBase", fmt.Sprintf("%s asked the agent to merge `%s` into !%d", by, base, mr.IID), resolverOf(by), by, func() error {
		return a.mergeBase(ctx, it, mr, task, "asked by "+by)
	})
}
