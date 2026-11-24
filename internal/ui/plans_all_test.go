package ui

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

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
		_, list := shopPlanList(t, env)
		if old := list["shop-plan-old"]; len(list) != 2 || old.Milestone == nil || *old.Milestone != want || list["shop-plan-new"].Milestone != nil {
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
	wantProjects := []PlanProject{{Namespace: "team-a", Name: "blog", Path: "acme/blog", Steer: true}, {Namespace: "team-a", Name: "shop", Path: "acme/shop"}}
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

// setPlansEnv is allPlansEnv with the set acme in team-a (group acme): the
// active child web, the dormant entries tools/foo (child tools-foo, with the
// plan foo-plan), legacy (its name held by an unrelated AgentProject) and
// secret, an excluded and a nameTaken entry. The set hidden in team-b has a
// dormant entry. viewers see team-a, mixed sees team-a and steers tools-foo
// only, narrow sees the set but not secret, setless sees tools-foo but not
// the set.
func setPlansEnv(t *testing.T, roles ...string) *testEnv {
	t.Helper()
	cfg := withChat(oidcConfig(
		Rule{Roles: []string{"leads"}, Projects: []string{"team-a/*"}, Steer: true},
		Rule{Roles: []string{"viewers", "mixed"}, Projects: []string{"team-a/*"}},
		Rule{Roles: []string{"mixed"}, Projects: []string{"team-a/tools-foo"}, Steer: true},
		Rule{Roles: []string{"narrow"}, Projects: []string{"team-a/acme", "team-a/tools-foo", "team-a/shop"}, Steer: true},
		Rule{Roles: []string{"setless"}, Projects: []string{"team-a/tools-foo"}, Steer: true},
	))
	env := newTestEnv(t, cfg)
	addPlans(t, env)
	at := metav1.NewTime(time.Date(2026, 11, 10, 13, 0, 0, 0, time.UTC))
	objects := []client.Object{
		&aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "web", Labels: map[string]string{aiv1.LabelProjectSet: "acme"}},
			Spec: aiv1.AgentProjectSpec{GitLab: aiv1.GitLabSpec{URL: "https://gitlab.example.com", Project: "acme/web"}}},
		&aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "legacy"},
			Spec: aiv1.AgentProjectSpec{GitLab: aiv1.GitLabSpec{URL: "https://gitlab.example.com", Project: "other/legacy"}}},
		&aiv1.AgentPlan{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "foo-plan", CreationTimestamp: at,
			Labels: map[string]string{aiv1.LabelProjectSet: "acme"}},
			Spec: aiv1.AgentPlanSpec{ProjectRef: "tools-foo", CreatedBy: "ada@example.com"}},
		&aiv1.AgentProjectSet{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "acme"},
			Spec: aiv1.AgentProjectSetSpec{GitLab: aiv1.ProjectSetGitLab{Group: "acme"}},
			Status: aiv1.AgentProjectSetStatus{Projects: []aiv1.ProjectSetProjectStatus{
				{Path: "acme/web", State: aiv1.ProjectSetActive, Child: "web"},
				{Path: "acme/tools/foo", State: aiv1.ProjectSetDormant},
				{Path: "acme/legacy", State: aiv1.ProjectSetDormant},
				{Path: "acme/secret", State: aiv1.ProjectSetDormant},
				{Path: "acme/old", State: aiv1.ProjectSetExcluded, Reason: "archived"},
				{Path: "acme/taken", State: aiv1.ProjectSetNameTaken},
			}}},
		&aiv1.AgentProjectSet{ObjectMeta: metav1.ObjectMeta{Namespace: "team-b", Name: "hidden"},
			Spec:   aiv1.AgentProjectSetSpec{GitLab: aiv1.ProjectSetGitLab{Group: "beta"}},
			Status: aiv1.AgentProjectSetStatus{Projects: []aiv1.ProjectSetProjectStatus{{Path: "beta/api", State: aiv1.ProjectSetDormant}}}},
	}
	for _, o := range objects {
		if err := env.reader.Create(context.Background(), o); err != nil {
			t.Fatal(err)
		}
	}
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	grants := map[string]any{}
	for _, r := range roles {
		grants[r] = map[string]any{}
	}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": grants}
	env.login(t, "/")
	return env
}

var fooProject = PlanProject{Namespace: "team-a", Name: "tools-foo", Path: "acme/tools/foo", Set: "acme", Dormant: true}

func TestAllPlansSetProjects(t *testing.T) {
	if name := aiv1.ProjectSetChildName("acme", "acme/tools/foo"); name != "tools-foo" {
		t.Fatalf("child name %q", name)
	}
	steerFoo := fooProject
	steerFoo.Steer = true
	for _, tc := range []struct {
		role  string
		want  []PlanProject
		plans []string
	}{
		// The active child by its AgentProject; neither the excluded and
		// nameTaken entries, legacy (name held), nor team-b's set.
		{"leads", []PlanProject{
			{Namespace: "team-a", Name: "legacy", Path: "other/legacy", Steer: true},
			{Namespace: "team-a", Name: "secret", Path: "acme/secret", Set: "acme", Dormant: true, Steer: true},
			{Namespace: "team-a", Name: "shop", Path: "acme/shop", Steer: true},
			steerFoo,
			{Namespace: "team-a", Name: "web", Path: "acme/web", Set: "acme", Steer: true},
		}, []string{"team-a/tools-foo/foo-plan", "team-a/shop/shop-plan-old", "team-a/shop/shop-plan-new"}},
		{"viewers", []PlanProject{
			{Namespace: "team-a", Name: "legacy", Path: "other/legacy"},
			{Namespace: "team-a", Name: "secret", Path: "acme/secret", Set: "acme", Dormant: true},
			{Namespace: "team-a", Name: "shop", Path: "acme/shop"},
			fooProject,
			{Namespace: "team-a", Name: "web", Path: "acme/web", Set: "acme"},
		}, []string{"team-a/tools-foo/foo-plan", "team-a/shop/shop-plan-old", "team-a/shop/shop-plan-new"}},
		// Steer follows CanSteer on the child name.
		{"mixed", []PlanProject{
			{Namespace: "team-a", Name: "legacy", Path: "other/legacy"},
			{Namespace: "team-a", Name: "secret", Path: "acme/secret", Set: "acme", Dormant: true},
			{Namespace: "team-a", Name: "shop", Path: "acme/shop"},
			steerFoo,
			{Namespace: "team-a", Name: "web", Path: "acme/web", Set: "acme"},
		}, []string{"team-a/tools-foo/foo-plan", "team-a/shop/shop-plan-old", "team-a/shop/shop-plan-new"}},
		// secret's child name is not visible.
		{"narrow", []PlanProject{{Namespace: "team-a", Name: "shop", Path: "acme/shop", Steer: true}, steerFoo},
			[]string{"team-a/tools-foo/foo-plan", "team-a/shop/shop-plan-old", "team-a/shop/shop-plan-new"}},
		// The set is not visible, so neither are its entries.
		{"setless", []PlanProject{}, nil},
	} {
		t.Run(tc.role, func(t *testing.T) {
			env := setPlansEnv(t, tc.role)
			var got PlanOverview
			if code := env.get(t, "/api/v1/plans", &got); code != http.StatusOK {
				t.Fatalf("status %d", code)
			}
			if !slices.Equal(got.Projects, tc.want) {
				t.Errorf("projects = %+v", got.Projects)
			}
			if names := planNames(got.Plans); !slices.Equal(names, tc.plans) {
				t.Errorf("plans = %v", names)
			}
		})
	}
}

func TestAllPlansSetProjectFilter(t *testing.T) {
	env := setPlansEnv(t, "mixed")
	var got PlanOverview
	if code := env.get(t, "/api/v1/plans?project=team-a/tools-foo", &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	steerFoo := fooProject
	steerFoo.Steer = true
	if names := planNames(got.Plans); !slices.Equal(names, []string{"team-a/tools-foo/foo-plan"}) ||
		!slices.Equal(got.Projects, []PlanProject{steerFoo}) || !got.Plans[0].Permissions.Steer {
		t.Errorf("tools-foo only: %v %+v", names, got.Projects)
	}
	got = PlanOverview{}
	if code := env.get(t, "/api/v1/plans?project=team-a/web", &got); code != http.StatusOK ||
		!slices.Equal(got.Projects, []PlanProject{{Namespace: "team-a", Name: "web", Path: "acme/web", Set: "acme"}}) {
		t.Errorf("web: %d %+v", code, got.Projects)
	}
	for _, ref := range []string{"team-a/legacy-x", "team-a/old", "team-a/taken", "team-b/api"} {
		if code := env.get(t, "/api/v1/plans?project="+ref, nil); code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", ref, code)
		}
	}
}

func TestSetEntryProject(t *testing.T) {
	env := setPlansEnv(t, "viewers")
	p := Principal{Username: "ada@example.com", Roles: []string{"viewers"}}
	ctx := context.Background()
	if e, ok, err := env.server.setEntryProject(ctx, p, "team-a", "tools-foo"); err != nil || !ok ||
		e != (setEntryProject{Set: "acme", Namespace: "team-a", Name: "tools-foo", Path: "acme/tools/foo"}) {
		t.Errorf("tools-foo: %+v %v %v", e, ok, err)
	}
	for _, name := range []string{"web", "legacy", "old", "taken"} {
		if _, ok, err := env.server.setEntryProject(ctx, p, "team-a", name); err != nil || ok {
			t.Errorf("%s: offered (%v, %v)", name, ok, err)
		}
	}
}

func TestAllPlansWithoutSetCRD(t *testing.T) {
	env := allPlansEnv(t, true, "leads")
	env.server.reader = interceptor.NewClient(env.reader.(client.WithWatch), interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*aiv1.AgentProjectSetList); ok {
				return &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: aiv1.GroupVersion.Group, Kind: "AgentProjectSet"}}
			}
			return c.List(ctx, list, opts...)
		},
	})
	var got PlanOverview
	if code := env.get(t, "/api/v1/plans", &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	want := []PlanProject{{Namespace: "team-a", Name: "blog", Path: "acme/blog", Steer: true}, {Namespace: "team-a", Name: "shop", Path: "acme/shop", Steer: true}}
	if !slices.Equal(got.Projects, want) || len(got.Plans) != 3 {
		t.Errorf("projects = %+v, %d plans", got.Projects, len(got.Plans))
	}
	if code := env.get(t, "/api/v1/plans?project=team-a/nope", nil); code != http.StatusNotFound {
		t.Errorf("unknown project: %d", code)
	}
}
