package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestPromptViews(t *testing.T) {
	posted := metav1.Now()
	holder := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shop-spec-m3-checkout"},
		Spec:       aiv1.AgentTaskSpec{OwnerSubjects: []string{"ada@example.com"}},
	}
	// Q1..Q12 closed, Q13 open, newest last as in status.
	for i := 1; i <= 13; i++ {
		p := aiv1.Prompt{ID: fmt.Sprintf("Q%d", i), Source: aiv1.PromptSource{Kind: aiv1.PromptMainHealth, IssueIID: 40}, Question: "Main is red", State: aiv1.PromptSuperseded}
		if i == 13 {
			p = aiv1.Prompt{
				ID: "Q13", Source: aiv1.PromptSource{Kind: aiv1.PromptMainHealth, IssueIID: 40, MRIID: 7, Task: "shop-40-flaky"}, Question: "Main is red",
				Context: "job `test` failed", AllowFreeText: true, PostedAt: &posted,
				Options: []aiv1.PromptOption{
					{ID: "A", Label: "Retry the job", Actions: []aiv1.PilotAction{{Tool: "retryPipeline", PipelineID: 9}}},
					{ID: "B", Label: "Hand #40 to the agent", Detail: "it failed once", Recommended: true, Actions: []aiv1.PilotAction{{Tool: "fixFlaky", IssueIID: 40}}},
					{ID: "C", Label: "Ignore it"},
				},
			}
		}
		if i == 12 {
			p.State, p.Answer = aiv1.PromptAnswered, &aiv1.PromptAnswer{OptionID: "A", By: "ada", At: posted, Via: "desk", Outcome: "retried"}
		}
		holder.Status.Prompts = append(holder.Status.Prompts, p)
	}

	views := promptViews(holder, "shop-pilot-m3-checkout", "ada@example.com", true)
	ids := []string{}
	for _, v := range views {
		ids = append(ids, v.ID)
	}
	if want := []string{"Q13", "Q12", "Q11", "Q10", "Q9", "Q8", "Q7", "Q6", "Q5", "Q4", "Q3"}; !slices.Equal(ids, want) {
		t.Errorf("prompts %v, want the open one and the newest 10 closed, newest first", ids)
	}
	open := views[0]
	if open.Kind != aiv1.PromptMainHealth || open.State != aiv1.PromptOpen || open.IssueIID != 40 || open.MRIID != 7 || open.Task != "shop-40-flaky" ||
		open.Context != "job `test` failed" || !open.AllowFreeText || open.PostedAt == nil || open.Answer != nil {
		t.Errorf("open prompt = %+v", open)
	}
	if !open.CanAnswer || views[1].CanAnswer {
		t.Errorf("an owner answers the open prompt only: %v %v", open.CanAnswer, views[1].CanAnswer)
	}
	if open.Target != (PromptTarget{Namespace: "team-a", Task: "shop-pilot-m3-checkout"}) {
		t.Errorf("target = %+v", open.Target)
	}
	order := []string{}
	for _, o := range open.Options {
		order = append(order, o.ID)
	}
	if !slices.Equal(order, []string{"B", "A", "C"}) {
		t.Errorf("options %v, want the recommended one first", order)
	}
	if b := open.Options[0]; !b.Recommended || b.Detail != "it failed once" ||
		!slices.Equal(b.Actions, []string{"fixFlaky #40: the flaky issue goes to the agent in this milestone"}) {
		t.Errorf("option B = %+v", b)
	}
	if c := open.Options[2]; c.Actions == nil || len(c.Actions) != 0 {
		t.Errorf("the do-nothing option has no actions: %+v", c)
	}
	if a := views[1].Answer; a == nil || a.OptionID != "A" || a.By != "ada" || a.Via != "desk" || a.Outcome != "retried" {
		t.Errorf("answer = %+v", a)
	}

	for _, tc := range []struct {
		name, target, user string
		chat               bool
	}{
		{"not an owner", "shop-pilot-m3-checkout", "bob@example.com", true},
		{"without the Desk chat", "shop-pilot-m3-checkout", "ada@example.com", false},
		{"without a target", "", "ada@example.com", true},
	} {
		if v := promptViews(holder, tc.target, tc.user, tc.chat); v[0].CanAnswer {
			t.Errorf("%s: canAnswer", tc.name)
		}
	}
}

func TestPromptsOnTheMilestoneAndTask(t *testing.T) {
	env, gl := newSteeringEnv(t,
		Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}, Chat: true})
	withChat(env.server.cfg)
	withAutopilot(t, env, gl)
	ctx := context.Background()
	question := aiv1.Prompt{ID: "Q1", Source: aiv1.PromptSource{Kind: aiv1.PromptNeedsInput, IssueIID: 42}, Question: "Which API?", AllowFreeText: true}
	for name, p := range map[string]aiv1.Prompt{
		"shop-spec-m3-checkout": {ID: "Q4", Source: aiv1.PromptSource{Kind: aiv1.PromptProposal}, Question: "Re-review #42?",
			Options: []aiv1.PromptOption{{ID: "A", Label: "Yes", Actions: []aiv1.PilotAction{{Tool: "reReview", IssueIID: 42}}, Recommended: true}, {ID: "B", Label: "No"}}},
		"shop-42-add-paypal": question,
	} {
		var task aiv1.AgentTask
		if err := env.reader.Get(ctx, client.ObjectKey{Namespace: "team-a", Name: name}, &task); err != nil {
			t.Fatal(err)
		}
		task.Spec.OwnerSubjects = []string{"ada@example.com"}
		task.Status.Prompts = []aiv1.Prompt{p}
		if err := env.reader.Update(ctx, &task); err != nil {
			t.Fatal(err)
		}
	}
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")

	var m MilestoneView
	if code := env.get(t, shopMilestone, &m); code != http.StatusOK {
		t.Fatalf("milestone: status %d", code)
	}
	if len(m.Prompts) != 1 || m.Prompts[0].ID != "Q4" || !m.Prompts[0].CanAnswer ||
		m.Prompts[0].Target != (PromptTarget{Namespace: "team-a", Task: "shop-pilot-m3-checkout"}) ||
		!slices.Equal(m.Prompts[0].Options[0].Actions, []string{"reReview #42: its reviewers run again on the pushed head"}) {
		t.Errorf("milestone prompts = %+v", m.Prompts)
	}
	if m.Pilot == nil || len(m.Pilot.Proposals) != 1 {
		t.Errorf("legacy proposals stay: %+v", m.Pilot)
	}

	var tv struct {
		Prompts []PromptView `json:"prompts"`
	}
	if code := env.get(t, "/api/v1/projects/team-a/shop/tasks/shop-42-add-paypal", &tv); code != http.StatusOK {
		t.Fatalf("task: status %d", code)
	}
	if len(tv.Prompts) != 1 || tv.Prompts[0].ID != "Q1" || tv.Prompts[0].Kind != aiv1.PromptNeedsInput || !tv.Prompts[0].CanAnswer ||
		tv.Prompts[0].Target != (PromptTarget{Namespace: "team-a", Task: "shop-42-add-paypal"}) {
		t.Errorf("task prompts = %+v", tv.Prompts)
	}
	tv.Prompts = nil
	if code := env.get(t, "/api/v1/projects/team-a/shop/tasks/shop-spec-m3-checkout", &tv); code != http.StatusOK || tv.Prompts == nil || len(tv.Prompts) != 0 {
		t.Errorf("the spec task's prompts are the milestone's: status %d, %+v", code, tv.Prompts)
	}
}

func TestTaskResponseShape(t *testing.T) {
	env, _ := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}, Chat: true})
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")
	var raw map[string]json.RawMessage
	if code := env.get(t, "/api/v1/projects/team-a/shop/tasks/shop-spec-m3-checkout", &raw); code != http.StatusOK {
		t.Fatalf("task: status %d", code)
	}
	if string(raw["prompts"]) != "[]" {
		t.Errorf("prompts = %s, want []", raw["prompts"])
	}
	var perms map[string]bool
	if err := json.Unmarshal(raw["permissions"], &perms); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"transcript", "gitlab", "steer", "chat"} {
		if _, ok := perms[k]; !ok {
			t.Errorf("permissions lacks %q: %v", k, perms)
		}
	}
	var tr TaskResponse
	if code := env.get(t, "/api/v1/projects/team-a/shop/tasks/shop-spec-m3-checkout", &tr); code != http.StatusOK || tr.Permissions == nil || tr.Task.Name == "" {
		t.Errorf("TaskResponse = %+v", tr)
	}
}
