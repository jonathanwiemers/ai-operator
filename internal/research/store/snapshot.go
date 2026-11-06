package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// The Desk's snapshot of milestones, written by the operator (the owner)
// from task status and read by the UI, so a milestone outlives its tasks.

// Follow-up states: open, opened as an issue, taken over by a follow-up
// milestone, or dismissed.
const (
	FollowUpOpen      = "open"
	FollowUpIssue     = "issue"
	FollowUpMilestone = "milestone"
	FollowUpDismissed = "dismissed"
)

// MilestoneSnapshot is what the Desk keeps of a milestone. Zero IIDs and
// times are stored as NULL. State is open or closed; Phase is the
// autopilot's phase, empty without autopilot. Owners are GitLab usernames,
// OwnerSubjects their Desk login subjects.
type MilestoneSnapshot struct {
	Project       ProjectKey
	IID           int64
	MilestoneID   int64
	Title         string
	WebURL        string
	State         string
	Phase         string
	StartedAt     time.Time
	DoneAt        time.Time
	TrackingIID   int64
	Owners        []string
	OwnerSubjects []string
	RetroNoteID   int64
	RetroText     string
	RetroPostedAt time.Time
	// Issues is the milestone's whole issue list, in order: issues not in
	// it are removed.
	Issues []IssueSnapshot
	// FollowUps are written, never removed.
	FollowUps []FollowUpSnapshot
}

// IssueSnapshot is one issue of a milestone. State is the issue's state as
// the recorder sees it.
type IssueSnapshot struct {
	IID             int64
	Title           string
	State           string
	MergeRequestIID int64
	MergeRequestURL string
}

// FollowUpSnapshot is one leftover of a milestone's retrospective. State is
// one of the FollowUp constants, TargetIID the issue or milestone it became.
type FollowUpSnapshot struct {
	Number    int
	Title     string
	Body      string
	State     string
	TargetIID int64
}

// SaveMilestone writes m with its issues and follow-ups in one transaction
// and reports whether a row changed. Only rows whose values differ are
// written, so a repeated snapshot changes nothing and keeps updated_at and
// expires_at; a changed row gets expiresAt. Start and done times, once set,
// stay, and so does a stored retrospective. Issues missing from m are
// deleted (a re-plan dropped them); follow-ups are never deleted.
func (s *Store) SaveMilestone(ctx context.Context, m MilestoneSnapshot, expiresAt time.Time) (bool, error) {
	p := m.Project
	b := &pgx.Batch{}
	// The retrospective is kept as one: once its text is stored, none of
	// its columns change.
	b.Queue(`INSERT INTO desk_milestones AS m (namespace, project, iid, milestone_id, title, web_url, state, phase,
			started_at, done_at, tracking_iid, owners, owner_subjects, retro_note_id, retro_text, retro_posted_at,
			updated_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, nullif($11::bigint, 0), $12, $13, nullif($14::bigint, 0), $15, $16, now(), $17)
		ON CONFLICT (namespace, project, iid) DO UPDATE SET
			milestone_id = EXCLUDED.milestone_id, title = EXCLUDED.title, web_url = EXCLUDED.web_url,
			state = EXCLUDED.state, phase = EXCLUDED.phase,
			started_at = coalesce(m.started_at, EXCLUDED.started_at), done_at = coalesce(m.done_at, EXCLUDED.done_at),
			tracking_iid = EXCLUDED.tracking_iid, owners = EXCLUDED.owners, owner_subjects = EXCLUDED.owner_subjects,
			retro_note_id = CASE WHEN m.retro_text <> '' THEN m.retro_note_id ELSE coalesce(EXCLUDED.retro_note_id, m.retro_note_id) END,
			retro_text = CASE WHEN m.retro_text <> '' THEN m.retro_text ELSE EXCLUDED.retro_text END,
			retro_posted_at = CASE WHEN m.retro_text <> '' THEN m.retro_posted_at ELSE coalesce(EXCLUDED.retro_posted_at, m.retro_posted_at) END,
			updated_at = EXCLUDED.updated_at, expires_at = EXCLUDED.expires_at
		WHERE (m.milestone_id, m.title, m.web_url, m.state, m.phase, m.started_at, m.done_at, m.tracking_iid,
				m.owners, m.owner_subjects, m.retro_note_id, m.retro_text, m.retro_posted_at)
			IS DISTINCT FROM (EXCLUDED.milestone_id, EXCLUDED.title, EXCLUDED.web_url, EXCLUDED.state, EXCLUDED.phase,
				coalesce(m.started_at, EXCLUDED.started_at), coalesce(m.done_at, EXCLUDED.done_at), EXCLUDED.tracking_iid,
				EXCLUDED.owners, EXCLUDED.owner_subjects,
				CASE WHEN m.retro_text <> '' THEN m.retro_note_id ELSE coalesce(EXCLUDED.retro_note_id, m.retro_note_id) END,
				CASE WHEN m.retro_text <> '' THEN m.retro_text ELSE EXCLUDED.retro_text END,
				CASE WHEN m.retro_text <> '' THEN m.retro_posted_at ELSE coalesce(EXCLUDED.retro_posted_at, m.retro_posted_at) END)`,
		p.Namespace, p.Name, m.IID, m.MilestoneID, m.Title, m.WebURL, m.State, m.Phase,
		nullTime(m.StartedAt), nullTime(m.DoneAt), m.TrackingIID, nonNil(m.Owners), nonNil(m.OwnerSubjects),
		m.RetroNoteID, m.RetroText, nullTime(m.RetroPostedAt), expiresAt)
	iids := make([]int64, 0, len(m.Issues))
	for i, is := range m.Issues {
		iids = append(iids, is.IID)
		b.Queue(`INSERT INTO desk_milestone_issues AS i (namespace, project, milestone_iid, iid, title, state,
				mr_iid, mr_url, position, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, nullif($7::bigint, 0), $8, $9, $10)
			ON CONFLICT (namespace, project, milestone_iid, iid) DO UPDATE SET
				title = EXCLUDED.title, state = EXCLUDED.state, mr_iid = EXCLUDED.mr_iid, mr_url = EXCLUDED.mr_url,
				position = EXCLUDED.position, expires_at = EXCLUDED.expires_at
			WHERE (i.title, i.state, i.mr_iid, i.mr_url, i.position)
				IS DISTINCT FROM (EXCLUDED.title, EXCLUDED.state, EXCLUDED.mr_iid, EXCLUDED.mr_url, EXCLUDED.position)`,
			p.Namespace, p.Name, m.IID, is.IID, is.Title, is.State, is.MergeRequestIID, is.MergeRequestURL, i, expiresAt)
	}
	b.Queue(`DELETE FROM desk_milestone_issues
		WHERE namespace = $1 AND project = $2 AND milestone_iid = $3 AND iid <> ALL($4::bigint[])`,
		p.Namespace, p.Name, m.IID, iids)
	for _, f := range m.FollowUps {
		b.Queue(`INSERT INTO desk_follow_ups AS f (namespace, project, milestone_iid, number, title, body, state,
				target_iid, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8::bigint, 0), $9)
			ON CONFLICT (namespace, project, milestone_iid, number) DO UPDATE SET
				title = EXCLUDED.title, body = EXCLUDED.body, state = EXCLUDED.state,
				target_iid = EXCLUDED.target_iid, expires_at = EXCLUDED.expires_at
			WHERE (f.title, f.body, f.state, f.target_iid)
				IS DISTINCT FROM (EXCLUDED.title, EXCLUDED.body, EXCLUDED.state, EXCLUDED.target_iid)`,
			p.Namespace, p.Name, m.IID, f.Number, f.Title, f.Body, f.State, f.TargetIID, expiresAt)
	}
	changed := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		br := tx.SendBatch(ctx, b)
		for range b.Len() {
			tag, err := br.Exec()
			if err != nil {
				br.Close()
				return err
			}
			changed = changed || tag.RowsAffected() > 0
		}
		return br.Close()
	})
	return changed && err == nil, err
}

// CloseMilestone marks a stored milestone closed, done at at unless it
// already has a done time, and reports whether it changed. A milestone
// without a snapshot is left alone.
func (s *Store) CloseMilestone(ctx context.Context, p ProjectKey, iid int64, at, expiresAt time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE desk_milestones
		SET state = 'closed', done_at = coalesce(done_at, $4), updated_at = now(), expires_at = $5
		WHERE namespace = $1 AND project = $2 AND iid = $3 AND (state <> 'closed' OR done_at IS NULL)`,
		p.Namespace, p.Name, iid, at, expiresAt)
	return tag.RowsAffected() > 0, err
}

// DeleteExpiredSnapshot deletes the milestones whose rows all expired before
// now, with their issues and follow-ups, and returns how many milestones. A
// milestone with an unexpired row stays whole. It runs as the owner.
func (s *Store) DeleteExpiredSnapshot(ctx context.Context, now time.Time) (int64, error) {
	var total int64
	for {
		tag, err := s.pool.Exec(ctx, `DELETE FROM desk_milestones WHERE (namespace, project, iid) IN (
			SELECT m.namespace, m.project, m.iid FROM desk_milestones m
			WHERE m.expires_at < $1
				AND NOT EXISTS (SELECT 1 FROM desk_milestone_issues i WHERE i.namespace = m.namespace
					AND i.project = m.project AND i.milestone_iid = m.iid AND i.expires_at >= $1)
				AND NOT EXISTS (SELECT 1 FROM desk_follow_ups f WHERE f.namespace = m.namespace
					AND f.project = m.project AND f.milestone_iid = m.iid AND f.expires_at >= $1)
			ORDER BY m.expires_at LIMIT $2)`, now, deleteBatch)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < deleteBatch {
			return total, nil
		}
	}
}

// nullTime is NULL for the zero time.
func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// nonNil is s, or an empty slice for nil (text[] NOT NULL).
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
