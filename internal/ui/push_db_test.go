package ui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// The push routes against Postgres, with the reader's grants: the key is
// generated once, the first PUT seeds the ledger with the Inbox problem
// and the recent release, and a refresh does not seed again.
func TestPushRoutesDatabase(t *testing.T) {
	owner, reader := readerStore(t)
	env := newTestEnv(t, withResearch(&Config{Auth: AuthConfig{Mode: AuthNone}}, false))
	env.server.SetResearchStore(reader)
	createOrbit(t, env)
	now := time.Now()
	if _, err := owner.InsertEvents(t.Context(), []store.Event{
		{Key: "rel-new", At: now.Add(-5 * time.Minute), Namespace: "agents", Project: "orbit", Kind: "release.ended",
			Summary: "Released 2026.12.1", Data: map[string]any{"state": "Released"}, ExpiresAt: now.Add(24 * time.Hour)},
		{Key: "rel-old", At: now.Add(-time.Hour), Namespace: "agents", Project: "orbit", Kind: "release.ended",
			Summary: "Released 2026.11.9", Data: map[string]any{"state": "Released"}, ExpiresAt: now.Add(24 * time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	recent, err := reader.Stream(t.Context(), store.StreamQuery{Projects: []store.ProjectKey{{Namespace: "agents", Name: "orbit"}},
		After: &store.Cursor{At: now.Add(-10 * time.Minute)}})
	if err != nil || len(recent) != 1 {
		t.Fatalf("recent events %+v: %v", recent, err)
	}

	env.server.preparePushKey(t.Context())
	stored, ok, err := reader.PushKey(t.Context())
	if err != nil || !ok {
		t.Fatalf("no push key after start: %v", err)
	}
	code, body := env.call(t, http.MethodGet, "/api/v1/push", "", nil)
	var info PushInfo
	if err := json.Unmarshal([]byte(body), &info); err != nil || code != http.StatusOK || !info.Available {
		t.Fatalf("GET: %d %s", code, body)
	}
	if info.PublicKey != base64.RawURLEncoding.EncodeToString(stored.Public) {
		t.Errorf("GET public key %q differs from the stored one", info.PublicKey)
	}

	ep := "https://fcm.googleapis.com/fcm/send/db"
	if code, body := env.call(t, http.MethodPut, "/api/v1/push/subscription", pushBody(ep, ""), nil); code != http.StatusNoContent {
		t.Fatalf("PUT: %d %s", code, body)
	}
	if code, body := env.call(t, http.MethodPut, "/api/v1/push/subscription", pushBody(ep, `["release"]`), nil); code != http.StatusNoContent {
		t.Fatalf("second PUT: %d %s", code, body)
	}
	subs, err := owner.PushSubscriptions(t.Context())
	if err != nil || len(subs) != 1 {
		t.Fatalf("subscriptions %+v: %v", subs, err)
	}
	if s := subs[0]; s.Username != "anonymous" || !slices.Equal(s.Categories, []string{"release"}) {
		t.Errorf("subscription %+v", s)
	}
	seeded := []string{"agents/orbit/problem/5/7/open", fmt.Sprintf("event/%d", recent[0].ID)}
	if again, err := owner.ClaimPush(t.Context(), subs[0].ID, seeded, now); err != nil || len(again) != 0 {
		t.Errorf("the seeded keys were not claimed: %v %v", again, err)
	}
	if fresh, err := owner.ClaimPush(t.Context(), subs[0].ID, []string{"event/999999"}, now); err != nil || len(fresh) != 1 {
		t.Errorf("an unseen key: %v %v", fresh, err)
	}

	// Another user's subscription is not the caller's to delete.
	theirs := "https://fcm.googleapis.com/fcm/send/bob"
	if _, _, err := owner.UpsertPushSubscription(t.Context(), store.PushSubscription{Endpoint: theirs, P256dh: testP256dh,
		Auth: testAuth, Username: "bob"}, false, now); err != nil {
		t.Fatal(err)
	}
	if code, _ := env.call(t, http.MethodDelete, "/api/v1/push/subscription", `{"endpoint":"`+theirs+`"}`, nil); code != http.StatusNotFound {
		t.Errorf("DELETE of bob's subscription: %d, want 404", code)
	}
	if code, _ := env.call(t, http.MethodPut, "/api/v1/push/subscription", pushBody(theirs, ""), nil); code != http.StatusConflict {
		t.Errorf("PUT of bob's endpoint: %d, want 409", code)
	}
	if code, _ := env.call(t, http.MethodDelete, "/api/v1/push/subscription", `{"endpoint":"`+ep+`"}`, nil); code != http.StatusNoContent {
		t.Errorf("DELETE of mine: %d, want 204", code)
	}
	if subs, _ := owner.PushSubscriptions(t.Context()); len(subs) != 1 || subs[0].Endpoint != theirs {
		t.Errorf("subscriptions after DELETE: %+v", subs)
	}
}
