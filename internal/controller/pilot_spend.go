package controller

import (
	"cmp"
	"context"
	"slices"

	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// SpendSource reads spend from the history database (agent_calls).
type SpendSource interface {
	Spend(ctx context.Context, sc store.SpendScope) (store.Spend, error)
}

// maxSpendDrivers is how many issues, kinds and models a SpendSummary
// names.
const maxSpendDrivers = 3

// retroSpend is the spend of the coordinate task's milestone for its
// retrospective, nil without a spend source or when the query fails.
func (r *AgentProjectReconciler) retroSpend(ctx context.Context, coord *aiv1.AgentTask) *aiv1.SpendSummary {
	if r.Spend == nil || coord.Spec.Milestone == nil {
		return nil
	}
	sc := store.SpendScope{Namespace: coord.Namespace, Project: coord.Spec.ProjectRef, MilestoneIID: coord.Spec.Milestone.IID}
	sp, err := r.Spend.Spend(ctx, sc)
	if err != nil {
		log.FromContext(ctx).Error(err, "cannot read the milestone's spend for the retrospective; it sums the live tasks",
			"task", coord.Name, "milestone", sc.MilestoneIID)
		return nil
	}
	return spendSummary(sp)
}

// spendSummary is sp with its top issues, kinds and models by USD, then
// tokens. The milestone's own calls (spec, pilot, retro) are no issue.
func spendSummary(sp store.Spend) *aiv1.SpendSummary {
	t := sp.Totals
	s := &aiv1.SpendSummary{CostUSD: aiv1.FormatCost(t.CostUSD.USD), CostKnown: t.CostUSD.Known, SubagentCalls: t.SubagentCalls, UnmeteredCalls: t.UnmeteredCalls,
		Calls: t.Calls, OldBasisCalls: t.OldBasisCalls,
		Tokens: aiv1.SpendTokens{Input: t.InputTokens, Output: t.OutputTokens, CacheRead: t.CacheReadTokens, CacheWrite: t.CacheWriteTokens}}
	var issues []aiv1.SpendDriver
	for _, c := range sp.Children {
		if c.Level == store.SpendIssue {
			issues = append(issues, spendDriver(c.Key, c.Label, c.Totals))
		}
	}
	s.TopIssues = topDrivers(issues)
	for _, b := range []struct {
		out   *[]aiv1.SpendDriver
		in    []store.SpendBreakdown
		label func(key string, unmetered bool) string
	}{
		{&s.TopKinds, sp.ByKind, func(key string, _ bool) string { return key }},
		// cursor's auto as "cursor auto (unmetered)".
		{&s.TopModels, sp.ByModel, aiv1.ModelLabel},
	} {
		var ds []aiv1.SpendDriver
		for _, br := range b.in {
			if br.Key != "" {
				d := spendDriver(br.Key, br.Key, br.Totals)
				d.Label = b.label(br.Key, d.Unmetered)
				ds = append(ds, d)
			}
		}
		*b.out = topDrivers(ds)
	}
	return s
}

func spendDriver(key, label string, t store.SpendTotals) aiv1.SpendDriver {
	d := aiv1.SpendDriver{Key: key, Label: label, Tokens: t.Tokens(), Unmetered: t.Calls > 0 && t.UnmeteredCalls == t.Calls}
	if t.CostUSD.Known {
		d.CostUSD = aiv1.FormatCost(t.CostUSD.USD)
	}
	return d
}

// topDrivers returns the biggest maxSpendDrivers of ds by USD, then
// tokens.
func topDrivers(ds []aiv1.SpendDriver) []aiv1.SpendDriver {
	slices.SortStableFunc(ds, func(a, b aiv1.SpendDriver) int {
		return cmp.Or(cmp.Compare(aiv1.CostOf(b.CostUSD), aiv1.CostOf(a.CostUSD)), cmp.Compare(b.Tokens, a.Tokens))
	})
	return ds[:min(len(ds), maxSpendDrivers)]
}
