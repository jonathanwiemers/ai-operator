package runner

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
)

// These tests pin that a claude CLI which keeps running after its answer (a
// pending wakeup, #504) is ended after answerGrace and the answer is used.

const keptRunning = "agent kept running after it answered"

// runGrace runs ClaudeCLI against a fake claude script with a short grace and
// returns the result, the error, the log and how long Run took.
func runGrace(t *testing.T, script string) (*Result, error, string, time.Duration) {
	t.Helper()
	old := answerGrace
	answerGrace = 50 * time.Millisecond
	t.Cleanup(func() { answerGrace = old })
	var mu sync.Mutex
	var logs strings.Builder
	log := funcr.New(func(prefix, args string) {
		mu.Lock()
		defer mu.Unlock()
		logs.WriteString(prefix + " " + args + "\n")
	}, funcr.Options{})
	ctx := logr.NewContext(context.Background(), log)
	start := time.Now()
	res, err := (&ClaudeCLI{Binary: sessionFake(t, script), Env: os.Environ()}).Run(ctx, Invocation{Dir: t.TempDir()})
	took := time.Since(start)
	mu.Lock()
	defer mu.Unlock()
	return res, err, logs.String(), took
}

// lineFile writes a stream line for a fake to cat.
func lineFile(t *testing.T, line string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "line.jsonl")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClaudeRunEndsCLIAfterAnswerGrace(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "sleep.pid")
	t.Cleanup(func() { // the orphaned sleep still holds the pipes' write ends
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	// The sleep is a child, not exec'd: it keeps stdout open after the kill.
	script := "#!/bin/sh\ncat " + lineFile(t, answerLine) + "\nsleep 30 &\necho $! > " + pidFile + "\nwait\n"
	res, err, logs, took := runGrace(t, script)
	if err != nil {
		t.Fatalf("Run = %v, want the answer", err)
	}
	if took > 5*time.Second {
		t.Errorf("Run took %v, want it ended after the grace", took)
	}
	var got struct{ Status string }
	if err := res.Decode(&got); err != nil || got.Status != "done" {
		t.Fatalf("Decode = %+v, %v; want the answer", got, err)
	}
	if res.CostUSD != 1.0 {
		t.Errorf("CostUSD = %v, want 1.0", res.CostUSD)
	}
	if !strings.Contains(logs, keptRunning) || !strings.Contains(logs, `"grace"="50ms"`) {
		t.Errorf("log %q, want %q with the grace", logs, keptRunning)
	}
}

func TestClaudeRunFailsOnErrorBeforeGraceKill(t *testing.T) {
	script := "#!/bin/sh\ncat " + lineFile(t, answerLine) + "\ncat " + lineFile(t, laterErrorLine) + "\nexec sleep 30\n"
	_, err, logs, _ := runGrace(t, script)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the error after the answer", err)
	}
	if !strings.Contains(logs, keptRunning) {
		t.Errorf("log %q, want %q", logs, keptRunning)
	}
}

func TestClaudeRunExitAfterAnswerNotKilled(t *testing.T) {
	_, err, logs, _ := runGrace(t, "#!/bin/sh\ncat "+lineFile(t, answerLine)+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs, keptRunning) {
		t.Errorf("log %q, want no kill for a CLI that exits", logs)
	}
}

func TestClaudeRunWithoutAnswerNotEndedEarly(t *testing.T) {
	plain := `{"type":"result","subtype":"success","is_error":false,"result":"Here: {\"status\":\"done\"}","total_cost_usd":0.5}`
	res, err, logs, took := runGrace(t, "#!/bin/sh\ncat "+lineFile(t, plain)+"\nsleep 0.3\n")
	if err != nil {
		t.Fatal(err)
	}
	if took < 300*time.Millisecond {
		t.Errorf("Run took %v, want it to wait for the CLI to exit", took)
	}
	if strings.Contains(logs, keptRunning) {
		t.Errorf("log %q, want no kill without a structured answer", logs)
	}
	var got struct{ Status string }
	if err := res.Decode(&got); err != nil || got.Status != "done" {
		t.Fatalf("Decode = %+v, %v", got, err)
	}
}
