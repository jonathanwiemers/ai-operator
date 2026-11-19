package store

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// Flow over a range: the issues of many projects merged in the range, for
// the Desk's Metrics page. An issue counts once, at its latest merged event
// in the range, and is measured whole: its wall time runs from its first
// run.started (as issueFigures computes it), its agent time and review and
// fix calls are of all its calls, also those before the range.

// FlowBucket is the issues merged in one bucket: Bucket is its local start
// date, YYYY-MM-DD.
type FlowBucket struct {
	Bucket string
	Merged int
}

// FlowTotals is the figures of the issues merged in a range. The medians
// are of the issues they can be measured on: WallIssues have a run.started,
// AgentIssues at least one call; both are 0 without such issues. The means
// are per merged issue.
type FlowTotals struct {
	MergedIssues        int
	WallIssues          int
	MedianWallSec       int64
	AgentIssues         int
	MedianAgentSec      int64
	ReviewCallsPerIssue float64
	FixCallsPerIssue    float64
}

// FlowRange is the flow of a range: Buckets holds the buckets with merged
// issues, by start.
type FlowRange struct {
	Buckets []FlowBucket
	Totals  FlowTotals
}

// flowIssue is an issue merged in the range: its latest merge there, its
// first run start (nil without one) and its calls.
type flowIssue struct {
	Namespace, Project string
	IssueIID           int64
	MergedAt           time.Time
	FirstRun           *time.Time
	Calls, DurationMS  int64
	ReviewCalls        int64
	FixCalls           int64
}

// flowSQL is FlowInRange's query and its arguments: the merged events of
// q's projects in [q.From, q.To) on history_events_at_idx (the project
// filter is no condition history_events_project_idx could serve, as in
// pausesInRangeSQL), the latest per issue; per issue its first run.started
// (history_events_issue_idx) and its calls (agent_calls_issue_idx), at any
// time, research never.
func flowSQL(q CostQuery, now time.Time) (string, []any) {
	refs := make([]string, 0, len(q.Projects))
	for _, p := range q.Projects {
		refs = append(refs, p.Namespace+"/"+p.Name)
	}
	return `WITH merged AS (
			SELECT DISTINCT ON (namespace, project, issue_iid) namespace, project, issue_iid, at
			FROM history_events
			WHERE at >= $1 AND at < $2 AND kind = 'merged' AND issue_iid > 0
				AND namespace || '/' || project = ANY($3::text[]) AND expires_at + interval '0 seconds' > $4
			ORDER BY namespace, project, issue_iid, at DESC, id DESC
		)
		SELECT m.namespace, m.project, m.issue_iid, m.at AS merged_at,
			(SELECT min(r.at) FROM history_events r WHERE r.namespace = m.namespace AND r.project = m.project
				AND r.issue_iid = m.issue_iid AND r.kind = 'run.started' AND r.expires_at > $4) AS first_run,
			c.calls, c.duration_ms, c.review_calls, c.fix_calls
		FROM merged m CROSS JOIN LATERAL (
			SELECT count(*) AS calls, coalesce(sum(a.duration_ms), 0)::bigint AS duration_ms,
				count(*) FILTER (WHERE a.kind = 'review') AS review_calls,
				count(*) FILTER (WHERE a.kind = 'fix') AS fix_calls
			FROM agent_calls a
			WHERE a.namespace = m.namespace AND a.project = m.project AND a.issue_iid = m.issue_iid
				AND a.expires_at > $4 AND a.kind <> 'research'
		) c
		ORDER BY m.at, m.namespace, m.project, m.issue_iid`,
		[]any{q.From, q.To, refs, now}
}

// FlowInRange returns the flow of the issues of q's projects merged in
// [q.From, q.To): the merged issues per bucket (day, week or month of the
// merge, in q.Loc, as CostSeries buckets) and the totals. q's kinds and
// engine do not apply.
func (s *Store) FlowInRange(ctx context.Context, q CostQuery, bucket string, now time.Time) (FlowRange, error) {
	if !costBuckets[bucket] {
		return FlowRange{}, fmt.Errorf("flow: unknown bucket %q", bucket)
	}
	if len(q.Projects) == 0 {
		return flowRange(nil, bucket, q.Loc), nil
	}
	sql, args := flowSQL(q, now)
	rows, _ := s.pool.Query(ctx, sql, args...)
	issues, err := pgx.CollectRows(rows, pgx.RowToStructByName[flowIssue])
	if err != nil {
		return FlowRange{}, err
	}
	return flowRange(issues, bucket, q.Loc), nil
}

// flowRange sums the merged issues per bucket of their merge in loc (nil is
// UTC) and in total.
func flowRange(issues []flowIssue, bucket string, loc *time.Location) FlowRange {
	loc = cmp.Or(loc, time.UTC)
	r := FlowRange{Buckets: []FlowBucket{}}
	buckets := map[string]int{}
	var wall, agent []int64
	var review, fix int64
	for _, is := range issues {
		key := bucketStart(is.MergedAt, bucket, loc)
		i, ok := buckets[key]
		if !ok {
			i = len(r.Buckets)
			buckets[key] = i
			r.Buckets = append(r.Buckets, FlowBucket{Bucket: key})
		}
		r.Buckets[i].Merged++
		if is.FirstRun != nil {
			wall = append(wall, max(0, int64(is.MergedAt.Sub(*is.FirstRun).Seconds())))
		}
		if is.Calls > 0 {
			agent = append(agent, is.DurationMS)
		}
		review += is.ReviewCalls
		fix += is.FixCalls
	}
	slices.SortFunc(r.Buckets, func(a, b FlowBucket) int { return cmp.Compare(a.Bucket, b.Bucket) })
	t := &r.Totals
	t.MergedIssues, t.WallIssues, t.AgentIssues = len(issues), len(wall), len(agent)
	t.MedianWallSec = int64(math.Round(median(wall)))
	t.MedianAgentSec = int64(math.Round(median(agent) / 1000))
	if n := float64(len(issues)); n > 0 {
		t.ReviewCallsPerIssue, t.FixCallsPerIssue = float64(review)/n, float64(fix)/n
	}
	return r
}

// median is the median of vs as percentile_cont(0.5) computes it: the mean
// of the two middle values of an even count; 0 for none. It sorts vs.
func median(vs []int64) float64 {
	if len(vs) == 0 {
		return 0
	}
	slices.Sort(vs)
	m := len(vs) / 2
	if len(vs)%2 == 1 {
		return float64(vs[m])
	}
	return (float64(vs[m-1]) + float64(vs[m])) / 2
}
