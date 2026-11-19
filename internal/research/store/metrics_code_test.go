package store_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

// TestCodeInRange: two projects over three Berlin days, shop with a
// companion. Written lines are bucketed by call time, merged lines by their
// event's time (a companion after its home merge, one of an issue merged
// before the range), the cost per 100 production lines per issue merged in
// the range, whole: its calls before the range and its companions count,
// unmeasured issues and research calls do not. Companion lines count under
// the home project.
func TestCodeInRange(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	exp, gone := time.Now().Add(24*time.Hour), time.Now().Add(-time.Minute)
	// 2026-11-02 to 2026-11-04 in Berlin (UTC+1): [11-01 23:00, 11-04 23:00) UTC.
	from, to := time.Date(2026, 11, 2, 0, 0, 0, 0, berlin), time.Date(2026, 11, 5, 0, 0, 0, 0, berlin)
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 11, day, hour, minute, 0, 0, berlin) }
	ev := func(key, project string, issue, mr int64, kind string, when time.Time, lines *store.MergedLines, expires time.Time) store.Event {
		data := map[string]any{}
		if kind == "companion.merged" {
			data["companion"] = "group/infra"
		}
		if lines != nil {
			data["lines"] = lines
		}
		return store.Event{Key: key, At: when, Namespace: "agents", Project: project, MilestoneIID: 4, IssueIID: issue,
			MergeRequestIID: mr, Task: fmt.Sprintf("%s-%d", project, issue), Kind: kind, Data: data, ExpiresAt: expires}
	}
	var seq int64
	call := func(project string, issue int64, kind string, when time.Time, added, deleted int64, cost *float64, expires time.Time) store.Call {
		seq++
		source := "reported"
		if cost == nil {
			source = "unknown"
		}
		return store.Call{Key: fmt.Sprintf("c%d", seq), At: when, Namespace: "agents", Project: project, MilestoneIID: 4,
			IssueIID: issue, Task: fmt.Sprintf("%s-%d", project, issue), Run: 1, Seq: seq, Kind: kind, Role: kind,
			Engine: "claude", Model: "claude-opus-5-5", CostUSD: cost, CostSource: source, InputTokens: 100,
			LinesAdded: added, LinesDeleted: deleted, ExpiresAt: expires}
	}
	usd := func(v float64) *float64 { return &v }
	if _, err := owner.InsertEvents(ctx, []store.Event{
		// shop 1: merged on the 3rd, its companion on the 4th.
		ev("m1", "shop", 1, 101, "merged", at(3, 12, 0), &store.MergedLines{Production: lc(40, 5), Tests: lc(60, 0)}, exp),
		ev("c1", "shop", 1, 7, "companion.merged", at(4, 9, 0), &store.MergedLines{Production: lc(12, 3)}, exp),
		// shop 2: merged without a count, at 23:30 on the 3rd in Berlin
		// (22:30 UTC): unmeasured.
		ev("m2", "shop", 2, 102, "merged", time.Date(2026, 11, 3, 22, 30, 0, 0, time.UTC), nil, exp),
		// shop 3: merged before the range, its partial companion in it.
		ev("m3", "shop", 3, 103, "merged", at(1, 12, 0), &store.MergedLines{Production: lc(500, 0)}, exp),
		ev("c3", "shop", 3, 8, "companion.merged", at(2, 15, 0), &store.MergedLines{Production: lc(4, 1), Partial: true}, exp),
		// billing 5: docs only; billing 6 at 23:59 on the 4th in Berlin, without calls.
		ev("m5", "billing", 5, 105, "merged", at(4, 11, 0), &store.MergedLines{Docs: lc(10, 0)}, exp),
		ev("m6", "billing", 6, 106, "merged", time.Date(2026, 11, 4, 22, 59, 0, 0, time.UTC),
			&store.MergedLines{Production: lc(8, 2), Generated: lc(100, 100)}, exp),
		// Out: at the range's end (00:00 on the 5th in Berlin), expired, another project's.
		ev("m7", "billing", 7, 107, "merged", to, &store.MergedLines{Production: lc(900, 0)}, exp),
		ev("m8", "shop", 8, 108, "merged", at(3, 12, 0), &store.MergedLines{Production: lc(900, 0)}, gone),
		ev("m9", "other", 9, 109, "merged", at(3, 12, 0), &store.MergedLines{Production: lc(900, 0)}, exp),
		// Not a merge.
		ev("r1", "shop", 1, 0, "run.started", at(2, 9, 0), nil, exp),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.InsertCalls(ctx, []store.Call{
		// shop 1: before the range (cost counts, lines do not), in it.
		call("shop", 1, "implement", at(1, 12, 0), 50, 0, usd(1.5), exp),
		call("shop", 1, "implement", at(2, 10, 0), 100, 10, usd(2), exp),
		call("shop", 1, "fix", at(3, 10, 0), 20, 5, usd(1), exp),
		call("shop", 1, "review", at(3, 11, 0), 0, 0, usd(0.5), exp),
		// Research never counts.
		call("shop", 1, "research", at(3, 11, 0), 999, 999, usd(9), exp),
		// shop 2 at 23:30 UTC on the 2nd, 00:30 on the 3rd in Berlin, cost unknown.
		call("shop", 2, "implement", time.Date(2026, 11, 2, 23, 30, 0, 0, time.UTC), 7, 3, nil, exp),
		// Expired, after the range, another project's.
		call("shop", 2, "implement", at(3, 9, 0), 300, 300, usd(1), gone),
		call("shop", 2, "implement", to, 300, 300, usd(1), exp),
		call("other", 9, "implement", at(3, 9, 0), 300, 300, usd(1), exp),
		call("billing", 5, "implement", at(4, 10, 0), 30, 30, usd(3), exp),
	}); err != nil {
		t.Fatal(err)
	}
	q := store.CostQuery{Projects: []store.ProjectKey{{Namespace: "agents", Name: "billing"}, {Namespace: "agents", Name: "shop"}},
		From: from, To: to, Loc: berlin, Kind: "review", Engine: "cursor"}
	r, err := s.CodeInRange(ctx, q, "day")
	if err != nil {
		t.Fatal(err)
	}
	wantBuckets := []store.CodeBucket{
		{Bucket: "2026-11-02", Written: lc(100, 10), Merged: store.MergedLines{Production: lc(4, 1), Partial: true}},
		{Bucket: "2026-11-03", Written: lc(27, 8), Merged: store.MergedLines{Production: lc(40, 5), Tests: lc(60, 0)}},
		{Bucket: "2026-11-04", Written: lc(30, 30),
			Merged: store.MergedLines{Production: lc(20, 5), Docs: lc(10, 0), Generated: lc(100, 100)}},
	}
	if !reflect.DeepEqual(r.Buckets, wantBuckets) {
		t.Errorf("buckets = %+v\nwant %+v", r.Buckets, wantBuckets)
	}
	// Merged in the range: shop 1 and 2, billing 5 and 6; shop 2 unmeasured.
	// shop 1 is +52/−8 production with its companion and cost $5 with its
	// call before the range; billing $3 over 10 production lines.
	tot := r.Totals
	if tot.Written != lc(157, 48) ||
		!reflect.DeepEqual(tot.Merged, store.MergedLines{Production: lc(64, 11), Tests: lc(60, 0), Docs: lc(10, 0),
			Generated: lc(100, 100), Partial: true}) {
		t.Errorf("totals written %+v, merged %+v", tot.Written, tot.Merged)
	}
	wantIssues := store.MergedLineTotals{Merged: 4, Measured: 3, CostPer100USD: store.USD(8.0 * 100 / 70),
		Lines: store.MergedLines{Production: lc(60, 10), Tests: lc(60, 0), Docs: lc(10, 0), Generated: lc(100, 100)}}
	if !reflect.DeepEqual(tot.Issues, wantIssues) {
		t.Errorf("issues = %+v\nwant %+v", tot.Issues, wantIssues)
	}
	if tot.Cost.CostUSD != store.USD(8) || tot.Cost.Calls != 5 || tot.Cost.UnknownCalls != 0 {
		t.Errorf("cost = %+v", tot.Cost)
	}
	if len(r.ByProject) != 2 {
		t.Fatalf("by project = %+v", r.ByProject)
	}
	billing, shop := r.ByProject[0], r.ByProject[1]
	if billing.Project != "billing" || billing.Written != lc(30, 30) || billing.Merged.Production != lc(8, 2) ||
		billing.Issues.Merged != 2 || billing.Issues.Measured != 2 || billing.Issues.CostPer100USD != store.USD(30) {
		t.Errorf("billing = %+v", billing)
	}
	if shop.Project != "shop" || shop.Written != lc(127, 18) || shop.Merged.Production != lc(56, 9) || !shop.Merged.Partial ||
		shop.Issues.Merged != 2 || shop.Issues.Measured != 1 || shop.Issues.Lines.Production != lc(52, 8) ||
		shop.Issues.Lines.Partial || shop.Issues.CostPer100USD != store.USD(5.0*100/60) {
		t.Errorf("shop = %+v", shop)
	}

	// Billing 5 alone has no production lines: the cost per 100 is unknown.
	one := store.CostQuery{Projects: q.Projects[:1], From: at(4, 0, 0), To: at(4, 12, 0), Loc: berlin}
	if r, err := s.CodeInRange(ctx, one, "day"); err != nil || r.Totals.Issues.Measured != 1 || r.Totals.Issues.CostPer100USD.Known {
		t.Errorf("no production lines = %+v, %v", r.Totals, err)
	}
	// By week and month in Berlin all three days are in one bucket.
	for bucket, want := range map[string]string{"week": "2026-11-02", "month": "2026-11-01"} {
		if r, err := s.CodeInRange(ctx, q, bucket); err != nil || len(r.Buckets) != 1 || r.Buckets[0].Bucket != want ||
			r.Buckets[0].Written != lc(157, 48) {
			t.Errorf("%s: buckets = %+v, %v", bucket, r.Buckets, err)
		}
	}

	// No projects: zeros, not nil; an unknown bucket is an error.
	if r, err := s.CodeInRange(ctx, store.CostQuery{From: from, To: to}, "day"); err != nil || r.Buckets == nil || r.ByProject == nil ||
		r.Totals.Issues.Merged != 0 {
		t.Errorf("no projects = %+v, %v", r, err)
	}
	if _, err := s.CodeInRange(ctx, q, "year"); err == nil {
		t.Error("unknown bucket: no error")
	}
}

// TestCodeInRangeUsesTimeIndex: the written lines are read through
// agent_calls_project_at_idx, the merged events and the issues merged in
// the range through history_events_at_idx, not a scan of the tables.
func TestCodeInRangeUsesTimeIndex(t *testing.T) {
	owner := storetest.New(t)
	q := store.CostQuery{Projects: []store.ProjectKey{{Namespace: "agents", Name: "shop"}, {Namespace: "agents", Name: "billing"}},
		From: time.Now().Add(-30 * 24 * time.Hour), To: time.Now()}
	sqls, args := store.CodeQueries(q, "day")
	if len(sqls) != 3 {
		t.Fatalf("%d statements", len(sqls))
	}
	assertUsesIndex(t, owner.Pool(), sqls[0], args[0], "agent_calls_project_at_idx")
	assertUsesIndex(t, owner.Pool(), sqls[1], args[1], "history_events_at_idx")
	assertUsesIndex(t, owner.Pool(), sqls[2], args[2], "history_events_at_idx")
	assertUsesIndex(t, owner.Pool(), sqls[2], args[2], "history_events_issue_idx")
}
