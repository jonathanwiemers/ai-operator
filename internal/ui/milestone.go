package ui

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// MilestoneView is one planned milestone: its spec task, autopilot queue,
// tracking issue, pilot and implement tasks. Pause reasons and proposal
// summaries are raw agent and CI text: the app shows them as text.
type MilestoneView struct {
	ID        int64               `json:"id"`
	IID       int64               `json:"iid"`
	Title     string              `json:"title"`
	WebURL    string              `json:"webURL"`
	Spec      Task                `json:"spec"`
	Autopilot *MilestoneAutopilot `json:"autopilot,omitempty"`
	Tracking  *TrackingIssueRef   `json:"tracking,omitempty"`
	Pilot     *MilestonePilot     `json:"pilot,omitempty"`
	// Prompts are the milestone's questions to its owners, newest first:
	// the open ones and the newest closed ones. Answers go to the pilot.
	Prompts []PromptView `json:"prompts"`
	// Tasks are the implement tasks, by issue IID.
	Tasks []Task `json:"tasks"`
	// Companions are the companion merge requests of the milestone's
	// issues, by home issue: drafts before the home merge, then their
	// landing.
	Companions []CompanionView `json:"companions"`
	// Pauses is the autopilot's pause history, absent without the history
	// database or when it cannot be read.
	Pauses *PauseHistoryView `json:"pauses,omitempty"`
	Cost   `tstype:",extends"`
	// Permissions is what the app may offer; the endpoints check again.
	Permissions *MilestonePermissions `json:"permissions,omitempty"`
}

// MilestonePermissions: gitlab, the chat can be shown (after connecting);
// steer, the user may post to the pilot; chat, the user may ask the pilot
// on the Desk; transcript, the user may see its live transcript; merge, the
// user may send merge anyway: chat and one of the milestone's owner subjects
// (the operator checks again).
type MilestonePermissions struct {
	GitLab     bool `json:"gitlab"`
	Steer      bool `json:"steer"`
	Chat       bool `json:"chat"`
	Transcript bool `json:"transcript"`
	Merge      bool `json:"merge"`
}

// MilestoneAutopilot is the autopilot of a milestone and its queue.
type MilestoneAutopilot struct {
	Phase        aiv1.AutopilotPhase `json:"phase"`
	PausedReason string              `json:"pausedReason,omitempty"`
	WaitingFor   int64               `json:"waitingFor,omitempty"`
	// PauseKind and PausedFor say why and over which issue it paused;
	// Head is that issue's pushed head (what "merge anyway" names) and
	// ReviewErrors why its reviewers failed.
	PauseKind     aiv1.PauseKind      `json:"pauseKind,omitempty"`
	PausedFor     int64               `json:"pausedFor,omitempty"`
	Head          string              `json:"head,omitempty"`
	ReviewErrors  []aiv1.ReviewError  `json:"reviewErrors,omitempty"`
	MergeOverride *aiv1.MergeOverride `json:"mergeOverride,omitempty"`
	// Exits are the ways out of the pause (aiv1.PauseExits) with their
	// live state and the viewer's buttons, while paused (set by the handler,
	// which knows the viewer).
	Exits []ExitView  `json:"exits,omitempty"`
	Items []QueueItem `json:"items"`
}

// QueueItem is an issue in the autopilot's queue, in queue order.
type QueueItem struct {
	IID             int64                   `json:"iid"`
	Title           string                  `json:"title"`
	State           aiv1.AutopilotItemState `json:"state"`
	MergeRequestIID int64                   `json:"mergeRequestIID,omitempty"`
	MergeRequestURL string                  `json:"mergeRequestURL,omitempty"`
	// Task is the issue's implement task, if there is one.
	Task string `json:"task,omitempty"`
}

// TrackingIssueRef is the milestone's tracking issue, its chat.
type TrackingIssueRef struct {
	IID    int64  `json:"iid"`
	WebURL string `json:"webURL"`
}

// MilestonePilot is the milestone's pilot (its Coordinate task).
type MilestonePilot struct {
	Task     string             `json:"task"`
	Autonomy aiv1.PilotAutonomy `json:"autonomy"`
	// Proposals are the open ones, waiting for an owner's yes: those made
	// before proposals became prompts.
	Proposals []ProposalView `json:"proposals"`
	CostUSD   string         `json:"costUSD"`
	// Phase, Message and PodName say whether the pilot is up to answer.
	Phase   aiv1.TaskPhase `json:"phase,omitempty"`
	Message string         `json:"message,omitempty"`
	PodName string         `json:"podName,omitempty"`
}

// ProposalView is an open proposal of the pilot.
type ProposalView struct {
	ID       int64        `json:"id"`
	Summary  string       `json:"summary"`
	PostedAt *metav1.Time `json:"postedAt,omitempty"`
}

// maxPauseViews bounds the pauses a milestone view carries; its totals
// cover all of them.
const maxPauseViews = 20

// PauseHistoryView is a milestone's autopilot pauses within the history's
// retention: the newest ones, newest first, and the totals of all.
type PauseHistoryView struct {
	Pauses []PauseView     `json:"pauses"`
	Totals PauseTotalsView `json:"totals"`
}

// PauseView is one pause. Until is absent while it is open; Seconds then
// counts up to the view's time. Kind is "unknown" for a pause without one.
type PauseView struct {
	Kind       aiv1.PauseKind     `json:"kind" tstype:"aiv1.PauseKind | 'unknown'"`
	Reason     string             `json:"reason,omitempty"`
	Item       int64              `json:"item,omitempty"`
	Since      time.Time          `json:"since"`
	Until      *time.Time         `json:"until,omitempty"`
	ResolvedBy aiv1.PauseResolver `json:"resolvedBy,omitempty"`
	Seconds    int64              `json:"seconds"`
}

// PauseKindTotalView sums the pauses of one kind.
type PauseKindTotalView struct {
	Count int   `json:"count"`
	Sec   int64 `json:"sec"`
}

// PauseTotalsView sums all pauses: pausedSec their time (open ones up to
// now), meanResumeSec the mean time to resume of the ended ones, byResolver
// who ended them ("unknown" when not recorded).
type PauseTotalsView struct {
	Count         int                           `json:"count"`
	PausedSec     int64                         `json:"pausedSec"`
	ByKind        map[string]PauseKindTotalView `json:"byKind" tstype:"{ [kind: string]: PauseKindTotalView }"`
	MeanResumeSec int64                         `json:"meanResumeSec"`
	ByResolver    map[string]int                `json:"byResolver" tstype:"{ [resolver: string]: number }"`
}

// pauseReader reads a milestone's pauses (the history store).
type pauseReader interface {
	Pauses(ctx context.Context, project store.ProjectKey, milestone int64, now time.Time) (store.PauseHistory, error)
}

// pauseStore is the reader of pauses, nil without the history database.
func (s *Server) pauseStore() pauseReader {
	if s.pauses != nil {
		return s.pauses
	}
	if st := s.spendStore(); st != nil {
		return st
	}
	return nil
}

// milestonePauses is the pause history of the milestone, nil without the
// history database or when it fails (logged).
func (s *Server) milestonePauses(ctx context.Context, ap *aiv1.AgentProject, iid int64, now time.Time) *PauseHistoryView {
	st := s.pauseStore()
	if st == nil {
		return nil
	}
	h, err := st.Pauses(ctx, store.ProjectKey{Namespace: ap.Namespace, Name: ap.Name}, iid, now)
	if err != nil {
		s.log.Error(err, "cannot read the milestone's pauses", "namespace", ap.Namespace, "project", ap.Name, "milestone", iid)
		return nil
	}
	return pauseHistoryView(h)
}

func pauseHistoryView(h store.PauseHistory) *PauseHistoryView {
	t := h.Totals
	v := &PauseHistoryView{Pauses: []PauseView{}, Totals: PauseTotalsView{Count: t.Count, PausedSec: t.PausedSec,
		ByKind: map[string]PauseKindTotalView{}, MeanResumeSec: t.MeanResumeSec, ByResolver: map[string]int{}}}
	for k, kt := range t.ByKind {
		v.Totals.ByKind[k] = PauseKindTotalView{Count: kt.Count, Sec: kt.Sec}
	}
	maps.Copy(v.Totals.ByResolver, t.ByResolver)
	for i := len(h.Pauses) - 1; i >= 0 && len(v.Pauses) < maxPauseViews; i-- {
		p := h.Pauses[i]
		v.Pauses = append(v.Pauses, PauseView{Kind: aiv1.PauseKind(p.Kind), Reason: p.Reason, Item: p.Item, Since: p.Since,
			Until: p.Until, ResolvedBy: aiv1.PauseResolver(p.ResolvedBy), Seconds: p.Seconds})
	}
	return v
}

// milestoneTasks returns the spec task and the Coordinate task of the
// milestone with the IID, nil where there is none.
func milestoneTasks(tasks []aiv1.AgentTask, iid int64) (st, coord *aiv1.AgentTask) {
	for i := range tasks {
		t := &tasks[i]
		if t.Spec.Milestone == nil || t.Spec.Milestone.IID != iid {
			continue
		}
		switch t.Spec.Type {
		case aiv1.TaskTypeSpec:
			st = t
		case aiv1.TaskTypeCoordinate:
			coord = t
		}
	}
	return st, coord
}

// trackingIssueIID is the milestone's tracking issue: the autopilot's, else
// the one "/pilot coordinate" opened, else that of an autopilot switched
// off. 0 without one.
func trackingIssueIID(st, coord *aiv1.AgentTask) int64 {
	ap := st.Status.Autopilot
	if ap != nil && ap.TrackingIssueIID != 0 && ap.Phase != aiv1.AutopilotOff {
		return ap.TrackingIssueIID
	}
	if coord != nil && coord.Spec.Issue != nil && coord.Spec.Issue.IID != 0 {
		return coord.Spec.Issue.IID
	}
	if ap != nil {
		return ap.TrackingIssueIID
	}
	return 0
}

// milestoneView builds the view of the milestone of st from the project's
// tasks.
func milestoneView(p *aiv1.AgentProject, tasks []aiv1.AgentTask, st, coord *aiv1.AgentTask, now time.Time) MilestoneView {
	ms, web := st.Spec.Milestone, gitLabRef(p).WebURL
	v := MilestoneView{ID: ms.ID, IID: ms.IID, Title: ms.Title, WebURL: fmt.Sprintf("%s/-/milestones/%d", web, ms.IID),
		Spec: taskView(st, p, now), Tasks: []Task{}}
	var cost float64
	byIssue := map[int64]string{}
	for i := range tasks {
		t := &tasks[i]
		if t.Spec.Milestone == nil || t.Spec.Milestone.IID != ms.IID {
			continue
		}
		cost += aiv1.CostOf(t.Status.Run.CostUSD)
		if t.Spec.Type == aiv1.TaskTypeImplement {
			v.Tasks = append(v.Tasks, taskView(t, p, now))
			if t.Spec.Issue != nil {
				byIssue[t.Spec.Issue.IID] = t.Name
			}
		}
	}
	sortTasks(v.Tasks)
	v.Companions = milestoneCompanions(p, tasks, ms.IID)
	v.Cost = tasksCost(cost)
	if ap := st.Status.Autopilot; ap != nil {
		titles := map[int64]string{}
		for _, pi := range st.Status.Plan {
			titles[pi.IID] = pi.Title
		}
		a := &MilestoneAutopilot{Phase: ap.Phase, PausedReason: ap.PausedReason, WaitingFor: waitingFor(ap, tasks), PauseKind: ap.PauseKind,
			PausedFor: ap.PausedFor, MergeOverride: ap.MergeOverride, Items: []QueueItem{}}
		for i := range tasks {
			if t := &tasks[i]; ap.Phase == aiv1.AutopilotPaused && ap.PausedFor != 0 && t.Spec.Issue != nil && t.Spec.Issue.IID == ap.PausedFor {
				a.Head, a.ReviewErrors = t.Status.Run.PushedSHA, t.Status.Run.ReviewErrorKinds
			}
		}
		for _, it := range ap.Items {
			a.Items = append(a.Items, QueueItem{IID: it.IID, Title: titles[it.IID], State: it.State, MergeRequestIID: it.MergeRequestIID, MergeRequestURL: it.MergeRequestURL, Task: byIssue[it.IID]})
		}
		v.Autopilot = a
	}
	if iid := trackingIssueIID(st, coord); iid != 0 {
		v.Tracking = &TrackingIssueRef{IID: iid, WebURL: fmt.Sprintf("%s/-/issues/%d", web, iid)}
	}
	if coord != nil {
		pv := &MilestonePilot{Task: coord.Name, Autonomy: p.Spec.Pilot.AutonomyOf(coord), Proposals: []ProposalView{},
			CostUSD: aiv1.ShowCost(aiv1.CostOf(coord.Status.Run.CostUSD)), Phase: coord.Status.Phase, Message: coord.Status.Message, PodName: coord.Status.PodName}
		if ps := coord.Status.Pilot; ps != nil {
			for _, pr := range ps.Proposals {
				pv.Proposals = append(pv.Proposals, ProposalView{ID: pr.ID, Summary: pr.Summary, PostedAt: pr.PostedAt})
			}
		}
		v.Pilot = pv
	}
	return v
}

// loadMilestone reads the project and the spec and Coordinate tasks of the
// milestone of the request path. A milestone without a spec task is not
// found.
func (s *Server) loadMilestone(w http.ResponseWriter, r *http.Request) (*aiv1.AgentProject, []aiv1.AgentTask, *aiv1.AgentTask, *aiv1.AgentTask, bool) {
	ap, tasks, ok := s.loadProject(w, r)
	if !ok {
		return nil, nil, nil, nil, false
	}
	iid, err := strconv.ParseInt(r.PathValue("iid"), 10, 64)
	if err != nil {
		notFound(w)
		return nil, nil, nil, nil, false
	}
	st, coord := milestoneTasks(tasks, iid)
	if st == nil {
		notFound(w)
		return nil, nil, nil, nil, false
	}
	return ap, tasks, st, coord, true
}

// milestone returns one milestone of a project.
func (s *Server) milestone(w http.ResponseWriter, r *http.Request) {
	ap, tasks, st, coord, ok := s.loadMilestone(w, r)
	if !ok {
		return
	}
	now := time.Now()
	v := milestoneView(ap, tasks, st, coord, now)
	v.Cost = s.scopeCost(r.Context(), store.SpendScope{Namespace: ap.Namespace, Project: ap.Name, MilestoneIID: v.IID}, v.Cost)
	v.Pauses = s.milestonePauses(r.Context(), ap, v.IID, now)
	gl := s.gitlab != nil && s.gitlab.serves(ap)
	p := principalOf(r)
	chat := s.cfg.ChatEnabled() && s.authz.CanChat(p, ap.Namespace, ap.Name)
	v.Permissions = &MilestonePermissions{GitLab: gl, Steer: gl && s.authz.CanSteer(p, ap.Namespace, ap.Name),
		Chat:       chat,
		Transcript: s.logs != nil && s.authz.CanSeeTranscript(p, ap.Namespace, ap.Name),
		Merge:      chat && slices.Contains(st.Spec.OwnerSubjects, p.Username)}
	if v.Autopilot != nil {
		v.Autopilot.Exits = exitViews(st.Status.Autopilot, tasks, ap, *v.Permissions)
	}
	var target string
	if coord != nil {
		target = coord.Name
	}
	v.Prompts = promptViews(st, target, p.Username, chat)
	writeJSON(w, http.StatusOK, v)
}

// milestoneThread returns the comments of the milestone's tracking issue,
// read with the user's own GitLab token, after confirming that the issue is
// the operator's tracking issue of this milestone (its number comes from
// task status, which the agent pods may write).
func (s *Server) milestoneThread(w http.ResponseWriter, r *http.Request) {
	ap, gl, tok, part, ok := s.trackingIssue(w, r)
	if !ok {
		return
	}
	iid := part.IID
	notes, err := gl.ListIssueNotes(r.Context(), ap.Spec.GitLab.Project, iid)
	if err != nil {
		if gitlab.StatusCode(err) == http.StatusUnauthorized {
			s.gitlabRejected(w, tok)
			return
		}
		s.log.V(1).Info("cannot read comments", "target", "tracking", "iid", iid, "error", err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GitLab did not return the comments"})
		return
	}
	part.Notes = make([]NoteView, 0, len(notes))
	for _, n := range notes {
		part.Notes = append(part.Notes, NoteView{ID: n.ID, Author: n.Author.Username, Body: n.Body, System: n.System, CreatedAt: n.CreatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"parts": []ThreadPart{*part}})
}

// trackingIssue loads the milestone of the request and the user's GitLab
// client and confirms the milestone's tracking issue as the operator's
// tracking issue of this milestone (its number comes from task status,
// which the agent pods may write). It answers the request itself when that
// is not possible.
func (s *Server) trackingIssue(w http.ResponseWriter, r *http.Request) (*aiv1.AgentProject, gitlab.API, *GitLabToken, *ThreadPart, bool) {
	ap, _, st, coord, ok := s.loadMilestone(w, r)
	if !ok {
		return nil, nil, nil, nil, false
	}
	iid := trackingIssueIID(st, coord)
	if iid == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "the milestone has no tracking issue"})
		return nil, nil, nil, nil, false
	}
	gl, tok, ok := s.userGitLab(w, r, ap)
	if !ok {
		return nil, nil, nil, nil, false
	}
	part := &ThreadPart{Target: "tracking", IID: iid, WebURL: fmt.Sprintf("%s/-/issues/%d", gitLabRef(ap).WebURL, iid)}
	if err := s.checkTarget(r.Context(), gl, ap, st, part); err != nil {
		s.log.Info("refusing a tracking issue", "user", principalOf(r).Username, "iid", iid, "reason", err.Error())
		s.targetRefused(w, tok, "the tracking issue could not be confirmed as this milestone's", err)
		return nil, nil, nil, nil, false
	}
	return ap, gl, tok, part, true
}

// postMilestoneNote writes a chat message to the milestone's pilot on its
// tracking issue, in the user's GitLab name, with the project's pilot
// prefix in front unless the message starts with it. Whether the pilot
// takes it as an owner's instruction is the operator's decision.
func (s *Server) postMilestoneNote(w http.ResponseWriter, r *http.Request) {
	if !s.mayPost(w, r) {
		return
	}
	req, ok := readNote(w, r)
	if !ok {
		return
	}
	ap, gl, tok, part, ok := s.trackingIssue(w, r)
	if !ok {
		return
	}
	s.createNote(w, r, ap, gl, tok, part, toPilot(req.Body, ap.Spec.Workflow.WithDefaults().PilotPrefix))
}

// toPilot addresses a trimmed chat message to the pilot: prefix and the
// message, unless the operator already reads it as one to the pilot.
func toPilot(body, prefix string) string {
	if _, ok := command.Pilot(body, prefix, ""); ok {
		return body
	}
	return prefix + " " + body
}
