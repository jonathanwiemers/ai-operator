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

// QueueLane is the lane of the project's run queue a task waits in.
// +kubebuilder:validation:Enum=urgent
type QueueLane string

// QueueLaneUrgent goes ahead of every other task: the fix of a red default
// branch.
const QueueLaneUrgent QueueLane = "urgent"

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

	// OwnerSubjects are the Desk identities (OIDC username claim) that own
	// the milestone, from its "<prefix> owner-subjects …" line. Used to
	// authorize Desk accept/reject of pilot proposals. No project-level
	// or default fallback.
	// +optional
	OwnerSubjects []string `json:"ownerSubjects,omitempty"`

	// Rules are the standing rules of the milestone: its description's
	// lines "/agent rule R<n>: <kind> <text>" without "/agent rule " (spec
	// and Coordinate tasks).
	// +optional
	Rules []string `json:"rules,omitempty"`

	// BudgetUSD is the milestone's budget in USD, from its description's
	// line "/agent budget <usd>" (spec tasks). Empty: no budget. On implement
	// tasks the issue's budget, from an "/agent budget <usd>" comment on it,
	// in place of the project's budget.perIssueUSD.
	// +optional
	BudgetUSD string `json:"budgetUSD,omitempty"`

	// BudgetStop is set by the operator on implement tasks while autopilot
	// is paused for their budget (the issue's, or the milestone's for all
	// of its implement tasks). The runner starts no further agent call: a
	// run in progress ends deferred after its current call, a pending one
	// waits until the flag is cleared. Desk chat turns still run.
	// +optional
	BudgetStop bool `json:"budgetStop,omitempty"`

	// QueueLane is set by the operator while the task carries the fix of a
	// red default branch (urgent). Empty: the task queues by its milestone.
	// +optional
	QueueLane QueueLane `json:"queueLane,omitempty"`

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

	// PilotRun is a run request the pilot started (Implement tasks): that
	// run keeps the issue's CI fix rounds (status.run.ciRound), unlike a
	// human's.
	// +optional
	PilotRun int64 `json:"pilotRun,omitempty"`

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

	// ChatMessages are Desk chat turns for this task's agent. Written by
	// the operator, answered by the runner, dropped by the operator once
	// the reply is stored. At most 20 queued. Kind "command" rows are
	// applied by the project reconciler, not the runner.
	// +optional
	// +kubebuilder:validation:MaxItems=20
	ChatMessages []ChatMessage `json:"chatMessages,omitempty"`

	// ChatContext is the Desk chat a fresh implementer session gets instead
	// of the comment threads on a project without GitLab input
	// (workflow.inputs [desk]): the task's last answered messages of both
	// sides, oldest first, at most 30 KiB of text. Set by the operator with
	// each run request.
	// +optional
	// +kubebuilder:validation:MaxItems=20
	ChatContext []ChatMessage `json:"chatContext,omitempty"`

	// LastChatCommandID is the newest Desk chat command (kind "command")
	// the project reconciler has applied (Implement, Spec autopilot, or
	// Coordinate tasks).
	// +optional
	LastChatCommandID int64 `json:"lastChatCommandID,omitempty"`

	// ChatAnswers are the operator's answers to Desk chat commands, keyed
	// by message ID. The chatreply reconciler copies them into
	// chat_messages and drops them with their messages. At most 20.
	// +optional
	// +kubebuilder:validation:MaxItems=20
	ChatAnswers []ChatReply `json:"chatAnswers,omitempty"`

	// PilotEvents are the events the operator woke the pilot for
	// (Coordinate tasks), each assessed by the run of its request.
	// +optional
	PilotEvents []PilotEvent `json:"pilotEvents,omitempty"`

	// PromptRefines are owners' free-text answers to action prompts
	// (Coordinate tasks), each turned by the pilot's run of its request into
	// one option or one follow-up question (status.pilot.refined).
	// +optional
	// +kubebuilder:validation:MaxItems=5
	PromptRefines []PromptRefine `json:"promptRefines,omitempty"`

	// PromptAnswers are owners' answers to the implementer's needsInput
	// prompts (Implement tasks). The runner delivers each to the agent's
	// session once, from the run of its request on.
	// +optional
	// +kubebuilder:validation:MaxItems=5
	PromptAnswers []PromptAnswerDelivery `json:"promptAnswers,omitempty"`

	// Retrospective is the run request that writes the retrospective of the
	// milestone (Coordinate tasks), once its autopilot is done.
	// +optional
	Retrospective int64 `json:"retrospective,omitempty"`

	// RetroSpend is what the milestone cost, from the history database
	// (agent_calls), set by the operator with Retrospective (Coordinate
	// tasks). Unset without the database or when its query failed: the
	// retrospective sums the live tasks then.
	// +optional
	RetroSpend *SpendSummary `json:"retroSpend,omitempty"`

	// PilotInstruction is an instruction an owner accepted from the pilot
	// (Implement tasks), for the run of its request.
	// +optional
	PilotInstruction *PilotInstruction `json:"pilotInstruction,omitempty"`
}

// SpendSummary is a scope's spend with its biggest cost drivers, each list
// the top 3 by USD.
type SpendSummary struct {
	// CostUSD sums the known costs (4 decimals).
	// +optional
	CostUSD string `json:"costUSD,omitempty"`
	// CostKnown is false when no call reported or estimated a cost.
	// +optional
	CostKnown bool `json:"costKnown,omitempty"`
	// +optional
	Tokens SpendTokens `json:"tokens,omitempty"`
	// SubagentCalls is how often the calls delegated to a subagent.
	// +optional
	SubagentCalls int64 `json:"subagentCalls,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=3
	TopIssues []SpendDriver `json:"topIssues,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=3
	TopKinds []SpendDriver `json:"topKinds,omitempty"`
	// TopModels counts subagent models as their own entries.
	// +optional
	// +kubebuilder:validation:MaxItems=3
	TopModels []SpendDriver `json:"topModels,omitempty"`
}

// SpendTokens are tokens by type.
type SpendTokens struct {
	// +optional
	Input int64 `json:"input,omitempty"`
	// +optional
	Output int64 `json:"output,omitempty"`
	// +optional
	CacheRead int64 `json:"cacheRead,omitempty"`
	// +optional
	CacheWrite int64 `json:"cacheWrite,omitempty"`
}

// Total is the sum of all tokens.
func (t SpendTokens) Total() int64 { return t.Input + t.Output + t.CacheRead + t.CacheWrite }

// SpendDriver is one issue, kind or model of a SpendSummary: Key is the
// issue IID, the kind or the model, Label how to name it.
type SpendDriver struct {
	Key string `json:"key"`
	// +optional
	Label string `json:"label,omitempty"`
	// CostUSD is empty when none of its calls has a known cost.
	// +optional
	CostUSD string `json:"costUSD,omitempty"`
	// +optional
	Tokens int64 `json:"tokens,omitempty"`
}

// ChatMessage is one Desk chat turn handed to the runner.
type ChatMessage struct {
	// ID is the chat_messages row id.
	ID int64 `json:"id"`
	// Kind is message, interrupt or command. The runner skips command;
	// the operator handles those.
	// +kubebuilder:validation:Enum=message;interrupt;command
	Kind string `json:"kind"`
	// Text is the user's message, at most 16 KiB.
	// +kubebuilder:validation:MaxLength=16384
	Text string `json:"text"`
	// Author is the OIDC subject.
	Author string `json:"author"`
	// AuthorName is a display name for the prompt.
	// +optional
	AuthorName string `json:"authorName,omitempty"`
	// Role is who wrote a chatContext message: user or agent. Unset on
	// chatMessages, which are the user's.
	// +optional
	// +kubebuilder:validation:Enum=user;agent
	Role string `json:"role,omitempty"`
	// At is when the message was written.
	At metav1.Time `json:"at"`
}

// PilotInstruction is what the implementer gets of an accepted instruct:
// a fixed kind the runner turns into a fixed sentence, with the pilot's
// note as untrusted data, or an owner's own words.
type PilotInstruction struct {
	// +optional
	Kind string `json:"kind,omitempty"`
	// +optional
	Note string `json:"note,omitempty"`
	// +optional
	Owner string `json:"owner,omitempty"`
	// +optional
	Text string `json:"text,omitempty"`
	// Request is the run request it is for.
	Request int64 `json:"request"`
}

// PilotInstructKinds are the instructions the pilot may give.
var PilotInstructKinds = []string{"ci-fix", "install-toolchains", "rerun"}

// MaxPilotSpec bounds the new spec text of an editSpec, in bytes.
const MaxPilotSpec = 64 << 10

// MaxAutopilotItemDecisions bounds AutopilotItem.Decisions (the newest are kept).
const MaxAutopilotItemDecisions = 20

// PilotAction is one action of a proposal of the pilot; the operator
// validates and executes it once an owner accepts the proposal.
type PilotAction struct {
	// Tool: retry, reReview, instruct, openIssue, fixFlaky, retryPipeline,
	// rebase, mergeBase, skip, unskip, startAnyway, markDone, pause, resume,
	// revisit, setRule, forgetRule or editSpec; the owner-only raiseBudget
	// and endMilestone come only in options the operator builds (budget
	// prompts), never from the model. unskip, startAnyway and markDone run
	// only on an owner's answer.
	Tool string `json:"tool"`
	// Decision to revisit, e.g. D12-3; Note is the change asked for.
	// +optional
	Decision string `json:"decision,omitempty"`
	// +optional
	IssueIID int64 `json:"issueIID,omitempty"`
	// Kind of an instruct, one of PilotInstructKinds.
	// +optional
	Kind string `json:"kind,omitempty"`
	// Note: the pilot's hint to the implementer (instruct), the body of
	// the issue it opens (openIssue), the change asked for (revisit), why
	// an issue is done (markDone). Untrusted.
	// +optional
	Note string `json:"note,omitempty"`
	// Title of the issue it opens (openIssue).
	// +optional
	Title string `json:"title,omitempty"`
	// Owner and Text: an owner's own words for the implementer ("/pilot
	// tell #N: …"). Only the operator sets them.
	// +optional
	Owner string `json:"owner,omitempty"`
	// +optional
	Text string `json:"text,omitempty"`
	// Rule is the ID ("R2") of the standing rule forgetRule removes. A
	// setRule has its kind (switch, pilot, prompt) in Kind and its text in
	// Note; a forgetRule the operator's copy of the rule's text in Note.
	// +optional
	Rule string `json:"rule,omitempty"`
	// Spec is the full new spec text of an editSpec, which replaces the
	// milestone description except its command lines (markers, owners,
	// rules). Untrusted until an owner accepts its diff.
	// +optional
	Spec string `json:"spec,omitempty"`
	// PipelineID is the red default-branch pipeline whose failed jobs
	// retryPipeline retries.
	// +optional
	PipelineID int64 `json:"pipelineID,omitempty"`
	// Amount is the new milestone budget of a raiseBudget, a decimal in
	// USD ("48").
	// +optional
	Amount string `json:"amount,omitempty"`
}

// PilotProposal is a set of actions that waits for an owner's "yes".
type PilotProposal struct {
	// ID is n of "P<n>", given when the operator posts it.
	// +optional
	ID      int64  `json:"id,omitempty"`
	Summary string `json:"summary"`
	// Actions are empty for a proposal with Options: each option carries
	// its own.
	// +optional
	Actions []PilotAction `json:"actions,omitempty"`
	// State is PilotState when it was made.
	State string `json:"state"`
	// +optional
	PostedAt *metav1.Time `json:"postedAt,omitempty"`
	// AcceptedBy is the owner who accepted it.
	// +optional
	AcceptedBy string `json:"acceptedBy,omitempty"`
	// OwnerIDs are the user IDs of the milestone's owners when it was
	// posted, sorted: only they answer it. Empty on a proposal posted before
	// they were pinned.
	// +optional
	OwnerIDs []int64 `json:"ownerIDs,omitempty"`
	// Source is the message or event it answers, so a run started again
	// does not propose it twice.
	// +optional
	Source string `json:"source,omitempty"`
	// Trigger is the issue whose event or message it answers, 0 for the
	// milestone (the tracking issue, a merge request): an openIssue counts
	// against it.
	// +optional
	Trigger int64 `json:"trigger,omitempty"`
	// Why is the classification and evidence, or the owner's message, it
	// answers, for the audit comment. Untrusted.
	// +optional
	Why string `json:"why,omitempty"`
	// Quiet: the run did not mention the owners (an assessment of a pilot
	// that manages), so the operator does when it posts the proposal.
	// +optional
	Quiet bool `json:"quiet,omitempty"`
	// Prompt is the id ("Q3") of the prompt whose chosen option this
	// accepted proposal runs: its outcome goes into the prompt's answer.
	// +optional
	Prompt string `json:"prompt,omitempty"`
	// Kind, Question, Context and Options make it a prompt with the
	// pilot's own options (an assessment) instead of "Do it" or "Do
	// nothing" for Actions; State is then that of the recommended option's
	// actions. Untrusted; the operator checks every option's actions.
	// +optional
	Kind PromptKind `json:"kind,omitempty"`
	// +optional
	Question string `json:"question,omitempty"`
	// +optional
	Context string `json:"context,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=5
	Options []PromptOption `json:"options,omitempty"`
}

// PilotEvent is something the pilot assesses without being asked: an
// autopilot pause, an issue that needs input or is blocked by infra or
// quota, a red or flaky default branch.
type PilotEvent struct {
	// Key identifies the event: it wakes the pilot once.
	Key string `json:"key"`
	// IssueIID is the issue it is about, if any.
	// +optional
	IssueIID int64 `json:"issueIID,omitempty"`
	// PipelineID is the red pipeline it is about, if any.
	// +optional
	PipelineID int64 `json:"pipelineID,omitempty"`
	// Text says what happened; it can quote titles and GitLab's errors.
	Text string `json:"text"`
	// Request is the run request that assesses it.
	Request int64 `json:"request"`
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
	// user ID. No longer written: each comment gets its own reply, counted
	// in the project's status.commandReplies.authors.
	// +optional
	Rejected map[string]metav1.Time `json:"rejected,omitempty"`

	// Events are the keys of the events handed to the pilot (Coordinate
	// tasks), those that hold first, then the last ones that ended.
	// +optional
	Events []string `json:"events,omitempty"`

	// Proposed are the proposals of the pilot's runs the operator has not
	// posted yet (Coordinate tasks).
	// +optional
	Proposed []PilotProposal `json:"proposed,omitempty"`

	// Proposals are the open proposals, posted on the tracking issue.
	// +optional
	Proposals []PilotProposal `json:"proposals,omitempty"`

	// Accepted are the proposals owners accepted, run at the end of the
	// poll.
	// +optional
	Accepted []PilotProposal `json:"accepted,omitempty"`

	// Refined are the pilot's answers to spec.source.promptRefines
	// (Coordinate tasks), newest last, until the operator takes them.
	// +optional
	// +kubebuilder:validation:MaxItems=5
	Refined []PromptRefinement `json:"refined,omitempty"`

	// LastProposal is the ID of the newest proposal.
	// +optional
	LastProposal int64 `json:"lastProposal,omitempty"`

	// OpenedIssues are the title keys of the issues the pilot opened.
	// +optional
	OpenedIssues []string `json:"openedIssues,omitempty"`

	// Commands are the queue commands of accepted proposals (spec tasks),
	// oldest first, for autopilot and the revisits to apply once each.
	// +optional
	Commands []PilotCommand `json:"commands,omitempty"`

	// AutonomyOverride is the owners' last "/pilot stop" or "/pilot go"
	// (Coordinate tasks). It can only lower the project's pilot.autonomy.
	// +optional
	AutonomyOverride *PilotAutonomyOverride `json:"autonomyOverride,omitempty"`

	// Interventions count the pilot's own actions per issue on the issue's
	// current head (Coordinate tasks), by issue IID; "0" is the milestone.
	// +optional
	Interventions map[string]PilotIntervention `json:"interventions,omitempty"`

	// Retrospective is the retrospective of the milestone once its autopilot
	// is done: recorded by the pilot's run (Coordinate tasks) and copied to
	// the spec task before the pilot is deleted. A milestone gets one.
	// +optional
	Retrospective *PilotRetrospective `json:"retrospective,omitempty"`
}

// PilotRetrospective is the last comment of a finished autopilot milestone's
// pilot on its tracking issue.
type PilotRetrospective struct {
	// NoteID is the comment.
	// +optional
	NoteID int64 `json:"noteID,omitempty"`
	// +optional
	PostedAt *metav1.Time `json:"postedAt,omitempty"`
	// FollowUps are the numbered follow-up proposals, which an owner opens
	// as issues. Untrusted.
	// +optional
	FollowUps []PilotFollowUp `json:"followUps,omitempty"`
	// Failed says why there is no retrospective; it is not tried again.
	// +optional
	Failed string `json:"failed,omitempty"`
	// OwnerIDs are the user IDs of the milestone's owners when it was
	// posted, sorted: only they open the follow-ups.
	// +optional
	OwnerIDs []int64 `json:"ownerIDs,omitempty"`
	// Created are the issues opened from the follow-ups, by number ("1").
	// +optional
	Created map[string]int64 `json:"created,omitempty"`
	// LastNoteID is the newest comment on the tracking issue read for
	// "create 1 2" (spec tasks).
	// +optional
	LastNoteID int64 `json:"lastNoteID,omitempty"`
}

// PilotFollowUp is a follow-up proposal of a retrospective.
type PilotFollowUp struct {
	Title string `json:"title"`
	// +optional
	Body string `json:"body,omitempty"`
}

// PilotCommand is a skip, pause, resume or revisit an owner accepted from
// the pilot.
type PilotCommand struct {
	// Seq orders the commands; each consumer remembers the last it handled.
	Seq    int64       `json:"seq"`
	Action PilotAction `json:"action"`
	// Owner is who accepted it, OnIssue where it is answered.
	Owner   string `json:"owner"`
	OnIssue int64  `json:"onIssue"`
}

// PilotAutonomyOverride is an owner's "/pilot stop" (propose) or "/pilot
// go" (managed).
type PilotAutonomyOverride struct {
	Autonomy PilotAutonomy `json:"autonomy"`
	By       string        `json:"by"`
	At       metav1.Time   `json:"at"`
}

// PilotIntervention counts the pilot's own actions on an issue's head.
type PilotIntervention struct {
	// +optional
	Head  string `json:"head,omitempty"`
	Count int32  `json:"count"`
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
	// and Coordinate tasks).
	// +optional
	Pilot *PilotStatus `json:"pilot,omitempty"`

	// Chat holds the runner's replies to Desk chat messages. The operator
	// copies them into chat_messages and drops the answered messages from
	// the spec.
	// +optional
	Chat *ChatStatus `json:"chat,omitempty"`

	// Prompts are the open and recent questions to the owners, newest last,
	// at most MaxPrompts: the milestone's on spec tasks, the agent's
	// (needsInput) on Implement tasks.
	// +optional
	// +kubebuilder:validation:MaxItems=20
	Prompts []Prompt `json:"prompts,omitempty"`

	// LastPrompt is n of the newest "Q<n>" given out.
	// +optional
	LastPrompt int64 `json:"lastPrompt,omitempty"`

	// WriteFailures are the status writes of the pilot and the operator to
	// this task that the API server rejected, one per source and error,
	// newest last, at most MaxWriteFailures.
	// +optional
	// +kubebuilder:validation:MaxItems=5
	WriteFailures []WriteFailure `json:"writeFailures,omitempty"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// MaxWriteFailures and MaxWriteFailureError bound status.writeFailures.
const (
	MaxWriteFailures     = 5
	MaxWriteFailureError = 1000
)

// WriteFailure is a rejected status write: the pilot's (runner) or the
// operator's patch the API server refused, e.g. for a schema violation.
type WriteFailure struct {
	// Source is the write, e.g. "runner/recordProposal" or
	// "operator/autopilot.save".
	// +kubebuilder:validation:MaxLength=100
	Source string `json:"source"`

	// Error is the API server's error, cut to MaxWriteFailureError bytes.
	// +kubebuilder:validation:MaxLength=1000
	Error string `json:"error"`

	// Key is a hash of source and error: the same failure again only
	// bumps Count.
	// +kubebuilder:validation:MaxLength=64
	Key string `json:"key"`

	// At is the last time it failed.
	// +optional
	At *metav1.Time `json:"at,omitempty"`

	// Count is how often it failed.
	// +optional
	// +kubebuilder:validation:Minimum=0
	Count int32 `json:"count,omitempty"`
}

// ChatStatus is the runner's answers to Desk chat turns.
type ChatStatus struct {
	// Replies are answers keyed by message id, newest last, at most 20.
	// +optional
	// +kubebuilder:validation:MaxItems=20
	Replies []ChatReply `json:"replies,omitempty"`

	// LastTurnTime is when the runner last finished a chat turn.
	// +optional
	LastTurnTime *metav1.Time `json:"lastTurnTime,omitempty"`
}

// ChatReply is one answer to a ChatMessage.
type ChatReply struct {
	MessageID int64 `json:"messageID"`
	// Text is the agent's answer, made inert and cut to 16 KiB.
	// +kubebuilder:validation:MaxLength=16384
	// +optional
	Text string      `json:"text,omitempty"`
	At   metav1.Time `json:"at"`
	// Error is set when the turn did not finish normally (e.g. "interrupted",
	// "usage limit until <time>").
	// +optional
	Error string `json:"error,omitempty"`
	// PushedSHA is set when a writable turn committed and pushed.
	// +optional
	PushedSHA string `json:"pushedSHA,omitempty"`
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

	// Questions are the questions the agent asked in the most recent run,
	// structured ones first, at most MaxQuestionDrafts: the operator makes
	// needsInput prompts of them.
	// +optional
	// +kubebuilder:validation:MaxItems=5
	Questions []QuestionDraft `json:"questions,omitempty"`

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

	// DeferredUntil is set when the requested run hit a usage or rate limit,
	// or its agent ran out of memory: the run is retried after this time.
	// +optional
	DeferredUntil *metav1.Time `json:"deferredUntil,omitempty"`

	// LimitedEngine is the engine whose limit deferred the run.
	// Deprecated: the deferring limit is the usageLimits entry with
	// deferred set. Still written for one release; read only when no entry
	// is marked (tasks of an older release).
	// +optional
	LimitedEngine Engine `json:"limitedEngine,omitempty"`

	// BlockReason is why the issue waits for something outside it: quota
	// while the run is deferred by a usage limit or after it gave up on
	// one; infra after it failed because the agent pod or container was
	// lost (also out of memory) or it was interrupted too often. The
	// operator blocks the issue with it.
	// +optional
	BlockReason BlockReason `json:"blockReason,omitempty"`

	// EngineUsage is the newest usage reading per engine the runner got
	// from an agent call of this task (claude only); the project collects
	// the freshest of all tasks.
	// +optional
	EngineUsage []EngineUsage `json:"engineUsage,omitempty"`

	// UsageLimits are the limits this task's runs hit: those they went
	// around with a fallback engine, and the one that deferred the run
	// (deferred set). The operator adds the active ones to the project's
	// usage limits.
	// +optional
	UsageLimits []UsageLimit `json:"usageLimits,omitempty"`

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

	// ReviewErrorKinds says why each reviewer failed in the last review.
	// +optional
	// +listType=atomic
	ReviewErrorKinds []ReviewError `json:"reviewErrorKinds,omitempty"`

	// ReviewedFor is the head the last re-review was asked for
	// (spec.source.reviewFor).
	// +optional
	ReviewedFor string `json:"reviewedFor,omitempty"`

	// ReviewedSHA is the commit the last review saw. Runs without a review
	// (CI fix runs) can push commits after it, so a clean review only covers
	// PushedSHA when the two are equal.
	// +optional
	ReviewedSHA string `json:"reviewedSHA,omitempty"`

	// Checkpoint is the last checkpoint pushed; compare SHA with the branch
	// head, it may be stale. Cleared when a run pushes a regular head.
	// +optional
	Checkpoint *CheckpointStatus `json:"checkpoint,omitempty"`

	// Calls are the task's newest engine calls, oldest first (AppendCalls
	// keeps the last MaxCalls). The operator writes each call it has not
	// seen yet to its history.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=50
	Calls []AgentCall `json:"calls,omitempty"`

	// LastCallSeq is the seq of the task's newest call; it keeps growing
	// across runs while Calls is trimmed.
	// +optional
	LastCallSeq int64 `json:"lastCallSeq,omitempty"`
}

// CheckpointStatus is a checkpoint commit the runner pushed after an
// implementer call was cut off.
type CheckpointStatus struct {
	// SHA is the checkpoint commit.
	SHA string `json:"sha"`

	// Branch it was pushed to.
	Branch string `json:"branch"`

	// Reason the call was cut off: usage-limit, engine-error, timeout or
	// budget-stop.
	Reason string `json:"reason"`

	// Run is the run (status.run.runCount after it) and Step the
	// implementer call of the run that was cut off, from 1.
	// +optional
	Run int64 `json:"run,omitempty"`
	// +optional
	Step int32 `json:"step,omitempty"`

	// Files, Added and Deleted are the checkpoint's diff stat.
	// +optional
	Files int32 `json:"files,omitempty"`
	// +optional
	Added int64 `json:"added,omitempty"`
	// +optional
	Deleted int64 `json:"deleted,omitempty"`

	// Skipped are the files left out (too big, or new files that look like
	// secrets); they stayed in the workspace.
	// +optional
	// +listType=atomic
	Skipped []string `json:"skipped,omitempty"`

	// At is when the checkpoint was pushed.
	At metav1.Time `json:"at"`
}

// Kinds of agent calls (AgentCall.Kind).
const (
	CallKindSpec      = "spec"
	CallKindImplement = "implement"
	CallKindReview    = "review"
	CallKindFix       = "fix"
	CallKindCI        = "ci"
	CallKindConflict  = "conflict"
	CallKindDescribe  = "describe"
	CallKindPilot     = "pilot"
	CallKindRetro     = "retro"
	CallKindChat      = "chat"
	CallKindResearch  = "research"
)

// Sources of an agent call's cost (AgentCall.CostSource).
const (
	CostReported  = "reported"
	CostEstimated = "estimated"
	CostUnknown   = "unknown"
)

// AgentCall is one engine call: what ran, for what, and what it consumed.
type AgentCall struct {
	// Seq numbers the task's calls, starting at 1.
	Seq int64 `json:"seq"`

	// At is when the call started.
	At metav1.Time `json:"at"`

	// Kind is what the call was for: spec, implement, review, fix, ci,
	// conflict, describe, pilot, retro, chat or research.
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
	metav1.TypeMeta   `json:",inline" tstype:",extends"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentTaskSpec   `json:"spec,omitempty"`
	Status AgentTaskStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentTaskList contains a list of AgentTask.
type AgentTaskList struct {
	metav1.TypeMeta `json:",inline" tstype:",extends"`
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

// HasChatTurns reports whether the task has unanswered Desk chat messages
// that need the agent pod (message or interrupt). Commands are applied by
// the project reconciler and do not keep a pod.
func (t *AgentTask) HasChatTurns() bool {
	for _, m := range t.Spec.Source.ChatMessages {
		if m.Kind != "command" {
			return true
		}
	}
	return false
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

// MergeOverride lets the merge gate skip its review checks for one head:
// failed or missing reviews and blocking findings. CI, the MR state and
// approvals still apply.
type MergeOverride struct {
	IssueIID int64 `json:"issueIID"`
	// SHA is the head it was given for.
	SHA string `json:"sha"`
	// By is who decided it.
	By string      `json:"by"`
	At metav1.Time `json:"at"`
}

// ReviewErrorKind is why one reviewer failed.
// +kubebuilder:validation:Enum=usageLimit;transient;other
type ReviewErrorKind string

const (
	// ReviewErrorUsageLimit: the reviewer's engine hit its usage limit.
	ReviewErrorUsageLimit ReviewErrorKind = "usageLimit"
	// ReviewErrorTransient: an outage of the reviewer's service.
	ReviewErrorTransient ReviewErrorKind = "transient"
	// ReviewErrorOther: any other failure.
	ReviewErrorOther ReviewErrorKind = "other"
)

// ReviewError is one reviewer's failure in the last review.
type ReviewError struct {
	Reviewer ReviewerKind    `json:"reviewer"`
	Kind     ReviewErrorKind `json:"kind"`
	// Engine is the limited engine (kind usageLimit).
	// +optional
	Engine Engine `json:"engine,omitempty"`
	// ResetAt is when the limit resets, if the engine said so.
	// +optional
	ResetAt *metav1.Time `json:"resetAt,omitempty"`
}

// PauseKind is why autopilot paused.
// +kubebuilder:validation:Enum=owner;cycle;stuck;needsInput;runFailed;findings;reviewErrors;notReviewed;ciRed;ciCanceled;mergeRequest;mainRed;config;budget
type PauseKind string

const (
	// PauseOwner: the pause command.
	PauseOwner PauseKind = "owner"
	// PauseCycle: the plan has a dependency cycle.
	PauseCycle PauseKind = "cycle"
	// PauseStuck: no queued issue can start.
	PauseStuck PauseKind = "stuck"
	// PauseNeedsInput: the active issue has the needs-input label.
	PauseNeedsInput PauseKind = "needsInput"
	// PauseRunFailed: the active issue's last run did not succeed.
	PauseRunFailed PauseKind = "runFailed"
	// PauseFindings: blocking review findings remain.
	PauseFindings PauseKind = "findings"
	// PauseReviewErrors: reviewers failed (status.run.reviewErrorKinds).
	PauseReviewErrors PauseKind = "reviewErrors"
	// PauseNotReviewed: the head was pushed after the last review.
	PauseNotReviewed PauseKind = "notReviewed"
	// PauseCIRed: CI of the MR is red.
	PauseCIRed PauseKind = "ciRed"
	// PauseCICanceled: the MR's pipeline was canceled.
	PauseCICanceled PauseKind = "ciCanceled"
	// PauseMergeRequest: the MR cannot be merged (state, draft, approvals,
	// discussions, conflicts, a moved head, GitLab refused).
	PauseMergeRequest PauseKind = "mergeRequest"
	// PauseMainRed: the default branch is red (merge freeze).
	PauseMainRed PauseKind = "mainRed"
	// PauseConfig: the project or issue setup does not allow a merge (no
	// workflow label, no reviewers, no pushed MR).
	PauseConfig PauseKind = "config"
	// PauseBudget: the milestone spent its budget (spec.budgetUSD).
	PauseBudget PauseKind = "budget"
)

// PauseResolver is who ended a pause (status.autopilot.resumedBy, the
// autopilot.resumed history event).
// +kubebuilder:validation:Enum=operator;pilot;owner;selfHealed
type PauseResolver string

const (
	// ResolvedByOperator: an automatic exit (the cause went away).
	ResolvedByOperator PauseResolver = "operator"
	// ResolvedByPilot: the pilot's own action (managed autonomy).
	ResolvedByPilot PauseResolver = "pilot"
	// ResolvedByOwner: an owner's command or answered prompt.
	ResolvedByOwner PauseResolver = "owner"
	// ResolvedBySelfHealed: the operator rebased a BaseFailing MR onto a
	// green default branch.
	ResolvedBySelfHealed PauseResolver = "selfHealed"
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

	// PauseKind is the cause of the pause, for the Desk's pause card. Empty
	// for a pause from before it was recorded.
	// +optional
	PauseKind PauseKind `json:"pauseKind,omitempty"`

	// PausedFor is the active issue the pause is about. Autopilot continues
	// by itself once that issue is merged or closed by hand, and, with
	// ResumeWhenReady, once it passes the merge gate again (a later run
	// fixed it).
	// +optional
	PausedFor int64 `json:"pausedFor,omitempty"`

	// ResumeWhenReady: the pause is over PausedFor's run, review or CI.
	// +optional
	ResumeWhenReady bool `json:"resumeWhenReady,omitempty"`

	// MergeOverride is an owner's decision to merge one head of an issue
	// without a full review (the `merge` command). A new head voids it.
	// +optional
	MergeOverride *MergeOverride `json:"mergeOverride,omitempty"`

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

	// LastPilotCommand is the newest queue command from the pilot handled
	// (status.pilot.commands).
	// +optional
	LastPilotCommand int64 `json:"lastPilotCommand,omitempty"`

	// TrackingHash is the hash of the tracking issue's last rendered
	// description, so it is only edited when it changes.
	// +optional
	TrackingHash string `json:"trackingHash,omitempty"`
	// CloseWaitsFor are the open issues a done milestone was last reported
	// to wait for before autopilot closes it.
	// +optional
	CloseWaitsFor []int64 `json:"closeWaitsFor,omitempty"`

	// Budget is the milestone's budget and its spend, refreshed every poll
	// while the milestone has a budget line.
	// +optional
	Budget *BudgetStatus `json:"budget,omitempty"`

	// StateHash is a hash of the queue's state (items, CI states, MR
	// heads, main health, phase and pause reason); StateChangedAt is when
	// it last changed. A pause it has not changed in for pilot.stuckAfter
	// is assessed by the pilot.
	// +optional
	StateHash string `json:"stateHash,omitempty"`
	// +optional
	StateChangedAt *metav1.Time `json:"stateChangedAt,omitempty"`

	// PausedAt is when the current pause started; unset while running.
	// +optional
	PausedAt *metav1.Time `json:"pausedAt,omitempty"`

	// ResumedBy is who ended the last pause; cleared by the next pause.
	// +optional
	ResumedBy PauseResolver `json:"resumedBy,omitempty"`
}

// BudgetStatus is the budget of an autopilot milestone and what it spent,
// from the history database (agent_calls).
type BudgetStatus struct {
	// LimitUSD is the budget (spec.budgetUSD).
	LimitUSD string `json:"limitUSD"`
	// SpentUSD is the milestone's spend; empty while it cannot be read
	// (no database, or the query failed).
	// +optional
	SpentUSD string `json:"spentUSD,omitempty"`
	// WarnedAt is when the owners were told that 80 % of the budget is
	// spent.
	// +optional
	WarnedAt *metav1.Time `json:"warnedAt,omitempty"`
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

	// HealedOn is the commit of the green default branch the operator last
	// rebased the MR onto because its CI failed with the default branch
	// (BaseFailing); once per such commit.
	// +optional
	HealedOn string `json:"healedOn,omitempty"`

	// MarkedReady is set once the operator marked the draft MR ready, or
	// found it ready, to run CI. A draft again after that is a human holding
	// it, or a run that left problems.
	// +optional
	MarkedReady bool `json:"markedReady,omitempty"`

	// Decisions recorded on the issue, collected when it was merged: the
	// newest 20; the full history is in the history database.
	// +optional
	Decisions []DecisionRef `json:"decisions,omitempty"`

	// RevisitOf is set for follow-up issues the operator created to revisit
	// a decision of a merged issue: the decision's ID.
	// +optional
	RevisitOf string `json:"revisitOf,omitempty"`

	// FlakyFix is set for a flaky job's issue handed to the agent
	// (fixFlaky, mainHealth.autoFixFlaky): not in the plan, it stays on a
	// re-plan.
	// +optional
	FlakyFix bool `json:"flakyFix,omitempty"`

	// StartAnyway is set by an owner's startAnyway: a skipped dependency
	// counts as met for this item. Pending dependencies and issue links
	// still hold it back.
	// +optional
	StartAnyway bool `json:"startAnyway,omitempty"`

	// Budget is the issue's budget (its implement task's spec.budgetUSD,
	// else the project's budget.perIssueUSD) and its spend, refreshed every
	// poll while the issue has one; warnedAt is unused.
	// +optional
	Budget *BudgetStatus `json:"budget,omitempty"`
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
	// LastPilotCommand is the newest revisit from the pilot handled
	// (status.pilot.commands).
	// +optional
	LastPilotCommand int64 `json:"lastPilotCommand,omitempty"`
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
