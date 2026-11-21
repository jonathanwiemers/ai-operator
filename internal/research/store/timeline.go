package store

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"
	"time"
)

// The issue timeline: when each issue ran, waited and was held, from its
// history events, for the Desk's Timeline.

// The event kinds of the timeline (internal/history's names); merged,
// closed and item.skipped end an issue's life.
const (
	tlRunStarted  = "run.started"
	tlRunFinished = "run.finished"
	tlHoldSet     = "hold.set"
	tlHoldCleared = "hold.cleared"
	tlMerged      = "merged"
	tlClosed      = "closed"
	tlSkipped     = "item.skipped"
)

// The segment kinds of a timeline bar.
const (
	SegmentRunning = "running"
	SegmentWaiting = "waiting"
	SegmentHeld    = "held"
)

// TimelineSegment is a part of an issue's life of Kind (SegmentRunning,
// SegmentWaiting, SegmentHeld). End is nil while it goes on.
type TimelineSegment struct {
	Kind  string
	Start time.Time
	End   *time.Time
}

// TimelineIssue is an issue's bar: its segments in the window, oldest
// first, and when it merged or was skipped (closed without a merge).
type TimelineIssue struct {
	Namespace, Project     string
	MilestoneIID, IssueIID int64
	Segments               []TimelineSegment
	MergedAt, SkippedAt    *time.Time
}

// holdKeySince is the hold's start in a hold event's key
// (internal/history: hold/<iid>/<since unix>/<kind>), for events written
// before they carried since.
var holdKeySince = regexp.MustCompile(`/hold/\d+/(-?\d+)/`)

// span is a half-open interval [start, end).
type span struct{ start, end time.Time }

func inSpans(ss []span, t time.Time) bool {
	return slices.ContainsFunc(ss, func(s span) bool { return !t.Before(s.start) && t.Before(s.end) })
}

// BuildTimeline turns the timeline events of issues (any order) into their
// bars in [from, to), by project, milestone and issue. An issue's life
// starts at its first run (or hold) and ends at its merge, its skip (or a
// close without a merge, unless a run starts after it) or now. In it, an
// issue runs from each run.started to its task's run.finished (a start
// without one runs to the task's next start, else on), is held from
// hold.set to hold.cleared, and waits otherwise; running wins over held.
// Segments are cut at from and to; the one an open issue is in at now has
// End nil when now is in the window. Issues without a segment or a marker
// in the window are left out.
func BuildTimeline(events []Event, from, to, now time.Time) []TimelineIssue {
	type key struct {
		ns, project string
		iid         int64
	}
	groups := map[key][]Event{}
	for _, e := range events {
		if e.IssueIID != 0 {
			k := key{e.Namespace, e.Project, e.IssueIID}
			groups[k] = append(groups[k], e)
		}
	}
	var out []TimelineIssue
	for k, es := range groups {
		slices.SortStableFunc(es, func(a, b Event) int { return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.ID, b.ID)) })
		ti := issueTimeline(es, from, to, now)
		if len(ti.Segments) == 0 && !inWindow(ti.MergedAt, from, to) && !inWindow(ti.SkippedAt, from, to) {
			continue
		}
		ti.Namespace, ti.Project, ti.IssueIID = k.ns, k.project, k.iid
		out = append(out, ti)
	}
	slices.SortFunc(out, func(a, b TimelineIssue) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Project, b.Project),
			cmp.Compare(a.MilestoneIID, b.MilestoneIID), cmp.Compare(a.IssueIID, b.IssueIID))
	})
	return out
}

func inWindow(t *time.Time, from, to time.Time) bool {
	return t != nil && !t.Before(from) && t.Before(to)
}

// issueTimeline builds the bar of one issue's events, oldest first.
func issueTimeline(es []Event, from, to, now time.Time) TimelineIssue {
	var ti TimelineIssue
	var runs, holdSpans []span
	open := map[string]time.Time{} // a task's run start without a finish
	holds := map[int64]*span{}     // by the hold's start, unix seconds
	var holdOrder []int64
	var end *time.Time // the life's end, merged or skipped
	merged := false
	for _, e := range es {
		if e.MilestoneIID != 0 {
			ti.MilestoneIID = e.MilestoneIID
		}
		if merged || (end != nil && e.Kind != tlRunStarted && e.Kind != tlMerged) {
			continue
		}
		switch e.Kind {
		case tlRunStarted:
			if end != nil { // closed or skipped, and runs again
				end = nil
			}
			if s, ok := open[e.Task]; ok {
				runs = append(runs, span{s, e.At})
			}
			open[e.Task] = e.At
		case tlRunFinished:
			if s, ok := open[e.Task]; ok {
				runs = append(runs, span{s, e.At})
				delete(open, e.Task)
			} else if d, ok := number(e.Data["durationSec"]); ok && d > 0 {
				runs = append(runs, span{e.At.Add(-time.Duration(d) * time.Second), e.At})
			}
		case tlHoldSet, tlHoldCleared:
			since, ok := holdSince(e)
			if !ok {
				continue
			}
			h := holds[since]
			if h == nil {
				h = &span{start: time.Unix(since, 0).UTC()}
				holds[since] = h
				holdOrder = append(holdOrder, since)
			}
			if e.Kind == tlHoldSet {
				h.start = e.At
			} else {
				h.end = e.At
			}
		case tlMerged:
			at := e.At
			end, merged = &at, true
		case tlClosed, tlSkipped:
			at := e.At
			end = &at
		}
	}
	lifeEnd := now
	if end != nil {
		lifeEnd = *end
		if merged {
			ti.MergedAt = end
		} else {
			ti.SkippedAt = end
		}
	}
	for _, s := range open {
		runs = append(runs, span{s, lifeEnd})
	}
	for _, since := range holdOrder {
		h := *holds[since]
		if h.end.IsZero() {
			h.end = lifeEnd
		}
		holdSpans = append(holdSpans, h)
	}
	var lifeStart time.Time
	for _, s := range slices.Concat(runs, holdSpans) {
		if lifeStart.IsZero() || s.start.Before(lifeStart) {
			lifeStart = s.start
		}
	}
	if lifeStart.IsZero() {
		return ti
	}
	ti.Segments = segments(lifeStart, lifeEnd, runs, holdSpans, from, to)
	if end == nil && !now.Before(from) && !now.After(to) && len(ti.Segments) > 0 {
		if last := &ti.Segments[len(ti.Segments)-1]; last.End != nil && last.End.Equal(now) {
			last.End = nil
		}
	}
	return ti
}

// segments cuts [start, end) into running, held and waiting parts within
// [from, to), merging neighbours of one kind.
func segments(start, end time.Time, runs, holds []span, from, to time.Time) []TimelineSegment {
	if start.Before(from) {
		start = from
	}
	if end.After(to) {
		end = to
	}
	if !start.Before(end) {
		return nil
	}
	points := []time.Time{start, end}
	for _, s := range slices.Concat(runs, holds) {
		for _, t := range []time.Time{s.start, s.end} {
			if t.After(start) && t.Before(end) {
				points = append(points, t)
			}
		}
	}
	slices.SortFunc(points, time.Time.Compare)
	points = slices.CompactFunc(points, time.Time.Equal)
	var out []TimelineSegment
	for i := 0; i+1 < len(points); i++ {
		kind := SegmentWaiting
		switch {
		case inSpans(runs, points[i]):
			kind = SegmentRunning
		case inSpans(holds, points[i]):
			kind = SegmentHeld
		}
		e := points[i+1]
		if n := len(out); n > 0 && out[n-1].Kind == kind {
			out[n-1].End = &e
			continue
		}
		out = append(out, TimelineSegment{Kind: kind, Start: points[i], End: &e})
	}
	return out
}

// holdSince is a hold event's start, unix seconds: data's since, else the
// one in its key.
func holdSince(e Event) (int64, bool) {
	if s, ok := e.Data["since"].(string); ok {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.Unix(), true
		}
	}
	if m := holdKeySince.FindStringSubmatch(e.Key); m != nil {
		n, err := strconv.ParseInt(m[1], 10, 64)
		return n, err == nil
	}
	return 0, false
}

// number is a JSON number of an event's data (float64 from the database,
// an int in tests).
func number(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	}
	return 0, false
}
