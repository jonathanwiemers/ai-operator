package ui

import (
	"net/http"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

func TestCostsMetrics(t *testing.T) {
	env, fake := costsEnv(t, false, "devs")
	fake.metrics = store.Metrics{
		Arms: []store.MetricsGroup{
			{Namespace: "team-a", Project: "shop", Key: "baseline", Issues: 5, MergedLines: 500, CostUSD: 11, Tokens: 600},
			{Namespace: "team-a", Project: "shop", Key: "fresh", Issues: 4, MergedLines: 200, CostUSD: 7, Tokens: 500},
		},
		ByModel:       []store.MetricsGroup{{Key: "claude-opus-5-5", Issues: 9, MergedLines: 700, CostUSD: 18, Tokens: 1100}},
		ByEffort:      []store.MetricsGroup{{Key: "high", Issues: 9, MergedLines: 700, CostUSD: 18, Tokens: 1100}},
		ByStepSession: []store.MetricsGroup{{Key: "", Issues: 0, MergedLines: 0, CostUSD: 2, Tokens: 100}},
		BaseContext:   []store.RoleMedian{{Namespace: "team-a", Project: "shop", Role: "implement", MedianTokens: 21000, Calls: 3}},
		TopTools: []store.ToolUsage{
			{Namespace: "team-a", Project: "shop", Tool: "Read", Calls: 12, Bytes: 81234},
			{Namespace: "team-a", Project: "shop", Tool: "Bash", Calls: 3, Bytes: 4000},
		},
	}
	var v MetricsView
	if code := env.get(t, "/api/v1/costs/metrics?range=7d&kind=implement", &v); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	// Only the viewer's project reaches the store, research is excluded, the
	// range applies.
	if len(fake.metricsQ) != 1 {
		t.Fatalf("queries %d", len(fake.metricsQ))
	}
	if q := fake.metricsQ[0]; !slices.Equal(q.Projects, shopKey) || !slices.Equal(q.ExcludeKinds, []string{"research"}) ||
		q.To.Sub(q.From).Hours() != 7*24 {
		t.Errorf("query = %+v", q)
	}
	// The cost per 100 merged lines; low below 5 merged issues; the tokens
	// estimated from the bytes; no experiment name, team-a/shop has none.
	sameJSON(t, v, `{
		"arms": [
			{"project": "team-a/shop", "key": "baseline", "issues": 5, "mergedLines": 500, "costUSD": 11, "tokens": 600,
				"costPer100Lines": 2.2, "low": false},
			{"project": "team-a/shop", "key": "fresh", "issues": 4, "mergedLines": 200, "costUSD": 7, "tokens": 500,
				"costPer100Lines": 3.5, "low": true}
		],
		"byModel": [{"key": "claude-opus-5-5", "issues": 9, "mergedLines": 700, "costUSD": 18, "tokens": 1100,
			"costPer100Lines": 2.5714285714285716, "low": false}],
		"byEffort": [{"key": "high", "issues": 9, "mergedLines": 700, "costUSD": 18, "tokens": 1100,
			"costPer100Lines": 2.5714285714285716, "low": false}],
		"byStepSession": [{"key": "", "issues": 0, "mergedLines": 0, "costUSD": 2, "tokens": 100, "costPer100Lines": 0, "low": true}],
		"context": [{"project": "team-a/shop",
			"baseContext": [{"role": "implement", "medianTokens": 21000, "calls": 3}],
			"topTools": [{"tool": "Read", "calls": 12, "bytes": 81234, "tokens": 20308}, {"tool": "Bash", "calls": 3, "bytes": 4000, "tokens": 1000}]}]
	}`)

	if code := env.get(t, "/api/v1/costs/metrics?project=team-b/billing", nil); code != http.StatusNotFound {
		t.Errorf("another project: status %d", code)
	}
	if code := env.get(t, "/api/v1/costs/metrics?range=week", nil); code != http.StatusBadRequest {
		t.Errorf("bad range: status %d", code)
	}
	none, _ := costsEnv(t, true, "devs")
	if code := none.get(t, "/api/v1/costs/metrics", nil); code != http.StatusServiceUnavailable {
		t.Errorf("without the database: status %d", code)
	}
	off := milestonesEnv(t)
	off.server.costReads = &fakeCosts{}
	if code := off.get(t, "/api/v1/costs/metrics", nil); code != http.StatusNotFound {
		t.Errorf("research off: status %d", code)
	}
}

// TestMetricsViewEmpty: without data every list is empty, not null, so the
// web hides the arm table and shows empty tables.
func TestMetricsViewEmpty(t *testing.T) {
	sameJSON(t, metricsView(store.Metrics{}, nil), `{"arms": [], "byModel": [], "byEffort": [], "byStepSession": [], "context": []}`)
}

// TestMetricsViewExperiment: an arm of the project's current experiment
// carries the experiment's name, an arm it no longer has none; projects
// with only base context or only tools get a context block each.
func TestMetricsViewExperiment(t *testing.T) {
	shop := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shop"}}
	shop.Spec.Agents.Implement.Experiment = &aiv1.Experiment{Name: "cost-levers",
		Arms: []aiv1.ExperimentArm{{Name: "baseline"}, {Name: "fresh", StepSession: "fresh"}}}
	v := metricsView(store.Metrics{
		Arms: []store.MetricsGroup{
			{Namespace: "team-a", Project: "shop", Key: "baseline"},
			{Namespace: "team-a", Project: "shop", Key: "old"},
			{Namespace: "team-b", Project: "billing", Key: "baseline"},
		},
		BaseContext: []store.RoleMedian{{Namespace: "team-a", Project: "shop", Role: "review", MedianTokens: 9, Calls: 1}},
		TopTools:    []store.ToolUsage{{Namespace: "team-b", Project: "billing", Tool: "Grep", Calls: 1, Bytes: 7}},
	}, map[string]*aiv1.AgentProject{"team-a/shop": shop})
	if got := []string{v.Arms[0].Experiment, v.Arms[1].Experiment, v.Arms[2].Experiment}; !slices.Equal(got, []string{"cost-levers", "", ""}) {
		t.Errorf("experiments = %q", got)
	}
	if len(v.Context) != 2 || v.Context[0].Project != "team-a/shop" || len(v.Context[0].TopTools) != 0 ||
		v.Context[1].Project != "team-b/billing" || len(v.Context[1].BaseContext) != 0 || v.Context[1].TopTools[0].Tokens != 1 {
		t.Errorf("context = %+v", v.Context)
	}
}
