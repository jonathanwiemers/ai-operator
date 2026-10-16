package runner

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

func TestClaudeArgs(t *testing.T) {
	inv := Invocation{Model: "claude-opus-5-5", Effort: "high", MaxBudgetUSD: "5", SystemPrompt: "sys",
		AllowedTools: []string{"Read", "Bash(git log:*)"}, JSONSchema: "{}", Resume: "s1"}
	args := strings.Join((&ClaudeCLI{}).Args(inv), " ")
	for _, want := range []string{"-p --output-format json", "--model claude-opus-5-5", "--effort high",
		"--max-budget-usd 5", "--append-system-prompt sys", "--allowedTools Read,Bash(git log:*)", "--json-schema {}", "--resume s1"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q miss %q", args, want)
		}
	}
}

func TestCursorFoldsInstructionsIntoPrompt(t *testing.T) {
	c := &CursorCLI{}
	inv := Invocation{SystemPrompt: "be careful", Prompt: "review this", JSONSchema: `{"type":"object"}`, ReadOnly: true, Model: "gpt-5"}
	p := c.Prompt(inv)
	for _, want := range []string{"<instructions>\nbe careful", "read-only", "review this", `{"type":"object"}`} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt misses %q:\n%s", want, p)
		}
	}
	args := c.Args(inv, p)
	if slices.Contains(args, "--force") {
		t.Error("read-only roles must not get --force")
	}
	if !slices.Contains(args, "gpt-5") {
		t.Error("model not passed")
	}
	inv.ReadOnly = false
	inv.Resume = "chat-1"
	if p := c.Prompt(inv); strings.Contains(p, "<instructions>") {
		t.Error("instructions are already in the resumed session")
	}
	if args := c.Args(inv, "x"); !slices.Contains(args, "--force") || !slices.Contains(args, "chat-1") {
		t.Errorf("unexpected args %v", args)
	}
}

func TestParseResultAndDecode(t *testing.T) {
	res, err := ParseResult([]byte(`{"type":"result","is_error":false,"session_id":"abc","total_cost_usd":0.42,"result":"done","structured_output":{"status":"done","summary":"s","commitMessage":"c","questions":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	var a ImplementAnswer
	if err := res.Decode(&a); err != nil || a.Status != "done" || res.SessionID != "abc" || res.CostUSD != 0.42 {
		t.Fatalf("unexpected %+v %+v %v", res, a, err)
	}

	// cursor-agent style: JSON in a fenced text result, log noise before it.
	res, err = ParseResult([]byte("some log line\n{\"type\":\"result\",\"session_id\":\"c1\",\"result\":\"Here you go:\\n```json\\n{\\\"summary\\\":\\\"ok\\\",\\\"findings\\\":[]}\\n```\"}"))
	if err != nil {
		t.Fatal(err)
	}
	var r ReviewAnswer
	if err := res.Decode(&r); err != nil || r.Summary != "ok" {
		t.Fatalf("decode: %+v %v", r, err)
	}
}

func TestScrubEnv(t *testing.T) {
	env := scrubEnv([]string{"GITLAB_TOKEN=x", "AI_TASK_NAME=t", "KUBERNETES_SERVICE_HOST=h", "CURSOR_API_KEY=k", "CLAUDE_CODE_OAUTH_TOKEN=o", "PATH=/bin"})
	joined := strings.Join(env, " ")
	for _, gone := range []string{"GITLAB_TOKEN", "AI_TASK_NAME", "KUBERNETES_SERVICE_HOST"} {
		if strings.Contains(joined, gone) {
			t.Errorf("%s must be scrubbed", gone)
		}
	}
	for _, kept := range []string{"CURSOR_API_KEY=k", "CLAUDE_CODE_OAUTH_TOKEN=o", "PATH=/bin"} {
		if !strings.Contains(joined, kept) {
			t.Errorf("%s must be kept", kept)
		}
	}
}

func TestApplyPlan(t *testing.T) {
	gl := gitlabfake.New()
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2"}
	gl.Milestones = []gitlab.Milestone{m}
	spec := aiv1.AgentProjectSpec{}.WithDefaults()
	ctx := context.Background()

	plan := SpecPlan{Issues: []PlannedItem{
		{Key: "ui", Title: "Checkout UI", Description: "Build the UI.", Profile: "fast", DependsOn: []string{"api"}},
		{Key: "api", Title: "Checkout API", Description: "Build the API.", Profile: "bogus", Questions: []string{"REST or gRPC? Default: REST"}},
	}}
	created, updated, skipped, err := applyPlan(ctx, gl, "g/p", &m, spec, plan, nil)
	if err != nil || created != 2 || updated != 0 || skipped != 0 {
		t.Fatalf("created=%d updated=%d skipped=%d err=%v", created, updated, skipped, err)
	}
	ui, api := gl.Issues[1], gl.Issues[2]
	if !ui.HasLabel("agent::draft") || !ui.HasLabel("ai-generated") || ui.Milestone == nil {
		t.Fatalf("unexpected issue %+v", ui)
	}
	if !ui.HasLabel("agent-profile::fast") || spec.ProfileOf(api.Labels) != "" {
		t.Fatalf("expected fast profile on ui and none (unknown choice) on api: %v / %v", ui.Labels, api.Labels)
	}
	if !strings.Contains(ui.Description, "- #2") {
		t.Fatalf("forward dependency should be resolved:\n%s", ui.Description)
	}
	if !strings.Contains(api.Description, "1. REST or gRPC?") || issueKey(api.Description) != "api" {
		t.Fatalf("questions / key missing:\n%s", api.Description)
	}

	// Re-plan: draft issues are updated, started ones are left alone, and a
	// profile a human chose is kept.
	gl.SetLabels(2, "agent::working")
	gl.SetLabels(1, "ai-generated", "agent::draft", "agent-profile::strong")
	issues, _ := gl.ListIssues(ctx, "g/p", gitlab.ListIssuesOptions{Milestone: m.Title, State: "all"})
	plan.Issues[0].Description = "Build the UI with dark mode."
	plan.Issues[1].Description = "Changed API plan."
	created, updated, skipped, err = applyPlan(ctx, gl, "g/p", &m, spec, plan, issues)
	if err != nil || created != 0 || updated != 1 || skipped != 1 {
		t.Fatalf("created=%d updated=%d skipped=%d err=%v", created, updated, skipped, err)
	}
	if !strings.Contains(gl.Issues[1].Description, "dark mode") || strings.Contains(gl.Issues[2].Description, "Changed") {
		t.Fatal("only the draft issue may change")
	}
	if l := gl.Labels(1); slices.Contains(l, "agent-profile::fast") || !slices.Contains(l, "agent-profile::strong") {
		t.Fatalf("manual profile override must be kept, labels %v", l)
	}
}

// scriptedEngine plays the implementer and the reviewers.
type scriptedEngine struct {
	mu          sync.Mutex
	calls       []Invocation
	reviewRound int
}

func (s *scriptedEngine) Run(_ context.Context, inv Invocation) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, inv)
	out := func(v any) (*Result, error) {
		b, _ := json.Marshal(v)
		return &Result{SessionID: "sess-impl", CostUSD: 0.5, Structured: b}, nil
	}
	if inv.ReadOnly {
		// Reviewer: one blocker in the first round, nothing afterwards. Also
		// misbehave by touching a file, which the runner must discard.
		_ = os.WriteFile(filepath.Join(inv.Dir, "reviewer-was-here"), []byte("x"), 0o644)
		if strings.Contains(inv.SystemPrompt, "security reviewer") {
			return out(ReviewAnswer{Summary: "fine"})
		}
		s.reviewRound++
		if s.reviewRound == 1 {
			return out(ReviewAnswer{Findings: []Finding{{Severity: "blocker", File: "paypal.go", Title: "nil deref", Detail: "crashes"}}})
		}
		return out(ReviewAnswer{Findings: []Finding{{Severity: "nit", File: "paypal.go", Title: "naming", Detail: "meh"}}})
	}
	if strings.Contains(inv.Prompt, "Automated reviewers found") {
		_ = os.WriteFile(filepath.Join(inv.Dir, "paypal.go"), []byte("package app // fixed\n"), 0o644)
		return out(ImplementAnswer{Status: "done", Summary: "Added PayPal, fixed nil deref.", CommitMessage: "Fix nil deref"})
	}
	_ = os.WriteFile(filepath.Join(inv.Dir, "paypal.go"), []byte("package app\n"), 0o644)
	return out(ImplementAnswer{Status: "done", Summary: "Added PayPal.", CommitMessage: "Add PayPal button"})
}

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

func TestImplementEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	// A "remote" repository with one commit on main.
	origin := filepath.Join(tmp, "origin.git")
	seed := filepath.Join(tmp, "seed")
	gitCmd(t, tmp, "init", "--bare", "--initial-branch=main", origin)
	gitCmd(t, tmp, "clone", origin, seed)
	_ = os.WriteFile(filepath.Join(seed, "README.md"), []byte("app\n"), 0o644)
	gitCmd(t, seed, "add", ".")
	gitCmd(t, seed, "commit", "-m", "init")
	gitCmd(t, seed, "push", "origin", "HEAD:main")

	gl := gitlabfake.New()
	gl.Project.HTTPURLToRepo = origin
	gl.AddIssue(42, "Add PayPal button", "agent::working")

	scheme := runtime.NewScheme()
	_ = aiv1.AddToScheme(scheme)
	project := &aiv1.AgentProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: "agents"},
		Spec: aiv1.AgentProjectSpec{
			GitLab: aiv1.GitLabSpec{Project: "group/app", TokenSecretRef: corev1.SecretKeySelector{Key: "t"}},
			Agents: aiv1.AgentsSpec{Image: "x"},
		},
		Status: aiv1.AgentProjectStatus{BotUserID: gl.Bot.ID},
	}
	task := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-42-add-paypal-button", Namespace: "agents"},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeImplement, RunRequest: 1,
			Issue: &aiv1.IssueRef{IID: 42, Title: "Add PayPal button"}},
	}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, task).
		WithStatusSubresource(&aiv1.AgentTask{}).Build()

	engine := &scriptedEngine{}
	r := newTestRunner(t, tmp, k8s, gl, engine, task.Name)

	ran, err := r.Step(context.Background())
	if err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}

	// The branch was pushed with the implementation and the review fix, but
	// not with the reviewer's stray file.
	files := gitCmd(t, tmp, "--git-dir", origin, "ls-tree", "--name-only", "agent/42-add-paypal-button")
	if !strings.Contains(files, "paypal.go") || strings.Contains(files, "reviewer-was-here") {
		t.Fatalf("unexpected files on branch: %s", files)
	}
	log := gitCmd(t, tmp, "--git-dir", origin, "log", "--format=%s", "agent/42-add-paypal-button")
	if !strings.Contains(log, "Add PayPal button") || !strings.Contains(log, "Fix nil deref") {
		t.Fatalf("unexpected history:\n%s", log)
	}

	// 1 implement + 2 reviewers, 1 fix + 2 reviewers.
	if len(engine.calls) != 6 {
		t.Fatalf("expected 6 engine calls, got %d", len(engine.calls))
	}
	// No profile label: agents.implement (strong model). Reviewers run on the light model.
	for _, c := range engine.calls {
		want := aiv1.DefaultModel
		if c.ReadOnly {
			want = aiv1.DefaultLightModel
		}
		if c.Model != want {
			t.Fatalf("call (readOnly=%v) used model %q, want %q", c.ReadOnly, c.Model, want)
		}
	}

	mr := gl.MRs[1]
	if mr == nil || mr.SourceBranch != "agent/42-add-paypal-button" || !strings.HasPrefix(mr.Title, "Draft: ") {
		t.Fatalf("unexpected MR %+v", mr)
	}
	if labels := gl.Labels(42); !slices.Equal(labels, []string{"agent::review"}) {
		t.Fatalf("issue should be in review, has %v", labels)
	}

	var got aiv1.AgentTask
	_ = k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: task.Name}, &got)
	run := got.Status.Run
	if run.ObservedRunRequest != 1 || run.RunCount != 1 || run.LastResult != aiv1.RunResultSucceeded ||
		run.MergeRequestIID != 1 || run.Branch != "agent/42-add-paypal-button" || run.CostUSD != "3.0000" {
		t.Fatalf("unexpected run status %+v", run)
	}

	// Meanwhile a human pushes a commit to the agent's branch.
	gitCmd(t, seed, "fetch", "origin")
	gitCmd(t, seed, "checkout", "-b", "agent/42-add-paypal-button", "origin/agent/42-add-paypal-button")
	_ = os.WriteFile(filepath.Join(seed, "HUMAN.md"), []byte("by hand\n"), 0o644)
	gitCmd(t, seed, "add", ".")
	gitCmd(t, seed, "commit", "-m", "Human tweak")
	gitCmd(t, seed, "push", "origin", "HEAD:agent/42-add-paypal-button")

	// A follow-up comment resumes the implementer session with just the delta.
	gl.AddNote(42, gitlab.User{ID: 1, Username: "jonathan"}, "/agent also log the payment id")
	orig := got.DeepCopy()
	got.Spec.RunRequest = 2
	if err := k8s.Patch(context.Background(), &got, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	engine.calls = nil
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := engine.calls[0]
	if first.Resume != "sess-impl" || !strings.Contains(first.Prompt, "also log the payment id") || strings.Contains(first.Prompt, "Implement issue #42") {
		t.Fatalf("follow-up should resume with only the new comment: resume=%q prompt=%q", first.Resume, first.Prompt)
	}
	// The second run built on the human's commit and pushed on top of it.
	log = gitCmd(t, tmp, "--git-dir", origin, "log", "--format=%s", "agent/42-add-paypal-button")
	if lines := strings.Split(log, "\n"); len(lines) < 5 || lines[1] != "Human tweak" {
		t.Fatalf("expected a new agent commit on top of the human commit:\n%s", log)
	}
}

// newTestRunner wires a runner to a real agent server over a unix socket, the
// same way the two containers of an agent pod are connected.
func newTestRunner(t *testing.T, tmp string, k8s client.Client, gl gitlab.API, engine Engine, task string) *Runner {
	t.Helper()
	// Unix socket paths are limited to ~100 bytes; t.TempDir() can be longer.
	sockDir, err := os.MkdirTemp("", "ai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	exchange := filepath.Join(tmp, "exchange")
	if err := os.MkdirAll(exchange, 0o755); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(sockDir, "agent.sock")
	server := &AgentServer{Agent: &LocalAgent{
		Workspace: &Workspace{Dir: filepath.Join(tmp, "agent", "repo"), AuthorName: "AI Operator", AuthorEmail: "ai@example.com"},
		Engines:   map[aiv1.Engine]Engine{aiv1.EngineClaude: engine},
		Exchange:  exchange,
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := server.Serve(ctx, socket); err != nil {
			t.Errorf("agent server: %v", err)
		}
	}()
	t.Cleanup(func() { cancel(); <-done })

	agent := NewAgentClient(socket)
	for i := 0; agent.Ready(context.Background()) != nil; i++ {
		if i > 100 {
			t.Fatal("agent server did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return &Runner{
		K8s: k8s, Namespace: "agents", TaskName: task,
		NewGitLab: func(string, string) gitlab.API { return gl },
		Agent:     agent,
		Mirror:    &Mirror{Dir: filepath.Join(tmp, "runner", "mirror.git"), Token: "secret-token"},
		StateDir:  filepath.Join(tmp, "runner"),
		Exchange:  exchange,
		Log:       logr.Discard(),
	}
}

func TestMirrorEnvIsMinimal(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "glpat-secret")
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	m := &Mirror{Dir: "/runner/mirror.git", Token: "glpat-secret"}
	local := strings.Join(m.env(false), "\n")
	if strings.Contains(local, "glpat-secret") || strings.Contains(local, "KUBERNETES") {
		t.Fatalf("local git commands must not see credentials:\n%s", local)
	}
	remote := strings.Join(m.env(true), "\n")
	if strings.Contains(remote, "GITLAB_TOKEN") || !strings.Contains(remote, "GIT_CONFIG_VALUE_0=Authorization: Basic ") {
		t.Fatalf("remote commands get the token only as an auth header:\n%s", remote)
	}
	for _, want := range []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"} {
		if !strings.Contains(local, want) {
			t.Errorf("missing %s", want)
		}
	}
}

func TestAgentRejectsPathsOutsideExchange(t *testing.T) {
	a := &LocalAgent{Workspace: &Workspace{Dir: t.TempDir()}, Exchange: "/exchange"}
	for _, p := range []string{"/runner/mirror.git/config", "/exchange/../runner/x", "/exchange"} {
		if _, err := a.Bundle(context.Background(), "b", "main", p); err == nil {
			t.Errorf("bundle path %q must be rejected", p)
		}
		if err := a.Prepare(context.Background(), PrepareRequest{Bundle: p}); err == nil {
			t.Errorf("prepare path %q must be rejected", p)
		}
	}
}

func TestSpecSchemaListsProfiles(t *testing.T) {
	schema := specSchema(aiv1.DefaultProfiles())
	var v map[string]any
	if err := json.Unmarshal([]byte(schema), &v); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	if !strings.Contains(schema, `"enum": ["fast","strong"]`) {
		t.Fatalf("profiles missing from schema:\n%s", schema)
	}
	prompt := buildSpecPrompt(&gitlab.Milestone{Title: "M", Description: "spec"}, nil, aiv1.DefaultProfiles(), false)
	if !strings.Contains(prompt, "- fast: Small") || !strings.Contains(prompt, "- strong: Everything else") {
		t.Fatalf("profiles missing from prompt:\n%s", prompt)
	}
}

type planEngine struct{ calls []Invocation }

func (p *planEngine) Run(_ context.Context, inv Invocation) (*Result, error) {
	p.calls = append(p.calls, inv)
	// The planner works on a checkout of the default branch.
	if _, err := os.Stat(filepath.Join(inv.Dir, "README.md")); err != nil {
		return nil, err
	}
	b, _ := json.Marshal(SpecPlan{Summary: "two steps", Issues: []PlannedItem{
		{Key: "api", Title: "API", Description: "Build the API.", Profile: "strong"},
		{Key: "ui", Title: "UI", Description: "Build the UI.", Profile: "fast", DependsOn: []string{"api"}},
	}})
	return &Result{SessionID: "sess-spec", Structured: b}, nil
}

func TestSpecEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin.git")
	seed := filepath.Join(tmp, "seed")
	gitCmd(t, tmp, "init", "--bare", "--initial-branch=main", origin)
	gitCmd(t, tmp, "clone", origin, seed)
	_ = os.WriteFile(filepath.Join(seed, "README.md"), []byte("app\n"), 0o644)
	gitCmd(t, seed, "add", ".")
	gitCmd(t, seed, "commit", "-m", "init")
	gitCmd(t, seed, "push", "origin", "HEAD:main")

	gl := gitlabfake.New()
	gl.Project.HTTPURLToRepo = origin
	gl.Milestones = []gitlab.Milestone{{ID: 11, IID: 3, Title: "Checkout v2", Description: "/agent spec\nBuild it."}}

	scheme := runtime.NewScheme()
	_ = aiv1.AddToScheme(scheme)
	project := &aiv1.AgentProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: "agents"},
		Spec:       aiv1.AgentProjectSpec{GitLab: aiv1.GitLabSpec{Project: "group/app"}},
	}
	task := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-spec-m3-checkout-v2", Namespace: "agents"},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeSpec, RunRequest: 1,
			Milestone: &aiv1.MilestoneRef{ID: 11, IID: 3, Title: "Checkout v2"}},
	}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, task).
		WithStatusSubresource(&aiv1.AgentTask{}).Build()

	engine := &planEngine{}
	r := newTestRunner(t, tmp, k8s, gl, engine, task.Name)
	if ran, err := r.Step(context.Background()); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if len(engine.calls) != 1 || !engine.calls[0].ReadOnly {
		t.Fatalf("expected one read-only planner call, got %+v", engine.calls)
	}
	api, ui := gl.Issues[1], gl.Issues[2]
	if api == nil || ui == nil || !api.HasLabel("agent-profile::strong") || !ui.HasLabel("agent-profile::fast") {
		t.Fatalf("planned issues missing or mislabelled: %+v %+v", api, ui)
	}
	var got aiv1.AgentTask
	_ = k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: task.Name}, &got)
	if got.Status.Run.LastResult != aiv1.RunResultSucceeded || !strings.Contains(got.Status.Run.Summary, "2 created") {
		t.Fatalf("unexpected run status %+v", got.Status.Run)
	}
}
