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
		if sleepCtx(ctx, wait) != nil {
			return err
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
	return b + time.Duration(rand.Int64N(int64(b)/2+1)) // #nosec G404 -- the jitter only spreads retries out, it needs no unpredictability
}

// A rate-limited request (429) did not run, so it is retried whatever its
// method: at the reset GitLab names, or else at the next full minute plus
// 1–5 s of jitter, max429Attempts times at most and with no more than
// max429Total of waiting in one call.
const (
	max429Attempts = 5
	max429Total    = 2 * time.Minute
)

// retryState counts the failed attempts of one call.
type retryState struct {
	failed, limited int
	waited          time.Duration
}

// next says whether a failed attempt is retried, and after how long; safe
// marks a request that may run twice (a read). Server errors and broken
// connections are retried for reads only, maxAttempts times at most.
func (s *retryState) next(safe bool, err error, now time.Time) (time.Duration, bool) {
	var ge *Error
	if errors.As(err, &ge) && ge.StatusCode == http.StatusTooManyRequests {
		s.limited++
		wait := max(ge.Reset.Sub(now), 0)
		if ge.Reset.IsZero() {
			wait = nextMinute(now).Sub(now) + time.Second + time.Duration(rand.Int64N(int64(4*time.Second)+1)) // #nosec G404 -- the jitter only spreads retries out, it needs no unpredictability
		}
		if s.limited >= max429Attempts || s.waited+wait > max429Total {
			return 0, false
		}
		s.waited += wait
		return wait, true
	}
	s.failed++
	if !safe || s.failed >= maxAttempts {
		return 0, false
	}
	if ge != nil {
		return backoffAfter(s.failed), ge.StatusCode >= 500
	}
	return backoffAfter(s.failed), IsTransient(err)
}

func nextMinute(t time.Time) time.Time { return t.Truncate(time.Minute).Add(time.Minute) }

// limitSleep waits out a 429, or until ctx is done; tests replace it.
var limitSleep = sleepCtx

// SkipRateLimitWaits makes the client's waits for GitLab's rate limit (429
// retries and pacing) return at once, for the tests of other packages, and
// returns a function that restores them.
func SkipRateLimitWaits() (restore func()) {
	prevLimit, prevPace := limitSleep, paceSleep
	skip := func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	limitSleep, paceSleep = skip, skip
	return func() { limitSleep, paceSleep = prevLimit, prevPace }
}

// sleepCtx waits d or until ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// RateLimitReset returns the latest reset GitLab named in a 429 found in err,
// through wrapped and joined errors; false when there is none.
func RateLimitReset(err error) (time.Time, bool) {
	var latest time.Time
	switch e := err.(type) {
	case *Error:
		if e.StatusCode == http.StatusTooManyRequests {
			latest = e.Reset
		}
	case interface{ Unwrap() []error }:
		for _, x := range e.Unwrap() {
			if t, ok := RateLimitReset(x); ok && t.After(latest) {
				latest = t
			}
		}
	case interface{ Unwrap() error }:
		latest, _ = RateLimitReset(e.Unwrap())
	}
	return latest, !latest.IsZero()
}

// rateLimitReset reads when GitLab lets a rate-limited client try again:
// Retry-After (seconds or a date), else RateLimit-Reset (a Unix time).
func rateLimitReset(h http.Header, now time.Time) (time.Time, bool) {
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			return now.Add(time.Duration(max(secs, 0)) * time.Second), true
		}
		if t, err := http.ParseTime(v); err == nil {
			return t, true
		}
	}
	if v := strings.TrimSpace(h.Get("RateLimit-Reset")); v != "" {
		if unix, err := strconv.ParseInt(v, 10, 64); err == nil {
			return time.Unix(unix, 0), true
		}
	}
	return time.Time{}, false
}
