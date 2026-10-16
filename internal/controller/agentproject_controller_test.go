package controller

import (
	"context"
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

const ns = "agents"

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := aiv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func testProject() *aiv1.AgentProject {
	return &aiv1.AgentProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: ns, UID: "uid-shop"},
		Spec: aiv1.AgentProjectSpec{
			GitLab: aiv1.GitLabSpec{
				Project:        "group/app",
				TokenSecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "gitlab"}, Key: "token"},
			},
			Agents: aiv1.AgentsSpec{Image: "agent:latest", CredentialsSecretName: "agent-creds"},
		},
	}
}

type projectEnv struct {
	t   *testing.T
	c   client.Client
	gl  *gitlabfake.Fake
	r   *AgentProjectReconciler
	ctx context.Context
}

func newProjectEnv(t *testing.T) *projectEnv {
	s := testScheme(t)
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "gitlab", Namespace: ns}, Data: map[string][]byte{"token": []byte("tok")}}
	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(testProject(), secret).
		WithStatusSubresource(&aiv1.AgentProject{}, &aiv1.AgentTask{}).
		Build()
	gl := gitlabfake.New()
	r := &AgentProjectReconciler{Client: c, Scheme: s, NewGitLab: func(string, string) gitlab.API { return gl }}
	return &projectEnv{t: t, c: c, gl: gl, r: r, ctx: context.Background()}
}

func (e *projectEnv) reconcile() {
	e.t.Helper()
	res, err := e.r.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: "shop"}})
	if err != nil {
		e.t.Fatal(err)
	}
	if res.RequeueAfter != time.Minute {
		e.t.Fatalf("expected poll requeue, got %v", res.RequeueAfter)
	}
	var p aiv1.AgentProject
	_ = e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &p)
	for _, c := range p.Status.Conditions {
		if c.Type == "Synced" && c.Status != metav1.ConditionTrue {
			e.t.Fatalf("sync failed: %s", c.Message)
		}
	}
}

func (e *projectEnv) tasks() map[string]aiv1.AgentTask {
	e.t.Helper()
	var list aiv1.AgentTaskList
	if err := e.c.List(e.ctx, &list); err != nil {
		e.t.Fatal(err)
	}
	out := map[string]aiv1.AgentTask{}
	for _, t := range list.Items {
		out[t.Name] = t
	}
	return out
}

func TestMilestoneSpecTasks(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.Milestones = []gitlab.Milestone{
		{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: "/agent spec\nBuild a new checkout."},
		{ID: 12, IID: 4, Title: "Unplanned", State: "active", Description: "no marker here"},
	}
	e.reconcile()

	tasks := e.tasks()
	task, ok := tasks["shop-spec-m3-checkout-v2"]
	if !ok || len(tasks) != 1 {
		t.Fatalf("expected exactly the spec task, got %v", keys(tasks))
	}
	if task.Spec.Type != aiv1.TaskTypeSpec || task.Spec.RunRequest != 1 || task.Spec.Milestone.ID != 11 {
		t.Fatalf("unexpected spec %+v", task.Spec)
	}
	if len(task.OwnerReferences) != 1 || task.OwnerReferences[0].Name != "shop" {
		t.Fatalf("task must be owned by the project: %+v", task.OwnerReferences)
	}

	// Unchanged description: no new run.
	e.reconcile()
	if got := e.tasks()["shop-spec-m3-checkout-v2"].Spec.RunRequest; got != 1 {
		t.Fatalf("run request bumped without change: %d", got)
	}

	// Changed description: re-plan once.
	e.gl.Milestones[0].Description += "\nAlso support PayPal."
	e.reconcile()
	e.reconcile()
	if got := e.tasks()["shop-spec-m3-checkout-v2"].Spec.RunRequest; got != 2 {
		t.Fatalf("expected one re-plan, run request %d", got)
	}

	// Closed milestone: task removed.
	e.gl.Milestones = nil
	e.reconcile()
	if len(e.tasks()) != 0 {
		t.Fatalf("spec task should be deleted, got %v", keys(e.tasks()))
	}
}

func TestIssueLifecycle(t *testing.T) {
	e := newProjectEnv(t)
	human := gitlab.User{ID: 1, Username: "jonathan"}
	e.gl.AddIssue(42, "Add PayPal button", "agent::draft")
	e.gl.AddNote(42, human, "/agent this is an old note and must not trigger")

	e.reconcile()
	if len(e.tasks()) != 0 {
		t.Fatalf("draft issues must not get a task")
	}

	// Human moves the issue to ready.
	e.gl.SetLabels(42, "agent::ready")
	e.reconcile()
	task, ok := e.tasks()["shop-42-add-paypal-button"]
	if !ok {
		t.Fatalf("expected implement task, got %v", keys(e.tasks()))
	}
	if task.Spec.RunRequest != 1 || task.Spec.Issue.IID != 42 || task.Labels[LabelIssueIID] != "42" {
		t.Fatalf("unexpected task %+v", task.Spec)
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::working"}) {
		t.Fatalf("issue should be moved to working, has %v", labels)
	}

	// Polling again does not request another run.
	e.reconcile()
	if got := e.tasks()["shop-42-add-paypal-button"].Spec.RunRequest; got != 1 {
		t.Fatalf("unexpected run request %d", got)
	}

	// Agent finishes and moves the issue to review; plain discussion and the
	// bot's own notes do not trigger runs.
	e.gl.SetLabels(42, "agent::review")
	e.gl.AddNote(42, human, "looks good so far")
	e.gl.AddNote(42, e.gl.Bot, "/agent I am the bot")
	e.gl.AddNote(42, human, "/agents are great")
	e.reconcile()
	if got := e.tasks()["shop-42-add-paypal-button"].Spec.RunRequest; got != 1 {
		t.Fatalf("chatter must not trigger a run, run request %d", got)
	}

	// A command does.
	e.gl.AddNote(42, human, "/agent please also handle refunds")
	e.reconcile()
	if got := e.tasks()["shop-42-add-paypal-button"].Spec.RunRequest; got != 2 {
		t.Fatalf("command should trigger a run, run request %d", got)
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::working"}) {
		t.Fatalf("issue should be back in working, has %v", labels)
	}

	// Renaming the issue keeps the same task.
	e.gl.Issues[42].Title = "Add PayPal and refunds"
	e.reconcile()
	if _, ok := e.tasks()["shop-42-add-paypal-button"]; !ok || len(e.tasks()) != 1 {
		t.Fatalf("rename must not create a new task: %v", keys(e.tasks()))
	}

	// Human moves it to done: task (and with it pod + workspace) deleted.
	e.gl.SetLabels(42, "agent::done")
	e.reconcile()
	if len(e.tasks()) != 0 {
		t.Fatalf("done issue must delete the task, got %v", keys(e.tasks()))
	}
}

func TestClosedIssueDeletesTask(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.AddIssue(7, "Fix login", "agent::ready")
	e.reconcile()
	if len(e.tasks()) != 1 {
		t.Fatal("expected a task")
	}
	e.gl.Issues[7].State = "closed"
	e.reconcile()
	if len(e.tasks()) != 0 {
		t.Fatal("closed issue must delete the task")
	}
}

func TestIsCommand(t *testing.T) {
	human := gitlab.User{ID: 1}
	cases := map[string]bool{
		"/agent":               true,
		"/agent go":            true,
		"  /agent: retry":      true,
		"/agent\nmore details": true,
		"/agents":              false,
		"please /agent":        false,
		"":                     false,
	}
	for body, want := range cases {
		if got := IsCommand(gitlab.Note{Body: body, Author: human}, 999, "/agent"); got != want {
			t.Errorf("IsCommand(%q) = %v, want %v", body, got, want)
		}
	}
	if IsCommand(gitlab.Note{Body: "/agent", System: true, Author: human}, 999, "/agent") {
		t.Error("system notes are never commands")
	}
}

func keys(m map[string]aiv1.AgentTask) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestUsageLimitsAggregation(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	mk := func(engine aiv1.Engine, until time.Time) aiv1.AgentTask {
		var task aiv1.AgentTask
		task.Status.Run.LimitedEngine = engine
		task.Status.Run.DeferredUntil = &metav1.Time{Time: until}
		task.Status.Run.Summary = string(engine) + " limit"
		return task
	}
	limits := usageLimits([]aiv1.AgentTask{
		mk(aiv1.EngineClaude, now.Add(time.Hour)),
		mk(aiv1.EngineClaude, now.Add(2*time.Hour)),
		mk(aiv1.EngineCursor, now.Add(-time.Minute)), // expired
		{},
	}, now)
	if len(limits) != 1 || limits[0].Engine != aiv1.EngineClaude || !limits[0].Until.Time.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("expected the latest active claude limit only, got %+v", limits)
	}
}
