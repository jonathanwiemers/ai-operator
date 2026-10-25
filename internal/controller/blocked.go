package controller

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// block records that the task's issue cannot go on for an outside reason.
// The labels follow on the poll (syncBlocked), so a failed label call heals.
// Callers block only when no run is pending (a deferred one waits) and the
// CI loop is final.
func (r *AgentProjectReconciler) block(ctx context.Context, wf aiv1.WorkflowSpec, is gitlab.Issue, task *aiv1.AgentTask, reason aiv1.BlockReason, message string) error {
	b := task.Status.Blocked
	if b != nil && b.Applied && !is.HasLabel(wf.BlockedLabel) {
		b = nil // lifted by a human; syncBlocked drops it
	}
	if b != nil && b.Reason == reason {
		return nil
	}
	restore := ""
	for _, l := range []string{wf.ReadyLabel, wf.WorkingLabel, wf.NeedsInputLabel, wf.ReviewLabel} {
		if is.HasLabel(l) {
			restore = l
		}
	}
	applied := false
	if b != nil {
		// A new reason keeps the label to come back to; syncBlocked swaps
		// the reason label.
		restore, applied = b.RestoreLabel, b.Applied
	}
	return r.patchBlocked(ctx, task, &aiv1.BlockedStatus{Reason: reason, Message: message, Since: metav1.NewTime(r.now()), RestoreLabel: restore, Applied: applied})
}

// unblock lifts the block and puts the label it replaced back.
func (r *AgentProjectReconciler) unblock(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, is gitlab.Issue, task *aiv1.AgentTask) error {
	b := task.Status.Blocked
	if b == nil {
		return nil
	}
	if b.Applied && is.HasLabel(spec.Workflow.BlockedLabel) {
		opts := gitlab.UpdateIssueOptions{RemoveLabels: []string{spec.Workflow.BlockedLabel, spec.Workflow.BlockedReasonPrefix + string(b.Reason)}}
		if b.RestoreLabel != "" {
			opts.AddLabels = []string{b.RestoreLabel}
		}
		if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, is.IID, opts); err != nil {
			return fmt.Errorf("unblocking issue #%d: %w", is.IID, err)
		}
	}
	return r.patchBlocked(ctx, task, nil)
}

// syncRunBlock blocks the issue for the runner's report (run.blockReason):
// a run deferred by a usage limit, or one that gave up for an outside reason.
// Each report blocks once. A new run (running, or pending and not deferred)
// lifts any block, also when the label move of its request failed; a quota
// block is also lifted when the run ended otherwise. An infra block gets one
// itself after lifecycle.infraRetryAfter, once per run request.
func (r *AgentProjectReconciler) syncRunBlock(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, is gitlab.Issue, task *aiv1.AgentTask) error {
	run := task.Status.Run
	reason := run.BlockReason
	newRun := run.ActiveRunRequest > run.ObservedRunRequest || task.RunPending() && run.DeferredUntil == nil
	if newRun {
		reason = ""
	}
	switch b := task.Status.Blocked; {
	case b != nil && newRun && b.Applied && !is.HasLabel(spec.Workflow.BlockedLabel):
		return nil // lifted by a human: syncBlocked drops it
	case b != nil && newRun:
		b.RestoreLabel = spec.Workflow.WorkingLabel // what a run request shows
		return r.unblock(ctx, gl, spec, is, task)
	case b != nil && b.Reason == aiv1.BlockQuota && reason != aiv1.BlockQuota:
		return r.unblock(ctx, gl, spec, is, task)
	case reason == "" || run.LastActivityTime == nil:
		return nil
	case run.LastActivityTime.Equal(task.Status.RunBlockedAt):
		if b == nil || b.Reason != aiv1.BlockInfra || reason != aiv1.BlockInfra || task.RunPending() ||
			task.Spec.Source.InfraRetry == run.ObservedRunRequest || r.now().Before(run.LastActivityTime.Add(spec.Lifecycle.InfraRetryAfter.Duration)) {
			return nil
		}
		log.FromContext(ctx).Info("retrying a run that failed for an outside reason", "issue", is.IID)
		task.Spec.Source.InfraRetry = task.Spec.RunRequest + 1
		if err := r.requestRun(ctx, gl, spec, task, is.IID); err != nil {
			return err
		}
		return r.unblock(ctx, gl, spec, is, task)
	}
	if err := r.block(ctx, spec.Workflow, is, task, reason, run.Summary); err != nil {
		return err
	}
	orig := task.DeepCopy()
	task.Status.RunBlockedAt = run.LastActivityTime
	return client.IgnoreNotFound(r.Status().Patch(ctx, task, client.MergeFrom(orig)))
}

// syncBlocked puts the blocked labels on the issue of a blocked task. A block
// a human lifted is dropped: an applied one whose issue lost the blocked
// label, or one not applied yet whose issue moved on from the label it
// would replace or got a run (moved on the board, or `/agent …`); a deferred
// run is pending too, but waits.
func (r *AgentProjectReconciler) syncBlocked(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, is gitlab.Issue, task *aiv1.AgentTask) error {
	b := task.Status.Blocked
	if b == nil {
		return nil
	}
	wf := spec.Workflow
	reasonLabel := wf.BlockedReasonPrefix + string(b.Reason)
	switch {
	case b.Applied && !is.HasLabel(wf.BlockedLabel),
		!b.Applied && (task.RunPending() && b.Reason != aiv1.BlockQuota || b.RestoreLabel != "" && !is.HasLabel(b.RestoreLabel)):
		if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, is.IID, gitlab.UpdateIssueOptions{RemoveLabels: []string{reasonLabel}}); err != nil {
			return fmt.Errorf("removing the block reason of issue #%d: %w", is.IID, err)
		}
		return r.patchBlocked(ctx, task, nil)
	case b.Applied && is.HasLabel(reasonLabel):
		return nil
	}
	// Not applied yet, or applied with a new reason (or a reason label
	// removed by hand): the blocked label stays or comes back.
	remove := []string{wf.ReadyLabel, wf.WorkingLabel, wf.NeedsInputLabel, wf.ReviewLabel}
	for _, reason := range []aiv1.BlockReason{aiv1.BlockInfra, aiv1.BlockMainRed, aiv1.BlockQuota, aiv1.BlockDependency} {
		if reason != b.Reason { // the reason of an earlier block
			remove = append(remove, wf.BlockedReasonPrefix+string(reason))
		}
	}
	if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, is.IID, gitlab.UpdateIssueOptions{
		AddLabels: []string{wf.BlockedLabel, reasonLabel}, RemoveLabels: remove,
	}); err != nil {
		return fmt.Errorf("blocking issue #%d: %w", is.IID, err)
	}
	applied := *b
	applied.Applied = true
	return r.patchBlocked(ctx, task, &applied)
}

func (r *AgentProjectReconciler) patchBlocked(ctx context.Context, task *aiv1.AgentTask, b *aiv1.BlockedStatus) error {
	orig := task.DeepCopy()
	task.Status.Blocked = b
	return client.IgnoreNotFound(r.Status().Patch(ctx, task, client.MergeFrom(orig)))
}
