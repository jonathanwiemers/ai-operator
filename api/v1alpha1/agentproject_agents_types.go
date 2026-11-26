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

	// Effort level (claude engine only). Defaults for a claude role that
	// sets none: high for agents.spec, medium for agents.implement, the
	// profiles, agents.review and agents.pilot, low for agents.describe.
	// A fallback gets no default; agents.implement.conflict gets none of
	// its own and follows the implementer's effort on the same engine.
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

	// Fallback is the engine and model the role runs on while its own
	// engine is at a usage limit, instead of waiting for the reset. Without
	// one, or while the fallback's engine is limited too, runs wait.
	// +optional
	Fallback *RoleFallback `json:"fallback,omitempty"`

	// Subagents the role may delegate to, passed to claude as --agents
	// (claude engine only; cursor roles ignore them with a warning). The
	// default is none: claude keeps its built-in subagents.
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=8
	// +optional
	Subagents []Subagent `json:"subagents,omitempty"`

	// ReadGate blocks the role's reads of big files whole and tells it to ask
	// a subagent or read a section instead (claude engine only; cursor roles
	// and cursor fallbacks ignore it with a warning). Unset, no gate.
	// +optional
	ReadGate *ReadGate `json:"readGate,omitempty"`

	// BashMaxOutputChars sets Claude Code's BASH_MAX_OUTPUT_LENGTH for the
	// role's calls: Bash output longer than this many characters is cut
	// before it enters the context. Unset, the variable is not set and
	// Claude Code's own limit applies. Cursor ignores it.
	// +kubebuilder:validation:Minimum=1000
	// +kubebuilder:validation:Maximum=200000
	// +optional
	BashMaxOutputChars *int32 `json:"bashMaxOutputChars,omitempty"`
}

// DefaultReadGateLines is the ReadGate.MinLines default.
const DefaultReadGateLines = 350

// ReadGate configures the read gate of a role, see
// docs/reference/agents-and-models.md#read-gate.
type ReadGate struct {
	// MinLines: reads of files with more lines are blocked.
	// +kubebuilder:default=350
	// +kubebuilder:validation:Minimum=100
	// +optional
	MinLines int32 `json:"minLines,omitempty"`
}

// Lines is the effective threshold: MinLines, or 350 when it is 0 (objects
// stored before the CRD default existed, specs built by hand).
func (g *ReadGate) Lines() int {
	if g == nil || g.MinLines == 0 {
		return DefaultReadGateLines
	}
	return int(g.MinLines)
}

// Subagent is a custom claude subagent of a role, e.g. a read-only scout on
// a cheaper model for searches, lookups and log reading.
type Subagent struct {
	// Name the role delegates to.
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9-]{0,39}$`
	Name string `json:"name"`

	// Description tells the role when to use the subagent.
	// +kubebuilder:validation:MinLength=1
	Description string `json:"description"`

	// Model of the subagent, e.g. haiku or claude-haiku-5-5. Empty, it
	// inherits the role's model.
	// +optional
	Model string `json:"model,omitempty"`

	// Tools the subagent may use. Empty, claude's default for subagents.
	// +kubebuilder:validation:items:Enum=Read;Grep;Glob;WebFetch;WebSearch;Bash;Edit;Write;NotebookEdit;TodoWrite
	// +optional
	Tools []string `json:"tools,omitempty"`

	// Prompt is the subagent's system prompt.
	// +kubebuilder:validation:MinLength=1
	Prompt string `json:"prompt"`
}

// RoleFallback is the engine and model a role runs on while its own engine
// is limited. It must be another engine and an allowed model (see
// agents.models).
type RoleFallback struct {
	Engine Engine `json:"engine"`

	// Model passed to the fallback engine, or its alias, e.g. sonnet.
	// +kubebuilder:validation:MinLength=1
	Model string `json:"model"`

	// Effort level (claude engine only).
	// +kubebuilder:validation:Enum=low;medium;high;xhigh;max
	// +optional
	Effort string `json:"effort,omitempty"`
}

// ImplementSpec configures the implementing agent.
type ImplementSpec struct {
	RoleSpec `json:",inline" tstype:",extends"`

	// StepSession is how a stepwise run (draft MR, workflow.stepCommits)
	// goes on after each step: "resume" continues the same session,
	// "fresh" starts every step after the first in a new session that gets
	// the agent notes and the summaries of the steps before. Fresh steps
	// carry less context, so their cost does not grow with the session,
	// but they lose what the session remembered beyond the notes.
	// Review-fix rounds and the next run resume the last step's session
	// either way. Applies to the profiles too.
	// +kubebuilder:default=resume
	// +optional
	StepSession StepSessionMode `json:"stepSession,omitempty"`

	// MaxStepLines turns on the step-size gate of stepwise runs (draft MR,
	// workflow.stepCommits): once the uncommitted diff passes this many
	// changed lines, a hook tells the implementer to finish its step, and
	// the steps note asks to keep a step under it. Claude only; unset is
	// off. A profile's maxStepLines overrides it.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxStepLines *int32 `json:"maxStepLines,omitempty"`

	// Conflict is the role of the calls that resolve the merge conflicts of
	// a conflict run, e.g. a cheaper model for mostly mechanical merges. Its
	// fields override the issue's implementer (agents.implement or the
	// profile, with the model and effort labels); unset fields are the
	// implementer's. Unset, the implementer resolves the conflicts in its
	// own session. Applies to the profiles too.
	// +optional
	Conflict *ConflictRoleSpec `json:"conflict,omitempty"`

	// Experiment tries settings of the implementer side by side: each issue
	// gets one of its arms at its first implement run, by a stable hash of
	// the project, the experiment's name and the issue's IID, and keeps it.
	// The arm's overrides sit between the issue's labels and the project's
	// config. The ExperimentValid condition reports cross-field problems;
	// an invalid experiment assigns no arms. Unset, no experiment.
	// +optional
	Experiment *Experiment `json:"experiment,omitempty"`
}

// Experiment is a named set of arms, see ImplementSpec.Experiment.
type Experiment struct {
	// Name of the experiment, recorded with the arm. Renaming it starts a
	// new experiment: issues assigned under the old name run without arm.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=40
	Name string `json:"name"`

	// Arms are the variants the issues are spread over. At most one arm may
	// override nothing: the baseline.
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MinItems=2
	// +kubebuilder:validation:MaxItems=4
	Arms []ExperimentArm `json:"arms"`
}

// ExperimentArm overrides settings of the implement role for the issues
// assigned to it. Unset fields keep the project's config.
type ExperimentArm struct {
	// Name of the arm, recorded on the task and its agent calls.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=40
	Name string `json:"name"`

	// StepSession overrides agents.implement.stepSession.
	// +optional
	StepSession StepSessionMode `json:"stepSession,omitempty"`

	// Effort overrides the implementer's effort, as an effort label the
	// planner set would. It may not be above agents.maxEffort.
	// +kubebuilder:validation:Enum=low;medium;high;xhigh;max
	// +optional
	Effort string `json:"effort,omitempty"`

	// Profile runs the issue on this profile instead of the one the planner
	// picked. It must be one of the project's profiles.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	// +optional
	Profile string `json:"profile,omitempty"`
}

// Empty reports whether the arm overrides nothing (the baseline).
func (a ExperimentArm) Empty() bool {
	return a.StepSession == "" && a.Effort == "" && a.Profile == ""
}

// ConflictRoleSpec configures the calls that resolve merge conflicts.
type ConflictRoleSpec struct {
	RoleSpec `json:",inline" tstype:",extends"`

	// FreshSession starts each conflict call in a new session with a
	// self-contained prompt (the issue, the agent notes, the conflicting
	// files and the MR's diff stat) instead of resuming the implementer's
	// long session; the implementer's session stays for its later calls.
	// false resumes the implementer's session when the engine is the same.
	// +kubebuilder:default=true
	// +optional
	FreshSession *bool `json:"freshSession,omitempty"`
}

// Fresh reports whether a conflict call starts a new session: freshSession,
// true when unset.
func (c *ConflictRoleSpec) Fresh() bool {
	return c != nil && (c.FreshSession == nil || *c.FreshSession)
}

// StepSessionMode is how an implementer goes on after a step.
// +kubebuilder:validation:Enum=resume;fresh
type StepSessionMode string

const (
	StepSessionResume StepSessionMode = "resume"
	StepSessionFresh  StepSessionMode = "fresh"
)
