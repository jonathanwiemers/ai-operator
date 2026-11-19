package ui

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

func (f *fakeCosts) FlowInRange(_ context.Context, q store.CostQuery, bucket string, _ time.Time) (store.FlowRange, error) {
	f.flowCalls = append(f.flowCalls, costSeriesCall{q: q, bucket: bucket})
	return f.flow, nil
}

// TestMetricsFlow: three issues merged today, one without a run start, as
// the store answers them; the range, the project filter, an invisible
// project and bad parameters.
func TestMetricsFlow(t *testing.T) {
	env, fake := costsEnv(t, false, "devs")
	today := time.Now().UTC().Format(time.DateOnly)
	fake.flow = store.FlowRange{
		Buckets: []store.FlowBucket{{Bucket: today, Merged: 3}},
		Totals: store.FlowTotals{MergedIssues: 3, WallIssues: 2, MedianWallSec: 10800, AgentIssues: 3, MedianAgentSec: 1350,
			ReviewCallsPerIssue: 1, FixCallsPerIssue: 0.5},
	}
	var v MetricsFlowView
	if code := env.get(t, "/api/v1/metrics/flow?range=7d&kind=review&engine=cursor&group=kind", &v); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	// Only the viewer's project reaches the store, over the range; kind and
	// engine do not apply and research is never added.
	if len(fake.flowCalls) != 1 {
		t.Fatalf("queries %d", len(fake.flowCalls))
	}
	if c := fake.flowCalls[0]; !slices.Equal(c.q.Projects, shopKey) || c.q.Kind != "" || c.q.Engine != "" || c.q.ExcludeKinds != nil ||
		c.q.To.Sub(c.q.From).Hours() != 7*24 || c.bucket != "day" {
		t.Errorf("query = %+v", c)
	}
	if v.Range != "7d" || v.Bucket != "day" || v.TZ != "UTC" || v.To != today || len(v.Buckets) != 7 {
		t.Fatalf("range = %s %s %s %s, %d buckets", v.Range, v.Bucket, v.TZ, v.To, len(v.Buckets))
	}
	// Every bucket of the range is there, the ones without merges 0.
	sameJSON(t, v.Buckets[0], `{"bucket": "`+v.From+`", "merged": 0}`)
	sameJSON(t, v.Buckets[6], `{"bucket": "`+today+`", "merged": 3}`)
	sameJSON(t, v.Totals, `{"mergedIssues": 3, "wallIssues": 2, "medianWallSec": 10800, "agentIssues": 3, "medianAgentSec": 1350,
		"reviewCallsPerIssue": 1, "fixCallsPerIssue": 0.5}`)

	// The project filter and a 90-day range by day in a zone; 12 months by
	// month.
	if code := env.get(t, "/api/v1/metrics/flow?range=90d&project=team-a/shop&tz=Europe/Berlin", &v); code != http.StatusOK {
		t.Fatalf("project: status %d", code)
	}
	if c := fake.flowCalls[1]; !slices.Equal(c.q.Projects, shopKey) || c.q.Loc.String() != "Europe/Berlin" || c.bucket != "day" ||
		v.TZ != "Europe/Berlin" || len(v.Buckets) != 90 {
		t.Errorf("90d: query %+v, %d buckets", c, len(v.Buckets))
	}
	if code := env.get(t, "/api/v1/metrics/flow?range=12m", &v); code != http.StatusOK || fake.flowCalls[2].bucket != "month" ||
		len(v.Buckets) == 0 || v.Bucket != "month" {
		t.Errorf("12m: status %d, bucket %s", code, v.Bucket)
	}

	// A project the viewer may not see is not found, as on /api/v1/costs;
	// bad parameters are 400.
	for _, path := range []string{"/api/v1/metrics/flow?project=team-b/billing", "/api/v1/metrics/flow?project=team-a/nope"} {
		if code := env.get(t, path, nil); code != http.StatusNotFound {
			t.Errorf("%s: status %d", path, code)
		}
	}
	for _, path := range []string{"/api/v1/metrics/flow?range=week", "/api/v1/metrics/flow?tz=Mars/Base",
		"/api/v1/metrics/flow?from=2026-01-01", "/api/v1/metrics/flow?kind=nope", "/api/v1/metrics/flow?group=nope"} {
		if code := env.get(t, path, nil); code != http.StatusBadRequest {
			t.Errorf("%s: status %d", path, code)
		}
	}
	if len(fake.flowCalls) != 3 {
		t.Errorf("queries %d, want 3", len(fake.flowCalls))
	}

	none, _ := costsEnv(t, true, "devs")
	if code := none.get(t, "/api/v1/metrics/flow", nil); code != http.StatusServiceUnavailable {
		t.Errorf("without the database: status %d", code)
	}
	off := milestonesEnv(t)
	off.server.costReads = &fakeCosts{}
	if code := off.get(t, "/api/v1/metrics/flow", nil); code != http.StatusNotFound {
		t.Errorf("research off: status %d", code)
	}
}

// TestMetricsFlowViewEmpty: nothing merged gives zeros and an empty bucket
// per bucket start, no nulls.
func TestMetricsFlowViewEmpty(t *testing.T) {
	day := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	p := costParams{rng: "custom", first: day, last: day.AddDate(0, 0, 1), bucket: "day", q: store.CostQuery{Loc: time.UTC}}
	sameJSON(t, metricsFlowView(p, store.FlowRange{}), `{"range": "custom", "from": "2026-11-02", "to": "2026-11-03",
		"bucket": "day", "tz": "UTC", "buckets": [{"bucket": "2026-11-02", "merged": 0}, {"bucket": "2026-11-03", "merged": 0}],
		"totals": {"mergedIssues": 0, "wallIssues": 0, "medianWallSec": 0, "agentIssues": 0, "medianAgentSec": 0,
			"reviewCallsPerIssue": 0, "fixCallsPerIssue": 0}}`)
}
