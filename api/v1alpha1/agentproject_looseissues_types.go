package v1alpha1

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
}

// Parallel is MaxParallel, or 1 when unset (0), for objects that did not pass
// the API server's defaulting: tests and AgentProjectSet merges.
func (l LooseIssuesSpec) Parallel() int32 {
	if l.MaxParallel <= 0 {
		return 1
	}
	return l.MaxParallel
}
