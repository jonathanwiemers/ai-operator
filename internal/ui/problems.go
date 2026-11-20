package ui

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/problem"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// ProblemStatus is the live state of a problem, computed from the CRD and
// never stored: waiting for an owner, the operator acting on an accepted
// action or an automatic way out, the pilot looking at it, or resolved.
type ProblemStatus string

const (
	ProblemWaiting  ProblemStatus = "waiting"
	ProblemActing   ProblemStatus = "acting"
	ProblemPilot    ProblemStatus = "pilot"
	ProblemResolved ProblemStatus = "resolved"
)

// ProblemView is one problem of a milestone: an issue (0: the milestone
// itself) that got stuck. ID is the desk_problems row, 0 for a problem the
// history database does not know (it is off, or the operator has not
// recorded it yet). Cause is untrusted text.
type ProblemView struct {
	ID         int64         `json:"id"`
	IssueIID   int64         `json:"issueIID"`
	Title      string        `json:"title,omitempty"`
	State      string        `json:"state"`
	Status     ProblemStatus `json:"status"`
	Cause      string        `json:"cause"`
	OpenedAt   time.Time     `json:"openedAt"`
	ReopenedAt *time.Time    `json:"reopenedAt,omitempty"`
	ResolvedAt *time.Time    `json:"resolvedAt,omitempty"`
	ResolvedBy string        `json:"resolvedBy,omitempty"`
}

// ProblemList is the problems of a milestone, open first, then newest
// first. History is false without the history database: only the open
// problems, from the CRD.
type ProblemList struct {
	Problems []ProblemView `json:"problems"`
	History  bool          `json:"history"`
}

// ProblemThread is one problem with what is needed to act on it and its
// timeline. Prompt is the newest open question about it (its options are
// the one bar to act), Exits the ways out of its hold or pause while no
// question is open. Prompts are its closed questions, for a thread without
// history. Events are the timeline, newest first; Older is the cursor of
// the next page. Pilot is the Coordinate task whose chat takes messages.
type ProblemThread struct {
	Problem     ProblemView          `json:"problem"`
	Facts       *problem.Facts       `json:"facts,omitempty"`
	Prompt      *PromptView          `json:"prompt,omitempty"`
	Prompts     []PromptView         `json:"prompts"`
	Exits       []ExitView           `json:"exits"`
	Events      []StreamEvent        `json:"events"`
	Older       string               `json:"older,omitempty"`
	History     bool                 `json:"history"`
	Pilot       string               `json:"pilot,omitempty"`
	Permissions MilestonePermissions `json:"permissions"`
}

// problemWindow widens a thread's window so the event that opened the
// problem, recorded a moment before the row, is in it.
const problemWindow = time.Minute

// problemThreadLimit is the page size of a thread's timeline.
const problemThreadLimit = 200

// problems lists the problems of a milestone: the stored ones with the live
// status of those still open, and the open ones the database does not know
// yet. A stored open problem the CRD no longer has shows as resolved.
func (s *Server) problems(w http.ResponseWriter, r *http.Request) {
	ap, tasks, iid, ok := s.loadMilestoneIID(w, r)
	if !ok {
		return
	}
	st, coord := milestoneTasks(tasks, iid)
	list := ProblemList{Problems: []ProblemView{}}
	var rows []store.Problem
	if db := s.spendStore(); db != nil {
		var err error
		if rows, err = db.Problems(r.Context(), store.ProjectKey{Namespace: ap.Namespace, Name: ap.Name}, iid); err != nil {
			s.internalError(w, err)
			return
		}
		list.History = true
	}
	list.Problems = mergeProblems(rows, liveProblems(st, coord, time.Now()))
	writeJSON(w, http.StatusOK, list)
}

// problemThread returns one problem of a milestone. id is a desk_problems
// id, or issue-<iid>: the newest problem of the issue (0: the milestone),
// open first, which also works without the history database.
func (s *Server) problemThread(w http.ResponseWriter, r *http.Request) {
	ap, tasks, iid, ok := s.loadMilestoneIID(w, r)
	if !ok {
		return
	}
	st, coord := milestoneTasks(tasks, iid)
	now := time.Now()
	live := liveProblems(st, coord, now)
	pk := store.ProjectKey{Namespace: ap.Namespace, Name: ap.Name}
	db := s.spendStore()
	var view *ProblemView
	ref := r.PathValue("id")
	if is, found := strings.CutPrefix(ref, "issue-"); found {
		issue, err := strconv.ParseInt(is, 10, 64)
		if err != nil || issue < 0 {
			notFound(w)
			return
		}
		var rows []store.Problem
		if db != nil {
			if rows, err = db.Problems(r.Context(), pk, iid); err != nil {
				s.internalError(w, err)
				return
			}
		}
		for _, v := range mergeProblems(rows, live) {
			if v.IssueIID == issue {
				view = &v
				break
			}
		}
	} else {
		id, err := strconv.ParseInt(ref, 10, 64)
		if err != nil || id <= 0 || db == nil {
			notFound(w)
			return
		}
		row, err := db.GetProblem(r.Context(), pk, iid, id)
		if errors.Is(err, store.ErrNoProblem) {
			notFound(w)
			return
		}
		if err != nil {
			s.internalError(w, err)
			return
		}
		for _, v := range mergeProblems([]store.Problem{row}, live) {
			if v.ID == id {
				view = &v
			}
		}
	}
	if view == nil {
		notFound(w)
		return
	}
	t := ProblemThread{Problem: *view, Prompts: []PromptView{}, Exits: []ExitView{}, Events: []StreamEvent{}}
	p := principalOf(r)
	gl := s.gitlab != nil && s.gitlab.serves(ap)
	chat := s.cfg.ChatEnabled() && s.authz.CanChat(p, ap.Namespace, ap.Name) && coord != nil
	t.Permissions = MilestonePermissions{GitLab: gl, Steer: gl && s.authz.CanSteer(p, ap.Namespace, ap.Name),
		Chat:       chat,
		Transcript: s.logs != nil && s.authz.CanSeeTranscript(p, ap.Namespace, ap.Name),
		Merge:      chat && st != nil && slices.Contains(st.Spec.OwnerSubjects, p.Username)}
	if coord != nil {
		t.Pilot = coord.Name
	}
	if st != nil {
		s.threadOfCRD(&t, ap, st, tasks, p.Username)
	}
	if db != nil {
		if err := s.threadEvents(r, db, &t, pk, iid, coord); err != nil {
			if errors.Is(err, errBadCursor) {
				badRequest(w, err.Error())
				return
			}
			s.internalError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, t)
}

// threadOfCRD adds the live parts of t from the spec task st: the issue's
// facts, its questions and, while it is open with no question, its ways out.
func (s *Server) threadOfCRD(t *ProblemThread, ap *aiv1.AgentProject, st *aiv1.AgentTask, tasks []aiv1.AgentTask, user string) {
	iid := t.Problem.IssueIID
	if iid != 0 {
		web := gitLabRef(ap).WebURL
		f := milestoneProblemFacts(web, st, tasks, iid, t.Problem.Title)
		t.Facts = &f
	}
	from, until := problemSpan(t.Problem)
	for _, v := range promptViews(st, t.Pilot, user, t.Permissions.Chat) {
		if v.IssueIID != iid {
			continue
		}
		if v.State == aiv1.PromptOpen {
			if t.Prompt == nil && t.Problem.State == store.ProblemOpen {
				t.Prompt = &v
			}
			continue
		}
		if v.PostedAt != nil && (v.PostedAt.Time.Before(from) || until != nil && v.PostedAt.Time.After(*until)) {
			continue
		}
		t.Prompts = append(t.Prompts, v)
	}
	if t.Problem.State != store.ProblemOpen {
		return
	}
	if kind, ok := problemKind(st.Status.Autopilot, iid); ok {
		t.Exits = pauseExitViews(kind, iid, st.Status.Autopilot, tasks, ap, t.Permissions)
	}
}

// milestoneProblemFacts are the facts of the issue iid of st's milestone.
func milestoneProblemFacts(web string, st *aiv1.AgentTask, tasks []aiv1.AgentTask, iid int64, title string) problem.Facts {
	open := map[int64]bool{}
	for _, it := range st.Status.Plan {
		open[it.IID] = true
	}
	if ap := st.Status.Autopilot; ap != nil {
		for _, it := range ap.Items {
			open[it.IID] = it.State == aiv1.AutopilotItemActive || it.State == aiv1.AutopilotItemPending
		}
	}
	is := problem.IssueFacts{IID: iid, Title: title, URL: fmt.Sprintf("%s/-/issues/%d", web, iid)}
	return problem.Build(is, issueTask(tasks, iid), st.Status.Plan, open)
}

// problemKind is the pause kind over iid: its hold, or the pause whose
// problem it is.
func problemKind(ap *aiv1.AutopilotStatus, iid int64) (aiv1.PauseKind, bool) {
	if ap == nil {
		return "", false
	}
	for _, it := range ap.Items {
		if iid != 0 && it.IID == iid && it.Hold != nil {
			return it.Hold.Kind, true
		}
	}
	if ap.Phase == aiv1.AutopilotPaused && aiv1.ProblemIssue(ap) == iid {
		return ap.PauseKind, true
	}
	return "", false
}

// errBadCursor is a before parameter that is no cursor.
var errBadCursor = errors.New("before is not a cursor")

// threadEvents sets t's timeline: the events of the issue (for the
// milestone's problem, those of the pilot's kinds without an issue) and the
// chat of its tasks and of the pilot about it, from a minute before it
// opened until a minute after it resolved, newest first.
func (s *Server) threadEvents(r *http.Request, db *store.Store, t *ProblemThread, pk store.ProjectKey, milestone int64, coord *aiv1.AgentTask) error {
	t.History = true
	from, until := problemSpan(t.Problem)
	before, err := parseCursor(r.URL.Query().Get("before"))
	if err != nil {
		return errBadCursor
	}
	if before == nil && until != nil {
		before = &store.Cursor{At: *until}
	}
	iid := t.Problem.IssueIID
	q := store.StreamQuery{Projects: []store.ProjectKey{pk}, Milestone: milestone, Issue: iid, Before: before, Limit: problemThreadLimit}
	if iid == 0 {
		q.Kinds = historyStreamKinds
	}
	events, err := db.Stream(r.Context(), q)
	if err != nil {
		return err
	}
	if iid == 0 {
		events = slices.DeleteFunc(events, func(e store.Event) bool { return e.IssueIID != 0 })
	}
	var names []string
	if iid != 0 {
		names = issueTaskNames(r, s, pk, milestone, iid)
	}
	if coord != nil {
		names = append(names, coord.Name)
	}
	var chat []store.ChatMessage
	if len(names) > 0 {
		if chat, err = db.ChatStream(r.Context(), q.Projects, names, nil, before, nil, problemThreadLimit); err != nil {
			return err
		}
	}
	if coord != nil {
		chat = problemChat(chat, coord.Name, iid)
	}
	conversation := map[store.ProjectKey]bool{pk: s.authz.CanReadConversation(principalOf(r), pk.Namespace, pk.Name)}
	merged := mergeStream(events, chat, conversation, problemThreadLimit, false)
	full := len(merged) == problemThreadLimit
	merged = slices.DeleteFunc(merged, func(e StreamEvent) bool { return e.At.Before(from) })
	t.Events = merged
	if n := len(merged); full && n == problemThreadLimit {
		t.Older = formatStreamCursor(merged[n-1])
	}
	return nil
}

// issueTaskNames are the names of the tasks of issue iid in milestone.
func issueTaskNames(r *http.Request, s *Server, pk store.ProjectKey, milestone, iid int64) []string {
	names, err := s.milestoneTaskNames(r, pk.Namespace, pk.Name, milestone, iid)
	if err != nil {
		s.log.Error(err, "cannot list the issue's tasks", "namespace", pk.Namespace, "project", pk.Name, "issue", iid)
		return nil
	}
	return names
}

// problemChat keeps, of the pilot's chat, the messages about the problem
// iid (their body starts with its prefix, see chatAbout) and the replies to
// them; other tasks' messages stay. Messages are newest first.
func problemChat(chat []store.ChatMessage, pilot string, iid int64) []store.ChatMessage {
	about := map[int64]bool{}
	for _, m := range slices.Backward(chat) {
		if m.Task == pilot && (chatAbout(m.Body, iid) || m.ReplyTo != 0 && about[m.ReplyTo]) {
			about[m.ID] = true
		}
	}
	return slices.DeleteFunc(chat, func(m store.ChatMessage) bool { return m.Task == pilot && !about[m.ID] })
}

// chatAbout reports whether a pilot chat body is about the problem iid: it
// starts with "problem: #<iid>" (not followed by a digit), or with
// "problem: milestone" for iid 0.
func chatAbout(body string, iid int64) bool {
	prefix := "problem: milestone"
	if iid != 0 {
		prefix = "problem: #" + strconv.FormatInt(iid, 10)
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(body), prefix)
	if !ok {
		return false
	}
	r := []rune(rest)
	return len(r) == 0 || !unicode.IsDigit(r[0]) && !unicode.IsLetter(r[0])
}

// problemSpan is the window of a problem's thread: from a minute before it
// opened until a minute after it resolved (nil while open).
func problemSpan(v ProblemView) (from time.Time, until *time.Time) {
	from = v.OpenedAt.Add(-problemWindow)
	if v.ResolvedAt != nil {
		u := v.ResolvedAt.Add(problemWindow)
		until = &u
	}
	return from, until
}

// liveProblems are the open problems of the spec task st by issue, with
// their live status.
func liveProblems(st, coord *aiv1.AgentTask, now time.Time) map[int64]ProblemView {
	out := map[int64]ProblemView{}
	if st == nil {
		return out
	}
	for _, iid := range problem.Open(st) {
		out[iid] = ProblemView{IssueIID: iid, Title: issueTitle(st, iid), State: store.ProblemOpen,
			Status: problemStatus(st, coord, iid), Cause: problem.Why(st, iid), OpenedAt: problemSince(st, iid, now)}
	}
	return out
}

// mergeProblems joins the stored problems rows (newest first) with the
// live ones: the newest stored open row of an issue takes its live status
// and title; other stored open rows are resolved; live problems without a
// row come first with ID 0. Open problems come first, then newest first.
func mergeProblems(rows []store.Problem, live map[int64]ProblemView) []ProblemView {
	out := []ProblemView{}
	used := map[int64]bool{}
	titles := map[int64]string{}
	for iid, v := range live {
		titles[iid] = v.Title
	}
	for _, row := range rows {
		v := ProblemView{ID: row.ID, IssueIID: row.IssueIID, Title: titles[row.IssueIID], State: row.State,
			Status: ProblemResolved, Cause: row.Cause, OpenedAt: row.OpenedAt, ReopenedAt: row.ReopenedAt,
			ResolvedAt: row.ResolvedAt, ResolvedBy: row.ResolvedBy}
		if l, ok := live[row.IssueIID]; ok && row.State == store.ProblemOpen && !used[row.IssueIID] {
			used[row.IssueIID] = true
			v.Status = l.Status
			if v.Cause == "" {
				v.Cause = l.Cause
			}
		} else if row.State == store.ProblemOpen {
			v.State = store.ProblemResolved
		}
		out = append(out, v)
	}
	for iid, v := range live {
		if !used[iid] {
			out = append(out, v)
		}
	}
	slices.SortStableFunc(out, func(a, b ProblemView) int {
		if (a.State == store.ProblemOpen) != (b.State == store.ProblemOpen) {
			if a.State == store.ProblemOpen {
				return -1
			}
			return 1
		}
		return cmp.Or(b.OpenedAt.Compare(a.OpenedAt), cmp.Compare(b.ID, a.ID), cmp.Compare(a.IssueIID, b.IssueIID))
	})
	return out
}

// problemStatus is the live status of the open problem iid: waiting while
// a question about it is open, acting while the operator runs an accepted
// action on it or an automatic way out is under way, pilot while the pilot
// runs, else waiting (the ways out are the owner's).
func problemStatus(st, coord *aiv1.AgentTask, iid int64) ProblemStatus {
	for _, p := range st.Status.Prompts {
		if p.State == aiv1.PromptOpen && p.Source.IssueIID == iid {
			return ProblemWaiting
		}
	}
	if coord != nil && coord.Status.Pilot != nil {
		for _, pr := range coord.Status.Pilot.Accepted {
			for _, a := range pr.Actions {
				if a.IssueIID == iid {
					return ProblemActing
				}
			}
		}
	}
	if kind, ok := problemKind(st.Status.Autopilot, iid); ok {
		for _, e := range aiv1.PauseExits(kind) {
			if e.Auto {
				if s, _ := exitStatus(e.ID, iid, st.Status.Autopilot, nil, nil); s == ExitStatusInProgress {
					return ProblemActing
				}
			}
		}
	}
	if coord != nil && coord.Status.Phase == aiv1.TaskPhaseRunning {
		return ProblemPilot
	}
	return ProblemWaiting
}

// problemSince is when the open problem iid started: its hold, the pause,
// or its oldest open question; now when none says.
func problemSince(st *aiv1.AgentTask, iid int64, now time.Time) time.Time {
	ap := st.Status.Autopilot
	if ap != nil {
		for _, it := range ap.Items {
			if iid != 0 && it.IID == iid && it.Hold != nil && !it.Hold.Since.IsZero() {
				return it.Hold.Since.Time
			}
		}
		if ap.Phase == aiv1.AutopilotPaused && aiv1.ProblemIssue(ap) == iid && ap.PausedAt != nil {
			return ap.PausedAt.Time
		}
	}
	for _, p := range st.Status.Prompts {
		if p.State == aiv1.PromptOpen && p.Source.IssueIID == iid && p.PostedAt != nil {
			return p.PostedAt.Time
		}
	}
	return now
}

// issueTitle is the planned title of iid in st's milestone, "" for 0 or an
// issue the plan does not name.
func issueTitle(st *aiv1.AgentTask, iid int64) string {
	for _, it := range st.Status.Plan {
		if it.IID == iid && iid != 0 {
			return it.Title
		}
	}
	return ""
}
