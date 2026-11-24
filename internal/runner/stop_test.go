package runner

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

// waitSession waits until the agent's call in flight showed its session ID.
func waitSession(ctx context.Context, t *testing.T, a *LocalAgent) {
	t.Helper()
	for ctx.Err() == nil {
		a.liveMu.Lock()
		c := a.running
		a.liveMu.Unlock()
		if id, _ := c.stopped(); id != "" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the call never showed its session ID")
}

func TestStopReturnsAPartialResult(t *testing.T) {
	for _, stream := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		a := streamAgent(t, "FAKE_CLAUDE_WORK=20", "FAKE_CLAUDE_SIGINT=1")
		client := serveAgent(t, &AgentServer{Log: logr.Discard(), Agent: a})
		out := make(chan runResponse, 1)
		go func() {
			res, err := client.Run(ctx, "claude", Invocation{Prompt: "implement #1", JSONSchema: `{"type":"object"}`, StreamInput: stream})
			out <- runResponse{Result: res, Error: errString(err)}
		}()
		waitSession(ctx, t, a)
		start := time.Now()
		if err := client.Stop(ctx); err != nil {
			t.Fatalf("stream=%v: stop: %v", stream, err)
		}
		got := <-out
		if got.Error != "" || got.Result == nil || !got.Result.Stopped || got.Result.SessionID != "fake-session" || got.Result.IsError {
			t.Fatalf("stream=%v: a stopped call returns a partial result with its session and no error: %+v %q", stream, got.Result, got.Error)
		}
		if time.Since(start) > 10*time.Second {
			t.Fatalf("stream=%v: the stop must not wait for the work to end", stream)
		}
		if err := client.Stop(ctx); err == nil || err.Error() != errNoRunningCall.Error() {
			t.Fatalf("stream=%v: a stop without a call must fail: %v", stream, err)
		}
		cancel()
	}
}

func TestStopAfterTheAnswerIsRefused(t *testing.T) {
	c := &runningCall{}
	c.line([]byte(`{"type":"system","subtype":"init","session_id":"s1"}`), false)
	c.line([]byte(`{"type":"result","session_id":"s1"}`), true)
	if c.interrupt() {
		t.Fatal("a call that answered is not stopped")
	}
	if id, stop := c.stopped(); id != "s1" || stop {
		t.Fatalf("session %q, stopped %v", id, stop)
	}
	res := stoppedResult(nil, nil, &turnReader{}, "s1")
	if !res.Stopped || res.SessionID != "s1" || res.CallUsage != nil {
		t.Fatalf("an empty partial result keeps the session: %+v", res)
	}
}
