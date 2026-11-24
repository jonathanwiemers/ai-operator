package controller

import (
	"fmt"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// runningOf marks the run of issue iid's task as in progress, as the runner
// does when it takes the request.
func (e *projectEnv) runningOf(iid int64) {
	e.t.Helper()
	e.patchStatus(e.taskOf(iid), func(s *aiv1.AgentTaskStatus) {
		s.Run.ActiveRunRequest = s.Run.ObservedRunRequest + 1
	})
}

// acceptInstruct puts an accepted proposal from source with one instruct
// action for #1 on the pilot.
func (e *projectEnv) acceptInstruct(source string, a aiv1.PilotAction) {
	e.t.Helper()
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		if s.Pilot == nil {
			s.Pilot = &aiv1.PilotStatus{}
		}
		s.Pilot.Accepted = append(s.Pilot.Accepted, aiv1.PilotProposal{ID: 7, Source: source, Summary: "tell #1", AcceptedBy: "alice", Actions: []aiv1.PilotAction{a}})
	})
}

// lastActed is the newest line of the pilot's acted log.
func (e *projectEnv) lastActed() string {
	e.t.Helper()
	acted := e.pilot().Status.Pilot.Acted
	if len(acted) == 0 || len(acted[len(acted)-1].Lines) == 0 {
		e.t.Fatalf("nothing acted: %+v", acted)
	}
	l := acted[len(acted)-1].Lines
	return l[len(l)-1]
}

var ownersWords = aiv1.PilotAction{Tool: "instruct", IssueIID: 1, Owner: "alice", Text: "use the v2 API"}

func TestPilotInstructOnARunningTaskQueuesTheOwnersMessage(t *testing.T) {
	e := newManagedEnv(t)
	e.runningOf(1)
	before := e.implTaskOf(1).Spec.RunRequest
	e.acceptInstruct("tell/55", ownersWords)
	e.reconcile()
	task := e.implTaskOf(1)
	msgs := task.Spec.Source.OwnerMessages
	if len(msgs) != 1 || msgs[0].ID != "tell/55" || msgs[0].Owner != "alice" || msgs[0].Text != "use the v2 API" || msgs[0].At.IsZero() {
		t.Fatalf("one owner message from the tell: %+v", msgs)
	}
	if task.Spec.RunRequest != before || task.Spec.Source.PilotInstruction != nil || task.Spec.Source.PilotRun != 0 {
		t.Fatalf("no run is requested and no instruction set: run %d → %d, %+v", before, task.Spec.RunRequest, task.Spec.Source.PilotInstruction)
	}
	if c := botComments(e, 1); len(c) == 0 || c[len(c)-1] != "🧭 Message from `@alice` via the pilot, passed to the running implementer: use the v2 API" {
		t.Fatalf("the issue shows the message: %q", c)
	}
	if l := e.lastActed(); !strings.HasSuffix(l, "use the v2 API; queued for #1's running implementer") {
		t.Fatalf("the pilot says where it went: %q", l)
	}
	// A retried action from the same source appends nothing.
	e.acceptInstruct("tell/55", ownersWords)
	e.reconcile()
	if msgs := e.implTaskOf(1).Spec.Source.OwnerMessages; len(msgs) != 1 || e.implTaskOf(1).Spec.RunRequest != before {
		t.Fatalf("appended once: %+v", msgs)
	}
}

func TestPilotInstructOnAnIdleTaskGoesToTheNextRun(t *testing.T) {
	e := newManagedEnv(t)
	before := e.implTaskOf(1).Spec.RunRequest
	e.acceptInstruct("tell/55", ownersWords)
	e.reconcile()
	task := e.implTaskOf(1)
	pi := task.Spec.Source.PilotInstruction
	if len(task.Spec.Source.OwnerMessages) != 0 || task.Spec.RunRequest != before+1 || pi == nil || pi.Text != "use the v2 API" || pi.Request != before+1 {
		t.Fatalf("today's path: an instruction and a run: %+v, run %d → %d", pi, before, task.Spec.RunRequest)
	}
	if c := botComments(e, 1); len(c) == 0 || !strings.Contains(c[len(c)-1], "for the next run: - **instruct** #1, instruction from `@alice` via pilot: use the v2 API") {
		t.Fatalf("the issue shows the instruction: %q", c)
	}
	if l := e.lastActed(); !strings.HasSuffix(l, "; queued for #1's next run") {
		t.Fatalf("the pilot says where it went: %q", l)
	}
}

func TestPilotKindOnARunningTaskGoesToTheNextRun(t *testing.T) {
	e := newManagedEnv(t)
	e.runningOf(1)
	before := e.implTaskOf(1).Spec.RunRequest
	e.acceptInstruct("event/ci", aiv1.PilotAction{Tool: "instruct", IssueIID: 1, Kind: "ci-fix", Note: "the lint job"})
	e.reconcile()
	task := e.implTaskOf(1)
	pi := task.Spec.Source.PilotInstruction
	if len(task.Spec.Source.OwnerMessages) != 0 || task.Spec.RunRequest != before+1 || pi == nil || pi.Kind != "ci-fix" || task.Spec.Source.PilotRun != before+1 {
		t.Fatalf("the pilot's own kind takes the next run: %+v, run %d → %d", pi, before, task.Spec.RunRequest)
	}
	if l := e.lastActed(); strings.Contains(l, "queued") {
		t.Fatalf("a pilot kind keeps its text: %q", l)
	}
}

func TestPilotTellOnARunningTaskKeysTheMessageByTheNote(t *testing.T) {
	e := newProposalEnv(t)
	e.runningOf(1)
	n := e.gl.AddNote(e.tracking(), alice, "/pilot tell #1: use the v2 API")
	e.reconcile()
	e.choose("Q1", "A", alice)
	e.reconcile()
	if msgs := e.implTaskOf(1).Spec.Source.OwnerMessages; len(msgs) != 1 || msgs[0].ID != fmt.Sprintf("tell/%d", n.ID) {
		t.Fatalf("the message is keyed by the tell's note: %+v", msgs)
	}
}

func TestOwnerMessageID(t *testing.T) {
	for p, want := range map[*aiv1.PilotProposal]string{
		{Source: "chat/3", Prompt: "Q1", ID: 2}: "chat/3",
		{Prompt: "Q1", ID: 2}:                   "prompt/Q1",
		{ID: 2}:                                 "acted/2",
	} {
		if got := ownerMessageID(*p); got != want {
			t.Errorf("%+v: %q, want %q", *p, got, want)
		}
	}
}
