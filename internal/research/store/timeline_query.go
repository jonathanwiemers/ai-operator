package store

import (
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// The window query of the issue timeline (timeline.go builds the bars).

var (
	// timelineKinds pick the issues; timelineEventKinds are read of them,
	// also the CI kinds, which neither pick an issue nor end its life.
	timelineKinds      = []string{tlRunStarted, tlRunFinished, tlHoldSet, tlHoldCleared, tlMerged, tlClosed, tlSkipped}
	timelineEventKinds = slices.Concat(timelineKinds, []string{tlPush, tlCIStarted, tlCI})
	timelineEnders     = []string{tlMerged, tlClosed, tlSkipped}
)

// IssueKey names an issue of a project.
type IssueKey struct {
	Namespace, Project string
	IssueIID           int64
}

// ReviewSpan is a reviewer call of an issue: it started At and took
// DurationMS.
type ReviewSpan struct {
	Namespace, Project string
	IssueIID           int64
	At                 time.Time
	DurationMS         int64
}

// maxTimelineIssues bounds the issues TimelineEvents reads, the newest
// first. A var, so tests can lower it.
var maxTimelineIssues = 500

// TimelineQuery selects the timeline of Projects' issues in [From, To),
// of milestone Milestone only when it is not 0.
type TimelineQuery struct {
	Projects  []ProjectKey
	Milestone int64
	From, To  time.Time
}

// TimelineEvents returns the timeline events before q.To, oldest first, of
// every issue whose life overlaps [q.From, q.To): one with such an event in
// the window, or whose newest one before it does not end its life (it still
// ran, waited or was held at q.From). Truncated: more than
// maxTimelineIssues issues matched, only those with the newest events were
// read. The issues are picked on history_events_project_idx (or
// history_events_milestone_idx), their events read on
// history_events_issue_idx.
func (s *Store) TimelineEvents(ctx context.Context, q TimelineQuery, now time.Time) (events []Event, truncated bool, err error) {
	if len(q.Projects) == 0 {
		return nil, false, nil
	}
	nss, names := split(q.Projects)
	scope := `(namespace, project) IN (SELECT * FROM unnest($1::text[], $2::text[])) AND issue_iid IS NOT NULL
		AND kind = ANY($3::text[]) AND at < $4 AND expires_at + interval '0 seconds' > $5
		AND ($6::bigint = 0 OR milestone_iid = $6)`
	rows, _ := s.pool.Query(ctx, `SELECT namespace, project, issue_iid FROM (
			SELECT DISTINCT ON (namespace, project, issue_iid) namespace, project, issue_iid, kind, at, id
			FROM history_events WHERE `+scope+`
			ORDER BY namespace, project, issue_iid, at DESC, id DESC) newest
		WHERE at >= $7 OR NOT kind = ANY($8::text[])
		ORDER BY at DESC, id DESC LIMIT $9`,
		nss, names, timelineKinds, q.To, now, q.Milestone, q.From, timelineEnders, maxTimelineIssues+1)
	picked, err := pgx.CollectRows(rows, pgx.RowToStructByName[IssueKey])
	if err != nil || len(picked) == 0 {
		return nil, false, err
	}
	if truncated = len(picked) > maxTimelineIssues; truncated {
		picked = picked[:maxTimelineIssues]
	}
	pns, pnames, iids := splitIssues(picked)
	rows, _ = s.pool.Query(ctx, `SELECT `+eventColumns+` FROM history_events WHERE `+scope+`
		AND (namespace, project, issue_iid) IN (SELECT * FROM unnest($7::text[], $8::text[], $9::bigint[]))
		ORDER BY at, id`,
		nss, names, timelineEventKinds, q.To, now, q.Milestone, pns, pnames, iids)
	events, err = pgx.CollectRows(rows, pgx.RowToStructByName[Event])
	return events, truncated, err
}

// TimelineReviews returns the unexpired reviewer calls of issues, oldest
// first, on agent_calls_issue_idx; none for no issues.
func (s *Store) TimelineReviews(ctx context.Context, issues []IssueKey, now time.Time) ([]ReviewSpan, error) {
	if len(issues) == 0 {
		return nil, nil
	}
	nss, names, iids := splitIssues(issues)
	rows, _ := s.pool.Query(ctx, `SELECT namespace, project, issue_iid, at, duration_ms FROM agent_calls
		WHERE (namespace, project, issue_iid) IN (SELECT * FROM unnest($1::text[], $2::text[], $3::bigint[]))
		AND kind = 'review' AND expires_at > $4
		ORDER BY at, id`, nss, names, iids, now)
	return pgx.CollectRows(rows, pgx.RowToStructByName[ReviewSpan])
}

// splitIssues is issues as the columns of an unnest.
func splitIssues(issues []IssueKey) (namespaces, projects []string, iids []int64) {
	for _, k := range issues {
		namespaces, projects, iids = append(namespaces, k.Namespace), append(projects, k.Project), append(iids, k.IssueIID)
	}
	return namespaces, projects, iids
}
