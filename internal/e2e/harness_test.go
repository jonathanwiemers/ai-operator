package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/controller"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
	"github.com/jonathanwiemers/ai-operator/internal/runner"
)

const (
	ns          = "agents"
	projectName = "ai-operator"
	// The home project is the operator's own (--self-project), the
	// companion the infra repository Flux applies, and secrets a project
	// of the same group that the bot's token reaches but nothing may write.
	homeProject    = "jo.wiemers/ai-operator"
	infraProject   = "jo.wiemers/infra/cluster"
	secretsProject = "jo.wiemers/personal/kubernetes"
	allowedPaths   = "clusters/netcup01/applications/ai-operator/"
	valuesFile     = allowedPaths + "values.yaml"
	homeFile       = "internal/feature/companion.go"
	milestoneTitle = "Cross-repo e2e"
	milestoneID    = 21
	milestoneIID   = 7
)

// owner is the milestone's owner, mentioned on the companion.
var owner = gitlab.User{ID: 7, Username: "jo.wiemers"}

// clock is the fake time of the operator and the runners. Nothing sleeps:
// the test advances it.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// writes records the GitLab writes that move code (merge requests, merges,
// rebases, pipelines, branch deletes) of every project, in order, as
// "<project> <call> <ref>". Reads and comments pass through unrecorded.
type writes struct {
	gitlab.API
	mu    sync.Mutex
	calls []string
}

func (w *writes) record(project, call string, ref any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, fmt.Sprintf("%s %s %v", aiv1.NormalizeProject(project), call, ref))
}

func (w *writes) list() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.calls)
}

func (w *writes) CreateMergeRequest(ctx context.Context, project string, opts gitlab.CreateMergeRequestOptions) (*gitlab.MergeRequest, error) {
	mr, err := w.API.CreateMergeRequest(ctx, project, opts)
	if err == nil {
		w.record(project, "create", "!"+fmt.Sprint(mr.IID))
	}
	return mr, err
}

func (w *writes) MergeMergeRequest(ctx context.Context, project string, iid int64, opts gitlab.MergeOptions) (*gitlab.MergeRequest, error) {
	w.record(project, "merge", fmt.Sprintf("!%d@%s", iid, opts.SHA))
	return w.API.MergeMergeRequest(ctx, project, iid, opts)
}

func (w *writes) RebaseMergeRequest(ctx context.Context, project string, iid int64) error {
	w.record(project, "rebase", fmt.Sprintf("!%d", iid))
	return w.API.RebaseMergeRequest(ctx, project, iid)
}

func (w *writes) UpdateMergeRequest(ctx context.Context, project string, iid int64, opts gitlab.UpdateMergeRequestOptions) (*gitlab.MergeRequest, error) {
	if opts.StateEvent != "" {
		w.record(project, opts.StateEvent, fmt.Sprintf("!%d", iid))
	}
	return w.API.UpdateMergeRequest(ctx, project, iid, opts)
}

func (w *writes) DeleteBranch(ctx context.Context, project, branch string) error {
	w.record(project, "delete-branch", branch)
	return w.API.DeleteBranch(ctx, project, branch)
}

func (w *writes) CreatePipeline(ctx context.Context, project, ref string) error {
	w.record(project, "pipeline", ref)
	return w.API.CreatePipeline(ctx, project, ref)
}

func (w *writes) CreateMergeRequestPipeline(ctx context.Context, project string, iid int64) error {
	w.record(project, "mr-pipeline", fmt.Sprintf("!%d", iid))
	return w.API.CreateMergeRequestPipeline(ctx, project, iid)
}

// logs collects the log lines of the operator, the runners and the agent
// servers; a failed test prints the last ones.
type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) logger() logr.Logger {
	return funcr.New(func(prefix, args string) {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.lines = append(l.lines, prefix+" "+args)
	}, funcr.Options{Verbosity: 1})
}

// containing are the lines that contain part.
func (l *logs) containing(part string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, line := range l.lines {
		if strings.Contains(line, part) {
			out = append(out, line)
		}
	}
	return out
}

func (l *logs) tail(n int) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines[max(0, len(l.lines)-n):], "\n")
}

// env is the operator's AgentProject reconciler and one runner per agent
// task, on one fake Kubernetes API and a fake GitLab with three projects,
// the home and the companion each backed by a local bare git repository.
type env struct {
	t     *testing.T
	ctx   context.Context
	tmp   string
	clock *clock
	logs  *logs
	k8s   client.Client
	// gl is the fake GitLab behind writes, which both the operator and the
	// runners use.
	gl                    *gitlabfake.Projects
	home, infra, secrets  *gitlabfake.Fake
	api                   *writes
	homeOrigin, infraOrig string
	// infraMain is the companion's default branch head at the start.
	infraMain string
	r         *controller.AgentProjectReconciler
	engine    *engine
	// mergeLine: the milestone line has "merge".
	mergeLine bool
	runners   map[string]*runner.Runner
}

// scenario says how the milestone and the implementer behave.
type scenario struct {
	// merge puts "merge" on the milestone's repo line.
	merge bool
	// edit changes the companion checkout in the implementer's call; nil
	// edits values.yaml within the allowed paths.
	edit func(dir string)
}

func newEnv(t *testing.T, sc scenario) *env {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	start := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	e := &env{t: t, ctx: context.Background(), tmp: t.TempDir(), clock: &clock{now: start}, logs: &logs{}, runners: map[string]*runner.Runner{}, mergeLine: sc.merge}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("last log lines:\n%s", e.logs.tail(150))
		}
	})
	e.ctx = log.IntoContext(e.ctx, e.logs.logger().WithName("operator"))

	// The two repositories, one commit on main each.
	e.homeOrigin = e.bareRepo("home", map[string]string{"README.md": "ai-operator\n", "go.mod": "module example.com/op\n"})
	e.infraOrig = e.bareRepo("infra", map[string]string{
		valuesFile: "replicas: 1\n",
		"clusters/netcup01/applications/bloop/values.yaml": "replicas: 1\n",
		".gitlab-ci.yml": "stages: [build]\n",
	})
	e.infraMain = e.heads(e.infraOrig)["main"]

	home := gitlabfake.New()
	home.Clock = start
	home.Bot = gitlab.User{ID: 999, Username: "operator-ai"}
	home.Users = []gitlab.User{owner}
	home.Project = gitlab.Project{ID: 100, PathWithNamespace: homeProject, DefaultBranch: "main", HTTPURLToRepo: e.homeOrigin}
	milestone := "/agent autopilot\n/agent repo " + infraProject
	if sc.merge {
		milestone += " merge"
	}
	milestone += "\n/agent owners @" + owner.Username + "\n\nTurn the companion feature on in the cluster."
	home.Milestones = []gitlab.Milestone{{ID: milestoneID, IID: milestoneIID, Title: milestoneTitle, State: "active", Description: milestone}}
	e.gl = gitlabfake.NewProjects(home)
	e.home = home
	e.infra = e.gl.Add(infraProject)
	e.infra.Project.HTTPURLToRepo = e.infraOrig
	e.infra.Files = []string{valuesFile, "clusters/netcup01/applications/bloop/values.yaml", ".gitlab-ci.yml"}
	e.secrets = e.gl.Add(secretsProject)
	e.api = &writes{API: e.gl}

	e.engine = &engine{edit: sc.edit}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := aiv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	ref := corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "gitlab"}, Key: "token"}
	project := &aiv1.AgentProject{
		ObjectMeta: metav1.ObjectMeta{Name: projectName, Namespace: ns, UID: "uid-ai-operator"},
		Spec: aiv1.AgentProjectSpec{
			GitLab: aiv1.GitLabSpec{Project: homeProject, TokenSecretRef: ref},
			Agents: aiv1.AgentsSpec{Image: "agent:latest", CredentialsSecretName: "agent-creds"},
			CrossRepo: &aiv1.CrossRepoSpec{Targets: []aiv1.CrossRepoTarget{
				{Project: infraProject, Paths: []string{allowedPaths}, AutoMerge: true},
			}},
		},
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "gitlab", Namespace: ns}, Data: map[string][]byte{"token": []byte("glpat-test")}}
	e.k8s = fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, secret).
		WithStatusSubresource(&aiv1.AgentProject{}, &aiv1.AgentTask{}).Build()

	e.r = &controller.AgentProjectReconciler{
		Client: e.k8s, Scheme: scheme,
		NewGitLab:        func(string, string) gitlab.API { return e.api },
		Now:              e.clock.Now,
		CrossRepoAllowed: []string{infraProject},
		SelfProject:      aiv1.NormalizeProject(homeProject),
		Version:          "2026.11.4",
	}
	return e
}

// bareRepo creates a bare repository with files committed on main.
func (e *env) bareRepo(name string, files map[string]string) string {
	e.t.Helper()
	origin := filepath.Join(e.tmp, name+".git")
	seed := filepath.Join(e.tmp, name+"-seed")
	e.git(e.tmp, "init", "--bare", "--initial-branch=main", origin)
	e.git(e.tmp, "clone", origin, seed)
	for path, body := range files {
		writeFile(e.t, filepath.Join(seed, path), body)
	}
	e.git(seed, "add", ".")
	e.git(seed, "commit", "-m", "init")
	e.git(seed, "push", "origin", "HEAD:main")
	return origin
}

func (e *env) git(dir string, args ...string) string {
	e.t.Helper()
	out, err := git(dir, args...)
	if err != nil {
		e.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return out
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	b, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(b)), err
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// heads lists the branches of a bare repository with their commits.
func (e *env) heads(origin string) map[string]string {
	e.t.Helper()
	out := map[string]string{}
	for _, line := range strings.Split(e.git(e.tmp, "--git-dir", origin, "for-each-ref", "--format=%(refname:short) %(objectname)", "refs/heads"), "\n") {
		if name, sha, ok := strings.Cut(line, " "); ok {
			out[name] = sha
		}
	}
	return out
}

// reconcile is one poll of the operator, a minute after the last one.
func (e *env) reconcile() {
	e.t.Helper()
	e.clock.Advance(time.Minute)
	if _, err := e.r.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: projectName}}); err != nil {
		e.t.Fatal(err)
	}
	p := e.project()
	for _, c := range p.Status.Conditions {
		if c.Type == "Synced" && c.Status != metav1.ConditionTrue {
			e.t.Fatalf("sync failed: %s", c.Message)
		}
	}
}

func (e *env) project() *aiv1.AgentProject {
	e.t.Helper()
	var p aiv1.AgentProject
	if err := e.k8s.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: projectName}, &p); err != nil {
		e.t.Fatal(err)
	}
	return &p
}

func (e *env) tasks() []aiv1.AgentTask {
	e.t.Helper()
	var list aiv1.AgentTaskList
	if err := e.k8s.List(e.ctx, &list, client.InNamespace(ns)); err != nil {
		e.t.Fatal(err)
	}
	return list.Items
}

// task is the task of type typ (of the issue iid, for implement tasks), or nil.
func (e *env) task(typ aiv1.TaskType, iid int64) *aiv1.AgentTask {
	e.t.Helper()
	for _, t := range e.tasks() {
		if t.Spec.Type == typ && (typ != aiv1.TaskTypeImplement || t.Spec.Issue != nil && t.Spec.Issue.IID == iid) {
			return &t
		}
	}
	return nil
}

func (e *env) specTask() *aiv1.AgentTask {
	e.t.Helper()
	st := e.task(aiv1.TaskTypeSpec, 0)
	if st == nil {
		e.t.Fatal("no spec task")
	}
	return st
}

func (e *env) autopilot() *aiv1.AutopilotStatus {
	e.t.Helper()
	ap := e.specTask().Status.Autopilot
	if ap == nil {
		e.t.Fatal("no autopilot status")
	}
	return ap
}

// runner is the runner of an agent task, with its own agent server,
// mirror, state and exchange directories, like the two containers of the
// task's pod.
func (e *env) runner(task string) *runner.Runner {
	e.t.Helper()
	if r, ok := e.runners[task]; ok {
		return r
	}
	dir := filepath.Join(e.tmp, "pods", task)
	exchange := filepath.Join(dir, "exchange")
	agent := serveAgent(e.t, &runner.AgentServer{Log: e.logs.logger().WithName("agent"), Agent: &runner.LocalAgent{
		Workspace:  &runner.Workspace{Dir: filepath.Join(dir, "workspace", "repo"), AuthorName: "AI Operator", AuthorEmail: "ai@example.com"},
		Workspaces: filepath.Join(dir, "workspace"),
		Engines:    map[aiv1.Engine]runner.Engine{aiv1.EngineClaude: e.engine, aiv1.EngineCursor: e.engine},
		Exchange:   exchange,
	}})
	r := &runner.Runner{
		K8s: e.k8s, Namespace: ns, TaskName: task,
		GitLabToken: "glpat-test", CompanionToken: "glpat-test",
		NewGitLab: func(string, string) gitlab.API { return e.api },
		Agent:     agent,
		Mirror:    &runner.Mirror{Dir: filepath.Join(dir, "runner", "mirror.git"), Token: "glpat-test", AuthorName: "AI Operator", AuthorEmail: "ai@example.com"},
		StateDir:  filepath.Join(dir, "runner"),
		Exchange:  exchange,
		Log:       e.logs.logger().WithName("runner").WithValues("task", task),
		Now:       e.clock.Now,
	}
	e.runners[task] = r
	return r
}

// serveAgent starts server on a unix socket and returns a client for it.
func serveAgent(t *testing.T, server *runner.AgentServer) *runner.AgentClient {
	t.Helper()
	// Unix socket paths are limited to ~100 bytes; t.TempDir() can be longer.
	sockDir, err := os.MkdirTemp("", "e2e")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	if err := os.MkdirAll(server.Agent.Exchange, 0o755); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(sockDir, "agent.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := server.Serve(ctx, socket); err != nil {
			t.Errorf("agent server: %v", err)
		}
	}()
	t.Cleanup(func() { cancel(); <-done })
	agent := runner.NewAgentClient(socket)
	// The server listens once Serve has bound the socket; this waits on
	// that, not on time.
	for i := 0; agent.Ready(context.Background()) != nil; i++ {
		if i > 5000 {
			t.Fatal("agent server did not start")
		}
		time.Sleep(time.Millisecond)
	}
	return agent
}

// run runs the pending run request of a task, as its pod would.
func (e *env) run(task *aiv1.AgentTask) {
	e.t.Helper()
	if task == nil || !task.RunPending() {
		e.t.Fatalf("no run pending: %+v", task)
	}
	ran, err := e.runner(task.Name).Step(e.ctx)
	if err != nil || !ran {
		e.t.Fatalf("run of %s: ran=%v err=%v", task.Name, ran, err)
	}
	e.syncHeads()
}

// syncHeads plays GitLab's side of a push: every open merge request's head
// is its source branch's commit in the bare repository. A head the fake's
// rebase made ("<sha>-rebased") stays: that rebase does not touch git.
func (e *env) syncHeads() {
	e.t.Helper()
	for f, origin := range map[*gitlabfake.Fake]string{e.home: e.homeOrigin, e.infra: e.infraOrig} {
		heads := e.heads(origin)
		for _, mr := range f.MRs {
			if sha, ok := heads[mr.SourceBranch]; ok && mr.State == "opened" && !strings.HasSuffix(mr.SHA, "-rebased") {
				mr.SHA = sha
			}
		}
	}
}

// engine plays every agent role: the planner plans one issue with the
// companion, the implementer edits a home file and the companion's
// values.yaml, the reviewers find nothing, the describer describes and
// the pilot writes the retrospective.
type engine struct {
	mu    sync.Mutex
	calls []runner.Invocation
	edit  func(dir string)
}

// lastPrompt is the prompt of the last call of role, or "".
func (g *engine) lastPrompt(role string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i := len(g.calls) - 1; i >= 0; i-- {
		if g.calls[i].Role == role {
			return g.calls[i].Prompt
		}
	}
	return ""
}

func (g *engine) Run(_ context.Context, inv runner.Invocation) (*runner.Result, error) {
	g.mu.Lock()
	g.calls = append(g.calls, inv)
	g.mu.Unlock()
	out := func(v any) (*runner.Result, error) {
		b, err := json.Marshal(v)
		return &runner.Result{SessionID: "sess-" + inv.Role, CostUSD: 0.25, Structured: b}, err
	}
	switch inv.Role {
	case "spec":
		return out(runner.SpecPlan{Summary: "One issue with its companion.", Issues: []runner.PlannedItem{{
			Key: "turn-on", Title: "Turn on the companion feature", Profile: "strong", Repo: infraProject,
			Description: "Add the feature to the operator and enable it in " + valuesFile + ".",
		}}})
	case "implement":
		if len(inv.AddDirs) != 1 {
			return nil, fmt.Errorf("the implementer gets the companion checkout as its one extra directory, got %v", inv.AddDirs)
		}
		if err := os.MkdirAll(filepath.Join(inv.Dir, filepath.Dir(homeFile)), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(inv.Dir, homeFile), []byte("package feature\n\nconst Enabled = true\n"), 0o644); err != nil {
			return nil, err
		}
		if g.edit != nil {
			g.edit(inv.AddDirs[0])
		} else if err := os.WriteFile(filepath.Join(inv.AddDirs[0], valuesFile), []byte("replicas: 1\ncompanion:\n  enabled: true\n"), 0o644); err != nil {
			return nil, err
		}
		return out(runner.ImplementAnswer{Status: "done", Summary: "Added the feature and enabled it in the cluster.",
			CommitMessage: "Add the companion feature", Handoff: "Feature and values done."})
	case "describe":
		return out(runner.DescribeAnswer{Why: "The cluster needs the feature on.", What: "- the feature", How: "- `" + homeFile + "`",
			Verification: "- e2e test"})
	case "retrospective":
		return out(runner.RetroAnswer{Summary: "One issue, one companion; it landed after the release."})
	}
	if inv.ReadOnly {
		return out(runner.ReviewAnswer{Summary: "fine"}) // a reviewer: nothing found
	}
	return nil, fmt.Errorf("the scripted engine has no answer for role %q", inv.Role)
}
