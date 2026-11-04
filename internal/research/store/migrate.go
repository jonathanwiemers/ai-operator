package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrateLock is the pg_advisory_lock key that serializes migrations and
// grants of concurrent operator starts.
const migrateLock int64 = 0x61692d726573 // "ai-res"

// Grants are the logins Migrate grants rights to. Grants are additive: a
// role removed from the lists keeps its rights until revoked by hand.
type Grants struct {
	// Reader may read every table (the UI).
	Reader string
	// Writers may read, insert and update runs, results and items (the
	// runners of research runs), and nothing else.
	Writers []string
	// ChatWriter may select and insert user rows on chat_messages (the
	// UI's chat login); RLS rejects non-user inserts.
	ChatWriter string
}

type migration struct {
	version int
	name    string
}

// Migrate applies the embedded migrations in order, each in its own
// transaction, and then grants g on every call, so changed role lists apply.
// It refuses a database that has a migration this binary does not know.
func (s *Store) Migrate(ctx context.Context, g Grants) error {
	return s.migrate(ctx, migrations, g)
}

func (s *Store) migrate(ctx context.Context, fsys fs.FS, g Grants) error {
	known, err := readMigrations(fsys)
	if err != nil {
		return err
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrateLock); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	// A lock left behind by a failed unlock ends with the connection.
	defer func() {
		if _, err := conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrateLock); err != nil {
			conn.Conn().Close(context.WithoutCancel(ctx))
		}
	}()
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version int PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	rows, _ := conn.Query(ctx, "SELECT version FROM schema_migrations")
	applied, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return err
	}
	for _, v := range applied {
		if !slices.ContainsFunc(known, func(m migration) bool { return m.version == v }) {
			return fmt.Errorf("research database has migration %d, which this ai-operator does not know (downgrade?)", v)
		}
	}
	for _, m := range known {
		if slices.Contains(applied, m.version) {
			continue
		}
		sql, err := fs.ReadFile(fsys, m.name)
		if err != nil {
			return err
		}
		if err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", m.version)
			return err
		}); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
	}
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error { return grant(ctx, tx, g) })
}

// readMigrations returns migrations/NNNN_name.sql ordered by version.
func readMigrations(fsys fs.FS) ([]migration, error) {
	names, err := fs.Glob(fsys, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, name := range names {
		prefix, _, _ := strings.Cut(path.Base(name), "_")
		v, err := strconv.Atoi(prefix)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("migration %s: name must start with a version, e.g. 0001_", name)
		}
		if slices.ContainsFunc(ms, func(m migration) bool { return m.version == v }) {
			return nil, fmt.Errorf("migration %s: version %d is used twice", name, v)
		}
		ms = append(ms, migration{version: v, name: name})
	}
	slices.SortFunc(ms, func(a, b migration) int { return a.version - b.version })
	return ms, nil
}

// grant gives the reader and the writers their rights in the current schema,
// now and, by default privileges, on tables the owner creates later. Role
// names are quoted identifiers ("agents.research_writer" has a dot).
func grant(ctx context.Context, tx pgx.Tx, g Grants) error {
	var schema string
	if err := tx.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		return fmt.Errorf("current schema: %w", err)
	}
	sc := pgx.Identifier{schema}.Sanitize()
	tables := strings.Join([]string{sc + ".research_runs", sc + ".research_results", sc + ".research_items"}, ", ")
	defaults := "ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA " + sc + " GRANT "
	// to runs each statement, completed by "TO <role>", for a role.
	to := func(role string, stmts ...string) error {
		if role == "" {
			return nil
		}
		id := pgx.Identifier{role}.Sanitize()
		for _, stmt := range append([]string{"GRANT USAGE ON SCHEMA " + sc}, stmts...) {
			if _, err := tx.Exec(ctx, stmt+" TO "+id); err != nil {
				return fmt.Errorf("grant to role %q: %w", role, err)
			}
		}
		return nil
	}
	if err := to(g.Reader, "GRANT SELECT ON ALL TABLES IN SCHEMA "+sc, defaults+"SELECT ON TABLES"); err != nil {
		return err
	}
	if err := to(g.ChatWriter,
		"GRANT SELECT, INSERT ON "+sc+".chat_messages",
		"GRANT USAGE ON SEQUENCE "+sc+".chat_messages_id_seq"); err != nil {
		return err
	}
	// Writers get the research tables by name and nothing else: earlier
	// versions granted them every future table by default privileges, which
	// would have let research pods write the factory's history, chat or
	// agent calls.
	revokeDefaults := "ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA " + sc + " REVOKE SELECT, INSERT, UPDATE ON TABLES FROM "
	for _, w := range g.Writers {
		if w == "" {
			continue
		}
		if err := to(w, "GRANT SELECT, INSERT, UPDATE ON "+tables); err != nil {
			return err
		}
		id := pgx.Identifier{w}.Sanitize()
		for _, stmt := range []string{
			revokeDefaults + id,
			"REVOKE ALL ON " + sc + ".history_events FROM " + id,
			"REVOKE ALL ON " + sc + ".chat_messages FROM " + id,
			"REVOKE ALL ON " + sc + ".agent_calls FROM " + id,
		} {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("revoke from role %q: %w", w, err)
			}
		}
	}
	return nil
}
