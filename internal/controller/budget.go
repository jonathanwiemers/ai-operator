package controller

import (
	"context"
	"fmt"
	"strconv"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// budget holds the milestone to its budget (spec.budgetUSD) and every open
// issue of its queue to the issue's (its implement task's spec.budgetUSD,
// else the project's budget.perIssueUSD), once per poll and so between
// agent calls: one of them spent, a running autopilot pauses (a running
// call finishes, nothing new starts or merges), PausedFor the issue or 0
// for the milestone. A budget pause ends by itself once the owner raised
// that budget above the spend or removed it. The spend is one query of the
// milestone, its issues as children. Without it (no database, or the query
// failed) the budgets are not enforced.
func (a *autopilot) budget(ctx context.Context) error {
	limit := a.st.Spec.BudgetUSD
	if limit == "" {
		a.ap.Budget = nil
	} else {
		b := &aiv1.BudgetStatus{LimitUSD: limit}
		if old := a.ap.Budget; old != nil && old.LimitUSD == limit {
			b.WarnedAt = old.WarnedAt // a new budget is warned about again
		}
		a.ap.Budget = b
	}
	var issues []*aiv1.AutopilotItem
	for i := range a.ap.Items {
		it := &a.ap.Items[i]
		it.Budget = nil
		if l := a.issueBudget(it); l != "" {
			it.Budget = &aiv1.BudgetStatus{LimitUSD: l}
			issues = append(issues, it)
		}
	}

	// A pause whose budget is gone ends without the spend.
	if a.budgetPaused() {
		if b := a.pausedBudget(); b == nil {
			why := "the milestone has no budget any more"
			if a.ap.PausedFor != 0 {
				why = fmt.Sprintf("#%d has no budget any more, or is merged or skipped", a.ap.PausedFor)
			}
			if err := a.resumeBudget(ctx, why); err != nil {
				return err
			}
		}
	}
	if a.ap.Budget == nil && len(issues) == 0 {
		return nil
	}
	if a.r.Spend == nil {
		a.log.Info("no history database: the budgets are not enforced", "budgetUSD", limit, "issueBudgets", len(issues))
		return nil
	}
	sp, err := a.r.Spend.Spend(ctx, store.SpendScope{Namespace: a.st.Namespace, Project: a.st.Spec.ProjectRef, MilestoneIID: a.st.Spec.Milestone.IID})
	if err != nil {
		a.log.Error(err, "cannot read the milestone's spend: its budgets are not enforced", "budgetUSD", limit, "issueBudgets", len(issues))
		return nil
	}
	if a.ap.Budget != nil {
		a.ap.Budget.SpentUSD = aiv1.FormatCost(sp.Totals.CostUSD)
	}
	spent := map[string]float64{}
	for _, c := range sp.Children {
		if c.Level == store.SpendIssue {
			spent[c.Key] = c.Totals.CostUSD
		}
	}
	for _, it := range issues {
		it.Budget.SpentUSD = aiv1.FormatCost(spent[strconv.FormatInt(it.IID, 10)])
	}

	// The spent budget that pauses: the milestone's first, then the
	// issues' in queue order.
	over, reason := int64(-1), ""
	prefix := a.spec.Workflow.CommandPrefix
	if b := a.ap.Budget; b != nil && aiv1.BudgetLeft(b.LimitUSD, b.SpentUSD, 0) <= 0 {
		over, reason = 0, fmt.Sprintf("the budget of $%s is used ($%s spent). Raise it with `%s budget <usd>` in the milestone description",
			showBudget(b.LimitUSD), showBudget(b.SpentUSD), prefix)
	} else {
		for _, it := range issues {
			if b := it.Budget; aiv1.BudgetLeft(b.LimitUSD, b.SpentUSD, 0) <= 0 {
				over, reason = it.IID, fmt.Sprintf("#%d used its budget of $%s ($%s spent). Raise it with `%s budget <usd>` on the issue",
					it.IID, showBudget(b.LimitUSD), showBudget(b.SpentUSD), prefix)
				break
			}
		}
	}
	switch paused := a.budgetPaused(); {
	case over >= 0 && (a.ap.Phase == aiv1.AutopilotRunning || paused && a.ap.PausedFor != over):
		if err := a.pause(ctx, aiv1.PauseBudget, reason); err != nil {
			return err
		}
		a.ap.PausedFor = over
	case over < 0 && paused:
		b, why := a.pausedBudget(), "the budget"
		if a.ap.PausedFor != 0 {
			why = fmt.Sprintf("the budget of #%d", a.ap.PausedFor)
		}
		return a.resumeBudget(ctx, fmt.Sprintf("%s is $%s now ($%s spent)", why, showBudget(b.LimitUSD), showBudget(b.SpentUSD)))
	}
	return nil
}

// issueBudget is the budget of an item that can still spend (pending or
// active), formatted for storage: its task's override, else the project's
// default; "" for none.
func (a *autopilot) issueBudget(it *aiv1.AutopilotItem) string {
	if it.State != aiv1.AutopilotItemPending && it.State != aiv1.AutopilotItemActive {
		return ""
	}
	if t := a.tasks[it.IID]; t != nil && t.Spec.BudgetUSD != "" {
		return t.Spec.BudgetUSD
	}
	if b := a.spec.Budget; b != nil && aiv1.CostOf(b.PerIssueUSD) > 0 {
		return aiv1.FormatCost(aiv1.CostOf(b.PerIssueUSD))
	}
	return ""
}

// budgetPaused reports whether autopilot is paused for a budget.
func (a *autopilot) budgetPaused() bool {
	return a.ap.Phase == aiv1.AutopilotPaused && a.ap.PauseKind == aiv1.PauseBudget
}

// budgetStopped reports whether the implement task of issue iid gets
// spec.budgetStop: the autopilot of its milestone (spec task st) is paused
// for the issue's budget, or for the milestone's (every issue of it). Like
// the pause reason, a pause from this poll reaches the task next poll, and
// a resume clears it.
func budgetStopped(st *aiv1.AgentTask, iid int64) bool {
	ap := st.Status.Autopilot
	return st.Spec.Autopilot && ap != nil && ap.Phase == aiv1.AutopilotPaused && ap.PauseKind == aiv1.PauseBudget && (ap.PausedFor == 0 || ap.PausedFor == iid)
}

// pausedBudget is the budget a budget pause is for, nil once it is gone.
func (a *autopilot) pausedBudget() *aiv1.BudgetStatus {
	if a.ap.PausedFor == 0 {
		return a.ap.Budget
	}
	for i := range a.ap.Items {
		if a.ap.Items[i].IID == a.ap.PausedFor {
			return a.ap.Items[i].Budget
		}
	}
	return nil
}

func showBudget(usd string) string { return aiv1.ShowCost(aiv1.CostOf(usd)) }

// resumeBudget ends a budget pause and says why.
func (a *autopilot) resumeBudget(ctx context.Context, why string) error {
	a.log.Info("autopilot continues by itself", "why", why)
	if err := a.comment(ctx, fmt.Sprintf("▶️ Autopilot continues: %s.", why)); err != nil {
		return err
	}
	a.ap.Phase, a.ap.PausedReason, a.ap.PauseKind, a.ap.PausedFor, a.ap.ResumeWhenReady = aiv1.AutopilotRunning, "", "", 0, false
	return nil
}
