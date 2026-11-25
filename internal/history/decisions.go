package history

import (
	"context"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// decKey is an issue in the decision queue.
type decKey struct {
	p   store.ProjectKey
	iid int64
}

// RecordIssueDecisions queues the decisions read from one issue, replacing
// queued ones of the issue: written, they replace the issue's stored set. It
// never blocks: with the queue full of other issues it is dropped.
func (r *Recorder) RecordIssueDecisions(d store.IssueDecisions) {
	if r == nil || !r.started.Load() {
		return
	}
	k := decKey{d.Project, d.IssueIID}
	r.decMu.Lock()
	defer r.decMu.Unlock()
	if _, ok := r.decs[k]; !ok && len(r.decs) >= queueSize {
		r.droppedDecs.Add(1)
		return
	}
	r.decs[k] = d
}

// decisionEvents is one decision.recorded event per decision of d. The key
// holds the decision ID, unique per project, so a re-save or a move to another
// issue never writes a second one.
func (r *Recorder) decisionEvents(d store.IssueDecisions, expires time.Time) []store.Event {
	events := make([]store.Event, 0, len(d.Decisions))
	for _, row := range d.Decisions {
		at := row.PostedAt
		if at.IsZero() {
			at = r.clock.Now()
		}
		data := map[string]any{"id": row.ID, "title": row.Title, "choice": row.Choice, "kind": row.Kind}
		if row.Supersedes != "" {
			data["supersedes"] = row.Supersedes
		}
		events = append(events, store.Event{
			Key:          "decision/" + d.Project.Namespace + "/" + d.Project.Name + "/" + row.ID,
			At:           at,
			Namespace:    d.Project.Namespace,
			Project:      d.Project.Name,
			MilestoneIID: d.MilestoneIID,
			IssueIID:     d.IssueIID,
			Kind:         KindDecisionRecorded,
			Summary:      row.ID + " " + row.Title,
			Data:         data,
			ExpiresAt:    expires,
		})
	}
	return events
}

// writeDecisions writes the queued decisions, with the expiry of the
// milestone snapshots, and empties the queue.
func (r *Recorder) writeDecisions(ctx context.Context) {
	r.decMu.Lock()
	decs := r.decs
	if len(decs) > 0 {
		r.decs = map[decKey]store.IssueDecisions{}
	}
	r.decMu.Unlock()
	expires := r.clock.Now().Add(r.retention)
	var lost int
	var last error
	for _, d := range decs {
		if err := r.db.Store().SaveIssueDecisions(ctx, d, expires); err != nil {
			lost, last = lost+1, err
			continue
		}
		if _, err := r.db.Store().InsertEvents(ctx, r.decisionEvents(d, expires)); err != nil && ctx.Err() == nil {
			r.log.Error(err, "decision events lost", "issue", d.IssueIID)
		}
	}
	if lost > 0 && ctx.Err() == nil {
		r.log.Error(last, "decisions lost", "issues", lost)
	}
	if n := r.droppedDecs.Swap(0); n > 0 {
		r.log.Info("decision queue full, decisions dropped", "issues", n)
	}
}
