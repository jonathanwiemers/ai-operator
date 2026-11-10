package store

import (
	"context"
	"errors"
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
// Merged counts the issues merged, skipped or (backfilled) closed, Open the
// others.
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

// milestoneSelect is the list's statement without ORDER BY: $1 and $2 are
// the projects' namespaces and names, $3 the state, empty for both. The
// extra columns come after the list's, before the spend totals; filter is
// ANDed to the WHERE clause.
func milestoneSelect(extra, filter string) string {
	const inProjects = `(namespace, project) IN (SELECT * FROM unnest($1::text[], $2::text[]))`
	return spendWith(inProjects+` AND milestone_iid IS NOT NULL`) + `
		SELECT m.namespace, m.project, m.iid, m.title, m.web_url, m.state, m.phase,
			m.started_at, m.done_at, coalesce(m.tracking_iid, 0),
			coalesce(i.total, 0), coalesce(i.merged, 0), coalesce(f.open, 0),
			` + extra + `coalesce(sp.cost, 0), coalesce(sp.unknown, 0), coalesce(sp.input, 0), coalesce(sp.output, 0),
			coalesce(sp.cache_read, 0), coalesce(sp.cache_write, 0), coalesce(sp.calls, 0),
			coalesce(sp.subagent_calls, 0), coalesce(sp.duration, 0), coalesce(sp.legacy, 0), coalesce(sp.unmetered, 0),
			coalesce(sp.checkpoints, 0), coalesce(sp.old_basis, 0)
		FROM desk_milestones m
		LEFT JOIN (
			SELECT namespace, project, milestone_iid, count(*) AS total,
				count(*) FILTER (WHERE state IN ('merged', 'skipped', 'closed')) AS merged
			FROM desk_milestone_issues WHERE ` + inProjects + ` GROUP BY 1, 2, 3
		) i ON i.namespace = m.namespace AND i.project = m.project AND i.milestone_iid = m.iid
		LEFT JOIN (
			SELECT namespace, project, milestone_iid, count(*) AS open
			FROM desk_follow_ups WHERE state = 'open' AND ` + inProjects + ` GROUP BY 1, 2, 3
		) f ON f.namespace = m.namespace AND f.project = m.project AND f.milestone_iid = m.iid
		LEFT JOIN (
			SELECT namespace, project, milestone_iid, ` + spendTotals + ` FROM spend GROUP BY 1, 2, 3
		) sp (namespace, project, milestone_iid, cost, unknown, input, output, cache_read, cache_write,
			calls, subagent_calls, duration, legacy, unmetered, checkpoints, old_basis)
			ON sp.namespace = m.namespace AND sp.project = m.project AND sp.milestone_iid = m.iid
		WHERE (m.namespace, m.project) IN (SELECT * FROM unnest($1::text[], $2::text[]))
			AND ($3 = '' OR m.state = $3)` + filter
}

// scanMilestone scans a row of milestoneSelect, extra the destinations of
// its extra columns.
func scanMilestone(row pgx.Row, extra ...any) (MilestoneRow, error) {
	var m MilestoneRow
	var started, done *time.Time
	var err error
	m.Spend, err = scanTotals(row, append([]any{&m.Project.Namespace, &m.Project.Name, &m.IID, &m.Title, &m.WebURL,
		&m.State, &m.Phase, &started, &done, &m.TrackingIID, &m.Issues, &m.Merged, &m.FollowUpsOpen}, extra...)...)
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
	rows, _ := s.pool.Query(ctx, milestoneSelect("", "")+`
		ORDER BY m.state = 'closed', CASE WHEN m.state = 'open' THEN m.started_at END NULLS LAST,
			m.done_at DESC NULLS LAST, m.namespace, m.project, m.iid
		LIMIT $4`, namespaces, names, q.State, q.Limit)
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (MilestoneRow, error) { return scanMilestone(row) })
}

// IssueFigures are one issue of a milestone with its figures. FirstRun is
// its first run.started event, nil if it never ran; MergedAt its latest
// merged event, nil if none. WallSec runs from FirstRun to MergedAt, or to
// now while it is not merged, 0 if it never ran. AgentMS sums its calls'
// durations, PausedSec the pauses of the milestone whose item is the issue
// (open ones up to now). Spend sums its calls as MilestoneRow does: without
// calls the cost is unknown.
type IssueFigures struct {
	IID          int64
	Title, State string
	MRIID        int64
	MRURL        string
	Position     int
	FirstRun     *time.Time
	MergedAt     *time.Time
	WallSec      int64
	AgentMS      int64
	PausedSec    int64
	Spend        SpendTotals
}

// FollowUpRow is a stored leftover of a milestone's retrospective, see
// FollowUpSnapshot.
type FollowUpRow struct {
	Number    int
	Title     string
	Body      string
	State     string
	TargetIID int64
}

// MilestoneDetail is one milestone with its retrospective, owners, issues
// in order and follow-ups by number. RetroPostedAt is zero and RetroNoteID
// 0 while no retrospective is stored. Issues shadows the row's issue count,
// MilestoneRow.Issues.
type MilestoneDetail struct {
	MilestoneRow
	RetroText     string
	RetroPostedAt time.Time
	RetroNoteID   int64
	Owners        []string
	OwnerSubjects []string
	Issues        []IssueFigures
	FollowUps     []FollowUpRow
}

// Milestone returns project's milestone iid with its issues' figures in a
// fixed number of queries, however many issues it has: the milestone row,
// its follow-ups, its issues with their calls, their run and merge events
// and the milestone's pauses. ErrNotFound if it has no snapshot.
func (s *Store) Milestone(ctx context.Context, project ProjectKey, iid int64, now time.Time) (MilestoneDetail, error) {
	var d MilestoneDetail
	var posted *time.Time
	var err error
	d.MilestoneRow, err = scanMilestone(s.pool.QueryRow(ctx,
		milestoneSelect(`m.retro_text, m.retro_posted_at, coalesce(m.retro_note_id, 0), m.owners, m.owner_subjects, `,
			` AND m.iid = $4`),
		[]string{project.Namespace}, []string{project.Name}, "", iid),
		&d.RetroText, &posted, &d.RetroNoteID, &d.Owners, &d.OwnerSubjects)
	if errors.Is(err, pgx.ErrNoRows) {
		return MilestoneDetail{}, ErrNotFound
	}
	if err != nil {
		return MilestoneDetail{}, err
	}
	if posted != nil {
		d.RetroPostedAt = *posted
	}
	if d.FollowUps, err = s.followUps(ctx, project, iid); err != nil {
		return MilestoneDetail{}, err
	}
	if d.Issues, err = s.issueFigures(ctx, project, iid, 0, now); err != nil {
		return MilestoneDetail{}, err
	}
	return d, nil
}

// IssueFigures returns the figures of issue in project's milestone as
// Milestone does, ErrNotFound if the milestone's snapshot lacks the issue.
func (s *Store) IssueFigures(ctx context.Context, project ProjectKey, milestone, issue int64, now time.Time) (IssueFigures, error) {
	if issue == 0 {
		return IssueFigures{}, ErrNotFound
	}
	issues, err := s.issueFigures(ctx, project, milestone, issue, now)
	if err != nil {
		return IssueFigures{}, err
	}
	if len(issues) == 0 {
		return IssueFigures{}, ErrNotFound
	}
	return issues[0], nil
}

// issueFigures returns the figures of the milestone's issues in order, of
// issue only unless it is 0, in three queries.
func (s *Store) issueFigures(ctx context.Context, project ProjectKey, milestone, issue int64, now time.Time) ([]IssueFigures, error) {
	const inScope = `namespace = $1 AND project = $2 AND milestone_iid = $3 AND ($4::bigint = 0 OR issue_iid = $4)`
	rows, _ := s.pool.Query(ctx, spendWith(inScope)+`
		SELECT i.iid, i.title, i.state, coalesce(i.mr_iid, 0), i.mr_url, i.position,
			coalesce(sp.cost, 0), coalesce(sp.unknown, 0), coalesce(sp.input, 0), coalesce(sp.output, 0),
			coalesce(sp.cache_read, 0), coalesce(sp.cache_write, 0), coalesce(sp.calls, 0),
			coalesce(sp.subagent_calls, 0), coalesce(sp.duration, 0), coalesce(sp.legacy, 0), coalesce(sp.unmetered, 0),
			coalesce(sp.checkpoints, 0), coalesce(sp.old_basis, 0)
		FROM desk_milestone_issues i
		LEFT JOIN (
			SELECT issue_iid, `+spendTotals+` FROM spend WHERE issue_iid IS NOT NULL GROUP BY 1
		) sp (issue_iid, cost, unknown, input, output, cache_read, cache_write, calls, subagent_calls, duration, legacy, unmetered, checkpoints, old_basis)
			ON sp.issue_iid = i.iid
		WHERE i.namespace = $1 AND i.project = $2 AND i.milestone_iid = $3 AND ($4::bigint = 0 OR i.iid = $4)
		ORDER BY i.position, i.iid`, project.Namespace, project.Name, milestone, issue)
	issues, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (IssueFigures, error) {
		var f IssueFigures
		var err error
		f.Spend, err = scanTotals(row, &f.IID, &f.Title, &f.State, &f.MRIID, &f.MRURL, &f.Position)
		if f.Spend.Calls == 0 && f.Spend.LegacyCostUSD == 0 {
			f.Spend.CostKnown = false
		}
		f.AgentMS = f.Spend.DurationMS
		return f, err
	})
	if err != nil || len(issues) == 0 {
		return issues, err
	}

	type times struct{ firstRun, merged *time.Time }
	rows, _ = s.pool.Query(ctx, `SELECT issue_iid, min(at) FILTER (WHERE kind = 'run.started'),
			max(at) FILTER (WHERE kind = 'merged')
		FROM history_events
		WHERE `+inScope+` AND issue_iid IS NOT NULL AND kind IN ('run.started', 'merged') AND expires_at > $5
		GROUP BY 1`, project.Namespace, project.Name, milestone, issue, now)
	byIssue := map[int64]times{}
	var iid int64
	var first, merged *time.Time
	// Copies: a scan may reuse what the pointers point to.
	clone := func(t *time.Time) *time.Time {
		if t == nil {
			return nil
		}
		v := *t
		return &v
	}
	if _, err := pgx.ForEachRow(rows, []any{&iid, &first, &merged}, func() error {
		byIssue[iid] = times{clone(first), clone(merged)}
		return nil
	}); err != nil {
		return nil, err
	}

	pauses, err := s.Pauses(ctx, project, milestone, now)
	if err != nil {
		return nil, err
	}
	paused := map[int64]int64{}
	for _, p := range pauses.Pauses {
		if p.Item != 0 {
			paused[p.Item] += p.Seconds
		}
	}

	for i := range issues {
		f := &issues[i]
		t := byIssue[f.IID]
		f.FirstRun, f.MergedAt = t.firstRun, t.merged
		f.PausedSec = paused[f.IID]
		if f.FirstRun != nil {
			end := now
			if f.MergedAt != nil {
				end = *f.MergedAt
			}
			f.WallSec = max(0, int64(end.Sub(*f.FirstRun).Seconds()))
		}
	}
	return issues, nil
}
