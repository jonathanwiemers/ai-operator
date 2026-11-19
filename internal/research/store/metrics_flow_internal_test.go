package store

import (
	"slices"
	"testing"
	"time"
)

// TestFlowRange: three issues merged with known run starts and calls give
// the count per bucket, the medians and the means; an issue without a
// run.started has no wall time, one without calls no agent time.
func TestFlowRange(t *testing.T) {
	day := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return day.Add(time.Duration(h) * time.Hour) }
	run := func(h int) *time.Time { r := at(h); return &r }
	issues := []flowIssue{
		{Namespace: "agents", Project: "shop", IssueIID: 1, MergedAt: at(10), FirstRun: run(8), Calls: 4, DurationMS: 600_000, ReviewCalls: 2, FixCalls: 1},
		{Namespace: "agents", Project: "shop", IssueIID: 2, MergedAt: at(12), FirstRun: run(6), Calls: 3, DurationMS: 1_200_000, ReviewCalls: 1},
		{Namespace: "agents", Project: "billing", IssueIID: 2, MergedAt: at(30), FirstRun: run(29), Calls: 6, DurationMS: 2_400_000, ReviewCalls: 3, FixCalls: 2},
	}
	r := flowRange(slices.Clone(issues), "day", nil)
	if want := []FlowBucket{{"2026-11-02", 2}, {"2026-11-03", 1}}; !slices.Equal(r.Buckets, want) {
		t.Errorf("buckets = %+v, want %+v", r.Buckets, want)
	}
	// Wall: 2 h, 6 h, 1 h; agent: 10, 20, 40 min; review 6 and fix 3 calls.
	want := FlowTotals{MergedIssues: 3, WallIssues: 3, MedianWallSec: 7200, AgentIssues: 3, MedianAgentSec: 1200,
		ReviewCallsPerIssue: 2, FixCallsPerIssue: 1}
	if r.Totals != want {
		t.Errorf("totals = %+v, want %+v", r.Totals, want)
	}

	// Without a run.started or calls: out of that median, still merged and
	// in the means. Even counts take the mean of the middle two.
	issues[2].FirstRun, issues[2].Calls, issues[2].DurationMS = nil, 0, 0
	r = flowRange(issues, "day", nil)
	want = FlowTotals{MergedIssues: 3, WallIssues: 2, MedianWallSec: 4 * 3600, AgentIssues: 2, MedianAgentSec: 900,
		ReviewCallsPerIssue: 2, FixCallsPerIssue: 1}
	if r.Totals != want {
		t.Errorf("unmeasured: totals = %+v, want %+v", r.Totals, want)
	}

	// A run start after the merge (a rerun) gives no negative wall time.
	if r := flowRange([]flowIssue{{MergedAt: at(1), FirstRun: run(2)}}, "day", nil); r.Totals.MedianWallSec != 0 || r.Totals.WallIssues != 1 {
		t.Errorf("rerun: totals = %+v", r.Totals)
	}

	// Nothing merged: empty buckets, zeros.
	if r := flowRange(nil, "week", nil); r.Buckets == nil || len(r.Buckets) != 0 || r.Totals != (FlowTotals{}) {
		t.Errorf("none = %+v", r)
	}
}

// TestFlowRangeBucketEdges: buckets are local dates of the merge in a
// non-UTC zone; weeks start on Monday, months on the first.
func TestFlowRangeBucketEdges(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-11-01 23:30 UTC is Monday 2026-11-02 00:30 in Berlin; 2026-10-31
	// 22:59 UTC is Oct 31 23:59 there.
	monday := time.Date(2026, 11, 1, 23, 30, 0, 0, time.UTC)
	saturday := time.Date(2026, 10, 31, 22, 59, 0, 0, time.UTC)
	issues := []flowIssue{{IssueIID: 1, MergedAt: saturday}, {IssueIID: 2, MergedAt: monday}}
	for _, c := range []struct {
		bucket string
		want   []FlowBucket
	}{
		{"day", []FlowBucket{{"2026-10-31", 1}, {"2026-11-02", 1}}},
		{"week", []FlowBucket{{"2026-10-26", 1}, {"2026-11-02", 1}}},
		{"month", []FlowBucket{{"2026-10-01", 1}, {"2026-11-01", 1}}},
	} {
		if r := flowRange(issues, c.bucket, berlin); !slices.Equal(r.Buckets, c.want) {
			t.Errorf("%s: buckets = %+v, want %+v", c.bucket, r.Buckets, c.want)
		}
	}
	// In UTC both are on their UTC dates.
	if r := flowRange(issues, "day", time.UTC); !slices.Equal(r.Buckets, []FlowBucket{{"2026-10-31", 1}, {"2026-11-01", 1}}) {
		t.Errorf("utc: buckets = %+v", r.Buckets)
	}
}
