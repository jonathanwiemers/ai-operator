package controller

import (
	"context"
	"fmt"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// budget holds the milestone to its budget (spec.budgetUSD), once per poll
// and so between agent calls: spent at least the budget, a running autopilot
// pauses (a running call finishes, nothing new starts or merges); a budget
// pause ends by itself once the owner raised the budget above the spend or
// removed the line. Without the spend (no database, or the query failed)
// the budget is not enforced.
func (a *autopilot) budget(ctx context.Context) error {
	limit := a.st.Spec.BudgetUSD
	paused := a.ap.Phase == aiv1.AutopilotPaused && a.ap.PauseKind == aiv1.PauseBudget
	if limit == "" {
		a.ap.Budget = nil
		if paused {
			return a.resumeBudget(ctx, "the milestone has no budget any more")
		}
		return nil
	}
	b := &aiv1.BudgetStatus{LimitUSD: limit}
	if old := a.ap.Budget; old != nil && old.LimitUSD == limit {
		b.WarnedAt = old.WarnedAt // a new budget is warned about again
	}
	a.ap.Budget = b
	if a.r.Spend == nil {
		a.log.Info("no history database: the milestone's budget is not enforced", "budgetUSD", limit)
		return nil
	}
	sp, err := a.r.Spend.Spend(ctx, store.SpendScope{Namespace: a.st.Namespace, Project: a.st.Spec.ProjectRef, MilestoneIID: a.st.Spec.Milestone.IID})
	if err != nil {
		a.log.Error(err, "cannot read the milestone's spend: its budget is not enforced", "budgetUSD", limit)
		return nil
	}
	b.SpentUSD = aiv1.FormatCost(sp.Totals.CostUSD)
	left := aiv1.BudgetLeft(limit, b.SpentUSD, 0)
	switch {
	case a.ap.Phase == aiv1.AutopilotRunning && left <= 0:
		return a.pause(ctx, aiv1.PauseBudget, fmt.Sprintf("the budget of $%s is used ($%s spent). Raise it with `%s budget <usd>` in the milestone description",
			aiv1.ShowCost(aiv1.CostOf(limit)), aiv1.ShowCost(sp.Totals.CostUSD), a.spec.Workflow.CommandPrefix))
	case paused && left > 0:
		return a.resumeBudget(ctx, fmt.Sprintf("the budget is $%s now ($%s spent)", aiv1.ShowCost(aiv1.CostOf(limit)), aiv1.ShowCost(sp.Totals.CostUSD)))
	}
	return nil
}

// resumeBudget ends a budget pause and says why.
func (a *autopilot) resumeBudget(ctx context.Context, why string) error {
	a.log.Info("autopilot continues by itself", "why", why)
	if err := a.comment(ctx, fmt.Sprintf("▶️ Autopilot continues: %s.", why)); err != nil {
		return err
	}
	a.ap.Phase, a.ap.PausedReason, a.ap.PauseKind, a.ap.PausedFor, a.ap.ResumeWhenReady = aiv1.AutopilotRunning, "", "", 0, false
	return nil
}
