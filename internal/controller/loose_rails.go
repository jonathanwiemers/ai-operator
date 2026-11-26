package controller

import (
	"context"
	"fmt"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// looseCompanion is the hold reason of a queue item whose implement task has
// a companion repository: its landing and releases need a milestone.
const looseCompanion = "companion repositories need a milestone"

// looseFreeze keeps the loose issues queue frozen (ap.FrozenAt) while the
// default branch is red with mainHealth.freezeAutopilot: nothing starts
// (pickLoose) and nothing merges (drive). It comments once when the freeze
// begins and once when it ends; the tracking issue says so while it lasts.
// The main-red fix is the milestone lane's, never the queue's.
func (a *autopilot) looseFreeze(ctx context.Context) error {
	mh := a.r.freshMainHealth(a.spec, a.project.Status.MainHealth, mainHealthStale)
	freeze := *a.spec.MainHealth.FreezeAutopilot
	red := mh != nil && freeze && (mh.State == aiv1.MainHealthRed || mh.State == aiv1.MainHealthRetrying && mh.IssueIID != 0)
	switch {
	case red && a.ap.FrozenAt == nil:
		// The comment first: a failed one is tried again on the next poll.
		if err := a.comment(ctx, "🧊 "+frozenLine(mh.IssueIID)); err != nil {
			return err
		}
		a.log.Info("loose issues queue frozen: the default branch is red", "issue", mh.IssueIID)
		a.ap.FrozenAt = &metav1.Time{Time: a.r.now()}
	case !red && a.ap.FrozenAt != nil:
		why := "the default branch is green again"
		switch {
		case mh == nil:
			why = "the health of the default branch is not watched any more"
		case !freeze:
			why = "the merge freeze is switched off (`mainHealth.freezeAutopilot`)"
		}
		if err := a.comment(ctx, fmt.Sprintf("▶️ The freeze is over: %s. The loose issues queue goes on.", why)); err != nil {
			return err
		}
		a.log.Info("loose issues queue goes on after the freeze", "why", why)
		a.ap.FrozenAt = nil
	}
	return nil
}

// frozenLine says why the queue is frozen; issue is the main-red issue, 0 for
// none.
func frozenLine(issue int64) string {
	ref := ""
	if issue != 0 {
		ref = fmt.Sprintf(" (#%d)", issue)
	}
	return fmt.Sprintf("Frozen: the default branch is red%s; nothing starts or merges until it is green.", ref)
}

// looseCompanionHold holds it when its implement task has a companion
// repository (a companion run, or a cross-repo target), before any companion
// step of the gate; it reports whether it is held.
func (a *autopilot) looseCompanionHold(ctx context.Context, it *aiv1.AutopilotItem, task *aiv1.AgentTask) (bool, error) {
	if !a.loose || task == nil || task.Spec.Companion == nil && task.Status.Run.Companion == nil {
		return false, nil
	}
	return true, a.hold(ctx, it, aiv1.PauseConfig, looseCompanion)
}

// queueIssues are the issues of the loose issues queue's items that did not
// leave it, among tasks: syncCompanions records no companion of theirs.
func queueIssues(tasks []aiv1.AgentTask) map[int64]bool {
	in := map[int64]bool{}
	for _, t := range tasks {
		if t.Spec.Type != aiv1.TaskTypeQueue || t.Status.Autopilot == nil {
			continue
		}
		for _, it := range t.Status.Autopilot.Items {
			if it.State != aiv1.AutopilotItemLeft {
				in[it.IID] = true
			}
		}
	}
	return in
}

// looseBudget holds each active item of the loose issues queue to its budget
// (issueBudget: `/agent budget` on the issue, else budget.perIssueUSD). A
// spent budget holds that item only, the others go on; the hold ends by
// itself once the budget is raised above the spend or removed. The queue has
// no budget of its own. The spend is one query per active item with a budget
// or a spend alert; without it the budgets are not enforced.
func (a *autopilot) looseBudget(ctx context.Context) error {
	a.ap.Budget = nil
	n := 0
	for i := range a.ap.Items {
		it := &a.ap.Items[i]
		it.Budget = nil
		if l := a.issueBudget(it); l != "" {
			it.Budget = &aiv1.BudgetStatus{LimitUSD: l}
			n++
		}
	}
	if n+a.rearmAlerts() > 0 && a.r.Spend != nil {
		children := map[string]*store.SpendChild{}
		for i := range a.ap.Items {
			it := &a.ap.Items[i]
			if alert, warn := a.alertThresholds(it); it.State != aiv1.AutopilotItemActive || it.Budget == nil && alert == "" && warn == "" {
				continue
			}
			sp, err := a.r.Spend.Spend(ctx, store.SpendScope{Namespace: a.st.Namespace, Project: a.st.Spec.ProjectRef, IssueIID: it.IID})
			if err != nil {
				a.log.Error(err, "cannot read the issue's spend: its budget is not enforced", "issue", it.IID)
				continue
			}
			key := strconv.FormatInt(it.IID, 10)
			children[key] = &store.SpendChild{Level: store.SpendIssue, Key: key, Totals: sp.Totals, Kinds: sp.ByKind, Models: modelSplits(sp.ByModel)}
			if it.Budget != nil {
				it.Budget.SpentUSD = aiv1.FormatCost(sp.Totals.CostUSD.USD)
			}
		}
		if err := a.budgetAlerts(ctx, children); err != nil {
			return err
		}
	}
	for i := range a.ap.Items {
		it := &a.ap.Items[i]
		b, held := it.Budget, it.Hold != nil && it.Hold.Kind == aiv1.PauseBudget
		var err error
		switch {
		case it.State != aiv1.AutopilotItemActive:
		case b != nil && b.SpentUSD != "" && aiv1.BudgetLeft(b.LimitUSD, b.SpentUSD, 0) <= 0:
			if !held { // the spend grows a little while the last call ends
				err = a.hold(ctx, it, aiv1.PauseBudget, a.issueBudgetReason(it))
			}
		case held && b == nil:
			err = a.unhold(ctx, it, byOperator, fmt.Sprintf("#%d has no budget any more", it.IID))
		case held && b.SpentUSD != "":
			err = a.unhold(ctx, it, byOperator, fmt.Sprintf("the budget of #%d is $%s now ($%s spent)", it.IID, showBudget(b.LimitUSD), showBudget(b.SpentUSD)))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// modelSplits are the spend's models by cost as a child's model split.
func modelSplits(ms []store.SpendBreakdown) []store.ModelSplit {
	out := make([]store.ModelSplit, 0, len(ms))
	for _, m := range ms {
		out = append(out, store.ModelSplit{Model: m.Key, Tokens: m.Totals.Tokens(), CostUSD: m.Totals.CostUSD.USD, CostKnown: m.Totals.CostUSD.Known})
	}
	return out
}
