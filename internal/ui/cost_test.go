package ui

import (
	"net/http"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

type taskCostResponse struct {
	Cost Cost `json:"cost"`
}

// checkCost compares the chip fields of got with want, and that a cost
// from calls carries its breakdown.
func checkCost(t *testing.T, what string, got Cost, want Cost) {
	t.Helper()
	if got.CostUSD != want.CostUSD || got.CostKnown != want.CostKnown || got.Tokens != want.Tokens || got.CostFrom != want.CostFrom ||
		(got.Spend != nil) != (got.CostFrom == CostFromCalls) {
		t.Errorf("%s cost = %+v, want %+v", what, got, want)
	}
}

// Without the database the chips sum the live tasks, as before.
func TestCostsFromTasks(t *testing.T) {
	for name, env := range map[string]*testEnv{
		"database off":           newTestEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}}),
		"database not connected": spendEnv(t, nil),
	} {
		var d ProjectDetail
		if code := env.get(t, "/api/v1/projects/team-a/shop", &d); code != http.StatusOK || len(d.Milestones) != 1 {
			t.Fatalf("%s: project: status %d, %+v", name, code, d)
		}
		checkCost(t, name+": project", d.Cost, Cost{CostUSD: "1.75", CostKnown: true, CostFrom: CostFromTasks})
		checkCost(t, name+": milestone row", d.Milestones[0].Cost, Cost{CostUSD: "1.75", CostKnown: true, CostFrom: CostFromTasks})
		var m MilestoneView
		if code := env.get(t, shopMilestone, &m); code != http.StatusOK {
			t.Fatalf("%s: milestone: status %d", name, code)
		}
		checkCost(t, name+": milestone", m.Cost, Cost{CostUSD: "1.75", CostKnown: true, CostFrom: CostFromTasks})
		var task taskCostResponse
		if code := env.get(t, shopTask, &task); code != http.StatusOK {
			t.Fatalf("%s: task: status %d", name, code)
		}
		checkCost(t, name+": task", task.Cost, Cost{CostUSD: "0.50", CostKnown: true, CostFrom: CostFromTasks})
	}
}

// With the database the chips sum agent_calls and legacy runs: a deleted
// task still counts, and the CRDs' costs do not.
func TestCostsFromCalls(t *testing.T) {
	owner, reader := readerStore(t)
	base := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	exp := base.Add(720 * time.Hour)
	usd := func(v float64) *float64 { return &v }
	call := func(key string, milestone, issue int64, task string, cost float64, in, out, cacheRead int64) store.Call {
		return store.Call{Key: key, At: base, Namespace: "team-a", Project: "shop", MilestoneIID: milestone, IssueIID: issue,
			Task: task, Run: 2, Seq: 1, Kind: "implement", Role: "implement", Engine: "claude", Model: "claude-opus-5-5",
			InputTokens: in, OutputTokens: out, CacheReadTokens: cacheRead, CostUSD: usd(cost), CostSource: "reported",
			ExpiresAt: exp}
	}
	if _, err := owner.InsertCalls(t.Context(), []store.Call{
		call("c1", 3, 42, "shop-42-add-paypal", 1.5, 1000, 200, 300000),
		// The task of #41 was deleted.
		call("c2", 3, 41, "shop-41-gone", 2, 10, 10, 0),
		// A Desk chat turn: the project's, no milestone's.
		call("c3", 0, 0, "shop-chat", 0.25, 1, 1, 0),
		// Another project.
		{Key: "c4", At: base, Namespace: "team-b", Project: "billing", MilestoneIID: 3, Task: "billing-7-invoice", Run: 1, Seq: 1,
			Kind: "implement", Engine: "claude", CostUSD: usd(9), CostSource: "reported", ExpiresAt: exp},
	}); err != nil {
		t.Fatal(err)
	}
	// Run 1 of #42 is from before agent_calls.
	if _, err := owner.InsertEvents(t.Context(), []store.Event{{Key: "e1", At: base.Add(-time.Hour), Namespace: "team-a", Project: "shop",
		MilestoneIID: 3, IssueIID: 42, Task: "shop-42-add-paypal", Kind: "run.finished",
		Data: map[string]any{"costUSD": "0.7500", "runCount": 1}, ExpiresAt: exp}}); err != nil {
		t.Fatal(err)
	}
	env := spendEnv(t, reader)

	var d ProjectDetail
	if code := env.get(t, "/api/v1/projects/team-a/shop", &d); code != http.StatusOK || len(d.Milestones) != 1 {
		t.Fatalf("project: status %d, %+v", code, d)
	}
	checkCost(t, "project", d.Cost, Cost{CostUSD: "4.50", CostKnown: true, Tokens: 301222, CostFrom: CostFromCalls})
	checkCost(t, "milestone row", d.Milestones[0].Cost, Cost{CostUSD: "4.25", CostKnown: true, Tokens: 301220, CostFrom: CostFromCalls})

	var m MilestoneView
	if code := env.get(t, shopMilestone, &m); code != http.StatusOK {
		t.Fatalf("milestone: status %d", code)
	}
	checkCost(t, "milestone", m.Cost, Cost{CostUSD: "4.25", CostKnown: true, Tokens: 301220, CostFrom: CostFromCalls})

	var task taskCostResponse
	if code := env.get(t, shopTask, &task); code != http.StatusOK {
		t.Fatalf("task: status %d", code)
	}
	checkCost(t, "task", task.Cost, Cost{CostUSD: "2.25", CostKnown: true, Tokens: 301200, CostFrom: CostFromCalls})
	if s := task.Cost.Spend; s == nil || s.LegacyCostUSD != "0.75" || s.CacheReadTokens != 300000 || s.Calls != 1 {
		t.Errorf("task breakdown = %+v", s)
	}
}

func TestCallsCostUnknown(t *testing.T) {
	c := callsCost(store.SpendTotals{UnknownCalls: 2, Calls: 2, InputTokens: 100, OutputTokens: 50})
	if c.CostUSD != "" || c.CostKnown || c.Tokens != 150 || c.CostFrom != CostFromCalls || c.Spend == nil {
		t.Errorf("cost = %+v, want an unknown cost with 150 tokens", c)
	}
}
