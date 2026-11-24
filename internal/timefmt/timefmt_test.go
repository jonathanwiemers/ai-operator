package timefmt

import (
	"testing"
	"time"
)

func TestUTCMinute(t *testing.T) {
	berlin := time.FixedZone("CET", 3600)
	in := time.Date(2026, 11, 21, 15, 5, 59, 0, berlin)
	if got, want := UTCMinute(in), "2026-11-21 14:05 UTC"; got != want {
		t.Errorf("UTCMinute = %q, want %q", got, want)
	}
	// Past midnight in the input's zone, the day before in UTC.
	in = time.Date(2026, 11, 22, 0, 30, 0, 0, berlin)
	if got, want := UTCMinute(in), "2026-11-21 23:30 UTC"; got != want {
		t.Errorf("UTCMinute = %q, want %q", got, want)
	}
}
