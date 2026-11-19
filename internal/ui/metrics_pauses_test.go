package ui

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

func (f *fakeCosts) PausesInRange(_ context.Context, q store.CostQuery, bucket string, _ time.Time) (store.PauseRange, error) {
	f.pausesCalls = append(f.pausesCalls, costSeriesCall{q: q, bucket: bucket})
	return f.pauses, nil
}

// TestMetricsPauses: the acceptance scenario as the store answers it: two
// red-CI pauses the owner resumed, a budget stop open now.
func TestMetricsPauses(t *testing.T) {
	env, fake := costsEnv(t, false, "devs")
	today := time.Now().UTC().Format(time.DateOnly)
	since := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	until := since.Add(2 * time.Hour)
	fake.pauses = store.PauseRange{
		Buckets: []store.PauseBucket{{Bucket: today, Count: 3, PausedSec: 30600,
			ByKind: map[string]store.PauseKindTotal{"ciRed": {Count: 2, Sec: 9000}, "budget": {Count: 1, Sec: 21600}}}},
		Totals: store.PauseTotals{Count: 3, PausedSec: 30600, MeanResumeSec: 4500,
			ByKind:     map[string]store.PauseKindTotal{"ciRed": {Count: 2, Sec: 9000}, "budget": {Count: 1, Sec: 21600}},
			ByResolver: map[string]int{"owner": 2}, ByVia: map[string]int{"command": 2}},
		OpenNow:   1,
		ByProject: []store.PauseProject{{Namespace: "team-a", Project: "shop", Count: 3, PausedSec: 30600}},
		Longest: []store.RangePause{
			{Namespace: "team-a", Project: "shop", MilestoneIID: 3,
				Pause: store.Pause{Kind: "budget", Reason: "over budget", Since: since.Add(4 * time.Hour), Seconds: 21600}},
			{Namespace: "team-a", Project: "shop", MilestoneIID: 3,
				Pause: store.Pause{Kind: "ciRed", Since: since, Until: &until, ResolvedBy: "owner", Seconds: 7200}},
		},
		Truncated: true,
	}
	var v MetricsPausesView
	if code := env.get(t, "/api/v1/metrics/pauses?range=7d&kind=review&engine=cursor&group=kind", &v); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	// Only the viewer's project reaches the store, over the range; kind and
	// engine do not apply and research is never added.
	if len(fake.pausesCalls) != 1 {
		t.Fatalf("queries %d", len(fake.pausesCalls))
	}
	if c := fake.pausesCalls[0]; !slices.Equal(c.q.Projects, shopKey) || c.q.Kind != "" || c.q.Engine != "" || c.q.ExcludeKinds != nil ||
		c.q.To.Sub(c.q.From).Hours() != 7*24 || c.bucket != "day" {
		t.Errorf("query = %+v", c)
	}
	if v.Range != "7d" || v.Bucket != "day" || v.TZ != "UTC" || v.To != today || len(v.Buckets) != 7 {
		t.Fatalf("range = %s %s %s %s, %d buckets", v.Range, v.Bucket, v.TZ, v.To, len(v.Buckets))
	}
	// Every bucket of the range is there, the ones without pauses empty.
	sameJSON(t, v.Buckets[0], `{"bucket": "`+v.From+`", "count": 0, "pausedSec": 0, "byKind": {}}`)
	sameJSON(t, v.Buckets[6], `{"bucket": "`+today+`", "count": 3, "pausedSec": 30600,
		"byKind": {"budget": {"count": 1, "sec": 21600}, "ciRed": {"count": 2, "sec": 9000}}}`)
	sameJSON(t, struct {
		Totals    store.PauseTotals  `json:"totals"`
		OpenNow   int                `json:"openNow"`
		ByProject []PauseProjectView `json:"byProject"`
		Longest   []LongPauseView    `json:"longest"`
		Truncated bool               `json:"truncated"`
	}{v.Totals, v.OpenNow, v.ByProject, v.Longest, v.Truncated}, `{
		"totals": {"count": 3, "pausedSec": 30600, "byKind": {"budget": {"count": 1, "sec": 21600}, "ciRed": {"count": 2, "sec": 9000}},
			"meanResumeSec": 4500, "byResolver": {"owner": 2}, "byVia": {"command": 2}},
		"openNow": 1,
		"byProject": [{"namespace": "team-a", "project": "shop", "count": 3, "pausedSec": 30600}],
		"longest": [
			{"namespace": "team-a", "project": "shop", "milestoneIID": 3, "kind": "budget", "reason": "over budget", "seconds": 21600,
				"since": "2026-11-05T14:00:00Z", "open": true, "deskURL": "/projects/team-a/shop/milestones/3?tab=pilot"},
			{"namespace": "team-a", "project": "shop", "milestoneIID": 3, "kind": "ciRed", "seconds": 7200,
				"since": "2026-11-05T10:00:00Z", "open": false, "deskURL": "/projects/team-a/shop/milestones/3?tab=pilot"}
		],
		"truncated": true
	}`)

	// The project filter and a 90-day range by day; 12 months by month.
	if code := env.get(t, "/api/v1/metrics/pauses?range=90d&project=team-a/shop&tz=Europe/Berlin", &v); code != http.StatusOK {
		t.Fatalf("project: status %d", code)
	}
	if c := fake.pausesCalls[1]; !slices.Equal(c.q.Projects, shopKey) || c.q.Loc.String() != "Europe/Berlin" || c.bucket != "day" ||
		v.TZ != "Europe/Berlin" || len(v.Buckets) != 90 {
		t.Errorf("90d: query %+v, %d buckets", c, len(v.Buckets))
	}
	if code := env.get(t, "/api/v1/metrics/pauses?range=12m", &v); code != http.StatusOK || fake.pausesCalls[2].bucket != "month" ||
		len(v.Buckets) == 0 || v.Bucket != "month" {
		t.Errorf("12m: status %d, bucket %s", code, v.Bucket)
	}

	// A project the viewer may not see is not found, as on /api/v1/costs;
	// bad parameters are 400.
	for _, path := range []string{"/api/v1/metrics/pauses?project=team-b/billing", "/api/v1/metrics/pauses?project=team-a/nope"} {
		if code := env.get(t, path, nil); code != http.StatusNotFound {
			t.Errorf("%s: status %d", path, code)
		}
	}
	for _, path := range []string{"/api/v1/metrics/pauses?range=week", "/api/v1/metrics/pauses?tz=Mars/Base",
		"/api/v1/metrics/pauses?from=2026-01-01", "/api/v1/metrics/pauses?kind=nope"} {
		if code := env.get(t, path, nil); code != http.StatusBadRequest {
			t.Errorf("%s: status %d", path, code)
		}
	}
	if len(fake.pausesCalls) != 3 {
		t.Errorf("queries %d, want 3", len(fake.pausesCalls))
	}

	none, _ := costsEnv(t, true, "devs")
	if code := none.get(t, "/api/v1/metrics/pauses", nil); code != http.StatusServiceUnavailable {
		t.Errorf("without the database: status %d", code)
	}
	off := milestonesEnv(t)
	off.server.costReads = &fakeCosts{}
	if code := off.get(t, "/api/v1/metrics/pauses", nil); code != http.StatusNotFound {
		t.Errorf("research off: status %d", code)
	}
}

// TestMetricsPausesViewEmpty: without pauses every list is empty, not null (the store
// sends empty maps).
func TestMetricsPausesViewEmpty(t *testing.T) {
	day := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	p := costParams{rng: "custom", first: day, last: day, bucket: "day", q: store.CostQuery{Loc: time.UTC}}
	sameJSON(t, metricsPausesView(p, store.PauseRange{Totals: store.PauseTotals{
		ByKind: map[string]store.PauseKindTotal{}, ByResolver: map[string]int{}, ByVia: map[string]int{}}}), `{"range": "custom", "from": "2026-11-02", "to": "2026-11-02",
		"bucket": "day", "tz": "UTC", "buckets": [{"bucket": "2026-11-02", "count": 0, "pausedSec": 0, "byKind": {}}],
		"totals": {"count": 0, "pausedSec": 0, "byKind": {}, "meanResumeSec": 0, "byResolver": {}, "byVia": {}},
		"openNow": 0, "byProject": [], "longest": [], "truncated": false}`)
}
