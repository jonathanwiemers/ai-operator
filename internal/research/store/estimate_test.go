package store_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

func TestEstimate(t *testing.T) {
	// spent is a milestone of reqs requirements and issues issues that cost
	// usd per requirement and 1000·usd tokens per requirement.
	spent := func(reqs, issues int, usd float64) store.MilestoneSpend {
		return store.MilestoneSpend{Requirements: reqs, Issues: issues, CostUSD: store.USD(usd * float64(reqs)),
			Tokens: int64(1000 * usd * float64(reqs))}
	}
	type want struct {
		low, typical, high float64
		milestones         int
		notEnough          bool
	}
	check := func(name string, got store.EstimateResult, w want) {
		t.Helper()
		if got.Milestones != w.milestones || got.NotEnoughHistory != w.notEnough {
			t.Errorf("%s: milestones %d, not enough %v; want %d, %v", name, got.Milestones, got.NotEnoughHistory, w.milestones, w.notEnough)
		}
		if w.notEnough {
			if got.LowUSD.Known || got.TypicalUSD.Known || got.HighUSD.Known || got.LowTokens+got.TypicalTokens+got.HighTokens != 0 {
				t.Errorf("%s: not enough history but %+v", name, got)
			}
			return
		}
		usd := []float64{got.LowUSD.USD, got.TypicalUSD.USD, got.HighUSD.USD}
		known := got.LowUSD.Known && got.TypicalUSD.Known && got.HighUSD.Known
		if !known || !slices.EqualFunc(usd, []float64{w.low, w.typical, w.high}, func(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }) {
			t.Errorf("%s: USD %v (known %v), want %v %v %v", name, usd, known, w.low, w.typical, w.high)
		}
		tokens := []int64{got.LowTokens, got.TypicalTokens, got.HighTokens}
		if wt := []int64{int64(w.low * 1000), int64(w.typical * 1000), int64(w.high * 1000)}; !slices.Equal(tokens, wt) {
			t.Errorf("%s: tokens %v, want %v", name, tokens, wt)
		}
	}

	two := []store.MilestoneSpend{spent(2, 3, 1), spent(4, 1, 2)}
	check("2 milestones", store.Estimate(two, store.UnitRequirement, 5), want{milestones: 2, notEnough: true})
	check("none", store.Estimate(nil, store.UnitIssue, 5), want{notEnough: true})

	// Rates 1, 2, 3 per requirement: Q1 1.5, median 2, Q3 2.5.
	three := []store.MilestoneSpend{spent(2, 1, 3), spent(1, 1, 1), spent(5, 1, 2)}
	check("3 milestones", store.Estimate(three, store.UnitRequirement, 4), want{6, 8, 10, 3, false})
	// n=0 costs nothing, with enough history.
	check("n=0", store.Estimate(three, store.UnitRequirement, 0), want{0, 0, 0, 3, false})

	// Rates 1…10 per issue, shuffled: Q1 at position 2.25 is 3.25, the
	// median at 4.5 is 5.5, Q3 at 6.75 is 7.75.
	var ten []store.MilestoneSpend
	for _, rate := range []float64{7, 3, 10, 1, 5, 9, 2, 8, 4, 6} {
		m := spent(2, 2, rate)
		m.Requirements = 0
		ten = append(ten, m)
	}
	check("10 milestones", store.Estimate(ten, store.UnitIssue, 2), want{6.5, 11, 15.5, 10, false})
	// The same milestones have no known requirements.
	check("unknown requirements", store.Estimate(ten, store.UnitRequirement, 2), want{notEnough: true})

	// Unknown costs and milestones without the unit are left out.
	mixed := slices.Clone(three)
	mixed = append(mixed, store.MilestoneSpend{Requirements: 3, Issues: 3, Tokens: 99999}, spent(0, 4, 50))
	check("unknown costs skipped", store.Estimate(mixed, store.UnitRequirement, 4), want{6, 8, 10, 3, false})
	mixed[0].CostUSD = store.Money{}
	check("too few known costs", store.Estimate(mixed, store.UnitRequirement, 4), want{milestones: 2, notEnough: true})

	b, err := json.Marshal(store.Estimate(three, store.UnitRequirement, 4))
	if err != nil {
		t.Fatal(err)
	}
	const wantJSON = `{"unit":"requirement","count":4,"lowUSD":"6.00","typicalUSD":"8.00","highUSD":"10.00",` +
		`"lowTokens":6000,"typicalTokens":8000,"highTokens":10000,"milestones":3,"notEnoughHistory":false}`
	if string(b) != wantJSON {
		t.Errorf("JSON\n got %s\nwant %s", b, wantJSON)
	}
}

func TestMilestoneSpendHistory(t *testing.T) {
	owner := storetest.New(t)
	// The UI reads it as the reader.
	s := asReader(t, owner)
	ctx := t.Context()
	exp := time.Now().Add(24 * time.Hour)
	base := time.Now().Add(-30 * 24 * time.Hour).Truncate(time.Second)
	usd := func(v float64) *float64 { return &v }
	var calls []store.Call
	call := func(p store.ProjectKey, milestone int64, cost *float64, in, out, cacheRead, cacheWrite int64, expiresAt time.Time) {
		calls = append(calls, store.Call{Key: fmt.Sprintf("c%d", len(calls)), At: base, Namespace: p.Namespace, Project: p.Name,
			MilestoneIID: milestone, Task: fmt.Sprintf("orbit-%d", milestone), Run: 1, Seq: int64(len(calls)), Kind: "implement",
			Role: "implement", Engine: "claude", Model: "claude-opus-5-5", InputTokens: in, OutputTokens: out,
			CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite, CostUSD: cost, CostSource: "reported", ExpiresAt: expiresAt})
	}
	// Milestones 1…12 finished a day apart, so 12 is the newest; milestone
	// n has n issues, requirements 2n (unknown for 5) and two calls.
	for n := int64(1); n <= 12; n++ {
		m := store.MilestoneSnapshot{Project: orbit, IID: n, Title: fmt.Sprintf("M%d", n), State: "closed",
			StartedAt: base, DoneAt: base.Add(time.Duration(n) * 24 * time.Hour)}
		for i := int64(1); i <= n; i++ {
			m.Issues = append(m.Issues, store.IssueSnapshot{IID: 100*n + i, Title: "issue", State: "closed"})
		}
		if _, err := owner.SaveMilestone(ctx, m, exp); err != nil {
			t.Fatal(err)
		}
		if n != 5 {
			if _, err := owner.SetMilestoneRequirements(ctx, orbit, n, int(2*n)); err != nil {
				t.Fatal(err)
			}
		}
		call(orbit, n, usd(float64(n)), n, 1, 10, 100, exp)
		call(orbit, n, usd(0.5), 1, 1, 1, 1, exp)
	}
	// Milestone 9's costs are unknown; it still has tokens.
	for i := range calls {
		if calls[i].MilestoneIID == 9 {
			calls[i].CostUSD, calls[i].CostSource = nil, "unknown"
		}
	}
	// Not counted: an expired call, another project's call on the same IID.
	call(orbit, 12, usd(1000), 1000, 0, 0, 0, time.Now().Add(-time.Hour))
	other := store.ProjectKey{Namespace: "agents", Name: "other"}
	call(other, 12, usd(1000), 1000, 0, 0, 0, exp)
	// Not finished: milestone 13 is open, without a done time.
	if _, err := owner.SaveMilestone(ctx, store.MilestoneSnapshot{Project: orbit, IID: 13, State: "open", StartedAt: base,
		Issues: []store.IssueSnapshot{{IID: 1301}}}, exp); err != nil {
		t.Fatal(err)
	}
	call(orbit, 13, usd(7), 1, 1, 1, 1, exp)
	// Another project's finished milestone.
	if _, err := owner.SaveMilestone(ctx, store.MilestoneSnapshot{Project: other, IID: 12, State: "closed", StartedAt: base,
		DoneAt: base.Add(400 * time.Hour)}, exp); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.InsertCalls(ctx, calls); err != nil {
		t.Fatal(err)
	}

	got, err := s.MilestoneSpendHistory(ctx, orbit, 10)
	if err != nil {
		t.Fatal(err)
	}
	var want []store.MilestoneSpend
	for n := int64(12); n >= 3; n-- {
		m := store.MilestoneSpend{IID: n, Requirements: int(2 * n), Issues: int(n), CostUSD: store.USD(float64(n) + 0.5),
			Tokens: n + 1 + 10 + 100 + 4}
		if n == 5 {
			m.Requirements = 0
		}
		if n == 9 {
			m.CostUSD = store.Money{}
		}
		want = append(want, m)
	}
	if !slices.Equal(got, want) {
		t.Errorf("history:\n got %+v\nwant %+v", got, want)
	}

	if got, err := s.MilestoneSpendHistory(ctx, store.ProjectKey{Namespace: "agents", Name: "none"}, 10); err != nil || len(got) != 0 {
		t.Errorf("project without milestones: %+v, %v", got, err)
	}

	// The reader sees the requirements; unknown and missing are 0.
	for iid, want := range map[int64]int{4: 8, 5: 0, 99: 0} {
		if n, err := s.MilestoneRequirements(ctx, orbit, iid); err != nil || n != want {
			t.Errorf("requirements of %d: %d, %v; want %d", iid, n, err, want)
		}
	}
}
