package controller

import (
	"reflect"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
)

// newDraftEnv is a finished autopilot milestone owned by alice (and her
// Desk subject) whose pilot posted a retrospective with three follow-ups
// just now.
func newDraftEnv(t *testing.T) *projectEnv {
	t.Helper()
	e := newPilotEnv(t)
	e.gl.Milestones[0].Description += "\n/agent owner-subjects alice@example.com"
	e.reconcile()
	finishAutopilot(e)
	e.reconcile()
	e.reconcile()
	request := e.pilot().Spec.Source.Retrospective
	if request == 0 {
		t.Fatalf("the retrospective is asked for: %+v", e.pilot().Spec)
	}
	posted := metav1.NewTime(e.r.Now())
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Run.ObservedRunRequest = request
		s.Pilot = &aiv1.PilotStatus{Retrospective: &aiv1.PilotRetrospective{NoteID: 42, PostedAt: &posted, OwnerIDs: []int64{alice.ID},
			FollowUps: []aiv1.PilotFollowUp{{Title: "Docs", Body: "Write them."}, {Title: "Flaky test"}, {Title: "Cleanup"}}}}
	})
	e.reconcile()
	return e
}

// chatCommand hands a Desk command to the pilot and reconciles.
func (e *projectEnv) chatCommand(id int64, author, body string) {
	e.t.Helper()
	pilot := e.pilot()
	pilot.Spec.Source.ChatMessages = append(pilot.Spec.Source.ChatMessages, aiv1.ChatMessage{ID: id, Kind: "command", Text: body, Author: author, AuthorName: strings.Split(author, "@")[0]})
	if err := e.c.Update(e.ctx, &pilot); err != nil {
		e.t.Fatal(err)
	}
	e.reconcile()
}

// chatAnswer is the operator's answer to the Desk message id.
func chatAnswer(task aiv1.AgentTask, id int64) string {
	for _, a := range task.Spec.Source.ChatAnswers {
		if a.MessageID == id {
			return a.Text
		}
	}
	return ""
}

func TestPilotStaysWhileFollowUpsAreOpen(t *testing.T) {
	e := newDraftEnv(t)
	if _, ok := e.tasks()[pilotTaskName]; !ok {
		t.Fatalf("the pilot stays while the follow-ups are open: %v", keys(e.tasks()))
	}
	if ps := e.tasks()[specTaskName].Status.Pilot; ps == nil || ps.Retrospective == nil || len(ps.Retrospective.FollowUps) != 3 {
		t.Fatalf("the retrospective is on the spec task: %+v", ps)
	}
	runs := e.pilot().Spec.RunRequest
	e.reconcile()
	if p := e.pilot(); p.Spec.RunRequest != runs || p.Spec.Source.FollowUpDraft != nil {
		t.Fatalf("the kept pilot starts no run by itself: %d → %+v", runs, p.Spec)
	}

	// Opened and taken over: settled, and the pilot goes.
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Pilot.Retrospective.Created = map[string]int64{"1": 20, "2": 21}
		s.Pilot.Retrospective.Taken = map[string]int64{"3": 9}
	})
	e.reconcile()
	if _, ok := e.tasks()[pilotTaskName]; ok {
		t.Fatalf("the pilot goes once the follow-ups are settled: %v", keys(e.tasks()))
	}
}

func TestPilotGoesWhenTheFollowUpWindowEnds(t *testing.T) {
	e := newDraftEnv(t)
	e.advance(aiv1.FollowUpWindow)
	e.reconcile()
	if _, ok := e.tasks()[pilotTaskName]; ok {
		t.Fatalf("the pilot goes once the window to open the follow-ups is over: %v", keys(e.tasks()))
	}
}

func TestDraftCommandAsksThePilotOnce(t *testing.T) {
	e := newDraftEnv(t)
	before := e.pilot().Spec.RunRequest

	e.chatCommand(301, "mallory@x", `{"cmd":"draft","followUps":[1]}`)
	p := e.pilot()
	if got := chatAnswer(p, 301); got != "Only the milestone's owners can ask for a draft of a follow-up milestone." {
		t.Fatalf("a non-owner is refused: %q", got)
	}
	if p.Spec.RunRequest != before || p.Spec.Source.FollowUpDraft != nil {
		t.Fatalf("a non-owner's draft starts nothing: %+v", p.Spec)
	}

	e.chatCommand(302, "alice@example.com", `{"cmd":"draft","followUps":[3,1]}`)
	p = e.pilot()
	want := &aiv1.FollowUpDraftRequest{Request: before + 1, Numbers: []int{1, 3}, By: "alice"}
	if p.Spec.RunRequest != before+1 || !reflect.DeepEqual(p.Spec.Source.FollowUpDraft, want) {
		t.Fatalf("an owner's draft is one run request: %d %+v", p.Spec.RunRequest, p.Spec.Source.FollowUpDraft)
	}
	if got := chatAnswer(p, 302); !strings.Contains(got, "from follow-ups 1, 3") {
		t.Fatalf("the owner is told: %q", got)
	}

	// While it is written, another waits.
	e.chatCommand(303, "alice@example.com", `{"cmd":"draft","followUps":[2]}`)
	p = e.pilot()
	if got := chatAnswer(p, 303); !strings.Contains(got, "writing a draft already") || p.Spec.RunRequest != before+1 {
		t.Fatalf("one draft at a time: %q %d", got, p.Spec.RunRequest)
	}

	// Written: the next one is taken.
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) { s.Run.ObservedRunRequest = before + 1 })
	e.chatCommand(304, "alice@example.com", `{"cmd":"draft","followUps":[2]}`)
	if p = e.pilot(); p.Spec.RunRequest != before+2 || p.Spec.Source.FollowUpDraft.Request != before+2 {
		t.Fatalf("a new draft after the last one: %+v", p.Spec)
	}
}

func TestDraftCommandRefusals(t *testing.T) {
	e := newDraftEnv(t)
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) { s.Pilot.Retrospective.Created = map[string]int64{"2": 20} })
	before := e.pilot().Spec.RunRequest
	// In ID order: the pilot reads chat commands after the last one it read.
	for _, tc := range []struct {
		id         int64
		body, want string
	}{
		{401, `{"cmd":"draft","followUps":[1,4]}`, "No draft: follow-up 4 is unknown, the retrospective has 3."},
		{402, `{"cmd":"draft","followUps":[2]}`, "No draft: follow-up 2 was opened as #20."},
		{403, `{"cmd":"accept","proposal":1}`, "The autopilot of this milestone is done: its pilot only writes drafts of a follow-up milestone now."},
		{404, `{"cmd":"draft","followUps":[]}`, "draft needs followUps"},
	} {
		e.chatCommand(tc.id, "alice@example.com", tc.body)
		if got := chatAnswer(e.pilot(), tc.id); !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s: answer %q, want %q", tc.body, got, tc.want)
		}
	}
	if p := e.pilot(); p.Spec.RunRequest != before || p.Spec.Source.FollowUpDraft != nil {
		t.Fatalf("a refused draft starts nothing: %+v", p.Spec)
	}
}

func TestDraftCommandWithoutRetrospective(t *testing.T) {
	st := &aiv1.AgentTask{}
	coord := &aiv1.AgentTask{}
	if reply, req := draftRequest(st, coord, nil, "alice", []int{1}, time.Now()); req != nil || !strings.Contains(reply, "no retrospective with follow-ups") {
		t.Fatalf("no retrospective, no draft: %q %+v", reply, req)
	}
	st.Status.Pilot = &aiv1.PilotStatus{Retrospective: &aiv1.PilotRetrospective{Failed: "the engine crashed"}}
	if _, req := draftRequest(st, coord, nil, "alice", []int{1}, time.Now()); req != nil {
		t.Fatalf("a failed retrospective has no follow-ups: %+v", req)
	}
	posted := metav1.NewTime(time.Now().Add(-8 * 24 * time.Hour))
	st.Status.Pilot.Retrospective = &aiv1.PilotRetrospective{PostedAt: &posted, FollowUps: []aiv1.PilotFollowUp{{Title: "Docs"}}}
	if reply, req := draftRequest(st, coord, nil, "alice", []int{1}, time.Now()); req != nil || !strings.Contains(reply, "settled") {
		t.Fatalf("settled follow-ups, no draft: %q %+v", reply, req)
	}
}

func TestChatCommandAllowedDraftOnTheCoordinateTaskOnly(t *testing.T) {
	cmd := mustParse(t, `{"cmd":"draft","followUps":[1]}`)
	for typ, want := range map[aiv1.TaskType]bool{aiv1.TaskTypeCoordinate: true, aiv1.TaskTypeSpec: false, aiv1.TaskTypeImplement: false} {
		task := aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: typ, Autopilot: true}}
		if got := chatCommandAllowed(task, cmd); got != want {
			t.Errorf("%s: allowed = %v, want %v", typ, got, want)
		}
	}
}

func mustParse(t *testing.T, body string) chatcmd.Command {
	t.Helper()
	cmd, err := chatcmd.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}
