package v1alpha1

import (
	"time"

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

	// QueuedBehind are the iids of the older autopilot milestones that are
	// still open, ascending (spec tasks of autopilot milestones). Set while
	// the task waits for its turn with runRequest 0; empty once its first
	// run is requested.
	// +optional
	QueuedBehind []int64 `json:"queuedBehind,omitempty"`

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

	// MaxParallel is the milestone's parallel-issue limit, from its
	// description's line "/agent parallel <n>" (spec tasks). 0 means use
	// the project's autopilot.maxParallel.
	// +optional
	MaxParallel int32 `json:"maxParallel,omitempty"`

	// FollowUpOf names what this milestone took over from another one, from
	// its description's line "/agent followup-of <iid> <numbers>" (spec
	// tasks). Nil without the line or for a malformed one.
	// +optional
	FollowUpOf *FollowUpOf `json:"followUpOf,omitempty"`

	// BudgetStop is set by the operator on implement tasks while autopilot
	// is paused for their budget (the issue's, or the milestone's for all
	// of its implement tasks). The runner starts no further agent call: a
	// run in progress ends deferred after its current call, a pending one
	// waits until the flag is cleared. Desk chat turns still run.
	// +optional
	BudgetStop bool `json:"budgetStop,omitempty"`

	// WarmUntil is written by the operator: the pod is kept up, or
	// started, until this time, also without a run or chat turn. It starts
	// no run; an idle pod costs no tokens. Suspend wins.
	// +optional
	WarmUntil *metav1.Time `json:"warmUntil,omitempty"`

	// WarmRequestedAt is written by the Desk: it keeps the pod up, or
	// starts it, for lifecycle.chatIdleTimeout from this time, like
	// WarmUntil.
	// +optional
	WarmRequestedAt *metav1.Time `json:"warmRequestedAt,omitempty"`

	// Repos are the milestone's "/agent repo <project> [merge]" lines (spec
	// tasks), each with the problem that makes the operator ignore it: a
	// malformed or repeated line, or a project that is no valid companion
	// target. Only lines without a problem count.
	// +optional
	Repos []MilestoneRepo `json:"repos,omitempty"`

	// Companion is the companion repository the issue changes too
	// (Implement tasks), written by the operator from the issue's
	// agent-repo label once it counts; unset without one. The runner only
	// reads it. Changing it requests no run.
	// +optional
	Companion *CompanionSpec `json:"companion,omitempty"`

	// QueueLane is set by the operator while the task carries the fix of a
	// red default branch (urgent). Empty: the task queues by its milestone.
	// +optional
	QueueLane QueueLane `json:"queueLane,omitempty"`

	// Source records which GitLab state the operator has already turned into
	// run requests, so that every change triggers exactly one run.
	// +optional
	Source SourceState `json:"source,omitempty"`
}

// MilestoneRepo is one "/agent repo" line of a milestone.
type MilestoneRepo struct {
	// Project is the companion's full path, normalized; empty for a
	// malformed line.
	// +optional
	Project string `json:"project,omitempty"`
	// Merge: the line ends in "merge". Autopilot merges the companion's
	// merge request only with it, on an autopilot milestone, and when the
	// target has autoMerge.
	// +optional
	Merge bool `json:"merge,omitempty"`
	// Problem says why the line is ignored; empty for a line that counts.
	// +optional
	Problem string `json:"problem,omitempty"`
	// Line is the line as written, for a line with a problem.
	// +optional
	Line string `json:"line,omitempty"`
}

// CompanionSpec is the companion repository an implement task changes in
// the same run as its home project (docs/specs/cross-repo-changes.spec.md).
type CompanionSpec struct {
	// Project is the companion's full path, as spec.crossRepo.targets
	// names it.
	Project string `json:"project"`
	// Paths is the target's allow-list of the paths the change may touch.
	Paths []string `json:"paths"`
	// Branch is the work branch in the companion project,
	// "agent/<home-slug>-<iid>-<slug>".
	Branch string `json:"branch"`
	// AutoMerge: autopilot may merge the companion's merge request. Only
	// when the milestone line has "merge", the milestone is on autopilot
	// and the target has autoMerge.
	// +optional
	AutoMerge bool `json:"autoMerge,omitempty"`
}

// CompanionRebase is a rebase of the companion's merge request the
// autopilot asked GitLab for.
type CompanionRebase struct {
	// From is the companion head that was rebased.
	From string `json:"from"`
	// Diff is the hash of the companion's change at From: the review holds
	// on the rebased head if its change hashes the same.
	// +optional
	Diff string `json:"diff,omitempty"`
	// At is when the rebase was asked for.
	// +optional
	At *metav1.Time `json:"at,omitempty"`
}

// MilestoneRef identifies a GitLab milestone.
type MilestoneRef struct {
	ID    int64  `json:"id"`
	IID   int64  `json:"iid"`
	Title string `json:"title"`
}

// FollowUpOf names follow-ups of a milestone's retrospective that another
// milestone took over.
type FollowUpOf struct {
	// MilestoneIID is the milestone whose retrospective proposed them.
	MilestoneIID int64 `json:"milestoneIID"`
	// Numbers are the follow-ups' numbers (1..9), deduplicated and sorted.
	Numbers []int `json:"numbers"`
}

// FollowUpOfStatus is what a milestone's line "/agent followup-of" took
// over.
type FollowUpOfStatus struct {
	// MilestoneIID is the milestone the line named.
	MilestoneIID int64 `json:"milestoneIID"`
	// Numbers are the follow-ups taken over, sorted: each was in the
	// retrospective and not opened, dismissed or taken by another
	// milestone.
	// +optional
	Numbers []int `json:"numbers,omitempty"`
	// Problem says what the line named that was not taken over: an unknown
	// milestone or number, or a follow-up settled otherwise. Empty when
	// all was.
	// +optional
	Problem string `json:"problem,omitempty"`
	// Noticed is set once Problem was posted on a tracking issue; another
	// problem is posted again.
	// +optional
	Noticed bool `json:"noticed,omitempty"`
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

	// CIFixProject is the project of CIFixPipelineID: the companion's full
	// path when its pipeline failed, empty for the home project.
	// +optional
	CIFixProject string `json:"ciFixProject,omitempty"`

	// CompanionRebase is the autopilot's rebase of the companion's merge
	// request, asked once per companion head (From).
	// +optional
	CompanionRebase *CompanionRebase `json:"companionRebase,omitempty"`

	// MergeBaseFor is the merge request head the operator saw conflicting
	// with the default branch. The next run merges the default branch into
	// the work branch and has the agent resolve the conflicts; it is asked
	// once per head.
	// +optional
	MergeBaseFor string `json:"mergeBaseFor,omitempty"`

	// ReviewFor is a merge request head to review again without the
	// implementer (after its reviewers failed, or "/agent review"). With a
	// companion it names both heads, "<home>+<companion>" (ReviewHeads). It is
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

	// CompanionNotice is the agent-repo label the operator told the issue
	// it ignores (Implement tasks), so it is told once per label. Cleared
	// when the label goes or counts.
	// +optional
	CompanionNotice string `json:"companionNotice,omitempty"`

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

	// FollowUpDraft is an owner's request for a draft of a follow-up
	// milestone from the retrospective's follow-ups (Coordinate tasks, from
	// the Desk command "draft"). The run of its request writes
	// status.pilot.followUpDraft.
	// +optional
	FollowUpDraft *FollowUpDraftRequest `json:"followUpDraft,omitempty"`

	// PilotInstruction is an instruction an owner accepted from the pilot
	// (Implement tasks), for the run of its request.
	// +optional
	PilotInstruction *PilotInstruction `json:"pilotInstruction,omitempty"`

	// OwnerMessages are an owner's words for the running implementer, passed
	// on by the pilot; the runner reports each one in
	// status.run.deliveredMessages. The last MaxOwnerMessages are kept.
	// +kubebuilder:validation:MaxItems=20
	// +optional
	OwnerMessages []OwnerMessage `json:"ownerMessages,omitempty"`
}

// FollowUpDraftRequest asks the pilot for a draft of a follow-up milestone.
type FollowUpDraftRequest struct {
	// Request is the run request that writes the draft.
	Request int64 `json:"request"`
	// Numbers are the retrospective's follow-ups to draft from (1..9),
	// deduplicated and sorted.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=9
	Numbers []int `json:"numbers"`
	// By is the owner who asked.
	By string `json:"by"`
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

	// Experiment is the issue's experiment arm, assigned at its first
	// implement run and kept for every later run (implement tasks of a
	// project with agents.implement.experiment).
	// +optional
	Experiment *ExperimentAssignment `json:"experiment,omitempty"`

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

	// FollowUpOf is what the operator made of Spec.FollowUpOf (spec
	// tasks): the follow-ups this milestone took over.
	// +optional
	FollowUpOf *FollowUpOfStatus `json:"followUpOf,omitempty"`

	// Autopilot is the operator's queue of an autopilot milestone (spec
	// tasks).
	// +optional
	Autopilot *AutopilotStatus `json:"autopilot,omitempty"`

	// Revisits tracks the "/agent revisit" comments the operator handled on
	// closed issues of the milestone and on its tracking issue (spec tasks).
	// +optional
	Revisits *RevisitStatus `json:"revisits,omitempty"`

	// Decisions is the watermark of the operator's copy of the milestone's
	// decisions into the history database (spec tasks).
	// +optional
	Decisions *DecisionSyncStatus `json:"decisions,omitempty"`

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

// ConditionStalled is set on a Running task when the operator's stall check
// finds silence or a stuck pod (see lifecycle.stallAfter). Stalled=False
// with reason Active once a check finds a recent log line; the condition is
// removed when no run is in progress.
const ConditionStalled = "Stalled"

// Stall condition reasons.
const (
	StallReasonSilent     = "Silent"
	StallReasonPodPending = "PodPending"
	StallReasonCrashLoop  = "CrashLoop"
	StallReasonActive     = "Active"
)

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

	// CurrentStepSince is when the runner started currentStep. Empty with it.
	// +optional
	CurrentStepSince *metav1.Time `json:"currentStepSince,omitempty"`

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

	// NeedsInputCause is why the issue got the needs-input label, written
	// before the label. Empty once a run or CI loop ends without it, and on
	// tasks of an older release.
	// +optional
	NeedsInputCause NeedsInputCause `json:"needsInputCause,omitempty"`

	// NeedsInputURL is the comment NeedsInputCause links to: the failure
	// note on the issue (runFailed) or the report on the merge request
	// (agentQuestion, nothingPushed). ciRed leaves it empty and links to
	// status.ci.pipelineURL.
	// +optional
	NeedsInputURL string `json:"needsInputURL,omitempty"`

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

	// DeliveredMessages says where each owner message went.
	// +kubebuilder:validation:MaxItems=20
	// +optional
	DeliveredMessages []DeliveredMessage `json:"deliveredMessages,omitempty"`

	// PushedSHA is the commit the runner last pushed to the agent's branch.
	// +optional
	PushedSHA string `json:"pushedSHA,omitempty"`

	// PushedAt is when PushedSHA was pushed.
	// +optional
	PushedAt *metav1.Time `json:"pushedAt,omitempty"`

	// CommitsAhead is the number of commits on the agent's branch beyond
	// origin/<base> when the last run ended, the empty start commit and
	// checkpoints included.
	// +optional
	CommitsAhead int32 `json:"commitsAhead,omitempty"`

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

	// UncommittedLines are the lines (added + deleted) a failed run left
	// uncommitted in the workspace, not saved on the branch: a retry
	// discards them. Each run's end sets it again.
	// +optional
	UncommittedLines int64 `json:"uncommittedLines,omitempty"`

	// RunCheckpoints are the task's pushed checkpoints that no agent call
	// carries (a budget stop, or a lost pod or OOM kill the next pod saved
	// the work of), newest last; AppendRunCheckpoint keeps the last
	// MaxRunCheckpoints. Unlike Checkpoint they stay after a fold: the
	// operator writes each one it has not seen to its history, for the
	// checkpoint counts of the Spend tab and the Task page.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=20
	RunCheckpoints []RunCheckpoint `json:"runCheckpoints,omitempty"`

	// Companion is the companion repository's side of the run
	// (spec.companion): its branch, last push and draft merge request. Set
	// by the run that first pushes a companion change, cleared (and the
	// merge request closed) by a run that ends without one.
	// +optional
	Companion *CompanionRun `json:"companion,omitempty"`

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
	// CallKindFollowUpDraft is the pilot's draft of a follow-up milestone.
	CallKindFollowUpDraft = "followUpDraft"
	CallKindChat          = "chat"
	CallKindResearch      = "research"
	CallKindPlan          = "plan"
)

// Sources of an agent call's cost (AgentCall.CostSource).
const (
	CostReported  = "reported"
	CostEstimated = "estimated"
	CostUnknown   = "unknown"
)

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

// WarmUntil returns until when the pod is kept warm: the later of
// spec.warmUntil and spec.warmRequestedAt plus chatIdleTimeout. Zero
// without either.
func (t *AgentTask) WarmUntil(chatIdleTimeout time.Duration) time.Time {
	var until time.Time
	if w := t.Spec.WarmUntil; w != nil {
		until = w.Time
	}
	if r := t.Spec.WarmRequestedAt; r != nil && r.Add(chatIdleTimeout).After(until) {
		until = r.Add(chatIdleTimeout)
	}
	return until
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
