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
	wf := aiv1.WorkflowSpec{}.WithDefaults()
	ctx := context.Background()

	plan := SpecPlan{Issues: []PlannedItem{
		{Key: "ui", Title: "Checkout UI", Description: "Build the UI.", DependsOn: []string{"api"}},
		{Key: "api", Title: "Checkout API", Description: "Build the API.", Questions: []string{"REST or gRPC? Default: REST"}},
	}}
	created, updated, skipped, err := applyPlan(ctx, gl, "g/p", &m, wf, plan, nil)
	if err != nil || created != 2 || updated != 0 || skipped != 0 {
		t.Fatalf("created=%d updated=%d skipped=%d err=%v", created, updated, skipped, err)
	}
	ui, api := gl.Issues[1], gl.Issues[2]
	if !ui.HasLabel("agent::draft") || !ui.HasLabel("ai-generated") || ui.Milestone == nil {
		t.Fatalf("unexpected issue %+v", ui)
	}
	if !strings.Contains(ui.Description, "- #2") {
		t.Fatalf("forward dependency should be resolved:\n%s", ui.Description)
	}
	if !strings.Contains(api.Description, "1. REST or gRPC?") || issueKey(api.Description) != "api" {
		t.Fatalf("questions / key missing:\n%s", api.Description)
	}

	// Re-plan: draft issues are updated, started ones are left alone.
	gl.SetLabels(2, "agent::working")
	issues, _ := gl.ListIssues(ctx, "g/p", gitlab.ListIssuesOptions{Milestone: m.Title, State: "all"})
	plan.Issues[0].Description = "Build the UI with dark mode."
	plan.Issues[1].Description = "Changed API plan."
	created, updated, skipped, err = applyPlan(ctx, gl, "g/p", &m, wf, plan, issues)
	if err != nil || created != 0 || updated != 1 || skipped != 1 {
		t.Fatalf("created=%d updated=%d skipped=%d err=%v", created, updated, skipped, err)
	}
	if !strings.Contains(gl.Issues[1].Description, "dark mode") || strings.Contains(gl.Issues[2].Description, "Changed") {
		t.Fatal("only the draft issue may change")
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
	r := &Runner{
		K8s: k8s, Namespace: "agents", TaskName: task.Name,
		Workspace:  filepath.Join(tmp, "workspace"),
		NewGitLab:  func(string, string) gitlab.API { return gl },
		Engines:    map[aiv1.Engine]Engine{aiv1.EngineClaude: engine},
		AuthorName: "AI Operator", AuthorEmail: "ai@example.com",
		Log: logr.Discard(),
	}

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
}
