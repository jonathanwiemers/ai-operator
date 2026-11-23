package runner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

func TestCheckBranch(t *testing.T) {
	for _, ok := range []string{"main", "agent/739-runner-args", "release/2026.11", "feature_x-1"} {
		if err := checkBranch(ok); err != nil {
			t.Errorf("checkBranch(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "-", "--upload-pack=x", "-c core.sshCommand=x", "a..b", "a b", "a:b",
		"a~1", "a^", "a?", "a*", "a[", `a\b`, "a@{1}", "@", "/a", "a/", "a//b", "a.", ".a", "a/.b", "a.lock",
		"a/b.lock/c", "a\tb", "a\x7f"} {
		if err := checkBranch(bad); err == nil {
			t.Errorf("checkBranch(%q) = nil, want an error", bad)
		}
	}
}

// fakeGit puts a git on PATH that records each call's arguments (one per
// line, a call ends with "--end-of-call--") and succeeds without output. It
// returns a function reading the calls.
func fakeGit(t *testing.T) func() [][]string {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" --end-of-call-- >> '" + log + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil { // #nosec G306 -- a test's fake binary
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() [][]string {
		b, _ := os.ReadFile(log) // #nosec G304 -- the test's own file
		var calls [][]string
		for _, c := range strings.Split(string(b), "--end-of-call--\n") {
			if c != "" {
				calls = append(calls, strings.Split(strings.TrimSuffix(c, "\n"), "\n"))
			}
		}
		return calls
	}
}

// A branch named like an option never reaches git: the entry points refuse
// it before running anything.
func TestBranchLikeOptionNeverReachesGit(t *testing.T) {
	calls := fakeGit(t)
	ctx := context.Background()
	m := &Mirror{Dir: filepath.Join(t.TempDir(), "mirror"), Log: logr.Discard()}
	w := &Workspace{Dir: t.TempDir()}
	const evil = "--upload-pack=x"
	for name, err := range map[string]error{
		"Sync":        m.Sync(ctx, "https://gitlab.example/g/p.git", "main", evil),
		"Import":      m.Import(ctx, "/tmp/b.bundle", evil),
		"StartBranch": func() error { _, err := m.StartBranch(ctx, "agent/1-x", "-c core.sshCommand=x", "m"); return err }(),
		"Prepare":     w.Prepare(ctx, PrepareRequest{Branch: evil, Base: "main"}),
		"PrepareBase": w.Prepare(ctx, PrepareRequest{Branch: "agent/1-x", Base: "--initial-branch=x"}),
	} {
		if err == nil || !strings.Contains(err.Error(), "is not allowed") {
			t.Errorf("%s: err = %v, want the branch refused", name, err)
		}
	}
	if c := calls(); len(c) > 0 {
		t.Errorf("git ran %v", c)
	}
}

// Commit IDs the agent side gets over the socket are checked before git
// takes them as revisions.
func TestWorkspaceRefusesRevisionsThatAreNoCommitIDs(t *testing.T) {
	calls := fakeGit(t)
	ctx := context.Background()
	w := &Workspace{Dir: t.TempDir()}
	sha := strings.Repeat("a", 40)
	if _, err := w.Unfold(ctx, UnfoldRequest{Tip: sha, Fold: "--output=/tmp/x"}); err == nil {
		t.Error("Unfold took a fold that is not a commit ID")
	}
	if _, err := w.Unfold(ctx, UnfoldRequest{Tip: "-p", Fold: sha}); err == nil {
		t.Error("Unfold took a tip that is not a commit ID")
	}
	if _, err := w.FinishMerge(ctx, FinishMerge{From: "--hard", Abort: true}); err == nil {
		t.Error("FinishMerge took a From that is not a commit ID")
	}
	if c := calls(); len(c) > 0 {
		t.Errorf("git ran %v", c)
	}
}

// Refs and refspecs follow --end-of-options, so even a value that slipped
// past the checks is an operand: here a push and a workspace diff.
func TestGitRefsFollowEndOfOptions(t *testing.T) {
	calls := fakeGit(t)
	ctx := context.Background()
	m := &Mirror{Dir: t.TempDir(), Log: logr.Discard()}
	if err := m.pushArgs(ctx, true, "--force-with-lease=refs/heads/agent/x:"+strings.Repeat("b", 40), "refs/heads/agent/x:refs/heads/agent/x"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (&Workspace{Dir: t.TempDir()}).Diff(ctx, "-x"); err != nil {
		t.Fatal(err)
	}
	got := calls()
	if len(got) != 3 {
		t.Fatalf("calls = %v, want a push and two diffs", got)
	}
	for _, c := range got {
		end := slices.Index(c, "--end-of-options")
		if end < 0 || end != len(c)-2 && !(c[end+1] == "origin" && end == len(c)-3) {
			t.Errorf("call %q: the operands do not follow --end-of-options", c)
		}
	}
	if push := got[0]; !slices.Contains(push, "ci.skip") || !slices.Contains(push, "--force-with-lease=refs/heads/agent/x:"+strings.Repeat("b", 40)) {
		t.Errorf("push = %q, want ci.skip and the lease before --end-of-options", push)
	}
}

// An engine value that looks like a flag never reaches the CLI: the model,
// the effort, the session ID and the tools are checked before it starts.
func TestEngineValueLikeFlagNeverReachesCLI(t *testing.T) {
	dir := t.TempDir()
	ran := filepath.Join(dir, "ran")
	bin := filepath.Join(dir, "engine")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ntouch '"+ran+"'\n"), 0o755); err != nil { // #nosec G306 -- a test's fake binary
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, inv := range []Invocation{
		{Model: "--dangerously-skip-permissions"},
		{Model: "opus --dangerously-skip-permissions"},
		{Effort: "-x"},
		{Resume: "--dangerously-skip-permissions"},
		{AllowedTools: []string{"--dangerously-skip-permissions", "Read"}},
		{DisallowedTools: []string{"-x"}},
	} {
		inv.Dir = dir
		if _, err := (&ClaudeCLI{Binary: bin}).Run(ctx, inv); err == nil || !strings.Contains(err.Error(), "is not allowed") {
			t.Errorf("claude Run(%+v) err = %v, want refused", inv, err)
		}
		if _, err := (&ClaudeCLI{Binary: bin}).Start(ctx, inv); err == nil {
			t.Errorf("claude Start(%+v) started", inv)
		}
		if _, err := (&CursorCLI{Binary: bin}).Run(ctx, inv); err == nil {
			t.Errorf("cursor Run(%+v) ran", inv)
		}
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("the engine ran")
	}
	for _, ok := range []string{"claude-opus-5-5", "sonnet[1m]", "us.anthropic.claude-x-v1:0", "high", "0b9c7a3e-5d1f-4c2a-9e8b-1f2d3c4b5a69"} {
		if err := checkEngineArgs(Invocation{Model: ok, Effort: ok, Resume: ok, AllowedTools: []string{"Bash(go test:*)"}}); err != nil {
			t.Errorf("checkEngineArgs(%q) = %v", ok, err)
		}
	}
}

// cursor-agent takes the prompt as its last argument: a prompt that starts
// like an option reaches it as the operand.
func TestCursorPromptLikeFlagIsAnOperand(t *testing.T) {
	bin := fakeCursor(t)
	if _, err := (&CursorCLI{Binary: bin, Env: os.Environ()}).Run(context.Background(), Invocation{Dir: t.TempDir(), Prompt: "--force"}); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(filepath.Join(filepath.Dir(bin), "args")) // #nosec G304 -- the fake's own output
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSuffix(string(args), "\n"); !strings.HasSuffix(got, "--trust --force  --force") {
		t.Errorf("args = %q, want the prompt %q after the flags", got, " --force")
	}
}
