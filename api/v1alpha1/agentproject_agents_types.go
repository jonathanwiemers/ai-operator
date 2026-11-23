package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

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
	// profile label (e.g. issues created by hand). Its stepSession applies
	// to the profiles too.
	// +optional
	Implement ImplementSpec `json:"implement,omitempty"`

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

	// FallbackAtPercent switches a role with a fallback to it before its
	// engine's hard limit: when the project's freshest usage reading of the
	// engine has a window, not reset since, used at least this much. Roles
	// without a fallback are not affected. Unset, only a limit switches.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	FallbackAtPercent *int32 `json:"fallbackAtPercent,omitempty"`

	// Chat turns Desk live chat on for this project's tasks. The operator
	// hands queued chat_messages rows to the tasks; without it those rows
	// are marked failed. Needs the research database.
	// +kubebuilder:default=false
	// +optional
	Chat bool `json:"chat,omitempty"`
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
	RoleSpec `json:",inline" tstype:",extends"`

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

	RoleSpec `json:",inline" tstype:",extends"`

	// MaxStepLines overrides agents.implement.maxStepLines for this
	// profile; unset inherits it.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxStepLines *int32 `json:"maxStepLines,omitempty"`
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

// CommandsFrom is who may give "/agent" commands.
// +kubebuilder:validation:Enum=members;owners
type CommandsFrom string

const (
	CommandsFromMembers CommandsFrom = "members"
	CommandsFromOwners  CommandsFrom = "owners"
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
