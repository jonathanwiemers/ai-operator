package research

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/jonathanwiemers/ai-operator/internal/research/blocks"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

func TestDatabaseConfigLogin(t *testing.T) {
	c := DatabaseConfig{Host: "db", Port: 5432, Name: "ai_operator", SSLMode: "require"}
	sc, err := c.Login("research_owner", "secret")
	if err != nil || sc != (store.Config{Host: "db", Port: 5432, Database: "ai_operator", SSLMode: "require",
		User: "research_owner", Password: "secret"}) {
		t.Errorf("Login = %+v, %v", sc, err)
	}
	for name, bad := range map[string]func() error{
		"no name":     func() error { c := c; c.Name = ""; _, err := c.Login("u", "p"); return err },
		"no user":     func() error { _, err := c.Login("", "p"); return err },
		"no password": func() error { _, err := c.Login("u", ""); return err },
		"bad sslmode": func() error { c := c; c.SSLMode = "prefer"; _, err := c.Login("u", "p"); return err },
	} {
		if bad() == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// TestDatabaseRetries needs no database: setup fails, Start backs off from
// 5 s to 5 min and returns when its context ends, without Ready.
func TestDatabaseRetries(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	d := NewDatabase(store.Config{}, store.Grants{}, clk, testr.New(t))
	var attempts atomic.Int32
	d.open = func(context.Context) (*store.Store, error) {
		attempts.Add(1)
		return nil, errors.New("connection refused")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error)
	go func() { done <- d.Start(ctx) }()
	for i, delay := range []time.Duration{5, 10, 20, 40, 80, 160, 300, 300} {
		eventually(t, func() bool { return attempts.Load() == int32(i+1) && clk.HasWaiters() })
		clk.Step(delay * time.Second)
	}
	eventually(t, func() bool { return attempts.Load() == 9 })
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Start = %v", err)
	}
	if d.Store() != nil {
		t.Error("Store is set without a successful setup")
	}
}

func TestDatabase(t *testing.T) {
	owner := storetest.New(t)
	ctx := t.Context()
	suffix := randomHex(t)
	readerRole, writerRole := "r_"+suffix, "agents.w_"+suffix
	reader, writer := storetest.Role(t, readerRole), storetest.Role(t, writerRole)
	if _, err := reader.PreviousItemURLs(ctx, "agents", "old", 10); err == nil {
		t.Fatal("reader could read before the grants")
	}
	clk := clocktesting.NewFakeClock(time.Now())
	saveRun(t, owner, "old", clk.Now().Add(-time.Minute))
	saveRun(t, owner, "kept", clk.Now().Add(2*time.Hour))

	d := NewDatabase(store.Config{}, store.Grants{Reader: readerRole, Writers: []string{writerRole}}, clk, testr.New(t))
	var attempts int
	d.open = func(ctx context.Context) (*store.Store, error) {
		// Unreachable first, as during a database outage.
		if attempts++; attempts < 3 {
			return store.Open(ctx, store.Config{Host: "127.0.0.1", Port: 1, Database: "x", SSLMode: "disable", User: "u"})
		}
		return owner, nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error)
	go func() { done <- d.Start(runCtx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Start = %v", err)
		}
	}()
	eventually(t, func() bool {
		select {
		case <-d.Ready():
			return true
		default:
		}
		if clk.HasWaiters() {
			clk.Step(maxRetry)
		}
		return false
	})
	if attempts != 3 || d.Store() != owner {
		t.Fatalf("ready after %d attempts with store %p, want 3 and %p", attempts, d.Store(), owner)
	}
	// The hourly ticker is created after the first deletion.
	eventually(t, clk.HasWaiters)
	if urls := previous(t, reader, "old"); len(urls) != 0 {
		t.Errorf("expired run not deleted at start: %v", urls)
	}
	saveRun(t, writer, "new", clk.Now().Add(30*time.Minute))
	clk.Step(retention)
	eventually(t, func() bool { return len(previous(t, reader, "new")) == 0 })
	if urls := previous(t, reader, "kept"); len(urls) != 1 {
		t.Errorf("run that has not expired: urls %v, want 1", urls)
	}
}

func saveRun(t *testing.T, s *store.Store, research string, expires time.Time) {
	t.Helper()
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	id := hex.EncodeToString(b)
	id = id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
	run := store.Run{ID: id, Namespace: "agents", Research: research, StartedAt: time.Now(), ExpiresAt: expires}
	if err := s.StartRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	result := blocks.Result{SchemaVersion: 1, Title: "t", Blocks: []blocks.Block{{Type: blocks.TypeItems,
		Items: &blocks.Items{Items: []blocks.Item{{Title: "a", URL: "https://example.com/" + research, Importance: 2}}}}}}
	if err := s.SaveResult(t.Context(), id, result, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func previous(t *testing.T, s *store.Store, research string) []string {
	t.Helper()
	urls, err := s.PreviousItemURLs(t.Context(), "agents", research, 10)
	if err != nil {
		t.Fatal(err)
	}
	return urls
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in 20 s")
		}
	}
}

func randomHex(t *testing.T) string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
