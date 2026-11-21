package controller

import (
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// queuedFiveSixSeven is a project with %5, %6 and %7 queued in that order,
// %7 owned by alice (owner subject ada@x), with %7's tracking issue.
func queuedFiveSixSeven(t *testing.T) (*projectEnv, int64) {
	t.Helper()
	e := newProjectEnv(t)
	e.gl.Users = []gitlab.User{alice, mallory}
	e.gl.Milestones = []gitlab.Milestone{
		turnMilestone(5, "/agent autopilot\nM5."),
		turnMilestone(6, "/agent autopilot\nM6."),
		turnMilestone(7, "/agent owners @alice\n/agent owner-subjects ada@x\n/agent autopilot\nM7."),
	}
	e.reconcile()
	e.reconcile()
	e.wantOrder(5, 6, 7)
	tracking := e.trackingOf(7)
	if tracking == 0 {
		t.Fatalf("%%7 has its tracking issue: %+v", e.specFor(7).Status.Autopilot)
	}
	return e, tracking
}

// say comments a command on the tracking issue and syncs once.
func (e *projectEnv) say(tracking int64, who gitlab.User, body string) {
	e.t.Helper()
	e.gl.AddNote(tracking, who, body)
	e.reconcile()
}

func (e *projectEnv) wantNotes(tracking int64, text string, n int) {
	e.t.Helper()
	if got := strings.Count(e.notesOf(tracking), text); got != n {
		e.t.Fatalf("%q is on the tracking issue %d times, want %d:\n%s", text, got, n, e.notesOf(tracking))
	}
}

func TestMoveTopBottomBeforeAndAfter(t *testing.T) {
	e, tracking := queuedFiveSixSeven(t)
	e.say(tracking, alice, "/agent move before %6")
	e.wantOrder(5, 7, 6)
	e.wantNotes(tracking, "⏫ Moved before %6 (by @alice).", 1)
	// The next sync ranks by it: %7 waits behind %5 only.
	e.reconcile()
	e.wantOrder(5, 7, 6)
	e.wantWaiting(7, 5)
	e.wantWaiting(6, 5, 7)

	e.say(tracking, alice, "/agent move after 6")
	e.wantOrder(5, 6, 7)
	e.wantNotes(tracking, "⏫ Moved after %6 (by @alice).", 1)

	e.say(tracking, alice, "/agent MOVE Top")
	e.wantOrder(7, 5, 6)
	e.wantNotes(tracking, "⏫ Moved to the top (by @alice).", 1)
	e.reconcile()
	if d := e.gl.Issues[tracking].Description; strings.Contains(d, "/agent move top") {
		t.Fatalf("the first milestone offers no move top:\n%s", d)
	}

	e.say(tracking, alice, "/agent move bottom")
	e.wantOrder(5, 6, 7)
	e.wantNotes(tracking, "⏬ Moved to the bottom (by @alice).", 1)

	// A move that changes nothing is answered and posts no note.
	e.say(tracking, alice, "/agent move bottom")
	e.say(tracking, alice, "/agent move after %6")
	e.wantOrder(5, 6, 7)
	e.wantNotes(tracking, "Already at the bottom of the run order; nothing changed.", 1)
	e.wantNotes(tracking, "Already after %6 in the run order; nothing changed.", 1)
	e.wantNotes(tracking, "⏬ Moved", 1)
	e.wantNotes(tracking, "⏫ Moved after", 1)
}

func TestMoveRefusals(t *testing.T) {
	e, tracking := queuedFiveSixSeven(t)
	e.gl.Milestones = append(e.gl.Milestones,
		gitlab.Milestone{ID: 108, IID: 8, Title: "M8", State: "closed", Description: "/agent autopilot\nM8."},
		turnMilestone(4, "/agent spec\nM4."))
	e.reconcile()
	e.setPhase(6, aiv1.AutopilotDone)
	e.reconcile()
	e.wantOrder(5, 7)

	for _, tc := range []struct {
		who        gitlab.User
		body, want string
	}{
		{mallory, "/agent move top", notOwnerMove},
		{alice, "/agent move before %7", "A milestone cannot move before or after itself."},
		{alice, "/agent move before %42", "%42 is not a milestone of this project."},
		{alice, "/agent move after %6", "%6 is done; it is no longer in the run order."},
		{alice, "/agent move before %8", "%8 is closed; it is no longer in the run order."},
		{alice, "/agent move before %4", "%4 is not an open autopilot milestone of this project."},
		{alice, "/agent move up", "move needs exactly one of top, bottom, before %N or after %N, e.g. `/agent move before %6`."},
	} {
		e.say(tracking, tc.who, tc.body)
		e.wantNotes(tracking, tc.want, 1)
		e.wantOrder(5, 7)
	}
	e.wantNotes(tracking, "Moved", 0)
}

func TestMoveAboveTheRunningMilestone(t *testing.T) {
	e := newAutopilotEnv(t)
	e.gl.Users = []gitlab.User{alice, mallory}
	e.reconcile()
	e.reconcile() // #1 of %3 is active
	e.gl.Milestones = append(e.gl.Milestones, turnMilestone(5, "/agent owners @alice\n/agent autopilot\nYounger."))
	e.reconcile()
	e.reconcile()
	e.wantWaiting(5, 3)
	tracking := e.trackingOf(5)

	e.say(tracking, alice, "/agent move before %3")
	e.wantOrder(5, 3)
	e.wantNotes(tracking, "⏫ Moved before %3 (by @alice). %3's active issues finish first; it starts no new issues until this milestone is done.", 1)

	// The next sync: %5 plans, %3 lets #1 finish and holds #2 for %5.
	e.reconcile()
	if st := e.specFor(5); st.Spec.RunRequest != 1 || st.Spec.QueuedBehind != nil {
		t.Fatalf("the moved %%5 plans with %%3 open: %+v", st.Spec)
	}
	e.finishRun(1, "sha-api", nil)
	e.gl.MRs[1].Draft, e.gl.MRs[1].DetailedMergeStatus = false, "mergeable"
	e.reconcile()
	e.reconcile()
	ap := e.specFor(3).Status.Autopilot
	if !slices.Equal(e.gl.Merged, []int64{1}) || ap.Items[1].State != aiv1.AutopilotItemPending {
		t.Fatalf("#1 finishes, #2 waits behind %%5: merged %v, %+v", e.gl.Merged, ap)
	}
	if d := e.gl.Issues[ap.TrackingIssueIID].Description; !strings.Contains(d, "waiting for its turn: %5 go first") {
		t.Fatalf("%%3 holds for %%5:\n%s", d)
	}

	// Moving back below the running milestone says nothing about holding.
	e.say(tracking, alice, "/agent move bottom")
	e.wantOrder(3, 5)
	e.wantNotes(tracking, "⏬ Moved to the bottom (by @alice).\n", 1)
}

// A replayed tracking-issue note (its order write was lost) applies the
// order again and posts no second note.
func TestReplayedMoveNotePostsOnce(t *testing.T) {
	e, tracking := queuedFiveSixSeven(t)
	e.say(tracking, alice, "/agent move before %6")
	e.wantOrder(5, 7, 6)
	e.setOrder(5, 6, 7)
	e.patchStatus(e.specFor(7).Name, func(s *aiv1.AgentTaskStatus) { s.Autopilot.LastCommandNoteID = 0 })
	e.reconcile()
	e.wantOrder(5, 7, 6)
	e.wantNotes(tracking, "⏫ Moved before %6", 1)
	e.wantNotes(tracking, "Already", 0)
}

func TestDeskMove(t *testing.T) {
	e, tracking := queuedFiveSixSeven(t)
	if got := e.deskSayAt(7, "ada@x", `{"cmd":"move","before":6}`); !slices.Equal(got, []string{"Moved before %6."}) {
		t.Fatalf("answers = %q", got)
	}
	e.wantOrder(5, 7, 6)
	e.wantNotes(tracking, "⏫ Moved before %6 (by Ada via the Desk).", 1)

	// The answer was lost with the order: the replay finds its note.
	e.setOrder(5, 6, 7)
	st := e.specFor(7)
	st.Spec.Source.LastChatCommandID--
	if err := e.c.Update(e.ctx, &st); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	e.wantOrder(5, 7, 6)
	e.wantNotes(tracking, "⏫ Moved before %6", 1)

	got := e.deskSayAt(7, "ada@x", `{"cmd":"move","before":6}`, `{"cmd":"move","to":"bottom"}`, `{"cmd":"start"}`)
	want := []string{"Already before %6 in the run order; nothing changed.", "Moved to the bottom.", "Moved to the top."}
	if !slices.Equal(got, want) {
		t.Fatalf("answers = %q, want %q", got, want)
	}
	e.wantOrder(7, 5, 6)
	e.wantNotes(tracking, "⏫ Moved before %6", 1)

	if got := e.deskSayAt(7, "eve@x", `{"cmd":"move","after":6}`, `{"cmd":"start"}`); !slices.Equal(got, []string{notOwnerMove, notOwnerStart}) {
		t.Fatalf("a non-owner is refused: %q", got)
	}
	e.wantOrder(7, 5, 6)
}
