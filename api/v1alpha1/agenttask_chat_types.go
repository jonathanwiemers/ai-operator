package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	// Decisions are the owner's answers to a plan's decisions, sent with
	// this message from the Desk. Plan messages only.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=30
	Decisions []PlanDecisionAnswer `json:"decisions,omitempty"`
}

// PlanDecisionAnswer is an owner's answer to one plan decision at the
// version the owner saw: keep it, pick option Option or answer Other.
// Exactly one of Keep, Option and Other is set.
type PlanDecisionAnswer struct {
	// ID is the decision's ID: D1, D2, …
	// +kubebuilder:validation:MaxLength=16
	ID string `json:"id"`
	// Version is the decision's version the owner answered.
	// +kubebuilder:validation:Minimum=1
	Version int64 `json:"version"`
	// Keep signs the version off as it is.
	// +optional
	Keep bool `json:"keep,omitempty"`
	// Option is the 0-based index of the option the owner picked.
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4
	Option *int32 `json:"option,omitempty"`
	// Other is the owner's own answer.
	// +optional
	// +kubebuilder:validation:MaxLength=2000
	Other string `json:"other,omitempty"`
}
