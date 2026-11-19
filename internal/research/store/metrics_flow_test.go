package store_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

// TestFlowInRange: three issues merged in the range, with run starts and
// calls also before it; an issue merged twice counts once, at its latest
// merge in the range; issues merged outside the range, expired events,
// other projects and research calls stay out.
func TestFlowInRange(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	exp, gone := now.Add(24*time.Hour), now.Add(-time.Minute)
	from, to := now.Add(-7*24*time.Hour), now.Add(time.Hour)
	ev := func(key, project string, issue int64, kind string, at, expires time.Time) store.Event {
		return store.Event{Key: key, At: at, Namespace: "agents", Project: project, MilestoneIID: 4, IssueIID: issue,
			Task: fmt.Sprintf("%s-%d", project, issue), Kind: kind, ExpiresAt: expires}
	}
	call := func(seq int64, project string, issue int64, kind string, at time.Time, ms int64) store.Call {
		return store.Call{Key: fmt.Sprintf("%s-%d-%d", project, issue, seq), At: at, Namespace: "agents", Project: project,
			MilestoneIID: 4, IssueIID: issue, Task: fmt.Sprintf("%s-%d", project, issue), Run: 1, Seq: seq, Kind: kind, Role: kind,
			Engine: "claude", Model: "claude-opus-5-5", CostSource: "unknown", DurationMS: ms, ExpiresAt: exp}
	}
	m1, m2, m3 := now.Add(-48*time.Hour), now.Add(-24*time.Hour), now.Add(-3*time.Hour)
	if _, err := owner.InsertEvents(ctx, []store.Event{
		// Issue 1: started before the range, merged in it after 4 h.
		ev("r1", "shop", 1, "run.started", from.Add(-time.Hour), exp),
		ev("r1b", "shop", 1, "run.started", m1.Add(-time.Hour), exp),
		ev("m1", "shop", 1, "merged", from.Add(3*time.Hour), exp),
		// Issue 2: merged twice in the range, the latest counts (2 h).
		ev("r2", "shop", 2, "run.started", m2.Add(-2*time.Hour), exp),
		ev("m2a", "shop", 2, "merged", m2.Add(-time.Hour), exp),
		ev("m2b", "shop", 2, "merged", m2, exp),
		// Issue 3: no run.started, no wall time.
		ev("m3", "shop", 3, "merged", m3, exp),
		// Merged before and after the range, expired, another project's.
		ev("m4", "shop", 4, "merged", from.Add(-time.Minute), exp),
		ev("m5", "shop", 5, "merged", to, exp),
		ev("m6", "shop", 6, "merged", m1, gone),
		ev("m7", "billing", 7, "merged", m1, exp),
		// Not a merge.
		ev("x1", "shop", 8, "run.started", m1, exp),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.InsertCalls(ctx, []store.Call{
		// Issue 1: an implement call before the range counts.
		call(1, "shop", 1, "implement", from.Add(-30*time.Minute), 600_000),
		call(2, "shop", 1, "review", m1.Add(-time.Hour), 60_000),
		call(3, "shop", 1, "fix", m1.Add(-30*time.Minute), 120_000),
		call(4, "shop", 1, "review", m1.Add(-10*time.Minute), 60_000),
		// Issue 2.
		call(1, "shop", 2, "implement", m2.Add(-90*time.Minute), 1_800_000),
		call(2, "shop", 2, "review", m2.Add(-80*time.Minute), 60_000),
		// Research never counts.
		call(3, "shop", 2, "research", m2.Add(-70*time.Minute), 9_000_000),
		// Issue 4 merged before the range: its calls do not count.
		call(1, "shop", 4, "fix", m1, 5_000_000),
	}); err != nil {
		t.Fatal(err)
	}
	q := store.CostQuery{Projects: []store.ProjectKey{{Namespace: "agents", Name: "shop"}}, From: from, To: to, Loc: time.UTC,
		Kind: "review", Engine: "cursor"}
	r, err := s.FlowInRange(ctx, q, "day", now)
	if err != nil {
		t.Fatal(err)
	}
	// Wall: issue 1 from its first run (before the range), 4 h + the merge
	// lag; issue 2 2 h. Agent: 14 and 31 min. Review 3, fix 1 over 3 issues.
	wall1 := int64(from.Add(3 * time.Hour).Sub(from.Add(-time.Hour)).Seconds())
	want := store.FlowTotals{MergedIssues: 3, WallIssues: 2, MedianWallSec: (wall1 + 7200) / 2, AgentIssues: 2,
		MedianAgentSec: (840 + 1860) / 2, ReviewCallsPerIssue: 1, FixCallsPerIssue: 1.0 / 3}
	if r.Totals != want {
		t.Errorf("totals = %+v, want %+v", r.Totals, want)
	}
	n := 0
	for _, b := range r.Buckets {
		n += b.Merged
	}
	if n != 3 {
		t.Errorf("buckets = %+v", r.Buckets)
	}

	// No projects: nothing, not nil; an unknown bucket is an error.
	if r, err := s.FlowInRange(ctx, store.CostQuery{From: from, To: to}, "day", now); err != nil || r.Totals.MergedIssues != 0 || r.Buckets == nil {
		t.Errorf("no projects = %+v, %v", r, err)
	}
	if _, err := s.FlowInRange(ctx, q, "year", now); err == nil {
		t.Error("unknown bucket: no error")
	}
}

// TestFlowInRangeUsesTimeIndex: the merged events are read through
// history_events_at_idx, each issue's first run through
// history_events_issue_idx, and its calls through an index of agent_calls
// by project (on empty tables the planner picks agent_calls_issue_idx or
// agent_calls_project_at_idx at the same cost), not a scan of the table.
func TestFlowInRangeUsesTimeIndex(t *testing.T) {
	owner := storetest.New(t)
	q := store.CostQuery{Projects: []store.ProjectKey{{Namespace: "agents", Name: "shop"}, {Namespace: "agents", Name: "billing"}},
		From: time.Now().Add(-30 * 24 * time.Hour), To: time.Now()}
	sql, args := store.FlowQuery(q, time.Now())
	assertUsesIndex(t, owner.Pool(), sql, args, "history_events_at_idx")
	assertUsesIndex(t, owner.Pool(), sql, args, "history_events_issue_idx")
	assertUsesIndex(t, owner.Pool(), sql, args, "agent_calls_")
}
