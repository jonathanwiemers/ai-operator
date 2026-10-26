package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

const ruleLines = "\n/agent rule R1: switch review.maxIterations=0\n/agent rule R2: prompt keep functions short\n/agent rule R3: pilot never propose skips"

func TestPromptRulesReachImplementerAndReviewers(t *testing.T) {
	e := newImplementEnv(t)
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2", State: "active", Description: "Build it.\n/agent owners @alice" + ruleLines}
	e.gl.Milestones = []gitlab.Milestone{m}
	e.gl.Issues[42].Milestone = &m
	engine := &scriptedEngine{}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	const block = "## Rules from the milestone owners"
	impl := engine.calls[0].Prompt
	if !strings.Contains(impl, block) || !strings.Contains(impl, "- R2: keep functions short") ||
		strings.Contains(impl, "/agent rule") || strings.Contains(impl, "never propose skips") || strings.Contains(impl, "maxIterations") {
		t.Fatalf("the implementer gets the prompt rules, marked, and no rule lines:\n%s", impl)
	}
	reviewers := 0
	for _, c := range engine.calls[1:] {
		if strings.Contains(c.Prompt, "Automated reviewers found") {
			t.Fatal("review.maxIterations=0 of the milestone: no fix round")
		}
		if c.ReadOnly && c.JSONSchema == reviewSchema {
			reviewers++
			if !strings.Contains(c.Prompt, block) || !strings.Contains(c.Prompt, "- R2: keep functions short") {
				t.Fatalf("a reviewer gets the prompt rules:\n%s", c.Prompt)
			}
		}
	}
	if reviewers != 2 {
		t.Fatalf("expected one review round of 2 reviewers, got %d", reviewers)
	}
}

func TestPilotGetsItsRulesAndRefusesForbiddenSwitches(t *testing.T) {
	e := newPilotEnv(t, "")
	e.gl.Milestones[0].Description += ruleLines
	task := e.task(t)
	task.Spec.Rules = []string{"R1: switch review.maxIterations=0", "R2: prompt keep functions short", "R3: pilot never propose skips"}
	if err := e.k8s.Update(context.Background(), &task); err != nil {
		t.Fatal(err)
	}
	e.engine.proposal = proposalOf(t, `{"summary": "rules", "actions": [{"tool": "setRule", "kind": "switch", "note": "pilot.autonomy=managed"},
		{"tool": "setRule", "kind": "prompt", "note": "also fix lint in touched files"}]}`)
	e.step(t)
	call := e.engine.calls[0]
	if !strings.Contains(call.SystemPrompt, "Rules for you from the milestone's owners") || !strings.Contains(call.SystemPrompt, "- R3: never propose skips") ||
		strings.Contains(call.SystemPrompt, "keep functions short") {
		t.Fatalf("the pilot gets its own rules:\n%s", call.SystemPrompt)
	}
	if !strings.Contains(call.Prompt, `"id": "R2"`) || strings.Contains(call.Prompt, "/agent rule") {
		t.Fatalf("the state has the rules, the spec has no rule lines:\n%s", call.Prompt)
	}
	if replies := botNotes(e.gl, 5); len(replies) != 1 || !strings.Contains(replies[0], "Rule not proposed: `pilot.autonomy` cannot be switched by a rule") {
		t.Fatalf("a forbidden switch is refused in the reply: %q", replies)
	}
	ps := e.task(t).Status.Pilot
	if ps == nil || len(ps.Proposed) != 1 || len(ps.Proposed[0].Actions) != 1 || ps.Proposed[0].Actions[0].Kind != "prompt" {
		t.Fatalf("only the valid rule is proposed: %+v", ps)
	}
}

func TestAssessmentRefusesForbiddenSwitches(t *testing.T) {
	e := newAssessEnv(t, "")
	e.engine.assessment.Proposal = proposalOf(t, `{"summary": "more rounds", "actions": [{"tool": "setRule", "kind": "switch", "note": "lifecycle.maxRunsPerTask=99"},
		{"tool": "retry", "issue": 1}]}`)
	e.step(t)
	if notes := botNotes(e.gl, 5); len(notes) != 1 || !strings.Contains(notes[0], "Rule not proposed: `lifecycle.maxRunsPerTask` cannot be switched by a rule") {
		t.Fatalf("the assessment says the switch is refused: %q", notes)
	}
	if ps := e.task(t).Status.Pilot; ps == nil || len(ps.Proposed) != 1 || len(ps.Proposed[0].Actions) != 1 || ps.Proposed[0].Actions[0].Tool != "retry" {
		t.Fatalf("only the retry is proposed: %+v", ps)
	}
}
