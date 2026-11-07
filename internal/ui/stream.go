package ui

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/history"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// StreamEvent is one event of a project's stream.
type StreamEvent struct {
	ID              int64          `json:"id"`
	At              time.Time      `json:"at"`
	Namespace       string         `json:"namespace"`
	Project         string         `json:"project"`
	MilestoneIID    int64          `json:"milestoneIID,omitempty"`
	IssueIID        int64          `json:"issueIID,omitempty"`
	MergeRequestIID int64          `json:"mergeRequestIID,omitempty"`
	Task            string         `json:"task,omitempty"`
	Kind            string         `json:"kind" tstype:"StreamKind"`
	Actor           string         `json:"actor,omitempty"`
	Summary         string         `json:"summary"`
	Redacted        bool           `json:"redacted,omitempty"`
	Data            map[string]any `json:"data" tstype:"StreamEventData"`
}

// StreamPage is a page of events, newest first.
type StreamPage struct {
	Events []StreamEvent `json:"events"`
	// Older is the cursor of the next page, absent at the end.
	Older string `json:"older,omitempty"`
	// Newest is the cursor of the first event, for asking what is newer.
	Newest string `json:"newest,omitempty"`
	// History is false without a database: there are no events.
	History bool `json:"history"`
}

// Every event kind belongs to one tab of a milestone: pilotKinds to Pilot
// (questions, pauses, what the pilot did, the owners' messages, decisions,
// the retrospective), runtimeKinds to Stream (runs, pushes, CI, reviews,
// merges). web/src/stream.ts mirrors both lists.
var (
	pilotKinds = []string{history.KindPromptPosted, history.KindPromptAnswered, history.KindPromptClosed,
		history.KindNeeds, history.KindAutopilot, history.KindAutopilotPaused, history.KindAutopilotResumed,
		history.KindProposal, history.KindPilotWriteFailed, history.KindNote, "chat.user", "chat.agent",
		history.KindRetrospective}
	runtimeKinds = []string{history.KindRunStarted, history.KindRunFinished, history.KindPush, history.KindCI,
		history.KindReview, history.KindMerged, history.KindClosed, history.KindCheckpoint}
)

// streamKinds are the event kinds a stream may be filtered by.
var streamKinds = slices.Concat(pilotKinds, runtimeKinds)

var chatStreamKinds = []string{"chat.user", "chat.agent"}

// historyStreamKinds are the kinds of history_events.
var historyStreamKinds = slices.DeleteFunc(slices.Clone(streamKinds), func(k string) bool {
	return slices.Contains(chatStreamKinds, k)
})

const (
	maxStreamLimit = 100
	maxSearch      = 200
)

// stream returns events of the projects the user may see, of one project,
// or of one milestone, newest first.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	q := r.URL.Query()
	if !s.cfg.Research.Enabled() {
		writeJSON(w, http.StatusOK, StreamPage{Events: []StreamEvent{}})
		return
	}
	st := s.research.Load()
	if st == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the history database is not connected yet"})
		return
	}
	var query store.StreamQuery
	if ref := q.Get("project"); ref != "" {
		ns, name, ok := strings.Cut(ref, "/")
		if !ok || !s.authz.CanView(p, ns, name) {
			notFound(w)
			return
		}
		query.Projects = []store.ProjectKey{{Namespace: ns, Name: name}}
		if m := q.Get("milestone"); m != "" {
			iid, err := strconv.ParseInt(m, 10, 64)
			if err != nil || iid <= 0 {
				badRequest(w, "milestone must be a number")
				return
			}
			issues, err := s.milestoneIssues(r, ns, name, iid)
			if err != nil {
				s.internalError(w, err)
				return
			}
			query.Milestone, query.Issues = iid, issues
		}
		if is := q.Get("issue"); is != "" {
			iid, err := strconv.ParseInt(is, 10, 64)
			if err != nil || iid <= 0 || query.Milestone == 0 {
				badRequest(w, "issue must be a number, with a milestone")
				return
			}
			query.Issue = iid
		}
	} else if q.Get("issue") != "" {
		badRequest(w, "issue needs a project and a milestone")
		return
	} else {
		var projects aiv1.AgentProjectList
		if err := s.reader.List(r.Context(), &projects); err != nil {
			s.internalError(w, err)
			return
		}
		for _, ap := range projects.Items {
			if s.authz.CanView(p, ap.Namespace, ap.Name) {
				query.Projects = append(query.Projects, store.ProjectKey{Namespace: ap.Namespace, Name: ap.Name})
			}
		}
	}
	// Chat rows come only when kinds asks for them: their ids are not
	// unique against history events, and clients key events by id.
	wantHistory, wantChat := true, false
	if k := q.Get("kinds"); k != "" {
		wantHistory = false
		for kind := range strings.SplitSeq(k, ",") {
			if !slices.Contains(streamKinds, kind) {
				badRequest(w, "unknown kind "+strconv.Quote(kind))
				return
			}
			query.Kinds = append(query.Kinds, kind)
			if slices.Contains(historyStreamKinds, kind) {
				wantHistory = true
			}
			if slices.Contains(chatStreamKinds, kind) {
				wantChat = true
			}
		}
	}
	query.Text = strings.TrimSpace(q.Get("q"))
	if len(query.Text) > maxSearch {
		badRequest(w, fmt.Sprintf("the search may have up to %d characters", maxSearch))
		return
	}
	conversation := map[store.ProjectKey]bool{}
	for _, pk := range query.Projects {
		if s.authz.CanReadConversation(p, pk.Namespace, pk.Name) {
			conversation[pk] = true
		} else if query.Text != "" {
			query.TextWithoutNotes = append(query.TextWithoutNotes, pk)
		}
	}
	var err error
	if query.Before, err = parseCursor(q.Get("before")); err == nil {
		query.After, err = parseCursor(q.Get("after"))
	}
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	query.Limit = 50
	if l := q.Get("limit"); l != "" {
		if query.Limit, err = strconv.Atoi(l); err != nil || query.Limit < 1 || query.Limit > maxStreamLimit {
			badRequest(w, fmt.Sprintf("limit must be 1 to %d", maxStreamLimit))
			return
		}
	}
	// Chat is not in history_events: fetch both and merge by (at, id).
	// Cursors are shared across tables, so equal timestamps can rarely
	// skip or duplicate a row at a page boundary.
	var events []store.Event
	if wantHistory {
		hq := query
		if len(hq.Kinds) > 0 {
			hq.Kinds = slices.DeleteFunc(slices.Clone(hq.Kinds), func(k string) bool {
				return slices.Contains(chatStreamKinds, k)
			})
			if len(hq.Kinds) == 0 {
				wantHistory = false
			}
		}
		if wantHistory {
			events, err = st.Stream(r.Context(), hq)
			if err != nil {
				s.internalError(w, err)
				return
			}
		}
	}
	var chat []store.ChatMessage
	if wantChat && query.Text == "" {
		var taskNames []string
		if query.Milestone != 0 {
			if taskNames, err = s.milestoneTaskNames(r, query.Projects[0].Namespace, query.Projects[0].Name, query.Milestone, query.Issue); err != nil {
				s.internalError(w, err)
				return
			}
			// A milestone without tasks has no chat (no names would mean
			// every task of the project).
			wantChat = len(taskNames) > 0
		}
		if wantChat {
			chat, err = st.ChatStream(r.Context(), query.Projects, taskNames, chatRoles(query.Kinds), query.Before, query.After, query.Limit)
			if err != nil {
				s.internalError(w, err)
				return
			}
		}
	}
	merged := mergeStream(events, chat, conversation, query.Limit, query.After != nil)
	page := StreamPage{Events: merged, History: true}
	if n := len(merged); n > 0 {
		page.Newest = formatStreamCursor(merged[0])
		if n == query.Limit && query.After == nil {
			page.Older = formatStreamCursor(merged[n-1])
		}
	}
	writeJSON(w, http.StatusOK, page)
}

// milestoneTaskNames are the task names of the project's milestone iid,
// with issue those of the issue in it only.
func (s *Server) milestoneTaskNames(r *http.Request, namespace, name string, iid, issue int64) ([]string, error) {
	var list aiv1.AgentTaskList
	if err := s.reader.List(r.Context(), &list, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	var names []string
	for _, t := range list.Items {
		if t.Namespace == namespace && t.Spec.ProjectRef == name && t.Spec.Milestone != nil && t.Spec.Milestone.IID == iid &&
			(issue == 0 || t.Spec.Issue != nil && t.Spec.Issue.IID == issue) {
			names = append(names, t.Name)
		}
	}
	if issue != 0 {
		return names, nil
	}
	if st, _ := milestoneTasks(list.Items, iid); st != nil && st.Namespace == namespace && st.Spec.ProjectRef == name {
		names = append(names, st.Name)
	}
	return names, nil
}

// chatRoles are the chat roles the stream kinds ask for; nil for both.
func chatRoles(kinds []string) []string {
	user, agent := slices.Contains(kinds, "chat.user"), slices.Contains(kinds, "chat.agent")
	switch {
	case user && !agent:
		return []string{"user"}
	case agent && !user:
		return []string{"agent", "system"}
	}
	return nil
}

func chatStreamKind(role string) string {
	if role == "user" {
		return "chat.user"
	}
	return "chat.agent"
}

// mergeStream merges history events and chat messages by (at, id), newest
// first, up to limit: the newest of them, or with after (paging newer) the
// oldest, so the next page continues without a gap.
func mergeStream(events []store.Event, chat []store.ChatMessage, conversation map[store.ProjectKey]bool, limit int, after bool) []StreamEvent {
	out := make([]StreamEvent, 0, len(events)+len(chat))
	i, j := 0, 0
	for i < len(events) || j < len(chat) {
		takeChat := false
		switch {
		case i >= len(events):
			takeChat = true
		case j >= len(chat):
		default:
			a, b := events[i], chat[j]
			takeChat = b.At.After(a.At) || b.At.Equal(a.At) && b.ID > a.ID
		}
		if takeChat {
			m := chat[j]
			j++
			pk := store.ProjectKey{Namespace: m.Namespace, Name: m.Project}
			out = append(out, chatStreamEvent(m, conversation[pk]))
			continue
		}
		e := events[i]
		i++
		out = append(out, streamEvent(e, conversation[store.ProjectKey{Namespace: e.Namespace, Name: e.Project}]))
	}
	if len(out) > limit {
		if after {
			return out[len(out)-limit:]
		}
		return out[:limit]
	}
	return out
}

func chatStreamEvent(m store.ChatMessage, conversation bool) StreamEvent {
	data := map[string]any{"role": m.Role, "kind": m.Kind, "state": m.State}
	if m.ReplyTo != 0 {
		data["replyTo"] = m.ReplyTo
	}
	if m.AuthorName != "" {
		data["authorName"] = m.AuthorName
	}
	out := StreamEvent{
		ID: m.ID, At: m.At, Namespace: m.Namespace, Project: m.Project, Task: m.Task,
		Kind: chatStreamKind(m.Role), Actor: m.Author, Summary: m.Kind, Data: data,
	}
	if conversation {
		data["body"] = m.Body
	} else {
		out.Redacted = true
	}
	return out
}

func formatStreamCursor(e StreamEvent) string {
	return fmt.Sprintf("c:%d:%d", e.At.UnixNano(), e.ID)
}

// streamEvent is e as the user sees it: without the comment's body unless
// they may read the conversation.
func streamEvent(e store.Event, conversation bool) StreamEvent {
	data := e.Data
	if data == nil {
		data = map[string]any{}
	}
	out := StreamEvent{ID: e.ID, At: e.At, Namespace: e.Namespace, Project: e.Project, MilestoneIID: e.MilestoneIID,
		IssueIID: e.IssueIID, MergeRequestIID: e.MergeRequestIID, Task: e.Task, Kind: e.Kind, Actor: e.Actor,
		Summary: e.Summary, Data: data}
	if e.Kind == "note" && !conversation {
		delete(data, "body")
		out.Redacted = true
	}
	return out
}

// milestoneIssues are the issues of the project's milestone iid and its
// tracking issue.
func (s *Server) milestoneIssues(r *http.Request, namespace, name string, iid int64) ([]int64, error) {
	var list aiv1.AgentTaskList
	if err := s.reader.List(r.Context(), &list); err != nil {
		return nil, err
	}
	issues := []int64{}
	var tasks []aiv1.AgentTask
	for _, t := range list.Items {
		if t.Namespace != namespace || t.Spec.ProjectRef != name {
			continue
		}
		tasks = append(tasks, t)
		if t.Spec.Milestone != nil && t.Spec.Milestone.IID == iid && t.Spec.Issue != nil {
			issues = append(issues, t.Spec.Issue.IID)
		}
	}
	if st, coord := milestoneTasks(tasks, iid); st != nil {
		if tr := trackingIssueIID(st, coord); tr != 0 {
			issues = append(issues, tr)
		}
		if ap := st.Status.Autopilot; ap != nil {
			for _, it := range ap.Items {
				issues = append(issues, it.IID)
			}
		}
	}
	return issues, nil
}

// Cursors: "c:<unix nanoseconds>:<id>" is an event's position, "t:<RFC 3339
// time>" a point in time (before only: what is older).
func formatCursor(e store.Event) string {
	return fmt.Sprintf("c:%d:%d", e.At.UnixNano(), e.ID)
}

func parseCursor(s string) (*store.Cursor, error) {
	if s == "" {
		return nil, nil
	}
	errBad := errors.New("invalid cursor")
	kind, rest, _ := strings.Cut(s, ":")
	switch kind {
	case "c":
		ns, id, ok := strings.Cut(rest, ":")
		n, err1 := strconv.ParseInt(ns, 10, 64)
		i, err2 := strconv.ParseInt(id, 10, 64)
		if !ok || err1 != nil || err2 != nil || i <= 0 {
			return nil, errBad
		}
		return &store.Cursor{At: time.Unix(0, n), ID: i}, nil
	case "t":
		t, err := time.Parse(time.RFC3339, rest)
		if err != nil {
			return nil, errBad
		}
		return &store.Cursor{At: t}, nil
	}
	return nil, errBad
}

// HistoryDay is a project's activity on one day.
type HistoryDay struct {
	Day     string  `json:"day"`
	Runs    int     `json:"runs"`
	Merged  int     `json:"merged"`
	CostUSD float64 `json:"costUSD"`
	Events  int     `json:"events"`
}

// HistoryTotals sums days.
type HistoryTotals struct {
	Runs    int     `json:"runs"`
	Merged  int     `json:"merged"`
	CostUSD float64 `json:"costUSD"`
	Events  int     `json:"events"`
}

// HistoryResponse is a project's activity per day and in total, with the
// totals of the period before for comparison.
type HistoryResponse struct {
	Days     []HistoryDay  `json:"days"`
	Totals   HistoryTotals `json:"totals"`
	Previous HistoryTotals `json:"previous"`
}

const maxHistoryDays = 90

// history returns a project's activity of the last days (default 30) in
// the time zone tz (default UTC), every day, oldest first.
func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	ns, name := r.PathValue("namespace"), r.PathValue("name")
	if !s.cfg.Research.Enabled() || !s.authz.CanView(principalOf(r), ns, name) {
		notFound(w)
		return
	}
	st := s.research.Load()
	if st == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the history database is not connected yet"})
		return
	}
	days := 30
	if d := r.URL.Query().Get("days"); d != "" {
		var err error
		if days, err = strconv.Atoi(d); err != nil || days < 1 || days > maxHistoryDays {
			badRequest(w, fmt.Sprintf("days must be 1 to %d", maxHistoryDays))
			return
		}
	}
	loc := time.UTC
	if tz := r.URL.Query().Get("tz"); tz != "" {
		var err error
		if loc, err = time.LoadLocation(tz); err != nil {
			badRequest(w, "unknown time zone")
			return
		}
	}
	y, m, d := time.Now().In(loc).Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, loc)
	first := today.AddDate(0, 0, -(days - 1))
	rows, err := st.Days(r.Context(), store.ProjectKey{Namespace: ns, Name: name}, first.AddDate(0, 0, -days), loc)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, historyOf(rows, first, days))
}

// historyOf spreads rows over the days from first on, and sums them and the
// days before first.
func historyOf(rows []store.Day, first time.Time, days int) HistoryResponse {
	byDay := map[string]store.Day{}
	for _, d := range rows {
		byDay[d.Day.Format(time.DateOnly)] = d
	}
	out := HistoryResponse{Days: make([]HistoryDay, 0, days)}
	for i := range days {
		key := first.AddDate(0, 0, i).Format(time.DateOnly)
		d := byDay[key]
		out.Days = append(out.Days, HistoryDay{Day: key, Runs: d.Runs, Merged: d.Merged, CostUSD: d.CostUSD, Events: d.Events})
		out.Totals.add(d)
	}
	start := first.Format(time.DateOnly)
	for _, d := range rows {
		if d.Day.Format(time.DateOnly) < start {
			out.Previous.add(d)
		}
	}
	return out
}

func (t *HistoryTotals) add(d store.Day) {
	t.Runs += d.Runs
	t.Merged += d.Merged
	t.CostUSD += d.CostUSD
	t.Events += d.Events
}

// InboxItem is something that needs a human now.
type InboxItem struct {
	Namespace    string `json:"namespace"`
	Project      string `json:"project"`
	Kind         string `json:"kind" tstype:"'task' | 'proposal' | 'pause' | 'prompt' | 'pilotError' | 'companionReady'"`
	Task         string `json:"task,omitempty"`
	IssueIID     int64  `json:"issueIID,omitempty"`
	MilestoneIID int64  `json:"milestoneIID,omitempty"`
	ProposalID   int64  `json:"proposalID,omitempty"`
	// PromptID is the open prompt (kind prompt); Reason is its kind.
	PromptID string     `json:"promptID,omitempty"`
	Title    string     `json:"title"`
	Reason   string     `json:"reason"`
	At       *time.Time `json:"at,omitempty"`
	URL      string     `json:"url,omitempty"`
	// PauseKind is the cause of a paused autopilot (kind pause).
	PauseKind aiv1.PauseKind `json:"pauseKind,omitempty"`
	// PausedSince is when the pause started (kind pause), from
	// status.autopilot.pausedAt; unset when the operator did not record it.
	PausedSince *time.Time `json:"pausedSince,omitempty"`
}

// inbox lists what needs the user across the projects they may see: tasks
// with questions, failed or blocked, the pilots' open proposals and paused
// autopilots. It comes from the live state, not the history.
func (s *Server) inbox(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	var projects aiv1.AgentProjectList
	if err := s.reader.List(r.Context(), &projects); err != nil {
		s.internalError(w, err)
		return
	}
	var tasks aiv1.AgentTaskList
	if err := s.reader.List(r.Context(), &tasks); err != nil {
		s.internalError(w, err)
		return
	}
	visible := map[string]*aiv1.AgentProject{}
	for i := range projects.Items {
		ap := &projects.Items[i]
		if s.authz.CanView(p, ap.Namespace, ap.Name) {
			visible[ap.Namespace+"/"+ap.Name] = ap
		}
	}
	items := inboxOf(visible, tasks.Items, time.Now())
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// inboxTitleMax cuts a prompt's question in the Inbox.
const inboxTitleMax = 140

// pilotErrorTTL is how long a rejected status write stays in the Inbox after
// it was last seen.
const pilotErrorTTL = 24 * time.Hour

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func inboxOf(projects map[string]*aiv1.AgentProject, tasks []aiv1.AgentTask, now time.Time) []InboxItem {
	items := []InboxItem{}
	for i := range tasks {
		t := &tasks[i]
		ap := projects[t.Namespace+"/"+t.Spec.ProjectRef]
		if ap == nil {
			continue
		}
		base := InboxItem{Namespace: t.Namespace, Project: ap.Name, Task: t.Name}
		if t.Spec.Milestone != nil {
			base.MilestoneIID = t.Spec.Milestone.IID
		}
		if t.Spec.Issue != nil {
			base.IssueIID, base.Title, base.URL = t.Spec.Issue.IID, t.Spec.Issue.Title, t.Spec.Issue.WebURL
		}
		if a := t.Status.Run.LastActivityTime; a != nil {
			at := a.Time
			base.At = &at
		}
		if t.Spec.Type == aiv1.TaskTypeCoordinate && t.Status.Pilot != nil {
			for _, pr := range t.Status.Pilot.Proposals {
				it := base
				it.Kind, it.ProposalID, it.Title, it.Reason = "proposal", pr.ID, pr.Summary, "proposal"
				if pr.PostedAt != nil {
					at := pr.PostedAt.Time
					it.At = &at
				}
				items = append(items, it)
			}
		}
		needsInputPrompt := false
		for _, pr := range t.Status.Prompts {
			if pr.State.Closed() {
				continue
			}
			it := base
			it.Kind, it.PromptID, it.Title, it.Reason = "prompt", pr.ID, truncateRunes(pr.Question, inboxTitleMax), string(pr.Source.Kind)
			if pr.Source.Kind == aiv1.PromptNeedsInput {
				needsInputPrompt = true
			}
			it.At = nil
			if pr.PostedAt != nil {
				at := pr.PostedAt.Time
				it.At = &at
			}
			items = append(items, it)
		}
		for _, f := range t.Status.WriteFailures {
			if f.At == nil || now.Sub(f.At.Time) > pilotErrorTTL {
				continue
			}
			it := base
			it.Kind, it.Title, it.Reason = "pilotError", f.Source, f.Error
			at := f.At.Time
			it.At = &at
			items = append(items, it)
		}
		if a := t.Status.Autopilot; a != nil && a.Phase == aiv1.AutopilotPaused && t.Spec.Milestone != nil {
			it := base
			it.Kind, it.IssueIID, it.Title, it.Reason, it.PauseKind = "pause", a.PausedFor, t.Spec.Milestone.Title, a.PausedReason, a.PauseKind
			if a.PausedAt != nil {
				since := a.PausedAt.Time
				it.PausedSince, it.At = &since, &since
			}
			items = append(items, it)
			continue
		}
		switch stage := StageOf(t, ciWindow(ap), now); stage {
		case StageNeedsInput, StageFailed, StageBlocked:
			if needsInputPrompt && stage == StageNeedsInput {
				continue // the prompt is the item
			}
			it := base
			it.Kind, it.Reason = "task", string(stage)
			if it.Title == "" && t.Spec.Milestone != nil {
				it.Title = t.Spec.Milestone.Title
			}
			items = append(items, it)
		}
	}
	items = append(items, companionReadyItems(projects, tasks)...)
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].At, items[j].At
		switch {
		case a == nil:
			return false
		case b == nil:
			return true
		}
		return a.After(*b)
	})
	return items
}

// companionReadyItems are the companion merge requests that are ready and
// wait for the owners to merge them: autopilot does not, and the owners
// were told (status.companions, decision 9 of
// docs/specs/cross-repo-changes.spec.md). Merged and closed ones are no
// longer ready, so they leave the Inbox.
func companionReadyItems(projects map[string]*aiv1.AgentProject, tasks []aiv1.AgentTask) []InboxItem {
	// The home issue's implement task, by project and issue.
	homeTask := map[string]string{}
	for i := range tasks {
		if t := &tasks[i]; t.Spec.Type == aiv1.TaskTypeImplement && t.Spec.Issue != nil {
			homeTask[fmt.Sprintf("%s/%s#%d", t.Namespace, t.Spec.ProjectRef, t.Spec.Issue.IID)] = t.Name
		}
	}
	var items []InboxItem
	for _, k := range slices.Sorted(maps.Keys(projects)) {
		ap := projects[k]
		for i := range ap.Status.Companions {
			c := &ap.Status.Companions[i]
			if c.State != aiv1.LandingReady || c.AutoMerge || c.NotifiedAt == nil {
				continue
			}
			it := InboxItem{Namespace: ap.Namespace, Project: ap.Name, Kind: "companionReady", IssueIID: c.HomeIssueIID,
				MilestoneIID: c.MilestoneIID, Task: homeTask[fmt.Sprintf("%s#%d", k, c.HomeIssueIID)],
				Title: companionRef(c.Project, c.MRIID), URL: c.MRURL, Reason: "ready to merge"}
			if c.Release != "" {
				it.Reason = "ready to merge: release " + c.Release + " runs"
			}
			at := c.NotifiedAt.Time
			it.At = &at
			items = append(items, it)
		}
	}
	return items
}
