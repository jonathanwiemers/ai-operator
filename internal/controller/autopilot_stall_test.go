package controller

import (
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// stalledAtCap is a managed milestone whose active issue #1 is stalled in
// step 18 push for 50m, with the pilot's interventions on its head at the
// cap (2 of 2) and one interruption so far.
func stalledAtCap(t *testing.T) *projectEnv {
	t.Helper()
	e := newManagedEnv(t)
	e.stallRun()
	since := metav1.NewTime(e.gl.Clock.Add(-50 * time.Minute))
	e.patchStatus(e.taskOf(1), func(s *aiv1.AgentTaskStatus) { s.Run.CurrentStepSince, s.Run.Interruptions = &since, 1 })
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Pilot = &aiv1.PilotStatus{Interventions: map[string]aiv1.PilotIntervention{"1": {Head: "sha1", Count: 2}}}
	})
	return e
}

func TestAutopilotPausesAStalledRunPastThePilotsRestarts(t *testing.T) {
	e := stalledAtCap(t)
	e.reconcile()
	ap := e.autopilot()
	want := "#1's run is stalled again after 2 of 2 pilot interventions: no log line for 42m in step 18 push; step 18 push runs for 50m (interruption 1 of 3)"
	if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseStalled || ap.PausedFor != 1 || ap.PausedReason != want || !ap.ResumeWhenReady {
		t.Fatalf("past the pilot's restarts a stalled run pauses with kind stalled and its evidence:\n%+v", ap)
	}
	if c := trackingComments(e); !strings.Contains(c, "⏸️ Autopilot paused: "+want) || !strings.Contains(c, "the run logs again or ends") {
		t.Fatalf("the pause comment shows the evidence and the auto exit:\n%s", c)
	}
	// The stall goes on: the ages grow, the pause is not announced again.
	e.gl.Clock = e.gl.Clock.Add(10 * time.Minute)
	e.reconcile()
	if n := strings.Count(trackingComments(e), "Autopilot paused"); n != 1 || e.autopilot().PauseKind != aiv1.PauseStalled {
		t.Fatalf("a longer stall keeps the one pause: %d comments, %+v", n, e.autopilot())
	}
}

func TestAutopilotPausesAStalledRunAtMaxInterruptions(t *testing.T) {
	e := newManagedEnv(t)
	e.stallRun()
	e.patchStatus(e.taskOf(1), func(s *aiv1.AgentTaskStatus) { s.Run.Interruptions = aiv1.MaxInterruptions })
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseStalled ||
		!strings.HasPrefix(ap.PausedReason, "#1's run is stalled again after 0 of 2 pilot interventions") || !strings.HasSuffix(ap.PausedReason, "(interruption 3 of 3)") {
		t.Fatalf("a stalled run at MaxInterruptions pauses: %+v", ap)
	}
	// The pilot still has interventions, but its restart would fail the run.
	e.proposeFor(1, "event/"+aiv1.RunStalledEventPrefix+"1/1", aiv1.PilotAction{Tool: "restartRun", IssueIID: 1})
	e.reconcile()
	if !e.podOf1() || e.autopilot().PauseKind != aiv1.PauseStalled {
		t.Fatalf("at MaxInterruptions the pilot restarts nothing, the pause holds for the owners: pod %v, %+v", e.podOf1(), e.autopilot())
	}
	if c := e.pilot().Status.Pilot; c != nil && c.Interventions["1"].Count != 0 {
		t.Fatalf("a refused restart counts no intervention: %+v", c.Interventions)
	}
}

func TestAutopilotLeavesAStalledRunToThePilotWithInterventionsLeft(t *testing.T) {
	e := newManagedEnv(t)
	e.stallRun()
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		// Two on an older head do not count against this one.
		s.Pilot = &aiv1.PilotStatus{Interventions: map[string]aiv1.PilotIntervention{"1": {Head: "sha0", Count: 2}}}
	})
	e.reconcile()
	if ap := e.autopilot(); ap.Phase == aiv1.AutopilotPaused {
		t.Fatalf("with restarts left the pilot handles the stall: %+v", ap)
	}
}

func TestAutopilotStalledPauseEndsOnceTheRunMoves(t *testing.T) {
	e := stalledAtCap(t)
	e.reconcile()
	if e.autopilot().PauseKind != aiv1.PauseStalled {
		t.Fatalf("expected a stalled pause: %+v", e.autopilot())
	}
	// An owner's restart, or the run logs again: the check finds it moving.
	e.patchStatus(e.taskOf(1), func(s *aiv1.AgentTaskStatus) {
		meta.SetStatusCondition(&s.Conditions, metav1.Condition{Type: aiv1.ConditionStalled, Status: metav1.ConditionFalse,
			Reason: aiv1.StallReasonActive, Message: "the run is making progress"})
	})
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotRunning || !strings.Contains(trackingComments(e), "Autopilot continues: the run of #1 is no longer stalled") {
		t.Fatalf("the pause lifts by itself once the run moves: %+v\n%s", ap, trackingComments(e))
	}
}

func TestAutopilotStalledPauseOfAFailedRunTurnsIntoRunFailed(t *testing.T) {
	e := stalledAtCap(t)
	e.reconcile()
	// The run ended (failed after the interruptions): the gate judges it.
	e.patchStatus(e.taskOf(1), func(s *aiv1.AgentTaskStatus) {
		meta.RemoveStatusCondition(&s.Conditions, aiv1.ConditionStalled)
		s.Run.ObservedRunRequest, s.Run.CurrentStep, s.Run.CurrentStepSince = s.Run.ActiveRunRequest, "", nil
		s.Run.LastResult = aiv1.RunResultFailed
	})
	e.gl.SetLabels(1, "ai-generated", "agent::review")
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseRunFailed {
		t.Fatalf("a run that ended failed pauses as runFailed: %+v", ap)
	}
}

func TestStalledPastWithoutAPilot(t *testing.T) {
	task := &aiv1.AgentTask{}
	a := &autopilot{spec: aiv1.AgentProjectSpec{}.WithDefaults()}
	if got := a.stalledPast(1, task); got != "" {
		t.Fatalf("a run that is not stalled pauses nothing: %q", got)
	}
	meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{Type: aiv1.ConditionStalled, Status: metav1.ConditionTrue,
		Reason: aiv1.StallReasonPodPending, Message: "pod pending for 35m"})
	if got, want := a.stalledPast(1, task), "#1's run is stalled and no pilot restarts it: pod pending for 35m (interruption 0 of 3)"; got != want {
		t.Fatalf("without a pilot nobody restarts it, so it pauses at once:\n got %q\nwant %q", got, want)
	}
	if got := a.stalledPast(1, nil); got != "" {
		t.Fatalf("no task, no stall: %q", got)
	}
}
