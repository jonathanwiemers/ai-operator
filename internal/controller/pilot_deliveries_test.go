package controller

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// ownerMessagesOf sets the owner messages of issue iid's task, as the
// pilot's instruct leaves them.
func (e *projectEnv) ownerMessagesOf(iid int64, msgs ...aiv1.OwnerMessage) {
	e.t.Helper()
	var task aiv1.AgentTask
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: e.taskOf(iid)}, &task); err != nil {
		e.t.Fatal(err)
	}
	task.Spec.Source.OwnerMessages = msgs
	if err := e.c.Update(e.ctx, &task); err != nil {
		e.t.Fatal(err)
	}
}

var deliveredAt = metav1.NewTime(time.Date(2026, 11, 24, 14, 5, 30, 0, time.UTC))

func TestDescribeDelivery(t *testing.T) {
	calls := []aiv1.AgentCall{
		{Seq: 4, Kind: aiv1.CallKindImplement, StoppedForMessage: true},
		{Seq: 5, Kind: aiv1.CallKindImplement, Resumed: true},
		{Seq: 6, Kind: "review"},
		{Seq: 7, Kind: "ci"},
	}
	for _, c := range []struct {
		how     aiv1.DeliveryHow
		call    int64
		running bool
		want    string
	}{
		{aiv1.DeliveryStream, 3, true, "delivered to #712's running implementer at 2026-11-24 14:05 UTC"},
		{aiv1.DeliveryResume, 5, true, "delivered to #712's implementer, the step restarted with it, at 2026-11-24 14:05 UTC"},
		{aiv1.DeliveryResume, 7, true, "delivered with #712's ci call at 2026-11-24 14:05 UTC"},
		{aiv1.DeliveryResume, 8, true, ""}, // its call is not recorded yet
		{aiv1.DeliveryResume, 9, false, "delivered with #712's next implementer call at 2026-11-24 14:05 UTC"},
		{aiv1.DeliveryNextRun, 9, false, "delivered with #712's next run at 2026-11-24 14:05 UTC"},
	} {
		d := aiv1.DeliveredMessage{ID: "tell/1", Call: c.call, How: c.how, At: deliveredAt}
		if got := describeDelivery(712, d, calls, c.running); got != c.want {
			t.Errorf("%s into %d: %q, want %q", c.how, c.call, got, c.want)
		}
	}
}

func TestPilotReportsADeliveryOnce(t *testing.T) {
	e := newManagedEnv(t)
	e.runningOf(1)
	e.ownerMessagesOf(1, aiv1.OwnerMessage{ID: "tell/55", Owner: "alice", Text: "use the v2 API", At: deliveredAt},
		aiv1.OwnerMessage{ID: "chat/3", Owner: "alice", Text: "and log it", At: deliveredAt})
	e.patchStatus(e.taskOf(1), func(s *aiv1.AgentTaskStatus) {
		s.Run.DeliveredMessages = []aiv1.DeliveredMessage{{ID: "tell/55", Call: 3, How: aiv1.DeliveryStream, At: deliveredAt}}
	})
	before := e.implTaskOf(1).Spec.RunRequest
	const where = "delivered to #1's running implementer at 2026-11-24 14:05 UTC"
	for range 2 {
		e.reconcile()
	}
	want := "🧭 Message from `@alice` via the pilot (use the v2 API): " + where
	if n, m := countComments(e, e.tracking(), want), countComments(e, 1, want); n != 1 || m != 1 {
		t.Fatalf("one line on the tracking issue (%d) and on the issue (%d): %q", n, m, botComments(e, 1))
	}
	n := 0
	for _, a := range e.pilot().Status.Pilot.Acted {
		if strings.Contains(a.Summary, where) {
			n++
			if a.By != "alice" || len(a.Lines) != 1 || !strings.HasSuffix(a.Lines[0], "use the v2 API; "+where) || len(a.Issues) != 1 || a.Issues[0] != 1 {
				t.Fatalf("the acted entry: %+v", a)
			}
		}
	}
	if n != 1 {
		t.Fatalf("one acted entry: %+v", e.pilot().Status.Pilot.Acted)
	}
	task := e.implTaskOf(1)
	if msgs := task.Spec.Source.OwnerMessages; len(msgs) != 1 || msgs[0].ID != "chat/3" {
		t.Fatalf("the reported message leaves, the pending one stays: %+v", msgs)
	}
	if task.Spec.RunRequest != before {
		t.Fatalf("no run is requested while one is in progress: %d → %d", before, task.Spec.RunRequest)
	}
}

func TestPilotRequestsOneRunForALeftoverMessage(t *testing.T) {
	e := newManagedEnv(t)
	now := time.Now()
	e.patchStatus(e.taskOf(1), func(s *aiv1.AgentTaskStatus) {
		s.Run.RunCount = 2
		s.Run.Calls = []aiv1.AgentCall{{Seq: 1, Run: 2, Kind: aiv1.CallKindImplement, At: metav1.NewTime(now.Add(-10 * time.Minute))}}
	})
	e.ownerMessagesOf(1, aiv1.OwnerMessage{ID: "tell/9", Owner: "alice", Text: "use the v2 API", At: metav1.NewTime(now.Add(-5 * time.Minute))})
	before := e.implTaskOf(1).Spec.RunRequest
	e.reconcile()
	if got := e.implTaskOf(1).Spec.RunRequest; got != before+1 {
		t.Fatalf("the message that came during the ended run gets a run: %d → %d", before, got)
	}
	e.reconcile()
	if got := e.implTaskOf(1).Spec.RunRequest; got != before+1 {
		t.Fatalf("a requested run is not requested again: %d", got)
	}
	// The run took no implementer call; it started after the message.
	e.patchStatus(e.taskOf(1), func(s *aiv1.AgentTaskStatus) {
		s.Run.ActiveRunRequest, s.Run.ObservedRunRequest, s.Run.RunCount = before+1, before+1, 3
		s.Run.Calls = append(s.Run.Calls, aiv1.AgentCall{Seq: 2, Run: 3, Kind: "review", At: metav1.NewTime(now)})
	})
	e.reconcile()
	if task := e.implTaskOf(1); task.Spec.RunRequest != before+1 || len(task.PendingOwnerMessages()) != 1 {
		t.Fatalf("no loop: run %d, pending %+v", task.Spec.RunRequest, task.PendingOwnerMessages())
	}
}

func TestLeftover(t *testing.T) {
	at := time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC)
	task := func(mutate func(*aiv1.AgentTask)) *aiv1.AgentTask {
		t := &aiv1.AgentTask{}
		t.Spec.RunRequest, t.Status.Run.ObservedRunRequest, t.Status.Run.ActiveRunRequest, t.Status.Run.RunCount = 4, 4, 4, 2
		t.Spec.Source.OwnerMessages = []aiv1.OwnerMessage{{ID: "tell/1", At: metav1.NewTime(at)}}
		t.Status.Run.Calls = []aiv1.AgentCall{{Seq: 1, Run: 1, At: metav1.NewTime(at.Add(-time.Hour))}, {Seq: 2, Run: 2, At: metav1.NewTime(at.Add(-time.Minute))}}
		if mutate != nil {
			mutate(t)
		}
		return t
	}
	for name, c := range map[string]struct {
		t    *aiv1.AgentTask
		want bool
	}{
		"after its run":   {task(nil), true},
		"running":         {task(func(t *aiv1.AgentTask) { t.Status.Run.ActiveRunRequest = 5 }), false},
		"requested":       {task(func(t *aiv1.AgentTask) { t.Spec.RunRequest = 5 }), false},
		"no calls":        {task(func(t *aiv1.AgentTask) { t.Status.Run.Calls = nil }), false},
		"run after it":    {task(func(t *aiv1.AgentTask) { t.Status.Run.Calls[1].At = metav1.NewTime(at.Add(time.Minute)) }), false},
		"delivered":       {task(func(t *aiv1.AgentTask) { t.Status.Run.DeliveredMessages = []aiv1.DeliveredMessage{{ID: "tell/1"}} }), false},
		"nothing pending": {task(func(t *aiv1.AgentTask) { t.Spec.Source.OwnerMessages = nil }), false},
	} {
		if got := leftover(c.t); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}
