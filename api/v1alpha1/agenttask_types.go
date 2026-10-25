package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TaskType is the kind of work an AgentTask performs.
// +kubebuilder:validation:Enum=Spec;Implement;Coordinate
type TaskType string

const (
	// TaskTypeSpec turns a milestone description into implementable issues.
	TaskTypeSpec TaskType = "Spec"
	// TaskTypeImplement implements one issue as one merge request.
	TaskTypeImplement TaskType = "Implement"
	// TaskTypeCoordinate is a milestone's pilot: it answers its owners.
	TaskTypeCoordinate TaskType = "Coordinate"
)

// AgentTaskSpec is the desired state of an AgentTask. It is owned by the
// operator: the project controller creates tasks and bumps RunRequest
// whenever GitLab asks for (more) work.
type AgentTaskSpec struct {
	// ProjectRef is the name of the AgentProject in the same namespace.
	ProjectRef string `json:"projectRef"`

	// Type of the task.
	Type TaskType `json:"type"`

	// Milestone the task belongs to.
	// +optional
	Milestone *MilestoneRef `json:"milestone,omitempty"`

	// Issue implemented by this task (Implement tasks), or the tracking
	// issue the pilot listens on (Coordinate tasks).
	// +optional
	Issue *IssueRef `json:"issue,omitempty"`

	// RunRequest is a monotonically increasing counter. Every increment asks
	// the runner for exactly one more run. The runner acknowledges by setting
	// status.run.observedRunRequest.
	// +optional
	RunRequest int64 `json:"runRequest,omitempty"`

	// Suspend hibernates the task: its pod is deleted, the workspace is kept.
	// +optional
	Suspend bool `json:"suspend,omitempty"`

	// Autopilot is set for tasks of autopilot milestones: the agents must
	// not ask questions but take their defaults and record them.
	// +optional
	Autopilot bool `json:"autopilot,omitempty"`

	// Owners are the GitLab usernames mentioned when the task needs a human:
	// the milestone's, else the project's, else the operator's default.
	// +optional
	Owners []string `json:"owners,omitempty"`

	// Source records which GitLab state the operator has already turned into
	// run requests, so that every change triggers exactly one run.
	// +optional
	Source SourceState `json:"source,omitempty"`
}

// MilestoneRef identifies a GitLab milestone.
type MilestoneRef struct {
	ID    int64  `json:"id"`
	IID   int64  `json:"iid"`
	Title string `json:"title"`
}

// IssueRef identifies a GitLab issue.
type IssueRef struct {
	ID    int64  `json:"id"`
	IID   int64  `json:"iid"`
	Title string `json:"title"`
	// +optional
	WebURL string `json:"webURL,omitempty"`
}

// SourceState is the operator's bookkeeping of what it has already seen.
type SourceState struct {
	// DescriptionHash of the milestone description (Spec tasks).
	// +optional
	DescriptionHash string `json:"descriptionHash,omitempty"`

	// LastCommandNoteID is the newest "/agent" comment that was turned into a
	// run request (Implement tasks).
	// +optional
	LastCommandNoteID int64 `json:"lastCommandNoteID,omitempty"`

	// LastRoutingNoteID is the newest "/agent use" or "/agent effort"
	// comment that was applied to the issue's labels (Implement tasks).
	// +optional
	LastRoutingNoteID int64 `json:"lastRoutingNoteID,omitempty"`

	// IssueUpdatedAt / MergeRequestUpdatedAt let the operator skip fetching
	// comments when nothing changed.
	// +optional
	IssueUpdatedAt *metav1.Time `json:"issueUpdatedAt,omitempty"`
	// +optional
	MergeRequestUpdatedAt *metav1.Time `json:"mergeRequestUpdatedAt,omitempty"`

	// CIFixPipelineID is the failed pipeline the operator asked the agent to
	// fix with the latest run request.
	// +optional
	CIFixPipelineID int64 `json:"ciFixPipelineID,omitempty"`

	// MergeBaseFor is the merge request head the operator saw conflicting
	// with the default branch. The next run merges the default branch into
	// the work branch and has the agent resolve the conflicts; it is asked
	// once per head.
	// +optional
	MergeBaseFor string `json:"mergeBaseFor,omitempty"`

	// ReviewFor is a merge request head to review again without the
	// implementer (after its reviewers failed, or "/agent review"). It is
	// asked once per head; if the branch moved on, the run is a normal one.
	// +optional
	ReviewFor string `json:"reviewFor,omitempty"`

	// InfraRetry is the run request the operator made to retry a run that
	// failed for an outside reason (run.blockReason infra), once per run
	// request: a retry that fails the same way waits for a human.
	// +optional
	InfraRetry int64 `json:"infraRetry,omitempty"`

	// AutopilotPause is the reason autopilot is paused over this issue
	// (Implement tasks), copied from the milestone's spec task, so a run
	// during the pause knows it. Empty when it is not paused over it.
	// +optional
	AutopilotPause string `json:"autopilotPause,omitempty"`

	// LastPilotNoteID is the newest comment on the issue or its merge
	// request handed to the pilot (Implement tasks).
	// +optional
	LastPilotNoteID int64 `json:"lastPilotNoteID,omitempty"`

	// PilotMessages are the owners' messages to the pilot (Coordinate
	// tasks), each answered by the run of its request.
	// +optional
	PilotMessages []PilotMessage `json:"pilotMessages,omitempty"`
}

// PilotMessage is a comment addressed to the pilot.
type PilotMessage struct {
	NoteID int64 `json:"noteID"`
	// Where it was written: an issue, or a merge request.
	// +optional
	IssueIID int64 `json:"issueIID,omitempty"`
	// +optional
	MergeRequestIID int64  `json:"mergeRequestIID,omitempty"`
	Author          string `json:"author"`
	// Body without the address ("/pilot", the mention).
	Body string `json:"body"`
	// Request is the run request that answers it.
	Request int64 `json:"request"`
}

// PilotStatus is the operator's bookkeeping of a milestone's pilot.
type PilotStatus struct {
	// LastNoteID is the newest comment on the tracking issue handed to the
	// pilot.
	// +optional
	LastNoteID int64 `json:"lastNoteID,omitempty"`

	// Rejected is when a user who is no owner was last told so, by GitLab
	// user ID; later messages within a day get no answer.
	// +optional
	Rejected map[string]metav1.Time `json:"rejected,omitempty"`
}

// CIState is the state of the pipeline of the agent's last push.
// +kubebuilder:validation:Enum=Pending;Running;Fixing;Success;Failed;BaseFailing;Canceled;None
type CIState string

const (
	// CIStatePending: no pipeline yet for the pushed commit.
	CIStatePending CIState = "Pending"
	// CIStateRunning: the pipeline (or a retry of its failed jobs) runs.
	CIStateRunning CIState = "Running"
	// CIStateFixing: the agent was asked to fix the red pipeline.
	CIStateFixing CIState = "Fixing"
	// CIStateSuccess: green (or skipped / waiting for manual jobs).
	CIStateSuccess CIState = "Success"
	// CIStateFailed: still red after MaxFixRounds, handed to a human.
	CIStateFailed CIState = "Failed"
	// CIStateBaseFailing: the same jobs fail on the default branch too.
	CIStateBaseFailing CIState = "BaseFailing"
	// CIStateCanceled: the pipeline was canceled.
	CIStateCanceled CIState = "Canceled"
	// CIStateNone: no pipeline ran for the commit.
	CIStateNone CIState = "None"
)

// Final reports whether the loop is done with this commit.
func (s CIState) Final() bool {
	switch s {
	case CIStateSuccess, CIStateFailed, CIStateBaseFailing, CIStateCanceled, CIStateNone:
		return true
	}
	return false
}

// CIStatus tracks the pipeline of the agent's last push. It is written by
// the operator.
type CIStatus struct {
	// SHA of the pushed commit this status is about.
	SHA   string  `json:"sha"`
	State CIState `json:"state"`
	// +optional
	PipelineID int64 `json:"pipelineID,omitempty"`
	// +optional
	PipelineURL string `json:"pipelineURL,omitempty"`
	// Retried is set once the failed jobs of the pipeline were retried.
	// +optional
	Retried bool `json:"retried,omitempty"`
	// FailedJobs of the latest failed pipeline.
	// +optional
	FailedJobs []string `json:"failedJobs,omitempty"`
	// PingedFor is the event the owners were last mentioned for on this
	// commit, so a pipeline run again in review does not ping them again
	// for the same thing. A ping is sent at most once: if its comment
	// fails, the comment is posted again without it.
	// +optional
	PingedFor NotifyEvent `json:"pingedFor,omitempty"`
}

// TaskPhase is a coarse summary of where a task is in its lifecycle.
// +kubebuilder:validation:Enum=Pending;Queued;Running;Waiting;Idle;Hibernated;Archived;Blocked
type TaskPhase string

const (
	// TaskPhasePending: created, nothing has run yet and no run is requested.
	TaskPhasePending TaskPhase = "Pending"
	// TaskPhaseQueued: a run is requested but the project is at capacity.
	TaskPhaseQueued TaskPhase = "Queued"
	// TaskPhaseRunning: a pod exists and a run is requested or in progress.
	TaskPhaseRunning TaskPhase = "Running"
	// TaskPhaseWaiting: a run is requested but deferred because an agent
	// engine hit its usage or rate limit; it resumes at run.deferredUntil.
	TaskPhaseWaiting TaskPhase = "Waiting"
	// TaskPhaseIdle: the run is done; the pod waits IdleTimeout for a
	// follow-up request before it is stopped.
	TaskPhaseIdle TaskPhase = "Idle"
	// TaskPhaseHibernated: pod deleted after IdleTimeout, workspace kept.
	TaskPhaseHibernated TaskPhase = "Hibernated"
	// TaskPhaseArchived: workspace deleted after RetentionPeriod.
	TaskPhaseArchived TaskPhase = "Archived"
	// TaskPhaseBlocked: the task hit a safety limit and needs a human.
	TaskPhaseBlocked TaskPhase = "Blocked"
)

// RunResult is the outcome of the last agent run.
type RunResult string

const (
	RunResultSucceeded  RunResult = "Succeeded"
	RunResultNeedsInput RunResult = "NeedsInput"
	RunResultFailed     RunResult = "Failed"
	// RunResultDeferred: the run hit a usage or rate limit and will be
	// retried; it does not count towards maxRunsPerTask.
	RunResultDeferred RunResult = "Deferred"
)

// ContainerTermination is how a container last stopped.
type ContainerTermination struct {
	// Reason as the kubelet reports it, e.g. OOMKilled or Error.
	// +optional
	Reason string `json:"reason,omitempty"`

	// +optional
	ExitCode int32 `json:"exitCode,omitempty"`

	// +optional
	FinishedAt *metav1.Time `json:"finishedAt,omitempty"`

	// Restarts of the container in its pod so far.
	// +optional
	Restarts int32 `json:"restarts,omitempty"`

	// MemoryLimit of the container, if it has one.
	// +optional
	MemoryLimit string `json:"memoryLimit,omitempty"`
}

// OOMKilled reports whether the container was killed for exceeding its
// memory limit.
func (t *ContainerTermination) OOMKilled() bool {
	return t != nil && t.Reason == "OOMKilled"
}

// AgentTaskStatus is the observed state of an AgentTask. The top-level fields
// are written by the operator, Run is written by the runner inside the pod.
// Both sides use merge patches so they never overwrite each other.
type AgentTaskStatus struct {
	// +optional
	Phase TaskPhase `json:"phase,omitempty"`

	// +optional
	PodName string `json:"podName,omitempty"`

	// +optional
	WorkspaceClaimName string `json:"workspaceClaimName,omitempty"`

	// +optional
	Message string `json:"message,omitempty"`

	// AgentTermination is the last time the agent container stopped while
	// its pod ran (it is restarted in place), e.g. killed for using more than
	// its memory limit. The runner names it when a run loses the agent.
	// +optional
	AgentTermination *ContainerTermination `json:"agentTermination,omitempty"`

	// AgentMemoryLimit is the agent container's memory limit for this task,
	// raised by the operator after OOM kills (see agents.maxMemory). It
	// applies while it is above the project's limit.
	// +optional
	AgentMemoryLimit string `json:"agentMemoryLimit,omitempty"`

	// +optional
	Run RunStatus `json:"run,omitempty"`

	// CI is the operator's view of the pipeline of the agent's last push.
	// +optional
	CI *CIStatus `json:"ci,omitempty"`

	// Blocked is set while the issue cannot go on for an outside reason;
	// the operator shows it with the blocked labels.
	// +optional
	Blocked *BlockedStatus `json:"blocked,omitempty"`

	// RunBlockedAt is the run.lastActivityTime of the deferral or failure
	// that last blocked the issue (run.blockReason). Each report blocks
	// once, so a block lifted by hand stays lifted.
	// +optional
	RunBlockedAt *metav1.Time `json:"runBlockedAt,omitempty"`

	// Plan is the spec agent's last plan, in plan order (spec tasks).
	// +optional
	Plan []PlannedIssue `json:"plan,omitempty"`

	// Autopilot is the operator's queue of an autopilot milestone (spec
	// tasks).
	// +optional
	Autopilot *AutopilotStatus `json:"autopilot,omitempty"`

	// Revisits tracks the "/agent revisit" comments the operator handled on
	// closed issues of the milestone and on its tracking issue (spec tasks).
	// +optional
	Revisits *RevisitStatus `json:"revisits,omitempty"`

	// Pilot is the operator's bookkeeping of the milestone's pilot (spec
	// tasks).
	// +optional
	Pilot *PilotStatus `json:"pilot,omitempty"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// MaxInterruptions bounds how often one run request is started again after
// its previous attempt was interrupted.
const MaxInterruptions = 3

// RunStatus is reported by the runner.
type RunStatus struct {
	// ActiveRunRequest is the run request the runner is currently working on.
	// +optional
	ActiveRunRequest int64 `json:"activeRunRequest,omitempty"`

	// ObservedRunRequest is the last run request the runner has finished.
	// +optional
	ObservedRunRequest int64 `json:"observedRunRequest,omitempty"`

	// CurrentStep is the step the runner is working on, e.g. "9 run
	// implementer". It is empty between runs.
	// +optional
	CurrentStep string `json:"currentStep,omitempty"`

	// RunCount is the number of runs performed so far.
	// +optional
	RunCount int32 `json:"runCount,omitempty"`

	// LastActivityTime is when the runner last started or finished a run.
	// +optional
	LastActivityTime *metav1.Time `json:"lastActivityTime,omitempty"`

	// LastResult of the most recent run.
	// +optional
	LastResult RunResult `json:"lastResult,omitempty"`

	// Summary of the most recent run.
	// +optional
	Summary string `json:"summary,omitempty"`

	// CostUSD is the accumulated model spend of this task, as a decimal string.
	// +optional
	CostUSD string `json:"costUSD,omitempty"`

	// Branch the agent pushes to.
	// +optional
	Branch string `json:"branch,omitempty"`

	// MergeRequestIID of the agent's merge request.
	// +optional
	MergeRequestIID int64 `json:"mergeRequestIID,omitempty"`

	// MergeRequestURL of the agent's merge request.
	// +optional
	MergeRequestURL string `json:"mergeRequestURL,omitempty"`

	// DeferredUntil is set when the requested run hit a usage or rate limit:
	// the run is retried after this time.
	// +optional
	DeferredUntil *metav1.Time `json:"deferredUntil,omitempty"`

	// LimitedEngine is the engine whose limit deferred the run.
	// +optional
	LimitedEngine Engine `json:"limitedEngine,omitempty"`

	// BlockReason is why the issue waits for something outside it: quota
	// while the run is deferred by a usage limit or after it gave up on
	// one; infra after it failed because the agent pod or container was
	// lost (also out of memory) or it was interrupted too often. The
	// operator blocks the issue with it.
	// +optional
	BlockReason BlockReason `json:"blockReason,omitempty"`

	// Deferrals counts consecutive deferrals of the current run request; it
	// drives the exponential backoff when the reset time is unknown.
	// +optional
	Deferrals int32 `json:"deferrals,omitempty"`

	// OOMRetries counts runs of the current request started again after the
	// agent container ran out of memory.
	// +optional
	OOMRetries int32 `json:"oomRetries,omitempty"`

	// Interruptions counts runs of the current request started again because
	// the previous attempt never finished: its pod or node was lost, or the
	// runner stopped. After MaxInterruptions the run fails.
	// +optional
	Interruptions int32 `json:"interruptions,omitempty"`

	// PushedSHA is the commit the runner last pushed to the agent's branch.
	// +optional
	PushedSHA string `json:"pushedSHA,omitempty"`

	// PushedAt is when PushedSHA was pushed.
	// +optional
	PushedAt *metav1.Time `json:"pushedAt,omitempty"`

	// CIRound counts the CI fix runs since the last push a human asked for.
	// +optional
	CIRound int32 `json:"ciRound,omitempty"`

	// CIFixedPipelineID is the failed pipeline the last CI fix run handled.
	// +optional
	CIFixedPipelineID int64 `json:"ciFixedPipelineID,omitempty"`

	// MergedBaseFor is the head the last merge of the default branch was
	// asked for (spec.source.mergeBaseFor).
	// +optional
	MergedBaseFor string `json:"mergedBaseFor,omitempty"`

	// BlockingFindings is the number of blocker and major review findings
	// left after the last review.
	// +optional
	BlockingFindings int32 `json:"blockingFindings,omitempty"`

	// ReviewErrors is the number of reviewers that failed in the last review.
	// +optional
	ReviewErrors int32 `json:"reviewErrors,omitempty"`

	// ReviewErrorsTransient is set when all reviewers that failed in the
	// last review failed transiently (an outage of their service).
	// +optional
	ReviewErrorsTransient bool `json:"reviewErrorsTransient,omitempty"`

	// ReviewedFor is the head the last re-review was asked for
	// (spec.source.reviewFor).
	// +optional
	ReviewedFor string `json:"reviewedFor,omitempty"`

	// ReviewedSHA is the commit the last review saw. Runs without a review
	// (CI fix runs) can push commits after it, so a clean review only covers
	// PushedSHA when the two are equal.
	// +optional
	ReviewedSHA string `json:"reviewedSHA,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=at
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Issue",type=integer,JSONPath=`.spec.issue.iid`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Step",type=string,JSONPath=`.status.run.currentStep`
// +kubebuilder:printcolumn:name="Result",type=string,JSONPath=`.status.run.lastResult`
// +kubebuilder:printcolumn:name="Runs",type=integer,JSONPath=`.status.run.runCount`
// +kubebuilder:printcolumn:name="Cost",type=string,JSONPath=`.status.run.costUSD`
// +kubebuilder:printcolumn:name="CI",type=string,JSONPath=`.status.ci.state`
// +kubebuilder:printcolumn:name="Retry",type=date,JSONPath=`.status.run.deferredUntil`,priority=1
// +kubebuilder:printcolumn:name="MR",type=string,JSONPath=`.status.run.mergeRequestURL`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentTask is one unit of agent work: planning a milestone or implementing
// an issue. It owns the agent pod and the workspace PVC.
type AgentTask struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentTaskSpec   `json:"spec,omitempty"`
	Status AgentTaskStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentTaskList contains a list of AgentTask.
type AgentTaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentTask `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentTask{}, &AgentTaskList{})
}

// RunPending reports whether a run has been requested that the runner has not
// finished yet.
func (t *AgentTask) RunPending() bool {
	return t.Spec.RunRequest > t.Status.Run.ObservedRunRequest
}

// PlannedIssue is one issue of a milestone plan.
type PlannedIssue struct {
	// Key is the planner's stable identifier of the issue.
	Key   string `json:"key"`
	IID   int64  `json:"iid"`
	Title string `json:"title,omitempty"`
	// DependsOn are the IIDs of planned issues that must be merged first.
	// +optional
	DependsOn []int64 `json:"dependsOn,omitempty"`
	// Linked are the dependencies the operator linked as GitLab issue
	// links; only these are unlinked when the plan drops them.
	// +optional
	Linked []int64 `json:"linked,omitempty"`
}

// AutopilotPhase is the state of an autopilot milestone.
// +kubebuilder:validation:Enum=Running;Paused;Done;Off
type AutopilotPhase string

const (
	// AutopilotRunning: the operator works through the queue.
	AutopilotRunning AutopilotPhase = "Running"
	// AutopilotPaused: something needs a human; "/agent resume" continues.
	AutopilotPaused AutopilotPhase = "Paused"
	// AutopilotDone: every planned issue is merged or skipped.
	AutopilotDone AutopilotPhase = "Done"
	// AutopilotOff: the marker was removed from the milestone.
	AutopilotOff AutopilotPhase = "Off"
)

// AutopilotItemState is the state of one issue in the queue.
// +kubebuilder:validation:Enum=Pending;Active;Merged;Skipped
type AutopilotItemState string

const (
	AutopilotItemPending AutopilotItemState = "Pending"
	AutopilotItemActive  AutopilotItemState = "Active"
	AutopilotItemMerged  AutopilotItemState = "Merged"
	AutopilotItemSkipped AutopilotItemState = "Skipped"
)

// AutopilotStatus is the queue of an autopilot milestone.
type AutopilotStatus struct {
	Phase AutopilotPhase `json:"phase"`

	// TrackingIssueIID is the bot's tracking issue of the milestone.
	// +optional
	TrackingIssueIID int64 `json:"trackingIssueIID,omitempty"`

	// Reason why the milestone is paused.
	// +optional
	PausedReason string `json:"pausedReason,omitempty"`

	// PausedFor is the active issue the pause is about. Autopilot continues
	// by itself once that issue is merged or closed by hand, and, with
	// ResumeWhenReady, once it passes the merge gate again (a later run
	// fixed it).
	// +optional
	PausedFor int64 `json:"pausedFor,omitempty"`

	// ResumeWhenReady: the pause is over PausedFor's run, review or CI.
	// +optional
	ResumeWhenReady bool `json:"resumeWhenReady,omitempty"`

	// Items in queue order: dependencies first, ties in plan order.
	// +optional
	Items []AutopilotItem `json:"items,omitempty"`

	// MainRedFix is the main-red issue (mainHealth.autoFix) this milestone
	// drives through the merge gate while the default branch is red.
	// +optional
	MainRedFix *AutopilotItem `json:"mainRedFix,omitempty"`

	// LastCommandNoteID is the newest command on the tracking issue the
	// operator has handled.
	// +optional
	LastCommandNoteID int64 `json:"lastCommandNoteID,omitempty"`

	// TrackingHash is the hash of the tracking issue's last rendered
	// description, so it is only edited when it changes.
	// +optional
	TrackingHash string `json:"trackingHash,omitempty"`
}

// AutopilotItem is one issue in the queue of an autopilot milestone.
type AutopilotItem struct {
	IID   int64              `json:"iid"`
	State AutopilotItemState `json:"state"`

	// MergeRequestIID and MergeRequestURL of the issue's merge request.
	// +optional
	MergeRequestIID int64 `json:"mergeRequestIID,omitempty"`
	// +optional
	MergeRequestURL string `json:"mergeRequestURL,omitempty"`

	// MergedSHA is the commit the merge request was merged as.
	// +optional
	MergedSHA string `json:"mergedSHA,omitempty"`

	// RebasedFrom is the head the operator asked GitLab to rebase; set
	// while the rebase is in progress.
	// +optional
	RebasedFrom string `json:"rebasedFrom,omitempty"`

	// Rebases counts the rebases of this item; only one is attempted.
	// +optional
	Rebases int32 `json:"rebases,omitempty"`

	// RebasedDiff is a hash of the reviewed change, taken before the rebase.
	// The rebased head is only treated as reviewed if its change hashes the
	// same, i.e. the rebase moved the change without changing it.
	// +optional
	RebasedDiff string `json:"rebasedDiff,omitempty"`

	// RebasedAt is when the rebase was requested.
	// +optional
	RebasedAt *metav1.Time `json:"rebasedAt,omitempty"`

	// MarkedReady is set once the operator marked the draft MR ready, or
	// found it ready, to run CI. A draft again after that is a human holding
	// it, or a run that left problems.
	// +optional
	MarkedReady bool `json:"markedReady,omitempty"`

	// Decisions recorded on the issue, collected when it was merged.
	// +optional
	Decisions []DecisionRef `json:"decisions,omitempty"`

	// RevisitOf is set for follow-up issues the operator created to revisit
	// a decision of a merged issue: the decision's ID.
	// +optional
	RevisitOf string `json:"revisitOf,omitempty"`
}

// RevisitStatus is the watermark of handled revisit comments.
type RevisitStatus struct {
	// Since is when the operator started watching; older comments are
	// ignored.
	Since metav1.Time `json:"since"`
	// CheckedAt is the time of the last check.
	CheckedAt metav1.Time `json:"checkedAt"`
	// LastNoteIDs is the newest handled comment per issue IID.
	// +optional
	LastNoteIDs map[string]int64 `json:"lastNoteIDs,omitempty"`
}

// DecisionRef points to a decision recorded on an issue.
type DecisionRef struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	URL   string `json:"url,omitempty"`
	// +optional
	Superseded bool `json:"superseded,omitempty"`
}

// BlockReason is why an issue is blocked; it is the suffix of its reason label.
// +kubebuilder:validation:Enum=infra;main-red;quota;dependency
type BlockReason string

const (
	// BlockInfra: an engine or reviewer outage, a lost agent pod.
	BlockInfra BlockReason = "infra"
	// BlockMainRed: autopilot does not merge while the default branch is red.
	BlockMainRed BlockReason = "main-red"
	// BlockQuota: a usage limit defers the run.
	BlockQuota BlockReason = "quota"
	// BlockDependency: autopilot waits for an issue that will not be merged.
	BlockDependency BlockReason = "dependency"
)

// BlockedStatus is an issue's block.
type BlockedStatus struct {
	Reason BlockReason `json:"reason"`
	// +optional
	Message string      `json:"message,omitempty"`
	Since   metav1.Time `json:"since"`
	// RestoreLabel is the workflow label the blocked label replaced.
	// +optional
	RestoreLabel string `json:"restoreLabel,omitempty"`
	// Applied is set once the issue carries the blocked labels. An applied
	// block whose issue lost the blocked label was lifted by a human.
	// +optional
	Applied bool `json:"applied,omitempty"`
}
