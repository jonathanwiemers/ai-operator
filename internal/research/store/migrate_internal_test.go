package store

import "testing"

// The embedded migrations parse and number 1 to n without gaps or a version
// used twice. It needs no database, so a clash of two branches' migrations
// (rebased onto each other) fails without Postgres too.
func TestEmbeddedMigrations(t *testing.T) {
	ms, err := readMigrations(migrations)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range ms {
		if m.version != i+1 {
			t.Fatalf("migration %s has version %d, want %d (no gaps)", m.name, m.version, i+1)
		}
	}
}
