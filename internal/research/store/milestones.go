package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// MilestoneQuery selects snapshot milestones. Projects is required: the
// projects the caller may see (an empty list selects nothing). State is
// open, closed or empty for both. Limit is capped at MaxMilestones, and 0
// or less means MaxMilestones.
type MilestoneQuery struct {
	Projects []ProjectKey
	State    string
	Limit    int
}

// MaxMilestones caps a milestone list.
const MaxMilestones = 200

// MilestoneRow is one milestone of the list with its progress and spend.
// Merged counts the issues merged or skipped, Open the others.
// FollowUpsOpen are the leftovers still open. Spend sums the milestone's
// calls as SpendTotals does: without calls, CostKnown is false and every
// sum is 0, never a cost of $0.
type MilestoneRow struct {
	Project              ProjectKey
	IID                  int64
	Title, WebURL        string
	State, Phase         string
	StartedAt, DoneAt    time.Time
	TrackingIID          int64
	Issues, Merged, Open int
	FollowUpsOpen        int
	Spend                SpendTotals
}

// Milestones returns the milestones q selects in one query: open ones
// first, the longest running first, then closed ones, the newest done
// first.
func (s *Store) Milestones(ctx context.Context, q MilestoneQuery) ([]MilestoneRow, error) {
	if len(q.Projects) == 0 {
		return nil, nil
	}
	if q.State != "" && q.State != "open" && q.State != "closed" {
		return nil, fmt.Errorf("milestones: unknown state %q", q.State)
	}
	if q.Limit <= 0 || q.Limit > MaxMilestones {
		q.Limit = MaxMilestones
	}
	namespaces, names := split(q.Projects)
	const inProjects = `(namespace, project) IN (SELECT * FROM unnest($1::text[], $2::text[]))`
	rows, _ := s.pool.Query(ctx, spendWith(inProjects+` AND milestone_iid IS NOT NULL`)+`
		SELECT m.namespace, m.project, m.iid, m.title, m.web_url, m.state, m.phase,
			m.started_at, m.done_at, coalesce(m.tracking_iid, 0),
			coalesce(i.total, 0), coalesce(i.merged, 0), coalesce(f.open, 0),
			coalesce(sp.cost, 0), coalesce(sp.unknown, 0), coalesce(sp.input, 0), coalesce(sp.output, 0),
			coalesce(sp.cache_read, 0), coalesce(sp.cache_write, 0), coalesce(sp.calls, 0),
			coalesce(sp.subagent_calls, 0), coalesce(sp.duration, 0), coalesce(sp.legacy, 0)
		FROM desk_milestones m
		LEFT JOIN (
			SELECT namespace, project, milestone_iid, count(*) AS total,
				count(*) FILTER (WHERE state IN ('merged', 'skipped')) AS merged
			FROM desk_milestone_issues WHERE `+inProjects+` GROUP BY 1, 2, 3
		) i ON i.namespace = m.namespace AND i.project = m.project AND i.milestone_iid = m.iid
		LEFT JOIN (
			SELECT namespace, project, milestone_iid, count(*) AS open
			FROM desk_follow_ups WHERE state = 'open' AND `+inProjects+` GROUP BY 1, 2, 3
		) f ON f.namespace = m.namespace AND f.project = m.project AND f.milestone_iid = m.iid
		LEFT JOIN (
			SELECT namespace, project, milestone_iid, `+spendTotals+` FROM spend GROUP BY 1, 2, 3
		) sp (namespace, project, milestone_iid, cost, unknown, input, output, cache_read, cache_write,
			calls, subagent_calls, duration, legacy)
			ON sp.namespace = m.namespace AND sp.project = m.project AND sp.milestone_iid = m.iid
		WHERE (m.namespace, m.project) IN (SELECT * FROM unnest($1::text[], $2::text[]))
			AND ($3 = '' OR m.state = $3)
		ORDER BY m.state = 'closed', CASE WHEN m.state = 'open' THEN m.started_at END NULLS LAST,
			m.done_at DESC NULLS LAST, m.namespace, m.project, m.iid
		LIMIT $4`, namespaces, names, q.State, q.Limit)
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (MilestoneRow, error) {
		var m MilestoneRow
		var started, done *time.Time
		var err error
		m.Spend, err = scanTotals(row, &m.Project.Namespace, &m.Project.Name, &m.IID, &m.Title, &m.WebURL,
			&m.State, &m.Phase, &started, &done, &m.TrackingIID, &m.Issues, &m.Merged, &m.FollowUpsOpen)
		if started != nil {
			m.StartedAt = *started
		}
		if done != nil {
			m.DoneAt = *done
		}
		m.Open = m.Issues - m.Merged
		// scanTotals calls no spend at all known ($0); a milestone without
		// calls is unknown.
		if m.Spend.Calls == 0 && m.Spend.LegacyCostUSD == 0 {
			m.Spend.CostKnown = false
		}
		return m, err
	})
}
