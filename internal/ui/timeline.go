package ui

import (
	"cmp"
	"math"
	"net/http"
	"slices"
	"strconv"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/desklink"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// maxTimelineWindow is the longest window of the timeline's window mode.
const maxTimelineWindow = 7 * 24 * time.Hour

// TimelineSegmentView is a part of an issue's bar: running, in review (a
// reviewer call), in CI (a pipeline of its merge request), waiting or held
// from Start to End, End nil while it goes on.
type TimelineSegmentView struct {
	Kind  string     `json:"kind" tstype:"'running' | 'review' | 'ci' | 'waiting' | 'held'"`
	Start time.Time  `json:"start"`
	End   *time.Time `json:"end,omitempty"`
}

// TimelineRowView is one issue's bar. Titles and Wave come from the live
// plan of the milestone's spec task (else the implement task's issue);
// empty and 0 when unknown. DeskURL is the issue page of the Desk, or the
// task page for an issue without a milestone ("" without a task).
type TimelineRowView struct {
	Namespace      string                `json:"namespace"`
	Project        string                `json:"project"`
	MilestoneIID   int64                 `json:"milestoneIID"`
	MilestoneTitle string                `json:"milestoneTitle"`
	IssueIID       int64                 `json:"issueIID"`
	IssueTitle     string                `json:"issueTitle"`
	Wave           int                   `json:"wave"`
	Segments       []TimelineSegmentView `json:"segments"`
	MergedAt       *time.Time            `json:"mergedAt,omitempty"`
	SkippedAt      *time.Time            `json:"skippedAt,omitempty"`
	DeskURL        string                `json:"deskURL"`
}

// TimelineView is the answer of GET /api/v1/timeline: the bars of the
// issues in [From, To), sorted by project, wave, first start and issue.
// History is false without the history database (and Rows empty).
// Truncated: more issues matched than the store reads.
type TimelineView struct {
	History   bool              `json:"history"`
	From      time.Time         `json:"from"`
	To        time.Time         `json:"to"`
	Now       time.Time         `json:"now"`
	Rows      []TimelineRowView `json:"rows"`
	Truncated bool              `json:"truncated"`
}

// timeline answers GET /api/v1/timeline. With ?project=ns/name&milestone=iid
// it is that milestone's issues from its first run to now; else the issues
// of the viewer's projects (or ?project=) in ?from=&to= (RFC 3339, at most
// 7 days). It only reads.
func (s *Server) timeline(w http.ResponseWriter, r *http.Request) {
	visible, tasks, ok := s.visibleProjects(w, r)
	if !ok {
		return
	}
	visible, ok = projectFilter(w, r, visible)
	if !ok {
		return
	}
	now := time.Now().UTC()
	q, from, to, err := timelineQuery(r, now)
	if err != "" {
		badRequest(w, err)
		return
	}
	v := TimelineView{From: from, To: to, Now: now, Rows: []TimelineRowView{}}
	if !s.cfg.Research.Enabled() {
		writeJSON(w, http.StatusOK, v)
		return
	}
	st := s.costStore()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "the history database is not connected yet")
		return
	}
	for _, ap := range visible {
		q.Projects = append(q.Projects, store.ProjectKey{Namespace: ap.Namespace, Name: ap.Name})
	}
	events, truncated, e := st.TimelineEvents(r.Context(), q, now)
	if e != nil {
		s.internalError(w, e)
		return
	}
	reviews, e := st.TimelineReviews(r.Context(), timelineIssues(events), now)
	if e != nil {
		s.internalError(w, e)
		return
	}
	v.History, v.Truncated = true, truncated
	v.Rows = timelineRows(store.BuildTimeline(events, reviews, q.From, q.To, now), visible, tasks)
	if q.Milestone != 0 { // from the milestone's first run
		v.From = now
		for _, row := range v.Rows {
			if len(row.Segments) > 0 && row.Segments[0].Start.Before(v.From) {
				v.From = row.Segments[0].Start
			}
		}
	}
	writeJSON(w, http.StatusOK, v)
}

// timelineIssues is the distinct issues of events, in their order.
func timelineIssues(events []store.Event) []store.IssueKey {
	var out []store.IssueKey
	seen := map[store.IssueKey]bool{}
	for _, e := range events {
		k := store.IssueKey{Namespace: e.Namespace, Project: e.Project, IssueIID: e.IssueIID}
		if e.IssueIID != 0 && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// timelineQuery reads the mode's parameters: the store query (without
// projects) and the window the view states, or the 400's message.
func timelineQuery(r *http.Request, now time.Time) (q store.TimelineQuery, from, to time.Time, msg string) {
	qs := r.URL.Query()
	if m := qs.Get("milestone"); m != "" {
		iid, err := strconv.ParseInt(m, 10, 64)
		switch {
		case err != nil || iid <= 0:
			return q, from, to, "milestone must be a milestone iid"
		case qs.Get("project") == "":
			return q, from, to, "milestone needs project"
		case qs.Get("from") != "" || qs.Get("to") != "":
			return q, from, to, "milestone takes no from or to"
		}
		// The whole milestone: no lower bound, up to now.
		return store.TimelineQuery{Milestone: iid, To: now}, now, now, ""
	}
	if qs.Get("from") == "" || qs.Get("to") == "" {
		return q, from, to, "from and to are required without milestone"
	}
	from, err1 := time.Parse(time.RFC3339, qs.Get("from"))
	to, err2 := time.Parse(time.RFC3339, qs.Get("to"))
	switch {
	case err1 != nil || err2 != nil:
		return q, from, to, "from and to must be RFC 3339 times"
	case !to.After(from):
		return q, from, to, "to must be after from"
	case to.Sub(from) > maxTimelineWindow:
		return q, from, to, "the window may be at most 7 days"
	}
	from, to = from.UTC(), to.UTC()
	return store.TimelineQuery{From: from, To: to}, from, to, ""
}

// timelineRows turns the store's bars into rows with the titles and waves
// of the projects' live tasks, sorted by project, wave (unknown last), first
// start and issue.
func timelineRows(bars []store.TimelineIssue, projects map[string]*aiv1.AgentProject, tasks []aiv1.AgentTask) []TimelineRowView {
	type msKey struct {
		project string
		iid     int64
	}
	specs := map[msKey]*aiv1.AgentTask{}
	waves := map[msKey]map[int64]int{}
	impls := map[msKey]map[int64]*aiv1.AgentTask{} // by milestone (0: none), then issue
	for i := range tasks {
		t := &tasks[i]
		k := msKey{project: t.Namespace + "/" + t.Spec.ProjectRef}
		if projects[k.project] == nil {
			continue
		}
		if t.Spec.Milestone != nil {
			k.iid = t.Spec.Milestone.IID
		}
		switch {
		case t.Spec.Type == aiv1.TaskTypeSpec && k.iid != 0:
			specs[k], waves[k] = t, planWaves(t.Status.Plan)
		case t.Spec.Type == aiv1.TaskTypeImplement && t.Spec.Issue != nil:
			if impls[k] == nil {
				impls[k] = map[int64]*aiv1.AgentTask{}
			}
			impls[k][t.Spec.Issue.IID] = t
		}
	}
	rows := make([]TimelineRowView, 0, len(bars))
	for _, b := range bars {
		k := msKey{b.Namespace + "/" + b.Project, b.MilestoneIID}
		row := TimelineRowView{Namespace: b.Namespace, Project: b.Project, MilestoneIID: b.MilestoneIID, IssueIID: b.IssueIID,
			Wave: waves[k][b.IssueIID], Segments: make([]TimelineSegmentView, 0, len(b.Segments)),
			MergedAt: b.MergedAt, SkippedAt: b.SkippedAt}
		for _, sg := range b.Segments {
			row.Segments = append(row.Segments, TimelineSegmentView{Kind: sg.Kind, Start: sg.Start, End: sg.End})
		}
		if st := specs[k]; st != nil {
			row.MilestoneTitle = st.Spec.Milestone.Title
			if i := slices.IndexFunc(st.Status.Plan, func(pi aiv1.PlannedIssue) bool { return pi.IID == b.IssueIID }); i >= 0 {
				row.IssueTitle = st.Status.Plan[i].Title
			}
		}
		impl := impls[k][b.IssueIID]
		if row.IssueTitle == "" && impl != nil {
			row.IssueTitle = impl.Spec.Issue.Title
		}
		switch {
		case b.MilestoneIID != 0:
			row.DeskURL = desklink.IssuePath(b.Namespace, b.Project, b.MilestoneIID, b.IssueIID)
		case impl != nil:
			row.DeskURL = desklink.TaskPath(b.Namespace, b.Project, impl.Name)
		}
		rows = append(rows, row)
	}
	slices.SortStableFunc(rows, func(a, b TimelineRowView) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Project, b.Project),
			cmp.Compare(waveRank(a.Wave), waveRank(b.Wave)), rowStart(a).Compare(rowStart(b)),
			cmp.Compare(a.IssueIID, b.IssueIID))
	})
	return rows
}

// waveRank sorts the unknown wave 0 after every known one.
func waveRank(w int) int {
	if w == 0 {
		return math.MaxInt
	}
	return w
}

// rowStart is a row's first start: its first segment, else its marker.
func rowStart(r TimelineRowView) time.Time {
	switch {
	case len(r.Segments) > 0:
		return r.Segments[0].Start
	case r.MergedAt != nil:
		return *r.MergedAt
	case r.SkippedAt != nil:
		return *r.SkippedAt
	}
	return time.Time{}
}
