package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
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

// replan plays a finished planning run of the spec task: plan is what it
// recorded, and the run request is observed.
func replan(e *projectEnv, plan []aiv1.PlannedIssue) {
	e.t.Helper()
	st := e.task(specTaskName)
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Plan = plan
		s.Run.ObservedRunRequest = st.Spec.RunRequest
	})
}

// notDone fails unless autopilot of the env's milestone is still undone:
// no done comment, tracking issue and milestone open, no retrospective.
func notDone(e *projectEnv, tracking int64) {
	e.t.Helper()
	if ap := e.autopilot(); ap.Phase == aiv1.AutopilotDone {
		e.t.Fatalf("an empty queue is not done: %+v", ap)
	}
	if n := countComments(e, tracking, "Autopilot is done"); n != 0 {
		e.t.Fatalf("no done comment: %v", botComments(e, tracking))
	}
	if is := e.gl.Issues[tracking]; is == nil || is.State == "closed" {
		e.t.Fatalf("the tracking issue stays open: %+v", is)
	}
	if e.gl.Milestones[0].State == "closed" {
		e.t.Fatal("the milestone stays open")
	}
	if coord, ok := e.tasks()[pilotTaskName]; ok && coord.Spec.Source.Retrospective != 0 {
		e.t.Fatalf("no retrospective: %+v", coord.Spec.Source)
	}
}

// Bloop %20: a planned milestone, nothing started yet, whose owner edits the
// description. The re-plan records an empty plan, so the queue drops both
// pending issues, and an accepted endMilestone then reached done with 0 of 0.
func TestEmptyQueueIsNeverDone(t *testing.T) {
	e := newUnplannedAutopilotEnv(t)
	e.gl.Users = []gitlab.User{alice, mallory}
	e.gl.Milestones[0].Description += "\n/agent owners @alice"
	e.reconcile()
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddNote(tracking, alice, "/agent pause")
	e.reconcile()
	replan(e, []aiv1.PlannedIssue{{Key: "ui", IID: 2, Title: "UI", DependsOn: []int64{1}}, {Key: "api", IID: 1, Title: "API"}})
	e.reconcile()
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || len(ap.Items) != 2 || ap.Items[0].State != aiv1.AutopilotItemPending {
		t.Fatalf("planned and paused before anything started: %+v", ap)
	}
	if _, ok := e.tasks()[pilotTaskName]; !ok {
		t.Fatalf("the milestone has a pilot: %v", keys(e.tasks()))
	}

	e.gl.Milestones[0].Description += "\nAlso a receipt page."
	e.reconcile()
	if !e.task(specTaskName).RunPending() {
		t.Fatal("the edit plans the milestone again")
	}
	replan(e, []aiv1.PlannedIssue{})
	if err := e.r.queueCommand(e.ctx, e.task(specTaskName), aiv1.PilotCommand{Action: aiv1.PilotAction{Tool: "endMilestone"}, Owner: "alice"}); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	e.reconcile()
	e.reconcile()
	notDone(e, tracking)
	if ap := e.autopilot(); len(ap.Items) != 0 || ap.Phase != aiv1.AutopilotPaused {
		t.Fatalf("the empty plan dropped the pending issues, the pause stays: %+v", ap)
	}
	if countComments(e, tracking, "Nothing to end (@alice via the pilot): nothing is planned in this milestone") != 1 ||
		countComments(e, tracking, "close the milestone in GitLab or remove `/agent autopilot`") != 1 {
		t.Fatalf("endMilestone says there is nothing to end, once: %v", botComments(e, tracking))
	}

	// Planned again with both issues, the queue goes on.
	e.gl.Milestones[0].Description += "\nAnd a receipt mail."
	e.reconcile()
	replan(e, []aiv1.PlannedIssue{{Key: "ui", IID: 2, Title: "UI", DependsOn: []int64{1}}, {Key: "api", IID: 1, Title: "API"}})
	e.reconcile()
	if ap := e.autopilot(); len(ap.Items) != 2 {
		t.Fatalf("the new plan fills the queue again: %+v", ap)
	}
	notDone(e, tracking)
}

func TestEndMilestoneBeforeThePlan(t *testing.T) {
	e := newUnplannedAutopilotEnv(t)
	e.gl.Users = []gitlab.User{alice}
	e.gl.Milestones[0].Description += "\n/agent owners @alice"
	e.reconcile()
	tracking := e.autopilot().TrackingIssueIID
	if err := e.r.queueCommand(e.ctx, e.task(specTaskName), aiv1.PilotCommand{Action: aiv1.PilotAction{Tool: "endMilestone"}, Owner: "alice"}); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	notDone(e, tracking)
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || countComments(e, tracking, "Nothing to end") != 1 {
		t.Fatalf("a milestone still being planned keeps running: %+v\n%v", ap, botComments(e, tracking))
	}
}

func TestDoneWithAnEmptyQueueKeepsThePhase(t *testing.T) {
	for _, phase := range []aiv1.AutopilotPhase{aiv1.AutopilotRunning, aiv1.AutopilotPaused} {
		a := &autopilot{ap: &aiv1.AutopilotStatus{Phase: phase, TrackingIssueIID: 7}, log: logr.Discard()}
		// No GitLab: done must not close or comment anything.
		if err := a.done(context.Background()); err != nil || a.ap.Phase != phase {
			t.Fatalf("done with no items keeps %s: %v %+v", phase, err, a.ap)
		}
	}
}
