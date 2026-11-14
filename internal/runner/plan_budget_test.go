package runner

import (
	"context"
	"encoding/json"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// planCostAnswer answers a plan turn that cost usd (reported when known)
// and used tokens.
func planCostAnswer(usd float64, known bool, tokens int64) func(Invocation) (*Result, error) {
	return func(Invocation) (*Result, error) {
		b, _ := json.Marshal(PlanTurn{Reply: "Ok."})
		return &Result{SessionID: "s", CostUSD: usd, CostReported: known, Structured: b,
			CallUsage: &CallUsage{InputTokens: tokens / 2, OutputTokens: tokens / 4, CacheReadTokens: tokens / 4}}, nil
	}
}

func TestPlanTurnRecordsCallsAndSpend(t *testing.T) {
	e := newPlanEnv(t, planCostAnswer(0.25, true, 1000), planCostAnswer(0.25, true, 1000))
	e.run.MaxBudgetUSD = "5"
	e.turn(t, 10, "show the version")
	p := e.turn(t, 11, "the footer")

	s := p.Status
	if s.SpendUSD != "0.5000" || s.Tokens != 2000 || len(s.Calls) != 2 {
		t.Fatalf("spend %q, tokens %d, %d calls", s.SpendUSD, s.Tokens, len(s.Calls))
	}
	for i, c := range s.Calls {
		if c.Kind != aiv1.CallKindPlan || c.Role != "plan" || c.Seq != int64(i+1) || c.CostUSD != "0.2500" || c.CostSource != aiv1.CostReported {
			t.Fatalf("call %d: %+v", i, c)
		}
	}
	if got := []string{e.engine.calls[0].MaxBudgetUSD, e.engine.calls[1].MaxBudgetUSD}; got[0] != "5.00" || got[1] != "4.75" {
		t.Fatalf("the invocations' budgets are %v, want the rest of the cap", got)
	}
}

func TestPlanTurnRefusedAtCapUntilRaised(t *testing.T) {
	e := newPlanEnv(t, planCostAnswer(0.25, true, 10), planCostAnswer(0.25, true, 10), planCostAnswer(0.1, true, 10))
	e.run.MaxBudgetUSD = "0.3"
	e.turn(t, 10, "one")
	e.turn(t, 11, "two") // one turn may run over the rest (D437-P2)
	if got := e.engine.calls[1].MaxBudgetUSD; got != "0.05" {
		t.Fatalf("second turn's budget %q", got)
	}
	p := e.turn(t, 12, "three")
	if rep := lastReply(t, p, 12); rep.Error != "the plan's budget of $0.30 is used; raise it on the Desk" || len(e.engine.calls) != 2 {
		t.Fatalf("reply %+v after %d engine calls", rep, len(e.engine.calls))
	}
	if p.Status.Phase != aiv1.AgentPlanReady || len(p.Status.Calls) != 2 {
		t.Fatalf("a refused message changed the plan: %+v", p.Status)
	}

	p.Spec.MaxBudgetUSD = "1"
	if err := e.k8s.Update(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	p = e.turn(t, 13, "four")
	if rep := lastReply(t, p, 13); rep.Error != "" || rep.Text != "Ok." || e.engine.calls[2].MaxBudgetUSD != "0.50" {
		t.Fatalf("after the raise: reply %+v, budget %q", rep, e.engine.calls[2].MaxBudgetUSD)
	}
	if p.Status.SpendUSD != "0.6000" {
		t.Fatalf("spend %q", p.Status.SpendUSD)
	}
}

func TestPlanTurnUnknownCostAddsTokensOnly(t *testing.T) {
	e := newPlanEnv(t, planCostAnswer(0, false, 400), planCostAnswer(0, false, 400))
	e.project.Spec.Agents.Spec.Engine = aiv1.EngineCursor
	e.turn(t, 10, "one")
	p := e.turn(t, 11, "two")

	if s := p.Status; s.SpendUSD != "" || s.Tokens != 800 || len(s.Calls) != 2 || s.Calls[1].CostSource != aiv1.CostUnknown {
		t.Fatalf("spend %q, tokens %d, calls %+v", s.SpendUSD, s.Tokens, s.Calls)
	}
	if got := e.engine.calls[0].MaxBudgetUSD; got != "" {
		t.Fatalf("without a cap the invocation has no budget, got %q", got)
	}
}
