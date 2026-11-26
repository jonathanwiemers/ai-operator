package ui

import (
	"context"
	"net/http"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestMilestoneIssueTiers(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}})
	withAutopilot(t, env, gl)
	ctx := context.Background()
	var st aiv1.AgentTask
	if err := env.reader.Get(ctx, client.ObjectKey{Namespace: "team-a", Name: "shop-spec-m3-checkout"}, &st); err != nil {
		t.Fatal(err)
	}
	for i := range st.Status.Plan {
		if st.Status.Plan[i].IID == 42 {
			st.Status.Plan[i].Profile, st.Status.Plan[i].ProfileWhy = "balanced", "New logic in <b>one</b> package."
		}
	}
	st.Status.Plan = append(st.Status.Plan, aiv1.PlannedIssue{Key: "c", IID: 44, Title: "Add Giropay", Profile: "gone", ProfileWhy: "Old tier."})
	if err := env.reader.Update(ctx, &st); err != nil {
		t.Fatal(err)
	}
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")

	want := map[int64]*TierView{
		42: {Profile: "balanced", Engine: "claude", Model: aiv1.DefaultLightModel, Effort: "high", Why: "New logic in <b>one</b> package."},
		43: nil,
		44: {Profile: "gone", Why: "Old tier."},
	}
	var m MilestoneView
	if code := env.get(t, shopMilestone, &m); code != http.StatusOK {
		t.Fatalf("milestone: status %d", code)
	}
	if len(m.Issues) != len(want) {
		t.Fatalf("issues = %+v", m.Issues)
	}
	for _, row := range m.Issues {
		if w, got := want[row.IID], row.Tier; (w == nil) != (got == nil) || w != nil && *w != *got {
			t.Errorf("#%d: tier = %+v, want %+v", row.IID, got, w)
		}
	}

	var v IssueView
	if code := env.get(t, shopMilestone+"/issues/42", &v); code != http.StatusOK {
		t.Fatalf("issue: status %d", code)
	}
	if v.Tier == nil || *v.Tier != *want[42] {
		t.Errorf("issue tier = %+v, want %+v", v.Tier, want[42])
	}
}
