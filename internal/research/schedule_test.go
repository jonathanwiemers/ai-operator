package research

import (
	"strings"
	"testing"
	"time"
)

func TestParseSchedule_errors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, spec, tz, wantSub string
	}{
		{"bad cron", "not a cron", "", "spec.schedule:"},
		{"too many fields", "0 7 * * * *", "", "spec.schedule:"},
		{"unknown zone", "0 7 * * *", "Not/AZone", `spec.timeZone: unknown time zone "Not/AZone"`},
		{"CRON_TZ prefix", "CRON_TZ=Europe/Berlin 0 7 * * *", "", "spec.schedule: CRON_TZ=/TZ="},
		{"TZ prefix", "TZ=UTC 0 7 * * *", "Europe/Berlin", "spec.schedule: CRON_TZ=/TZ="},
		{"cron_tz lower", "cron_tz=UTC 0 * * * *", "", "spec.schedule: CRON_TZ=/TZ="},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseSchedule(tc.spec, tc.tz)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q, want substring %q", err, tc.wantSub)
			}
		})
	}
}

func TestParseSchedule_emptyZoneIsUTC(t *testing.T) {
	t.Parallel()
	s, err := ParseSchedule("0 7 * * *", "")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	got := s.Next(after)
	want := time.Date(2026, 6, 1, 7, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("Next = %v, want %v", got, want)
	}
}

func TestSchedule_Next_descriptors(t *testing.T) {
	t.Parallel()
	s, err := ParseSchedule("@hourly", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 1, 1, 10, 30, 0, 0, time.UTC)
	got := s.Next(after)
	want := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("Next = %v, want %v", got, want)
	}
}

func TestSchedule_Next_EuropeBerlin_DST(t *testing.T) {
	t.Parallel()
	s, err := ParseSchedule("0 7 * * *", "Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	// Europe/Berlin: CEST (UTC+2) until 2026-10-25 03:00 local, then CET (UTC+1);
	// CET until 2027-03-28 02:00 local, then CEST.
	cases := []struct {
		name  string
		after time.Time
		want  time.Time
	}{
		{
			"before autumn fall-back (CEST)",
			time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 10, 24, 5, 0, 0, 0, time.UTC), // 07:00 CEST
		},
		{
			"day of autumn fall-back (CET)",
			time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 10, 25, 6, 0, 0, 0, time.UTC), // 07:00 CET
		},
		{
			"after autumn fall-back (CET)",
			time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 10, 26, 6, 0, 0, 0, time.UTC), // 07:00 CET
		},
		{
			"day of spring forward (CEST)",
			time.Date(2027, 3, 28, 0, 0, 0, 0, time.UTC),
			time.Date(2027, 3, 28, 5, 0, 0, 0, time.UTC), // 07:00 CEST
		},
		{
			"after spring forward (CEST)",
			time.Date(2027, 3, 29, 0, 0, 0, 0, time.UTC),
			time.Date(2027, 3, 29, 5, 0, 0, 0, time.UTC), // 07:00 CEST
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := s.Next(tc.after)
			if !got.Equal(tc.want) {
				t.Fatalf("Next(%v) = %v, want %v", tc.after, got, tc.want)
			}
			if got.Location() != time.UTC {
				t.Fatalf("location %v, want UTC", got.Location())
			}
		})
	}
}

func TestNextRun_catchUpOnce(t *testing.T) {
	t.Parallel()
	s, err := ParseSchedule("0 * * * *", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	// last at 10:00; now 13:30 → missed 11:00, 12:00, 13:00 → one due; next 14:00
	last := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	now := time.Date(2026, 1, 1, 13, 30, 0, 0, time.UTC)
	due, next := NextRun(s, last, now)
	if !due {
		t.Fatal("due = false, want true after three missed hours")
	}
	wantNext := time.Date(2026, 1, 1, 14, 0, 0, 0, time.UTC)
	if !next.Equal(wantNext) {
		t.Fatalf("next = %v, want %v", next, wantNext)
	}
}

func TestNextRun_notDue(t *testing.T) {
	t.Parallel()
	s, err := ParseSchedule("0 7 * * *", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	last := time.Date(2026, 6, 1, 7, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	due, next := NextRun(s, last, now)
	if due {
		t.Fatal("due = true, want false")
	}
	wantNext := time.Date(2026, 6, 2, 7, 0, 0, 0, time.UTC)
	if !next.Equal(wantNext) {
		t.Fatalf("next = %v, want %v", next, wantNext)
	}
}

func TestNextRun_dueAtExactFire(t *testing.T) {
	t.Parallel()
	s, err := ParseSchedule("0 7 * * *", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	last := time.Date(2026, 6, 1, 7, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 2, 7, 0, 0, 0, time.UTC)
	due, next := NextRun(s, last, now)
	if !due {
		t.Fatal("due = false, want true at the fire time")
	}
	wantNext := time.Date(2026, 6, 3, 7, 0, 0, 0, time.UTC)
	if !next.Equal(wantNext) {
		t.Fatalf("next = %v, want %v", next, wantNext)
	}
}
