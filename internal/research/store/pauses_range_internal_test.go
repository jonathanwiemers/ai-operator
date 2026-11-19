package store

import (
	"slices"
	"testing"
	"time"
)

// rangeEvent is pauseEventOf of task uid in milestone iid of agents/project.
func rangeEvent(project string, iid int64, uid, kind string, min, start int, pk, by string) Event {
	e := pauseEventOf(uid, kind, min, start, pk, by, nil)
	e.Namespace, e.Project, e.MilestoneIID = "agents", project, iid
	return e
}

// newestFirst is events as PausesInRange reads them.
func newestFirst(events ...Event) []Event {
	slices.SortStableFunc(events, func(a, b Event) int { return b.At.Compare(a.At) })
	return events
}

// TestPauseRangeAcceptance: a milestone paused twice for red CI (2 h and
// 30 min, both resumed by the owner) and paused now for a budget stop.
func TestPauseRangeAcceptance(t *testing.T) {
	const h = 60
	now := pauseBase.Add(10 * h * time.Minute)
	q := CostQuery{From: pauseBase.Add(-29 * 24 * time.Hour), To: pauseBase.Add(24 * time.Hour), Loc: time.UTC}
	events := newestFirst(
		rangeEvent("shop", 4, "u1", pausedKind, 0, 0, "ciRed", ""),
		rangeEvent("shop", 4, "u1", resumedKind, 2*h, 0, "ciRed", "owner"),
		rangeEvent("shop", 4, "u1", pausedKind, 3*h, 3*h, "ciRed", ""),
		rangeEvent("shop", 4, "u1", resumedKind, 3*h+30, 3*h, "ciRed", "owner"),
		rangeEvent("shop", 4, "u1", pausedKind, 4*h, 4*h, "budget", ""),
	)
	r := pauseRange(events, q, "day", now)
	tt := r.Totals
	if tt.Count != 3 || tt.ByKind["ciRed"] != (PauseKindTotal{Count: 2, Sec: 9000}) || tt.ByResolver["owner"] != 2 || r.OpenNow != 1 {
		t.Errorf("totals = %+v, open %d", tt, r.OpenNow)
	}
	// Budget: open for 6 h up to now; the mean time to resume is of the two
	// resumed ones only.
	if tt.PausedSec != 9000+6*3600 || tt.MeanResumeSec != 4500 || tt.ByKind["budget"] != (PauseKindTotal{Count: 1, Sec: 6 * 3600}) {
		t.Errorf("seconds = %+v", tt)
	}
	if len(r.Longest) != 3 {
		t.Fatalf("longest = %+v", r.Longest)
	}
	if l := r.Longest[0]; l.Kind != "budget" || l.Until != nil || l.Seconds != 6*3600 || l.Project != "shop" || l.MilestoneIID != 4 ||
		l.Reason != "budget reason" {
		t.Errorf("longest[0] = %+v", l)
	}
	if r.Longest[1].Seconds != 7200 || r.Longest[2].Seconds != 1800 {
		t.Errorf("longest = %+v", r.Longest)
	}
	if len(r.Buckets) != 1 || r.Buckets[0].Bucket != "2026-11-05" || r.Buckets[0].Count != 3 || r.Buckets[0].ByKind["ciRed"].Count != 2 {
		t.Errorf("buckets = %+v", r.Buckets)
	}
	if len(r.ByProject) != 1 || r.ByProject[0] != (PauseProject{Namespace: "agents", Project: "shop", Count: 3, PausedSec: tt.PausedSec}) {
		t.Errorf("by project = %+v", r.ByProject)
	}
}

// TestPauseRangeEdges: a pause that started before the range is left out,
// also when its paused event is not read; one that started at To too. Two
// milestones with tasks of the same uid pair apart.
func TestPauseRangeEdges(t *testing.T) {
	now := pauseBase.Add(300 * time.Minute)
	q := CostQuery{From: pauseBase.Add(60 * time.Minute), To: pauseBase.Add(240 * time.Minute)}
	events := newestFirst(
		// Started before From, ended in the range: out, paused event read or not.
		rangeEvent("shop", 1, "u1", pausedKind, 0, 0, "stuck", ""),
		rangeEvent("shop", 1, "u1", resumedKind, 90, 0, "stuck", "owner"),
		rangeEvent("shop", 2, "u2", resumedKind, 100, 10, "stuck", "pilot"),
		// In: from From on.
		rangeEvent("shop", 1, "u1", pausedKind, 60, 60, "ciRed", ""),
		rangeEvent("shop", 1, "u1", resumedKind, 70, 60, "ciRed", "selfHealed"),
		// Same uid, another milestone: its own pause, still open.
		rangeEvent("billing", 1, "u1", pausedKind, 80, 80, "config", ""),
		// At To: out.
		rangeEvent("shop", 2, "u2", pausedKind, 240, 240, "budget", ""),
	)
	r := pauseRange(events, q, "day", now)
	if r.Totals.Count != 2 || r.OpenNow != 1 || r.Totals.ByKind["stuck"].Count != 0 {
		t.Fatalf("totals = %+v, open %d", r.Totals, r.OpenNow)
	}
	// The open config pause counts up to now: 220 minutes.
	if l := r.Longest[0]; l.Project != "billing" || l.Kind != "config" || l.Seconds != 220*60 {
		t.Errorf("longest = %+v", r.Longest)
	}
	if len(r.ByProject) != 2 || r.ByProject[0].Project != "billing" || r.ByProject[1] != (PauseProject{Namespace: "agents", Project: "shop", Count: 1, PausedSec: 600}) {
		t.Errorf("by project = %+v", r.ByProject)
	}
	if empty := pauseRange(nil, q, "day", now); empty.Buckets == nil || empty.ByProject == nil || empty.Longest == nil ||
		empty.Totals.ByKind == nil || empty.Totals.Count != 0 {
		t.Errorf("empty = %+v", empty)
	}
}

// TestPauseRangeBuckets: pauses fall into the local dates of the zone, the
// way CostSeries truncates; Longest keeps the ten longest.
func TestPauseRangeBuckets(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-11-05 22:30 UTC is 23:30 in Berlin, 23:30 UTC is 00:30 of the 6th.
	late := time.Date(2026, 11, 5, 22, 30, 0, 0, time.UTC)
	ev := func(uid string, at time.Time, kind string) Event {
		start := int(at.Sub(pauseBase) / time.Minute)
		e := rangeEvent("shop", 1, uid, kind, start, start, "ciRed", "owner")
		if kind == resumedKind {
			e.At = e.At.Add(10 * time.Minute)
		}
		return e
	}
	events := newestFirst(
		ev("a", late, pausedKind), ev("a", late, resumedKind),
		ev("b", late.Add(time.Hour), pausedKind), ev("b", late.Add(time.Hour), resumedKind),
	)
	q := CostQuery{From: time.Date(2026, 11, 1, 0, 0, 0, 0, berlin), To: time.Date(2026, 12, 1, 0, 0, 0, 0, berlin), Loc: berlin}
	now := late.Add(5 * time.Hour)
	for bucket, want := range map[string][]string{
		"day":   {"2026-11-05", "2026-11-06"},
		"week":  {"2026-11-02"},
		"month": {"2026-11-01"},
	} {
		r := pauseRange(events, q, bucket, now)
		var got []string
		for _, b := range r.Buckets {
			got = append(got, b.Bucket)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s buckets = %v, want %v", bucket, got, want)
		}
	}
	// In UTC both are the 5th.
	q.Loc = nil
	if r := pauseRange(events, q, "day", now); len(r.Buckets) != 1 || r.Buckets[0].Bucket != "2026-11-05" || r.Buckets[0].PausedSec != 1200 {
		t.Errorf("UTC buckets = %+v", r.Buckets)
	}

	var many []Event
	for i := range 12 {
		many = append(many, rangeEvent("shop", 1, "u1", pausedKind, i*100, i*100, "stuck", ""),
			rangeEvent("shop", 1, "u1", resumedKind, i*100+i+1, i*100, "stuck", "owner"))
	}
	q = CostQuery{From: pauseBase, To: pauseBase.Add(48 * time.Hour)}
	r := pauseRange(newestFirst(many...), q, "day", pauseBase.Add(48*time.Hour))
	if len(r.Longest) != 10 || r.Longest[0].Seconds != 12*60 || r.Longest[9].Seconds != 3*60 || r.Totals.Count != 12 {
		t.Errorf("longest = %+v", r.Longest)
	}
}
