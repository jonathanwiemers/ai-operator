package store_test

import (
	"fmt"
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
	check("issue of the milestone", store.StreamQuery{Projects: bloop, Milestone: 20, Issue: 142}, "merged-1", "run-1", "note-1")
	check("issue in another milestone", store.StreamQuery{Projects: bloop, Milestone: 21, Issue: 142}, "note-1")
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

func TestPauses(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	base := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	now := base.Add(100 * time.Minute)
	exp := now.Add(24 * time.Hour)
	ev := func(kind string, minutes, start int, project string, milestone int64, pk, by string, expires time.Time) store.Event {
		d := map[string]any{"kind": pk, "reason": "why " + pk, "item": int64(88)}
		if kind == "autopilot.resumed" {
			d["resolvedBy"], d["pausedSec"] = by, int64((minutes-start)*60)
		}
		startAt := base.Add(time.Duration(start) * time.Minute)
		return store.Event{Key: fmt.Sprintf("task/u1/%s/%d/%s", kind, startAt.Unix(), pk), At: base.Add(time.Duration(minutes) * time.Minute),
			Namespace: "agents", Project: project, MilestoneIID: milestone, IssueIID: 7, Kind: kind, Summary: kind, Data: d, ExpiresAt: expires}
	}
	events := []store.Event{
		ev("autopilot.paused", 0, 0, "bloop", 24, "ciRed", "", exp),
		ev("autopilot.resumed", 10, 0, "bloop", 24, "ciRed", "selfHealed", exp),
		ev("autopilot.paused", 20, 20, "bloop", 24, "stuck", "", exp),
		ev("autopilot.resumed", 50, 20, "bloop", 24, "stuck", "owner", exp),
		ev("autopilot.paused", 90, 90, "bloop", 24, "budget", "", exp),
		// Past the retention: left out.
		ev("autopilot.paused", -60, -60, "bloop", 24, "review", "", now.Add(-time.Minute)),
		// Other kinds, milestones and projects: left out.
		{Key: "autopilot-1", At: base, Namespace: "agents", Project: "bloop", MilestoneIID: 24, Kind: "autopilot", ExpiresAt: exp},
		ev("autopilot.paused", 5, 5, "bloop", 25, "merge", "", exp),
		ev("autopilot.paused", 6, 6, "shop", 24, "merge", "", exp),
	}
	// Keys must differ across projects and milestones.
	events[7].Key += "/m25"
	events[8].Key += "/shop"
	// The stuck pause says how it ended; the ciRed one is from before "via".
	events[3].Data["via"] = "takeover"
	// An event without an issue (NULL issue_iid) still reads.
	events[4].IssueIID = 0
	if _, err := s.InsertEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	h, err := s.Pauses(ctx, store.ProjectKey{Namespace: "agents", Name: "bloop"}, 24, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Pauses) != 3 {
		t.Fatalf("pauses = %+v", h.Pauses)
	}
	if p := h.Pauses[0]; p.Kind != "ciRed" || p.Reason != "why ciRed" || p.Item != 88 || p.ResolvedBy != "selfHealed" ||
		p.Seconds != 600 || !p.Since.Equal(base) || p.Until == nil || !p.Until.Equal(base.Add(10*time.Minute)) {
		t.Errorf("first pause = %+v", p)
	}
	if p := h.Pauses[2]; p.Kind != "budget" || p.Until != nil || p.Seconds != 600 {
		t.Errorf("open pause = %+v", p)
	}
	tot := h.Totals
	if tot.Count != 3 || tot.PausedSec != 600+1800+600 || tot.MeanResumeSec != 1200 ||
		tot.ByKind["stuck"] != (store.PauseKindTotal{Count: 1, Sec: 1800}) || len(tot.ByKind) != 3 ||
		tot.ByResolver["owner"] != 1 || tot.ByResolver["selfHealed"] != 1 || len(tot.ByResolver) != 2 ||
		tot.ByVia["takeover"] != 1 || tot.ByVia["unknown"] != 1 || len(tot.ByVia) != 2 {
		t.Errorf("totals = %+v", tot)
	}
	if h.Pauses[0].Via != "" || h.Pauses[1].Via != "takeover" || h.Pauses[2].Via != "" {
		t.Errorf("via = %q, %q, %q", h.Pauses[0].Via, h.Pauses[1].Via, h.Pauses[2].Via)
	}

	if h, err := s.Pauses(ctx, store.ProjectKey{Namespace: "agents", Name: "bloop"}, 99, now); err != nil || len(h.Pauses) != 0 || h.Totals.Count != 0 {
		t.Errorf("no pauses = %+v, %v", h, err)
	}
}
