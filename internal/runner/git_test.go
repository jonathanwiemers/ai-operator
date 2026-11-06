package runner

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// guardedMirror is a mirror of a local bare remote with main and
// agent/42-x, its push policy for that remote, and a commit on
// refs/heads/agent/42-x in the mirror one ahead of the remote.
func guardedMirror(t *testing.T) (m *Mirror, origin string, log *[]string) {
	t.Helper()
	e := newImplementEnv(t)
	gitCmd(t, e.seed, "push", "-q", "origin", "HEAD:agent/42-x")
	var lines []string
	m = &Mirror{Dir: filepath.Join(e.tmp, "mirror.git"),
		Log:    funcr.New(func(_, args string) { lines = append(lines, args) }, funcr.Options{}),
		Policy: &PushPolicy{Project: "group/app", DefaultBranch: "main", URL: e.origin}}
	ctx := context.Background()
	if err := m.Sync(ctx, e.origin, "main", "agent/42-x"); err != nil {
		t.Fatal(err)
	}
	sha, err := m.run(ctx, false, "-c", "user.name=t", "-c", "user.email=t@t", "commit-tree",
		"refs/remotes/origin/agent/42-x^{tree}", "-p", "refs/remotes/origin/agent/42-x", "-m", "work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.run(ctx, false, "update-ref", "refs/heads/agent/42-x", sha); err != nil {
		t.Fatal(err)
	}
	return m, e.origin, &lines
}

// remoteRefs lists every ref of the remote with its commit.
func remoteRefs(t *testing.T, origin string) string {
	t.Helper()
	return gitCmd(t, filepath.Dir(origin), "--git-dir", origin, "for-each-ref", "--format=%(refname) %(objectname)")
}

func TestMirrorPushesAgentBranch(t *testing.T) {
	m, origin, log := guardedMirror(t)
	ctx := context.Background()
	if err := m.Push(ctx, "agent/42-x", false); err != nil {
		t.Fatal(err)
	}
	want, _ := m.resolve(ctx, "refs/heads/agent/42-x")
	if got := gitCmd(t, filepath.Dir(origin), "--git-dir", origin, "rev-parse", "agent/42-x"); got != want {
		t.Fatalf("remote agent/42-x at %s, want %s", got, want)
	}
	// A start commit of a new agent/ branch too.
	if _, err := m.StartBranch(ctx, "agent/43-y", "main", "Start work on #43"); err != nil {
		t.Fatal(err)
	}
	if len(*log) != 0 {
		t.Fatalf("nothing refused, nothing logged: %q", *log)
	}
}

func TestMirrorRefusesPushes(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		push func(m *Mirror) error
		// policy changes the mirror's policy before the push.
		policy func(m *Mirror)
	}{
		{name: "default branch", push: func(m *Mirror) error { return m.push(ctx, "refs/heads/agent/42-x", "refs/heads/main", false) }},
		{name: "bare default branch", push: func(m *Mirror) error { return m.push(ctx, "refs/heads/agent/42-x", "main", false) }},
		{name: "release branch", push: func(m *Mirror) error { return m.push(ctx, "refs/heads/agent/42-x", "refs/heads/release/x", false) }},
		{name: "tag", push: func(m *Mirror) error { return m.push(ctx, "refs/heads/agent/42-x", "refs/tags/v1", false) }},
		{name: "agent tag", push: func(m *Mirror) error { return m.push(ctx, "refs/heads/agent/42-x", "refs/tags/agent/v1", false) }},
		{name: "tag source to a bare name", push: func(m *Mirror) error { return m.push(ctx, "refs/tags/v1", "agent/v1", false) }},
		{name: "delete", push: func(m *Mirror) error { return m.push(ctx, "", "refs/heads/agent/42-x", false) }},
		{name: "option as source", push: func(m *Mirror) error { return m.push(ctx, "--delete", "refs/heads/agent/42-x", false) }},
		{name: "forced", push: func(m *Mirror) error { return m.push(ctx, "+refs/heads/agent/42-x", "refs/heads/agent/42-x", false) }},
		{name: "path out of agent/", push: func(m *Mirror) error { return m.push(ctx, "refs/heads/agent/42-x", "refs/heads/agent/../main", false) }},
		{name: "glob", push: func(m *Mirror) error { return m.push(ctx, "refs/heads/agent/*", "refs/heads/agent/*", false) }},
		{name: "Push of main", push: func(m *Mirror) error { return m.Push(ctx, "main", false) }},
		{name: "StartBranch of release/x", push: func(m *Mirror) error {
			_, err := m.StartBranch(ctx, "release/x", "main", "start")
			return err
		}},
		{name: "agent default branch", policy: func(m *Mirror) { m.Policy.DefaultBranch = "agent/42-x" },
			push: func(m *Mirror) error { return m.Push(ctx, "agent/42-x", false) }},
		{name: "unknown default branch", policy: func(m *Mirror) { m.Policy.DefaultBranch = "" },
			push: func(m *Mirror) error { return m.Push(ctx, "agent/42-x", false) }},
		{name: "no policy", policy: func(m *Mirror) { m.Policy = nil },
			push: func(m *Mirror) error { return m.Push(ctx, "agent/42-x", false) }},
		{name: "another project's remote", policy: func(m *Mirror) {
			m.Policy = &PushPolicy{Project: "group/infra", DefaultBranch: "main", URL: "https://gitlab.example/group/infra.git"}
		}, push: func(m *Mirror) error { return m.Push(ctx, "agent/42-x", false) }},
		{name: "remote URL from config of an earlier process", policy: func(m *Mirror) {
			m.remoteURL = ""
			m.Policy.URL = "https://gitlab.example/group/infra.git"
		}, push: func(m *Mirror) error { return m.Push(ctx, "agent/42-x", false) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, origin, log := guardedMirror(t)
			before := remoteRefs(t, origin)
			if tc.policy != nil {
				tc.policy(m)
			}
			err := tc.push(m)
			var ge *gitlab.GuardError
			if !errors.As(err, &ge) || gitlab.IsTransient(err) {
				t.Fatalf("want a *GuardError, got %v", err)
			}
			if after := remoteRefs(t, origin); after != before {
				t.Fatalf("the remote changed:\n%s\nwas\n%s", after, before)
			}
			if len(*log) != 1 || !strings.Contains((*log)[0], `"msg"="guard refused"`) || !strings.Contains((*log)[0], `"op"=`) ||
				!strings.Contains((*log)[0], `"reason"=`) || !strings.Contains((*log)[0], `"project"=`) {
				t.Fatalf("want one refusal logged, got %q", *log)
			}
		})
	}
}

// A mirror synced by an earlier process knows its remote from its config.
func TestMirrorPushUsesConfiguredRemote(t *testing.T) {
	m, _, _ := guardedMirror(t)
	m.remoteURL = ""
	if err := m.Push(context.Background(), "agent/42-x", false); err != nil {
		t.Fatal(err)
	}
}

// The runner's client allows merges at home and in the task's companion
// only when both the task and the target allow them.
func TestGuardedGitLab(t *testing.T) {
	ctx := context.Background()
	def := gitlabfake.New()
	all := gitlabfake.NewProjects(def)
	for _, f := range []*gitlabfake.Fake{def, all.Add("group/infra"), all.Add("group/docs")} {
		f.MRs[1] = &gitlab.MergeRequest{IID: 1, State: "opened", SHA: "s", DetailedMergeStatus: "mergeable"}
	}
	r := &Runner{NewGitLab: func(string, string) gitlab.API { return all }, Log: logr.Discard()}
	spec := aiv1.AgentProjectSpec{GitLab: aiv1.GitLabSpec{Project: "group/app"}, CrossRepo: &aiv1.CrossRepoSpec{Targets: []aiv1.CrossRepoTarget{
		{Project: "group/infra", Paths: []string{"apps/"}, AutoMerge: true},
		{Project: "group/docs", Paths: []string{"docs/"}, AutoMerge: true},
	}}}
	merge := func(task *aiv1.AgentTask, project string) error {
		_, err := r.guardedGitLab(spec, task).MergeMergeRequest(ctx, project, 1, gitlab.MergeOptions{SHA: "s"})
		return err
	}
	withCompanion := func(autoMerge bool) *aiv1.AgentTask {
		return &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Companion: &aiv1.CompanionSpec{Project: "group/infra", AutoMerge: autoMerge}}}
	}
	var ge *gitlab.GuardError
	if err := merge(&aiv1.AgentTask{}, "group/app"); err != nil {
		t.Fatalf("home merge: %v", err)
	}
	if err := merge(&aiv1.AgentTask{}, "group/infra"); !errors.As(err, &ge) {
		t.Fatalf("no companion, no target: %v", err)
	}
	if err := merge(withCompanion(false), "group/infra"); !errors.As(err, &ge) {
		t.Fatalf("the task does not auto-merge: %v", err)
	}
	if err := merge(withCompanion(true), "group/docs"); !errors.As(err, &ge) {
		t.Fatalf("only the task's companion is a target: %v", err)
	}
	if err := merge(withCompanion(true), "group/infra"); err != nil {
		t.Fatalf("companion merge: %v", err)
	}
}
