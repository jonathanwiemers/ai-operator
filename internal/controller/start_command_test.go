package controller

import (
	"slices"
	"time"

	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// waitingBehindSeven is a project with %7 open and %9, owned by alice,
// waiting for its turn behind it, with its tracking issue.
func waitingBehindSeven(t *testing.T) *projectEnv {
	t.Helper()
	e := newProjectEnv(t)
	e.gl.Users = []gitlab.User{alice, mallory}
	e.gl.Milestones = []gitlab.Milestone{
		turnMilestone(7, "/agent autopilot\nFirst."),
		turnMilestone(9, "/agent owners @alice\n/agent autopilot\nSecond."),
	}
	e.reconcile()
	e.reconcile()
	e.wantWaiting(9, 7)
	if e.trackingOf(9) == 0 {
		t.Fatalf("the waiting milestone has its tracking issue: %+v", e.specFor(9).Status.Autopilot)
	}
	return e
}

func (e *projectEnv) trackingOf(iid int64) int64 {
	e.t.Helper()
	if ap := e.specFor(iid).Status.Autopilot; ap != nil {
		return ap.TrackingIssueIID
	}
	return 0
}

func (e *projectEnv) notesOf(issue int64) string {
	var b strings.Builder
	for _, n := range e.gl.IssueNotes[issue] {
		b.WriteString(n.Body + "\n")
	}
	return b.String()
}

func TestOwnerStartsAWaitingMilestone(t *testing.T) {
	e := waitingBehindSeven(t)
	tracking := e.trackingOf(9)
	if body := e.gl.Issues[tracking].Description; !strings.Contains(body, "`/agent start`") {
		t.Fatalf("the waiting milestone's tracking issue offers start:\n%s", body)
	}

	e.gl.AddNote(tracking, alice, "/agent start")
	e.reconcile()
	if ap := e.specFor(9).Status.Autopilot; ap.StartRequestedBy != "@alice" || ap.StartRequestedAt == nil {
		t.Fatalf("the start is recorded as a run-next mark: %q, %v", ap.StartRequestedBy, ap.StartRequestedAt)
	}
	if !strings.Contains(e.notesOf(tracking), "▶️ Planning starts now, ahead of its turn (by @alice).") {
		t.Fatalf("the start is said on the tracking issue:\n%s", e.notesOf(tracking))
	}

	// The next poll asks for the first planning run, %7 still open.
	e.reconcile()
	if st := e.specFor(9); st.Spec.RunRequest != 1 || st.Spec.QueuedBehind != nil {
		t.Fatalf("%%9 asks for its first run: %+v", st.Spec)
	}
	e.reconcile()
	if got := e.specFor(9).Spec.RunRequest; got != 1 {
		t.Fatalf("the first run is asked for once: run request %d", got)
	}
	if body := e.gl.Issues[tracking].Description; strings.Contains(body, "`/agent start`") {
		t.Fatalf("a planning milestone no longer offers start:\n%s", body)
	}
}

func TestStartByANonOwnerIsRefused(t *testing.T) {
	e := waitingBehindSeven(t)
	tracking := e.trackingOf(9)
	e.gl.AddNote(tracking, mallory, "/agent start")
	e.reconcile()
	e.reconcile()
	e.wantWaiting(9, 7)
	if got := e.specFor(9).Status.Autopilot.StartRequestedBy; got != "" {
		t.Fatalf("a non-owner starts nothing: %q", got)
	}
	if notes := e.notesOf(tracking); !strings.Contains(notes, notOwnerStart) || strings.Contains(notes, "Planning starts now") {
		t.Fatalf("the refusal is answered:\n%s", notes)
	}
}

func TestUnknownCommandOffersStartAndStartOff(t *testing.T) {
	e := waitingBehindSeven(t)
	tracking := e.trackingOf(9)
	e.gl.AddNote(tracking, alice, "/agent frobnicate")
	e.reconcile()
	if notes := e.notesOf(tracking); !strings.Contains(notes, "understands `/agent start`, `/agent pause`") {
		t.Fatalf("a waiting milestone lists start:\n%s", notes)
	}

	p := ownedAutopilotEnv(t)
	tracking = p.trackingOf(3)
	p.gl.AddNote(tracking, alice, "/agent start")
	p.reconcile()
	p.gl.AddNote(tracking, alice, "/agent frobnicate")
	p.reconcile()
	if notes := p.notesOf(tracking); !strings.Contains(notes, "understands `/agent start off`, `/agent pause`") {
		t.Fatalf("a marked milestone lists start off:\n%s", notes)
	}
}

// ownedAutopilotEnv is newAutopilotEnv (%3 planned, owned by alice) with an
// older %2 that plans first: %3 merged #1 and holds #2 behind it.
func ownedAutopilotEnv(t *testing.T) *projectEnv {
	t.Helper()
	e := newAutopilotEnv(t)
	e.gl.Users = []gitlab.User{alice, mallory}
	e.gl.Milestones[0].Description = "/agent owners @alice\n" + e.gl.Milestones[0].Description
	e.reconcile() // #1 of %3 starts
	e.gl.Milestones = append(e.gl.Milestones, turnMilestone(2, "/agent autopilot\nOlder."))
	e.reconcile()
	e.finishRun(1, "sha-api", nil)
	e.gl.MRs[1].Draft, e.gl.MRs[1].DetailedMergeStatus = false, "mergeable"
	e.reconcile()
	e.reconcile()
	e.wantHolding()
	return e
}

// wantHolding fails unless %3 holds #2 behind the older %2.
func (e *projectEnv) wantHolding() {
	e.t.Helper()
	ap := e.specFor(3).Status.Autopilot
	if !slices.Equal(e.gl.Merged, []int64{1}) || ap.Items[1].State != aiv1.AutopilotItemPending {
		e.t.Fatalf("%%3 merges #1 and holds #2: merged %v, %+v", e.gl.Merged, ap)
	}
	if d := e.gl.Issues[ap.TrackingIssueIID].Description; !strings.Contains(d, "waiting for its turn: %2 go first") {
		e.t.Fatalf("%%3 waits for %%2:\n%s", d)
	}
}

func TestStartMarksAPlannedMilestoneToRunNext(t *testing.T) {
	e := ownedAutopilotEnv(t)
	tracking := e.trackingOf(3)
	if d := e.gl.Issues[tracking].Description; !strings.Contains(d, "`/agent start` (owners: run it next") {
		t.Fatalf("a planned milestone offers start:\n%s", d)
	}
	e.gl.AddNote(tracking, alice, "/agent start")
	e.reconcile()
	ap := e.specFor(3).Status.Autopilot
	if ap.StartRequestedBy != "@alice" || ap.StartRequestedAt == nil {
		t.Fatalf("start marks the planned milestone: %q, %v", ap.StartRequestedBy, ap.StartRequestedAt)
	}
	if !strings.Contains(e.notesOf(tracking), "⏭️ This milestone runs next (by @alice): it plans and starts issues before the others; their active issues finish.") {
		t.Fatalf("the mark is said on the tracking issue:\n%s", e.notesOf(tracking))
	}
	// The next poll ranks %3 first: it starts #2 while the older %2 is open.
	e.reconcile()
	if ap := e.specFor(3).Status.Autopilot; ap.Items[1].State != aiv1.AutopilotItemActive {
		t.Fatalf("the marked %%3 starts #2 with the older %%2 open: %+v", ap)
	}
	if d := e.gl.Issues[tracking].Description; !strings.Contains(d, "⏭️ runs next (marked by @alice).") || !strings.Contains(d, "`/agent start off` (owners: clear the run-next mark)") {
		t.Fatalf("the tracking issue shows the mark and how to clear it:\n%s", d)
	}
}

func TestStartTwiceKeepsTheFirstMark(t *testing.T) {
	e := ownedAutopilotEnv(t)
	tracking := e.trackingOf(3)
	e.gl.AddNote(tracking, alice, "/agent start")
	e.reconcile()
	first := e.specFor(3).Status.Autopilot.StartRequestedAt.DeepCopy()
	e.r.Now = func() time.Time { return first.Add(time.Hour) }
	e.gl.AddNote(tracking, alice, "/agent start")
	e.reconcile()
	if got := e.specFor(3).Status.Autopilot.StartRequestedAt; !got.Equal(first) {
		t.Fatalf("a repeated start keeps the mark's time: %v, was %v", got, first)
	}
	notes := e.notesOf(tracking)
	if !strings.Contains(notes, "The milestone is already marked to run next (by @alice).") || strings.Count(notes, "⏭️ This milestone runs next") != 1 {
		t.Fatalf("the repeated start is answered:\n%s", notes)
	}
}

func TestStartOffClearsTheMark(t *testing.T) {
	e := ownedAutopilotEnv(t)
	tracking := e.trackingOf(3)
	e.gl.AddNote(tracking, alice, "/agent start off")
	e.reconcile()
	if !strings.Contains(e.notesOf(tracking), "No run-next mark to clear.") {
		t.Fatalf("start off without a mark is answered:\n%s", e.notesOf(tracking))
	}
	e.gl.AddNote(tracking, alice, "/agent start")
	e.reconcile()
	e.gl.AddNote(tracking, alice, "/agent start off")
	e.reconcile()
	if ap := e.specFor(3).Status.Autopilot; ap.StartRequestedBy != "" || ap.StartRequestedAt != nil {
		t.Fatalf("start off clears the mark: %q, %v", ap.StartRequestedBy, ap.StartRequestedAt)
	}
	if !strings.Contains(e.notesOf(tracking), "⏹️ Run-next mark cleared (by @alice); the milestone waits for its turn again.") {
		t.Fatalf("the cleared mark is said on the tracking issue:\n%s", e.notesOf(tracking))
	}
	// Age order again (run_next_test.go); the active #2 finishes, and start
	// is offered again.
	if d := e.gl.Issues[tracking].Description; !strings.Contains(d, "`/agent start` (owners: run it next") || strings.Contains(d, "runs next (marked") {
		t.Fatalf("the tracking issue offers start and shows no mark:\n%s", d)
	}
}

func TestStartOnADoneMilestoneDoesNothing(t *testing.T) {
	e := ownedAutopilotEnv(t)
	tracking := e.trackingOf(3)
	e.patchStatus(e.specFor(3).Name, func(s *aiv1.AgentTaskStatus) { s.Autopilot.Phase = aiv1.AutopilotDone })
	e.gl.AddNote(tracking, alice, "/agent start")
	e.reconcile()
	if ap := e.specFor(3).Status.Autopilot; ap.StartRequestedBy != "" || ap.StartRequestedAt != nil {
		t.Fatalf("start marks no done milestone: %+v", ap)
	}
	if notes := e.notesOf(tracking); !strings.Contains(notes, "The milestone is done; `start` does nothing.") {
		t.Fatalf("the command is answered:\n%s", notes)
	}
}

func TestStartOnAPlannedMilestoneByANonOwnerIsRefused(t *testing.T) {
	e := ownedAutopilotEnv(t)
	tracking := e.trackingOf(3)
	e.gl.AddNote(tracking, mallory, "/agent start")
	e.reconcile()
	e.gl.AddNote(tracking, mallory, "/agent start off")
	e.reconcile()
	if ap := e.specFor(3).Status.Autopilot; ap.StartRequestedBy != "" || ap.StartRequestedAt != nil {
		t.Fatalf("a non-owner marks nothing: %+v", ap)
	}
	if notes := e.notesOf(tracking); strings.Count(notes, notOwnerStart) != 2 || strings.Contains(notes, "runs next") {
		t.Fatalf("both forms are refused:\n%s", notes)
	}
	e.wantHolding()
}
