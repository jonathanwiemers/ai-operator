package gitlab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Pacing. Every process that calls GitLab with a token shares that token's
// per-user bucket with the others, so each one reads the RateLimit headers of
// every response, keeps the latest reading per token, and paces from it: at
// or below the reserve, background reads wait for the reset; at 0, every call
// does. Work calls use the reserve. Without headers nothing waits.

// Priority says whether a call may wait for the rate limit's reset to leave
// the reserve to others.
type Priority int

const (
	// Work is the default: runs, pushes, merges, labels, notes, Desk writes.
	Work Priority = iota
	// Background is for polls, heartbeats, discovery and Desk reads. Only
	// reads honour it; a write is always Work.
	Background
)

func (p Priority) String() string {
	if p == Background {
		return "background"
	}
	return "work"
}

type priorityKey struct{}

// WithPriority returns ctx with the priority of the GitLab calls made with it.
func WithPriority(ctx context.Context, p Priority) context.Context {
	return context.WithValue(ctx, priorityKey{}, p)
}

// PriorityOf returns the priority set with WithPriority, Work by default.
func PriorityOf(ctx context.Context) Priority { return priorityOf(ctx) }

func priorityOf(ctx context.Context) Priority {
	p, _ := ctx.Value(priorityKey{}).(Priority)
	return p
}

// maxPaceWait bounds a wait for the reset, so a wrong header cannot stall a
// call for longer than GitLab's one-minute window.
const maxPaceWait = 65 * time.Second

var rateLimitReserve atomic.Int64

func init() { rateLimitReserve.Store(20) }

// SetRateLimitReserve sets how many requests of the bucket background reads
// leave to work calls (default 20).
func SetRateLimitReserve(n int) { rateLimitReserve.Store(int64(max(n, 0))) }

// RateLimitReserve is the reserve set by SetRateLimitReserve.
func RateLimitReserve() int { return int(rateLimitReserve.Load()) }

// RateLimitReading is the latest RateLimit header reading of a token, with the
// requests sent since counted off Remaining.
type RateLimitReading struct {
	Limit, Remaining, Observed int
	ResetAt, ReadAt            time.Time
}

// tokenLimit is the shared reading of one token at one GitLab.
type tokenLimit struct {
	mu    sync.Mutex
	r     RateLimitReading
	known bool
}

// limits holds a *tokenLimit per base URL and token hash, for every Client of
// the process.
var limits sync.Map

func limitFor(baseURL, token string) *tokenLimit {
	sum := sha256.Sum256([]byte(token))
	key := baseURL + " " + hex.EncodeToString(sum[:])
	if v, ok := limits.Load(key); ok {
		return v.(*tokenLimit)
	}
	v, _ := limits.LoadOrStore(key, &tokenLimit{})
	return v.(*tokenLimit)
}

// observe takes the reading of one response; one without RateLimit-Remaining
// changes nothing.
func (l *tokenLimit) observe(h http.Header, now time.Time) {
	remaining, ok := headerInt(h, "RateLimit-Remaining")
	if !ok {
		return
	}
	r := RateLimitReading{Remaining: remaining, ReadAt: now}
	r.Limit, _ = headerInt(h, "RateLimit-Limit")
	r.Observed, _ = headerInt(h, "RateLimit-Observed")
	if reset, ok := headerInt(h, "RateLimit-Reset"); ok {
		r.ResetAt = time.Unix(int64(reset), 0)
	}
	l.mu.Lock()
	l.r, l.known = r, true
	l.mu.Unlock()
}

func headerInt(h http.Header, name string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(h.Get(name)))
	return n, err == nil
}

// sent counts one request off the reading until the next response says more.
func (l *tokenLimit) sent() {
	l.mu.Lock()
	if l.known {
		l.r.Remaining--
	}
	l.mu.Unlock()
}

// limited takes a 429: nothing is left until reset (or a later reset already
// known), so the process's other calls wait for it too.
func (l *tokenLimit) limited(reset, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.known || !now.Before(l.r.ResetAt) || reset.After(l.r.ResetAt) {
		l.r.ResetAt = reset
	}
	l.r.Remaining, l.r.ReadAt, l.known = 0, now, true
}

// wait is how long a call of priority p waits before it is sent.
func (l *tokenLimit) wait(p Priority, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.known || !now.Before(l.r.ResetAt) {
		return 0
	}
	if l.r.Remaining <= 0 || (p == Background && int64(l.r.Remaining) <= rateLimitReserve.Load()) {
		return min(l.r.ResetAt.Sub(now), maxPaceWait)
	}
	return 0
}

// RateLimit returns the latest reading of the client's token, false before
// any response carried one.
func (c *Client) RateLimit() (RateLimitReading, bool) {
	if c.limit == nil {
		return RateLimitReading{}, false
	}
	c.limit.mu.Lock()
	defer c.limit.mu.Unlock()
	return c.limit.r, c.limit.known
}

// pacedNanos counts the time calls waited for the reset, per priority.
var pacedNanos [2]atomic.Int64

// PacedSeconds returns the seconds calls of priority p waited for the rate
// limit's reset in this process.
func PacedSeconds(p Priority) float64 {
	return time.Duration(pacedNanos[p].Load()).Seconds()
}

// PacedError is returned for a call that would have to wait for the rate
// limit's reset past its context's deadline; it was not sent. It is
// transient.
type PacedError struct {
	Method, Path string
	Priority     Priority
	Wait         time.Duration
}

func (e *PacedError) Error() string {
	return fmt.Sprintf("gitlab: %s %s: not sent, a %s call waits %s for the rate limit's reset, past the deadline", e.Method, e.Path, e.Priority, e.Wait.Round(time.Second))
}

// paceSleep waits d or until ctx is done; tests replace it.
var paceSleep = sleepCtx

// pace waits as the token's reading says before r is sent, and counts the
// request off it. Only reads honour a Background context.
func (c *Client) pace(ctx context.Context, r request) error {
	if c.limit == nil {
		return nil
	}
	p := Work
	if r.safe {
		p = priorityOf(ctx)
	}
	if wait := c.limit.wait(p, time.Now()); wait > 0 {
		if d, has := ctx.Deadline(); has && time.Until(d) < wait {
			return &PacedError{Method: r.method, Path: r.path, Priority: p, Wait: wait}
		}
		start := time.Now()
		if err := paceSleep(ctx, wait); err != nil {
			pacedNanos[p].Add(int64(time.Since(start)))
			return err
		}
		pacedNanos[p].Add(int64(wait))
	}
	c.limit.sent()
	return nil
}
