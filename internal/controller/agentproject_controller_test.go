package controller

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

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

func TestPickUpAssignsBot(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.AddIssue(42, "Add PayPal button", "agent::ready")
	e.reconcile()

	// Label and assignee change in one update.
	if len(e.gl.IssueUpdates) != 1 {
		t.Fatalf("expected one issue update, got %+v", e.gl.IssueUpdates)
	}
	if u := e.gl.IssueUpdates[0]; !slices.Equal(u.AddLabels, []string{"agent::working"}) || !slices.Equal(u.AssigneeIDs, []int64{e.gl.Bot.ID}) {
		t.Fatalf("unexpected update %+v", u)
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::working"}) {
		t.Fatalf("issue should be moved to working, has %v", labels)
	}
	if got := e.gl.Issues[42].Assignees; !slices.Equal(got, []gitlab.User{e.gl.Bot}) {
		t.Fatalf("issue should be assigned to the bot, has %v", got)
	}

	// Another run request on the issue the bot is assigned to: only the label
	// changes.
	e.gl.SetLabels(42, "agent::ready")
	e.reconcile()
	if len(e.gl.IssueUpdates) != 2 || e.gl.IssueUpdates[1].AssigneeIDs != nil {
		t.Fatalf("an assigned issue must not be reassigned: %+v", e.gl.IssueUpdates)
	}
	if got := e.gl.Issues[42].Assignees; !slices.Equal(got, []gitlab.User{e.gl.Bot}) {
		t.Fatalf("issue should stay assigned to the bot, has %v", got)
	}
}

func TestPickUpKeepsHumanAssignee(t *testing.T) {
	e := newProjectEnv(t)
	human := gitlab.User{ID: 1, Username: "jonathan"}
	e.gl.AddIssue(42, "Add PayPal button", "agent::ready").Assignees = []gitlab.User{human}
	e.reconcile()

	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::working"}) {
		t.Fatalf("issue should be moved to working, has %v", labels)
	}
	if len(e.gl.IssueUpdates) != 1 || e.gl.IssueUpdates[0].AssigneeIDs != nil {
		t.Fatalf("a human assignee must not be replaced: %+v", e.gl.IssueUpdates)
	}
	if got := e.gl.Issues[42].Assignees; !slices.Equal(got, []gitlab.User{human}) {
		t.Fatalf("human assignee should stay, has %v", got)
	}
}

func TestPickUpAssignBotDisabled(t *testing.T) {
	e := newProjectEnv(t)
	var project aiv1.AgentProject
	_ = e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &project)
	off := false
	project.Spec.Workflow.AssignBot = &off
	if err := e.c.Update(e.ctx, &project); err != nil {
		t.Fatal(err)
	}
	e.gl.AddIssue(42, "Add PayPal button", "agent::ready")
	e.reconcile()

	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::working"}) {
		t.Fatalf("issue should be moved to working, has %v", labels)
	}
	if len(e.gl.IssueUpdates) != 1 || e.gl.IssueUpdates[0].AssigneeIDs != nil || e.gl.Issues[42].Assignees != nil {
		t.Fatalf("assignBot: false must not assign: %+v", e.gl.IssueUpdates)
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
	// A limit a run went around with a fallback holds for the others too.
	var routed aiv1.AgentTask
	routed.Status.Run.UsageLimits = []aiv1.UsageLimit{
		{Engine: aiv1.EngineCursor, Until: metav1.NewTime(now.Add(24 * time.Hour)), Message: "monthly"},
		{Engine: aiv1.EngineClaude, Until: metav1.NewTime(now.Add(-time.Hour))}, // expired
	}
	limits = usageLimits([]aiv1.AgentTask{mk(aiv1.EngineClaude, now.Add(time.Hour)), routed}, now)
	if len(limits) != 2 || limits[0].Engine != aiv1.EngineClaude || !limits[0].Until.Time.Equal(now.Add(time.Hour)) ||
		limits[1].Engine != aiv1.EngineCursor || limits[1].Message != "monthly" {
		t.Fatalf("expected claude's deferral and cursor's routed limit, got %+v", limits)
	}
}

func TestEngineUsageKeepsTheFreshestReading(t *testing.T) {
	at := func(min int) metav1.Time { return metav1.NewTime(time.Date(2026, 10, 28, 12, min, 0, 0, time.UTC)) }
	reading := func(engine aiv1.Engine, min int, percent int32) aiv1.EngineUsage {
		return aiv1.EngineUsage{Engine: engine, ObservedAt: at(min), Windows: []aiv1.UsageWindow{{Name: "five_hour", Percent: percent}}}
	}
	task := func(us ...aiv1.EngineUsage) aiv1.AgentTask {
		var t aiv1.AgentTask
		t.Status.Run.EngineUsage = us
		return t
	}
	// The previous status has an older claude reading and a cursor one no
	// task has any more (its task is gone).
	prev := []aiv1.EngineUsage{reading(aiv1.EngineClaude, 1, 5), reading(aiv1.EngineCursor, 2, 50)}
	got := engineUsage(prev, []aiv1.AgentTask{
		task(reading(aiv1.EngineClaude, 10, 20)),
		task(reading(aiv1.EngineClaude, 5, 10)),
		{},
	})
	if len(got) != 2 || got[0].Engine != aiv1.EngineClaude || got[0].Windows[0].Percent != 20 ||
		got[1].Engine != aiv1.EngineCursor || got[1].Windows[0].Percent != 50 {
		t.Fatalf("expected claude's freshest reading (20 %%) and the kept cursor one, got %+v", got)
	}
	if prev[0].Windows[0].Percent != 5 {
		t.Fatal("the previous status must not be changed in place")
	}
}

func TestFallbacksValidCondition(t *testing.T) {
	var project aiv1.AgentProject
	spec := project.Spec.WithDefaults()
	reportFallbacks(&project, spec)
	if c := meta.FindStatusCondition(project.Status.Conditions, aiv1.ConditionFallbacksValid); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("expected FallbacksValid true without fallbacks, got %+v", c)
	}
	spec.Agents.Review.Fallback = &aiv1.RoleFallback{Engine: aiv1.EngineClaude, Model: "gpt-9"}
	reportFallbacks(&project, spec)
	c := meta.FindStatusCondition(project.Status.Conditions, aiv1.ConditionFallbacksValid)
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "InvalidFallback" || !strings.Contains(c.Message, "review: ") {
		t.Fatalf("expected FallbacksValid false naming the review, got %+v", c)
	}
}

func TestReviewCommandRequestsAReReview(t *testing.T) {
	e := newProjectEnv(t)
	human := gitlab.User{ID: 1, Username: "jonathan"}
	e.gl.AddIssue(42, "Add PayPal button", "agent::ready")
	e.reconcile()
	name := "shop-42-add-paypal-button"
	e.gl.MRs[1] = &gitlab.MergeRequest{IID: 1, State: "opened", SHA: "sha-head", SourceBranch: "agent/42-add-paypal-button"}
	finished := func(mutate func(*aiv1.RunStatus)) {
		task := e.tasks()[name]
		orig := task.DeepCopy()
		task.Status.Run.ObservedRunRequest, task.Status.Run.MergeRequestIID, task.Status.Run.PushedSHA = task.Spec.RunRequest, 1, "sha-head"
		if mutate != nil {
			mutate(&task.Status.Run)
		}
		if err := e.c.Status().Patch(e.ctx, &task, client.MergeFrom(orig)); err != nil {
			t.Fatal(err)
		}
		e.gl.SetLabels(42, "agent::review")
	}
	finished(nil)

	e.gl.AddMRNote(1, human, "/agent review")
	e.reconcile()
	task := e.tasks()[name]
	if task.Spec.RunRequest != 2 || task.Spec.Source.ReviewFor != "sha-head" {
		t.Fatalf("/agent review should request a re-review of the head: %+v", task.Spec)
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::working"}) {
		t.Fatalf("issue should be back in working, has %v", labels)
	}

	// Asked again for the head it reviewed already: reviewed again.
	finished(func(r *aiv1.RunStatus) { r.ReviewedFor = "sha-head" })
	e.gl.AddMRNote(1, human, "/agent review")
	e.reconcile()
	if task = e.tasks()[name]; task.Spec.RunRequest != 3 || task.Status.Run.ReviewedFor != "" {
		t.Fatalf("a repeated /agent review should review the head again: %+v %+v", task.Spec, task.Status.Run)
	}

	// An instruction next to it needs the implementer: a normal run.
	finished(func(r *aiv1.RunStatus) { r.ReviewedFor = "sha-head" })
	e.gl.AddNote(42, human, "/agent also handle refunds")
	e.gl.AddMRNote(1, human, "/agent review")
	e.reconcile()
	if task = e.tasks()[name]; task.Spec.RunRequest != 4 || task.Status.Run.ReviewedFor != "sha-head" {
		t.Fatalf("an instruction with /agent review is a normal run: %+v %+v", task.Spec, task.Status.Run)
	}

	// A head someone else pushed is no re-review: the agent takes it over.
	finished(nil)
	e.gl.MRs[1].SHA = "sha-foreign"
	e.gl.AddMRNote(1, human, "/agent review")
	e.reconcile()
	if task = e.tasks()[name]; task.Spec.RunRequest != 5 || task.Spec.Source.ReviewFor != "sha-head" {
		t.Fatalf("a foreign head must not be re-reviewed without the implementer: %+v", task.Spec)
	}
}

// A repeated "/agent review" clears reviewedFor before the run request, so a
// runner never sees the request with the old reviewedFor.
func TestReviewCommandClearsReviewedForBeforeTheRunRequest(t *testing.T) {
	e := newProjectEnv(t)
	failUpdate := false
	e.r.Client = interceptor.NewClient(e.c.(client.WithWatch), interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, ok := obj.(*aiv1.AgentTask); ok && failUpdate {
				return errors.New("conflict")
			}
			return c.Update(ctx, obj, opts...)
		},
	})
	human := gitlab.User{ID: 1, Username: "jonathan"}
	e.gl.AddIssue(42, "Add PayPal button", "agent::ready")
	e.reconcile()
	name := "shop-42-add-paypal-button"
	e.gl.MRs[1] = &gitlab.MergeRequest{IID: 1, State: "opened", SHA: "sha-head"}
	task := e.tasks()[name]
	orig := task.DeepCopy()
	task.Status.Run.ObservedRunRequest, task.Status.Run.MergeRequestIID = 1, 1
	task.Status.Run.PushedSHA, task.Status.Run.ReviewedFor = "sha-head", "sha-head"
	if err := e.c.Status().Patch(e.ctx, &task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	e.gl.SetLabels(42, "agent::review")
	e.gl.AddMRNote(1, human, "/agent review")
	failUpdate = true
	_, _ = e.r.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: "shop"}})
	if task = e.tasks()[name]; task.Spec.RunRequest != 1 || task.Status.Run.ReviewedFor != "" {
		t.Fatalf("reviewedFor must be cleared before the run request: %+v %+v", task.Spec, task.Status.Run)
	}
	failUpdate = false
	e.reconcile()
	if task = e.tasks()[name]; task.Spec.RunRequest != 2 || task.Spec.Source.ReviewFor != "sha-head" {
		t.Fatalf("the next poll requests the re-review: %+v", task.Spec)
	}
}
