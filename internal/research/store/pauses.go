package store

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The autopilot pause events the operator writes (internal/history).
const (
	pausedKind  = "autopilot.paused"
	resumedKind = "autopilot.resumed"
	// maxPauseEvents bounds what Pauses reads of one milestone.
	maxPauseEvents = 2000
)

// Pause is one autopilot pause of a milestone. Until is nil while it is
// open; Seconds then counts up to now.
type Pause struct {
	Kind       string
	Reason     string
	Item       int64
	Since      time.Time
	Until      *time.Time
	ResolvedBy string
	// Via is how it ended (aiv1.PauseResumeVia), "" for an open pause and
	// for one that ended before it was recorded.
	Via     string
	Seconds int64
}

// PauseKindTotal sums the pauses of one kind.
type PauseKindTotal struct {
	Count int
	Sec   int64
}

// PauseTotals sum pauses: PausedSec all of their time (open ones up to
// now), MeanResumeSec the mean time to resume of the ended ones and
// ByResolver who ended them, ByVia how ("unknown" when not recorded).
type PauseTotals struct {
	Count         int
	PausedSec     int64
	ByKind        map[string]PauseKindTotal
	MeanResumeSec int64
	ByResolver    map[string]int
	ByVia         map[string]int
}

// PauseHistory is a milestone's pauses, oldest first, and their totals.
type PauseHistory struct {
	Pauses []Pause
	Totals PauseTotals
}

// Pauses returns the autopilot pauses of project's milestone within the
// retention (the events not expired at now): the newest maxPauseEvents
// events, so an open pause is never cut off.
func (s *Store) Pauses(ctx context.Context, project ProjectKey, milestone int64, now time.Time) (PauseHistory, error) {
	rows, _ := s.pool.Query(ctx, `SELECT `+eventColumns+` FROM history_events
		WHERE namespace = $1 AND project = $2 AND milestone_iid = $3 AND kind = ANY($4::text[]) AND expires_at > $5
		ORDER BY at DESC, id DESC LIMIT $6`,
		project.Namespace, project.Name, milestone, []string{pausedKind, resumedKind}, now, maxPauseEvents)
	events, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Event, error) {
		var e Event
		err := row.Scan(&e.ID, &e.Key, &e.At, &e.Namespace, &e.Project, &e.MilestoneIID, &e.IssueIID,
			&e.MergeRequestIID, &e.Task, &e.Kind, &e.Actor, &e.Summary, &e.Data, &e.ExpiresAt)
		return e, err
	})
	if err != nil {
		return PauseHistory{}, err
	}
	slices.Reverse(events) // the newest maxPauseEvents, oldest first
	return pairPauses(events, now), nil
}

// pairPauses pairs paused and resumed events (oldest first) into pauses.
// The two events of a pause share their key but for the kind segment
// ("task/<uid>/autopilot.paused/<start>/<kind>"), and a task has one pause
// at a time. A resumed event whose paused one is gone (expired, or keyed by
// another resource version) ends its task's open pause of the same kind,
// else it is a pause of its own that started pausedSec before it. A pause
// still open when its task pauses again ended then, resolver unknown.
// Pauses are ordered by their start.
func pairPauses(events []Event, now time.Time) PauseHistory {
	h := PauseHistory{Pauses: []Pause{}, Totals: PauseTotals{ByKind: map[string]PauseKindTotal{}, ByResolver: map[string]int{}, ByVia: map[string]int{}}}
	type open struct {
		id string
		i  int
	}
	byTask := map[string]open{}
	// byID is every pause by its key: a resumed event at the same time as
	// the next paused one may come after it and still names its pause.
	byID := map[string]int{}
	for _, e := range events {
		task, rest, ok := strings.Cut(e.Key, "/"+e.Kind+"/")
		if !ok {
			continue // not the operator's key
		}
		id := task + "/" + rest
		p := Pause{Kind: dataString(e.Data, "kind"), Reason: dataString(e.Data, "reason"), Item: dataInt(e.Data, "item")}
		cur, isOpen := byTask[task]
		at := e.At
		switch e.Kind {
		case pausedKind:
			if isOpen {
				h.Pauses[cur.i].Until = &at
			}
			p.Since = at
			byID[id] = len(h.Pauses)
			byTask[task] = open{id: id, i: len(h.Pauses)}
			h.Pauses = append(h.Pauses, p)
		case resumedKind:
			if j, ok := byID[id]; ok && (!isOpen || cur.i != j) {
				// Its pause was ended by the next one: only the resolver is new.
				if h.Pauses[j].ResolvedBy == "" {
					h.Pauses[j].ResolvedBy, h.Pauses[j].Via = dataString(e.Data, "resolvedBy"), dataString(e.Data, "via")
				}
				continue
			}
			i := cur.i
			if isOpen && (cur.id == id || h.Pauses[i].Kind == p.Kind) {
				delete(byTask, task)
			} else {
				i = len(h.Pauses)
				p.Since = at.Add(-time.Duration(dataInt(e.Data, "pausedSec")) * time.Second)
				h.Pauses = append(h.Pauses, p)
			}
			h.Pauses[i].Until, h.Pauses[i].ResolvedBy, h.Pauses[i].Via = &at, dataString(e.Data, "resolvedBy"), dataString(e.Data, "via")
		}
	}
	sort.SliceStable(h.Pauses, func(i, j int) bool { return h.Pauses[i].Since.Before(h.Pauses[j].Since) })
	var resolved, resumeSec int64
	for i := range h.Pauses {
		p := &h.Pauses[i]
		if p.Kind == "" {
			p.Kind = "unknown"
		}
		end := now
		if p.Until != nil {
			end = *p.Until
		}
		p.Seconds = max(0, int64(end.Sub(p.Since).Seconds()))
		t := &h.Totals
		t.Count++
		t.PausedSec += p.Seconds
		k := t.ByKind[p.Kind]
		k.Count++
		k.Sec += p.Seconds
		t.ByKind[p.Kind] = k
		if p.Until != nil {
			resolved++
			resumeSec += p.Seconds
			by := p.ResolvedBy
			if by == "" {
				by = "unknown"
			}
			t.ByResolver[by]++
			via := p.Via
			if via == "" {
				via = "unknown"
			}
			t.ByVia[via]++
		}
	}
	if resolved > 0 {
		h.Totals.MeanResumeSec = resumeSec / resolved
	}
	return h
}

func dataString(d map[string]any, key string) string {
	s, _ := d[key].(string)
	return s
}

// dataInt reads a number of an event's data: float64 from the database,
// int64 when the operator built it.
func dataInt(d map[string]any, key string) int64 {
	switch v := d[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}
