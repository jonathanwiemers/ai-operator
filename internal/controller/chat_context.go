package controller

import (
	"context"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// ChatContextSource reads a task's Desk chat from the research database.
type ChatContextSource interface {
	ChatContext(ctx context.Context, namespace, project, task string, limit int) ([]store.ChatMessage, error)
}

const (
	// maxChatContext and maxChatContextBytes bound what a fresh session of a
	// project without GitLab input gets of the chat: the last 20 messages,
	// at most 30 KiB of text.
	maxChatContext      = 20
	maxChatContextBytes = 30 << 10
	// maxChatContextText is the CRD's bound of one message's text.
	maxChatContextText = 16 << 10
)

// setChatContext hands an Implement task of a project without GitLab input
// its chat with a run request (spec.source.chatContext): a fresh session
// gets it instead of the comment threads. Without a database it stays
// empty; when the chat cannot be read, the earlier one stays.
func (r *AgentProjectReconciler) setChatContext(ctx context.Context, spec aiv1.AgentProjectSpec, task *aiv1.AgentTask) {
	if task.Spec.Type != aiv1.TaskTypeImplement || spec.Workflow.Takes(aiv1.InputGitLab) {
		return
	}
	if r.ChatContext == nil {
		log.FromContext(ctx).Info("no research database: a fresh session gets no chat context", "task", task.Name)
		return
	}
	rows, err := r.ChatContext.ChatContext(ctx, task.Namespace, task.Spec.ProjectRef, task.Name, maxChatContext)
	if err != nil {
		log.FromContext(ctx).Error(err, "cannot read the chat context, keeping the earlier one", "task", task.Name)
		return
	}
	task.Spec.Source.ChatContext = chatContext(rows)
}

// chatContext is the newest of rows (oldest first) that fit
// maxChatContext and maxChatContextBytes, cut from the oldest.
func chatContext(rows []store.ChatMessage) []aiv1.ChatMessage {
	var out []aiv1.ChatMessage
	size := 0
	for i := len(rows) - 1; i >= 0 && len(out) < maxChatContext; i-- {
		m := rows[i]
		text := textutil.Truncate(m.Body, maxChatContextText-len("…"))
		if size += len(text); size > maxChatContextBytes {
			break
		}
		out = append(out, aiv1.ChatMessage{ID: m.ID, Kind: "message", Role: m.Role, Text: text,
			Author: m.Author, AuthorName: m.AuthorName, At: metav1.NewTime(m.At)})
	}
	slices.Reverse(out)
	return out
}
