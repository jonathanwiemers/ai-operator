package controller

import (
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// assess records an assessment of #1 with options, as the runner does.
func (e *projectEnv) assess(source string, options ...aiv1.PromptOption) {
	e.t.Helper()
	p := aiv1.PilotProposal{Summary: "an assessment", Source: source, Trigger: 1, Quiet: true, Kind: aiv1.PromptAssessment,
		Why: "`env`: no Rust toolchain", Question: "#1 is stuck: what now?", Context: "**Classification:** `env`\n\n**Evidence:** cargo is missing",
		Options: options}
	p.State = e.pilotState(recommendedActions(options))
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		if s.Pilot == nil {
			s.Pilot = &aiv1.PilotStatus{}
		}
		s.Pilot.Proposed = append(s.Pilot.Proposed, p)
	})
}

// optionLabels are a prompt's options as "A:label", the recommended one
// marked with "*".
func optionLabels(p aiv1.Prompt) string {
	var out []string
	for _, o := range p.Options {
		s := o.ID + ":" + o.Label
		if o.Recommended {
			s += "*"
		}
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

func TestPilotAssessmentOptionsBecomeAPrompt(t *testing.T) {
	e := newProposalEnv(t)
	before := e.toolEffect()
	e.assess("event/stuck/1/sha1@1",
		aiv1.PromptOption{Label: "Retry #1", Detail: "a new run", Recommended: true, Actions: []aiv1.PilotAction{{Tool: "retry", IssueIID: 1}}},
		aiv1.PromptOption{Label: "Merge it", Actions: []aiv1.PilotAction{{Tool: "merge", IssueIID: 1}}},
		aiv1.PromptOption{Label: "Do nothing"})
	e.reconcile()
	q := e.prompt(specTaskName, "Q1")
	if q.Source != (aiv1.PromptSource{Kind: aiv1.PromptAssessment, IssueIID: 1, Key: "event/stuck/1/sha1@1"}) || q.Question != "#1 is stuck: what now?" ||
		!strings.Contains(q.Context, "cargo is missing") || q.Quiet {
		t.Fatalf("the assessment is the prompt, with its evidence as context: %+v", q)
	}
	if got := optionLabels(q); got != "A:Retry #1* B:Do nothing" {
		t.Fatalf("the option the operator would not run is dropped: %s", got)
	}
	if ps := e.pilot().Status.Pilot; len(ps.Proposed) != 0 {
		t.Fatalf("the operator takes it out: %+v", ps.Proposed)
	}
	notes := promptNotes(e, 1)
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "cc @alice") || !strings.Contains(notes[0].Body, "Retry #1") || strings.Contains(notes[0].Body, "Merge it") {
		t.Fatalf("its comment, on the issue it is about, pings the owners: nobody did before: %+v", notes)
	}
	e.gl.AddReply(1, notes[0].ID, alice, "A")
	e.reconcile()
	if after := e.toolEffect(); after.runs != before.runs+1 {
		t.Fatalf("A runs the retry: %+v → %+v", before, after)
	}

	// The same assessment again (its run's status was not written) is
	// not put twice.
	e.assess("event/stuck/1/sha1@1", aiv1.PromptOption{Label: "Look", Recommended: true}, aiv1.PromptOption{Label: "Do nothing"})
	e.reconcile()
	if got := promptStates(e.task(specTaskName)); got != "Q1=answered" {
		t.Fatalf("one prompt per source: %s", got)
	}
}

func TestPilotAssessmentWithoutAnOptionToRunStillPrompts(t *testing.T) {
	e := newProposalEnv(t)
	e.assess("event/a", aiv1.PromptOption{Label: "Merge it", Recommended: true, Actions: []aiv1.PilotAction{{Tool: "merge", IssueIID: 1}}},
		aiv1.PromptOption{Label: "Do nothing"})
	e.reconcile()
	q := e.prompt(specTaskName, "Q1")
	if got := optionLabels(q); got != "A:Look into it myself* B:Do nothing" || q.Source.Kind != aiv1.PromptAssessment {
		t.Fatalf("the owners still get the question: %s", got)
	}
	if !strings.Contains(q.Context, "cargo is missing") || !strings.Contains(q.Context, "not offered: no option has actions the operator runs") ||
		!strings.Contains(q.Context, "`merge` is not a tool of the pilot") {
		t.Fatalf("the context says why the pilot's options went:\n%s", q.Context)
	}

	// One option alone is no prompt either: the same.
	e.assess("event/b", aiv1.PromptOption{Label: "Do nothing", Recommended: true})
	e.reconcile()
	if got := optionLabels(e.prompt(specTaskName, "Q2")); got != "A:Look into it myself* B:Do nothing" {
		t.Fatalf("a prompt of one option gets the two: %s", got)
	}
}

func TestPilotManagedRunsTheRecommendedOption(t *testing.T) {
	e := newManagedEnv(t)
	tracking := e.tracking()
	seen := len(botComments(e, tracking))
	before := e.toolEffect()
	retry := aiv1.PromptOption{Label: "Retry #1", Recommended: true, Actions: []aiv1.PilotAction{{Tool: "retry", IssueIID: 1}}}
	e.assess("event/a", retry, aiv1.PromptOption{Label: "Pause", Actions: []aiv1.PilotAction{{Tool: "pause"}}}, aiv1.PromptOption{Label: "Do nothing"})
	e.reconcile()
	e.reconcile()
	notes := botNotesSince(e, tracking, seen)
	if after := e.toolEffect(); after.runs != before.runs+1 || len(notes) != 1 || !strings.Contains(notes[0], "The pilot acted on its own") ||
		!strings.Contains(notes[0], "**Why:** `env`: no Rust toolchain") {
		t.Fatalf("the recommended retry runs without an answer, audited: %+v → %+v, %q", before, after, notes)
	}
	if got := promptStates(e.task(specTaskName)); got != "" {
		t.Fatalf("what the pilot runs on its own is no prompt: %s", got)
	}
	if ps := e.pilot().Status.Pilot; len(ps.Accepted) != 0 || ps.Interventions["1"].Count != 1 {
		t.Fatalf("it counts as one intervention: %+v", ps)
	}

	// A recommended option it does not run on its own (a queue tool, or
	// "do nothing") goes to the owners whole, and counts nothing.
	retry.Recommended = false
	e.assess("event/b", aiv1.PromptOption{Label: "Pause", Recommended: true, Actions: []aiv1.PilotAction{{Tool: "pause"}}}, retry, aiv1.PromptOption{Label: "Do nothing"})
	e.assess("event/c", aiv1.PromptOption{Label: "Do nothing", Recommended: true}, retry)
	e.reconcile()
	if a, b := optionLabels(e.prompt(specTaskName, "Q1")), optionLabels(e.prompt(specTaskName, "Q2")); a != "A:Pause* B:Retry #1 C:Do nothing" || b != "A:Do nothing* B:Retry #1" {
		t.Fatalf("the owners choose: %s / %s", a, b)
	}
	if after := e.toolEffect(); after.runs != before.runs+1 || e.pilot().Status.Pilot.Interventions["1"].Count != 1 {
		t.Fatalf("nothing more ran or counted: %+v", after)
	}
}
