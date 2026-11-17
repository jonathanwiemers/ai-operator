package store

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// Metrics: what a merged line costs, by experiment arm, model, effort and
// step-session mode, and what fills the context, for the Desk's Costs page.
//
// The unit is the issue: one row per (project, issue) with calls in the
// range. An issue's cost and tokens are those of all its calls in the range,
// of every kind. Its arm is the arm its calls carry; its model, effort and
// step session are those of its implement calls with the largest summed
// cost. It is merged when the history or the Desk snapshot says so (at any
// time); its merged lines are the lines of its implement calls in the range
// (lines_added + lines_deleted, the step commits' size). Groups sum these
// rows: Issues and MergedLines count merged issues only, Cost and Tokens all
// of the group's issues, so the cost of unmerged work counts against the
// group's merged lines.

// MetricsGroup is one group of issues: an arm of a project, a model, an
// effort or a step-session mode. Key "" is none (no effort or no step
// session recorded, or no implement call). Namespace and Project are set in
// Metrics.Arms only.
type MetricsGroup struct {
	Namespace, Project, Key string
	// Issues and MergedLines are of the group's merged issues.
	Issues, MergedLines int64
	// CostUSD (known cost) and Tokens are of all the group's issues.
	CostUSD float64
	Tokens  int64
}

// RoleMedian is the median base context of a project's calls of one role:
// the first turn's context size of the calls that started their session.
type RoleMedian struct {
	Namespace, Project, Role string
	MedianTokens, Calls      int64
}

// ToolUsage is what one tool put into a project's contexts: its results'
// count and size in bytes.
type ToolUsage struct {
	Namespace, Project, Tool string
	Calls, Bytes             int64
}

// Metrics is the metrics of a range.
type Metrics struct {
	// Arms is by project and arm, issues without an arm left out, ordered by
	// project and arm.
	Arms []MetricsGroup
	// ByModel, ByEffort and ByStepSession cover all issues, ordered by
	// merged issues, descending, then key.
	ByModel, ByEffort, ByStepSession []MetricsGroup
	// BaseContext is by project and role, ordered so.
	BaseContext []RoleMedian
	// TopTools is each project's top MetricsTopTools tools by result bytes
	// (ties by name), ordered by project, then rank.
	TopTools []ToolUsage
}

// MetricsTopTools is how many tools Metrics.TopTools has per project.
const MetricsTopTools = 5

// metricsIssues is the per-issue rows of the scoped calls, relation issues:
// namespace, project, issue_iid, arm, model, effort, step_session, merged,
// lines, cost_usd and tokens. Missing attributes are empty strings.
const metricsIssues = `, impl AS (
		SELECT namespace, project, issue_iid, model, effort, coalesce(step_session, '') AS step_session,
			row_number() OVER (PARTITION BY namespace, project, issue_iid
				ORDER BY sum(cost_usd) DESC NULLS LAST, model, effort, coalesce(step_session, '')) AS rank
		FROM scoped WHERE kind = 'implement' AND issue_iid IS NOT NULL
		GROUP BY namespace, project, issue_iid, model, effort, coalesce(step_session, '')
	), per_issue AS (
		SELECT namespace, project, issue_iid, max(arm) AS arm,
			coalesce(sum(lines_added + lines_deleted) FILTER (WHERE kind = 'implement'), 0) AS lines,
			coalesce(sum(cost_usd), 0) AS cost_usd,
			coalesce(sum(input_tokens + output_tokens + cache_read_tokens + cache_write_tokens), 0) AS tokens
		FROM scoped WHERE issue_iid IS NOT NULL
		GROUP BY namespace, project, issue_iid
	), issues AS (
		SELECT i.namespace, i.project, i.issue_iid, i.arm, coalesce(m.model, '') AS model, coalesce(m.effort, '') AS effort,
			coalesce(m.step_session, '') AS step_session, i.lines, i.cost_usd, i.tokens,
			EXISTS (SELECT 1 FROM history_events e WHERE e.namespace = i.namespace AND e.project = i.project
					AND e.issue_iid = i.issue_iid AND e.kind = 'merged' AND e.expires_at > now())
				OR EXISTS (SELECT 1 FROM desk_milestone_issues d WHERE d.namespace = i.namespace AND d.project = i.project
					AND d.iid = i.issue_iid AND d.state = 'merged' AND d.expires_at > now()) AS merged
		FROM per_issue i LEFT JOIN impl m
			ON m.namespace = i.namespace AND m.project = i.project AND m.issue_iid = i.issue_iid AND m.rank = 1
	) `

// metricsGroupCols aggregates issues rows into a MetricsGroup's figures.
const metricsGroupCols = `count(*) FILTER (WHERE merged), coalesce(sum(lines) FILTER (WHERE merged), 0)::bigint,
	sum(cost_usd)::float8, sum(tokens)::bigint`

// metricsSQL is Metrics' statements in the order it reads them: arms, by
// model, effort and step session, base context, top tools.
func metricsSQL(q CostQuery) []costStmt {
	nss, names := split(q.Projects)
	// scoped as spendWith's (on agent_calls_project_at_idx), with the
	// excluded kinds; the kind and engine filters do not apply, an issue
	// counts whole.
	with := `WITH scoped AS (
		SELECT * FROM agent_calls WHERE expires_at > now()
			AND (namespace, project) IN (SELECT * FROM unnest($1::text[], $2::text[])) AND at >= $3 AND at < $4`
	args := []any{nss, names, q.From, q.To}
	if len(q.ExcludeKinds) > 0 {
		with += ` AND NOT kind = ANY($5::text[])`
		args = append(args, q.ExcludeKinds)
	}
	with += `
	) `
	groups := with + metricsIssues
	by := func(key string) costStmt {
		return costStmt{groups + `SELECT '', '', ` + key + `, ` + metricsGroupCols + ` FROM issues
			GROUP BY 3 ORDER BY count(*) FILTER (WHERE merged) DESC, 3`, args}
	}
	return []costStmt{
		{groups + `SELECT namespace, project, arm, ` + metricsGroupCols + ` FROM issues WHERE arm IS NOT NULL
			GROUP BY 1, 2, 3 ORDER BY 1, 2, 3`, args},
		by(`model`), by(`effort`), by(`step_session`),
		{with + `SELECT namespace, project, role, round(percentile_cont(0.5) WITHIN GROUP (ORDER BY base_context_tokens))::bigint, count(*)
			FROM scoped WHERE base_context_tokens IS NOT NULL GROUP BY 1, 2, 3 ORDER BY 1, 2, 3`, args},
		{with + `SELECT namespace, project, tool, calls, bytes FROM (
				SELECT c.namespace, c.project, t.key AS tool,
					coalesce(sum((t.value->>'calls')::bigint), 0)::bigint AS calls, coalesce(sum((t.value->>'bytes')::bigint), 0)::bigint AS bytes,
					row_number() OVER (PARTITION BY c.namespace, c.project
						ORDER BY coalesce(sum((t.value->>'bytes')::bigint), 0) DESC, t.key) AS rank
				FROM scoped c CROSS JOIN LATERAL jsonb_each(c.tool_results) t
				WHERE c.tool_results IS NOT NULL AND jsonb_typeof(c.tool_results) = 'object'
				GROUP BY 1, 2, 3
			) ranked WHERE rank <= ` + strconv.Itoa(MetricsTopTools) + ` ORDER BY namespace, project, rank`, args},
	}
}

// Metrics returns q's metrics in one round trip. Kind, Engine and Loc of q
// are ignored; ExcludeKinds applies. No projects give empty slices.
func (s *Store) Metrics(ctx context.Context, q CostQuery) (Metrics, error) {
	out := Metrics{Arms: []MetricsGroup{}, ByModel: []MetricsGroup{}, ByEffort: []MetricsGroup{}, ByStepSession: []MetricsGroup{},
		BaseContext: []RoleMedian{}, TopTools: []ToolUsage{}}
	if len(q.Projects) == 0 {
		return out, nil
	}
	b := &pgx.Batch{}
	for _, st := range metricsSQL(q) {
		b.Queue(st.sql, st.args...)
	}
	br := s.pool.SendBatch(ctx, b)
	err := func() error {
		for _, dst := range []*[]MetricsGroup{&out.Arms, &out.ByModel, &out.ByEffort, &out.ByStepSession} {
			rows, _ := br.Query()
			got, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (MetricsGroup, error) {
				var g MetricsGroup
				err := row.Scan(&g.Namespace, &g.Project, &g.Key, &g.Issues, &g.MergedLines, &g.CostUSD, &g.Tokens)
				return g, err
			})
			if err != nil {
				return err
			}
			*dst = append(*dst, got...)
		}
		rows, _ := br.Query()
		base, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (RoleMedian, error) {
			var m RoleMedian
			err := row.Scan(&m.Namespace, &m.Project, &m.Role, &m.MedianTokens, &m.Calls)
			return m, err
		})
		if err != nil {
			return err
		}
		out.BaseContext = append(out.BaseContext, base...)
		rows, _ = br.Query()
		tools, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ToolUsage, error) {
			var u ToolUsage
			err := row.Scan(&u.Namespace, &u.Project, &u.Tool, &u.Calls, &u.Bytes)
			return u, err
		})
		out.TopTools = append(out.TopTools, tools...)
		return err
	}()
	if cerr := br.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Metrics{}, err
	}
	return out, nil
}
