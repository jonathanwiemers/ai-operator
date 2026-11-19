package ui

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

func (f *fakeCosts) CodeInRange(_ context.Context, q store.CostQuery, bucket string) (store.CodeRange, error) {
	f.codeCalls = append(f.codeCalls, costSeriesCall{q: q, bucket: bucket})
	return f.code, nil
}

// TestMetricsCode: the store's lines of today, with a project row, as the
// view answers them; the range, the project filter (no project rows), an
// invisible project and bad parameters.
func TestMetricsCode(t *testing.T) {
	env, fake := costsEnv(t, false, "devs")
	today := time.Now().UTC().Format(time.DateOnly)
	totals := store.CodeTotals{
		Written: store.LineCount{Added: 157, Deleted: 48},
		Merged:  store.MergedLines{Production: store.LineCount{Added: 64, Deleted: 11}, Tests: store.LineCount{Added: 60}, Partial: true},
		Issues: store.MergedLineTotals{Merged: 4, Measured: 3, CostPer100USD: store.USD(8.0 * 100 / 70),
			Lines: store.MergedLines{Production: store.LineCount{Added: 60, Deleted: 10}}},
		Cost: store.SpendTotals{CostUSD: store.USD(8), Calls: 5},
	}
	fake.code = store.CodeRange{
		Buckets:   []store.CodeBucket{{Bucket: today, Written: totals.Written, Merged: totals.Merged}},
		Totals:    totals,
		ByProject: []store.CodeProject{{Namespace: "team-a", Project: "shop", CodeTotals: totals}},
	}
	var v MetricsCodeView
	if code := env.get(t, "/api/v1/metrics/code?range=7d&kind=review&engine=cursor&group=kind", &v); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	// Only the viewer's project reaches the store, over the range; kind and
	// engine do not apply and research is never added.
	if len(fake.codeCalls) != 1 {
		t.Fatalf("queries %d", len(fake.codeCalls))
	}
	if c := fake.codeCalls[0]; !slices.Equal(c.q.Projects, shopKey) || c.q.Kind != "" || c.q.Engine != "" || c.q.ExcludeKinds != nil ||
		c.q.To.Sub(c.q.From).Hours() != 7*24 || c.bucket != "day" {
		t.Errorf("query = %+v", c)
	}
	if v.Range != "7d" || v.Bucket != "day" || v.TZ != "UTC" || v.To != today || len(v.Buckets) != 7 {
		t.Fatalf("range = %s %s %s %s, %d buckets", v.Range, v.Bucket, v.TZ, v.To, len(v.Buckets))
	}
	// Every bucket of the range is there, the ones without lines zeros.
	zero := `{"added": 0, "deleted": 0}`
	sameJSON(t, v.Buckets[0], `{"bucket": "`+v.From+`", "written": `+zero+`, "merged": {"production": `+zero+`, "tests": `+zero+`,
		"docs": `+zero+`, "generated": `+zero+`}}`)
	sameJSON(t, v.Buckets[6], `{"bucket": "`+today+`", "written": {"added": 157, "deleted": 48}, "merged": {
		"production": {"added": 64, "deleted": 11}, "tests": {"added": 60, "deleted": 0}, "docs": `+zero+`, "generated": `+zero+`,
		"partial": true}}`)
	wantTotals := `{"written": {"added": 157, "deleted": 48},
		"merged": {"production": {"added": 64, "deleted": 11}, "tests": {"added": 60, "deleted": 0}, "docs": ` + zero + `,
			"generated": ` + zero + `, "partial": true},
		"issues": {"merged": 4, "measured": 3, "costPer100USD": "11.43", "lines": {"production": {"added": 60, "deleted": 10},
			"tests": ` + zero + `, "docs": ` + zero + `, "generated": ` + zero + `}},
		"cost": {"costUSD": "8.00", "costKnown": true, "unknownCalls": 0, "unmeteredCalls": 0, "oldBasisCalls": 0,
			"inputTokens": 0, "outputTokens": 0, "cacheReadTokens": 0, "cacheWriteTokens": 0, "calls": 5, "subagentCalls": 0,
			"durationMS": 0, "checkpoints": 0}}`
	sameJSON(t, v.Totals, wantTotals)
	if len(v.ByProject) != 1 {
		t.Fatalf("by project = %+v", v.ByProject)
	}
	sameJSON(t, v.ByProject[0], `{"namespace": "team-a", "project": "shop", "totals": `+wantTotals+`}`)

	// The project filter: no project rows. A 90-day range by day in a zone;
	// 12 months by month.
	if code := env.get(t, "/api/v1/metrics/code?range=90d&project=team-a/shop&tz=Europe/Berlin", &v); code != http.StatusOK {
		t.Fatalf("project: status %d", code)
	}
	if c := fake.codeCalls[1]; !slices.Equal(c.q.Projects, shopKey) || c.q.Loc.String() != "Europe/Berlin" || c.bucket != "day" ||
		v.TZ != "Europe/Berlin" || len(v.Buckets) != 90 || v.ByProject == nil || len(v.ByProject) != 0 {
		t.Errorf("90d: query %+v, %d buckets, by project %+v", c, len(v.Buckets), v.ByProject)
	}
	if code := env.get(t, "/api/v1/metrics/code?range=12m", &v); code != http.StatusOK || fake.codeCalls[2].bucket != "month" ||
		len(v.Buckets) == 0 || v.Bucket != "month" {
		t.Errorf("12m: status %d, bucket %s", code, v.Bucket)
	}

	// A project the viewer may not see is not found, as on /api/v1/costs;
	// bad parameters are 400.
	for _, path := range []string{"/api/v1/metrics/code?project=team-b/billing", "/api/v1/metrics/code?project=team-a/nope"} {
		if code := env.get(t, path, nil); code != http.StatusNotFound {
			t.Errorf("%s: status %d", path, code)
		}
	}
	for _, path := range []string{"/api/v1/metrics/code?range=week", "/api/v1/metrics/code?tz=Mars/Base",
		"/api/v1/metrics/code?from=2026-01-01", "/api/v1/metrics/code?kind=nope", "/api/v1/metrics/code?engine=nope"} {
		if code := env.get(t, path, nil); code != http.StatusBadRequest {
			t.Errorf("%s: status %d", path, code)
		}
	}
	if len(fake.codeCalls) != 3 {
		t.Errorf("queries %d, want 3", len(fake.codeCalls))
	}

	none, _ := costsEnv(t, true, "devs")
	if code := none.get(t, "/api/v1/metrics/code", nil); code != http.StatusServiceUnavailable {
		t.Errorf("without the database: status %d", code)
	}
	off := milestonesEnv(t)
	off.server.costReads = &fakeCosts{}
	if code := off.get(t, "/api/v1/metrics/code", nil); code != http.StatusNotFound {
		t.Errorf("research off: status %d", code)
	}
}

// TestMetricsCodeViewEmpty: no lines give zeros per bucket start, an
// unknown cost per 100 lines (left out) and empty lists, no nulls.
func TestMetricsCodeViewEmpty(t *testing.T) {
	day := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	p := costParams{rng: "custom", first: day, last: day, bucket: "day", q: store.CostQuery{Loc: time.UTC}}
	zero := `{"added": 0, "deleted": 0}`
	lines := `{"production": ` + zero + `, "tests": ` + zero + `, "docs": ` + zero + `, "generated": ` + zero + `}`
	sameJSON(t, metricsCodeView(p, store.CodeRange{}, true), `{"range": "custom", "from": "2026-11-02", "to": "2026-11-02",
		"bucket": "day", "tz": "UTC", "buckets": [{"bucket": "2026-11-02", "written": `+zero+`, "merged": `+lines+`}],
		"totals": {"written": `+zero+`, "merged": `+lines+`, "issues": {"merged": 0, "measured": 0, "lines": `+lines+`},
			"cost": {"costUSD": "", "costKnown": false, "unknownCalls": 0, "unmeteredCalls": 0, "oldBasisCalls": 0,
				"inputTokens": 0, "outputTokens": 0, "cacheReadTokens": 0, "cacheWriteTokens": 0, "calls": 0, "subagentCalls": 0,
				"durationMS": 0, "checkpoints": 0}},
		"byProject": []}`)
}
