package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentProjectSpec connects one GitLab project to the operator and describes
// how its milestones and issues are turned into agent work.
type AgentProjectSpec struct {
	// GitLab connection settings.
	GitLab GitLabSpec `json:"gitlab"`

	// Milestones selects which milestones get a spec agent.
	// +optional
	Milestones MilestoneSelector `json:"milestones,omitempty"`

	// Workflow defines the labels that drive the issue state machine.
	// +optional
	Workflow WorkflowSpec `json:"workflow,omitempty"`

	// Agents configures the agent pods and the individual agent roles.
	Agents AgentsSpec `json:"agents"`

	// Lifecycle controls how long agent pods and workspaces are kept around.
	// +optional
	Lifecycle LifecycleSpec `json:"lifecycle,omitempty"`

	// CI configures how the agent reacts to the merge request pipeline.
	// +optional
	CI CISpec `json:"ci,omitempty"`

	// MainHealth watches the pipelines of the default branch.
	// +optional
	MainHealth MainHealthSpec `json:"mainHealth,omitempty"`

	// Notify says whom the operator mentions when it needs a human.
	// +optional
	Notify NotifySpec `json:"notify,omitempty"`

	// Pilot configures the milestone coordinator (see agents.pilot).
	// +optional
	Pilot PilotSpec `json:"pilot,omitempty"`

	// PollInterval is how often GitLab is polled for changes.
	// +kubebuilder:default="1m"
	// +optional
	PollInterval metav1.Duration `json:"pollInterval,omitempty"`

	// MaxConcurrentRuns limits how many agent runs (i.e. token-consuming work)
	// may be in flight for this project at the same time. Further runs queue.
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxConcurrentRuns int32 `json:"maxConcurrentRuns,omitempty"`

	// Suspend stops polling GitLab and starting new runs. Existing pods are
	// left alone and are still subject to the lifecycle timeouts.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// GitLabSpec describes how to reach the GitLab project.
type GitLabSpec struct {
	// URL of the GitLab instance, e.g. https://gitlab.com.
	// +kubebuilder:default="https://gitlab.com"
	// +optional
	URL string `json:"url,omitempty"`

	// Project is the full path of the project, e.g. "my-group/my-project".
	// +kubebuilder:validation:MinLength=1
	Project string `json:"project"`

	// TokenSecretRef points to a project/group access token with the api and
	// write_repository scopes. The token is used by the operator and by the
	// runner inside the agent pods; it is never handed to the model.
	TokenSecretRef corev1.SecretKeySelector `json:"tokenSecretRef"`
}

// MilestoneSelector selects milestones that should be planned by the spec agent.
type MilestoneSelector struct {
	// TitlePattern is an optional regular expression milestone titles must match.
	// +optional
	TitlePattern string `json:"titlePattern,omitempty"`

	// SpecMarker must appear in the milestone description for it to be picked
	// up. This keeps existing milestones from being planned by accident. Set it
	// to an empty string to plan every active milestone with a description.
	// +kubebuilder:default="/agent spec"
	// +optional
	SpecMarker *string `json:"specMarker,omitempty"`

	// AutopilotMarker in a milestone description plans the milestone (like
	// SpecMarker) and runs it end to end: the agents take their defaults
	// instead of asking, the issues are worked through one at a time in
	// dependency order, and each merge request is merged once its pipeline
	// is green and its review is clean. The bot needs permission to merge
	// into the default branch. Removing the marker stops it.
	// +kubebuilder:default="/agent autopilot"
	// +optional
	AutopilotMarker *string `json:"autopilotMarker,omitempty"`
}

// WorkflowSpec defines the GitLab labels used as the agent state machine. Use
// scoped labels (with "::") so GitLab keeps exactly one of them on an issue and
// they map nicely onto board columns.
type WorkflowSpec struct {
	// DraftLabel is put on issues created by the spec agent. Draft issues are
	// not worked on until a human moves them to ReadyLabel.
	// +kubebuilder:default="agent::draft"
	// +optional
	DraftLabel string `json:"draftLabel,omitempty"`

	// ReadyLabel is set by a human to hand an issue to the implementing agent.
	// +kubebuilder:default="agent::ready"
	// +optional
	ReadyLabel string `json:"readyLabel,omitempty"`

	// WorkingLabel is set by the operator once it has picked up a ready issue.
	// +kubebuilder:default="agent::working"
	// +optional
	WorkingLabel string `json:"workingLabel,omitempty"`

	// NeedsInputLabel is set by the agent when it has questions it cannot
	// resolve on its own.
	// +kubebuilder:default="agent::needs-input"
	// +optional
	NeedsInputLabel string `json:"needsInputLabel,omitempty"`

	// ReviewLabel is set by the agent once a merge request is ready for a human.
	// +kubebuilder:default="agent::review"
	// +optional
	ReviewLabel string `json:"reviewLabel,omitempty"`

	// BlockedLabel is set by the operator when an issue cannot go on for a
	// reason outside it (an outage, a red default branch); the label it
	// replaced comes back once the block is lifted.
	// +kubebuilder:default="agent::blocked"
	// +optional
	BlockedLabel string `json:"blockedLabel,omitempty"`

	// BlockedReasonPrefix: a blocked issue also carries
	// "<prefix><reason>", e.g. "agent-blocked::infra".
	// +kubebuilder:default="agent-blocked::"
	// +optional
	BlockedReasonPrefix string `json:"blockedReasonPrefix,omitempty"`

	// DoneLabel (or closing the issue) ends the task and deletes its pod and
	// workspace.
	// +kubebuilder:default="agent::done"
	// +optional
	DoneLabel string `json:"doneLabel,omitempty"`

	// GeneratedLabel is added to every issue the spec agent creates.
	// +kubebuilder:default="ai-generated"
	// +optional
	GeneratedLabel string `json:"generatedLabel,omitempty"`

	// ProfileLabelPrefix: an issue labelled "<prefix><profile name>" is
	// implemented with that agent profile (see agents.profiles). The spec agent
	// sets one on every issue it creates; change the label to override its
	// choice. Issues without a profile label use agents.implement.
	// +kubebuilder:default="agent-profile::"
	// +optional
	ProfileLabelPrefix string `json:"profileLabelPrefix,omitempty"`

	// ModelLabelPrefix: an issue labelled "<prefix><engine>/<model or
	// alias>" (e.g. "agent-model::claude/opus") is implemented with that
	// engine and model instead of its profile's, if agents.models allows it.
	// "/agent use <engine> <model>" sets it.
	// +kubebuilder:default="agent-model::"
	// +optional
	ModelLabelPrefix string `json:"modelLabelPrefix,omitempty"`

	// EffortLabelPrefix: an issue labelled "<prefix><level>" (e.g.
	// "agent-effort::high") is implemented with that effort, at most
	// agents.maxEffort (claude engine only). The spec agent sets one on the
	// issues it creates; "/agent effort <level>" changes it.
	// +kubebuilder:default="agent-effort::"
	// +optional
	EffortLabelPrefix string `json:"effortLabelPrefix,omitempty"`

	// CommandPrefix: a comment on the issue or its merge request that starts
	// with this prefix triggers another agent run (e.g. "/agent please also
	// cover the error case"). Plain discussion does not trigger runs, so no
	// tokens are spent on chatter.
	// +kubebuilder:default="/agent"
	// +optional
	CommandPrefix string `json:"commandPrefix,omitempty"`

	// PilotPrefix: a comment that starts with this prefix talks to the
	// milestone's pilot (e.g. "/pilot why are we paused?").
	// +kubebuilder:default="/pilot"
	// +optional
	PilotPrefix string `json:"pilotPrefix,omitempty"`

	// AssignBot assigns the bot user to an issue when the operator picks it up
	// and the issue has no assignee, so GitLab's milestone page lists it as
	// ongoing. A human assignee is never replaced.
	// +kubebuilder:default=true
	// +optional
	AssignBot *bool `json:"assignBot,omitempty"`

	// ResolveConflicts lets the agent resolve merge conflicts: when the
	// merge request of an issue in review conflicts with the default branch,
	// the next run merges the default branch into the work branch and the
	// agent resolves the conflicts. The result is reviewed and checked by CI
	// like any push. Off: conflicts are left to humans (autopilot pauses).
	// +kubebuilder:default=true
	// +optional
	ResolveConflicts *bool `json:"resolveConflicts,omitempty"`

	// StepCommits: while the merge request is a draft, the implementer works
	// in small steps; each one is committed and pushed (without CI) as it is
	// done, and comments written in the meantime reach the agent before the
	// next step, so a human can follow and steer. Off: one commit per run.
	// +kubebuilder:default=true
	// +optional
	StepCommits *bool `json:"stepCommits,omitempty"`
}

// AgentsSpec configures the agent pods and the roles running inside them.
type AgentsSpec struct {
	// Image of the agent pod. It must contain the ai-runner binary, git, the
	// agent CLIs used (claude, cursor-agent) and whatever toolchain the
	// project needs to build and test. Defaults to the operator's
	// --default-agent-image (the Helm chart sets it to the agent image of the
	// same release).
	// +optional
	Image string `json:"image,omitempty"`

	// +kubebuilder:default=IfNotPresent
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// ServiceAccountName used by the agent pods. It needs read access to
	// AgentTasks/AgentProjects and patch access to agenttasks/status. Its
	// token is only mounted into the runner container.
	// +kubebuilder:default="ai-operator-agent"
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// CredentialsSecretName names a secret whose keys are exposed to the agent
	// CLIs as environment variables. Use whichever the engines need:
	//   CLAUDE_CODE_OAUTH_TOKEN  Claude subscription (Pro/Max), from `claude setup-token`
	//   ANTHROPIC_API_KEY        Claude API billing instead of a subscription
	//   CURSOR_API_KEY           cursor-agent (Cursor subscription)
	CredentialsSecretName string `json:"credentialsSecretName"`

	// Resources of the agent container (agent CLIs, builds and tests).
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// MaxMemory caps the agent container's memory limit when it is raised
	// after an OOM kill: each kill raises the limit of that task's pod by
	// half, and the run starts again, until the limit would pass MaxMemory.
	// Defaults to twice resources.limits.memory; without a memory limit
	// nothing is raised.
	// +optional
	MaxMemory *resource.Quantity `json:"maxMemory,omitempty"`

	// RunnerResources of the runner container (GitLab, git mirror). Defaults
	// to 50m CPU / 128Mi memory requests and a 1Gi memory limit.
	// +optional
	RunnerResources *corev1.ResourceRequirements `json:"runnerResources,omitempty"`

	// WorkspaceSize of the per-task PVC holding the checkout, build output and
	// the agent session state.
	// +kubebuilder:default="10Gi"
	// +optional
	WorkspaceSize resource.Quantity `json:"workspaceSize,omitempty"`

	// StorageClassName for the workspace PVCs.
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// CacheClaimName optionally names an existing (ideally ReadWriteMany) PVC
	// that is mounted into every agent pod at /cache and used for dependency
	// caches (Go modules, npm, pip, ...), shared across tasks.
	// +optional
	CacheClaimName string `json:"cacheClaimName,omitempty"`

	// Env is added to the agent container.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// LogLevel of the runner and agent containers. Defaults to the operator's
	// --agent-log-level (the Helm chart sets it to agent.logLevel, falling
	// back to logLevel). A running pod picks up a change when it is next
	// started, e.g. after hibernation.
	// +optional
	LogLevel LogLevel `json:"logLevel,omitempty"`

	// NodeSelector for agent pods.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations for agent pods.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Spec configures the spec (planning) agent.
	// +optional
	Spec RoleSpec `json:"spec,omitempty"`

	// Pilot configures the milestone coordinator, who answers the
	// milestone's owners. Defaults to the claude engine with
	// claude-sonnet-5-5.
	// +optional
	Pilot RoleSpec `json:"pilot,omitempty"`

	// Implement configures the implementing agent for issues without a
	// profile label (e.g. issues created by hand).
	// +optional
	Implement RoleSpec `json:"implement,omitempty"`

	// Profiles are named implementing agents an issue selects with a
	// profile label, e.g. "agent-profile::fast". The spec agent picks a profile
	// for every issue based on the descriptions. Defaults to "fast"
	// (claude-sonnet-5-5) and "strong" (claude-opus-5-5).
	// +listType=map
	// +listMapKey=name
	// +optional
	Profiles []AgentProfile `json:"profiles,omitempty"`

	// Review configures the review agents. Defaults to the claude engine with
	// claude-sonnet-5-5.
	// +optional
	Review ReviewSpec `json:"review,omitempty"`

	// Describe configures the agent that writes the merge request
	// description after each push that changed the diff. Defaults to the
	// claude engine with claude-sonnet-5-5.
	// +optional
	Describe DescribeSpec `json:"describe,omitempty"`

	// Models an issue may switch its implementer to, with "/agent use
	// <engine> <model>" or a model label. Defaults to claude-opus-5-5,
	// claude-sonnet-5-5 and claude-haiku-5-5 (aliases opus, sonnet, haiku).
	// The engines and models of the profiles and of implement are always
	// allowed.
	// +optional
	Models []ModelOption `json:"models,omitempty"`

	// LogPrompts logs every prompt the agents get (role, system prompt and
	// prompt, each cut at 64 KiB) in the agent container, where the web UI's
	// live view shows them. Prompts hold issue text, comments, diffs and CI
	// logs, so they are off by default.
	// +optional
	LogPrompts bool `json:"logPrompts,omitempty"`

	// MaxEffort caps the effort an issue may ask for (effort label, "/agent
	// effort", the spec agent's choice). The efforts of the roles and
	// profiles themselves are not capped.
	// +kubebuilder:validation:Enum=low;medium;high;xhigh;max
	// +kubebuilder:default=high
	// +optional
	MaxEffort string `json:"maxEffort,omitempty"`
}

// ModelOption is an engine and model an issue may switch to.
type ModelOption struct {
	Engine Engine `json:"engine"`

	// Model passed to the engine, e.g. claude-opus-5-5 or "auto".
	// +kubebuilder:validation:MinLength=1
	Model string `json:"model"`

	// Alias is a short name for commands and labels, e.g. "opus".
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9.-]*$`
	// +optional
	Alias string `json:"alias,omitempty"`
}

// DescribeSpec configures the agent that keeps the merge request description
// up to date: why, what, how, verification and, where it helps, a mermaid
// diagram of the change.
type DescribeSpec struct {
	RoleSpec `json:",inline"`

	// Disabled keeps the placeholder description; the reports carry the
	// summaries.
	// +optional
	Disabled bool `json:"disabled,omitempty"`
}

// AgentProfile is a named implementing agent configuration.
type AgentProfile struct {
	// Name used in the profile label.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	Name string `json:"name"`

	// Description tells the spec agent which issues this profile is for.
	// +optional
	Description string `json:"description,omitempty"`

	RoleSpec `json:",inline"`
}

// LogLevel of the agent pods. "info" logs every step of a run and every call
// between runner and agent; "debug" adds the git commands of the runner.
// +kubebuilder:validation:Enum=debug;info;error
type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelError LogLevel = "error"
)

// Engine is the coding agent CLI that runs a role.
// +kubebuilder:validation:Enum=claude;cursor
type Engine string

const (
	// EngineClaude runs the Claude Code CLI (`claude -p`).
	EngineClaude Engine = "claude"
	// EngineCursor runs the Cursor CLI (`cursor-agent -p`).
	EngineCursor Engine = "cursor"
)

// RoleSpec configures a single agent role.
type RoleSpec struct {
	// Engine is the agent CLI used for this role. Roles can mix engines, e.g.
	// implement with claude and review with cursor.
	// +kubebuilder:default=claude
	// +optional
	Engine Engine `json:"engine,omitempty"`

	// Model passed to the engine, e.g. claude-opus-5-5 or claude-sonnet-5-5
	// for claude, "auto" for cursor. For the claude engine it defaults to
	// claude-opus-5-5 (claude-sonnet-5-5 for reviews); cursor uses its own
	// default.
	// +optional
	Model string `json:"model,omitempty"`

	// Effort level (claude engine only).
	// +kubebuilder:validation:Enum=low;medium;high;xhigh;max
	// +optional
	Effort string `json:"effort,omitempty"`

	// MaxBudgetUSD caps the spend of a single invocation of this role,
	// as a decimal string, e.g. "5" or "2.50" (claude engine only).
	// +kubebuilder:validation:Pattern=`^[0-9]+(\.[0-9]+)?$`
	// +optional
	MaxBudgetUSD string `json:"maxBudgetUSD,omitempty"`

	// AllowedTools overrides the tools the role may use (claude engine only;
	// cursor reads its permissions from .cursor/cli.json).
	// +optional
	AllowedTools []string `json:"allowedTools,omitempty"`

	// Instructions are appended to the role's system prompt, e.g. house rules.
	// +optional
	Instructions string `json:"instructions,omitempty"`
}

// ReviewSpec configures the review loop that runs after each implementation.
type ReviewSpec struct {
	RoleSpec `json:",inline"`

	// Reviewers to run. Each reviewer is a separate, read-only agent that only
	// gets the diff plus the issue as input. They run in parallel.
	// +kubebuilder:default={bugbot,security}
	// +optional
	Reviewers []ReviewerKind `json:"reviewers,omitempty"`

	// MaxIterations is how often the implementer may fix blocking review
	// findings before the result is handed to a human anyway.
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxIterations *int32 `json:"maxIterations,omitempty"`

	// Retries is how often a reviewer is called again after a transient
	// failure (an outage of the engine's service, a reset connection), with
	// a growing pause in between. Other failures are not retried.
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=0
	// +optional
	Retries *int32 `json:"retries,omitempty"`

	// RetryAfter is how long autopilot waits before it asks for one
	// re-review of a head whose reviewers all failed transiently.
	// +kubebuilder:default="15m"
	// +optional
	RetryAfter metav1.Duration `json:"retryAfter,omitempty"`
}

// ReviewerKind names a built-in reviewer.
// +kubebuilder:validation:Enum=bugbot;security
type ReviewerKind string

const (
	ReviewerBugbot   ReviewerKind = "bugbot"
	ReviewerSecurity ReviewerKind = "security"
)

// LifecycleSpec controls how long pods and workspaces live.
//
// A task goes through: Running -> Idle (briefly, for follow-up requests) ->
// Hibernated (pod deleted, workspace PVC kept) -> Archived (PVC deleted; the branch and the
// GitLab history remain, so the next run rehydrates from a fresh clone).
type LifecycleSpec struct {
	// IdleTimeout after which a pod without pending runs is deleted while its
	// workspace is kept. The short default stops the pod right after a run;
	// a request that arrives during the run or within the grace starts in the
	// same pod. Everything the next run needs stays on the workspace, so a
	// longer timeout only saves the pod start (scheduling, volume attach,
	// image pull if needed) at the cost of holding the pod's resources. After
	// a failed run the pod stays at least an hour, so its logs can be read.
	// +kubebuilder:default="1m"
	// +optional
	IdleTimeout metav1.Duration `json:"idleTimeout,omitempty"`

	// RetentionPeriod after the last activity after which the workspace PVC is
	// deleted as well.
	// +kubebuilder:default="168h"
	// +optional
	RetentionPeriod metav1.Duration `json:"retentionPeriod,omitempty"`

	// MaxRunsPerTask is a safety net against runaway loops.
	// +kubebuilder:default=20
	// +optional
	MaxRunsPerTask int32 `json:"maxRunsPerTask,omitempty"`

	// UsageLimitBackoff is the first wait after an engine reports a usage or
	// rate limit without saying when it resets. It doubles with every
	// further deferral of the same run, up to MaxUsageLimitBackoff.
	// +kubebuilder:default="5m"
	// +optional
	UsageLimitBackoff metav1.Duration `json:"usageLimitBackoff,omitempty"`

	// MaxUsageLimitBackoff caps the wait between retries.
	// +kubebuilder:default="1h"
	// +optional
	MaxUsageLimitBackoff metav1.Duration `json:"maxUsageLimitBackoff,omitempty"`

	// InfraRetryAfter is the wait before the operator starts a run that
	// failed for an outside reason (the issue is blocked, infra) once more.
	// +kubebuilder:default="30m"
	// +optional
	InfraRetryAfter metav1.Duration `json:"infraRetryAfter,omitempty"`
}

// CISpec configures the CI feedback loop: after the agent pushes, the
// operator waits for the merge request pipeline. A green pipeline moves the
// issue to review; a red one is retried once and then handed back to the
// implementer with the failing jobs, up to MaxFixRounds times.
type CISpec struct {
	// Disabled turns the loop off: issues go to review right after the push.
	// +optional
	Disabled bool `json:"disabled,omitempty"`

	// MaxFixRounds is how many times the agent may try to fix a red pipeline
	// after a push triggered by a human, before the issue goes to
	// needs-input.
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxFixRounds *int32 `json:"maxFixRounds,omitempty"`

	// RetryFailedJobs retries the failed jobs of a red pipeline once before
	// the agent spends tokens on it, to weed out flaky jobs.
	// +kubebuilder:default=true
	// +optional
	RetryFailedJobs *bool `json:"retryFailedJobs,omitempty"`

	// NoPipelineTimeout: if no pipeline shows up for the pushed commit
	// within this time, the project is assumed to have no CI for it and the
	// issue goes to review.
	// +kubebuilder:default="10m"
	// +optional
	NoPipelineTimeout metav1.Duration `json:"noPipelineTimeout,omitempty"`
}

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
	// CheckedAt is when the pipelines were last checked.
	// +optional
	CheckedAt *metav1.Time `json:"checkedAt,omitempty"`
}

// NotifyEvent is a situation in which the operator needs a human.
// +kubebuilder:validation:Enum=failed;needsInput;ciFailed;review;autopilotPaused;autopilotDone;mainRed;blocked
type NotifyEvent string

const (
	// NotifyFailed: a run failed.
	NotifyFailed NotifyEvent = "failed"
	// NotifyNeedsInput: the agent asked questions or could not finish alone.
	NotifyNeedsInput NotifyEvent = "needsInput"
	// NotifyCIFailed: the pipeline stayed red after the fix rounds.
	NotifyCIFailed NotifyEvent = "ciFailed"
	// NotifyReview: an issue reached review (not on autopilot).
	NotifyReview NotifyEvent = "review"
	// NotifyAutopilotPaused: autopilot paused a milestone.
	NotifyAutopilotPaused NotifyEvent = "autopilotPaused"
	// NotifyAutopilotDone: autopilot finished a milestone.
	NotifyAutopilotDone NotifyEvent = "autopilotDone"
	// NotifyMainRed: the default branch stayed red after a retry.
	NotifyMainRed NotifyEvent = "mainRed"
	// NotifyBlocked: a run gave up for an outside reason; the issue is
	// blocked and waits for a human.
	NotifyBlocked NotifyEvent = "blocked"
)

// NotifySpec says whom the operator mentions ("cc @…") in the comments that
// need a human. A milestone overrides the users with a line
// "<commandPrefix> owners @alice @bob" in its description, for itself and
// its issues.
type NotifySpec struct {
	// Users are the GitLab usernames (without "@") to mention. Empty: the
	// operator's --default-owners. They must be members of the project to
	// be notified.
	// +optional
	Users []string `json:"users,omitempty"`

	// Events that mention the users. Defaults to all of them.
	// +optional
	Events []NotifyEvent `json:"events,omitempty"`

	// RequestReview makes the users the reviewers of a merge request whose
	// issue reaches review (not on autopilot), unless it has reviewers.
	// +kubebuilder:default=true
	// +optional
	RequestReview *bool `json:"requestReview,omitempty"`
}

// PilotSpec configures the milestone coordinator ("/pilot").
type PilotSpec struct {
	// Mention: a comment that starts with a mention of the bot user talks
	// to the pilot too, like workflow.pilotPrefix.
	// +kubebuilder:default=true
	// +optional
	Mention *bool `json:"mention,omitempty"`

	// MaxBudgetUSD caps the pilot's spend per milestone, as a decimal
	// string. Once it is used up, the pilot answers that it is, without a
	// model call.
	// +kubebuilder:default="20"
	// +kubebuilder:validation:Pattern=`^[0-9]+(\.[0-9]+)?$`
	// +optional
	MaxBudgetUSD string `json:"maxBudgetUSD,omitempty"`

	// Autonomy: managed, the pilot retries, re-reviews, instructs (a fixed
	// kind) and opens issues on its own, counted and audited on the
	// tracking issue; propose, every action waits for an owner's "yes".
	// Unset: managed on autopilot milestones, propose on the others; a set
	// value applies to every milestone. Neither a milestone's description
	// nor its rules change it; an owner's "/pilot stop" lowers it.
	// +kubebuilder:validation:Enum=managed;propose
	// +optional
	Autonomy PilotAutonomy `json:"autonomy,omitempty"`

	// MaxInterventionsPerIssue bounds the pilot's own actions per issue
	// and head when it manages; past it, it proposes.
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxInterventionsPerIssue int32 `json:"maxInterventionsPerIssue,omitempty"`
}

// PilotAutonomy is how far the pilot acts without an owner's "yes".
type PilotAutonomy string

const (
	PilotManaged PilotAutonomy = "managed"
	PilotPropose PilotAutonomy = "propose"
)

// AgentProjectStatus is the observed state of an AgentProject.
type AgentProjectStatus struct {
	// LastSyncTime is when GitLab was last polled successfully.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// BotUserID is the GitLab user behind the token. Its comments never
	// trigger runs.
	// +optional
	BotUserID int64 `json:"botUserID,omitempty"`

	// BotUsername is the username of BotUserID, read on every poll, so a
	// renamed bot is still recognized in mentions.
	// +optional
	BotUsername string `json:"botUsername,omitempty"`

	// ActiveMilestones is the number of milestones currently tracked.
	// +optional
	ActiveMilestones int32 `json:"activeMilestones,omitempty"`

	// ActiveTasks is the number of AgentTasks belonging to this project.
	// +optional
	ActiveTasks int32 `json:"activeTasks,omitempty"`

	// UsageLimits lists engines that currently hit a usage or rate limit.
	// Runs that need such an engine wait instead of hitting the limit again;
	// limits belong to the credentials, so they apply to the whole project.
	// +optional
	UsageLimits []UsageLimit `json:"usageLimits,omitempty"`

	// MainHealth is the health of the default branch.
	// +optional
	MainHealth *MainHealthStatus `json:"mainHealth,omitempty"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// UsageLimit is an engine's active usage or rate limit.
type UsageLimit struct {
	Engine Engine      `json:"engine"`
	Until  metav1.Time `json:"until"`
	// Message reported by the engine.
	// +optional
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ap
// +kubebuilder:printcolumn:name="Project",type=string,JSONPath=`.spec.gitlab.project`
// +kubebuilder:printcolumn:name="Tasks",type=integer,JSONPath=`.status.activeTasks`
// +kubebuilder:printcolumn:name="Last Sync",type=date,JSONPath=`.status.lastSyncTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentProject connects a GitLab project to the operator.
type AgentProject struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentProjectSpec   `json:"spec,omitempty"`
	Status AgentProjectStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentProjectList contains a list of AgentProject.
type AgentProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentProject `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentProject{}, &AgentProjectList{})
}
