package controller

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

func TestSpecRetryDueNeedsARunThatEnded(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	r := &AgentProjectReconciler{Now: func() time.Time { return now }}
	long := &metav1.Time{Time: now.Add(-24 * time.Hour)}

	// A spec task waiting for its turn: run request 0, its run status empty.
	waiting := &aiv1.AgentTask{}
	if r.specRetryDue(waiting, aiv1.DefaultInfraRetryAfter) {
		t.Fatal("a spec task that never ran has nothing to retry")
	}
	// Even with a run status that reads like an outside failure.
	waiting.Status.Run = aiv1.RunStatus{LastResult: aiv1.RunResultFailed, BlockReason: aiv1.BlockInfra, LastActivityTime: long}
	if r.specRetryDue(waiting, aiv1.DefaultInfraRetryAfter) {
		t.Fatal("a spec task that never ran is not retried")
	}

	// The same failure on a run that ended is retried.
	ran := waiting.DeepCopy()
	ran.Spec.RunRequest, ran.Status.Run.ObservedRunRequest = 1, 1
	if !r.specRetryDue(ran, aiv1.DefaultInfraRetryAfter) {
		t.Fatal("a planning run that failed for an outside reason is retried")
	}
}

func TestSpecTurnHonoursAnOwnerPause(t *testing.T) {
	paused := func(kind aiv1.PauseKind, startBy string) *aiv1.AgentTask {
		return &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{
			Phase: aiv1.AutopilotPaused, PauseKind: kind, StartRequestedBy: startBy}}}
	}
	for _, tc := range []struct {
		name  string
		task  *aiv1.AgentTask
		ahead []int64
		want  bool
	}{
		{"no status, nothing ahead", &aiv1.AgentTask{}, nil, true},
		{"no status, older open", &aiv1.AgentTask{}, []int64{7}, false},
		{"owner pause, nothing ahead", paused(aiv1.PauseOwner, ""), nil, false},
		{"owner pause, start", paused(aiv1.PauseOwner, "@alice"), nil, true},
		{"owner pause, start, older open", paused(aiv1.PauseOwner, "@alice"), []int64{7}, true},
		{"budget pause, nothing ahead", paused(aiv1.PauseBudget, ""), nil, true},
		{"running, nothing ahead", phaseTask(aiv1.AutopilotRunning), nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := specTurn(tc.task, tc.ahead); got != tc.want {
				t.Fatalf("specTurn = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPilotEventsSkipAWaitingMilestonesPause(t *testing.T) {
	spec := testProject().Spec.WithDefaults()
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	long := &metav1.Time{Time: now.Add(-48 * time.Hour)}
	for _, kind := range []aiv1.PauseKind{aiv1.PauseOwner, aiv1.PauseBudget} {
		st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Milestone: &aiv1.MilestoneRef{IID: 9}, Autopilot: true, QueuedBehind: []int64{7}},
			Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PauseKind: kind,
				PausedReason: "paused by @alice", PausedAt: long, StateChangedAt: long}}}
		if evs := pilotEvents(spec, nil, st, nil, nil, false, now); len(evs) != 0 {
			t.Fatalf("%s: a milestone whose planning never ran wakes no pilot: %+v", kind, evs)
		}

		// Once planned, the same pause is an event (and a budget one stalls).
		st.Spec.RunRequest, st.Spec.QueuedBehind, st.Status.Run.ObservedRunRequest = 1, nil, 1
		st.Status.Plan = []aiv1.PlannedIssue{{IID: 1}}
		want := 1
		if kind != aiv1.PauseOwner {
			want = 2
		}
		if evs := pilotEvents(spec, nil, st, nil, nil, false, now); len(evs) != want {
			t.Fatalf("%s: a planned milestone's pause wakes the pilot: %+v", kind, evs)
		}
	}
}

// pauseWaiting has alice pause the waiting %9 and checks it took.
func pauseWaiting(t *testing.T, e *projectEnv) int64 {
	t.Helper()
	tracking := e.trackingOf(9)
	e.gl.AddNote(tracking, alice, "/agent pause")
	e.reconcile()
	if ap := e.specFor(9).Status.Autopilot; ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseOwner {
		t.Fatalf("a pause on a waiting milestone is an owner's pause: %+v", ap)
	}
	return tracking
}

func TestOwnerPausedWaitingMilestoneStaysUnplannedWhenItsTurnComes(t *testing.T) {
	e := waitingBehindSeven(t)
	tracking := pauseWaiting(t, e)

	// %7 is done: %9's turn has come, but its owner paused it.
	e.setPhase(7, aiv1.AutopilotDone)
	for range 3 {
		e.reconcile()
	}
	e.wantWaiting(9)
	if ap := e.specFor(9).Status.Autopilot; ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseOwner {
		t.Fatalf("the turn does not lift the owner's pause: %+v", ap)
	}
	if body := e.gl.Issues[tracking].Description; !strings.Contains(body, "⏸️ paused, paused by @") {
		t.Fatalf("the tracking issue says it is paused:\n%s", body)
	}
	if p := e.pilotOf(9); p != nil && len(p.Spec.Source.PilotEvents) > 0 {
		t.Fatalf("the paused, never planned milestone wakes no pilot: %+v", p.Spec.Source.PilotEvents)
	}

	// resume: planning starts with the turn come.
	e.gl.AddNote(tracking, alice, "/agent resume")
	e.reconcile()
	e.reconcile()
	e.wantPlanning(9, rules.Spec("/agent owners @alice\n/agent autopilot\nSecond.", "/agent"))
	if got := e.specFor(9).Status.Autopilot.Phase; got == aiv1.AutopilotPaused {
		t.Fatalf("resume lifts the pause: %s", got)
	}
}

func TestResumeOfAWaitingMilestoneKeepsItsTurn(t *testing.T) {
	e := waitingBehindSeven(t)
	tracking := pauseWaiting(t, e)
	e.gl.AddNote(tracking, alice, "/agent resume")
	e.reconcile()
	e.reconcile()
	// %7 is still open: resume lifts the pause, the turn has not come.
	e.wantWaiting(9, 7)
}

func TestStartPlansAnOwnerPausedWaitingMilestoneAndKeepsThePause(t *testing.T) {
	e := waitingBehindSeven(t)
	tracking := pauseWaiting(t, e)
	e.gl.AddNote(tracking, alice, "/agent start")
	e.reconcile()
	e.reconcile()
	e.wantPlanning(9, rules.Spec("/agent owners @alice\n/agent autopilot\nSecond.", "/agent"))
	if ap := e.specFor(9).Status.Autopilot; ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseOwner || ap.StartRequestedBy != "@alice" {
		t.Fatalf("start plans, the pause stays until resume: %+v", ap)
	}
}

// pilotOf is the Coordinate task of milestone iid, if there is one.
func (e *projectEnv) pilotOf(iid int64) *aiv1.AgentTask {
	e.t.Helper()
	for _, task := range e.tasks() {
		if task.Spec.Type == aiv1.TaskTypeCoordinate && task.Spec.Milestone != nil && task.Spec.Milestone.IID == iid {
			return &task
		}
	}
	return nil
}
