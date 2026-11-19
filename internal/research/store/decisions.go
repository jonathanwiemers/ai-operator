package store

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The decisions of planned milestones, copied by the operator (the owner)
// from the bot's decision comments on GitLab and read by the UI. GitLab
// stays the source of truth: each read of an issue replaces its rows.

// Kinds of a decision: posted by the planner (D<issue>-P<n>) or by an
// implementer (D<issue>-<n>).
const (
	DecisionPlanner     = "planner"
	DecisionImplementer = "implementer"
)

// Sources of a revisit request: a human comment, or a follow-up issue the bot
// opened for it.
const (
	RevisitComment  = "comment"
	RevisitFollowUp = "followUp"
)

// Decision states a query filters on; anything else is DecisionStateAll.
const (
	DecisionStateAll        = "all"
	DecisionStateActive     = "active"
	DecisionStateSuperseded = "superseded"
)

// MaxDecisions is the most decisions Decisions returns.
const MaxDecisions = 200

// DecisionRow is one stored decision. Supersedes is the ID it replaces, ""
// when none; Superseded is the marker on its comment. URL is the GitLab
// comment or issue it was read from, IssueState the issue's state (opened or
// closed). Seq is the number after "-" or "-P" of the ID, for sorting:
// SaveIssueDecisions sets it from ID, and MilestoneIID and IssueIID from its
// IssueDecisions.
type DecisionRow struct {
	MilestoneIID int64
	ID           string `db:"decision_id"`
	IssueIID     int64
	Kind         string
	Seq          int
	Title        string
	Question     string
	Choice       string
	Why          string
	Alternatives []string
	Supersedes   string
	Superseded   bool
	RunID        string
	URL          string
	IssueTitle   string
	IssueState   string
	PostedAt     time.Time
}

// RevisitRequestRow is one request to revisit DecisionID. SourceIssueIID is
// the issue it was read from (the commented issue or the follow-up), Source
// one of the Revisit constants. FollowUpIID is the follow-up issue (0 for a
// comment), FollowUpState its state (opened, closed or ""). Applied is only
// set by Decisions: a decision that supersedes DecisionID exists.
// SaveIssueDecisions sets MilestoneIID and SourceIssueIID from its
// IssueDecisions and ignores Applied.
type RevisitRequestRow struct {
	MilestoneIID   int64
	SourceIssueIID int64
	DecisionID     string
	Source         string
	RequestedBy    string
	Instruction    string
	URL            string
	FollowUpIID    int64
	FollowUpState  string
	At             time.Time
	Applied        bool
}

// IssueDecisions is everything read from one issue of a milestone: its
// decisions and the revisit requests found on it.
type IssueDecisions struct {
	Project      ProjectKey
	MilestoneIID int64
	IssueIID     int64
	Decisions    []DecisionRow
	Revisits     []RevisitRequestRow
}

// SaveIssueDecisions replaces the stored decisions of d's issue in its
// milestone, and the revisit requests read from that issue, by d in one
// transaction, with expiresAt. A re-plan that renumbered the planner's
// decisions leaves none of the old IDs. A decision ID stored for another
// issue or milestone moves to this one.
func (s *Store) SaveIssueDecisions(ctx context.Context, d IssueDecisions, expiresAt time.Time) error {
	p := d.Project
	b := &pgx.Batch{}
	b.Queue(`DELETE FROM desk_decisions
		WHERE namespace = $1 AND project = $2 AND milestone_iid = $3 AND issue_iid = $4`,
		p.Namespace, p.Name, d.MilestoneIID, d.IssueIID)
	b.Queue(`DELETE FROM desk_decision_revisits WHERE namespace = $1 AND project = $2 AND source_issue_iid = $3`,
		p.Namespace, p.Name, d.IssueIID)
	for _, r := range d.Decisions {
		b.Queue(`INSERT INTO desk_decisions (namespace, project, milestone_iid, decision_id, issue_iid, kind, seq,
				title, question, choice, why, alternatives, supersedes, superseded, run_id, url, issue_title,
				issue_state, posted_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
			ON CONFLICT (namespace, project, decision_id) DO UPDATE SET
				milestone_iid = EXCLUDED.milestone_iid, issue_iid = EXCLUDED.issue_iid, kind = EXCLUDED.kind,
				seq = EXCLUDED.seq, title = EXCLUDED.title, question = EXCLUDED.question, choice = EXCLUDED.choice,
				why = EXCLUDED.why, alternatives = EXCLUDED.alternatives, supersedes = EXCLUDED.supersedes,
				superseded = EXCLUDED.superseded, run_id = EXCLUDED.run_id, url = EXCLUDED.url,
				issue_title = EXCLUDED.issue_title, issue_state = EXCLUDED.issue_state,
				posted_at = EXCLUDED.posted_at, expires_at = EXCLUDED.expires_at`,
			p.Namespace, p.Name, d.MilestoneIID, r.ID, d.IssueIID, r.Kind, decisionSeq(r.ID), r.Title, r.Question,
			r.Choice, r.Why, nonNil(r.Alternatives), r.Supersedes, r.Superseded, r.RunID, r.URL, r.IssueTitle,
			r.IssueState, r.PostedAt, expiresAt)
	}
	for _, r := range d.Revisits {
		b.Queue(`INSERT INTO desk_decision_revisits (namespace, project, milestone_iid, source_issue_iid,
				decision_id, source, requested_by, instruction, url, follow_up_iid, follow_up_state, at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, nullif($10::bigint, 0), $11, $12, $13)`,
			p.Namespace, p.Name, d.MilestoneIID, d.IssueIID, r.DecisionID, r.Source, r.RequestedBy, r.Instruction,
			r.URL, r.FollowUpIID, r.FollowUpState, r.At, expiresAt)
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return tx.SendBatch(ctx, b).Close()
	})
}

// decisionSeq is the number after the last "-" (and a "P") of a decision ID:
// 3 for D42-3 and D42-P3, 0 when there is none.
func decisionSeq(id string) int {
	if i := strings.LastIndex(id, "-"); i >= 0 {
		seq, _ := strconv.Atoi(strings.TrimPrefix(id[i+1:], "P"))
		return seq
	}
	return 0
}

// DeleteExpiredDecisions deletes the decisions and revisit requests that
// expired before now and returns how many rows. It runs as the owner.
func (s *Store) DeleteExpiredDecisions(ctx context.Context, now time.Time) (int64, error) {
	return s.deleteExpiredRows(ctx, now, "desk_decisions", "desk_decision_revisits")
}

// DecisionQuery selects the decisions of Project: of milestone Milestone (0
// for all), matching Q (case-insensitive, in the ID, title, question,
// choice, why or alternatives; "" for all), in State (a DecisionState
// constant), at most Limit (0 or more than MaxDecisions is MaxDecisions).
type DecisionQuery struct {
	Project   ProjectKey
	Milestone int64
	Q         string
	State     string
	Limit     int
}

// DecisionResult is a stored decision as it reads now. SupersededBy is the
// decision that supersedes it, "" when none; Superseded is true when there
// is one or its comment is marked superseded. Revisits are the requests to
// revisit it, oldest first.
type DecisionResult struct {
	DecisionRow
	SupersededBy string
	Revisits     []RevisitRequestRow `db:"-"`
}

// Decisions returns the unexpired decisions q selects: newest milestones
// first, then by issue in the milestone's plan order (issues the plan does
// not list after, by IID), then the planner's before the implementer's, each
// by number.
func (s *Store) Decisions(ctx context.Context, q DecisionQuery) ([]DecisionResult, error) {
	limit := q.Limit
	if limit <= 0 || limit > MaxDecisions {
		limit = MaxDecisions
	}
	pattern := ""
	if t := strings.TrimSpace(q.Q); t != "" {
		pattern = "%" + likeEscaper.Replace(t) + "%"
	}
	state := q.State
	if state != DecisionStateActive && state != DecisionStateSuperseded {
		state = DecisionStateAll
	}
	p := q.Project
	rows, _ := s.pool.Query(ctx, `SELECT d.milestone_iid, d.decision_id, d.issue_iid, d.kind, d.seq, d.title,
			d.question, d.choice, d.why, d.alternatives, d.supersedes, d.superseded OR s.decision_id IS NOT NULL AS superseded,
			d.run_id, d.url, d.issue_title, d.issue_state, d.posted_at, coalesce(s.decision_id, '') AS superseded_by
		FROM desk_decisions d
		LEFT JOIN LATERAL (SELECT s.decision_id FROM desk_decisions s
			WHERE s.namespace = d.namespace AND s.project = d.project AND s.supersedes = d.decision_id
				AND s.expires_at > now()
			ORDER BY s.posted_at, s.decision_id LIMIT 1) s ON true
		LEFT JOIN desk_milestone_issues i ON i.namespace = d.namespace AND i.project = d.project
			AND i.milestone_iid = d.milestone_iid AND i.iid = d.issue_iid
		WHERE d.namespace = $1 AND d.project = $2 AND ($3::bigint = 0 OR d.milestone_iid = $3) AND d.expires_at > now()
			AND ($4::text = '' OR d.decision_id ILIKE $4 OR d.title ILIKE $4 OR d.question ILIKE $4
				OR d.choice ILIKE $4 OR d.why ILIKE $4 OR EXISTS (SELECT 1 FROM unnest(d.alternatives) a WHERE a ILIKE $4))
			AND ($5::text = 'all' OR ($5 = 'superseded') = (d.superseded OR s.decision_id IS NOT NULL))
		ORDER BY d.milestone_iid DESC, i.position NULLS LAST, d.issue_iid, d.kind <> 'planner', d.seq, d.decision_id
		LIMIT $6`, p.Namespace, p.Name, q.Milestone, pattern, state, limit)
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[DecisionResult])
	if err != nil || len(out) == 0 {
		return out, err
	}
	ids := make([]string, 0, len(out))
	at := make(map[string]int, len(out))
	for i, d := range out {
		ids = append(ids, d.ID)
		at[d.ID] = i
	}
	rows, _ = s.pool.Query(ctx, `SELECT r.milestone_iid, r.source_issue_iid, r.decision_id, r.source, r.requested_by,
			r.instruction, r.url, coalesce(r.follow_up_iid, 0) AS follow_up_iid, r.follow_up_state, r.at,
			EXISTS (SELECT 1 FROM desk_decisions s WHERE s.namespace = r.namespace AND s.project = r.project
				AND s.supersedes = r.decision_id AND s.expires_at > now()) AS applied
		FROM desk_decision_revisits r
		WHERE r.namespace = $1 AND r.project = $2 AND r.decision_id = ANY($3::text[]) AND r.expires_at > now()
		ORDER BY r.at, r.source_issue_iid`, p.Namespace, p.Name, ids)
	revisits, err := pgx.CollectRows(rows, pgx.RowToStructByName[RevisitRequestRow])
	if err != nil {
		return nil, err
	}
	for _, r := range revisits {
		d := &out[at[r.DecisionID]]
		d.Revisits = append(d.Revisits, r)
	}
	return out, nil
}

// likeEscaper escapes the wildcards of a LIKE pattern (the escape character
// is the default backslash).
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
