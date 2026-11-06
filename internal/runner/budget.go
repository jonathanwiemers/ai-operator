package runner

import (
	"context"
	"errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// BudgetStopError means the operator stopped the task for its budget
// (spec.budgetStop) before the next agent call. The run ends deferred, like
// after a usage limit, and waits until the flag is cleared.
type BudgetStopError struct{}

func (*BudgetStopError) Error() string {
	return "stopped between agent calls: the budget is used; the run continues once it is raised"
}

// isBudgetStop reports whether err is a BudgetStopError.
func isBudgetStop(err error) bool {
	var stop *BudgetStopError
	return errors.As(err, &stop)
}

// budgetStop reads the task again before an agent call (the operator may
// have set spec.budgetStop since the run started). A failed read does not
// stop the call.
func (r *Runner) budgetStop(ctx context.Context) error {
	var task aiv1.AgentTask
	if err := r.K8s.Get(ctx, types.NamespacedName{Namespace: r.Namespace, Name: r.TaskName}, &task); err != nil {
		r.log().Info("could not read the task for its budget stop", "error", err.Error())
		return nil
	}
	if task.Spec.BudgetStop {
		return &BudgetStopError{}
	}
	return nil
}

// deferForBudget ends a run stopped by the budget: the request stays
// pending (Step waits while spec.budgetStop is set), what the run pushed
// stays, the workspace and session resume with the next attempt.
func (r *Runner) deferForBudget(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, task *aiv1.AgentTask, out *Outcome, err error) error {
	r.log().Info("budget used, deferring run", "steps", r.run.lines())
	now := r.now()
	// A note only when a checkpoint was saved: it is a new commit on the MR.
	if saved := r.run.checkpointText(); saved != "" && task.Spec.Type == aiv1.TaskTypeImplement && task.Spec.Issue != nil {
		body := "⏸️ Stopped between agent calls: the budget is used. The run continues once it is raised.\n\n" + saved
		if _, nerr := gl.CreateIssueNote(ctx, spec.GitLab.Project, task.Spec.Issue.IID, body); nerr != nil {
			r.log().Error(nerr, "posting budget stop note")
		}
	}
	return r.patchRunWithCalls(ctx, func(s *aiv1.RunStatus) {
		// The request stays pending, but nothing is in progress any more.
		s.ActiveRunRequest = s.ObservedRunRequest
		s.CurrentStep = ""
		// Not a usage limit: an earlier one of this request is over.
		s.DeferredUntil, s.BlockReason = nil, ""
		s.ClearDeferringLimit()
		s.LastActivityTime = &metav1.Time{Time: now}
		s.LastResult = aiv1.RunResultDeferred
		s.Summary = textutil.Truncate(err.Error(), 2000)
		s.CostUSD = aiv1.AddCost(s.CostUSD, out.CostUSD)
	})
}
