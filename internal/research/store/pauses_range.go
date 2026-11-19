package store

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// Pauses over a range: the milestone pauses of many projects, for the
// Desk's Metrics page. A pause counts in the range, and in the bucket, where
// it started; its seconds are its full length, an open one's up to now.

// maxRangePauseEvents bounds what PausesInRange reads, the newest first. A
// var, so tests can lower it.
var maxRangePauseEvents = 20000

// longestPauses is how many pauses PauseRange.Longest lists.
const longestPauses = 10

// RangePause is a pause of a project's milestone.
type RangePause struct {
	Namespace, Project string
	MilestoneIID       int64
	Pause
}

// PauseBucket is the pauses that started in one bucket: Bucket is its local
// start date, YYYY-MM-DD; ByKind is never nil.
type PauseBucket struct {
	Bucket    string
	Count     int
	PausedSec int64
	ByKind    map[string]PauseKindTotal
}

// PauseProject is the pauses of one project.
type PauseProject struct {
	Namespace, Project string
	Count              int
	PausedSec          int64
}

// PauseRange is the pauses that started in a range. Buckets holds the
// buckets with pauses, by start; ByProject the projects with pauses, the
// longest paused first; Longest the longest pauses, at most longestPauses.
// OpenNow counts the pauses still open. Truncated: there were more than
// maxRangePauseEvents events, only the newest were read.
type PauseRange struct {
	Buckets   []PauseBucket
	Totals    PauseTotals
	OpenNow   int
	ByProject []PauseProject
	Longest   []RangePause
	Truncated bool
}

// pausesInRangeSQL is PausesInRange's query and its arguments: the pause
// events of q's projects from q.From up to now (a pause that started in the
// range may end after it), newest first, on history_events_at_idx. Neither
// the project filter nor the expiry is one another index could serve
// (history_events_project_idx, history_events_expires_idx, which the
// planner otherwise picks and then sorts): the time index reads in the
// order of the answer and stops at the limit.
func pausesInRangeSQL(q CostQuery, now time.Time) (string, []any) {
	refs := make([]string, 0, len(q.Projects))
	for _, p := range q.Projects {
		refs = append(refs, p.Namespace+"/"+p.Name)
	}
	return `SELECT ` + eventColumns + ` FROM history_events
		WHERE at >= $1 AND kind = ANY($2::text[]) AND namespace || '/' || project = ANY($3::text[]) AND expires_at + interval '0 seconds' > $4
		ORDER BY at DESC, id DESC LIMIT $5`,
		[]any{q.From, []string{pausedKind, resumedKind}, refs, now, maxRangePauseEvents + 1}
}

// PausesInRange returns the milestone pauses of q's projects that started in
// [q.From, q.To), per bucket (day, week or month, in q.Loc, as CostSeries
// buckets), in total, per project and the longest. The events are paired
// per milestone as Pauses pairs them. q's kinds and engine do not apply.
func (s *Store) PausesInRange(ctx context.Context, q CostQuery, bucket string, now time.Time) (PauseRange, error) {
	if !costBuckets[bucket] {
		return PauseRange{}, fmt.Errorf("pauses: unknown bucket %q", bucket)
	}
	if len(q.Projects) == 0 {
		return pauseRange(nil, q, bucket, now), nil
	}
	sql, args := pausesInRangeSQL(q, now)
	rows, _ := s.pool.Query(ctx, sql, args...)
	events, err := pgx.CollectRows(rows, pgx.RowToStructByName[Event])
	if err != nil {
		return PauseRange{}, err
	}
	truncated := len(events) > maxRangePauseEvents
	if truncated {
		events = events[:maxRangePauseEvents]
	}
	r := pauseRange(events, q, bucket, now)
	r.Truncated = truncated
	return r, nil
}

// pauseRange pairs events (newest first) per milestone and sums the pauses
// that started in [q.From, q.To).
func pauseRange(events []Event, q CostQuery, bucket string, now time.Time) PauseRange {
	type milestone struct {
		ns, project string
		iid         int64
	}
	groups := map[milestone][]Event{}
	var order []milestone
	for i := len(events) - 1; i >= 0; i-- { // oldest first
		e := events[i]
		m := milestone{e.Namespace, e.Project, e.MilestoneIID}
		if _, ok := groups[m]; !ok {
			order = append(order, m)
		}
		groups[m] = append(groups[m], e)
	}
	var kept []RangePause
	for _, m := range order {
		for _, p := range pairPauses(groups[m], now).Pauses {
			if !p.Since.Before(q.From) && p.Since.Before(q.To) {
				kept = append(kept, RangePause{Namespace: m.ns, Project: m.project, MilestoneIID: m.iid, Pause: p})
			}
		}
	}
	pauses := make([]Pause, 0, len(kept))
	for _, p := range kept {
		pauses = append(pauses, p.Pause)
	}
	r := PauseRange{Buckets: []PauseBucket{}, Totals: sumPauses(pauses), ByProject: []PauseProject{}, Longest: []RangePause{}}
	loc := cmp.Or(q.Loc, time.UTC)
	buckets, projects := map[string]int{}, map[[2]string]int{}
	for _, p := range kept {
		if p.Until == nil {
			r.OpenNow++
		}
		key := bucketStart(p.Since, bucket, loc)
		i, ok := buckets[key]
		if !ok {
			i = len(r.Buckets)
			buckets[key] = i
			r.Buckets = append(r.Buckets, PauseBucket{Bucket: key, ByKind: map[string]PauseKindTotal{}})
		}
		b := &r.Buckets[i]
		b.Count++
		b.PausedSec += p.Seconds
		k := b.ByKind[p.Kind]
		k.Count++
		k.Sec += p.Seconds
		b.ByKind[p.Kind] = k
		pk := [2]string{p.Namespace, p.Project}
		j, ok := projects[pk]
		if !ok {
			j = len(r.ByProject)
			projects[pk] = j
			r.ByProject = append(r.ByProject, PauseProject{Namespace: p.Namespace, Project: p.Project})
		}
		r.ByProject[j].Count++
		r.ByProject[j].PausedSec += p.Seconds
	}
	slices.SortFunc(r.Buckets, func(a, b PauseBucket) int { return cmp.Compare(a.Bucket, b.Bucket) })
	slices.SortFunc(r.ByProject, func(a, b PauseProject) int {
		return cmp.Or(cmp.Compare(b.PausedSec, a.PausedSec), cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Project, b.Project))
	})
	// The longest first; on a tie the newer, then by milestone.
	slices.SortFunc(kept, func(a, b RangePause) int {
		return cmp.Or(cmp.Compare(b.Seconds, a.Seconds), b.Since.Compare(a.Since), cmp.Compare(a.Namespace, b.Namespace),
			cmp.Compare(a.Project, b.Project), cmp.Compare(a.MilestoneIID, b.MilestoneIID))
	})
	r.Longest = append(r.Longest, kept[:min(longestPauses, len(kept))]...)
	return r
}

// bucketStart is the local start date of the bucket of t in loc, as
// CostSeries truncates: a week starts on Monday, a month on the first.
func bucketStart(t time.Time, bucket string, loc *time.Location) string {
	y, m, d := t.In(loc).Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	switch bucket {
	case "week":
		day = day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
	case "month":
		day = time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	}
	return day.Format(time.DateOnly)
}
