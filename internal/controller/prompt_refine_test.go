package controller

import (
	"fmt"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func textBody(prompt, text string) string {
	return fmt.Sprintf(`{"cmd":"answer","prompt":%q,"text":%q}`, prompt, text)
}

// deskText sends body as Desk chat command id of author to the pilot, polls
// once and returns the answer, "" for none yet.
func (e *projectEnv) deskText(id int64, author, body string) string {
	e.t.Helper()
	pilot := e.pilot()
	name, _, _ := strings.Cut(author, "@")
	pilot.Spec.Source.ChatMessages = append(pilot.Spec.Source.ChatMessages, aiv1.ChatMessage{ID: id, Kind: "command", Text: body, Author: author, AuthorName: name})
	if err := e.c.Update(e.ctx, &pilot); err != nil {
		e.t.Fatal(err)
	}
	e.reconcile()
	return e.chatAnswer(id)
}

func (e *projectEnv) chatAnswer(id int64) string {
	e.t.Helper()
	for _, a := range e.pilot().Spec.Source.ChatAnswers {
		if a.MessageID == id {
			return a.Text
		}
	}
	return ""
}

// refine has the pilot answer the refine of chat message id with res, as
// the runner does, polls once and returns the answer to the message.
func (e *projectEnv) refine(id int64, res aiv1.PromptRefinement) string {
	e.t.Helper()
	for _, rf := range e.pilot().Spec.Source.PromptRefines {
		if rf.MessageID == id {
			res.Prompt, res.MessageID, res.Request = rf.Prompt, rf.MessageID, rf.Request
		}
	}
	if res.Request == 0 {
		e.t.Fatalf("no refine of message %d", id)
	}
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) {
		if s.Pilot == nil {
			s.Pilot = &aiv1.PilotStatus{}
		}
		s.Pilot.Refined = append(s.Pilot.Refined, res)
	})
	e.reconcile()
	return e.chatAnswer(id)
}

// freeTextPrompt is trackingPrompt (four options, A recommended) with a
// text box.
func freeTextPrompt() aiv1.Prompt {
	p := trackingPrompt()
	p.AllowFreeText = true
	return p
}

func pilotOption(label string, actions ...aiv1.PilotAction) *aiv1.PromptOption {
	return &aiv1.PromptOption{Label: "From your answer: " + label, Detail: "d", Actions: actions, FromAnswer: true}
}

// answerLabels lists the prompt's options as "id:label", * marking the
// recommended one and + one from an answer.
func answerLabels(p aiv1.Prompt) string {
	var out []string
	for _, o := range p.Options {
		s := o.ID + ":" + o.Label
		if o.Recommended {
			s += "*"
		}
		if o.FromAnswer {
			s += "+"
		}
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

const producerOptions = "A:Retry #1* B:Re-review #1 @mallory C:Pause D:Do nothing"

func TestFreeTextGoesToThePilot(t *testing.T) {
	e := newAnswerEnv(t)
	st := e.task(specTaskName)
	e.addPrompt(st, st, freeTextPrompt())
	e.reconcile()
	before := e.pilot().Spec.RunRequest

	if r := e.deskText(301, "mallory@x", textBody("Q1", "re-review it")); r != "Only the milestone's owners can answer prompts." {
		t.Fatalf("a non-owner is refused: %q", r)
	}
	if r := e.deskText(302, "alice@example.com", textBody("Q1", "re-review it, then retry")); r != "" {
		t.Fatalf("the owner's text is answered once the pilot has: %q", r)
	}
	pilot := e.pilot()
	if pilot.Spec.RunRequest != before+1 || pilot.Spec.Source.LastChatCommandID != 302 {
		t.Fatalf("one run request, the command taken: %d → %d, mark %d", before, pilot.Spec.RunRequest, pilot.Spec.Source.LastChatCommandID)
	}
	if rfs := pilot.Spec.Source.PromptRefines; len(rfs) != 1 {
		t.Fatalf("one refine: %+v", rfs)
	} else if rf := rfs[0]; rf.Prompt != "Q1" || rf.Text != "re-review it, then retry" || rf.By != "alice" || rf.MessageID != 302 ||
		rf.Request != pilot.Spec.RunRequest || rf.Question != freeTextPrompt().Question || len(rf.Options) != 4 {
		t.Fatalf("the refine carries the prompt and the text: %+v", rf)
	}
	if p := e.prompt(specTaskName, "Q1"); p.State != aiv1.PromptOpen || p.Answer != nil {
		t.Fatalf("the prompt stays open: %+v", p)
	}

	for i := range aiv1.MaxRefines - 1 {
		if r := e.deskText(int64(303+i), "alice@example.com", textBody("Q1", "again")); r != "" {
			t.Fatalf("refine %d: %q", i+2, r)
		}
	}
	runs := e.pilot().Spec.RunRequest
	if r := e.deskText(310, "alice@example.com", textBody("Q1", "once more")); r != "The pilot is still working on your earlier answers; answer again once it has answered them." {
		t.Fatalf("a sixth refine is refused: %q", r)
	}
	if pilot := e.pilot(); len(pilot.Spec.Source.PromptRefines) != aiv1.MaxRefines || pilot.Spec.RunRequest != runs {
		t.Fatalf("the sixth changes nothing: %d refines, run request %d → %d", len(pilot.Spec.Source.PromptRefines), runs, pilot.Spec.RunRequest)
	}
}

func TestPilotOptionIsAddedForTheOwnerToConfirm(t *testing.T) {
	e := newAnswerEnv(t)
	st := e.task(specTaskName)
	e.addPrompt(st, st, freeTextPrompt())
	e.reconcile()
	tracking := e.autopilot().TrackingIssueIID
	comments := len(botComments(e, tracking))
	before := e.toolEffect()

	e.deskText(301, "alice@example.com", textBody("Q1", "re-review it"))
	if r := e.refine(301, aiv1.PromptRefinement{Option: pilotOption("re-review #1", aiv1.PilotAction{Tool: "reReview", IssueIID: 1})}); r != "Q1 has option E from your answer; confirm it there." {
		t.Fatalf("answer: %q", r)
	}
	p := e.prompt(specTaskName, "Q1")
	if got := answerLabels(p); got != producerOptions+" E:From your answer: re-review #1+" || p.State != aiv1.PromptOpen {
		t.Fatalf("the option follows the producer's, the recommendation stays: %s (%s)", got, p.State)
	}
	if after := e.toolEffect(); after != before {
		t.Fatalf("nothing runs before the owner confirms: %+v → %+v", before, after)
	}
	pilot := e.pilot()
	if len(pilot.Spec.Source.PromptRefines) != 0 || len(pilot.Status.Pilot.Refined) != 0 {
		t.Fatalf("the refine and its refinement are taken: %+v %+v", pilot.Spec.Source.PromptRefines, pilot.Status.Pilot.Refined)
	}
	if n := len(botComments(e, tracking)); n != comments {
		t.Fatalf("GitLab hears nothing of the option: %q", botComments(e, tracking)[comments:])
	}

	// A later answer replaces the option from the earlier one.
	e.deskText(302, "alice@example.com", textBody("Q1", "no, just retry it"))
	if r := e.refine(302, aiv1.PromptRefinement{Option: pilotOption("retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})}); r != "Q1 has option E from your answer; confirm it there." {
		t.Fatalf("answer: %q", r)
	}
	if got := answerLabels(e.prompt(specTaskName, "Q1")); got != producerOptions+" E:From your answer: retry #1+" {
		t.Fatalf("the earlier option is replaced: %s", got)
	}

	// One click runs it like any option.
	if r := e.deskCommand(303, "alice@example.com", answerBody("Q1", "E")); r != "Q1 answered: From your answer: retry #1. The outcome follows on the prompt." {
		t.Fatalf("confirming: %q", r)
	}
	if after := e.toolEffect(); after.runs != before.runs+1 || after.reviewFor != before.reviewFor {
		t.Fatalf("the confirmed option's retry ran, nothing else: %+v → %+v", before, after)
	}
	if p := e.prompt(specTaskName, "Q1"); p.State != aiv1.PromptAnswered || !strings.Contains(p.Answer.Outcome, "**retry** #1") {
		t.Fatalf("the outcome is on the prompt: %+v", p.Answer)
	}
}

func TestPilotOptionsThatAreNotAdded(t *testing.T) {
	e := newAnswerEnv(t)
	st := e.task(specTaskName)
	e.addPrompt(st, st, freeTextPrompt())
	e.reconcile()
	for i, tc := range []struct {
		res  aiv1.PromptRefinement
		want string
	}{
		{aiv1.PromptRefinement{Option: pilotOption("retry #99", aiv1.PilotAction{Tool: "retry", IssueIID: 99})},
			"The pilot's option for Q1 was not added:\n\n- **retry** #99: a new run (not run: #99 is not an issue of this milestone"},
		{aiv1.PromptRefinement{FollowUp: "Retry which issue, @bob? <!-- x -->"}, "The pilot asks about your answer on Q1: Retry which issue,"},
		{aiv1.PromptRefinement{Error: "the pilot's budget is used up"}, "The pilot could not turn your answer on Q1 into an option: the pilot's budget is used up."},
	} {
		id := int64(301 + i)
		e.deskText(id, "alice@example.com", textBody("Q1", "do it differently"))
		if r := e.refine(id, tc.res); !strings.HasPrefix(r, tc.want) {
			t.Errorf("%+v: answer %q, want %q…", tc.res, r, tc.want)
		} else if strings.Contains(r, "@bob") || strings.Contains(r, "<!--") {
			t.Errorf("the pilot's words are inert: %q", r)
		}
	}
	if p := e.prompt(specTaskName, "Q1"); answerLabels(p) != producerOptions || p.State != aiv1.PromptOpen {
		t.Fatalf("the prompt keeps its options and stays open: %s (%s)", answerLabels(p), p.State)
	}

	// Its run ended without an answer: the owner hears so.
	e.deskText(310, "alice@example.com", textBody("Q1", "do it differently"))
	e.patchStatus(pilotTaskName, func(s *aiv1.AgentTaskStatus) { s.Run.ObservedRunRequest = e.pilot().Spec.RunRequest })
	e.reconcile()
	if r := e.chatAnswer(310); r != "The pilot's run ended without an answer to yours on Q1; answer again." {
		t.Fatalf("answer: %q", r)
	}

	// The prompt was answered meanwhile.
	e.deskText(311, "alice@example.com", textBody("Q1", "do it differently"))
	e.deskCommand(312, "alice@example.com", answerBody("Q1", "D"))
	if r := e.refine(311, aiv1.PromptRefinement{Option: pilotOption("retry #1", aiv1.PilotAction{Tool: "retry", IssueIID: 1})}); r != "Q1 no longer applies (answered), so the pilot's option for it was not added." {
		t.Fatalf("answer: %q", r)
	}
	if pilot := e.pilot(); len(pilot.Spec.Source.PromptRefines) != 0 || len(pilot.Status.Pilot.Refined) != 0 {
		t.Fatalf("all refines are taken: %+v %+v", pilot.Spec.Source.PromptRefines, pilot.Status.Pilot.Refined)
	}
}

// An option from an answer does not make a proposal prompt's state change,
// which would supersede it.
func TestPilotOptionKeepsAProposalOpen(t *testing.T) {
	e := newAnswerEnv(t)
	st := e.task(specTaskName)
	var project aiv1.AgentProject
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &project); err != nil {
		t.Fatal(err)
	}
	tasks, err := e.r.issueTasks(e.ctx, &project, st.Spec.Milestone.IID)
	if err != nil {
		t.Fatal(err)
	}
	p := mainPrompt("P1")
	p.Source.Kind, p.AllowFreeText = aiv1.PromptProposal, true
	p.PilotState = proposalState(st, tasks, p)
	e.addPrompt(st, st, p)
	e.reconcile()
	e.deskText(301, "alice@example.com", textBody("Q1", "pause instead"))
	if r := e.refine(301, aiv1.PromptRefinement{Option: pilotOption("pause", aiv1.PilotAction{Tool: "pause"})}); r != "Q1 has option C from your answer; confirm it there." {
		t.Fatalf("answer: %q", r)
	}
	e.reconcile()
	if q := e.prompt(specTaskName, "Q1"); q.State != aiv1.PromptOpen || len(q.Options) != 3 {
		t.Fatalf("the proposal stays open with the option: %+v", q)
	}
}
