package controller

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// warmPilot is a Coordinate task without a run request or chat turn.
func warmPilot(name string, until, requested *metav1.Time) *aiv1.AgentTask {
	task := newImplTask(name, 0)
	task.Spec.Type = aiv1.TaskTypeCoordinate
	task.Spec.WarmUntil = until
	task.Spec.WarmRequestedAt = requested
	return task
}

func TestWarmUntilStartsAndKeepsAPod(t *testing.T) {
	until := t0.Add(time.Hour)
	e := newTaskEnv(t, warmPilot("p", &metav1.Time{Time: until}, nil))

	e.reconcile("p")
	var pod corev1.Pod
	if !e.exists(&pod, "p") {
		t.Fatal("a warm task gets a pod without a run or chat")
	}
	got := e.task("p")
	if got.Status.Phase != aiv1.TaskPhaseRunning || !strings.Contains(got.Status.Message, "kept warm until") {
		t.Fatalf("phase=%s message=%q", got.Status.Phase, got.Status.Message)
	}
	if got.Spec.RunRequest != 0 || got.Status.Run.RunCount != 0 {
		t.Fatalf("warmth starts no run: runRequest=%d runCount=%d", got.Spec.RunRequest, got.Status.Run.RunCount)
	}

	// Past idleTimeout (and chatIdleTimeout) the pod stays, idle, and the
	// task is checked again at warmUntil.
	e.now = t0.Add(30 * time.Minute)
	res := e.reconcile("p")
	if !e.exists(&pod, "p") {
		t.Fatal("a warm pod is not idle-expired")
	}
	if phase := e.task("p").Status.Phase; phase != aiv1.TaskPhaseIdle {
		t.Fatalf("phase %s", phase)
	}
	if want := until.Sub(e.now) + time.Second; res.RequeueAfter != want {
		t.Fatalf("requeue %v, want %v", res.RequeueAfter, want)
	}

	// Once warmUntil passes, the usual rules hibernate it.
	e.now = until.Add(time.Second)
	e.reconcile("p")
	if e.exists(&pod, "p") {
		t.Fatal("the pod hibernates after warmUntil")
	}
	if phase := e.task("p").Status.Phase; phase != aiv1.TaskPhaseHibernated {
		t.Fatalf("phase %s", phase)
	}
	// And it does not start again.
	e.reconcile("p")
	if e.exists(&pod, "p") {
		t.Fatal("a past warmUntil starts no pod")
	}
}

func TestWarmRequestedAtKeepsThePodForChatIdleTimeout(t *testing.T) {
	e := newTaskEnv(t, warmPilot("p", nil, &metav1.Time{Time: t0}))
	e.reconcile("p")
	var pod corev1.Pod
	if !e.exists(&pod, "p") {
		t.Fatal("a Desk warm request starts the pod")
	}

	e.now = t0.Add(aiv1.DefaultChatIdleTimeout - time.Minute)
	res := e.reconcile("p")
	if !e.exists(&pod, "p") {
		t.Fatal("the pod stays for chatIdleTimeout from the request")
	}
	if want := time.Minute + time.Second; res.RequeueAfter != want {
		t.Fatalf("requeue %v, want %v", res.RequeueAfter, want)
	}

	e.now = t0.Add(aiv1.DefaultChatIdleTimeout + time.Second)
	e.reconcile("p")
	if e.exists(&pod, "p") {
		t.Fatal("the pod hibernates after chatIdleTimeout from the request")
	}
}

func TestWarmSuspendedTaskGetsNoPod(t *testing.T) {
	task := warmPilot("p", &metav1.Time{Time: t0.Add(time.Hour)}, &metav1.Time{Time: t0})
	task.Spec.Suspend = true
	e := newTaskEnv(t, task)
	e.reconcile("p")
	var pod corev1.Pod
	if e.exists(&pod, "p") {
		t.Fatal("suspend wins over warmth")
	}

	// A warm pod stops when the task is suspended.
	task = e.task("p")
	task.Spec.Suspend = false
	if err := e.c.Update(e.ctx, task); err != nil {
		t.Fatal(err)
	}
	e.reconcile("p")
	if !e.exists(&pod, "p") {
		t.Fatal("expected pod once resumed")
	}
	task = e.task("p")
	task.Spec.Suspend = true
	if err := e.c.Update(e.ctx, task); err != nil {
		t.Fatal(err)
	}
	e.reconcile("p")
	if e.exists(&pod, "p") {
		t.Fatal("a suspended warm pod hibernates")
	}
}

func TestIdlePodKeptForWarmRequestTheCacheMissed(t *testing.T) {
	e := newTaskEnv(t, newImplTask("x", 1))
	e.reconcile("x")
	e.finishRun("x")
	// The API server already has a Desk warm request the cache misses.
	newer := e.task("x")
	newer.Spec.WarmRequestedAt = &metav1.Time{Time: e.now}
	e.r.APIReader = fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(newer).Build()

	e.now = e.now.Add(aiv1.DefaultIdleTimeout + time.Second)
	e.reconcile("x")
	var pod corev1.Pod
	if !e.exists(&pod, "x") {
		t.Fatal("pod must be kept for a warm request the cache missed")
	}
}
