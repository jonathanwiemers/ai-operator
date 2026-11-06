package store_test

import (
	"slices"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

func TestMilestones(t *testing.T) {
	owner := storetest.New(t)
	// The UI lists milestones as the reader.
	s := asReader(t, owner)
	ctx := t.Context()
	base := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	exp := time.Now().Add(24 * time.Hour)
	other := store.ProjectKey{Namespace: "agents", Name: "other"}

	ms := []store.MilestoneSnapshot{
		{Project: bloop, IID: 20, Title: "Desk", State: "open", Phase: "implementing", StartedAt: base, TrackingIID: 300,
			Issues: []store.IssueSnapshot{
				{IID: 148, Title: "Schema", State: "merged", MergeRequestIID: 900},
				{IID: 149, Title: "Dropped", State: "skipped"},
				{IID: 150, Title: "Queries", State: "active"},
			},
			FollowUps: []store.FollowUpSnapshot{
				{Number: 1, Title: "Backfill", State: store.FollowUpOpen},
				{Number: 2, Title: "Docs", State: store.FollowUpOpen},
				{Number: 3, Title: "Seed", State: store.FollowUpIssue, TargetIID: 410},
			}},
		// Started later, no issues and no calls.
		{Project: bloop, IID: 21, Title: "Pilot", State: "open", Phase: "waiting", StartedAt: base.Add(time.Hour)},
		{Project: bloop, IID: 10, Title: "Old", State: "closed", Phase: "done", StartedAt: base, DoneAt: base.Add(2 * time.Hour)},
		{Project: bloop, IID: 11, Title: "Newer", State: "closed", Phase: "done", StartedAt: base, DoneAt: base.Add(3 * time.Hour)},
		// Another project with the same IID and costly calls.
		{Project: other, IID: 20, Title: "Elsewhere", State: "open", StartedAt: base.Add(-time.Hour),
			Issues:    []store.IssueSnapshot{{IID: 148, State: "merged"}},
			FollowUps: []store.FollowUpSnapshot{{Number: 1, State: store.FollowUpOpen}}},
	}
	for _, m := range ms {
		if _, err := owner.SaveMilestone(ctx, m, exp); err != nil {
			t.Fatal(err)
		}
	}

	usd := func(v float64) *float64 { return &v }
	call := func(key string, p store.ProjectKey, milestone, issue int64, cost *float64, in, out int64) store.Call {
		source := "reported"
		if cost == nil {
			source = "unknown"
		}
		return store.Call{Key: key, At: base.Add(time.Hour), Namespace: p.Namespace, Project: p.Name, MilestoneIID: milestone,
			IssueIID: issue, Task: "t-" + key, Run: 1, Seq: 1, Kind: "implement", Role: "implement", Engine: "claude",
			Model: "claude-opus-5-5", InputTokens: in, OutputTokens: out, CostUSD: cost, CostSource: source, ExpiresAt: exp}
	}
	if _, err := owner.InsertCalls(ctx, []store.Call{
		call("c1", bloop, 20, 148, usd(1.5), 10, 20),
		call("c2", bloop, 20, 150, nil, 5, 5),
		call("c3", other, 20, 148, usd(9), 1000, 1000),
		// No milestone: counts for none.
		call("c4", bloop, 0, 0, usd(4), 1, 1),
	}); err != nil {
		t.Fatal(err)
	}
	// Milestone 10's run from before agent_calls: its cost counts, without
	// tokens.
	if _, err := owner.InsertEvents(ctx, []store.Event{{Key: "e1", At: base, Namespace: "agents", Project: "bloop",
		MilestoneIID: 10, IssueIID: 140, Task: "bloop-140", Kind: "run.finished",
		Data: map[string]any{"costUSD": "2.0000", "runCount": 1}, ExpiresAt: exp}}); err != nil {
		t.Fatal(err)
	}

	list := func(q store.MilestoneQuery) []store.MilestoneRow {
		t.Helper()
		rows, err := s.Milestones(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	iids := func(rows []store.MilestoneRow) []int64 {
		var out []int64
		for _, r := range rows {
			out = append(out, r.IID)
		}
		return out
	}

	all := list(store.MilestoneQuery{Projects: []store.ProjectKey{bloop}})
	// Open first by start, then closed newest done first; other never.
	if got, want := iids(all), []int64{20, 21, 11, 10}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for _, r := range all {
		if r.Project != bloop {
			t.Errorf("row of %v listed", r.Project)
		}
	}

	m20 := all[0]
	if m20.Title != "Desk" || m20.Phase != "implementing" || m20.TrackingIID != 300 || !m20.StartedAt.Equal(base) || !m20.DoneAt.IsZero() {
		t.Errorf("milestone 20 = %+v", m20)
	}
	if m20.Issues != 3 || m20.Merged != 2 || m20.Open != 1 || m20.FollowUpsOpen != 2 {
		t.Errorf("milestone 20 progress = %d issues, %d merged, %d open, %d follow-ups open",
			m20.Issues, m20.Merged, m20.Open, m20.FollowUpsOpen)
	}
	if sp := m20.Spend; !sp.CostKnown || sp.CostUSD != 1.5 || sp.Calls != 2 || sp.UnknownCalls != 1 || sp.Tokens() != 40 {
		t.Errorf("milestone 20 spend = %+v, want only bloop's calls", sp)
	}

	m21 := all[1]
	if m21.Issues != 0 || m21.Open != 0 || m21.FollowUpsOpen != 0 {
		t.Errorf("milestone 21 progress = %+v", m21)
	}
	if sp := m21.Spend; sp.CostKnown || sp.CostUSD != 0 || sp.Calls != 0 || sp.Tokens() != 0 {
		t.Errorf("milestone 21 without calls: spend = %+v, want unknown", sp)
	}

	m10 := all[3]
	if !m10.DoneAt.Equal(base.Add(2*time.Hour)) || m10.State != "closed" {
		t.Errorf("milestone 10 = %+v", m10)
	}
	if sp := m10.Spend; !sp.CostKnown || sp.CostUSD != 2 || sp.LegacyCostUSD != 2 || sp.Calls != 0 || sp.Tokens() != 0 {
		t.Errorf("milestone 10 spend = %+v, want the legacy run's cost", sp)
	}

	if got := iids(list(store.MilestoneQuery{Projects: []store.ProjectKey{bloop}, State: "closed"})); !slices.Equal(got, []int64{11, 10}) {
		t.Errorf("closed = %v", got)
	}
	if got := iids(list(store.MilestoneQuery{Projects: []store.ProjectKey{bloop}, State: "open", Limit: 1})); !slices.Equal(got, []int64{20}) {
		t.Errorf("open, limit 1 = %v", got)
	}

	both := list(store.MilestoneQuery{Projects: []store.ProjectKey{bloop, other}, State: "open"})
	if len(both) != 3 || both[0].Project != other || both[0].Spend.CostUSD != 9 || both[0].Merged != 1 || both[0].FollowUpsOpen != 1 {
		t.Errorf("both projects, open = %+v", both)
	}

	if rows := list(store.MilestoneQuery{}); len(rows) != 0 {
		t.Errorf("no projects = %v, want none", rows)
	}
	if _, err := s.Milestones(ctx, store.MilestoneQuery{Projects: []store.ProjectKey{bloop}, State: "merged"}); err == nil {
		t.Error("unknown state: no error")
	}
}
