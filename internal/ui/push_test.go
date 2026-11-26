package ui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// fakePushStore is the push tables in memory.
type fakePushStore struct {
	mu  sync.Mutex
	key *store.PushKey
	// readErr fails PushKey, claimErr ClaimPush.
	readErr, claimErr error
	subs              []store.PushSubscription
	nextID            int64
	sent              map[int64]map[string]bool
	events            []store.Event
	// locked is the sender's lock.
	locked bool
}

func (f *fakePushStore) PushKey(context.Context) (store.PushKey, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return store.PushKey{}, false, f.readErr
	}
	if f.key == nil {
		return store.PushKey{}, false, nil
	}
	return *f.key, true, nil
}

// stored is the stored key, nil for none.
func (f *fakePushStore) stored() *store.PushKey {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.key
}

// deleteKey deletes the stored key.
func (f *fakePushStore) deleteKey() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.key = nil
}

func (f *fakePushStore) EnsurePushKey(_ context.Context, public, private []byte) (store.PushKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.key == nil {
		f.key = &store.PushKey{Public: public, Private: private}
	}
	return *f.key, nil
}

func (f *fakePushStore) UpsertPushSubscription(_ context.Context, sub store.PushSubscription, keep bool, now time.Time) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.subs {
		if old := &f.subs[i]; old.Endpoint == sub.Endpoint {
			if old.Username != sub.Username {
				return 0, false, store.ErrPushSubscriptionTaken
			}
			sub.ID, sub.CreatedAt, sub.RefreshedAt = old.ID, old.CreatedAt, now
			if keep {
				sub.Categories = old.Categories
			}
			*old = sub
			return sub.ID, false, nil
		}
	}
	f.nextID++
	sub.ID, sub.CreatedAt, sub.RefreshedAt = f.nextID, now, now
	f.subs = append(f.subs, sub)
	return sub.ID, true, nil
}

func (f *fakePushStore) DeletePushSubscription(_ context.Context, endpoint, username string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(f.subs)
	f.subs = slices.DeleteFunc(f.subs, func(s store.PushSubscription) bool { return s.Endpoint == endpoint && s.Username == username })
	return len(f.subs) < n, nil
}

func (f *fakePushStore) DeletePushSubscriptionID(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = slices.DeleteFunc(f.subs, func(s store.PushSubscription) bool { return s.ID == id })
	delete(f.sent, id)
	return nil
}

func (f *fakePushStore) ClaimPush(_ context.Context, id int64, keys []string, _ time.Time) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	if f.sent == nil {
		f.sent = map[int64]map[string]bool{}
	}
	if f.sent[id] == nil {
		f.sent[id] = map[string]bool{}
	}
	var claimed []string
	for _, k := range keys {
		if !f.sent[id][k] {
			f.sent[id][k] = true
			claimed = append(claimed, k)
		}
	}
	slices.Sort(claimed)
	return claimed, nil
}

func (f *fakePushStore) Stream(_ context.Context, q store.StreamQuery) ([]store.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.Event
	for _, e := range f.events {
		if slices.Contains(q.Projects, store.ProjectKey{Namespace: e.Namespace, Name: e.Project}) &&
			slices.Contains(q.Kinds, e.Kind) && (q.After == nil || e.At.After(q.After.At)) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakePushStore) PushSubscriptions(context.Context) ([]store.PushSubscription, error) {
	return f.subscriptions(), nil
}

func (f *fakePushStore) UnclaimPush(_ context.Context, id int64, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sent[id], key)
	return nil
}

func (f *fakePushStore) ForgetPush(_ context.Context, id int64, keep []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	maps.DeleteFunc(f.sent[id], func(k string, _ bool) bool {
		return !strings.HasPrefix(k, "event/") && !slices.Contains(keep, k)
	})
	return nil
}

func (f *fakePushStore) PushFailed(_ context.Context, id int64, max int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.subs {
		if f.subs[i].ID == id {
			if f.subs[i].Failures++; f.subs[i].Failures >= max {
				f.subs = slices.Delete(f.subs, i, i+1)
				delete(f.sent, id)
				return true, nil
			}
		}
	}
	return false, nil
}

func (f *fakePushStore) PushSucceeded(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.subs {
		if f.subs[i].ID == id {
			f.subs[i].Failures = 0
		}
	}
	return nil
}

func (f *fakePushStore) DeleteExpiredPush(context.Context, time.Time) (int64, int64, error) {
	return 0, 0, nil
}

// TryPushLock is the advisory lock of all senders sharing f.
func (f *fakePushStore) TryPushLock(context.Context) (func(), bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.locked {
		return nil, false, nil
	}
	f.locked = true
	return func() {
		f.mu.Lock()
		f.locked = false
		f.mu.Unlock()
	}, true, nil
}

// subscriptions are the stored subscriptions.
func (f *fakePushStore) subscriptions() []store.PushSubscription {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.subs)
}

// ledger is the sorted ledger of subscription id.
func (f *fakePushStore) ledger(id int64) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(maps.Keys(f.sent[id]))
}

// pushEnv is a Desk with auth mode none, a database and push on, whose push
// tables are pushes.
func pushEnv(t *testing.T) (*testEnv, *fakePushStore) {
	t.Helper()
	env := newTestEnv(t, withResearch(&Config{Auth: AuthConfig{Mode: AuthNone}}, false))
	pushes := &fakePushStore{}
	env.server.pushes = pushes
	return env, pushes
}

func getPushInfo(t *testing.T, env *testEnv) PushInfo {
	t.Helper()
	code, body := env.call(t, http.MethodGet, "/api/v1/push", "", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/v1/push: %d %s", code, body)
	}
	var info PushInfo
	if err := json.Unmarshal([]byte(body), &info); err != nil {
		t.Fatal(err)
	}
	return info
}

func TestPushInfoAvailable(t *testing.T) {
	env, pushes := pushEnv(t)
	info := getPushInfo(t, env)
	if !info.Available || info.Reason != "" {
		t.Fatalf("info = %+v, want available", info)
	}
	if k, err := base64.RawURLEncoding.DecodeString(info.PublicKey); err != nil || len(k) != 65 || k[0] != 4 {
		t.Errorf("publicKey %q is not a base64url P-256 point: %v", info.PublicKey, err)
	}
	if pushes.stored() == nil {
		t.Fatal("the generated key was not stored")
	}
	// The stored key is read again, not generated again.
	if again := getPushInfo(t, env); again.PublicKey != info.PublicKey {
		t.Errorf("second GET has key %q, want %q", again.PublicKey, info.PublicKey)
	}
	// Deleting the row rotates the key.
	pushes.deleteKey()
	if rotated := getPushInfo(t, env); rotated.PublicKey == info.PublicKey || !rotated.Available {
		t.Errorf("after deleting the key: %+v", rotated)
	}
}

func TestPushInfoOff(t *testing.T) {
	off := false
	cases := []struct {
		name   string
		change func(*Server)
		want   string
	}{
		{"webPush.enabled false", func(s *Server) { s.cfg.WebPush.Enabled = &off },
			"Push notifications are turned off on this Desk (ui.webPush.enabled)."},
		{"no database", func(s *Server) { s.cfg.Research = ResearchConfig{} },
			"Push notifications need the Desk's database (database.host)."},
		{"no externalURL", func(s *Server) { s.cfg.ExternalURL = "" },
			"Push notifications need the Desk's external URL (ui.externalURL)."},
		{"not connected", func(s *Server) { s.pushes = nil },
			"The Desk's database is not connected yet."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env, _ := pushEnv(t)
			c.change(env.server)
			if info := getPushInfo(t, env); info != (PushInfo{Reason: c.want}) {
				t.Errorf("info = %+v, want reason %q", info, c.want)
			}
		})
	}
}

func TestPushInfoStoreError(t *testing.T) {
	env, pushes := pushEnv(t)
	pushes.readErr = errors.New("connection refused")
	if code, _ := env.call(t, http.MethodGet, "/api/v1/push", "", nil); code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500", code)
	}
}

func TestPushKeyBroken(t *testing.T) {
	pushes := &fakePushStore{key: &store.PushKey{Public: []byte{4, 1}, Private: []byte{1}}}
	if _, _, err := pushKey(t.Context(), pushes); err == nil {
		t.Error("a broken stored key was accepted")
	}
}

func TestPreparePushKey(t *testing.T) {
	env, pushes := pushEnv(t)
	env.server.preparePushKey(t.Context())
	if pushes.stored() == nil {
		t.Fatal("no key after preparePushKey")
	}
	stored := *pushes.stored()
	env.server.preparePushKey(t.Context())
	if string(pushes.stored().Public) != string(stored.Public) {
		t.Error("preparePushKey replaced the stored key")
	}
	// With push off it does not touch the store.
	env, pushes = pushEnv(t)
	env.server.cfg.ExternalURL = ""
	env.server.preparePushKey(t.Context())
	if pushes.stored() != nil {
		t.Error("preparePushKey generated a key with push off")
	}
}

func TestAllowedPushEndpoint(t *testing.T) {
	cases := []struct {
		endpoint string
		want     bool
	}{
		{"https://fcm.googleapis.com/fcm/send/abc", true},
		{"https://FCM.googleapis.com/fcm/send/abc", true},
		{"https://fcm.googleapis.com:443/fcm/send/abc", true},
		{"https://web.push.apple.com/QF1", true},
		{"https://a.b.push.apple.com/QF1", true},
		{"https://updates.push.services.mozilla.com/wpush/v2/x", true},
		{"https://wns2-par02p.notify.windows.com/w/?token=x", true},
		{"https://evil.example/fcm", false},
		{"http://fcm.googleapis.com/fcm/send/abc", false},
		{"https://fcm.googleapis.com:8443/fcm/send/abc", false},
		{"https://push.apple.com/", false},
		{"https://.push.apple.com/", false},
		{"https://evilpush.apple.com/", false},
		{"https://fcm.googleapis.com.evil.example/", false},
		{"https://user:pw@fcm.googleapis.com/", false},
		{"https:fcm.googleapis.com/x", false},
		{"//fcm.googleapis.com/x", false},
		{"https://127.0.0.1/x", false},
		{"", false},
		{"https://fcm.googleapis.com/%zz", false},
	}
	for _, c := range cases {
		if got := allowedPushEndpoint(c.endpoint, DefaultPushHosts); got != c.want {
			t.Errorf("allowedPushEndpoint(%q) = %v, want %v", c.endpoint, got, c.want)
		}
	}
	if allowedPushEndpoint("https://fcm.googleapis.com/x", nil) {
		t.Error("an empty allowlist allowed a host")
	}
}

func TestPushEventCategory(t *testing.T) {
	cases := []struct {
		kind string
		data map[string]any
		want string
	}{
		{"milestone.started", nil, "milestoneStarted"},
		{"autopilot", map[string]any{"phase": "Done"}, "milestoneDone"},
		{"autopilot", map[string]any{"phase": "Running"}, ""},
		{"release.ended", map[string]any{"state": "Released"}, "release"},
		{"release.ended", map[string]any{"state": "Failed"}, "release"},
		{"release.ended", map[string]any{"state": "Skipped"}, ""},
		{"note", nil, ""},
	}
	for _, c := range cases {
		if got := pushEventCategory(StreamEvent{Kind: c.kind, Data: c.data}); got != c.want {
			t.Errorf("%s %v: %q, want %q", c.kind, c.data, got, c.want)
		}
	}
}

var (
	testP256dh = base64.RawURLEncoding.EncodeToString(append([]byte{4}, make([]byte, 64)...))
	testAuth   = base64.RawURLEncoding.EncodeToString(make([]byte, 16))
)

// pushBody is a PUT body for endpoint; categories is the JSON of the
// categories field, empty to leave it out.
func pushBody(endpoint, categories string) string {
	body := `{"endpoint":"` + endpoint + `","keys":{"p256dh":"` + testP256dh + `","auth":"` + testAuth + `"}`
	if categories != "" {
		body += `,"categories":` + categories
	}
	return body + "}"
}

func TestPutPushSubscription(t *testing.T) {
	env, pushes := pushEnv(t)
	put := func(body string) (int, string) {
		return env.call(t, http.MethodPut, "/api/v1/push/subscription", body, nil)
	}
	for _, ep := range []string{"https://fcm.googleapis.com/fcm/send/a", "https://web.push.apple.com/b"} {
		if code, body := put(pushBody(ep, "")); code != http.StatusNoContent {
			t.Errorf("PUT %s: %d %s", ep, code, body)
		}
	}
	for name, body := range map[string]string{
		"evil host":        pushBody("https://evil.example/a", ""),
		"http":             pushBody("http://fcm.googleapis.com/a", ""),
		"port":             pushBody("https://fcm.googleapis.com:8443/a", ""),
		"bare apple":       pushBody("https://push.apple.com/", ""),
		"unknown category": pushBody("https://fcm.googleapis.com/c", `["problem","sounds"]`),
		"no keys":          `{"endpoint":"https://fcm.googleapis.com/c"}`,
		"short p256dh":     `{"endpoint":"https://fcm.googleapis.com/c","keys":{"p256dh":"BAAA","auth":"` + testAuth + `"}}`,
		"not JSON":         `endpoint`,
		"too big":          pushBody("https://fcm.googleapis.com/"+strings.Repeat("x", maxPushBody), ""),
	} {
		if code, got := put(body); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, code, got)
		}
	}
	subs := pushes.subscriptions()
	if len(subs) != 2 {
		t.Fatalf("%d subscriptions, want 2: %+v", len(subs), subs)
	}
	if s := subs[0]; s.Username != "anonymous" || s.P256dh != testP256dh || s.Auth != testAuth ||
		!slices.Equal(s.Categories, defaultPushCategories()) || slices.Contains(s.Categories, "failed") {
		t.Errorf("new subscription without categories: %+v", s)
	}
	// Categories replace the stored ones; a PUT without them keeps them.
	ep := "https://fcm.googleapis.com/fcm/send/a"
	if code, body := put(pushBody(ep, `["release","failed","release"]`)); code != http.StatusNoContent {
		t.Fatalf("PUT with categories: %d %s", code, body)
	}
	if code, body := put(pushBody(ep, "")); code != http.StatusNoContent {
		t.Fatalf("PUT without categories: %d %s", code, body)
	}
	if got := pushes.subscriptions()[0].Categories; !slices.Equal(got, []string{"failed", "release"}) {
		t.Errorf("categories %v, want [failed release]", got)
	}
	if code, body := put(pushBody(ep, `["plan"]`)); code != http.StatusNoContent || !slices.Equal(pushes.subscriptions()[0].Categories, []string{"plan"}) {
		t.Errorf("PUT with plan: %d %s, categories %v", code, body, pushes.subscriptions()[0].Categories)
	}
	if !slices.Contains(defaultPushCategories(), "plan") {
		t.Errorf("plan is not on by default: %v", defaultPushCategories())
	}
	if code, body := put(pushBody(ep, `[]`)); code != http.StatusNoContent || len(pushes.subscriptions()[0].Categories) != 0 {
		t.Errorf("PUT with no categories: %d %s, categories %v", code, body, pushes.subscriptions()[0].Categories)
	}
}

// createOrbit creates the project agents/orbit with one open problem, the
// Inbox item agents/orbit/problem/5/7/open.
func createOrbit(t *testing.T, env *testEnv) {
	t.Helper()
	posted := metav1.NewTime(time.Now().Add(-time.Minute).Truncate(time.Second))
	for _, o := range []client.Object{
		&aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "orbit"}},
		&aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "orbit-spec"},
			Spec: aiv1.AgentTaskSpec{ProjectRef: "orbit", Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: 5, Title: "Poker"}},
			Status: aiv1.AgentTaskStatus{
				Plan: []aiv1.PlannedIssue{{IID: 7, Title: "Blinds"}},
				Prompts: []aiv1.Prompt{{ID: "Q1", State: aiv1.PromptOpen, Question: "Split?", PostedAt: &posted,
					Source: aiv1.PromptSource{Kind: aiv1.PromptAssessment, IssueIID: 7}}},
			}},
	} {
		if err := env.reader.Create(t.Context(), o); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPutPushSubscriptionSeeds(t *testing.T) {
	env, pushes := pushEnv(t)
	createOrbit(t, env)
	now := time.Now()
	event := func(id int64, kind, state string, ago time.Duration) store.Event {
		return store.Event{ID: id, At: now.Add(-ago), Namespace: "agents", Project: "orbit", Kind: kind,
			Data: map[string]any{"state": state}}
	}
	pushes.events = []store.Event{
		event(1, "release.ended", "Released", 5*time.Minute),
		event(2, "release.ended", "Released", time.Hour),
		event(3, "release.ended", "Skipped", time.Minute),
	}
	ep := "https://fcm.googleapis.com/fcm/send/a"
	if code, body := env.call(t, http.MethodPut, "/api/v1/push/subscription", pushBody(ep, `["failed"]`), nil); code != http.StatusNoContent {
		t.Fatalf("PUT: %d %s", code, body)
	}
	id := pushes.subscriptions()[0].ID
	ledger := pushes.ledger(id)
	cands, err := env.server.pushCandidates(t.Context(), pushes, Principal{Username: "anonymous"}, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{}
	for _, c := range cands {
		want = append(want, c.Key)
	}
	slices.Sort(want)
	if !slices.Equal(ledger, want) {
		t.Errorf("ledger %v, want the candidates %v", ledger, want)
	}
	// The problem is seeded although the device picked only failed.
	for _, k := range []string{"agents/orbit/problem/5/7/open", "event/1"} {
		if !slices.Contains(ledger, k) {
			t.Errorf("ledger %v lacks %s", ledger, k)
		}
	}
	for _, k := range []string{"event/2", "event/3"} {
		if slices.Contains(ledger, k) {
			t.Errorf("ledger %v has %s", ledger, k)
		}
	}
	// A refresh does not seed again: a new item stays unclaimed.
	pushes.mu.Lock()
	pushes.events = append(pushes.events, event(4, "release.ended", "Failed", time.Second))
	pushes.mu.Unlock()
	if code, body := env.call(t, http.MethodPut, "/api/v1/push/subscription", pushBody(ep, ""), nil); code != http.StatusNoContent {
		t.Fatalf("second PUT: %d %s", code, body)
	}
	if got := pushes.ledger(id); !slices.Equal(got, ledger) {
		t.Errorf("second PUT changed the ledger to %v", got)
	}
}

func TestPutPushSubscriptionSeedFails(t *testing.T) {
	env, pushes := pushEnv(t)
	pushes.claimErr = errors.New("connection reset")
	if code, _ := env.call(t, http.MethodPut, "/api/v1/push/subscription", pushBody("https://fcm.googleapis.com/a", ""), nil); code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500", code)
	}
	if subs := pushes.subscriptions(); len(subs) != 0 {
		t.Errorf("a subscription without its ledger stayed: %+v", subs)
	}
}

func TestDeletePushSubscription(t *testing.T) {
	env, pushes := pushEnv(t)
	del := func(endpoint string) int {
		code, _ := env.call(t, http.MethodDelete, "/api/v1/push/subscription", `{"endpoint":"`+endpoint+`"}`, nil)
		return code
	}
	mine, theirs := "https://fcm.googleapis.com/mine", "https://fcm.googleapis.com/theirs"
	for _, sub := range []store.PushSubscription{{Endpoint: mine, Username: "anonymous"}, {Endpoint: theirs, Username: "bob"}} {
		if _, _, err := pushes.UpsertPushSubscription(t.Context(), sub, false, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if code := del(theirs); code != http.StatusNotFound {
		t.Errorf("DELETE of bob's subscription: %d, want 404", code)
	}
	// Nor can the caller take bob's endpoint over with a PUT.
	if code, body := env.call(t, http.MethodPut, "/api/v1/push/subscription", pushBody(theirs, ""), nil); code != http.StatusConflict {
		t.Errorf("PUT of bob's endpoint: %d %s, want 409", code, body)
	}
	if subs := pushes.subscriptions(); subs[1].Username != "bob" || subs[1].P256dh != "" {
		t.Errorf("bob's subscription changed: %+v", subs[1])
	}
	if code := del(mine); code != http.StatusNoContent {
		t.Errorf("DELETE of my subscription: %d, want 204", code)
	}
	if code := del(mine); code != http.StatusNotFound {
		t.Errorf("second DELETE: %d, want 404", code)
	}
	if code, _ := env.call(t, http.MethodDelete, "/api/v1/push/subscription", `{}`, nil); code != http.StatusBadRequest {
		t.Errorf("DELETE without endpoint: %d, want 400", code)
	}
	if subs := pushes.subscriptions(); len(subs) != 1 || subs[0].Endpoint != theirs {
		t.Errorf("subscriptions %+v, want bob's only", subs)
	}
}

func TestPushSubscriptionRoutesOff(t *testing.T) {
	env, pushes := pushEnv(t)
	env.server.cfg.Research = ResearchConfig{}
	code, body := env.call(t, http.MethodPut, "/api/v1/push/subscription", pushBody("https://fcm.googleapis.com/a", ""), nil)
	if code != http.StatusConflict || !strings.Contains(body, "database.host") {
		t.Errorf("PUT without a database: %d %s, want 409 with the reason", code, body)
	}
	if code, _ := env.call(t, http.MethodDelete, "/api/v1/push/subscription", `{"endpoint":"https://fcm.googleapis.com/a"}`, nil); code != http.StatusConflict {
		t.Errorf("DELETE without a database: %d, want 409", code)
	}
	if len(pushes.subscriptions()) != 0 {
		t.Error("a subscription was stored with push off")
	}
}
