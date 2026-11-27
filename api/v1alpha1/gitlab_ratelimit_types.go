package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// FailureKind is the recorded outside cause of a failed run.
// +kubebuilder:validation:Enum=gitlab
type FailureKind string

// FailureGitLab: the run failed on a transient GitLab error (429 or 5xx).
const FailureGitLab FailureKind = "gitlab"

// GitLabRateLimitReading is one reading of GitLab's RateLimit-* response
// headers for the bot token.
type GitLabRateLimitReading struct {
	// Observed is RateLimit-Observed: the requests counted in the window.
	// +optional
	Observed int32 `json:"observed,omitempty"`
	// Remaining is RateLimit-Remaining: the requests left in the window.
	// +optional
	Remaining int32 `json:"remaining,omitempty"`
	// Limit is RateLimit-Limit: the requests allowed per window.
	// +optional
	Limit int32 `json:"limit,omitempty"`
	// ResetAt is RateLimit-Reset: when the window starts again.
	// +optional
	ResetAt *metav1.Time `json:"resetAt,omitempty"`
	// ReadAt is when the headers were read.
	// +optional
	ReadAt *metav1.Time `json:"readAt,omitempty"`
}

// GitLabRateLimitStatus is a project's view of the bot token's GitLab rate
// limit: the latest reading, the limit that defers runs and the peak.
type GitLabRateLimitStatus struct {
	GitLabRateLimitReading `json:",inline" tstype:",extends"`
	// LimitedUntil is set by a run deferred on a GitLab limit: runs of the
	// project that would start before then defer to it without calling
	// GitLab.
	// +optional
	LimitedUntil *metav1.Time `json:"limitedUntil,omitempty"`
	// PeakObserved is the highest Observed count seen recently.
	// +optional
	PeakObserved int32 `json:"peakObserved,omitempty"`
	// PeakAt is when PeakObserved was read.
	// +optional
	PeakAt *metav1.Time `json:"peakAt,omitempty"`
}

// GitLabProjectStatus is the state of a project's GitLab API use.
type GitLabProjectStatus struct {
	// RateLimit is the bot token's rate limit.
	// +optional
	RateLimit *GitLabRateLimitStatus `json:"rateLimit,omitempty"`
}
