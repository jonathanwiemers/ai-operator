package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func init() { retryBackoff = time.Millisecond }

// flakyServer answers the first fails requests with status (and headers),
// then with ok.
func flakyServer(t *testing.T, fails int, status int, header http.Header, ok string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if int(n.Add(1)) <= fails {
			for k, v := range header {
				w.Header()[k] = v
			}
			http.Error(w, `{"message":"try again"}`, status)
			return
		}
		fmt.Fprint(w, ok)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestRetriesReadsOnServerErrors(t *testing.T) {
	srv, n := flakyServer(t, 2, http.StatusBadGateway, nil, `{"iid":7}`)
	c := New(srv.URL, "tok")
	is, err := c.GetIssue(context.Background(), "g/p", 7)
	if err != nil || is.IID != 7 || n.Load() != 3 || c.Stats() != (Stats{Requests: 3, Retries: 2}) {
		t.Fatalf("a GET is retried on 502: %v %+v requests %d stats %+v", err, is, n.Load(), c.Stats())
	}

	srv, n = flakyServer(t, 5, http.StatusBadGateway, nil, `{}`)
	_, err = New(srv.URL, "tok").GetIssue(context.Background(), "g/p", 7)
	if StatusCode(err) != http.StatusBadGateway || !IsTransient(err) || n.Load() != int64(maxAttempts) {
		t.Fatalf("gives up after %d attempts: %v, %d requests", maxAttempts, err, n.Load())
	}
}

func TestDoesNotRetryWritesOnServerErrors(t *testing.T) {
	srv, n := flakyServer(t, 1, http.StatusBadGateway, nil, `{"id":1}`)
	_, err := New(srv.URL, "tok").CreateIssueNote(context.Background(), "g/p", 7, "hi")
	if StatusCode(err) != http.StatusBadGateway || n.Load() != 1 {
		t.Fatalf("a POST may have been applied, so it is not retried: %v, %d requests", err, n.Load())
	}
}

func TestRetriesRateLimitedWrites(t *testing.T) {
	srv, n := flakyServer(t, 1, http.StatusTooManyRequests, http.Header{"Retry-After": {"0"}}, `{"id":1}`)
	note, err := New(srv.URL, "tok").CreateIssueNote(context.Background(), "g/p", 7, "hi")
	if err != nil || note.ID != 1 || n.Load() != 2 {
		t.Fatalf("a rate-limited request did not run, so it is retried: %v, %d requests", err, n.Load())
	}

	srv, n = flakyServer(t, 1, http.StatusTooManyRequests, http.Header{"Retry-After": {"3600"}}, `{"id":1}`)
	_, err = New(srv.URL, "tok").CreateIssueNote(context.Background(), "g/p", 7, "hi")
	if StatusCode(err) != http.StatusTooManyRequests || !IsTransient(err) || n.Load() != 1 {
		t.Fatalf("an hour is not waited out: %v, %d requests", err, n.Load())
	}
}

func TestRetryStopsWithTheContext(t *testing.T) {
	srv, n := flakyServer(t, 5, http.StatusTooManyRequests, http.Header{"Retry-After": {"30"}}, `{}`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	_, err := New(srv.URL, "tok").GetIssue(ctx, "g/p", 7)
	if StatusCode(err) != http.StatusTooManyRequests || n.Load() != 1 || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("a wait past the deadline is not started: %v, %d requests after %s", err, n.Load(), time.Since(start))
	}
}

func TestRetriesReadsAfterATimeout(t *testing.T) {
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			time.Sleep(200 * time.Millisecond)
		}
		fmt.Fprint(w, `{"iid":7}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	c.http.Timeout = 50 * time.Millisecond
	if is, err := c.GetIssue(context.Background(), "g/p", 7); err != nil || is.IID != 7 || n.Load() != 2 {
		t.Fatalf("a timed out GET is retried: %v, %d requests", err, n.Load())
	}
}

func TestRateLimitWait(t *testing.T) {
	now := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		h    http.Header
		want time.Duration
		ok   bool
	}{
		{http.Header{"Retry-After": {"12"}}, 12 * time.Second, true},
		{http.Header{"Retry-After": {now.Add(5 * time.Second).Format(http.TimeFormat)}}, 5 * time.Second, true},
		{http.Header{"Ratelimit-Reset": {fmt.Sprint(now.Add(30 * time.Second).Unix())}}, 30 * time.Second, true},
		{http.Header{"Ratelimit-Reset": {fmt.Sprint(now.Add(-time.Minute).Unix())}}, 0, true},
		{http.Header{}, 0, false},
	} {
		if got, ok := rateLimitWait(tc.h, now); got != tc.want || ok != tc.ok {
			t.Errorf("%v: got %s %v, want %s %v", tc.h, got, ok, tc.want, tc.ok)
		}
	}
}

func TestIsTransient(t *testing.T) {
	limited := &Error{StatusCode: 429}
	bad := &Error{StatusCode: 502}
	missing := &Error{StatusCode: 404}
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{limited, true},
		{fmt.Errorf("listing issues: %w", bad), true},
		{missing, false},
		{errors.Join(fmt.Errorf("a: %w", limited), bad), true},
		{errors.Join(bad, missing), false},
		{fmt.Errorf("x: %w", context.DeadlineExceeded), true},
		{errors.New("invalid milestones.titlePattern"), false},
	} {
		if got := IsTransient(tc.err); got != tc.want {
			t.Errorf("%v: got %v", tc.err, got)
		}
	}
}
