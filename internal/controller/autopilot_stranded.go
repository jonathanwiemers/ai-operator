package controller

import (
	"context"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// stranded handles an active issue still on the working label after its run
// failed and the failure report could not move the label (gateResult.stranded).
// It pauses runFailed and moves the issue to needs-input. A run that failed on
// GitLab (failureKind gitlab) is never stranded: the runner asks for an infra
// block, and syncRunBlock blocks it and raises its one retry.
func (a *autopilot) stranded(ctx context.Context, it *aiv1.AutopilotItem, is gitlab.Issue, task *aiv1.AgentTask, g gateResult) error {
	if err := a.failGate(ctx, it, is, task, g); err != nil {
		return err
	}
	a.moveStranded(ctx, it.IID)
	return nil
}

// moveStranded moves a stranded issue from working to needs-input, the label
// move its failure report lost. A failed move is only logged: the issue stays
// stranded and the next poll tries again.
func (a *autopilot) moveStranded(ctx context.Context, iid int64) {
	wf := a.spec.Workflow
	if err := gitlab.RetryTransient(ctx, func() error {
		_, err := a.gl.UpdateIssue(ctx, a.spec.GitLab.Project, iid, gitlab.UpdateIssueOptions{
			AddLabels: []string{wf.NeedsInputLabel}, RemoveLabels: []string{wf.WorkingLabel},
		})
		return err
	}); err != nil {
		a.log.Error(err, "moving a stranded issue to needs-input", "issue", iid)
	}
}
