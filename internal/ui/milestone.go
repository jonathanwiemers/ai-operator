package ui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/history"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// MilestoneView is one planned milestone: its spec task, autopilot queue,
// tracking issue, pilot and implement tasks. Pause reasons and proposal
// summaries are raw agent and CI text: the app shows them as text. Stored:
// its tasks are gone and the view is the snapshot's (history database):
// spec is empty (no name), ID 0, and there is no autopilot, pilot, prompt or
// task.
type MilestoneView struct {
	ID     int64          `json:"id"`
	IID    int64          `json:"iid"`
	Title  string         `json:"title"`
	WebURL string         `json:"webURL"`
	State  MilestoneState `json:"state"`
	Stored bool           `json:"stored,omitempty"`
	Spec   Task           `json:"spec"`
	// Issues are the milestone's issues in plan order, then the autopilot's
	// items the plan lacks, as the snapshot holds them.
	Issues []MilestoneIssueRow `json:"issues"`
	// Releases are the milestone's release decisions by ID: live from
	// status.autopilot.releases, else the snapshot's.
	Releases  []ReleaseView       `json:"releases"`
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
	// Pauses is the autopilot's pause history within the history's
	// retention: the newest 20 pauses (maxPauseViews), newest first, and the
	// totals of all. Absent without the history database or when it cannot
	// be read.
	Pauses *store.PauseHistory `json:"pauses,omitempty"`
	// Retrospective is live from task status, else the snapshot's; absent
	// while there is none.
	Retrospective *RetrospectiveView `json:"retrospective,omitempty"`
	Cost          `tstype:",extends"`
	// Permissions is what the app may offer; the endpoints check again.
	Permissions *MilestonePermissions `json:"permissions,omitempty"`
}

// MilestoneIssueRow is an issue of a milestone. State is the autopilot
// item's in lower case, planned before autopilot queued it, closed for an
// issue the backfill found closed without a merge on record. Task is its live
// implement task, if any. Figures are absent without the history database
// or while the snapshot lacks the issue. Titles are untrusted text.
type MilestoneIssueRow struct {
	IID             int64             `json:"iid"`
	Title           string            `json:"title"`
	State           string            `json:"state" tstype:"'planned' | 'pending' | 'active' | 'merged' | 'skipped' | 'closed'"`
	MergeRequestIID int64             `json:"mergeRequestIID,omitempty"`
	MergeRequestURL string            `json:"mergeRequestURL,omitempty"`
	Task            string            `json:"task,omitempty"`
	Figures         *IssueFiguresView `json:"figures,omitempty"`
	// Version is the one the issue shipped in: that of the first Released
	// or AlreadyReleased decision that contains it.
	Version string `json:"version,omitempty"`
}

// IssueFiguresView is an issue's time and spend: wallSec from its first run
// to its merge (or now), 0 if it never ran; agentSec its calls' durations;
// pausedSec the milestone's pauses over it (open ones up to now). Its cost
// is unknown without calls, never $0.
type IssueFiguresView struct {
	WallSec   int64 `json:"wallSec"`
	AgentSec  int64 `json:"agentSec"`
	PausedSec int64 `json:"pausedSec"`
	Cost      `tstype:",extends"`
}

func issueFiguresView(f store.IssueFigures) *IssueFiguresView {
	c := callsCost(f.Spend)
	// The row has no room for the breakdown; the issue's view will.
	c.Spend = nil
	return &IssueFiguresView{WallSec: f.WallSec, AgentSec: f.AgentMS / 1000, PausedSec: f.PausedSec, Cost: c}
}

// milestoneDetailReader reads a milestone's snapshot and its issues' figures
// (the history store).
type milestoneDetailReader interface {
	Milestone(ctx context.Context, project store.ProjectKey, iid int64, now time.Time) (store.MilestoneDetail, error)
	IssueFigures(ctx context.Context, project store.ProjectKey, milestone, issue int64, now time.Time) (store.IssueFigures, error)
}

// milestoneDetailStore is the reader of milestone snapshots, nil without
// the history database.
func (s *Server) milestoneDetailStore() milestoneDetailReader {
	if s.milestoneDetails != nil {
		return s.milestoneDetails
	}
	if st := s.spendStore(); st != nil {
		return st
	}
	return nil
}

// issueRows are the issues of the snapshot of the spec task st, with their
// implement tasks from byIssue and the versions of its release decisions.
func issueRows(st *aiv1.AgentTask, byIssue map[int64]string, now time.Time) []MilestoneIssueRow {
	rows := []MilestoneIssueRow{}
	snap, _ := history.Snapshot(st, "", now)
	for _, is := range snap.Issues {
		rows = append(rows, MilestoneIssueRow{IID: is.IID, Title: is.Title, State: is.State,
			MergeRequestIID: is.MergeRequestIID, MergeRequestURL: is.MergeRequestURL, Task: byIssue[is.IID]})
	}
	setIssueVersions(rows, liveReleases(st))
	return rows
}

// ReleaseView is one release decision of a milestone (aiv1.ReleaseRecord).
// Why and Reason are untrusted text: the app shows them as text. Unset
// times are absent.
type ReleaseView struct {
	ID            int64             `json:"id"`
	By            aiv1.ReleaseBy    `json:"by"`
	Actor         string            `json:"actor,omitempty"`
	Why           string            `json:"why,omitempty"`
	AfterIssue    int64             `json:"afterIssue,omitempty"`
	SHA           string            `json:"sha,omitempty"`
	ReleasedSHA   string            `json:"releasedSHA,omitempty"`
	State         aiv1.ReleaseState `json:"state"`
	Reason        string            `json:"reason,omitempty"`
	Issues        []int64           `json:"issues"`
	MergeRequests []int64           `json:"mergeRequests"`
	Version       string            `json:"version,omitempty"`
	PipelineURL   string            `json:"pipelineURL,omitempty"`
	ReleaseURL    string            `json:"releaseURL,omitempty"`
	DecidedAt     *time.Time        `json:"decidedAt,omitempty"`
	StartedAt     *time.Time        `json:"startedAt,omitempty"`
	EndedAt       *time.Time        `json:"endedAt,omitempty"`
}

// liveReleases are the views of the spec task st's release decisions
// (status.autopilot.releases), by ID; never nil.
func liveReleases(st *aiv1.AgentTask) []ReleaseView {
	out := []ReleaseView{}
	if st.Status.Autopilot == nil {
		return out
	}
	for _, r := range st.Status.Autopilot.Releases {
		v := ReleaseView{ID: r.ID, By: r.By, Actor: r.Actor, Why: r.Why, AfterIssue: r.AfterIssue, SHA: r.SHA,
			ReleasedSHA: r.ReleasedSHA, State: r.State, Reason: r.Reason, Issues: nonNilIDs(r.Issues),
			MergeRequests: nonNilIDs(r.MergeRequests), Version: r.Version, PipelineURL: r.PipelineURL,
			ReleaseURL: r.ReleaseURL, DecidedAt: timeOrNil(r.DecidedAt.Time)}
		if r.StartedAt != nil {
			v.StartedAt = timeOrNil(r.StartedAt.Time)
		}
		if r.EndedAt != nil {
			v.EndedAt = timeOrNil(r.EndedAt.Time)
		}
		out = append(out, v)
	}
	return byReleaseID(out)
}

// storedReleases are the views of a snapshot's release decisions, by ID;
// never nil.
func storedReleases(recs []store.ReleaseSnapshot) []ReleaseView {
	out := []ReleaseView{}
	for _, r := range recs {
		out = append(out, ReleaseView{ID: r.ID, By: aiv1.ReleaseBy(r.By), Actor: r.Actor, Why: r.Why,
			AfterIssue: r.AfterIID, SHA: r.SHA, ReleasedSHA: r.ReleasedSHA, State: aiv1.ReleaseState(r.State),
			Reason: r.Reason, Issues: nonNilIDs(r.Issues), MergeRequests: nonNilIDs(r.MergeRequests),
			Version: r.Version, PipelineURL: r.PipelineURL, ReleaseURL: r.ReleaseURL, DecidedAt: timeOrNil(r.DecidedAt),
			StartedAt: timeOrNil(r.StartedAt), EndedAt: timeOrNil(r.EndedAt)})
	}
	return byReleaseID(out)
}

func byReleaseID(rs []ReleaseView) []ReleaseView {
	slices.SortStableFunc(rs, func(a, b ReleaseView) int { return cmp.Compare(a.ID, b.ID) })
	return rs
}

// setIssueVersions gives each row the version of the first Released or
// AlreadyReleased decision of rs (by ID) that contains its issue.
func setIssueVersions(rows []MilestoneIssueRow, rs []ReleaseView) {
	for i := range rows {
		for _, r := range rs {
			if (r.State == aiv1.ReleaseReleased || r.State == aiv1.ReleaseAlreadyReleased) && slices.Contains(r.Issues, rows[i].IID) {
				rows[i].Version = r.Version
				break
			}
		}
	}
}

// nonNilIDs is s, or an empty slice for nil.
func nonNilIDs(s []int64) []int64 {
	if s == nil {
		return []int64{}
	}
	return s
}

// timeOrNil is t, nil when zero.
func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// storedMilestoneView is the view of a milestone whose tasks are gone, from
// its snapshot d.
func storedMilestoneView(p *aiv1.AgentProject, d store.MilestoneDetail) MilestoneView {
	web := gitLabRef(p).WebURL
	v := MilestoneView{IID: d.IID, Title: d.Title, WebURL: cmp.Or(d.WebURL, fmt.Sprintf("%s/-/milestones/%d", web, d.IID)),
		State: storedMilestoneState(d.MilestoneRow), Stored: true, Issues: []MilestoneIssueRow{}, Prompts: []PromptView{},
		Tasks: []Task{}, Companions: []CompanionView{}, Cost: callsCost(d.Spend), Releases: storedReleases(d.Releases)}
	for _, f := range d.Issues {
		v.Issues = append(v.Issues, MilestoneIssueRow{IID: f.IID, Title: f.Title, State: f.State,
			MergeRequestIID: f.MRIID, MergeRequestURL: f.MRURL, Figures: issueFiguresView(f)})
	}
	setIssueVersions(v.Issues, v.Releases)
	if d.TrackingIID != 0 {
		v.Tracking = &TrackingIssueRef{IID: d.TrackingIID, WebURL: fmt.Sprintf("%s/-/issues/%d", web, d.TrackingIID)}
	}
	v.Retrospective = storedRetrospective(web, v.Tracking, d)
	return v
}

// MilestonePermissions: gitlab, the chat can be shown (after connecting);
// steer, the user may post to the pilot; chat, the user may ask the pilot
// on the Desk; transcript, the user may see its live transcript; merge, the
// user may send merge anyway: chat and one of the milestone's owner subjects
// (the operator checks again); draft, the user may ask the pilot for a draft
// of a follow-up milestone (the Coordinate task's "draft" command): as merge,
// while the pilot exists and a follow-up of the retrospective is open;
// followUp, the user may create a follow-up milestone from the Desk (POST
// …/followup): steer, one of the milestone's owner subjects, and a
// follow-up of the retrospective is open, also once its tasks are gone;
// dismiss, the user may dismiss the remaining follow-ups (POST
// …/followups/dismiss): steer, one of the owner subjects, on an open
// autopilot milestone whose posted retrospective has open follow-ups none
// dismissed yet.
type MilestonePermissions struct {
	GitLab     bool `json:"gitlab"`
	Steer      bool `json:"steer"`
	Chat       bool `json:"chat"`
	Transcript bool `json:"transcript"`
	Merge      bool `json:"merge"`
	Draft      bool `json:"draft"`
	FollowUp   bool `json:"followUp"`
	Dismiss    bool `json:"dismiss"`
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
	// QueuedBehind are the older autopilot milestones it waits for, ascending,
	// while it waits for its turn to be planned (running, not paused: no
	// Inbox item).
	QueuedBehind []int64 `json:"queuedBehind,omitempty"`
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
func (s *Server) milestonePauses(ctx context.Context, ap *aiv1.AgentProject, iid int64, now time.Time) *store.PauseHistory {
	st := s.pauseStore()
	if st == nil {
		return nil
	}
	h, err := st.Pauses(ctx, store.ProjectKey{Namespace: ap.Namespace, Name: ap.Name}, iid, now)
	if err != nil {
		s.log.Error(err, "cannot read the milestone's pauses", "namespace", ap.Namespace, "project", ap.Name, "milestone", iid)
		return nil
	}
	return newestPauses(h)
}

// newestPauses keeps the newest maxPauseViews pauses of h, newest first,
// and the totals of all.
func newestPauses(h store.PauseHistory) *store.PauseHistory {
	newest := make([]store.Pause, 0, min(len(h.Pauses), maxPauseViews))
	for i := len(h.Pauses) - 1; i >= 0 && len(newest) < maxPauseViews; i-- {
		newest = append(newest, h.Pauses[i])
	}
	return &store.PauseHistory{Pauses: newest, Totals: h.Totals}
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

// queuedBehind are the older autopilot milestones the spec task st waits
// for, ascending, while it waits for its turn to be planned; nil otherwise.
func queuedBehind(st *aiv1.AgentTask) []int64 {
	ap := st.Status.Autopilot
	if ap == nil || st.Spec.RunRequest != 0 || len(st.Status.Plan) != 0 || ap.StartRequestedBy != "" || len(st.Spec.QueuedBehind) == 0 {
		return nil
	}
	return slices.Sorted(slices.Values(st.Spec.QueuedBehind))
}

// milestoneView builds the view of the milestone of st from the project's
// tasks.
func milestoneView(p *aiv1.AgentProject, tasks []aiv1.AgentTask, st, coord *aiv1.AgentTask, now time.Time) MilestoneView {
	ms, web := st.Spec.Milestone, gitLabRef(p).WebURL
	v := MilestoneView{ID: ms.ID, IID: ms.IID, Title: ms.Title, WebURL: fmt.Sprintf("%s/-/milestones/%d", web, ms.IID),
		State: liveMilestoneState(st), Spec: taskView(st, p, now), Tasks: []Task{}}
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
	v.Issues, v.Releases = issueRows(st, byIssue, now), liveReleases(st)
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
		a.QueuedBehind = queuedBehind(st)
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
	if v.Retrospective = liveRetrospective(web, v.Tracking, st, coord); v.Retrospective != nil {
		v.Retrospective.Draft = followUpDraftView(coord)
	}
	return v
}

// loadMilestone reads the project and the spec and Coordinate tasks of the
// milestone of the request path. A milestone without a spec task is not
// found.
func (s *Server) loadMilestone(w http.ResponseWriter, r *http.Request) (*aiv1.AgentProject, []aiv1.AgentTask, *aiv1.AgentTask, *aiv1.AgentTask, bool) {
	ap, tasks, iid, ok := s.loadMilestoneIID(w, r)
	if !ok {
		return nil, nil, nil, nil, false
	}
	st, coord := milestoneTasks(tasks, iid)
	if st == nil {
		notFound(w)
		return nil, nil, nil, nil, false
	}
	return ap, tasks, st, coord, true
}

// loadMilestoneIID reads the project and its tasks and the milestone IID
// of the request path.
func (s *Server) loadMilestoneIID(w http.ResponseWriter, r *http.Request) (*aiv1.AgentProject, []aiv1.AgentTask, int64, bool) {
	ap, tasks, ok := s.loadProject(w, r)
	if !ok {
		return nil, nil, 0, false
	}
	iid, err := strconv.ParseInt(r.PathValue("iid"), 10, 64)
	if err != nil {
		notFound(w)
		return nil, nil, 0, false
	}
	return ap, tasks, iid, true
}

// milestone returns one milestone of a project: from its live tasks, else
// from the snapshot of the history database.
func (s *Server) milestone(w http.ResponseWriter, r *http.Request) {
	ap, tasks, iid, ok := s.loadMilestoneIID(w, r)
	if !ok {
		return
	}
	st, coord := milestoneTasks(tasks, iid)
	if st == nil {
		s.storedMilestone(w, r, ap, iid)
		return
	}
	now := time.Now()
	v := milestoneView(ap, tasks, st, coord, now)
	v.Cost = s.scopeCost(r.Context(), store.SpendScope{Namespace: ap.Namespace, Project: ap.Name, MilestoneIID: v.IID}, v.Cost)
	v.Pauses = s.milestonePauses(r.Context(), ap, v.IID, now)
	if d := s.issueFigures(r.Context(), ap, &v, now); v.Retrospective == nil && d != nil {
		v.Retrospective = storedRetrospective(gitLabRef(ap).WebURL, v.Tracking, *d)
	}
	s.addCostDrivers(r.Context(), ap, v.IID, v.Retrospective)
	gl := s.gitlab != nil && s.gitlab.serves(ap)
	p := principalOf(r)
	chat := s.cfg.ChatEnabled() && s.authz.CanChat(p, ap.Namespace, ap.Name)
	v.Permissions = &MilestonePermissions{GitLab: gl, Steer: gl && s.authz.CanSteer(p, ap.Namespace, ap.Name),
		Chat:       chat,
		Transcript: s.logs != nil && s.authz.CanSeeTranscript(p, ap.Namespace, ap.Name),
		Merge:      chat && slices.Contains(st.Spec.OwnerSubjects, p.Username)}
	v.Permissions.Draft = v.Permissions.Merge && coord != nil && openFollowUps(v.Retrospective)
	v.Permissions.FollowUp = v.Permissions.Steer && slices.Contains(st.Spec.OwnerSubjects, p.Username) && openFollowUps(v.Retrospective)
	v.Permissions.Dismiss = v.Permissions.Steer && slices.Contains(st.Spec.OwnerSubjects, p.Username) && dismissable(st)
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

// issueFigures adds the snapshot's figures to the issues of the live view
// v, in one call, and returns the snapshot. Without the history database,
// without a snapshot or when it fails (logged), they stay absent and it
// returns nil.
func (s *Server) issueFigures(ctx context.Context, ap *aiv1.AgentProject, v *MilestoneView, now time.Time) *store.MilestoneDetail {
	st := s.milestoneDetailStore()
	if st == nil || len(v.Issues) == 0 {
		return nil
	}
	d, err := st.Milestone(ctx, store.ProjectKey{Namespace: ap.Namespace, Name: ap.Name}, v.IID, now)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Error(err, "cannot read the milestone's figures", "namespace", ap.Namespace, "project", ap.Name, "milestone", v.IID)
		}
		return nil
	}
	byIID := map[int64]store.IssueFigures{}
	for _, f := range d.Issues {
		byIID[f.IID] = f
	}
	for i := range v.Issues {
		if f, ok := byIID[v.Issues[i].IID]; ok {
			v.Issues[i].Figures = issueFiguresView(f)
		}
	}
	return &d
}

// storedMilestone answers with the snapshot of the project's milestone iid
// that has no live tasks: not found without the history database or the
// snapshot. GitLab, steer and followUp are as for a live milestone;
// without a pilot there is no chat, transcript, merge or draft.
func (s *Server) storedMilestone(w http.ResponseWriter, r *http.Request, ap *aiv1.AgentProject, iid int64) {
	st := s.milestoneDetailStore()
	if st == nil {
		notFound(w)
		return
	}
	now := time.Now()
	d, err := st.Milestone(r.Context(), store.ProjectKey{Namespace: ap.Namespace, Name: ap.Name}, iid, now)
	if errors.Is(err, store.ErrNotFound) {
		notFound(w)
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	v := storedMilestoneView(ap, d)
	v.Pauses = s.milestonePauses(r.Context(), ap, iid, now)
	s.addCostDrivers(r.Context(), ap, iid, v.Retrospective)
	gl := s.gitlab != nil && s.gitlab.serves(ap)
	p := principalOf(r)
	v.Permissions = &MilestonePermissions{GitLab: gl, Steer: gl && s.authz.CanSteer(p, ap.Namespace, ap.Name)}
	v.Permissions.FollowUp = v.Permissions.Steer && slices.Contains(d.OwnerSubjects, p.Username) && openFollowUps(v.Retrospective)
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
		writeError(w, http.StatusBadGateway, "GitLab did not return the comments")
		return
	}
	part.Notes = make([]NoteView, 0, len(notes))
	for _, n := range notes {
		part.Notes = append(part.Notes, NoteView{ID: n.ID, Author: n.Author.Username, Body: n.Body, System: n.System, CreatedAt: n.CreatedAt})
	}
	writeJSON(w, http.StatusOK, ThreadResponse{Parts: []ThreadPart{*part}})
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
		writeError(w, http.StatusNotFound, "the milestone has no tracking issue")
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
