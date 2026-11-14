package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// MilestoneSpend is what a finished milestone cost: its unexpired calls'
// known costs (unknown without any) and all their tokens. Requirements is 0
// when unknown, Issues the planned issues.
type MilestoneSpend struct {
	IID                  int64
	Requirements, Issues int
	CostUSD              Money
	Tokens               int64
}

// MilestoneSpendHistory is the spend of the project's last limit milestones
// with a done time, newest first.
func (s *Store) MilestoneSpendHistory(ctx context.Context, project ProjectKey, limit int) ([]MilestoneSpend, error) {
	rows, err := s.pool.Query(ctx, `SELECT m.iid, coalesce(m.requirements, 0),
		(SELECT count(*) FROM desk_milestone_issues i
			WHERE i.namespace = m.namespace AND i.project = m.project AND i.milestone_iid = m.iid),
		c.cost, coalesce(c.tokens, 0)::bigint
		FROM desk_milestones m, LATERAL (SELECT sum(cost_usd)::float8 AS cost,
			sum(input_tokens + output_tokens + cache_read_tokens + cache_write_tokens) AS tokens
			FROM agent_calls a WHERE a.namespace = m.namespace AND a.project = m.project
				AND a.milestone_iid = m.iid AND a.expires_at > now()) c
		WHERE m.namespace = $1 AND m.project = $2 AND m.done_at IS NOT NULL
		ORDER BY m.done_at DESC, m.iid DESC LIMIT $3`, project.Namespace, project.Name, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (MilestoneSpend, error) {
		var m MilestoneSpend
		var cost *float64
		err := row.Scan(&m.IID, &m.Requirements, &m.Issues, &cost, &m.Tokens)
		if cost != nil {
			m.CostUSD = USD(*cost)
		}
		return m, err
	})
}

// MilestoneRequirements is the number of requirements of the milestone's
// description, 0 when unknown or without the milestone.
func (s *Store) MilestoneRequirements(ctx context.Context, key ProjectKey, iid int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT coalesce(max(requirements), 0) FROM desk_milestones
		WHERE namespace = $1 AND project = $2 AND iid = $3`, key.Namespace, key.Name, iid).Scan(&n)
	return n, err
}
