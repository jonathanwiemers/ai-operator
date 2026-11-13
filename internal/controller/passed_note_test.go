package controller

import (
	"errors"
	"strings"
	"testing"
)

// A milestone passed by a younger marked one says so once on its tracking
// issue, again after the mark was cleared and set again, and retries a note
// that failed.
func TestPassedMilestoneAnnouncesTheMarkOnce(t *testing.T) {
	e := ownedAutopilotEnv(t)
	older := e.trackingOf(2)
	const note = "⏭️ %3 goes first (by @alice)."
	count := func() int { return strings.Count(e.notesOf(older), note) }

	e.setMark(3, "@alice")
	e.reconcile()
	e.reconcile()
	if n := count(); n != 1 {
		t.Fatalf("%%2 announces the mark once, got %d:\n%s", n, e.notesOf(older))
	}
	if n := strings.Count(e.notesOf(e.trackingOf(3)), "goes first"); n != 0 {
		t.Fatalf("the marked %%3 announces nothing, got %d", n)
	}

	e.setMark(3, "")
	e.reconcile()
	if ap := e.specFor(2).Status.Autopilot; len(ap.PassedBy) != 0 {
		t.Fatalf("a cleared mark is forgotten: %v", ap.PassedBy)
	}
	e.gl.NoteErrors = map[int64]error{older: errors.New("boom")}
	e.setMark(3, "@alice")
	e.reconcileFailing()
	if ap := e.specFor(2).Status.Autopilot; count() != 1 || len(ap.PassedBy) != 0 {
		t.Fatalf("a failed note is not recorded: %d notes, %v", count(), ap.PassedBy)
	}
	delete(e.gl.NoteErrors, older)
	e.reconcile()
	e.reconcile()
	if n := count(); n != 2 {
		t.Fatalf("the new mark is announced again after the failure, got %d:\n%s", n, e.notesOf(older))
	}
}
