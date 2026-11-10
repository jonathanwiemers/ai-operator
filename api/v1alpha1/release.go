package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// MaxMilestoneReleases bounds status.autopilot.releases.
const MaxMilestoneReleases = 50

// MaxProjectReleases bounds AgentProject status.releases.
const MaxProjectReleases = 20

// ReleaseBy is who decided a release.
// +kubebuilder:validation:Enum=planner;pilot;owner;final;companion;sweep
type ReleaseBy string

const (
	// ReleaseByPlanner: the planning run, with the issue.
	ReleaseByPlanner ReleaseBy = "planner"
	// ReleaseByPilot: the pilot, in a run it does anyway.
	ReleaseByPilot ReleaseBy = "pilot"
	// ReleaseByOwner: an owner's /agent release.
	ReleaseByOwner ReleaseBy = "owner"
	// ReleaseByFinal: the operator's release that ends a milestone.
	ReleaseByFinal ReleaseBy = "final"
	// ReleaseByCompanion: a companion waiting for a release.
	ReleaseByCompanion ReleaseBy = "companion"
	// ReleaseBySweep: the operator's release of unreleased merges outside
	// milestones.
	ReleaseBySweep ReleaseBy = "sweep"
)

// ReleaseState is where a release decision stands.
// +kubebuilder:validation:Enum=Planned;Waiting;Running;Released;AlreadyReleased;NotNeeded;NotApplicable;Dropped;Failed
type ReleaseState string

const (
	// ReleasePlanned: decided, its commit is not merged yet.
	ReleasePlanned ReleaseState = "Planned"
	// ReleaseWaiting: due, waiting (green main, interval, another release).
	ReleaseWaiting ReleaseState = "Waiting"
	// ReleaseRunning: the release pipeline runs.
	ReleaseRunning ReleaseState = "Running"
	// ReleaseReleased: the operator's pipeline released it.
	ReleaseReleased ReleaseState = "Released"
	// ReleaseAlreadyReleased: a tag already contained the commit.
	ReleaseAlreadyReleased ReleaseState = "AlreadyReleased"
	// ReleaseNotNeeded: nothing release-relevant since the last tag.
	ReleaseNotNeeded ReleaseState = "NotNeeded"
	// ReleaseNotApplicable: the project does not tag its default branch.
	ReleaseNotApplicable ReleaseState = "NotApplicable"
	// ReleaseDropped: withdrawn by the pilot or an owner.
	ReleaseDropped ReleaseState = "Dropped"
	// ReleaseFailed: the pipeline failed after its retry.
	ReleaseFailed ReleaseState = "Failed"
)

// Ended is true once the decision needs nothing more.
func (s ReleaseState) Ended() bool {
	switch s {
	case ReleaseReleased, ReleaseAlreadyReleased, ReleaseNotNeeded, ReleaseNotApplicable, ReleaseDropped, ReleaseFailed:
		return true
	}
	return false
}

// ReleaseRecord is one release decision.
type ReleaseRecord struct {
	// ID ascends within its list (the largest plus one).
	ID int64 `json:"id"`
	// By is who decided.
	By ReleaseBy `json:"by"`
	// Actor is "@alice" for an owner, empty otherwise.
	// +optional
	Actor string `json:"actor,omitempty"`
	// Why is the decider's one-line reason; untrusted for planner and pilot.
	// +optional
	Why string `json:"why,omitempty"`
	// AfterIssue is the issue the release follows.
	// +optional
	AfterIssue int64 `json:"afterIssue,omitempty"`
	// SHA is the commit the release must contain; empty while Planned.
	// +optional
	SHA string `json:"sha,omitempty"`
	// State of the decision.
	State ReleaseState `json:"state"`
	// Reason is the operator's words on why it waits, failed or was not
	// needed.
	// +optional
	Reason string `json:"reason,omitempty"`
	// Issues the release contains.
	// +optional
	Issues []int64 `json:"issues,omitempty"`
	// MergeRequests the release contains.
	// +optional
	MergeRequests []int64 `json:"mergeRequests,omitempty"`
	// Version of the release.
	// +optional
	Version string `json:"version,omitempty"`
	// ReleasedSHA is the head the release pipeline ran on.
	// +optional
	ReleasedSHA string `json:"releasedSHA,omitempty"`
	// +optional
	PipelineID int64 `json:"pipelineID,omitempty"`
	// +optional
	PipelineURL string `json:"pipelineURL,omitempty"`
	// +optional
	ReleaseURL string `json:"releaseURL,omitempty"`
	// Attempts of the release pipeline.
	// +optional
	Attempts int32 `json:"attempts,omitempty"`
	// Final: the milestone's last release.
	// +optional
	Final bool `json:"final,omitempty"`
	// DecidedAt is when the decision was made.
	DecidedAt metav1.Time `json:"decidedAt"`
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	// +optional
	EndedAt *metav1.Time `json:"endedAt,omitempty"`
}

// TrimReleases keeps at most max records: it drops the oldest ended records
// first and never one that has not ended (unless all of them have not).
func TrimReleases(recs []ReleaseRecord, max int) []ReleaseRecord {
	drop := len(recs) - max
	if drop <= 0 {
		return recs
	}
	out := make([]ReleaseRecord, 0, max)
	for _, r := range recs {
		if drop > 0 && r.State.Ended() {
			drop--
			continue
		}
		out = append(out, r)
	}
	return out
}
