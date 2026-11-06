package runner

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// stepGateRepo is a repository with one committed file of 10 lines.
func stepGateRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitCmd(t, dir, "init", "--quiet", "--initial-branch=main")
	writeFile(t, dir, "app.go", strings.Repeat("x\n", 10))
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "--quiet", "-m", "init")
	return dir
}

func stepGateHook(t *testing.T, sessionID, tool, cwd string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"session_id": sessionID, "cwd": cwd, "tool_name": tool,
		"hook_event_name": "PostToolUse", "tool_input": map[string]string{"command": "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// runStepGate runs the gate and returns its output and error.
func runStepGate(t *testing.T, stdin string, maxLines int, stateDir string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := StepGate(strings.NewReader(stdin), &out, maxLines, stateDir)
	return out.String(), err
}

func TestStepGate(t *testing.T) {
	nudge := `{"hookSpecificOutput":{"additionalContext":"Your uncommitted diff is 14 lines (step limit 10). ` +
		`Finish this step: get it to build, then return in_progress with its commitMessage.","hookEventName":"PostToolUse"}}` + "\n"
	for _, tc := range []struct {
		name string
		// setup changes the repository and returns the hook input.
		setup   func(t *testing.T, repo string) string
		want    string
		wantErr bool
	}{
		{"clean tree", func(t *testing.T, repo string) string {
			return stepGateHook(t, "s1", "Edit", repo)
		}, "", false},
		{"under the limit", func(t *testing.T, repo string) string {
			writeFile(t, repo, "new.go", strings.Repeat("y\n", 10))
			return stepGateHook(t, "s1", "Write", repo)
		}, "", false},
		// app.go: 2 lines changed (+2/−2), new.go +10: 14 lines.
		{"over the limit", func(t *testing.T, repo string) string {
			writeFile(t, repo, "app.go", "a\nb\n"+strings.Repeat("x\n", 8))
			writeFile(t, repo, "new.go", strings.Repeat("y\n", 10))
			return stepGateHook(t, "s1", "Bash", repo)
		}, nudge, false},
		{"over the limit from a subdirectory", func(t *testing.T, repo string) string {
			writeFile(t, repo, "app.go", "a\nb\n"+strings.Repeat("x\n", 8))
			writeFile(t, repo, "new.go", strings.Repeat("y\n", 10))
			if err := os.Mkdir(filepath.Join(repo, "sub"), 0o755); err != nil {
				t.Fatal(err)
			}
			return stepGateHook(t, "s1", "Edit", filepath.Join(repo, "sub"))
		}, nudge, false},
		{"read tool ignored", func(t *testing.T, repo string) string {
			writeFile(t, repo, "new.go", strings.Repeat("y\n", 50))
			return stepGateHook(t, "s1", "Read", repo)
		}, "", false},
		{"subagent call ignored", func(t *testing.T, repo string) string {
			writeFile(t, repo, "new.go", strings.Repeat("y\n", 50))
			return `{"session_id":"s1","cwd":"` + repo + `","tool_name":"Edit","agent_id":"a1"}`
		}, "", false},
		{"untracked binary file not counted", func(t *testing.T, repo string) string {
			writeFile(t, repo, "logo.png", "\x89PNG\x00"+strings.Repeat("\n", 50))
			return stepGateHook(t, "s1", "Write", repo)
		}, "", false},
		{"malformed stdin", func(t *testing.T, repo string) string {
			writeFile(t, repo, "new.go", strings.Repeat("y\n", 50))
			return `{"session_id":`
		}, "", true},
		{"missing git repo", func(t *testing.T, repo string) string {
			dir := t.TempDir()
			writeFile(t, dir, "new.go", strings.Repeat("y\n", 50))
			return stepGateHook(t, "s1", "Edit", dir)
		}, "", true},
		{"unsafe session id", func(t *testing.T, repo string) string {
			writeFile(t, repo, "new.go", strings.Repeat("y\n", 50))
			return stepGateHook(t, "../s1", "Edit", repo)
		}, "", true},
		{"no cwd", func(t *testing.T, repo string) string {
			return stepGateHook(t, "s1", "Edit", "")
		}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := stepGateRepo(t)
			got, err := runStepGate(t, tc.setup(t, repo), 10, t.TempDir())
			if (err != nil) != tc.wantErr {
				t.Fatalf("error %v, want error %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("output %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStepGateCadence: over 12 calls above the limit the gate nudges at
// calls 1, 6 and 11, and the state file counts the nudges.
func TestStepGateCadence(t *testing.T) {
	repo, state := stepGateRepo(t), t.TempDir()
	writeFile(t, repo, "new.go", strings.Repeat("y\n", 50))
	var nudged []int
	for call := 1; call <= 12; call++ {
		got, err := runStepGate(t, stepGateHook(t, "sess-1", "Edit", repo), 10, state)
		if err != nil {
			t.Fatalf("call %d: %v", call, err)
		}
		if got != "" {
			if !strings.Contains(got, "Your uncommitted diff is 50 lines (step limit 10).") {
				t.Fatalf("call %d: output %q", call, got)
			}
			nudged = append(nudged, call)
		}
	}
	if want := []int{1, 6, 11}; !slices.Equal(nudged, want) {
		t.Fatalf("nudges at calls %v, want %v", nudged, want)
	}
	if n := StepGateNudges(state, "sess-1"); n != 3 {
		t.Fatalf("StepGateNudges %d, want 3", n)
	}
	data, err := os.ReadFile(filepath.Join(state, "step-gate-sess-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"callsSinceNudge":1,"nudges":3}`; string(data) != want {
		t.Fatalf("state file %s, want %s", data, want)
	}
	// Another session has its own state, and ignored tools do not count.
	if n := StepGateNudges(state, "other"); n != 0 {
		t.Fatalf("StepGateNudges of another session %d, want 0", n)
	}
	if got, err := runStepGate(t, stepGateHook(t, "other", "Read", repo), 10, state); got != "" || err != nil {
		t.Fatalf("Read: output %q, error %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(state, "step-gate-other.json")); !os.IsNotExist(err) {
		t.Fatalf("Read wrote state: %v", err)
	}
}

// TestStepGateBelowLimitCountsCalls: calls under the limit count for the
// cadence, so the first nudge after a closed step can come at once.
func TestStepGateBelowLimitCountsCalls(t *testing.T) {
	repo, state := stepGateRepo(t), t.TempDir()
	writeFile(t, repo, "new.go", strings.Repeat("y\n", 50))
	call := func() string {
		t.Helper()
		got, err := runStepGate(t, stepGateHook(t, "s", "Bash", repo), 10, state)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if call() == "" {
		t.Fatal("first call over the limit: no nudge")
	}
	gitCmd(t, repo, "add", ".")
	gitCmd(t, repo, "commit", "--quiet", "-m", "step")
	for i := 0; i < stepGateEvery; i++ {
		if got := call(); got != "" {
			t.Fatalf("clean tree: %q", got)
		}
	}
	writeFile(t, repo, "more.go", strings.Repeat("z\n", 20))
	if call() == "" {
		t.Fatal("over the limit after 5 calls: no nudge")
	}
	if n := StepGateNudges(state, "s"); n != 2 {
		t.Fatalf("StepGateNudges %d, want 2", n)
	}
}

func TestStepGateFailsOpenOnState(t *testing.T) {
	repo := stepGateRepo(t)
	writeFile(t, repo, "new.go", strings.Repeat("y\n", 50))
	hook := stepGateHook(t, "s", "Edit", repo)

	// A state directory that does not exist: nothing can be saved.
	missing := filepath.Join(t.TempDir(), "missing")
	if got, err := runStepGate(t, hook, 10, missing); got != "" || err == nil {
		t.Fatalf("unwritable state: output %q, error %v", got, err)
	}
	// A corrupt state file.
	state := t.TempDir()
	writeFile(t, state, "step-gate-s.json", "{")
	if got, err := runStepGate(t, hook, 10, state); got != "" || err == nil {
		t.Fatalf("corrupt state: output %q, error %v", got, err)
	}
	if n := StepGateNudges(state, "s"); n != 0 {
		t.Fatalf("StepGateNudges of a corrupt state %d, want 0", n)
	}
	// A limit that is not positive.
	if got, err := runStepGate(t, hook, 0, t.TempDir()); got != "" || err == nil {
		t.Fatalf("max lines 0: output %q, error %v", got, err)
	}
}

func TestStepGateDefaultStateDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	repo := stepGateRepo(t)
	writeFile(t, repo, "new.go", strings.Repeat("y\n", 50))
	if got, err := runStepGate(t, stepGateHook(t, "d", "Write", repo), 10, ""); got == "" || err != nil {
		t.Fatalf("output %q, error %v", got, err)
	}
	if n := StepGateNudges("", "d"); n != 1 {
		t.Fatalf("StepGateNudges %d, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(tmp, "step-gate-d.json")); err != nil {
		t.Fatal(err)
	}
}
