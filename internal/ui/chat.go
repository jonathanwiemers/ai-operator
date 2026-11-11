package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// maxChatBody bounds a Desk chat message posted from the UI (16 KiB).
const maxChatBody = 16 << 10

var chatKinds = map[string]bool{"message": true, "interrupt": true, "command": true}

// chatRequest is a message posted from the Desk composer.
type chatRequest struct {
	Body      string `json:"body"`
	Kind      string `json:"kind"`
	ClientKey string `json:"clientKey"`
}

// ChatMessageView is one chat row as the API returns it.
type ChatMessageView struct {
	ID         int64     `json:"id"`
	At         time.Time `json:"at"`
	Author     string    `json:"author,omitempty"`
	AuthorName string    `json:"authorName,omitempty"`
	Role       string    `json:"role" tstype:"ChatRole"`
	Kind       string    `json:"kind" tstype:"ChatKind"`
	Body       string    `json:"body,omitempty"`
	ReplyTo    int64     `json:"replyTo,omitempty"`
	State      string    `json:"state,omitempty" tstype:"ChatState"`
	Reason     string    `json:"reason,omitempty"`
	Redacted   bool      `json:"redacted,omitempty"`
}

// postChat inserts a user chat message for the task. Needs the chat grant
// and the chat writer login; 201 on create, 200 when the client key already
// existed (retry-safe). The audit line never includes the text.
func (s *Server) postChat(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.ChatEnabled() {
		notFound(w)
		return
	}
	p := principalOf(r)
	ns, name, taskName := r.PathValue("namespace"), r.PathValue("name"), r.PathValue("task")
	if !s.authz.CanChat(p, ns, name) {
		if !s.authz.CanView(p, ns, name) {
			notFound(w)
			return
		}
		writeError(w, http.StatusForbidden, "no access to chat")
		return
	}
	_, task, ok := s.loadTask(w, r)
	if !ok {
		return
	}
	req, ok := readChat(w, r)
	if !ok {
		return
	}
	if ok, _ := s.chats.allow(p.Username, time.Now()); !ok {
		writeError(w, http.StatusTooManyRequests, "too many chat messages, try again in a minute")
		return
	}
	st := s.chat.Load()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "the chat database is not connected yet")
		return
	}
	id, created, err := st.InsertUserMessage(r.Context(), store.ChatMessage{
		Namespace:  ns,
		Project:    name,
		Task:       task.Name,
		Author:     p.Username,
		AuthorName: p.Username, // display name is not stored in the session
		Kind:       req.Kind,
		Body:       req.Body,
		ClientKey:  req.ClientKey,
		ExpiresAt:  time.Now().Add(s.historyRetention),
	})
	if err != nil {
		s.internalError(w, err)
		return
	}
	s.log.Info("audit", "action", "chat.post", "user", p.Username, "namespace", ns, "project", name,
		"task", taskName, "kind", req.Kind, "id", id, "created", created)
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	writeJSON(w, code, ChatPostResponse{ID: id})
}

// ChatPostResponse is POST .../tasks/{task}/chat: the message's id, also
// when its client key matched an earlier message.
type ChatPostResponse struct {
	ID int64 `json:"id"`
}

// ChatPage is GET .../tasks/{task}/chat (newest first).
type ChatPage struct {
	Messages []ChatMessageView `json:"messages"`
}

// getChat returns the task's chat messages, newest first. Bodies only with
// the conversation grant.
func (s *Server) getChat(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Research.Enabled() {
		notFound(w)
		return
	}
	ns, name := r.PathValue("namespace"), r.PathValue("name")
	if !s.authz.CanView(principalOf(r), ns, name) {
		notFound(w)
		return
	}
	_, task, ok := s.loadTask(w, r)
	if !ok {
		return
	}
	st := s.research.Load()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "the history database is not connected yet")
		return
	}
	q := r.URL.Query()
	var before, after int64
	var err error
	if b := q.Get("before"); b != "" {
		if before, err = strconv.ParseInt(b, 10, 64); err != nil || before <= 0 {
			badRequest(w, "before must be a positive id")
			return
		}
	}
	if a := q.Get("after"); a != "" {
		if after, err = strconv.ParseInt(a, 10, 64); err != nil || after <= 0 {
			badRequest(w, "after must be a positive id")
			return
		}
	}
	limit := 50
	if l := q.Get("limit"); l != "" {
		if limit, err = strconv.Atoi(l); err != nil || limit < 1 || limit > maxStreamLimit {
			badRequest(w, fmt.Sprintf("limit must be 1 to %d", maxStreamLimit))
			return
		}
	}
	msgs, err := st.ChatMessages(r.Context(), ns, name, task.Name, before, after, limit)
	if err != nil {
		s.internalError(w, err)
		return
	}
	conversation := s.authz.CanReadConversation(principalOf(r), ns, name)
	out := make([]ChatMessageView, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, chatView(m, conversation))
	}
	writeJSON(w, http.StatusOK, ChatPage{Messages: out})
}

func chatView(m store.ChatMessage, conversation bool) ChatMessageView {
	v := ChatMessageView{
		ID: m.ID, At: m.At, Author: m.Author, AuthorName: m.AuthorName,
		Role: m.Role, Kind: m.Kind, ReplyTo: m.ReplyTo, State: m.State, Reason: m.Reason,
	}
	if conversation {
		v.Body = m.Body
	} else {
		v.Redacted = true
	}
	return v
}

// readChat reads a posted chat message within the size limit.
func readChat(w http.ResponseWriter, r *http.Request) (chatRequest, bool) {
	var req chatRequest
	// Room for the JSON envelope around a 16 KiB body.
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxChatBody+1024))
	if err := dec.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "the message is larger than 16 KiB")
			return req, false
		}
		writeError(w, http.StatusBadRequest, "invalid request")
		return req, false
	}
	req.Body = strings.TrimSpace(req.Body)
	req.ClientKey = strings.TrimSpace(req.ClientKey)
	if req.Kind == "" {
		req.Kind = "message"
	}
	if !chatKinds[req.Kind] {
		writeError(w, http.StatusBadRequest, "kind must be message, interrupt or command")
		return req, false
	}
	// The composer's retry key: without it a retry would double the message.
	if req.ClientKey == "" || len(req.ClientKey) > 128 {
		writeError(w, http.StatusBadRequest, "clientKey must be 1 to 128 characters")
		return req, false
	}
	if req.Body == "" {
		writeError(w, http.StatusBadRequest, "the message must not be empty")
		return req, false
	}
	if utf8.RuneCountInString(req.Body) > maxChatBody {
		writeError(w, http.StatusRequestEntityTooLarge, "the message is larger than 16 KiB")
		return req, false
	}
	if req.Kind == "command" {
		if _, err := chatcmd.Parse(req.Body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return req, false
		}
	}
	return req, true
}

// loadTask loads the project and the named task of the request path. A
// missing task or project the user may not see is 404.
func (s *Server) loadTask(w http.ResponseWriter, r *http.Request) (*aiv1.AgentProject, *aiv1.AgentTask, bool) {
	ap, tasks, ok := s.loadProject(w, r)
	if !ok {
		return nil, nil, false
	}
	name := r.PathValue("task")
	for i := range tasks {
		if tasks[i].Name == name {
			return ap, &tasks[i], true
		}
	}
	notFound(w)
	return nil, nil, false
}
