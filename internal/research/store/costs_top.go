package store

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// CostTopRow is one milestone, issue or run of CostTop with its project.
// The IIDs are 0 for none; a run carries its milestone and issue, an issue
// its milestone, so the Spend drill-down can be addressed.
type CostTopRow struct {
	Namespace, Project     string
	MilestoneIID, IssueIID int64
	// Task and Run are set for runs only; Kind is the kind of the run's
	// first call, "" for a run before agent_calls.
	Task  string
	Run   int64
	Kind  string
	Label string
	// At is the latest call (a legacy run's end).
	At     time.Time
	Totals SpendTotals
}

// CostTop is the most expensive milestones, issues and runs of a range.
type CostTop struct {
	Milestones, Issues, Runs []CostTopRow
}

// costTopCols are the columns of spend that CostTop reads.
const costTopCols = `legacy, namespace, project, coalesce(milestone_iid, 0) AS milestone_iid, coalesce(issue_iid, 0) AS issue_iid,
	task, coalesce(run, 0) AS run, seq, at, kind, engine, cost_usd, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
	subagent_calls, duration_ms, checkpoints, token_basis`

// costTopLevels are CostTop's groups: the filter of the rows that have one,
// the key columns and the milestone, issue, task, run and kind columns.
var costTopLevels = []struct{ where, key, cols string }{
	{`milestone_iid <> 0`, `milestone_iid`, `milestone_iid, 0, '', 0, ''`},
	{`issue_iid <> 0`, `issue_iid`, `max(milestone_iid), issue_iid, '', 0, ''`},
	{`true`, `task, run`, `max(milestone_iid), max(issue_iid), task, run,
		coalesce((array_agg(kind ORDER BY at, seq) FILTER (WHERE NOT legacy))[1], '')`},
}

// costTopSQL is CostTop's statements, milestones, issues and runs, each the
// n first by known cost (rows with only unknown cost last), then latest call.
func costTopSQL(q CostQuery, n int) []costStmt {
	c := newCostSQL(q, q.From, q.To)
	limit := c.arg(n)
	stmts := make([]costStmt, 0, len(costTopLevels))
	for _, lv := range costTopLevels {
		stmts = append(stmts, costStmt{c.with + `SELECT namespace, project, ` + lv.cols + `, max(at), ` + spendTotals +
			` FROM ` + c.rowsWith(costTopCols, false) + ` AND ` + lv.where +
			` GROUP BY namespace, project, ` + lv.key +
			` ORDER BY sum(cost_usd) DESC NULLS LAST, max(at) DESC, namespace, project, ` + lv.key + ` LIMIT ` + limit, c.args})
	}
	return stmts
}

// CostTop returns q's n most expensive milestones, issues and runs (n is
// clamped to 1..50), from the same rows as CostSeries: legacy runs count,
// chat and research runs are runs. They rank by known cost, descending,
// then by their latest call; rows whose cost is all unknown come after the
// known ones with their UnknownCalls. No projects or no rows give empty
// slices.
func (s *Store) CostTop(ctx context.Context, q CostQuery, n int) (CostTop, error) {
	out := CostTop{Milestones: []CostTopRow{}, Issues: []CostTopRow{}, Runs: []CostTopRow{}}
	if len(q.Projects) == 0 {
		return out, nil
	}
	b := &pgx.Batch{}
	for _, st := range costTopSQL(q, min(max(n, 1), 50)) {
		b.Queue(st.sql, st.args...)
	}
	br := s.pool.SendBatch(ctx, b)
	err := func() error {
		for _, dst := range []*[]CostTopRow{&out.Milestones, &out.Issues, &out.Runs} {
			rows, _ := br.Query()
			got, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (CostTopRow, error) {
				var r CostTopRow
				var err error
				r.Totals, err = scanTotals(row, &r.Namespace, &r.Project, &r.MilestoneIID, &r.IssueIID, &r.Task, &r.Run, &r.Kind, &r.At)
				r.Label = r.label()
				return r, err
			})
			if err != nil {
				return err
			}
			*dst = append(*dst, got...)
		}
		return nil
	}()
	if cerr := br.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return CostTop{}, err
	}
	return out, nil
}

// label names r as Spend's children do (childLabel): "<task> run 3" for a
// run, "#281" for an issue, "%14" for a milestone.
func (r CostTopRow) label() string {
	switch {
	case r.Task != "":
		return r.Task + " run " + strconv.FormatInt(r.Run, 10)
	case r.IssueIID != 0:
		return "#" + strconv.FormatInt(r.IssueIID, 10)
	}
	return "%" + strconv.FormatInt(r.MilestoneIID, 10)
}
