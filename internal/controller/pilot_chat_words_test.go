package controller

import (
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

const retryHelper = "tell #1 to use the existing retry helper"

// alicesWords is the instruct a chat turn on retryHelper records: her words
// verbatim, in her name.
var alicesWords = aiv1.PilotAction{Tool: "instruct", IssueIID: 1, Owner: "alice@example.com", Text: "use the existing retry helper"}

// chatPrompted reports whether the chat proposal of message 7 waits as a
// proposal prompt.
func (e *projectEnv) chatPrompted() bool {
	return slices.ContainsFunc(e.task(specTaskName).Status.Prompts, func(q aiv1.Prompt) bool {
		return q.Source.Kind == aiv1.PromptProposal && q.Source.Key == "chat/7" && !q.State.Closed()
	})
}

func TestPilotChatPassesAnOwnersOwnWordsOnWithoutAnAccept(t *testing.T) {
	for _, running := range []bool{false, true} {
		e := newProblemChatEnv(t, aiv1.PilotPropose)
		if running {
			e.runningOf(1)
		}
		e.chatMessage(7, "alice@example.com", retryHelper)
		e.chatPropose(7, 7, 1, alicesWords)
		e.reconcile()
		task := e.implTaskOf(1)
		switch {
		case e.chatPrompted():
			t.Fatalf("running %v: no Accept card", running)
		case running && (len(task.Spec.Source.OwnerMessages) != 1 || task.Spec.Source.OwnerMessages[0].Text != alicesWords.Text ||
			task.Spec.Source.OwnerMessages[0].ID != "chat/7"):
			t.Fatalf("the running implementer gets her message: %+v", task.Spec.Source.OwnerMessages)
		case !running && (task.Spec.Source.PilotInstruction == nil || task.Spec.Source.PilotInstruction.Text != alicesWords.Text):
			t.Fatalf("the next run gets her words: %+v", task.Spec.Source.PilotInstruction)
		}
		if acted := e.acted(); len(acted) != 1 || acted[0].By != "alice" || !acted[0].Ran {
			t.Fatalf("running %v: acted as alice: %+v", running, acted)
		}
	}
}

func TestPilotChatKeepsWordsThatAreNotTheOwnersAProposal(t *testing.T) {
	for name, tc := range map[string]struct {
		autonomy aiv1.PilotAutonomy
		actions  []aiv1.PilotAction
		prompted bool
	}{
		"not verbatim":        {aiv1.PilotManaged, []aiv1.PilotAction{{Tool: "instruct", IssueIID: 1, Owner: "alice@example.com", Text: "delete the retry helper"}}, false},
		"with another action": {aiv1.PilotManaged, []aiv1.PilotAction{alicesWords, {Tool: "retry", IssueIID: 1}}, true},
		// The pilot's own instruct from a chat turn never runs by autonomy.
		"a pilot kind under managed autonomy": {aiv1.PilotManaged, []aiv1.PilotAction{{Tool: "instruct", IssueIID: 1, Kind: "rerun", Note: "use the helper"}}, true},
	} {
		t.Run(name, func(t *testing.T) {
			e := newProblemChatEnv(t, tc.autonomy)
			before := e.toolEffect()
			e.chatMessage(7, "alice@example.com", retryHelper)
			e.chatPropose(7, 7, 1, tc.actions...)
			e.reconcile()
			e.reconcile()
			task := e.implTaskOf(1)
			if after := e.toolEffect(); after != before || len(e.acted()) != 0 || len(task.Spec.Source.OwnerMessages) != 0 {
				t.Fatalf("nothing runs: %+v → %+v, acted %+v", before, after, e.acted())
			}
			if e.chatPrompted() != tc.prompted {
				t.Fatalf("prompted %v, want %v: %+v", !tc.prompted, tc.prompted, e.task(specTaskName).Status.Prompts)
			}
			for _, q := range e.task(specTaskName).Status.Prompts {
				if strings.Contains(q.Context+q.Options[0].Detail, "via pilot") {
					t.Fatalf("no words in alice's name wait for an Accept: %+v", q)
				}
			}
		})
	}
}

func TestOwnWords(t *testing.T) {
	m := aiv1.ChatMessage{ID: 7, Author: "alice@example.com", Text: "  " + retryHelper + "\n"}
	one := func(f func(*aiv1.PilotAction)) []aiv1.PilotAction {
		a := alicesWords
		f(&a)
		return []aiv1.PilotAction{a}
	}
	for name, tc := range map[string]struct {
		p    aiv1.PilotProposal
		want bool
	}{
		"verbatim":          {aiv1.PilotProposal{Trigger: 1, Actions: one(func(*aiv1.PilotAction) {})}, true},
		"trimmed":           {aiv1.PilotProposal{Trigger: 1, Actions: one(func(a *aiv1.PilotAction) { a.Text = " use the existing retry helper\n" })}, true},
		"the whole message": {aiv1.PilotProposal{Trigger: 1, Actions: one(func(a *aiv1.PilotAction) { a.Text = retryHelper })}, true},
		"not in it":         {aiv1.PilotProposal{Trigger: 1, Actions: one(func(a *aiv1.PilotAction) { a.Text = "use a new retry helper" })}, false},
		"empty":             {aiv1.PilotProposal{Trigger: 1, Actions: one(func(a *aiv1.PilotAction) { a.Text = "  " })}, false},
		"another owner":     {aiv1.PilotProposal{Trigger: 1, Actions: one(func(a *aiv1.PilotAction) { a.Owner = "bob@example.com" })}, false},
		"another issue":     {aiv1.PilotProposal{Trigger: 2, Actions: one(func(*aiv1.PilotAction) {})}, false},
		"a pilot kind too":  {aiv1.PilotProposal{Trigger: 1, Actions: one(func(a *aiv1.PilotAction) { a.Kind = "rerun" })}, false},
		"a pilot note too":  {aiv1.PilotProposal{Trigger: 1, Actions: one(func(a *aiv1.PilotAction) { a.Note = "and more" })}, false},
		"another tool":      {aiv1.PilotProposal{Trigger: 1, Actions: one(func(a *aiv1.PilotAction) { a.Tool = "retry" })}, false},
		"two actions":       {aiv1.PilotProposal{Trigger: 1, Actions: append(one(func(*aiv1.PilotAction) {}), aiv1.PilotAction{Tool: "retry", IssueIID: 1})}, false},
		"options": {aiv1.PilotProposal{Trigger: 1, Actions: one(func(*aiv1.PilotAction) {}),
			Options: []aiv1.PromptOption{{Label: "A"}}}, false},
	} {
		if got := ownWords(m, tc.p); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

func TestChatAcceptedNeverRunsWordsInAnOwnersNameOnAProblem(t *testing.T) {
	coord := &aiv1.AgentTask{}
	coord.Spec.OwnerSubjects = []string{"alice@example.com"}
	coord.Spec.Source.ChatMessages = []aiv1.ChatMessage{{ID: 7, Author: "alice@example.com", Text: "problem: #1\ndo it"}}
	st := &aiv1.AgentTask{}
	actions := []aiv1.PilotAction{{Tool: "retry", IssueIID: 1}, {Tool: "instruct", IssueIID: 1, Owner: "alice@example.com", Text: "delete the tests"}}
	p := aiv1.PilotProposal{Trigger: 1, ChatMessageID: 7, Actions: actions, State: aiv1.PilotState(st, nil, actions)}
	if by, why := chatAccepted(st, coord, nil, p); by != "" || why == "" {
		t.Fatalf("the pilot's text in alice's name needs an Accept: accepted by %q", by)
	}
}
