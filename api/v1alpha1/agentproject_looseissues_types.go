package v1alpha1

import "time"

// LooseIssuesSpec configures the loose issues queue: a second autopilot lane
// for open issues without a milestone, which owners pick. It is one
// AgentTask of type Queue per AgentProject.
type LooseIssuesSpec struct {
	// Enabled turns the queue on.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Owners are the GitLab usernames the queue mentions and who may command
	// it. Empty: the controller falls back to spec.notify.users.
	// +optional
	Owners []string `json:"owners,omitempty"`

	// OwnerSubjects are the Desk logins (OIDC username claim) that may take
	// issues and answer questions. There is no fallback, as with milestones.
	// +optional
	OwnerSubjects []string `json:"ownerSubjects,omitempty"`

	// MaxParallel is how many queue issues may run at the same time.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	// +optional
	MaxParallel int32 `json:"maxParallel,omitempty"`

	// Pilot configures the rounds of the queue's pilot.
	// +kubebuilder:default={}
	// +optional
	Pilot LooseIssuesPilotSpec `json:"pilot,omitempty"`
}

// Defaults of LooseIssuesPilotSpec, for objects that skipped API defaulting.
const (
	defaultRoundIssues = 5
	defaultRoundHours  = 24
)

// LooseIssuesPilotSpec configures the rounds of the loose issues queue's
// pilot. A round ends at the first limit reached, and the next one starts
// with a fresh session. Both limits 0: one endless session.
type LooseIssuesPilotSpec struct {
	// RoundIssues is how many finished queue issues (merged or left) end a
	// round. 0 switches this limit off.
	// +kubebuilder:default=5
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=50
	// +optional
	RoundIssues *int32 `json:"roundIssues,omitempty"`

	// RoundHours is how many hours after its start a round ends. 0 switches
	// this limit off.
	// +kubebuilder:default=24
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=168
	// +optional
	RoundHours *int32 `json:"roundHours,omitempty"`
}

// Issues is RoundIssues, or 5 when unset (nil); 0 means no limit.
func (p LooseIssuesPilotSpec) Issues() int32 {
	if p.RoundIssues == nil {
		return defaultRoundIssues
	}
	return *p.RoundIssues
}

// Duration is RoundHours as a duration, or 24h when unset (nil); 0 means no
// limit.
func (p LooseIssuesPilotSpec) Duration() time.Duration {
	h := int32(defaultRoundHours)
	if p.RoundHours != nil {
		h = *p.RoundHours
	}
	return time.Duration(h) * time.Hour
}

// Parallel is MaxParallel, or 1 when unset (0), for objects that did not pass
// the API server's defaulting: tests and AgentProjectSet merges.
func (l LooseIssuesSpec) Parallel() int32 {
	if l.MaxParallel <= 0 {
		return 1
	}
	return l.MaxParallel
}
