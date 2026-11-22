package gitlab

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RetryTransient calls fn until it returns an error that is not transient (or
// none), maxAttempts times at most, with the client's backoff in between. It
// is for writes the caller made safe to repeat, which the client does not
// retry on its own. A wait past the context's deadline is not started; the
// last error is returned.
func RetryTransient(ctx context.Context, fn func() error) error {
	for attempt := 1; ; attempt++ {
		err := fn()
		if !IsTransient(err) || attempt >= maxAttempts || ctx.Err() != nil {
			return err
		}
		wait := backoffAfter(attempt)
		if d, has := ctx.Deadline(); has && time.Until(d) < wait {
			return err
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return err
		case <-t.C:
		}
	}
}

// SetRetryBackoff sets the first wait between retries, for the tests of
// other packages, and returns a function that restores it.
func SetRetryBackoff(d time.Duration) (restore func()) {
	prev := retryBackoff
	retryBackoff = d
	return func() { retryBackoff = prev }
}

// backoffAfter is the wait after the attempt-th attempt: exponential, with jitter.
func backoffAfter(attempt int) time.Duration {
	b := retryBackoff << (attempt - 1)
	return b + time.Duration(rand.Int64N(int64(b)/2+1))
}

// retryWait says whether a failed attempt is retried, and after how long;
// safe marks a request that may run twice (a read).
func retryWait(safe bool, resp *http.Response, err error, attempt int) (time.Duration, bool) {
	backoff := backoffAfter(attempt)
	var ge *Error
	switch {
	case errors.As(err, &ge) && ge.StatusCode == http.StatusTooManyRequests:
		if wait, ok := rateLimitWait(resp.Header, time.Now()); ok {
			return wait, wait <= maxRetryWait
		}
		return backoff, true
	case !safe:
		return 0, false
	case ge != nil:
		return backoff, ge.StatusCode >= 500
	default:
		return backoff, IsTransient(err)
	}
}

// rateLimitWait reads how long GitLab asks a rate-limited client to wait:
// Retry-After (seconds or a date), else RateLimit-Reset (a Unix time).
func rateLimitWait(h http.Header, now time.Time) (time.Duration, bool) {
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			return time.Duration(max(secs, 0)) * time.Second, true
		}
		if t, err := http.ParseTime(v); err == nil {
			return max(t.Sub(now), 0), true
		}
	}
	if v := strings.TrimSpace(h.Get("RateLimit-Reset")); v != "" {
		if unix, err := strconv.ParseInt(v, 10, 64); err == nil {
			return max(time.Unix(unix, 0).Sub(now), 0), true
		}
	}
	return 0, false
}
