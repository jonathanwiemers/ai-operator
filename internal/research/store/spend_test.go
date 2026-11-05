package store_test

import (
	"slices"
	"strconv"
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
	impl.SubagentCalls, impl.ContextSeries, impl.SubagentTurns = 1, []int64{1000, 2500, 4000}, []int32{1}
	review := call("c2", 3*time.Hour, 20, 148, "bloop-148", 2, 2, "review", "security", "claude-sonnet-5-5", usd(0.5), 100, 10, 1000, 0, 10000)
	// #150: a cursor call without a cost.
	fix := call("c3", 4*time.Hour, 20, 150, "bloop-150", 1, 1, "fix", "fix", "gpt-5", nil, 100, 50, 0, 0, 5000)
	fix.Engine = "cursor"
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
		Calls: 6, SubagentCalls: 3, DurationMS: 96500, LegacyCostUSD: 2}
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
		CacheReadTokens: 1030, CacheWriteTokens: 40, Calls: 2, SubagentCalls: 1, DurationMS: 70000, LegacyCostUSD: 2}); sp.Totals != want {
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
		!slices.Equal(c.SubagentTurns, []int32{1}) || len(c.Models) != 2 || c.CostUSD == nil || *c.CostUSD != 1.23 {
		t.Errorf("call 1 row = %+v", c)
	}
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
	calls := []store.Call{
		// Run 1: implement, two reviewers, fix, one reviewer, fix, two
		// reviewers. Inserted out of seq order: rounds follow seq.
		call("bloop-148", 1, 9, "review", "security", usd(0.5), 50, 5),
		call("bloop-148", 1, 1, "implement", "implement", usd(4), 400, 40),
		scouted(resumed(call("bloop-148", 1, 2, "implement", "implement", usd(6), 600, 60)), 100),
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
	}
	var g []subagents
	for _, b := range sp.BySubagents {
		g = append(g, subagents{b.Role, b.WithSubagents, b.Totals.CostUSD, b.Totals.Calls, b.Totals.Tokens(),
			b.Totals.SubagentCalls, b.SubagentTokens})
	}
	if want := []subagents{
		{"ci", false, 1, 1, 110, 0, 0},
		{"fix", false, 5, 3, 550, 0, 0},
		{"implement", false, 12, 2, 1320, 0, 0},
		{"implement", true, 6, 1, 660, 1, 100},
		{"security", false, 3.25, 6, 354, 0, 0},
		{"security", true, 2, 1, 320, 1, 200},
		{"style", false, 2, 2, 231, 0, 0},
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
