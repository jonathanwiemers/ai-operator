package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func agentPlan(name, project, set string) *aiv1.AgentPlan {
	p := &aiv1.AgentPlan{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agents"},
		Spec:       aiv1.AgentPlanSpec{ProjectRef: project, CreatedBy: "jo"},
	}
	if set != "" {
		p.Labels = map[string]string{aiv1.LabelProjectSet: set}
	}
	return p
}

// Plans are work: they bring up a dormant entry's child and keep a settling
// child active, but not when another AgentProject holds the name.
func TestProjectSetPlanPlansCountAsWork(t *testing.T) {
	set := planSet()
	settling := agentProject("agents", "shop", "jo.wiemers/shop", set)
	settling.Status.SettlingMilestones = 1
	cases := []struct {
		name       string
		path       string
		projects   []aiv1.AgentProject
		ready      bool
		plans      map[string]int
		wantState  aiv1.ProjectSetState
		wantChild  string
		wantReason string
	}{
		{"dormant entry with a plan", "jo.wiemers/tools/foo", nil, false, map[string]int{"tools-foo": 1},
			aiv1.ProjectSetActive, "tools-foo", "kept: 1 plan"},
		{"dormant entry without a plan", "jo.wiemers/tools/foo", nil, false, map[string]int{"other": 1},
			aiv1.ProjectSetDormant, "", "no milestone with the spec marker, ready issue or task"},
		{"settling child with plans", "jo.wiemers/shop", []aiv1.AgentProject{settling}, false, map[string]int{"shop": 2},
			aiv1.ProjectSetActive, "shop", "kept: 2 plans"},
		{"plan next to a ready issue", "jo.wiemers/shop", []aiv1.AgentProject{settling}, true, map[string]int{"shop": 1},
			aiv1.ProjectSetActive, "shop", ""},
		{"plans of a project outside the set", "jo.wiemers/shop", []aiv1.AgentProject{agentProject("agents", "shop", "other/shop", nil)},
			false, map[string]int{"shop": 1}, aiv1.ProjectSetDormant, "", "no milestone with the spec marker, ready issue or task"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := set.DeepCopy()
			s.Status.Projects = []aiv1.ProjectSetEntry{{Path: tc.path, State: aiv1.ProjectSetDormant, LastActiveTime: ago(500 * time.Hour)}}
			var ready []gitlab.DiscoveredIssue
			if tc.ready {
				ready = []gitlab.DiscoveredIssue{{Project: tc.path, IID: 7}}
			}
			plan := planProjectSet(projectSetInput{
				Set: s, Projects: tc.projects, PlanCounts: tc.plans, Now: planNow,
				Discovery: &gitlab.GroupDiscovery{
					Projects:    []gitlab.DiscoveredProject{{FullPath: tc.path}},
					ReadyIssues: ready,
				},
			})
			e := plan.Entries[0]
			if e.State != tc.wantState || e.Child != tc.wantChild || e.Reason != tc.wantReason {
				t.Errorf("entry = %s/%q %q, want %s/%q %q", e.State, e.Child, e.Reason, tc.wantState, tc.wantChild, tc.wantReason)
			}
			if active := tc.wantState == aiv1.ProjectSetActive; active != e.LastActiveTime.Equal(ago(0)) {
				t.Errorf("last active %v", e.LastActiveTime)
			}
			if planned := len(plan.Children) == 1 && plan.Children[0].Name == tc.wantChild; planned != (tc.wantChild != "") {
				t.Errorf("children = %+v", plan.Children)
			}
		})
	}
}

// After its last plan is gone, an idle child goes dormant dormantAfter after
// the plan last kept it active.
func TestProjectSetPlanDeletedPlanGoesDormant(t *testing.T) {
	set := planSet()
	child := agentProject("agents", "tools-foo", "jo.wiemers/tools/foo", set)
	in := projectSetInput{
		Set: set, Projects: []aiv1.AgentProject{child}, PlanCounts: map[string]int{"tools-foo": 1}, Now: planNow,
		Discovery: &gitlab.GroupDiscovery{Projects: []gitlab.DiscoveredProject{{FullPath: "jo.wiemers/tools/foo"}}},
	}
	set.Status.Projects = planProjectSet(in).Entries
	in.PlanCounts = nil
	for _, tc := range []struct {
		after time.Duration
		want  aiv1.ProjectSetState
	}{{time.Hour, aiv1.ProjectSetActive}, {167 * time.Hour, aiv1.ProjectSetActive}, {168 * time.Hour, aiv1.ProjectSetDormant}} {
		in.Now = planNow.Add(tc.after)
		e := planProjectSet(in).Entries[0]
		if e.State != tc.want || e.Child != "tools-foo" || strings.HasPrefix(e.Reason, "kept") {
			t.Errorf("after %s: entry = %+v, want %s", tc.after, e, tc.want)
		}
	}
}

func TestKeptReason(t *testing.T) {
	for _, tc := range []struct {
		tasks, plans int
		reason, want string
	}{
		{1, 0, "", "kept: 1 open task"},
		{3, 0, "no work for 168h", "kept: 3 open tasks; no work for 168h"},
		{0, 1, "", "kept: 1 plan"},
		{0, 2, "not matched by include", "kept: 2 plans; not matched by include"},
		{1, 2, "", "kept: 1 open task, 2 plans"},
		{2, 1, "no work for 168h", "kept: 2 open tasks, 1 plan; no work for 168h"},
	} {
		if got := keptReason(tc.tasks, tc.plans, tc.reason); got != tc.want {
			t.Errorf("keptReason(%d, %d, %q) = %q, want %q", tc.tasks, tc.plans, tc.reason, got, tc.want)
		}
	}
}

func TestCountPlansSkipsDeleting(t *testing.T) {
	gone := agentPlan("old", "shop", "")
	gone.DeletionTimestamp = ago(0)
	got := countPlans([]aiv1.AgentPlan{*agentPlan("a", "shop", ""), *agentPlan("b", "shop", ""), *gone, *agentPlan("c", "cart", "")})
	if len(got) != 2 || got["shop"] != 2 || got["cart"] != 1 {
		t.Errorf("counts = %v", got)
	}
}

// A child with a plan outlives dormantAfter; after the plan is deleted it
// goes dormant and is removed as before.
func TestProjectSetRemovalKeepsChildWithPlan(t *testing.T) {
	e := newSetEnv(t, nil)
	e.discover(activeProject("jo.wiemers/shop"))
	e.reconcile()
	plan := agentPlan("shop-plan", "shop", "")
	if err := e.c.Create(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	e.discover(gitlab.DiscoveredProject{FullPath: "jo.wiemers/shop"})
	e.now = e.now.Add(168 * time.Hour)
	e.reconcile()
	e.poll()
	if c := e.child("shop"); removing(c) || !c.Spec.Webhook.Enabled {
		t.Errorf("child with a plan touched: webhook %v, annotations %v", c.Spec.Webhook.Enabled, c.Annotations)
	}
	if en := setEntry(e.set(), "jo.wiemers/shop"); en.State != aiv1.ProjectSetActive || en.Child != "shop" || en.Reason != "kept: 1 plan" {
		t.Errorf("entry = %+v", en)
	}

	if err := e.c.Delete(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	e.poll()
	if c := e.child("shop"); removing(c) {
		t.Fatal("child switched off before dormantAfter")
	}
	e.now = e.now.Add(168 * time.Hour)
	e.reconcile()
	if c := e.child("shop"); !removing(c) {
		t.Fatalf("child not switched off: webhook %v, annotations %v", c.Spec.Webhook.Enabled, c.Annotations)
	}
	e.poll()
	if !e.gone("shop") {
		t.Fatal("child not deleted after its plan was gone")
	}
}

func TestProjectSetRemovalLivePlanCheck(t *testing.T) {
	e := dormantShop(t)
	e.hook("shop", 0, "", "")
	// The cache has no plan yet, the API server has.
	e.r.APIReader = fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(agentPlan("shop-plan", "shop", "")).Build()
	e.poll()
	if e.gone("shop") {
		t.Fatal("child deleted although the live read found a plan")
	}
	if got := setEntry(e.set(), "jo.wiemers/shop"); !strings.HasPrefix(got.Reason, "kept: 1 plan;") || got.Child != "shop" {
		t.Errorf("entry = %+v", got)
	}
}

// Creating a labelled plan wakes its set, which discovers at once and
// creates the dormant entry's child. Plan updates wake nothing.
func TestProjectSetPlanCreateWakesSet(t *testing.T) {
	e := newSetEnv(t, nil)
	e.discover(gitlab.DiscoveredProject{FullPath: "jo.wiemers/tools/foo"})
	e.reconcile()
	if len(e.children()) != 0 {
		t.Fatal("dormant entry has a child")
	}
	calls := e.disc.calls
	plan := agentPlan("foo-plan", "tools-foo", "jo")
	if err := e.c.Create(context.Background(), plan); err != nil {
		t.Fatal(err)
	}

	unlabelled := agentPlan("other", "tools-foo", "")
	if planCreatedInSet.Create(event.CreateEvent{Object: unlabelled}) || e.r.wakeForPlan(context.Background(), unlabelled) != nil {
		t.Error("a plan without the set label wakes")
	}
	if planCreatedInSet.Update(event.UpdateEvent{ObjectOld: plan, ObjectNew: plan}) ||
		planCreatedInSet.Delete(event.DeleteEvent{Object: plan}) || planCreatedInSet.Generic(event.GenericEvent{Object: plan}) {
		t.Error("a plan update, delete or generic event passes")
	}
	e.now = e.now.Add(time.Minute)
	e.reconcile()
	if e.disc.calls != calls {
		t.Fatal("the set discovered before its poll without a wake")
	}

	if !planCreatedInSet.Create(event.CreateEvent{Object: plan}) {
		t.Fatal("a labelled plan create is filtered out")
	}
	reqs := e.r.wakeForPlan(context.Background(), plan)
	if want := (types.NamespacedName{Namespace: "agents", Name: "jo"}); len(reqs) != 1 || reqs[0].NamespacedName != want {
		t.Fatalf("requests = %v, want %v", reqs, want)
	}
	e.reconcile()
	if e.disc.calls != calls+1 {
		t.Fatalf("discoveries %d after the wake, want 1", e.disc.calls-calls)
	}
	e.child("tools-foo")
	if en := setEntry(e.set(), "jo.wiemers/tools/foo"); en.State != aiv1.ProjectSetActive || en.Reason != "kept: 1 plan" {
		t.Errorf("entry = %+v", en)
	}
}
