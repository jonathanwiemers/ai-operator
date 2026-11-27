package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"
)

// record429Waits skips the rate limit's waits and records the 429 ones.
func record429Waits(t *testing.T) func() []time.Duration {
	t.Helper()
	var mu sync.Mutex
	var waits []time.Duration
	t.Cleanup(SkipRateLimitWaits())
	limitSleep = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()
		return ctx.Err()
	}
	return func() []time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Duration(nil), waits...)
	}
}

func TestRateLimitedCallWaitsForTheReset(t *testing.T) {
	in3 := func() time.Time { return time.Now().Add(3 * time.Second) }
	for name, tc := range map[string]struct {
		h        http.Header
		min, max time.Duration
	}{
		"Retry-After seconds": {http.Header{"Retry-After": {"3"}}, 2900 * time.Millisecond, 3 * time.Second},
		"Retry-After date":    {http.Header{"Retry-After": {in3().UTC().Format(http.TimeFormat)}}, time.Second, 3 * time.Second},
		"RateLimit-Reset":     {http.Header{"Ratelimit-Reset": {strconv.FormatInt(in3().Unix(), 10)}}, time.Second, 3 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			waits := record429Waits(t)
			srv, n := flakyServer(t, 1, http.StatusTooManyRequests, tc.h, `{"iid":7}`)
			is, err := New(srv.URL, "tok").GetIssue(context.Background(), "g/p", 7)
			w := waits()
			if err != nil || is.IID != 7 || n.Load() != 2 || len(w) != 1 || w[0] < tc.min || w[0] > tc.max {
				t.Fatalf("waits for the reset and succeeds: %v, %d requests, waits %v", err, n.Load(), w)
			}
		})
	}
}

func TestRateLimitedCallWithoutHeadersWaitsForTheNextMinute(t *testing.T) {
	waits := record429Waits(t)
	srv, n := flakyServer(t, 1, http.StatusTooManyRequests, nil, `{"iid":7}`)
	before := time.Now()
	_, err := New(srv.URL, "tok").GetIssue(context.Background(), "g/p", 7)
	after := time.Now()
	w := waits()
	if err != nil || n.Load() != 2 || len(w) != 1 {
		t.Fatalf("%v, %d requests, waits %v", err, n.Load(), w)
	}
	slack := after.Sub(before)
	if lo, hi := nextMinute(before).Add(time.Second-slack), nextMinute(after).Add(5*time.Second+slack); before.Add(w[0]).Before(lo) || after.Add(w[0]).After(hi) {
		t.Errorf("waited %v from %v, want the next minute plus 1-5 s", w[0], before)
	}
}

func TestRateLimitedWriteIsRetried(t *testing.T) {
	waits := record429Waits(t)
	srv, n := flakyServer(t, 2, http.StatusTooManyRequests, nil, `{"id":1}`)
	note, err := New(srv.URL, "tok").CreateIssueNote(context.Background(), "g/p", 7, "hi")
	if err != nil || note.ID != 1 || n.Load() != 3 || len(waits()) != 2 {
		t.Fatalf("a POST GitLab refused with 429 is retried: %v, %d requests, waits %v", err, n.Load(), waits())
	}
}

func TestRateLimitedCallGivesUp(t *testing.T) {
	t.Run("after five attempts", func(t *testing.T) {
		waits := record429Waits(t)
		srv, n := flakyServer(t, 10, http.StatusTooManyRequests, http.Header{"Retry-After": {"1"}}, `{}`)
		start := time.Now()
		_, err := New(srv.URL, "tok").GetIssue(context.Background(), "g/p", 7)
		reset, ok := RateLimitReset(fmt.Errorf("listing issue notes: %w", err))
		if StatusCode(err) != http.StatusTooManyRequests || n.Load() != max429Attempts || len(waits()) != max429Attempts-1 {
			t.Fatalf("%v, %d requests, waits %v", err, n.Load(), waits())
		}
		if !ok || reset.Before(start.Add(time.Second)) || reset.After(time.Now().Add(time.Second)) {
			t.Errorf("RateLimitReset = %v %v, want the last Retry-After", reset, ok)
		}
	})
	t.Run("after two minutes of waits", func(t *testing.T) {
		waits := record429Waits(t)
		srv, n := flakyServer(t, 10, http.StatusTooManyRequests, http.Header{"Retry-After": {"50"}}, `{}`)
		_, err := New(srv.URL, "tok").CreateIssueNote(context.Background(), "g/p", 7, "hi")
		if _, ok := RateLimitReset(err); StatusCode(err) != http.StatusTooManyRequests || !ok || n.Load() != 3 || len(waits()) != 2 {
			t.Fatalf("50 s, 50 s, then 50 s more would pass 2 min: %v %v, %d requests, waits %v", err, ok, n.Load(), waits())
		}
	})
}

func TestRateLimitedCallPacesTheOtherCalls(t *testing.T) {
	srv, _ := flakyServer(t, 1, http.StatusTooManyRequests, http.Header{"Retry-After": {"30"}}, `{}`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c := New(srv.URL, "shared-429-tok")
	if _, err := c.GetIssue(ctx, "g/p", 7); StatusCode(err) != http.StatusTooManyRequests {
		t.Fatalf("a 30 s wait past the deadline is not started: %v", err)
	}
	r, ok := c.RateLimit()
	if !ok || r.Remaining != 0 || r.ResetAt.Before(time.Now().Add(29*time.Second)) {
		t.Fatalf("the 429 is in the shared reading: %+v %v", r, ok)
	}
	other := New(srv.URL, "shared-429-tok")
	if _, err := other.GetIssue(ctx, "g/p", 7); !errors.As(err, new(*PacedError)) {
		t.Errorf("another client of the token waits for the reset: %v", err)
	}
}

func TestRateLimitResetOfErrors(t *testing.T) {
	early, late := time.Unix(1_800_000_000, 0), time.Unix(1_800_000_060, 0)
	limited := func(at time.Time) error { return &Error{StatusCode: http.StatusTooManyRequests, Reset: at} }
	for _, tc := range []struct {
		err  error
		want time.Time
	}{
		{nil, time.Time{}},
		{errors.New("boom"), time.Time{}},
		{&Error{StatusCode: http.StatusBadGateway, Reset: late}, time.Time{}},
		{&Error{StatusCode: http.StatusTooManyRequests}, time.Time{}},
		{fmt.Errorf("listing issue notes: %w", limited(early)), early},
		{errors.Join(limited(late), fmt.Errorf("a: %w", limited(early)), errors.New("x")), late},
		{fmt.Errorf("run: %w", errors.Join(fmt.Errorf("b: %w", limited(early)), limited(late))), late},
	} {
		if got, ok := RateLimitReset(tc.err); !got.Equal(tc.want) || ok != !tc.want.IsZero() {
			t.Errorf("%v: got %v %v, want %v", tc.err, got, ok, tc.want)
		}
	}
}
