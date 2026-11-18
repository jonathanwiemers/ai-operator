package runner

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestMirrorCommitsAhead(t *testing.T) {
	m, _, _ := guardedMirror(t)
	ctx := context.Background()
	// The branch of the seed sits on main; the mirror's head holds one more.
	if n, err := m.CommitsAhead(ctx, "main", "agent/42-x"); err != nil || n != 1 {
		t.Fatalf("ahead = %d, %v, want 1", n, err)
	}
	if _, err := m.StartBranch(ctx, "agent/43-y", "main", "Start work on #43"); err != nil {
		t.Fatal(err)
	}
	if n, err := m.CommitsAhead(ctx, "main", "agent/43-y"); err != nil || n != 1 {
		t.Fatalf("ahead of the start commit = %d, %v, want 1", n, err)
	}
	if _, err := m.CommitsAhead(ctx, "main", "agent/none"); err == nil {
		t.Fatal("a missing branch counts as an error")
	}
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

// leaseMirror is guardedMirror with a checkpoint pushed on agent/42-x and,
// in the mirror's refs/heads/agent/42-x, a step commit that folds it: on the
// checkpoint's parent. It returns the checkpoint.
func leaseMirror(t *testing.T) (m *Mirror, origin, cp string) {
	t.Helper()
	m, origin, _ = guardedMirror(t)
	ctx := context.Background()
	base, _ := m.resolve(ctx, "refs/remotes/origin/agent/42-x")
	commit := func(parent, msg string) string {
		sha, err := m.run(ctx, false, "-c", "user.name=t", "-c", "user.email=t@t", "commit-tree", parent+"^{tree}", "-p", parent, "-m", msg)
		if err != nil {
			t.Fatal(err)
		}
		return sha
	}
	setHead := func(sha string) {
		if _, err := m.run(ctx, false, "update-ref", "refs/heads/agent/42-x", sha); err != nil {
			t.Fatal(err)
		}
	}
	cp = commit(base, "chore(wip): checkpoint #42 step 1 (usage-limit)\n\nstat\n\nAgent-Checkpoint: 1/1 usage-limit")
	setHead(cp)
	if err := m.Push(ctx, "agent/42-x", false); err != nil {
		t.Fatal(err)
	}
	setHead(commit(base, "feat: the step"))
	return m, origin, cp
}

func originHead(t *testing.T, origin string) string {
	t.Helper()
	return gitCmd(t, filepath.Dir(origin), "--git-dir", origin, "rev-parse", "agent/42-x")
}

func TestMirrorPushWithLeaseFolds(t *testing.T) {
	m, origin, cp := leaseMirror(t)
	ctx := context.Background()
	// ci.skip, with the fallback for a remote without push options.
	if err := m.PushWithLease(ctx, "agent/42-x", cp, true); err != nil {
		t.Fatal(err)
	}
	want, _ := m.resolve(ctx, "refs/heads/agent/42-x")
	if got := originHead(t, origin); got != want {
		t.Fatalf("remote agent/42-x at %s, want the fold %s", got, want)
	}
}

func TestMirrorPushWithLeaseRejected(t *testing.T) {
	m, origin, cp := leaseMirror(t)
	ctx := context.Background()
	// Someone pushes on top of the checkpoint.
	dir := filepath.Dir(origin)
	human := gitCmd(t, dir, "--git-dir", origin, "commit-tree", cp+"^{tree}", "-p", cp, "-m", "Human fix")
	gitCmd(t, dir, "--git-dir", origin, "update-ref", "refs/heads/agent/42-x", human)
	err := m.PushWithLease(ctx, "agent/42-x", cp, false)
	if !errors.Is(err, ErrLeaseRejected) {
		t.Fatalf("want ErrLeaseRejected, got %v", err)
	}
	if got := originHead(t, origin); got != human {
		t.Fatalf("the human commit must stay: %s", got)
	}
}

func TestMirrorPushWithLeaseRefuses(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		expect func(m *Mirror, cp string) string
	}{
		{"not a commit ID", func(*Mirror, string) string { return "refs/heads/agent/42-x" }},
		{"unknown commit", func(*Mirror, string) string { return strings.Repeat("a", 40) }},
		// A regular commit on the checkpoint was pushed, and the lease is
		// it: the push would remove it with the checkpoint.
		{"removes a regular commit", func(m *Mirror, cp string) string {
			fold, _ := m.resolve(ctx, "refs/heads/agent/42-x")
			human, err := m.run(ctx, false, "-c", "user.name=t", "-c", "user.email=t@t", "commit-tree", cp+"^{tree}", "-p", cp, "-m", "Human fix")
			for _, ref := range []string{human, fold} {
				if err == nil {
					_, err = m.run(ctx, false, "update-ref", "refs/heads/agent/42-x", ref)
				}
				if err == nil && ref == human {
					err = m.Push(ctx, "agent/42-x", false)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			return human
		}},
		{"removes nothing", func(m *Mirror, _ string) string {
			sha, _ := m.resolve(ctx, "refs/heads/agent/42-x")
			return sha
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, origin, cp := leaseMirror(t)
			expect := tc.expect(m, cp)
			before := remoteRefs(t, origin)
			err := m.PushWithLease(ctx, "agent/42-x", expect, false)
			var ge *gitlab.GuardError
			if !errors.As(err, &ge) || errors.Is(err, ErrLeaseRejected) {
				t.Fatalf("want a *GuardError, got %v", err)
			}
			if after := remoteRefs(t, origin); after != before {
				t.Fatalf("the remote changed:\n%s\nwas\n%s", after, before)
			}
		})
	}
	// The policy applies as to every push.
	m, _, cp := leaseMirror(t)
	var ge *gitlab.GuardError
	if err := m.PushWithLease(ctx, "main", cp, false); !errors.As(err, &ge) {
		t.Fatalf("a lease push to main: %v", err)
	}
}

// hangingRemote is the URL of a remote that accepts connections and never
// answers. The remote commands' timeouts are 1s until the test ends.
func hangingRemote(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	for _, d := range []*time.Duration{&pushTimeout, &fetchTimeout, &lsRemoteTimeout, &gitWaitDelay} {
		old := *d
		*d = time.Second
		t.Cleanup(func() { *d = old })
	}
	return "http://" + ln.Addr().String() + "/x.git"
}

func TestMirrorRemoteCommandsTimeOut(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, want string
		run        func(m *Mirror, cp string) error
	}{
		{"push", "git push timed out after 1s", func(m *Mirror, _ string) error { return m.Push(ctx, "agent/42-x", true) }},
		{"lease push", "git push timed out after 1s", func(m *Mirror, cp string) error { return m.PushWithLease(ctx, "agent/42-x", cp, false) }},
		{"ls-remote", "git ls-remote timed out after 1s", func(m *Mirror, _ string) error { return m.Sync(ctx, m.remoteURL, "main") }},
		// Sync's fetch, which only runs after ls-remote answered.
		{"fetch", "git fetch timed out after 1s", func(m *Mirror, _ string) error {
			_, err := m.runRemote(ctx, fetchTimeout, "fetch", "--quiet", "--no-tags", "origin", "+refs/heads/main:refs/remotes/origin/main")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, cp := leaseMirror(t)
			url := hangingRemote(t)
			if _, err := m.run(ctx, false, "remote", "set-url", "origin", url); err != nil {
				t.Fatal(err)
			}
			m.remoteURL, m.Policy.URL = url, url
			start := time.Now()
			err := tc.run(m, cp)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
			// The timeout plus the wait for the pipes, with slack; a plain
			// push tries three times (retryOneOff).
			if took := time.Since(start); took > 20*time.Second {
				t.Fatalf("took %s", took)
			}
		})
	}
}

func TestMirrorRemoteFailureKeepsError(t *testing.T) {
	m, _, _ := guardedMirror(t)
	ctx := context.Background()
	_, err := m.runRemote(ctx, time.Minute, "fetch", "--quiet", "origin", "refs/heads/missing")
	if err == nil || strings.Contains(err.Error(), "timed out") || !strings.HasPrefix(err.Error(), "git fetch --quiet origin refs/heads/missing: ") {
		t.Fatalf("want the git error, got %v", err)
	}
	// A cancelled run is not a timeout either.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := m.runRemote(cancelled, time.Minute, "ls-remote", "origin"); err == nil || strings.Contains(err.Error(), "timed out") {
		t.Fatalf("want the cancellation, got %v", err)
	}
}

func TestMirrorLowSpeedAndTimeoutOnlyForRemote(t *testing.T) {
	m, _, _ := guardedMirror(t)
	ctx := context.Background()
	for key, want := range map[string]string{"http.lowSpeedLimit": "1000", "http.lowSpeedTime": "60"} {
		if got, err := m.runInput(ctx, true, "", "config", "--get", key); err != nil || got != want {
			t.Fatalf("remote command: %s = %q (%v), want %s", key, got, err, want)
		}
		if got, err := m.run(ctx, false, "config", "--get", key); err == nil {
			t.Fatalf("local command: %s = %q, want unset", key, got)
		}
	}
	// Local commands have no timeout: they work with the remote ones at 1ns.
	for _, d := range []*time.Duration{&pushTimeout, &fetchTimeout, &lsRemoteTimeout} {
		old := *d
		*d = time.Nanosecond
		t.Cleanup(func() { *d = old })
	}
	path := filepath.Join(t.TempDir(), "work.bundle")
	if _, err := m.run(ctx, false, "bundle", "create", "--quiet", path, "refs/heads/agent/42-x"); err != nil {
		t.Fatal(err)
	}
	if err := m.Import(ctx, path, "agent/42-x"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Bundle(ctx, path, nil, "main"); err != nil {
		t.Fatal(err)
	}
	if err := m.Push(ctx, "agent/42-x", false); err == nil || !strings.Contains(err.Error(), "git push timed out after 1ns") {
		t.Fatalf("a remote command at 1ns: %v", err)
	}
}
