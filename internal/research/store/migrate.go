package store

import (
	"cmp"
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

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

// A migration's version is the prefix of its file name: a legacy version
// 0001 to 9999 (the migrations up to 0009) or, for every new migration, the
// UTC time it was created as YYYYMMDDHHMMSS (date -u +%Y%m%d%H%M%S). Two
// branches then only pick the same version when they create a migration in
// the same second, so a rebase never has to renumber one.
type migration struct {
	version int64
	name    string
}

// timestampLayout is the time layout of a timestamp version.
const timestampLayout = "20060102150405"

// Migrate applies the embedded migrations in order of their versions (every
// legacy version before every timestamp), each in its own transaction, and
// then grants g on every call, so changed role lists apply. A migration that
// is not applied yet is applied even when a later version already is: the
// migrations of two branches can land on main in either order. It refuses a
// database that has a migration this binary does not know.
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
		version bigint PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	if err := widenVersion(ctx, conn.Conn()); err != nil {
		return err
	}
	rows, _ := conn.Query(ctx, "SELECT version FROM schema_migrations")
	applied, err := pgx.CollectRows(rows, pgx.RowTo[int64])
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

// widenVersion makes schema_migrations.version a bigint, which a timestamp
// version needs: databases from before timestamp versions created it as
// int. It runs under the migration lock and alters only an int column.
func widenVersion(ctx context.Context, conn *pgx.Conn) error {
	var typ string
	if err := conn.QueryRow(ctx, `SELECT data_type FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'schema_migrations' AND column_name = 'version'`).Scan(&typ); err != nil {
		return fmt.Errorf("type of schema_migrations.version: %w", err)
	}
	if typ != "integer" {
		return nil
	}
	if _, err := conn.Exec(ctx, "ALTER TABLE schema_migrations ALTER COLUMN version TYPE bigint"); err != nil {
		return fmt.Errorf("widen schema_migrations.version to bigint: %w", err)
	}
	return nil
}

// readMigrations returns migrations/<version>_name.sql ordered by version.
func readMigrations(fsys fs.FS) ([]migration, error) {
	names, err := fs.Glob(fsys, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, name := range names {
		v, err := parseVersion(path.Base(name))
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w; name new migrations by the UTC time, $(date -u +%%Y%%m%%d%%H%%M%%S)_name.sql", name, err)
		}
		if i := slices.IndexFunc(ms, func(m migration) bool { return m.version == v }); i >= 0 {
			return nil, fmt.Errorf("migration %s: version %d is used twice (also by %s)", name, v, ms[i].name)
		}
		ms = append(ms, migration{version: v, name: name})
	}
	slices.SortFunc(ms, func(a, b migration) int { return cmp.Compare(a.version, b.version) })
	return ms, nil
}

// parseVersion returns the version of a migration file name: a legacy
// version (4 digits, 0001 to 9999) or a timestamp version (14 digits, a
// valid UTC time YYYYMMDDHHMMSS), followed by "_".
func parseVersion(base string) (int64, error) {
	prefix, _, found := strings.Cut(base, "_")
	if !found || prefix == "" || strings.ContainsFunc(prefix, func(r rune) bool { return r < '0' || r > '9' }) {
		return 0, fmt.Errorf("name must start with a version and _, e.g. 20261107143000_name.sql")
	}
	switch len(prefix) {
	case 4:
		v, _ := strconv.ParseInt(prefix, 10, 64)
		if v == 0 {
			return 0, fmt.Errorf("legacy version 0000 is not valid")
		}
		return v, nil
	case len(timestampLayout):
		if _, err := time.ParseInLocation(timestampLayout, prefix, time.UTC); err != nil {
			return 0, fmt.Errorf("version %s is not a valid UTC time YYYYMMDDHHMMSS", prefix)
		}
		return strconv.ParseInt(prefix, 10, 64)
	default:
		return 0, fmt.Errorf("version %s must be a legacy 4-digit version or a 14-digit UTC time YYYYMMDDHHMMSS", prefix)
	}
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
	// would have let research pods write the factory's history, chat,
	// agent calls or the Desk's milestone snapshot and its backfill.
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
			"REVOKE ALL ON " + sc + ".desk_milestones, " + sc + ".desk_milestone_issues, " + sc + ".desk_follow_ups, " +
				sc + ".desk_milestone_releases FROM " + id,
			"REVOKE ALL ON " + sc + ".desk_backfill, " + sc + ".desk_backfill_projects FROM " + id,
		} {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("revoke from role %q: %w", w, err)
			}
		}
	}
	return nil
}
