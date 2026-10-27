package controller

import (
	"reflect"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// finishAutopilot skips both issues of the pilot env's queue: autopilot is
// done with the next poll.
func finishAutopilot(e *projectEnv) {
	e.t.Helper()
	tracking := e.autopilot().TrackingIssueIID
	e.gl.AddNote(tracking, alice, "/agent skip #1")
	e.gl.AddNote(tracking, alice, "/agent skip #2")
}

func TestRetrospectiveBeforeThePilotGoes(t *testing.T) {
	e := newPilotEnv(t)
	before := e.pilot().Spec.RunRequest
	finishAutopilot(e)
	e.reconcile()
	e.reconcile()
	if e.autopilot().Phase != aiv1.AutopilotDone {
		t.Fatalf("autopilot is done: %+v", e.autopilot())
	}
	pilot, ok := e.tasks()[pilotTaskName]
	if !ok || pilot.Spec.RunRequest != before+1 || pilot.Spec.Source.Retrospective != pilot.Spec.RunRequest {
		t.Fatalf("the pilot stays for one retrospective run: %v %+v", keys(e.tasks()), pilot.Spec)
	}

	// The run posted it: it goes to the spec task, then the pilot goes.
	retro := aiv1.PilotRetrospective{NoteID: 42, FollowUps: []aiv1.PilotFollowUp{{Title: "Docs", Body: "Write them."}}, OwnerIDs: []int64{alice.ID}}
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) { s.Pilot = &aiv1.PilotStatus{Retrospective: retro.DeepCopy()} })
	e.reconcile()
	if _, ok := e.tasks()[pilotTaskName]; ok {
		t.Fatalf("the pilot goes after its retrospective: %v", keys(e.tasks()))
	}
	if ps := e.tasks()[specTaskName].Status.Pilot; ps == nil || !reflect.DeepEqual(ps.Retrospective, &retro) {
		t.Fatalf("the retrospective outlives the pilot on the spec task: %+v", ps)
	}

	// A re-plan brings the pilot back; done again, it gets no second one.
	mc := e.gl.Milestones[0]
	docs := e.gl.AddIssue(10, "Docs", "ai-generated", "agent::draft")
	docs.Milestone, docs.Description = &mc, "Write it.\n<!-- ai-operator:key=docs -->"
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Plan = append(s.Plan, aiv1.PlannedIssue{Key: "docs", IID: 10, Title: "Docs"})
	})
	e.reconcile()
	e.reconcile()
	if _, ok := e.tasks()[pilotTaskName]; !ok {
		t.Fatalf("the pilot comes back with the re-plan: %v", keys(e.tasks()))
	}
	e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/agent skip #10")
	e.reconcile()
	if _, ok := e.tasks()[pilotTaskName]; ok || e.autopilot().Phase != aiv1.AutopilotDone {
		t.Fatalf("a milestone gets one retrospective: %v %+v", keys(e.tasks()), e.autopilot())
	}
}

func TestRetrospectiveThatFailedIsNotTriedAgain(t *testing.T) {
	e := newPilotEnv(t)
	finishAutopilot(e)
	e.reconcile()
	pilot := e.pilot()
	request := pilot.Spec.Source.Retrospective
	if request == 0 {
		t.Fatalf("the retrospective is asked for: %+v", pilot.Spec)
	}
	// Still running.
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) { s.Run.ObservedRunRequest = request - 1 })
	e.reconcile()
	if p := e.pilot(); p.Spec.Source.Retrospective != request || p.Spec.RunRequest != request {
		t.Fatalf("the retrospective is asked for once: %+v", p.Spec)
	}
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Run.ObservedRunRequest, s.Run.LastResult, s.Run.Summary = request, aiv1.RunResultFailed, "the engine crashed"
	})
	e.reconcile()
	e.reconcile()
	if _, ok := e.tasks()[pilotTaskName]; ok {
		t.Fatalf("a failed retrospective does not keep the pilot: %v", keys(e.tasks()))
	}
	if ps := e.tasks()[specTaskName].Status.Pilot; ps == nil || ps.Retrospective == nil || !strings.Contains(ps.Retrospective.Failed, "the engine crashed") {
		t.Fatalf("the failure is recorded: %+v", ps)
	}
}

func TestNoRetrospectiveWithoutAPilot(t *testing.T) {
	e := newPilotEnv(t)
	// Done before this release: its pilot was deleted already.
	pilot := e.pilot()
	if err := e.c.Delete(e.ctx, &pilot); err != nil {
		t.Fatal(err)
	}
	finishAutopilot(e)
	e.reconcile()
	e.reconcile()
	if _, ok := e.tasks()[pilotTaskName]; ok || e.autopilot().Phase != aiv1.AutopilotDone {
		t.Fatalf("no pilot comes back for a retrospective: %v %+v", keys(e.tasks()), e.autopilot())
	}
	if ps := e.tasks()[specTaskName].Status.Pilot; ps != nil && ps.Retrospective != nil {
		t.Fatalf("no retrospective without a pilot: %+v", ps.Retrospective)
	}
}
