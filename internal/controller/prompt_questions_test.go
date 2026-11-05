package controller

import (
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// implCommand sends body as Desk chat command id of author to the
// Implement task of #1, polls once and returns the reply.
func (e *projectEnv) implCommand(id int64, author, body string) string {
	e.t.Helper()
	task := e.task(e.taskOf(1))
	name, _, _ := strings.Cut(author, "@")
	task.Spec.Source.ChatMessages = append(task.Spec.Source.ChatMessages, aiv1.ChatMessage{ID: id, Kind: "command", Text: body, Author: author, AuthorName: name})
	if err := e.c.Update(e.ctx, task); err != nil {
		e.t.Fatal(err)
	}
	e.reconcile()
	for _, a := range e.task(e.taskOf(1)).Spec.Source.ChatAnswers {
		if a.MessageID == id {
			return a.Text
		}
	}
	e.t.Fatalf("no answer to chat command %d", id)
	return ""
}

// askQuestions finishes run n of issue iid with the agent's questions: a decision
// between two databases (PostgreSQL its default) and a prose question.
func (e *projectEnv) askQuestions(iid int64, n int32) {
	e.t.Helper()
	impl := e.task(e.taskOf(iid))
	e.patchStatus(impl.Name, func(s *aiv1.AgentTaskStatus) {
		s.Run.LastResult, s.Run.RunCount, s.Run.ObservedRunRequest = aiv1.RunResultNeedsInput, n, impl.Spec.RunRequest
		s.Run.MergeRequestIID = 0
		s.Run.Questions = []aiv1.QuestionDraft{
			{Question: "Which database?", Options: []aiv1.PromptOption{
				{ID: "A", Label: "PostgreSQL", Recommended: true}, {ID: "B", Label: "SQLite", Detail: "a file"}}},
			{Question: "Keep the old API?", AllowFreeText: true},
		}
	})
}

func TestAgentQuestionsBecomePromptsOnce(t *testing.T) {
	e := newAnswerEnv(t)
	e.askQuestions(1, 1)
	e.reconcile()
	e.reconcile()

	impl := e.task(e.taskOf(1))
	if got := promptStates(impl); got != "Q1=open Q2=open" {
		t.Fatalf("one prompt per question, made once: %s", got)
	}
	q1, q2 := impl.Status.Prompts[0], impl.Status.Prompts[1]
	if src := q1.Source; src.Kind != aiv1.PromptNeedsInput || src.IssueIID != 1 || src.Task != impl.Name || src.Key != "run/1/0" ||
		src.RunRequest != impl.Spec.RunRequest {
		t.Fatalf("the prompt names its issue, task, run and question: %+v", src)
	}
	if len(q1.Options) != 2 || !q1.Options[0].Recommended || q1.Options[1].Detail != "a file" || q1.AllowFreeText {
		t.Fatalf("the decision keeps its options: %+v", q1)
	}
	if q2.Source.Key != "run/1/1" || len(q2.Options) != 0 || !q2.AllowFreeText || q2.Question != "Keep the old API?" {
		t.Fatalf("the prose question is one free-text prompt: %+v", q2)
	}
	if st := e.task(specTaskName); st.Status.LastPrompt != 2 {
		t.Fatalf("the ids are the milestone's: %d", st.Status.LastPrompt)
	}
	notes := promptNotes(e, 1)
	if len(notes) != 2 || !strings.Contains(notes[0].Body, "🧭 **Q1:** Which database?") || !strings.Contains(notes[0].Body, "- **B** SQLite — a file") ||
		!strings.Contains(notes[1].Body, "🧭 **Q2:** Keep the old API?") {
		t.Fatalf("each question is a comment on the issue: %+v", notes)
	}
	if !q1.Quiet || strings.Contains(notes[0].Body, "cc @") {
		t.Fatalf("on autopilot nobody is mentioned: %s", notes[0].Body)
	}

	// The next run's questions are prompts of their own.
	impl.Spec.RunRequest++
	if err := e.c.Update(e.ctx, impl); err != nil {
		t.Fatal(err)
	}
	e.askQuestions(1, 2)
	e.reconcile()
	if got := promptStates(e.task(e.taskOf(1))); got != "Q1=superseded Q2=superseded Q3=open Q4=open" {
		t.Fatalf("the questions of run 2 are new prompts: %s", got)
	}
}

func TestAgentQuestionsMentionTheOwnersOnce(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.Users = []gitlab.User{alice}
	e.r.DefaultOwners = []string{"alice"}
	e.gl.AddIssue(42, "Add PayPal button", "agent::working")
	e.reconcile()
	e.askQuestions(42, 1)
	e.reconcile()
	impl := e.task(e.taskOf(42))
	if got := promptStates(impl); got != "Q1=open Q2=open" || impl.Status.Prompts[0].Quiet || !impl.Status.Prompts[1].Quiet {
		t.Fatalf("without a milestone the task counts; the first prompt mentions: %s %+v", got, impl.Status.Prompts)
	}
	notes := promptNotes(e, 42)
	if len(notes) != 2 || !strings.Contains(notes[0].Body, "cc @alice") || strings.Contains(notes[1].Body, "cc @") {
		t.Fatalf("the owners are mentioned once: %+v", notes)
	}
}

func TestAgentQuestionsAnsweredFromTheDesk(t *testing.T) {
	e := newAnswerEnv(t)
	e.askQuestions(1, 1)
	e.reconcile()
	impl := e.task(e.taskOf(1))
	runs := impl.Spec.RunRequest
	if !chatCommandAllowed(*impl, chatcmd.Command{Cmd: "answer", Prompt: "Q1", Option: "A"}) {
		t.Fatal("answer is delivered onto Implement tasks")
	}

	if r := e.implCommand(401, "mallory@example.com", answerBody("Q1", "A")); r != "Only the milestone's owners can answer prompts." {
		t.Fatalf("a non-owner is refused: %q", r)
	}
	if r := e.implCommand(402, "alice@example.com", answerBody("Q1", "B")); r != "Q1 answered: SQLite. The agent continues once Q2 is answered too." {
		t.Fatalf("reply %q", r)
	}
	impl = e.task(e.taskOf(1))
	if impl.Spec.RunRequest != runs {
		t.Fatal("no run while a question of the run is open")
	}
	if p := impl.Status.Prompts[0]; p.State != aiv1.PromptAnswered || p.Answer == nil || p.Answer.OptionID != "B" || p.Answer.By != "alice" || p.Answer.Via != "desk" {
		t.Fatalf("the answer is shown on the prompt: %+v", p)
	}
	if r := e.implCommand(403, "alice@example.com", answerBody("Q1", "A")); r != "Q1 was answered already by @alice: SQLite." {
		t.Fatalf("a second answer names the first: %q", r)
	}
	if r := e.implCommand(404, "alice@example.com", answerBody("Q2", "A")); r != "Q2 has no option A." {
		t.Fatalf("reply %q", r)
	}

	text := `{"cmd":"answer","prompt":"Q2","text":"No, drop it."}`
	if r := e.implCommand(405, "alice@example.com", text); r != `Q2 answered: "No, drop it.". The agent continues with your answers.` {
		t.Fatalf("reply %q", r)
	}
	impl = e.task(e.taskOf(1))
	if impl.Spec.RunRequest != runs+1 {
		t.Fatalf("the last answer requests the run: %d → %d", runs, impl.Spec.RunRequest)
	}
	want := []aiv1.PromptAnswerDelivery{
		{Prompt: "Q1", Question: "Which database?", OptionID: "B", OptionLabel: "SQLite", By: "alice", Request: runs + 1},
		{Prompt: "Q2", Question: "Keep the old API?", Text: "No, drop it.", By: "alice", Request: runs + 1},
	}
	if !slices.Equal(impl.Spec.Source.PromptAnswers, want) {
		t.Fatalf("both answers go with that run:\n%+v\nwant\n%+v", impl.Spec.Source.PromptAnswers, want)
	}
	if got := promptStates(impl); got != "Q1=answered Q2=answered" {
		t.Fatalf("the newer run supersedes no answered question: %s", got)
	}
	if l := e.labelsOf(1); !slices.Contains(l, "agent::working") || slices.Contains(l, "agent::needs-input") {
		t.Fatalf("the issue moves to working: %v", l)
	}
	e.reconcile()
	var replies []string
	for _, b := range botComments(e, 1) {
		if strings.HasPrefix(b, "✅ **Q") {
			replies = append(replies, b)
		}
	}
	if len(replies) != 2 || !strings.Contains(replies[0], "**B** SQLite") || !strings.Contains(replies[1], "> No, drop it.") {
		t.Fatalf("the issue's comments show the answers: %q", replies)
	}
	if r := e.implCommand(406, "alice@example.com", text); r != `Q2 was answered already by @alice: "No, drop it.".` {
		t.Fatalf("a second text answer is refused: %q", r)
	}
	if e.task(e.taskOf(1)).Spec.RunRequest != runs+1 {
		t.Fatal("a refused answer requests no run")
	}
}

func TestAgentQuestionSupersededByANewerRun(t *testing.T) {
	e := newAnswerEnv(t)
	e.askQuestions(1, 1)
	e.reconcile()
	e.implCommand(401, "alice@example.com", answerBody("Q1", "A"))
	// "/agent retry" or a new label: the agent runs without the other answer.
	impl := e.task(e.taskOf(1))
	impl.Spec.RunRequest++
	if err := e.c.Update(e.ctx, impl); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if got := promptStates(e.task(e.taskOf(1))); got != "Q1=answered Q2=superseded" {
		t.Fatalf("the open question goes, the answer stays: %s", got)
	}
	if r := e.implCommand(402, "alice@example.com", `{"cmd":"answer","prompt":"Q2","text":"yes"}`); r != "Q2 no longer applies (superseded)." {
		t.Fatalf("reply %q", r)
	}
}
