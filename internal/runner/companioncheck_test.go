package runner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/pathspec"
)

const allowedTree = "clusters/my-cluster/applications/ai-operator"

// companionRepo is a work clone whose main is the mirror's origin/main;
// the cases commit on top of it and hand the result to the mirror.
type companionRepo struct {
	t          *testing.T
	work       string
	mirror     *Mirror
	baseCommit string
}

func newCompanionRepo(t *testing.T) *companionRepo {
	t.Helper()
	tmp := t.TempDir()
	work, mirror := filepath.Join(tmp, "work"), filepath.Join(tmp, "mirror.git")
	gitCmd(t, tmp, "init", "--initial-branch=main", work)
	gitCmd(t, tmp, "init", "--bare", mirror)
	c := &companionRepo{t: t, work: work, mirror: &Mirror{Dir: mirror, Log: logr.Discard()}}
	c.write("README.md", "infra\n")
	c.write(allowedTree+"/x", "x: 1\n")
	c.write("docs/old.md", "old\n")
	c.commit("init")
	c.baseCommit = gitCmd(t, work, "rev-parse", "HEAD")
	gitCmd(t, work, "push", "--quiet", mirror, "HEAD:refs/remotes/origin/main")
	gitCmd(t, work, "checkout", "--quiet", "-b", "agent/x")
	return c
}

func (c *companionRepo) write(path, content string) {
	c.t.Helper()
	full := filepath.Join(c.work, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		c.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		c.t.Fatal(err)
	}
}

func (c *companionRepo) commit(msg string) string {
	c.t.Helper()
	gitCmd(c.t, c.work, "add", "-A")
	gitCmd(c.t, c.work, "commit", "--quiet", "--allow-empty", "-m", msg)
	return gitCmd(c.t, c.work, "rev-parse", "HEAD")
}

// head hands the branch to the mirror (as the import does) and returns it.
func (c *companionRepo) head() string {
	c.t.Helper()
	gitCmd(c.t, c.work, "push", "--quiet", "--force", c.mirror.Dir, "HEAD:refs/heads/agent/x")
	return "refs/heads/agent/x"
}

func TestCheckCompanion(t *testing.T) {
	paths, err := pathspec.Compile([]string{allowedTree + "/"})
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("a", int(companionMaxBytes)+1)
	half := strings.Repeat("b", int(companionMaxBytes)/2+1)
	for _, tc := range []struct {
		name string
		// change commits on agent/x and returns the commit the violation
		// names, if any.
		change func(c *companionRepo) string
		// path and reason are what the violation says; "" passes.
		path, reason string
		// netDiff: the violation is the net diff's, without a commit.
		netDiff bool
	}{
		{name: "allowed", change: func(c *companionRepo) string {
			c.write(allowedTree+"/release.yaml", "version: 1\n")
			c.write(allowedTree+"/x", "x: 2\n")
			c.commit("allowed")
			c.write(allowedTree+"/more/y.yaml", "y: 1\n")
			c.commit("more")
			return ""
		}},
		{name: "allowed executable", change: func(c *companionRepo) string {
			c.write(allowedTree+"/run.sh", "#!/bin/sh\n")
			_ = os.Chmod(filepath.Join(c.work, allowedTree, "run.sh"), 0o755)
			c.commit("script")
			return ""
		}},
		{name: "no change", change: func(*companionRepo) string { return "" }},
		{name: "outside", path: "clusters/other/y.yaml", reason: "outside the allowed paths", change: func(c *companionRepo) string {
			c.write("clusters/other/y.yaml", "y: 1\n")
			return c.commit("outside")
		}},
		{name: "delete outside", path: "docs/old.md", reason: "outside the allowed paths", change: func(c *companionRepo) string {
			gitCmd(c.t, c.work, "rm", "--quiet", "docs/old.md")
			return c.commit("delete")
		}},
		{name: ".gitlab-ci.yml", path: ".gitlab-ci.yml", reason: "CI configuration", change: func(c *companionRepo) string {
			c.write(".gitlab-ci.yml", "stages: []\n")
			return c.commit("ci")
		}},
		{name: ".gitlab-ci.yml inside the allowed tree", path: allowedTree + "/.gitlab-ci.yml", reason: "CI configuration", change: func(c *companionRepo) string {
			c.write(allowedTree+"/.gitlab-ci.yml", "stages: []\n")
			return c.commit("ci")
		}},
		{name: ".gitlab directory", path: allowedTree + "/.gitlab/ci.yml", reason: "CI configuration", change: func(c *companionRepo) string {
			c.write(allowedTree+"/.gitlab/ci.yml", "stages: []\n")
			return c.commit("ci")
		}},
		{name: ".gitmodules", path: allowedTree + "/.gitmodules", reason: ".gitmodules", change: func(c *companionRepo) string {
			c.write(allowedTree+"/.gitmodules", "[submodule \"x\"]\n")
			return c.commit("modules")
		}},
		{name: "symlink inside the allowed tree", path: allowedTree + "/link", reason: "adds a symlink", change: func(c *companionRepo) string {
			if err := os.Symlink("../../../../README.md", filepath.Join(c.work, allowedTree, "link")); err != nil {
				c.t.Fatal(err)
			}
			return c.commit("link")
		}},
		{name: "file turned into a symlink", path: allowedTree + "/x", reason: "adds a symlink", change: func(c *companionRepo) string {
			_ = os.Remove(filepath.Join(c.work, allowedTree, "x"))
			if err := os.Symlink("/etc/passwd", filepath.Join(c.work, allowedTree, "x")); err != nil {
				c.t.Fatal(err)
			}
			return c.commit("link")
		}},
		{name: "submodule", path: allowedTree + "/sub", reason: "adds a submodule", change: func(c *companionRepo) string {
			gitCmd(c.t, c.work, "update-index", "--add", "--cacheinfo", "160000,"+c.baseCommit+","+allowedTree+"/sub")
			gitCmd(c.t, c.work, "commit", "--quiet", "-m", "submodule")
			return gitCmd(c.t, c.work, "rev-parse", "HEAD")
		}},
		{name: "rename out of the allowed tree", path: "clusters/other/x", reason: "outside the allowed paths", change: func(c *companionRepo) string {
			_ = os.MkdirAll(filepath.Join(c.work, "clusters", "other"), 0o755)
			gitCmd(c.t, c.work, "mv", allowedTree+"/x", "clusters/other/x")
			return c.commit("rename")
		}},
		{name: "rename into the allowed tree", path: "docs/old.md", reason: "outside the allowed paths", change: func(c *companionRepo) string {
			gitCmd(c.t, c.work, "mv", "docs/old.md", allowedTree+"/old.md")
			return c.commit("rename")
		}},
		{name: "added, then removed in a later commit", path: "clusters/other/z", reason: "outside the allowed paths", change: func(c *companionRepo) string {
			c.write(allowedTree+"/ok.yaml", "ok: 1\n")
			c.commit("fine")
			c.write("clusters/other/z", "z\n")
			bad := c.commit("outside")
			gitCmd(c.t, c.work, "rm", "--quiet", "clusters/other/z")
			c.commit("removed again")
			return bad
		}},
		{name: "merge commit", reason: "merge commit", change: func(c *companionRepo) string {
			gitCmd(c.t, c.work, "checkout", "--quiet", "-b", "side", "main")
			c.write(allowedTree+"/side.yaml", "side: 1\n")
			c.commit("side")
			gitCmd(c.t, c.work, "checkout", "--quiet", "agent/x")
			c.write(allowedTree+"/main.yaml", "main: 1\n")
			c.commit("work")
			gitCmd(c.t, c.work, "merge", "--quiet", "--no-ff", "-m", "merge side", "side")
			return gitCmd(c.t, c.work, "rev-parse", "HEAD")
		}},
		{name: "size cap", reason: "more than the cap of 1048576 bytes", netDiff: true, change: func(c *companionRepo) string {
			c.write(allowedTree+"/dump.yaml", big)
			c.commit("dump")
			return ""
		}},
		{name: "size cap over several files", reason: "more than the cap", netDiff: true, change: func(c *companionRepo) string {
			c.write(allowedTree+"/a.yaml", half)
			c.write(allowedTree+"/b.yaml", half+"b")
			c.commit("two halves")
			return ""
		}},
		{name: "size cap counts blobs a later commit replaced", reason: "more than the cap", netDiff: true, change: func(c *companionRepo) string {
			c.write(allowedTree+"/dump.yaml", big)
			c.commit("dump")
			c.write(allowedTree+"/dump.yaml", "small\n")
			c.commit("shrink")
			return ""
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newCompanionRepo(t)
			commit := tc.change(c)
			err := c.mirror.CheckCompanion(context.Background(), "main", c.head(), paths, companionMaxBytes)
			if tc.reason == "" {
				if err != nil {
					t.Fatalf("must pass: %v", err)
				}
				return
			}
			v, ok := AsCompanionViolation(err)
			if !ok {
				t.Fatalf("expected a *CompanionViolation, got %v", err)
			}
			if v.Path != tc.path || !strings.Contains(v.Reason, tc.reason) {
				t.Fatalf("violation %q / %q, want %q / %q", v.Path, v.Reason, tc.path, tc.reason)
			}
			if !tc.netDiff && v.Commit != commit {
				t.Fatalf("violation names commit %q, want %q", v.Commit, commit)
			}
			msg := v.Error()
			if tc.path != "" && !strings.Contains(msg, tc.path) || !strings.Contains(msg, "Neither repository was pushed") {
				t.Fatalf("message %q", msg)
			}
		})
	}
}

// TestCheckCompanionStartsAtTheMergeBase: commits the default branch got
// since the companion branch started (outside the allowed paths) are not
// part of the range, and a branch without common history is refused.
func TestCheckCompanionStartsAtTheMergeBase(t *testing.T) {
	paths, _ := pathspec.Compile([]string{allowedTree + "/"})
	ctx := context.Background()
	c := newCompanionRepo(t)
	c.write(allowedTree+"/ok.yaml", "ok: 1\n")
	c.commit("work")
	head := c.head()
	// main moves on with a change outside the allowed paths.
	gitCmd(t, c.work, "checkout", "--quiet", "main")
	c.write("clusters/other/new.yaml", "n: 1\n")
	c.commit("main moved")
	gitCmd(t, c.work, "push", "--quiet", "--force", c.mirror.Dir, "HEAD:refs/remotes/origin/main")
	if err := c.mirror.CheckCompanion(ctx, "main", head, paths, companionMaxBytes); err != nil {
		t.Fatalf("the default branch's own commits are not the companion's: %v", err)
	}

	gitCmd(t, c.work, "checkout", "--quiet", "--orphan", "unrelated")
	c.write(allowedTree+"/ok.yaml", "ok: 1\n")
	c.commit("unrelated")
	gitCmd(t, c.work, "push", "--quiet", "--force", c.mirror.Dir, "HEAD:refs/heads/agent/x")
	if _, ok := AsCompanionViolation(c.mirror.CheckCompanion(ctx, "main", "refs/heads/agent/x", paths, companionMaxBytes)); !ok {
		t.Fatal("a branch without common history must be refused")
	}
	if err := c.mirror.CheckCompanion(ctx, "nope", "refs/heads/agent/x", paths, companionMaxBytes); err == nil {
		t.Fatal("a missing default branch must fail the check")
	}
	if err := c.mirror.CheckCompanion(ctx, "main", "refs/heads/missing", paths, companionMaxBytes); err == nil {
		t.Fatal("a missing head must fail the check")
	}
}

// remoteHeads lists the branches of a bare remote.
func remoteHeads(t *testing.T, dir, remote string) string {
	t.Helper()
	return gitCmd(t, dir, "--git-dir", remote, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/")
}

// TestImplementCompanionViolationPushesNeitherTree: a companion change
// outside the allowed paths fails the run before the home push. The home
// branch holds only the start commit of its merge request, the companion
// remote has no branch, and the failure report on the issue names the
// path and the reason.
func TestImplementCompanionViolationPushesNeitherTree(t *testing.T) {
	for _, tc := range []struct {
		name, path, reason string
		edit               func(dir string)
	}{
		{name: "outside", path: "clusters/other/x.yaml", reason: "outside the allowed paths", edit: func(dir string) {
			_ = os.MkdirAll(filepath.Join(dir, "clusters", "other"), 0o755)
			_ = os.WriteFile(filepath.Join(dir, "clusters", "other", "x.yaml"), []byte("x: 1\n"), 0o644)
		}},
		{name: "ci config", path: ".gitlab-ci.yml", reason: "CI configuration", edit: func(dir string) {
			_ = os.WriteFile(filepath.Join(dir, ".gitlab-ci.yml"), []byte("stages: []\n"), 0o644)
		}},
		{name: "symlink", path: "clusters/app/link", reason: "adds a symlink", edit: func(dir string) {
			_ = os.Symlink("/etc/passwd", filepath.Join(dir, "clusters", "app", "link"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCompanionEnv(t)
			companionDir := CompanionDir(filepath.Join(e.tmp, "agent"), "group-infra")
			r := newTestRunner(t, e.tmp, e.k8s, e.gl, &companionEngine{dir: companionDir, edit: tc.edit}, e.task.Name)
			r.CompanionToken = "companion-token"
			infraBefore := remoteHeads(t, e.tmp, e.infraOrigin)

			if _, err := r.Step(context.Background()); err != nil {
				t.Fatal(err)
			}
			var task aiv1.AgentTask
			_ = e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
			if task.Status.Run.LastResult != aiv1.RunResultFailed {
				t.Fatalf("the run must fail, got %q: %s", task.Status.Run.LastResult, task.Status.Run.Summary)
			}
			if task.Status.Run.PushedSHA != "" {
				t.Fatalf("nothing is pushed: %s", task.Status.Run.PushedSHA)
			}
			// The home branch has only the start commit of the draft MR, not
			// the agent's work.
			if log := e.originLog(t, "agent/42-add-paypal-button"); len(log) != 2 || !strings.HasPrefix(log[0], "Start work on #42") {
				t.Fatalf("the home branch must not get the run's commit: %q", log)
			}
			if after := remoteHeads(t, e.tmp, e.infraOrigin); after != infraBefore || strings.Contains(after, companionBranch) {
				t.Fatalf("the companion remote changed:\n%s\n---\n%s", infraBefore, after)
			}
			var failure string
			for _, n := range e.implementEnv.gl.IssueNotes[42] {
				if strings.Contains(n.Body, "The agent run failed") {
					failure = n.Body
				}
			}
			if !strings.Contains(failure, tc.path) || !strings.Contains(failure, tc.reason) || !strings.Contains(failure, "Neither repository was pushed") {
				t.Fatalf("the failure report must name the path and the reason:\n%s", failure)
			}
		})
	}
}

// stepCompanionEngine is stepEngine that also adds a file outside the
// companion's allowed paths in the first step.
type stepCompanionEngine struct {
	*stepEngine
	dir string
}

func (s *stepCompanionEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	if !inv.ReadOnly {
		_ = os.WriteFile(filepath.Join(s.dir, "outside.yaml"), []byte("x: 1\n"), 0o644)
	}
	return s.stepEngine.Run(ctx, inv)
}

// TestCompanionViolationStopsStepPushes: a step push checks the companion
// too; a violation fails the run there instead of pushing the step, and no
// later step runs.
func TestCompanionViolationStopsStepPushes(t *testing.T) {
	e := newCompanionEnv(t)
	companionDir := CompanionDir(filepath.Join(e.tmp, "agent"), "group-infra")
	steps := &stepEngine{gl: e.implementEnv.gl, lastChanges: true}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, &stepCompanionEngine{stepEngine: steps, dir: companionDir}, e.task.Name)
	r.CompanionToken = "companion-token"
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	var task aiv1.AgentTask
	_ = e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	if task.Status.Run.LastResult != aiv1.RunResultFailed || !strings.Contains(task.Status.Run.Summary, "outside.yaml") {
		t.Fatalf("the run must fail on the step's companion change: %q %s", task.Status.Run.LastResult, task.Status.Run.Summary)
	}
	if steps.n != 1 {
		t.Fatalf("no step may run after the violation, %d ran", steps.n)
	}
	if log := e.originLog(t, "agent/42-add-paypal-button"); len(log) != 2 {
		t.Fatalf("the step must not be pushed: %q", log)
	}
	if heads := remoteHeads(t, e.tmp, e.infraOrigin); strings.Contains(heads, companionBranch) {
		t.Fatalf("the companion remote must have no branch: %s", heads)
	}
	if !slices.Contains(stepNames(r.run), "push step 1") {
		t.Fatalf("the violation is found at the step push: %v", stepNames(r.run))
	}
}
