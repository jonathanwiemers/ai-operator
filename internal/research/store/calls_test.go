package store_test

import (
	"slices"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

func TestAgentCalls(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	base := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	exp := base.Add(24 * time.Hour)
	cost, opusCost := 1.23456, 1.2
	calls := []store.Call{
		{Key: "call/u1/1", At: base, Namespace: "agents", Project: "bloop", MilestoneIID: 20, IssueIID: 148,
			MergeRequestIID: 702, Task: "bloop-148", Run: 2, Seq: 1, Kind: "implement", Role: "implement",
			Engine: "claude", Model: "claude-opus-5-5", Effort: "high", Resumed: true,
			InputTokens: 10, OutputTokens: 20, CacheReadTokens: 30, CacheWriteTokens: 40,
			CostUSD: &cost, CostSource: "reported", Turns: 7, DurationMS: 65000,
			Models: []store.CallModel{
				{Model: "claude-opus-5-5", InputTokens: 8, OutputTokens: 15, CacheReadTokens: 30, CacheWriteTokens: 40, CostUSD: &opusCost},
				{Model: "claude-haiku-5-5", InputTokens: 2, OutputTokens: 5},
			},
			SubagentCalls: 1, ContextSeries: []int64{1000, 2500, 4000}, SubagentTurns: []int32{1},
			ExpiresAt: exp},
		// A cursor call without IIDs or a known cost.
		{Key: "call/u2/1", At: base, Namespace: "agents", Project: "bloop", Task: "bloop-pilot", Seq: 1,
			Kind: "pilot", Engine: "cursor", FallbackFrom: "claude", CostSource: "unknown", DurationMS: 4000,
			ExpiresAt: base.Add(-time.Hour)},
	}
	n, err := s.InsertCalls(ctx, calls)
	if err != nil || n != 2 {
		t.Fatalf("InsertCalls = %d, %v; want 2", n, err)
	}
	// The same keys again write nothing.
	if n, err := s.InsertCalls(ctx, calls); err != nil || n != 0 {
		t.Fatalf("InsertCalls again = %d, %v; want 0", n, err)
	}
	if n, err := s.InsertCalls(ctx, nil); err != nil || n != 0 {
		t.Fatalf("InsertCalls(nil) = %d, %v", n, err)
	}

	type row struct {
		milestone, issue, mr *int64
		cost                 *float64
		source, fallback     string
		resumed              bool
		models               []store.CallModel
		subagents            int32
		series               []int64
		turns                []int32
	}
	get := func(key string) row {
		t.Helper()
		var r row
		if err := s.Pool().QueryRow(ctx, `SELECT milestone_iid, issue_iid, mr_iid, cost_usd::float8, cost_source,
				fallback_from, resumed, models, subagent_calls, context_series, subagent_turns
			FROM agent_calls WHERE key = $1`, key).Scan(&r.milestone, &r.issue, &r.mr, &r.cost, &r.source,
			&r.fallback, &r.resumed, &r.models, &r.subagents, &r.series, &r.turns); err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := get("call/u1/1")
	if r.milestone == nil || *r.milestone != 20 || r.issue == nil || *r.issue != 148 || r.mr == nil || *r.mr != 702 {
		t.Errorf("IIDs = %v %v %v", r.milestone, r.issue, r.mr)
	}
	if r.cost == nil || *r.cost != 1.2346 || r.source != "reported" || !r.resumed {
		t.Errorf("cost %v %q, resumed %v; want 1.2346 (4 decimals) reported, resumed", r.cost, r.source, r.resumed)
	}
	if len(r.models) != 2 {
		t.Fatalf("models = %+v", r.models)
	}
	if m := r.models[0]; m.Model != "claude-opus-5-5" || m.InputTokens != 8 || m.OutputTokens != 15 ||
		m.CacheReadTokens != 30 || m.CacheWriteTokens != 40 || m.CostUSD == nil || *m.CostUSD != 1.2 {
		t.Errorf("opus = %+v", m)
	}
	if m := r.models[1]; m.Model != "claude-haiku-5-5" || m.OutputTokens != 5 || m.CostUSD != nil {
		t.Errorf("haiku = %+v", m)
	}
	if r.subagents != 1 || !slices.Equal(r.series, []int64{1000, 2500, 4000}) || !slices.Equal(r.turns, []int32{1}) {
		t.Errorf("subagents %d, series %v, turns %v", r.subagents, r.series, r.turns)
	}

	r = get("call/u2/1")
	if r.milestone != nil || r.issue != nil || r.mr != nil || r.cost != nil || r.source != "unknown" || r.fallback != "claude" {
		t.Errorf("cursor call = %+v; want NULL IIDs and cost", r)
	}
	if r.models == nil || len(r.models) != 0 || r.series == nil || len(r.series) != 0 || r.turns == nil || len(r.turns) != 0 {
		t.Errorf("empty jsonb columns = %v %v %v; want []", r.models, r.series, r.turns)
	}

	bad := calls[1]
	bad.Key, bad.CostSource = "call/u2/2", "guessed"
	if _, err := s.InsertCalls(ctx, []store.Call{bad}); err == nil {
		t.Error("InsertCalls with cost source guessed succeeded, want a check violation")
	}

	// Retention: the cursor call has expired.
	if n, err := s.DeleteExpiredCalls(ctx, base); err != nil || n != 1 {
		t.Errorf("DeleteExpiredCalls = %d, %v; want 1", n, err)
	}
	if c := count(t, s.Pool(), "agent_calls"); c != 1 {
		t.Errorf("%d calls left, want 1", c)
	}
	exec(t, s.Pool(), `INSERT INTO agent_calls (key, at, namespace, project, task, seq, kind, cost_source, expires_at)
		SELECT 'bulk/' || i, now(), 'agents', 'bloop', 't', i, 'review', 'unknown', $1 FROM generate_series(1, 1500) i`, base)
	if n, err := s.DeleteExpiredCalls(ctx, exp.Add(time.Second)); err != nil || n != 1501 {
		t.Errorf("DeleteExpiredCalls = %d, %v; want 1501 (more than a batch)", n, err)
	}
}
