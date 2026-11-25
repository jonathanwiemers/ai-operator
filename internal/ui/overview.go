package ui

import (
	"cmp"
	"context"
	"net/http"
	"net/url"
	"slices"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// OverviewView is the answer of GET /api/v1/overview: the dashboard's
// summary strip and Working now. Running, paused and waiting count the
// visible milestones by state; NeedsYou is the Inbox's count. SpendToday
// (the browser's day from tz, UTC when it is missing or unknown) and Spend7d (it and the 6 days before) are absent without
// the history database: unknown, not $0. History: the database answered.
type OverviewView struct {
	Running    int          `json:"running"`
	Paused     int          `json:"paused"`
	Waiting    int          `json:"waiting"`
	NeedsYou   int          `json:"needsYou"`
	SpendToday *Cost        `json:"spendToday,omitempty"`
	Spend7d    *Cost        `json:"spend7d,omitempty"`
	Working    []WorkingNow `json:"working"`
	History    bool         `json:"history"`
}

// WorkingNext is the item a project runs next: the first queued task, else
// the next pending issue of its oldest running autopilot.
type WorkingNext struct {
	IssueIID     int64  `json:"issueIID"`
	Title        string `json:"title"`
	MilestoneIID int64  `json:"milestoneIID,omitempty"`
}

// HeldItem is an issue of a running autopilot that a hold keeps back, with
// the hold's reason.
type HeldItem struct {
	IssueIID     int64  `json:"issueIID"`
	Title        string `json:"title,omitempty"`
	MilestoneIID int64  `json:"milestoneIID"`
	Reason       string `json:"reason,omitempty"`
}

// OverviewMilestone is a milestone of the project queue: the one that runs,
// and the next one in rank that is not paused.
type OverviewMilestone struct {
	IID   int64  `json:"iid"`
	Title string `json:"title"`
	State string `json:"state" tstype:"'running' | 'next'"`
}

// WorkingNow is one running implement task of a project, which holds a run
// slot since Since, or a project with an autopilot, queued work or finishing
// milestones and no running task (no issue then). Step and StepSince are the
// run's current step (empty between runs), CI the state of the pushed head's
// pipeline. Next, Finishing, Held, CIWaits, Milestones and the engine limits
// are the project's, repeated on each of its entries.
type WorkingNow struct {
	Namespace    string              `json:"namespace"`
	Project      string              `json:"project"`
	IssueIID     int64               `json:"issueIID,omitempty"`
	Title        string              `json:"title,omitempty"`
	MilestoneIID int64               `json:"milestoneIID,omitempty"`
	Task         string              `json:"task,omitempty"`
	Since        *time.Time          `json:"since,omitempty"`
	Step         string              `json:"step,omitempty"`
	StepSince    *time.Time          `json:"stepSince,omitempty"`
	CI           aiv1.CIState        `json:"ci,omitempty" tstype:"aiv1.CIState"`
	Next         *WorkingNext        `json:"next,omitempty"`
	Finishing    int                 `json:"finishing,omitempty"`
	Held         []HeldItem          `json:"held,omitempty"`
	CIWaits      []WorkingNext       `json:"ciWaits,omitempty"`
	Milestones   []OverviewMilestone `json:"milestones,omitempty"`
	UsageLimits  []aiv1.UsageLimit   `json:"usageLimits,omitempty"`
	EngineUsage  []aiv1.EngineUsage  `json:"engineUsage,omitempty"`
}

// spendSincer sums the spend of projects from a time on (the history
// store).
type spendSincer interface {
	SpendSince(ctx context.Context, projects []store.ProjectKey, since time.Time) (store.SpendTotals, error)
}

// overview answers the summary strip and Working now of the projects the
// user may see, from the live tasks and, for spend, the history database.
func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	visible, tasks, ok := s.visibleProjects(w, r)
	if !ok {
		return
	}
	now := time.Now()
	out := OverviewView{NeedsYou: len(inboxOf(visible, tasks, now)), Working: workingNow(visible, tasks, now)}
	for _, m := range liveMilestoneRows(visible, tasks, now) {
		switch m.State {
		case MilestoneStateRunning:
			out.Running++
		case MilestoneStatePaused:
			out.Paused++
		case MilestoneStateWaiting:
			out.Waiting++
		}
	}
	var st spendSincer = s.overviewSpend
	if st == nil {
		if ss := s.spendStore(); ss != nil {
			st = ss
		}
	}
	if st != nil {
		keys := make([]store.ProjectKey, 0, len(visible))
		for _, ap := range visible {
			keys = append(keys, store.ProjectKey{Namespace: ap.Namespace, Name: ap.Name})
		}
		today, weekStart := overviewDays(now, r.URL.Query())
		day, err := st.SpendSince(r.Context(), keys, today)
		var week store.SpendTotals
		if err == nil {
			week, err = st.SpendSince(r.Context(), keys, weekStart)
		}
		if err != nil {
			s.log.Error(err, "cannot read the overview's spend")
		} else {
			out.SpendToday, out.Spend7d, out.History = overviewCost(day), overviewCost(week), true
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// overviewDays returns the start of the browser's day and of the week (6 days
// before it) from the tz query. A missing, unknown or "Local" zone is UTC:
// the dashboard never fails over it.
func overviewDays(now time.Time, q url.Values) (today, weekStart time.Time) {
	loc := time.UTC
	if name := q.Get("tz"); name != "" && name != "Local" {
		if l, err := time.LoadLocation(name); err == nil {
			loc = l
		}
	}
	today = localDay(now, loc)
	return today, today.AddDate(0, 0, -6)
}

// overviewCost is a cost chip without the breakdown.
func overviewCost(t store.SpendTotals) *Cost {
	c := callsCost(t)
	c.Spend = nil
	return &c
}

// workingNow lists the projects' running implement tasks, and the projects
// with an active autopilot, queued work or finishing milestones but none
// running, by project, the longest running first. Finishing is len of the
// same list ProjectDetail.Finishing uses.
func workingNow(projects map[string]*aiv1.AgentProject, tasks []aiv1.AgentTask, now time.Time) []WorkingNow {
	out := []WorkingNow{}
	for _, ap := range projects {
		pt := projectTasks(tasks, ap)
		base := WorkingNow{Namespace: ap.Namespace, Project: ap.Name, Next: nextItem(pt),
			Finishing: len(finishing(pt, gitLabRef(ap).WebURL, now)), Held: heldItems(pt),
			CIWaits: ciWaits(pt), Milestones: overviewMilestones(queue(ap.Status.MilestoneOrder, pt, nil)),
			UsageLimits: ap.Status.UsageLimits, EngineUsage: ap.Status.EngineUsage}
		active := base.Next != nil || base.Finishing > 0 || len(base.CIWaits) > 0
		running := 0
		for i := range pt {
			t := &pt[i]
			if a := t.Status.Autopilot; a != nil && (a.Phase == aiv1.AutopilotRunning || a.Phase == aiv1.AutopilotPaused) {
				active = true
			}
			if t.Spec.Type != aiv1.TaskTypeImplement || t.Status.Phase != aiv1.TaskPhaseRunning || t.Spec.Issue == nil {
				continue
			}
			e := base
			e.IssueIID, e.Title, e.Task = t.Spec.Issue.IID, t.Spec.Issue.Title, t.Name
			if t.Spec.Milestone != nil {
				e.MilestoneIID = t.Spec.Milestone.IID
			}
			if a := t.Status.Run.LastActivityTime; a != nil {
				e.Since = optTime(a.Time)
			}
			if e.Step = t.Status.Run.CurrentStep; e.Step != "" && t.Status.Run.CurrentStepSince != nil {
				e.StepSince = optTime(t.Status.Run.CurrentStepSince.Time)
			}
			if t.Status.CI != nil {
				e.CI = t.Status.CI.State
			}
			out = append(out, e)
			running++
		}
		if running == 0 && active {
			out = append(out, base)
		}
	}
	slices.SortStableFunc(out, func(a, b WorkingNow) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Project, b.Project),
			compareTimes(a.Since, b.Since, -1), cmp.Compare(a.IssueIID, b.IssueIID))
	})
	return out
}

// nextItem is what the project's tasks run next: the queued implement task
// first in the operator's queue order (oldest milestone first), else the
// first pending issue of the oldest running autopilot. nil when nothing is
// next.
func nextItem(tasks []aiv1.AgentTask) *WorkingNext {
	var first *aiv1.AgentTask
	for i := range tasks {
		t := &tasks[i]
		if t.Spec.Type == aiv1.TaskTypeImplement && t.Status.Phase == aiv1.TaskPhaseQueued && t.Spec.Issue != nil &&
			!t.Spec.Suspend && (first == nil || aiv1.QueueLess(t, first)) {
			first = t
		}
	}
	if first != nil {
		n := &WorkingNext{IssueIID: first.Spec.Issue.IID, Title: first.Spec.Issue.Title}
		if first.Spec.Milestone != nil {
			n.MilestoneIID = first.Spec.Milestone.IID
		}
		return n
	}
	var spec *aiv1.AgentTask
	for i := range tasks {
		t := &tasks[i]
		if a := t.Status.Autopilot; t.Spec.Type == aiv1.TaskTypeSpec && t.Spec.Milestone != nil && a != nil &&
			a.Phase == aiv1.AutopilotRunning && slices.ContainsFunc(a.Items, pendingItem) &&
			(spec == nil || t.Spec.Milestone.IID < spec.Spec.Milestone.IID) {
			spec = t
		}
	}
	if spec == nil {
		return nil
	}
	iid := spec.Status.Autopilot.Items[slices.IndexFunc(spec.Status.Autopilot.Items, pendingItem)].IID
	return &WorkingNext{IssueIID: iid, Title: itemTitle(spec, tasks, iid), MilestoneIID: spec.Spec.Milestone.IID}
}

// itemTitle is the title of issue iid of spec's milestone: the plan's, else
// that of a task of the issue.
func itemTitle(spec *aiv1.AgentTask, tasks []aiv1.AgentTask, iid int64) string {
	if i := slices.IndexFunc(spec.Status.Plan, func(p aiv1.PlannedIssue) bool { return p.IID == iid }); i >= 0 && spec.Status.Plan[i].Title != "" {
		return spec.Status.Plan[i].Title
	}
	for i := range tasks {
		if t := &tasks[i]; t.Spec.Issue != nil && t.Spec.Issue.IID == iid && t.Spec.Issue.Title != "" {
			return t.Spec.Issue.Title
		}
	}
	return ""
}

// heldItems lists the active issues with a hold of the project's running
// autopilots, by milestone and issue.
func heldItems(tasks []aiv1.AgentTask) []HeldItem {
	var out []HeldItem
	for i := range tasks {
		t := &tasks[i]
		a := t.Status.Autopilot
		if t.Spec.Type != aiv1.TaskTypeSpec || t.Spec.Milestone == nil || a == nil || a.Phase != aiv1.AutopilotRunning {
			continue
		}
		for _, it := range a.Items {
			if it.Hold != nil && it.State == aiv1.AutopilotItemActive {
				out = append(out, HeldItem{IssueIID: it.IID, Title: itemTitle(t, tasks, it.IID),
					MilestoneIID: t.Spec.Milestone.IID, Reason: it.Hold.Reason})
			}
		}
	}
	slices.SortFunc(out, func(a, b HeldItem) int {
		return cmp.Or(cmp.Compare(a.MilestoneIID, b.MilestoneIID), cmp.Compare(a.IssueIID, b.IssueIID))
	})
	return out
}

// ciWaits lists the issues whose implement task is between runs (Idle or
// Hibernated) while the pipeline of its pushed head is still to finish, by
// milestone and issue: they wait for CI.
func ciWaits(tasks []aiv1.AgentTask) []WorkingNext {
	var out []WorkingNext
	for i := range tasks {
		t := &tasks[i]
		between := t.Status.Phase == aiv1.TaskPhaseIdle || t.Status.Phase == aiv1.TaskPhaseHibernated
		if t.Spec.Type != aiv1.TaskTypeImplement || t.Spec.Issue == nil || !between || t.Status.CI == nil || (t.Status.CI.State != aiv1.CIStatePending && t.Status.CI.State != aiv1.CIStateRunning) {
			continue
		}
		w := WorkingNext{IssueIID: t.Spec.Issue.IID, Title: t.Spec.Issue.Title}
		if t.Spec.Milestone != nil {
			w.MilestoneIID = t.Spec.Milestone.IID
		}
		out = append(out, w)
	}
	slices.SortFunc(out, func(a, b WorkingNext) int {
		return cmp.Or(cmp.Compare(a.MilestoneIID, b.MilestoneIID), cmp.Compare(a.IssueIID, b.IssueIID))
	})
	return out
}

// overviewMilestones picks from the project queue (queue, the project
// page's) the milestone that runs and the first after it that waits for
// its turn: paused milestones are neither.
func overviewMilestones(q []QueueEntry) []OverviewMilestone {
	var out []OverviewMilestone
	for _, e := range q {
		switch {
		case e.State == "running" && len(out) == 0:
			out = append(out, OverviewMilestone{IID: e.IID, Title: e.Title, State: "running"})
		case e.State == "holding" || e.State == "waiting":
			return append(out, OverviewMilestone{IID: e.IID, Title: e.Title, State: "next"})
		}
	}
	return out
}

func pendingItem(it aiv1.AutopilotItem) bool {
	return it.State == aiv1.AutopilotItemPending && it.IID != 0
}
