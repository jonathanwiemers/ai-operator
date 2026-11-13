package gitlab_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

const (
	home      = "group/app"
	infra     = "group/infra/cluster"
	docs      = "group/docs"
	secrets   = "example/secrets"
	unrelated = "group/other"
)

// never is the cluster's never-list: the secrets project.
var never = []string{secrets}

// guardSpec is a home project with three targets the cluster allows:
// infra with autoMerge, docs without, and the never-target.
func guardSpec() (aiv1.AgentProjectSpec, []string) {
	spec := aiv1.AgentProjectSpec{GitLab: aiv1.GitLabSpec{Project: "Group/App"}}
	spec.CrossRepo = &aiv1.CrossRepoSpec{Targets: []aiv1.CrossRepoTarget{
		{Project: "Group/Infra/Cluster", Paths: []string{"clusters/app/"}, AutoMerge: true},
		{Project: docs, Paths: []string{"docs/app/"}},
		{Project: secrets, Paths: []string{"tfvars/"}, AutoMerge: true},
	}}
	return spec, []string{infra, docs, secrets}
}

// guardFake is a fake GitLab with the home project, the targets and an
// unrelated project, each with merge request 1 ready to merge.
func guardFake() *gitlabfake.Projects {
	def := gitlabfake.New()
	all := gitlabfake.NewProjects(def)
	for _, f := range []*gitlabfake.Fake{def, all.Add(infra), all.Add(docs), all.Add(secrets), all.Add(unrelated)} {
		f.MRs[1] = &gitlab.MergeRequest{IID: 1, State: "opened", SHA: "s1", DetailedMergeStatus: "mergeable", SourceBranch: "agent/1-x"}
	}
	return all
}

func project(t *testing.T, all *gitlabfake.Projects, path string) *gitlabfake.Fake {
	t.Helper()
	if strings.EqualFold(path, home) {
		return all.Default
	}
	return all.Add(path) // already registered: the same Fake
}

// recorder is a logger that keeps every line.
func recorder() (logr.Logger, *[]string) {
	var lines []string
	return funcr.New(func(prefix, args string) { lines = append(lines, args) }, funcr.Options{}), &lines
}

func TestPolicyDropsInvalidTargets(t *testing.T) {
	spec, allowed := guardSpec()
	p := gitlab.NewPolicy(spec, allowed, never)
	if p.Home() != home {
		t.Fatalf("home %q", p.Home())
	}
	if tp, ok := p.Target(infra); !ok || !tp.AutoMerge {
		t.Fatalf("infra: %+v %v", tp, ok)
	}
	if tp, ok := p.Target(docs); !ok || tp.AutoMerge {
		t.Fatalf("docs: %+v %v", tp, ok)
	}
	// Never a target, even when the spec names it and the cluster allows it.
	if _, ok := p.Target(secrets); ok || len(p.Targets()) != 2 {
		t.Fatalf("the never-target is in the policy: %v", p.Targets())
	}
	// The never-list is the cluster's: without it the project is a target.
	if _, ok := gitlab.NewPolicy(spec, allowed, nil).Target(secrets); !ok {
		t.Fatalf("without a never-list the allowed project is no target")
	}
	// Without the cluster's allow-list there are no targets at all.
	if off := gitlab.NewPolicy(spec, nil, never); len(off.Targets()) != 0 || off.Home() != home {
		t.Fatalf("feature off: %v", off.Targets())
	}
}

func TestPolicyForCompanion(t *testing.T) {
	spec, allowed := guardSpec()
	p := gitlab.NewPolicy(spec, allowed, never)
	for _, tc := range []struct {
		name      string
		c         *aiv1.CompanionSpec
		target    string
		autoMerge bool
	}{
		{"no companion", nil, "", false},
		{"task and target auto-merge", &aiv1.CompanionSpec{Project: infra, AutoMerge: true}, infra, true},
		{"task without auto-merge", &aiv1.CompanionSpec{Project: infra}, infra, false},
		{"target without auto-merge", &aiv1.CompanionSpec{Project: docs, AutoMerge: true}, docs, false},
		{"never-target", &aiv1.CompanionSpec{Project: secrets, AutoMerge: true}, "", false},
		{"unknown project", &aiv1.CompanionSpec{Project: unrelated, AutoMerge: true}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := p.ForCompanion(tc.c)
			if got.Home() != home {
				t.Fatalf("home %q", got.Home())
			}
			targets := got.Targets()
			if tc.target == "" {
				if len(targets) != 0 {
					t.Fatalf("targets %v", targets)
				}
				return
			}
			if tp, ok := got.Target(tc.target); len(targets) != 1 || !ok || tp.AutoMerge != tc.autoMerge {
				t.Fatalf("targets %v, %s: %+v", targets, tc.target, tp)
			}
		})
	}
}

func TestGuard(t *testing.T) {
	ctx := context.Background()
	type call func(api gitlab.API, project string) error
	merge := func(api gitlab.API, project string) error {
		_, err := api.MergeMergeRequest(ctx, project, 1, gitlab.MergeOptions{SHA: "s1"})
		return err
	}
	deleteBranch := func(branch string) call {
		return func(api gitlab.API, project string) error { return api.DeleteBranch(ctx, project, branch) }
	}
	merged := func(f *gitlabfake.Fake) bool { return len(f.Merged) > 0 }
	deleted := func(f *gitlabfake.Fake) bool { return len(f.DeletedBranches) > 0 }
	for _, tc := range []struct {
		name    string
		project string
		call    call
		did     func(*gitlabfake.Fake) bool
		allowed bool
		// defaultBranch, if set, is the project's default branch.
		defaultBranch string
	}{
		{name: "home merge", project: home, call: merge, did: merged, allowed: true},
		{name: "home merge, other case", project: "GROUP/app", call: merge, did: merged, allowed: true},
		{name: "target with autoMerge merge", project: infra, call: merge, did: merged, allowed: true},
		{name: "target without autoMerge merge", project: docs, call: merge, did: merged},
		{name: "never-target merge", project: secrets, call: merge, did: merged},
		{name: "unknown project merge", project: unrelated, call: merge, did: merged},
		{name: "project by ID merge", project: "1", call: merge, did: merged},
		{name: "delete default branch main", project: home, call: deleteBranch("main"), did: deleted},
		{name: "delete an agent default branch", project: infra, call: deleteBranch("agent/main"), did: deleted, defaultBranch: "agent/main"},
		{name: "delete main in a target", project: infra, call: deleteBranch("main"), did: deleted},
		{name: "delete release branch", project: home, call: deleteBranch("release/x"), did: deleted},
		{name: "delete agent prefix only", project: home, call: deleteBranch("agent/"), did: deleted},
		{name: "delete agent branch with ..", project: home, call: deleteBranch("agent/../main"), did: deleted},
		{name: "delete agent branch in unknown project", project: unrelated, call: deleteBranch("agent/x"), did: deleted},
		{name: "delete agent branch in never-target", project: secrets, call: deleteBranch("agent/x"), did: deleted},
		{name: "delete agent branch in target", project: infra, call: deleteBranch("agent/x"), did: deleted, allowed: true},
		{name: "delete agent branch at home", project: home, call: deleteBranch("agent/42-x"), did: deleted, allowed: true},
		{name: "create MR in target", project: docs, allowed: true, call: func(api gitlab.API, p string) error {
			_, err := api.CreateMergeRequest(ctx, p, gitlab.CreateMergeRequestOptions{SourceBranch: "agent/1-x", TargetBranch: "main", Title: "T"})
			return err
		}, did: func(f *gitlabfake.Fake) bool { return len(f.MRs) > 1 }},
		{name: "create MR in unknown project", project: unrelated, call: func(api gitlab.API, p string) error {
			_, err := api.CreateMergeRequest(ctx, p, gitlab.CreateMergeRequestOptions{SourceBranch: "agent/1-x", TargetBranch: "main", Title: "T"})
			return err
		}, did: func(f *gitlabfake.Fake) bool { return len(f.MRs) > 1 }},
		{name: "update MR in unknown project", project: unrelated, call: func(api gitlab.API, p string) error {
			_, err := api.UpdateMergeRequest(ctx, p, 1, gitlab.UpdateMergeRequestOptions{Title: "x"})
			return err
		}, did: func(f *gitlabfake.Fake) bool { return f.MRs[1].Title == "x" }},
		{name: "update MR in target without autoMerge", project: docs, allowed: true, call: func(api gitlab.API, p string) error {
			_, err := api.UpdateMergeRequest(ctx, p, 1, gitlab.UpdateMergeRequestOptions{Title: "x"})
			return err
		}, did: func(f *gitlabfake.Fake) bool { return f.MRs[1].Title == "x" }},
		{name: "rebase in unknown project", project: unrelated, call: func(api gitlab.API, p string) error {
			return api.RebaseMergeRequest(ctx, p, 1)
		}, did: func(f *gitlabfake.Fake) bool { return len(f.Rebased) > 0 }},
		{name: "rebase in target", project: docs, allowed: true, call: func(api gitlab.API, p string) error {
			return api.RebaseMergeRequest(ctx, p, 1)
		}, did: func(f *gitlabfake.Fake) bool { return len(f.Rebased) > 0 }},
		{name: "pipeline in never-target", project: secrets, call: func(api gitlab.API, p string) error {
			_, err := api.CreatePipeline(ctx, p, "agent/1-x", gitlab.PipelineVariable{Key: "AGENT_RELEASE", Value: "true"})
			return err
		}, did: func(f *gitlabfake.Fake) bool { return len(f.BranchPipelines) > 0 }},
		{name: "pipeline at home", project: home, allowed: true, call: func(api gitlab.API, p string) error {
			_, err := api.CreatePipeline(ctx, p, "agent/1-x", gitlab.PipelineVariable{Key: "AGENT_RELEASE", Value: "true"})
			return err
		}, did: func(f *gitlabfake.Fake) bool { return len(f.BranchPipelines) > 0 }},
		{name: "retry pipeline in unknown project", project: unrelated, call: func(api gitlab.API, p string) error {
			_, err := api.RetryPipeline(ctx, p, 1)
			return err
		}, did: func(f *gitlabfake.Fake) bool { return len(f.Retried) > 0 }},
		{name: "release in never-target", project: secrets, call: func(api gitlab.API, p string) error {
			return api.CreateRelease(ctx, p, "2026.11.1", "main", "notes")
		}, did: func(f *gitlabfake.Fake) bool { return len(f.Releases) > 0 }},
		{name: "release at home", project: home, allowed: true, call: func(api gitlab.API, p string) error {
			return api.CreateRelease(ctx, p, "2026.11.1", "main", "notes")
		}, did: func(f *gitlabfake.Fake) bool { return len(f.Releases) > 0 }},
		{name: "MR pipeline in unknown project", project: unrelated, call: func(api gitlab.API, p string) error {
			return api.CreateMergeRequestPipeline(ctx, p, 1)
		}, did: func(f *gitlabfake.Fake) bool { return len(f.MRPipelines) > 0 }},
		{name: "MR pipeline in target", project: infra, allowed: true, call: func(api gitlab.API, p string) error {
			return api.CreateMergeRequestPipeline(ctx, p, 1)
		}, did: func(f *gitlabfake.Fake) bool { return len(f.MRPipelines) > 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			all := guardFake()
			inner := all.Default // the default project has ID 1
			if tc.project != "1" {
				inner = project(t, all, tc.project)
			}
			if tc.defaultBranch != "" {
				inner.Project.DefaultBranch = tc.defaultBranch
			}
			spec, allowed := guardSpec()
			log, lines := recorder()
			err := tc.call(gitlab.Guard(all, gitlab.NewPolicy(spec, allowed, never), log), tc.project)
			var ge *gitlab.GuardError
			if tc.allowed {
				if err != nil || !tc.did(inner) || len(*lines) != 0 {
					t.Fatalf("want allowed: err %v, did %v, log %q", err, tc.did(inner), *lines)
				}
				return
			}
			if !errors.As(err, &ge) || gitlab.IsTransient(err) {
				t.Fatalf("want a non-transient *GuardError, got %v", err)
			}
			if tc.did(inner) {
				t.Fatalf("the refused call reached GitLab")
			}
			if ge.Project != tc.project || ge.Op == "" || ge.Reason == "" {
				t.Fatalf("error %+v", ge)
			}
			if len(*lines) != 1 || !strings.Contains((*lines)[0], `"project"="`+tc.project+`"`) ||
				!strings.Contains((*lines)[0], `"op"=`) || !strings.Contains((*lines)[0], `"reason"=`) {
				t.Fatalf("want one log line with project, op and reason, got %q", *lines)
			}
		})
	}
}

// A policy that is handed a companion naming the never-target, or one with
// autoMerge on a target without it, still refuses the merge.
func TestGuardWithCompanionPolicy(t *testing.T) {
	ctx := context.Background()
	spec, allowed := guardSpec()
	for _, tc := range []struct {
		c       *aiv1.CompanionSpec
		project string
		allowed bool
	}{
		{&aiv1.CompanionSpec{Project: infra, AutoMerge: true}, infra, true},
		{&aiv1.CompanionSpec{Project: infra}, infra, false},
		{&aiv1.CompanionSpec{Project: docs, AutoMerge: true}, docs, false},
		{&aiv1.CompanionSpec{Project: secrets, AutoMerge: true}, secrets, false},
		// Only the task's companion is a target in the runner.
		{&aiv1.CompanionSpec{Project: docs}, infra, false},
	} {
		all := guardFake()
		inner := project(t, all, tc.project)
		api := gitlab.Guard(all, gitlab.NewPolicy(spec, allowed, never).ForCompanion(tc.c), logr.Discard())
		_, err := api.MergeMergeRequest(ctx, tc.project, 1, gitlab.MergeOptions{SHA: "s1"})
		var ge *gitlab.GuardError
		if tc.allowed != (err == nil) || !tc.allowed && (!errors.As(err, &ge) || len(inner.Merged) != 0) {
			t.Errorf("companion %+v, merge in %s: err %v, merged %v", tc.c, tc.project, err, inner.Merged)
		}
	}
}

// Reads, issue and note writes in other projects pass: links and comments
// there are wanted.
func TestGuardPassesReadsAndNotes(t *testing.T) {
	ctx := context.Background()
	all := guardFake()
	other := all.Add(unrelated)
	other.AddIssue(5, "Elsewhere")
	spec, allowed := guardSpec()
	log, lines := recorder()
	api := gitlab.Guard(all, gitlab.NewPolicy(spec, allowed, never), log)
	if _, err := api.GetMergeRequest(ctx, unrelated, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := api.CreateIssueNote(ctx, unrelated, 5, "hello"); err != nil {
		t.Fatal(err)
	}
	if len(other.IssueNotes[5]) != 1 || len(*lines) != 0 {
		t.Fatalf("notes %v, log %q", other.IssueNotes[5], *lines)
	}
}

// Guard keeps the inner client's request counts.
func TestGuardKeepsStats(t *testing.T) {
	spec, allowed := guardSpec()
	api := gitlab.Guard(gitlab.New("https://gitlab.example", "t"), gitlab.NewPolicy(spec, allowed, never), logr.Discard())
	if _, ok := api.(interface{ Stats() gitlab.Stats }); !ok {
		t.Fatal("Stats is lost")
	}
	if _, ok := gitlab.Guard(gitlabfake.New(), gitlab.NewPolicy(spec, allowed, never), logr.Discard()).(interface{ Stats() gitlab.Stats }); ok {
		t.Fatal("a client without Stats must not get one")
	}
}

func TestIsAgentBranch(t *testing.T) {
	for b, want := range map[string]bool{
		"agent/42-x": true, "agent/group-app-7-y": true, "agent/a/b": true,
		"main": false, "agent/": false, "agent": false, "release/x": false, "refs/heads/agent/x": false,
		"agent/../main": false, "agent/x:main": false, "agent/*": false, "agent/x.lock": false,
		"agent/.x": false, "agent//x": false, "agent/x/": false, "agent/x@{1}": false,
	} {
		if got := gitlab.IsAgentBranch(b); got != want {
			t.Errorf("IsAgentBranch(%q) = %v, want %v", b, got, want)
		}
	}
}
