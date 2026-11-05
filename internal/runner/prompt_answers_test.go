package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestBuildPromptAnswers(t *testing.T) {
	got := buildPromptAnswers([]aiv1.PromptAnswerDelivery{
		{Prompt: "Q5", Question: "Which database?", OptionLabel: "PostgreSQL", Text: "Use the managed one.", By: "alice", Request: 2},
		{Prompt: "Q6", Question: "Keep the old API?", Text: "No, drop it.", By: "bob", Request: 2},
		{Prompt: "Q7", OptionLabel: "Yes", Request: 2},
	})
	want := "## Answers to your questions\n\nThe owners answered; continue the work with their answers." +
		"\n\nAnswer to your question Q5 (\"Which database?\") from @alice: PostgreSQL\n\nUse the managed one." +
		"\n\nAnswer to your question Q6 (\"Keep the old API?\") from @bob:\n\nNo, drop it." +
		"\n\nAnswer to your question Q7 from an owner: Yes"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func setPromptAnswers(t *testing.T, c client.Client, name string, runRequest int64, answers ...aiv1.PromptAnswerDelivery) {
	t.Helper()
	task := getTask(t, c, name)
	task.Spec.Source.PromptAnswers = answers
	task.Spec.RunRequest = runRequest
	if err := c.Update(context.Background(), task); err != nil {
		t.Fatal(err)
	}
}

func TestPromptAnswersReachTheImplementerOnce(t *testing.T) {
	e := newImplementEnv(t)
	q5 := aiv1.PromptAnswerDelivery{Prompt: "Q5", Question: "Which database?", OptionLabel: "PostgreSQL", By: "alice", Request: 1}
	q6 := aiv1.PromptAnswerDelivery{Prompt: "Q6", Question: "Keep the old API?", Text: "No, drop it.", By: "bob", Request: 1}
	q7 := aiv1.PromptAnswerDelivery{Prompt: "Q7", Question: "Which port?", OptionLabel: "8080", By: "alice", Request: 3}
	setPromptAnswers(t, e.k8s, e.task.Name, 1, q5, q6, q7)
	// Every run is a new runner, as after a restart of its pod.
	newRunner := func() *Runner {
		return newTestRunner(t, e.tmp, e.k8s, e.gl, &decisionEngine{}, e.task.Name)
	}
	step := func() Invocation {
		t.Helper()
		engine := &decisionEngine{}
		r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
		if _, err := r.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
		return engine.calls[0]
	}

	// A fresh session gets the answers due in its initial prompt, several
	// in one turn; Q7 is for a later run request.
	first := step()
	if first.Resume != "" || strings.Count(first.Prompt, "## Answers to your questions") != 1 ||
		!strings.Contains(first.Prompt, `Answer to your question Q5 ("Which database?") from @alice: PostgreSQL`) ||
		!strings.Contains(first.Prompt, "Answer to your question Q6 (\"Keep the old API?\") from @bob:\n\nNo, drop it.") ||
		strings.Contains(first.Prompt, "Q7") {
		t.Fatalf("fresh prompt: resume=%q\n%s", first.Resume, first.Prompt)
	}

	// The runner restarts: the resumed session does not get them again.
	setPromptAnswers(t, e.k8s, e.task.Name, 2, q5, q6, q7)
	second := step()
	if second.Resume != "sess-dec" || strings.Contains(second.Prompt, "Answer to your question") {
		t.Fatalf("delivered answers came again: resume=%q\n%s", second.Resume, second.Prompt)
	}

	// Q7's request: the resumed session gets only Q7.
	setPromptAnswers(t, e.k8s, e.task.Name, 3, q5, q6, q7)
	third := step()
	if third.Resume != "sess-dec" || !strings.Contains(third.Prompt, "## Answers to your questions") ||
		!strings.Contains(third.Prompt, `Answer to your question Q7 ("Which port?") from @alice: 8080`) ||
		strings.Contains(third.Prompt, "Q5") || strings.Contains(third.Prompt, "Q6") {
		t.Fatalf("resumed prompt: resume=%q\n%s", third.Resume, third.Prompt)
	}
	if st := savedState(t, newRunner()); strings.Join(st.PromptsDelivered, ",") != "Q5,Q6,Q7" {
		t.Fatalf("delivered: %v", st.PromptsDelivered)
	}

	// An archived workspace: the fresh session gets all of them.
	if err := os.Remove(filepath.Join(e.tmp, "runner", "state.json")); err != nil {
		t.Fatal(err)
	}
	setPromptAnswers(t, e.k8s, e.task.Name, 4, q5, q6, q7)
	fresh := step()
	if fresh.Resume != "" || strings.Count(fresh.Prompt, "## Answers to your questions") != 1 ||
		!strings.Contains(fresh.Prompt, "Q5") || !strings.Contains(fresh.Prompt, "Q6") || !strings.Contains(fresh.Prompt, "Q7") {
		t.Fatalf("fresh prompt after archiving: resume=%q\n%s", fresh.Resume, fresh.Prompt)
	}
}

func TestPromptAnswersInALiveChatSession(t *testing.T) {
	e, r, agent := newChatEnv(t)
	q5 := aiv1.PromptAnswerDelivery{Prompt: "Q5", Question: "Which database?", OptionLabel: "PostgreSQL", By: "alice"}
	setPromptAnswers(t, e.k8s, e.task.Name, 0, q5)
	setChatMessages(t, e.k8s, e.task.Name, chatMsg(10, "message", "status?", "alice"))
	if did, err := r.tick(context.Background(), nil); err != nil || !did {
		t.Fatalf("did=%v err=%v", did, err)
	}
	setChatMessages(t, e.k8s, e.task.Name, chatMsg(10, "message", "status?", "alice"), chatMsg(11, "message", "and now?", "alice"))
	if did, err := r.tick(context.Background(), nil); err != nil || !did {
		t.Fatalf("did=%v err=%v", did, err)
	}
	if len(agent.promptCalls) != 2 ||
		!strings.HasPrefix(agent.promptCalls[0], "## Answers to your questions") ||
		!strings.Contains(agent.promptCalls[0], `Answer to your question Q5 ("Which database?") from @alice: PostgreSQL`) ||
		!strings.Contains(agent.promptCalls[0], "status?") ||
		strings.Contains(agent.promptCalls[1], "Answer to your question") {
		t.Fatalf("prompts: %q", agent.promptCalls)
	}
}
