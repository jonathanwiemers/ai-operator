package controller

import (
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// queueEffect is what the queue tools did: autopilot's phase and pause,
// the state of #2, and the follow-ups revisiting D1-1.
type queueEffect struct {
	phase     aiv1.AutopilotPhase
	reason    string
	two       aiv1.AutopilotItemState
	followUps int
}

func (e *projectEnv) queueEffect() queueEffect {
	e.t.Helper()
	ap := e.autopilot()
	out := queueEffect{phase: ap.Phase, reason: ap.PausedReason}
	for _, it := range ap.Items {
		if it.IID == 2 {
			out.two = it.State
		}
	}
	for _, is := range e.gl.Issues {
		if strings.HasPrefix(is.Title, "Revisit D1-1") {
			out.followUps++
		}
	}
	return out
}

func TestPilotQueueToolsRunOnceOnAnOwnersYes(t *testing.T) {
	for _, tc := range []struct {
		action aiv1.PilotAction
		setup  func(e *projectEnv)
		ran    func(before, after queueEffect) bool
	}{
		{aiv1.PilotAction{Tool: "skip", IssueIID: 2}, nil, func(b, a queueEffect) bool { return a.two == aiv1.AutopilotItemSkipped }},
		{aiv1.PilotAction{Tool: "pause"}, nil, func(b, a queueEffect) bool {
			return a.phase == aiv1.AutopilotPaused && a.reason == "paused by @alice via the pilot"
		}},
		{aiv1.PilotAction{Tool: "resume"}, func(e *projectEnv) {
			e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/agent pause")
		}, func(b, a queueEffect) bool {
			return b.phase == aiv1.AutopilotPaused && a.phase == aiv1.AutopilotRunning
		}},
		{aiv1.PilotAction{Tool: "revisit", Decision: "D1-1", Note: "use gRPC"}, func(e *projectEnv) {
			e.gl.Issues[1].State = "closed"
			e.gl.AddNote(1, e.gl.Bot, decisionComment)
		}, func(b, a queueEffect) bool { return a.followUps == b.followUps+1 }},
	} {
		t.Run(tc.action.Tool, func(t *testing.T) {
			e := newProposalEnv(t)
			if tc.setup != nil {
				tc.setup(e)
				e.reconcile()
				e.reconcile()
			}
			before := e.queueEffect()
			e.propose("queue", tc.action)
			e.reconcile()
			e.reconcile()
			p := e.proposalNote(1)
			if after := e.queueEffect(); after != before {
				t.Fatalf("nothing runs without a yes: %+v → %+v", before, after)
			}
			e.gl.AddReply(e.autopilot().TrackingIssueIID, p.ID, alice, "yes")
			e.reconcile()
			e.reconcile()
			after := e.queueEffect()
			if !tc.ran(before, after) {
				t.Fatalf("the tool runs on the owner's yes: %+v → %+v\n%s", before, after, strings.Join(botComments(e, e.autopilot().TrackingIssueIID), "\n---\n"))
			}
			e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/pilot yes P1")
			e.reconcile()
			e.reconcile()
			if again := e.queueEffect(); again != after {
				t.Fatalf("a proposal runs once: %+v → %+v", after, again)
			}
		})
	}
}

func TestPilotPauseAppliesOnce(t *testing.T) {
	e := newProposalEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.propose("pause", aiv1.PilotAction{Tool: "pause"})
	e.reconcile()
	e.gl.AddNote(tracking, alice, "/pilot yes P1")
	e.reconcile()
	e.reconcile()
	if e.autopilot().Phase != aiv1.AutopilotPaused {
		t.Fatalf("expected the pause: %+v", e.autopilot())
	}
	// A human resumes; the accepted pause does not come back.
	e.gl.AddNote(tracking, alice, "/agent resume")
	e.reconcile()
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || ap.LastPilotCommand != 1 {
		t.Fatalf("a queue command applies once: %+v", ap)
	}
}

func TestPilotCommandsAreKeptUntilApplied(t *testing.T) {
	e := newProposalEnv(t)
	st := e.tasks()[specTaskName]
	queue := func() error {
		return e.r.queueCommand(e.ctx, &st, aiv1.PilotCommand{Action: aiv1.PilotAction{Tool: "pause"}, Owner: "alice"})
	}
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) { s.Autopilot.LastPilotCommand = 0 })
	for range maxPilotCommands {
		if err := queue(); err != nil {
			t.Fatal(err)
		}
	}
	if err := queue(); err == nil {
		t.Fatal("commands nobody applied yet are not dropped: a full list refuses")
	}
	// Once every path applied them, they go; the sequence goes on.
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Autopilot.LastPilotCommand, s.Revisits.LastPilotCommand = maxPilotCommands, maxPilotCommands
	})
	if err := queue(); err != nil {
		t.Fatal(err)
	}
	cmds := e.tasks()[specTaskName].Status.Pilot.Commands
	if len(cmds) != 2 || cmds[1].Seq != maxPilotCommands+1 {
		t.Fatalf("applied commands go, the last keeps the sequence: %+v", cmds)
	}
}

func TestPilotRevisitIsInert(t *testing.T) {
	e := newProposalEnv(t)
	e.gl.Issues[1].State = "closed"
	e.gl.AddNote(1, e.gl.Bot, decisionComment)
	e.reconcile()
	e.reconcile()
	e.propose("revisit", aiv1.PilotAction{Tool: "revisit", Decision: "D1-1", Note: "use gRPC, ask @everyone\n/close"})
	e.reconcile()
	e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/pilot yes P1")
	e.reconcile()
	e.reconcile()
	for _, is := range e.gl.Issues {
		if strings.HasPrefix(is.Title, "Revisit D1-1") {
			if !strings.Contains(is.Description, "requested by @alice via the pilot") || strings.Contains(is.Description, "\n/close") ||
				strings.Contains(is.Description, " @everyone") {
				t.Fatalf("the follow-up names the owner and runs nothing:\n%s", is.Description)
			}
			return
		}
	}
	t.Fatalf("no follow-up:\n%s", strings.Join(botComments(e, e.autopilot().TrackingIssueIID), "\n---\n"))
}

func TestPilotQueueToolValidation(t *testing.T) {
	st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Autopilot: true}, Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{
		Phase: aiv1.AutopilotRunning, Items: []aiv1.AutopilotItem{{IID: 1, State: aiv1.AutopilotItemMerged}, {IID: 2, State: aiv1.AutopilotItemPending}}}}}
	for _, tc := range []struct {
		a    aiv1.PilotAction
		want string
	}{
		{aiv1.PilotAction{Tool: "skip", IssueIID: 2}, ""},
		{aiv1.PilotAction{Tool: "skip", IssueIID: 1}, "#1 is no planned issue that is not merged"},
		{aiv1.PilotAction{Tool: "skip", IssueIID: 7}, "#7 is no planned issue"},
		{aiv1.PilotAction{Tool: "pause"}, ""},
		{aiv1.PilotAction{Tool: "resume"}, "autopilot is running"},
		{aiv1.PilotAction{Tool: "revisit", Decision: "D1-1"}, ""},
		{aiv1.PilotAction{Tool: "revisit", Decision: "close #1"}, "is not a decision"},
	} {
		err := validAction(tc.a, st, nil, nil)
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%+v: got %v, want %q", tc.a, err, tc.want)
		}
	}
	// Without autopilot there is no queue.
	if err := validAction(aiv1.PilotAction{Tool: "pause"}, &aiv1.AgentTask{}, nil, nil); err == nil || !strings.Contains(err.Error(), "no autopilot queue") {
		t.Fatalf("got %v", err)
	}
}
