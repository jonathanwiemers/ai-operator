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

	// CommandPrefix: a comment on the issue or its merge request that starts
	// with this prefix triggers another agent run (e.g. "/agent please also
	// cover the error case"). Plain discussion does not trigger runs, so no
	// tokens are spent on chatter.
	// +kubebuilder:default="/agent"
	// +optional
	CommandPrefix string `json:"commandPrefix,omitempty"`
}

// AgentsSpec configures the agent pods and the roles running inside them.
type AgentsSpec struct {
	// Image of the agent pod. It must contain the ai-runner binary, git, the
	// agent CLIs used (claude, cursor-agent) and whatever toolchain the
	// project needs to build and test.
	Image string `json:"image"`

	// +kubebuilder:default=IfNotPresent
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// ServiceAccountName used by the agent pods. It needs read access to
	// AgentTasks/AgentProjects and patch access to agenttasks/status.
	// +kubebuilder:default="ai-operator-agent"
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// CredentialsSecretName names a secret whose keys are exposed to the agent
	// CLIs as environment variables. Use whichever the engines need:
	//   CLAUDE_CODE_OAUTH_TOKEN  Claude subscription (Pro/Max), from `claude setup-token`
	//   ANTHROPIC_API_KEY        Claude API billing instead of a subscription
	//   CURSOR_API_KEY           cursor-agent (Cursor subscription)
	CredentialsSecretName string `json:"credentialsSecretName"`

	// Resources of the agent container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

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

	// NodeSelector for agent pods.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations for agent pods.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Spec configures the spec (planning) agent.
	// +optional
	Spec RoleSpec `json:"spec,omitempty"`

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
// A task goes through: Running -> Idle (pod kept warm) -> Hibernated (pod
// deleted, workspace PVC kept) -> Archived (PVC deleted; the branch and the
// GitLab history remain, so the next run rehydrates from a fresh clone).
type LifecycleSpec struct {
	// IdleTimeout after which an idle pod is deleted while its workspace is kept.
	// +kubebuilder:default="2h"
	// +optional
	IdleTimeout metav1.Duration `json:"idleTimeout,omitempty"`

	// RetentionPeriod after the last activity after which the workspace PVC is
	// deleted as well.
	// +kubebuilder:default="336h"
	// +optional
	RetentionPeriod metav1.Duration `json:"retentionPeriod,omitempty"`

	// MaxRunsPerTask is a safety net against runaway loops.
	// +kubebuilder:default=20
	// +optional
	MaxRunsPerTask int32 `json:"maxRunsPerTask,omitempty"`
}

// AgentProjectStatus is the observed state of an AgentProject.
type AgentProjectStatus struct {
	// LastSyncTime is when GitLab was last polled successfully.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// BotUserID is the GitLab user behind the token. Its comments never
	// trigger runs.
	// +optional
	BotUserID int64 `json:"botUserID,omitempty"`

	// ActiveMilestones is the number of milestones currently tracked.
	// +optional
	ActiveMilestones int32 `json:"activeMilestones,omitempty"`

	// ActiveTasks is the number of AgentTasks belonging to this project.
	// +optional
	ActiveTasks int32 `json:"activeTasks,omitempty"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
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
