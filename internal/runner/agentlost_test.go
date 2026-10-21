package runner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// lostEngine loses the agent container in its first implementer calls: the
// operator records how it stopped, and the connection breaks without an
// answer, as when the kubelet kills the container.
type lostEngine struct {
	k8s    client.Client
	task   string
	reason string
	fails  int
	calls  int
	// stale records the termination before the run started.
	stale bool
}

func (l *lostEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	var v any = ReviewAnswer{Summary: "fine"}
	if !inv.ReadOnly {
		l.calls++
		if l.calls <= l.fails {
			var task aiv1.AgentTask
			_ = l.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: l.task}, &task)
			orig := task.DeepCopy()
			finished := time.Now()
			if l.stale {
				finished = finished.Add(-time.Hour)
			}
			task.Status.AgentTermination = &aiv1.ContainerTermination{Reason: l.reason, ExitCode: 137,
				FinishedAt: &metav1.Time{Time: finished}, Restarts: int32(l.calls), MemoryLimit: "4Gi"}
			// A git command killed with the container leaves its lock.
			_ = os.WriteFile(filepath.Join(inv.Dir, ".git", "index.lock"), nil, 0o644)
			_ = l.k8s.Status().Patch(ctx, &task, client.MergeFrom(orig))
			panic(http.ErrAbortHandler) // the server drops the connection
		}
		_ = os.WriteFile(filepath.Join(inv.Dir, "work.go"), []byte("package app\n"), 0o644)
		v = ImplementAnswer{Status: "done", Summary: "Done.", CommitMessage: "Work", Handoff: "x"}
	}
	b, _ := json.Marshal(v)
	return &Result{SessionID: "sess-l", Structured: b}, nil
}

func fastAgentLost(t *testing.T) {
	back, term, poll := agentBackTimeout, terminationTimeout, agentLostPoll
	agentBackTimeout, terminationTimeout, agentLostPoll = time.Second, time.Second, 10*time.Millisecond
	t.Cleanup(func() { agentBackTimeout, terminationTimeout, agentLostPoll = back, term, poll })
}

func TestOOMKilledAgentFailsTheRunWithTheReason(t *testing.T) {
	fastAgentLost(t)
	e := newImplementEnv(t)
	engine := &lostEngine{k8s: e.k8s, task: e.task.Name, reason: "OOMKilled", fails: 5}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if engine.calls != 1 {
		t.Fatalf("running out of memory would happen again: no retry, got %d calls", engine.calls)
	}
	note := failureNote(e)
	if !strings.Contains(note, "ran out of memory") || !strings.Contains(note, "memory limit 4Gi") || !strings.Contains(note, "spec.agents.resources") {
		t.Fatalf("the failure must name the OOM kill and the limit: %q", note)
	}
}

func TestAgentCrashIsTriedOnceMore(t *testing.T) {
	fastAgentLost(t)
	e := newImplementEnv(t)
	engine := &lostEngine{k8s: e.k8s, task: e.task.Name, reason: "Error", fails: 1}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	var task aiv1.AgentTask
	_ = e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	if engine.calls != 2 || task.Status.Run.LastResult != aiv1.RunResultSucceeded {
		t.Fatalf("a crash is tried once more: %d calls, result %s (%s)", engine.calls, task.Status.Run.LastResult, task.Status.Run.Summary)
	}
}

func TestAgentLostWithoutARecordedReason(t *testing.T) {
	err := &AgentLostError{After: 88 * time.Minute, Err: &agentConnError{method: "run", err: io.EOF}}
	if msg := err.Error(); !strings.Contains(msg, "lost the agent container after 1h28m0s") || !strings.Contains(msg, "kubectl describe pod") {
		t.Fatalf("unexpected message %q", msg)
	}
}

func failureNote(e *implementEnv) string {
	var note string
	for _, n := range e.gl.IssueNotes[42] {
		if strings.Contains(n.Body, "run failed") {
			note = n.Body
		}
	}
	return note
}

func TestAgentCrashTwiceFailsWithTheReason(t *testing.T) {
	fastAgentLost(t)
	e := newImplementEnv(t)
	engine := &lostEngine{k8s: e.k8s, task: e.task.Name, reason: "Error", fails: 5}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	_, _ = r.Step(context.Background())
	if note := failureNote(e); engine.calls != 2 || !strings.Contains(note, "stopped after") || !strings.Contains(note, "Error, exit code 137") {
		t.Fatalf("one retry, then the reason: %d calls, %q", engine.calls, note)
	}
}

func TestAgentLossIgnoresATerminationBeforeTheRun(t *testing.T) {
	fastAgentLost(t)
	e := newImplementEnv(t)
	engine := &lostEngine{k8s: e.k8s, task: e.task.Name, reason: "OOMKilled", fails: 5, stale: true}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	_, _ = r.Step(context.Background())
	if note := failureNote(e); engine.calls != 1 || strings.Contains(note, "OOMKilled") || !strings.Contains(note, "lost the agent container") {
		t.Fatalf("an older termination is not this run's: %d calls, %q", engine.calls, note)
	}
}
