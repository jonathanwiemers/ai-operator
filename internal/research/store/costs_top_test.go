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

// topRow is the part of a CostTopRow the tests compare.
type topRow struct {
	project, label        string
	milestone, issue, run int64
	task, kind            string
	cost                  float64
	known                 bool
	unknown               int64
	at                    int // hour of day
}

func topRows(got []store.CostTopRow) []topRow {
	out := []topRow{}
	for _, r := range got {
		t := r.Totals
		out = append(out, topRow{r.Namespace + "/" + r.Project, r.Label, r.MilestoneIID, r.IssueIID, r.Run, r.Task, r.Kind,
			math.Round(t.CostUSD.USD*1e6) / 1e6, t.CostUSD.Known, t.UnknownCalls, r.At.UTC().Hour()})
	}
	return out
}

func TestCostTop(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	exp := time.Now().Add(24 * time.Hour)
	day := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	usd := func(v float64) *float64 { return &v }
	call := func(ns, project string, ms, issue int64, task string, run, seq int64, hour int, kind string, cost *float64) store.Call {
		source := "reported"
		if cost == nil {
			source = "unknown"
		}
		return store.Call{Key: fmt.Sprintf("%s/%s/%s/%d/%d/%d", ns, project, task, run, seq, hour), At: day.Add(time.Duration(hour) * time.Hour),
			Namespace: ns, Project: project, MilestoneIID: ms, IssueIID: issue, Task: task, Run: run, Seq: seq,
			Kind: kind, Role: kind, Engine: "claude", Model: "claude-opus-5-5", InputTokens: 10, CostUSD: cost, CostSource: source, ExpiresAt: exp}
	}
	if _, err := owner.InsertCalls(ctx, []store.Call{
		// shop %1 #2: one run of two calls, 1.5.
		call("agents", "shop", 1, 2, "shop-2", 1, 1, 10, "implement", usd(1)),
		call("agents", "shop", 1, 2, "shop-2", 1, 2, 11, "review", usd(0.5)),
		// shop %1 #3: a run of unknown cost.
		call("agents", "shop", 1, 3, "shop-3", 2, 1, 12, "implement", nil),
		// shop %6 #8: unknown only, later than #3.
		call("agents", "shop", 6, 8, "shop-8", 1, 1, 15, "implement", nil),
		// billing %5 #7: 3.
		call("agents", "billing", 5, 7, "billing-7", 1, 1, 9, "implement", usd(3)),
		// A research run and a chat run of the same cost, the chat later.
		call("agents", "billing", 0, 0, "research/feeds", 3, 1, 13, "research", usd(0.25)),
		call("agents", "shop", 0, 0, "shop-chat", 4, 1, 14, "chat", usd(0.25)),
		// Out of range, and another namespace's project of the same name.
		call("agents", "shop", 1, 2, "shop-2", 1, 3, 24, "fix", usd(7)),
		call("other", "shop", 1, 2, "shop-2", 1, 1, 10, "implement", usd(100)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.InsertEvents(ctx, []store.Event{
		// billing %5 #9: a run from before agent_calls, 2.
		{Key: "l1", At: day.Add(13 * time.Hour), Namespace: "agents", Project: "billing", MilestoneIID: 5, IssueIID: 9,
			Task: "billing-9", Kind: "run.finished", Data: map[string]any{"costUSD": "2.0000", "runCount": 1}, ExpiresAt: exp},
	}); err != nil {
		t.Fatal(err)
	}
	shop, billing := store.ProjectKey{Namespace: "agents", Name: "shop"}, store.ProjectKey{Namespace: "agents", Name: "billing"}
	q := store.CostQuery{Projects: []store.ProjectKey{shop, billing}, From: day, To: day.Add(24 * time.Hour)}
	top := func(q store.CostQuery, n int) store.CostTop {
		t.Helper()
		got, err := s.CostTop(ctx, q, n)
		if err != nil {
			t.Fatal(err)
		}
		if got.Milestones == nil || got.Issues == nil || got.Runs == nil {
			t.Fatalf("CostTop returned a nil slice: %+v", got)
		}
		return got
	}
	check := func(what string, got []store.CostTopRow, want []topRow) {
		t.Helper()
		if g := topRows(got); !slices.Equal(g, want) {
			t.Errorf("%s:\n got %+v\nwant %+v", what, g, want)
		}
	}

	got := top(q, 10)
	check("milestones", got.Milestones, []topRow{
		{"agents/billing", "%5", 5, 0, 0, "", "", 5, true, 0, 13},
		{"agents/shop", "%1", 1, 0, 0, "", "", 1.5, true, 1, 12},
		{"agents/shop", "%6", 6, 0, 0, "", "", 0, false, 1, 15},
	})
	issues := []topRow{
		{"agents/billing", "#7", 5, 7, 0, "", "", 3, true, 0, 9},
		{"agents/billing", "#9", 5, 9, 0, "", "", 2, true, 0, 13},
		{"agents/shop", "#2", 1, 2, 0, "", "", 1.5, true, 0, 11},
		// Unknown only: last, the latest first.
		{"agents/shop", "#8", 6, 8, 0, "", "", 0, false, 1, 15},
		{"agents/shop", "#3", 1, 3, 0, "", "", 0, false, 1, 12},
	}
	check("issues", got.Issues, issues)
	runs := []topRow{
		{"agents/billing", "billing-7 run 1", 5, 7, 1, "billing-7", "implement", 3, true, 0, 9},
		{"agents/billing", "billing-9 run 1", 5, 9, 1, "billing-9", "", 2, true, 0, 13},
		{"agents/shop", "shop-2 run 1", 1, 2, 1, "shop-2", "implement", 1.5, true, 0, 11},
		// The same cost: the later first.
		{"agents/shop", "shop-chat run 4", 0, 0, 4, "shop-chat", "chat", 0.25, true, 0, 14},
		{"agents/billing", "research/feeds run 3", 0, 0, 3, "research/feeds", "research", 0.25, true, 0, 13},
		{"agents/shop", "shop-8 run 1", 6, 8, 1, "shop-8", "implement", 0, false, 1, 15},
		{"agents/shop", "shop-3 run 2", 1, 3, 2, "shop-3", "implement", 0, false, 1, 12},
	}
	check("runs", got.Runs, runs)

	// n is clamped to 1..50.
	got = top(q, 2)
	check("two issues", got.Issues, issues[:2])
	got = top(q, 0)
	check("n 0: one run", got.Runs, runs[:1])

	// Filters apply as in CostSeries; the kind filter drops legacy runs.
	got = top(store.CostQuery{Projects: q.Projects, From: q.From, To: q.To, ExcludeKinds: []string{"research"}}, 10)
	check("without research", got.Runs, slices.Delete(slices.Clone(runs), 4, 5))
	got = top(store.CostQuery{Projects: q.Projects, From: q.From, To: q.To, Kind: "implement"}, 10)
	check("implement issues", got.Issues, []topRow{issues[0], {"agents/shop", "#2", 1, 2, 0, "", "", 1, true, 0, 10}, issues[3], issues[4]})

	// Only the projects asked for.
	got = top(store.CostQuery{Projects: []store.ProjectKey{shop}, From: q.From, To: q.To}, 10)
	check("shop milestones", got.Milestones, []topRow{
		{"agents/shop", "%1", 1, 0, 0, "", "", 1.5, true, 1, 12},
		{"agents/shop", "%6", 6, 0, 0, "", "", 0, false, 1, 15},
	})

	// An empty range and no projects give empty slices.
	for what, q := range map[string]store.CostQuery{
		"empty range": {Projects: q.Projects, From: day.AddDate(0, 0, 2), To: day.AddDate(0, 0, 3)},
		"no projects": {From: q.From, To: q.To},
	} {
		if got := top(q, 10); len(got.Milestones)+len(got.Issues)+len(got.Runs) != 0 {
			t.Errorf("%s: %+v", what, got)
		}
	}
}

// TestCostTopUsesTimeIndex: every statement of CostTop reads agent_calls
// through its (namespace, project, at) index.
func TestCostTopUsesTimeIndex(t *testing.T) {
	owner := storetest.New(t)
	q := store.CostQuery{Projects: []store.ProjectKey{{Namespace: "agents", Name: "shop"}},
		From: time.Now().Add(-30 * 24 * time.Hour), To: time.Now(), Engine: "claude", ExcludeKinds: []string{"chat"}}
	sqls, args := store.CostTopQueries(q, 10)
	if len(sqls) != 3 {
		t.Fatalf("%d statements, want 3", len(sqls))
	}
	for i, sql := range sqls {
		assertUsesIndex(t, owner.Pool(), sql, args[i], "agent_calls_project_at_idx")
	}
}
