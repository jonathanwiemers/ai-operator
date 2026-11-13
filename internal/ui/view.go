package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// The API returns these views instead of the raw resources: they leave out
// the operator's bookkeeping (spec.source, secret references) and add what
// the UI derives, so the API stays stable when the CRDs change.

// Stage is where a task stands, in the terms of the GitLab board.
type Stage string

const (
	// StagePending: nothing ran yet and no run is requested.
	StagePending Stage = "pending"
	// StageWorking: a run is requested or in progress.
	StageWorking Stage = "working"
	// StageWaiting: a run is requested but waits for capacity or for a
	// usage limit to reset.
	StageWaiting Stage = "waiting"
	// StageNeedsInput: the agent has questions, or CI is still red after the
	// fix rounds.
	StageNeedsInput Stage = "needs-input"
	// StageCI: the pipeline of the agent's push runs or is being fixed.
	StageCI Stage = "ci"
	// StageReview: the merge request waits for a human.
	StageReview Stage = "review"
	// StagePlanned: the spec agent planned the milestone.
	StagePlanned Stage = "planned"
	// StageFailed: the last run failed.
	StageFailed Stage = "failed"
	// StageBlocked: the task hit a safety limit.
	StageBlocked Stage = "blocked"
)

// StageOf derives a task's stage from its status at now. ciWindow is how
// long a push waits for its pipeline to be followed (see ciWindow), zero
// for projects without the CI loop.
func StageOf(t *aiv1.AgentTask, ciWindow time.Duration, now time.Time) Stage {
	if t.Status.Phase == aiv1.TaskPhaseBlocked {
		return StageBlocked
	}
	if t.RunPending() {
		switch {
		case t.Spec.Suspend, t.Status.Phase == aiv1.TaskPhaseQueued, t.Status.Phase == aiv1.TaskPhaseWaiting:
			return StageWaiting
		}
		return StageWorking
	}
	switch t.Status.Run.LastResult {
	case aiv1.RunResultNeedsInput:
		return StageNeedsInput
	case aiv1.RunResultFailed:
		return StageFailed
	case aiv1.RunResultDeferred:
		return StageWaiting
	case "":
		return StagePending
	}
	if t.Spec.Type == aiv1.TaskTypeSpec {
		return StagePlanned
	}
	// The operator writes the pipeline of a new push on its next poll;
	// until then the issue is still waiting for CI. A push that is still not
	// followed after the window never will be (the issue went to review
	// before the operator saw it); the CI status of an older commit then
	// says nothing about it.
	run := t.Status.Run
	if ciWindow > 0 && run.PushedSHA != "" && run.PushedAt != nil && (t.Status.CI == nil || t.Status.CI.SHA != run.PushedSHA) {
		if now.Sub(run.PushedAt.Time) < ciWindow {
			return StageCI
		}
		return StageReview
	}
	if ci := t.Status.CI; ci != nil {
		switch {
		case ci.State == aiv1.CIStateFailed:
			return StageNeedsInput
		case !ci.State.Final():
			return StageCI
		}
	}
	return StageReview
}

// GitLabRef points to the project in GitLab.
type GitLabRef struct {
	URL     string `json:"url"`
	Project string `json:"project"`
	WebURL  string `json:"webURL"`
}

// ProjectSummary is an AgentProject with counts over its tasks.
type ProjectSummary struct {
	Namespace         string              `json:"namespace"`
	Name              string              `json:"name"`
	GitLab            GitLabRef           `json:"gitlab"`
	Suspend           bool                `json:"suspend"`
	MaxConcurrentRuns int32               `json:"maxConcurrentRuns"`
	PollInterval      string              `json:"pollInterval"`
	LastSyncTime      *metav1.Time        `json:"lastSyncTime,omitempty"`
	UsageLimits       []aiv1.UsageLimit   `json:"usageLimits,omitempty"`
	EngineUsage       []aiv1.EngineUsage  `json:"engineUsage,omitempty"`
	Conditions        []metav1.Condition  `json:"conditions,omitempty"`
	Tasks             int                 `json:"tasks"`
	Stages            map[Stage]int       `json:"stages" tstype:"{ [key in Stage]?: number }"`
	CreatedAt         metav1.Time         `json:"createdAt"`
	Autopilots        []AutopilotOverview `json:"autopilots,omitempty"`

	// Cost sums the tasks here; the project endpoint takes it from
	// agent_calls when the history database is connected.
	Cost `tstype:",extends"`
}

// AutopilotOverview is the state of one autopilot milestone.
type AutopilotOverview struct {
	MilestoneIID int64               `json:"milestoneIID"`
	Title        string              `json:"title"`
	Phase        aiv1.AutopilotPhase `json:"phase"`
	PausedReason string              `json:"pausedReason,omitempty"`
	// WaitingFor is the issue a paused autopilot waits for: a run is working
	// on it, and autopilot continues by itself once it passes the gate.
	WaitingFor int64 `json:"waitingFor,omitempty"`
	Done       int   `json:"done"`
	Total      int   `json:"total"`
}

// ProjectDetail is a project with its tasks grouped by milestone.
type ProjectDetail struct {
	ProjectSummary `tstype:",extends"`
	// Milestones, newest first.
	Milestones []Milestone `json:"milestones"`
	// Unplanned are tasks of issues without a milestone.
	Unplanned []Task `json:"unplanned"`
	// Queue is in run order; queueCommands: Desk chat on the project.
	Queue         []QueueEntry `json:"queue"`
	QueueCommands bool         `json:"queueCommands"`
	// Finishing are the done milestones still open on GitLab, oldest first.
	Finishing []FinishingEntry `json:"finishing"`
}

// Milestone groups the tasks of one milestone.
type Milestone struct {
	ID     int64  `json:"id"`
	IID    int64  `json:"iid"`
	Title  string `json:"title"`
	WebURL string `json:"webURL"`
	// Spec is the milestone's spec task, if it is planned by the agent.
	Spec *Task `json:"spec,omitempty"`
	// TrackingIssueIID is the milestone's tracking issue (its chat), 0
	// without one.
	TrackingIssueIID int64 `json:"trackingIssueIID,omitempty"`
	// Tasks are the implement tasks, by issue IID.
	Tasks []Task `json:"tasks"`
	Cost  `tstype:",extends"`
}

// TaskResponse is GET .../tasks/{task}. prompts: the agent's questions
// (Implement tasks), newest first; always an array.
type TaskResponse struct {
	Project     ProjectSummary   `json:"project"`
	Task        Task             `json:"task"`
	Prompts     []PromptView     `json:"prompts"`
	Cost        Cost             `json:"cost"`
	Permissions *TaskPermissions `json:"permissions,omitempty"`
}

// TaskPermissions is what the task page may offer; the endpoints check
// again. transcript: the live transcript (and prompts) of the task's runs.
// gitlab: the project's GitLab is connected to the UI (thread after
// connecting your account); steer: you may post comments. chat: Desk chat
// with the task's agent (no GitLab token).
type TaskPermissions struct {
	Transcript bool `json:"transcript"`
	GitLab     bool `json:"gitlab"`
	Steer      bool `json:"steer"`
	Chat       bool `json:"chat"`
}

// Task is an AgentTask.
type Task struct {
	Namespace  string               `json:"namespace"`
	Name       string               `json:"name"`
	Project    string               `json:"project"`
	Type       aiv1.TaskType        `json:"type"`
	Milestone  *aiv1.MilestoneRef   `json:"milestone,omitempty"`
	Issue      *aiv1.IssueRef       `json:"issue,omitempty"`
	Suspend    bool                 `json:"suspend"`
	Autopilot  bool                 `json:"autopilot"`
	RunRequest int64                `json:"runRequest"`
	RunPending bool                 `json:"runPending"`
	Stage      Stage                `json:"stage"`
	CreatedAt  metav1.Time          `json:"createdAt"`
	Status     aiv1.AgentTaskStatus `json:"status"`
	// Companion is the issue's companion merge request, with the landing
	// state of the project's status once the home change is merged; the
	// issue and merge request cards show it as a chip.
	Companion *CompanionView `json:"companion,omitempty"`
}

func gitLabRef(p *aiv1.AgentProject) GitLabRef {
	base := strings.TrimSuffix(p.Spec.GitLab.URL, "/")
	if base == "" {
		base = "https://gitlab.com"
	}
	return GitLabRef{URL: base, Project: p.Spec.GitLab.Project, WebURL: base + "/" + p.Spec.GitLab.Project}
}

// ciWindow is how long the operator may take to follow the pipeline of a
// push: its no-pipeline timeout, checked on a poll, so plus one poll
// interval. Zero without the CI loop.
func ciWindow(p *aiv1.AgentProject) time.Duration {
	if p.Spec.CI.Disabled {
		return 0
	}
	timeout, poll := p.Spec.CI.NoPipelineTimeout.Duration, p.Spec.PollInterval.Duration
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	if poll <= 0 {
		poll = time.Minute
	}
	return timeout + poll
}

// taskView is the view of t, a task of p.
func taskView(t *aiv1.AgentTask, p *aiv1.AgentProject, now time.Time) Task {
	return Task{
		Namespace:  t.Namespace,
		Name:       t.Name,
		Project:    t.Spec.ProjectRef,
		Type:       t.Spec.Type,
		Milestone:  t.Spec.Milestone,
		Issue:      t.Spec.Issue,
		Suspend:    t.Spec.Suspend,
		Autopilot:  t.Spec.Autopilot,
		RunRequest: t.Spec.RunRequest,
		RunPending: t.RunPending(),
		Stage:      StageOf(t, ciWindow(p), now),
		CreatedAt:  t.CreationTimestamp,
		Status:     t.Status,
		Companion:  taskCompanion(t, p),
	}
}

// summarize builds the summary of p from its tasks.
func summarize(p *aiv1.AgentProject, tasks []aiv1.AgentTask, now time.Time) ProjectSummary {
	s := ProjectSummary{
		Namespace:         p.Namespace,
		Name:              p.Name,
		GitLab:            gitLabRef(p),
		Suspend:           p.Spec.Suspend,
		MaxConcurrentRuns: p.Spec.MaxConcurrentRuns,
		PollInterval:      shortDuration(p.Spec.PollInterval.Duration),
		LastSyncTime:      p.Status.LastSyncTime,
		UsageLimits:       p.Status.UsageLimits,
		EngineUsage:       p.Status.EngineUsage,
		Conditions:        p.Status.Conditions,
		Tasks:             len(tasks),
		Stages:            map[Stage]int{},
		CreatedAt:         p.CreationTimestamp,
	}
	var cost float64
	for i := range tasks {
		t := &tasks[i]
		s.Stages[StageOf(t, ciWindow(p), now)]++
		cost += aiv1.CostOf(t.Status.Run.CostUSD)
		if ap := t.Status.Autopilot; ap != nil && t.Spec.Milestone != nil {
			o := AutopilotOverview{
				MilestoneIID: t.Spec.Milestone.IID,
				Title:        t.Spec.Milestone.Title,
				Phase:        ap.Phase,
				PausedReason: ap.PausedReason,
				WaitingFor:   waitingFor(ap, tasks),
				Done:         itemsDone(ap),
				Total:        len(ap.Items),
			}
			s.Autopilots = append(s.Autopilots, o)
		}
	}
	sort.Slice(s.Autopilots, func(i, j int) bool { return s.Autopilots[i].MilestoneIID > s.Autopilots[j].MilestoneIID })
	s.Cost = tasksCost(cost)
	return s
}

// detail groups the tasks of p by milestone.
func detail(p *aiv1.AgentProject, tasks []aiv1.AgentTask, now time.Time) ProjectDetail {
	d := ProjectDetail{ProjectSummary: summarize(p, tasks, now), Milestones: []Milestone{}, Unplanned: []Task{}}
	gl := d.GitLab
	byIID := map[int64]*Milestone{}
	costs := map[int64]float64{}
	for i := range tasks {
		t := &tasks[i]
		v := taskView(t, p, now)
		ms := t.Spec.Milestone
		if ms == nil {
			d.Unplanned = append(d.Unplanned, v)
			continue
		}
		m := byIID[ms.IID]
		if m == nil {
			m = &Milestone{
				ID:     ms.ID,
				IID:    ms.IID,
				Title:  ms.Title,
				WebURL: fmt.Sprintf("%s/-/milestones/%d", gl.WebURL, ms.IID),
				Tasks:  []Task{},
			}
			byIID[ms.IID] = m
		}
		costs[ms.IID] += aiv1.CostOf(t.Status.Run.CostUSD)
		if t.Spec.Type == aiv1.TaskTypeSpec {
			m.Spec = &v
			// The spec task carries the milestone's current title.
			m.Title = ms.Title
		} else {
			m.Tasks = append(m.Tasks, v)
		}
	}
	for iid, m := range byIID {
		if st, coord := milestoneTasks(tasks, iid); st != nil {
			m.TrackingIssueIID = trackingIssueIID(st, coord)
		}
		sortTasks(m.Tasks)
		m.Cost = tasksCost(costs[iid])
		d.Milestones = append(d.Milestones, *m)
	}
	sort.Slice(d.Milestones, func(i, j int) bool { return d.Milestones[i].IID > d.Milestones[j].IID })
	sortTasks(d.Unplanned)
	d.Queue = queue(tasks, costs)
	d.Finishing = finishing(tasks, gl.WebURL, now)
	return d
}

func sortTasks(ts []Task) {
	sort.Slice(ts, func(i, j int) bool {
		a, b := issueIID(ts[i]), issueIID(ts[j])
		if a != b {
			return a < b
		}
		return ts[i].Name < ts[j].Name
	})
}

func issueIID(t Task) int64 {
	if t.Issue == nil {
		return 0
	}
	return t.Issue.IID
}

// shortDuration drops the zero units Go prints: 1m instead of 1m0s.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// waitingFor returns the issue a paused autopilot waits for: the pause is
// over that issue's run, review or CI (it ends by itself), and a run is
// working on the issue now. 0 otherwise: the pause needs a human.
func waitingFor(ap *aiv1.AutopilotStatus, tasks []aiv1.AgentTask) int64 {
	if ap.Phase != aiv1.AutopilotPaused || !ap.ResumeWhenReady || ap.PausedFor == 0 {
		return 0
	}
	for i := range tasks {
		if t := &tasks[i]; t.Spec.Issue != nil && t.Spec.Issue.IID == ap.PausedFor && t.RunPending() {
			return ap.PausedFor
		}
	}
	return 0
}
