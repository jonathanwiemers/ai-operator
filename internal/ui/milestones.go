package ui

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// MilestoneState is where a milestone row stands.
type MilestoneState string

const (
	// MilestoneStateRunning: autopilot works on it.
	MilestoneStateRunning MilestoneState = "running"
	// MilestoneStatePaused: autopilot paused.
	MilestoneStatePaused MilestoneState = "paused"
	// MilestoneStateWaiting: autopilot waits for its turn to be planned.
	MilestoneStateWaiting MilestoneState = "waiting"
	// MilestoneStateDone: autopilot is done or the milestone is closed.
	MilestoneStateDone MilestoneState = "done"
	// MilestoneStatePlanned: a spec milestone without autopilot.
	MilestoneStatePlanned MilestoneState = "planned"
)

// MilestoneProgress counts a milestone's issues: merged (or skipped) of
// total, open the others.
type MilestoneProgress struct {
	Merged int `json:"merged"`
	Total  int `json:"total"`
	Open   int `json:"open"`
}

// MilestoneRow is one milestone of the dashboard's lists. State and
// progress come from the live tasks, spend from agent_calls (the tasks'
// costs without the history database). Phase is the autopilot's, in lower
// case, empty without autopilot. FollowUpsOpen are the retrospective's
// leftovers still open. NeedsYou: the milestone has an Inbox item. Stored:
// its tasks are gone, the row is the snapshot's.
type MilestoneRow struct {
	Namespace     string            `json:"namespace"`
	Project       string            `json:"project"`
	IID           int64             `json:"iid"`
	Title         string            `json:"title"`
	WebURL        string            `json:"webURL"`
	State         MilestoneState    `json:"state"`
	Phase         string            `json:"phase,omitempty"`
	Progress      MilestoneProgress `json:"progress"`
	FollowUpsOpen int               `json:"followUpsOpen"`
	Cost          `tstype:",extends"`
	StartedAt     *time.Time `json:"startedAt,omitempty"`
	DoneAt        *time.Time `json:"doneAt,omitempty"`
	NeedsYou      bool       `json:"needsYou"`
	Stored        bool       `json:"stored"`
}

// MilestoneList is the answer of GET /api/v1/milestones. History: the
// history database answered, so done milestones whose tasks are gone are
// in it.
type MilestoneList struct {
	Milestones []MilestoneRow `json:"milestones"`
	History    bool           `json:"history"`
}

// Limits of GET /api/v1/milestones.
const (
	defaultMilestoneRows = 50
	maxMilestoneRows     = store.MaxMilestones
)

// milestoneLister reads the snapshot's milestone rows (the history store).
type milestoneLister interface {
	Milestones(ctx context.Context, q store.MilestoneQuery) ([]store.MilestoneRow, error)
}

// milestoneStore is the reader of milestone rows, nil without the history
// database.
func (s *Server) milestoneStore() milestoneLister {
	if s.milestoneRows != nil {
		return s.milestoneRows
	}
	if st := s.spendStore(); st != nil {
		return st
	}
	return nil
}

// milestones lists the milestones of the projects the user may see: those
// with live spec tasks and, with the history database, the done ones whose
// tasks are gone. state is open (default), done or all; limit caps the rows.
func (s *Server) milestones(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := cmp.Or(q.Get("state"), "open")
	if state != "open" && state != "done" && state != "all" {
		badRequest(w, "state must be open, done or all")
		return
	}
	limit := defaultMilestoneRows
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n <= 0 {
			badRequest(w, "limit must be a positive number")
			return
		}
		limit = min(n, maxMilestoneRows)
	}
	visible, tasks, ok := s.visibleProjects(w, r)
	if !ok {
		return
	}
	now := time.Now()
	live := liveMilestoneRows(visible, tasks, now)

	out := MilestoneList{Milestones: []MilestoneRow{}}
	var stored []store.MilestoneRow
	if st := s.milestoneStore(); st != nil {
		keys := make([]store.ProjectKey, 0, len(visible))
		for _, ap := range visible {
			keys = append(keys, store.ProjectKey{Namespace: ap.Namespace, Name: ap.Name})
		}
		var err error
		// All states: an open snapshot may be done live, a closed one may
		// still have its tasks; the filter applies after the merge.
		if stored, err = st.Milestones(r.Context(), store.MilestoneQuery{Projects: keys, Limit: store.MaxMilestones}); err != nil {
			s.log.Error(err, "cannot read the milestones' snapshot")
			stored = nil
		} else {
			out.History = true
		}
	}
	for _, m := range mergeMilestoneRows(live, stored) {
		if state == "all" || (state == "done") == (m.State == MilestoneStateDone) {
			out.Milestones = append(out.Milestones, m)
		}
	}
	sortMilestoneRows(out.Milestones)
	if len(out.Milestones) > limit {
		out.Milestones = out.Milestones[:limit]
	}
	writeJSON(w, http.StatusOK, out)
}

// visibleProjects lists the projects the user may see, by namespace/name,
// and all tasks, once each.
func (s *Server) visibleProjects(w http.ResponseWriter, r *http.Request) (map[string]*aiv1.AgentProject, []aiv1.AgentTask, bool) {
	p := principalOf(r)
	var projects aiv1.AgentProjectList
	if err := s.reader.List(r.Context(), &projects); err != nil {
		s.internalError(w, err)
		return nil, nil, false
	}
	var tasks aiv1.AgentTaskList
	if err := s.reader.List(r.Context(), &tasks); err != nil {
		s.internalError(w, err)
		return nil, nil, false
	}
	visible := map[string]*aiv1.AgentProject{}
	for i := range projects.Items {
		ap := &projects.Items[i]
		if s.authz.CanView(p, ap.Namespace, ap.Name) {
			visible[ap.Namespace+"/"+ap.Name] = ap
		}
	}
	return visible, tasks.Items, true
}

// milestoneKey is a milestone across projects.
type milestoneKey struct {
	namespace, project string
	iid                int64
}

// liveMilestoneRows builds a row for every spec task of the projects, with
// the tasks' costs, needsYou from the Inbox.
func liveMilestoneRows(projects map[string]*aiv1.AgentProject, tasks []aiv1.AgentTask, now time.Time) []MilestoneRow {
	byProject := map[string][]aiv1.AgentTask{}
	for _, t := range tasks {
		if k := t.Namespace + "/" + t.Spec.ProjectRef; projects[k] != nil {
			byProject[k] = append(byProject[k], t)
		}
	}
	needs := map[milestoneKey]bool{}
	for _, it := range inboxOf(projects, tasks, now) {
		if it.MilestoneIID != 0 {
			needs[milestoneKey{it.Namespace, it.Project, it.MilestoneIID}] = true
		}
	}
	var rows []MilestoneRow
	for k, pt := range byProject {
		ap := projects[k]
		for i := range pt {
			st := &pt[i]
			if st.Spec.Type != aiv1.TaskTypeSpec || st.Spec.Milestone == nil || st.Spec.Milestone.IID == 0 {
				continue
			}
			row := liveMilestoneRow(ap, pt, st)
			row.NeedsYou = needs[milestoneKey{row.Namespace, row.Project, row.IID}]
			rows = append(rows, row)
		}
	}
	return rows
}

// liveMilestoneRow is the row of the milestone of the spec task st, one of
// the project's tasks. Its issues are the plan's and the autopilot's items,
// as the snapshot counts them.
func liveMilestoneRow(p *aiv1.AgentProject, tasks []aiv1.AgentTask, st *aiv1.AgentTask) MilestoneRow {
	ms := st.Spec.Milestone
	row := MilestoneRow{Namespace: p.Namespace, Project: p.Name, IID: ms.IID, Title: ms.Title,
		WebURL: fmt.Sprintf("%s/-/milestones/%d", gitLabRef(p).WebURL, ms.IID), State: MilestoneStatePlanned,
		StartedAt: optTime(st.CreationTimestamp.Time)}
	issues := map[int64]bool{}
	for _, pi := range st.Status.Plan {
		if pi.IID != 0 {
			issues[pi.IID] = false
		}
	}
	if ap := st.Status.Autopilot; ap != nil {
		row.Phase = strings.ToLower(string(ap.Phase))
		switch {
		case ap.Phase == aiv1.AutopilotPaused:
			row.State = MilestoneStatePaused
		case ap.Phase == aiv1.AutopilotDone:
			row.State = MilestoneStateDone
		case ap.Phase == aiv1.AutopilotRunning && len(queuedBehind(st)) > 0:
			row.State = MilestoneStateWaiting
		case ap.Phase == aiv1.AutopilotRunning:
			row.State = MilestoneStateRunning
		}
		for _, it := range ap.Items {
			if it.IID != 0 {
				issues[it.IID] = it.State == aiv1.AutopilotItemMerged || it.State == aiv1.AutopilotItemSkipped
			}
		}
	}
	for _, merged := range issues {
		row.Progress.Total++
		if merged {
			row.Progress.Merged++
		}
	}
	row.Progress.Open = row.Progress.Total - row.Progress.Merged
	if ps := st.Status.Pilot; ps != nil && ps.Retrospective != nil {
		for i := range ps.Retrospective.FollowUps {
			if ps.Retrospective.Created[strconv.Itoa(i+1)] == 0 {
				row.FollowUpsOpen++
			}
		}
	}
	var cost float64
	for i := range tasks {
		if t := &tasks[i]; t.Spec.Milestone != nil && t.Spec.Milestone.IID == ms.IID {
			cost += aiv1.CostOf(t.Status.Run.CostUSD)
		}
	}
	row.Cost = tasksCost(cost)
	return row
}

// mergeMilestoneRows merges the live rows with the snapshot's: a live row
// keeps its state, title and progress and takes the snapshot's spend,
// times and follow-ups. A snapshot row without live tasks is a stored row
// if it is done; an open one without tasks is in no work (the operator
// deletes the tasks of milestones no longer planned) and stays out.
func mergeMilestoneRows(live []MilestoneRow, stored []store.MilestoneRow) []MilestoneRow {
	byKey := map[milestoneKey]int{}
	for i, m := range live {
		byKey[milestoneKey{m.Namespace, m.Project, m.IID}] = i
	}
	out := live
	for _, sm := range stored {
		cost := callsCost(sm.Spend)
		// The list has no room for the breakdown; the milestone's view has it.
		cost.Spend = nil
		if i, ok := byKey[milestoneKey{sm.Project.Namespace, sm.Project.Name, sm.IID}]; ok {
			m := &out[i]
			m.Cost, m.FollowUpsOpen = cost, sm.FollowUpsOpen
			m.StartedAt = cmp.Or(optTime(sm.StartedAt), m.StartedAt)
			if m.State == MilestoneStateDone {
				m.DoneAt = optTime(sm.DoneAt)
			}
			continue
		}
		if sm.State != "closed" && sm.Phase != "done" {
			continue
		}
		out = append(out, MilestoneRow{Namespace: sm.Project.Namespace, Project: sm.Project.Name, IID: sm.IID, Title: sm.Title,
			WebURL: sm.WebURL, State: MilestoneStateDone, Phase: sm.Phase,
			Progress:      MilestoneProgress{Merged: sm.Merged, Total: sm.Issues, Open: sm.Open},
			FollowUpsOpen: sm.FollowUpsOpen, Cost: cost, StartedAt: optTime(sm.StartedAt), DoneAt: optTime(sm.DoneAt), Stored: true})
	}
	return out
}

// optTime is t, nil when zero.
func optTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// milestoneRank orders the open rows: needs you, then running, then the
// others (waiting, paused, planned). Done rows come last.
func milestoneRank(m MilestoneRow) int {
	switch {
	case m.State == MilestoneStateDone:
		return 3
	case m.NeedsYou:
		return 0
	case m.State == MilestoneStateRunning:
		return 1
	}
	return 2
}

// sortMilestoneRows sorts open rows by rank, the oldest first within one,
// then done rows, the newest done first. Rows without the time come last.
func sortMilestoneRows(rows []MilestoneRow) {
	slices.SortStableFunc(rows, func(a, b MilestoneRow) int {
		if c := cmp.Compare(milestoneRank(a), milestoneRank(b)); c != 0 {
			return c
		}
		var c int
		if a.State == MilestoneStateDone {
			c = compareTimes(b.DoneAt, a.DoneAt, 1)
		} else {
			c = compareTimes(a.StartedAt, b.StartedAt, -1)
		}
		return cmp.Or(c, cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Project, b.Project), cmp.Compare(a.IID, b.IID))
	})
}

// compareTimes compares a and b; nil is greater than any time when
// nilOrder is -1 (it sorts last ascending), smaller when it is 1 (last
// descending).
func compareTimes(a, b *time.Time, nilOrder int) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -nilOrder
	case b == nil:
		return nilOrder
	}
	return a.Compare(*b)
}
