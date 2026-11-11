package ui

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// sameJSON fails unless got encodes to the JSON of want, a golden literal:
// the same keys and values, absent keys absent.
func sameJSON(t *testing.T, got any, want string) {
	t.Helper()
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var g, w any
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("golden: %v", err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("JSON =\n%s\nwant\n%s", b, want)
	}
}

// TestPauseHistoryJSON pins the milestone's pause history as the web reads
// it: newest first, an open pause without until, resolver and via.
func TestPauseHistoryJSON(t *testing.T) {
	env, gl := newSteeringEnv(t, Rule{Roles: []string{"devs"}, Projects: []string{"team-a/*"}})
	withAutopilot(t, env, gl)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"devs": map[string]any{}}}
	env.login(t, "/")

	since := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	until := since.Add(10 * time.Minute)
	env.server.pauses = &fakePauses{h: store.PauseHistory{
		Pauses: []store.Pause{
			{Kind: "ciRed", Reason: "pipeline failed", Item: 42, Since: since, Until: &until, ResolvedBy: "selfHealed", Via: "selfHealed", Seconds: 600},
			{Kind: "unknown", Since: since.Add(time.Hour), Seconds: 120},
		},
		Totals: store.PauseTotals{Count: 2, PausedSec: 720, MeanResumeSec: 600,
			ByKind:     map[string]store.PauseKindTotal{"ciRed": {Count: 1, Sec: 600}, "unknown": {Count: 1, Sec: 120}},
			ByResolver: map[string]int{"selfHealed": 1}, ByVia: map[string]int{"selfHealed": 1}},
	}}
	code, m := env.getJSON(t, shopMilestone)
	if code != http.StatusOK {
		t.Fatalf("milestone: status %d", code)
	}
	sameJSON(t, m["pauses"], `{
		"pauses": [
			{"kind": "unknown", "since": "2026-11-05T11:00:00Z", "seconds": 120},
			{"kind": "ciRed", "reason": "pipeline failed", "item": 42, "since": "2026-11-05T10:00:00Z",
			 "until": "2026-11-05T10:10:00Z", "resolvedBy": "selfHealed", "via": "selfHealed", "seconds": 600}
		],
		"totals": {
			"count": 2, "pausedSec": 720, "meanResumeSec": 600,
			"byKind": {"ciRed": {"count": 1, "sec": 600}, "unknown": {"count": 1, "sec": 120}},
			"byResolver": {"selfHealed": 1}, "byVia": {"selfHealed": 1}
		}
	}`)
}

// TestChatPageJSON pins a chat page's messages: the store's routing fields
// (namespace, project, task, client key, expiry) stay out, and without the
// conversation grant the body is redacted.
func TestChatPageJSON(t *testing.T) {
	at := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	user := store.ChatMessage{ID: 7, Namespace: "team-a", Project: "shop", Task: "shop-42", Author: "ada@example.com",
		AuthorName: "Ada", Role: "user", Kind: "message", Body: "ship it", ClientKey: "k1", State: "answered",
		At: at, ExpiresAt: at.Add(time.Hour)}
	reply := store.ChatMessage{ID: 8, Namespace: "team-a", Project: "shop", Task: "shop-42", Role: "agent", Kind: "message",
		Body: "on it", ReplyTo: 7, State: "answered", Reason: "delivered late", At: at.Add(time.Minute), ExpiresAt: at.Add(time.Hour)}

	sameJSON(t, ChatPage{Messages: []ChatMessageView{chatView(reply, true), chatView(user, true)}}, `{"messages": [
		{"id": 8, "at": "2026-11-05T10:01:00Z", "role": "agent", "kind": "message", "body": "on it", "replyTo": 7,
		 "state": "answered", "reason": "delivered late"},
		{"id": 7, "at": "2026-11-05T10:00:00Z", "author": "ada@example.com", "authorName": "Ada", "role": "user",
		 "kind": "message", "body": "ship it", "state": "answered"}
	]}`)
	sameJSON(t, ChatPage{Messages: []ChatMessageView{chatView(user, false)}}, `{"messages": [
		{"id": 7, "at": "2026-11-05T10:00:00Z", "author": "ada@example.com", "authorName": "Ada", "role": "user",
		 "kind": "message", "state": "answered", "redacted": true}
	]}`)
}
