package ui

import (
	"sync"
	"time"
)

// rateLimiter allows each user per actions within a sliding window.
type rateLimiter struct {
	per    int
	window time.Duration

	mu   sync.Mutex
	seen map[string][]time.Time
}

func newRateLimiter(per int, window time.Duration) *rateLimiter {
	return &rateLimiter{per: per, window: window, seen: map[string][]time.Time{}}
}

// allow counts an action of user at now. Over the limit it counts nothing
// and returns how long until the next action is allowed.
func (l *rateLimiter) allow(user string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.seen[user][:0]
	for _, t := range l.seen[user] {
		if now.Sub(t) < l.window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.per {
		l.seen[user] = recent
		return false, l.window - now.Sub(recent[0])
	}
	l.seen[user] = append(recent, now)
	return true, 0
}
