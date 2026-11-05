package ui

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

// chatStores returns owner, reader and chat-writer stores of a fresh schema.
func chatStores(t *testing.T) (owner, reader, chat *store.Store) {
	t.Helper()
	owner = storetest.New(t)
	suffix := strings.ToLower(rand.Text()[:10])
	readerRole, chatRole := "ui_r_"+suffix, "ui_cw_"+suffix
	reader, chat = storetest.Role(t, readerRole), storetest.Role(t, chatRole)
	if err := owner.Migrate(t.Context(), store.Grants{Reader: readerRole, ChatWriter: chatRole}); err != nil {
		t.Fatal(err)
	}
	return owner, reader, chat
}

func withChat(c *Config) *Config {
	withResearch(c, false)
	c.ChatDBUser, c.ChatDBPassword = "chat_writer", "secret"
	return c
}

func chatEnv(t *testing.T, rules []Rule, reader, chat *store.Store) *testEnv {
	t.Helper()
	env := newTestEnv(t, withChat(oidcConfig(rules...)))
	env.server.SetResearchStore(reader)
	env.server.SetChatStore(chat)
	return env
}

func (e *testEnv) postChat(t *testing.T, path string, body any) (int, map[string]any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, out
}

func (e *testEnv) postChatErr(t *testing.T, path string, body any) (int, string) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out["error"]
}

func TestChatGrantValidation(t *testing.T) {
	valid := func() *Config {
		c := &Config{
			Auth:           AuthConfig{Mode: AuthNone},
			Research:       ResearchConfig{Database: DatabaseConfig{Host: "127.0.0.1", Name: "ai", SSLMode: "disable"}},
			ResearchDBUser: "r", ResearchDBPassword: "p",
		}
		return c
	}
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "chat ok", mutate: func(c *Config) {
			c.Authorization.Rules = []Rule{{Roles: []string{"a"}, Chat: true}}
			c.ChatDBUser, c.ChatDBPassword = "cw", "pw"
		}},
		{name: "research with chat", mutate: func(c *Config) {
			c.Authorization.Rules = []Rule{{Roles: []string{"a"}, Research: ResearchRead, Chat: true}}
		}, wantErr: "grants no projects"},
		{name: "chat user without password", mutate: func(c *Config) {
			c.ChatDBUser = "cw"
		}, wantErr: EnvChatDBUser},
		{name: "chat without research host", mutate: func(c *Config) {
			c.Research = ResearchConfig{}
			c.ChatDBUser, c.ChatDBPassword = "cw", "pw"
		}, wantErr: "research.database.host"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := valid()
			tc.mutate(c)
			err := c.Validate("127.0.0.1:9080")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestCanChat(t *testing.T) {
	a := NewAuthorizer(&Config{
		Auth: AuthConfig{Mode: AuthOIDC},
		Authorization: AuthorizationConfig{Rules: []Rule{
			{Roles: []string{"chatters"}, Projects: []string{"team-a/shop"}, Chat: true},
			{Roles: []string{"viewers"}, Projects: []string{"team-a/shop"}},
		}},
	})
	if !a.CanChat(Principal{Roles: []string{"chatters"}}, "team-a", "shop") {
		t.Fatal("chatters may chat")
	}
	if a.CanChat(Principal{Roles: []string{"viewers"}}, "team-a", "shop") {
		t.Fatal("viewers may not chat")
	}
	if !a.SeesChat(Principal{Roles: []string{"chatters"}}) || a.SeesChat(Principal{Roles: []string{"viewers"}}) {
		t.Fatal("SeesChat")
	}
}

func TestPostChat(t *testing.T) {
	_, reader, chat := chatStores(t)
	shop := "/api/v1/projects/team-a/shop/tasks/shop-42-add-paypal/chat"
	rules := []Rule{
		{Roles: []string{"chatters"}, Projects: []string{"team-a/shop"}, Chat: true, Conversation: true},
		{Roles: []string{"viewers"}, Projects: []string{"team-a/shop"}},
	}

	login := func(t *testing.T, role string) *testEnv {
		env := chatEnv(t, rules, reader, chat)
		env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
		env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{role: map[string]any{"1": "acme"}}}
		env.login(t, "/")
		return env
	}

	t.Run("create and retry", func(t *testing.T) {
		env := login(t, "chatters")
		code, out := env.postChat(t, shop, map[string]any{"body": "hello", "clientKey": "k1"})
		if code != http.StatusCreated || out["id"] == nil {
			t.Fatalf("create: %d %+v", code, out)
		}
		id := out["id"]
		code, out = env.postChat(t, shop, map[string]any{"body": "hello again", "clientKey": "k1"})
		if code != http.StatusOK || out["id"] != id {
			t.Fatalf("retry: %d %+v, want 200 id=%v", code, out, id)
		}
	})

	t.Run("no client key", func(t *testing.T) {
		env := login(t, "chatters")
		if code, _ := env.postChat(t, shop, map[string]any{"body": "hello"}); code != http.StatusBadRequest {
			t.Fatalf("without clientKey: %d, want 400 (a retry would double it)", code)
		}
	})

	t.Run("invalid command body", func(t *testing.T) {
		env := login(t, "chatters")
		code, errBody := env.postChatErr(t, shop, map[string]any{
			"body": `{"cmd":"nope"}`, "kind": "command", "clientKey": "badcmd",
		})
		if code != http.StatusBadRequest || !strings.Contains(errBody, "unknown cmd") {
			t.Fatalf("invalid command: %d %q", code, errBody)
		}
		code, errBody = env.postChatErr(t, shop, map[string]any{
			"body": `{"cmd":"revisit","decision":"D12"}`, "kind": "command", "clientKey": "badrevisit",
		})
		if code != http.StatusBadRequest || !strings.Contains(errBody, "revisit needs a decision") {
			t.Fatalf("malformed decision: %d %q", code, errBody)
		}
	})

	t.Run("oversize", func(t *testing.T) {
		env := login(t, "chatters")
		code, _ := env.postChat(t, shop, map[string]any{"body": strings.Repeat("x", maxChatBody+1), "clientKey": "big"})
		if code != http.StatusRequestEntityTooLarge {
			t.Fatalf("oversize body: %d, want 413", code)
		}
	})

	t.Run("no grant", func(t *testing.T) {
		env := login(t, "viewers")
		if code, _ := env.postChat(t, shop, map[string]any{"body": "nope", "clientKey": "v1"}); code != http.StatusForbidden {
			t.Fatalf("no grant: %d, want 403", code)
		}
	})

	t.Run("feature off", func(t *testing.T) {
		env := newTestEnv(t, withResearch(oidcConfig(rules...), false))
		env.server.SetResearchStore(reader)
		env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
		env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"chatters": map[string]any{"1": "acme"}}}
		env.login(t, "/")
		if code, _ := env.postChat(t, shop, map[string]any{"body": "nope", "clientKey": "off"}); code != http.StatusNotFound {
			t.Fatalf("feature off: %d, want 404", code)
		}
	})
}

func TestGetChatRedaction(t *testing.T) {
	owner, reader, chat := chatStores(t)
	exp := time.Now().Add(time.Hour)
	if _, _, err := owner.InsertUserMessage(t.Context(), store.ChatMessage{
		Namespace: "team-a", Project: "shop", Task: "shop-42-add-paypal", Author: "ada@example.com",
		Body: "secret plan", ClientKey: "g1", ExpiresAt: exp,
	}); err != nil {
		t.Fatal(err)
	}
	_ = chat
	rules := []Rule{
		{Roles: []string{"viewers"}, Projects: []string{"team-a/shop"}},
		{Roles: []string{"leads"}, Projects: []string{"team-a/shop"}, Conversation: true},
	}
	path := "/api/v1/projects/team-a/shop/tasks/shop-42-add-paypal/chat"
	login := func(t *testing.T, role string) *testEnv {
		env := chatEnv(t, rules, reader, chat)
		env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
		env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{role: map[string]any{"1": "acme"}}}
		env.login(t, "/")
		return env
	}

	type page struct {
		Messages []ChatMessageView `json:"messages"`
	}
	viewer := login(t, "viewers")
	var got page
	if code := viewer.get(t, path, &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(got.Messages) != 1 || !got.Messages[0].Redacted || got.Messages[0].Body != "" {
		t.Fatalf("viewer = %+v, want redacted", got.Messages)
	}

	lead := login(t, "leads")
	got = page{}
	if code := lead.get(t, path, &got); code != http.StatusOK || len(got.Messages) != 1 || got.Messages[0].Body != "secret plan" {
		t.Fatalf("lead = %+v", got.Messages)
	}
}

func TestStreamChatKinds(t *testing.T) {
	owner, reader, chat := chatStores(t)
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	if _, err := owner.InsertEvents(t.Context(), []store.Event{{
		Key: "shop-run", At: at, Namespace: "team-a", Project: "shop", Kind: "run.finished",
		Summary: "shop-run", ExpiresAt: at.Add(24 * time.Hour),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := owner.InsertUserMessage(t.Context(), store.ChatMessage{
		Namespace: "team-a", Project: "shop", Task: "shop-42-add-paypal", Author: "ada@example.com",
		Body: "chat body", ClientKey: "s1", ExpiresAt: at.Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	// Force the chat row's at after the run so merge order is deterministic.
	storetest.Exec(t, `UPDATE chat_messages SET at = $1 WHERE client_key = 's1'`, at.Add(time.Minute))

	rules := []Rule{
		{Roles: []string{"devs"}, Projects: []string{"team-a/shop"}},
		{Roles: []string{"leads"}, Projects: []string{"team-a/shop"}, Conversation: true},
	}
	login := func(t *testing.T, role string) *testEnv {
		env := chatEnv(t, rules, reader, chat)
		env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
		env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{role: map[string]any{"1": "acme"}}}
		env.login(t, "/")
		return env
	}

	dev := login(t, "devs")
	var page StreamPage
	if code := dev.get(t, "/api/v1/stream?project=team-a/shop", &page); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	// Without kinds the stream stays history only (ids collide across tables).
	if len(page.Events) != 1 || page.Events[0].Kind != "run.finished" {
		t.Fatalf("default stream = %+v", page.Events)
	}
	page = StreamPage{}
	if code := dev.get(t, "/api/v1/stream?project=team-a/shop&kinds=chat.user,chat.agent,run.finished", &page); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(page.Events) != 2 || page.Events[0].Kind != "chat.user" || page.Events[1].Kind != "run.finished" {
		t.Fatalf("merged = %+v", page.Events)
	}
	if !page.Events[0].Redacted || page.Events[0].Data["body"] != nil {
		t.Fatalf("chat without conversation = %+v", page.Events[0])
	}

	lead := login(t, "leads")
	page = StreamPage{}
	lead.get(t, "/api/v1/stream?project=team-a/shop&kinds=chat.user", &page)
	if len(page.Events) != 1 || page.Events[0].Data["body"] != "chat body" || page.Events[0].Redacted {
		t.Fatalf("lead chat = %+v", page.Events)
	}

	// The role filter runs in SQL: one agent row under newer user rows still
	// fills a page of kinds=chat.agent.
	id, _, err := owner.InsertUserMessage(t.Context(), store.ChatMessage{
		Namespace: "team-a", Project: "shop", Task: "shop-42-add-paypal", Author: "ada@example.com",
		Body: "question", ClientKey: "s2", ExpiresAt: at.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.InsertReply(t.Context(), store.ChatMessage{
		Namespace: "team-a", Project: "shop", Task: "shop-42-add-paypal", Role: "agent",
		Body: "answer", ReplyTo: id, ExpiresAt: at.Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	storetest.Exec(t, `UPDATE chat_messages SET at = $1 WHERE role = 'agent'`, at.Add(2*time.Minute))
	for i := range 3 {
		if _, _, err := owner.InsertUserMessage(t.Context(), store.ChatMessage{
			Namespace: "team-a", Project: "shop", Task: "shop-42-add-paypal", Author: "ada@example.com",
			Body: "more", ClientKey: "m" + strconv.Itoa(i), ExpiresAt: at.Add(24 * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	page = StreamPage{}
	lead.get(t, "/api/v1/stream?project=team-a/shop&kinds=chat.agent&limit=1", &page)
	if len(page.Events) != 1 || page.Events[0].Kind != "chat.agent" || page.Events[0].Data["body"] != "answer" {
		t.Fatalf("chat.agent page = %+v", page.Events)
	}

	// A milestone without tasks has no chat, not the whole project's.
	page = StreamPage{}
	if code := lead.get(t, "/api/v1/stream?project=team-a/shop&milestone=999&kinds=chat.user,chat.agent", &page); code != http.StatusOK || len(page.Events) != 0 {
		t.Fatalf("empty milestone = %d %+v", code, page.Events)
	}
}

func TestFeaturesChat(t *testing.T) {
	cfg := withChat(&Config{Auth: AuthConfig{Mode: AuthNone}, Authorization: AuthorizationConfig{Rules: []Rule{
		{Roles: []string{"*"}, Chat: true},
	}}})
	env := newTestEnv(t, cfg)
	var got featuresResponse
	if code := env.get(t, "/api/v1/features", &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	want := []Feature{{Name: FeatureFactory}, {Name: FeatureHistory}, {Name: FeatureChat}, {Name: FeatureResearch}}
	if fmt.Sprint(got.Features) != fmt.Sprint(want) {
		t.Errorf("features = %+v, want %+v", got.Features, want)
	}
}

func TestMergeStreamPagesWithoutGap(t *testing.T) {
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	// Newest first, as the store returns them: history at minutes 5, 3, 1;
	// chat at minutes 4, 2.
	events := []store.Event{{ID: 5, At: at.Add(5 * time.Minute)}, {ID: 3, At: at.Add(3 * time.Minute)}, {ID: 1, At: at.Add(time.Minute)}}
	chat := []store.ChatMessage{{ID: 4, Role: "user", At: at.Add(4 * time.Minute)}, {ID: 2, Role: "agent", At: at.Add(2 * time.Minute)}}
	ids := func(evs []StreamEvent) []int64 {
		var out []int64
		for _, e := range evs {
			out = append(out, e.ID)
		}
		return out
	}
	if got := ids(mergeStream(events, chat, nil, 3, false)); !slices.Equal(got, []int64{5, 4, 3}) {
		t.Errorf("older page = %v, want the newest three [5 4 3]", got)
	}
	// Paging newer: the oldest three, so the next page continues from 3.
	if got := ids(mergeStream(events, chat, nil, 3, true)); !slices.Equal(got, []int64{3, 2, 1}) {
		t.Errorf("newer page = %v, want the oldest three [3 2 1]", got)
	}
}
