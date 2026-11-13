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
		{Key: "call/u1/1", At: base, Namespace: "agents", Project: "orbit", MilestoneIID: 20, IssueIID: 148,
			MergeRequestIID: 702, Task: "orbit-148", Run: 2, Seq: 1, Kind: "implement", Role: "implement",
			Engine: "claude", Model: "claude-opus-5-5", Effort: "high", Resumed: true,
			InputTokens: 10, OutputTokens: 20, CacheReadTokens: 30, CacheWriteTokens: 40,
			CostUSD: &cost, CostSource: "reported", Turns: 7, DurationMS: 65000,
			Models: []store.CallModel{
				{Model: "claude-opus-5-5", InputTokens: 8, OutputTokens: 15, CacheReadTokens: 30, CacheWriteTokens: 40, CostUSD: &opusCost},
				{Model: "claude-haiku-5-5", InputTokens: 2, OutputTokens: 5},
			},
			SubagentCalls: 1, ContextSeries: []int64{1000, 2500, 4000}, SubagentTurns: []int32{1}, ReadGateBlocks: 3,
			Checkpoint: "usage-limit", CheckpointSHA: "0123abcd", LinesAdded: 412, LinesDeleted: 38,
			ExpiresAt: exp},
		// A cursor call without IIDs or a known cost.
		{Key: "call/u2/1", At: base, Namespace: "agents", Project: "orbit", Task: "orbit-pilot", Seq: 1,
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
		milestone, issue, mr  *int64
		cost                  *float64
		source, fallback      string
		resumed               bool
		models                []store.CallModel
		subagents, gateBlocks int32
		checkpoint, cpSHA     string
		added, deleted        int64
		series                []int64
		turns                 []int32
	}
	get := func(key string) row {
		t.Helper()
		var r row
		if err := s.Pool().QueryRow(ctx, `SELECT milestone_iid, issue_iid, mr_iid, cost_usd::float8, cost_source,
				fallback_from, resumed, models, subagent_calls, context_series, subagent_turns, read_gate_blocks,
				checkpoint, checkpoint_sha, lines_added, lines_deleted
			FROM agent_calls WHERE key = $1`, key).Scan(&r.milestone, &r.issue, &r.mr, &r.cost, &r.source,
			&r.fallback, &r.resumed, &r.models, &r.subagents, &r.series, &r.turns, &r.gateBlocks,
			&r.checkpoint, &r.cpSHA, &r.added, &r.deleted); err != nil {
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
	if r.gateBlocks != 3 {
		t.Errorf("read gate blocks %d, want 3", r.gateBlocks)
	}
	if r.checkpoint != "usage-limit" || r.cpSHA != "0123abcd" || r.added != 412 || r.deleted != 38 {
		t.Errorf("checkpoint %q %q, lines +%d/−%d; want usage-limit 0123abcd, +412/−38", r.checkpoint, r.cpSHA, r.added, r.deleted)
	}

	r = get("call/u2/1")
	if r.milestone != nil || r.issue != nil || r.mr != nil || r.cost != nil || r.source != "unknown" || r.fallback != "claude" {
		t.Errorf("cursor call = %+v; want NULL IIDs and cost", r)
	}
	if r.gateBlocks != 0 {
		t.Errorf("cursor call read gate blocks %d, want 0", r.gateBlocks)
	}
	if r.checkpoint != "" || r.cpSHA != "" || r.added != 0 || r.deleted != 0 {
		t.Errorf("cursor call checkpoint %q %q, lines +%d/−%d; want none", r.checkpoint, r.cpSHA, r.added, r.deleted)
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
		SELECT 'bulk/' || i, now(), 'agents', 'orbit', 't', i, 'review', 'unknown', $1 FROM generate_series(1, 1500) i`, base)
	if n, err := s.DeleteExpiredCalls(ctx, exp.Add(time.Second)); err != nil || n != 1501 {
		t.Errorf("DeleteExpiredCalls = %d, %v; want 1501 (more than a batch)", n, err)
	}
}

// Migration 5 adds read_gate_blocks on top of 0004; rows written before it
// read 0.
func TestReadGateBlocksMigration(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	exec(t, s.Pool(), "ALTER TABLE agent_calls DROP COLUMN read_gate_blocks")
	exec(t, s.Pool(), "DELETE FROM schema_migrations WHERE version = 5")
	exec(t, s.Pool(), insertCall)
	if err := s.Migrate(ctx, store.Grants{}); err != nil {
		t.Fatal(err)
	}
	var n int32
	if err := s.Pool().QueryRow(ctx, "SELECT read_gate_blocks FROM agent_calls WHERE key = 'k'").Scan(&n); err != nil || n != 0 {
		t.Errorf("old row read_gate_blocks = %d, %v; want 0", n, err)
	}
}

// Migration 7 adds the checkpoint and lines columns to agent_calls; rows
// written before it read an empty checkpoint and no lines.
func TestCheckpointColumnsMigration(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	exec(t, s.Pool(), `ALTER TABLE agent_calls DROP COLUMN checkpoint, DROP COLUMN checkpoint_sha,
		DROP COLUMN lines_added, DROP COLUMN lines_deleted`)
	exec(t, s.Pool(), "DELETE FROM schema_migrations WHERE version = 7")
	exec(t, s.Pool(), insertCall)
	if err := s.Migrate(ctx, store.Grants{}); err != nil {
		t.Fatal(err)
	}
	var reason, sha string
	var added, deleted int64
	if err := s.Pool().QueryRow(ctx, "SELECT checkpoint, checkpoint_sha, lines_added, lines_deleted FROM agent_calls WHERE key = 'k'").
		Scan(&reason, &sha, &added, &deleted); err != nil || reason != "" || sha != "" || added != 0 || deleted != 0 {
		t.Errorf("old row = %q %q +%d/−%d, %v; want '' '' 0 0", reason, sha, added, deleted, err)
	}
}

// Migration 8 adds step_gate_nudges to agent_calls; rows written before it
// read 0.
func TestStepGateNudgesMigration(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	exec(t, s.Pool(), "ALTER TABLE agent_calls DROP COLUMN step_gate_nudges")
	exec(t, s.Pool(), "DELETE FROM schema_migrations WHERE version = 8")
	exec(t, s.Pool(), insertCall)
	if err := s.Migrate(ctx, store.Grants{}); err != nil {
		t.Fatal(err)
	}
	var n int32
	if err := s.Pool().QueryRow(ctx, "SELECT step_gate_nudges FROM agent_calls WHERE key = 'k'").Scan(&n); err != nil || n != 0 {
		t.Errorf("old row step_gate_nudges = %d, %v; want 0", n, err)
	}
}

// token_basis is stored as given and read back by scanCall; rows from before
// the column read 0 and are not rewritten.
func TestTokenBasis(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	at := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	mk := func(key string, seq int64, basis int16) store.Call {
		return store.Call{Key: key, At: at, Namespace: "agents", Project: "p", Task: "t", Seq: seq, Kind: "implement",
			CostSource: "unknown", ExpiresAt: at.Add(time.Hour), TokenBasis: basis}
	}
	if _, err := s.InsertCalls(ctx, []store.Call{mk("a", 1, 1), mk("b", 2, 0)}); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]int16{"a": 1, "b": 0} {
		var got int16
		if err := s.Pool().QueryRow(ctx, "SELECT token_basis FROM agent_calls WHERE key = $1", key).Scan(&got); err != nil || got != want {
			t.Errorf("%s: token_basis = %d, %v; want %d", key, got, err, want)
		}
	}

	// A row from before the column reads 0 after the migration.
	exec(t, s.Pool(), "ALTER TABLE agent_calls DROP COLUMN token_basis")
	exec(t, s.Pool(), "DELETE FROM schema_migrations WHERE version = 20261110114727")
	if err := s.Migrate(ctx, store.Grants{}); err != nil {
		t.Fatal(err)
	}
	var n int16
	if err := s.Pool().QueryRow(ctx, "SELECT token_basis FROM agent_calls WHERE key = 'a'").Scan(&n); err != nil || n != 0 {
		t.Errorf("old row token_basis = %d, %v; want 0", n, err)
	}
}
