package store_test

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

// costPoint is the part of a CostPoint the tests compare.
type costPoint struct {
	bucket, key        string
	cost, legacy       float64
	known              bool
	calls, unknown     int64
	unmetered, tokens  int64
	checkpoints, durMS int64
}

func costPoints(got []store.CostPoint) []costPoint {
	out := []costPoint{}
	for _, p := range got {
		t := p.Totals
		out = append(out, costPoint{p.Bucket, p.Key, math.Round(t.CostUSD.USD*1e6) / 1e6, t.LegacyCostUSD.USD, t.CostUSD.Known,
			t.Calls, t.UnknownCalls, t.UnmeteredCalls, t.Tokens(), t.Checkpoints, t.DurationMS})
	}
	return out
}

func TestCostSeries(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	exp := time.Now().Add(24 * time.Hour)
	day := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	usd := func(v float64) *float64 { return &v }
	call := func(key, ns, project string, at time.Time, task, kind, engine, model string, cost *float64, in, out int64) store.Call {
		source := "reported"
		if cost == nil {
			source = "unknown"
		}
		return store.Call{Key: key, At: at, Namespace: ns, Project: project, MilestoneIID: 1, IssueIID: 2,
			Task: task, Run: 1, Seq: 1, Kind: kind, Role: kind, Engine: engine, Model: model,
			InputTokens: in, OutputTokens: out, CostUSD: cost, CostSource: source, DurationMS: 1000, ExpiresAt: exp}
	}
	// An implement call with a haiku subagent: two models.
	split := call("k1", "agents", "shop", day.Add(10*time.Hour), "shop-2", "implement", "claude", "claude-opus-5-5", usd(1.23), 10, 20)
	split.CacheReadTokens, split.CacheWriteTokens = 30, 40
	split.Models = []store.CallModel{
		{Model: "claude-opus-5-5", InputTokens: 8, OutputTokens: 15, CacheReadTokens: 30, CacheWriteTokens: 40, CostUSD: usd(1.2)},
		{Model: "claude-haiku-5-5", InputTokens: 2, OutputTokens: 5, CostUSD: usd(0.03)},
	}
	research := call("k4", "agents", "billing", day.Add(12*time.Hour), "research/feeds", "research", "claude", "claude-sonnet-5-5", usd(0.5), 1, 0)
	if _, err := owner.InsertCalls(ctx, []store.Call{
		split,
		// Tokens without a cost: unknown.
		call("k2", "agents", "shop", day.Add(11*time.Hour), "shop-3", "review", "cursor", "gpt-5", nil, 100, 50),
		// Neither cost nor tokens: unknown and unmetered.
		call("k3", "agents", "billing", day.Add(11*time.Hour), "billing-2", "implement", "cursor", "auto", nil, 0, 0),
		research,
		// At From: in range.
		call("k5", "agents", "shop", day, "shop-chat", "chat", "claude", "claude-sonnet-5-5", usd(0.1), 1, 0),
		// At To: out of range.
		call("k6", "agents", "shop", day.Add(24*time.Hour), "shop-4", "implement", "claude", "claude-opus-5-5", usd(7), 1, 0),
		// Another namespace's project of the same name.
		call("k7", "other", "shop", day.Add(10*time.Hour), "shop-2", "implement", "claude", "claude-opus-5-5", usd(100), 1, 0),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.InsertEvents(ctx, []store.Event{
		// A run from before agent_calls.
		{Key: "l1", At: day.Add(13 * time.Hour), Namespace: "agents", Project: "billing", MilestoneIID: 1, IssueIID: 9,
			Task: "billing-9", Kind: "run.finished", Data: map[string]any{"costUSD": "2.0000", "runCount": 1}, ExpiresAt: exp},
		// A checkpoint no call carries: no cost, left out.
		{Key: "l2", At: day.Add(13 * time.Hour), Namespace: "agents", Project: "billing", MilestoneIID: 1, IssueIID: 9,
			Task: "billing-9", Kind: "checkpoint", Data: map[string]any{"run": 2, "reason": "budget", "sha": "zzz"}, ExpiresAt: exp},
	}); err != nil {
		t.Fatal(err)
	}
	shop, billing := store.ProjectKey{Namespace: "agents", Name: "shop"}, store.ProjectKey{Namespace: "agents", Name: "billing"}
	q := store.CostQuery{Projects: []store.ProjectKey{shop, billing}, From: day, To: day.Add(24 * time.Hour)}
	series := func(q store.CostQuery, bucket, group string) []store.CostPoint {
		t.Helper()
		got, err := s.CostSeries(ctx, q, bucket, group)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			t.Fatal("CostSeries returned nil, want a slice")
		}
		return got
	}
	check := func(what string, got []store.CostPoint, want []costPoint) {
		t.Helper()
		if g := costPoints(got); !slices.Equal(g, want) {
			t.Errorf("%s:\n got %+v\nwant %+v", what, g, want)
		}
	}
	const d = "2026-09-10"
	legacy := costPoint{d, "", 2, 2, true, 0, 0, 0, 0, 0, 0}

	check("one series", series(q, "day", ""), []costPoint{{d, "", 3.83, 2, true, 5, 2, 1, 252, 0, 5000}})
	byKind := []costPoint{
		legacy,
		{d, "chat", 0.1, 0, true, 1, 0, 0, 1, 0, 1000},
		{d, "implement", 1.23, 0, true, 2, 1, 1, 100, 0, 2000},
		{d, "research", 0.5, 0, true, 1, 0, 0, 1, 0, 1000},
		{d, "review", 0, 0, false, 1, 1, 0, 150, 0, 1000},
	}
	check("by kind", series(q, "day", "kind"), byKind)
	check("by project", series(q, "day", "project"), []costPoint{
		{d, "agents/billing", 2.5, 2, true, 2, 1, 1, 1, 0, 2000},
		{d, "agents/shop", 1.33, 0, true, 3, 1, 0, 251, 0, 3000},
	})
	check("by engine", series(q, "day", "engine"), []costPoint{
		legacy,
		{d, "claude", 1.83, 0, true, 3, 0, 0, 102, 0, 3000},
		{d, "cursor", 0, 0, false, 2, 2, 1, 150, 0, 2000},
	})
	byModel := series(q, "day", "model")
	check("by model", byModel, []costPoint{
		legacy,
		{d, "auto", 0, 0, false, 1, 1, 1, 0, 0, 1000},
		// The subagent's share: no duration, which stays with the main model.
		{d, "claude-haiku-5-5", 0.03, 0, true, 1, 0, 0, 7, 0, 0},
		{d, "claude-opus-5-5", 1.2, 0, true, 1, 0, 0, 93, 0, 1000},
		{d, "claude-sonnet-5-5", 0.6, 0, true, 2, 0, 0, 2, 0, 2000},
		{d, "gpt-5", 0, 0, false, 1, 1, 0, 150, 0, 1000},
	})
	// The model split adds up to the ungrouped cost and tokens.
	var sum store.SpendTotals
	for _, p := range byModel {
		sum.CostUSD.USD += p.Totals.CostUSD.USD
		sum.InputTokens += p.Totals.InputTokens
		sum.OutputTokens += p.Totals.OutputTokens
		sum.CacheReadTokens += p.Totals.CacheReadTokens
		sum.CacheWriteTokens += p.Totals.CacheWriteTokens
		sum.DurationMS += p.Totals.DurationMS
	}
	all := series(q, "day", "")[0].Totals
	if math.Abs(sum.CostUSD.USD-all.CostUSD.USD) > 1e-9 || sum.InputTokens != all.InputTokens || sum.OutputTokens != all.OutputTokens ||
		sum.CacheReadTokens != all.CacheReadTokens || sum.CacheWriteTokens != all.CacheWriteTokens || sum.DurationMS != all.DurationMS {
		t.Errorf("model split sums to %+v, want %+v", sum, all)
	}

	// Filters: a kind or an engine drops the legacy run; excluded kinds
	// keep it.
	kq := q
	kq.Kind = "implement"
	check("kind implement", series(kq, "day", "kind"), []costPoint{byKind[2]})
	eq := q
	eq.Engine = "cursor"
	check("engine cursor", series(eq, "day", "kind"), []costPoint{
		{d, "implement", 0, 0, false, 1, 1, 1, 0, 0, 1000},
		byKind[4],
	})
	xq := q
	xq.ExcludeKinds = []string{"research"}
	check("without research", series(xq, "day", "kind"), []costPoint{byKind[0], byKind[1], byKind[2], byKind[4]})
	xq.Kind = "research"
	check("research excluded and chosen", series(xq, "day", "model"), []costPoint{})

	// Empty: no projects, an empty range, a range without calls, a project
	// not in Projects.
	check("no projects", series(store.CostQuery{From: day, To: day.Add(24 * time.Hour)}, "day", "kind"), []costPoint{})
	empty := q
	empty.To = empty.From
	check("empty range", series(empty, "month", "project"), []costPoint{})
	empty.From, empty.To = day.AddDate(-1, 0, 0), day.AddDate(-1, 0, 1)
	check("range without calls", series(empty, "day", ""), []costPoint{})
	check("other namespace only", series(store.CostQuery{Projects: []store.ProjectKey{{Namespace: "nobody", Name: "shop"}},
		From: day, To: day.Add(24 * time.Hour)}, "day", "project"), []costPoint{})

	for _, bad := range [][2]string{{"hour", ""}, {"", "kind"}, {"day", "role"}, {"Week", "project"}} {
		if got, err := s.CostSeries(ctx, q, bad[0], bad[1]); err == nil {
			t.Errorf("CostSeries(%q, %q) = %+v, want an error", bad[0], bad[1], got)
		}
	}
}

// TestCostSeriesBuckets: buckets are local dates in Loc, across the end of
// daylight saving time in Berlin (2026-10-25, UTC+2 to UTC+1); weeks start on
// a local Monday, months on the 1st.
func TestCostSeriesBuckets(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(24 * time.Hour)
	one := 1.0
	var calls []store.Call
	for i, at := range []string{
		"2026-10-18T21:30:00Z", // Sunday 23:30 CEST: week of 10-12
		"2026-10-24T22:30:00Z", // 10-25 00:30 CEST
		"2026-10-25T22:30:00Z", // 10-25 23:30 CET
		"2026-10-25T23:30:00Z", // Monday 10-26 00:30 CET
		"2026-10-31T23:30:00Z", // Sunday 11-01 00:30 CET
	} {
		ts, err := time.Parse(time.RFC3339, at)
		if err != nil {
			t.Fatal(err)
		}
		calls = append(calls, store.Call{Key: "d" + at, At: ts, Namespace: "agents", Project: "shop", Task: "shop-1", Run: 1,
			Seq: int64(i + 1), Kind: "implement", Role: "implement", Engine: "claude", Model: "claude-opus-5-5",
			CostUSD: &one, CostSource: "reported", ExpiresAt: exp})
	}
	if _, err := owner.InsertCalls(ctx, calls); err != nil {
		t.Fatal(err)
	}
	q := store.CostQuery{Projects: []store.ProjectKey{{Namespace: "agents", Name: "shop"}},
		From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), Loc: berlin}
	for _, c := range []struct {
		bucket string
		want   map[string]float64
	}{
		{"day", map[string]float64{"2026-10-18": 1, "2026-10-25": 2, "2026-10-26": 1, "2026-11-01": 1}},
		{"week", map[string]float64{"2026-10-12": 1, "2026-10-19": 2, "2026-10-26": 2}},
		{"month", map[string]float64{"2026-10-01": 4, "2026-11-01": 1}},
	} {
		got, err := s.CostSeries(ctx, q, c.bucket, "")
		if err != nil {
			t.Fatal(err)
		}
		g := map[string]float64{}
		var order []string
		for _, p := range got {
			g[p.Bucket] = p.Totals.CostUSD.USD
			order = append(order, p.Bucket)
		}
		if len(g) != len(c.want) || !slices.IsSorted(order) {
			t.Errorf("%s buckets = %v, want %v", c.bucket, order, c.want)
		}
		for b, v := range c.want {
			if g[b] != v {
				t.Errorf("%s bucket %s = %v, want %v (got %v)", c.bucket, b, g[b], v, g)
			}
		}
	}
	// In UTC the call of 10-24 22:30 stays on the 24th.
	q.Loc = nil
	got, err := s.CostSeries(ctx, q, "day", "")
	if err != nil || len(got) == 0 || got[1].Bucket != "2026-10-24" {
		t.Errorf("UTC days = %+v, %v", got, err)
	}
}

// TestCostSeriesUsesTimeIndex: the series reads agent_calls through its
// (namespace, project, at) index, also when split by model and filtered.
func TestCostSeriesUsesTimeIndex(t *testing.T) {
	owner := storetest.New(t)
	q := store.CostQuery{Projects: []store.ProjectKey{{Namespace: "agents", Name: "shop"}},
		From: time.Now().Add(-30 * 24 * time.Hour), To: time.Now(), Loc: time.UTC, Kind: "implement", ExcludeKinds: []string{"research"}}
	for _, group := range []string{"project", "model"} {
		sql, args, err := store.CostSeriesQuery(q, "day", group)
		if err != nil {
			t.Fatal(err)
		}
		assertUsesIndex(t, owner.Pool(), sql, args, "agent_calls_project_at_idx")
	}
}

// breakdownPoints is the costPoints of breakdown rows, bucket "".
func breakdownPoints(got []store.SpendBreakdown) []costPoint {
	ps := []store.CostPoint{}
	for _, b := range got {
		ps = append(ps, store.CostPoint{Key: b.Key, Totals: b.Totals})
	}
	return costPoints(ps)
}

func TestCostBreakdowns(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	exp := time.Now().Add(24 * time.Hour)
	day := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	usd := func(v float64) *float64 { return &v }
	call := func(key, project string, at time.Time, kind, engine, model string, cost *float64, in, out int64) store.Call {
		source := "reported"
		if cost == nil {
			source = "unknown"
		}
		return store.Call{Key: key, At: at, Namespace: "agents", Project: project, MilestoneIID: 1, IssueIID: 2,
			Task: project + "-" + key, Run: 1, Seq: 1, Kind: kind, Role: kind, Engine: engine, Model: model,
			InputTokens: in, OutputTokens: out, CostUSD: cost, CostSource: source, DurationMS: 1000, ExpiresAt: exp}
	}
	// An implement call with a haiku subagent and a step of 40 lines.
	split := call("k1", "shop", day.Add(10*time.Hour), "implement", "claude", "claude-opus-5-5", usd(1.23), 10, 20)
	split.CacheReadTokens, split.CacheWriteTokens = 30, 40
	split.LinesAdded, split.LinesDeleted, split.StepGateNudges = 30, 10, 1
	split.Models = []store.CallModel{
		{Model: "claude-opus-5-5", InputTokens: 8, OutputTokens: 15, CacheReadTokens: 30, CacheWriteTokens: 40, CostUSD: usd(1.2)},
		{Model: "claude-haiku-5-5", InputTokens: 2, OutputTokens: 5, CostUSD: usd(0.03)},
	}
	// The previous period's last call, with half its input from the cache
	// and a step that is not this range's.
	prev := call("p1", "shop", day.Add(-time.Nanosecond), "implement", "claude", "claude-opus-5-5", usd(4), 50, 0)
	prev.CacheReadTokens, prev.LinesAdded = 50, 100
	if _, err := owner.InsertCalls(ctx, []store.Call{
		split,
		// Tokens without a cost: unknown.
		call("k2", "shop", day.Add(11*time.Hour), "review", "cursor", "gpt-5", nil, 100, 50),
		// Neither cost nor tokens: unknown and unmetered.
		call("k3", "billing", day.Add(11*time.Hour), "implement", "cursor", "auto", nil, 0, 0),
		call("k4", "billing", day.Add(12*time.Hour), "research", "claude", "claude-sonnet-5-5", usd(0.5), 1, 0),
		// At From: in range, not in the previous period.
		call("k5", "shop", day, "chat", "claude", "claude-sonnet-5-5", usd(0.1), 1, 0),
		// At To: out of range.
		call("k6", "shop", day.Add(24*time.Hour), "implement", "claude", "claude-opus-5-5", usd(7), 1, 0),
		prev,
		// The previous period's first instant, and the instant before it.
		call("p2", "shop", day.Add(-24*time.Hour), "chat", "claude", "claude-sonnet-5-5", usd(0.5), 0, 1),
		call("p3", "shop", day.Add(-24*time.Hour-time.Nanosecond), "chat", "claude", "claude-sonnet-5-5", usd(8), 0, 1),
		// The first research call, long before the range.
		call("r0", "billing", day.AddDate(0, -1, 0), "research", "claude", "claude-sonnet-5-5", usd(0.2), 1, 0),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.InsertCalls(ctx, []store.Call{{Key: "o1", At: day.Add(time.Hour), Namespace: "other", Project: "shop",
		Task: "shop-1", Run: 1, Seq: 1, Kind: "implement", Role: "implement", Engine: "claude", Model: "claude-opus-5-5",
		CostUSD: usd(100), CostSource: "reported", ExpiresAt: exp}}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.InsertEvents(ctx, []store.Event{
		// A run from before agent_calls.
		{Key: "l1", At: day.Add(13 * time.Hour), Namespace: "agents", Project: "billing", MilestoneIID: 1, IssueIID: 9,
			Task: "billing-9", Kind: "run.finished", Data: map[string]any{"costUSD": "2.0000", "runCount": 1}, ExpiresAt: exp},
		// A checkpoint no call carries: no cost, left out.
		{Key: "l2", At: day.Add(13 * time.Hour), Namespace: "agents", Project: "billing", MilestoneIID: 1, IssueIID: 9,
			Task: "billing-9", Kind: "checkpoint", Data: map[string]any{"run": 2, "reason": "budget", "sha": "zzz"}, ExpiresAt: exp},
	}); err != nil {
		t.Fatal(err)
	}
	shop, billing := store.ProjectKey{Namespace: "agents", Name: "shop"}, store.ProjectKey{Namespace: "agents", Name: "billing"}
	q := store.CostQuery{Projects: []store.ProjectKey{shop, billing}, From: day, To: day.Add(24 * time.Hour)}
	breakdowns := func(q store.CostQuery) store.CostBreakdowns {
		t.Helper()
		got, err := s.CostBreakdowns(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	check := func(what string, got []store.SpendBreakdown, want []costPoint) {
		t.Helper()
		if g := breakdownPoints(got); !slices.Equal(g, want) {
			t.Errorf("%s:\n got %+v\nwant %+v", what, g, want)
		}
	}
	totals := func(what string, got store.SpendTotals, want costPoint) {
		t.Helper()
		check(what, []store.SpendBreakdown{{Totals: got}}, []costPoint{want})
	}
	near := func(what string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("%s = %v, want %v", what, got, want)
		}
	}
	legacy := costPoint{"", "", 2, 2, true, 0, 0, 0, 0, 0, 0}

	b := breakdowns(q)
	totals("totals", b.Totals, costPoint{"", "", 3.83, 2, true, 5, 2, 1, 252, 0, 5000})
	// p2 at the previous period's start counts, p3 before it and k5 at From
	// do not.
	totals("previous", b.Previous, costPoint{"", "", 4.5, 0, true, 2, 0, 0, 101, 0, 2000})
	near("cache-read share", b.CacheReadShare, 30.0/(112+30+40))
	near("previous cache-read share", b.PreviousCacheReadShare, 0.5)
	check("by project", b.ByProject, []costPoint{
		{"", "agents/billing", 2.5, 2, true, 2, 1, 1, 1, 0, 2000},
		{"", "agents/shop", 1.33, 0, true, 3, 1, 0, 251, 0, 3000},
	})
	byKind := []costPoint{
		legacy,
		{"", "implement", 1.23, 0, true, 2, 1, 1, 100, 0, 2000},
		{"", "research", 0.5, 0, true, 1, 0, 0, 1, 0, 1000},
		{"", "chat", 0.1, 0, true, 1, 0, 0, 1, 0, 1000},
		{"", "review", 0, 0, false, 1, 1, 0, 150, 0, 1000},
	}
	check("by kind", b.ByKind, byKind)
	check("by model", b.ByModel, []costPoint{
		legacy,
		{"", "claude-opus-5-5", 1.2, 0, true, 1, 0, 0, 93, 0, 1000},
		{"", "claude-sonnet-5-5", 0.6, 0, true, 2, 0, 0, 2, 0, 2000},
		{"", "claude-haiku-5-5", 0.03, 0, true, 1, 0, 0, 7, 0, 0},
		{"", "auto", 0, 0, false, 1, 1, 1, 0, 0, 1000},
		{"", "gpt-5", 0, 0, false, 1, 1, 0, 150, 0, 1000},
	})
	check("by engine", b.ByEngine, []costPoint{
		legacy,
		{"", "claude", 1.83, 0, true, 3, 0, 0, 102, 0, 3000},
		{"", "cursor", 0, 0, false, 2, 2, 1, 150, 0, 2000},
	})
	if want := (store.StepLines{Count: 1, Median: 40, P90: 40, Max: 40, Nudges: 1}); b.StepLines != want {
		t.Errorf("step lines = %+v, want %+v", b.StepLines, want)
	}
	if want := day.AddDate(0, -1, 0); b.ResearchSince == nil || !b.ResearchSince.Equal(want) {
		t.Errorf("research since = %v, want %v", b.ResearchSince, want)
	}

	// Filters apply to every field. A kind drops the legacy run and the
	// research date unless it is research.
	kq := q
	kq.Kind = "implement"
	b = breakdowns(kq)
	implTotals := byKind[1]
	implTotals.key = ""
	totals("implement totals", b.Totals, implTotals)
	totals("implement previous", b.Previous, costPoint{"", "", 4, 0, true, 1, 0, 0, 100, 0, 1000})
	check("implement by kind", b.ByKind, []costPoint{byKind[1]})
	check("implement by project", b.ByProject, []costPoint{
		{"", "agents/shop", 1.23, 0, true, 1, 0, 0, 100, 0, 1000},
		{"", "agents/billing", 0, 0, false, 1, 1, 1, 0, 0, 1000},
	})
	if b.StepLines.Count != 1 || b.ResearchSince != nil {
		t.Errorf("implement: step lines %+v, research since %v", b.StepLines, b.ResearchSince)
	}
	kq.Kind = "research"
	if b = breakdowns(kq); b.ResearchSince == nil || b.StepLines != (store.StepLines{}) || b.Totals.Calls != 1 {
		t.Errorf("research: %+v", b)
	}
	eq := q
	eq.Engine = "cursor"
	b = breakdowns(eq)
	totals("cursor totals", b.Totals, costPoint{"", "", 0, 0, false, 2, 2, 1, 150, 0, 2000})
	totals("cursor previous", b.Previous, costPoint{"", "", 0, 0, true, 0, 0, 0, 0, 0, 0})
	check("cursor by engine", b.ByEngine, []costPoint{{"", "cursor", 0, 0, false, 2, 2, 1, 150, 0, 2000}})
	if b.StepLines != (store.StepLines{}) || b.CacheReadShare != 0 {
		t.Errorf("cursor: step lines %+v, cache-read share %v", b.StepLines, b.CacheReadShare)
	}
	xq := q
	xq.ExcludeKinds = []string{"research"}
	b = breakdowns(xq)
	check("without research by kind", b.ByKind, []costPoint{byKind[0], byKind[1], byKind[3], byKind[4]})
	if b.ResearchSince != nil || b.Totals.Calls != 4 {
		t.Errorf("without research: totals %+v, research since %v", b.Totals, b.ResearchSince)
	}

	// Only the projects asked for; none gives zeros and empty slices.
	if b = breakdowns(store.CostQuery{Projects: []store.ProjectKey{shop}, From: day, To: day.Add(24 * time.Hour)}); b.ResearchSince != nil || len(b.ByProject) != 1 {
		t.Errorf("shop only: %+v", b)
	}
	b = breakdowns(store.CostQuery{From: day, To: day.Add(24 * time.Hour)})
	if b.Totals.Calls != 0 || !b.Totals.CostUSD.Known || b.ByKind == nil || len(b.ByProject) != 0 || b.ResearchSince != nil {
		t.Errorf("no projects: %+v", b)
	}

	// One project over all time: the figures of its Spend.
	all := breakdowns(store.CostQuery{Projects: []store.ProjectKey{shop},
		From: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)})
	sp, err := s.Spend(ctx, store.SpendScope{Namespace: "agents", Project: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	check("all time by kind", all.ByKind, breakdownPoints(sp.ByKind))
	check("all time by model", all.ByModel, breakdownPoints(sp.ByModel))
	near("all time cache-read share", all.CacheReadShare, sp.CacheReadShare)
	if all.StepLines != sp.StepLines {
		t.Errorf("all time step lines = %+v, Spend's %+v", all.StepLines, sp.StepLines)
	}
}

// TestCostBreakdownsUseTimeIndex: every statement of CostBreakdowns reads
// agent_calls through its (namespace, project, at) index.
func TestCostBreakdownsUseTimeIndex(t *testing.T) {
	owner := storetest.New(t)
	q := store.CostQuery{Projects: []store.ProjectKey{{Namespace: "agents", Name: "shop"}},
		From: time.Now().Add(-30 * 24 * time.Hour), To: time.Now(), Engine: "claude", ExcludeKinds: []string{"chat"}}
	sqls, args := store.CostBreakdownsQueries(q)
	if len(sqls) != 8 {
		t.Fatalf("%d statements, want 8 with the research date", len(sqls))
	}
	for i, sql := range sqls {
		assertUsesIndex(t, owner.Pool(), sql, args[i], "agent_calls_project_at_idx")
	}
}
