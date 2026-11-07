package store

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// The embedded migrations parse: the legacy versions are exactly 1 to 9,
// every later one is a valid UTC timestamp, and no version is used twice. It
// needs no database, so a clash of two branches' migrations (rebased onto
// each other) fails without Postgres too.
func TestEmbeddedMigrations(t *testing.T) {
	ms, err := readMigrations(migrations)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]string{}
	var legacy []int64
	for _, m := range ms {
		if other, ok := seen[m.version]; ok {
			t.Errorf("migrations %s and %s share version %d", other, m.name, m.version)
		}
		seen[m.version] = m.name
		if m.version <= 9999 {
			legacy = append(legacy, m.version)
			continue
		}
		prefix, _, _ := strings.Cut(strings.TrimPrefix(m.name, "migrations/"), "_")
		if _, err := time.ParseInLocation(timestampLayout, prefix, time.UTC); err != nil || len(prefix) != len(timestampLayout) {
			t.Errorf("migration %s: version %d is not a UTC timestamp YYYYMMDDHHMMSS", m.name, m.version)
		}
	}
	if !slices.Equal(legacy, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9}) {
		t.Fatalf("legacy versions are %v, want exactly 1 to 9: new migrations are named $(date -u +%%Y%%m%%d%%H%%M%%S)_name.sql", legacy)
	}
}

// Legacy versions sort before timestamps, and both forms parse.
func TestReadMigrationsOrder(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/20261107143000_b.sql": {},
		"migrations/0002_b.sql":           {},
		"migrations/20251231235959_a.sql": {},
		"migrations/0001_a.sql":           {},
		"migrations/9999_last_legacy.sql": {},
	}
	ms, err := readMigrations(fsys)
	if err != nil {
		t.Fatal(err)
	}
	want := []migration{
		{1, "migrations/0001_a.sql"},
		{2, "migrations/0002_b.sql"},
		{9999, "migrations/9999_last_legacy.sql"},
		{20251231235959, "migrations/20251231235959_a.sql"},
		{20261107143000, "migrations/20261107143000_b.sql"},
	}
	if len(ms) != len(want) {
		t.Fatalf("migrations = %v, want %v", ms, want)
	}
	for i := range want {
		if ms[i] != want[i] {
			t.Errorf("migration %d = %v, want %v", i, ms[i], want[i])
		}
	}
}

// Duplicate and malformed versions fail, naming the file.
func TestReadMigrationsRejects(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []string
		want  string
	}{
		{"same legacy number", []string{"0010_a.sql", "0010_b.sql"}, "used twice"},
		{"same timestamp, other names", []string{"20261107143000_a.sql", "20261107143000_b.sql"}, "used twice"},
		{"5-digit prefix", []string{"00010_a.sql"}, "14-digit UTC time"},
		{"month 13", []string{"20261307143000_a.sql"}, "not a valid UTC time"},
		{"hour 24", []string{"20261107243000_a.sql"}, "not a valid UTC time"},
		{"legacy 0000", []string{"0000_a.sql"}, "0000"},
		{"no version", []string{"init.sql"}, "must start with a version"},
		{"sign", []string{"+001_a.sql"}, "must start with a version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{"migrations/0001_init.sql": {}}
			for _, f := range tc.files {
				fsys["migrations/"+f] = &fstest.MapFile{}
			}
			_, err := readMigrations(fsys)
			if err == nil {
				t.Fatalf("readMigrations(%v) = nil, want an error", tc.files)
			}
			last := "migrations/" + tc.files[len(tc.files)-1]
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), last) {
				t.Errorf("readMigrations(%v) = %q, want it to name %s and contain %q", tc.files, err, last, tc.want)
			}
		})
	}
}
