package controller

import (
	"strings"
	"testing"

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
	if got := e.specFor(9).Status.Autopilot.StartRequestedBy; got != "@alice" {
		t.Fatalf("the start is recorded: %q", got)
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

func TestStartOnAPlannedMilestoneDoesNothing(t *testing.T) {
	e := newAutopilotEnv(t)
	e.gl.Users = []gitlab.User{alice}
	e.gl.Milestones[0].Description = "/agent owners @alice\n" + e.gl.Milestones[0].Description
	e.reconcile()
	before := e.tasks()[specTaskName]
	e.gl.AddNote(e.autopilot().TrackingIssueIID, alice, "/agent start")
	e.reconcile()
	e.reconcile()
	after := e.tasks()[specTaskName]
	if after.Spec.RunRequest != before.Spec.RunRequest || e.autopilot().StartRequestedBy != "" {
		t.Fatalf("start changes nothing on a planned milestone: %+v", e.autopilot())
	}
	notes := trackingComments(e)
	if !strings.Contains(notes, "The milestone is already planned (or being planned); `start` does nothing.") || strings.Contains(notes, "Planning starts now") {
		t.Fatalf("the command is answered:\n%s", notes)
	}
}

func TestUnknownCommandOffersStartOnlyWhileWaiting(t *testing.T) {
	e := waitingBehindSeven(t)
	tracking := e.trackingOf(9)
	e.gl.AddNote(tracking, alice, "/agent frobnicate")
	e.reconcile()
	if notes := e.notesOf(tracking); !strings.Contains(notes, "understands `/agent start`, `/agent pause`") {
		t.Fatalf("a waiting milestone lists start:\n%s", notes)
	}

	p := newAutopilotEnv(t)
	p.reconcile()
	p.gl.AddNote(p.autopilot().TrackingIssueIID, alice, "/agent frobnicate")
	p.reconcile()
	if notes := trackingComments(p); !strings.Contains(notes, "only understands") || strings.Contains(notes, "`/agent start`") {
		t.Fatalf("a planned milestone does not list start:\n%s", notes)
	}
}
