package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
)

type taskEnv struct {
	t   *testing.T
	c   client.Client
	r   *AgentTaskReconciler
	now time.Time
	ctx context.Context
}

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newTaskEnv(t *testing.T, objs ...client.Object) *taskEnv {
	s := testScheme(t)
	project := testProject()
	project.Spec.MaxConcurrentRuns = 1
	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(append([]client.Object{project}, objs...)...).
		WithStatusSubresource(&aiv1.AgentProject{}, &aiv1.AgentTask{}).
		Build()
	e := &taskEnv{t: t, c: c, now: t0, ctx: context.Background()}
	e.r = &AgentTaskReconciler{Client: c, Scheme: s, Now: func() time.Time { return e.now }}
	return e
}

func newImplTask(name string, runRequest int64) *aiv1.AgentTask {
	return &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: ns, UID: types.UID("uid-" + name),
			CreationTimestamp: metav1.NewTime(t0),
			Labels:            map[string]string{LabelProject: "shop", LabelType: "implement"},
		},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeImplement, RunRequest: runRequest,
			Issue: &aiv1.IssueRef{IID: 42, Title: "Add PayPal"}},
	}
}

func (e *taskEnv) reconcile(name string) ctrl.Result {
	e.t.Helper()
	res, err := e.r.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: name}})
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *taskEnv) task(name string) *aiv1.AgentTask {
	e.t.Helper()
	var task aiv1.AgentTask
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: name}, &task); err != nil {
		e.t.Fatal(err)
	}
	return &task
}

func (e *taskEnv) exists(obj client.Object, name string) bool {
	e.t.Helper()
	err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: name}, obj)
	if apierrors.IsNotFound(err) {
		return false
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return true
}

// finishRun simulates the runner completing all requested runs.
func (e *taskEnv) finishRun(name string) {
	e.t.Helper()
	task := e.task(name)
	orig := task.DeepCopy()
	task.Status.Run.ObservedRunRequest = task.Spec.RunRequest
	task.Status.Run.ActiveRunRequest = task.Spec.RunRequest
	task.Status.Run.RunCount++
	task.Status.Run.LastActivityTime = &metav1.Time{Time: e.now}
	if err := e.c.Status().Patch(e.ctx, task, client.MergeFrom(orig)); err != nil {
		e.t.Fatal(err)
	}
}

func TestTaskLifecycle(t *testing.T) {
	const name = "shop-42-add-paypal"
	e := newTaskEnv(t, newImplTask(name, 1))

	// Run requested: workspace and pod are created.
	e.reconcile(name)
	var pod corev1.Pod
	var pvc corev1.PersistentVolumeClaim
	if !e.exists(&pod, name) || !e.exists(&pvc, naming.Workspace(name)) {
		t.Fatal("expected pod and workspace")
	}
	if got := e.task(name).Status.Phase; got != aiv1.TaskPhaseRunning {
		t.Fatalf("phase %s", got)
	}
	if pod.Spec.Containers[0].EnvFrom[0].SecretRef.Name != "agent-creds" {
		t.Fatal("engine credentials must be injected from the credentials secret")
	}
	if !metav1.IsControlledBy(&pod, e.task(name)) || !metav1.IsControlledBy(&pvc, e.task(name)) {
		t.Fatal("pod and pvc must be owned by the task")
	}

	// Run finished: the pod is kept warm.
	e.now = e.now.Add(10 * time.Minute)
	e.finishRun(name)
	res := e.reconcile(name)
	if got := e.task(name).Status.Phase; got != aiv1.TaskPhaseIdle {
		t.Fatalf("phase %s", got)
	}
	if !e.exists(&pod, name) {
		t.Fatal("idle pod must be kept")
	}
	if res.RequeueAfter < time.Hour || res.RequeueAfter > 2*time.Hour+time.Second {
		t.Fatalf("should requeue at the idle timeout, got %v", res.RequeueAfter)
	}

	// Idle timeout: pod removed, workspace kept.
	e.now = e.now.Add(2*time.Hour + time.Minute)
	e.reconcile(name)
	if e.exists(&pod, name) {
		t.Fatal("pod should be hibernated")
	}
	if !e.exists(&pvc, naming.Workspace(name)) {
		t.Fatal("workspace must survive hibernation")
	}
	if got := e.task(name).Status.Phase; got != aiv1.TaskPhaseHibernated {
		t.Fatalf("phase %s", got)
	}

	// New input wakes the task up on the same workspace.
	task := e.task(name)
	task.Spec.RunRequest = 2
	if err := e.c.Update(e.ctx, task); err != nil {
		t.Fatal(err)
	}
	e.reconcile(name)
	if !e.exists(&pod, name) || e.task(name).Status.Phase != aiv1.TaskPhaseRunning {
		t.Fatal("pod should be back")
	}
	e.finishRun(name)
	e.reconcile(name)

	// Long inactivity: hibernate, then archive the workspace.
	e.now = e.now.Add(3 * time.Hour)
	e.reconcile(name)
	e.now = e.now.Add(15 * 24 * time.Hour)
	e.reconcile(name)
	if e.exists(&pvc, naming.Workspace(name)) {
		t.Fatal("workspace should be archived")
	}
	if got := e.task(name).Status.Phase; got != aiv1.TaskPhaseArchived {
		t.Fatalf("phase %s", got)
	}

	// Even an archived task can be resumed: fresh workspace and pod.
	task = e.task(name)
	task.Spec.RunRequest = 3
	if err := e.c.Update(e.ctx, task); err != nil {
		t.Fatal(err)
	}
	e.reconcile(name)
	if !e.exists(&pod, name) || !e.exists(&pvc, naming.Workspace(name)) {
		t.Fatal("archived task should be rehydrated")
	}
}

func TestTaskQueueing(t *testing.T) {
	e := newTaskEnv(t, newImplTask("a", 1), newImplTask("b", 1))
	e.reconcile("a")
	res := e.reconcile("b")
	if got := e.task("b").Status.Phase; got != aiv1.TaskPhaseQueued {
		t.Fatalf("second task should queue (maxConcurrentRuns=1), phase %s", got)
	}
	if res.RequeueAfter != queueRetry {
		t.Fatalf("queued task should retry, got %v", res.RequeueAfter)
	}
	var pod corev1.Pod
	if e.exists(&pod, "b") {
		t.Fatal("queued task must not get a pod")
	}
	e.finishRun("a")
	e.reconcile("a")
	e.reconcile("b")
	if got := e.task("b").Status.Phase; got != aiv1.TaskPhaseRunning {
		t.Fatalf("b should run once a is idle, phase %s", got)
	}
}

func TestTaskBlockedAfterMaxRuns(t *testing.T) {
	task := newImplTask("x", 21)
	task.Status.Run.RunCount = 20
	task.Status.Run.ObservedRunRequest = 20
	task.Status.Run.ActiveRunRequest = 20
	e := newTaskEnv(t, task)
	e.reconcile("x")
	if got := e.task("x").Status.Phase; got != aiv1.TaskPhaseBlocked {
		t.Fatalf("phase %s", got)
	}
	var pod corev1.Pod
	if e.exists(&pod, "x") {
		t.Fatal("blocked task must not start a pod")
	}
}
