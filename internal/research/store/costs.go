package store

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// Costs: the spend of many projects over time, for the Desk's Costs page.
// It sums the same rows as Spend (spendWith), per time bucket and group key.

// CostQuery selects calls of projects in [From, To).
type CostQuery struct {
	Projects []ProjectKey
	From, To time.Time
	// Loc is the time zone of the buckets, local dates; nil is UTC.
	Loc *time.Location
	// Kind and Engine keep only the calls with them, "" all. Set, they drop
	// the legacy runs, which have neither.
	Kind, Engine string
	// ExcludeKinds drops the calls of these kinds, e.g. research for viewers
	// without research access.
	ExcludeKinds []string
}

// CostPoint is the totals of one bucket and group key.
type CostPoint struct {
	// Bucket is the local start date of the bucket, YYYY-MM-DD.
	Bucket string
	// Key is the group key: namespace/name of a project, a kind, model or
	// engine; "" for legacy runs ("before calls") and for group "".
	Key    string
	Totals SpendTotals
}

// costBuckets are CostSeries' buckets, date_trunc's fields: a week starts on
// Monday (ISO).
var costBuckets = map[string]bool{"day": true, "week": true, "month": true}

// costGroups are the key expressions of CostSeries' groups over costRows.
var costGroups = map[string]string{
	"":        `''`,
	"project": `namespace || '/' || project`,
	"kind":    `kind`,
	"model":   `model`,
	"engine":  `engine`,
}

// costCols are the columns of spend and split that CostSeries reads.
const costCols = `legacy, namespace, project, at, kind, engine, coalesce(model, '') AS model, cost_usd,
	input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, subagent_calls, duration_ms, checkpoints, token_basis`

// CostSeries returns the totals of q's calls and legacy runs per bucket
// (day, week or month) and group (project, kind, model, engine or "" for
// one series), ordered by bucket and key. Group model splits calls by their
// models as Spend.ByModel does: a call counts as a call of each of its
// models, its cost and tokens add up. Checkpoint events are left out: they
// cost nothing. No projects or no rows give an empty slice.
func (s *Store) CostSeries(ctx context.Context, q CostQuery, bucket, group string) ([]CostPoint, error) {
	sql, args, err := costSeriesSQL(q, bucket, group)
	if err != nil || len(q.Projects) == 0 {
		return []CostPoint{}, err
	}
	rows, _ := s.pool.Query(ctx, sql, args...)
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (CostPoint, error) {
		var p CostPoint
		var err error
		p.Totals, err = scanTotals(row, &p.Bucket, &p.Key)
		return p, err
	})
}

// costSeriesSQL is CostSeries' query and its arguments. The calls are
// selected by project and time, on agent_calls_project_at_idx; the filters
// apply to the rows of spend or split, so the legacy and checkpoint CTEs,
// which lack their columns, never see them.
func costSeriesSQL(q CostQuery, bucket, group string) (string, []any, error) {
	if !costBuckets[bucket] {
		return "", nil, fmt.Errorf("costs: unknown bucket %q", bucket)
	}
	key, ok := costGroups[group]
	if !ok {
		return "", nil, fmt.Errorf("costs: unknown group %q", group)
	}
	loc := q.Loc
	if loc == nil {
		loc = time.UTC
	}
	nss, names := split(q.Projects)
	args := []any{nss, names, q.From, q.To, bucket, loc.String()}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	// Legacy runs, not checkpoint events (legacy with a checkpoint).
	rows := `SELECT ` + costCols + ` FROM spend WHERE NOT legacy OR checkpoint = ''`
	if group == "model" {
		rows = `SELECT ` + costCols + ` FROM split UNION ALL SELECT ` + costCols + ` FROM spend WHERE legacy AND checkpoint = ''`
	}
	filter := ""
	if q.Kind != "" {
		filter += ` AND kind = ` + arg(q.Kind)
	}
	if q.Engine != "" {
		filter += ` AND engine = ` + arg(q.Engine)
	}
	if len(q.ExcludeKinds) > 0 {
		filter += ` AND NOT kind = ANY(` + arg(q.ExcludeKinds) + `::text[])`
	}
	return spendWith(`(namespace, project) IN (SELECT * FROM unnest($1::text[], $2::text[])) AND at >= $3 AND at < $4`) +
		`SELECT to_char(date_trunc($5::text, at AT TIME ZONE $6::text), 'YYYY-MM-DD'), ` + key + `, ` + spendTotals + `
		FROM (` + rows + `) r WHERE true` + filter + ` GROUP BY 1, 2 ORDER BY 1, 2`, args, nil
}
