package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// newRoundEnv is a loose issues queue with its pilot, in round 1, at a
// pilot cost of $0.40 when the round started.
func newRoundEnv(t *testing.T, issues, hours int32) (*projectEnv, int64) {
	t.Helper()
	e := newLooseEnv(t, func(l *aiv1.LooseIssuesSpec) {
		l.Pilot = aiv1.LooseIssuesPilotSpec{RoundIssues: &issues, RoundHours: &hours}
	})
	e.gl.Users = []gitlab.User{alice, bob, mallory}
	for i := 0; i < 3; i++ {
		if _, ok := e.tasks()[queuePilotName]; ok {
			break
		}
		e.reconcile() // the pilot
	}
	e.patchStatus(queuePilotName, func(s *aiv1.AgentTaskStatus) { s.Run.CostUSD = "0.4000" })
	e.reconcile() // round 1
	return e, e.queue().Status.Autopilot.TrackingIssueIID
}

func (e *projectEnv) round() *aiv1.LooseRound {
	e.t.Helper()
	rd := e.queue().Status.Autopilot.Round
	if rd == nil {
		e.t.Fatal("no round")
	}
	return rd
}

// finishItems adds queue items that finished now.
func (e *projectEnv) finishItems(state aiv1.AutopilotItemState, iids ...int64) {
	e.t.Helper()
	at := &metav1.Time{Time: e.r.Now()}
	e.patchStatus(queueTaskName, func(s *aiv1.AgentTaskStatus) {
		for _, iid := range iids {
			it := aiv1.AutopilotItem{IID: iid, State: state}
			if state == aiv1.AutopilotItemMerged {
				it.MergedAt = at
			} else {
				it.LeftAt, it.LeftReason = at, "closed by a human"
			}
			s.Autopilot.Items = append(s.Autopilot.Items, it)
		}
	})
}

// pilotRan ends the pilot's runs up to its run request, with its cost and
// mutate on its status.
func (e *projectEnv) pilotRan(cost string, mutate func(*aiv1.AgentTaskStatus)) {
	e.t.Helper()
	req := e.queuePilot().Spec.RunRequest
	e.patchStatus(queuePilotName, func(s *aiv1.AgentTaskStatus) {
		s.Run.ObservedRunRequest, s.Run.CostUSD = req, cost
		if mutate != nil {
			mutate(s)
		}
	})
}

func (e *projectEnv) roundNotes(tracking int64, n int32) int {
	return strings.Count(strings.Join(botComments(e, tracking), "\n"), roundNoteMarker(n))
}

func TestQueueRoundStartsAndEndsByIssues(t *testing.T) {
	e, tracking := newRoundEnv(t, 5, 24)
	rd := e.round()
	if rd.Number != 1 || rd.StartCostUSD != "0.4000" || rd.NoteRequest != 0 {
		t.Fatalf("round 1: %+v", rd)
	}
	if pr := e.queuePilot().Spec.Source.PilotRound; pr == nil || pr.Number != 1 || !pr.StartedAt.Equal(&rd.StartedAt) || pr.StartCostUSD != "0.4000" {
		t.Fatalf("round 1 copied for the runner: %+v", pr)
	}

	e.finishItems(aiv1.AutopilotItemMerged, 1, 2, 3, 4)
	e.pilotRan("1.1000", nil)
	e.reconcile()
	if rd := e.round(); rd.Finished != 4 || rd.CostUSD != "0.7000" || rd.NoteRequest != 0 {
		t.Fatalf("four finished, not due: %+v", rd)
	}

	e.finishItems(aiv1.AutopilotItemLeft, 5)
	e.reconcile()
	coord := e.queuePilot()
	req := coord.Spec.RunRequest
	if rd := e.round(); rd.Finished != 5 || rd.NoteRequest != req || coord.Spec.Source.RoundNote != req {
		t.Fatalf("the fifth finished issue asks for the note: %+v, roundNote %d, request %d", rd, coord.Spec.Source.RoundNote, req)
	}

	// While the note is written, an owner's message waits.
	msg := e.gl.AddNote(tracking, bob, "/pilot why is #421 held?")
	e.reconcile()
	if coord := e.queuePilot(); coord.Spec.RunRequest != req || len(coord.Spec.Source.PilotMessages) != 0 {
		t.Fatalf("the message waits for the next round: %+v", coord.Spec.Source.PilotMessages)
	}

	e.pilotRan("1.9000", func(s *aiv1.AgentTaskStatus) {
		s.Pilot = &aiv1.PilotStatus{RoundNote: &aiv1.PilotRoundNote{Request: req, Note: "Merged #1-#4, #5 left. @alice see #421.\n/close"}}
	})
	e.reconcile()
	c := strings.Join(botComments(e, tracking), "\n")
	if !strings.Contains(c, "🧭 **Round 1 note**") || !strings.Contains(c, "Merged #1-#4, #5 left.") ||
		!strings.Contains(c, "Round 2 started.") || strings.Contains(c, "@alice") || strings.Contains(c, "\n/close") {
		t.Fatalf("the pilot's note, inert, and the next round on the tracking issue:\n%s", c)
	}
	rd = e.round()
	coord = e.queuePilot()
	pr := coord.Spec.Source.PilotRound
	if rd.Number != 2 || rd.Finished != 0 || rd.StartCostUSD != "1.9000" || rd.NoteRequest != 0 || !strings.HasPrefix(rd.Note, "Merged #1-#4") ||
		pr == nil || pr.Number != 2 || pr.Note != rd.Note || pr.StartCostUSD != "1.9000" || coord.Spec.Source.RoundNote != 0 {
		t.Fatalf("round 2: %+v, for the runner %+v (roundNote %d)", rd, pr, coord.Spec.Source.RoundNote)
	}

	// The message goes to the new round; the note is posted once.
	e.reconcile()
	if msgs := e.queuePilot().Spec.Source.PilotMessages; len(msgs) != 1 || msgs[0].NoteID != msg.ID {
		t.Fatalf("the waiting message reaches round 2: %+v", msgs)
	}
	if rd := e.round(); rd.CostUSD != "0.0000" || rd.Finished != 0 {
		t.Fatalf("round 2 counts from its start: %+v", rd)
	}
	if n := e.roundNotes(tracking, 1); n != 1 {
		t.Fatalf("one note of round 1, got %d", n)
	}
}

func TestQueueRoundEndsByHoursAfterTheRunningCall(t *testing.T) {
	e, tracking := newRoundEnv(t, 5, 24)
	e.finishItems(aiv1.AutopilotItemMerged, 7)
	e.gl.AddIssue(8, "Held one", "agent::working")
	e.patchStatus(queueTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Autopilot.Items = append(s.Autopilot.Items, aiv1.AutopilotItem{IID: 8, State: aiv1.AutopilotItemActive,
			Hold: &aiv1.ItemHold{Kind: aiv1.PauseCIRed, Reason: "CI is red"}})
	})
	e.reconcile()
	e.advance(24 * time.Hour)
	// A call runs.
	coord := e.queuePilot()
	coord.Spec.RunRequest++
	if err := e.c.Update(e.ctx, coord); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if rd := e.round(); rd.NoteRequest != 0 || e.queuePilot().Spec.Source.RoundNote != 0 {
		t.Fatalf("the note waits for the running call: %+v", rd)
	}

	e.pilotRan("0.9000", nil)
	e.reconcile()
	req := e.round().NoteRequest
	if req == 0 || req != e.queuePilot().Spec.RunRequest {
		t.Fatalf("after the call, the note is asked for: %+v", e.round())
	}
	// The note call failed: the facts.
	e.pilotRan("1.0000", func(s *aiv1.AgentTaskStatus) {
		s.Pilot = &aiv1.PilotStatus{RoundNote: &aiv1.PilotRoundNote{Request: req, Failed: "the budget is used up"}}
	})
	e.reconcile()
	c := strings.Join(botComments(e, tracking), "\n")
	if !strings.Contains(c, "🧭 **Round 1 note**") || !strings.Contains(c, "Merged: #7.") ||
		!strings.Contains(c, "Held: #8 (CI is red).") || !strings.Contains(c, "Spent $0.60.") || !strings.Contains(c, "Round 2 started.") {
		t.Fatalf("a facts-only note:\n%s", c)
	}
	if rd := e.round(); rd.Number != 2 || !strings.Contains(rd.Note, "Merged: #7.") {
		t.Fatalf("round 2 carries the facts: %+v", rd)
	}
}

func TestQueueRoundEndlessWithBothLimitsOff(t *testing.T) {
	e, tracking := newRoundEnv(t, 0, 0)
	e.finishItems(aiv1.AutopilotItemMerged, 1, 2, 3, 4, 5, 6)
	e.advance(200 * time.Hour)
	e.reconcile()
	if rd := e.round(); rd.Number != 1 || rd.Finished != 6 || rd.NoteRequest != 0 || e.roundNotes(tracking, 1) != 0 {
		t.Fatalf("both limits off: one endless round: %+v", rd)
	}
}

func TestQueueRoundEmptyNoteAndLostWrites(t *testing.T) {
	e, tracking := newRoundEnv(t, 1, 0)
	e.finishItems(aiv1.AutopilotItemLeft, 3)
	e.reconcile()
	req := e.round().NoteRequest
	// The round's status write was lost: the request is taken from the pilot.
	e.patchStatus(queueTaskName, func(s *aiv1.AgentTaskStatus) { s.Autopilot.Round.NoteRequest = 0 })
	e.reconcile()
	if rd := e.round(); rd.NoteRequest != req || e.queuePilot().Spec.RunRequest != req {
		t.Fatalf("no second note call: %+v, request %d", rd, e.queuePilot().Spec.RunRequest)
	}
	// An empty note: the facts.
	e.pilotRan("0.4000", func(s *aiv1.AgentTaskStatus) {
		s.Pilot = &aiv1.PilotStatus{RoundNote: &aiv1.PilotRoundNote{Request: req}}
	})
	e.reconcile()
	if c := strings.Join(botComments(e, tracking), "\n"); !strings.Contains(c, "Left: #3 (closed by a human).") {
		t.Fatalf("an empty note gives the facts:\n%s", c)
	}
	// The reset's status write was lost: it is done again, not posted again.
	e.patchStatus(queueTaskName, func(s *aiv1.AgentTaskStatus) {
		s.Autopilot.Round.Number, s.Autopilot.Round.NoteRequest = 1, req
	})
	e.reconcile()
	if rd := e.round(); rd.Number != 2 || rd.NoteRequest != 0 || e.roundNotes(tracking, 1) != 1 {
		t.Fatalf("one note of round 1 after the retry: %+v, %d notes", rd, e.roundNotes(tracking, 1))
	}
}

func TestQueueRoundWaitsForThePilotAndTheTrackingIssue(t *testing.T) {
	a := &autopilot{ap: &aiv1.AutopilotStatus{}}
	if err := a.syncRound(context.Background(), nil); err != nil || a.ap.Round != nil {
		t.Fatalf("without a pilot no round: %v %+v", err, a.ap.Round)
	}
	// Without a tracking issue, the note waits.
	if posted, err := a.postRoundNote(context.Background(), 1, "note"); posted || err != nil {
		t.Fatalf("no tracking issue, nothing posted: %v %v", posted, err)
	}
}

func TestQueueRoundHoldsBackCommentsOnQueueIssues(t *testing.T) {
	e, _ := newLooseRunEnv(t, 0)
	e.gl.SetLabels(350, "agent::working")
	e.reconcile() // the pilot, the task of #350
	e.reconcile() // the comments so far are history
	e.patchStatus(queueTaskName, func(s *aiv1.AgentTaskStatus) { s.Autopilot.Round.NoteRequest = 99 })
	n := e.gl.AddNote(350, bob, "/pilot why is #350 slow?")
	e.reconcile()
	if msgs := e.queuePilot().Spec.Source.PilotMessages; len(msgs) != 0 {
		t.Fatalf("the comment waits while the note is written: %+v", msgs)
	}
	e.patchStatus(queueTaskName, func(s *aiv1.AgentTaskStatus) { s.Autopilot.Round.NoteRequest = 0 })
	e.reconcile()
	if msgs := e.queuePilot().Spec.Source.PilotMessages; len(msgs) != 1 || msgs[0].NoteID != n.ID {
		t.Fatalf("the comment reaches the pilot after the note: %+v", msgs)
	}
}

func TestQueueRoundLine(t *testing.T) {
	five, none := int32(5), int32(0)
	rd := &aiv1.LooseRound{Number: 3, StartedAt: metav1.NewTime(time.Date(2026, 11, 26, 10, 12, 0, 0, time.FixedZone("CET", 3600))),
		Finished: 2, CostUSD: "1.4000"}
	for name, c := range map[string]struct {
		p    aiv1.LooseIssuesPilotSpec
		want string
	}{
		"defaults": {aiv1.LooseIssuesPilotSpec{}, "Round 3 · started 2026-11-26 09:12 UTC · 2 of 5 issues · $1.40"},
		"no issue limit": {aiv1.LooseIssuesPilotSpec{RoundIssues: &none},
			"Round 3 · started 2026-11-26 09:12 UTC · 2 issues, no issue limit · $1.40"},
		"no time limit": {aiv1.LooseIssuesPilotSpec{RoundIssues: &five, RoundHours: &none},
			"Round 3 · started 2026-11-26 09:12 UTC · 2 of 5 issues · no time limit · $1.40"},
	} {
		if got := roundLine(c.p, rd); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
	if got := roundLine(aiv1.LooseIssuesPilotSpec{}, nil); got != "" {
		t.Errorf("no round, no line: %q", got)
	}

	e, tracking := newRoundEnv(t, 5, 24)
	if d := e.gl.Issues[tracking].Description; !strings.Contains(d, "\n🧭 Round 1 · started ") || !strings.Contains(d, " · 0 of 5 issues · $0.00\n") {
		t.Fatalf("the tracking issue shows the round:\n%s", d)
	}
}
