package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// The window query of the issue timeline (timeline.go builds the bars).

var (
	timelineKinds  = []string{tlRunStarted, tlRunFinished, tlHoldSet, tlHoldCleared, tlMerged, tlClosed, tlSkipped}
	timelineEnders = []string{tlMerged, tlClosed, tlSkipped}
)

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
	type issue struct {
		Namespace, Project string
		IssueIID           int64
	}
	picked, err := pgx.CollectRows(rows, pgx.RowToStructByName[issue])
	if err != nil || len(picked) == 0 {
		return nil, false, err
	}
	if truncated = len(picked) > maxTimelineIssues; truncated {
		picked = picked[:maxTimelineIssues]
	}
	var pns, pnames []string
	var iids []int64
	for _, p := range picked {
		pns, pnames, iids = append(pns, p.Namespace), append(pnames, p.Project), append(iids, p.IssueIID)
	}
	rows, _ = s.pool.Query(ctx, `SELECT `+eventColumns+` FROM history_events WHERE `+scope+`
		AND (namespace, project, issue_iid) IN (SELECT * FROM unnest($7::text[], $8::text[], $9::bigint[]))
		ORDER BY at, id`,
		nss, names, timelineKinds, q.To, now, q.Milestone, pns, pnames, iids)
	events, err = pgx.CollectRows(rows, pgx.RowToStructByName[Event])
	return events, truncated, err
}
