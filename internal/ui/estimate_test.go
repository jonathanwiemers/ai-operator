package ui

import (
	"context"
	"math"
	"net/http"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

const shopEstimate = "/api/v1/projects/team-a/shop/estimate"

// fakeEstimates returns hist and the requirements reqs; it records the
// history's limits.
type fakeEstimates struct {
	hist   []store.MilestoneSpend
	reqs   map[int64]int
	limits []int
}

func (f *fakeEstimates) MilestoneSpendHistory(_ context.Context, p store.ProjectKey, limit int) ([]store.MilestoneSpend, error) {
	if p != shopKey[0] {
		return nil, nil
	}
	f.limits = append(f.limits, limit)
	return f.hist, nil
}

func (f *fakeEstimates) MilestoneRequirements(_ context.Context, p store.ProjectKey, iid int64) (int, error) {
	if p != shopKey[0] {
		return 0, nil
	}
	return f.reqs[iid], nil
}

// estimateEnv is planEnv for a viewer of team-a whose shop has the plan
// shop-plan-r with a draft of 3 requirements and finished milestones that
// cost $2 per requirement and per issue (0, 1, 4 and 9 tokens).
func estimateEnv(t *testing.T) (*testEnv, *fakeEstimates) {
	t.Helper()
	env := planEnv(t, true, "devs")
	pl := &aiv1.AgentPlan{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shop-plan-r"},
		Spec:   aiv1.AgentPlanSpec{ProjectRef: "shop", CreatedBy: "ada@example.com"},
		Status: aiv1.AgentPlanStatus{Phase: aiv1.AgentPlanIdle, Draft: "## Requirements\n\n- **R1** one\n- **R2** two\n- **R3** three, see R1\n"}}
	if err := env.reader.Create(context.Background(), pl); err != nil {
		t.Fatal(err)
	}
	fake := &fakeEstimates{reqs: map[int64]int{9: 5}}
	for i, tok := range []int64{0, 1, 4, 9} {
		fake.hist = append(fake.hist, store.MilestoneSpend{IID: int64(10 + i), Requirements: 1, Issues: 1, CostUSD: store.USD(2), Tokens: tok})
	}
	env.server.estimateReads = fake
	return env, fake
}

func TestEstimate(t *testing.T) {
	env, fake := estimateEnv(t)
	cases := []struct {
		name, query string
		unit        store.EstimateUnit
		count       int
		typical     int64
	}{
		{"plan draft's requirements", "?plan=shop-plan-r", store.UnitRequirement, 3, 3 * 2},
		{"planned milestone uses issues", "?milestone=3", store.UnitIssue, 1, 2},
		{"unplanned milestone uses requirements", "?milestone=9", store.UnitRequirement, 5, 5 * 2},
	}
	for _, tc := range cases {
		var got EstimateView
		if code := env.get(t, shopEstimate+tc.query, &got); code != http.StatusOK {
			t.Fatalf("%s: status %d", tc.name, code)
		}
		usd := float64(2 * tc.count)
		if got.Unit != tc.unit || got.Count != tc.count || got.NotEnoughHistory || got.Milestones != 4 || got.Reason != "" ||
			got.LowUSD != store.USD(usd) || got.TypicalUSD != store.USD(usd) || got.HighUSD != store.USD(usd) ||
			got.TypicalTokens != int64(math.Round(2.5*float64(tc.count))) {
			t.Errorf("%s: %+v", tc.name, got)
		}
	}
	if len(fake.limits) != len(cases) || fake.limits[0] != 10 {
		t.Errorf("history limits %v, want 10 per estimate", fake.limits)
	}

	// The requirements of %7 are unknown, the old plan's draft has none.
	for _, q := range []string{"?milestone=7", "?plan=shop-plan-old"} {
		var got EstimateView
		if code := env.get(t, shopEstimate+q, &got); code != http.StatusOK || !got.NotEnoughHistory ||
			got.Unit != store.UnitRequirement || got.Count != 0 || got.TypicalUSD.Known || got.Reason == "" {
			t.Errorf("%s: %d %+v", q, code, got)
		}
	}

	// Fewer than 3 finished milestones.
	fake.hist = fake.hist[:2]
	var got EstimateView
	if code := env.get(t, shopEstimate+"?plan=shop-plan-r", &got); code != http.StatusOK || !got.NotEnoughHistory ||
		got.Milestones != 2 || got.Count != 3 || got.TypicalUSD.Known || got.Reason == "" {
		t.Errorf("few milestones: %d %+v", code, got)
	}
}

func TestEstimateErrors(t *testing.T) {
	env, _ := estimateEnv(t)
	for q, want := range map[string]int{
		"":                              http.StatusBadRequest,
		"?plan=shop-plan-r&milestone=3": http.StatusBadRequest,
		"?milestone=x":                  http.StatusBadRequest,
		"?milestone=0":                  http.StatusBadRequest,
		"?plan=":                        http.StatusBadRequest,
		"?plan=billing-plan":            http.StatusNotFound,
		"?plan=nope":                    http.StatusNotFound,
	} {
		if code := env.get(t, shopEstimate+q, nil); code != want {
			t.Errorf("%q: status %d, want %d", q, code, want)
		}
	}
	if code := env.get(t, "/api/v1/projects/team-b/billing/estimate?milestone=8", nil); code != http.StatusNotFound {
		t.Errorf("no view of the project: status %d", code)
	}

	env.server.estimateReads = nil
	if code := env.get(t, shopEstimate+"?milestone=3", nil); code != http.StatusServiceUnavailable {
		t.Errorf("without the database: status %d", code)
	}
	off := milestonesEnv(t)
	off.server.estimateReads = &fakeEstimates{}
	if code := off.get(t, shopEstimate+"?milestone=3", nil); code != http.StatusNotFound {
		t.Errorf("research off: status %d", code)
	}
}
