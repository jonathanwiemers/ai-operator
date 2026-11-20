package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// The event kinds that open and resolve problems (desk_problems): writing
// such an event also writes its problem, in the same transaction.
const (
	KindProblemOpened   = "problem.opened"
	KindProblemResolved = "problem.resolved"
)

// ReopenWithin is how long after it was resolved a problem reopens when its
// issue gets stuck again, instead of a new one starting.
const ReopenWithin = 24 * time.Hour

// Problem is a milestone's problem: an issue the autopilot is stuck on, or
// the milestone itself (IssueIID 0). State is open or resolved.
type Problem struct {
	ID           int64
	Namespace    string
	Project      string
	MilestoneIID int64
	IssueIID     int64
	State        string
	Cause        string
	OpenedAt     time.Time
	ReopenedAt   *time.Time
	ResolvedAt   *time.Time
	ResolvedBy   string
}

const problemColumns = `id, namespace, project, milestone_iid, issue_iid, state, cause, opened_at, reopened_at,
	resolved_at, resolved_by`

// The states of a problem.
const (
	ProblemOpen     = "open"
	ProblemResolved = "resolved"
)

// applyProblem writes the problem of e, a problem.opened or problem.resolved
// event that was just inserted. Opening an open problem keeps it (and sets a
// newer cause); opening one resolved within ReopenWithin reopens it.
// Resolving resolves the open one, if any. Data["cause"] and
// Data["resolvedBy"] carry the texts.
func applyProblem(ctx context.Context, tx pgx.Tx, e Event) error {
	str := func(k string) string { s, _ := e.Data[k].(string); return s }
	switch e.Kind {
	case KindProblemOpened:
		tag, err := tx.Exec(ctx, `UPDATE desk_problems SET
				reopened_at = CASE WHEN state = 'resolved' THEN $5 ELSE reopened_at END,
				state = 'open', resolved_at = NULL, resolved_by = '',
				cause = CASE WHEN $6 = '' THEN cause ELSE $6 END
			WHERE id = (SELECT id FROM desk_problems
				WHERE namespace = $1 AND project = $2 AND milestone_iid = $3 AND issue_iid = $4
					AND (state = 'open' OR resolved_at > $7)
				ORDER BY state = 'open' DESC, opened_at DESC LIMIT 1)`,
			e.Namespace, e.Project, e.MilestoneIID, e.IssueIID, e.At, str("cause"), e.At.Add(-ReopenWithin))
		if err != nil || tag.RowsAffected() > 0 {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO desk_problems (namespace, project, milestone_iid, issue_iid, cause, opened_at)
			VALUES ($1, $2, $3, $4, $5, $6)`, e.Namespace, e.Project, e.MilestoneIID, e.IssueIID, str("cause"), e.At)
		return err
	case KindProblemResolved:
		_, err := tx.Exec(ctx, `UPDATE desk_problems SET state = 'resolved', resolved_at = $5, resolved_by = $6
			WHERE namespace = $1 AND project = $2 AND milestone_iid = $3 AND issue_iid = $4 AND state = 'open'`,
			e.Namespace, e.Project, e.MilestoneIID, e.IssueIID, e.At, str("resolvedBy"))
		return err
	}
	return nil
}

// Problems returns the problems of a milestone, newest first.
func (s *Store) Problems(ctx context.Context, p ProjectKey, milestone int64) ([]Problem, error) {
	rows, _ := s.pool.Query(ctx, `SELECT `+problemColumns+` FROM desk_problems
		WHERE namespace = $1 AND project = $2 AND milestone_iid = $3 ORDER BY opened_at DESC, id DESC LIMIT 200`,
		p.Namespace, p.Name, milestone)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Problem])
}

// OpenProblems returns the open problems of the projects, newest first.
func (s *Store) OpenProblems(ctx context.Context, projects []ProjectKey) ([]Problem, error) {
	if len(projects) == 0 {
		return nil, nil
	}
	nss, names := split(projects)
	rows, _ := s.pool.Query(ctx, `SELECT `+problemColumns+` FROM desk_problems
		WHERE state = 'open' AND (namespace, project) IN (SELECT * FROM unnest($1::text[], $2::text[]))
		ORDER BY opened_at DESC, id DESC LIMIT 500`, nss, names)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Problem])
}

// ErrNoProblem is GetProblem's error for an unknown ID.
var ErrNoProblem = errors.New("no such problem")

// GetProblem returns the problem id of the project's milestone.
func (s *Store) GetProblem(ctx context.Context, p ProjectKey, milestone, id int64) (Problem, error) {
	rows, _ := s.pool.Query(ctx, `SELECT `+problemColumns+` FROM desk_problems
		WHERE id = $1 AND namespace = $2 AND project = $3 AND milestone_iid = $4`, id, p.Namespace, p.Name, milestone)
	pr, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Problem])
	if errors.Is(err, pgx.ErrNoRows) {
		return Problem{}, ErrNoProblem
	}
	return pr, err
}
