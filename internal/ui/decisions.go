package ui

import (
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

// Revisit modes of a decision: what the Desk's Revisit does.
const (
	// RevisitModeNone: nothing, it was already replaced or a follow-up
	// already revisits it.
	RevisitModeNone = "none"
	// RevisitModePrompt: open the revisit prompt of the milestone's pilot.
	RevisitModePrompt = "prompt"
	// RevisitModeComment: comment the revisit on the open issue.
	RevisitModeComment = "comment"
	// RevisitModeCommand: copy the command and comment it on GitLab.
	RevisitModeCommand = "command"
)

// maxDecisionQuery is the longest search text, in characters.
const maxDecisionQuery = 200

// DecisionsResponse is the decisions of a project or one milestone.
// Available is false without the history database.
type DecisionsResponse struct {
	Available bool           `json:"available"`
	Decisions []DecisionView `json:"decisions"`
}

// DecisionView is one stored decision with its chain and what revisiting it
// does. Kind is planner or implementer; URL is the GitLab comment.
type DecisionView struct {
	ID           string               `json:"id"`
	Kind         string               `json:"kind" tstype:"'planner' | 'implementer'"`
	Title        string               `json:"title"`
	Question     string               `json:"question"`
	Choice       string               `json:"choice"`
	Why          string               `json:"why"`
	Alternatives []string             `json:"alternatives"`
	RunID        string               `json:"runID,omitempty"`
	URL          string               `json:"url,omitempty"`
	PostedAt     time.Time            `json:"postedAt"`
	MilestoneIID int64                `json:"milestoneIID"`
	Issue        DecisionIssueView    `json:"issue"`
	Supersedes   string               `json:"supersedes,omitempty"`
	Superseded   bool                 `json:"superseded"`
	SupersededBy string               `json:"supersededBy,omitempty"`
	Revisits     []RevisitRequestView `json:"revisits"`
	Revisit      RevisitView          `json:"revisit"`
}

// DecisionIssueView is the issue a decision belongs to. State is opened or
// closed; Task is its live implement task, "" without one.
type DecisionIssueView struct {
	IID   int64  `json:"iid"`
	Title string `json:"title"`
	State string `json:"state"`
	Task  string `json:"task,omitempty"`
}

// RevisitRequestView is one request to revisit a decision. Source is
// comment or followUp; FollowUpIID the follow-up issue (0 for a comment) and
// FollowUpState its state. Applied: a decision supersedes it.
type RevisitRequestView struct {
	Source        string    `json:"source" tstype:"'comment' | 'followUp'"`
	RequestedBy   string    `json:"requestedBy"`
	Instruction   string    `json:"instruction"`
	URL           string    `json:"url,omitempty"`
	FollowUpIID   int64     `json:"followUpIID,omitempty"`
	FollowUpState string    `json:"followUpState,omitempty"`
	At            time.Time `json:"at"`
	Applied       bool      `json:"applied"`
}

// RevisitView is what the Desk's Revisit of a decision does (Mode, a
// RevisitMode constant, "" for a user who may not revisit) and the line that
// says so. Task is the task to send it to: the pilot for prompt, the
// implement task for comment. Command is the command to comment, for
// command.
type RevisitView struct {
	Mode    string `json:"mode" tstype:"RevisitMode | ''"`
	Text    string `json:"text"`
	Task    string `json:"task,omitempty"`
	Command string `json:"command,omitempty"`
}

type decisionReader interface {
	Decisions(ctx context.Context, q store.DecisionQuery) ([]store.DecisionResult, error)
}

// decisions lists the project's stored decisions: ?milestone=<iid> (empty or
// 0 for all), ?q= (search) and ?state=all|active|superseded.
func (s *Server) decisions(w http.ResponseWriter, r *http.Request) {
	ap, tasks, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	var ms int64
	if m := query.Get("milestone"); m != "" {
		iid, err := strconv.ParseInt(m, 10, 64)
		if err != nil || iid < 0 {
			badRequest(w, "milestone must be an IID")
			return
		}
		ms = iid
	}
	var st decisionReader = s.decisionReads
	if db := s.research.Load(); st == nil && db != nil {
		st = db
	}
	if st == nil {
		writeJSON(w, http.StatusOK, DecisionsResponse{Decisions: []DecisionView{}})
		return
	}
	q := strings.TrimSpace(query.Get("q"))
	if rs := []rune(q); len(rs) > maxDecisionQuery {
		q = string(rs[:maxDecisionQuery])
	}
	rows, err := st.Decisions(r.Context(), store.DecisionQuery{Project: store.ProjectKey{Namespace: ap.Namespace, Name: ap.Name},
		Milestone: ms, Q: q, State: query.Get("state")})
	if err != nil {
		s.internalError(w, err)
		return
	}
	p := principalOf(r)
	chat := s.cfg.ChatEnabled() && s.authz.CanChat(p, ap.Namespace, ap.Name)
	steer := s.gitlab != nil && s.gitlab.serves(ap) && s.authz.CanSteer(p, ap.Namespace, ap.Name)
	wf := ap.Spec.Workflow.WithDefaults()
	byMilestone := map[int64]milestoneTaskSet{}
	out := DecisionsResponse{Available: true, Decisions: make([]DecisionView, 0, len(rows))}
	for _, d := range rows {
		mt, seen := byMilestone[d.MilestoneIID]
		if !seen {
			mt = milestoneTaskSetOf(tasks, d.MilestoneIID)
			byMilestone[d.MilestoneIID] = mt
		}
		mt.implement = liveIssueTask(tasks, d.IssueIID)
		mt.ready, mt.prefix = wf.ReadyLabel, wf.CommandPrefix
		owner := chat && mt.spec != nil && slices.Contains(mt.spec.Spec.OwnerSubjects, p.Username)
		out.Decisions = append(out.Decisions, decisionView(d, mt, owner, steer))
	}
	writeJSON(w, http.StatusOK, out)
}

// milestoneTaskSet is what the revisit mode of a decision depends on: the
// milestone's spec task, its Coordinate (pilot) task, the live implement task
// of the decision's issue (nil where there is none), whether autopilot is on,
// and the project's ready label and command prefix.
type milestoneTaskSet struct {
	spec, pilot, implement *aiv1.AgentTask
	autopilot              bool
	ready, prefix          string
}

// milestoneTaskSetOf is the milestone's spec and pilot task and its
// autopilot state, as revisitAfterMerge decides it.
func milestoneTaskSetOf(tasks []aiv1.AgentTask, iid int64) milestoneTaskSet {
	st, coord := milestoneTasks(tasks, iid)
	ap := st != nil && st.Spec.Autopilot && st.Status.Autopilot != nil && st.Status.Autopilot.Phase != aiv1.AutopilotOff
	return milestoneTaskSet{spec: st, pilot: coord, autopilot: ap}
}

// liveIssueTask is the issue's implement task unless it is archived, nil
// without one.
func liveIssueTask(tasks []aiv1.AgentTask, iid int64) *aiv1.AgentTask {
	if t := issueTask(tasks, iid); t != nil && t.Status.Phase != aiv1.TaskPhaseArchived {
		return t
	}
	return nil
}

// decisionView is the view of d with its revisit mode.
func decisionView(d store.DecisionResult, mt milestoneTaskSet, owner, steer bool) DecisionView {
	v := DecisionView{ID: d.ID, Kind: d.Kind, Title: d.Title, Question: d.Question, Choice: d.Choice, Why: d.Why,
		Alternatives: d.Alternatives, RunID: d.RunID, URL: d.URL, PostedAt: d.PostedAt, MilestoneIID: d.MilestoneIID,
		Issue:      DecisionIssueView{IID: d.IssueIID, Title: d.IssueTitle, State: d.IssueState},
		Supersedes: d.Supersedes, Superseded: d.Superseded, SupersededBy: d.SupersededBy,
		Revisits: make([]RevisitRequestView, 0, len(d.Revisits)), Revisit: revisitOf(d, mt, owner, steer)}
	if v.Alternatives == nil {
		v.Alternatives = []string{}
	}
	if mt.implement != nil {
		v.Issue.Task = mt.implement.Name
	}
	for _, r := range d.Revisits {
		v.Revisits = append(v.Revisits, RevisitRequestView{Source: r.Source, RequestedBy: r.RequestedBy,
			Instruction: r.Instruction, URL: r.URL, FollowUpIID: r.FollowUpIID, FollowUpState: r.FollowUpState,
			At: r.At, Applied: r.Applied})
	}
	return v
}

// revisitOf decides what revisiting d does: nothing when it is superseded or
// an open follow-up already revisits it; the pilot's revisit prompt for a
// closed issue; a comment on the open issue with a live implement task when
// the user may steer (post in their GitLab name); else the command to
// comment. owner: the user may revisit (chat and one of the milestone's
// owner subjects); otherwise the mode is "" (except none) and Task is empty.
func revisitOf(d store.DecisionResult, mt milestoneTaskSet, owner, steer bool) RevisitView {
	if d.Superseded {
		if d.SupersededBy == "" {
			return RevisitView{Mode: RevisitModeNone, Text: "Already replaced."}
		}
		return RevisitView{Mode: RevisitModeNone, Text: "Already replaced by " + d.SupersededBy + "."}
	}
	for _, r := range d.Revisits {
		if r.FollowUpIID != 0 && r.FollowUpState == "opened" && !r.Applied {
			return RevisitView{Mode: RevisitModeNone, Text: fmt.Sprintf("#%d already revisits %s; comment there.", r.FollowUpIID, d.ID)}
		}
	}
	var v RevisitView
	switch {
	case d.IssueState == "closed" && mt.pilot != nil:
		v = RevisitView{Mode: RevisitModePrompt, Task: mt.pilot.Name}
		if mt.autopilot {
			v.Text = "Opens a revisit prompt. Your choice creates a follow-up issue in this milestone, queued after the planned issues. " +
				d.ID + " is marked superseded when the follow-up posts its new decision."
		} else {
			v.Text = "Opens a revisit prompt. Your choice creates a follow-up issue in this milestone, and the follow-up starts right away (" +
				mt.ready + "). " + d.ID + " is marked superseded when the follow-up posts its new decision."
		}
	case d.IssueState == "opened" && mt.implement != nil && steer:
		v = RevisitView{Mode: RevisitModeComment, Task: mt.implement.Name,
			Text: fmt.Sprintf("Comments on #%d. Its next run changes the code and posts a decision that supersedes %s.", d.IssueIID, d.ID)}
	default:
		v = RevisitView{Mode: RevisitModeCommand, Command: mt.prefix + " revisit " + d.ID + " ",
			Text: fmt.Sprintf("Comment this command on #%d or the tracking issue.", d.IssueIID)}
	}
	if !owner {
		v.Mode, v.Task = "", ""
	}
	return v
}
