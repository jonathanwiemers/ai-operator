package store_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

// TestTimelineEvents: the issues whose life overlaps the window, with their
// timeline events before its end, oldest first; by project and milestone,
// capped at the issues with the newest events.
func TestTimelineEvents(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	from, to := now.Add(-24*time.Hour), now
	exp, gone := now.Add(24*time.Hour), now.Add(-time.Minute)
	n := 0
	ev := func(project string, milestone, iid int64, kind string, at time.Time) store.Event {
		n++
		return store.Event{Key: fmt.Sprintf("tl/%d", n), At: at, Namespace: "agents", Project: project, MilestoneIID: milestone,
			IssueIID: iid, Task: fmt.Sprintf("%s-%d", project, iid), Kind: kind, ExpiresAt: exp}
	}
	expired := ev("shop", 4, 8, "run.started", now.Add(-time.Hour))
	expired.ExpiresAt = gone
	if _, err := owner.InsertEvents(ctx, []store.Event{
		// #1 ran in the window; its push is no timeline kind.
		ev("shop", 4, 1, "run.started", now.Add(-2*time.Hour)),
		ev("shop", 4, 1, "push", now.Add(-90*time.Minute)),
		ev("shop", 4, 1, "run.finished", now.Add(-time.Hour)),
		// #2 started before the window and is still open: the lookback.
		ev("shop", 4, 2, "run.started", from.Add(-3*time.Hour)),
		// #3 merged before the window.
		ev("shop", 4, 3, "run.started", from.Add(-5*time.Hour)),
		ev("shop", 4, 3, "merged", from.Add(-4*time.Hour)),
		// #4 was closed, then ran again before the window.
		ev("shop", 4, 4, "run.started", from.Add(-6*time.Hour)),
		ev("shop", 4, 4, "closed", from.Add(-5*time.Hour)),
		ev("shop", 4, 4, "run.started", from.Add(-time.Hour)),
		// #5 starts after the window.
		ev("shop", 4, 5, "run.started", now.Add(time.Hour)),
		// #6 is another project's, #7 another milestone's.
		ev("billing", 4, 6, "run.started", now.Add(-3*time.Hour)),
		ev("shop", 9, 7, "run.started", now.Add(-30*time.Minute)),
		// #8 expired; an event without an issue.
		expired,
		ev("shop", 4, 0, "run.started", now.Add(-time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}
	shop := []store.ProjectKey{{Namespace: "agents", Name: "shop"}}
	read := func(q store.TimelineQuery) ([]store.Event, []int64, bool) {
		t.Helper()
		q.From, q.To = from, to
		events, truncated, err := s.TimelineEvents(ctx, q, now)
		if err != nil {
			t.Fatal(err)
		}
		var iids []int64
		for i, e := range events {
			if i > 0 && e.At.Before(events[i-1].At) {
				t.Errorf("not oldest first: %+v", events)
			}
			iids = append(iids, e.IssueIID)
		}
		return events, slices.Compact(slices.Sorted(slices.Values(iids))), truncated
	}

	events, iids, truncated := read(store.TimelineQuery{Projects: shop})
	if !slices.Equal(iids, []int64{1, 2, 4, 7}) || truncated {
		t.Fatalf("issues %v, truncated %v", iids, truncated)
	}
	var kinds []string
	for _, e := range events {
		if e.IssueIID == 1 || e.IssueIID == 4 {
			kinds = append(kinds, fmt.Sprintf("#%d %s", e.IssueIID, e.Kind))
		}
	}
	if want := []string{"#4 run.started", "#4 closed", "#4 run.started", "#1 run.started", "#1 run.finished"}; !slices.Equal(kinds, want) {
		t.Errorf("events %v, want %v", kinds, want)
	}

	if _, iids, _ := read(store.TimelineQuery{Projects: shop, Milestone: 4}); !slices.Equal(iids, []int64{1, 2, 4}) {
		t.Errorf("milestone 4: %v", iids)
	}
	if _, iids, _ := read(store.TimelineQuery{Projects: shop, Milestone: 9}); !slices.Equal(iids, []int64{7}) {
		t.Errorf("milestone 9: %v", iids)
	}
	if _, iids, _ := read(store.TimelineQuery{Projects: []store.ProjectKey{{Namespace: "agents", Name: "billing"}}}); !slices.Equal(iids, []int64{6}) {
		t.Errorf("billing: %v", iids)
	}
	if events, _, _ := read(store.TimelineQuery{}); len(events) != 0 {
		t.Errorf("no projects: %+v", events)
	}

	// The cap keeps the issues with the newest events: #7, then #1.
	store.SetMaxTimelineIssues(t, 2)
	if _, iids, truncated := read(store.TimelineQuery{Projects: shop}); !slices.Equal(iids, []int64{1, 7}) || !truncated {
		t.Errorf("capped: %v, truncated %v", iids, truncated)
	}

	// The events build the bars: #2 has run since before the window.
	all, _, _ := read(store.TimelineQuery{Projects: shop, Milestone: 4})
	for _, ti := range store.BuildTimeline(all, from, to, now) {
		if ti.IssueIID == 2 && (len(ti.Segments) != 1 || ti.Segments[0].Kind != store.SegmentRunning ||
			!ti.Segments[0].Start.Equal(from) || ti.Segments[0].End != nil) {
			t.Errorf("#2 = %+v", ti)
		}
	}
}
