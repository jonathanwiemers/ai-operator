package ui

import (
	"errors"
	"net/http"
	"testing"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

func TestIssueView(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}})
	withAutopilot(t, env, gl)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")
	const live, stored = shopMilestone + "/issues/42", "/api/v1/projects/team-a/shop/milestones/4/issues/50"

	var v IssueView
	if code := env.get(t, live, &v); code != http.StatusOK {
		t.Fatalf("live issue: status %d", code)
	}
	if v.Namespace != "team-a" || v.Project != "shop" || v.MilestoneIID != 3 || v.MilestoneTitle != "Checkout" || v.Stored ||
		v.IID != 42 || v.Title != "Add PayPal" || v.State != "active" || v.MergeRequestIID != 5 || v.Task != "shop-42-add-paypal" ||
		v.WebURL != gl.srv.URL+"/acme/shop/-/issues/42" || v.Figures != nil {
		t.Errorf("without a database: %+v", v)
	}
	if code := env.get(t, stored, nil); code != http.StatusNotFound {
		t.Errorf("a stored issue without a database: status %d, want 404", code)
	}

	fake := &fakeMilestoneDetails{d: map[int64]store.MilestoneDetail{3: {Issues: []store.IssueFigures{paypalFigures()}}, 4: storedCheckout()}}
	env.server.milestoneDetails = fake
	v = IssueView{}
	if code := env.get(t, live, &v); code != http.StatusOK {
		t.Fatalf("live issue: status %d", code)
	}
	if f := v.Figures; v.Task != "shop-42-add-paypal" || v.State != "active" || f == nil || f.WallSec != 3600 || f.AgentSec != 1500 || f.CostUSD != "1.50" {
		t.Errorf("live issue with figures: %+v %+v", v, v.Figures)
	}
	v = IssueView{}
	if code := env.get(t, shopMilestone+"/issues/43", &v); code != http.StatusOK || v.Title != "Add Klarna" || v.Task != "" || v.Figures != nil {
		t.Errorf("an issue without a task or figures: status %d, %+v", code, v)
	}

	v = IssueView{}
	if code := env.get(t, stored, &v); code != http.StatusOK {
		t.Fatalf("stored issue: status %d", code)
	}
	if f := v.Figures; !v.Stored || v.MilestoneIID != 4 || v.MilestoneTitle != "Refunds" || v.IID != 50 || v.State != "merged" ||
		v.MergeRequestIID != 9 || v.Task != "" || f == nil || f.WallSec != 7200 || f.CostUSD != "2.50" {
		t.Errorf("stored issue: %+v %+v", v, v.Figures)
	}

	for _, path := range []string{
		shopMilestone + "/issues/50",                          // in another milestone
		"/api/v1/projects/team-a/shop/milestones/4/issues/42", // likewise, stored
		shopMilestone + "/issues/x",
		"/api/v1/projects/team-a/shop/milestones/5/issues/42",
		"/api/v1/projects/team-b/billing/milestones/3/issues/42",
	} {
		if code := env.get(t, path, nil); code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, code)
		}
	}

	fake.err = errors.New("connection refused")
	v = IssueView{}
	if code := env.get(t, live, &v); code != http.StatusOK || v.Figures != nil || v.Task == "" {
		t.Errorf("a failing query leaves the live issue without figures: status %d, %+v", code, v)
	}
	if code := env.get(t, stored, nil); code != http.StatusInternalServerError {
		t.Errorf("a failing query on a stored issue: status %d, want 500", code)
	}
}

func TestIssueViewNeedsTheViewRole(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-b/*"}})
	withAutopilot(t, env, gl)
	fake := &fakeMilestoneDetails{d: map[int64]store.MilestoneDetail{4: storedCheckout()}}
	env.server.milestoneDetails = fake
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")
	for _, path := range []string{shopMilestone + "/issues/42", "/api/v1/projects/team-a/shop/milestones/4/issues/50"} {
		if code := env.get(t, path, nil); code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, code)
		}
	}
	if len(fake.got) != 0 {
		t.Errorf("the snapshot may not be read: %v", fake.got)
	}
}
