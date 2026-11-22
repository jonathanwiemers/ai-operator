package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ExperimentAssignment is the arm an issue got in an experiment.
type ExperimentAssignment struct {
	// Name of the experiment.
	Name string `json:"name"`

	// Arm is the arm's name; empty is "no arm": the issue had an explicit
	// profile, effort or model label.
	// +optional
	Arm string `json:"arm,omitempty"`
}

// AgentCall is one engine call: what ran, for what, and what it consumed.
type AgentCall struct {
	// Seq numbers the task's calls, starting at 1.
	Seq int64 `json:"seq"`

	// At is when the call started.
	At metav1.Time `json:"at"`

	// Kind is what the call was for: spec, implement, review, fix, ci,
	// conflict, describe, pilot, retro, followUpDraft, chat, research or plan.
	Kind string `json:"kind"`

	// Role is the agent's role, e.g. implement, bugbot or pilot.
	// +optional
	Role string `json:"role,omitempty"`

	Engine Engine `json:"engine"`

	// Model is the configured model, or the reported main model when the
	// engine picked it.
	// +optional
	Model string `json:"model,omitempty"`

	// +optional
	Effort string `json:"effort,omitempty"`

	// Resumed is set when the call continued an earlier session.
	// +optional
	Resumed bool `json:"resumed,omitempty"`

	// FallbackFrom is the limited engine this call stood in for.
	// +optional
	FallbackFrom Engine `json:"fallbackFrom,omitempty"`

	// +optional
	InputTokens int64 `json:"inputTokens,omitempty"`
	// +optional
	OutputTokens int64 `json:"outputTokens,omitempty"`
	// +optional
	CacheReadTokens int64 `json:"cacheReadTokens,omitempty"`
	// +optional
	CacheWriteTokens int64 `json:"cacheWriteTokens,omitempty"`

	// CostUSD is the call's cost as a decimal string (4 decimals); empty
	// when it is unknown.
	// +optional
	CostUSD string `json:"costUSD,omitempty"`

	// CostSource says where CostUSD comes from: reported by the engine,
	// estimated from tokens, or unknown.
	CostSource string `json:"costSource"`

	// +optional
	Turns int32 `json:"turns,omitempty"`

	// +optional
	DurationMS int64 `json:"durationMS,omitempty"`

	// Run is the task's run (RunCount after it) the call belongs to.
	// +optional
	Run int64 `json:"run,omitempty"`

	// Models is the per-model split as the engine reported it, subagent
	// models included.
	// +optional
	// +listType=atomic
	Models []CallModelUsage `json:"models,omitempty"`

	// SubagentCalls is how many subagents the call started.
	// +optional
	SubagentCalls int32 `json:"subagentCalls,omitempty"`

	// ReadGateBlocks is how many tool calls the read gate denied in the
	// call.
	// +optional
	ReadGateBlocks int32 `json:"readGateBlocks,omitempty"`

	// Arm is the experiment arm of the task's issue (status.experiment)
	// while the project's experiment still has it; empty without one.
	// +optional
	Arm string `json:"arm,omitempty"`

	// StepSession is the step-session mode (resume or fresh) of an
	// implement-kind call of a stepwise run; empty otherwise.
	// +optional
	StepSession string `json:"stepSession,omitempty"`

	// ToolResults is what each tool put into the context during the call,
	// by tool name (claude engine only). A subagent's tool results count
	// under the tool that started the subagent; MCP tools keep their full
	// name; results of an unknown tool_use count under "unknown".
	// +optional
	ToolResults map[string]ToolResultUsage `json:"toolResults,omitempty"`

	// BaseContextTokens is the context size of the first turn of a call
	// that started a new session (system prompt, tools, CLAUDE.md files);
	// 0 for resumed calls and the cursor engine.
	// +optional
	BaseContextTokens int64 `json:"baseContextTokens,omitempty"`

	// Checkpoint is the reason (e.g. usage-limit) when the call's end saved
	// a checkpoint commit of the work tree; CheckpointSHA is that commit.
	// +optional
	Checkpoint string `json:"checkpoint,omitempty"`
	// +optional
	CheckpointSHA string `json:"checkpointSHA,omitempty"`

	// LinesAdded and LinesDeleted are the uncommitted diff the implementer
	// call left in the work tree (git diff --numstat HEAD plus untracked
	// text files): the lines of the step commit that follows it. Other
	// roles' calls leave them zero.
	// +optional
	LinesAdded int64 `json:"linesAdded,omitempty"`
	// +optional
	LinesDeleted int64 `json:"linesDeleted,omitempty"`

	// StepGateNudges is how often the step-size gate told the implementer
	// to close its step during the call.
	// +optional
	StepGateNudges int32 `json:"stepGateNudges,omitempty"`

	// TokenBasis is how the runner computed the call's tokens and model
	// split, see TokenBasisModels. 0 (omitted) is not recorded: an older
	// agent image wrote the call.
	// +optional
	TokenBasis int32 `json:"tokenBasis,omitempty"`

	// ContextSeries is the context size (input and cache tokens) of each
	// main-thread assistant turn. Only the newest calls in status keep it.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=500
	ContextSeries []int64 `json:"contextSeries,omitempty"`

	// SubagentTurns are the indexes into ContextSeries after which a
	// subagent ran. Only the newest calls in status keep them.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=500
	SubagentTurns []int32 `json:"subagentTurns,omitempty"`
}

// ToolResultUsage is how many results of one tool a call got and their
// size; EstimatedTokens turns the bytes into tokens.
type ToolResultUsage struct {
	// +optional
	Calls int32 `json:"calls,omitempty"`
	// +optional
	Bytes int64 `json:"bytes,omitempty"`
}

// CallModelUsage is one model's share of an agent call.
type CallModelUsage struct {
	Model string `json:"model"`
	// +optional
	InputTokens int64 `json:"inputTokens,omitempty"`
	// +optional
	OutputTokens int64 `json:"outputTokens,omitempty"`
	// +optional
	CacheReadTokens int64 `json:"cacheReadTokens,omitempty"`
	// +optional
	CacheWriteTokens int64 `json:"cacheWriteTokens,omitempty"`
	// CostUSD as a decimal string (4 decimals).
	// +optional
	CostUSD string `json:"costUSD,omitempty"`
}
