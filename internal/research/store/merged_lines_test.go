package store_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

// lc is a LineCount.
func lc(added, deleted int64) store.LineCount { return store.LineCount{Added: added, Deleted: deleted} }

func TestMergedLines(t *testing.T) {
	owner := storetest.New(t)
	s := asReader(t, owner)
	ctx := t.Context()
	base := time.Now().Add(-10 * time.Hour).Truncate(time.Second)
	now := time.Now()
	exp := now.Add(24 * time.Hour)

	snap := func(iid int64, issues ...store.IssueSnapshot) store.MilestoneSnapshot {
		return store.MilestoneSnapshot{Project: orbit, IID: iid, Title: "M", State: "open", StartedAt: base, Issues: issues}
	}
	for _, m := range []store.MilestoneSnapshot{
		snap(40, store.IssueSnapshot{IID: 401, State: "merged"}, store.IssueSnapshot{IID: 402, State: "merged"},
			store.IssueSnapshot{IID: 403, State: "merged"}, store.IssueSnapshot{IID: 404, State: "active"}),
		snap(41, store.IssueSnapshot{IID: 410, State: "merged"}, store.IssueSnapshot{IID: 411, State: "merged"}),
		snap(42, store.IssueSnapshot{IID: 420, State: "merged"}),
		// Nothing merged: no merged lines at all.
		snap(43, store.IssueSnapshot{IID: 430, State: "active"}),
	} {
		if _, err := owner.SaveMilestone(ctx, m, exp); err != nil {
			t.Fatal(err)
		}
	}

	home := func(key string, milestone, issue int64, lines *store.MergedLines, expires time.Time) store.Event {
		data := map[string]any{"sha": "abc"}
		if lines != nil {
			data["lines"] = lines
		}
		return store.Event{Key: key, At: base.Add(time.Hour), Namespace: orbit.Namespace, Project: orbit.Name,
			MilestoneIID: milestone, IssueIID: issue, MergeRequestIID: issue + 1000, Task: "orbit-auto", Kind: "merged",
			Data: data, ExpiresAt: expires}
	}
	companion := func(key string, milestone, issue, mr int64, lines *store.MergedLines) store.Event {
		data := map[string]any{"companion": "group/infra"}
		if lines != nil {
			data["lines"] = lines
		}
		return store.Event{Key: key, At: base.Add(2 * time.Hour), Namespace: orbit.Namespace, Project: orbit.Name,
			MilestoneIID: milestone, IssueIID: issue, MergeRequestIID: mr, Kind: "companion.merged", Data: data, ExpiresAt: exp}
	}
	infra401 := store.MergedLines{Production: lc(12, 3)}
	infra411 := store.MergedLines{Production: lc(1, 1), Partial: true}
	infra404 := store.MergedLines{Production: lc(9, 9)}
	if _, err := owner.InsertEvents(ctx, []store.Event{
		// 401: home +40/−5 production, +60 tests, +10 docs, and a companion.
		home("m401", 40, 401, &store.MergedLines{Production: lc(40, 5), Tests: lc(60, 0), Docs: lc(10, 0)}, exp),
		companion("c401", 40, 401, 7, &infra401),
		// 402: home only.
		home("m402", 40, 402, &store.MergedLines{Production: lc(8, 2), Generated: lc(100, 100)}, exp),
		// 403: merged without lines (the count failed): unmeasured. Its
		// expired merged event with lines does not count.
		home("m403", 40, 403, nil, exp),
		home("m403old", 40, 403, &store.MergedLines{Production: lc(500, 0)}, now.Add(-time.Minute)),
		// 404: only its companion merged; the issue is not merged.
		companion("c404", 40, 404, 8, &infra404),
		// 410: a companion merged without a count: measured, but partial.
		home("m410", 41, 410, &store.MergedLines{Production: lc(5, 0)}, exp),
		companion("c410", 41, 410, 9, nil),
		// 411: a partial companion.
		home("m411", 41, 411, &store.MergedLines{Production: lc(10, 5)}, exp),
		companion("c411", 41, 411, 10, &infra411),
		// 420: docs only, no production lines.
		home("m420", 42, 420, &store.MergedLines{Docs: lc(3, 0)}, exp),
	}); err != nil {
		t.Fatal(err)
	}

	usd := func(v float64) *float64 { return &v }
	call := func(key string, milestone, issue int64, cost *float64) store.Call {
		source := "reported"
		if cost == nil {
			source = "unknown"
		}
		return store.Call{Key: key, At: base.Add(time.Hour), Namespace: orbit.Namespace, Project: orbit.Name,
			MilestoneIID: milestone, IssueIID: issue, Task: "t-" + key, Run: 1, Seq: 1, Kind: "implement", Role: "implement",
			Engine: "claude", Model: "claude-opus-5-5", InputTokens: 10, OutputTokens: 10, CostUSD: cost, CostSource: source,
			ExpiresAt: exp}
	}
	if _, err := owner.InsertCalls(ctx, []store.Call{
		call("c401a", 40, 401, usd(1.5)), call("c401b", 40, 401, usd(0.5)),
		call("c402", 40, 402, usd(3)),
		// Unmeasured issues' calls add no cost per 100 lines.
		call("c403", 40, 403, usd(100)), call("c404", 40, 404, usd(7)),
		// Milestone 41's calls have no cost.
		call("c410", 41, 410, nil), call("c411", 41, 411, nil),
		call("c420", 42, 420, usd(1)),
	}); err != nil {
		t.Fatal(err)
	}

	t.Run("issues", func(t *testing.T) {
		d, err := s.Milestone(ctx, orbit, 40, now)
		if err != nil {
			t.Fatal(err)
		}
		byIID := map[int64]store.IssueFigures{}
		for _, f := range d.Issues {
			byIID[f.IID] = f
		}
		want401 := &store.MergedLines{Production: lc(52, 8), Tests: lc(60, 0), Docs: lc(10, 0)}
		if got := byIID[401]; !reflect.DeepEqual(got.MergedLines, want401) ||
			!reflect.DeepEqual(got.Companions, []store.CompanionLines{{Project: "group/infra", MRIID: 7, Lines: &infra401}}) {
			t.Errorf("401: lines %+v, companions %+v", got.MergedLines, got.Companions)
		}
		want402 := &store.MergedLines{Production: lc(8, 2), Generated: lc(100, 100)}
		if got := byIID[402]; !reflect.DeepEqual(got.MergedLines, want402) || got.Companions != nil {
			t.Errorf("402: lines %+v, companions %+v", got.MergedLines, got.Companions)
		}
		// Unmeasured: no lines, not 0 lines.
		if got := byIID[403]; got.MergedLines != nil || got.Companions != nil {
			t.Errorf("403: lines %+v, companions %+v, want none", got.MergedLines, got.Companions)
		}
		if got := byIID[404]; got.MergedLines != nil ||
			!reflect.DeepEqual(got.Companions, []store.CompanionLines{{Project: "group/infra", MRIID: 8, Lines: &infra404}}) {
			t.Errorf("404: lines %+v, companions %+v", got.MergedLines, got.Companions)
		}

		f, err := s.IssueFigures(ctx, orbit, 41, 410, now)
		if err != nil {
			t.Fatal(err)
		}
		if want := (&store.MergedLines{Production: lc(5, 0), Partial: true}); !reflect.DeepEqual(f.MergedLines, want) ||
			!reflect.DeepEqual(f.Companions, []store.CompanionLines{{Project: "group/infra", MRIID: 9}}) {
			t.Errorf("410: lines %+v, companions %+v", f.MergedLines, f.Companions)
		}
		f, err = s.IssueFigures(ctx, orbit, 41, 411, now)
		if err != nil {
			t.Fatal(err)
		}
		if want := (&store.MergedLines{Production: lc(11, 6), Partial: true}); !reflect.DeepEqual(f.MergedLines, want) {
			t.Errorf("411: lines %+v, want %+v", f.MergedLines, want)
		}
	})

	want40 := store.MergedLineTotals{Merged: 3, Measured: 2,
		Lines: store.MergedLines{Production: lc(60, 10), Tests: lc(60, 0), Docs: lc(10, 0), Generated: lc(100, 100)}}
	want41 := store.MergedLineTotals{Merged: 2, Measured: 2, Lines: store.MergedLines{Production: lc(16, 6), Partial: true}}
	want42 := store.MergedLineTotals{Merged: 1, Measured: 1, Lines: store.MergedLines{Docs: lc(3, 0)}}

	t.Run("milestone rows", func(t *testing.T) {
		rows, err := s.Milestones(ctx, store.MilestoneQuery{Projects: []store.ProjectKey{orbit}})
		if err != nil {
			t.Fatal(err)
		}
		got := map[int64]*store.MergedLineTotals{}
		for _, r := range rows {
			got[r.IID] = r.MergedLines
		}
		want := map[int64]*store.MergedLineTotals{40: &want40, 41: &want41, 42: &want42, 43: nil}
		if !reflect.DeepEqual(got, want) {
			for iid := range want {
				t.Errorf("milestone %d: got %+v, want %+v", iid, got[iid], want[iid])
			}
		}
		d, err := s.Milestone(ctx, orbit, 40, now)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(d.MergedLines, &want40) {
			t.Errorf("detail: %+v, want %+v", d.MergedLines, want40)
		}
	})

	t.Run("spend", func(t *testing.T) {
		withCost := func(m store.MergedLineTotals, usd float64) *store.MergedLineTotals {
			m.CostPer100USD = store.USD(usd)
			return &m
		}
		for _, tc := range []struct {
			name string
			sc   store.SpendScope
			want *store.MergedLineTotals
		}{
			// (1.5 + 0.5 + 3) × 100 ÷ 70: 403's and 404's calls do not count.
			{"milestone", store.SpendScope{MilestoneIID: 40}, withCost(want40, 5*100/70.0)},
			{"issue", store.SpendScope{MilestoneIID: 40, IssueIID: 401}, withCost(store.MergedLineTotals{Merged: 1, Measured: 1,
				Lines: store.MergedLines{Production: lc(52, 8), Tests: lc(60, 0), Docs: lc(10, 0)}}, 2*100/60.0)},
			{"unmeasured issue", store.SpendScope{MilestoneIID: 40, IssueIID: 403}, &store.MergedLineTotals{Merged: 1}},
			{"not merged", store.SpendScope{MilestoneIID: 40, IssueIID: 404}, nil},
			// The cost is unknown.
			{"partial, unknown cost", store.SpendScope{MilestoneIID: 41}, &want41},
			// No production lines.
			{"no production lines", store.SpendScope{MilestoneIID: 42}, &want42},
			{"project", store.SpendScope{}, withCost(store.MergedLineTotals{Merged: 6, Measured: 5,
				Lines: store.MergedLines{Production: lc(76, 16), Tests: lc(60, 0), Docs: lc(13, 0), Generated: lc(100, 100),
					Partial: true}}, 6*100/92.0)},
			{"task", store.SpendScope{MilestoneIID: 40, IssueIID: 401, Task: "t-c401a"}, nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tc.sc.Namespace, tc.sc.Project = orbit.Namespace, orbit.Name
				sp, err := s.Spend(ctx, tc.sc)
				if err != nil {
					t.Fatal(err)
				}
				got := sp.MergedLines
				if got != nil && tc.want != nil && got.CostPer100USD.String() == tc.want.CostPer100USD.String() {
					got.CostPer100USD = tc.want.CostPer100USD
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Errorf("got %+v, want %+v", got, tc.want)
				}
			})
		}
	})
}
