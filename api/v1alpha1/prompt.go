package v1alpha1

import (
	"errors"
	"fmt"
	"regexp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PromptKind is what a prompt asks about.
// +kubebuilder:validation:Enum=assessment;mainHealth;needsInput;revisit;budget;proposal
type PromptKind string

const (
	// PromptAssessment: the pilot assessed an event (stuck, review rounds,
	// infra).
	PromptAssessment PromptKind = "assessment"
	// PromptMainHealth: the default branch is red or flaky.
	PromptMainHealth PromptKind = "mainHealth"
	// PromptNeedsInput: the agent asked a question (Implement tasks).
	PromptNeedsInput PromptKind = "needsInput"
	// PromptRevisit: an owner revisits a decision.
	PromptRevisit PromptKind = "revisit"
	// PromptBudget: the milestone's budget is nearly or fully spent.
	PromptBudget PromptKind = "budget"
	// PromptProposal: a proposal of the pilot.
	PromptProposal PromptKind = "proposal"
)

// PromptState is where a prompt is in its life.
// +kubebuilder:validation:Enum=open;answered;superseded;expired
type PromptState string

const (
	PromptOpen     PromptState = "open"
	PromptAnswered PromptState = "answered"
	// PromptSuperseded: its source resolved, or a newer prompt has the same
	// Kind and Key.
	PromptSuperseded PromptState = "superseded"
	// PromptExpired: a proposal prompt nobody answered in time.
	PromptExpired PromptState = "expired"
)

// Closed reports whether the prompt can no longer be answered.
func (s PromptState) Closed() bool {
	return s == PromptAnswered || s == PromptSuperseded || s == PromptExpired
}

// Bounds of a prompt; the status keeps the newest MaxPrompts.
const (
	MaxPrompts            = 20
	MaxPromptQuestion     = 1000
	MaxPromptContext      = 8 << 10
	MaxPromptOptionLabel  = 200
	MaxPromptOptionDetail = 1000
	// MaxPromptText bounds an answer's free text and outcome.
	MaxPromptText = 4000
)

// maxPromptOptions bounds the producer's options; one option made from an
// owner's free text may follow them.
const maxPromptOptions = 4

// PromptSource is what a prompt is about and who made it.
type PromptSource struct {
	// +optional
	Kind PromptKind `json:"kind,omitempty"`
	// +optional
	IssueIID int64 `json:"issueIID,omitempty"`
	// +optional
	MRIID int64 `json:"mrIID,omitempty"`
	// Task is the AgentTask it is about, if any.
	// +optional
	Task string `json:"task,omitempty"`
	// Key is the producer's dedupe key (event key, proposal source,
	// decision id): a newer prompt with the same Kind and Key supersedes an
	// older one.
	// +optional
	Key string `json:"key,omitempty"`
	// RunRequest is the run request of Task the question came from
	// (needsInput): a newer one supersedes it.
	// +optional
	RunRequest int64 `json:"runRequest,omitempty"`
}

// PromptOption is one answer an owner can choose.
type PromptOption struct {
	// ID is "A".."E", in order.
	// +optional
	ID string `json:"id,omitempty"`
	// +optional
	Label string `json:"label,omitempty"`
	// Detail says what the option does. Untrusted.
	// +optional
	Detail string `json:"detail,omitempty"`
	// Actions run when an owner chooses it, validated like an accepted
	// proposal's; none: the "do nothing" option.
	// +optional
	Actions []PilotAction `json:"actions,omitempty"`
	// +optional
	Recommended bool `json:"recommended,omitempty"`
	// FromAnswer: the pilot made it from an owner's free text; it follows
	// the producer's options and waits for the owner to confirm it.
	// +optional
	FromAnswer bool `json:"fromAnswer,omitempty"`
}

// PromptAnswer is an owner's answer to a prompt.
type PromptAnswer struct {
	// OptionID is the chosen option, empty for a free-text answer.
	// +optional
	OptionID string `json:"optionID,omitempty"`
	// Text is the owner's free text. Untrusted.
	// +optional
	Text string `json:"text,omitempty"`
	// By is the owner who answered.
	By string      `json:"by"`
	At metav1.Time `json:"at"`
	// Via is where the answer came from.
	// +kubebuilder:validation:Enum=desk;gitlab
	// +optional
	Via string `json:"via,omitempty"`
	// Outcome is the operator's markdown of what the chosen actions did,
	// set after they ran.
	// +optional
	Outcome string `json:"outcome,omitempty"`
}

// Prompt is one question to the owners: a question, options (one
// recommended) and an optional free-text box.
type Prompt struct {
	// ID is "Q<n>", counted per milestone (or task).
	// +optional
	ID string `json:"id,omitempty"`
	// +optional
	Source PromptSource `json:"source,omitempty"`
	// +optional
	Question string `json:"question,omitempty"`
	// Context is markdown evidence for the question. Untrusted.
	// +optional
	Context string `json:"context,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=5
	Options []PromptOption `json:"options,omitempty"`
	// +optional
	AllowFreeText bool `json:"allowFreeText,omitempty"`
	// Quiet: the producer mentioned the owners itself, so the GitLab
	// comment that mirrors the prompt does not.
	// +optional
	Quiet bool `json:"quiet,omitempty"`
	// +optional
	State PromptState `json:"state,omitempty"`
	// +optional
	Answer *PromptAnswer `json:"answer,omitempty"`
	// +optional
	PostedAt *metav1.Time `json:"postedAt,omitempty"`
	// +optional
	ClosedAt *metav1.Time `json:"closedAt,omitempty"`
	// PostedNoteID, PostedNoteURL and MirroredState are the GitLab comment
	// that mirrors the prompt and the state it last showed.
	// +optional
	PostedNoteID int64 `json:"postedNoteID,omitempty"`
	// +optional
	PostedNoteURL string `json:"postedNoteURL,omitempty"`
	// +optional
	MirroredState PromptState `json:"mirroredState,omitempty"`
	// PilotState is PilotState of the milestone and the options' actions
	// when a proposal prompt was made: once it changes, the proposal is
	// superseded.
	// +optional
	PilotState string `json:"pilotState,omitempty"`
	// OwnerIDs are the user IDs of the owners when it was posted: only
	// they answer it in GitLab, whoever takes their names later. [0]: there
	// were none.
	// +optional
	OwnerIDs []int64 `json:"ownerIDs,omitempty"`
}

// MaxRefines bounds spec.source.promptRefines and status.pilot.refined.
const MaxRefines = 5

// MaxRefineFollowUp bounds the pilot's follow-up question, in bytes.
const MaxRefineFollowUp = 500

// PromptRefine is an owner's free text on an action prompt, handed to the
// pilot with the prompt: the pilot makes one option of it or asks one
// follow-up question. Written by the operator.
type PromptRefine struct {
	// Prompt is the prompt's id ("Q3").
	Prompt string `json:"prompt"`
	// Question, Context and Options are the prompt's when it was answered.
	// Untrusted.
	// +optional
	Question string `json:"question,omitempty"`
	// +optional
	Context string `json:"context,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=5
	Options []PromptOption `json:"options,omitempty"`
	// Text is the owner's free text. Untrusted.
	// +kubebuilder:validation:MaxLength=4000
	Text string `json:"text"`
	// By is the owner who wrote it.
	// +optional
	By string `json:"by,omitempty"`
	// Request is the run request that refines it.
	Request int64 `json:"request"`
	// MessageID is the Desk chat message of the answer, 0 for another.
	// +optional
	MessageID int64 `json:"messageID,omitempty"`
}

// PromptRefinement is the pilot's answer to a PromptRefine: exactly one of
// Option, FollowUp and Error. Written by the runner.
type PromptRefinement struct {
	Prompt string `json:"prompt"`
	// +optional
	MessageID int64 `json:"messageID,omitempty"`
	// Request is the PromptRefine's.
	// +optional
	Request int64 `json:"request,omitempty"`
	// Option is the option the pilot made of the text (FromAnswer), its
	// actions cut to the operator's bounds; the operator validates them
	// before the owner confirms it. Untrusted.
	// +optional
	Option *PromptOption `json:"option,omitempty"`
	// FollowUp is the pilot's question back to the owner, at most
	// MaxRefineFollowUp bytes. Untrusted.
	// +optional
	FollowUp string `json:"followUp,omitempty"`
	// Error says why there is neither.
	// +optional
	Error string `json:"error,omitempty"`
}

var promptID = regexp.MustCompile(`^Q[1-9][0-9]*$`)

// Validate checks the prompt's shape. Whether its actions apply to the
// milestone is checked by the operator.
func (p Prompt) Validate() error {
	switch {
	case !promptID.MatchString(p.ID):
		return fmt.Errorf("prompt id %q is not Q<n>", p.ID)
	case p.Question == "":
		return errors.New("prompt has no question")
	case len(p.Question) > MaxPromptQuestion:
		return fmt.Errorf("question is longer than %d bytes", MaxPromptQuestion)
	case len(p.Context) > MaxPromptContext:
		return fmt.Errorf("context is longer than %d bytes", MaxPromptContext)
	}
	switch p.Source.Kind {
	case PromptAssessment, PromptMainHealth, PromptNeedsInput, PromptRevisit, PromptBudget, PromptProposal:
	default:
		return fmt.Errorf("prompt kind %q is unknown", p.Source.Kind)
	}
	switch p.State {
	case "", PromptOpen, PromptAnswered, PromptSuperseded, PromptExpired:
	default:
		return fmt.Errorf("prompt state %q is unknown", p.State)
	}
	var producer, fromAnswer, recommended int
	acting, doNothing := false, false
	for i, o := range p.Options {
		switch want := string(rune('A' + i)); {
		case o.ID != want:
			return fmt.Errorf("option %d has id %q, want %s", i+1, o.ID, want)
		case o.Label == "":
			return fmt.Errorf("option %s has no label", o.ID)
		case len(o.Label) > MaxPromptOptionLabel:
			return fmt.Errorf("option %s: label is longer than %d bytes", o.ID, MaxPromptOptionLabel)
		case len(o.Detail) > MaxPromptOptionDetail:
			return fmt.Errorf("option %s: detail is longer than %d bytes", o.ID, MaxPromptOptionDetail)
		case fromAnswer > 0:
			return fmt.Errorf("option %s follows the option from the owner's answer", o.ID)
		case o.FromAnswer && len(o.Actions) == 0:
			return fmt.Errorf("option %s from the owner's answer has no actions", o.ID)
		}
		for _, a := range o.Actions {
			if a.Tool == "" {
				return fmt.Errorf("option %s has an action without a tool", o.ID)
			}
		}
		if o.FromAnswer {
			fromAnswer++
		} else {
			producer++
		}
		if o.Recommended {
			recommended++
		}
		acting = acting || len(o.Actions) > 0
		doNothing = doNothing || len(o.Actions) == 0
	}
	switch {
	case producer > maxPromptOptions:
		return fmt.Errorf("prompt has %d options, at most %d", producer, maxPromptOptions)
	case p.Source.Kind == PromptNeedsInput && len(p.Options) == 0 && !p.AllowFreeText:
		return errors.New("a question without options needs free text")
	case p.Source.Kind == PromptNeedsInput && len(p.Options) == 0:
	case producer < 2:
		return fmt.Errorf("prompt has %d options, at least 2", producer)
	case recommended != 1:
		return fmt.Errorf("prompt has %d recommended options, want 1", recommended)
	case p.Source.Kind != PromptNeedsInput && acting && !doNothing:
		return errors.New(`prompt has no "do nothing" option`)
	}
	if a := p.Answer; a != nil {
		switch {
		case a.OptionID != "" && p.Option(a.OptionID) == nil:
			return fmt.Errorf("answer names option %q, which the prompt does not have", a.OptionID)
		case len(a.Text) > MaxPromptText:
			return fmt.Errorf("answer text is longer than %d bytes", MaxPromptText)
		case len(a.Outcome) > MaxPromptText:
			return fmt.Errorf("answer outcome is longer than %d bytes", MaxPromptText)
		}
	}
	return nil
}

// Option is the option with the id, nil if there is none.
func (p Prompt) Option(id string) *PromptOption {
	for i := range p.Options {
		if p.Options[i].ID == id {
			return &p.Options[i]
		}
	}
	return nil
}

// DoNothing is the first option without actions, nil if there is none.
func (p Prompt) DoNothing() *PromptOption {
	for i := range p.Options {
		if len(p.Options[i].Actions) == 0 {
			return &p.Options[i]
		}
	}
	return nil
}
