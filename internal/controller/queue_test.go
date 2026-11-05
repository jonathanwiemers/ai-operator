package controller

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// queueTask is an implement task of milestone ms (0: none) for issue iid.
func queueTask(name string, ms, iid int64, phase aiv1.TaskPhase) *aiv1.AgentTask {
	t := newImplTask(name, 1)
	t.Spec.Issue = &aiv1.IssueRef{IID: iid}
	if ms != 0 {
		t.Spec.Milestone = &aiv1.MilestoneRef{IID: ms}
	}
	t.Status.Phase = phase
	return t
}

// holder is a task that holds the only slot.
func holder() *aiv1.AgentTask { return queueTask("hold", 1, 1, aiv1.TaskPhaseRunning) }

func (e *taskEnv) hasPod(name string) bool {
	e.t.Helper()
	var pod corev1.Pod
	return e.exists(&pod, name)
}

func (e *taskEnv) setPhase(name string, phase aiv1.TaskPhase) {
	e.t.Helper()
	task := e.task(name)
	orig := task.DeepCopy()
	task.Status.Phase = phase
	if err := e.c.Status().Patch(e.ctx, task, client.MergeFrom(orig)); err != nil {
		e.t.Fatal(err)
	}
}

func TestQueuedTasksStartOldestMilestoneFirst(t *testing.T) {
	e := newTaskEnv(t, holder(), queueTask("m9", 9, 90, ""), queueTask("m6", 6, 60, ""))
	e.reconcile("m9")
	e.reconcile("m6")
	e.setPhase("hold", aiv1.TaskPhaseIdle)

	// The younger milestone reconciles first, but waits for the older one.
	e.reconcile("m9")
	if e.hasPod("m9") || e.task("m9").Status.Phase != aiv1.TaskPhaseQueued {
		t.Fatalf("m9 must wait for m6, phase %s", e.task("m9").Status.Phase)
	}
	e.reconcile("m6")
	if !e.hasPod("m6") {
		t.Fatal("m6 must start")
	}
	if got := e.task("m9").Status.Message; got != "waiting for capacity (0/1 runs in flight), 2nd in line behind #60 (milestone %6)" {
		t.Fatalf("message %q", got)
	}
}

func TestNewYoungerTaskDoesNotOvertakeQueuedOlderOne(t *testing.T) {
	e := newTaskEnv(t, queueTask("m6", 6, 60, aiv1.TaskPhaseQueued), queueTask("m9", 9, 90, ""))
	res := e.reconcile("m9") // a slot is free
	if e.hasPod("m9") || e.task("m9").Status.Phase != aiv1.TaskPhaseQueued || res.RequeueAfter != queueRetry {
		t.Fatalf("m9 must queue behind m6, phase %s", e.task("m9").Status.Phase)
	}
	// It keeps its place by rank: m6 goes first, then m9.
	e.reconcile("m6")
	if !e.hasPod("m6") {
		t.Fatal("m6 must start")
	}
}

func TestNewOlderTaskGoesAheadOfQueuedYoungerOne(t *testing.T) {
	e := newTaskEnv(t, queueTask("m9", 9, 90, aiv1.TaskPhaseQueued), queueTask("m6", 6, 60, ""))
	e.reconcile("m6")
	if !e.hasPod("m6") {
		t.Fatal("m6 must start with a free slot")
	}
	e.reconcile("m9")
	if e.hasPod("m9") {
		t.Fatal("m9 waits: the slot is taken")
	}
}

func TestTaskWithoutMilestoneAndUrgentTaskGoAhead(t *testing.T) {
	urgent := queueTask("fix", 0, 99, aiv1.TaskPhaseQueued)
	urgent.Spec.QueueLane = aiv1.QueueLaneUrgent
	e := newTaskEnv(t, queueTask("m6", 6, 60, aiv1.TaskPhaseQueued), queueTask("hand", 0, 70, aiv1.TaskPhaseQueued), urgent)
	e.reconcile("hand")
	if e.hasPod("hand") {
		t.Fatal("the urgent fix is ahead of a hand-labelled issue")
	}
	e.reconcile("m6")
	if e.hasPod("m6") {
		t.Fatal("a hand-labelled issue is ahead of a milestone task")
	}
	e.reconcile("fix")
	if !e.hasPod("fix") {
		t.Fatal("the urgent fix starts")
	}
}

func TestQueuedTaskThatCannotRunHoldsNobodyBack(t *testing.T) {
	suspended := queueTask("suspended", 1, 10, aiv1.TaskPhaseQueued)
	suspended.Spec.Suspend = true
	deferred := queueTask("deferred", 2, 20, aiv1.TaskPhaseQueued)
	deferred.Status.Run.DeferredUntil = &metav1.Time{Time: t0.Add(time.Hour)}
	budget := queueTask("budget", 3, 30, aiv1.TaskPhaseQueued)
	budget.Spec.BudgetStop = true
	done := queueTask("done", 4, 40, aiv1.TaskPhaseQueued)
	done.Spec.RunRequest = 1
	done.Status.Run.ObservedRunRequest = 1
	e := newTaskEnv(t, suspended, deferred, budget, done, queueTask("m9", 9, 90, ""))
	e.reconcile("m9")
	if !e.hasPod("m9") {
		t.Fatalf("m9 uses the free slot, phase %s: %s", e.task("m9").Status.Phase, e.task("m9").Status.Message)
	}
}

func TestWantsSlot(t *testing.T) {
	deferred := queueTask("a", 6, 1, aiv1.TaskPhaseQueued)
	deferred.Status.Run.DeferredUntil = &metav1.Time{Time: t0.Add(time.Hour)}
	if wantsSlot(deferred, t0, 20) {
		t.Error("a queued task whose run is deferred takes no slot")
	}
	if !wantsSlot(deferred, t0.Add(2*time.Hour), 20) {
		t.Error("a deferred run that is due wants its slot")
	}
	if !wantsSlot(queueTask("b", 6, 2, aiv1.TaskPhaseQueued), t0, 20) {
		t.Error("a pending run wants a slot")
	}
	spent := queueTask("c", 6, 3, aiv1.TaskPhaseQueued)
	spent.Status.Run.RunCount = 20
	if wantsSlot(spent, t0, 20) {
		t.Error("a task at its run limit is blocked")
	}
	if !wantsSlot(queuedChat("d", 6, 4, aiv1.TaskPhaseQueued), t0, 20) {
		t.Error("a chat turn wants a slot")
	}
}

// queuedChat has an unanswered chat turn and no pending run.
func queuedChat(name string, ms, iid int64, phase aiv1.TaskPhase) *aiv1.AgentTask {
	task := queueTask(name, ms, iid, phase)
	task.Status.Run.ObservedRunRequest = task.Spec.RunRequest
	task.Spec.Source.ChatMessages = []aiv1.ChatMessage{{Kind: "message"}}
	return task
}

func TestChatTurnNeedsAFreeSlotOnly(t *testing.T) {
	e := newTaskEnv(t, queuedChat("m9", 9, 90, ""), queueTask("m6", 6, 60, aiv1.TaskPhaseQueued))
	e.reconcile("m9")
	if !e.hasPod("m9") {
		t.Fatal("a chat turn does not wait for higher ranks")
	}

	e = newTaskEnv(t, holder(), queuedChat("m9", 9, 90, ""))
	e.reconcile("m9")
	if e.hasPod("m9") || e.task("m9").Status.Phase != aiv1.TaskPhaseQueued {
		t.Fatal("a chat turn waits for a free slot")
	}
}

func TestRunThatLostItsPodKeepsItsPlace(t *testing.T) {
	lost := queueTask("m9", 9, 90, aiv1.TaskPhaseRunning)
	lost.Status.Run.ActiveRunRequest = lost.Spec.RunRequest
	e := newTaskEnv(t, lost, queueTask("m6", 6, 60, aiv1.TaskPhaseQueued))
	e.reconcile("m9")
	if !e.hasPod("m9") {
		t.Fatal("a run in progress goes on before a queued higher rank")
	}
}

func TestQueuedTaskThatFailsToStartLeavesTheQueue(t *testing.T) {
	stray := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "m6", Namespace: ns}}
	e := newTaskEnv(t, queueTask("m6", 6, 60, aiv1.TaskPhaseQueued), queueTask("m9", 9, 90, ""), stray)
	if _, err := e.r.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: "m6"}}); err == nil {
		t.Fatal("a pod the task does not own is an error")
	}
	if got := e.task("m6").Status.Phase; got != aiv1.TaskPhasePending {
		t.Fatalf("m6 is %s, want it out of the queue", got)
	}
	e.reconcile("m9")
	if !e.hasPod("m9") {
		t.Fatal("m9 must not wait for a task that cannot start")
	}
}

func TestQueueNamesTheTaskDirectlyAhead(t *testing.T) {
	// Listed by name, so a6 comes before z3: the rank must sort them.
	e := newTaskEnv(t, holder(), queueTask("a6", 6, 60, aiv1.TaskPhaseQueued), queueTask("z3", 3, 30, aiv1.TaskPhaseQueued), queueTask("m9", 9, 90, ""))
	e.reconcile("m9")
	if got := e.task("m9").Status.Message; got != "waiting for capacity (1/1 runs in flight), 3rd in line behind #60 (milestone %6)" {
		t.Fatalf("message %q", got)
	}
}

func TestTaskWithPodIsNotHeldBack(t *testing.T) {
	e := newTaskEnv(t, queueTask("m9", 9, 90, ""))
	e.reconcile("m9")
	if !e.hasPod("m9") {
		t.Fatal("m9 starts")
	}
	older := queueTask("m6", 6, 60, aiv1.TaskPhaseQueued)
	if err := e.c.Create(e.ctx, older); err != nil {
		t.Fatal(err)
	}
	e.reconcile("m9")
	if got := e.task("m9").Status.Phase; got != aiv1.TaskPhaseRunning || !e.hasPod("m9") {
		t.Fatalf("a run with a pod is never stopped for a higher rank, phase %s", got)
	}
}

func TestPilotIsNotHeldBackAndHoldsNobodyBack(t *testing.T) {
	pilot := func(name string, ms int64) *aiv1.AgentTask {
		p := queueTask(name, ms, 5, aiv1.TaskPhaseQueued)
		p.Labels[LabelType] = "coordinate"
		p.Spec.Type = aiv1.TaskTypeCoordinate
		return p
	}
	e := newTaskEnv(t, pilot("young", 9), pilot("old", 1), queueTask("m6", 6, 60, aiv1.TaskPhaseQueued))
	e.reconcile("young")
	if !e.hasPod("young") {
		t.Fatal("a pilot is outside the queue, m6 ranks ahead of it")
	}
	e.reconcile("m6")
	if !e.hasPod("m6") {
		t.Fatal("a queued pilot holds nobody back, old ranks ahead of m6")
	}
}

func TestCapacityTwoStartsTheTopTwo(t *testing.T) {
	e := newTaskEnv(t, queueTask("m6", 6, 60, aiv1.TaskPhaseQueued), queueTask("m7", 7, 70, aiv1.TaskPhaseQueued), queueTask("m9", 9, 90, aiv1.TaskPhaseQueued))
	var project aiv1.AgentProject
	if err := e.c.Get(e.ctx, client.ObjectKey{Namespace: ns, Name: "shop"}, &project); err != nil {
		t.Fatal(err)
	}
	project.Spec.MaxConcurrentRuns = 2
	if err := e.c.Update(e.ctx, &project); err != nil {
		t.Fatal(err)
	}
	e.reconcile("m9")
	if e.hasPod("m9") {
		t.Fatal("m9 is third")
	}
	e.reconcile("m7")
	e.reconcile("m6")
	if !e.hasPod("m7") || !e.hasPod("m6") {
		t.Fatal("the top two start together")
	}
	e.reconcile("m9")
	if e.hasPod("m9") {
		t.Fatal("both slots are taken")
	}
}

func TestQueueMessage(t *testing.T) {
	ms := func(iid, m int64) *aiv1.AgentTask { return queueTask("x", m, iid, "") }
	spec := queueTask("spec", 6, 0, "")
	spec.Spec.Issue = nil
	tests := []struct {
		ahead []*aiv1.AgentTask
		want  string
	}{
		{nil, "waiting for capacity (1/1 runs in flight), next in line"},
		{[]*aiv1.AgentTask{ms(140, 6)}, "waiting for capacity (1/1 runs in flight), 2nd in line behind #140 (milestone %6)"},
		{[]*aiv1.AgentTask{ms(1, 6), spec}, "waiting for capacity (1/1 runs in flight), 3rd in line behind milestone %6"},
		{[]*aiv1.AgentTask{ms(70, 0)}, "waiting for capacity (1/1 runs in flight), 2nd in line behind #70"},
	}
	for _, tt := range tests {
		if got := queueMessage(1, 1, tt.ahead); got != tt.want {
			t.Errorf("got %q, want %q", got, tt.want)
		}
	}
}
