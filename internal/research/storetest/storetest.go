// Package storetest gives tests a research store in a fresh schema of the
// Postgres at AI_TEST_DATABASE_URL (a URI, for tests only), e.g. the one of
// make test-db. Without the variable tests are skipped, unless
// AI_TEST_REQUIRE_DATABASE=1 (CI), then they fail.
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// testDB is what Role needs of the store New made for a test.
type testDB struct {
	url    string
	schema string
	admin  *pgxpool.Pool
}

var (
	mu  sync.Mutex
	dbs = map[testing.TB]testDB{}
)

// New returns a store connected as the URL's user (the owner) to a new,
// migrated schema without grants. The schema is dropped on cleanup.
func New(t testing.TB) *store.Store {
	t.Helper()
	url := os.Getenv("AI_TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("AI_TEST_REQUIRE_DATABASE") == "1" {
			t.Fatal("AI_TEST_DATABASE_URL is not set, but AI_TEST_REQUIRE_DATABASE=1")
		}
		t.Skip("AI_TEST_DATABASE_URL is not set (make test-db starts a Postgres)")
	}
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	// A CI service container may still be starting.
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(time.Second) {
		if err = admin.Ping(ctx); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("test database: %v", err)
		}
	}
	db := testDB{url: url, schema: "t_" + random(t, 8), admin: admin}
	exec(t, admin, "CREATE SCHEMA "+pgx.Identifier{db.schema}.Sanitize())
	t.Cleanup(func() { exec(t, admin, "DROP SCHEMA "+pgx.Identifier{db.schema}.Sanitize()+" CASCADE") })
	pc, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	s := open(t, db, pc)
	if err := s.Migrate(ctx, store.Grants{}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	dbs[t] = db
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		delete(dbs, t)
		mu.Unlock()
	})
	return s
}

// Role creates the login role name with a random password that needs
// quoting in a URI, and returns a store connected as it, through
// store.Config, to the schema of New. The role is dropped on cleanup.
// Roles are global to the server: tests pick unique names.
func Role(t testing.TB, name string) *store.Store {
	t.Helper()
	mu.Lock()
	db, ok := dbs[t]
	mu.Unlock()
	if !ok {
		t.Fatal("storetest.Role needs storetest.New first")
	}
	id := pgx.Identifier{name}.Sanitize()
	password := "p@:/?#" + random(t, 16)
	exec(t, db.admin, "CREATE ROLE "+id+" LOGIN PASSWORD '"+password+"'")
	t.Cleanup(func() {
		exec(t, db.admin, "DROP OWNED BY "+id)
		exec(t, db.admin, "DROP ROLE "+id)
	})
	u, err := pgx.ParseConfig(db.url)
	if err != nil {
		t.Fatal(err)
	}
	mode := "disable"
	if u.TLSConfig != nil {
		mode = "require"
	}
	c := store.Config{Host: u.Host, Port: int(u.Port), Database: u.Database, SSLMode: mode, User: name, Password: password}
	pc, err := c.PoolConfig()
	if err != nil {
		t.Fatal(err)
	}
	return open(t, db, pc)
}

// open connects with pc to the test's schema; closed on cleanup.
func open(t testing.TB, db testDB, pc *pgxpool.Config) *store.Store {
	t.Helper()
	pc.ConnConfig.RuntimeParams["search_path"] = db.schema
	s, err := store.OpenPool(t.Context(), pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func exec(t testing.TB, pool *pgxpool.Pool, sql string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func random(t testing.TB, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// Count returns the rows of run id in table (research_results or
// research_items) of the schema of New.
func Count(t testing.TB, table, id string) int {
	t.Helper()
	mu.Lock()
	db, found := dbs[t]
	mu.Unlock()
	if !found {
		t.Fatal("storetest.Count needs storetest.New first")
	}
	var n int
	if err := db.admin.QueryRow(t.Context(), "SELECT count(*) FROM "+pgx.Identifier{db.schema, table}.Sanitize()+
		" WHERE run_id = $1", id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// RunStatus returns the status and error of run id in the schema of New, ok
// false if there is no such run.
func RunStatus(t testing.TB, id string) (status, errMsg string, ok bool) {
	t.Helper()
	mu.Lock()
	db, found := dbs[t]
	mu.Unlock()
	if !found {
		t.Fatal("storetest.RunStatus needs storetest.New first")
	}
	err := db.admin.QueryRow(t.Context(), "SELECT status, error FROM "+pgx.Identifier{db.schema, "research_runs"}.Sanitize()+
		" WHERE id = $1", id).Scan(&status, &errMsg)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return status, errMsg, true
}
