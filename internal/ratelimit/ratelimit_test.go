package ratelimit

import (
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	l := NewRateLimiter(2, time.Hour)
	t0 := time.Date(2026, 10, 29, 7, 0, 0, 0, time.UTC)
	for _, at := range []time.Duration{0, 10 * time.Minute} {
		if ok, _ := l.Allow("ada", t0.Add(at)); !ok {
			t.Fatalf("at %v refused", at)
		}
	}
	if ok, wait := l.Allow("ada", t0.Add(30*time.Minute)); ok || wait != 30*time.Minute {
		t.Errorf("third: %v, wait %v", ok, wait)
	}
	if ok, _ := l.Allow("bob", t0.Add(30*time.Minute)); !ok {
		t.Error("bob has his own limit")
	}
	if ok, _ := l.Allow("ada", t0.Add(time.Hour)); !ok {
		t.Error("the first left the window")
	}
}
