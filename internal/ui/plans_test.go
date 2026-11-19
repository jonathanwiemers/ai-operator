package ui

import (
	"context"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

const shopPlans = "/api/v1/projects/team-a/shop/plans"

// planEnv logs in ada@example.com with roles on a chat-enabled UI whose
// shop project has two plans and billing one.
func planEnv(t *testing.T, chat bool, roles ...string) *testEnv {
	t.Helper()
	cfg := oidcConfig(
		Rule{Roles: []string{"leads"}, Projects: []string{"team-a/*"}, Steer: true},
		Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}},
		Rule{Roles: []string{"readers"}, Projects: []string{"team-a/*"}, Conversation: true},
	)
	if chat {
		withChat(cfg)
	}
	env := newTestEnv(t, cfg)
	addPlans(t, env)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	grants := map[string]any{}
	for _, r := range roles {
		grants[r] = map[string]any{}
	}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": grants}
	env.login(t, "/")
	return env
}

// addPlans gives shop the plans shop-plan-old (Created) and shop-plan-new
// (Idle), billing the plan billing-plan.
func addPlans(t *testing.T, env *testEnv) {
	t.Helper()
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	turn := metav1.NewTime(at.Add(3 * time.Hour))
	for _, pl := range []*aiv1.AgentPlan{
		{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shop-plan-old", CreationTimestamp: metav1.NewTime(at)},
			Spec: aiv1.AgentPlanSpec{ProjectRef: "shop", Title: "Refunds", CreatedBy: "ada@example.com", MaxBudgetUSD: "8"},
			Status: aiv1.AgentPlanStatus{Phase: aiv1.AgentPlanCreated, Draft: "# Refunds", DraftRevision: 4, DraftBy: "owner",
				SpendUSD: "1.2000", Tokens: 900, CreatedMilestoneIID: 5, CreatedMilestoneURL: "https://gitlab.example.com/m/5",
				Chat: &aiv1.ChatStatus{LastTurnTime: &turn}}},
		{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shop-plan-new", CreationTimestamp: metav1.NewTime(at.Add(time.Hour))},
			Spec: aiv1.AgentPlanSpec{ProjectRef: "shop", CreatedBy: "bea"}, Status: aiv1.AgentPlanStatus{Phase: aiv1.AgentPlanIdle}},
		{ObjectMeta: metav1.ObjectMeta{Namespace: "team-b", Name: "billing-plan"}, Spec: aiv1.AgentPlanSpec{ProjectRef: "billing", CreatedBy: "bea"}},
	} {
		if err := env.reader.Create(context.Background(), pl); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlansListAndGet(t *testing.T) {
	env := planEnv(t, true, "devs")
	var list []PlanListItem
	if code := env.get(t, shopPlans, &list); code != http.StatusOK || len(list) != 2 {
		t.Fatalf("list: %d %+v", code, list)
	}
	if list[0].Name != "shop-plan-new" || list[1].Name != "shop-plan-old" {
		t.Errorf("not newest first: %s, %s", list[0].Name, list[1].Name)
	}
	if l := list[0]; l.BudgetUSD != DefaultPlanMaxBudgetUSD || l.BudgetLimitUSD != DefaultPlanMaxBudgetUSDLimit || l.Permissions.Steer {
		t.Errorf("default budget, viewer without steer: %+v", l)
	}

	var got PlanView
	if code := env.get(t, shopPlans+"/shop-plan-old", &got); code != http.StatusOK {
		t.Fatalf("get: %d", code)
	}
	sameJSON(t, got, `{"name":"shop-plan-old","namespace":"team-a","project":"shop","title":"Refunds","phase":"Created","createdBy":"ada@example.com",
		"createdAt":"2026-11-10T09:00:00Z","lastActivityAt":"2026-11-10T12:00:00Z","spendUSD":"1.2000","tokens":900,
		"budgetUSD":"8","budgetLimitUSD":"20","createdMilestoneIID":5,"createdMilestoneURL":"https://gitlab.example.com/m/5",
		"permissions":{"steer":false},"draft":"# Refunds","draftRevision":4,"draftBy":"owner",
		"decisions":[],"decisionsPending":0}`)

	for _, path := range []string{shopPlans + "/billing-plan", shopPlans + "/nope", "/api/v1/projects/team-b/billing/plans",
		"/api/v1/projects/team-b/billing/plans/billing-plan"} {
		if code := env.get(t, path, nil); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, code)
		}
	}
}

func TestPlansNeedChat(t *testing.T) {
	env := planEnv(t, false, "leads")
	if code := env.get(t, shopPlans, nil); code != http.StatusNotFound {
		t.Errorf("without chat: %d, want 404", code)
	}
}

func TestPlanSteerPermission(t *testing.T) {
	cases := []struct {
		name   string
		role   string
		owners []string
		want   bool
	}{
		{"viewer", "devs", nil, false},
		{"steer, project without owner-subjects", "leads", nil, true},
		{"steer, among the owner-subjects", "leads", []string{"bea", "ada@example.com"}, true},
		{"steer, not an owner-subject", "leads", []string{"bea"}, false},
		{"owner-subject without steer", "devs", []string{"ada@example.com"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := planEnv(t, true, tc.role)
			if tc.owners != nil {
				setOwners(t, env, tc.owners...)
			}
			var got PlanView
			if code := env.get(t, shopPlans+"/shop-plan-new", &got); code != http.StatusOK || got.Permissions.Steer != tc.want {
				t.Errorf("%d steer=%v, want %v", code, got.Permissions.Steer, tc.want)
			}
		})
	}
}

// planWriterEnv is planEnv with the reader as the writer client.
func planWriterEnv(t *testing.T, roles ...string) *testEnv {
	t.Helper()
	env := planEnv(t, true, roles...)
	env.server.SetResearchWriter(env.reader)
	return env
}

func (e *testEnv) planExists(t *testing.T, name string) bool {
	t.Helper()
	return e.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: name}, &aiv1.AgentPlan{}) == nil
}

func TestCreatePlan(t *testing.T) {
	env := planWriterEnv(t, "leads")
	var ap aiv1.AgentProject
	if err := env.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "shop"}, &ap); err != nil {
		t.Fatal(err)
	}
	code, out := env.postJSON(t, shopPlans, map[string]string{"title": "  Refunds v2 "})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, out)
	}
	name, _ := out["name"].(string)
	if !regexp.MustCompile(`^plan-\d{8}-[a-z2-7]{6}$`).MatchString(name) {
		t.Errorf("name %q", name)
	}
	if out["title"] != "Refunds v2" || out["createdBy"] != "ada@example.com" || out["draft"] != "" ||
		out["permissions"].(map[string]any)["steer"] != true {
		t.Errorf("view: %v", out)
	}
	var pl aiv1.AgentPlan
	if err := env.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: name}, &pl); err != nil {
		t.Fatal(err)
	}
	if pl.Spec.ProjectRef != "shop" || pl.Spec.Title != "Refunds v2" || pl.Spec.CreatedBy != "ada@example.com" || pl.Spec.MaxBudgetUSD != "" {
		t.Errorf("spec: %+v", pl.Spec)
	}
	want := []metav1.OwnerReference{{APIVersion: aiv1.GroupVersion.String(), Kind: "AgentProject", Name: "shop", UID: ap.UID}}
	if !reflect.DeepEqual(pl.OwnerReferences, want) {
		t.Errorf("owner references: %+v, want %+v", pl.OwnerReferences, want)
	}

	// Without a body the title is empty.
	if resp := env.do(t, http.MethodPost, shopPlans, nil, nil); resp.StatusCode != http.StatusCreated {
		t.Errorf("empty body: %d", resp.StatusCode)
	}
}

func TestCreatePlanTitle(t *testing.T) {
	env := planWriterEnv(t, "leads")
	for name, title := range map[string]string{
		"too long":  strings.Repeat("é", maxPlanTitle+1),
		"two lines": "Refunds\nand more",
		"carriage":  "Refunds\rand more",
	} {
		if code, out := env.postJSON(t, shopPlans, map[string]string{"title": title}); code != http.StatusBadRequest || out["field"] != "title" {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	if code, _ := env.postJSON(t, shopPlans, map[string]string{"title": strings.Repeat("é", maxPlanTitle)}); code != http.StatusCreated {
		t.Errorf("255 characters: %d", code)
	}
	if code, _ := env.postJSON(t, shopPlans, map[string]int{"title": 1}); code != http.StatusBadRequest {
		t.Errorf("not a string: %d", code)
	}
}

func TestCreatePlanRateLimit(t *testing.T) {
	env := planWriterEnv(t, "leads")
	for i := range plansPerWindow {
		if code, _ := env.postJSON(t, shopPlans, map[string]string{}); code != http.StatusCreated {
			t.Fatalf("create %d: %d", i, code)
		}
	}
	resp := env.do(t, http.MethodPost, shopPlans, []byte(`{}`), nil)
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Errorf("over the limit: %d, Retry-After %q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	var list aiv1.AgentPlanList
	if err := env.reader.List(context.Background(), &list); err != nil || len(list.Items) != 3+plansPerWindow {
		t.Errorf("plans: %d %v", len(list.Items), err)
	}
}

func TestDeletePlan(t *testing.T) {
	env := planWriterEnv(t, "leads")
	// Any owner deletes, not only the creator (bea created it).
	if code := env.del(t, shopPlans+"/shop-plan-new"); code != http.StatusNoContent || env.planExists(t, "shop-plan-new") {
		t.Errorf("delete: %d", code)
	}
	for _, path := range []string{shopPlans + "/shop-plan-new", shopPlans + "/billing-plan", "/api/v1/projects/team-b/billing/plans/billing-plan"} {
		if code := env.del(t, path); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, code)
		}
	}
	if !env.planExists(t, "shop-plan-old") {
		t.Error("deleted another plan")
	}
}

func TestPlanWriteAccess(t *testing.T) {
	cases := []struct {
		name   string
		chat   bool
		roles  []string
		owners []string
		want   int
	}{
		{"viewer", true, []string{"devs"}, nil, http.StatusForbidden},
		{"steer, not an owner-subject", true, []string{"leads"}, []string{"bea"}, http.StatusForbidden},
		{"no view of the project", true, nil, nil, http.StatusNotFound},
		{"without chat", false, []string{"leads"}, nil, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := planEnv(t, tc.chat, tc.roles...)
			env.server.SetResearchWriter(env.reader)
			if tc.owners != nil {
				setOwners(t, env, tc.owners...)
			}
			if code, _ := env.postJSON(t, shopPlans, map[string]string{"title": "x"}); code != tc.want {
				t.Errorf("create: %d, want %d", code, tc.want)
			}
			if code := env.del(t, shopPlans+"/shop-plan-old"); code != tc.want {
				t.Errorf("delete: %d, want %d", code, tc.want)
			}
			var list aiv1.AgentPlanList
			if err := env.reader.List(context.Background(), &list); err != nil || len(list.Items) != 3 {
				t.Errorf("plans changed: %d %v", len(list.Items), err)
			}
		})
	}
}

func TestPlanWriteWithoutWriter(t *testing.T) {
	env := planEnv(t, true, "leads")
	if code, _ := env.postJSON(t, shopPlans, map[string]string{}); code != http.StatusNotFound {
		t.Errorf("create: %d", code)
	}
	if code := env.del(t, shopPlans+"/shop-plan-old"); code != http.StatusNotFound || !env.planExists(t, "shop-plan-old") {
		t.Errorf("delete: %d", code)
	}
}

// The plan's title: status.title (the agent's, then the milestone's), else
// spec.title, else empty for the web's fallback (Decision 4).
func TestPlanItemTitle(t *testing.T) {
	s := &Server{cfg: &Config{}}
	for _, c := range []struct{ spec, status, want string }{
		{"Mine", "Desk: version in the rail", "Desk: version in the rail"},
		{"Mine", "", "Mine"},
		{"", "Agent's", "Agent's"},
		{"", "", ""},
	} {
		pl := &aiv1.AgentPlan{Spec: aiv1.AgentPlanSpec{Title: c.spec}, Status: aiv1.AgentPlanStatus{Title: c.status}}
		if got := s.planItem(pl, false).Title; got != c.want {
			t.Errorf("spec %q, status %q: title %q, want %q", c.spec, c.status, got, c.want)
		}
	}
}
