package runner

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

// Reasons a checkpoint is saved for (subject, trailer, status).
const (
	cutUsageLimit  = "usage-limit"
	cutEngineError = "engine-error"
	cutTimeout     = "timeout"
	cutBudgetStop  = "budget-stop"
	// cutContainerRestart: the agent container restarted during the call.
	cutContainerRestart = "container-restart"
	// cutPodLost and cutOOMKill are saved by the next pod (lostAttempt).
	cutPodLost = "pod-lost"
	cutOOMKill = "oom-kill"
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
	// run is the run whose work it saves when that is not the current one
	// (a lost attempt); expect, if set, is the commit HEAD must be or
	// descend from.
	run    int64
	expect string
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
	return r.cut != nil && implementerKind(inv.Kind)
}

// implementerKind reports whether calls of kind are the implementer's:
// they edit the work tree for a step commit.
func implementerKind(kind string) bool {
	switch kind {
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
// error that cut the call off. It returns nil when nothing was attempted,
// and the checkpoint commit on the branch: "" unless it was pushed.
func (r *Runner) checkpoint(ctx context.Context, reason string, res *Result) (*CheckpointResult, string) {
	cp, pushed := r.saveCheckpoint(ctx, reason, res)
	if cp == nil {
		return nil, ""
	}
	noted := *cp
	if !pushed {
		noted.SHA = "" // the reports only claim what is on the branch
	}
	r.run.noteCheckpoint(checkpointNote{Reason: reason, CheckpointResult: noted})
	return cp, noted.SHA
}

func (r *Runner) saveCheckpoint(ctx context.Context, reason string, res *Result) (*CheckpointResult, bool) {
	cut := r.cut
	if cut == nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), checkpointTimeout)
	defer cancel()
	s := r.step(ctx, "save checkpoint", "reason", reason, "step", cut.step)
	runNo := r.runNumber
	if cut.run != 0 {
		runNo = cut.run
	}
	req := CheckpointRequest{Reason: reason, Issue: cut.issue, Run: runNo, Step: cut.step, Branch: cut.branch, Base: cut.base, Expect: cut.expect}
	if n := r.projectSpec.Checkpoint.MaxFileBytes; n != nil {
		req.MaxFileBytes = *n
	}
	if res != nil {
		req.Status = res.Text
	}
	cp, err := r.Agent.Checkpoint(ctx, req)
	if err != nil {
		s.done("failed: "+err.Error(), "error", err.Error())
		return &cp, false
	}
	skipped := skippedNote(cp.Skipped)
	switch {
	case cp.Refused != "":
		s.done("nothing saved: "+cp.Refused, "refused", cp.Refused)
		return &cp, false
	case cp.Aborted != "":
		s.done(fmt.Sprintf("nothing saved: the %s in progress was aborted", cp.Aborted), "aborted", cp.Aborted)
		return &cp, false
	case cp.SHA == "":
		s.done("nothing to save, the work tree is clean"+skipped, "skipped", len(cp.Skipped))
		return &cp, false
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
		return &cp, false
	}
	r.checkpointHead = r.pushedHead
	paths := make([]string, len(cp.Skipped))
	for i, f := range cp.Skipped {
		paths[i] = f.Path
	}
	if err := r.patchRun(ctx, func(st *aiv1.RunStatus) {
		st.Checkpoint = &aiv1.CheckpointStatus{SHA: r.checkpointHead, Branch: cut.branch, Reason: reason, Run: runNo, Step: int32(cut.step),
			Files: int32(cp.Files), Added: cp.Added, Deleted: cp.Deleted, Skipped: paths, At: metav1.Time{Time: r.now()}}
	}); err != nil {
		r.log().Error(err, "recording the checkpoint")
	}
	s.done(fmt.Sprintf("%s pushed without CI: %d files, +%d/−%d", shortSHA(r.checkpointHead), cp.Files, cp.Added, cp.Deleted)+skipped,
		"sha", r.checkpointHead, "files", cp.Files, "added", cp.Added, "deleted", cp.Deleted, "skipped", paths)
	return &cp, true
}

// noteFold remembers the checkpoint a step's commit folded (CommitStep), for
// the next publish.
func (r *Runner) noteFold(res CommitResult) {
	if res.FoldedFrom != "" {
		r.foldLease, r.foldCommit = res.FoldedFrom, res.SHA
	}
}

// publishFold pushes head, the step's commit that folded the checkpoints up
// to r.foldLease, over that checkpoint with --force-with-lease, the only
// forced push there is. If someone pushed to the branch since, or the
// mirror refuses the fold, the fold is undone: the step's commit goes on top
// of the checkpoint, which is kept, and is published normally. Any other
// failure leaves the fold for the next publish to try again.
func (r *Runner) publishFold(ctx context.Context, branch, base, head string, skipCI bool) (bool, error) {
	lease, fold := r.foldLease, r.foldCommit
	s := r.step(ctx, "fold checkpoints", "branch", branch, "checkpoint", lease)
	err := r.Mirror.PushWithLease(ctx, branch, lease, skipCI)
	var refused *gitlab.GuardError
	switch {
	case err == nil:
		r.foldLease, r.foldCommit = "", ""
		r.pushedHead, r.checkpointHead = head, ""
		if err := r.patchRun(ctx, func(st *aiv1.RunStatus) { st.Checkpoint = nil }); err != nil {
			r.log().Error(err, "clearing the folded checkpoint")
		}
		s.done(fmt.Sprintf("%s replaces the checkpoint %s (--force-with-lease)", shortSHA(head), shortSHA(lease)), "sha", head)
		return true, nil
	case !errors.Is(err, ErrLeaseRejected) && !errors.As(err, &refused):
		return false, s.fail(err)
	}
	r.foldLease, r.foldCommit = "", ""
	why := "someone pushed to the branch"
	if refused != nil {
		why = "the fold was refused (" + refused.Reason + ")"
	}
	if _, uerr := r.Agent.Unfold(ctx, UnfoldRequest{Tip: lease, Fold: fold}); uerr != nil {
		return false, s.fail(fmt.Errorf("%s, and undoing the fold failed: %w", why, uerr))
	}
	r.run.noteCheckpoint(checkpointNote{Kept: fmt.Sprintf("The checkpoint `%s` was kept: %s, so it was not folded into the step's commit.",
		shortSHA(lease), notify.Defuse(why))})
	s.done("checkpoint kept: "+why, "error", err.Error())
	return r.publish(ctx, branch, base, skipCI)
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

// markInFlight records an implementer call that is about to start in the
// state file, so that a new pod after a lost one can save its work
// (checkpointLostAttempt). Other calls change nothing.
func (r *Runner) markInFlight(inv Invocation) {
	if r.cutsOff(inv) && r.cut.st != nil {
		r.setInFlight(r.cut.st, &inFlight{Run: r.runNumber, Step: r.cut.step, Branch: r.cut.branch, Kind: inv.Kind})
	}
}

// setInFlight sets the in-flight call (nil: none) in the run's state st and
// in the state file, which it writes only when the value changes. The file
// is read again, so nothing else of st is saved before its time.
func (r *Runner) setInFlight(st *state, v *inFlight) {
	if old := st.InFlight; (old == nil) == (v == nil) && (v == nil || *old == *v) {
		return
	}
	st.InFlight = v
	disk, err := r.loadState()
	if err == nil {
		disk.InFlight = v
		err = r.saveState(disk)
	}
	if err != nil {
		r.log().Error(err, "recording the implementer call in flight")
	}
}

// endCut ends the implementer part of the run: no more checkpoints, and the
// in-flight marker goes. It stays when the run ends because the agent
// container was lost or the pod is stopping: the next attempt saves what
// the call left in the workspace.
func (r *Runner) endCut(ctx context.Context, err error) {
	cut := r.cut
	r.cut = nil
	var lost *AgentLostError
	var conn *agentConnError
	if cut == nil || cut.st == nil || errors.As(err, &lost) || errors.As(err, &conn) || ctx.Err() != nil {
		return
	}
	r.setInFlight(cut.st, nil)
}

// checkpointLostAttempt runs between the mirror's sync and Prepare, which
// resets the workspace. In a run started again after a lost pod or an OOM
// kill, with checkpoints on, it saves what the lost implementer call left in
// the workspace as a checkpoint (pod-lost, oom-kill), and pushes step commits
// the lost pod made but did not push. The workspace refuses when it is on
// another branch or its HEAD does not descend from the pushed head (someone
// pushed since): then nothing is saved and the run goes on as before.
func (run *implementRun) checkpointLostAttempt(ctx context.Context) {
	r, branch := run.r, run.branch
	st, err := r.loadState()
	if err != nil || st.InFlight == nil {
		return // readComments fails on a state it cannot read
	}
	// Prepare resets the workspace next: the marker is used up either way.
	lost := *st.InFlight
	st.InFlight = nil
	if err := r.saveState(st); err != nil {
		r.log().Error(err, "clearing the implementer call in flight")
	}
	mrIID := run.task.Status.Run.MergeRequestIID
	expect, onRemote := r.Mirror.resolve(ctx, "refs/remotes/origin/"+branch)
	if r.lostAttempt == "" || run.spec.Checkpoint.Disabled || lost.Branch != branch || mrIID == 0 || !onRemote {
		return
	}
	r.cut = &cutTarget{gl: run.gl, glProject: run.glProject, branch: branch, base: run.base, issue: run.issue.IID,
		mr: &gitlab.MergeRequest{IID: mrIID}, st: &st, step: lost.Step, run: lost.Run, expect: expect}
	cp, _ := r.checkpoint(ctx, r.lostAttempt, nil)
	r.cut = nil
	if cp == nil || cp.Refused != "" || cp.SHA != "" {
		return
	}
	// A clean tree: Prepare would still drop step commits that were not pushed.
	pushed, err := r.publish(ctx, branch, run.base, true)
	switch {
	case err != nil:
		r.step(ctx, "push unpushed steps", "branch", branch).done("failed: "+err.Error(), "error", err.Error())
	case pushed && r.pushedHead == expect:
		r.pushedHead = "" // the bundle held only commits origin has
	case pushed:
		r.step(ctx, "push unpushed steps", "branch", branch).done(fmt.Sprintf("%s pushed without CI", shortSHA(r.pushedHead)), "sha", r.pushedHead)
	}
}

// checkpointNote is a checkpoint of this run and why it was made. SHA is
// empty when the commit is not on the branch.
type checkpointNote struct {
	Reason string
	CheckpointResult
	// Kept, if set, is the whole note: a checkpoint that was not folded.
	Kept string
}

func (rl *runLog) noteCheckpoint(n checkpointNote) {
	if rl == nil {
		return
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.checkpoints = append(rl.checkpoints, n)
}

// checkpointText is the report text for the run's checkpoints ("" if none).
func (rl *runLog) checkpointText() string {
	if rl == nil {
		return ""
	}
	rl.mu.Lock()
	notes := slices.Clone(rl.checkpoints)
	rl.mu.Unlock()
	return strings.Join(checkpointLines(notes), "\n\n")
}

// keptNotices are the run's notes about checkpoints that were kept instead
// of folded, for the report of a run that ends normally.
func (rl *runLog) keptNotices() []string {
	if rl == nil {
		return nil
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	var kept []string
	for _, n := range rl.checkpoints {
		if n.Kept != "" {
			kept = append(kept, n.Kept)
		}
	}
	return kept
}

// cutWords says why a checkpoint was saved.
var cutWords = map[string]string{
	cutUsageLimit:       "the usage limit was reached",
	cutEngineError:      "the agent call failed",
	cutTimeout:          "the agent call timed out",
	cutBudgetStop:       "the budget is used",
	cutContainerRestart: "the agent container restarted",
	cutPodLost:          "the pod was lost",
	cutOOMKill:          "the agent container ran out of memory",
}

// checkpointLines renders what the checkpoints of a run did, for the run
// report and the progress comment. Text from the agent and the workspace goes
// through notify.Defuse.
func checkpointLines(notes []checkpointNote) []string {
	var lines []string
	for _, n := range notes {
		if n.Kept != "" {
			lines = append(lines, n.Kept)
			continue
		}
		if n.SHA != "" {
			why := cutWords[n.Reason]
			if why == "" {
				why = notify.Defuse(n.Reason)
			}
			lines = append(lines, fmt.Sprintf("💾 Saved a checkpoint (`%s`, %d files, +%d/−%d) because %s.",
				shortSHA(n.SHA), n.Files, n.Added, n.Deleted, why))
		}
		if len(n.Skipped) > 0 {
			parts := make([]string, len(n.Skipped))
			for i, f := range n.Skipped {
				parts[i] = fmt.Sprintf("%s (%s)", codeList([]string{f.Path}), f.Why)
			}
			lines = append(lines, notify.Defuse("Left out of the checkpoint (still in the workspace): "+strings.Join(parts, ", ")+"."))
		}
		if n.Aborted != "" {
			lines = append(lines, fmt.Sprintf("A %s was in progress; it was aborted and nothing was saved, half-resolved conflicts are never committed.", notify.Defuse(n.Aborted)))
		}
		if n.Refused != "" {
			lines = append(lines, "No checkpoint: "+notify.Defuse(n.Refused)+".")
		}
	}
	return lines
}
