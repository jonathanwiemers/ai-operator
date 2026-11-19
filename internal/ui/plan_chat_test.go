package ui

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
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

// withDecisions gives shop-plan-new the decisions D1 (version 2, three
// options) and D3 (version 1, two options).
func withDecisions(t *testing.T, env *testEnv) {
	t.Helper()
	pl := env.getPlan(t, "shop-plan-new")
	pl.Status.Decisions = []aiv1.PlanDecision{
		{ID: "D1", Title: "Store", Question: "Where?", Options: []string{"a", "b", "c"}, State: aiv1.PlanDecisionLocked, Version: 2, DecidedBy: "agent"},
		{ID: "D3", Title: "Auth", Question: "How?", Options: []string{"x", "y"}, State: aiv1.PlanDecisionOpen, Version: 1, DecidedBy: "agent"},
	}
	if err := env.reader.Status().Update(context.Background(), pl); err != nil {
		t.Fatal(err)
	}
}

func opt(i int32) *int32 { return &i }

func TestPostPlanChatDecisions(t *testing.T) {
	env, owner := planChatEnv(t, "leads", "readers")
	withDecisions(t, env)
	answers := []store.DecisionAnswer{{ID: "D1", Version: 2, Keep: true}, {ID: "D3", Version: 1, Other: "  Use OIDC  "}}
	code, out := env.postChat(t, newPlanChat, map[string]any{"clientKey": "a1", "decisions": answers})
	if code != http.StatusCreated {
		t.Fatalf("answers without a body: %d %+v, want 201", code, out)
	}
	msgs, err := owner.PlanMessages(t.Context(), "team-a", "shop", "shop-plan-new", 0, 0, 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("plan messages = %+v, %v", msgs, err)
	}
	want := []store.DecisionAnswer{{ID: "D1", Version: 2, Keep: true}, {ID: "D3", Version: 1, Other: "Use OIDC"}}
	if m := msgs[0]; m.Body != "" || !reflect.DeepEqual(m.Decisions, want) {
		t.Errorf("stored message = %+v, want body empty and answers %+v", m, want)
	}
	var page ChatPage
	if code := env.get(t, newPlanChat, &page); code != http.StatusOK || len(page.Messages) != 1 ||
		!reflect.DeepEqual(page.Messages[0].Decisions, want) {
		t.Errorf("get: %d %+v, want the answers", code, page.Messages)
	}

	viewer, viewerStore := planChatEnv(t, "devs")
	if _, _, err := viewerStore.InsertUserMessage(t.Context(), store.ChatMessage{Namespace: "team-a", Project: "shop",
		Plan: "shop-plan-new", Author: "bea", ClientKey: "p1", ExpiresAt: time.Now().Add(time.Hour), Decisions: want}); err != nil {
		t.Fatal(err)
	}
	page = ChatPage{}
	if code := viewer.get(t, newPlanChat, &page); code != http.StatusOK || len(page.Messages) == 0 ||
		!page.Messages[0].Redacted || page.Messages[0].Decisions != nil {
		t.Errorf("viewer get: %d %+v, want the answers redacted", code, page.Messages)
	}
}

func TestChatViewRedactsDecisions(t *testing.T) {
	m := store.ChatMessage{ID: 1, Body: "and more", Decisions: []store.DecisionAnswer{{ID: "D1", Version: 1, Other: "secret"}}}
	if v := chatView(m, true); v.Redacted || len(v.Decisions) != 1 || v.Body != "and more" {
		t.Errorf("with the conversation grant: %+v", v)
	}
	if v := chatView(m, false); !v.Redacted || v.Decisions != nil || v.Body != "" {
		t.Errorf("without the conversation grant: %+v, want body and answers dropped", v)
	}
}

// The refusals need no database.
func TestPostPlanChatDecisionsRefused(t *testing.T) {
	env := planEnv(t, true, "leads")
	withDecisions(t, env)
	long := strings.Repeat("é", maxAnswerOther+1)
	tooMany := make([]store.DecisionAnswer, maxPlanAnswers+1)
	for i := range tooMany {
		tooMany[i] = store.DecisionAnswer{ID: fmt.Sprintf("D%d", i+1), Version: 1, Keep: true}
	}
	for _, c := range []struct {
		name string
		body map[string]any
		code int
		msg  string
	}{
		{"stale version", map[string]any{"decisions": []store.DecisionAnswer{{ID: "D1", Version: 1, Keep: true}}}, http.StatusConflict, errDecisionsChanged},
		{"unknown id", map[string]any{"decisions": []store.DecisionAnswer{{ID: "D1", Version: 2, Keep: true}, {ID: "D2", Version: 1, Keep: true}}}, http.StatusConflict, errDecisionsChanged},
		{"empty without answers", map[string]any{"body": "  "}, http.StatusBadRequest, "the message must not be empty"},
		{"other too long", map[string]any{"decisions": []store.DecisionAnswer{{ID: "D1", Version: 2, Other: long}}}, http.StatusBadRequest,
			"decision answer 1 is longer than 2000 characters"},
		{"option out of range", map[string]any{"decisions": []store.DecisionAnswer{{ID: "D1", Version: 2, Keep: true}, {ID: "D3", Version: 1, Option: opt(2)}}},
			http.StatusBadRequest, "decision answer 2 picks an option the decision does not have"},
		{"negative option", map[string]any{"decisions": []store.DecisionAnswer{{ID: "D3", Version: 1, Option: opt(-1)}}},
			http.StatusBadRequest, "decision answer 1 picks an option the decision does not have"},
		{"two answers in one", map[string]any{"decisions": []store.DecisionAnswer{{ID: "D3", Version: 1, Keep: true, Option: opt(0)}}},
			http.StatusBadRequest, "decision answer 1 must set exactly one of keep, option and other"},
		{"blank other", map[string]any{"decisions": []store.DecisionAnswer{{ID: "D3", Version: 1, Other: "   "}}},
			http.StatusBadRequest, "decision answer 1 must set exactly one of keep, option and other"},
		{"repeated id", map[string]any{"decisions": []store.DecisionAnswer{{ID: "D3", Version: 1, Keep: true}, {ID: "D3", Version: 1, Option: opt(1)}}},
			http.StatusBadRequest, "decision answer 2 answers a decision twice"},
		{"interrupt", map[string]any{"kind": "interrupt", "decisions": []store.DecisionAnswer{{ID: "D3", Version: 1, Keep: true}}},
			http.StatusBadRequest, "decisions are answered with a message"},
		{"too many", map[string]any{"decisions": tooMany}, http.StatusBadRequest, "at most 30 decisions are answered at once"},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.body["clientKey"] = "r1"
			if code, msg := env.postChatErr(t, newPlanChat, c.body); code != c.code || msg != c.msg {
				t.Errorf("%d %q, want %d %q", code, msg, c.code, c.msg)
			}
		})
	}
}

// A task chat post with answers is refused before the store.
func TestPostChatDecisionsRefused(t *testing.T) {
	env := newTestEnv(t, withChat(oidcConfig(Rule{Roles: []string{"chatters"}, Projects: []string{"team-a/shop"}, Chat: true})))
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"chatters": map[string]any{}}}
	env.login(t, "/")
	code, msg := env.postChatErr(t, "/api/v1/projects/team-a/shop/tasks/shop-42-add-paypal/chat",
		map[string]any{"clientKey": "t1", "decisions": []store.DecisionAnswer{{ID: "D1", Version: 2, Keep: true}}})
	if code != http.StatusBadRequest || msg != "decisions are answered on a plan only" {
		t.Errorf("%d %q, want 400", code, msg)
	}
}
