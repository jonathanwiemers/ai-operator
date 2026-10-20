package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TaskType is the kind of work an AgentTask performs.
// +kubebuilder:validation:Enum=Spec;Implement
type TaskType string

const (
	// TaskTypeSpec turns a milestone description into implementable issues.
	TaskTypeSpec TaskType = "Spec"
	// TaskTypeImplement implements one issue as one merge request.
	TaskTypeImplement TaskType = "Implement"
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

	// Issue implemented by this task (Implement tasks only).
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
	// TaskPhaseIdle: the pod is kept warm, waiting for further input.
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

	// +optional
	Run RunStatus `json:"run,omitempty"`

	// CI is the operator's view of the pipeline of the agent's last push.
	// +optional
	CI *CIStatus `json:"ci,omitempty"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

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

	// Deferrals counts consecutive deferrals of the current run request; it
	// drives the exponential backoff when the reset time is unknown.
	// +optional
	Deferrals int32 `json:"deferrals,omitempty"`

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
