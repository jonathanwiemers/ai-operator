package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

func TestPoolConfig(t *testing.T) {
	// The environment must not redirect the connection.
	t.Setenv("PGHOST", "elsewhere")
	t.Setenv("PGSSLMODE", "disable")
	t.Setenv("PGPASSWORD", "env")
	password := `p@ss:w/rd?x=1&y#z% '"\`
	c := store.Config{Host: "db.example", Database: "ai_operator", User: "agents.research_writer", Password: password}
	pc, err := c.PoolConfig()
	if err != nil {
		t.Fatal(err)
	}
	cc := pc.ConnConfig
	if cc.Host != "db.example" || cc.Port != 5432 || cc.Database != "ai_operator" ||
		cc.User != "agents.research_writer" || cc.Password != password {
		t.Errorf("config = %s:%d/%s user %q password %q", cc.Host, cc.Port, cc.Database, cc.User, cc.Password)
	}
	if cc.TLSConfig == nil || !cc.TLSConfig.InsecureSkipVerify || len(cc.Fallbacks) != 0 {
		t.Errorf("default sslMode: want require without fallback, got TLS %v, %d fallbacks", cc.TLSConfig, len(cc.Fallbacks))
	}

	c.Port, c.SSLMode = 6432, "verify-full"
	if pc, err = c.PoolConfig(); err != nil || pc.ConnConfig.Port != 6432 ||
		pc.ConnConfig.TLSConfig.InsecureSkipVerify || pc.ConnConfig.TLSConfig.ServerName != "db.example" {
		t.Errorf("verify-full: %v %+v", err, pc)
	}
	c.SSLMode = "verify-ca"
	if pc, err = c.PoolConfig(); err != nil || pc.ConnConfig.TLSConfig.VerifyPeerCertificate == nil {
		t.Errorf("verify-ca: %v", err)
	}
	c.SSLMode = "disable"
	if pc, err = c.PoolConfig(); err != nil || pc.ConnConfig.TLSConfig != nil {
		t.Errorf("disable: %v", err)
	}
	for _, bad := range []store.Config{
		{Host: "h", Database: "d", User: "u", SSLMode: "prefer"},
		{Host: "h", Database: "d", User: "u", Port: 70000},
		{Database: "d", User: "u"},
		{Host: "h", User: "u"},
		{Host: "h", Database: "d"},
	} {
		if _, err := bad.PoolConfig(); err == nil {
			t.Errorf("%+v: want an error", bad)
		}
	}
}

func TestMigrateEmptyAndConcurrent(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	exec(t, s.Pool(), "DROP TABLE IF EXISTS chat_messages, history_events, research_items, research_results, research_runs, schema_migrations CASCADE")
	exec(t, s.Pool(), "DROP FUNCTION IF EXISTS chat_notify")

	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Go(func() { errs[i] = s.Migrate(ctx, store.Grants{}) })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Migrate: %v", err)
		}
	}
	for _, table := range []string{"research_runs", "research_results", "research_items", "history_events", "chat_messages", "schema_migrations"} {
		var exists bool
		if err := s.Pool().QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil || !exists {
			t.Errorf("table %s missing (%v)", table, err)
		}
	}
	if n := count(t, s.Pool(), "schema_migrations"); n != 3 {
		t.Errorf("schema_migrations has %d rows, want 3", n)
	}

	// A second run is a no-op.
	if err := s.Migrate(ctx, store.Grants{}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s.Pool(), "schema_migrations"); n != 3 {
		t.Errorf("after a second Migrate schema_migrations has %d rows, want 3", n)
	}
}

func TestMigrateRefusesUnknownVersion(t *testing.T) {
	s := storetest.New(t)
	exec(t, s.Pool(), "INSERT INTO schema_migrations (version) VALUES (9999)")
	err := s.Migrate(t.Context(), store.Grants{})
	if err == nil || !strings.Contains(err.Error(), "9999") {
		t.Fatalf("Migrate = %v, want an error naming version 9999", err)
	}
}

func TestGrants(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	suffix := randomHex(t)
	readerRole, writerRole := "r_"+suffix, "agents.research_writer_"+suffix
	reader, writer := storetest.Role(t, readerRole), storetest.Role(t, writerRole)
	run := insertRun(t, s.Pool(), time.Now().Add(time.Hour))
	exec(t, s.Pool(), `INSERT INTO research_results (run_id, expires_at, title, summary, blocks)
		VALUES ($1, now(), 't', 's', '[]')`, run)

	// Without USAGE on the schema the table is not even visible.
	if _, err := reader.Pool().Exec(ctx, "SELECT * FROM research_runs"); err == nil {
		t.Error("reader could read before Migrate granted it")
	}
	if err := s.Migrate(ctx, store.Grants{Reader: readerRole, Writers: []string{"", writerRole}}); err != nil {
		t.Fatal(err)
	}
	// Grants are idempotent.
	if err := s.Migrate(ctx, store.Grants{Reader: readerRole, Writers: []string{writerRole}}); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"research_runs", "research_results", "research_items", "schema_migrations"} {
		exec(t, reader.Pool(), "SELECT * FROM "+table)
	}
	mustFail(t, reader.Pool(), "reader insert", `INSERT INTO research_items (run_id, url_hash, first_seen) VALUES ($1, '\x01', now())`, run)
	mustFail(t, reader.Pool(), "reader update", "UPDATE research_runs SET status = 'failed'")

	for _, table := range []string{"research_runs", "research_results", "research_items"} {
		exec(t, writer.Pool(), "SELECT * FROM "+table)
	}
	own := insertRun(t, writer.Pool(), time.Now().Add(time.Hour))
	exec(t, writer.Pool(), "UPDATE research_runs SET status = 'succeeded', finished_at = now() WHERE id = $1", own)
	exec(t, writer.Pool(), `INSERT INTO research_results (run_id, expires_at, title, summary, blocks)
		VALUES ($1, now(), 't', 's', '[]')`, own)
	exec(t, writer.Pool(), "UPDATE research_results SET title = 'u' WHERE run_id = $1", own)
	exec(t, writer.Pool(), `INSERT INTO research_items (run_id, url_hash, first_seen) VALUES ($1, '\x01', now())`, own)
	exec(t, writer.Pool(), "UPDATE research_items SET topic = 'ai' WHERE run_id = $1", own)
	mustFail(t, writer.Pool(), "writer delete", "DELETE FROM research_items")
	mustFail(t, writer.Pool(), "writer delete", "DELETE FROM research_runs")
	mustFail(t, writer.Pool(), "writer create", "CREATE TABLE intruder (x int)")

	// The writer may not touch the factory's history or chat.
	mustFail(t, writer.Pool(), "writer reads history", "SELECT * FROM history_events")
	mustFail(t, writer.Pool(), "writer writes history", `INSERT INTO history_events (key, at, namespace, project, kind, expires_at)
		VALUES ('k', now(), 'n', 'p', 'note', now())`)
	mustFail(t, writer.Pool(), "writer reads chat", "SELECT * FROM chat_messages")
	mustFail(t, writer.Pool(), "writer writes chat", `INSERT INTO chat_messages (namespace, project, task, role, body, expires_at)
		VALUES ('n', 'p', 't', 'user', 'hi', now())`)

	// A table of a later migration follows by default privileges for the
	// reader, without a grant in that run; writers get tables by name only.
	later := fstest.MapFS{"migrations/9999_later.sql": {Data: []byte("CREATE TABLE later (x int); INSERT INTO later VALUES (1);")}}
	files, err := fs.Glob(store.Migrations, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		data, err := fs.ReadFile(store.Migrations, f)
		if err != nil {
			t.Fatal(err)
		}
		later[f] = &fstest.MapFile{Data: data}
	}
	if err := s.MigrateFS(ctx, later, store.Grants{}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, reader.Pool(), "later"); n != 1 {
		t.Errorf("reader sees %d rows of the later table, want 1", n)
	}
	mustFail(t, reader.Pool(), "reader insert into later", "INSERT INTO later VALUES (2)")
	mustFail(t, writer.Pool(), "writer insert into later", "INSERT INTO later VALUES (2)")

	// This binary does not know migration 9999.
	if err := s.Migrate(ctx, store.Grants{}); err == nil || !strings.Contains(err.Error(), "9999") {
		t.Errorf("Migrate after a newer migration = %v, want an error naming 9999", err)
	}
}

// Writers of an earlier version had default privileges on every future
// table; the history table, created after them, must still be closed to them.
func TestGrantsRevokeHistoryFromOldWriters(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	role := "agents.research_writer_" + randomHex(t)
	writer := storetest.Role(t, role)
	id := pgx.Identifier{role}.Sanitize()
	exec(t, s.Pool(), "GRANT USAGE ON SCHEMA "+pgx.Identifier{schemaOf(t, s)}.Sanitize()+" TO "+id)
	exec(t, s.Pool(), "ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER GRANT SELECT, INSERT, UPDATE ON TABLES TO "+id)
	exec(t, s.Pool(), "DROP TABLE history_events")
	exec(t, s.Pool(), "DELETE FROM schema_migrations WHERE version = 2")
	if err := s.Migrate(ctx, store.Grants{Writers: []string{role}}); err != nil {
		t.Fatal(err)
	}
	mustFail(t, writer.Pool(), "old writer writes history", `INSERT INTO history_events (key, at, namespace, project, kind, expires_at)
		VALUES ('k', now(), 'n', 'p', 'note', now())`)
	exec(t, writer.Pool(), "SELECT * FROM research_runs")
}

func schemaOf(t *testing.T, s *store.Store) string {
	t.Helper()
	var schema string
	if err := s.Pool().QueryRow(t.Context(), "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestGrantUnknownRole(t *testing.T) {
	s := storetest.New(t)
	role := "agents.nobody_" + randomHex(t)
	err := s.Migrate(t.Context(), store.Grants{Writers: []string{role}})
	if err == nil || !strings.Contains(err.Error(), role) {
		t.Fatalf("Migrate = %v, want an error naming %s", err, role)
	}
}

func TestDeleteExpired(t *testing.T) {
	s := storetest.New(t)
	now := time.Now()
	keep := insertRun(t, s.Pool(), now.Add(time.Minute))
	expired := insertRun(t, s.Pool(), now.Add(-time.Minute))
	for _, run := range []pgtype.UUID{keep, expired} {
		exec(t, s.Pool(), `INSERT INTO research_results (run_id, expires_at, title, summary, blocks)
			VALUES ($1, now(), 't', 's', '[]')`, run)
		exec(t, s.Pool(), `INSERT INTO research_items (run_id, url_hash, first_seen)
			VALUES ($1, '\x01', now()), ($1, '\x02', now())`, run)
	}
	// More than a batch of expired runs without results.
	exec(t, s.Pool(), `INSERT INTO research_runs (id, namespace, research, started_at, status, expires_at)
		SELECT gen_random_uuid(), 'agents', 'news', now(), 'failed', $1 FROM generate_series(1, 1500)`, now.Add(-time.Hour))

	n, err := s.DeleteExpired(t.Context(), now)
	if err != nil || n != 1501 {
		t.Fatalf("DeleteExpired = %d, %v, want 1501", n, err)
	}
	var runs, results, items int
	if err := s.Pool().QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM research_runs WHERE id = $1),
		(SELECT count(*) FROM research_results WHERE run_id = $1),
		(SELECT count(*) FROM research_items WHERE run_id = $1)`, keep).Scan(&runs, &results, &items); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || results != 1 || items != 2 {
		t.Errorf("kept run has %d runs, %d results, %d items, want 1, 1, 2", runs, results, items)
	}
	if c := count(t, s.Pool(), "research_runs"); c != 1 {
		t.Errorf("%d runs left, want 1", c)
	}
	if c := count(t, s.Pool(), "research_results"); c != 1 {
		t.Errorf("%d results left, want 1", c)
	}
	if c := count(t, s.Pool(), "research_items"); c != 2 {
		t.Errorf("%d items left, want 2", c)
	}
}

func TestSearch(t *testing.T) {
	s := storetest.New(t)
	run := insertRun(t, s.Pool(), time.Now().Add(time.Hour))
	exec(t, s.Pool(), `INSERT INTO research_results (run_id, expires_at, title, summary, blocks)
		VALUES ($1, now(), 'Morning news', 'Topics of the day', $2)`, run,
		`[{"type":"heading","text":"Tech"},{"type":"items","items":[{"title":"Launch","summary":"A zeppelin returns"}]}]`)
	for query, want := range map[string]int{"zeppelin": 1, "morning": 1, "topics day": 1, "zeppelin -launch": 0, "submarine": 0} {
		var n int
		if err := s.Pool().QueryRow(t.Context(),
			"SELECT count(*) FROM research_results WHERE search @@ websearch_to_tsquery('simple', $1)", query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("search %q found %d results, want %d", query, n, want)
		}
	}
}

func insertRun(t *testing.T, pool *pgxpool.Pool, expires time.Time) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO research_runs (id, namespace, research, started_at, status, expires_at)
		VALUES (gen_random_uuid(), 'agents', 'news', now(), 'running', $1) RETURNING id`, expires).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func mustFail(t *testing.T, pool *pgxpool.Pool, what, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), sql, args...); err == nil {
		t.Errorf("%s: %s succeeded, want permission denied", what, sql)
	} else if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("%s: %s: %v, want permission denied", what, sql, err)
	}
}

func count(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func randomHex(t *testing.T) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
