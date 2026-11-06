package controller

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// planRun plays a finished planning run of the spec task with result.
func planRun(e *projectEnv, plan []aiv1.PlannedIssue, result aiv1.RunResult) {
	e.t.Helper()
	replan(e, plan)
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) { s.Run.LastResult = result })
}

const emptyPlanReason = "the planning run succeeded but planned no issues"

// R11: a succeeded planning run with no issues pauses with emptyPlan, once;
// a changed spec lifts it, an empty re-plan pauses again, and a plan with
// issues starts the queue.
func TestEmptyPlanPauses(t *testing.T) {
	e := newUnplannedAutopilotEnv(t)
	e.reconcile()
	tracking := e.autopilot().TrackingIssueIID
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || countComments(e, tracking, "Autopilot paused") != 0 {
		t.Fatalf("no pause while the planning run is pending: %+v", ap)
	}

	planRun(e, []aiv1.PlannedIssue{}, aiv1.RunResultSucceeded)
	e.reconcile()
	e.reconcile()
	ap := e.autopilot()
	if ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseEmptyPlan || ap.PausedFor != 0 || ap.PausedReason != emptyPlanReason {
		t.Fatalf("an empty succeeded plan pauses with emptyPlan: %+v", ap)
	}
	if n := countComments(e, tracking, "⏸️ Autopilot paused: "+emptyPlanReason+"."); n != 1 {
		t.Fatalf("one pause comment, not %d: %v", n, botComments(e, tracking))
	}
	for _, way := range []string{
		"- By itself, once the milestone is planned again and the plan has issues: autopilot starts with the new plan.",
		"- In GitLab, change the milestone description so the planning agent plans issues: the milestone is planned again.",
	} {
		if countComments(e, tracking, way) != 1 {
			t.Fatalf("the pause comment lists %q: %v", way, botComments(e, tracking))
		}
	}
	if d := e.gl.Issues[tracking].Description; !strings.Contains(d, "⏸️ paused, "+emptyPlanReason+". Change the milestone description to plan it again.") {
		t.Fatalf("the tracking issue names the way out: %s", d)
	}

	// The owner changes the spec: the pending re-plan lifts the pause, and
	// the milestone stays unpaused while it runs.
	e.gl.Milestones[0].Description += "\nAlso a receipt page."
	e.reconcile()
	e.reconcile()
	if !e.task(specTaskName).RunPending() {
		t.Fatal("the edit plans the milestone again")
	}
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || ap.PauseKind != "" || ap.ResumedBy != aiv1.ResolvedByOperator {
		t.Fatalf("a pending re-plan lifts the pause: %+v", ap)
	}
	if n := countComments(e, tracking, "▶️ Autopilot continues: the milestone is being planned again (ended by the operator: the cause went away)."); n != 1 {
		t.Fatalf("one resume comment, not %d: %v", n, botComments(e, tracking))
	}

	// An empty re-plan pauses again.
	planRun(e, []aiv1.PlannedIssue{}, aiv1.RunResultSucceeded)
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseEmptyPlan {
		t.Fatalf("an empty re-plan pauses again: %+v", ap)
	}
	if n := countComments(e, tracking, "⏸️ Autopilot paused: "+emptyPlanReason); n != 2 {
		t.Fatalf("a second pause comment, not %d: %v", n, botComments(e, tracking))
	}

	// A plan with issues (recorded before autopilot saw the run pending)
	// lifts the pause and starts the queue.
	planRun(e, []aiv1.PlannedIssue{{Key: "ui", IID: 2, Title: "UI", DependsOn: []int64{1}}, {Key: "api", IID: 1, Title: "API"}}, aiv1.RunResultSucceeded)
	e.reconcile()
	ap = e.autopilot()
	if ap.Phase != aiv1.AutopilotRunning || len(ap.Items) != 2 || ap.Items[0].IID != 1 || ap.Items[0].State != aiv1.AutopilotItemActive {
		t.Fatalf("the new plan starts the queue: %+v", ap)
	}
	if countComments(e, tracking, "▶️ Autopilot continues: the milestone was planned again with 2 issues (ended by the operator: the cause went away).") != 1 {
		t.Fatalf("the resume says the plan has issues: %v", botComments(e, tracking))
	}
}

// A failed planning run, and a spec task that never ran, do not pause with
// emptyPlan.
func TestEmptyPlanNoPause(t *testing.T) {
	t.Run("never ran", func(t *testing.T) {
		// No run requested yet (planning waits for its turn), nothing
		// pending. No GitLab: a pause would comment and fail.
		st := &aiv1.AgentTask{}
		st.Status.Run.LastResult = aiv1.RunResultSucceeded
		a := &autopilot{st: st, ap: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}, log: logr.Discard()}
		if err := a.emptyPlan(context.Background()); err != nil || a.ap.Phase != aiv1.AutopilotRunning {
			t.Fatalf("a spec task that never ran does not pause: %v %+v", err, a.ap)
		}
	})
	t.Run("failed", func(t *testing.T) {
		e := newUnplannedAutopilotEnv(t)
		planRun(e, []aiv1.PlannedIssue{}, aiv1.RunResultFailed)
		e.reconcile()
		e.reconcile()
		if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || ap.PauseKind != "" {
			t.Fatalf("a failed planning run does not pause with emptyPlan: %+v", ap)
		}
	})
}

// An owner's pause is not replaced by emptyPlan.
func TestEmptyPlanKeepsAnOwnerPause(t *testing.T) {
	e := newUnplannedAutopilotEnv(t)
	e.reconcile()
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddNote(tracking, gitlab.User{ID: 1, Username: "jonathan"}, "/agent pause")
	e.reconcile()
	planRun(e, []aiv1.PlannedIssue{}, aiv1.RunResultSucceeded)
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseOwner {
		t.Fatalf("the owner's pause stays: %+v", ap)
	}
}
