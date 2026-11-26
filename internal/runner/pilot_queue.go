package runner

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/problem"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// queuePilot reports whether task is the pilot of the loose issues queue: a
// Coordinate task with spec.queueTask and no milestone.
func queuePilot(task *aiv1.AgentTask) bool {
	return task.Spec.Milestone == nil && task.Spec.QueueTask != ""
}

// queueFinishedItems is how many of the newest finished queue issues the
// queue's pilot sees.
const queueFinishedItems = 10

// queueIssuePages caps the issues without a milestone the queue's pilot
// reads for the items' titles: 5 pages of 100, the newest.
const queueIssuePages = 5

// queueState is what the queue's pilot sees of the loose issues queue.
type queueState struct {
	Phase        aiv1.AutopilotPhase `json:"phase"`
	PausedReason string              `json:"pausedReason,omitempty"`
	PauseKind    aiv1.PauseKind      `json:"pauseKind,omitempty"`
	// FrozenAt is when the freeze began: the default branch is red, nothing
	// starts or merges. Unset: not frozen.
	FrozenAt *metav1.Time `json:"frozenAt,omitempty"`
}

// queueItem is a pending, active or held issue of the queue.
type queueItem struct {
	IID             int64                   `json:"iid"`
	Title           string                  `json:"title"`
	Labels          []string                `json:"labels,omitempty"`
	State           aiv1.AutopilotItemState `json:"state"`
	Hold            *queueHold              `json:"hold,omitempty"`
	MergeRequestIID int64                   `json:"mergeRequestIID,omitempty"`
	MergeRequestURL string                  `json:"mergeRequestURL,omitempty"`
	Task            *queueTaskState         `json:"task,omitempty"`
}

type queueHold struct {
	Kind   aiv1.PauseKind `json:"kind"`
	Reason string         `json:"reason"`
	Since  metav1.Time    `json:"since"`
}

// queueTaskState is the state of a queue issue's implement task: its stage
// (pending before its first run, running, else the last run's result), the
// pipeline and the review.
type queueTaskState struct {
	Stage      string                 `json:"stage"`
	Summary    string                 `json:"lastRunSummary,omitempty"`
	Running    *pilotRunning          `json:"running,omitempty"`
	Blocked    *aiv1.BlockedStatus    `json:"blocked,omitempty"`
	Checkpoint *aiv1.CheckpointStatus `json:"checkpoint,omitempty"`
	CI         *aiv1.CIStatus         `json:"ci,omitempty"`
	Review     *queueReview           `json:"review,omitempty"`
}

type queueReview struct {
	PushedSHA        string `json:"pushedSHA,omitempty"`
	ReviewedSHA      string `json:"reviewedSHA,omitempty"`
	BlockingFindings int32  `json:"blockingFindings,omitempty"`
	ReviewErrors     int32  `json:"reviewErrors,omitempty"`
	FullyReviewed    bool   `json:"fullyReviewed"`
}

// queueFinished is a queue issue that merged or left.
type queueFinished struct {
	IID             int64                   `json:"iid"`
	Title           string                  `json:"title"`
	State           aiv1.AutopilotItemState `json:"state"`
	MergeRequestIID int64                   `json:"mergeRequestIID,omitempty"`
	MergedAt        *metav1.Time            `json:"mergedAt,omitempty"`
	LeftReason      string                  `json:"leftReason,omitempty"`
	LeftAt          *metav1.Time            `json:"leftAt,omitempty"`
}

type queueRound struct {
	Number    int32       `json:"number"`
	StartedAt metav1.Time `json:"startedAt"`
	// Note is the previous round's note.
	Note string `json:"note,omitempty"`
}

// queuePilotState renders the loose issues queue as JSON for its pilot: the
// queue, its pending, active and held items with their implement tasks, the
// newest finished ones, the round, the default branch's health, the last
// comments of the tracking issue, the open proposals and the engines. It
// has no milestone, spec, plan, releases, rules or retrospective. Like
// pilotState it also returns the state proposals are made on and the
// events' problem facts, for the queue's issues.
func (r *Runner) queuePilotState(ctx context.Context, gl gitlab.API, task *aiv1.AgentTask, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) (string, stateFunc, factsFunc, error) {
	glProject := spec.GitLab.Project
	var qt aiv1.AgentTask
	if err := r.K8s.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: task.Spec.QueueTask}, &qt); err != nil {
		return "", nil, nil, fmt.Errorf("getting the queue task %s: %w", task.Spec.QueueTask, err)
	}
	issues, err := gl.ListIssues(ctx, glProject, gitlab.ListIssuesOptions{NoMilestone: true, State: "all", NewestFirst: true, MaxPages: queueIssuePages})
	if err != nil {
		return "", nil, nil, fmt.Errorf("listing the issues without a milestone: %w", err)
	}
	var tasks aiv1.AgentTaskList
	if err := r.K8s.List(ctx, &tasks, client.InNamespace(r.Namespace), client.MatchingLabels{labelProject: project.Name}); err != nil {
		return "", nil, nil, fmt.Errorf("listing tasks: %w", err)
	}
	byIID := map[int64]*aiv1.AgentTask{}
	for i := range tasks.Items {
		if t := &tasks.Items[i]; t.Spec.Type == aiv1.TaskTypeImplement && t.Spec.Milestone == nil && t.Spec.Issue != nil {
			byIID[t.Spec.Issue.IID] = t
		}
	}
	gis := map[int64]gitlab.Issue{}
	for _, is := range issues {
		gis[is.IID] = is
	}
	var v struct {
		Queue      queueState             `json:"queue"`
		Items      []queueItem            `json:"items"`
		Finished   []queueFinished        `json:"recentFinished,omitempty"`
		Round      *queueRound            `json:"round,omitempty"`
		MainHealth *aiv1.MainHealthStatus `json:"mainHealth,omitempty"`
		Comments   []string               `json:"trackingIssueComments,omitempty"`
		Proposals  []aiv1.PilotProposal   `json:"openProposals,omitempty"`
		Engines    pilotEngines           `json:"engines"`
	}
	v.Queue.Phase, v.Items, v.MainHealth = aiv1.AutopilotRunning, []queueItem{}, project.Status.MainHealth
	v.Engines = enginesOf(project, spec, r.now())
	ap := qt.Status.Autopilot
	open, known := map[int64]bool{}, map[int64]problem.IssueFacts{}
	if ap != nil {
		if ap.Phase != "" {
			v.Queue.Phase = ap.Phase
		}
		v.Queue.PausedReason, v.Queue.PauseKind, v.Queue.FrozenAt = ap.PausedReason, ap.PauseKind, ap.FrozenAt
		var done []aiv1.AutopilotItem
		for _, it := range ap.Items {
			is, t := gis[it.IID], byIID[it.IID]
			title := is.Title
			if title == "" && t != nil {
				title = t.Spec.Issue.Title
			}
			known[it.IID] = problem.IssueFacts{IID: it.IID, Title: title, URL: is.WebURL, Labels: is.Labels}
			open[it.IID] = is.State == "opened"
			if it.State == aiv1.AutopilotItemMerged || it.State == aiv1.AutopilotItemLeft {
				done = append(done, it)
				continue
			}
			qi := queueItem{IID: it.IID, Title: title, Labels: is.Labels, State: it.State, MergeRequestIID: it.MergeRequestIID, MergeRequestURL: it.MergeRequestURL}
			if h := it.Hold; h != nil {
				qi.Hold = &queueHold{Kind: h.Kind, Reason: h.Reason, Since: h.Since}
			}
			if t != nil {
				qi.Task = queueTaskOf(t, spec, is.Labels)
			}
			v.Items = append(v.Items, qi)
		}
		v.Finished = newestQueueFinished(done, known)
	}
	if rd := task.Spec.Source.PilotRound; rd != nil {
		v.Round = &queueRound{Number: rd.Number, StartedAt: rd.StartedAt, Note: rd.Note}
	}
	if v.Comments, err = trackingComments(ctx, gl, glProject, task); err != nil {
		return "", nil, nil, err
	}
	if ps := task.Status.Pilot; ps != nil {
		v.Proposals = ps.Proposals
	}
	b, err := json.MarshalIndent(v, "", "  ")
	stateOf := func(a []aiv1.PilotAction) string { return aiv1.PilotState(&qt, byIID, a) }
	factsOf := func(e aiv1.PilotEvent) string {
		iid := e.IssueIID
		if iid == 0 && (strings.HasPrefix(e.Key, "pause-") || aiv1.StalledEvent(e)) {
			iid = aiv1.ProblemIssue(ap) // a pause over one held issue is its problem
		}
		is, ok := known[iid]
		if !ok {
			return ""
		}
		fb, _ := json.MarshalIndent(problem.Build(is, byIID[iid], nil, open), "", "  ")
		return string(fb)
	}
	return string(b), stateOf, factsOf, err
}

// queueTaskOf is the state of a queue issue's implement task t.
func queueTaskOf(t *aiv1.AgentTask, spec aiv1.AgentProjectSpec, labels []string) *queueTaskState {
	run := t.Status.Run
	out := &queueTaskState{Stage: string(run.LastResult), Summary: textutil.Truncate(run.Summary, 500), Running: runningOf(t, spec, labels),
		Blocked: t.Status.Blocked, Checkpoint: run.Checkpoint, CI: t.Status.CI}
	switch {
	case out.Running != nil:
		out.Stage = "running"
	case run.RunCount == 0 || out.Stage == "":
		out.Stage = "pending"
	}
	if run.PushedSHA != "" {
		out.Review = &queueReview{PushedSHA: run.PushedSHA, ReviewedSHA: run.ReviewedSHA, BlockingFindings: run.BlockingFindings,
			ReviewErrors: run.ReviewErrors, FullyReviewed: run.FullyReviewed()}
	}
	return out
}

// newestQueueFinished are the newest queueFinishedItems of the finished
// items done, newest first; of the same time the later item first.
func newestQueueFinished(done []aiv1.AutopilotItem, known map[int64]problem.IssueFacts) []queueFinished {
	at := func(it aiv1.AutopilotItem) int64 {
		switch {
		case it.State == aiv1.AutopilotItemMerged && it.MergedAt != nil:
			return it.MergedAt.UnixNano()
		case it.LeftAt != nil:
			return it.LeftAt.UnixNano()
		}
		return 0
	}
	slices.Reverse(done)
	slices.SortStableFunc(done, func(x, y aiv1.AutopilotItem) int { return cmp.Compare(at(y), at(x)) })
	var out []queueFinished
	for _, it := range done[:min(queueFinishedItems, len(done))] {
		f := queueFinished{IID: it.IID, Title: known[it.IID].Title, State: it.State}
		if it.State == aiv1.AutopilotItemMerged {
			f.MergeRequestIID, f.MergedAt = it.MergeRequestIID, it.MergedAt
		} else {
			f.LeftReason, f.LeftAt = it.LeftReason, it.LeftAt
		}
		out = append(out, f)
	}
	return out
}
