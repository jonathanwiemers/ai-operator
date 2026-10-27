package ui

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
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
	// Tasks are the implement tasks, by issue IID.
	Tasks   []Task `json:"tasks"`
	CostUSD string `json:"costUSD"`
}

// MilestoneAutopilot is the autopilot of a milestone and its queue.
type MilestoneAutopilot struct {
	Phase        aiv1.AutopilotPhase `json:"phase"`
	PausedReason string              `json:"pausedReason,omitempty"`
	WaitingFor   int64               `json:"waitingFor,omitempty"`
	Items        []QueueItem         `json:"items"`
}

// QueueItem is an issue in the autopilot's queue, in queue order.
type QueueItem struct {
	IID             int64                   `json:"iid"`
	Title           string                  `json:"title"`
	State           aiv1.AutopilotItemState `json:"state"`
	MergeRequestIID int64                   `json:"mergeRequestIID,omitempty"`
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
	// Proposals are the open ones, waiting for an owner's yes.
	Proposals []ProposalView `json:"proposals"`
	CostUSD   string         `json:"costUSD"`
}

// ProposalView is an open proposal of the pilot.
type ProposalView struct {
	ID       int64        `json:"id"`
	Summary  string       `json:"summary"`
	PostedAt *metav1.Time `json:"postedAt,omitempty"`
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
		Spec: taskView(st, ciWindow(p), now), Tasks: []Task{}}
	var cost float64
	byIssue := map[int64]string{}
	for i := range tasks {
		t := &tasks[i]
		if t.Spec.Milestone == nil || t.Spec.Milestone.IID != ms.IID {
			continue
		}
		cost += parseCost(t.Status.Run.CostUSD)
		if t.Spec.Type == aiv1.TaskTypeImplement {
			v.Tasks = append(v.Tasks, taskView(t, ciWindow(p), now))
			if t.Spec.Issue != nil {
				byIssue[t.Spec.Issue.IID] = t.Name
			}
		}
	}
	sortTasks(v.Tasks)
	v.CostUSD = formatCost(cost)
	if ap := st.Status.Autopilot; ap != nil {
		titles := map[int64]string{}
		for _, pi := range st.Status.Plan {
			titles[pi.IID] = pi.Title
		}
		a := &MilestoneAutopilot{Phase: ap.Phase, PausedReason: ap.PausedReason, WaitingFor: waitingFor(ap, tasks), Items: []QueueItem{}}
		for _, it := range ap.Items {
			a.Items = append(a.Items, QueueItem{IID: it.IID, Title: titles[it.IID], State: it.State, MergeRequestIID: it.MergeRequestIID, Task: byIssue[it.IID]})
		}
		v.Autopilot = a
	}
	if iid := trackingIssueIID(st, coord); iid != 0 {
		v.Tracking = &TrackingIssueRef{IID: iid, WebURL: fmt.Sprintf("%s/-/issues/%d", web, iid)}
	}
	if coord != nil {
		pv := &MilestonePilot{Task: coord.Name, Autonomy: p.Spec.Pilot.AutonomyOf(coord), Proposals: []ProposalView{},
			CostUSD: formatCost(parseCost(coord.Status.Run.CostUSD))}
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
	writeJSON(w, http.StatusOK, milestoneView(ap, tasks, st, coord, time.Now()))
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
