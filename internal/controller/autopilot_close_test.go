package controller

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// doneAndClosed finishes the pilot env's autopilot by skipping both issues,
// then closes them by hand: nothing is open in the milestone.
func doneAndClosed(e *projectEnv) {
	e.t.Helper()
	finishAutopilot(e)
	e.reconcile()
	e.reconcile()
	if e.autopilot().Phase != aiv1.AutopilotDone {
		e.t.Fatalf("autopilot is done: %+v", e.autopilot())
	}
	e.gl.Issues[1].State = "closed"
	e.gl.Issues[2].State = "closed"
}

func countComments(e *projectEnv, iid int64, part string) int {
	n := 0
	for _, c := range botComments(e, iid) {
		if strings.Contains(c, part) {
			n++
		}
	}
	return n
}

// retroDone puts the pilot's retrospective on the pilot and lets the next
// poll copy it to the spec task and delete the pilot.
func retroDone(e *projectEnv, retro aiv1.PilotRetrospective) {
	e.t.Helper()
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) { s.Pilot = &aiv1.PilotStatus{Retrospective: retro.DeepCopy()} })
	e.reconcile()
	if _, ok := e.tasks()[pilotTaskName]; ok {
		e.t.Fatalf("the pilot goes after its retrospective: %v", keys(e.tasks()))
	}
}

func TestDoneAutopilotClosesItsMilestoneAfterTheRetrospective(t *testing.T) {
	e := newPilotEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	doneAndClosed(e)
	e.reconcile()
	if e.gl.Milestones[0].State == "closed" || countComments(e, tracking, "stays open") != 0 {
		t.Fatalf("the pilot still writes the retrospective: milestone %s\n%v", e.gl.Milestones[0].State, botComments(e, tracking))
	}

	retroDone(e, aiv1.PilotRetrospective{NoteID: 42})
	e.reconcile()
	if e.gl.Milestones[0].State != "closed" || countComments(e, tracking, "The milestone is closed") != 1 {
		t.Fatalf("the milestone closes once the retrospective is in: %s\n%v", e.gl.Milestones[0].State, botComments(e, tracking))
	}
	e.reconcile()
	if _, ok := e.tasks()[specTaskName]; ok {
		t.Fatalf("a closed milestone is no longer planned: %v", keys(e.tasks()))
	}
	if countComments(e, tracking, "The milestone is closed") != 1 {
		t.Fatalf("one comment: %v", botComments(e, tracking))
	}
}

func TestDoneAutopilotWaitsForTheFollowUps(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after func(e *projectEnv)
	}{
		{"all created", func(e *projectEnv) {
			e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) { s.Pilot.Retrospective.Created = map[string]int64{"1": 20} })
		}},
		{"a week later", func(e *projectEnv) { e.gl.Clock = e.gl.Clock.Add(followUpWindow) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newPilotEnv(t)
			doneAndClosed(e)
			posted := metav1.NewTime(e.gl.Clock)
			retroDone(e, aiv1.PilotRetrospective{NoteID: 42, PostedAt: &posted, FollowUps: []aiv1.PilotFollowUp{{Title: "Docs", Body: "Write them."}}})
			e.gl.Clock = e.gl.Clock.Add(time.Hour)
			e.reconcile()
			if e.gl.Milestones[0].State == "closed" {
				t.Fatal("the owners can still open the follow-ups")
			}
			tc.after(e)
			e.reconcile()
			if e.gl.Milestones[0].State != "closed" {
				t.Fatalf("the follow-ups are settled: milestone %s", e.gl.Milestones[0].State)
			}
		})
	}
}

func TestDoneAutopilotKeepsAMilestoneWithOpenIssues(t *testing.T) {
	e := newPilotEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	// No pilot: nothing to wait for but the issues.
	pilot := e.pilot()
	if err := e.c.Delete(e.ctx, &pilot); err != nil {
		t.Fatal(err)
	}
	finishAutopilot(e) // skipped, still open
	e.reconcile()
	e.reconcile()
	e.reconcile()
	const waits = "The milestone stays open while #1, #2 are open"
	if e.gl.Milestones[0].State == "closed" || countComments(e, tracking, waits) != 1 || e.autopilot().Phase != aiv1.AutopilotDone {
		t.Fatalf("skipped issues keep the milestone open, said once: %s\n%v", e.gl.Milestones[0].State, botComments(e, tracking))
	}

	e.gl.Issues[2].State = "closed"
	e.reconcile()
	e.reconcile()
	if countComments(e, tracking, "The milestone stays open while #1 is open") != 1 {
		t.Fatalf("a changed list is said again: %v", botComments(e, tracking))
	}
	e.gl.Issues[1].State = "closed"
	e.reconcile()
	if e.gl.Milestones[0].State != "closed" {
		t.Fatalf("closed once nothing is open: %s", e.gl.Milestones[0].State)
	}
}

func TestDoneAutopilotDoesNotCloseWhileReplanning(t *testing.T) {
	e := newPilotEnv(t)
	doneAndClosed(e)
	e.reconcile()
	retroDone(e, aiv1.PilotRetrospective{NoteID: 42})
	// The owner edits the milestone before the next poll: it is planned again.
	e.gl.Milestones[0].Description += "\nAlso a receipt page."
	e.reconcile()
	st := e.tasks()[specTaskName]
	if !st.RunPending() || e.gl.Milestones[0].State == "closed" {
		t.Fatalf("a milestone that is planned again stays open: milestone %s", e.gl.Milestones[0].State)
	}
}

func TestDoneAutopilotClosesAfterAFailedRetrospective(t *testing.T) {
	e := newPilotEnv(t)
	doneAndClosed(e)
	e.reconcile()
	retroDone(e, aiv1.PilotRetrospective{Failed: "the run ended without one", FollowUps: []aiv1.PilotFollowUp{{Title: "Docs"}}})
	e.reconcile()
	if e.gl.Milestones[0].State != "closed" {
		t.Fatalf("a failed retrospective has nothing to wait for: milestone %s", e.gl.Milestones[0].State)
	}
}
