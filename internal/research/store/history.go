package store

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// The factory's history: events of projects, written by the operator (the
// owner) and read by the UI (the reader).

// Event is one thing that happened in a project. Zero IIDs are stored as
// NULL. Key makes writes idempotent.
type Event struct {
	ID              int64
	Key             string
	At              time.Time
	Namespace       string
	Project         string
	MilestoneIID    int64
	IssueIID        int64
	MergeRequestIID int64 `db:"mr_iid"`
	Task            string
	Kind            string
	Actor           string
	Summary         string
	Data            map[string]any
	ExpiresAt       time.Time
}

// InsertEvents writes events, skipping those whose key is already stored,
// and returns how many it wrote.
func (s *Store) InsertEvents(ctx context.Context, events []Event) (int64, error) {
	if len(events) == 0 {
		return 0, nil
	}
	b := &pgx.Batch{}
	for _, e := range events {
		data, err := json.Marshal(e.Data)
		if err != nil {
			return 0, err
		}
		if e.Data == nil {
			data = []byte("{}")
		}
		b.Queue(`INSERT INTO history_events
			(key, at, namespace, project, milestone_iid, issue_iid, mr_iid, task, kind, actor, summary, data, expires_at)
			VALUES ($1, $2, $3, $4, nullif($5, 0), nullif($6, 0), nullif($7, 0), $8, $9, $10, $11, $12, $13)
			ON CONFLICT (key) DO NOTHING`,
			e.Key, e.At, e.Namespace, e.Project, e.MilestoneIID, e.IssueIID, e.MergeRequestIID,
			e.Task, e.Kind, e.Actor, e.Summary, data, e.ExpiresAt)
	}
	var n int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		br := tx.SendBatch(ctx, b)
		for range events {
			tag, err := br.Exec()
			if err != nil {
				_ = br.Close()
				return err
			}
			n += tag.RowsAffected()
		}
		return br.Close()
	})
	return n, err
}

// ProjectKey names an AgentProject.
type ProjectKey struct{ Namespace, Name string }

// Cursor is a position in a stream: the event at At with ID. ID 0 is a
// point in time (before: everything older than At).
type Cursor struct {
	At time.Time
	ID int64
}

// StreamQuery selects events. Projects is required: the projects the caller
// may see (an empty list selects nothing).
type StreamQuery struct {
	Projects []ProjectKey
	// Milestone limits the events to the milestone: those recorded with
	// it, and those of its Issues (its issues and its tracking issue).
	Milestone int64
	Issues    []int64
	// Issue, with Milestone, limits the events to the issue's instead:
	// those recorded with it, in the milestone or without one.
	Issue int64
	Kinds []string
	// Text is a full-text search (all words). Comments of the projects in
	// TextWithoutNotes are left out of it: their bodies may not be read.
	Text             string
	TextWithoutNotes []ProjectKey
	// Before returns the events older than it, newest first; After the
	// events newer than it, newest first, the oldest of them when there
	// are more than Limit.
	Before, After *Cursor
	Limit         int
}

// eventColumns are Event's fields by name (pgx.RowToStructByName) and in
// order (Stream's Scan).
const eventColumns = `id, key, at, namespace, project, coalesce(milestone_iid, 0) AS milestone_iid,
	coalesce(issue_iid, 0) AS issue_iid, coalesce(mr_iid, 0) AS mr_iid, task, kind, actor, summary, data, expires_at`

// Stream returns the events q selects.
func (s *Store) Stream(ctx context.Context, q StreamQuery) ([]Event, error) {
	if len(q.Projects) == 0 {
		return nil, nil
	}
	nss, names := split(q.Projects)
	args := []any{nss, names}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	where := `(namespace, project) IN (SELECT * FROM unnest($1::text[], $2::text[]))`
	switch {
	case q.Milestone != 0 && q.Issue != 0:
		m := arg(q.Milestone)
		where += " AND issue_iid = " + arg(q.Issue) + " AND coalesce(milestone_iid, " + m + ") = " + m
	case q.Milestone != 0:
		where += " AND (milestone_iid = " + arg(q.Milestone) + " OR issue_iid = ANY(" + arg(q.Issues) + "::bigint[]))"
	}
	if len(q.Kinds) > 0 {
		where += " AND kind = ANY(" + arg(q.Kinds) + "::text[])"
	}
	if q.Text != "" {
		where += " AND search @@ plainto_tsquery('simple', " + arg(q.Text) + ")"
		if len(q.TextWithoutNotes) > 0 {
			ns, names := split(q.TextWithoutNotes)
			where += " AND NOT (kind = 'note' AND (namespace, project) IN (SELECT * FROM unnest(" + arg(ns) + "::text[], " + arg(names) + "::text[])))"
		}
	}
	order := "DESC"
	switch {
	case q.After != nil:
		where += " AND (at, id) > (" + arg(q.After.At) + ", " + arg(q.After.ID) + "::bigint)"
		order = "ASC"
	case q.Before != nil && q.Before.ID == 0:
		where += " AND at < " + arg(q.Before.At)
	case q.Before != nil:
		where += " AND (at, id) < (" + arg(q.Before.At) + ", " + arg(q.Before.ID) + "::bigint)"
	}
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, _ := s.pool.Query(ctx, `SELECT `+eventColumns+` FROM history_events WHERE `+where+
		` ORDER BY at `+order+`, id `+order+` LIMIT `+arg(limit), args...)
	events, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Event, error) {
		var e Event
		err := row.Scan(&e.ID, &e.Key, &e.At, &e.Namespace, &e.Project, &e.MilestoneIID, &e.IssueIID,
			&e.MergeRequestIID, &e.Task, &e.Kind, &e.Actor, &e.Summary, &e.Data, &e.ExpiresAt)
		return e, err
	})
	if err != nil {
		return nil, err
	}
	if order == "ASC" {
		for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
			events[i], events[j] = events[j], events[i]
		}
	}
	return events, nil
}

func split(ps []ProjectKey) (namespaces, names []string) {
	namespaces, names = make([]string, len(ps)), make([]string, len(ps))
	for i, p := range ps {
		namespaces[i], names[i] = p.Namespace, p.Name
	}
	return namespaces, names
}

// Day is a project's activity on one day.
type Day struct {
	Day     time.Time
	Runs    int
	Merged  int
	CostUSD float64
	Events  int
}

// Days returns the activity of project per day in loc since since (days
// without events are left out).
func (s *Store) Days(ctx context.Context, p ProjectKey, since time.Time, loc *time.Location) ([]Day, error) {
	rows, _ := s.pool.Query(ctx, `SELECT (at AT TIME ZONE $4)::date AS day,
			count(*) FILTER (WHERE kind = 'run.finished'),
			count(*) FILTER (WHERE kind = 'merged'),
			coalesce(sum((data->>'costUSD')::numeric) FILTER (WHERE kind = 'run.finished' AND data ? 'costUSD'), 0)::float8,
			count(*)
		FROM history_events WHERE namespace = $1 AND project = $2 AND at >= $3
		GROUP BY 1 ORDER BY 1`, p.Namespace, p.Name, since, loc.String())
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Day, error) {
		var d Day
		err := row.Scan(&d.Day, &d.Runs, &d.Merged, &d.CostUSD, &d.Events)
		return d, err
	})
}

// DeleteExpiredEvents deletes the events that expired before now and
// returns how many. It runs as the owner.
func (s *Store) DeleteExpiredEvents(ctx context.Context, now time.Time) (int64, error) {
	var total int64
	for {
		tag, err := s.pool.Exec(ctx, `DELETE FROM history_events WHERE id IN (
			SELECT id FROM history_events WHERE expires_at < $1 ORDER BY expires_at LIMIT $2)`, now, deleteBatch)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < deleteBatch {
			return total, nil
		}
	}
}
