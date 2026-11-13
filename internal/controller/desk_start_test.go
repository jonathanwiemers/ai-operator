package controller

import (
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// deskOwnedEnv is ownedAutopilotEnv with ada@x as the owner subject of %3.
func deskOwnedEnv(t *testing.T) *projectEnv {
	t.Helper()
	e := ownedAutopilotEnv(t)
	for i := range e.gl.Milestones {
		if e.gl.Milestones[i].IID == 3 {
			e.gl.Milestones[i].Description = "/agent owner-subjects ada@x\n" + e.gl.Milestones[i].Description
		}
	}
	e.reconcile()
	if s := e.specFor(3).Spec.OwnerSubjects; !slices.Equal(s, []string{"ada@x"}) {
		t.Fatalf("owner subjects = %v", s)
	}
	return e
}

// deskSay sends Desk commands to %3's spec task and returns the answers.
func (e *projectEnv) deskSay(author string, bodies ...string) []string {
	e.t.Helper()
	st := e.specFor(3)
	id := st.Spec.Source.LastChatCommandID
	for _, b := range bodies {
		id++
		st.Spec.Source.ChatMessages = append(st.Spec.Source.ChatMessages,
			aiv1.ChatMessage{ID: id, Kind: "command", Text: b, Author: author, AuthorName: "Ada"})
	}
	before := len(st.Spec.Source.ChatAnswers)
	if err := e.c.Update(e.ctx, &st); err != nil {
		e.t.Fatal(err)
	}
	e.reconcile()
	var got []string
	for _, a := range e.specFor(3).Spec.Source.ChatAnswers[before:] {
		got = append(got, a.Text)
	}
	return got
}

func TestDeskStartAndUnstart(t *testing.T) {
	e := deskOwnedEnv(t)
	tracking := e.trackingOf(3)
	if got := e.deskSay("ada@x", `{"cmd":"start"}`); !slices.Equal(got, []string{"The milestone runs next."}) {
		t.Fatalf("answers = %q", got)
	}
	ap := e.specFor(3).Status.Autopilot
	if ap.StartRequestedBy != "Ada via the Desk" || ap.StartRequestedAt == nil {
		t.Fatalf("start from the Desk marks the milestone: %q, %v", ap.StartRequestedBy, ap.StartRequestedAt)
	}
	if !strings.Contains(e.notesOf(tracking), "⏭️ This milestone runs next (by Ada via the Desk)") {
		t.Fatalf("the mark is said on the tracking issue:\n%s", e.notesOf(tracking))
	}
	if got := e.deskSay("ada@x", `{"cmd":"unstart"}`, `{"cmd":"unstart"}`); !slices.Equal(got, []string{"Run-next mark cleared.", "No run-next mark to clear."}) {
		t.Fatalf("answers = %q", got)
	}
	if ap := e.specFor(3).Status.Autopilot; ap.StartRequestedBy != "" || ap.StartRequestedAt != nil {
		t.Fatalf("unstart clears the mark: %q, %v", ap.StartRequestedBy, ap.StartRequestedAt)
	}
}

func TestDeskEndMilestone(t *testing.T) {
	e := deskOwnedEnv(t)
	got := e.deskSay("ada@x", `{"cmd":"endmilestone"}`)
	if !slices.Equal(got, []string{"The milestone ends here; the tracking issue says what was skipped."}) {
		t.Fatalf("answers = %q", got)
	}
	ap := e.specFor(3).Status.Autopilot
	// Done once its final release is through.
	if ap.Items[1].State != aiv1.AutopilotItemSkipped {
		t.Fatalf("endmilestone skips #2: %+v", ap)
	}
	if notes := e.notesOf(e.trackingOf(3)); !strings.Contains(notes, "⏹️ The milestone ends here (Ada via the Desk): #2 skipped.") {
		t.Fatalf("the end is said on the tracking issue:\n%s", notes)
	}
}

func TestDeskStartCommandsByANonOwnerAreRefused(t *testing.T) {
	e := deskOwnedEnv(t)
	got := e.deskSay("eve@x", `{"cmd":"start"}`, `{"cmd":"unstart"}`, `{"cmd":"endmilestone"}`)
	if !slices.Equal(got, []string{notOwnerStart, notOwnerStart, notOwnerEnd}) {
		t.Fatalf("answers = %q", got)
	}
	ap := e.specFor(3).Status.Autopilot
	if ap.StartRequestedBy != "" || ap.StartRequestedAt != nil || ap.Items[1].State != aiv1.AutopilotItemPending {
		t.Fatalf("a non-owner changes nothing: %+v", ap)
	}
	e.wantHolding()
}
