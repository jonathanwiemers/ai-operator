package controller

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

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
		{"flaky main with its log", func(e *projectEnv) {
			e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthFlaky, PipelineID: 901, FailedJobs: []string{"rust:test"},
				Flaky: []aiv1.FlakyJobReport{{Job: "rust:test", IssueIID: 340, Tests: []string{"a b"}, LogTail: "panicked"}}})
		}, "flaky/901/340"},
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

func TestPilotEventKeyOutlivesALostStatusPatch(t *testing.T) {
	e := newPilotEnv(t)
	e.gl.SetLabels(2, "agent::needs-input")
	e.reconcile()
	e.assessed()
	// The status patch after the wake-up was lost; meanwhile the event
	// ended and another one wakes the pilot.
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) { s.Pilot = nil })
	e.gl.SetLabels(2, "agent::ready")
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthFlaky, PipelineID: 901})
	e.reconcile()
	e.assessed()
	e.gl.SetLabels(2, "agent::needs-input")
	e.reconcile()
	e.reconcile()
	if _, req := e.pilotEventKeys(); req != 2 {
		t.Fatalf("a key handed over is remembered from the spec, got request %d", req)
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
	evs := pilotEvents(spec, nil, st, issues, map[int64]*aiv1.AgentTask{1: task}, true, time.Time{})
	if len(evs) != 1 || evs[0].Key != "stuck/1/sha3" || evs[0].Text != "Autopilot paused: #1 needs input" || evs[0].PipelineID != 77 {
		t.Fatalf("a pause over #1 and its needs-input label are one event: %+v", evs)
	}
}

// stalled returns the stalled-pause events handed to the pilot so far (the
// keys it remembers) and its run request.
func (e *projectEnv) stalled() ([]string, int64) {
	e.t.Helper()
	p := e.pilot()
	var keys []string
	if p.Status.Pilot != nil {
		for _, k := range p.Status.Pilot.Events {
			if strings.HasPrefix(k, aiv1.StalledEventPrefix) {
				keys = append(keys, k)
			}
		}
	}
	return keys, p.Spec.RunRequest
}

// wait moves the clock on by d and polls.
func (e *projectEnv) wait(d time.Duration) {
	e.t.Helper()
	e.gl.Clock = e.gl.Clock.Add(d)
	e.reconcile()
}

// The stuck pause of ai-operator milestone 4 (#2 waits for the skipped #1)
// that nothing changes in wakes the pilot once, after pilot.stuckAfter,
// with the pause's ways out.
func TestStalledPauseWakesThePilotOnce(t *testing.T) {
	e := newStuckEnv(t)
	ap := e.autopilot()
	if ap.PausedAt == nil || ap.PausedAt.After(e.gl.Clock) || e.gl.Clock.Sub(ap.PausedAt.Time) > time.Minute || ap.StateHash == "" || ap.StateChangedAt == nil {
		t.Fatalf("the pause and the state hash are stamped: %+v", ap)
	}
	e.assessed()
	_, before := e.stalled()
	e.wait(29 * time.Minute)
	if keys, req := e.stalled(); len(keys) != 0 || req != before {
		t.Fatalf("nothing before stuckAfter: %v (request %d)", keys, req)
	}
	e.wait(time.Minute)
	keys, req := e.stalled()
	if want := fmt.Sprintf("stalled/%d", ap.PausedAt.Unix()); !slices.Equal(keys, []string{want}) || req != before+1 {
		t.Fatalf("expected one wake-up %s after 30m: %v (request %d)", want, keys, req)
	}
	ev := e.pilot().Spec.Source.PilotEvents[slices.IndexFunc(e.pilot().Spec.Source.PilotEvents, aiv1.StalledEvent)]
	for _, want := range []string{"Autopilot has been paused for 30m and nothing changed: nothing can start: #2 waits for the skipped #1",
		"Ways out:\n", "`/agent startAnyway #N` here", "`/agent unskip #N` here"} {
		if !strings.Contains(ev.Text, want) {
			t.Errorf("the event lacks %q:\n%s", want, ev.Text)
		}
	}
	if ev.IssueIID != 0 {
		t.Errorf("the stuck pause is about no issue: %+v", ev)
	}
	// Polls after the assessment, however long, wake nobody.
	e.assessed()
	e.wait(time.Hour)
	e.wait(time.Hour)
	if keys, again := e.stalled(); again != req || len(keys) != 1 {
		t.Fatalf("one assessment per pause: %v (request %d)", keys, again)
	}
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseStuck {
		t.Fatalf("it never resumes on its own: %+v", ap)
	}
}

// Anything in the state hash that changes restarts the clock: a head, a CI
// state, main health. Spend and the tracking issue do not.
func TestStalledPauseWaitsWhileTheStateChanges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(e *projectEnv)
	}{
		{"head", func(e *projectEnv) {
			e.patchStatus(e.taskOf(1), func(s *aiv1.AgentTaskStatus) { s.Run.PushedSHA = "sha2" })
		}},
		{"CI state", func(e *projectEnv) {
			e.patchStatus(e.taskOf(1), func(s *aiv1.AgentTaskStatus) {
				s.CI = &aiv1.CIStatus{SHA: "sha1", State: aiv1.CIStateFailed, PipelineID: 77}
			})
		}},
		{"main health", func(e *projectEnv) {
			e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthGreen, SHA: "main-y"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newStuckEnv(t)
			e.assessed()
			e.wait(20 * time.Minute)
			hash := e.autopilot().StateHash
			tc.change(e)
			e.reconcile()
			if ap := e.autopilot(); ap.StateHash == hash || e.gl.Clock.Sub(ap.StateChangedAt.Time) > time.Minute {
				t.Fatalf("the change is in the hash: %+v", ap)
			}
			_, before := e.stalled()
			e.wait(20 * time.Minute) // 40m paused, 20m unchanged
			if keys, req := e.stalled(); len(keys) != 0 || req != before {
				t.Fatalf("nothing while the state changes: %v (request %d)", keys, req)
			}
			e.wait(10 * time.Minute)
			if keys, req := e.stalled(); len(keys) != 1 || req != before+1 {
				t.Fatalf("30m after the last change: %v (request %d)", keys, req)
			}
		})
	}
}

// A resume ends the pause and its clock; the stuck pause that follows at
// once is a new pause, with its own assessment 30m later.
func TestStalledPauseAfterAResumeIsANewPause(t *testing.T) {
	e := newStuckEnv(t)
	first := e.autopilot().PausedAt.Unix()
	e.assessed()
	e.wait(20 * time.Minute)
	e.gl.AddNote(e.tracking(), alice, "/agent resume")
	e.reconcile()
	ap := e.autopilot()
	if ap.PauseKind != aiv1.PauseStuck || ap.PausedAt == nil || ap.PausedAt.Unix() == first {
		t.Fatalf("expected a new stuck pause: %+v", ap)
	}
	e.assessed()
	_, before := e.stalled()
	e.wait(15 * time.Minute) // 35m after the first pause
	if keys, req := e.stalled(); len(keys) != 0 || req != before {
		t.Fatalf("nothing for the pause that was resumed: %v (request %d)", keys, req)
	}
	e.wait(15 * time.Minute)
	if keys, req := e.stalled(); !slices.Equal(keys, []string{fmt.Sprintf("stalled/%d", ap.PausedAt.Unix())}) || req != before+1 {
		t.Fatalf("the new pause gets its own: %v (request %d)", keys, req)
	}
}

func TestStalledPauseWithACustomStuckAfter(t *testing.T) {
	e := newStuckEnv(t)
	e.patchProject(func(p *aiv1.AgentProject) { p.Spec.Pilot.StuckAfter = &metav1.Duration{Duration: 10 * time.Minute} })
	e.assessed()
	_, before := e.stalled()
	e.wait(9 * time.Minute)
	if keys, _ := e.stalled(); len(keys) != 0 {
		t.Fatalf("nothing before 10m: %v", keys)
	}
	e.wait(time.Minute)
	if keys, req := e.stalled(); len(keys) != 1 || req != before+1 {
		t.Fatalf("one after 10m: %v (request %d)", keys, req)
	}
}

// Only a pause stalls: a running queue has none, a pause gets its PausedAt
// with the next save before it can, and an owner's pause never does.
func TestStalledEventNeedsAPause(t *testing.T) {
	spec := testProject().Spec.WithDefaults()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	long := metav1.NewTime(now.Add(-time.Hour))
	for name, ap := range map[string]*aiv1.AutopilotStatus{
		"running":           {Phase: aiv1.AutopilotRunning, PausedAt: &long, StateChangedAt: &long},
		"paused, unstamped": {Phase: aiv1.AutopilotPaused, StateChangedAt: &long},
		"an owner's pause":  {Phase: aiv1.AutopilotPaused, PauseKind: aiv1.PauseOwner, PausedAt: &long, StateChangedAt: &long},
	} {
		st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Milestone: &aiv1.MilestoneRef{IID: 3}}, Status: aiv1.AgentTaskStatus{Autopilot: ap}}
		if evs := pilotEvents(spec, nil, st, nil, nil, true, now); slices.ContainsFunc(evs, aiv1.StalledEvent) {
			t.Errorf("%s: no stalled event: %+v", name, evs)
		}
	}
	paused := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PausedReason: "the plan has a dependency cycle between #1 and #2", PauseKind: aiv1.PauseCycle, PausedAt: &long, StateChangedAt: &long}
	if ev, ok := stalledEvent(spec, paused, nil, now); !ok || ev.Key != fmt.Sprintf("stalled/%d", long.Unix()) ||
		!strings.HasPrefix(ev.Text, "Autopilot has been paused for 1h0m and nothing changed: the plan has a dependency cycle between #1 and #2\n\nWays out:\n- ") {
		t.Fatalf("a pause unchanged for an hour stalls: %+v", ev)
	}
}
