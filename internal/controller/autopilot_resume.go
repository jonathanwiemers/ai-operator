package controller

import (
	"context"
	"fmt"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// pauseActive pauses because of the active issue, unless its MR was merged
// meanwhile (by hand, while the issue is still open).
func (a *autopilot) pauseActive(ctx context.Context, it *aiv1.AutopilotItem, kind aiv1.PauseKind, reason string) error {
	if it.MergeRequestIID != 0 && it.Hold == nil && a.ap.Phase != aiv1.AutopilotPaused {
		mr, err := a.gl.GetMergeRequest(ctx, a.spec.GitLab.Project, it.MergeRequestIID)
		if err != nil && !gitlab.IsNotFound(err) {
			return fmt.Errorf("getting merge request !%d: %w", it.MergeRequestIID, err)
		}
		if err == nil && mr.State == "merged" {
			return a.merged(ctx, it, mr)
		}
	}
	return a.pause(ctx, kind, it.IID, a.about(it, reason))
}

// pauseMR pauses because of the active issue's MR (approvals, a push, its
// state): autopilot continues by itself only if it is merged by hand.
func (a *autopilot) pauseMR(ctx context.Context, it *aiv1.AutopilotItem, reason string) error {
	return a.pause(ctx, aiv1.PauseMergeRequest, it.IID, a.about(it, reason))
}

// pauseHead pauses because the MR's head is not the one the run reviewed: a
// run that takes it over can settle that, then autopilot continues by itself
// (autoResume).
func (a *autopilot) pauseHead(ctx context.Context, it *aiv1.AutopilotItem, reason string) error {
	if err := a.pauseMR(ctx, it, reason); err != nil {
		return err
	}
	a.ap.ResumeWhenReady = a.ap.Phase == aiv1.AutopilotPaused && a.ap.PausedFor == it.IID
	if it.Hold != nil {
		it.Hold.ResumeWhenReady = true
	}
	return nil
}

// pauseRun pauses because of the active issue's run, review or CI, which a
// later run can fix: then autopilot continues by itself (autoResume).
func (a *autopilot) pauseRun(ctx context.Context, it *aiv1.AutopilotItem, kind aiv1.PauseKind, reason string) error {
	if err := a.pauseActive(ctx, it, kind, reason); err != nil {
		return err
	}
	if a.ap.Phase == aiv1.AutopilotPaused && a.ap.PausedFor == it.IID {
		a.ap.ResumeWhenReady = true
	}
	if it.Hold != nil {
		it.Hold.ResumeWhenReady = true
	}
	return nil
}

// autoResume ends a pause about the active issue that no longer holds: it was
// merged or closed by hand, or (for a pause over its run, review or CI) a
// later run made it pass the gate; a pauseHead also needs the run to have
// taken over the MR's head. If a later run fixed the reason but the
// issue fails the gate for another one, the pause is announced again with
// that reason. A pause of any other kind waits for "resume".
func (a *autopilot) autoResume(ctx context.Context) error {
	if a.ap.Phase != aiv1.AutopilotPaused || a.ap.PausedFor == 0 {
		return nil
	}
	it := a.item(a.ap.PausedFor)
	if it == nil || it.State != aiv1.AutopilotItemActive {
		// A budget pause can be for a pending issue; budget ends it.
		if a.ap.PauseKind != aiv1.PauseBudget {
			a.ap.PausedFor, a.ap.ResumeWhenReady = 0, false
		}
		return nil
	}
	return a.resumeItem(ctx, it, itemPause{kind: a.ap.PauseKind, reason: a.ap.PausedReason, resumeWhenReady: a.ap.ResumeWhenReady,
		end: func(ctx context.Context, res resumption, why string) error {
			if err := a.unblockActive(ctx); err != nil {
				return err
			}
			a.log.Info("autopilot continues by itself", "issue", it.IID, "why", why, "via", res.via)
			return a.resumed(ctx, res, why)
		}})
}

// resumeHolds ends the holds that no longer hold, as autoResume ends a pause
// over an issue (unhold); a later run that fails for another reason holds
// anew with it. Every poll, also while the milestone is paused.
func (a *autopilot) resumeHolds(ctx context.Context) error {
	for i := range a.ap.Items {
		it := &a.ap.Items[i]
		if it.State != aiv1.AutopilotItemActive || it.Hold == nil {
			continue
		}
		h := it.Hold
		if err := a.resumeItem(ctx, it, itemPause{kind: h.Kind, reason: h.Reason, resumeWhenReady: h.ResumeWhenReady,
			end: func(ctx context.Context, res resumption, why string) error { return a.unhold(ctx, it, res, why) }}); err != nil {
			return err
		}
	}
	return nil
}

// itemPause is a milestone pause or a hold over one active item, as
// resumeItem checks it; end ends it as res says, with why.
type itemPause struct {
	kind            aiv1.PauseKind
	reason          string
	resumeWhenReady bool
	end             func(ctx context.Context, res resumption, why string) error
}

// resumeItem ends p over the active it once it no longer holds (autoResume).
func (a *autopilot) resumeItem(ctx context.Context, it *aiv1.AutopilotItem, p itemPause) error {
	task := a.tasks[it.IID]
	if task != nil && task.Status.Run.MergeRequestIID != 0 {
		// A run during the pause may have opened another MR.
		it.MergeRequestIID, it.MergeRequestURL = task.Status.Run.MergeRequestIID, task.Status.Run.MergeRequestURL
	}
	runBy, _ := pauseRunOf(a.ap, it.IID)
	resumeBy := func(by aiv1.PauseResolver, via aiv1.PauseResumeVia, why string) error {
		return p.end(ctx, resumption{by: by, via: via, who: fmt.Sprintf("#%d", it.IID), runBy: runBy}, why)
	}
	resume := func(via aiv1.PauseResumeVia, why string) error { return resumeBy(aiv1.ResolvedByOperator, via, why) }
	is, ok := a.issues[it.IID]
	if !ok || is.State != "opened" {
		mr, done, err := a.doneByHand(ctx, it.IID)
		if err != nil {
			return err
		}
		if done && mr != nil {
			return resume(aiv1.ResumedViaMergedByHand, fmt.Sprintf("!%d was merged", mr.IID))
		}
		if done {
			return resume(aiv1.ResumedViaClosedByHand, fmt.Sprintf("#%d was closed as done", it.IID))
		}
		return resume(aiv1.ResumedViaClosedByHand, fmt.Sprintf("#%d was closed", it.IID))
	}
	if it.MergeRequestIID != 0 {
		mr, err := a.gl.GetMergeRequest(ctx, a.spec.GitLab.Project, it.MergeRequestIID)
		switch {
		case err != nil && !gitlab.IsNotFound(err):
			// Tried again on the next poll; the tracking issue still updates.
			a.log.Info("could not check the paused issue's merge request", "mr", it.MergeRequestIID, "error", err.Error())
			return nil
		case err == nil && mr.State == "merged":
			if err := resume(aiv1.ResumedViaMergedByHand, fmt.Sprintf("!%d was merged", mr.IID)); err != nil {
				return err
			}
			return a.merged(ctx, it, mr)
		case err == nil && p.kind == aiv1.PauseMergeRequest && task != nil && mr.SHA != task.Status.Run.PushedSHA:
			// The gate would judge the head before the rebase: no run took
			// the new one over yet.
			return nil
		}
	}
	if p.kind == aiv1.PauseCompanion && task != nil {
		// The pause holds as long as the companion's merge request does.
		v, err := a.companionCheck(ctx, task)
		switch {
		case err != nil:
			a.log.Info("could not check the paused issue's companion", "issue", it.IID, "error", err.Error())
		case v.state == companionOK || v.state == companionRebased:
			return resume(aiv1.ResumedViaCauseGone, fmt.Sprintf("the companion of #%d can go on", it.IID))
		}
		return nil
	}
	if !p.resumeWhenReady {
		return nil
	}
	stalled := p.kind == aiv1.PauseStalled
	switch g := a.gateCheck(it, is, task); {
	case stalled && g.kind == aiv1.PauseStalled:
		return nil // still stalled: only the ages in the reason grew
	case stalled && (g.state == gateWaiting || g.state == gateCIUnfinished):
		// The run moves again or ended; the gate judges it when it is done.
		return resume(aiv1.ResumedViaCauseGone, fmt.Sprintf("the run of #%d is no longer stalled", it.IID))
	case g.state == gatePassed && a.overridden(it, task.Status.Run.PushedSHA, task.Status.Run):
		// The owner's merge override let the head through.
		return resumeBy(aiv1.ResolvedByLaterRun, aiv1.ResumedViaCommand, fmt.Sprintf("#%d is green and may merge without a full review", it.IID))
	case g.state == gatePassed:
		return resumeBy(aiv1.ResolvedByLaterRun, laterRunVia(a.ap, it.IID, task.Status.Run.PushedSHA), fmt.Sprintf("#%d is reviewed and green again", it.IID))
	case g.state == gateReReview:
		return a.reReview(ctx, it, task)
	case g.state == gateFailed && a.about(it, g.reason) != p.reason:
		// A later run fixed the old reason, but something else holds the
		// issue now: say so (and mention the owners), instead of keeping
		// the stale reason.
		a.log.Info("autopilot stays paused for another reason", "issue", it.IID, "was", p.reason, "now", g.reason)
		return a.failGate(ctx, it, is, task, g)
	}
	return nil
}
