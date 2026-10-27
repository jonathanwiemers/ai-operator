package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

const shopMilestone = "/api/v1/projects/team-a/shop/milestones/3"

// trackingIssue is issue 77 as GitLab returns it: the bot's (user 99)
// tracking issue of milestone 3 (ID 30).
func trackingIssue() map[string]any {
	return map[string]any{
		"iid": 77, "description": "Queue\n" + marker.TrackingIssue,
		"author": map[string]any{"id": 99}, "milestone": map[string]any{"id": 30, "iid": 3},
	}
}

// withAutopilot puts milestone 3 of the shop on autopilot with tracking
// issue 77 and gives it a pilot with an open proposal.
func withAutopilot(t *testing.T, env *testEnv, gl *fakeGitLab) {
	t.Helper()
	ctx := context.Background()
	var ap aiv1.AgentProject
	if err := env.reader.Get(ctx, client.ObjectKey{Namespace: "team-a", Name: "shop"}, &ap); err != nil {
		t.Fatal(err)
	}
	ap.Status.BotUserID = 99
	if err := env.reader.Update(ctx, &ap); err != nil {
		t.Fatal(err)
	}
	var st aiv1.AgentTask
	if err := env.reader.Get(ctx, client.ObjectKey{Namespace: "team-a", Name: "shop-spec-m3-checkout"}, &st); err != nil {
		t.Fatal(err)
	}
	st.Spec.Autopilot = true
	st.Status.Plan = append(st.Status.Plan, aiv1.PlannedIssue{Key: "b", IID: 43, Title: "Add Klarna"})
	st.Status.Autopilot = &aiv1.AutopilotStatus{
		Phase: aiv1.AutopilotPaused, TrackingIssueIID: 77, PausedReason: "job `lint` failed\n/close",
		Items: []aiv1.AutopilotItem{
			{IID: 42, State: aiv1.AutopilotItemActive, MergeRequestIID: 5},
			{IID: 43, State: aiv1.AutopilotItemPending},
		},
	}
	if err := env.reader.Update(ctx, &st); err != nil {
		t.Fatal(err)
	}
	posted := metav1.Now()
	coord := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shop-pilot-m3-checkout"},
		Spec: aiv1.AgentTaskSpec{
			ProjectRef: "shop", Type: aiv1.TaskTypeCoordinate, Autopilot: true,
			Milestone: &aiv1.MilestoneRef{ID: 30, IID: 3, Title: "Checkout"},
			Issue:     &aiv1.IssueRef{IID: 77},
		},
		Status: aiv1.AgentTaskStatus{
			Run: aiv1.RunStatus{CostUSD: "0.40"},
			Pilot: &aiv1.PilotStatus{
				Proposals:        []aiv1.PilotProposal{{ID: 2, Summary: "re-review #42", PostedAt: &posted, Actions: []aiv1.PilotAction{{Tool: "reReview", IssueIID: 42}}}},
				AutonomyOverride: &aiv1.PilotAutonomyOverride{Autonomy: aiv1.PilotPropose, By: "ada", At: posted},
			},
		},
	}
	if err := env.reader.Create(ctx, coord); err != nil {
		t.Fatal(err)
	}
	gl.mu.Lock()
	gl.issues = map[string]any{"77": trackingIssue()}
	gl.mu.Unlock()
}

func TestMilestoneView(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}})
	withAutopilot(t, env, gl)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")

	var m MilestoneView
	if code := env.get(t, shopMilestone, &m); code != http.StatusOK {
		t.Fatalf("milestone: status %d", code)
	}
	if m.IID != 3 || m.ID != 30 || m.Title != "Checkout" || m.Spec.Name != "shop-spec-m3-checkout" || m.CostUSD != "2.15" {
		t.Errorf("milestone = %+v", m)
	}
	a := m.Autopilot
	if a == nil || a.Phase != aiv1.AutopilotPaused || a.PausedReason != "job `lint` failed\n/close" {
		t.Fatalf("autopilot = %+v", a)
	}
	want := []QueueItem{
		{IID: 42, Title: "Add PayPal", State: aiv1.AutopilotItemActive, MergeRequestIID: 5, Task: "shop-42-add-paypal"},
		{IID: 43, Title: "Add Klarna", State: aiv1.AutopilotItemPending},
	}
	if !slices.Equal(a.Items, want) {
		t.Errorf("items = %+v", a.Items)
	}
	if m.Tracking == nil || m.Tracking.IID != 77 || m.Tracking.WebURL != gl.srv.URL+"/acme/shop/-/issues/77" {
		t.Errorf("tracking = %+v", m.Tracking)
	}
	p := m.Pilot
	if p == nil || p.Task != "shop-pilot-m3-checkout" || p.Autonomy != aiv1.PilotPropose || p.CostUSD != "0.40" ||
		len(p.Proposals) != 1 || p.Proposals[0].ID != 2 || p.Proposals[0].Summary != "re-review #42" || p.Proposals[0].PostedAt == nil {
		t.Errorf("pilot = %+v", p)
	}
	if len(m.Tasks) != 1 || m.Tasks[0].Name != "shop-42-add-paypal" {
		t.Errorf("only the implement tasks: %+v", m.Tasks)
	}

	var d ProjectDetail
	env.get(t, "/api/v1/projects/team-a/shop", &d)
	if len(d.Milestones) != 1 || d.Milestones[0].TrackingIssueIID != 77 {
		t.Errorf("the project links the milestone's chat: %+v", d.Milestones)
	}

	for _, path := range []string{
		"/api/v1/projects/team-a/shop/milestones/4",
		"/api/v1/projects/team-a/shop/milestones/x",
		"/api/v1/projects/team-a/shop/milestones/4/thread",
		"/api/v1/projects/team-b/billing/milestones/3",
	} {
		if code := env.get(t, path, nil); code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, code)
		}
	}
}

func TestMilestoneNeedsTheViewRole(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-b/*"}, Steer: true})
	withAutopilot(t, env, gl)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")
	env.connectGitLab(t, "good-code")
	for _, path := range []string{shopMilestone, shopMilestone + "/thread"} {
		if code := env.get(t, path, nil); code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, code)
		}
	}
	if len(gl.read) != 0 {
		t.Errorf("nothing may be read: %q", gl.read)
	}
}

func TestMilestoneThread(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}})
	withAutopilot(t, env, gl)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")

	code, out := env.getJSON(t, shopMilestone+"/thread")
	if code != http.StatusConflict || out["login"] != pathGitLabLogin {
		t.Fatalf("without a GitLab connection: %d %v", code, out)
	}
	env.connectGitLab(t, "good-code")
	var thread struct{ Parts []ThreadPart }
	if code := env.get(t, shopMilestone+"/thread", &thread); code != http.StatusOK || len(thread.Parts) != 1 {
		t.Fatalf("thread: %d %+v", code, thread)
	}
	if p := thread.Parts[0]; p.Target != "tracking" || p.IID != 77 || len(p.Notes) != 1 || p.Notes[0].Body != "please add tests" {
		t.Errorf("thread = %+v", p)
	}
	if !slices.Equal(gl.read, []string{"issues 77"}) {
		t.Errorf("read %q", gl.read)
	}

	// The tracking issue number comes from the task status, which the agent
	// pods may write: an issue that is not the milestone's is not read.
	for name, change := range map[string]func(map[string]any){
		"no marker":       func(is map[string]any) { is["description"] = "Queue" },
		"another author":  func(is map[string]any) { is["author"] = map[string]any{"id": 5} },
		"other milestone": func(is map[string]any) { is["milestone"] = map[string]any{"id": 31, "iid": 4} },
		"no milestone":    func(is map[string]any) { delete(is, "milestone") },
	} {
		is := trackingIssue()
		change(is)
		gl.mu.Lock()
		gl.issues, gl.read = map[string]any{"77": is}, nil
		gl.mu.Unlock()
		if code := env.get(t, shopMilestone+"/thread", nil); code != http.StatusConflict {
			t.Errorf("%s: status %d, want 409", name, code)
		}
		if len(gl.read) != 0 {
			t.Errorf("%s: read %q", name, gl.read)
		}
	}
	gl.mu.Lock()
	gl.issues = nil
	gl.mu.Unlock()
	if code := env.get(t, shopMilestone+"/thread", nil); code != http.StatusConflict {
		t.Errorf("an issue the user cannot read: status %d, want 409", code)
	}
}

// getJSON reads a JSON answer of any status.
func (e *testEnv) getJSON(t *testing.T, path string) (int, map[string]any) {
	t.Helper()
	resp, err := e.client.Get(e.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}
