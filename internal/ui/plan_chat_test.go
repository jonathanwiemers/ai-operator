package ui

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

const newPlanChat = shopPlans + "/shop-plan-new/chat"

// planChatEnv is planEnv with research and chat stores of a fresh schema,
// whose shop project also has a task message.
func planChatEnv(t *testing.T, roles ...string) (*testEnv, *store.Store) {
	t.Helper()
	owner, reader, chat := chatStores(t)
	if _, _, err := owner.InsertUserMessage(t.Context(), store.ChatMessage{Namespace: "team-a", Project: "shop",
		Task: "shop-42-add-paypal", Author: "bea", Body: "task message", ClientKey: "t1", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	env := planEnv(t, true, roles...)
	env.server.SetResearchStore(reader)
	env.server.SetChatStore(chat)
	return env, owner
}

func TestPostPlanChat(t *testing.T) {
	env, owner := planChatEnv(t, "leads", "readers")
	code, out := env.postChat(t, newPlanChat, map[string]any{"body": "plan refunds", "clientKey": "k1"})
	if code != http.StatusCreated || out["id"] == nil {
		t.Fatalf("create: %d %+v", code, out)
	}
	id := out["id"]
	if code, out = env.postChat(t, newPlanChat, map[string]any{"body": "plan refunds", "clientKey": "k1"}); code != http.StatusOK || out["id"] != id {
		t.Fatalf("retry: %d %+v, want 200 id=%v", code, out, id)
	}
	if code, _ = env.postChat(t, newPlanChat, map[string]any{"body": "stop", "kind": "interrupt", "clientKey": "k2"}); code != http.StatusCreated {
		t.Fatalf("interrupt: %d, want 201", code)
	}

	msgs, err := owner.PlanMessages(t.Context(), "team-a", "shop", "shop-plan-new", 0, 0, 10)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("plan messages = %+v, %v", msgs, err)
	}
	if m := msgs[1]; m.Plan != "shop-plan-new" || m.Task != "" || m.Author != "ada@example.com" || m.Body != "plan refunds" {
		t.Errorf("stored message = %+v", m)
	}

	var page ChatPage
	if code := env.get(t, newPlanChat+"?limit=1", &page); code != http.StatusOK || len(page.Messages) != 1 ||
		page.Messages[0].Body != "stop" || page.Messages[0].Redacted {
		t.Fatalf("get: %d %+v", code, page.Messages)
	}
	page = ChatPage{}
	if code := env.get(t, newPlanChat+"?before="+strconv.FormatInt(msgs[0].ID, 10), &page); code != http.StatusOK ||
		len(page.Messages) != 1 || page.Messages[0].Body != "plan refunds" {
		t.Fatalf("get before: %d %+v", code, page.Messages)
	}
	if code := env.get(t, newPlanChat+"?limit=0", nil); code != http.StatusBadRequest {
		t.Errorf("limit=0: %d, want 400", code)
	}
}

// The refusals need no database.
func TestPostPlanChatRefused(t *testing.T) {
	env := planEnv(t, true, "leads") // refused before the store
	code, msg := env.postChatErr(t, newPlanChat, map[string]any{"body": `{"cmd":"retry"}`, "kind": "command", "clientKey": "c1"})
	if code != http.StatusBadRequest || msg != "commands are not supported on a plan" {
		t.Errorf("command: %d %q", code, msg)
	}
	if code, _ := env.postChat(t, shopPlans+"/shop-plan-old/chat", map[string]any{"body": "more", "clientKey": "c2"}); code != http.StatusConflict {
		t.Errorf("created plan: %d, want 409", code)
	}
	for _, path := range []string{shopPlans + "/nope/chat", shopPlans + "/billing-plan/chat", "/api/v1/projects/team-b/billing/plans/billing-plan/chat"} {
		if code, _ := env.postChat(t, path, map[string]any{"body": "hi", "clientKey": "c3"}); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, code)
		}
		if code := env.get(t, path, nil); code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", path, code)
		}
	}
}

func TestPlanChatPermissions(t *testing.T) {
	t.Run("viewer reads redacted, may not post", func(t *testing.T) {
		env, owner := planChatEnv(t, "devs")
		if code, _ := env.postChat(t, newPlanChat, map[string]any{"body": "hi", "clientKey": "v1"}); code != http.StatusForbidden {
			t.Errorf("viewer post: %d, want 403", code)
		}
		if _, _, err := owner.InsertUserMessage(t.Context(), store.ChatMessage{Namespace: "team-a", Project: "shop",
			Plan: "shop-plan-new", Author: "bea", Body: "secret", ClientKey: "p1", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		var page ChatPage
		if code := env.get(t, newPlanChat, &page); code != http.StatusOK || len(page.Messages) != 1 ||
			!page.Messages[0].Redacted || page.Messages[0].Body != "" {
			t.Errorf("viewer get: %d %+v, want one redacted message", code, page.Messages)
		}
	})
	t.Run("steer but not an owner-subject", func(t *testing.T) {
		env := planEnv(t, true, "leads")
		setOwners(t, env, "bea")
		if code, _ := env.postChat(t, newPlanChat, map[string]any{"body": "hi", "clientKey": "o1"}); code != http.StatusForbidden {
			t.Errorf("not an owner: %d, want 403", code)
		}
	})
}
