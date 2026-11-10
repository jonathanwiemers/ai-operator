package store_test

import (
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

func TestSpend(t *testing.T) {
	owner := storetest.New(t)
	// The UI reads spend as the reader.
	s := asReader(t, owner)
	ctx := t.Context()
	base := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	exp, expired := time.Now().Add(24*time.Hour), time.Now().Add(-time.Hour)
	usd := func(v float64) *float64 { return &v }
	call := func(key string, at time.Duration, milestone, issue int64, task string, run, seq int64, kind, role, model string,
		cost *float64, in, out, cacheRead, cacheWrite, durationMS int64) store.Call {
		source := "reported"
		if cost == nil {
			source = "unknown"
		}
		return store.Call{Key: key, At: base.Add(at), Namespace: "agents", Project: "bloop", MilestoneIID: milestone,
			IssueIID: issue, Task: task, Run: run, Seq: seq, Kind: kind, Role: role, Engine: "claude", Model: model,
			InputTokens: in, OutputTokens: out, CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite,
			CostUSD: cost, CostSource: source, DurationMS: durationMS, ExpiresAt: exp}
	}
	// #148 run 2: an implement step with a haiku subagent, and a review.
	impl := call("c1", 2*time.Hour, 20, 148, "bloop-148", 2, 1, "implement", "implement", "claude-opus-5-5", usd(1.23), 10, 20, 30, 40, 60000)
	impl.Models = []store.CallModel{
		{Model: "claude-opus-5-5", InputTokens: 8, OutputTokens: 15, CacheReadTokens: 30, CacheWriteTokens: 40, CostUSD: usd(1.2)},
		{Model: "claude-haiku-5-5", InputTokens: 2, OutputTokens: 5, CostUSD: usd(0.03)},
	}
	impl.SubagentCalls, impl.ContextSeries, impl.SubagentTurns, impl.ReadGateBlocks = 1, []int64{1000, 2500, 4000}, []int32{1}, 3
	// It was cut off by the usage limit and left 120 lines.
	impl.Checkpoint, impl.CheckpointSHA, impl.LinesAdded, impl.LinesDeleted = "usage-limit", "abc123", 120, 7
	review := call("c2", 3*time.Hour, 20, 148, "bloop-148", 2, 2, "review", "security", "claude-sonnet-5-5", usd(0.5), 100, 10, 1000, 0, 10000)
	review.ReadGateBlocks = 1
	// #150: a cursor call without a cost.
	fix := call("c3", 4*time.Hour, 20, 150, "bloop-150", 1, 1, "fix", "fix", "gpt-5", nil, 100, 50, 0, 0, 5000)
	fix.Engine = "cursor"
	fix.Checkpoint, fix.CheckpointSHA = "usage-limit", "def456"
	// The milestone's spec: no issue.
	spec := call("c4", time.Hour, 20, 0, "bloop-spec-20", 1, 1, "spec", "spec", "claude-opus-5-5", usd(0.25), 1, 2, 3, 4, 1000)
	// Milestone 21, #160: the row has the model's alias, models the full
	// names; the main model is the one with the most output.
	alias := call("c5", 5*time.Hour, 21, 160, "bloop-160", 1, 1, "implement", "implement", "opus", usd(3), 5, 5, 100, 0, 20000)
	alias.Models = []store.CallModel{
		{Model: "claude-haiku-5-5", InputTokens: 1, CostUSD: usd(0.1)},
		{Model: "claude-opus-5-5", InputTokens: 4, OutputTokens: 5, CacheReadTokens: 100, CostUSD: usd(2.9)},
	}
	alias.SubagentCalls = 2
	alias.Checkpoint, alias.CheckpointSHA = "engine-error", "0a1b2c"
	// A Desk chat turn: no milestone.
	chat := call("c6", 6*time.Hour, 0, 0, "bloop-chat", 0, 1, "chat", "chat", "claude-sonnet-5-5", usd(0.1), 1, 1, 0, 0, 500)
	// Not in scope: expired, another project.
	old := call("c7", 3*time.Hour, 20, 148, "bloop-148", 2, 3, "fix", "fix", "claude-opus-5-5", usd(100), 1, 1, 1, 1, 1)
	old.ExpiresAt = expired
	other := call("c8", 3*time.Hour, 20, 148, "bloop-148", 2, 1, "implement", "implement", "claude-opus-5-5", usd(9), 1, 1, 1, 1, 1)
	other.Project = "other"
	if _, err := owner.InsertCalls(ctx, []store.Call{impl, review, fix, spec, alias, chat, old, other}); err != nil {
		t.Fatal(err)
	}
	finished := func(key string, at time.Duration, issue int64, task string, run int64, cost string, expiresAt time.Time) store.Event {
		return store.Event{Key: key, At: base.Add(at), Namespace: "agents", Project: "bloop", MilestoneIID: 20, IssueIID: issue,
			Task: task, Kind: "run.finished", Data: map[string]any{"costUSD": cost, "runCount": run}, ExpiresAt: expiresAt}
	}
	if _, err := owner.InsertEvents(ctx, []store.Event{
		// #148 run 1 is from before agent_calls: it counts.
		finished("e1", 0, 148, "bloop-148", 1, "2.0000", exp),
		// Run 2 has calls: its run.finished cost is theirs and does not count.
		finished("e2", 4*time.Hour, 148, "bloop-148", 2, "1.7300", exp),
		finished("e3", 0, 148, "bloop-148", 7, "50.0000", expired),
		{Key: "e4", At: base, Namespace: "agents", Project: "bloop", MilestoneIID: 20, IssueIID: 148, Task: "bloop-148",
			Kind: "run.finished", Data: map[string]any{"runCount": 9, "result": "Failed"}, ExpiresAt: exp},
	}); err != nil {
		t.Fatal(err)
	}

	spend := func(sc store.SpendScope) store.Spend {
		t.Helper()
		sc.Namespace, sc.Project = "agents", "bloop"
		sp, err := s.Spend(ctx, sc)
		if err != nil {
			t.Fatal(err)
		}
		return sp
	}
	type child struct {
		level, key, label string
		cost, legacy      float64
		known             bool
		calls, unknown    int64
	}
	checkChildren := func(what string, got []store.SpendChild, want []child) {
		t.Helper()
		var g []child
		for _, c := range got {
			g = append(g, child{c.Level, c.Key, c.Label, c.Totals.CostUSD, c.Totals.LegacyCostUSD, c.Totals.CostKnown,
				c.Totals.Calls, c.Totals.UnknownCalls})
		}
		if !slices.Equal(g, want) {
			t.Errorf("%s children:\n got %+v\nwant %+v", what, g, want)
		}
	}
	checkModels := func(what string, got []store.ModelSplit, want []store.ModelSplit) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s models:\n got %+v\nwant %+v", what, got, want)
		}
	}
	type breakdown struct {
		key   string
		cost  float64
		known bool
		calls int64
	}
	checkBreakdown := func(what string, got []store.SpendBreakdown, want []breakdown) {
		t.Helper()
		var g []breakdown
		for _, b := range got {
			g = append(g, breakdown{b.Key, b.Totals.CostUSD, b.Totals.CostKnown, b.Totals.Calls})
		}
		if !slices.Equal(g, want) {
			t.Errorf("%s:\n got %+v\nwant %+v", what, g, want)
		}
	}

	// The project: its milestones and the calls of none.
	sp := spend(store.SpendScope{})
	want := store.SpendTotals{CostUSD: 7.08, CostKnown: true, UnknownCalls: 1,
		InputTokens: 217, OutputTokens: 88, CacheReadTokens: 1133, CacheWriteTokens: 44,
		Calls: 6, SubagentCalls: 3, DurationMS: 96500, LegacyCostUSD: 2, Checkpoints: 3}
	if sp.Totals != want {
		t.Errorf("project totals:\n got %+v\nwant %+v", sp.Totals, want)
	}
	if w := 1133.0 / (217 + 1133 + 44); sp.CacheReadShare != w {
		t.Errorf("cache-read share = %v, want %v", sp.CacheReadShare, w)
	}
	checkChildren("project", sp.Children, []child{
		{"milestone", "20", "%20", 3.98, 2, true, 4, 1},
		{"milestone", "21", "%21", 3, 0, true, 1, 0},
		{"milestone", "", "No milestone", 0.1, 0, true, 1, 0},
	})
	checkModels("%20", sp.Children[0].Models, []store.ModelSplit{
		{Model: "claude-sonnet-5-5", Main: true, Tokens: 1110, CostUSD: 0.5, CostKnown: true},
		{Model: "gpt-5", Main: true, Tokens: 150},
		{Model: "claude-opus-5-5", Main: true, Tokens: 103, CostUSD: 1.45, CostKnown: true},
		{Model: "claude-haiku-5-5", Tokens: 7, CostUSD: 0.03, CostKnown: true},
	})
	checkModels("%21", sp.Children[1].Models, []store.ModelSplit{
		{Model: "claude-opus-5-5", Main: true, Tokens: 109, CostUSD: 2.9, CostKnown: true},
		{Model: "claude-haiku-5-5", Tokens: 1, CostUSD: 0.1, CostKnown: true},
	})
	if !sp.Children[0].At.Equal(base) {
		t.Errorf("%%20 at %v, want its first run %v", sp.Children[0].At, base)
	}
	checkBreakdown("project by model", sp.ByModel, []breakdown{
		{"claude-opus-5-5", 4.35, true, 3},
		{"claude-sonnet-5-5", 0.6, true, 2},
		{"claude-haiku-5-5", 0.13, true, 2},
		{"gpt-5", 0, false, 1},
	})
	// The calls that saved a checkpoint, by reason, the most frequent first.
	checkBreakdown("project by checkpoint", sp.ByCheckpoint, []breakdown{
		{"usage-limit", 1.23, true, 2},
		{"engine-error", 3, true, 1},
	})
	// Subagent calls and duration go to the main model.
	if b := sp.ByModel[0].Totals; b.SubagentCalls != 3 || b.DurationMS != 81000 {
		t.Errorf("opus subagent calls %d, duration %d; want 3, 81000", b.SubagentCalls, b.DurationMS)
	}

	// Milestone 20: its issues and its own calls.
	sp = spend(store.SpendScope{MilestoneIID: 20})
	if sp.Totals.CostUSD != 3.98 || sp.Totals.LegacyCostUSD != 2 || sp.Totals.Calls != 4 || sp.Totals.UnknownCalls != 1 {
		t.Errorf("%%20 totals = %+v", sp.Totals)
	}
	checkChildren("%20", sp.Children, []child{
		{"issue", "148", "#148", 3.73, 2, true, 2, 0},
		{"issue", "150", "#150", 0, 0, false, 1, 1},
		{"milestone", "20", "Milestone %20", 0.25, 0, true, 1, 0},
	})
	// Each issue's calls by kind, the most expensive first (the budget
	// alert names them).
	checkBreakdown("#148 kinds", sp.Children[0].Kinds, []breakdown{{"implement", 1.23, true, 1}, {"review", 0.5, true, 1}})
	checkBreakdown("#150 kinds", sp.Children[1].Kinds, []breakdown{{"fix", 0, false, 1}})
	checkBreakdown("%20 by kind", sp.ByKind, []breakdown{
		{"implement", 1.23, true, 1}, {"review", 0.5, true, 1}, {"spec", 0.25, true, 1}, {"fix", 0, false, 1}})
	checkBreakdown("%20 by role", sp.ByRole, []breakdown{
		{"implement", 1.23, true, 1}, {"security", 0.5, true, 1}, {"spec", 0.25, true, 1}, {"fix", 0, false, 1}})
	checkBreakdown("%20 by model", sp.ByModel, []breakdown{
		{"claude-opus-5-5", 1.45, true, 2}, {"claude-sonnet-5-5", 0.5, true, 1},
		{"claude-haiku-5-5", 0.03, true, 1}, {"gpt-5", 0, false, 1}})

	// #148: its runs, the legacy one without tokens.
	sp = spend(store.SpendScope{MilestoneIID: 20, IssueIID: 148})
	if want := (store.SpendTotals{CostUSD: 3.73, CostKnown: true, InputTokens: 110, OutputTokens: 30,
		CacheReadTokens: 1030, CacheWriteTokens: 40, Calls: 2, SubagentCalls: 1, DurationMS: 70000, LegacyCostUSD: 2, Checkpoints: 1}); sp.Totals != want {
		t.Errorf("#148 totals:\n got %+v\nwant %+v", sp.Totals, want)
	}
	checkChildren("#148", sp.Children, []child{
		{"run", "bloop-148/1", "bloop-148 run 1", 2, 2, true, 0, 0},
		{"run", "bloop-148/2", "bloop-148 run 2", 1.73, 0, true, 2, 0},
	})
	if c := sp.Children[0]; c.Totals.Tokens() != 0 || c.Models != nil || !c.At.Equal(base) {
		t.Errorf("legacy run = %+v; want no tokens or models, at %v", c, base)
	}
	checkModels("run 2", sp.Children[1].Models, []store.ModelSplit{
		{Model: "claude-sonnet-5-5", Main: true, Tokens: 1110, CostUSD: 0.5, CostKnown: true},
		{Model: "claude-opus-5-5", Main: true, Tokens: 93, CostUSD: 1.2, CostKnown: true},
		{Model: "claude-haiku-5-5", Tokens: 7, CostUSD: 0.03, CostKnown: true},
	})

	// #150 has only an unknown cursor call.
	sp = spend(store.SpendScope{IssueIID: 150})
	if sp.Totals.CostKnown || sp.Totals.UnknownCalls != 1 || sp.Totals.CostUSD != 0 || sp.Totals.Calls != 1 {
		t.Errorf("#150 totals = %+v; want unknown", sp.Totals)
	}
	checkModels("#150 run", sp.Children[0].Models, []store.ModelSplit{{Model: "gpt-5", Main: true, Tokens: 150}})

	// A run: its calls by seq, with their rows.
	sp = spend(store.SpendScope{Task: "bloop-148", Run: 2})
	if sp.Totals.CostUSD != 1.73 || sp.Totals.LegacyCostUSD != 0 || sp.Totals.Calls != 2 {
		t.Errorf("run 2 totals = %+v", sp.Totals)
	}
	checkChildren("run 2", sp.Children, []child{
		{"call", "1", "implement", 1.23, 0, true, 1, 0},
		{"call", "2", "review (security)", 0.5, 0, true, 1, 0},
	})
	if c := sp.Children[0].Call; c == nil || c.Key != "c1" || c.MilestoneIID != 20 || !slices.Equal(c.ContextSeries, []int64{1000, 2500, 4000}) ||
		!slices.Equal(c.SubagentTurns, []int32{1}) || c.ReadGateBlocks != 3 || len(c.Models) != 2 || c.CostUSD == nil || *c.CostUSD != 1.23 ||
		c.Checkpoint != "usage-limit" || c.CheckpointSHA != "abc123" || c.LinesAdded != 120 || c.LinesDeleted != 7 {
		t.Errorf("call 1 row = %+v", c)
	}
	if c := sp.Children[1].Call; c == nil || c.Checkpoint != "" || c.CheckpointSHA != "" || c.LinesAdded != 0 || c.LinesDeleted != 0 {
		t.Errorf("call 2 (a review) row = %+v; want no checkpoint or lines", c)
	}
	checkBreakdown("run 2 by checkpoint", sp.ByCheckpoint, []breakdown{{"usage-limit", 1.23, true, 1}})
	checkModels("call 1", sp.Children[0].Models, []store.ModelSplit{
		{Model: "claude-opus-5-5", Main: true, Tokens: 93, CostUSD: 1.2, CostKnown: true},
		{Model: "claude-haiku-5-5", Tokens: 7, CostUSD: 0.03, CostKnown: true},
	})
	checkModels("call 2", sp.Children[1].Models, []store.ModelSplit{
		{Model: "claude-sonnet-5-5", Main: true, Tokens: 1110, CostUSD: 0.5, CostKnown: true}})

	// A task: all its calls, and its legacy run in the totals only.
	sp = spend(store.SpendScope{Task: "bloop-148"})
	if sp.Totals.CostUSD != 3.73 || sp.Totals.LegacyCostUSD != 2 || len(sp.Children) != 2 {
		t.Errorf("task totals = %+v, %d children; want 3.73 with 2 legacy, 2 calls", sp.Totals, len(sp.Children))
	}

	// An empty scope cost nothing, which is known.
	sp = spend(store.SpendScope{MilestoneIID: 99})
	if (sp.Totals != store.SpendTotals{CostKnown: true}) || len(sp.Children) != 0 || len(sp.ByModel) != 0 || sp.CacheReadShare != 0 {
		t.Errorf("empty scope = %+v", sp)
	}

	// The totals of the chips: the same sums, without children.
	for _, sc := range []store.SpendScope{{}, {MilestoneIID: 20}, {Task: "bloop-148"}, {IssueIID: 150}} {
		want := spend(sc).Totals
		sc.Namespace, sc.Project = "agents", "bloop"
		if got, err := s.SpendTotals(ctx, sc); err != nil || got != want {
			t.Errorf("SpendTotals(%+v) = %+v, %v; want %+v", sc, got, err, want)
		}
	}
	byMilestone, err := s.SpendChildTotals(ctx, store.SpendScope{Namespace: "agents", Project: "bloop"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byMilestone) != 3 || byMilestone["20"].CostUSD != 3.98 || byMilestone["20"].LegacyCostUSD != 2 ||
		byMilestone["21"].CostUSD != 3 || byMilestone[""].CostUSD != 0.1 {
		t.Errorf("SpendChildTotals by milestone = %+v", byMilestone)
	}

	for _, sc := range []store.SpendScope{{Namespace: "agents"}, {Namespace: "agents", Project: "bloop", Run: 2}} {
		if _, err := s.Spend(ctx, sc); err == nil {
			t.Errorf("Spend(%+v) succeeded, want an error", sc)
		}
		if _, err := s.SpendTotals(ctx, sc); err == nil {
			t.Errorf("SpendTotals(%+v) succeeded, want an error", sc)
		}
		if _, err := s.SpendChildTotals(ctx, sc); err == nil {
			t.Errorf("SpendChildTotals(%+v) succeeded, want an error", sc)
		}
	}
}

// TestSpendOneTokenBasis (R6): the runner records a call's tokens as the
// sum of its models, so totals, by issue, by kind and by model show the
// same token total, with subagents and with a call without a split.
func TestSpendOneTokenBasis(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	at, exp := time.Now().Add(-time.Hour).Truncate(time.Second), time.Now().Add(24*time.Hour)
	usd := func(v float64) *float64 { return &v }
	call := func(key string, issue int64, kind, engine, model string, cost *float64, models ...store.CallModel) store.Call {
		c := store.Call{Key: key, At: at, Namespace: "agents", Project: "basis", MilestoneIID: 30, IssueIID: issue,
			Task: "basis-" + key, Run: 1, Seq: 1, Kind: kind, Role: kind, Engine: engine, Model: model,
			CostUSD: cost, CostSource: "reported", SubagentCalls: 1, Models: models, ExpiresAt: exp}
		if cost == nil {
			c.CostSource = "unknown"
		}
		for _, m := range models {
			c.InputTokens, c.OutputTokens = c.InputTokens+m.InputTokens, c.OutputTokens+m.OutputTokens
			c.CacheReadTokens, c.CacheWriteTokens = c.CacheReadTokens+m.CacheReadTokens, c.CacheWriteTokens+m.CacheWriteTokens
		}
		return c
	}
	// The R5 fixture's call: a general-purpose subagent on the main model.
	impl := call("b1", 301, "implement", "claude", "claude-opus-5-5", usd(0.2104),
		store.CallModel{Model: "claude-opus-5-5", InputTokens: 14, OutputTokens: 460, CacheReadTokens: 39000, CacheWriteTokens: 16500, CostUSD: usd(0.2104)})
	// A review with a haiku subagent.
	review := call("b2", 302, "review", "claude", "claude-sonnet-5-5", usd(0.6),
		store.CallModel{Model: "claude-sonnet-5-5", InputTokens: 100, OutputTokens: 20, CacheReadTokens: 1000, CostUSD: usd(0.55)},
		store.CallModel{Model: "claude-haiku-5-5", InputTokens: 10, OutputTokens: 5, CacheWriteTokens: 300, CostUSD: usd(0.05)})
	// A cursor call: no split, its own usage.
	fix := call("b3", 302, "fix", "cursor", "auto", nil)
	fix.InputTokens, fix.OutputTokens, fix.SubagentCalls = 50, 10, 0
	// The milestone's spec: no issue.
	spec := call("b4", 0, "spec", "claude", "claude-opus-5-5", usd(0.1),
		store.CallModel{Model: "claude-opus-5-5", InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4, CostUSD: usd(0.1)})
	if _, err := owner.InsertCalls(ctx, []store.Call{impl, review, fix, spec}); err != nil {
		t.Fatal(err)
	}
	const want = 55974 + 1435 + 60 + 10
	sp, err := s.Spend(ctx, store.SpendScope{Namespace: "agents", Project: "basis", MilestoneIID: 30})
	if err != nil {
		t.Fatal(err)
	}
	if got := sp.Totals.Tokens(); got != want {
		t.Errorf("totals: %d tokens, want %d", got, want)
	}
	sum := func(what string, n int, tokens func(i int) int64) {
		t.Helper()
		var got int64
		for i := range n {
			got += tokens(i)
		}
		if got != want {
			t.Errorf("%s: %d tokens, want the total %d", what, got, want)
		}
	}
	sum("by issue", len(sp.Children), func(i int) int64 { return sp.Children[i].Totals.Tokens() })
	sum("by kind", len(sp.ByKind), func(i int) int64 { return sp.ByKind[i].Totals.Tokens() })
	sum("by model", len(sp.ByModel), func(i int) int64 { return sp.ByModel[i].Totals.Tokens() })
	var split []store.ModelSplit
	for _, c := range sp.Children {
		split = append(split, c.Models...)
	}
	sum("the issues' model bars", len(split), func(i int) int64 { return split[i].Tokens })
	if len(sp.Children) != 3 || len(sp.ByKind) != 4 || len(sp.ByModel) != 4 {
		t.Errorf("%d issues, %d kinds, %d models; want 3, 4, 4", len(sp.Children), len(sp.ByKind), len(sp.ByModel))
	}
}

// TestSpendUnmetered (R8): a cursor call with model auto reports neither a
// cost nor tokens. It is counted as unmetered, its model split is marked so,
// and costKnown keeps its meaning.
func TestSpendUnmetered(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	at, exp := time.Now().Add(-time.Hour).Truncate(time.Second), time.Now().Add(24*time.Hour)
	cost := 0.5
	claude := store.Call{Key: "u1", At: at, Namespace: "agents", Project: "unmetered", MilestoneIID: 6, IssueIID: 401,
		Task: "unmetered-u1", Run: 1, Seq: 1, Kind: "implement", Role: "implement", Engine: "claude",
		Model: "claude-opus-5-5", InputTokens: 10, OutputTokens: 20, CacheReadTokens: 300, CostUSD: &cost,
		CostSource: "reported", ExpiresAt: exp}
	cursor := store.Call{Key: "u2", At: at.Add(time.Minute), Namespace: "agents", Project: "unmetered", MilestoneIID: 6,
		IssueIID: 402, Task: "unmetered-u2", Run: 1, Seq: 1, Kind: "review", Role: "bugbot", Engine: "cursor",
		Model: "auto", CostSource: "unknown", ExpiresAt: exp}
	if _, err := owner.InsertCalls(ctx, []store.Call{claude, cursor}); err != nil {
		t.Fatal(err)
	}
	sp, err := s.Spend(ctx, store.SpendScope{Namespace: "agents", Project: "unmetered", MilestoneIID: 6})
	if err != nil {
		t.Fatal(err)
	}
	if got := sp.Totals; got.UnmeteredCalls != 1 || got.UnknownCalls != 1 || got.Calls != 2 || !got.CostKnown || got.CostUSD != 0.5 {
		t.Errorf("totals = %+v, want 2 calls, 1 unknown, 1 unmetered, $0.50 known", got)
	}
	splits := map[string]store.ModelSplit{}
	for _, c := range sp.Children {
		for _, m := range c.Models {
			splits[m.Model] = m
		}
	}
	if m := splits["auto"]; !m.Unmetered || m.Tokens != 0 || m.CostKnown {
		t.Errorf("auto split = %+v, want unmetered without tokens or cost", m)
	}
	if m := splits["claude-opus-5-5"]; m.Unmetered || !m.CostKnown {
		t.Errorf("opus split = %+v, want metered", m)
	}
	for _, b := range sp.ByModel {
		want := int64(0)
		if b.Key == "auto" {
			want = 1
		}
		if b.Totals.UnmeteredCalls != want {
			t.Errorf("by model %s: %d unmetered, want %d", b.Key, b.Totals.UnmeteredCalls, want)
		}
	}
}

func TestSpendTuning(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	exp := time.Now().Add(24 * time.Hour)
	usd := func(v float64) *float64 { return &v }
	n := 0
	call := func(task string, run, seq int64, kind, role string, cost *float64, in, out int64) store.Call {
		n++
		source := "reported"
		if cost == nil {
			source = "unknown"
		}
		return store.Call{Key: "k" + strconv.Itoa(n), At: base.Add(time.Duration(n) * time.Minute), Namespace: "agents",
			Project: "bloop", MilestoneIID: 20, IssueIID: 148, Task: task, Run: run, Seq: seq, Kind: kind, Role: role,
			Engine: "claude", Model: "claude-opus-5-5", InputTokens: in, OutputTokens: out, CostUSD: cost,
			CostSource: source, ExpiresAt: exp}
	}
	resumed := func(c store.Call) store.Call { c.Resumed = true; return c }
	scouted := func(c store.Call, scoutTokens int64) store.Call {
		c.SubagentCalls = 1
		c.Models = []store.CallModel{
			{Model: "claude-opus-5-5", InputTokens: c.InputTokens - scoutTokens, OutputTokens: c.OutputTokens, CostUSD: c.CostUSD},
			{Model: "claude-haiku-5-5", InputTokens: scoutTokens},
		}
		return c
	}
	gated := func(c store.Call, blocks int32) store.Call { c.ReadGateBlocks = blocks; return c }
	calls := []store.Call{
		// Run 1: implement, two reviewers, fix, one reviewer, fix, two
		// reviewers. Inserted out of seq order: rounds follow seq.
		gated(call("bloop-148", 1, 9, "review", "security", usd(0.5), 50, 5), 1),
		call("bloop-148", 1, 1, "implement", "implement", usd(4), 400, 40),
		gated(scouted(resumed(call("bloop-148", 1, 2, "implement", "implement", usd(6), 600, 60)), 100), 3),
		call("bloop-148", 1, 3, "review", "security", usd(1), 100, 10),
		call("bloop-148", 1, 4, "review", "style", usd(2), 200, 20),
		call("bloop-148", 1, 5, "fix", "fix", usd(3), 300, 30),
		call("bloop-148", 1, 6, "review", "security", usd(0.75), 75, 5),
		call("bloop-148", 1, 7, "fix", "fix", usd(1), 100, 10),
		call("bloop-148", 1, 8, "review", "style", nil, 10, 1),
		// Run 2 starts at round 1 again; a ci call does not end a round.
		call("bloop-148", 2, 10, "review", "security", usd(0.25), 25, 2),
		call("bloop-148", 2, 11, "ci", "ci", usd(1), 100, 10),
		call("bloop-148", 2, 12, "review", "security", usd(0.25), 25, 2),
		call("bloop-148", 2, 13, "fix", "fix", usd(1), 100, 10),
		call("bloop-148", 2, 14, "review", "security", usd(0.5), 50, 5),
		resumed(call("bloop-148", 2, 15, "implement", "implement", usd(8), 800, 80)),
		scouted(call("bloop-148", 2, 16, "review", "security", usd(2), 300, 20), 200),
	}
	if _, err := owner.InsertCalls(ctx, calls); err != nil {
		t.Fatal(err)
	}
	sp, err := s.Spend(ctx, store.SpendScope{Namespace: "agents", Project: "bloop", MilestoneIID: 20})
	if err != nil {
		t.Fatal(err)
	}
	type breakdown struct {
		key            string
		cost           float64
		calls, unknown int64
		tokens         int64
	}
	check := func(what string, got []store.SpendBreakdown, want []breakdown) {
		t.Helper()
		var g []breakdown
		for _, b := range got {
			g = append(g, breakdown{b.Key, b.Totals.CostUSD, b.Totals.Calls, b.Totals.UnknownCalls, b.Totals.Tokens()})
		}
		if !slices.Equal(g, want) {
			t.Errorf("%s:\n got %+v\nwant %+v", what, g, want)
		}
	}
	// Round 1: run 1's seq 3 and 4, run 2's 10, 12. Round 2: run 1's 6,
	// run 2's 14, 16. Round 3: run 1's 8 (unknown cost) and 9.
	check("by review round", sp.ByReviewRound, []breakdown{
		{"1", 3.5, 4, 0, 384},
		{"2", 3.25, 3, 0, 455},
		{"3", 0.5, 2, 1, 66},
	})
	check("by session", sp.BySession, []breakdown{
		{"fresh", 4, 1, 0, 440},
		{"resumed", 14, 2, 0, 1540},
	})

	type subagents struct {
		role       string
		with       bool
		cost       float64
		calls      int64
		tokens     int64
		sub, scout int64
		blocks     int64
	}
	var g []subagents
	for _, b := range sp.BySubagents {
		g = append(g, subagents{b.Role, b.WithSubagents, b.Totals.CostUSD, b.Totals.Calls, b.Totals.Tokens(),
			b.Totals.SubagentCalls, b.SubagentTokens, b.ReadGateBlocks})
	}
	if want := []subagents{
		{"ci", false, 1, 1, 110, 0, 0, 0},
		{"fix", false, 5, 3, 550, 0, 0, 0},
		{"implement", false, 12, 2, 1320, 0, 0, 0},
		{"implement", true, 6, 1, 660, 1, 100, 3},
		{"security", false, 3.25, 6, 354, 0, 0, 1},
		{"security", true, 2, 1, 320, 1, 200, 0},
		{"style", false, 2, 2, 231, 0, 0, 0},
	}; !slices.Equal(g, want) {
		t.Errorf("by subagents:\n got %+v\nwant %+v", g, want)
	}

	// A scope without review or implement calls has no rounds or sessions.
	sp, err = s.Spend(ctx, store.SpendScope{Namespace: "agents", Project: "bloop", MilestoneIID: 21})
	if err != nil {
		t.Fatal(err)
	}
	if len(sp.ByReviewRound) != 0 || len(sp.BySession) != 0 || len(sp.BySubagents) != 0 {
		t.Errorf("empty scope = %+v", sp)
	}
}

// StepLines sums the scope's implement calls that left lines: 40, 100, 250
// and 1000 lines, so the median is 175 and the p90 775.
func TestStepLines(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	exp := time.Now().Add(24 * time.Hour)
	n := 0
	call := func(issue int64, kind string, added, deleted int64, nudges int32) store.Call {
		n++
		return store.Call{Key: "k" + strconv.Itoa(n), At: base.Add(time.Duration(n) * time.Minute), Namespace: "agents",
			Project: "bloop", MilestoneIID: 20, IssueIID: issue, Task: "bloop-" + strconv.FormatInt(issue, 10), Run: 1,
			Seq: int64(n), Kind: kind, Role: "implement", Engine: "claude", CostSource: "unknown", ExpiresAt: exp,
			LinesAdded: added, LinesDeleted: deleted, StepGateNudges: nudges}
	}
	calls := []store.Call{
		call(148, "implement", 100, 0, 0),
		call(148, "implement", 200, 50, 1),
		call(148, "implement", 30, 10, 0),
		call(148, "implement", 400, 600, 3),
		// No lines: a step that only answered.
		call(148, "implement", 0, 0, 0),
		// Not a step commit: a fix round and a review.
		call(148, "fix", 500, 0, 0),
		call(148, "review", 0, 0, 0),
		// Another issue.
		call(150, "implement", 9000, 999, 7),
	}
	if _, err := owner.InsertCalls(ctx, calls); err != nil {
		t.Fatal(err)
	}
	scope := store.SpendScope{Namespace: "agents", Project: "bloop", IssueIID: 148}
	want := store.StepLines{Count: 4, Median: 175, P90: 775, Max: 1000, Nudges: 4}
	if got, err := s.StepLines(ctx, scope); err != nil || got != want {
		t.Errorf("StepLines(#148) = %+v, %v; want %+v", got, err, want)
	}
	if sp, err := s.Spend(ctx, scope); err != nil || sp.StepLines != want {
		t.Errorf("Spend(#148).StepLines = %+v, %v; want %+v", sp.StepLines, err, want)
	}
	if got, err := s.StepLines(ctx, store.SpendScope{Namespace: "agents", Project: "bloop"}); err != nil ||
		got != (store.StepLines{Count: 5, Median: 250, P90: 6399, Max: 9999, Nudges: 11}) {
		t.Errorf("StepLines(project) = %+v, %v", got, err)
	}
	if got, err := s.StepLines(ctx, store.SpendScope{Namespace: "agents", Project: "bloop", IssueIID: 999}); err != nil || got != (store.StepLines{}) {
		t.Errorf("StepLines(no calls) = %+v, %v; want zeros", got, err)
	}
	// The call rows carry their nudges.
	sp, err := s.Spend(ctx, store.SpendScope{Namespace: "agents", Project: "bloop", IssueIID: 148, Task: "bloop-148"})
	if err != nil {
		t.Fatal(err)
	}
	if c := sp.Children[3].Call; c == nil || c.StepGateNudges != 3 {
		t.Errorf("call 4 row = %+v; want 3 nudges", c)
	}
	if c := sp.Children[0].Call; c == nil || c.StepGateNudges != 0 {
		t.Errorf("call 1 row = %+v; want no nudges", c)
	}
}

// TestSpendSince sums the calls and legacy runs of several projects from a
// time on, in one query: the overview's spend today and this week.
func TestSpendSince(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	now := time.Now().Truncate(time.Second)
	exp, expired := now.Add(24*time.Hour), now.Add(-time.Hour)
	usd := func(v float64) *float64 { return &v }
	call := func(key, project string, ago time.Duration, cost *float64, in int64) store.Call {
		source := "reported"
		if cost == nil {
			source = "unknown"
		}
		return store.Call{Key: key, At: now.Add(-ago), Namespace: "agents", Project: project, MilestoneIID: 1, IssueIID: 2,
			Task: project + "-2", Run: 1, Seq: 1, Kind: "implement", Role: "implement", Engine: "claude", Model: "claude-opus-5-5",
			InputTokens: in, CostUSD: cost, CostSource: source, ExpiresAt: exp}
	}
	old := call("s5", "shop", time.Hour, usd(100), 1)
	old.ExpiresAt = expired
	if _, err := owner.InsertCalls(ctx, []store.Call{
		call("s1", "shop", time.Hour, usd(1), 10),
		call("s2", "shop", 3*24*time.Hour, usd(2), 20),
		call("s3", "billing", 2*time.Hour, nil, 30),
		call("s4", "billing", 10*24*time.Hour, usd(4), 40),
		old,
		call("s6", "other", time.Hour, usd(8), 80),
	}); err != nil {
		t.Fatal(err)
	}
	// A run from before agent_calls: its cost without tokens.
	if _, err := owner.InsertEvents(ctx, []store.Event{{Key: "se1", At: now.Add(-30 * time.Minute), Namespace: "agents",
		Project: "billing", MilestoneIID: 1, IssueIID: 3, Task: "billing-3", Kind: "run.finished",
		Data: map[string]any{"costUSD": "0.5000", "runCount": 1}, ExpiresAt: exp}}); err != nil {
		t.Fatal(err)
	}
	both := []store.ProjectKey{{Namespace: "agents", Name: "shop"}, {Namespace: "agents", Name: "billing"}}
	for _, c := range []struct {
		what     string
		projects []store.ProjectKey
		since    time.Duration
		want     store.SpendTotals
	}{
		{"today", both, 12 * time.Hour, store.SpendTotals{CostUSD: 1.5, CostKnown: true, UnknownCalls: 1, UnmeteredCalls: 0,
			InputTokens: 40, Calls: 2, LegacyCostUSD: 0.5}},
		{"7 days", both, 7 * 24 * time.Hour, store.SpendTotals{CostUSD: 3.5, CostKnown: true, UnknownCalls: 1,
			InputTokens: 60, Calls: 3, LegacyCostUSD: 0.5}},
		{"one project", both[:1], 30 * 24 * time.Hour, store.SpendTotals{CostUSD: 3, CostKnown: true, InputTokens: 30, Calls: 2}},
		{"none", nil, time.Hour, store.SpendTotals{CostKnown: true}},
		{"nothing since", both, time.Minute, store.SpendTotals{CostKnown: true}},
	} {
		got, err := s.SpendSince(ctx, c.projects, now.Add(-c.since))
		if err != nil || got != c.want {
			t.Errorf("%s: SpendSince = %+v, %v\nwant %+v", c.what, got, err, c.want)
		}
	}
}

// TestSpendCallLessCheckpoints: checkpoint events (budget stop, lost pod)
// count as checkpoints at every scope, next to the calls that carry one,
// without adding calls; one a call carries counts once.
func TestSpendCallLessCheckpoints(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	at, exp := time.Now().Add(-time.Hour).Truncate(time.Second), time.Now().Add(24*time.Hour)
	usd := func(v float64) *float64 { return &v }
	call := func(key string, run, seq int64, kind string, cost float64) store.Call {
		return store.Call{Key: key, At: at, Namespace: "agents", Project: "cps", MilestoneIID: 40, IssueIID: 401,
			Task: "cps-401", Run: run, Seq: seq, Kind: kind, Role: kind, Engine: "claude", Model: "claude-opus-5-5",
			InputTokens: 10, OutputTokens: 10, CostUSD: usd(cost), CostSource: "reported", ExpiresAt: exp}
	}
	// Run 1: the implementer call ends on the usage limit with a checkpoint,
	// then a review. Run 2 starts after a lost pod: its call has none.
	limited := call("k1", 1, 1, "implement", 1)
	limited.Checkpoint, limited.CheckpointSHA = "usage-limit", "aaa111"
	if _, err := owner.InsertCalls(ctx, []store.Call{limited, call("k2", 1, 2, "review", 0.5), call("k3", 2, 3, "implement", 2)}); err != nil {
		t.Fatal(err)
	}
	event := func(key string, run int64, reason, sha string, expires time.Time) store.Event {
		return store.Event{Key: key, At: at.Add(time.Minute), Namespace: "agents", Project: "cps", MilestoneIID: 40, IssueIID: 401,
			MergeRequestIID: 9, Task: "cps-401", Kind: "checkpoint", Summary: "Checkpoint",
			Data: map[string]any{"reason": reason, "sha": sha, "run": run, "step": 2, "added": 3, "deleted": 0}, ExpiresAt: expires}
	}
	if _, err := owner.InsertEvents(ctx, []store.Event{
		// The new pod saved the lost call's work of run 2.
		event("task/u/checkpoint/2/bbb222", 2, "pod-lost", "bbb222", exp),
		// Expired, and one a call carries (counted with the call): neither counts.
		event("task/u/checkpoint/1/ccc333", 1, "budget-stop", "ccc333", time.Now().Add(-time.Minute)),
		event("task/u/checkpoint/1/aaa111", 1, "usage-limit", "aaa111", exp),
	}); err != nil {
		t.Fatal(err)
	}
	spend := func(sc store.SpendScope) store.Spend {
		t.Helper()
		sc.Namespace, sc.Project = "agents", "cps"
		sp, err := s.Spend(ctx, sc)
		if err != nil {
			t.Fatal(err)
		}
		return sp
	}
	type count struct {
		key          string
		count, calls int64
	}
	for _, sc := range []store.SpendScope{{}, {MilestoneIID: 40}, {MilestoneIID: 40, IssueIID: 401}, {Task: "cps-401"}} {
		sp := spend(sc)
		if sp.Totals.Checkpoints != 2 || sp.Totals.Calls != 3 || sp.Totals.CostUSD != 3.5 || sp.Totals.UnknownCalls != 0 ||
			!sp.Totals.CostKnown || sp.Totals.LegacyCostUSD != 0 {
			t.Errorf("%+v totals = %+v; want 2 checkpoints, 3 calls, $3.50", sc, sp.Totals)
		}
		var got []count
		for _, b := range sp.ByCheckpoint {
			got = append(got, count{b.Key, b.Count, b.Totals.Calls})
		}
		// Equal counts go by reason; only the usage limit has a call.
		if want := []count{{"pod-lost", 1, 0}, {"usage-limit", 1, 1}}; !slices.Equal(got, want) {
			t.Errorf("%+v by checkpoint = %+v, want %+v", sc, got, want)
		}
		var kinds []string
		for _, b := range sp.ByKind {
			kinds = append(kinds, b.Key)
		}
		if !slices.Equal(kinds, []string{"implement", "review"}) {
			t.Errorf("%+v by kind = %v; the checkpoint event is no call", sc, kinds)
		}
	}
	// The children have them too: the milestone, the issue, the run.
	if c := spend(store.SpendScope{}).Children; len(c) != 1 || c[0].Totals.Checkpoints != 2 || c[0].Totals.Calls != 3 {
		t.Errorf("project children = %+v", c)
	}
	runs := spend(store.SpendScope{IssueIID: 401}).Children
	if len(runs) != 2 || runs[0].Key != "cps-401/1" || runs[0].Totals.Checkpoints != 1 || runs[0].Totals.Calls != 2 ||
		runs[1].Key != "cps-401/2" || runs[1].Totals.Checkpoints != 1 || runs[1].Totals.Calls != 1 {
		t.Errorf("#401 runs = %+v", runs)
	}
	// A run's children are its calls only; its totals count the event.
	sp := spend(store.SpendScope{Task: "cps-401", Run: 2})
	if sp.Totals.Checkpoints != 1 || sp.Totals.Calls != 1 || len(sp.Children) != 1 || sp.Children[0].Totals.Checkpoints != 0 {
		t.Errorf("run 2 = %+v, children %+v", sp.Totals, sp.Children)
	}
	if got, err := s.SpendTotals(ctx, store.SpendScope{Namespace: "agents", Project: "cps", MilestoneIID: 40}); err != nil || got.Checkpoints != 2 || got.Calls != 3 {
		t.Errorf("SpendTotals = %+v, %v", got, err)
	}
	if got, err := s.SpendSince(ctx, []store.ProjectKey{{Namespace: "agents", Name: "cps"}}, at.Add(-time.Minute)); err != nil || got.Checkpoints != 2 || got.Calls != 3 {
		t.Errorf("SpendSince = %+v, %v", got, err)
	}
	if byIssue, err := s.SpendChildTotals(ctx, store.SpendScope{Namespace: "agents", Project: "cps", MilestoneIID: 40}); err != nil || byIssue["401"].Checkpoints != 2 {
		t.Errorf("SpendChildTotals = %+v, %v", byIssue, err)
	}
}

// TestSpendAddsUpAtEveryScope (#255): the per-model split uses the row
// tokens' definition, so at every scope the token sums of ByModel, ByKind,
// ByRole, the children and each child's models equal the total, and
// ByModel's cost is the total without the legacy runs. The mix: a claude
// call with an Opus main model and a Sonnet subagent, a resumed call, a row
// from before the fix whose models (carried over by --resume) exceed its
// tokens, a cursor auto call without usage and a legacy run without calls.
func TestSpendAddsUpAtEveryScope(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	at, exp := time.Now().Add(-time.Hour).Truncate(time.Second), time.Now().Add(24*time.Hour)
	usd := func(v float64) *float64 { return &v }
	n := 0
	call := func(issue int64, task string, run, seq int64, kind, role, engine, model string, cost *float64, models ...store.CallModel) store.Call {
		n++
		c := store.Call{Key: "a" + strconv.Itoa(n), At: at.Add(time.Duration(n) * time.Minute), Namespace: "agents", Project: "addsup",
			MilestoneIID: 40, IssueIID: issue, Task: task, Run: run, Seq: seq, Kind: kind, Role: role, Engine: engine, Model: model,
			CostUSD: cost, CostSource: "reported", DurationMS: 1000, Models: models, ExpiresAt: exp}
		if cost == nil {
			c.CostSource = "unknown"
		}
		for _, m := range models {
			c.InputTokens, c.OutputTokens = c.InputTokens+m.InputTokens, c.OutputTokens+m.OutputTokens
			c.CacheReadTokens, c.CacheWriteTokens = c.CacheReadTokens+m.CacheReadTokens, c.CacheWriteTokens+m.CacheWriteTokens
		}
		return c
	}
	opus, sonnet := "claude-opus-5-5", "claude-sonnet-5-5"
	// #501 run 1: an implement step with a Sonnet subagent, then a resumed
	// step that records its own delta (since #255).
	impl := call(501, "addsup-501", 1, 1, "implement", "implement", "claude", opus, usd(0.9),
		store.CallModel{Model: opus, InputTokens: 10, OutputTokens: 500, CacheReadTokens: 20000, CacheWriteTokens: 3000, CostUSD: usd(0.8)},
		store.CallModel{Model: sonnet, InputTokens: 5, OutputTokens: 100, CacheReadTokens: 2000, CacheWriteTokens: 500, CostUSD: usd(0.1)})
	impl.SubagentCalls = 1
	resumed := call(501, "addsup-501", 1, 2, "implement", "implement", "claude", opus, usd(0.3),
		store.CallModel{Model: opus, InputTokens: 2, OutputTokens: 200, CacheReadTokens: 25000, CacheWriteTokens: 400, CostUSD: usd(0.3)})
	resumed.Resumed = true
	// #501 run 2: a fix from before the fix. Its row holds usage (the main
	// thread of this call), its models the session's carried-over totals,
	// with a haiku that ran in an earlier call.
	prefix := call(501, "addsup-501", 2, 1, "fix", "fix", "claude", opus, usd(1.5))
	prefix.Resumed = true
	prefix.InputTokens, prefix.OutputTokens, prefix.CacheReadTokens, prefix.CacheWriteTokens = 0, 50, 4000, 100
	prefix.Models = []store.CallModel{
		{Model: opus, InputTokens: 100, OutputTokens: 2000, CacheReadTokens: 300000, CacheWriteTokens: 40000, CostUSD: usd(1.4)},
		{Model: "claude-haiku-5-5", InputTokens: 50, OutputTokens: 500, CacheReadTokens: 10000, CacheWriteTokens: 5000, CostUSD: usd(0.1)},
	}
	// #502: a cursor auto review that reports neither cost nor tokens.
	auto := call(502, "addsup-502", 1, 1, "review", "bugbot", "cursor", "auto", nil)
	if _, err := owner.InsertCalls(ctx, []store.Call{impl, resumed, prefix, auto}); err != nil {
		t.Fatal(err)
	}
	// #502 run 7 is from before agent_calls: a cost without tokens.
	if _, err := owner.InsertEvents(ctx, []store.Event{{Key: "legacy", At: at, Namespace: "agents", Project: "addsup",
		MilestoneIID: 40, IssueIID: 502, Task: "addsup-502", Kind: "run.finished",
		Data: map[string]any{"costUSD": "2.0000", "runCount": 7}, ExpiresAt: exp}}); err != nil {
		t.Fatal(err)
	}

	const wantTokens = 26115 + 25602 + 4150
	for _, sc := range []store.SpendScope{{}, {MilestoneIID: 40}, {MilestoneIID: 40, IssueIID: 501}, {IssueIID: 502},
		{Task: "addsup-501"}, {Task: "addsup-501", Run: 2}} {
		sc.Namespace, sc.Project = "agents", "addsup"
		sp, err := s.Spend(ctx, sc)
		if err != nil {
			t.Fatal(err)
		}
		total := sp.Totals.Tokens()
		if sc.IssueIID == 0 && sc.Task == "" && total != wantTokens {
			t.Errorf("%+v: %d tokens, want %d", sc, total, wantTokens)
		}
		sum := func(what string, bs []store.SpendBreakdown) {
			t.Helper()
			var got int64
			for _, b := range bs {
				got += b.Totals.Tokens()
			}
			if got != total {
				t.Errorf("%+v %s: %d tokens, want the total %d", sc, what, got, total)
			}
		}
		sum("by model", sp.ByModel)
		sum("by kind", sp.ByKind)
		sum("by role", sp.ByRole)
		var children int64
		for _, c := range sp.Children {
			children += c.Totals.Tokens()
			var models int64
			for _, m := range c.Models {
				models += m.Tokens
			}
			if models != c.Totals.Tokens() {
				t.Errorf("%+v child %s: models %d tokens, want its total %d (%+v)", sc, c.Label, models, c.Totals.Tokens(), c.Models)
			}
		}
		if children != total {
			t.Errorf("%+v children: %d tokens, want the total %d", sc, children, total)
		}
		var cost float64
		for _, b := range sp.ByModel {
			cost += b.Totals.CostUSD
		}
		if want := sp.Totals.CostUSD - sp.Totals.LegacyCostUSD; math.Abs(cost-want) > 1e-9 {
			t.Errorf("%+v by model: $%v, want the total without legacy runs $%v", sc, cost, want)
		}
	}

	// The milestone by model: the pre-fix row goes to Opus whole, its
	// carried-over haiku is gone; auto stays a row, unmetered.
	sp, err := s.Spend(ctx, store.SpendScope{Namespace: "agents", Project: "addsup", MilestoneIID: 40})
	if err != nil {
		t.Fatal(err)
	}
	type model struct {
		key       string
		tokens    int64
		cost      float64
		unmetered int64
	}
	var got []model
	for _, b := range sp.ByModel {
		got = append(got, model{b.Key, b.Totals.Tokens(), math.Round(b.Totals.CostUSD*1e4) / 1e4, b.Totals.UnmeteredCalls})
	}
	want := []model{{opus, 23510 + 25602 + 4150, 2.6, 0}, {sonnet, 2605, 0.1, 0}, {"auto", 0, 0, 1}}
	if !slices.Equal(got, want) {
		t.Errorf("by model:\n got %+v\nwant %+v", got, want)
	}
	if sp.Totals.UnmeteredCalls != 1 || sp.Totals.LegacyCostUSD != 2 || math.Abs(sp.Totals.CostUSD-4.7) > 1e-9 {
		t.Errorf("totals %+v, want 1 unmetered call, $2 legacy of $4.70", sp.Totals)
	}
}

// TestSpendKindModel: a milestone with implement, review, fix and two
// conflict calls of one run (Opus) and one of another run (Sonnet). The kind
// × model rows keep the conflict calls apart per model, ConflictRuns shows the
// run that resolved conflicts twice, and both add up to the totals. Then
// hack/spend-report.sql runs on the same data.
func TestSpendKindModel(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	at, exp := time.Now().Add(-time.Hour).Truncate(time.Second), time.Now().Add(24*time.Hour)
	opus, sonnet := "claude-opus-5-5", "claude-sonnet-5-5"
	n := 0
	call := func(issue int64, task string, run, seq int64, kind, model string, in, out, cr, cw int64, cost float64) store.Call {
		n++
		return store.Call{Key: "k" + strconv.Itoa(n), At: at.Add(time.Duration(n) * time.Minute), Namespace: "agents", Project: "kmx",
			MilestoneIID: 50, IssueIID: issue, Task: task, Run: run, Seq: seq, Kind: kind, Role: kind, Engine: "claude", Model: model,
			InputTokens: in, OutputTokens: out, CacheReadTokens: cr, CacheWriteTokens: cw, CostUSD: &cost, CostSource: "reported",
			DurationMS: 1000, Models: []store.CallModel{{Model: model, InputTokens: in, OutputTokens: out, CacheReadTokens: cr,
				CacheWriteTokens: cw, CostUSD: &cost}}, ExpiresAt: exp}
	}
	impl := call(601, "kmx-601", 1, 1, "implement", opus, 10, 500, 20000, 3000, 1.0)
	// A subagent: a Sonnet share of the implement call.
	impl.Models = append(impl.Models, store.CallModel{Model: sonnet, InputTokens: 5, OutputTokens: 50, CacheReadTokens: 1000, CacheWriteTokens: 100})
	impl.InputTokens, impl.OutputTokens, impl.CacheReadTokens, impl.CacheWriteTokens = 15, 550, 21000, 3100
	if _, err := owner.InsertCalls(ctx, []store.Call{
		impl,
		call(601, "kmx-601", 1, 2, "review", opus, 4, 300, 9000, 100, 0.4),
		call(601, "kmx-601", 1, 3, "fix", opus, 3, 200, 8000, 50, 0.3),
		call(601, "kmx-601", 1, 4, "conflict", opus, 2, 4000, 30000, 7000, 2.0),
		call(601, "kmx-601", 1, 5, "conflict", opus, 2, 3000, 31000, 1000, 1.5),
		call(602, "kmx-602", 1, 1, "conflict", sonnet, 1, 800, 9000, 2000, 0.2),
	}); err != nil {
		t.Fatal(err)
	}

	sp, err := s.Spend(ctx, store.SpendScope{Namespace: "agents", Project: "kmx", MilestoneIID: 50})
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]store.SpendBreakdown{}
	var sum int64
	for _, b := range sp.ByKindModel {
		byKey[b.Key] = b
		sum += b.Totals.Tokens()
	}
	if sum != sp.Totals.Tokens() {
		t.Errorf("by kind and model: %d tokens, want the total %d", sum, sp.Totals.Tokens())
	}
	co, cs := byKey["conflict/"+opus], byKey["conflict/"+sonnet]
	if co.Totals.Calls != 2 || co.Totals.OutputTokens != 7000 || co.Totals.CacheWriteTokens != 8000 || math.Abs(co.Totals.CostUSD-3.5) > 1e-9 ||
		cs.Totals.Calls != 1 || cs.Totals.OutputTokens != 800 || cs.Totals.CacheWriteTokens != 2000 {
		t.Errorf("conflict rows: opus %+v, sonnet %+v", co.Totals, cs.Totals)
	}
	if sub := byKey["implement/"+sonnet]; sub.Totals.OutputTokens != 50 || byKey["implement/"+opus].Totals.OutputTokens != 500 {
		t.Errorf("implement rows split by model: %+v", byKey)
	}
	if len(sp.ByKindModel) == 0 || sp.ByKindModel[0].Key != "conflict/"+opus {
		t.Errorf("by kind and model is not sorted by cost: %+v", sp.ByKindModel)
	}
	var runs []string
	for _, r := range sp.ConflictRuns {
		runs = append(runs, r.Key+"="+strconv.FormatInt(r.Totals.Calls, 10))
	}
	if want := []string{"601/kmx-601/1=2", "602/kmx-602/1=1"}; !slices.Equal(runs, want) {
		t.Errorf("conflict runs %v, want %v", runs, want)
	}
	// Only milestone and issue scope have them.
	for _, sc := range []store.SpendScope{{}, {Task: "kmx-601"}, {Task: "kmx-601", Run: 1}} {
		sc.Namespace, sc.Project = "agents", "kmx"
		if o, err := s.Spend(ctx, sc); err != nil || len(o.ConflictRuns) != 0 || len(o.ByKindModel) == 0 {
			t.Errorf("%+v: %d conflict runs, %d kind/model rows, %v", sc, len(o.ConflictRuns), len(o.ByKindModel), err)
		}
	}
	if o, err := s.Spend(ctx, store.SpendScope{Namespace: "agents", Project: "kmx", IssueIID: 602}); err != nil || len(o.ConflictRuns) != 1 {
		t.Errorf("issue scope: %+v, %v", o.ConflictRuns, err)
	}

	// hack/spend-report.sql: substitute psql's :'var' and drop its meta
	// commands, then run each statement.
	raw, err := os.ReadFile("../../../hack/spend-report.sql")
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{"project": "kmx", "milestone": "50", "issue": "601"}
	var script []string
	for _, line := range strings.Split(string(raw), "\n") {
		if l := strings.TrimSpace(line); !strings.HasPrefix(l, `\`) && !strings.HasPrefix(l, "--") {
			script = append(script, line)
		}
	}
	text := regexp.MustCompile(`:'(\w+)'`).ReplaceAllStringFunc(strings.Join(script, "\n"), func(m string) string {
		v, ok := vars[m[2:len(m)-1]]
		if !ok {
			t.Fatalf("spend-report.sql uses the unknown variable %s", m)
		}
		return "'" + v + "'"
	})
	stmts := 0
	for _, stmt := range strings.Split(text, ";") {
		if strings.TrimSpace(stmt) != "" {
			storetest.Exec(t, stmt)
			stmts++
		}
	}
	if stmts != 4 {
		t.Errorf("spend-report.sql has %d statements, want 4", stmts)
	}
}

// TestSpendByProject sums each project's calls and legacy runs in one query;
// a project without calls is missing, another namespace's same name stays out.
func TestSpendByProject(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	now := time.Now().Truncate(time.Second)
	exp := now.Add(24 * time.Hour)
	usd := func(v float64) *float64 { return &v }
	call := func(key, ns, project string, cost *float64, in int64) store.Call {
		source := "reported"
		if cost == nil {
			source = "unknown"
		}
		return store.Call{Key: key, At: now, Namespace: ns, Project: project, MilestoneIID: 1, IssueIID: 2,
			Task: project + "-2", Run: 1, Seq: 1, Kind: "implement", Role: "implement", Engine: "claude", Model: "claude-opus-5-5",
			InputTokens: in, CostUSD: cost, CostSource: source, ExpiresAt: exp}
	}
	if _, err := owner.InsertCalls(ctx, []store.Call{
		call("b1", "agents", "shop", usd(1), 10),
		call("b2", "agents", "shop", usd(2), 20),
		call("b3", "agents", "billing", nil, 30),
		call("b4", "other", "shop", usd(100), 1),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.InsertEvents(ctx, []store.Event{{Key: "bp1", At: now, Namespace: "agents", Project: "billing",
		MilestoneIID: 1, IssueIID: 3, Task: "billing-3", Kind: "run.finished",
		Data: map[string]any{"costUSD": "0.5000", "runCount": 1}, ExpiresAt: exp}}); err != nil {
		t.Fatal(err)
	}
	shop, billing, idle := store.ProjectKey{Namespace: "agents", Name: "shop"}, store.ProjectKey{Namespace: "agents", Name: "billing"},
		store.ProjectKey{Namespace: "agents", Name: "idle"}
	got, err := s.SpendByProject(ctx, []store.ProjectKey{shop, billing, idle})
	want := map[store.ProjectKey]store.SpendTotals{
		shop:    {CostUSD: 3, CostKnown: true, InputTokens: 30, Calls: 2},
		billing: {CostUSD: 0.5, CostKnown: true, UnknownCalls: 1, InputTokens: 30, Calls: 1, LegacyCostUSD: 0.5},
	}
	if err != nil || len(got) != len(want) || got[shop] != want[shop] || got[billing] != want[billing] {
		t.Errorf("SpendByProject = %+v, %v\nwant %+v", got, err, want)
	}
	if got, err := s.SpendByProject(ctx, nil); err != nil || len(got) != 0 {
		t.Errorf("none: %+v, %v", got, err)
	}
}
