package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// fakeLogs is a containerLogs that answers from maps and counts reads:
// calls those of LastLine, tails those of Tail.
type fakeLogs struct {
	last    map[string]time.Time
	err     error
	calls   int
	tail    map[string]string
	tailErr map[string]error
	tails   int
}

func (f *fakeLogs) LastLine(_ context.Context, _, _, container string) (time.Time, bool, error) {
	f.calls++
	t, ok := f.last[container]
	return t, ok, f.err
}

func (f *fakeLogs) Tail(_ context.Context, _, _, container string, _ int64, _ int) (string, error) {
	f.tails++
	return f.tail[container], f.tailErr[container]
}

// newRunningTask starts task x with run 1 in progress since t0 and its pod
// in the given phase.
func newRunningTask(t *testing.T, phase corev1.PodPhase) (*taskEnv, *fakeLogs) {
	e := newTaskEnv(t, newImplTask("x", 1))
	logs := &fakeLogs{last: map[string]time.Time{}}
	e.r.Logs = logs
	e.reconcile("x")
	var pod corev1.Pod
	if !e.exists(&pod, "x") {
		t.Fatal("expected pod")
	}
	// The fake client stamps the wall clock; stallOf counts from the later
	// of run start and pod creation.
	pod.CreationTimestamp = metav1.NewTime(t0)
	if err := e.c.Update(e.ctx, &pod); err != nil {
		t.Fatal(err)
	}
	pod.Status.Phase = phase
	if err := e.c.Status().Update(e.ctx, &pod); err != nil {
		t.Fatal(err)
	}
	task := e.task("x")
	orig := task.DeepCopy()
	task.Status.Run.ActiveRunRequest = 1
	task.Status.Run.CurrentStep = "18 push"
	task.Status.Run.LastActivityTime = &metav1.Time{Time: t0}
	if err := e.c.Status().Patch(e.ctx, task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	return e, logs
}

func (e *taskEnv) stalled() *metav1.Condition {
	return meta.FindStatusCondition(e.task("x").Status.Conditions, aiv1.ConditionStalled)
}

func TestStallCheckSetsAndClearsStalled(t *testing.T) {
	e, logs := newRunningTask(t, corev1.PodRunning)
	logs.last[RunnerContainer] = t0.Add(time.Minute)
	logs.last[AgentContainer] = t0.Add(2 * time.Minute)

	e.now = t0.Add(45 * time.Minute)
	if res := e.reconcile("x"); res.RequeueAfter == 0 || res.RequeueAfter > aiv1.DefaultStallCheckInterval {
		t.Fatalf("a run in progress must be checked again within the interval, RequeueAfter=%v", res.RequeueAfter)
	}
	c := e.stalled()
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != aiv1.StallReasonSilent || c.Message != "no log line for 43m in step 18 push" {
		t.Fatalf("silent containers must be Stalled=True/Silent, got %+v", c)
	}
	if got := testutil.ToFloat64(taskStalled.WithLabelValues(e.task("x").Namespace, "x", aiv1.StallReasonSilent)); got != 1 {
		t.Fatalf("successful patch must set ai_operator_task_stalled, got %v", got)
	}
	if logs.calls != 2 {
		t.Fatalf("one check reads both containers once, got %d reads", logs.calls)
	}
	start := c.LastTransitionTime
	if !start.Time.Equal(e.now) {
		t.Fatalf("LastTransitionTime is the stall start, got %v", start)
	}
	if e.task("x").Status.Phase != aiv1.TaskPhaseRunning {
		t.Fatalf("a stall changes no phase, got %s", e.task("x").Status.Phase)
	}

	// The runner's status patches reconcile often: no reads within the interval.
	e.now = e.now.Add(4 * time.Minute)
	e.reconcile("x")
	if logs.calls != 2 {
		t.Fatalf("two reconciles within the interval must read once, got %d reads", logs.calls)
	}

	// Still silent one interval later: the stall start stays.
	e.now = e.now.Add(time.Minute)
	e.reconcile("x")
	if c := e.stalled(); logs.calls != 4 || c.Status != metav1.ConditionTrue || !c.LastTransitionTime.Equal(&start) {
		t.Fatalf("still stalled: want the stall start kept after a second check, got %+v, %d reads", c, logs.calls)
	}

	// A new line: Active on the next check.
	logs.last[AgentContainer] = e.now.Add(time.Minute)
	e.now = e.now.Add(5 * time.Minute)
	e.reconcile("x")
	if c := e.stalled(); c == nil || c.Status != metav1.ConditionFalse || c.Reason != aiv1.StallReasonActive {
		t.Fatalf("a new log line must turn Stalled to False/Active, got %+v", c)
	}
	if taskStalled.DeleteLabelValues(e.task("x").Namespace, "x", aiv1.StallReasonSilent) {
		t.Fatal("Stalled=False after a successful patch must clear the gauge")
	}

	// The run ends: the condition is gone.
	e.finishRun("x")
	e.reconcile("x")
	if c := e.stalled(); c != nil {
		t.Fatalf("Stalled must be removed when the run ends, got %+v", c)
	}
}

func TestStallCheckSkipsYoungRuns(t *testing.T) {
	e, logs := newRunningTask(t, corev1.PodRunning)
	e.now = t0.Add(29 * time.Minute)
	e.reconcile("x")
	if logs.calls != 0 || e.stalled() != nil {
		t.Fatalf("a run younger than stallAfter is never looked at, got %d reads, %+v", logs.calls, e.stalled())
	}
}

func TestStallCheckSkipsIdleTasks(t *testing.T) {
	e, logs := newRunningTask(t, corev1.PodRunning)
	e.finishRun("x")
	e.now = t0.Add(45 * time.Minute)
	e.reconcile("x")
	if logs.calls != 0 || e.stalled() != nil {
		t.Fatalf("a task without a run in progress is never looked at, got %d reads, %+v", logs.calls, e.stalled())
	}
}

func TestStallCheckPendingPodNeedsNoLogs(t *testing.T) {
	e, logs := newRunningTask(t, corev1.PodPending)
	logs.err = errors.New("container is waiting to start")
	e.now = t0.Add(45 * time.Minute)
	e.reconcile("x")
	if c := e.stalled(); c == nil || c.Status != metav1.ConditionTrue || c.Reason != aiv1.StallReasonPodPending {
		t.Fatalf("a pod pending past stallAfter must be Stalled=True/PodPending, got %+v", c)
	}
	if logs.calls != 0 {
		t.Fatalf("a pending pod has no logs to read, got %d reads", logs.calls)
	}
}

func TestStallCheckLogErrorKeepsTheCondition(t *testing.T) {
	e, logs := newRunningTask(t, corev1.PodRunning)
	logs.err = errors.New("the log API is down")
	e.now = t0.Add(45 * time.Minute)
	e.reconcile("x")
	if c := e.stalled(); c != nil {
		t.Fatalf("a failed log read must not set Stalled, got %+v", c)
	}
}

func TestStallCheckRestartStartsANewStall(t *testing.T) {
	e, logs := newRunningTask(t, corev1.PodRunning)
	e.now = t0.Add(45 * time.Minute)
	e.reconcile("x")
	first := e.stalled()
	if first == nil || first.Status != metav1.ConditionTrue {
		t.Fatalf("expected a stall, got %+v", first)
	}
	// Restarted: the runner starts the request again.
	restart := e.now.Add(time.Minute)
	task := e.task("x")
	orig := task.DeepCopy()
	task.Status.Run.LastActivityTime, task.Status.Run.Interruptions = &metav1.Time{Time: restart}, 1
	if err := e.c.Status().Patch(e.ctx, task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	e.now = restart.Add(time.Minute)
	e.reconcile("x")
	if c := e.stalled(); c != nil {
		t.Fatalf("a stall found before the run started again is gone, got %+v", c)
	}
	// Silent again past stallAfter: a new stall with its own start.
	logs.last[RunnerContainer] = restart
	e.now = restart.Add(40 * time.Minute)
	e.reconcile("x")
	if c := e.stalled(); c == nil || c.Status != metav1.ConditionTrue || !c.LastTransitionTime.Time.Equal(e.now) {
		t.Fatalf("the new attempt's stall starts now, got %+v", c)
	}
}
