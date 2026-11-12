package store

import (
	"context"
	"fmt"
	"slices"
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

// costSeriesSQL is CostSeries' query and its arguments.
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
	c := newCostSQL(q, q.From, q.To)
	b, l := c.arg(bucket), c.arg(loc.String())
	return c.with + `SELECT to_char(date_trunc(` + b + `::text, at AT TIME ZONE ` + l + `::text), 'YYYY-MM-DD'), ` + key + `, ` + spendTotals + `
		FROM ` + c.rows(group == "model") + ` GROUP BY 1, 2 ORDER BY 1, 2`, c.args, nil
}

// costSQL is the common part of the cost queries over q's projects in
// [from, to): the common table expressions, q's filters and their
// arguments. The calls are selected by project and time, on
// agent_calls_project_at_idx; the filters apply to the rows of spend or
// split, so the legacy and checkpoint CTEs, which lack their columns, never
// see them.
type costSQL struct {
	with, filter string
	args         []any
}

func newCostSQL(q CostQuery, from, to time.Time) *costSQL {
	nss, names := split(q.Projects)
	c := &costSQL{
		with: spendWith(`(namespace, project) IN (SELECT * FROM unnest($1::text[], $2::text[])) AND at >= $3 AND at < $4`),
		args: []any{nss, names, from, to},
	}
	if q.Kind != "" {
		c.filter += ` AND kind = ` + c.arg(q.Kind)
	}
	if q.Engine != "" {
		c.filter += ` AND engine = ` + c.arg(q.Engine)
	}
	if len(q.ExcludeKinds) > 0 {
		c.filter += ` AND NOT kind = ANY(` + c.arg(q.ExcludeKinds) + `::text[])`
	}
	return c
}

// arg appends v to the arguments and returns its placeholder.
func (c *costSQL) arg(v any) string {
	c.args = append(c.args, v)
	return "$" + strconv.Itoa(len(c.args))
}

// rows is the filtered calls and legacy runs as relation r, without the
// checkpoint events (legacy with a checkpoint), which cost nothing; byModel
// splits the calls by model (split).
func (c *costSQL) rows(byModel bool) string {
	return c.rowsWith(costCols, byModel)
}

// rowsWith is rows with the columns cols of spend (and split, byModel).
func (c *costSQL) rowsWith(cols string, byModel bool) string {
	rows := `SELECT ` + cols + ` FROM spend WHERE NOT legacy OR checkpoint = ''`
	if byModel {
		rows = `SELECT ` + cols + ` FROM split UNION ALL SELECT ` + cols + ` FROM spend WHERE legacy AND checkpoint = ''`
	}
	return `(` + rows + `) r WHERE true` + c.filter
}

// CostBreakdowns is what q's calls and legacy runs cost in total and by
// project, kind, model and engine, with the totals of the previous period.
// The breakdowns are ordered by known cost, descending, then key; legacy
// runs have the key "" in ByKind, ByModel and ByEngine. ByModel splits calls
// by model as Spend.ByModel does, subagent models as their own. The cache
// read shares are Spend's: cache reads / (input + cache reads + cache
// writes), 0 without tokens.
type CostBreakdowns struct {
	// Previous is the totals of the period of equal length right before
	// From: [From-(To-From), From).
	Totals, Previous                       SpendTotals
	ByProject, ByKind, ByModel, ByEngine   []SpendBreakdown
	CacheReadShare, PreviousCacheReadShare float64
	// StepLines is the size of the range's step commits, as Spend's.
	StepLines StepLines
	// ResearchSince is the first research call among the projects still
	// stored (any time), nil without one or when research is excluded.
	ResearchSince *time.Time
}

// costStmt is one statement of CostBreakdowns' batch.
type costStmt struct {
	sql  string
	args []any
}

// costBreakdownsSQL is CostBreakdowns' statements in the order it reads
// them: totals, previous totals, by project, kind, model and engine, step
// lines, and the first research call unless research is excluded.
func costBreakdownsSQL(q CostQuery) []costStmt {
	cur, prev := newCostSQL(q, q.From, q.To), newCostSQL(q, q.From.Add(-q.To.Sub(q.From)), q.From)
	totals := `SELECT ` + spendTotals + ` FROM ` + cur.rows(false)
	stmts := []costStmt{{cur.with + totals, cur.args}, {prev.with + totals, prev.args}}
	for _, group := range []string{"project", "kind", "model", "engine"} {
		stmts = append(stmts, costStmt{cur.with + `SELECT ` + costGroups[group] + `, ` + spendTotals + ` FROM ` + cur.rows(group == "model") +
			` GROUP BY 1 ORDER BY sum(cost_usd) DESC NULLS LAST, 1`, cur.args})
	}
	stmts = append(stmts, costStmt{cur.with + stepLinesSQL(cur.filter), cur.args})
	if countsResearch(q) {
		// Each project's first research call, on agent_calls_project_at_idx.
		stmts = append(stmts, costStmt{`SELECT min(fr.at) FROM unnest($1::text[], $2::text[]) AS p(ns, name)
			CROSS JOIN LATERAL (
				SELECT at FROM agent_calls WHERE namespace = p.ns AND project = p.name AND kind = 'research' AND expires_at > now()
				ORDER BY at LIMIT 1
			) fr`, cur.args[:2]})
	}
	return stmts
}

// countsResearch is whether q's calls may include research calls.
func countsResearch(q CostQuery) bool {
	return (q.Kind == "" || q.Kind == "research") && !slices.Contains(q.ExcludeKinds, "research")
}

// CostBreakdowns returns the totals and breakdowns of q's calls and legacy
// runs (checkpoint events left out, as in CostSeries), in one round trip.
// No projects give zero totals and empty breakdowns.
func (s *Store) CostBreakdowns(ctx context.Context, q CostQuery) (CostBreakdowns, error) {
	out := CostBreakdowns{
		Totals: SpendTotals{CostUSD: USD(0)}, Previous: SpendTotals{CostUSD: USD(0)},
		ByProject: []SpendBreakdown{}, ByKind: []SpendBreakdown{}, ByModel: []SpendBreakdown{}, ByEngine: []SpendBreakdown{},
	}
	if len(q.Projects) == 0 {
		return out, nil
	}
	b := &pgx.Batch{}
	for _, st := range costBreakdownsSQL(q) {
		b.Queue(st.sql, st.args...)
	}
	br := s.pool.SendBatch(ctx, b)
	err := func() error {
		var err error
		if out.Totals, err = scanTotals(br.QueryRow()); err != nil {
			return err
		}
		if out.Previous, err = scanTotals(br.QueryRow()); err != nil {
			return err
		}
		for _, dst := range []*[]SpendBreakdown{&out.ByProject, &out.ByKind, &out.ByModel, &out.ByEngine} {
			rows, _ := br.Query()
			if *dst, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (SpendBreakdown, error) {
				var b SpendBreakdown
				var err error
				b.Totals, err = scanTotals(row, &b.Key)
				return b, err
			}); err != nil {
				return err
			}
		}
		if out.StepLines, err = stepLines(br.QueryRow()); err != nil {
			return err
		}
		if countsResearch(q) {
			return br.QueryRow().Scan(&out.ResearchSince)
		}
		return nil
	}()
	if cerr := br.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return CostBreakdowns{}, err
	}
	out.CacheReadShare, out.PreviousCacheReadShare = out.Totals.cacheReadShare(), out.Previous.cacheReadShare()
	return out, nil
}
