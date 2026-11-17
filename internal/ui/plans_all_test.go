package ui

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

var shopProject = store.ProjectKey{Namespace: "team-a", Name: "shop"}

// addRunningM5 gives shop a spec task of %5 (shop-plan-old's milestone) on
// autopilot with 2 of 4 issues merged.
func addRunningM5(t *testing.T, env *testEnv) {
	t.Helper()
	m5 := specTask("team-a", "shop", 5, 0, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: []aiv1.AutopilotItem{
		{IID: 1, State: aiv1.AutopilotItemMerged}, {IID: 2, State: aiv1.AutopilotItemSkipped}, {IID: 3, State: aiv1.AutopilotItemActive}}})
	m5.Status.Plan = []aiv1.PlannedIssue{{Key: "a", IID: 1}, {Key: "b", IID: 2}, {Key: "c", IID: 3}, {Key: "d", IID: 9}}
	if err := env.reader.Create(context.Background(), m5); err != nil {
		t.Fatal(err)
	}
}

func TestMergedMilestoneRows(t *testing.T) {
	env := milestonesEnv(t)
	env.server.milestoneRows = &fakeMilestones{rows: []store.MilestoneRow{
		{Project: shopProject, IID: 1, Title: "First", State: "closed", Phase: "done", DoneAt: t0, Issues: 3, Merged: 3},
	}}
	var projects aiv1.AgentProjectList
	var tasks aiv1.AgentTaskList
	if err := env.reader.List(context.Background(), &projects); err != nil {
		t.Fatal(err)
	}
	if err := env.reader.List(context.Background(), &tasks); err != nil {
		t.Fatal(err)
	}
	visible := map[string]*aiv1.AgentProject{}
	for i := range projects.Items {
		if ap := &projects.Items[i]; ap.Name == "shop" {
			visible["team-a/shop"] = ap
		}
	}
	rows, history := env.server.mergedMilestoneRows(context.Background(), visible, tasks.Items, time.Now())
	if !history {
		t.Error("history = false")
	}
	// Every state, unsorted: the live %3–%7 and the stored %1.
	iids := rowIIDs(rows)
	slices.Sort(iids)
	if !slices.Equal(iids, []int64{1, 3, 4, 5, 6, 7}) {
		t.Fatalf("rows = %v", iids)
	}
	if m4 := rowByIID(rows, 4); m4.Stored || m4.State != MilestoneStateRunning || m4.Progress != (MilestoneProgress{Merged: 2, Total: 4, Open: 2}) {
		t.Errorf("live row = %+v", m4)
	}
	if m1 := rowByIID(rows, 1); !m1.Stored || m1.State != MilestoneStateDone || m1.Progress != (MilestoneProgress{Merged: 3, Total: 3}) {
		t.Errorf("stored row = %+v", m1)
	}
}

// R6: a plan item carries its created milestone's row.
func TestPlanMilestoneState(t *testing.T) {
	t.Run("running", func(t *testing.T) {
		env := planEnv(t, true, "devs")
		addRunningM5(t, env)
		var got PlanView
		if code := env.get(t, shopPlans+"/shop-plan-old", &got); code != http.StatusOK {
			t.Fatalf("get: %d", code)
		}
		want := PlanMilestone{State: MilestoneStateRunning, Progress: MilestoneProgress{Merged: 2, Total: 4, Open: 2}}
		if got.Milestone == nil || *got.Milestone != want {
			t.Errorf("milestone = %+v, want %+v", got.Milestone, want)
		}
		var list []PlanListItem
		env.get(t, shopPlans, &list)
		if len(list) != 2 || list[1].Milestone == nil || *list[1].Milestone != want || list[0].Milestone != nil {
			t.Errorf("project list = %+v", list)
		}
	})
	t.Run("stored and done", func(t *testing.T) {
		env := planEnv(t, true, "devs")
		env.server.milestoneRows = &fakeMilestones{rows: []store.MilestoneRow{
			{Project: shopProject, IID: 5, State: "closed", Phase: "done", DoneAt: t0, Issues: 4, Merged: 4}}}
		var got PlanView
		env.get(t, shopPlans+"/shop-plan-old", &got)
		want := PlanMilestone{State: MilestoneStateDone, Progress: MilestoneProgress{Merged: 4, Total: 4}, Stored: true}
		if got.Milestone == nil || *got.Milestone != want {
			t.Errorf("milestone = %+v, want %+v", got.Milestone, want)
		}
	})
	t.Run("no row", func(t *testing.T) {
		env := planEnv(t, true, "devs")
		var got map[string]any
		env.get(t, shopPlans+"/shop-plan-old", &got)
		if _, ok := got["milestone"]; ok || got["createdMilestoneIID"] != float64(5) {
			t.Errorf("plan without a row: %v", got)
		}
	})
	t.Run("rows read only for a created milestone", func(t *testing.T) {
		env := planEnv(t, true, "devs")
		fake := &fakeMilestones{}
		env.server.milestoneRows = fake
		env.get(t, shopPlans+"/shop-plan-new", nil)
		if len(fake.got) != 0 {
			t.Errorf("snapshot read for a plan without a milestone: %+v", fake.got)
		}
		env.get(t, shopPlans+"/shop-plan-old", nil)
		if len(fake.got) != 1 || !slices.Equal(fake.got[0].Projects, []store.ProjectKey{shopProject}) {
			t.Errorf("snapshot queries = %+v", fake.got)
		}
	})
}

// allPlansEnv is planEnv with a second visible project, team-a/blog, and its
// plan blog-plan, last active between shop's two plans.
func allPlansEnv(t *testing.T, chat bool, roles ...string) *testEnv {
	t.Helper()
	env := planEnv(t, chat, roles...)
	blog := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "blog"},
		Spec: aiv1.AgentProjectSpec{GitLab: aiv1.GitLabSpec{URL: "https://gitlab.example.com", Project: "acme/blog"}}}
	plan := &aiv1.AgentPlan{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "blog-plan",
		CreationTimestamp: metav1.NewTime(time.Date(2026, 11, 10, 11, 0, 0, 0, time.UTC))},
		Spec: aiv1.AgentPlanSpec{ProjectRef: "blog", CreatedBy: "bea"}, Status: aiv1.AgentPlanStatus{Phase: aiv1.AgentPlanIdle}}
	for _, o := range []client.Object{blog, plan} {
		if err := env.reader.Create(context.Background(), o); err != nil {
			t.Fatal(err)
		}
	}
	return env
}

func planNames(items []PlanListItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Namespace+"/"+it.Project+"/"+it.Name)
	}
	return out
}

func TestAllPlans(t *testing.T) {
	env := allPlansEnv(t, true, "leads")
	// ada steers blog, not shop: shop's spec task names another owner.
	setOwners(t, env, "bea")
	addRunningM5(t, env)

	var got PlanOverview
	if code := env.get(t, "/api/v1/plans", &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	// Last active first (old 12:00, blog 11:00, new 10:00); team-b's
	// billing-plan is not visible.
	if names := planNames(got.Plans); !slices.Equal(names, []string{"team-a/shop/shop-plan-old", "team-a/blog/blog-plan", "team-a/shop/shop-plan-new"}) {
		t.Errorf("plans = %v", names)
	}
	wantProjects := []PlanProject{{Namespace: "team-a", Name: "blog", Steer: true}, {Namespace: "team-a", Name: "shop"}}
	if !slices.Equal(got.Projects, wantProjects) {
		t.Errorf("projects = %+v", got.Projects)
	}
	for _, it := range got.Plans {
		if it.Permissions.Steer != (it.Project == "blog") {
			t.Errorf("%s: steer %v", it.Name, it.Permissions.Steer)
		}
	}
	want := PlanMilestone{State: MilestoneStateRunning, Progress: MilestoneProgress{Merged: 2, Total: 4, Open: 2}}
	if m := got.Plans[0].Milestone; m == nil || *m != want {
		t.Errorf("milestone = %+v, want %+v", m, want)
	}

	got = PlanOverview{}
	if code := env.get(t, "/api/v1/plans?project=team-a/blog", &got); code != http.StatusOK {
		t.Fatalf("project filter: %d", code)
	}
	if names := planNames(got.Plans); !slices.Equal(names, []string{"team-a/blog/blog-plan"}) ||
		!slices.Equal(got.Projects, wantProjects[:1]) {
		t.Errorf("blog only: %v %+v", names, got.Projects)
	}

	for _, ref := range []string{"team-b/billing", "team-a/nope", "shop"} {
		if code := env.get(t, "/api/v1/plans?project="+ref, nil); code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", ref, code)
		}
	}
}

func TestAllPlansEmpty(t *testing.T) {
	env := allPlansEnv(t, true, "leads")
	var list aiv1.AgentPlanList
	if err := env.reader.List(context.Background(), &list); err != nil {
		t.Fatal(err)
	}
	for i := range list.Items {
		if err := env.reader.Delete(context.Background(), &list.Items[i]); err != nil {
			t.Fatal(err)
		}
	}
	var got map[string]any
	env.get(t, "/api/v1/plans", &got)
	plans, ok := got["plans"].([]any)
	if !ok || len(plans) != 0 || len(got["projects"].([]any)) != 2 {
		t.Errorf("no plans: %v", got)
	}
}

func TestAllPlansNeedChat(t *testing.T) {
	env := allPlansEnv(t, false, "leads")
	if code := env.get(t, "/api/v1/plans", nil); code != http.StatusNotFound {
		t.Errorf("without chat: %d, want 404", code)
	}
}
