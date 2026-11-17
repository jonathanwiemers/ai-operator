package store_test

import (
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

// metricsCall is the part of a call the metrics read.
type metricsCall struct {
	project                  string
	issue                    int64
	hour                     int
	kind, arm, model, effort string
	stepSession              string
	cost                     float64
	lines, base              int64
	tools                    map[string]store.ToolResultUsage
}

// group is the part of a MetricsGroup the tests compare.
type group struct {
	project, key  string
	issues, lines int64
	cost          float64
	tokens        int64
}

func groups(got []store.MetricsGroup) []group {
	out := []group{}
	for _, g := range got {
		project := ""
		if g.Project != "" {
			project = g.Namespace + "/" + g.Project
		}
		out = append(out, group{project, g.Key, g.Issues, g.MergedLines, math.Round(g.CostUSD*1e6) / 1e6, g.Tokens})
	}
	return out
}

// seedMetrics stores calls on day (hour 24 is the next day) for agents/shop
// and agents/billing, and the merged state of their issues: shop's from the
// history, billing's from the Desk snapshot.
func seedMetrics(t *testing.T, owner *store.Store, day time.Time, calls []metricsCall, merged map[string][]int64) {
	t.Helper()
	ctx := t.Context()
	exp := time.Now().Add(24 * time.Hour)
	var rows []store.Call
	for i, c := range calls {
		cost := c.cost
		rows = append(rows, store.Call{Key: fmt.Sprintf("m/%d", i), At: day.Add(time.Duration(c.hour) * time.Hour),
			Namespace: "agents", Project: c.project, IssueIID: c.issue, Task: fmt.Sprintf("%s-%d", c.project, c.issue), Run: 1, Seq: int64(i),
			Kind: c.kind, Role: c.kind, Engine: "claude", Model: c.model, Effort: c.effort, Arm: c.arm, StepSession: c.stepSession,
			InputTokens: 10, CacheReadTokens: 90, CostUSD: &cost, CostSource: "reported", LinesAdded: c.lines * 3 / 4,
			LinesDeleted: c.lines - c.lines*3/4, BaseContextTokens: c.base, ToolResults: c.tools, ExpiresAt: exp})
	}
	if _, err := owner.InsertCalls(ctx, rows); err != nil {
		t.Fatal(err)
	}
	var events []store.Event
	for _, iid := range merged["shop"] {
		events = append(events, store.Event{Key: fmt.Sprintf("merged/%d", iid), At: day, Namespace: "agents", Project: "shop",
			IssueIID: iid, Kind: "merged", ExpiresAt: exp})
	}
	// An expired merge does not count.
	events = append(events, store.Event{Key: "merged/old", At: day, Namespace: "agents", Project: "shop", IssueIID: 10,
		Kind: "merged", ExpiresAt: time.Now().Add(-time.Hour)})
	if _, err := owner.InsertEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	var issues []store.IssueSnapshot
	for _, iid := range merged["billing"] {
		issues = append(issues, store.IssueSnapshot{IID: iid, State: "merged"})
	}
	issues = append(issues, store.IssueSnapshot{IID: 2, State: "active"})
	m := store.MilestoneSnapshot{Project: store.ProjectKey{Namespace: "agents", Name: "billing"}, IID: 1, State: "open", Issues: issues}
	if _, err := owner.SaveMilestone(ctx, m, exp); err != nil {
		t.Fatal(err)
	}
}

func TestMetrics(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	day := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	const opus, sonnet = "claude-opus-5-5", "claude-sonnet-5-5"
	tools := func(kv ...any) map[string]store.ToolResultUsage {
		m := map[string]store.ToolResultUsage{}
		for i := 0; i < len(kv); i += 3 {
			m[kv[i].(string)] = store.ToolResultUsage{Calls: int64(kv[i+1].(int)), Bytes: int64(kv[i+2].(int))}
		}
		return m
	}
	var calls []metricsCall
	// shop #1-#5, arm baseline: one resumed opus/high implement call each,
	// 2 USD, 100 lines, merged. The first at the range's start.
	for iid := int64(1); iid <= 5; iid++ {
		calls = append(calls, metricsCall{project: "shop", issue: iid, hour: int(iid) - 1, kind: "implement", arm: "baseline",
			model: opus, effort: "high", stepSession: "resume", cost: 2, lines: 100})
	}
	// #1's review counts for the issue (its cost and tokens), not its model.
	calls = append(calls, metricsCall{project: "shop", issue: 1, hour: 6, kind: "review", arm: "baseline", model: sonnet, cost: 1, base: 50})
	// shop #6-#9, arm fresh: 1 USD, 50 lines, merged; #10 unmerged: 3 USD, 40 lines.
	for iid := int64(6); iid <= 9; iid++ {
		calls = append(calls, metricsCall{project: "shop", issue: iid, hour: 7, kind: "implement", arm: "fresh",
			model: opus, effort: "high", stepSession: "fresh", cost: 1, lines: 50})
	}
	calls = append(calls, metricsCall{project: "shop", issue: 10, hour: 8, kind: "implement", arm: "fresh",
		model: opus, effort: "high", stepSession: "fresh", cost: 3, lines: 40})
	// shop #11, no arm: sonnet/medium, no step session, merged.
	calls = append(calls, metricsCall{project: "shop", issue: 11, hour: 9, kind: "implement", model: sonnet, effort: "medium",
		cost: 1, lines: 20})
	// billing #1, arm sonnet: its opus/high calls cost more than its
	// sonnet/medium one, so it counts as opus/high. Merged per the snapshot.
	calls = append(calls,
		metricsCall{project: "billing", issue: 1, hour: 10, kind: "implement", arm: "sonnet", model: sonnet, effort: "medium", cost: 0.75, lines: 10},
		metricsCall{project: "billing", issue: 1, hour: 11, kind: "implement", arm: "sonnet", model: opus, effort: "high", cost: 0.5, lines: 5},
		metricsCall{project: "billing", issue: 1, hour: 12, kind: "implement", arm: "sonnet", model: opus, effort: "high", cost: 0.5, lines: 5})
	// The base context: shop implement 100, 300, 200 and no value (resumed),
	// shop review 50 (above), billing implement 1000 and 2000.
	for i, base := range []int64{100, 300, 200, 0} {
		calls[i].base = base
	}
	calls[len(calls)-2].base, calls[len(calls)-1].base = 1000, 2000
	// Tool results: shop's Read 1500 bytes over two calls, Bash and Grep tied
	// at 1200 (by name then), Edit, Task, and an MCP tool out of the top 5.
	calls[0].tools = tools("Read", 2, 1000, "Bash", 1, 1200, "Edit", 1, 100)
	calls[1].tools = tools("Read", 1, 500, "Grep", 4, 1200, "Task", 1, 50, "mcp__gitlab__get_issue", 1, 10)
	calls[len(calls)-1].tools = tools("Read", 1, 10)
	// Outside the range: the next day's start, an expensive fix of #1 with a
	// huge base context and tool result.
	calls = append(calls, metricsCall{project: "shop", issue: 1, hour: 24, kind: "fix", arm: "baseline", model: opus, cost: 100,
		lines: 1000, base: 9999, tools: tools("Write", 1, 99999)})
	seedMetrics(t, owner, day, calls, map[string][]int64{"shop": {1, 2, 3, 4, 5, 6, 7, 8, 9, 11}, "billing": {1}})

	shop, billing := store.ProjectKey{Namespace: "agents", Name: "shop"}, store.ProjectKey{Namespace: "agents", Name: "billing"}
	q := store.CostQuery{Projects: []store.ProjectKey{shop, billing}, From: day, To: day.Add(24 * time.Hour), Kind: "review"}
	got, err := s.Metrics(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	check := func(name string, got, want []group) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s =\n%+v\nwant\n%+v", name, got, want)
		}
	}
	// Kind is ignored: issues count whole. Arms leave out shop #11; fresh
	// counts #10's cost and tokens, not its lines.
	check("arms", groups(got.Arms), []group{
		{"agents/billing", "sonnet", 1, 20, 1.75, 300},
		{"agents/shop", "baseline", 5, 500, 11, 600},
		{"agents/shop", "fresh", 4, 200, 7, 500},
	})
	check("by model", groups(got.ByModel), []group{
		{"", opus, 10, 720, 19.75, 1400},
		{"", sonnet, 1, 20, 1, 100},
	})
	check("by effort", groups(got.ByEffort), []group{
		{"", "high", 10, 720, 19.75, 1400},
		{"", "medium", 1, 20, 1, 100},
	})
	check("by step session", groups(got.ByStepSession), []group{
		{"", "resume", 5, 500, 11, 600},
		{"", "fresh", 4, 200, 7, 500},
		{"", "", 2, 40, 2.75, 400},
	})
	wantBase := []store.RoleMedian{
		{Namespace: "agents", Project: "billing", Role: "implement", MedianTokens: 1500, Calls: 2},
		{Namespace: "agents", Project: "shop", Role: "implement", MedianTokens: 200, Calls: 3},
		{Namespace: "agents", Project: "shop", Role: "review", MedianTokens: 50, Calls: 1},
	}
	if !slices.Equal(got.BaseContext, wantBase) {
		t.Errorf("base context = %+v, want %+v", got.BaseContext, wantBase)
	}
	tool := func(project, name string, calls, bytes int64) store.ToolUsage {
		return store.ToolUsage{Namespace: "agents", Project: project, Tool: name, Calls: calls, Bytes: bytes}
	}
	wantTools := []store.ToolUsage{
		tool("billing", "Read", 1, 10),
		tool("shop", "Read", 3, 1500), tool("shop", "Bash", 1, 1200), tool("shop", "Grep", 4, 1200),
		tool("shop", "Edit", 1, 100), tool("shop", "Task", 1, 50),
	}
	if !slices.Equal(got.TopTools, wantTools) {
		t.Errorf("top tools = %+v, want %+v", got.TopTools, wantTools)
	}

	// The range's end is exclusive and its start inclusive: the next day has
	// only the fix of #1, which has no implement call there.
	next, err := s.Metrics(t.Context(), store.CostQuery{Projects: []store.ProjectKey{shop}, From: day.Add(24 * time.Hour), To: day.Add(48 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	check("next day's arms", groups(next.Arms), []group{{"agents/shop", "baseline", 1, 0, 100, 100}})
	check("next day by model", groups(next.ByModel), []group{{"", "", 1, 0, 100, 100}})
	if len(next.BaseContext) != 1 || next.BaseContext[0].MedianTokens != 9999 || len(next.TopTools) != 1 {
		t.Errorf("next day's context = %+v, %+v", next.BaseContext, next.TopTools)
	}
	// From an hour later, shop #1's implement call is out of range: #1 then
	// counts only its review.
	later, err := s.Metrics(t.Context(), store.CostQuery{Projects: []store.ProjectKey{shop}, From: day.Add(time.Hour), To: day.Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if g := groups(later.Arms)[0]; g != (group{"agents/shop", "baseline", 5, 400, 9, 500}) {
		t.Errorf("baseline from 01:00 = %+v", g)
	}

	// Excluded kinds drop their calls; no projects give empty slices.
	ex, err := s.Metrics(t.Context(), store.CostQuery{Projects: []store.ProjectKey{shop}, From: day, To: day.Add(24 * time.Hour),
		ExcludeKinds: []string{"review"}})
	if err != nil {
		t.Fatal(err)
	}
	if g := groups(ex.Arms)[0]; g.cost != 10 || len(ex.BaseContext) != 1 {
		t.Errorf("without review: baseline %+v, base context %+v", g, ex.BaseContext)
	}
	none, err := s.Metrics(t.Context(), store.CostQuery{From: day, To: day.Add(24 * time.Hour)})
	if err != nil || none.Arms == nil || len(none.ByModel) != 0 || none.TopTools == nil {
		t.Errorf("no projects = %+v, %v", none, err)
	}
}

// TestMetricsUseTimeIndex: every statement of Metrics reads agent_calls
// through its (namespace, project, at) index.
func TestMetricsUseTimeIndex(t *testing.T) {
	owner := storetest.New(t)
	q := store.CostQuery{Projects: []store.ProjectKey{{Namespace: "agents", Name: "shop"}},
		From: time.Now().Add(-30 * 24 * time.Hour), To: time.Now(), ExcludeKinds: []string{"research"}}
	sqls, args := store.MetricsQueries(q)
	if len(sqls) != 6 {
		t.Fatalf("%d statements, want 6", len(sqls))
	}
	for i, sql := range sqls {
		assertUsesIndex(t, owner.Pool(), sql, args[i], "agent_calls_project_at_idx")
	}
}
