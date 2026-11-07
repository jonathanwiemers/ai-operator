package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The backfill fills the Desk's snapshot once from GitLab for milestones the
// recorder never saw, one milestone at a time, and records each milestone
// (desk_backfill) so it can stop and restart. The recorder's rows take
// precedence: the backfill only inserts.

// BackfillAttempts is how often a milestone's backfill may fail before it is
// skipped.
const BackfillAttempts = 3

// maxBackfillError bounds the stored error text.
const maxBackfillError = 1000

// BackfillPending returns those of candidates, project's GitLab milestone
// IIDs, that still need a backfill, in candidates' order without repeats:
// neither backfilled, nor stored with issues (by the recorder), nor failed
// BackfillAttempts times.
func (s *Store) BackfillPending(ctx context.Context, p ProjectKey, candidates []int64) ([]int64, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	rows, _ := s.pool.Query(ctx, `SELECT c.iid FROM unnest($3::bigint[]) WITH ORDINALITY AS c(iid, n)
		WHERE NOT EXISTS (SELECT 1 FROM desk_backfill b WHERE b.namespace = $1 AND b.project = $2
				AND b.milestone_iid = c.iid AND (b.status = 'done' OR b.attempts >= $4))
			AND NOT EXISTS (SELECT 1 FROM desk_milestone_issues i WHERE i.namespace = $1 AND i.project = $2
				AND i.milestone_iid = c.iid)
		GROUP BY c.iid ORDER BY min(c.n)`, p.Namespace, p.Name, candidates, BackfillAttempts)
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// SaveBackfilled inserts the backfilled milestone m with its issues and
// follow-ups, marks it done in the same transaction and reports whether it
// inserted the milestone. It never changes a stored row: a milestone the
// recorder already wrote stays as it is, without the issues and follow-ups
// of m, and is marked done all the same.
func (s *Store) SaveBackfilled(ctx context.Context, m MilestoneSnapshot, expiresAt time.Time) (bool, error) {
	p := m.Project
	inserted := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO desk_milestones (namespace, project, iid, milestone_id, title, web_url,
				state, phase, started_at, done_at, tracking_iid, owners, owner_subjects, retro_note_id, retro_text,
				retro_posted_at, updated_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, nullif($11::bigint, 0), $12, $13, nullif($14::bigint, 0),
				$15, $16, now(), $17)
			ON CONFLICT (namespace, project, iid) DO NOTHING`,
			p.Namespace, p.Name, m.IID, m.MilestoneID, m.Title, m.WebURL, m.State, m.Phase,
			nullTime(m.StartedAt), nullTime(m.DoneAt), m.TrackingIID, nonNil(m.Owners), nonNil(m.OwnerSubjects),
			m.RetroNoteID, m.RetroText, nullTime(m.RetroPostedAt), expiresAt)
		if err != nil {
			return err
		}
		inserted = tag.RowsAffected() > 0
		b := &pgx.Batch{}
		if inserted {
			for i, is := range m.Issues {
				b.Queue(`INSERT INTO desk_milestone_issues (namespace, project, milestone_iid, iid, title, state,
						mr_iid, mr_url, position, expires_at)
					VALUES ($1, $2, $3, $4, $5, $6, nullif($7::bigint, 0), $8, $9, $10)
					ON CONFLICT DO NOTHING`,
					p.Namespace, p.Name, m.IID, is.IID, is.Title, is.State, is.MergeRequestIID, is.MergeRequestURL, i, expiresAt)
			}
			for _, f := range m.FollowUps {
				b.Queue(`INSERT INTO desk_follow_ups (namespace, project, milestone_iid, number, title, body, state,
						target_iid, expires_at)
					VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8::bigint, 0), $9)
					ON CONFLICT DO NOTHING`,
					p.Namespace, p.Name, m.IID, f.Number, f.Title, f.Body, f.State, f.TargetIID, expiresAt)
			}
		}
		// A repeat keeps the first done time; the failures before stay
		// counted.
		b.Queue(`INSERT INTO desk_backfill AS b (namespace, project, milestone_iid, status, at)
			VALUES ($1, $2, $3, 'done', now())
			ON CONFLICT (namespace, project, milestone_iid) DO UPDATE SET status = 'done', error = '', at = now()
			WHERE b.status <> 'done'`, p.Namespace, p.Name, m.IID)
		return tx.SendBatch(ctx, b).Close()
	})
	return inserted && err == nil, err
}

// BackfillFailed records a failed backfill of project's milestone iid with
// cause and returns its attempts so far. A milestone already backfilled
// stays done, and 0 is returned.
func (s *Store) BackfillFailed(ctx context.Context, p ProjectKey, iid int64, cause error) (int, error) {
	msg := "unknown error"
	if cause != nil {
		msg = strings.ReplaceAll(cause.Error(), "\x00", "")
	}
	if len(msg) > maxBackfillError {
		msg = msg[:maxBackfillError]
	}
	// Postgres text takes neither NUL nor a rune cut in half.
	msg = strings.ToValidUTF8(msg, "")
	var attempts int
	err := s.pool.QueryRow(ctx, `INSERT INTO desk_backfill AS b (namespace, project, milestone_iid, status, attempts, error, at)
		VALUES ($1, $2, $3, 'failed', 1, $4, now())
		ON CONFLICT (namespace, project, milestone_iid) DO UPDATE SET attempts = b.attempts + 1, error = EXCLUDED.error,
			at = EXCLUDED.at
		WHERE b.status = 'failed'
		RETURNING attempts`, p.Namespace, p.Name, iid, msg).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return attempts, err
}

// ProjectListed returns when project's closed milestones were last listed
// for the backfill, the zero time if never, and whether all of them were
// backfilled then.
func (s *Store) ProjectListed(ctx context.Context, p ProjectKey) (time.Time, bool, error) {
	var at time.Time
	var complete bool
	err := s.pool.QueryRow(ctx, `SELECT listed_at, complete FROM desk_backfill_projects
		WHERE namespace = $1 AND project = $2`, p.Namespace, p.Name).Scan(&at, &complete)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	return at, complete, err
}

// SetProjectListed records that project's closed milestones were listed at
// at, and whether all of them are backfilled.
func (s *Store) SetProjectListed(ctx context.Context, p ProjectKey, at time.Time, complete bool) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO desk_backfill_projects (namespace, project, listed_at, complete)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (namespace, project) DO UPDATE SET listed_at = EXCLUDED.listed_at, complete = EXCLUDED.complete`,
		p.Namespace, p.Name, at, complete)
	return err
}

// deleteOrphanBackfill deletes the done backfill rows whose milestone is
// gone. Failed rows have no milestone and stay, so a broken milestone stays
// skipped.
func (s *Store) deleteOrphanBackfill(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM desk_backfill b WHERE b.status = 'done' AND NOT EXISTS (
		SELECT 1 FROM desk_milestones m WHERE m.namespace = b.namespace AND m.project = b.project
			AND m.iid = b.milestone_iid)`)
	return err
}
