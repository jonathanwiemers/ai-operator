// Package ratelimit owns the per-user sliding-window limiter the Desk puts
// in front of comments, chats and task and plan creations.
package ratelimit

import (
	"sync"
	"time"
)

// RateLimiter allows each user per actions within a sliding window.
type RateLimiter struct {
	per    int
	window time.Duration

	mu   sync.Mutex
	seen map[string][]time.Time
}

func NewRateLimiter(per int, window time.Duration) *RateLimiter {
	return &RateLimiter{per: per, window: window, seen: map[string][]time.Time{}}
}

// Allow counts an action of user at now. Over the limit it counts nothing
// and returns how long until the next action is allowed.
func (l *RateLimiter) Allow(user string, now time.Time) (bool, time.Duration) {
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
