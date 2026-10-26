package controller

import (
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// pilotEventKeys returns the keys of the events handed to the pilot, and
// its run request.
func (e *projectEnv) pilotEventKeys() ([]string, int64) {
	e.t.Helper()
	p := e.pilot()
	var keys []string
	for _, ev := range p.Spec.Source.PilotEvents {
		keys = append(keys, ev.Key)
	}
	return keys, p.Spec.RunRequest
}

// assessed plays the pilot's runner finishing the pending run request.
func (e *projectEnv) assessed() {
	e.t.Helper()
	request := e.pilot().Spec.RunRequest
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) { s.Run.ObservedRunRequest = request })
}

func TestPilotWakesOncePerEvent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event func(e *projectEnv)
		key   string
	}{
		{"autopilot pause", func(e *projectEnv) {
			e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/agent pause")
		}, "pause-"},
		{"needs input", func(e *projectEnv) { e.gl.SetLabels(2, "agent::needs-input") }, "stuck/2/"},
		{"infra block", func(e *projectEnv) {
			e.gl.SetLabels(2, "agent::working")
			e.reconcile()
			e.ended(2, e.gl.Clock, aiv1.RunResultFailed, aiv1.BlockInfra)
		}, "infra/2/"},
		{"quota block", func(e *projectEnv) {
			e.gl.SetLabels(2, "agent::working")
			e.reconcile()
			e.deferred(2, e.gl.Clock)
		}, "quota/2/"},
		{"main red", func(e *projectEnv) {
			e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthRed, IssueIID: 50, PipelineID: 900, FailedJobs: []string{"test"}})
		}, "main-red/50"},
		{"flaky main", func(e *projectEnv) {
			e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthFlaky, PipelineID: 901, FailedJobs: []string{"test"}})
		}, "flaky/901"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newPilotEnv(t)
			if keys, req := e.pilotEventKeys(); len(keys) != 0 || req != 0 {
				t.Fatalf("nothing happened yet: %v (request %d)", keys, req)
			}
			tc.event(e)
			e.reconcile()
			e.reconcile()
			keys, req := e.pilotEventKeys()
			if len(keys) != 1 || keys[0][:len(tc.key)] != tc.key || req != 1 {
				t.Fatalf("expected one event %s… and one run request: %v (request %d)", tc.key, keys, req)
			}
			if got := e.pilot().Status.Pilot; got == nil || !slices.Equal(got.Events, keys) {
				t.Fatalf("the key is kept in the pilot's status: %+v", got)
			}
			// Polls while it holds, also after the assessment, wake nobody.
			e.assessed()
			e.reconcile()
			e.reconcile()
			if _, again := e.pilotEventKeys(); again != 1 {
				t.Fatalf("an event wakes the pilot once, got request %d", again)
			}
		})
	}
}

func TestPilotWakesAgainForANewHeadOrReason(t *testing.T) {
	e := newPilotEnv(t)
	e.gl.SetLabels(2, "agent::needs-input")
	e.reconcile()
	e.assessed()
	e.reconcile()
	if keys, req := e.pilotEventKeys(); !slices.Equal(keys, []string{"stuck/2/"}) || req != 1 {
		t.Fatalf("expected one wake-up for #2, no head yet: %v (request %d)", keys, req)
	}

	// A new head is a new event; the assessed one goes.
	e.assessed()
	e.patchStatus(e.taskOf(2), func(s *aiv1.AgentTaskStatus) { s.Run.PushedSHA = "sha2" })
	e.reconcile()
	e.reconcile()
	if keys, req := e.pilotEventKeys(); !slices.Equal(keys, []string{"stuck/2/sha2"}) || req != 2 {
		t.Fatalf("a new head wakes the pilot again: %v (request %d)", keys, req)
	}

	// A new reason too: the issue is blocked by infra at the same head.
	e.assessed()
	e.ended(2, e.gl.Clock, aiv1.RunResultFailed, aiv1.BlockInfra)
	e.reconcile()
	e.reconcile()
	if keys, req := e.pilotEventKeys(); !slices.Equal(keys, []string{"infra/2/sha2"}) || req != 3 {
		t.Fatalf("a new reason wakes the pilot again: %v (request %d)", keys, req)
	}
}

func TestPilotEventThatComesBackWakesNobody(t *testing.T) {
	e := newPilotEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.gl.SetLabels(2, "agent::needs-input")
	e.gl.AddNote(tracking, alice, "/agent pause")
	e.reconcile()
	e.assessed()
	// Another event's wake-up drops the assessed ones from the spec.
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthFlaky, PipelineID: 901})
	e.reconcile()
	e.assessed()
	// Anyone who can comment or label can flap both; that starts no pods.
	for range 3 {
		e.gl.SetLabels(2, "agent::ready")
		e.gl.AddNote(tracking, alice, "/agent resume")
		e.reconcile()
		e.gl.SetLabels(2, "agent::needs-input")
		e.gl.AddNote(tracking, alice, "/agent pause")
		e.reconcile()
	}
	if _, req := e.pilotEventKeys(); req != 2 {
		t.Fatalf("an event that comes back with the same key wakes nobody, got request %d", req)
	}
	if got := e.pilot().Status.Pilot.Events; len(got) != 3 {
		t.Fatalf("the keys are remembered: %v", got)
	}
}

// Bloop #331: CI stays red after two fix rounds, the issue goes to
// needs-input and autopilot pauses over it. That is one event: the pilot
// is woken once, with the red pipeline.
func TestPilotWakesOnceForAnIssueStuckAfterItsFixRounds(t *testing.T) {
	e := newPilotEnv(t)
	e.reconcile()
	name := e.taskOf(1)
	e.gl.SetLabels(1, "agent::working")
	pushed := func(sha string, round int32, fixed int64) int64 {
		request := e.tasks()[name].Spec.RunRequest
		e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
			s.Run.ObservedRunRequest, s.Run.ActiveRunRequest, s.Run.LastResult = request, request, aiv1.RunResultSucceeded
			s.Run.PushedSHA, s.Run.PushedAt, s.Run.CIRound, s.Run.CIFixedPipelineID = sha, &metav1.Time{Time: e.gl.Clock}, round, fixed
		})
		pid := e.gl.AddPipeline(sha, "agent/1", "failed", gitlab.Job{Name: "rust:lint", Stage: "lint", Status: "failed"})
		e.reconcile() // retried once
		e.gl.SetPipelineStatus(pid, "failed")
		e.reconcile()
		return pid
	}
	pid := pushed("sha1", 0, 0)
	pid = pushed("sha2", 1, pid)
	pid = pushed("sha3", 2, pid)
	if ci := e.tasks()[name].Status.CI; ci == nil || ci.State != aiv1.CIStateFailed || !slices.Equal(e.gl.Labels(1), []string{"agent::needs-input"}) {
		t.Fatalf("expected #1 in needs-input after two fix rounds: %+v %v", ci, e.gl.Labels(1))
	}
	for range 3 {
		e.reconcile()
	}
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PausedFor != 1 {
		t.Fatalf("expected autopilot paused over #1: %+v", ap)
	}
	p := e.pilot()
	if evs := p.Spec.Source.PilotEvents; len(evs) != 1 || evs[0].Key != "stuck/1/sha3" || evs[0].IssueIID != 1 || evs[0].PipelineID != pid || p.Spec.RunRequest != 1 {
		t.Fatalf("expected one event for #1 with its red pipeline %d: %+v (request %d)", pid, evs, p.Spec.RunRequest)
	}
}

func TestPilotEventsFoldAPauseAndItsNeedsInputLabel(t *testing.T) {
	spec := testProject().Spec.WithDefaults()
	m := &gitlab.Milestone{IID: 3}
	st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Milestone: &aiv1.MilestoneRef{IID: 3}},
		Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PausedFor: 1, PausedReason: "#1 needs input"}}}
	task := &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{PushedSHA: "sha3"},
		CI: &aiv1.CIStatus{SHA: "sha3", State: aiv1.CIStateFailed, PipelineID: 77}}}
	issues := map[int64]gitlab.Issue{1: {IID: 1, Milestone: m, Labels: []string{"agent::needs-input"}}}
	evs := pilotEvents(spec, nil, st, issues, map[int64]*aiv1.AgentTask{1: task})
	if len(evs) != 1 || evs[0].Key != "stuck/1/sha3" || evs[0].Text != "Autopilot paused: #1 needs input" || evs[0].PipelineID != 77 {
		t.Fatalf("a pause over #1 and its needs-input label are one event: %+v", evs)
	}
}
