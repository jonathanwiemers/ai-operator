package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	// RecoveredAt is when the operator saw the pipelines of these heads turn
	// green while the issue was held for red CI (needsInputCause: ciRed), and
	// lifted the hold. A new push starts a new status, without it.
	// +optional
	RecoveredAt *metav1.Time `json:"recoveredAt,omitempty"`
	// Infra records the retries of a pipeline that failed because of the
	// runner (spec.ci.infraFailures).
	// +optional
	Infra *CIInfraRetry `json:"infra,omitempty"`
	// Companion is the pipeline of the companion's pushed head
	// (status.run.companion.pushedSHA); unset without a pushed companion.
	// +optional
	Companion *CompanionCI `json:"companion,omitempty"`
}

// CompanionCI tracks the pipeline of the companion's pushed head, with the
// rules of the home pipeline except the default-branch check (BaseFailing).
type CompanionCI struct {
	// SHA is the companion head this status is about.
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
	// Since is when the operator first followed SHA: without a pipeline
	// after ci.noPipelineTimeout it counts as no CI (None).
	// +optional
	Since *metav1.Time `json:"since,omitempty"`
	// Infra records the retries of a pipeline that failed because of the
	// runner (spec.ci.infraFailures).
	// +optional
	Infra *CIInfraRetry `json:"infra,omitempty"`
}

// CIInfraRetry records the retries of a red pipeline whose failing jobs all
// failed because of the runner, for one pushed head. They use no fix round.
type CIInfraRetry struct {
	// Retries done for this head.
	// +optional
	Retries int32 `json:"retries,omitempty"`
	// DueAt is when the failed jobs of the judged pipeline are retried;
	// unset when no retry waits.
	// +optional
	DueAt *metav1.Time `json:"dueAt,omitempty"`
	// PipelineID is the failed pipeline judged a runner failure; unset once
	// it was retried or runs again.
	// +optional
	PipelineID int64 `json:"pipelineID,omitempty"`
	// Signature is what matched, e.g. "build:client: EAI_AGAIN".
	// +optional
	Signature string `json:"signature,omitempty"`
}

// Done reports whether the CI loop is done with the pushed heads: one tree
// failed (handed to a human), or both are final.
func (s *CIStatus) Done() bool {
	c := s.Companion
	switch {
	case s.State == CIStateFailed || c != nil && c.State == CIStateFailed:
		return true
	case c != nil && !c.State.Final():
		return false
	}
	return s.State.Final()
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
