package v1alpha1

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

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
