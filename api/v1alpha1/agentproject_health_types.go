package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MainHealthSpec configures the watcher of the default branch: its newest
// finished pipeline is checked every Interval. A red one is retried once;
// green after the retry is recorded as flaky, still red opens an issue.
type MainHealthSpec struct {
	// Disabled turns the watcher off.
	// +optional
	Disabled bool `json:"disabled,omitempty"`

	// Interval is how often the default branch's pipelines are checked.
	// +kubebuilder:default="5m"
	// +optional
	Interval metav1.Duration `json:"interval,omitempty"`

	// RetryFailedJobs retries the failed jobs of a red pipeline once before
	// it counts as red.
	// +kubebuilder:default=true
	// +optional
	RetryFailedJobs *bool `json:"retryFailedJobs,omitempty"`

	// RetryCanceled retries a canceled pipeline of the default branch once
	// when its commit has no successful pipeline of its own. Canceled again,
	// it is reported on the open main-red issue, else on a ci::main-canceled
	// issue, which freezes nothing. A pipeline with a newer one that is not
	// canceled or skipped is superseded and left alone, as are pipelines up
	// to the newest one when the switch first ran.
	// +kubebuilder:default=true
	// +optional
	RetryCanceled *bool `json:"retryCanceled,omitempty"`

	// Issue opens an issue for a red default branch and one per flaky job.
	// +kubebuilder:default=true
	// +optional
	Issue *bool `json:"issue,omitempty"`

	// FreezeAutopilot holds autopilot's merges while the default branch is
	// red: the issue that would be merged next is blocked (main-red), and
	// autopilot continues by itself once the branch is green.
	// +kubebuilder:default=true
	// +optional
	FreezeAutopilot *bool `json:"freezeAutopilot,omitempty"`

	// AutoFix hands the main-red issue to the agent (agent::ready), once
	// per red episode. On autopilot, the frozen milestone merges its fix
	// (the main-red lane); otherwise a human merges it.
	// +optional
	AutoFix bool `json:"autoFix,omitempty"`

	// AutoFixFlaky hands a flaky job's issue to the agent at its second
	// occurrence, like the pilot's fixFlaky: into the active autopilot
	// milestone with the lowest IID (its queue), else without a milestone
	// (agent::ready).
	// +optional
	AutoFixFlaky bool `json:"autoFixFlaky,omitempty"`
}

// WebhookSpec configures the deliveries of the GitLab project hook, which
// wake the operator to sync the project at once.
type WebhookSpec struct {
	// SecretRef points to the GitLab project hook's secret token. A delivery
	// counts only when its X-Gitlab-Token matches; without it, every delivery
	// for this project is refused.
	// +optional
	SecretRef *corev1.SecretKeySelector `json:"secretRef,omitempty"`

	// Enabled registers the project hook in GitLab: the operator creates or
	// updates one hook for URL with the events it reacts to (issues, notes,
	// merge requests, pipelines, pushes), SSL verification and SecretRef's
	// token, and deletes that hook again when switched off. It needs the
	// bot's Maintainer role; the WebhookRegistered condition tells how it went.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// URL is the public URL of the operator's /hooks/gitlab endpoint, e.g.
	// https://ai-operator.example.com/hooks/gitlab. Needed with Enabled.
	// +optional
	URL string `json:"url,omitempty"`

	// FallbackInterval is how often the project is fully synced while
	// deliveries arrive (see SilenceThreshold). In between, only the
	// milestones are polled every pollInterval, since milestone changes
	// cause no deliveries; a delivery syncs at once.
	// +kubebuilder:default="10m"
	// +optional
	FallbackInterval metav1.Duration `json:"fallbackInterval,omitempty"`

	// SilenceThreshold is how long after the last valid delivery the project
	// counts as receiving deliveries. After it, or without SecretRef, every
	// poll is a full sync again.
	// +kubebuilder:default="30m"
	// +optional
	SilenceThreshold metav1.Duration `json:"silenceThreshold,omitempty"`
}

// ConditionWebhookRegistered is the AgentProject condition that tells whether
// the project hook is registered (spec.webhook.enabled).
const ConditionWebhookRegistered = "WebhookRegistered"

// ConditionFallbacksValid is the AgentProject condition that tells whether
// every role's fallback names another engine and an allowed model: False,
// reason InvalidFallback, with the broken ones in the message.
const ConditionFallbacksValid = "FallbacksValid"

// ConditionWebhookDeliveries is the AgentProject condition that tells whether
// deliveries arrive (with spec.webhook.secretRef): True, reason Receiving,
// within webhook.silenceThreshold of the last valid delivery, else False,
// reason Silent.
const ConditionWebhookDeliveries = "WebhookDeliveries"

// WebhookStatus is the project hook the operator registered and the last
// delivery.
type WebhookStatus struct {
	// HookID is the hook's ID in GitLab.
	// +optional
	HookID int64 `json:"hookID,omitempty"`

	// ConfigHash is a hash of the settings last registered (URL, events, SSL
	// verification, a hash of the secret), so a change is noticed without
	// asking GitLab.
	// +optional
	ConfigHash string `json:"configHash,omitempty"`

	// CheckedAt is when the hook was last checked in GitLab.
	// +optional
	CheckedAt *metav1.Time `json:"checkedAt,omitempty"`

	// LastDeliveryTime is when the last valid delivery for this project
	// arrived. The operator keeps it in memory: after a restart this shows
	// the last one seen before, and the project is polled fully until the
	// next delivery.
	// +optional
	LastDeliveryTime *metav1.Time `json:"lastDeliveryTime,omitempty"`

	// HeartbeatTrigger is the event GitLab sends test deliveries of for
	// this hook (push_events, issues_events or milestone_events): the first
	// one GitLab accepted.
	// +optional
	HeartbeatTrigger string `json:"heartbeatTrigger,omitempty"`

	// HeartbeatTime is when the operator last asked GitLab for a test
	// delivery, which keeps a quiet project Receiving.
	// +optional
	HeartbeatTime *metav1.Time `json:"heartbeatTime,omitempty"`
}

// MainHealthState is the health of the default branch.
// +kubebuilder:validation:Enum=green;retrying;red;flaky
type MainHealthState string

const (
	MainHealthGreen MainHealthState = "green"
	// MainHealthRetrying: the failed jobs of the pipeline are retried.
	MainHealthRetrying MainHealthState = "retrying"
	MainHealthRed      MainHealthState = "red"
	// MainHealthFlaky: the pipeline was green after its failed jobs were
	// retried.
	MainHealthFlaky MainHealthState = "flaky"
)

// MainHealthStatus is the health of the default branch, as of its newest
// finished pipeline.
type MainHealthStatus struct {
	State MainHealthState `json:"state"`
	// +optional
	PipelineID int64 `json:"pipelineID,omitempty"`
	// +optional
	SHA string `json:"sha,omitempty"`
	// FailedJobs are the failing jobs of the pipeline, before a retry.
	// +optional
	FailedJobs []string `json:"failedJobs,omitempty"`
	// Since is when the state last changed.
	// +optional
	Since *metav1.Time `json:"since,omitempty"`
	// IssueIID is the open issue that reports the red default branch.
	// +optional
	IssueIID int64 `json:"issueIID,omitempty"`
	// ReportedJobs are the failing jobs the issue reports last.
	// +optional
	ReportedJobs []string `json:"reportedJobs,omitempty"`
	// AutoFixIssueIID is the issue handed to the agent in this red episode
	// (mainHealth.autoFix).
	// +optional
	AutoFixIssueIID int64 `json:"autoFixIssueIID,omitempty"`
	// RetriedPipelineID is the pipeline whose failed jobs were retried.
	// +optional
	RetriedPipelineID int64 `json:"retriedPipelineID,omitempty"`
	// RetriedJobIDs are the failed jobs of the retried pipeline, in the
	// order of FailedJobs: the retry replaces them, so their logs are read
	// by ID once the retry is green.
	// +optional
	RetriedJobIDs []int64 `json:"retriedJobIDs,omitempty"`
	// Flaky are the jobs that passed on retry, as reported on their flaky
	// issues (state flaky only).
	// +optional
	Flaky []FlakyJobReport `json:"flaky,omitempty"`
	// FlakyFixes are the flaky issues handed to the agent, once each: the
	// first hand-over wins. They are not filtered by milestones.titlePattern.
	// +optional
	FlakyFixes []FlakyFix `json:"flakyFixes,omitempty"`
	// CanceledSince is the newest finished pipeline when retryCanceled first
	// ran: canceled pipelines up to it are not retried.
	// +optional
	CanceledSince int64 `json:"canceledSince,omitempty"`
	// Canceled are the last canceled pipelines handled (retryCanceled),
	// newest first.
	// +optional
	Canceled []CanceledPipeline `json:"canceled,omitempty"`
	// CheckedAt is when the pipelines were last checked.
	// +optional
	CheckedAt *metav1.Time `json:"checkedAt,omitempty"`
}

// CanceledPipeline is a canceled pipeline of the default branch that a newer
// pipeline covers (superseded), or whose commit had no successful pipeline:
// whether it was retried, and whether its second cancel (or a refused retry)
// was reported.
type CanceledPipeline struct {
	PipelineID int64  `json:"pipelineID"`
	SHA        string `json:"sha"`
	// +optional
	Retried bool `json:"retried,omitempty"`
	// +optional
	Reported bool `json:"reported,omitempty"`
	// +optional
	Superseded bool `json:"superseded,omitempty"`
}

// FlakyJobReport is a job that passed on retry: its flaky issue, and what
// the log of its failed attempt shows. Tests and LogTail come from the job
// log and are untrusted data.
type FlakyJobReport struct {
	Job      string `json:"job"`
	IssueIID int64  `json:"issueIID"`
	// Tests are the failing tests the log names.
	// +optional
	Tests []string `json:"tests,omitempty"`
	// LogTail is the defused end of the log.
	// +optional
	LogTail string `json:"logTail,omitempty"`
}

// FlakyFix is a flaky issue handed to the agent: into which milestone (0:
// none), and when.
type FlakyFix struct {
	IssueIID int64 `json:"issueIID"`
	// +optional
	MilestoneIID int64 `json:"milestoneIID,omitempty"`
	// +optional
	MilestoneTitle string      `json:"milestoneTitle,omitempty"`
	At             metav1.Time `json:"at"`
}
