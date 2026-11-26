package ui

import (
	"context"
	"net/http"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
)

const shopLoose = "/api/v1/projects/team-a/shop/loose-issues"

// looseEnv logs in ada@example.com with steer and chat on team-a, connects
// their GitLab and turns the shop's loose issues queue on (or off). Its
// GitLab has #50, #51 and #53 without a milestone and #54 in one.
func looseEnv(t *testing.T, enabled bool) (*testEnv, *gitlabfake.Fake) {
	t.Helper()
	env, _ := steeringEnvOf(t, withChat(oidcConfig(Rule{Roles: []string{"leads"}, Projects: []string{"team-a/*"}, Steer: true, Chat: true})))
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"leads": map[string]any{}}}
	env.login(t, "/")
	env.connectGitLab(t, "good-code")
	fake := followUpGitLab(env)
	fake.AddIssue(50, "Flaky helm-lint", "ci::flaky")
	fake.AddIssue(51, "Runner timeout")
	fake.AddIssue(53, "<b>Docs</b>")
	fake.AddIssue(54, "In a milestone").Milestone = &gitlab.Milestone{Title: "Checkout"}
	var ap aiv1.AgentProject
	ctx := context.Background()
	if err := env.reader.Get(ctx, client.ObjectKey{Namespace: "team-a", Name: "shop"}, &ap); err != nil {
		t.Fatal(err)
	}
	ap.Spec.LooseIssues = aiv1.LooseIssuesSpec{Enabled: enabled, MaxParallel: 2}
	if err := env.reader.Update(ctx, &ap); err != nil {
		t.Fatal(err)
	}
	return env, fake
}

// withQueue adds the shop's Queue task owned by subjects, with #50 pending,
// #51 active and held, #49 merged and #48 left, and #51's implement task.
func withQueue(t *testing.T, env *testEnv, subjects ...string) {
	t.Helper()
	ctx := context.Background()
	merged := metav1.Now()
	q := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: naming.QueueTask("shop")},
		Spec:       aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeQueue, OwnerSubjects: subjects},
		Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, FrozenAt: &merged, Items: []aiv1.AutopilotItem{
			{IID: 50, State: aiv1.AutopilotItemPending},
			{IID: 51, State: aiv1.AutopilotItemActive, MergeRequestIID: 7, MergeRequestURL: "https://gitlab.example/mr/7",
				Hold: &aiv1.ItemHold{Kind: aiv1.PauseStuck, Reason: "CI stays red", Since: merged}},
			{IID: 49, State: aiv1.AutopilotItemMerged, MergedAt: &merged},
			{IID: 48, State: aiv1.AutopilotItemLeft, LeftReason: "dropped by @ada"},
		}}},
	}
	impl := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shop-51-runner-timeout"},
		Spec:       aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeImplement, Issue: &aiv1.IssueRef{IID: 51, Title: "Runner timeout"}},
		Status:     aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: "abc123"}},
	}
	for _, o := range []*aiv1.AgentTask{q, impl} {
		if err := env.reader.Create(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLooseIssuesView(t *testing.T) {
	env, _ := looseEnv(t, true)
	withQueue(t, env, "ada@example.com")
	var v LooseIssues
	if code := env.get(t, shopLoose, &v); code != http.StatusOK {
		t.Fatalf("GET: %d", code)
	}
	if v.Queue == nil || v.Queue.Phase != aiv1.AutopilotRunning || v.Queue.MaxParallel != 2 || v.Queue.FrozenAt == nil {
		t.Fatalf("queue %+v", v.Queue)
	}
	if !v.Take || v.QueueTask != naming.QueueTask("shop") || len(v.OwnerSubjects) != 1 {
		t.Errorf("take %v, task %q, owners %v", v.Take, v.QueueTask, v.OwnerSubjects)
	}
	if len(v.Items) != 4 {
		t.Fatalf("items %+v", v.Items)
	}
	held := v.Items[1]
	if held.Title != "Runner timeout" || held.HoldReason != "CI stays red" || held.Task != "shop-51-runner-timeout" ||
		held.HeadSHA != "abc123" || held.MergeRequestIID != 7 || held.WebURL != env.server.cfg.GitLab.URL+"/acme/shop/-/issues/51" {
		t.Errorf("held item %+v", held)
	}
	if v.Items[2].MergedAt == nil || v.Items[3].LeftReason != "dropped by @ada" || v.Items[0].Title != "Flaky helm-lint" {
		t.Errorf("items %+v", v.Items)
	}
	// Newest first, no milestone issue.
	want := []struct {
		iid     int64
		inQueue bool
	}{{53, false}, {51, true}, {50, true}}
	if len(v.Issues) != len(want) {
		t.Fatalf("issues %+v", v.Issues)
	}
	for i, w := range want {
		if is := v.Issues[i]; is.IID != w.iid || is.InQueue != w.inQueue {
			t.Errorf("issue %d = #%d inQueue %v; want #%d %v", i, is.IID, is.InQueue, w.iid, w.inQueue)
		}
	}
	if is := v.Issues[0]; is.Title != "<b>Docs</b>" || is.Labels == nil || is.UpdatedAt == nil || is.WebURL == "" {
		t.Errorf("issue %+v", is)
	}
	if v.Issues[2].Labels[0] != "ci::flaky" {
		t.Errorf("labels %v", v.Issues[2].Labels)
	}
}

func TestLooseIssuesTakeNeedsOwnerSubject(t *testing.T) {
	env, _ := looseEnv(t, true)
	withQueue(t, env, "bob@example.com")
	var v LooseIssues
	if code := env.get(t, shopLoose, &v); code != http.StatusOK || v.Take || v.OwnerSubjects[0] != "bob@example.com" {
		t.Fatalf("another login: %d take %v owners %v", code, v.Take, v.OwnerSubjects)
	}
}

func TestLooseIssuesWithoutQueueTask(t *testing.T) {
	env, _ := looseEnv(t, true)
	code, out := env.getJSON(t, shopLoose)
	if code != http.StatusOK || out["queue"] != nil || out["take"] != false {
		t.Fatalf("no Queue task: %d %v", code, out)
	}
	if issues, _ := out["issues"].([]any); len(issues) != 3 {
		t.Errorf("issues %v", out["issues"])
	}
}

func TestLooseIssuesOff(t *testing.T) {
	env, _ := looseEnv(t, false)
	withQueue(t, env, "ada@example.com")
	env.server.gitlab.newClient = func(string, string) gitlab.API {
		t.Error("GitLab is read with the queue off")
		return gitlabfake.New()
	}
	code, out := env.getJSON(t, shopLoose)
	if code != http.StatusOK || out["queue"] != nil {
		t.Fatalf("off: %d %v", code, out)
	}
	if items, _ := out["items"].([]any); items == nil || len(items) != 0 {
		t.Errorf("items %v", out["items"])
	}
	var d ProjectDetail
	if env.get(t, "/api/v1/projects/team-a/shop", &d); d.LooseIssues {
		t.Error("ProjectDetail.looseIssues with the queue off")
	}
	if code, _ := env.getJSON(t, "/api/v1/projects/team-a/nope/loose-issues"); code != http.StatusNotFound {
		t.Errorf("unknown project: %d", code)
	}
}

func TestProjectDetailLooseIssues(t *testing.T) {
	env, _ := looseEnv(t, true)
	var d ProjectDetail
	if env.get(t, "/api/v1/projects/team-a/shop", &d); !d.LooseIssues {
		t.Error("ProjectDetail.looseIssues with the queue on")
	}
	for _, task := range d.Unplanned {
		if task.Name == naming.QueueTask("shop") {
			t.Error("the Queue task is on the Unplanned board")
		}
	}
}

func TestLooseIssuesCap(t *testing.T) {
	env, fake := looseEnv(t, true)
	for i := int64(100); i < 700; i++ {
		fake.AddIssue(i, "issue")
	}
	var v LooseIssues
	if env.get(t, shopLoose, &v); len(v.Issues) != 500 || v.Issues[0].IID != 699 {
		t.Fatalf("%d issues, first #%d", len(v.Issues), v.Issues[0].IID)
	}
}
