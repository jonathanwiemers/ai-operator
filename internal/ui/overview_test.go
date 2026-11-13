package ui

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// fakeSpendSince answers today's totals for a since within the last day,
// the week's otherwise, or err; it records the queries.
type fakeSpendSince struct {
	day, week store.SpendTotals
	err       error
	projects  [][]store.ProjectKey
	since     []time.Time
}

func (f *fakeSpendSince) SpendSince(_ context.Context, projects []store.ProjectKey, since time.Time) (store.SpendTotals, error) {
	f.projects, f.since = append(f.projects, projects), append(f.since, since)
	if time.Since(since) <= 24*time.Hour {
		return f.day, f.err
	}
	return f.week, f.err
}

// overviewEnv is milestonesEnv plus, in team-a/shop, a second running
// implement task (#43 of %4, running since an hour) and two queued ones
// (#45 of %6, #44 of %4: #44 is next, its milestone is older), and an idle
// project team-a/cart with a usage limit and a running autopilot %2 whose
// next pending issue is #12, and a project team-a/empty with nothing.
func overviewEnv(t *testing.T) (*testEnv, time.Time) {
	t.Helper()
	env := milestonesEnv(t)
	since := time.Now().Add(-time.Hour).Truncate(time.Second)
	impl := func(ns, project string, ms, issue int64, phase aiv1.TaskPhase) *aiv1.AgentTask {
		return &aiv1.AgentTask{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: project + "-" + string(rune('0'+issue%10)) + "-i"},
			Spec: aiv1.AgentTaskSpec{ProjectRef: project, Type: aiv1.TaskTypeImplement, RunRequest: 1,
				Milestone: &aiv1.MilestoneRef{IID: ms}, Issue: &aiv1.IssueRef{IID: issue, Title: "Issue " + string(rune('0'+issue%10))}},
			Status: aiv1.AgentTaskStatus{Phase: phase},
		}
	}
	running := impl("team-a", "shop", 4, 43, aiv1.TaskPhaseRunning)
	running.Status.Run.LastActivityTime = &metav1.Time{Time: since}
	limit := []aiv1.UsageLimit{{Engine: "claude", Until: metav1.NewTime(since.Add(3 * time.Hour)), Message: "5-hour limit"}}
	cart := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "cart"},
		Spec:   aiv1.AgentProjectSpec{GitLab: aiv1.GitLabSpec{URL: "https://gitlab.example.com", Project: "acme/cart"}},
		Status: aiv1.AgentProjectStatus{UsageLimits: limit}}
	empty := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "empty"}}
	m2 := specTask("team-a", "cart", 2, 0, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: []aiv1.AutopilotItem{
		{IID: 11, State: aiv1.AutopilotItemMerged}, {IID: 12, State: aiv1.AutopilotItemPending}, {IID: 13, State: aiv1.AutopilotItemPending}}})
	m2.Status.Plan = []aiv1.PlannedIssue{{Key: "a", IID: 11}, {Key: "b", IID: 12, Title: "Coupons"}}
	for _, o := range []client.Object{running, impl("team-a", "shop", 6, 45, aiv1.TaskPhaseQueued),
		impl("team-a", "shop", 4, 44, aiv1.TaskPhaseQueued), cart, empty, m2} {
		if err := env.reader.Create(context.Background(), o); err != nil {
			t.Fatal(err)
		}
	}
	return env, since
}

func TestOverviewWithoutDatabase(t *testing.T) {
	env, since := overviewEnv(t)
	var o OverviewView
	if code := env.get(t, "/api/v1/overview", &o); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	// shop %4 and cart %2 run, %5 is paused, %6 waits; billing's %8 is not
	// visible.
	if o.Running != 2 || o.Paused != 1 || o.Waiting != 1 {
		t.Errorf("strip = %+v", o)
	}
	var inbox struct{ Items []InboxItem }
	env.get(t, "/api/v1/inbox", &inbox)
	if o.NeedsYou == 0 || o.NeedsYou != len(inbox.Items) {
		t.Errorf("needsYou = %d, inbox has %d", o.NeedsYou, len(inbox.Items))
	}
	if o.SpendToday != nil || o.Spend7d != nil || o.History {
		t.Errorf("spend without a database = %v, %v, history %v", o.SpendToday, o.Spend7d, o.History)
	}

	// cart: idle, its next issue and limit; shop: #43 since an hour, then
	// testObjects' #42 without a start; both with shop's next, #44. empty
	// has no work and stays out.
	next := &WorkingNext{IssueIID: 44, Title: "Issue 4", MilestoneIID: 4}
	type entry struct {
		project string
		issue   int64
		task    string
		next    WorkingNext
	}
	var got []entry
	for _, w := range o.Working {
		e := entry{w.Project, w.IssueIID, w.Task, WorkingNext{}}
		if w.Next != nil {
			e.next = *w.Next
		}
		got = append(got, e)
	}
	want := []entry{
		{"cart", 0, "", WorkingNext{IssueIID: 12, Title: "Coupons", MilestoneIID: 2}},
		{"shop", 43, "shop-3-i", *next},
		{"shop", 42, "shop-42-add-paypal", *next},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("working:\n got %+v\nwant %+v", got, want)
	}
	if c := o.Working[0]; len(c.UsageLimits) != 1 || c.UsageLimits[0].Message != "5-hour limit" || c.Since != nil || c.Title != "" {
		t.Errorf("idle cart = %+v", c)
	}
	if w := o.Working[1]; w.Since == nil || !w.Since.Equal(since) || w.MilestoneIID != 4 || w.Title != "Issue 3" || w.Namespace != "team-a" {
		t.Errorf("running #43 = %+v", w)
	}
}

func TestOverviewWithSpend(t *testing.T) {
	env, _ := overviewEnv(t)
	fake := &fakeSpendSince{day: store.SpendTotals{CostUSD: store.USD(1.5), InputTokens: 100, Calls: 2},
		week: store.SpendTotals{CostUSD: store.USD(12), InputTokens: 900, OutputTokens: 100, Calls: 9}}
	env.server.overviewSpend = fake
	var o OverviewView
	if code := env.get(t, "/api/v1/overview", &o); code != http.StatusOK || !o.History {
		t.Fatalf("status %d, history %v", code, o.History)
	}
	if d := o.SpendToday; d == nil || d.CostUSD != "1.50" || !d.CostKnown || d.Tokens != 100 || d.CostFrom != CostFromCalls || d.Spend != nil {
		t.Errorf("spendToday = %+v", d)
	}
	if w := o.Spend7d; w == nil || w.CostUSD != "12.00" || w.Tokens != 1000 {
		t.Errorf("spend7d = %+v", w)
	}
	// The visible projects only; today from UTC midnight, the week the 6
	// days before it too.
	if len(fake.projects) != 2 {
		t.Fatalf("%d queries", len(fake.projects))
	}
	keys := slices.SortedFunc(slices.Values(fake.projects[0]), func(a, b store.ProjectKey) int {
		return compareKeys(a, b)
	})
	if !slices.Equal(keys, []store.ProjectKey{{Namespace: "team-a", Name: "cart"}, {Namespace: "team-a", Name: "empty"},
		{Namespace: "team-a", Name: "shop"}}) {
		t.Errorf("projects = %v", keys)
	}
	y, m, d := time.Now().UTC().Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	if !fake.since[0].Equal(today) || !fake.since[1].Equal(today.AddDate(0, 0, -6)) {
		t.Errorf("since = %v", fake.since)
	}

	// A failing store: unknown spend, no history.
	fake.err = errors.New("connection refused")
	o = OverviewView{}
	if code := env.get(t, "/api/v1/overview", &o); code != http.StatusOK || o.History || o.SpendToday != nil || o.Spend7d != nil {
		t.Errorf("failing store: status %d, %+v", code, o)
	}
	if o.Running != 2 || len(o.Working) != 3 {
		t.Errorf("failing store keeps the live parts: %+v", o)
	}
}

func compareKeys(a, b store.ProjectKey) int {
	return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
}

func TestOverviewFinishing(t *testing.T) {
	env, _ := overviewEnv(t)
	// done-only: one done autopilot milestone held open, nothing else running.
	doneOnly := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "done-only"},
		Spec: aiv1.AgentProjectSpec{GitLab: aiv1.GitLabSpec{URL: "https://gitlab.example.com", Project: "acme/done-only"}}}
	m9 := specTask("team-a", "done-only", 9, 0, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotDone, CloseWaitsFor: []int64{50}})
	m9.Spec.Autopilot = true
	// cart already has a running autopilot; add a finishing milestone too.
	m3 := specTask("team-a", "cart", 3, 0, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotDone, CloseWaitsFor: []int64{99}})
	m3.Spec.Autopilot = true
	for _, o := range []client.Object{doneOnly, m9, m3} {
		if err := env.reader.Create(context.Background(), o); err != nil {
			t.Fatal(err)
		}
	}
	var o OverviewView
	if code := env.get(t, "/api/v1/overview", &o); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	byProject := map[string][]WorkingNow{}
	for _, w := range o.Working {
		byProject[w.Project] = append(byProject[w.Project], w)
	}
	if got := byProject["done-only"]; len(got) != 1 || got[0].Finishing != 1 || got[0].IssueIID != 0 || got[0].Next != nil {
		t.Errorf("finishing-only card = %+v", got)
	}
	if got := byProject["cart"]; len(got) != 1 || got[0].Finishing != 1 || got[0].Next == nil {
		t.Errorf("finishing with other work = %+v", got)
	}
	for _, w := range byProject["shop"] {
		if w.Finishing != 0 {
			t.Errorf("shop without Spec.Autopilot done = finishing %d on %+v", w.Finishing, w)
		}
	}
	if _, ok := byProject["empty"]; ok {
		t.Errorf("empty with finishing 0 must stay out: %+v", byProject["empty"])
	}
}
