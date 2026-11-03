package controller

import (
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func TestApplyChatCommands(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.AddIssue(42, "Add PayPal button", "agent::working")
	is := e.gl.Issues[42]
	task := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-42", Namespace: ns},
		Spec: aiv1.AgentTaskSpec{
			ProjectRef: "shop", Type: aiv1.TaskTypeImplement,
			Issue: &aiv1.IssueRef{IID: 42},
			Source: aiv1.SourceState{
				ChatMessages: []aiv1.ChatMessage{
					{ID: 10, Kind: "command", Text: `{"cmd":"retry"}`, Author: "ada"},
					{ID: 11, Kind: "command", Text: `{"cmd":"retry"}`, Author: "ada"},
				},
			},
		},
	}
	spec := testProject().Spec.WithDefaults()

	run, err := e.r.applyChatCommands(e.ctx, e.gl, spec, is, task, nil)
	if err != nil || !run {
		t.Fatalf("retry: run=%v err=%v", run, err)
	}
	if task.Spec.Source.LastChatCommandID != 11 {
		t.Fatalf("LastChatCommandID = %d", task.Spec.Source.LastChatCommandID)
	}
	if len(task.Spec.Source.ChatAnswers) != 2 {
		t.Fatalf("answers = %+v", task.Spec.Source.ChatAnswers)
	}
	// Caller bumps RunRequest once for the whole sync.
	before := task.Spec.Source.LastChatCommandID
	answers := len(task.Spec.Source.ChatAnswers)
	run, err = e.r.applyChatCommands(e.ctx, e.gl, spec, is, task, nil)
	if err != nil || run {
		t.Fatalf("second sync must be idempotent: run=%v err=%v", run, err)
	}
	if task.Spec.Source.LastChatCommandID != before || len(task.Spec.Source.ChatAnswers) != answers {
		t.Fatalf("replayed: mark=%d answers=%d", task.Spec.Source.LastChatCommandID, len(task.Spec.Source.ChatAnswers))
	}
}

func TestApplyChatCommandsUse(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.AddIssue(42, "Add PayPal button", "agent::working", "agent-model::cursor/auto")
	is := e.gl.Issues[42]
	task := &aiv1.AgentTask{
		Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeImplement, Source: aiv1.SourceState{
			ChatMessages: []aiv1.ChatMessage{
				{ID: 5, Kind: "command", Text: `{"cmd":"use","engine":"claude","model":"opus"}`, Author: "bob"},
			},
		}},
	}
	spec := testProject().Spec.WithDefaults()
	run, err := e.r.applyChatCommands(e.ctx, e.gl, spec, is, task, nil)
	if err != nil || run {
		t.Fatalf("use starts no run: run=%v err=%v", run, err)
	}
	if !slices.Contains(is.Labels, "agent-model::claude/opus") {
		t.Fatalf("labels = %v", is.Labels)
	}
	if n := len(task.Spec.Source.ChatAnswers); n != 1 || !strings.Contains(task.Spec.Source.ChatAnswers[0].Text, "claude/opus") {
		t.Fatalf("answer = %+v", task.Spec.Source.ChatAnswers)
	}
	if task.Spec.Source.LastChatCommandID != 5 {
		t.Fatalf("mark = %d", task.Spec.Source.LastChatCommandID)
	}
}

func TestApplyChatCommandsReviewWithoutHead(t *testing.T) {
	e := newProjectEnv(t)
	e.gl.AddIssue(42, "Add PayPal button", "agent::working")
	is := e.gl.Issues[42]
	task := &aiv1.AgentTask{
		Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeImplement, Source: aiv1.SourceState{
			ChatMessages: []aiv1.ChatMessage{
				{ID: 7, Kind: "command", Text: `{"cmd":"review"}`, Author: "ada"},
			},
		}},
	}
	spec := testProject().Spec.WithDefaults()
	run, err := e.r.applyChatCommands(e.ctx, e.gl, spec, is, task, &gitlab.MergeRequest{SHA: "abc"})
	if err != nil || run {
		t.Fatalf("review without pushed head: run=%v err=%v", run, err)
	}
	if got := task.Spec.Source.ChatAnswers[0].Text; got != "There is no pushed head of the agent to review." {
		t.Fatalf("answer = %q", got)
	}
	if task.Spec.Source.ReviewFor != "" {
		t.Fatalf("ReviewFor = %q", task.Spec.Source.ReviewFor)
	}
}
