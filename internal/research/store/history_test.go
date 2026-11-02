package store_test

import (
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

func TestHistoryEvents(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	base := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	exp := base.Add(24 * time.Hour)
	ev := func(key string, minutes int, kind string, issue, milestone int64, data map[string]any) store.Event {
		return store.Event{Key: key, At: base.Add(time.Duration(minutes) * time.Minute), Namespace: "agents",
			Project: "bloop", IssueIID: issue, MilestoneIID: milestone, Kind: kind, Summary: key, Data: data, ExpiresAt: exp}
	}
	events := []store.Event{
		ev("note-1", 0, "note", 142, 0, map[string]any{"body": "hold merges please"}),
		ev("run-1", 1, "run.finished", 142, 20, map[string]any{"costUSD": "1.25"}),
		ev("run-2", 2, "run.finished", 143, 20, map[string]any{"costUSD": "0.75"}),
		ev("merged-1", 3, "merged", 142, 20, nil),
		ev("other", 4, "note", 900, 0, nil),
	}
	n, err := s.InsertEvents(ctx, events)
	if err != nil || n != 5 {
		t.Fatalf("InsertEvents = %d, %v; want 5", n, err)
	}
	// The same keys again write nothing.
	if n, err := s.InsertEvents(ctx, events[:2]); err != nil || n != 0 {
		t.Fatalf("InsertEvents again = %d, %v; want 0", n, err)
	}
	other := store.Event{Key: "elsewhere", At: base, Namespace: "agents", Project: "shop", Kind: "note", ExpiresAt: exp}
	if _, err := s.InsertEvents(ctx, []store.Event{other}); err != nil {
		t.Fatal(err)
	}

	bloop := []store.ProjectKey{{Namespace: "agents", Name: "bloop"}}
	keys := func(q store.StreamQuery) []string {
		t.Helper()
		got, err := s.Stream(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		var ks []string
		for _, e := range got {
			ks = append(ks, e.Key)
		}
		return ks
	}
	check := func(name string, q store.StreamQuery, want ...string) {
		t.Helper()
		if got := keys(q); !equal(got, want) {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}

	check("newest first", store.StreamQuery{Projects: bloop}, "other", "merged-1", "run-2", "run-1", "note-1")
	check("no projects", store.StreamQuery{})
	check("page", store.StreamQuery{Projects: bloop, Limit: 2}, "other", "merged-1")
	all, _ := s.Stream(ctx, store.StreamQuery{Projects: bloop})
	check("before", store.StreamQuery{Projects: bloop, Before: &store.Cursor{At: all[1].At, ID: all[1].ID}, Limit: 2}, "run-2", "run-1")
	check("before a time", store.StreamQuery{Projects: bloop, Before: &store.Cursor{At: base.Add(2 * time.Minute)}}, "run-1", "note-1")
	check("after", store.StreamQuery{Projects: bloop, After: &store.Cursor{At: all[3].At, ID: all[3].ID}}, "other", "merged-1", "run-2")
	check("after, oldest first page", store.StreamQuery{Projects: bloop, After: &store.Cursor{At: all[4].At, ID: all[4].ID}, Limit: 2}, "run-2", "run-1")
	check("milestone", store.StreamQuery{Projects: bloop, Milestone: 20, Issues: []int64{142}}, "merged-1", "run-2", "run-1", "note-1")
	check("kinds", store.StreamQuery{Projects: bloop, Kinds: []string{"note"}}, "other", "note-1")
	check("search", store.StreamQuery{Projects: bloop, Text: "merges"}, "note-1")
	check("search without comments", store.StreamQuery{Projects: bloop, Text: "merges", TextWithoutNotes: bloop})

	days, err := s.Days(ctx, bloop[0], base.Add(-time.Hour), time.UTC)
	if err != nil || len(days) != 1 {
		t.Fatalf("Days = %v, %v", days, err)
	}
	if d := days[0]; d.Runs != 2 || d.Merged != 1 || d.CostUSD != 2 || d.Events != 5 {
		t.Errorf("day = %+v", d)
	}

	if n, err := s.DeleteExpiredEvents(ctx, exp.Add(time.Second)); err != nil || n != 6 {
		t.Errorf("DeleteExpiredEvents = %d, %v; want 6", n, err)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
