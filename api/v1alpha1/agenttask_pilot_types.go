package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
	// tell #N: …", or quoted from their Desk chat message). Only the
	// operator and a chat turn set them; the operator runs a chat turn's
	// only when the text stands in the owner's message as written.
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
	// ChatMessageID is the owner's chat message that asked for it; the
	// operator checks the message and its author before it runs.
	// +optional
	ChatMessageID int64 `json:"chatMessageID,omitempty"`
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

	// LastChatAccepted is the newest chat message whose request ran as its
	// owner: a message's request runs once, never one older than it.
	// +optional
	LastChatAccepted int64 `json:"lastChatAccepted,omitempty"`

	// OpenedIssues are the title keys of the issues the pilot opened.
	// +optional
	OpenedIssues []string `json:"openedIssues,omitempty"`

	// Commands are the queue commands of accepted proposals (spec tasks),
	// oldest first, for autopilot and the revisits to apply once each.
	// +optional
	Commands []PilotCommand `json:"commands,omitempty"`

	// ReleaseDecisions are the release decisions of the pilot's answers and
	// assessments (Coordinate tasks), oldest first, the newest
	// MaxPilotReleaseDecisions. The operator validates and applies each once,
	// by Seq. Untrusted.
	// +optional
	// +kubebuilder:validation:MaxItems=20
	ReleaseDecisions []PilotReleaseDecision `json:"releaseDecisions,omitempty"`

	// AutonomyOverride is the owners' last "/pilot stop" or "/pilot go"
	// (Coordinate tasks). It can only lower the project's pilot.autonomy.
	// +optional
	AutonomyOverride *PilotAutonomyOverride `json:"autonomyOverride,omitempty"`

	// Interventions count the pilot's own actions per issue on the issue's
	// current head (Coordinate tasks), by issue IID; "0" is the milestone.
	// +optional
	Interventions map[string]PilotIntervention `json:"interventions,omitempty"`

	// Acted are the proposals runAccepted ran without a prompt (Coordinate
	// tasks): the pilot's own actions and the proposals owners accepted.
	// Oldest first, the newest MaxPilotActed.
	// +optional
	// +kubebuilder:validation:MaxItems=30
	Acted []PilotActed `json:"acted,omitempty"`

	// Retrospective is the retrospective of the milestone once its autopilot
	// is done: recorded by the pilot's run (Coordinate tasks) and copied to
	// the spec task before the pilot is deleted. A milestone gets one.
	// +optional
	Retrospective *PilotRetrospective `json:"retrospective,omitempty"`

	// FollowUpDraft is the pilot's draft of a follow-up milestone for
	// spec.source.followUpDraft (Coordinate tasks), the newest only.
	// Untrusted until an owner confirms it.
	// +optional
	FollowUpDraft *PilotFollowUpDraft `json:"followUpDraft,omitempty"`

	// RoundNote is the queue's pilot's answer to spec.source.roundNote
	// (Coordinate tasks with queueTask), the newest only.
	// +optional
	RoundNote *PilotRoundNote `json:"roundNote,omitempty"`
}

// MaxRoundNote bounds a round note of the loose issues queue's pilot, in
// characters, shared by the runner and the operator.
const MaxRoundNote = 1500

// PilotRoundNote is the note the queue's pilot wrote at the end of a round.
type PilotRoundNote struct {
	// Request is the run request (spec.source.roundNote) it answers.
	Request int64 `json:"request"`
	// Note is the round note. Untrusted.
	// +kubebuilder:validation:MaxLength=1500
	// +optional
	Note string `json:"note,omitempty"`
	// Failed is why there is no note.
	// +optional
	Failed string `json:"failed,omitempty"`
}

// PilotFollowUpDraft is the pilot's draft of a follow-up milestone, or why
// there is none.
type PilotFollowUpDraft struct {
	// Request is the run request it answers (spec.source.followUpDraft).
	Request int64 `json:"request"`
	// Numbers are the follow-ups it drafts from.
	// +optional
	Numbers []int `json:"numbers,omitempty"`
	// Title is the milestone's title, one line of at most MaxDraftTitle
	// bytes. Untrusted.
	// +optional
	Title string `json:"title,omitempty"`
	// Description is the milestone's description: untrusted markdown
	// without HTML, at most MaxDraftDescription bytes.
	// +optional
	Description string      `json:"description,omitempty"`
	At          metav1.Time `json:"at"`
	// Error says why there is no draft.
	// +optional
	Error string `json:"error,omitempty"`
}

// MaxDraftTitle and MaxDraftDescription bound PilotFollowUpDraft, in bytes.
const (
	MaxDraftTitle       = 200
	MaxDraftDescription = 16 << 10
)

// PilotRetrospective is the last comment of a finished autopilot milestone's
// pilot on its tracking issue.
type PilotRetrospective struct {
	// NoteID is the comment.
	// +optional
	NoteID int64 `json:"noteID,omitempty"`
	// +optional
	PostedAt *metav1.Time `json:"postedAt,omitempty"`
	// Text is the comment as posted, for the Desk: untrusted markdown
	// without HTML (comments and tags removed), at most MaxRetroText bytes,
	// truncated beyond.
	// +optional
	Text string `json:"text,omitempty"`
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
	// Taken are the follow-ups other milestones of the project took over
	// with their line "/agent followup-of", by number ("1"): the taking
	// milestone's IID. The first to take one keeps it.
	// +optional
	Taken map[string]int64 `json:"taken,omitempty"`
	// DismissedBy is the owner who dismissed the remaining follow-ups with
	// "create none", DismissedAt when: they are settled, so the milestone
	// closes. Each can still be opened.
	// +optional
	DismissedBy string `json:"dismissedBy,omitempty"`
	// +optional
	DismissedAt *metav1.Time `json:"dismissedAt,omitempty"`
	// LastNoteID is the newest comment on the tracking issue read for
	// "create 1 2" and "create none" (spec tasks).
	// +optional
	LastNoteID int64 `json:"lastNoteID,omitempty"`
}

// MaxRetroText bounds PilotRetrospective.Text, in bytes.
const MaxRetroText = 16 << 10

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

// MaxPilotReleaseDecisions bounds status.pilot.releaseDecisions.
const MaxPilotReleaseDecisions = 20

// PilotReleaseDecision is the pilot's decision to add a release after an
// issue of its milestone, or to drop a planned one. It is no proposal: the
// operator validates it and carries it out.
type PilotReleaseDecision struct {
	// Seq ascends; the operator remembers the last it applied.
	Seq int64 `json:"seq"`
	// IssueIID is the issue the release follows, as the model gave it.
	IssueIID int64 `json:"issueIID"`
	// Release true adds a release after the issue, false drops a planned
	// one.
	Release bool `json:"release"`
	// Why is the pilot's one-line reason.
	// +optional
	Why string `json:"why,omitempty"`
	// Source is the message or event whose answer decided it ("note/2000",
	// "event/<key>@<request>"): a run started again records it once.
	// +optional
	Source string      `json:"source,omitempty"`
	At     metav1.Time `json:"at"`
}

// PilotAutonomyOverride is an owner's "/pilot stop" (propose) or "/pilot
// go" (managed).
type PilotAutonomyOverride struct {
	Autonomy PilotAutonomy `json:"autonomy"`
	By       string        `json:"by"`
	At       metav1.Time   `json:"at"`
}

// MaxPilotActed bounds status.pilot.acted; the other bounds cut a
// PilotActed's untrusted text, in bytes.
const (
	MaxPilotActed        = 30
	MaxPilotActedSummary = 300
	MaxPilotActedWhy     = 1000
	MaxPilotActedLines   = 8
	MaxPilotActedLine    = 300
)

// PilotActed is one proposal runAccepted ran without a prompt.
type PilotActed struct {
	At metav1.Time `json:"at"`
	// By is the owner who accepted it, empty when the pilot acted on its
	// own.
	// +optional
	By string `json:"by,omitempty"`
	// Summary is the proposal's summary, at most MaxPilotActedSummary
	// bytes. Untrusted.
	// +optional
	Summary string `json:"summary,omitempty"`
	// Why is the proposal's reason, at most MaxPilotActedWhy bytes.
	// Untrusted.
	// +optional
	Why string `json:"why,omitempty"`
	// Lines are the per-action result lines its comment shows, each at most
	// MaxPilotActedLine bytes. Untrusted.
	// +optional
	// +kubebuilder:validation:MaxItems=8
	Lines []string `json:"lines,omitempty"`
	// Issues are the intervention keys it counted against; 0 is the
	// milestone.
	// +optional
	Issues []int64 `json:"issues,omitempty"`
	// Ran is false when none of its actions ran.
	// +optional
	Ran bool `json:"ran,omitempty"`
}

// PilotIntervention counts the pilot's own actions on an issue's head.
type PilotIntervention struct {
	// +optional
	Head  string `json:"head,omitempty"`
	Count int32  `json:"count"`
}

// Limits of owner messages.
const (
	// MaxOwnerMessages bounds spec.source.ownerMessages and
	// status.run.deliveredMessages (the newest are kept).
	MaxOwnerMessages = 20
	// MaxOwnerMessageText bounds the text of an owner message, in characters.
	MaxOwnerMessageText = 4000
	// MaxMidRunDeliveries bounds the messages delivered mid-run per run.
	MaxMidRunDeliveries = 5
)

// OwnerMessage is an owner's words for a running implementer, passed on by
// the pilot.
type OwnerMessage struct {
	// ID is a stable key from the source (tell/<noteID>, chat/<messageID>),
	// so a retried reconcile never appends it twice.
	ID    string `json:"id"`
	Owner string `json:"owner"`
	// +kubebuilder:validation:MaxLength=4000
	Text string      `json:"text"`
	At   metav1.Time `json:"at"`
}

// DeliveryHow is the way an owner message reached the implementer.
// +kubebuilder:validation:Enum=stream;resume;nextRun
type DeliveryHow string

const (
	// DeliveryStream: written into the running claude call.
	DeliveryStream DeliveryHow = "stream"
	// DeliveryResume: the call stopped and resumed with the message.
	DeliveryResume DeliveryHow = "resume"
	// DeliveryNextRun: the message went into the next run's prompt.
	DeliveryNextRun DeliveryHow = "nextRun"
)

// DeliveredMessage records where an owner message went.
type DeliveredMessage struct {
	ID string `json:"id"`
	// Call is the AgentCall.Seq of the call that took the message.
	Call int64       `json:"call"`
	How  DeliveryHow `json:"how"`
	At   metav1.Time `json:"at"`
}
