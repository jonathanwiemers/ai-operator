package ui

import (
	"context"
	"net/http"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	)
	if chat {
		withChat(cfg)
	}
	env := newTestEnv(t, cfg)
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
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	grants := map[string]any{}
	for _, r := range roles {
		grants[r] = map[string]any{}
	}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": grants}
	env.login(t, "/")
	return env
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
	sameJSON(t, got, `{"name":"shop-plan-old","title":"Refunds","phase":"Created","createdBy":"ada@example.com",
		"createdAt":"2026-11-10T09:00:00Z","lastActivityAt":"2026-11-10T12:00:00Z","spendUSD":"1.2000","tokens":900,
		"budgetUSD":"8","budgetLimitUSD":"20","createdMilestoneIID":5,"createdMilestoneURL":"https://gitlab.example.com/m/5",
		"permissions":{"steer":false},"draft":"# Refunds","draftRevision":4,"draftBy":"owner"}`)

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
