package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// Reasons a checkpoint is saved for (subject, trailer, status).
const (
	cutUsageLimit  = "usage-limit"
	cutEngineError = "engine-error"
	cutTimeout     = "timeout"
	cutBudgetStop  = "budget-stop"
)

// checkpointTimeout bounds a checkpoint, which also runs when the pod stops.
const checkpointTimeout = 60 * time.Second

// cutTarget is where the work of an implementer call that is cut off goes:
// set by implementRun for the implementer part of a run (Runner.cut).
type cutTarget struct {
	gl           gitlab.API
	glProject    string
	branch, base string
	issue        int64
	mr           *gitlab.MergeRequest
	st           *state
	// step counts the run's implementer calls, from 1.
	step int
	// skipLimit is set for fix rounds, which keep their work on a usage
	// limit by committing and publishing it (reviewLoop).
	skipLimit bool
}

// cutStep counts the implementer call that starts next; fix rounds skip
// the checkpoint on a usage limit.
func (r *Runner) cutStep(fix bool) {
	if r.cut != nil {
		r.cut.step++
		r.cut.skipLimit = fix
	}
}

// cutReason says why the implementer call that ended with err is cut off,
// or "" when no checkpoint is due: no cut target, not an implementer call,
// no error, or a resume that invokeOn tries again with a fresh session.
func (r *Runner) cutReason(ctx context.Context, inv Invocation, res *Result, err error) string {
	if err == nil || !r.cutsOff(inv) || retriedFresh(inv, res, err) {
		return ""
	}
	if _, ok := AsUsageLimit(err); ok {
		if r.cut.skipLimit {
			return ""
		}
		return cutUsageLimit
	}
	if isTimeout(ctx, err) {
		return cutTimeout
	}
	return cutEngineError
}

// cutsOff reports whether inv is an implementer call whose cut-off leaves a
// checkpoint: one of the run's implementer part, with checkpoints on.
func (r *Runner) cutsOff(inv Invocation) bool {
	if r.cut == nil {
		return false
	}
	switch inv.Kind {
	case aiv1.CallKindImplement, aiv1.CallKindFix, aiv1.CallKindCI, aiv1.CallKindConflict:
		return true
	}
	return false
}

// retriedFresh reports whether invokeOn calls again with a fresh session
// after the call that resumed inv.Resume ended with err.
func retriedFresh(inv Invocation, res *Result, err error) bool {
	var lost *AgentLostError
	_, limited := AsUsageLimit(err)
	return inv.Resume != "" && err != nil && !limited && !errors.As(err, &lost) && (res == nil || res.SessionID == "")
}

// isTimeout reports whether err is a call that ran out of time or was
// cancelled (e.g. the pod stopping). Errors from the agent container come
// as text.
func isTimeout(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "context canceled") || strings.Contains(msg, "timed out")
}

// checkpoint saves the work tree of the cut-off implementer call as a
// checkpoint commit on the work branch and pushes it without CI, the MR set
// to draft first. res is the call's result, if any: its text is the agent's
// last status. Failures are logged in the run log and never replace the
// error that cut the call off. It returns nil when nothing was attempted.
func (r *Runner) checkpoint(ctx context.Context, reason string, res *Result) *CheckpointResult {
	cut := r.cut
	if cut == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), checkpointTimeout)
	defer cancel()
	s := r.step(ctx, "save checkpoint", "reason", reason, "step", cut.step)
	req := CheckpointRequest{Reason: reason, Issue: cut.issue, Run: r.runNumber, Step: cut.step, Branch: cut.branch, Base: cut.base}
	if n := r.projectSpec.Checkpoint.MaxFileBytes; n != nil {
		req.MaxFileBytes = *n
	}
	if res != nil {
		req.Status = res.Text
	}
	cp, err := r.Agent.Checkpoint(ctx, req)
	if err != nil {
		s.done("failed: "+err.Error(), "error", err.Error())
		return &cp
	}
	skipped := skippedNote(cp.Skipped)
	switch {
	case cp.Refused != "":
		s.done("nothing saved: "+cp.Refused, "refused", cp.Refused)
		return &cp
	case cp.Aborted != "":
		s.done(fmt.Sprintf("nothing saved: the %s in progress was aborted", cp.Aborted), "aborted", cp.Aborted)
		return &cp
	case cp.SHA == "":
		s.done("nothing to save, the work tree is clean"+skipped, "skipped", len(cp.Skipped))
		return &cp
	}
	// A checkpoint is never ready: draft before the push.
	if cur, gerr := cut.gl.GetMergeRequest(ctx, cut.glProject, cut.mr.IID); gerr != nil || !cur.Draft {
		r.setDraft(ctx, cut.gl, cut.glProject, cut.mr, cut.st, true)
	}
	pushed, err := r.publish(ctx, cut.branch, cut.base, true)
	if err != nil || !pushed {
		why := "nothing new to push"
		if err != nil {
			why = err.Error()
		}
		s.done(fmt.Sprintf("committed %s, not pushed: %s", shortSHA(cp.SHA), why)+skipped, "sha", cp.SHA, "error", why)
		return &cp
	}
	r.checkpointHead = r.pushedHead
	paths := make([]string, len(cp.Skipped))
	for i, f := range cp.Skipped {
		paths[i] = f.Path
	}
	if err := r.patchRun(ctx, func(st *aiv1.RunStatus) {
		st.Checkpoint = &aiv1.CheckpointStatus{SHA: r.checkpointHead, Branch: cut.branch, Reason: reason, Run: r.runNumber, Step: int32(cut.step),
			Files: int32(cp.Files), Added: cp.Added, Deleted: cp.Deleted, Skipped: paths, At: metav1.Time{Time: r.now()}}
	}); err != nil {
		r.log().Error(err, "recording the checkpoint")
	}
	s.done(fmt.Sprintf("%s pushed without CI: %d files, +%d/−%d", shortSHA(r.checkpointHead), cp.Files, cp.Added, cp.Deleted)+skipped,
		"sha", r.checkpointHead, "files", cp.Files, "added", cp.Added, "deleted", cp.Deleted, "skipped", paths)
	return &cp
}

// skippedNote names the files a checkpoint left out, for the run log.
func skippedNote(skipped []CheckpointSkip) string {
	if len(skipped) == 0 {
		return ""
	}
	parts := make([]string, len(skipped))
	for i, f := range skipped {
		parts[i] = fmt.Sprintf("%s (%s)", f.Path, f.Why)
	}
	return "; left out: " + strings.Join(parts, ", ")
}

// stepHead is the head a step of this run pushed, unless it is a checkpoint.
func (r *Runner) stepHead() string {
	if r.pushedHead == r.checkpointHead {
		return ""
	}
	return r.pushedHead
}
