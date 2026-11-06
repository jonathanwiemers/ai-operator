package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Spend: what the agent calls of a scope cost, summed by Postgres from
// agent_calls, plus the runs from before agent_calls existed: run.finished
// events whose (task, run) has no call carry the run's cost without tokens.
// It only reads, as the UI's reader.

// SpendScope selects the calls of a project, narrowed by every non-zero
// field. The most specific field decides the children: a project's are its
// milestones, a milestone's its issues, an issue's its runs, a task's or
// run's its calls. Run needs Task.
type SpendScope struct {
	Namespace, Project     string
	MilestoneIID, IssueIID int64
	Task                   string
	Run                    int64
}

// SpendTotals sums calls. CostUSD sums the known costs, LegacyCostUSD (the
// part of CostUSD from runs without calls) included; UnknownCalls are the
// calls without a cost, which never add $0. CostKnown is false only when
// every call is unknown and there is no legacy cost.
type SpendTotals struct {
	CostUSD                                                      float64
	CostKnown                                                    bool
	UnknownCalls                                                 int64
	InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens int64
	Calls                                                        int64
	SubagentCalls                                                int64
	DurationMS                                                   int64
	LegacyCostUSD                                                float64
}

// Tokens is the sum of all tokens: input, output and cache.
func (t SpendTotals) Tokens() int64 {
	return t.InputTokens + t.OutputTokens + t.CacheReadTokens + t.CacheWriteTokens
}

// ModelSplit is one model's share of a child. Main is true for the calls'
// own model; the other models ran as subagents. CostKnown is false when the
// calls report no cost for the model (cursor splits tokens only).
type ModelSplit struct {
	Model     string
	Main      bool
	Tokens    int64
	CostUSD   float64
	CostKnown bool
}

// The levels of spend children.
const (
	SpendMilestone = "milestone"
	SpendIssue     = "issue"
	SpendRun       = "run"
	SpendCall      = "call"
)

// SpendChild is one part of a scope at the next level. Key is the
// milestone or issue IID ("" for the calls of no milestone), task/run of a
// run, or the seq of a call. At is its first call (a legacy run's end).
// Call is set for calls only.
type SpendChild struct {
	Level  string
	Key    string
	Label  string
	Totals SpendTotals
	Models []ModelSplit
	At     time.Time
	Call   *Call
}

// SpendBreakdown sums the calls with one kind, role or model.
type SpendBreakdown struct {
	Key    string
	Totals SpendTotals
}

// SubagentBreakdown sums a role's calls that ran subagents
// (subagent_calls > 0) or ran none. SubagentTokens are the tokens of the
// calls' subagent models, not of their own model.
type SubagentBreakdown struct {
	Role           string
	WithSubagents  bool
	Totals         SpendTotals
	SubagentTokens int64
	// ReadGateBlocks is the sum of the calls' read gate denials.
	ReadGateBlocks int64
}

// Spend is what a scope cost. The breakdowns cover its calls (not legacy
// runs); ByModel splits calls by model, subagent models as their own.
// CacheReadShare is cache reads / (input + cache reads + cache writes).
//
// The tuning breakdowns: ByReviewRound sums review calls by round (key
// "1", "2", …): within a run, round n is the review calls after its
// (n-1)th fix call by seq. BySession splits implement calls by session,
// key "fresh" or "resumed". BySubagents splits each role's calls by
// whether subagents ran.
type Spend struct {
	Totals                  SpendTotals
	Children                []SpendChild
	ByKind, ByRole, ByModel []SpendBreakdown
	ByReviewRound           []SpendBreakdown
	BySession               []SpendBreakdown
	BySubagents             []SubagentBreakdown
	CacheReadShare          float64
}

// spendWith is the common table expressions of the spend queries for the
// scope where:
//   - scoped: the calls in scope;
//   - legacy: the run.finished events in scope with a cost whose run has no
//     call;
//   - spend: both as one shape, legacy rows without tokens;
//   - split: the calls split by model, from models or else the row's model.
//     The main model is the row's; when models lacks it (the row has an
//     alias, say), the one with the most output tokens, as the runner picks
//     it. Subagent calls and duration go to the main model.
func spendWith(where string) string {
	return `WITH scoped AS (
		SELECT * FROM agent_calls WHERE expires_at > now() AND ` + where + `
	), legacy AS (
		SELECT * FROM (
			SELECT namespace, project, milestone_iid, issue_iid, task, at,
				(data->>'runCount')::bigint AS run, (data->>'costUSD')::numeric AS cost_usd
			FROM history_events WHERE kind = 'run.finished' AND data ? 'costUSD' AND expires_at > now()
		) e WHERE ` + where + ` AND NOT EXISTS (
			SELECT 1 FROM agent_calls a WHERE a.namespace = e.namespace AND a.project = e.project
				AND a.task = e.task AND a.run = e.run AND a.expires_at > now())
	), spend AS (
		SELECT false AS legacy, id, milestone_iid, issue_iid, task, run, seq, at, kind, role, resumed,
			cost_usd, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, subagent_calls, duration_ms, read_gate_blocks
		FROM scoped
		UNION ALL
		SELECT true, NULL, milestone_iid, issue_iid, task, run, NULL, at, '', '', false, cost_usd, 0, 0, 0, 0, 0, 0, 0
		FROM legacy
	), split AS (
		SELECT false AS legacy, c.id, c.milestone_iid, c.issue_iid, c.task, c.run, c.seq, c.at, m.model, m.main,
			m.cost_usd, m.input_tokens, m.output_tokens, m.cache_read_tokens, m.cache_write_tokens,
			CASE WHEN m.main THEN c.subagent_calls ELSE 0 END AS subagent_calls,
			CASE WHEN m.main THEN c.duration_ms ELSE 0 END AS duration_ms
		FROM scoped c CROSS JOIN LATERAL (
			SELECT e.m->>'model' AS model,
				CASE WHEN bool_or(e.m->>'model' = c.model) OVER () THEN e.m->>'model' = c.model
					ELSE row_number() OVER (ORDER BY coalesce((e.m->>'outputTokens')::bigint, 0) DESC, e.ord) = 1
				END AS main,
				(e.m->>'costUSD')::numeric AS cost_usd,
				coalesce((e.m->>'inputTokens')::bigint, 0) AS input_tokens,
				coalesce((e.m->>'outputTokens')::bigint, 0) AS output_tokens,
				coalesce((e.m->>'cacheReadTokens')::bigint, 0) AS cache_read_tokens,
				coalesce((e.m->>'cacheWriteTokens')::bigint, 0) AS cache_write_tokens
			FROM jsonb_array_elements(c.models) WITH ORDINALITY AS e(m, ord)
			UNION ALL
			SELECT c.model, true, c.cost_usd, c.input_tokens, c.output_tokens, c.cache_read_tokens, c.cache_write_tokens
			WHERE jsonb_array_length(c.models) = 0
		) m
	) `
}

// spendTotals aggregates spend or split rows into a SpendTotals, see
// scanTotals.
const spendTotals = `coalesce(sum(cost_usd), 0)::float8,
	count(*) FILTER (WHERE NOT legacy AND cost_usd IS NULL),
	coalesce(sum(input_tokens), 0)::bigint, coalesce(sum(output_tokens), 0)::bigint,
	coalesce(sum(cache_read_tokens), 0)::bigint, coalesce(sum(cache_write_tokens), 0)::bigint,
	count(*) FILTER (WHERE NOT legacy), coalesce(sum(subagent_calls), 0)::bigint,
	coalesce(sum(duration_ms), 0)::bigint, coalesce(sum(cost_usd) FILTER (WHERE legacy), 0)::float8`

// scanTotals scans the spendTotals columns after the dest columns before
// them.
func scanTotals(row pgx.Row, dest ...any) (SpendTotals, error) {
	var t SpendTotals
	err := row.Scan(append(dest, &t.CostUSD, &t.UnknownCalls, &t.InputTokens, &t.OutputTokens,
		&t.CacheReadTokens, &t.CacheWriteTokens, &t.Calls, &t.SubagentCalls, &t.DurationMS, &t.LegacyCostUSD)...)
	t.CostKnown = t.UnknownCalls < t.Calls || t.Calls == 0 || t.LegacyCostUSD > 0
	return t, err
}

// spendLevel is how a scope's children are grouped: key is their key
// expression over spend and split rows, order their order.
type spendLevel struct {
	name, key, filter, order string
}

func (sc SpendScope) level() spendLevel {
	switch {
	case sc.Task != "":
		return spendLevel{SpendCall, `id::text`, ` WHERE NOT legacy`, `min(seq), min(id)`}
	case sc.IssueIID != 0:
		return spendLevel{SpendRun, `task || '/' || run`, ``, `min(at), 1`}
	case sc.MilestoneIID != 0:
		return spendLevel{SpendIssue, `coalesce(issue_iid::text, '')`, ``, `min(issue_iid) NULLS LAST`}
	}
	return spendLevel{SpendMilestone, `coalesce(milestone_iid::text, '')`, ``, `min(milestone_iid) NULLS LAST`}
}

// where is the scope's condition on the columns namespace, project,
// milestone_iid, issue_iid, task and run, its values appended to args.
func (sc SpendScope) where(args *[]any) string {
	arg := func(v any) string {
		*args = append(*args, v)
		return "$" + strconv.Itoa(len(*args))
	}
	w := "namespace = " + arg(sc.Namespace) + " AND project = " + arg(sc.Project)
	if sc.MilestoneIID != 0 {
		w += " AND milestone_iid = " + arg(sc.MilestoneIID)
	}
	if sc.IssueIID != 0 {
		w += " AND issue_iid = " + arg(sc.IssueIID)
	}
	if sc.Task != "" {
		w += " AND task = " + arg(sc.Task)
	}
	if sc.Run != 0 {
		w += " AND run = " + arg(sc.Run)
	}
	return w
}

// spendQuery checks sc and returns the common table expressions of its
// spend queries and their arguments.
func spendQuery(sc SpendScope) (string, []any, error) {
	if sc.Namespace == "" || sc.Project == "" {
		return "", nil, errors.New("spend: namespace and project are required")
	}
	if sc.Run != 0 && sc.Task == "" {
		return "", nil, errors.New("spend: a run needs its task")
	}
	var args []any
	return spendWith(sc.where(&args)), args, nil
}

// SpendTotals returns the scope's totals as Spend does, without children
// and breakdowns: what a cost chip shows.
func (s *Store) SpendTotals(ctx context.Context, sc SpendScope) (SpendTotals, error) {
	with, args, err := spendQuery(sc)
	if err != nil {
		return SpendTotals{}, err
	}
	return scanTotals(s.pool.QueryRow(ctx, with+`SELECT `+spendTotals+` FROM spend`, args...))
}

// SpendChildTotals returns the totals of the scope's children at the next
// level in one query, without their model split and calls, by grouping
// key: a project's milestones by IID ("" for the calls of no milestone), a
// milestone's issues by IID ("" for the milestone's own calls).
func (s *Store) SpendChildTotals(ctx context.Context, sc SpendScope) (map[string]SpendTotals, error) {
	with, args, err := spendQuery(sc)
	if err != nil {
		return nil, err
	}
	children, err := s.childTotals(ctx, sc, with, args)
	if err != nil {
		return nil, err
	}
	out := make(map[string]SpendTotals, len(children))
	for _, c := range children {
		out[c.Key] = c.Totals
	}
	return out, nil
}

// Spend returns what the scope cost: its totals, its children at the next
// level with their model split, and its breakdowns by kind, role and model.
// Expired calls and events are left out.
func (s *Store) Spend(ctx context.Context, sc SpendScope) (Spend, error) {
	with, args, err := spendQuery(sc)
	if err != nil {
		return Spend{}, err
	}
	var sp Spend
	if sp.Totals, err = scanTotals(s.pool.QueryRow(ctx, with+`SELECT `+spendTotals+` FROM spend`, args...)); err != nil {
		return Spend{}, err
	}
	if in := sp.Totals.InputTokens + sp.Totals.CacheReadTokens + sp.Totals.CacheWriteTokens; in > 0 {
		sp.CacheReadShare = float64(sp.Totals.CacheReadTokens) / float64(in)
	}
	if sp.Children, err = s.spendChildren(ctx, sc, with, args); err != nil {
		return Spend{}, err
	}
	const byCost = `sum(cost_usd) DESC NULLS LAST, 1`
	for _, b := range []struct {
		out              *[]SpendBreakdown
		key, from, order string
	}{
		{&sp.ByKind, "kind", "spend WHERE NOT legacy", byCost},
		{&sp.ByRole, "role", "spend WHERE NOT legacy", byCost},
		{&sp.ByModel, "model", "split", byCost},
		{&sp.ByReviewRound, "round::text", reviewRounds, "min(round)"},
		{&sp.BySession, "CASE WHEN resumed THEN 'resumed' ELSE 'fresh' END", "spend WHERE NOT legacy AND kind = 'implement'", "1"},
	} {
		rows, _ := s.pool.Query(ctx, with+`SELECT `+b.key+`, `+spendTotals+` FROM `+b.from+
			` GROUP BY 1 ORDER BY `+b.order, args...)
		if *b.out, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (SpendBreakdown, error) {
			var br SpendBreakdown
			br.Totals, err = scanTotals(row, &br.Key)
			return br, err
		}); err != nil {
			return Spend{}, err
		}
	}
	rows, _ := s.pool.Query(ctx, with+`SELECT role, subagent_calls > 0, coalesce(sum(sub_tokens), 0)::bigint,
			coalesce(sum(read_gate_blocks), 0)::bigint, `+spendTotals+`
		FROM spend LEFT JOIN (
			SELECT id AS sub_id, sum(input_tokens + output_tokens + cache_read_tokens + cache_write_tokens) AS sub_tokens
			FROM split WHERE NOT main GROUP BY id
		) sub ON sub.sub_id = spend.id
		WHERE NOT legacy GROUP BY 1, 2 ORDER BY 1, 2`, args...)
	if sp.BySubagents, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (SubagentBreakdown, error) {
		var b SubagentBreakdown
		var err error
		b.Totals, err = scanTotals(row, &b.Role, &b.WithSubagents, &b.SubagentTokens, &b.ReadGateBlocks)
		return b, err
	}); err != nil {
		return Spend{}, err
	}
	return sp, nil
}

// reviewRounds numbers the review calls of each run into rounds: 1 plus
// the fix calls before them by seq.
const reviewRounds = `(
	SELECT *, 1 + count(*) FILTER (WHERE kind = 'fix') OVER (PARTITION BY task, run ORDER BY seq, id) AS round
	FROM spend WHERE NOT legacy AND kind IN ('review', 'fix')
) r WHERE kind = 'review'`

// spendChildren returns the scope's children with their model split and,
// for calls, their rows.
func (s *Store) spendChildren(ctx context.Context, sc SpendScope, with string, args []any) ([]SpendChild, error) {
	lv := sc.level()
	children, err := s.childTotals(ctx, sc, with, args)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]*SpendChild, len(children))
	for i := range children {
		byKey[children[i].Key] = &children[i]
	}

	rows, _ := s.pool.Query(ctx, with+`SELECT `+lv.key+` AS ck, model, bool_or(main), `+spendTotals+` FROM split
		GROUP BY ck, model ORDER BY ck, bool_or(main) DESC, sum(input_tokens + output_tokens + cache_read_tokens + cache_write_tokens) DESC, model`, args...)
	type modelRow struct {
		key string
		ModelSplit
	}
	models, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (modelRow, error) {
		var m modelRow
		t, err := scanTotals(row, &m.key, &m.Model, &m.Main)
		m.Tokens, m.CostUSD, m.CostKnown = t.Tokens(), t.CostUSD, t.UnknownCalls < t.Calls
		return m, err
	})
	if err != nil {
		return nil, err
	}
	for _, m := range models {
		if c := byKey[m.key]; c != nil {
			c.Models = append(c.Models, m.ModelSplit)
		}
	}

	if lv.name == SpendCall {
		rows, _ := s.pool.Query(ctx, with+`SELECT `+callColumns+` FROM scoped`, args...)
		calls, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Call, error) { return scanCall(row) })
		if err != nil {
			return nil, err
		}
		for i := range calls {
			if c := byKey[strconv.FormatInt(calls[i].ID, 10)]; c != nil {
				c.Call = &calls[i]
			}
		}
	}
	for i := range children {
		children[i].Key, children[i].Label = childLabel(&children[i], sc)
	}
	return children, nil
}

// childTotals returns the scope's children with their grouping key, first
// call and totals.
func (s *Store) childTotals(ctx context.Context, sc SpendScope, with string, args []any) ([]SpendChild, error) {
	lv := sc.level()
	rows, _ := s.pool.Query(ctx, with+`SELECT `+lv.key+` AS ck, min(at), `+spendTotals+` FROM spend`+lv.filter+
		` GROUP BY ck ORDER BY `+lv.order, args...)
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (SpendChild, error) {
		c := SpendChild{Level: lv.name}
		var err error
		c.Totals, err = scanTotals(row, &c.Key, &c.At)
		return c, err
	})
}

// childLabel returns the key and label of c, whose key is still its
// grouping key.
func childLabel(c *SpendChild, sc SpendScope) (key, label string) {
	switch c.Level {
	case SpendMilestone:
		if c.Key == "" {
			return "", "No milestone"
		}
		return c.Key, "%" + c.Key
	case SpendIssue:
		if c.Key == "" {
			// The milestone's own calls: spec, pilot, retro.
			c.Level = SpendMilestone
			m := strconv.FormatInt(sc.MilestoneIID, 10)
			return m, "Milestone %" + m
		}
		return c.Key, "#" + c.Key
	case SpendRun:
		if i := strings.LastIndexByte(c.Key, '/'); i >= 0 {
			return c.Key, c.Key[:i] + " run " + c.Key[i+1:]
		}
	case SpendCall:
		if c.Call != nil {
			label := c.Call.Kind
			if c.Call.Role != "" && c.Call.Role != c.Call.Kind {
				label += " (" + c.Call.Role + ")"
			}
			return strconv.FormatInt(c.Call.Seq, 10), label
		}
	}
	return c.Key, c.Key
}

const callColumns = `id, key, at, namespace, project, coalesce(milestone_iid, 0), coalesce(issue_iid, 0),
	coalesce(mr_iid, 0), task, run, seq, kind, role, engine, model, effort, resumed, fallback_from,
	input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd::float8, cost_source,
	turns, duration_ms, models, subagent_calls, context_series, subagent_turns, expires_at, read_gate_blocks`

func scanCall(row pgx.Row) (Call, error) {
	var c Call
	err := row.Scan(&c.ID, &c.Key, &c.At, &c.Namespace, &c.Project, &c.MilestoneIID, &c.IssueIID,
		&c.MergeRequestIID, &c.Task, &c.Run, &c.Seq, &c.Kind, &c.Role, &c.Engine, &c.Model, &c.Effort, &c.Resumed,
		&c.FallbackFrom, &c.InputTokens, &c.OutputTokens, &c.CacheReadTokens, &c.CacheWriteTokens, &c.CostUSD,
		&c.CostSource, &c.Turns, &c.DurationMS, &c.Models, &c.SubagentCalls, &c.ContextSeries, &c.SubagentTurns,
		&c.ExpiresAt, &c.ReadGateBlocks)
	return c, err
}
