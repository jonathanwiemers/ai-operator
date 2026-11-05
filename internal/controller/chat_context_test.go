package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// fakeChatContext is a chat source with fixed rows, recording the tasks asked.
type fakeChatContext struct {
	rows  []store.ChatMessage
	err   error
	asked []string
}

func (f *fakeChatContext) ChatContext(_ context.Context, namespace, project, task string, limit int) ([]store.ChatMessage, error) {
	f.asked = append(f.asked, namespace+"/"+project+"/"+task)
	if limit < len(f.rows) {
		return f.rows[len(f.rows)-limit:], f.err
	}
	return f.rows, f.err
}

// chatRows are n rows of size bytes each, alternating user and agent.
func chatRows(n, size int) []store.ChatMessage {
	at := time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC)
	var rows []store.ChatMessage
	for i := range n {
		m := store.ChatMessage{ID: int64(i + 1), Role: "user", Author: "sub-alice", AuthorName: "Alice", Body: strings.Repeat("x", size), At: at.Add(time.Duration(i) * time.Minute)}
		if i%2 == 1 {
			m.Role, m.Author, m.AuthorName = "agent", "", "claude"
		}
		rows = append(rows, m)
	}
	return rows
}

func TestChatContextIsCutFromTheOldest(t *testing.T) {
	got := chatContext(chatRows(25, 2000))
	// 15 × 2000 bytes fit into 30 KiB, a 16th does not.
	if len(got) != 15 || got[0].ID != 11 || got[14].ID != 25 {
		t.Fatalf("want the newest 15 rows oldest first, got %d: first %d", len(got), got[0].ID)
	}
	if got[1].Role != "agent" || got[1].Kind != "message" || got[0].AuthorName != "Alice" || got[0].At.IsZero() {
		t.Fatalf("fields not copied: %+v", got[1])
	}

	got = chatContext(chatRows(25, 10))
	if len(got) != maxChatContext || got[0].ID != 6 {
		t.Fatalf("want at most %d messages, the newest, got %d from %d", maxChatContext, len(got), got[0].ID)
	}

	long := chatRows(3, 20<<10)
	got = chatContext(long)
	if len(got) != 1 || got[0].ID != 3 || len(got[0].Text) > maxChatContextText {
		t.Fatalf("a long reply is cut to the CRD's bound and the total to 30 KiB: %d messages, %d bytes", len(got), len(got[0].Text))
	}
}

func TestRunRequestCarriesTheChatOnADeskProject(t *testing.T) {
	e := newProjectEnv(t)
	e.patchProject(inputs(aiv1.InputDesk))
	src := &fakeChatContext{rows: chatRows(25, 100)}
	e.r.ChatContext = src
	e.gl.AddIssue(42, "Add PayPal button", "agent::ready")
	e.reconcile()

	task := e.tasks()[issueTaskName]
	if task.Spec.RunRequest != 1 || len(task.Spec.Source.ChatContext) != maxChatContext || task.Spec.Source.ChatContext[19].ID != 25 {
		t.Fatalf("want the run request with the last 20 messages, got request %d, %d messages", task.Spec.RunRequest, len(task.Spec.Source.ChatContext))
	}
	if len(src.asked) != 1 || src.asked[0] != ns+"/shop/"+issueTaskName {
		t.Fatalf("asked for %v", src.asked)
	}

	// Every run request reads it again; a failed read keeps the earlier one.
	src.rows = chatRows(2, 100)
	spec := e.project().Spec.WithDefaults()
	if err := e.r.requestRun(e.ctx, e.gl, spec, &task, 42); err != nil {
		t.Fatal(err)
	}
	if got := e.tasks()[issueTaskName].Spec.Source.ChatContext; len(got) != 2 {
		t.Fatalf("want the chat read again, got %d messages", len(got))
	}
	src.err = errors.New("database down")
	task = e.tasks()[issueTaskName]
	if err := e.r.requestRun(e.ctx, e.gl, spec, &task, 42); err != nil {
		t.Fatal(err)
	}
	if got := e.tasks()[issueTaskName]; got.Spec.RunRequest != 3 || len(got.Spec.Source.ChatContext) != 2 {
		t.Fatalf("want the run requested with the earlier chat, got request %d, %d messages", got.Spec.RunRequest, len(got.Spec.Source.ChatContext))
	}
}

func TestRunRequestWithoutChatContext(t *testing.T) {
	for name, c := range map[string]struct {
		inputs []aiv1.InputSource
		src    bool
	}{
		"gitlab input":   {[]aiv1.InputSource{aiv1.InputGitLab, aiv1.InputDesk}, true},
		"no database":    {[]aiv1.InputSource{aiv1.InputDesk}, false},
		"default inputs": {nil, true},
	} {
		t.Run(name, func(t *testing.T) {
			e := newProjectEnv(t)
			e.patchProject(inputs(c.inputs...))
			src := &fakeChatContext{rows: chatRows(3, 10)}
			if c.src {
				e.r.ChatContext = src
			}
			e.gl.AddIssue(42, "Add PayPal button", "agent::ready")
			e.reconcile()
			task := e.tasks()[issueTaskName]
			if task.Spec.RunRequest != 1 || task.Spec.Source.ChatContext != nil || len(src.asked) != 0 {
				t.Fatalf("want a run request without chat, got request %d, %d messages, asked %v", task.Spec.RunRequest, len(task.Spec.Source.ChatContext), src.asked)
			}
		})
	}
}
