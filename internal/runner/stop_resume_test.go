package runner

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// msgStopEngine is an engine without stream input, as cursor runs: its first
// call edits the tree and works until it is stopped (Agent.Stop), with the
// session session ("" for none seen yet); later calls answer at once.
type msgStopEngine struct {
	session string
	mu      sync.Mutex
	calls   []Invocation
}

func (e *msgStopEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	e.mu.Lock()
	e.calls = append(e.calls, inv)
	first := len(e.calls) == 1
	e.mu.Unlock()
	edit := filepath.Join(inv.Dir, "edit.txt")
	if !first {
		if _, err := os.Stat(edit); err != nil {
			return &Result{SessionID: "other", IsError: true, Text: "the stopped call's edits are gone"}, nil
		}
		return &Result{SessionID: orDefault(inv.Resume, "fresh-session"), CostUSD: 0.25, CostReported: true, Structured: []byte(`{"status":"done"}`)}, nil
	}
	if err := os.MkdirAll(inv.Dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(edit, []byte("work so far"), 0o644); err != nil {
		return nil, err
	}
	rc, _ := ctx.Value(runningKey{}).(*runningCall)
	for ctx.Err() == nil {
		if _, stop := rc.stopped(); stop {
			return &Result{Stopped: true, SessionID: e.session, CostUSD: 0.1, CostReported: true}, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, ctx.Err()
}

func (e *msgStopEngine) invocations() []Invocation {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Invocation(nil), e.calls...)
}

// stopAndResume runs a cursor implementer call that gets an owner message
// while it works and returns its result and the engine's calls.
func stopAndResume(t *testing.T, session string) (*Result, []Invocation, *Runner, *aiv1.AgentTask) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	eng := &msgStopEngine{session: session}
	r, k8s := messageEnv(t, eng, time.Now())
	type outcome struct {
		res *Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := r.invoke(ctx, aiv1.RoleSpec{Engine: aiv1.EngineCursor},
			Invocation{Role: "implement", Kind: aiv1.CallKindImplement, JSONSchema: `{"type":"object"}`}, "implement #42")
		done <- outcome{res, err}
	}()
	for len(eng.invocations()) == 0 && ctx.Err() == nil {
		time.Sleep(10 * time.Millisecond)
	}
	addOwnerMessages(t, k8s, ownerMessage("tell/1", time.Now()))
	out := <-done
	if out.err != nil {
		t.Fatalf("a stop for a message is no failure: %v", out.err)
	}
	return out.res, eng.invocations(), r, messageTaskNow(t, k8s)
}

func TestCursorStopAndResume(t *testing.T) {
	res, calls, r, task := stopAndResume(t, "cur-session")
	if len(calls) != 2 || calls[1].Resume != "cur-session" {
		t.Fatalf("the same session resumes: %+v", calls)
	}
	want := "## Message from @alice via the pilot\n\nWords tell/1.\n\nGo on with the task; answer as before."
	if calls[1].Prompt != want {
		t.Fatalf("the resumed call's prompt is the message:\n%s", calls[1].Prompt)
	}
	if string(res.Structured) != `{"status":"done"}` || math.Abs(res.CostUSD-0.35) > 1e-9 {
		t.Fatalf("the resumed call's result is the step's, the cost both calls': %+v", res)
	}
	run := task.Status.Run
	if len(run.Calls) != 2 || !run.Calls[0].StoppedForMessage || run.Calls[1].StoppedForMessage || !run.Calls[1].Resumed {
		t.Fatalf("a stopped call, then the resumed one: %+v", run.Calls)
	}
	if run.Calls[0].Checkpoint != "" || run.Calls[0].CostUSD != "0.1000" || run.Calls[1].CostUSD != "0.2500" {
		t.Fatalf("no checkpoint for a stop, and the calls add up to the step's cost: %+v", run.Calls)
	}
	if d := task.Delivery("tell/1"); d == nil || d.How != aiv1.DeliveryResume || d.Call != run.Calls[1].Seq {
		t.Fatalf("delivered by resume into the resumed call: %+v", run.DeliveredMessages)
	}
	if run.Interruptions != 0 || r.run.midRunLeft() != aiv1.MaxMidRunDeliveries-1 || len(task.PendingOwnerMessages()) != 0 {
		t.Fatalf("no interruption, one delivery of the cap, nothing pending: %+v", run)
	}
}

func TestStopWithoutSessionRestartsTheStep(t *testing.T) {
	res, calls, _, task := stopAndResume(t, "")
	if len(calls) != 2 || calls[1].Resume != "" {
		t.Fatalf("without a session ID the step's call starts again: %+v", calls)
	}
	if p := calls[1].Prompt; !strings.HasPrefix(p, "implement #42\n\n## Message from @alice via the pilot\n\nWords tell/1.") {
		t.Fatalf("the full prompt with the message after it:\n%s", p)
	}
	if string(res.Structured) != `{"status":"done"}` {
		t.Fatalf("the restarted call keeps the stopped call's edits: %+v", res)
	}
	if d := task.Delivery("tell/1"); d == nil || d.How != aiv1.DeliveryResume || task.Status.Run.Interruptions != 0 {
		t.Fatalf("recorded as resume, no interruption: %+v", task.Status.Run)
	}
}
