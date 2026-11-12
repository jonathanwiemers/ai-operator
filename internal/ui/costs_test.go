package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// fakeCosts answers series for the range's query, month for the month's
// (group ""), and breakdowns; it records the queries.
type fakeCosts struct {
	series, month []store.CostPoint
	breakdowns    store.CostBreakdowns
	seriesCalls   []costSeriesCall
	breakdownQ    []store.CostQuery
	top           store.CostTop
	topQ          []store.CostQuery
	topN          []int
}

type costSeriesCall struct {
	q             store.CostQuery
	bucket, group string
}

func (f *fakeCosts) CostSeries(_ context.Context, q store.CostQuery, bucket, group string) ([]store.CostPoint, error) {
	f.seriesCalls = append(f.seriesCalls, costSeriesCall{q, bucket, group})
	if group == "" {
		return f.month, nil
	}
	return f.series, nil
}

func (f *fakeCosts) CostBreakdowns(_ context.Context, q store.CostQuery) (store.CostBreakdowns, error) {
	f.breakdownQ = append(f.breakdownQ, q)
	return f.breakdowns, nil
}

func (f *fakeCosts) CostTop(_ context.Context, q store.CostQuery, n int) (store.CostTop, error) {
	f.topQ = append(f.topQ, q)
	f.topN = append(f.topN, n)
	return f.top, nil
}

// costsEnv is a Desk with the research feature on whose user may see
// team-a (devs) and, with the role readers, read research; the figures come
// from the returned fake unless noStore.
func costsEnv(t *testing.T, noStore bool, roles ...string) (*testEnv, *fakeCosts) {
	t.Helper()
	cfg := oidcConfig(Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}},
		Rule{Roles: []string{"readers"}, Research: ResearchRead})
	cfg.Research.Database = DatabaseConfig{Host: "db.example.svc", Name: "ai_operator"}
	cfg.ResearchDBUser, cfg.ResearchDBPassword = "reader", "secret"
	env := newTestEnv(t, cfg)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	granted := map[string]any{}
	for _, r := range roles {
		granted[r] = map[string]any{}
	}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": granted}
	fake := &fakeCosts{}
	if !noStore {
		env.server.costReads = fake
	}
	env.login(t, "/")
	return env, fake
}

var shopKey = []store.ProjectKey{{Namespace: "team-a", Name: "shop"}}

func TestCostsScope(t *testing.T) {
	env, fake := costsEnv(t, false, "devs")
	var v CostsView
	if code := env.get(t, "/api/v1/costs", &v); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	// team-b/billing exists but is not visible: the store never hears of it,
	// and research is left out for a viewer without research access.
	if len(fake.seriesCalls) != 2 || len(fake.breakdownQ) != 1 {
		t.Fatalf("queries: %d series, %d breakdowns", len(fake.seriesCalls), len(fake.breakdownQ))
	}
	for _, q := range []store.CostQuery{fake.seriesCalls[0].q, fake.seriesCalls[1].q, fake.breakdownQ[0]} {
		if !slices.Equal(q.Projects, shopKey) || !slices.Equal(q.ExcludeKinds, []string{"research"}) {
			t.Errorf("query = %+v", q)
		}
	}
	// Live state is there even without any: lists, not null.
	if v.PlanUsage == nil || v.Budgets == nil {
		t.Errorf("planUsage %v, budgets %v: want empty lists", v.PlanUsage, v.Budgets)
	}
	if v.Range != "30d" || v.Group != "project" || v.Bucket != "day" || v.TZ != "UTC" || len(v.Buckets) != 30 {
		t.Errorf("defaults = %s %s %s %s, %d buckets", v.Range, v.Group, v.Bucket, v.TZ, len(v.Buckets))
	}

	if code := env.get(t, "/api/v1/costs?project=team-b/billing", nil); code != http.StatusNotFound {
		t.Errorf("another project: status %d", code)
	}
	if code := env.get(t, "/api/v1/costs?project=team-a/nope", nil); code != http.StatusNotFound {
		t.Errorf("a missing project: status %d", code)
	}
	if code := env.get(t, "/api/v1/costs?project=team-a/shop&kind=review&engine=cursor", nil); code != http.StatusOK {
		t.Fatalf("own project: status %d", code)
	}
	if q := fake.breakdownQ[len(fake.breakdownQ)-1]; !slices.Equal(q.Projects, shopKey) || q.Kind != "review" || q.Engine != "cursor" {
		t.Errorf("filtered query = %+v", q)
	}
}

func TestCostsResearchAccess(t *testing.T) {
	env, fake := costsEnv(t, false, "devs", "readers")
	since := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	fake.breakdowns = store.CostBreakdowns{Totals: store.SpendTotals{CostUSD: store.USD(3)}, ResearchSince: &since,
		ByKind: []store.SpendBreakdown{{Key: "research", Totals: store.SpendTotals{CostUSD: store.USD(3), Calls: 1}}}}
	var v CostsView
	if code := env.get(t, "/api/v1/costs", &v); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if q := fake.breakdownQ[0]; len(q.ExcludeKinds) != 0 {
		t.Errorf("research reader excludes %v", q.ExcludeKinds)
	}
	if v.ResearchSince == nil || !v.ResearchSince.Equal(since) || len(v.ByKind) != 1 || v.ByKind[0].Key != "research" ||
		v.Totals.CostUSD != store.USD(3) {
		t.Errorf("view = %+v", v)
	}
}

func TestCostsBadParams(t *testing.T) {
	env, fake := costsEnv(t, false, "devs")
	today := time.Now().UTC()
	day := func(d int) string { return today.AddDate(0, 0, d).Format(time.DateOnly) }
	for _, q := range []string{
		"range=week", "range=30d&from=" + day(-3) + "&to=" + day(0), "from=" + day(-3), "to=" + day(0),
		"from=" + day(-1) + "&to=" + day(-2), "from=" + day(-1) + "&to=" + day(1), "from=" + day(-400) + "&to=" + day(0),
		"from=yesterday&to=" + day(0), "from=" + day(-3) + "&to=2026-13-01",
		"tz=Mars/Base", "tz=Local", "group=role", "kind=unknown", "engine=gpt",
	} {
		if code := env.get(t, "/api/v1/costs?"+q, nil); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, code)
		}
	}
	if len(fake.seriesCalls) != 0 {
		t.Errorf("bad requests reached the store: %+v", fake.seriesCalls)
	}
	// The oldest allowed from is today minus the retention (365 days).
	if code := env.get(t, "/api/v1/costs?from="+day(-365)+"&to="+day(0), nil); code != http.StatusOK {
		t.Errorf("from at the retention: status %d", code)
	}
}

func TestCostsUnavailable(t *testing.T) {
	env, _ := costsEnv(t, true, "devs")
	if code := env.get(t, "/api/v1/costs", nil); code != http.StatusServiceUnavailable {
		t.Errorf("without the database: status %d", code)
	}
	off := milestonesEnv(t)
	off.server.costReads = &fakeCosts{}
	if code := off.get(t, "/api/v1/costs", nil); code != http.StatusNotFound {
		t.Errorf("research off: status %d", code)
	}
}

func TestCostsMonth(t *testing.T) {
	env, fake := costsEnv(t, false, "devs")
	now := time.Now().UTC()
	this := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	last := this.AddDate(0, -1, 0)
	fake.month = []store.CostPoint{
		{Bucket: last.Format(time.DateOnly), Totals: store.SpendTotals{CostUSD: store.USD(1)}},
		{Bucket: last.AddDate(0, 0, 27).Format(time.DateOnly), Totals: store.SpendTotals{CostUSD: store.USD(2)}},
		{Bucket: this.Format(time.DateOnly), Totals: store.SpendTotals{CostUSD: store.USD(4)}},
	}
	var v CostsView
	if code := env.get(t, "/api/v1/costs?range=7d&kind=fix", &v); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	m := fake.seriesCalls[1]
	if m.group != "" || m.bucket != "day" || !m.q.From.Equal(last) ||
		!m.q.To.Equal(time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)) || m.q.Kind != "fix" {
		t.Errorf("month query = %+v", m)
	}
	if v.Month.Today != now.Day() || len(v.Month.Previous) != 2 || len(v.Month.Current) != 1 ||
		v.Month.Current[0].Totals.CostUSD != store.USD(4) {
		t.Errorf("month = %+v", v.Month)
	}
}

// costParamsAt parses query on s at now, without visible projects.
func costParamsAt(t *testing.T, s *Server, query string, now time.Time) costParams {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/costs?"+query, nil)
	p, err := s.parseCostParams(r, nil, now)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return p
}

func TestCostParamsRanges(t *testing.T) {
	env, _ := costsEnv(t, false, "devs")
	now := time.Date(2026, 11, 12, 15, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		query, first, last, bucket string
		buckets                    int
	}{
		{"", "2026-10-14", "2026-11-12", "day", 30},
		{"range=7d", "2026-11-06", "2026-11-12", "day", 7},
		{"range=90d", "2026-08-15", "2026-11-12", "day", 90},
		{"range=month", "2026-11-01", "2026-11-12", "day", 12},
		{"range=lastMonth", "2026-10-01", "2026-10-31", "day", 31},
		// Twelve calendar months by month.
		{"range=12m", "2025-12-01", "2026-11-12", "month", 12},
		// 91 days: weeks from the Monday on or before from (2026-08-12 is a
		// Wednesday).
		{"from=2026-08-12&to=2026-11-10", "2026-08-12", "2026-11-10", "week", 14},
		{"from=2026-05-16&to=2026-11-11", "2026-05-16", "2026-11-11", "week", 27},
		{"from=2026-05-15&to=2026-11-11", "2026-05-15", "2026-11-11", "month", 7},
	} {
		p := costParamsAt(t, env.server, c.query, now)
		if got := p.first.Format(time.DateOnly) + " " + p.last.Format(time.DateOnly) + " " + p.bucket; got != c.first+" "+c.last+" "+c.bucket {
			t.Errorf("%q = %s", c.query, got)
		}
		b := bucketStarts(p.first, p.last, p.bucket)
		if len(b) != c.buckets {
			t.Errorf("%q: %d buckets %v, want %d", c.query, len(b), b, c.buckets)
		}
	}
	if b := bucketStarts(costParamsAt(t, env.server, "from=2026-08-12&to=2026-11-10", now).first, now, "week"); b[0] != "2026-08-10" {
		t.Errorf("first week = %s", b[0])
	}

	// A named range keeps to a shorter retention.
	env.server.SetHistoryRetention(60 * 24 * time.Hour)
	if p := costParamsAt(t, env.server, "range=90d", now); p.first.Format(time.DateOnly) != "2026-09-13" || !p.earliest.Equal(p.first) {
		t.Errorf("90d with 60 days of retention: %s", p.first)
	}
}

// TestCostParamsDST: local dates across the end of summer time in Berlin
// (2026-10-25) become instants with each day's own offset.
func TestCostParamsDST(t *testing.T) {
	env, _ := costsEnv(t, false, "devs")
	// 23:30 UTC is already the 29th in Berlin.
	now := time.Date(2026, 10, 28, 23, 30, 0, 0, time.UTC)
	p := costParamsAt(t, env.server, "range=7d&tz="+url.QueryEscape("Europe/Berlin"), now)
	if p.q.Loc.String() != "Europe/Berlin" {
		t.Fatalf("loc = %v", p.q.Loc)
	}
	// 2026-10-23 00:00 CEST to 2026-10-30 00:00 CET.
	if want := time.Date(2026, 10, 22, 22, 0, 0, 0, time.UTC); !p.q.From.Equal(want) {
		t.Errorf("from = %v, want %v", p.q.From.UTC(), want)
	}
	if want := time.Date(2026, 10, 29, 23, 0, 0, 0, time.UTC); !p.q.To.Equal(want) {
		t.Errorf("to = %v, want %v", p.q.To.UTC(), want)
	}
	if b := bucketStarts(p.first, p.last, p.bucket); len(b) != 7 || b[0] != "2026-10-23" || b[6] != "2026-10-29" {
		t.Errorf("buckets = %v", b)
	}
}

func TestFoldSeries(t *testing.T) {
	var points []store.CostPoint
	for _, bucket := range []string{"2026-11-01", "2026-11-02"} {
		for i := range 25 {
			tot := store.SpendTotals{CostUSD: store.USD(float64(i + 1)), Calls: 1}
			if i == 24 {
				// The dearest by calls but unknown: it ranks last.
				tot = store.SpendTotals{UnknownCalls: 5, Calls: 5}
			}
			points = append(points, store.CostPoint{Bucket: bucket, Key: fmt.Sprintf("k%02d", i), Totals: tot})
		}
	}
	got := foldSeries(points, maxSeriesKeys)
	if len(got) != 2*21 {
		t.Fatalf("%d points", len(got))
	}
	for i, bucket := range []string{"2026-11-01", "2026-11-02"} {
		part := got[i*21 : (i+1)*21]
		other := part[20]
		// k04 to k23 are kept; k00 to k03 ($1 to $4) and k24 (unknown) fold.
		if part[0].Key != "k04" || part[19].Key != "k23" || part[0].Bucket != bucket || part[0].Other {
			t.Errorf("%s kept = %+v ... %+v", bucket, part[0], part[19])
		}
		if !other.Other || other.Key != "" || other.Bucket != bucket || other.Totals.CostUSD != store.USD(10) ||
			other.Totals.UnknownCalls != 5 || other.Totals.Calls != 9 {
			t.Errorf("%s other = %+v", bucket, other)
		}
	}
	// Few keys stay as they are.
	if got := foldSeries(points[:3], maxSeriesKeys); len(got) != 3 || slices.ContainsFunc(got, func(p CostPointView) bool { return p.Other }) {
		t.Errorf("few keys = %+v", got)
	}
}

func TestCostsTop(t *testing.T) {
	env, fake := costsEnv(t, false, "devs")
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	fake.top = store.CostTop{
		Milestones: []store.CostTopRow{{Namespace: "team-a", Project: "shop", MilestoneIID: 14, Label: "%14", At: at,
			Totals: store.SpendTotals{CostUSD: store.USD(5), Calls: 2}}},
		Runs: []store.CostTopRow{{Namespace: "team-a", Project: "shop", MilestoneIID: 14, IssueIID: 281, Task: "implement-281", Run: 3,
			Kind: "implement", Label: "implement-281 run 3", At: at, Totals: store.SpendTotals{CostUSD: store.USD(2), Calls: 1}}},
	}
	var v CostsTopView
	if code := env.get(t, "/api/v1/costs/top?group=kind&kind=implement", &v); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	// Only the viewer's project reaches the store, research is excluded, and
	// the lists have ten rows.
	if len(fake.topQ) != 1 || fake.topN[0] != 10 {
		t.Fatalf("queries %d, n %v", len(fake.topQ), fake.topN)
	}
	if q := fake.topQ[0]; !slices.Equal(q.Projects, shopKey) || !slices.Equal(q.ExcludeKinds, []string{"research"}) || q.Kind != "implement" {
		t.Errorf("query = %+v", q)
	}
	if len(v.Milestones) != 1 || v.Milestones[0].MilestoneIID != 14 || v.Milestones[0].Project != "shop" || len(v.Runs) != 1 ||
		v.Runs[0].Run != 3 || v.Runs[0].Task != "implement-281" || v.Issues == nil || len(v.Issues) != 0 {
		t.Errorf("view = %+v", v)
	}
	// Zero IIDs, task, run and kind are left out of the JSON.
	code, raw := env.getJSON(t, "/api/v1/costs/top")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	row := raw["milestones"].([]any)[0].(map[string]any)
	for _, k := range []string{"issueIID", "task", "run", "kind"} {
		if _, ok := row[k]; ok {
			t.Errorf("milestone row has %s: %v", k, row)
		}
	}
	if code := env.get(t, "/api/v1/costs/top?project=team-b/billing", nil); code != http.StatusNotFound {
		t.Errorf("another project: status %d", code)
	}
}

func TestCostsTopResearchAccess(t *testing.T) {
	env, fake := costsEnv(t, false, "devs", "readers")
	if code := env.get(t, "/api/v1/costs/top", nil); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if q := fake.topQ[0]; len(q.ExcludeKinds) != 0 {
		t.Errorf("research reader excludes %v", q.ExcludeKinds)
	}
}

func TestCostsTopErrors(t *testing.T) {
	env, fake := costsEnv(t, false, "devs")
	for _, q := range []string{"range=week", "from=2026-01-01", "tz=Mars/Base", "group=role", "kind=unknown", "engine=gpt"} {
		if code := env.get(t, "/api/v1/costs/top?"+q, nil); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, code)
		}
	}
	if len(fake.topQ) != 0 {
		t.Errorf("bad requests reached the store: %+v", fake.topQ)
	}
	none, _ := costsEnv(t, true, "devs")
	if code := none.get(t, "/api/v1/costs/top", nil); code != http.StatusServiceUnavailable {
		t.Errorf("without the database: status %d", code)
	}
	off := milestonesEnv(t)
	off.server.costReads = &fakeCosts{}
	if code := off.get(t, "/api/v1/costs/top", nil); code != http.StatusNotFound {
		t.Errorf("research off: status %d", code)
	}
}

func TestPlanUsage(t *testing.T) {
	at := func(h int) metav1.Time { return metav1.NewTime(time.Date(2026, 11, 12, h, 0, 0, 0, time.UTC)) }
	proj := func(ns, name string, usage ...aiv1.EngineUsage) *aiv1.AgentProject {
		ap := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
		ap.Status.EngineUsage = usage
		return ap
	}
	visible := map[string]*aiv1.AgentProject{
		"team-a/shop": proj("team-a", "shop",
			aiv1.EngineUsage{Engine: aiv1.EngineClaude, Status: "allowed", ObservedAt: at(9)},
			aiv1.EngineUsage{Engine: aiv1.EngineCursor, Status: "allowed", ObservedAt: at(11)}),
		"team-a/web": proj("team-a", "web",
			aiv1.EngineUsage{Engine: aiv1.EngineClaude, Status: "allowed_warning", ObservedAt: at(10),
				Windows: []aiv1.UsageWindow{{Name: "five_hour", Percent: 81}}}),
		"team-a/idle": proj("team-a", "idle"),
	}
	got := planUsage(visible)
	if len(got) != 2 {
		t.Fatalf("planUsage = %+v", got)
	}
	// Sorted by engine; the freshest reading wins across projects.
	if got[0].Usage.Engine != aiv1.EngineClaude || got[0].Project != "web" || got[0].Usage.Windows[0].Percent != 81 {
		t.Errorf("claude = %+v", got[0])
	}
	if got[1].Usage.Engine != aiv1.EngineCursor || got[1].Project != "shop" {
		t.Errorf("cursor = %+v", got[1])
	}
	// A project the viewer cannot see is not in the map: its newer reading
	// never contributes.
	delete(visible, "team-a/web")
	if got := planUsage(visible); got[0].Project != "shop" {
		t.Errorf("without web: claude from %s", got[0].Project)
	}
	if got := planUsage(nil); got == nil || len(got) != 0 {
		t.Errorf("no projects = %#v, want empty", got)
	}
}

func TestBudgetRows(t *testing.T) {
	visible := map[string]*aiv1.AgentProject{"team-a/shop": {ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shop"}}}
	milestone := func(ns, name string, iid int64, phase aiv1.AutopilotPhase, budget *aiv1.BudgetStatus, items ...aiv1.AutopilotItem) aiv1.AgentTask {
		t := aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: fmt.Sprintf("%s-m%d", name, iid)}}
		t.Spec.Type, t.Spec.ProjectRef = aiv1.TaskTypeSpec, name
		t.Spec.Milestone = &aiv1.MilestoneRef{IID: iid, Title: fmt.Sprintf("M%d", iid)}
		t.Status.Autopilot = &aiv1.AutopilotStatus{Phase: phase, Budget: budget, Items: items}
		return t
	}
	impl := aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shop-7"}}
	impl.Spec.Type, impl.Spec.ProjectRef = aiv1.TaskTypeImplement, "shop"
	impl.Spec.Issue = &aiv1.IssueRef{IID: 7, Title: "Checkout"}
	tasks := []aiv1.AgentTask{
		impl,
		milestone("team-a", "shop", 1, aiv1.AutopilotRunning, &aiv1.BudgetStatus{LimitUSD: "100", SpentUSD: "20"},
			aiv1.AutopilotItem{IID: 7, Budget: &aiv1.BudgetStatus{LimitUSD: "10", SpentUSD: "9"}},
			aiv1.AutopilotItem{IID: 8, Budget: &aiv1.BudgetStatus{LimitUSD: "10"}},
			aiv1.AutopilotItem{IID: 9}),
		milestone("team-a", "shop", 2, aiv1.AutopilotPaused, &aiv1.BudgetStatus{LimitUSD: "50", SpentUSD: "0"}),
		// Done: absent, as is a budget without a limit and another tenant's.
		milestone("team-a", "shop", 3, aiv1.AutopilotDone, &aiv1.BudgetStatus{LimitUSD: "5", SpentUSD: "5"}),
		milestone("team-a", "shop", 4, aiv1.AutopilotRunning, &aiv1.BudgetStatus{SpentUSD: "5"}),
		milestone("team-b", "billing", 1, aiv1.AutopilotRunning, &aiv1.BudgetStatus{LimitUSD: "1", SpentUSD: "1"}),
	}
	got := budgetRows(visible, tasks)
	type row struct {
		m, i         int64
		title, spent string
		share        float64
	}
	var rows []row
	for _, r := range got {
		if r.Namespace != "team-a" || r.Project != "shop" {
			t.Errorf("row of %s/%s", r.Namespace, r.Project)
		}
		rows = append(rows, row{r.MilestoneIID, r.IssueIID, r.Title, r.Spent.String(), r.Share})
	}
	want := []row{{1, 7, "Checkout", "9.00", 0.9}, {1, 0, "M1", "20.00", 0.2}, {2, 0, "M2", "0.00", 0}, {1, 8, "", "", 0}}
	if !slices.Equal(rows, want) {
		t.Errorf("budgets = %+v, want %+v", rows, want)
	}
	if got[3].Spent.Known || got[0].Limit.String() != "10.00" {
		t.Errorf("unknown spend = %+v, limit %s", got[3].Spent, got[0].Limit)
	}
	if got := budgetRows(nil, nil); got == nil {
		t.Error("no budgets = nil, want empty")
	}
}
