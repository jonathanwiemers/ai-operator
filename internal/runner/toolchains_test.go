package runner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestMirrorChangedPins(t *testing.T) {
	e := newImplementEnv(t)
	commit := func(branch, from string, files map[string]string, remove ...string) {
		t.Helper()
		gitCmd(t, e.seed, "checkout", "-B", branch, from)
		for name, body := range files {
			_ = os.MkdirAll(filepath.Dir(filepath.Join(e.seed, name)), 0o755)
			_ = os.WriteFile(filepath.Join(e.seed, name), []byte(body), 0o644)
		}
		for _, name := range remove {
			gitCmd(t, e.seed, "rm", "-q", name)
		}
		gitCmd(t, e.seed, "add", ".")
		gitCmd(t, e.seed, "commit", "-m", branch)
		gitCmd(t, e.seed, "push", "-q", "origin", "HEAD:"+branch)
	}
	commit("main", "main", map[string]string{".mise.toml": "[tools]\ngo = \"1.26.0\"\n", "rust-toolchain.toml": "[toolchain]\n"})
	commit("same", "main", map[string]string{"app.go": "package app\n", "web/.mise.toml": "[tools]\n"})
	commit("bump", "main", map[string]string{".mise.toml": "[tools]\ngo = \"1.27.0\"\n"})
	commit("drop", "main", nil, "rust-toolchain.toml")
	commit("gone", "main", map[string]string{".tool-versions": "nodejs 22\n"})
	// main moves on with a pin change of its own: not the branches'.
	commit("main", "main", map[string]string{".tool-versions": "nodejs 24\n"})

	m := &Mirror{Dir: filepath.Join(e.tmp, "mirror.git")}
	ctx := context.Background()
	if err := m.Sync(ctx, e.origin, "main", "same", "bump", "drop", "gone"); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, e.seed, "push", "-q", "origin", "--delete", "gone")
	if err := m.Sync(ctx, e.origin, "main", "gone"); err != nil {
		t.Fatal(err)
	}
	for branch, want := range map[string][]string{
		"bump": {".mise.toml"},
		"drop": {"rust-toolchain.toml"},
		"same": nil, // a pin below the root is no pin
		"gone": nil, // deleted on the remote
		"new":  nil, // never pushed
	} {
		if got := m.ChangedPins(ctx, "main", branch); !slices.Equal(got, want) {
			t.Errorf("%s: got %q, want %q", branch, got, want)
		}
	}
}

// envClaude writes a fake claude that answers with its toolchain variables.
func envClaude(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "claude")
	script := `#!/bin/sh
cat >/dev/null
printf '{"type":"result","subtype":"success","is_error":false,"result":"%s|%s|%s","session_id":"s"}\n' "$MISE_DATA_DIR" "$RUSTUP_HOME" "$CARGO_HOME"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestAgentRunsWithPerRunToolchains(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	root := filepath.Join(tmp, "ai-toolchains")
	earlier := filepath.Join(root, "shop-42-1", "rustup")
	if err := os.MkdirAll(earlier, 0o755); err != nil {
		t.Fatal(err)
	}
	// The image's shared directories.
	env := []string{"PATH=" + os.Getenv("PATH"), "MISE_DATA_DIR=/shared/mise", "RUSTUP_HOME=/shared/rustup", "CARGO_HOME=/shared/cargo"}
	a := &LocalAgent{Workspace: &Workspace{Dir: filepath.Join(tmp, "repo")},
		Engines: map[aiv1.Engine]Engine{aiv1.EngineClaude: &ClaudeCLI{Binary: envClaude(t), Env: env}}}
	ctx := context.Background()

	res, err := a.Run(ctx, aiv1.EngineClaude, Invocation{PerRunToolchains: "shop-42-2"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "shop-42-2")
	if want := strings.Join([]string{dir + "/mise", dir + "/rustup", dir + "/cargo"}, "|"); res.Text != want {
		t.Fatalf("the CLI must see the per-run directories:\n got %s\nwant %s", res.Text, want)
	}
	for _, d := range []string{"mise", "rustup", "cargo"} {
		if fi, err := os.Stat(filepath.Join(dir, d)); err != nil || !fi.IsDir() {
			t.Fatalf("%s not created: %v", d, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "shop-42-1")); !os.IsNotExist(err) {
		t.Fatalf("an earlier run's toolchains must be removed: %v", err)
	}

	for _, name := range []string{"../x", "x/../../etc", "Shop", "-x", ".", "x y"} {
		if _, err := a.Run(ctx, aiv1.EngineClaude, Invocation{PerRunToolchains: name}); err == nil {
			t.Errorf("name %q must be refused", name)
		}
	}
	if _, err := os.Stat(filepath.Join(tmp, "x")); !os.IsNotExist(err) {
		t.Fatalf("a refused name must not create anything: %v", err)
	}

	// A run without changed pins uses the shared directories, and the last
	// run's toolchains go.
	if res, err = a.Run(ctx, aiv1.EngineClaude, Invocation{}); err != nil || res.Text != "/shared/mise|/shared/rustup|/shared/cargo" {
		t.Fatalf("want the shared directories, got %v %v", res, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the per-run toolchains of the last run must be removed: %v", err)
	}
}

func TestAgentServerTakesNoEnvFromTheRunner(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	env := []string{"PATH=" + os.Getenv("PATH"), "MISE_DATA_DIR=/shared/mise", "RUSTUP_HOME=/shared/rustup", "CARGO_HOME=/shared/cargo"}
	agent := serveAgent(t, &AgentServer{Agent: &LocalAgent{Workspace: &Workspace{Dir: filepath.Join(tmp, "repo")}, Exchange: filepath.Join(tmp, "exchange"),
		Engines: map[aiv1.Engine]Engine{aiv1.EngineClaude: &ClaudeCLI{Binary: envClaude(t), Env: env}}}})
	res, err := agent.Run(context.Background(), aiv1.EngineClaude, Invocation{Env: []string{"MISE_DATA_DIR=/evil", "PATH=/evil"}})
	if err != nil || res.Text != "/shared/mise|/shared/rustup|/shared/cargo" {
		t.Fatalf("env from the runner must not reach the CLI: %v %v", res, err)
	}
	res, err = agent.Run(context.Background(), aiv1.EngineClaude, Invocation{PerRunToolchains: "t-1"})
	if dir := filepath.Join(tmp, "ai-toolchains", "t-1"); err != nil || !strings.HasPrefix(res.Text, dir+"/mise|") {
		t.Fatalf("the name must cross the socket: %v %v", res, err)
	}
}

// recordingEngine records the invocations an engine gets on the agent side.
type recordingEngine struct {
	Engine
	mu    sync.Mutex
	calls []Invocation
}

func (e *recordingEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	e.mu.Lock()
	e.calls = append(e.calls, inv)
	e.mu.Unlock()
	return e.Engine.Run(ctx, inv)
}

func TestRunWithBranchChangedPinsUsesPerRunToolchains(t *testing.T) {
	// Short: the agent socket goes below TMPDIR too.
	tmpDir, err := os.MkdirTemp("", "tc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	t.Setenv("TMPDIR", tmpDir)
	e := newImplementEnv(t)
	engine := &recordingEngine{Engine: steadyEngine{}}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	ctx := context.Background()

	// The first run starts the branch: no pin changed, shared directories.
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(engine.calls) == 0 {
		t.Fatal("no agent calls")
	}
	for _, c := range engine.calls {
		if c.PerRunToolchains != "" || c.Env != nil {
			t.Fatalf("a branch without pin changes uses the shared directories: %q %q", c.PerRunToolchains, c.Env)
		}
	}
	if strings.Contains(engine.calls[0].SystemPrompt, "Your branch changes the toolchain pins") {
		t.Fatal("no per-run note without changed pins")
	}

	// A human bumps the pin on the branch.
	branch := "agent/42-add-paypal-button"
	gitCmd(t, e.seed, "fetch", "origin", branch)
	gitCmd(t, e.seed, "checkout", "-b", branch, "origin/"+branch)
	_ = os.WriteFile(filepath.Join(e.seed, ".mise.toml"), []byte("[tools]\ngo = \"1.27.0\"\n"), 0o644)
	gitCmd(t, e.seed, "add", ".")
	gitCmd(t, e.seed, "commit", "-m", "bump go")
	gitCmd(t, e.seed, "push", "origin", branch)

	var got aiv1.AgentTask
	if err := e.k8s.Get(ctx, client.ObjectKeyFromObject(e.task), &got); err != nil {
		t.Fatal(err)
	}
	orig := got.DeepCopy()
	got.Spec.RunRequest = 2
	if err := e.k8s.Patch(ctx, &got, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	engine.calls = nil
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(engine.calls) < 2 {
		t.Fatalf("want the implementer and the reviewers, got %d calls", len(engine.calls))
	}
	dir := filepath.Join(tmpDir, "ai-toolchains", "shop-42-add-paypal-button-2")
	want := []string{"MISE_DATA_DIR=" + dir + "/mise", "RUSTUP_HOME=" + dir + "/rustup", "CARGO_HOME=" + dir + "/cargo"}
	for _, c := range engine.calls {
		if c.PerRunToolchains != "shop-42-add-paypal-button-2" || !slices.Equal(c.Env, want) {
			t.Fatalf("every call of the run needs the per-run directories (role %q): %q %q", c.Role, c.PerRunToolchains, c.Env)
		}
	}
	if p := engine.calls[0].SystemPrompt; !strings.Contains(p, "Your branch changes the toolchain pins in .mise.toml compared with main.") {
		t.Fatalf("the implementer must be told:\n%s", p)
	}
	if !slices.ContainsFunc(r.run.steps, func(s *step) bool { return strings.Contains(s.result, "changes .mise.toml") }) {
		t.Fatal("the run log should say the branch changes a pin")
	}
}

func TestToolchainsRunName(t *testing.T) {
	for id, want := range map[string]string{
		"shop-42-add-paypal-button#2":   "shop-42-add-paypal-button-2",
		"a.b#10":                        "a-b-10",
		"#3":                            "3",
		strings.Repeat("x", 120) + "#7": strings.Repeat("x", 98) + "-7",
	} {
		got := toolchainsRunName(id)
		if got != want || !toolchainsName.MatchString(got) {
			t.Errorf("%q: got %q, want %q", id, got, want)
		}
	}
}
