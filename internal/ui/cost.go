package ui

import (
	"context"
	"strconv"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// Where a Cost comes from.
const (
	// CostFromCalls: summed from agent_calls and the runs before them, so
	// deleted tasks still count.
	CostFromCalls = "calls"
	// CostFromTasks: summed over the live tasks' status, without tokens,
	// when the history database is off or not connected.
	CostFromTasks = "tasks"
)

// Cost is what a project, milestone or task cost: its cost chip. CostUSD
// is "" when no cost is known (cursor calls without pricing), never
// "0.00".
type Cost struct {
	CostUSD   string `json:"costUSD"`
	CostKnown bool   `json:"costKnown"`
	// Tokens is the input, output and cache tokens together.
	Tokens   int64  `json:"tokens"`
	CostFrom string `json:"costFrom"`
	// Spend is the breakdown of a cost from calls.
	Spend *SpendTotalsView `json:"spend,omitempty"`
}

// tasksCost is a cost summed over live tasks.
func tasksCost(usd float64) Cost {
	return Cost{CostUSD: aiv1.ShowCost(usd), CostKnown: true, CostFrom: CostFromTasks}
}

// callsCost is a cost summed from agent_calls.
func callsCost(t store.SpendTotals) Cost {
	v := spendTotalsView(t)
	return Cost{CostUSD: v.CostUSD, CostKnown: t.CostKnown, Tokens: t.Tokens(), CostFrom: CostFromCalls, Spend: &v}
}

// spendStore is the history database for costs, nil while it is off or not
// connected.
func (s *Server) spendStore() *store.Store {
	if !s.cfg.Research.Enabled() {
		return nil
	}
	return s.research.Load()
}

// projectCosts sets the costs of the project d and its milestones from
// agent_calls, in two queries. Without the history database, or when it
// fails, they stay the sums over the live tasks.
func (s *Server) projectCosts(ctx context.Context, d *ProjectDetail) {
	st := s.spendStore()
	if st == nil {
		return
	}
	sc := store.SpendScope{Namespace: d.Namespace, Project: d.Name}
	total, err := st.SpendTotals(ctx, sc)
	if err != nil {
		s.log.Error(err, "cannot read the project's spend", "namespace", d.Namespace, "project", d.Name)
		return
	}
	byMilestone, err := st.SpendChildTotals(ctx, sc)
	if err != nil {
		s.log.Error(err, "cannot read the milestones' spend", "namespace", d.Namespace, "project", d.Name)
		return
	}
	d.Cost = callsCost(total)
	for i := range d.Milestones {
		m := &d.Milestones[i]
		t, ok := byMilestone[strconv.FormatInt(m.IID, 10)]
		if !ok {
			// No calls and no runs: nothing spent, which is known.
			t.CostKnown = true
		}
		m.Cost = callsCost(t)
	}
}

// scopeCost returns the cost of sc from agent_calls, or fallback without
// the history database or when it fails.
func (s *Server) scopeCost(ctx context.Context, sc store.SpendScope, fallback Cost) Cost {
	st := s.spendStore()
	if st == nil {
		return fallback
	}
	t, err := st.SpendTotals(ctx, sc)
	if err != nil {
		s.log.Error(err, "cannot read spend", "namespace", sc.Namespace, "project", sc.Project,
			"milestone", sc.MilestoneIID, "task", sc.Task)
		return fallback
	}
	return callsCost(t)
}
