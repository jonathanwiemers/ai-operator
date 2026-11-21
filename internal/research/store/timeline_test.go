package store_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

var tl0 = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

// at is tl0 plus m minutes.
func at(m int) time.Time { return tl0.Add(time.Duration(m) * time.Minute) }

// tlEvent is an event of issue iid in milestone 4 of agents/shop, by task,
// at minute m. A hold event's data carries since (minute s, given as the
// last argument).
func tlEvent(iid int64, task, kind string, m int, since ...int) store.Event {
	e := store.Event{Key: fmt.Sprintf("%s/%d/%s/%d", task, iid, kind, m), At: at(m), Namespace: "agents", Project: "shop",
		MilestoneIID: 4, IssueIID: iid, Task: task, Kind: kind, Data: map[string]any{}}
	if len(since) > 0 {
		e.Data["since"] = at(since[0]).Format(time.RFC3339)
	}
	return e
}

// bar writes an issue's segments as "kind start-end" in minutes from tl0,
// "now" for an open end, and its markers.
func bar(ti store.TimelineIssue) string {
	var parts []string
	for _, s := range ti.Segments {
		end := "now"
		if s.End != nil {
			end = fmt.Sprint(int(s.End.Sub(tl0).Minutes()))
		}
		parts = append(parts, fmt.Sprintf("%s %d-%s", s.Kind, int(s.Start.Sub(tl0).Minutes()), end))
	}
	if ti.MergedAt != nil {
		parts = append(parts, fmt.Sprintf("merged %d", int(ti.MergedAt.Sub(tl0).Minutes())))
	}
	if ti.SkippedAt != nil {
		parts = append(parts, fmt.Sprintf("skipped %d", int(ti.SkippedAt.Sub(tl0).Minutes())))
	}
	return strings.Join(parts, ", ")
}

func TestBuildTimeline(t *testing.T) {
	const (
		started, finished = "run.started", "run.finished"
		set, cleared      = "hold.set", "hold.cleared"
	)
	for _, tc := range []struct {
		name          string
		events        []store.Event
		from, to, now int
		want          []string // one bar per issue, by iid
		wantIIDs      []int64
	}{{
		name: "overlapping runs of one issue",
		events: []store.Event{
			tlEvent(11, "impl", started, 0), tlEvent(11, "fix", started, 10), tlEvent(11, "impl", finished, 30),
			tlEvent(11, "fix", finished, 50), tlEvent(11, "spec", "merged", 60),
		},
		from: -60, to: 120, now: 100,
		want: []string{"running 0-50, waiting 50-60, merged 60"},
	}, {
		name: "two issues in parallel",
		events: []store.Event{
			tlEvent(12, "b", started, 5), tlEvent(11, "a", started, 0), tlEvent(11, "a", finished, 20),
			tlEvent(12, "b", finished, 25), tlEvent(11, "spec", "merged", 30), tlEvent(12, "spec", "merged", 40),
		},
		from: -60, to: 120, now: 100,
		want:     []string{"running 0-20, waiting 20-30, merged 30", "running 5-25, waiting 25-40, merged 40"},
		wantIIDs: []int64{11, 12},
	}, {
		name: "a hold inside waiting; running wins over held",
		events: []store.Event{
			tlEvent(11, "impl", started, 0), tlEvent(11, "spec", set, 5, 5), tlEvent(11, "impl", finished, 10),
			tlEvent(11, "spec", cleared, 15, 5), tlEvent(11, "spec", set, 20, 20), tlEvent(11, "spec", cleared, 40, 20),
			tlEvent(11, "spec", "merged", 60),
		},
		from: -60, to: 120, now: 100,
		want: []string{"running 0-10, held 10-15, waiting 15-20, held 20-40, waiting 40-60, merged 60"},
	}, {
		name: "a restarted hold: the new set is older than the old one's clear",
		events: []store.Event{
			tlEvent(11, "impl", started, 0), tlEvent(11, "impl", finished, 10), tlEvent(11, "spec", set, 20, 20),
			tlEvent(11, "spec", set, 25, 25), tlEvent(11, "spec", cleared, 30, 20), tlEvent(11, "spec", cleared, 40, 25),
			tlEvent(11, "spec", "merged", 50),
		},
		from: -60, to: 120, now: 100,
		want: []string{"running 0-10, waiting 10-20, held 20-40, waiting 40-50, merged 50"},
	}, {
		name: "a hold event without since takes it from its key",
		events: []store.Event{
			tlEvent(11, "impl", started, 0), tlEvent(11, "impl", finished, 10),
			{Key: fmt.Sprintf("task/u1/hold/11/%d/hold.cleared", at(20).Unix()), At: at(30), Namespace: "agents", Project: "shop",
				IssueIID: 11, Kind: cleared},
		},
		from: -60, to: 120, now: 40,
		want: []string{"running 0-10, waiting 10-20, held 20-30, waiting 30-now"},
	}, {
		name: "skipped, held when it was skipped",
		events: []store.Event{
			tlEvent(11, "impl", started, 0), tlEvent(11, "impl", finished, 10), tlEvent(11, "spec", set, 20, 20),
			tlEvent(11, "spec", "item.skipped", 30), tlEvent(11, "spec", cleared, 30, 20), tlEvent(11, "impl", "closed", 35),
		},
		from: -60, to: 120, now: 100,
		want: []string{"running 0-10, waiting 10-20, held 20-30, skipped 30"},
	}, {
		name: "closed without a merge is a skip; after a merge it is ignored",
		events: []store.Event{
			tlEvent(11, "a", started, 0), tlEvent(11, "a", finished, 10), tlEvent(11, "a", "closed", 20),
			tlEvent(12, "b", started, 0), tlEvent(12, "b", finished, 10), tlEvent(12, "spec", "merged", 20),
			tlEvent(12, "b", "closed", 30),
		},
		from: -60, to: 120, now: 100,
		want:     []string{"running 0-10, waiting 10-20, skipped 20", "running 0-10, waiting 10-20, merged 20"},
		wantIIDs: []int64{11, 12},
	}, {
		name: "a run after a close reopens the issue",
		events: []store.Event{
			tlEvent(11, "a", started, 0), tlEvent(11, "a", finished, 10), tlEvent(11, "a", "closed", 20),
			tlEvent(11, "b", started, 30), tlEvent(11, "b", finished, 40), tlEvent(11, "spec", "merged", 50),
		},
		from: -60, to: 120, now: 100,
		want: []string{"running 0-10, waiting 10-30, running 30-40, waiting 40-50, merged 50"},
	}, {
		name:   "an open issue goes on up to now",
		events: []store.Event{tlEvent(11, "impl", started, 0), tlEvent(11, "impl", finished, 10)},
		from:   -60, to: 120, now: 30,
		want: []string{"running 0-10, waiting 10-now"},
	}, {
		name:   "a running issue goes on up to now",
		events: []store.Event{tlEvent(11, "impl", started, 0)},
		from:   -60, to: 120, now: 30,
		want: []string{"running 0-now"},
	}, {
		name: "cut at both window edges; a window before now ends at to",
		events: []store.Event{
			tlEvent(11, "impl", started, 0), tlEvent(11, "impl", finished, 10), tlEvent(11, "spec", "merged", 50),
			tlEvent(12, "impl2", started, 0),
		},
		from: 5, to: 25, now: 100,
		want:     []string{"running 5-10, waiting 10-25, merged 50", "running 5-25"},
		wantIIDs: []int64{11, 12},
	}, {
		name: "a run start without a finish runs to the task's next start",
		events: []store.Event{
			tlEvent(11, "impl", started, 0), tlEvent(11, "impl", started, 20), tlEvent(11, "impl", finished, 30),
			tlEvent(11, "impl", started, 40), tlEvent(11, "spec", "merged", 60),
		},
		from: -60, to: 120, now: 100,
		want: []string{"running 0-30, waiting 30-40, running 40-60, merged 60"},
	}, {
		name: "a finish without its start counts back its duration",
		events: []store.Event{
			func() store.Event {
				e := tlEvent(11, "impl", finished, 30)
				e.Data["durationSec"] = float64(600)
				return e
			}(),
			tlEvent(11, "spec", "merged", 40),
		},
		from: -60, to: 120, now: 100,
		want: []string{"running 20-30, waiting 30-40, merged 40"},
	}, {
		name: "issues outside the window, and events without an issue, are left out",
		events: []store.Event{
			tlEvent(11, "a", started, 0), tlEvent(11, "a", finished, 10), tlEvent(11, "spec", "merged", 20),
			tlEvent(12, "b", started, 0), tlEvent(12, "b", finished, 10), tlEvent(12, "spec", "merged", 60),
			tlEvent(0, "spec", started, 50),
		},
		from: 30, to: 120, now: 100,
		want:     []string{"waiting 30-60, merged 60"},
		wantIIDs: []int64{12},
	}, {
		name: "a marker in the window alone keeps the issue",
		events: []store.Event{
			tlEvent(11, "spec", "merged", 40),
		},
		from: 30, to: 120, now: 100,
		want: []string{"merged 40"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got := store.BuildTimeline(tc.events, at(tc.from), at(tc.to), at(tc.now))
			if len(got) != len(tc.want) {
				t.Fatalf("%d bars, want %d: %+v", len(got), len(tc.want), got)
			}
			for i, ti := range got {
				if b := bar(ti); b != tc.want[i] {
					t.Errorf("bar %d = %q, want %q", i, b, tc.want[i])
				}
				if tc.wantIIDs != nil && ti.IssueIID != tc.wantIIDs[i] {
					t.Errorf("bar %d is #%d, want #%d", i, ti.IssueIID, tc.wantIIDs[i])
				}
				if ti.Namespace != "agents" || ti.Project != "shop" || ti.IssueIID == 0 || (len(tc.events) > 0 && ti.MilestoneIID != 4) {
					t.Errorf("bar %d = %+v", i, ti)
				}
			}
		})
	}
}
