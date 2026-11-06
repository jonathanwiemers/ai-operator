package v1alpha1

import (
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jonathanwiemers/ai-operator/internal/pathspec"
)

// CrossRepoSpec lets the project's issues change a companion repository in
// the same run as their home change (docs/specs/cross-repo-changes.spec.md).
// A target is used only when the cluster allows its project
// (--cross-repo-allowed-projects, the chart's crossRepo.allowedProjects);
// condition CrossRepoReady names the targets that are dropped.
type CrossRepoSpec struct {
	// Targets are the companion repositories the project's issues may
	// change.
	// +kubebuilder:validation:MinItems=1
	Targets []CrossRepoTarget `json:"targets"`
}

// CrossRepoTarget is one companion repository and what may change in it.
type CrossRepoTarget struct {
	// Project is the companion's full path, e.g. "my-group/infra/cluster".
	// It must be in the cluster's allow-list and must not be the home
	// project.
	// +kubebuilder:validation:MinLength=1
	Project string `json:"project"`

	// Paths is the gitignore-style allow-list of the paths a companion
	// change may touch: "*" and "?" within a segment, "**" for any number
	// of segments, a trailing "/" for a directory, a leading or inner "/"
	// anchors to the root. No "!" negation. A pattern must not match
	// .gitlab-ci.yml, anything under .gitlab/ or the whole tree.
	// +kubebuilder:validation:MinItems=1
	Paths []string `json:"paths"`

	// AutoMerge is the ceiling for autopilot: only with it may a milestone
	// line "/agent repo <project> merge" let autopilot merge the companion
	// MR. Otherwise the owners are asked to merge.
	// +kubebuilder:default=false
	// +optional
	AutoMerge bool `json:"autoMerge,omitempty"`

	// TokenSecretRef points to the token for the companion project.
	// Default: spec.gitlab.tokenSecretRef.
	// +optional
	TokenSecretRef *corev1.SecretKeySelector `json:"tokenSecretRef,omitempty"`
}

// ConditionCrossRepoReady is the AgentProject condition that tells whether
// every spec.crossRepo target is valid: True, reason TargetsValid, or False,
// reason InvalidTargets, naming each dropped target and why. It is absent
// without spec.crossRepo.
const ConditionCrossRepoReady = "CrossRepoReady"

// NeverTargets are the projects that are never a valid companion, even
// when the cluster allows them: their content must stay out of agent pods.
var NeverTargets = []string{"jo.wiemers/personal/kubernetes"}

// crossRepoProbes are the paths no companion pattern may match, with the
// reason a match gives: the CI configuration, and the whole tree (a
// top-level file or a file in a top-level directory).
var crossRepoProbes = []struct{ path, reason string }{
	{".gitlab-ci.yml", "matches .gitlab-ci.yml"},
	{".gitlab/x", "matches .gitlab/"},
	{"zz-probe", "matches the whole tree"},
	{"zz-probe/x", "matches the whole tree"},
}

// NormalizeProject is the form projects are compared in: GitLab paths are
// case-insensitive, without leading or trailing "/".
func NormalizeProject(p string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(p), "/"))
}

// ValidateCrossRepo returns the spec's valid companion targets, in order,
// and one problem per reason a target is dropped, each naming the target's
// project. allowed is the cluster's allow-list: empty, every target is
// invalid (the feature is off).
func ValidateCrossRepo(spec AgentProjectSpec, allowed []string) (valid []CrossRepoTarget, problems []string) {
	if spec.CrossRepo == nil {
		return nil, nil
	}
	allow := map[string]bool{}
	for _, a := range allowed {
		if a = NormalizeProject(a); a != "" {
			allow[a] = true
		}
	}
	home := NormalizeProject(spec.GitLab.Project)
	seen := map[string]bool{}
	for _, t := range spec.CrossRepo.Targets {
		project := NormalizeProject(t.Project)
		var reasons []string
		switch {
		case project == "":
			reasons = append(reasons, "the project is empty")
		case len(allow) == 0:
			reasons = append(reasons, "cross-repo changes are off in this cluster (--cross-repo-allowed-projects is empty)")
		case !allow[project]:
			reasons = append(reasons, "not in the cluster's allow-list (--cross-repo-allowed-projects)")
		}
		if project != "" && project == home {
			reasons = append(reasons, "is the home project")
		}
		for _, n := range NeverTargets {
			if project != "" && project == NormalizeProject(n) {
				reasons = append(reasons, "is never a valid target")
			}
		}
		if project != "" && seen[project] {
			reasons = append(reasons, "duplicates an earlier target")
		}
		seen[project] = true
		reasons = append(reasons, crossRepoPathProblems(t.Paths)...)
		if len(reasons) == 0 {
			valid = append(valid, t)
			continue
		}
		for _, r := range reasons {
			problems = append(problems, fmt.Sprintf("%s: %s", t.Project, r))
		}
	}
	return valid, problems
}

// crossRepoPathProblems checks a target's paths: non-empty, each pattern
// compiles and matches none of the probes.
func crossRepoPathProblems(paths []string) []string {
	if len(paths) == 0 {
		return []string{"paths is empty"}
	}
	var out []string
	for i, p := range paths {
		m, err := pathspec.Compile([]string{p})
		if err != nil {
			out = append(out, fmt.Sprintf("paths[%d]: %v", i, err))
			continue
		}
		reported := map[string]bool{}
		for _, probe := range crossRepoProbes {
			if m.Match(probe.path) && !reported[probe.reason] {
				reported[probe.reason] = true
				out = append(out, fmt.Sprintf("paths[%d] %q %s", i, p, probe.reason))
			}
		}
	}
	return out
}

// RepoLabelPrefix is the scoped label that gives an issue its companion
// repository: "agent-repo::<project>", e.g.
// "agent-repo::my-group/infra/cluster". Being scoped, an issue has at most
// one.
const RepoLabelPrefix = "agent-repo::"

// CompanionMRLabel marks the merge request the runner opens in a companion
// project.
const CompanionMRLabel = "agent::companion"

// FindCrossRepoTarget returns the target of project among targets,
// compared as NormalizeProject does.
func FindCrossRepoTarget(targets []CrossRepoTarget, project string) (CrossRepoTarget, bool) {
	p := NormalizeProject(project)
	for _, t := range targets {
		if p != "" && NormalizeProject(t.Project) == p {
			return t, true
		}
	}
	return CrossRepoTarget{}, false
}

// ReviewHeads is what spec.source.reviewFor names for a re-review: the
// pushed home head, and with a companion that has a pushed head
// "<home>+<companion>". A new companion head with the home head unchanged
// is a new review key, so it is reviewed again (also by "/agent review").
func ReviewHeads(home, companion string) string {
	if companion == "" {
		return home
	}
	return home + "+" + companion
}

// ReviewKey is ReviewHeads of the pushed heads in the status: the review
// key a re-review of the task's current change asks for.
func (s RunStatus) ReviewKey() string {
	var companion string
	if c := s.Companion; c != nil {
		companion = c.PushedSHA
	}
	return ReviewHeads(s.PushedSHA, companion)
}

// CompanionUnreviewed reports whether the companion's pushed head is not
// the one the last review saw. Without a pushed companion head it is false.
func (s RunStatus) CompanionUnreviewed() bool {
	c := s.Companion
	return c != nil && c.PushedSHA != "" && c.ReviewedSHA != c.PushedSHA
}

// LandingState is where a companion merge request stands after its home
// merge (status.companions[].state).
// +kubebuilder:validation:Enum=waitingRelease;rebasing;waitingCI;ready;merging;merged;closed;blocked
type LandingState string

const (
	// LandingWaitingRelease: the home change is merged, but not live yet
	// (no release that contains it runs, or no tag contains it).
	LandingWaitingRelease LandingState = "waitingRelease"
	// LandingRebasing: GitLab rebases the companion merge request.
	LandingRebasing LandingState = "rebasing"
	// LandingWaitingCI: the home change is live; the companion waits for a
	// green pipeline on its head, or for GitLab to check it.
	LandingWaitingCI LandingState = "waitingCI"
	// LandingReady: the companion can land: live, rebased if it had to be,
	// green on its head and mergeable.
	LandingReady LandingState = "ready"
	// LandingMerging: autopilot merges it.
	LandingMerging LandingState = "merging"
	// LandingMerged: it was merged.
	LandingMerged LandingState = "merged"
	// LandingClosed: it was closed without a merge (or is gone).
	LandingClosed LandingState = "closed"
	// LandingBlocked: a human must act (a conflict a rebase did not resolve,
	// a red pipeline, someone's push, an approval); reason says why.
	LandingBlocked LandingState = "blocked"
)

// Ended reports whether the companion's landing is over (merged or closed).
func (s LandingState) Ended() bool { return s == LandingMerged || s == LandingClosed }

// CompanionRetention is how long an ended companion stays in
// status.companions.
const CompanionRetention = 7 * 24 * time.Hour

// CompanionStatus is a companion merge request after its home merge
// (docs/specs/cross-repo-changes.spec.md, decision 8): the operator checks
// it once per poll until it is ready to land.
type CompanionStatus struct {
	// HomeIssueIID is the home issue the companion belongs to.
	HomeIssueIID int64 `json:"homeIssueIID"`
	// MilestoneIID and MilestoneTitle are the home issue's milestone.
	// +optional
	MilestoneIID int64 `json:"milestoneIID,omitempty"`
	// +optional
	MilestoneTitle string `json:"milestoneTitle,omitempty"`
	// HomeMRIID is the home merge request that was merged.
	HomeMRIID int64 `json:"homeMRIID"`
	// HomeMergeSHA is the commit the home merge left on the default branch
	// (the merge commit, else the squash commit, else the head).
	// +optional
	HomeMergeSHA string `json:"homeMergeSHA,omitempty"`
	// DocsOnly: the home merge touches only docs/** and *.md, so it is live
	// at merge.
	// +optional
	DocsOnly bool `json:"docsOnly,omitempty"`

	// Project is the companion's full path; MRIID its merge request, MRURL
	// the merge request's web URL.
	Project string `json:"project"`
	MRIID   int64  `json:"mrIID"`
	// +optional
	MRURL string `json:"mrURL,omitempty"`
	// HeadSHA is the companion head the landing checks: the agent's pushed
	// head at the home merge, then a rebased head whose change hashes as
	// Diff. A head with another change blocks the landing.
	// +optional
	HeadSHA string `json:"headSHA,omitempty"`
	// HeadSince is when HeadSHA was taken: a head without a pipeline after
	// spec.ci.noPipelineTimeout counts as one without CI.
	// +optional
	HeadSince *metav1.Time `json:"headSince,omitempty"`
	// Diff is the hash of the companion's change at HeadSHA when the home
	// merge was recorded; empty when it could not be hashed.
	// +optional
	Diff string `json:"diff,omitempty"`

	// State is where the landing stands; Reason says why it waits or is
	// blocked.
	State LandingState `json:"state"`
	// +optional
	Reason string `json:"reason,omitempty"`
	// Release is the release (tag) that made the home change live; empty
	// for a docs-only change or a project that does not tag.
	// +optional
	Release string `json:"release,omitempty"`
	// AutoMerge is the home task's spec.companion.autoMerge at the home
	// merge: autopilot may merge the companion.
	// +optional
	AutoMerge bool `json:"autoMerge,omitempty"`
	// Owners are the home task's owners, mentioned when the companion needs
	// a human outside autopilot.
	// +optional
	Owners []string `json:"owners,omitempty"`

	// RebasedSHA is the companion head the operator asked GitLab to rebase,
	// once per entry (an owner's resume allows one more); RebasedAt when.
	// +optional
	RebasedSHA string `json:"rebasedSHA,omitempty"`
	// +optional
	RebasedAt *metav1.Time `json:"rebasedAt,omitempty"`
	// BlockedNotice is the block reason the companion merge request was
	// told about (outside autopilot), so each block is commented once.
	// +optional
	BlockedNotice string `json:"blockedNotice,omitempty"`

	// ReadyAt is when the companion became ready; NotifiedAt when the owners
	// were told, RemindedAt when they were reminded.
	// +optional
	ReadyAt *metav1.Time `json:"readyAt,omitempty"`
	// +optional
	NotifiedAt *metav1.Time `json:"notifiedAt,omitempty"`
	// +optional
	RemindedAt *metav1.Time `json:"remindedAt,omitempty"`
	// EndedAt is when it was seen merged or closed; the entry is removed
	// CompanionRetention later.
	// +optional
	EndedAt *metav1.Time `json:"endedAt,omitempty"`
	// MergeSHA is the commit the companion's merge left.
	// +optional
	MergeSHA string `json:"mergeSHA,omitempty"`
}
