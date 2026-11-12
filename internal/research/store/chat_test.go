package store_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

func TestChatGrants(t *testing.T) {
	owner := storetest.New(t)
	ctx := t.Context()
	suffix := randomHex(t)
	readerRole, writerRole, chatRole := "r_"+suffix, "agents.w_"+suffix, "cw_"+suffix
	reader, writer, chat := storetest.Role(t, readerRole), storetest.Role(t, writerRole), storetest.Role(t, chatRole)
	if err := owner.Migrate(ctx, store.Grants{Reader: readerRole, Writers: []string{writerRole}, ChatWriter: chatRole}); err != nil {
		t.Fatal(err)
	}

	exp := time.Now().Add(time.Hour)
	id, created, err := chat.InsertUserMessage(ctx, store.ChatMessage{
		Namespace: "agents", Project: "bloop", Task: "issue-1", Author: "sub-1",
		Body: "hello", ClientKey: "k1", ExpiresAt: exp,
	})
	if err != nil || !created || id == 0 {
		t.Fatalf("chat writer InsertUserMessage = %d, %v, %v", id, created, err)
	}
	var seen int64
	if err := chat.Pool().QueryRow(ctx, "SELECT id FROM chat_messages WHERE id = $1", id).Scan(&seen); err != nil || seen != id {
		t.Fatalf("chat writer select: %d, %v", seen, err)
	}

	mustDeny(t, chat.Pool(), "chat agent role", `INSERT INTO chat_messages
		(namespace, project, task, role, body, expires_at) VALUES ('agents', 'bloop', 't', 'agent', 'no', $1)`, exp)
	mustDeny(t, chat.Pool(), "chat delivered state", `INSERT INTO chat_messages
		(namespace, project, task, role, body, state, expires_at) VALUES ('agents', 'bloop', 't', 'user', 'no', 'delivered', $1)`, exp)
	mustDeny(t, chat.Pool(), "chat with reply_to", `INSERT INTO chat_messages
		(namespace, project, task, role, body, reply_to, expires_at) VALUES ('agents', 'bloop', 't', 'user', 'no', $1, $2)`, id, exp)
	mustDeny(t, chat.Pool(), "chat oversized body", `INSERT INTO chat_messages
		(namespace, project, task, role, body, expires_at) VALUES ('agents', 'bloop', 't', 'user', repeat('x', 16385), $1)`, exp)
	mustFail(t, chat.Pool(), "chat update", "UPDATE chat_messages SET body = 'x'")
	mustFail(t, chat.Pool(), "chat delete", "DELETE FROM chat_messages")

	mustFail(t, writer.Pool(), "research writer select chat", "SELECT * FROM chat_messages")
	mustFail(t, writer.Pool(), "research writer insert chat", `INSERT INTO chat_messages
		(namespace, project, task, role, body, expires_at) VALUES ('agents', 'bloop', 't', 'user', 'no', $1)`, exp)

	exec(t, reader.Pool(), "SELECT * FROM chat_messages")
	mustFail(t, reader.Pool(), "reader insert chat", `INSERT INTO chat_messages
		(namespace, project, task, role, body, expires_at) VALUES ('agents', 'bloop', 't', 'user', 'no', $1)`, exp)
}

// mustDeny fails the test unless sql is rejected (privilege or RLS).
func mustDeny(t *testing.T, pool *pgxpool.Pool, what, sql string, args ...any) {
	t.Helper()
	_, err := pool.Exec(t.Context(), sql, args...)
	if err == nil {
		t.Errorf("%s: %s succeeded, want denial", what, sql)
		return
	}
	msg := err.Error()
	if !strings.Contains(msg, "permission denied") && !strings.Contains(msg, "row-level security") {
		t.Errorf("%s: %s: %v, want permission denied or RLS", what, sql, err)
	}
}

func TestInsertUserMessage(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	exp := time.Now().Add(time.Hour)
	base := store.ChatMessage{Namespace: "agents", Project: "bloop", Task: "issue-1", Author: "sub", Body: "hi", ExpiresAt: exp}

	id1, created, err := s.InsertUserMessage(ctx, withKey(base, "a"))
	if err != nil || !created {
		t.Fatalf("first = %d, %v, %v", id1, created, err)
	}
	id2, created, err := s.InsertUserMessage(ctx, withKey(base, "a"))
	if err != nil || created || id2 != id1 {
		t.Fatalf("idempotent = %d, %v, %v; want %d, false", id2, created, err, id1)
	}
	id3, created, err := s.InsertUserMessage(ctx, withKey(base, "b"))
	if err != nil || !created || id3 == id1 {
		t.Fatalf("other key = %d, %v, %v", id3, created, err)
	}
	nil1, created, err := s.InsertUserMessage(ctx, base)
	if err != nil || !created {
		t.Fatalf("nil key 1 = %d, %v, %v", nil1, created, err)
	}
	nil2, created, err := s.InsertUserMessage(ctx, base)
	if err != nil || !created || nil2 == nil1 {
		t.Fatalf("nil key 2 = %d, %v, %v; want new row", nil2, created, err)
	}
}

func TestQueuedAndState(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	exp := time.Now().Add(time.Hour)
	var ids []int64
	for _, key := range []string{"q1", "q2", "q3"} {
		id, _, err := s.InsertUserMessage(ctx, store.ChatMessage{
			Namespace: "agents", Project: "bloop", Task: "t", Author: "a",
			Body: key, ClientKey: key, ExpiresAt: exp,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	got, err := s.QueuedMessages(ctx, 2)
	if err != nil || len(got) != 2 || got[0].ID != ids[0] || got[1].ID != ids[1] {
		t.Fatalf("QueuedMessages = %+v, %v; want first two %v", got, err, ids[:2])
	}
	if err := s.SetMessageState(ctx, []int64{ids[0]}, "delivered", ""); err != nil {
		t.Fatal(err)
	}
	got, err = s.QueuedMessages(ctx, 10)
	if err != nil || len(got) != 2 || got[0].ID != ids[1] || got[1].ID != ids[2] {
		t.Fatalf("after deliver = %+v, %v", got, err)
	}
	if err := s.SetMessageState(ctx, ids[1:], "failed", "no chat"); err != nil {
		t.Fatal(err)
	}
	got, err = s.QueuedMessages(ctx, 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("all done = %+v, %v", got, err)
	}

	// A reply that lands before the operator marks its message delivered
	// (a re-dispatch after a crash) keeps the message answered.
	if _, err := s.InsertReply(ctx, store.ChatMessage{
		Namespace: "agents", Project: "bloop", Task: "t", Role: "agent",
		Body: "done", ReplyTo: ids[0], ExpiresAt: exp,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMessageState(ctx, []int64{ids[0]}, "delivered", ""); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := s.Pool().QueryRow(ctx, "SELECT state FROM chat_messages WHERE id = $1", ids[0]).Scan(&state); err != nil || state != "answered" {
		t.Fatalf("state after late delivered = %q, %v; want answered", state, err)
	}
}

func TestInsertReply(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	exp := time.Now().Add(time.Hour)
	id, _, err := s.InsertUserMessage(ctx, store.ChatMessage{
		Namespace: "agents", Project: "bloop", Task: "t", Author: "a",
		Body: "q", ClientKey: "r1", ExpiresAt: exp,
	})
	if err != nil {
		t.Fatal(err)
	}
	reply := store.ChatMessage{
		Namespace: "agents", Project: "bloop", Task: "t", Role: "agent",
		Body: "a", ReplyTo: id, ExpiresAt: exp,
	}
	created, err := s.InsertReply(ctx, reply)
	if err != nil || !created {
		t.Fatalf("InsertReply = %v, %v", created, err)
	}
	created, err = s.InsertReply(ctx, reply)
	if err != nil || created {
		t.Fatalf("second InsertReply = %v, %v; want false", created, err)
	}
	msgs, err := s.ChatMessages(ctx, "agents", "bloop", "t", 0, 0, 10)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("ChatMessages = %+v, %v", msgs, err)
	}
	var user, agent store.ChatMessage
	for _, m := range msgs {
		switch m.Role {
		case "user":
			user = m
		case "agent":
			agent = m
		}
	}
	if user.State != "answered" {
		t.Errorf("user state = %q, want answered", user.State)
	}
	// NULL reply_to and client_key read as zero values.
	if user.ReplyTo != 0 || user.ClientKey != "r1" || agent.ReplyTo != id || agent.ClientKey != "" {
		t.Errorf("user = %+v, agent = %+v; want reply_to only on the agent's, client_key only on the user's", user, agent)
	}
}

func TestChatMessagesPaging(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	exp := time.Now().Add(time.Hour)
	var ids []int64
	for i := range 5 {
		id, _, err := s.InsertUserMessage(ctx, store.ChatMessage{
			Namespace: "agents", Project: "bloop", Task: "page", Author: "a",
			Body: "m", ClientKey: "p" + string(rune('a'+i)), ExpiresAt: exp,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	newest, err := s.ChatMessages(ctx, "agents", "bloop", "page", 0, 0, 2)
	if err != nil || len(newest) != 2 || newest[0].ID != ids[4] || newest[1].ID != ids[3] {
		t.Fatalf("newest = ids %v, %v; want %d,%d", msgIDs(newest), err, ids[4], ids[3])
	}
	older, err := s.ChatMessages(ctx, "agents", "bloop", "page", newest[1].ID, 0, 2)
	if err != nil || len(older) != 2 || older[0].ID != ids[2] || older[1].ID != ids[1] {
		t.Fatalf("before = ids %v, %v; want %d,%d", msgIDs(older), err, ids[2], ids[1])
	}
	newer, err := s.ChatMessages(ctx, "agents", "bloop", "page", 0, ids[1], 2)
	if err != nil || len(newer) != 2 || newer[0].ID != ids[3] || newer[1].ID != ids[2] {
		t.Fatalf("after page = ids %v, %v; want %d,%d (oldest of newer)", msgIDs(newer), err, ids[3], ids[2])
	}
	allNewer, err := s.ChatMessages(ctx, "agents", "bloop", "page", 0, ids[1], 10)
	if err != nil || len(allNewer) != 3 || allNewer[0].ID != ids[4] || allNewer[2].ID != ids[2] {
		t.Fatalf("after all = ids %v, %v", msgIDs(allNewer), err)
	}
}

func TestChatContext(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	exp := time.Now().Add(time.Hour)
	user := func(key, kind, body string) int64 {
		t.Helper()
		id, _, err := s.InsertUserMessage(ctx, store.ChatMessage{
			Namespace: "agents", Project: "bloop", Task: "ctx", Author: "a", Kind: kind,
			Body: body, ClientKey: key, ExpiresAt: exp,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	reply := func(to int64, role, body string) {
		t.Helper()
		if _, err := s.InsertReply(ctx, store.ChatMessage{
			Namespace: "agents", Project: "bloop", Task: "ctx", Role: role, Body: body, ReplyTo: to, ExpiresAt: exp,
		}); err != nil {
			t.Fatal(err)
		}
	}
	reply(user("c1", "", "first"), "agent", "first answer")
	reply(user("c2", "command", `{"cmd":"retry"}`), "system", "retrying")
	reply(user("c3", "", "second"), "agent", "second answer")
	user("c4", "", "still queued")
	user("c5", "interrupt", "stop")
	if _, _, err := s.InsertUserMessage(ctx, store.ChatMessage{
		Namespace: "agents", Project: "bloop", Task: "other", Author: "a", Body: "elsewhere", ClientKey: "c6", ExpiresAt: exp,
	}); err != nil {
		t.Fatal(err)
	}

	msgs, err := s.ChatContext(ctx, "agents", "bloop", "ctx", 3)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range msgs {
		got = append(got, m.Role+":"+m.Body)
	}
	if want := []string{"agent:first answer", "user:second", "agent:second answer"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("ChatContext = %q, want %q", got, want)
	}
}

func TestListenChat(t *testing.T) {
	s := storetest.New(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	got := make(chan int64, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = s.ListenChat(ctx, func(id int64) { got <- id })
	}()
	// Give LISTEN time to register before the insert.
	time.Sleep(100 * time.Millisecond)
	id, _, err := s.InsertUserMessage(t.Context(), store.ChatMessage{
		Namespace: "agents", Project: "bloop", Task: "t", Author: "a",
		Body: "ping", ClientKey: "listen", ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-got:
		if n != id {
			t.Fatalf("notified %d, want %d", n, id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no notification")
	}
	cancel()
	wg.Wait()
}

func TestDeleteExpiredChat(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	now := time.Now()
	keep, _, err := s.InsertUserMessage(ctx, store.ChatMessage{
		Namespace: "agents", Project: "bloop", Task: "t", Author: "a",
		Body: "keep", ClientKey: "keep", ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	var gone []int64
	for i := range 3 {
		id, _, err := s.InsertUserMessage(ctx, store.ChatMessage{
			Namespace: "agents", Project: "bloop", Task: "t", Author: "a",
			Body: "gone", ClientKey: "gone" + string(rune('0'+i)), ExpiresAt: now.Add(-time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		gone = append(gone, id)
	}
	// A reply that expires later than its message outlives it.
	if _, err := s.InsertReply(ctx, store.ChatMessage{
		Namespace: "agents", Project: "bloop", Task: "t", Role: "agent",
		Body: "reply", ReplyTo: gone[0], ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	n, err := s.DeleteExpiredChat(ctx, now)
	if err != nil || n != 3 {
		t.Fatalf("DeleteExpiredChat = %d, %v; want 3", n, err)
	}
	msgs, err := s.ChatMessages(ctx, "agents", "bloop", "t", 0, 0, 10)
	if err != nil || len(msgs) != 2 || msgs[1].ID != keep || msgs[0].Body != "reply" || msgs[0].ReplyTo != 0 {
		t.Fatalf("left = %+v, %v; want the reply (reply_to cleared) and id %d", msgs, err, keep)
	}
}

func withKey(m store.ChatMessage, key string) store.ChatMessage {
	m.ClientKey = key
	return m
}

func msgIDs(msgs []store.ChatMessage) []int64 {
	ids := make([]int64, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}
	return ids
}
