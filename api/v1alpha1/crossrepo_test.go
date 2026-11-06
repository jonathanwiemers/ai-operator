package v1alpha1

import (
	"strings"
	"testing"
)

func crossRepoSpec(targets ...CrossRepoTarget) AgentProjectSpec {
	return AgentProjectSpec{GitLab: GitLabSpec{Project: "jo.wiemers/ai-operator"}, CrossRepo: &CrossRepoSpec{Targets: targets}}
}

func TestValidateCrossRepo(t *testing.T) {
	const infra = "jo.wiemers/infra/cluster"
	allowed := []string{infra, "jo.wiemers/ai-operator", "jo.wiemers/personal/kubernetes", "jo.wiemers/bloop"}
	good := []string{"clusters/netcup01/applications/ai-operator/"}
	for _, tc := range []struct {
		name    string
		targets []CrossRepoTarget
		allowed []string
		valid   []string // projects of the valid targets
		want    []string // a part of each problem, in order
	}{
		{name: "valid", targets: []CrossRepoTarget{{Project: infra, Paths: good}}, allowed: allowed, valid: []string{infra}},
		{name: "case and slashes", targets: []CrossRepoTarget{{Project: "/Jo.Wiemers/Infra/Cluster/", Paths: good}}, allowed: []string{" JO.WIEMERS/infra/cluster/ "}, valid: []string{"/Jo.Wiemers/Infra/Cluster/"}},
		{name: "not in the allow-list", targets: []CrossRepoTarget{{Project: "jo.wiemers/other", Paths: good}}, allowed: allowed,
			want: []string{"jo.wiemers/other: not in the cluster's allow-list"}},
		{name: "empty allow-list", targets: []CrossRepoTarget{{Project: infra, Paths: good}, {Project: "jo.wiemers/bloop", Paths: good}},
			want: []string{infra + ": cross-repo changes are off", "jo.wiemers/bloop: cross-repo changes are off"}},
		{name: "home project", targets: []CrossRepoTarget{{Project: "JO.WIEMERS/ai-operator", Paths: good}}, allowed: allowed,
			want: []string{"JO.WIEMERS/ai-operator: is the home project"}},
		{name: "never target", targets: []CrossRepoTarget{{Project: "jo.wiemers/personal/kubernetes", Paths: good}}, allowed: allowed,
			want: []string{"jo.wiemers/personal/kubernetes: is never a valid target"}},
		{name: "duplicate", targets: []CrossRepoTarget{{Project: infra, Paths: good}, {Project: infra + "/", Paths: []string{"other/"}}}, allowed: allowed,
			valid: []string{infra}, want: []string{infra + "/: duplicates an earlier target"}},
		{name: "empty paths", targets: []CrossRepoTarget{{Project: infra}}, allowed: allowed, want: []string{infra + ": paths is empty"}},
		{name: "not compiling", targets: []CrossRepoTarget{{Project: infra, Paths: []string{"!secrets/"}}}, allowed: allowed,
			want: []string{infra + ": paths[0]: pattern \"!secrets/\": negation"}},
		{name: ".gitlab-ci.yml", targets: []CrossRepoTarget{{Project: infra, Paths: []string{"clusters/", ".gitlab-ci.yml"}}}, allowed: allowed,
			want: []string{infra + `: paths[1] ".gitlab-ci.yml" matches .gitlab-ci.yml`}},
		{name: "*.yml", targets: []CrossRepoTarget{{Project: infra, Paths: []string{"*.yml"}}}, allowed: allowed,
			want: []string{infra + `: paths[0] "*.yml" matches .gitlab-ci.yml`}},
		{name: ".gitlab/**", targets: []CrossRepoTarget{{Project: infra, Paths: []string{".gitlab/**"}}}, allowed: allowed,
			want: []string{infra + `: paths[0] ".gitlab/**" matches .gitlab/`}},
		{name: "**", targets: []CrossRepoTarget{{Project: infra, Paths: []string{"**"}}}, allowed: allowed,
			want: []string{"matches .gitlab-ci.yml", "matches .gitlab/", `paths[0] "**" matches the whole tree`}},
		{name: "*", targets: []CrossRepoTarget{{Project: infra, Paths: []string{"*"}}}, allowed: allowed,
			want: []string{"matches .gitlab-ci.yml", "matches .gitlab/", `paths[0] "*" matches the whole tree`}},
		{name: "*/", targets: []CrossRepoTarget{{Project: infra, Paths: []string{"*/"}}}, allowed: allowed,
			want: []string{"matches .gitlab/", `paths[0] "*/" matches the whole tree`}},
		{name: "/", targets: []CrossRepoTarget{{Project: infra, Paths: []string{"/"}}}, allowed: allowed,
			want: []string{infra + `: paths[0]: pattern "/": empty pattern`}},
		{name: "several reasons", targets: []CrossRepoTarget{{Project: "jo.wiemers/personal/kubernetes"}}, allowed: []string{infra},
			want: []string{"not in the cluster's allow-list", "is never a valid target", "paths is empty"}},
		{name: "mixed", targets: []CrossRepoTarget{{Project: "jo.wiemers/other", Paths: good}, {Project: infra, Paths: good}, {Project: "jo.wiemers/bloop", Paths: []string{"clusters/netcup01/applications/bloop/", "README.md"}}}, allowed: allowed,
			valid: []string{infra, "jo.wiemers/bloop"}, want: []string{"jo.wiemers/other: not in"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			valid, problems := ValidateCrossRepo(crossRepoSpec(tc.targets...), tc.allowed)
			var got []string
			for _, v := range valid {
				got = append(got, v.Project)
			}
			if strings.Join(got, ",") != strings.Join(tc.valid, ",") {
				t.Errorf("valid = %v, want %v", got, tc.valid)
			}
			if len(problems) != len(tc.want) {
				t.Fatalf("problems = %q, want %d like %q", problems, len(tc.want), tc.want)
			}
			for i, w := range tc.want {
				if !strings.Contains(problems[i], w) {
					t.Errorf("problem %d = %q, want it to contain %q", i, problems[i], w)
				}
			}
		})
	}
}

func TestValidateCrossRepoUnset(t *testing.T) {
	valid, problems := ValidateCrossRepo(AgentProjectSpec{GitLab: GitLabSpec{Project: "a/b"}}, []string{"a/c"})
	if valid != nil || problems != nil {
		t.Errorf("without spec.crossRepo: %v, %v", valid, problems)
	}
}

func TestValidateCrossRepoKeepsTargets(t *testing.T) {
	target := CrossRepoTarget{Project: "jo.wiemers/infra/cluster", Paths: []string{"clusters/"}, AutoMerge: true}
	valid, _ := ValidateCrossRepo(crossRepoSpec(target), []string{"jo.wiemers/infra/cluster"})
	if len(valid) != 1 || !valid[0].AutoMerge || valid[0].Paths[0] != "clusters/" {
		t.Errorf("valid = %+v, want the target as it is", valid)
	}
}

func TestReviewKeyAndCompanionUnreviewed(t *testing.T) {
	s := RunStatus{PushedSHA: "home"}
	if s.ReviewKey() != "home" || s.CompanionUnreviewed() {
		t.Fatalf("without a companion the key is the home head: %q", s.ReviewKey())
	}
	s.Companion = &CompanionRun{Project: "group/infra"}
	if s.ReviewKey() != "home" || s.CompanionUnreviewed() {
		t.Fatalf("a companion without a pushed head adds nothing: %q", s.ReviewKey())
	}
	s.Companion.PushedSHA = "c1"
	if s.ReviewKey() != "home+c1" || !s.CompanionUnreviewed() {
		t.Fatalf("a pushed companion head is part of the key and unreviewed: %q", s.ReviewKey())
	}
	s.Companion.ReviewedSHA = "c1"
	if s.CompanionUnreviewed() {
		t.Fatal("reviewed at its pushed head")
	}
	s.Companion.PushedSHA = "c2"
	if s.ReviewKey() != ReviewHeads("home", "c2") || !s.CompanionUnreviewed() {
		t.Fatal("a companion-only push is unreviewed again")
	}
}
