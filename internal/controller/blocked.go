package controller

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// block records that the task's issue cannot go on for an outside reason.
// The labels follow on the poll (syncBlocked), so a failed label call heals.
// Callers block only when no run is pending and the CI loop is final.
func (r *AgentProjectReconciler) block(ctx context.Context, wf aiv1.WorkflowSpec, is gitlab.Issue, task *aiv1.AgentTask, reason aiv1.BlockReason, message string) error {
	if b := task.Status.Blocked; b != nil && b.Reason == reason {
		return nil
	}
	restore := ""
	for _, l := range []string{wf.ReadyLabel, wf.WorkingLabel, wf.NeedsInputLabel, wf.ReviewLabel} {
		if is.HasLabel(l) {
			restore = l
		}
	}
	if b := task.Status.Blocked; b != nil {
		restore = b.RestoreLabel // a new reason keeps the label to come back to
	}
	return r.patchBlocked(ctx, task, &aiv1.BlockedStatus{Reason: reason, Message: message, Since: metav1.NewTime(r.now()), RestoreLabel: restore})
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

// syncBlocked puts the blocked labels on the issue of a blocked task. An
// applied block whose issue lost the blocked label was lifted by a human
// (moved on the board, or `/agent …`): the task follows.
func (r *AgentProjectReconciler) syncBlocked(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, is gitlab.Issue, task *aiv1.AgentTask) error {
	b := task.Status.Blocked
	wf := spec.Workflow
	switch {
	case b == nil:
		return nil
	case b.Applied && !is.HasLabel(wf.BlockedLabel):
		if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, is.IID, gitlab.UpdateIssueOptions{RemoveLabels: []string{wf.BlockedReasonPrefix + string(b.Reason)}}); err != nil {
			return fmt.Errorf("removing the block reason of issue #%d: %w", is.IID, err)
		}
		return r.patchBlocked(ctx, task, nil)
	case b.Applied:
		return nil
	}
	remove := []string{wf.ReadyLabel, wf.WorkingLabel, wf.NeedsInputLabel, wf.ReviewLabel}
	for _, reason := range []aiv1.BlockReason{aiv1.BlockInfra, aiv1.BlockMainRed, aiv1.BlockQuota, aiv1.BlockDependency} {
		if reason != b.Reason { // the reason of an earlier block
			remove = append(remove, wf.BlockedReasonPrefix+string(reason))
		}
	}
	if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, is.IID, gitlab.UpdateIssueOptions{
		AddLabels: []string{wf.BlockedLabel, wf.BlockedReasonPrefix + string(b.Reason)}, RemoveLabels: remove,
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
