package controller

import (
	"fmt"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// newAnswerEnv is the managed pilot env whose milestone names
// alice@example.com as the Desk owner.
func newAnswerEnv(t *testing.T) *projectEnv {
	t.Helper()
	e := newManagedEnv(t)
	e.gl.Milestones[0].Description += "\n/agent owner-subjects alice@example.com"
	e.reconcile()
	return e
}

// deskCommand sends body as Desk chat command id of author to the pilot,
// polls once and returns the reply.
func (e *projectEnv) deskCommand(id int64, author, body string) string {
	e.t.Helper()
	pilot := e.pilot()
	name, _, _ := strings.Cut(author, "@")
	pilot.Spec.Source.ChatMessages = append(pilot.Spec.Source.ChatMessages, aiv1.ChatMessage{ID: id, Kind: "command", Text: body, Author: author, AuthorName: name})
	if err := e.c.Update(e.ctx, &pilot); err != nil {
		e.t.Fatal(err)
	}
	e.reconcile()
	for _, a := range e.pilot().Spec.Source.ChatAnswers {
		if a.MessageID == id {
			return a.Text
		}
	}
	e.t.Fatalf("no answer to chat command %d", id)
	return ""
}

func answerBody(prompt, option string) string {
	return fmt.Sprintf(`{"cmd":"answer","prompt":%q,"option":%q}`, prompt, option)
}

// flakyPrompt asks what to do about flaky #40: hand it to the agent
// (recommended) or do nothing.
func flakyPrompt() aiv1.Prompt {
	return aiv1.Prompt{Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment, Key: "flaky/40"}, Question: "rust:test is flaky: what now?",
		Options: []aiv1.PromptOption{
			{ID: "A", Label: "Hand #40 to the agent", Actions: []aiv1.PilotAction{{Tool: "fixFlaky", IssueIID: 40}}, Recommended: true},
			{ID: "B", Label: "Do nothing"},
		}}
}

// stuckPrompt is mainPrompt as an assessment of the milestone, which no
// source resolves in these tests.
func stuckPrompt() aiv1.Prompt {
	p := mainPrompt("blocked/1")
	p.Source.Kind = aiv1.PromptAssessment
	return p
}

func TestPromptAnswerRunsTheChosenOptionOnce(t *testing.T) {
	e := newAnswerEnv(t)
	tracking := e.autopilot().TrackingIssueIID
	e.flakyIssue(40, 2)
	st := e.task(specTaskName)
	e.addPrompt(st, st, flakyPrompt())
	e.reconcile()

	if r := e.deskCommand(301, "mallory@x", answerBody("Q1", "A")); r != "Only the milestone's owners can answer prompts." {
		t.Fatalf("a non-owner is refused: %q", r)
	}
	if p := e.prompt(specTaskName, "Q1"); p.State != aiv1.PromptOpen || p.Answer != nil || e.gl.Issues[40].Milestone != nil {
		t.Fatalf("a non-owner changes nothing: %+v", p)
	}

	if r := e.deskCommand(302, "alice@example.com", answerBody("Q1", "A")); r != "Q1 answered: Hand #40 to the agent. The outcome follows on the prompt." {
		t.Fatalf("the owner's answer: %q", r)
	}
	p := e.prompt(specTaskName, "Q1")
	if a := p.Answer; p.State != aiv1.PromptAnswered || a == nil || a.OptionID != "A" || a.By != "alice" || a.Via != "desk" || p.ClosedAt == nil {
		t.Fatalf("the answer is recorded: %+v %+v", p, a)
	}
	if !strings.Contains(p.Answer.Outcome, "**fixFlaky** #40") || strings.Contains(p.Answer.Outcome, "not run") {
		t.Fatalf("runAccepted records the outcome on the prompt: %q", p.Answer.Outcome)
	}
	e.handedOver(40, 3)
	if ps := e.pilot().Status.Pilot; len(ps.Accepted) != 0 {
		t.Fatalf("the accepted option ran and left: %+v", ps.Accepted)
	}
	notes := botComments(e, 40)
	if n := notes[len(notes)-1]; !strings.Contains(n, "prompt Q1, answered by `@alice`") {
		t.Fatalf("the flaky issue names the prompt: %s", n)
	}
	var reply string
	for _, c := range botComments(e, tracking) {
		if strings.Contains(c, "Proposal P0") {
			t.Fatalf("no proposal comment for a prompt's option: %s", c)
		}
		if strings.HasPrefix(c, "✅ **Q1** answered by `@alice` in the Desk") {
			reply = c
		}
	}
	if !strings.Contains(reply, "**A** Hand #40 to the agent") || !strings.Contains(reply, "**fixFlaky** #40") {
		t.Fatalf("the mirror replies with the answer and its outcome: %q", botComments(e, tracking))
	}

	// Idempotent: a second answer, the same or another, names the first.
	for i, opt := range []string{"A", "B"} {
		if r := e.deskCommand(int64(303+i), "alice@example.com", answerBody("Q1", opt)); r != "Q1 was answered already by @alice: Hand #40 to the agent." {
			t.Fatalf("a second answer %s is refused: %q", opt, r)
		}
	}
	if ps := e.pilot().Status.Pilot; len(ps.Accepted) != 0 || e.prompt(specTaskName, "Q1").Answer.OptionID != "A" {
		t.Fatalf("a second answer changes nothing: %+v", ps.Accepted)
	}
}

func TestPromptAnswerDoNothingRecordsTheAnswerOnly(t *testing.T) {
	e := newAnswerEnv(t)
	st := e.task(specTaskName)
	e.addPrompt(st, st, stuckPrompt())
	e.reconcile()
	before := e.toolEffect()
	if r := e.deskCommand(301, "alice@example.com", answerBody("Q1", "B")); r != "Q1 answered: Do nothing." {
		t.Fatalf("reply: %q", r)
	}
	p := e.prompt(specTaskName, "Q1")
	if p.State != aiv1.PromptAnswered || p.Answer == nil || p.Answer.OptionID != "B" || p.Answer.Outcome != "" {
		t.Fatalf("the answer is recorded without an outcome: %+v", p)
	}
	if ps := e.pilot().Status.Pilot; ps != nil && len(ps.Accepted) != 0 {
		t.Fatalf("nothing is queued: %+v", ps.Accepted)
	}
	if after := e.toolEffect(); after != before {
		t.Fatalf("nothing runs: %+v → %+v", before, after)
	}
}

func TestPromptAnswerRefusals(t *testing.T) {
	e := newAnswerEnv(t)
	st := e.task(specTaskName)
	e.addPrompt(st, st, stuckPrompt())
	e.addPrompt(st, st, stuckPrompt()) // Q2 supersedes Q1
	free := trackingPrompt()
	free.Source.Kind, free.AllowFreeText = aiv1.PromptRevisit, true // takes its text on its own path
	e.addPrompt(st, st, free)
	e.reconcile()
	before := e.toolEffect()
	for i, tc := range []struct{ body, want string }{
		{answerBody("Q9", "A"), "There is no prompt Q9."},
		{answerBody("Q1", "A"), "Q1 no longer applies (superseded)."},
		{answerBody("Q2", "E"), "Q2 has no option E."},
		{`{"cmd":"answer","prompt":"Q2","text":"retry it twice"}`, "Q2 takes no free text; pick an option."},
		{`{"cmd":"answer","prompt":"Q3","text":"retry it twice"}`, "Free text answers are not handled yet; pick an option."},
		{`{"cmd":"answer","prompt":"Q3"}`, "answer needs either an option or a text"},
	} {
		if r := e.deskCommand(int64(301+i), "alice@example.com", tc.body); r != tc.want {
			t.Errorf("%s: reply %q, want %q", tc.body, r, tc.want)
		}
	}
	if got := promptStates(e.task(specTaskName)); got != "Q1=superseded Q2=open Q3=open" {
		t.Fatalf("refusals change no prompt: %s", got)
	}
	if after := e.toolEffect(); after != before {
		t.Fatalf("nothing runs: %+v → %+v", before, after)
	}
}

func TestPromptAnswerToAnExpiredProposal(t *testing.T) {
	e := newAnswerEnv(t)
	st := e.task(specTaskName)
	p := mainPrompt("P1")
	p.Source.Kind = aiv1.PromptProposal
	e.addPrompt(st, st, p)
	e.reconcile()
	e.gl.Clock = e.gl.Clock.Add(proposalTTL)
	if r := e.deskCommand(301, "alice@example.com", answerBody("Q1", "A")); r != "Q1 no longer applies (expired)." {
		t.Fatalf("reply: %q", r)
	}
	if ps := e.pilot().Status.Pilot; ps != nil && len(ps.Accepted) != 0 {
		t.Fatalf("nothing is queued: %+v", ps.Accepted)
	}
}
