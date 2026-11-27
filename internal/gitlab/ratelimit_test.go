package gitlab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// limitServer serves RateLimit headers with the given remaining count and a
// reset resetIn ahead, and counts the requests it got.
func limitServer(t *testing.T, remaining int, resetIn time.Duration) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("RateLimit-Limit", "100")
		w.Header().Set("RateLimit-Observed", strconv.Itoa(100-remaining))
		w.Header().Set("RateLimit-Remaining", strconv.Itoa(remaining))
		w.Header().Set("RateLimit-Reset", strconv.FormatInt(time.Now().Add(resetIn).Unix(), 10))
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// recordWaits replaces paceSleep with one that records the waits.
func recordWaits(t *testing.T) *[]time.Duration {
	t.Helper()
	var waits []time.Duration
	prev := paceSleep
	paceSleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	t.Cleanup(func() { paceSleep = prev })
	return &waits
}

func get(t *testing.T, ctx context.Context, c *Client) error {
	t.Helper()
	_, err := c.do(ctx, http.MethodGet, "/projects/1", nil, nil, nil)
	return err
}

func TestBackgroundReadWaitsAtTheReserve(t *testing.T) {
	srv, _ := limitServer(t, 20, 30*time.Second)
	waits := recordWaits(t)
	c := New(srv.URL, "reserve-tok")
	ctx := context.Background()
	if err := get(t, ctx, c); err != nil { // reads the headers
		t.Fatal(err)
	}
	if err := get(t, ctx, c); err != nil {
		t.Fatal(err)
	}
	if len(*waits) != 0 {
		t.Fatalf("a work read waited: %v", *waits)
	}
	before := PacedSeconds(Background)
	if err := get(t, WithPriority(ctx, Background), c); err != nil {
		t.Fatal(err)
	}
	if len(*waits) != 1 || (*waits)[0] < 25*time.Second || (*waits)[0] > 30*time.Second {
		t.Fatalf("background waits = %v, want one until the reset", *waits)
	}
	if got := PacedSeconds(Background) - before; got < 25 {
		t.Errorf("paced background seconds rose by %v", got)
	}
}

func TestEveryCallWaitsAtZero(t *testing.T) {
	srv, _ := limitServer(t, 0, 2*time.Minute)
	waits := recordWaits(t)
	c := New(srv.URL, "zero-tok")
	ctx := context.Background()
	_ = get(t, ctx, c)
	_ = get(t, ctx, c)
	_ = get(t, WithPriority(ctx, Background), c)
	if _, err := c.do(ctx, http.MethodPost, "/projects/1/notes", nil, map[string]string{"body": "x"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(*waits) != 3 {
		t.Fatalf("waits = %v, want 3", *waits)
	}
	for _, w := range *waits {
		if w != maxPaceWait {
			t.Errorf("wait %v, want the cap %v", w, maxPaceWait)
		}
	}
}

func TestBackgroundReadPastItsDeadlineIsNotSent(t *testing.T) {
	srv, hits := limitServer(t, 5, 30*time.Second)
	recordWaits(t)
	c := New(srv.URL, "deadline-tok")
	_ = get(t, context.Background(), c)
	ctx, cancel := context.WithTimeout(WithPriority(context.Background(), Background), time.Second)
	defer cancel()
	err := get(t, ctx, c)
	var pe *PacedError
	if !errors.As(err, &pe) || pe.Priority != Background || !IsTransient(err) {
		t.Fatalf("err = %v, want a transient PacedError", err)
	}
	if hits.Load() != 1 {
		t.Errorf("server got %d requests, want 1", hits.Load())
	}
}

func TestWriteInABackgroundContextIsWork(t *testing.T) {
	srv, _ := limitServer(t, 10, 30*time.Second)
	waits := recordWaits(t)
	c := New(srv.URL, "write-tok")
	ctx := WithPriority(context.Background(), Background)
	_ = get(t, context.Background(), c)
	if _, err := c.do(ctx, http.MethodPost, "/projects/1/notes", nil, map[string]string{"body": "x"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(*waits) != 0 {
		t.Fatalf("a write waited: %v", *waits)
	}
}

func TestClientsShareTheReadingPerToken(t *testing.T) {
	srv, _ := limitServer(t, 42, 30*time.Second)
	a := New(srv.URL, "shared-tok")
	if _, ok := a.RateLimit(); ok {
		t.Fatal("reading before any response")
	}
	_ = get(t, context.Background(), a)
	r, ok := New(srv.URL, "shared-tok").RateLimit()
	if !ok || r.Remaining != 42 || r.Limit != 100 || r.Observed != 58 || r.ResetAt.IsZero() || r.ReadAt.IsZero() {
		t.Fatalf("shared reading = %+v, %v", r, ok)
	}
	if _, ok := New(srv.URL, "other-tok").RateLimit(); ok {
		t.Error("another token sees the reading")
	}
	// Each request sent counts off the reading until a response says more.
	a.limit.sent()
	if r, _ := a.RateLimit(); r.Remaining != 41 {
		t.Errorf("remaining after a send = %d, want 41", r.Remaining)
	}
}

func TestNoHeadersNoPacing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	waits := recordWaits(t)
	c := New(srv.URL, "plain-tok")
	ctx := WithPriority(context.Background(), Background)
	for range 3 {
		if err := get(t, ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := c.RateLimit(); ok || len(*waits) != 0 {
		t.Errorf("reading %v, waits %v", ok, *waits)
	}
	if (&Client{}).pace(ctx, request{}) != nil {
		t.Error("a client without a reading paced")
	}
	if _, ok := (&Client{}).RateLimit(); ok {
		t.Error("a client without a reading has one")
	}
}

func TestPassedResetNeverWaits(t *testing.T) {
	l := &tokenLimit{}
	now := time.Now()
	h := http.Header{}
	h.Set("RateLimit-Remaining", "0")
	h.Set("RateLimit-Reset", strconv.FormatInt(now.Add(-time.Second).Unix(), 10))
	l.observe(h, now)
	if w := l.wait(Work, now); w != 0 {
		t.Errorf("wait = %v after the reset", w)
	}
}

func TestSetRateLimitReserve(t *testing.T) {
	defer SetRateLimitReserve(int(rateLimitReserve.Load()))
	l := &tokenLimit{known: true, r: RateLimitReading{Remaining: 30, ResetAt: time.Now().Add(time.Minute)}}
	if l.wait(Background, time.Now()) != 0 {
		t.Fatal("waited above the default reserve")
	}
	SetRateLimitReserve(30)
	if l.wait(Background, time.Now()) == 0 {
		t.Error("did not wait at a raised reserve")
	}
	SetRateLimitReserve(-1)
	if rateLimitReserve.Load() != 0 {
		t.Error("negative reserve kept")
	}
}

func TestPaceSleep(t *testing.T) {
	if err := paceSleep(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := paceSleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	// A canceled wait fails the call without sending it.
	srv, hits := limitServer(t, 0, 30*time.Second)
	c := New(srv.URL, "cancel-tok")
	_ = get(t, context.Background(), c)
	if err := get(t, ctx, c); !errors.Is(err, context.Canceled) || hits.Load() != 1 {
		t.Errorf("err = %v, hits %d", err, hits.Load())
	}
	if Work.String() != "work" || Background.String() != "background" {
		t.Error("priority names")
	}
	if msg := (&PacedError{Method: "GET", Path: "/x", Priority: Background, Wait: 3 * time.Second}).Error(); msg == "" {
		t.Error("empty message")
	}
}
