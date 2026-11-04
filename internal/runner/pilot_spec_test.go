package runner

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestPilotRecordsASpecEditWithItsFullText(t *testing.T) {
	e := newPilotEnv(t, "")
	text := "Build it.\n\nThe Rink gets a second flipper on the left.\n"
	spec, _ := json.Marshal(text)
	e.engine.proposal = proposalOf(t, `{"summary": "a second flipper", "actions": [{"tool": "editSpec", "spec": `+string(spec)+`}]}`)
	e.step(t)
	if !strings.Contains(e.engine.calls[0].SystemPrompt, "editSpec") || !strings.Contains(e.engine.calls[0].SystemPrompt, "owner's yes, also when you manage") {
		t.Fatalf("the pilot is told about spec edits:\n%s", e.engine.calls[0].SystemPrompt)
	}
	ps := e.task(t).Status.Pilot
	if ps == nil || len(ps.Proposed) != 1 || len(ps.Proposed[0].Actions) != 1 {
		t.Fatalf("expected the spec edit recorded: %+v", ps)
	}
	if a := ps.Proposed[0].Actions[0]; a.Tool != "editSpec" || a.Spec != text || ps.Proposed[0].State == "" || ps.Proposed[0].Source != "note/2000" {
		t.Fatalf("recorded %+v", ps.Proposed[0])
	}
	if replies := botNotes(e.gl, 5); len(replies) != 1 || !strings.Contains(replies[0], "The proposal follows on #5") || strings.Contains(replies[0], "not proposed") {
		t.Fatalf("the reply points to the proposal: %q", replies)
	}
}

func TestPilotRefusesAnOversizeOrSecondSpecEdit(t *testing.T) {
	e := newPilotEnv(t, "")
	big := strings.Repeat("a", aiv1.MaxPilotSpec+1)
	e.engine.proposal = proposalOf(t, fmt.Sprintf(`{"summary": "edits", "actions": [{"tool": "editSpec", "spec": %q},
		{"tool": "retry", "issue": 1}, {"tool": "editSpec", "spec": "Build it fast."}, {"tool": "editSpec", "spec": "Build it slowly."}]}`, big))
	e.step(t)
	replies := botNotes(e.gl, 5)
	if len(replies) != 1 || !strings.Contains(replies[0], "Spec edit not proposed: the new spec is over 64 KiB") ||
		!strings.Contains(replies[0], "Spec edit not proposed: one proposal holds one spec edit") {
		t.Fatalf("the reply says which spec edits are refused: %q", replies)
	}
	ps := e.task(t).Status.Pilot
	if ps == nil || len(ps.Proposed) != 1 {
		t.Fatalf("expected one proposal: %+v", ps)
	}
	var got []string
	for _, a := range ps.Proposed[0].Actions {
		got = append(got, a.Tool+":"+a.Spec)
	}
	if want := []string{"retry:", "editSpec:Build it fast."}; !slices.Equal(got, want) {
		t.Fatalf("recorded %q, want %q", got, want)
	}

	// Exactly the limit is still proposed.
	p := proposalOf(t, fmt.Sprintf(`{"summary": "edit", "actions": [{"tool": "editSpec", "spec": %q}]}`, big[1:]))
	if note := rejectActions(p, true); note != "" || len(p.Actions) != 1 {
		t.Fatalf("a spec of %d bytes is proposed: %q", aiv1.MaxPilotSpec, note)
	}
}

func TestOversizeSpecEditAloneIsNotRecorded(t *testing.T) {
	e := newPilotEnv(t, "")
	e.engine.proposal = proposalOf(t, fmt.Sprintf(`{"summary": "edit", "actions": [{"tool": "editSpec", "spec": %q}]}`, strings.Repeat("a", aiv1.MaxPilotSpec+1)))
	e.step(t)
	if ps := e.task(t).Status.Pilot; ps != nil && len(ps.Proposed) != 0 {
		t.Fatalf("nothing is recorded: %+v", ps.Proposed)
	}
	if replies := botNotes(e.gl, 5); len(replies) != 1 || !strings.Contains(replies[0], "over 64 KiB") || strings.Contains(replies[0], "proposal follows") {
		t.Fatalf("the reply says so and points to no proposal: %q", replies)
	}
}

func TestAssessmentProposesNoSpecEdit(t *testing.T) {
	e := newAssessEnv(t, "")
	e.engine.assessment.Options = []AssessOption{
		{Label: "Rewrite and retry", Recommended: true, Actions: []ProposalAction{{Tool: "editSpec", Spec: "Build something else."}, {Tool: "retry", Issue: 1}}},
		{Label: "Do nothing"},
	}
	e.step(t)
	p := e.proposed(t)
	if !strings.Contains(p.Context, "Spec edit not proposed: only an owner's message asks for one") {
		t.Fatalf("the prompt says the spec edit is refused:\n%s", p.Context)
	}
	if got := labels(p.Options); got != "Rewrite and retry+* | Do nothing" || p.Options[0].Actions[0].Tool != "retry" {
		t.Fatalf("only the retry is offered: %s", got)
	}
}
