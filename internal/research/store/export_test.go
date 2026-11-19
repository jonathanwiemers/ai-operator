package store

import (
	"context"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrations are the embedded migrations, for tests that add their own.
var Migrations fs.FS = migrations

// Pool lets tests run SQL as the store's login.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// MigrateFS migrates with the migrations/*.sql of fsys instead of the
// embedded ones.
func (s *Store) MigrateFS(ctx context.Context, fsys fs.FS, g Grants) error {
	return s.migrate(ctx, fsys, g)
}

// SpendSinceQuery is the SQL and arguments SpendSince runs, for EXPLAIN tests.
func SpendSinceQuery(projects []ProjectKey, since time.Time) (string, []any) {
	nss, names := split(projects)
	return spendSinceSQL(), []any{nss, names, since}
}

// CostSeriesQuery is the SQL and arguments CostSeries runs, for EXPLAIN tests.
func CostSeriesQuery(q CostQuery, bucket, group string) (string, []any, error) {
	return costSeriesSQL(q, bucket, group)
}

// CostBreakdownsQueries is the SQL and arguments of each statement
// CostBreakdowns runs, for EXPLAIN tests.
func CostBreakdownsQueries(q CostQuery) (sqls []string, args [][]any) {
	for _, st := range costBreakdownsSQL(q) {
		sqls, args = append(sqls, st.sql), append(args, st.args)
	}
	return sqls, args
}

// CostTopQueries is the SQL and arguments of each statement CostTop runs,
// for EXPLAIN tests.
func CostTopQueries(q CostQuery, n int) (sqls []string, args [][]any) {
	for _, st := range costTopSQL(q, n) {
		sqls, args = append(sqls, st.sql), append(args, st.args)
	}
	return sqls, args
}

// MetricsQueries is the SQL and arguments of each statement Metrics runs,
// for EXPLAIN tests.
func MetricsQueries(q CostQuery) (sqls []string, args [][]any) {
	for _, st := range metricsSQL(q) {
		sqls, args = append(sqls, st.sql), append(args, st.args)
	}
	return sqls, args
}

// PausesInRangeQuery is the SQL and arguments PausesInRange runs, for
// EXPLAIN tests.
func PausesInRangeQuery(q CostQuery, now time.Time) (string, []any) {
	return pausesInRangeSQL(q, now)
}

// SetMaxRangePauseEvents lowers what PausesInRange reads, for truncation
// tests, until the test ends.
func SetMaxRangePauseEvents(t interface{ Cleanup(func()) }, n int) {
	old := maxRangePauseEvents
	maxRangePauseEvents = n
	t.Cleanup(func() { maxRangePauseEvents = old })
}

// FlowQuery is the SQL and arguments FlowInRange runs, for EXPLAIN tests.
func FlowQuery(q CostQuery, now time.Time) (string, []any) {
	return flowSQL(q, now)
}
