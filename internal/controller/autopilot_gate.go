package controller

import (
	"cmp"
	"fmt"
	"strconv"

	"k8s.io/apimachinery/pkg/api/meta"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// gateState is where the active issue stands at the merge gate's run,
// review and CI checks (the MR itself is checked when the gate runs).
type gateState int

const (
	// gateWaiting: the agent or CI is still working on it.
	gateWaiting gateState = iota
	// gateCIUnfinished: in review, but the CI loop never finished the
	// pushed commit.
	gateCIUnfinished
	// gateFailed: it cannot be merged; reason says why.
	gateFailed
	// gateReReview: its reviewers failed transiently; reviewed again
	// after review.retryAfter.
	gateReReview
	// gatePassed: reviewed and green.
	gatePassed
)

type gateResult struct {
	state  gateState
	kind   aiv1.PauseKind
	reason string
	// byRun: a later run can fix it (pauseRun), else a human must
	// (pauseActive).
	byRun bool
	// block: the pause also blocks the issue, for no human's fault.
	block aiv1.BlockReason
	// stranded: the issue is still on the working label after a failed run
	// that is over (strandedRun); the caller retries or moves the label.
	stranded bool
}

// strandedRun reports whether task's last run failed and recorded its cause,
// and no run is pending, running or blocked, nor asked to be blocked: its
// issue should have left the working label.
func strandedRun(task *aiv1.AgentTask) bool {
	if task == nil {
		return false
	}
	run := task.Status.Run
	// A failure with a block reason leaves the labels to syncRunBlock on purpose.
	return !task.RunPending() && run.ActiveRunRequest <= run.ObservedRunRequest && task.Status.Blocked == nil && run.BlockReason == "" &&
		run.LastResult == aiv1.RunResultFailed && run.NeedsInputCause == aiv1.NeedsInputRunFailed
}

// gateCheck checks the active issue's run, review and CI against the merge
// gate. drive pauses on a failure; while paused, autoResume continues on a
// pass and announces a failure for another reason.
func (a *autopilot) gateCheck(it *aiv1.AutopilotItem, is gitlab.Issue, task *aiv1.AgentTask) gateResult {
	wf := a.spec.Workflow
	failed := func(byRun bool, kind aiv1.PauseKind, format string, args ...any) gateResult {
		return gateResult{state: gateFailed, kind: kind, reason: fmt.Sprintf(format, args...), byRun: byRun}
	}
	stalled := a.stalledPast(it.IID, task)
	switch {
	case is.HasLabel(wf.NeedsInputLabel) && task != nil && companionCIRed(task) != "":
		// The CI loop handed the red companion to a human after the fix
		// rounds: red CI, in the companion.
		return failed(true, aiv1.PauseCIRed, "%s", companionCIRed(task))
	case is.HasLabel(wf.NeedsInputLabel):
		if reason, ok := needsInputReason(it.IID, task); ok {
			return failed(true, aiv1.PauseNeedsInput, "%s", reason)
		}
		return failed(true, aiv1.PauseNeedsInput, "#%d needs input (see its last comments): CI is still red, the run failed or the agent asked a question", it.IID)
	case !hasAny(is, wf.ActiveLabels()...):
		return failed(false, aiv1.PauseConfig, "#%d is open but has no workflow label any more; move it back to `%s` or close it", it.IID, wf.ReviewLabel)
	case stalled != "":
		return failed(true, aiv1.PauseStalled, "%s", stalled)
	case is.HasLabel(wf.WorkingLabel) && strandedRun(task):
		// The failure report could not move the label (GitLab was down or
		// rate-limited): the caller retries the run or moves the label.
		reason, _ := needsInputReason(it.IID, task)
		g := failed(true, aiv1.PauseRunFailed, "%s", reason)
		g.stranded = true
		return g
	case !is.HasLabel(wf.ReviewLabel) || task == nil || task.RunPending() || task.Status.Blocked != nil:
		return gateResult{state: gateWaiting} // blocked: waits for an outside reason
	}
	run := task.Status.Run
	// An owner's merge override skips the review checks for its head.
	reviewed := !a.overridden(it, run.PushedSHA, run)
	switch {
	case run.LastResult != aiv1.RunResultSucceeded && run.NeedsInputCause == aiv1.NeedsInputRunFailed:
		// A failed run records its cause, also a blocked one a human lifted.
		reason, _ := needsInputReason(it.IID, task)
		return failed(true, aiv1.PauseRunFailed, "%s", reason)
	case run.LastResult != aiv1.RunResultSucceeded:
		return failed(true, aiv1.PauseRunFailed, "the last run of #%d ended with %q", it.IID, run.LastResult)
	case reviewed && run.BlockingFindings > 0:
		return failed(true, aiv1.PauseFindings, "%d blocking review findings remain on !%d", run.BlockingFindings, it.MergeRequestIID)
	case reviewed && run.ReviewErrors > 0:
		return a.reviewErrors(it, task)
	case it.MergeRequestIID == 0 || run.PushedSHA == "":
		return failed(false, aiv1.PauseConfig, "#%d is in review without a pushed merge request", it.IID)
	case len(a.spec.Agents.Review.Reviewers) == 0:
		return failed(false, aiv1.PauseConfig, "no reviewers are configured (`agents.review.reviewers`), and autopilot only merges reviewed changes")
	case reviewed && run.ReviewedSHA != run.PushedSHA:
		return failed(true, aiv1.PauseNotReviewed, "the head of !%d (%s) was not reviewed (the last review saw %s)", it.MergeRequestIID, short(run.PushedSHA), short(run.ReviewedSHA))
	case reviewed && run.CompanionUnreviewed():
		// A companion-only push (a run without reviewers) after the review.
		c := run.Companion
		return failed(true, aiv1.PauseNotReviewed, "the companion head of #%d (%s, %s) was not reviewed (the last review saw %s)",
			it.IID, c.Project, short(c.PushedSHA), cmp.Or(short(c.ReviewedSHA), "none"))
	}
	if a.spec.CI.Disabled {
		return gateResult{state: gatePassed}
	}
	switch ci := task.Status.CI; {
	case ci == nil || ci.SHA != run.PushedSHA || !ci.State.Final():
		return gateResult{state: gateCIUnfinished}
	case ci.State == aiv1.CIStateCanceled:
		return failed(true, aiv1.PauseCICanceled, "the pipeline of !%d was canceled", it.MergeRequestIID)
	case ci.State == aiv1.CIStateBaseFailing:
		return failed(true, aiv1.PauseCIRed, "CI of !%d is red with jobs that also fail on the default branch (%s)", it.MergeRequestIID, jobList(ci.FailedJobs))
	case ci.State == aiv1.CIStateFailed:
		return failed(true, aiv1.PauseCIRed, "CI of !%d is red (%s)", it.MergeRequestIID, jobList(ci.FailedJobs))
	}
	// The companion must be green on its pushed head too. No pipeline (None)
	// passes like at home: its landing checks it again before its merge.
	if c := run.Companion; c != nil && c.PushedSHA != "" {
		ref := companionRef(c)
		switch cc := task.Status.CI.Companion; {
		case cc == nil || cc.SHA != c.PushedSHA || !cc.State.Final():
			return gateResult{state: gateCIUnfinished}
		case cc.State == aiv1.CIStateCanceled:
			return failed(true, aiv1.PauseCICanceled, "the pipeline of the companion %s was canceled", ref)
		case cc.State == aiv1.CIStateFailed:
			return failed(true, aiv1.PauseCIRed, "CI of the companion %s is red (%s)", ref, jobList(cc.FailedJobs))
		}
	}
	return gateResult{state: gatePassed}
}

// stalledPast is why issue iid's stalled run pauses the autopilot
// (aiv1.PauseStalled), "" while it is not stalled or may still be restarted:
// the pilot's interventions on the issue's head are used up (no pilot
// restarts it at all), or the run was interrupted MaxInterruptions times.
func (a *autopilot) stalledPast(iid int64, task *aiv1.AgentTask) string {
	if task == nil || !meta.IsStatusConditionTrue(task.Status.Conditions, aiv1.ConditionStalled) {
		return ""
	}
	run, limit, used := task.Status.Run, a.spec.Pilot.MaxInterventionsPerIssue, int32(0)
	if a.pilotTask == nil {
		used = limit
	} else if ps := a.pilotTask.Status.Pilot; ps != nil {
		if n := ps.Interventions[strconv.FormatInt(iid, 10)]; n.Head == run.PushedSHA {
			used = n.Count
		}
	}
	if used < limit && run.Interruptions < aiv1.MaxInterruptions {
		return ""
	}
	msg := meta.FindStatusCondition(task.Status.Conditions, aiv1.ConditionStalled).Message
	reason := fmt.Sprintf("#%d's run is stalled again after %d of %d pilot interventions: %s", iid, used, limit, msg)
	if a.pilotTask == nil {
		reason = fmt.Sprintf("#%d's run is stalled and no pilot restarts it: %s", iid, msg)
	}
	if run.CurrentStep != "" && run.CurrentStepSince != nil {
		reason += fmt.Sprintf("; step %s runs for %s", run.CurrentStep, minutes(a.r.now().Sub(run.CurrentStepSince.Time)))
	}
	return reason + fmt.Sprintf(" (interruption %d of %d)", run.Interruptions, aiv1.MaxInterruptions)
}

// companionRef names a companion merge request, "group/infra!3", or its
// project without one.
func companionRef(c *aiv1.CompanionRun) string {
	if c.MRIID == 0 {
		return c.Project
	}
	return fmt.Sprintf("%s!%d", c.Project, c.MRIID)
}

// companionCIRed is the reason a companion pipeline that stayed red after
// the fix rounds holds the issue, "" if it did not.
func companionCIRed(task *aiv1.AgentTask) string {
	c, ci := task.Status.Run.Companion, task.Status.CI
	if c == nil || c.PushedSHA == "" || ci == nil || ci.SHA != task.Status.Run.PushedSHA || ci.State == aiv1.CIStateFailed ||
		ci.Companion == nil || ci.Companion.SHA != c.PushedSHA || ci.Companion.State != aiv1.CIStateFailed {
		return ""
	}
	return fmt.Sprintf("CI of the companion %s is still red after %d fix rounds (%s)", companionRef(c), task.Status.Run.CIRound, jobList(ci.Companion.FailedJobs))
}
