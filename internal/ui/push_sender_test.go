package ui

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// orbitProblem is the key of createOrbit's Inbox item.
const orbitProblem = "agents/orbit/problem/5/7/open"

// receivedPush is a push the fake service got, decrypted.
type receivedPush struct {
	header  http.Header
	payload []byte
}

// fakeDevice is a browser's subscription at a fake push service, which
// decrypts every push with the device's key.
type fakeDevice struct {
	srv  *httptest.Server
	priv *ecdh.PrivateKey
	auth []byte

	mu sync.Mutex
	// code answers every push, 201 when zero; hold, when set, delays the
	// answers until it is closed, after arrived got the push.
	code    int
	hold    chan struct{}
	arrived chan struct{}
	got     []receivedPush
}

func newFakeDevice(t *testing.T) *fakeDevice {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDevice{priv: priv, auth: make([]byte, 16)}
	_, _ = rand.Read(d.auth)
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read push: %v", err)
		}
		plain, err := d.decrypt(body)
		if err != nil {
			t.Errorf("decrypt push: %v", err)
		}
		d.mu.Lock()
		d.got = append(d.got, receivedPush{header: r.Header.Clone(), payload: plain})
		code, hold, arrived := d.code, d.hold, d.arrived
		d.mu.Unlock()
		if hold != nil {
			arrived <- struct{}{}
			<-hold
		}
		if code == 0 {
			code = http.StatusCreated
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(d.srv.Close)
	return d
}

// decrypt is the receiving side of RFC 8291, as in the webpush tests.
func (d *fakeDevice) decrypt(body []byte) ([]byte, error) {
	if len(body) < 21+65 || body[20] != 65 {
		return nil, errors.New("short header")
	}
	salt, asPublicRaw, record := body[:16], body[21:86], body[86:]
	if binary.BigEndian.Uint32(body[16:20]) != 4096 {
		return nil, errors.New("record size")
	}
	asPublic, err := ecdh.P256().NewPublicKey(asPublicRaw)
	if err != nil {
		return nil, err
	}
	secret, err := d.priv.ECDH(asPublic)
	if err != nil {
		return nil, err
	}
	info := append(append([]byte("WebPush: info\x00"), d.priv.PublicKey().Bytes()...), asPublicRaw...)
	ikm, _ := hkdf.Key(sha256.New, secret, d.auth, string(info), 32)
	cek, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, record, nil)
	if err != nil {
		return nil, err
	}
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		return nil, errors.New("no last-record delimiter")
	}
	return plain[:len(plain)-1], nil
}

func (d *fakeDevice) setCode(code int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.code = code
}

func (d *fakeDevice) pushes() []receivedPush {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.got)
}

// keys are the keys of the items and events pushed, in order.
func (d *fakeDevice) keys(t *testing.T) []string {
	t.Helper()
	keys := []string{}
	for _, p := range d.pushes() {
		var m pushMessage
		if err := json.Unmarshal(p.payload, &m); err != nil {
			t.Fatalf("payload %s: %v", p.payload, err)
		}
		switch {
		case m.Item != nil:
			keys = append(keys, m.Item.Key)
		case m.Event != nil:
			keys = append(keys, fmt.Sprintf("event/%d", m.Event.ID))
		}
	}
	return keys
}

// subscribe stores a subscription of d for username with roles and
// categories, and returns its id.
func (d *fakeDevice) subscribe(t *testing.T, pushes *fakePushStore, username string, roles []string, categories ...string) int64 {
	t.Helper()
	id, _, err := pushes.UpsertPushSubscription(t.Context(), store.PushSubscription{
		Endpoint: d.srv.URL + "/push/" + rand.Text(), Username: username, Roles: roles, Categories: categories,
		P256dh: base64.RawURLEncoding.EncodeToString(d.priv.PublicKey().Bytes()),
		Auth:   base64.RawURLEncoding.EncodeToString(d.auth)}, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// senderEnv is a push Desk whose only Inbox item is createOrbit's problem:
// the principal may view agents/orbit alone.
func senderEnv(t *testing.T) (*testEnv, *fakePushStore) {
	t.Helper()
	env, pushes := pushEnv(t)
	env.server.authz = &Authorizer{mode: AuthOIDC, rules: []Rule{{Roles: []string{"orbit"}, Projects: []string{"agents/orbit"}}}}
	createOrbit(t, env)
	return env, pushes
}

// releaseEvent is a release of agents/orbit ago before now, event/id.
func releaseEvent(id int64, ago time.Duration) store.Event {
	return store.Event{ID: id, At: time.Now().Add(-ago), Namespace: "agents", Project: "orbit", Kind: "release.ended",
		Summary: "Released 2026.12.1", Data: map[string]any{"state": "Released", "title": "Poker"}}
}

func synced() bool { return true }

func cycle(t *testing.T, env *testEnv) pushStats {
	t.Helper()
	return env.server.pushCycle(t.Context(), synced)
}

func TestPushSenderSendsOnce(t *testing.T) {
	env, pushes := senderEnv(t)
	pushes.events = []store.Event{releaseEvent(1, time.Minute)}
	d := newFakeDevice(t)
	d.subscribe(t, pushes, "alice", []string{"orbit"}, "problem", "release")

	if st := cycle(t, env); st.sent != 2 || st.failed != 0 || st.subscriptions != 1 {
		t.Fatalf("first cycle %+v, want 2 sent", st)
	}
	if got := d.keys(t); !slices.Equal(got, []string{orbitProblem, "event/1"}) {
		t.Fatalf("pushed %v", got)
	}
	got := d.pushes()
	for i, urgency := range []string{"high", "normal"} {
		h := got[i].header
		if h.Get("TTL") != "86400" || h.Get("Urgency") != urgency || h.Get("Content-Encoding") != "aes128gcm" ||
			!strings.HasPrefix(h.Get("Authorization"), "vapid t=") {
			t.Errorf("push %d headers %v, want TTL 86400, Urgency %s, vapid", i, h, urgency)
		}
	}
	var m pushMessage
	if err := json.Unmarshal(got[0].payload, &m); err != nil || m.Item == nil || m.Event != nil || m.Item.Notify != "problem" {
		t.Errorf("item payload %s: %v", got[0].payload, err)
	}
	if err := json.Unmarshal(got[1].payload, &m); err != nil || m.Event == nil || m.Event.Data["state"] != "Released" {
		t.Errorf("event payload %s: %v", got[1].payload, err)
	}

	if st := cycle(t, env); st.sent != 0 || len(d.pushes()) != 2 {
		t.Errorf("second cycle %+v sent again: %v", st, d.keys(t))
	}
}

func TestPushSenderRetriesFailures(t *testing.T) {
	env, pushes := senderEnv(t)
	pushes.events = []store.Event{releaseEvent(1, time.Minute)}
	d := newFakeDevice(t)
	id := d.subscribe(t, pushes, "alice", []string{"orbit"}, "problem", "release")
	d.setCode(http.StatusInternalServerError)
	if st := cycle(t, env); st.sent != 0 || st.failed != 1 {
		t.Fatalf("failing cycle %+v, want 1 failed", st)
	}
	// The subscription stops after its first failure; the key is unclaimed.
	if len(d.pushes()) != 1 || len(pushes.ledger(id)) != 0 || pushes.subscriptions()[0].Failures != 1 {
		t.Fatalf("after a failure: %d pushes, ledger %v, %+v", len(d.pushes()), pushes.ledger(id), pushes.subscriptions())
	}
	d.setCode(http.StatusCreated)
	if st := cycle(t, env); st.sent != 2 || pushes.subscriptions()[0].Failures != 0 {
		t.Fatalf("retry %+v, %+v", st, pushes.subscriptions())
	}

	// Ten failures in a row delete the subscription.
	pushes.events = append(pushes.events, releaseEvent(2, time.Second))
	d.setCode(http.StatusTooManyRequests)
	for i := range maxPushFailures - 1 {
		if st := cycle(t, env); st.failed != 1 || st.deleted != 0 {
			t.Fatalf("failure %d: %+v", i+1, st)
		}
	}
	if st := cycle(t, env); st.deleted != 1 || len(pushes.subscriptions()) != 0 {
		t.Fatalf("tenth failure %+v, subscriptions %+v", st, pushes.subscriptions())
	}
	if n := len(d.pushes()); n != 3+maxPushFailures {
		t.Errorf("%d pushes, want %d", n, 3+maxPushFailures)
	}
}

func TestPushSenderGone(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusGone} {
		env, pushes := senderEnv(t)
		pushes.events = []store.Event{releaseEvent(1, time.Minute)}
		d := newFakeDevice(t)
		d.subscribe(t, pushes, "alice", []string{"orbit"}, "problem", "release")
		d.setCode(code)
		if st := cycle(t, env); st.deleted != 1 || st.failed != 0 || len(pushes.subscriptions()) != 0 || len(d.pushes()) != 1 {
			t.Errorf("%d: %+v, %d pushes, subscriptions %+v", code, st, len(d.pushes()), pushes.subscriptions())
		}
	}
}

func TestPushSenderNoRedirect(t *testing.T) {
	env, pushes := senderEnv(t)
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the sender followed a redirect")
	}))
	defer elsewhere.Close()
	d := newFakeDevice(t)
	d.srv.Config.Handler = http.RedirectHandler(elsewhere.URL, http.StatusTemporaryRedirect)
	d.subscribe(t, pushes, "alice", []string{"orbit"}, "problem")
	if st := cycle(t, env); st.failed != 1 || st.sent != 0 {
		t.Errorf("redirect: %+v, want a failure", st)
	}
}

func TestPushSenderOnlyViewable(t *testing.T) {
	env, pushes := senderEnv(t)
	pushes.events = []store.Event{releaseEvent(1, time.Minute)}
	other, mine := newFakeDevice(t), newFakeDevice(t)
	other.subscribe(t, pushes, "bob", []string{"team-b"}, pushCategories...)
	mine.subscribe(t, pushes, "alice", []string{"orbit"}, pushCategories...)
	if st := cycle(t, env); st.sent != 2 {
		t.Errorf("cycle %+v, want 2 sent", st)
	}
	if got := other.pushes(); len(got) != 0 {
		t.Errorf("bob, who cannot view agents/orbit, got %d pushes", len(got))
	}
	if got := mine.keys(t); len(got) != 2 {
		t.Errorf("alice got %v", got)
	}
}

func TestPushSenderCategories(t *testing.T) {
	env, pushes := senderEnv(t)
	pushes.events = []store.Event{releaseEvent(1, time.Minute)}
	d := newFakeDevice(t)
	id := d.subscribe(t, pushes, "alice", []string{"orbit"}, "question", "failed")
	if st := cycle(t, env); st.sent != 0 || len(d.pushes()) != 0 || len(pushes.ledger(id)) != 0 {
		t.Errorf("unpicked categories: %+v, pushes %v, ledger %v", st, d.keys(t), pushes.ledger(id))
	}
}

// Two senders sharing the store: the one holding the lock sends, the other
// skips its cycle, and a later cycle finds everything claimed.
func TestPushSenderTwoReplicas(t *testing.T) {
	env, pushes := senderEnv(t)
	env2, _ := senderEnv(t)
	env2.server.pushes = pushes
	d := newFakeDevice(t)
	d.subscribe(t, pushes, "alice", []string{"orbit"}, "problem")
	d.hold, d.arrived = make(chan struct{}), make(chan struct{}, 1)

	first := make(chan pushStats)
	go func() { first <- env.server.pushCycle(context.Background(), synced) }()
	<-d.arrived
	if st := cycle(t, env2); st != (pushStats{}) {
		t.Errorf("the second replica ran a cycle while the first held the lock: %+v", st)
	}
	close(d.hold)
	if st := <-first; st.sent != 1 {
		t.Errorf("first replica %+v, want 1 sent", st)
	}
	if st := cycle(t, env2); st.subscriptions != 1 || st.sent != 0 {
		t.Errorf("second replica after the first %+v, want nothing sent", st)
	}
	if got := d.keys(t); !slices.Equal(got, []string{orbitProblem}) {
		t.Errorf("pushed %v, want the problem once", got)
	}
}

// An item that leaves the Inbox and comes back is pushed again; an event
// that leaves the window and comes back is not.
func TestPushSenderReturns(t *testing.T) {
	env, pushes := senderEnv(t)
	ev := releaseEvent(1, time.Minute)
	pushes.events = []store.Event{ev}
	d := newFakeDevice(t)
	d.subscribe(t, pushes, "alice", []string{"orbit"}, "problem", "release")
	cycle(t, env)

	var task aiv1.AgentTask
	key := client.ObjectKey{Namespace: "agents", Name: "orbit-spec"}
	if err := env.reader.Get(t.Context(), key, &task); err != nil {
		t.Fatal(err)
	}
	if err := env.reader.Delete(t.Context(), &task); err != nil {
		t.Fatal(err)
	}
	pushes.mu.Lock()
	pushes.events = nil
	pushes.mu.Unlock()
	if st := cycle(t, env); st.sent != 0 {
		t.Fatalf("cycle without the item %+v", st)
	}
	task.ObjectMeta = metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name}
	if err := env.reader.Create(t.Context(), &task); err != nil {
		t.Fatal(err)
	}
	pushes.mu.Lock()
	pushes.events = []store.Event{ev}
	pushes.mu.Unlock()
	if st := cycle(t, env); st.sent != 1 {
		t.Errorf("cycle after the return %+v, want the item again", st)
	}
	if got := d.keys(t); !slices.Equal(got, []string{orbitProblem, "event/1", orbitProblem}) {
		t.Errorf("pushed %v", got)
	}
}

func TestPushSenderSkips(t *testing.T) {
	cases := []struct {
		name   string
		synced func() bool
		change func(*Server, *fakePushStore)
	}{
		{"not synced", func() bool { return false }, func(*Server, *fakePushStore) {}},
		{"push off", synced, func(s *Server, _ *fakePushStore) { s.cfg.ExternalURL = "" }},
		{"lock held", synced, func(_ *Server, p *fakePushStore) { p.locked = true }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env, pushes := senderEnv(t)
			d := newFakeDevice(t)
			d.subscribe(t, pushes, "alice", []string{"orbit"}, "problem")
			c.change(env.server, pushes)
			if st := env.server.pushCycle(t.Context(), c.synced); st != (pushStats{}) || len(d.pushes()) != 0 {
				t.Errorf("cycle %+v, %d pushes, want none", st, len(d.pushes()))
			}
		})
	}
}

func TestPushSenderStoreError(t *testing.T) {
	env, pushes := senderEnv(t)
	d := newFakeDevice(t)
	d.subscribe(t, pushes, "alice", []string{"orbit"}, "problem")
	pushes.claimErr = errors.New("connection reset")
	if st := cycle(t, env); st.sent != 0 || len(d.pushes()) != 0 {
		t.Errorf("cycle with a failing claim %+v", st)
	}
	if pushes.locked {
		t.Error("the cycle kept the lock")
	}
}

// The loop runs a cycle after a change, debounced, and on its ticker.
func TestRunPushSender(t *testing.T) {
	for _, c := range []struct {
		name     string
		interval time.Duration
		publish  bool
	}{{"change", time.Hour, true}, {"ticker", 20 * time.Millisecond, false}} {
		t.Run(c.name, func(t *testing.T) {
			env, pushes := senderEnv(t)
			d := newFakeDevice(t)
			d.subscribe(t, pushes, "alice", []string{"orbit"}, "problem")
			d.hold, d.arrived = make(chan struct{}), make(chan struct{}, 1)
			close(d.hold)
			env.server.pushTiming.debounce, env.server.pushTiming.interval = 10*time.Millisecond, c.interval
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() { env.server.RunPushSender(ctx, synced); close(done) }()
			if c.publish {
				// The hub has the sender's subscription once Subscribe ran.
				subscribed := func() bool {
					env.hub.mu.Lock()
					defer env.hub.mu.Unlock()
					return len(env.hub.subs) > 0
				}
				for deadline := time.Now().Add(5 * time.Second); !subscribed(); time.Sleep(time.Millisecond) {
					if time.Now().After(deadline) {
						t.Fatal("the sender did not subscribe to the hub")
					}
				}
				env.hub.Publish(Event{Kind: "task", Namespace: "agents", Name: "orbit-spec", Project: "orbit"})
			}
			select {
			case <-d.arrived:
			case <-time.After(5 * time.Second):
				t.Fatal("no push")
			}
			cancel()
			<-done
		})
	}
}

func TestPushPayloadCut(t *testing.T) {
	long := strings.Repeat("é\"x", 3500) // over 10 KB once encoded
	cases := map[string]pushCandidate{
		"item reason": {Item: &InboxItem{Key: "k", Title: "Blinds", Reason: long}},
		"item title":  {Item: &InboxItem{Key: "k", Title: long}},
		"event":       {Event: &StreamEvent{ID: 1, Kind: "release.ended", Data: map[string]any{"reason": long, "title": long, "state": "Failed"}}},
	}
	for name, c := range cases {
		b, err := pushPayload(c)
		if err != nil || len(b) > maxPushPayload || len(b) < maxPushPayload-10 || !utf8.Valid(b) {
			t.Errorf("%s: %d bytes, %v", name, len(b), err)
			continue
		}
		var m pushMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The reason is cut before the title.
	b, _ := pushPayload(cases["item reason"])
	var m pushMessage
	if _ = json.Unmarshal(b, &m); m.Item.Title != "Blinds" || !strings.HasSuffix(m.Item.Reason, "…") {
		t.Errorf("cut item %+v", m.Item)
	}
	if c := cases["event"]; c.Event.Data["reason"] != long {
		t.Error("the cut changed the candidate's event")
	}
	// What does not fit after cutting the reason and the title is skipped.
	if _, err := pushPayload(pushCandidate{Item: &InboxItem{Key: long}}); !errors.Is(err, errPushTooBig) {
		t.Errorf("a long key: %v, want errPushTooBig", err)
	}
	if b, err := pushPayload(pushCandidate{Item: &InboxItem{Key: "k", Reason: "short"}}); err != nil || !strings.Contains(string(b), `"reason":"short"`) {
		t.Errorf("a short item: %s %v", b, err)
	}
}
