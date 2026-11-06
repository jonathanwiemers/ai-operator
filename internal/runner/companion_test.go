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
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// TestAgentServerTreePaths: the agent server works only in the home
// checkout (repo "") and in <root>/repos/<valid slug>; every other repo is
// refused by every call on a tree, and merging the base is home-only.
func TestAgentServerTreePaths(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "workspace")
	exchange := filepath.Join(tmp, "exchange")
	local := &LocalAgent{Workspace: &Workspace{Dir: filepath.Join(root, "repo")}, Workspaces: root, Exchange: exchange}
	client := serveAgent(t, &AgentServer{Agent: local, Log: logr.Discard()})
	ctx := context.Background()

	for _, repo := range []string{homeTree, "jo.wiemers-infra-cluster"} {
		if _, err := client.RemoteRefs(ctx, repo); err != nil {
			t.Fatalf("repo %q must be accepted: %v", repo, err)
		}
	}
	if w, err := local.tree("jo.wiemers-infra-cluster"); err != nil || w.Dir != filepath.Join(root, "repos", "jo.wiemers-infra-cluster") {
		t.Fatalf("a slug is <root>/repos/<slug>: %v %v", w, err)
	}
	if w, _ := local.tree(homeTree); w.Dir != filepath.Join(root, "repo") {
		t.Fatalf("the home tree is the workspace's: %s", w.Dir)
	}

	bundle := filepath.Join(exchange, "x.bundle")
	for _, repo := range []string{"../x", "/etc", "a/b", "UPPER", ".git", " ", "/", "..", "repo/../../x"} {
		calls := map[string]error{}
		_, calls["refs"] = client.RemoteRefs(ctx, repo)
		calls["prepare"] = client.Prepare(ctx, PrepareRequest{Repo: repo, Branch: "agent/x", Base: "main"})
		_, calls["commit"] = client.Commit(ctx, repo, "msg")
		_, _, calls["diff"] = client.Diff(ctx, repo, "main")
		calls["reset"] = client.Reset(ctx, repo)
		_, calls["bundle"] = client.Bundle(ctx, BundleRequest{Repo: repo, Branch: "agent/x", Base: "main", Path: bundle})
		_, calls["merge-base"] = client.MergeBase(ctx, MergeRequest{Repo: repo, Base: "main", Message: "m"})
		_, calls["finish-merge"] = client.FinishMerge(ctx, FinishMerge{Repo: repo, Base: "main", Abort: true})
		for call, err := range calls {
			if err == nil {
				t.Errorf("%s with repo %q must be refused", call, repo)
			}
		}
	}
	// A valid slug, but merging the base has no companion use.
	if _, err := client.MergeBase(ctx, MergeRequest{Repo: "jo.wiemers-infra-cluster", Base: "main", Message: "m"}); err == nil ||
		!strings.Contains(err.Error(), "home checkout only") {
		t.Fatalf("merge-base must refuse a companion: %v", err)
	}
	// Nothing was created outside the root, and only the accepted slug's
	// directory (none: refs creates nothing) inside it.
	entries, _ := os.ReadDir(tmp)
	for _, e := range entries {
		if e.Name() != "workspace" && e.Name() != "exchange" {
			t.Fatalf("created %s outside the workspace root", e.Name())
		}
	}

	// Without a workspace root, companions are refused.
	noRoot := &LocalAgent{Workspace: &Workspace{Dir: filepath.Join(root, "repo")}, Exchange: exchange}
	if _, err := noRoot.RemoteRefs(ctx, "jo.wiemers-infra-cluster"); err == nil {
		t.Fatal("without Workspaces a companion must be refused")
	}
}

// TestRunnerCompanionMirrorPath: the companion mirror lives at
// <state>/mirrors/<slug>.git with the companion token; invalid slugs are
// refused.
func TestRunnerCompanionMirrorPath(t *testing.T) {
	r := &Runner{StateDir: "/runner", CompanionToken: "companion-token", Mirror: &Mirror{AuthorName: "Bot", AuthorEmail: "bot@x"}, Log: logr.Discard()}
	policy := &PushPolicy{Project: "jo.wiemers/infra/cluster", DefaultBranch: "main"}
	m, err := r.CompanionMirror("jo.wiemers-infra-cluster", policy)
	if err != nil || m.Dir != "/runner/mirrors/jo.wiemers-infra-cluster.git" || m.Token != "companion-token" || m.Policy != policy || m.AuthorName != "Bot" {
		t.Fatalf("mirror %+v, %v", m, err)
	}
	for _, slug := range []string{"", "../x", "a/b", "UPPER", ".git"} {
		if _, err := r.CompanionMirror(slug, policy); err == nil {
			t.Errorf("slug %q must be refused", slug)
		}
	}
}

// companionEngine is the scripted engine that also edits the companion
// checkout in the implementer's call.
type companionEngine struct {
	scriptedEngine
	dir string
}

func (c *companionEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	if !inv.ReadOnly && inv.JSONSchema != describeSchema && !strings.Contains(inv.Prompt, "Automated reviewers found") {
		_ = os.MkdirAll(filepath.Join(c.dir, "clusters", "app"), 0o755)
		_ = os.WriteFile(filepath.Join(c.dir, "clusters", "app", "paypal.yaml"), []byte("paypal: true\n"), 0o644)
	}
	return c.scriptedEngine.Run(ctx, inv)
}

type companionEnv struct {
	*implementEnv
	infraOrigin string
	infraSeed   string
	gl          *gitlabfake.Projects
}

const companionBranch = "agent/app-42-add-paypal-button"

// newCompanionEnv is newImplementEnv with a second bare remote, the
// companion group/infra, and the task's spec.companion on it.
func newCompanionEnv(t *testing.T) *companionEnv {
	t.Helper()
	e := newImplementEnv(t)
	origin, seed := filepath.Join(e.tmp, "infra.git"), filepath.Join(e.tmp, "infra-seed")
	gitCmd(t, e.tmp, "init", "--bare", "--initial-branch=main", origin)
	gitCmd(t, e.tmp, "clone", origin, seed)
	_ = os.MkdirAll(filepath.Join(seed, "clusters", "app"), 0o755)
	_ = os.WriteFile(filepath.Join(seed, "clusters", "app", "kustomization.yaml"), []byte("resources: []\n"), 0o644)
	gitCmd(t, seed, "add", ".")
	gitCmd(t, seed, "commit", "-m", "infra init")
	gitCmd(t, seed, "push", "origin", "HEAD:main")

	gl := gitlabfake.NewProjects(e.gl)
	infra := gl.Add("group/infra")
	infra.Project.HTTPURLToRepo = origin

	var task aiv1.AgentTask
	ctx := context.Background()
	if err := e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task); err != nil {
		t.Fatal(err)
	}
	task.Spec.Companion = &aiv1.CompanionSpec{Project: "group/infra", Paths: []string{"clusters/app/"}, Branch: companionBranch}
	if err := e.k8s.Update(ctx, &task); err != nil {
		t.Fatal(err)
	}
	return &companionEnv{implementEnv: e, infraOrigin: origin, infraSeed: seed, gl: gl}
}

func TestImplementPreparesAndImportsTheCompanion(t *testing.T) {
	e := newCompanionEnv(t)
	companionDir := CompanionDir(filepath.Join(e.tmp, "agent"), "group-infra")
	engine := &companionEngine{dir: companionDir}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	r.CompanionToken = "companion-token"

	if ran, err := r.Step(context.Background()); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	for _, s := range r.run.steps {
		if s.failed {
			t.Fatalf("step %q failed", s.name)
		}
	}
	steps := stepNames(r.run)
	for _, want := range []string{"read companion project", "clone companion", "prepare companion checkout", "commit companion", "import companion"} {
		if !slices.Contains(steps, want) {
			t.Fatalf("missing step %q in %v", want, steps)
		}
	}

	// Both trees were prepared: the companion checkout is on the companion
	// branch, started from the companion's default branch (it is new).
	if got := gitCmd(t, companionDir, "rev-parse", "--abbrev-ref", "HEAD"); got != companionBranch {
		t.Fatalf("companion checkout on %q", got)
	}
	if _, err := os.Stat(filepath.Join(companionDir, "clusters", "app", "kustomization.yaml")); err != nil {
		t.Fatalf("the companion checkout has the default branch's files: %v", err)
	}
	if got := gitCmd(t, filepath.Join(e.tmp, "agent", "repo"), "rev-parse", "--abbrev-ref", "HEAD"); got != "agent/42-add-paypal-button" {
		t.Fatalf("home checkout on %q", got)
	}

	// The companion's commit is in the companion mirror, not on its remote.
	mirror := filepath.Join(e.tmp, "runner", "mirrors", "group-infra.git")
	files := gitCmd(t, e.tmp, "--git-dir", mirror, "ls-tree", "-r", "--name-only", "refs/heads/"+companionBranch)
	if !strings.Contains(files, "clusters/app/paypal.yaml") || !strings.Contains(files, "clusters/app/kustomization.yaml") {
		t.Fatalf("companion mirror branch files:\n%s", files)
	}
	if msg := gitCmd(t, e.tmp, "--git-dir", mirror, "log", "-1", "--format=%s", "refs/heads/"+companionBranch); !strings.Contains(msg, "Add PayPal button") {
		t.Fatalf("companion commit message %q", msg)
	}
	if heads := gitCmd(t, e.tmp, "--git-dir", e.infraOrigin, "for-each-ref", "--format=%(refname)", "refs/heads/"); heads != "refs/heads/main" {
		t.Fatalf("the companion must not be pushed in this run: %s", heads)
	}
	// The home change went its usual way, without the companion's file.
	home := gitCmd(t, e.tmp, "--git-dir", e.origin, "ls-tree", "-r", "--name-only", "agent/42-add-paypal-button")
	if !strings.Contains(home, "paypal.go") || strings.Contains(home, "paypal.yaml") {
		t.Fatalf("home branch files:\n%s", home)
	}
	// The bundles of the companion used their own names and were removed.
	if left, _ := filepath.Glob(filepath.Join(e.tmp, "exchange", "*.bundle")); len(left) > 0 {
		t.Fatalf("bundles left in the exchange: %v", left)
	}
}

func TestImplementCompanionStartsOnItsExistingBranch(t *testing.T) {
	e := newCompanionEnv(t)
	// Someone pushed the companion branch already.
	gitCmd(t, e.infraSeed, "checkout", "-b", companionBranch)
	_ = os.WriteFile(filepath.Join(e.infraSeed, "clusters", "app", "earlier.yaml"), []byte("x: 1\n"), 0o644)
	gitCmd(t, e.infraSeed, "add", ".")
	gitCmd(t, e.infraSeed, "commit", "-m", "earlier companion work")
	gitCmd(t, e.infraSeed, "push", "origin", companionBranch)
	pushed := gitCmd(t, e.infraSeed, "rev-parse", "HEAD")

	companionDir := CompanionDir(filepath.Join(e.tmp, "agent"), "group-infra")
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, &companionEngine{dir: companionDir}, e.task.Name)
	r.CompanionToken = "companion-token"
	if ran, err := r.Step(context.Background()); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if _, err := os.Stat(filepath.Join(companionDir, "clusters", "app", "earlier.yaml")); err != nil {
		t.Fatalf("the checkout must start on origin/%s: %v", companionBranch, err)
	}
	mirror := filepath.Join(e.tmp, "runner", "mirrors", "group-infra.git")
	if parent := gitCmd(t, e.tmp, "--git-dir", mirror, "rev-parse", "refs/heads/"+companionBranch+"^"); parent != pushed {
		t.Fatalf("the companion commit must follow the pushed branch: parent %s, want %s", parent, pushed)
	}
	diff, stat, err := r.companionDiff(context.Background(), &companionTree{slug: "group-infra", base: "main"})
	if err != nil || !strings.Contains(diff, "paypal.yaml") || !strings.Contains(diff, "earlier.yaml") || stat == "" {
		t.Fatalf("companion diff against its default branch: %v\n%s", err, diff)
	}
}

// TestImplementWithoutCompanionTokenLeavesItOut: a pod without the
// companion token (its target is no longer valid) runs the home change
// only and says so in the report.
func TestImplementWithoutCompanionTokenLeavesItOut(t *testing.T) {
	e := newCompanionEnv(t)
	companionDir := CompanionDir(filepath.Join(e.tmp, "agent"), "group-infra")
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, &companionEngine{dir: companionDir}, e.task.Name)
	if ran, err := r.Step(context.Background()); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	for _, s := range stepNames(r.run) {
		if strings.Contains(s, "companion") {
			t.Fatalf("no companion step without its token: %v", stepNames(r.run))
		}
	}
	if _, err := os.Stat(filepath.Join(e.tmp, "runner", "mirrors")); err == nil {
		t.Fatal("no companion mirror without its token")
	}
	var found bool
	for _, notes := range e.implementEnv.gl.MRNotes {
		for _, n := range notes {
			found = found || strings.Contains(n.Body, "`group/infra` is left out of this run")
		}
	}
	for _, notes := range e.implementEnv.gl.IssueNotes {
		for _, n := range notes {
			found = found || strings.Contains(n.Body, "`group/infra` is left out of this run")
		}
	}
	if !found {
		t.Fatal("the run's report must say the companion was left out")
	}
}
