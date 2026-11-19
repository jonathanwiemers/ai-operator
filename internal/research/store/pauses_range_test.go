package store_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

// pauseRow is a pause event of task uid in milestone 4 of agents/project at
// at, for a pause of kind pk that started at start (keyed like
// internal/history).
func pauseRow(project, uid, kind string, at, start time.Time, pk, by string, exp time.Time) store.Event {
	d := map[string]any{"kind": pk, "reason": pk + " reason"}
	if kind == "autopilot.resumed" {
		d["resolvedBy"], d["via"], d["pausedSec"] = by, "command", int64(at.Sub(start).Seconds())
	}
	return store.Event{Key: fmt.Sprintf("task/%s/%s/%d/%s", uid, kind, start.Unix(), pk), At: at, Namespace: "agents",
		Project: project, MilestoneIID: 4, Task: project + "-m4", Kind: kind, Data: d, ExpiresAt: exp}
}

// TestPausesInRange: the acceptance scenario of the Metrics page's Pauses
// over the store: two red-CI pauses resumed by the owner, a budget stop open
// now; a pause that started before the range, an expired one, another
// project's and other event kinds stay out.
func TestPausesInRange(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	exp, gone := now.Add(24*time.Hour), now.Add(-time.Minute)
	from := now.Add(-30 * 24 * time.Hour)
	ci1, ci2, budget, before := now.Add(-48*time.Hour), now.Add(-24*time.Hour), now.Add(-3*time.Hour), from.Add(-time.Hour)
	if _, err := owner.InsertEvents(ctx, []store.Event{
		pauseRow("shop", "u1", "autopilot.paused", ci1, ci1, "ciRed", "", exp),
		pauseRow("shop", "u1", "autopilot.resumed", ci1.Add(2*time.Hour), ci1, "ciRed", "owner", exp),
		pauseRow("shop", "u1", "autopilot.paused", ci2, ci2, "ciRed", "", exp),
		pauseRow("shop", "u1", "autopilot.resumed", ci2.Add(30*time.Minute), ci2, "ciRed", "owner", exp),
		pauseRow("shop", "u1", "autopilot.paused", budget, budget, "budget", "", exp),
		// Started before the range, ended in it: its resumed event is read.
		pauseRow("shop", "u2", "autopilot.paused", before, before, "stuck", "", exp),
		pauseRow("shop", "u2", "autopilot.resumed", from.Add(time.Hour), before, "stuck", "pilot", exp),
		// Expired.
		pauseRow("shop", "u3", "autopilot.paused", ci1, ci1.Add(-time.Second), "config", "", gone),
		// Not one of the projects.
		pauseRow("billing", "u4", "autopilot.paused", ci1, ci1, "config", "", exp),
		// Not a pause event.
		{Key: "m1", At: ci1, Namespace: "agents", Project: "shop", MilestoneIID: 4, Kind: "merged", ExpiresAt: exp},
	}); err != nil {
		t.Fatal(err)
	}
	q := store.CostQuery{Projects: []store.ProjectKey{{Namespace: "agents", Name: "shop"}}, From: from, To: now.Add(time.Hour),
		Loc: time.UTC, Kind: "review", Engine: "cursor"}
	r, err := s.PausesInRange(ctx, q, "day", now)
	if err != nil {
		t.Fatal(err)
	}
	tt := r.Totals
	if tt.Count != 3 || tt.ByKind["ciRed"] != (store.PauseKindTotal{Count: 2, Sec: 9000}) || tt.ByResolver["owner"] != 2 ||
		tt.ByVia["command"] != 2 || r.OpenNow != 1 || r.Truncated {
		t.Errorf("totals = %+v, open %d, truncated %v", tt, r.OpenNow, r.Truncated)
	}
	if len(r.Longest) != 3 || r.Longest[0].Kind != "budget" || r.Longest[0].Until != nil || r.Longest[0].Seconds != 3*3600 ||
		r.Longest[0].MilestoneIID != 4 || r.Longest[0].Project != "shop" {
		t.Errorf("longest = %+v", r.Longest)
	}
	n := 0
	for _, b := range r.Buckets {
		n += b.Count
	}
	if n != 3 || len(r.ByProject) != 1 || r.ByProject[0].Count != 3 {
		t.Errorf("buckets %+v, projects %+v", r.Buckets, r.ByProject)
	}

	// No projects: nothing, not nil.
	if r, err := s.PausesInRange(ctx, store.CostQuery{From: from, To: now}, "day", now); err != nil || r.Totals.Count != 0 || r.Longest == nil {
		t.Errorf("no projects = %+v, %v", r, err)
	}
	if _, err := s.PausesInRange(ctx, q, "year", now); err == nil {
		t.Error("unknown bucket: no error")
	}

	// Truncated: only the newest events are read, here the budget pause and
	// the resumed event of the second CI pause, a pause of its own.
	store.SetMaxRangePauseEvents(t, 2)
	r, err = s.PausesInRange(ctx, q, "day", now)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Truncated || r.Totals.Count != 2 || r.Totals.ByKind["ciRed"] != (store.PauseKindTotal{Count: 1, Sec: 1800}) {
		t.Errorf("truncated = %v, totals %+v", r.Truncated, r.Totals)
	}
}

// TestPausesInRangeUsesTimeIndex: the events are read newest first through
// history_events_at_idx.
func TestPausesInRangeUsesTimeIndex(t *testing.T) {
	owner := storetest.New(t)
	q := store.CostQuery{Projects: []store.ProjectKey{{Namespace: "agents", Name: "shop"}, {Namespace: "agents", Name: "billing"}},
		From: time.Now().Add(-30 * 24 * time.Hour), To: time.Now()}
	sql, args := store.PausesInRangeQuery(q, time.Now())
	assertUsesIndex(t, owner.Pool(), sql, args, "history_events_at_idx")
}
