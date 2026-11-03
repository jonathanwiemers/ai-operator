package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// fakeSessionEngine is an in-memory SessionEngine for agent RPC tests.
type fakeSessionEngine struct {
	mu    sync.Mutex
	start func(Invocation) (Session, error)
	last  *fakeSession
}

func (e *fakeSessionEngine) Run(context.Context, Invocation) (*Result, error) {
	return nil, errors.New("one-shot not used")
}

func (e *fakeSessionEngine) StartSession(_ context.Context, inv Invocation) (Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.start != nil {
		s, err := e.start(inv)
		if fs, ok := s.(*fakeSession); ok {
			e.last = fs
		}
		return s, err
	}
	s := &fakeSession{id: "sess-" + inv.Role}
	if s.id == "sess-" {
		s.id = "sess-default"
	}
	e.last = s
	return s, nil
}

type fakeSession struct {
	mu     sync.Mutex
	id     string
	closed atomic.Bool
	prompt func(ctx context.Context, text string, fn func(Event)) (*Result, error)
	cancel atomic.Bool
	// cancelled is closed when Prompt sees ctx.Done or Cancel.
	cancelled chan struct{}
	once      sync.Once
}

func (s *fakeSession) ID() string { return s.id }

func (s *fakeSession) Prompt(ctx context.Context, text string, fn func(Event)) (*Result, error) {
	if s.prompt != nil {
		return s.prompt(ctx, text, fn)
	}
	if fn != nil {
		fn(Event{Type: "assistant", Subtype: "text", Line: json.RawMessage(`{"type":"assistant","text":"hi"}`)})
		fn(Event{Type: "result", Line: json.RawMessage(`{"type":"result"}`)})
	}
	return &Result{SessionID: s.id, Text: "echo:" + text}, nil
}

func (s *fakeSession) Cancel(context.Context) error {
	s.cancel.Store(true)
	s.once.Do(func() {
		if s.cancelled != nil {
			close(s.cancelled)
		}
	})
	return nil
}

func (s *fakeSession) Close() error {
	s.closed.Store(true)
	return nil
}

func (s *fakeSession) markCancelled() {
	s.once.Do(func() {
		if s.cancelled != nil {
			close(s.cancelled)
		}
	})
}

func sessionTestAgent(t *testing.T, eng SessionEngine) *AgentClient {
	t.Helper()
	tmp, err := os.MkdirTemp("", "ai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	local := &LocalAgent{
		Workspace: &Workspace{Dir: filepath.Join(tmp, "repo")},
		Exchange:  filepath.Join(tmp, "exchange"),
		Engines:   map[aiv1.Engine]Engine{aiv1.EngineClaude: eng.(Engine)},
	}
	return serveAgent(t, &AgentServer{Agent: local, Log: logr.Discard()})
}

func TestAgentSessionOpenPromptRoundTrip(t *testing.T) {
	eng := &fakeSessionEngine{}
	client := sessionTestAgent(t, eng)

	id, err := client.OpenSession(context.Background(), aiv1.EngineClaude, Invocation{Role: "implement"})
	if err != nil || id != "sess-implement" {
		t.Fatalf("open: id=%q err=%v", id, err)
	}

	var events []Event
	res, err := client.PromptSession(context.Background(), "first", func(ev Event) {
		events = append(events, ev)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != "assistant" || events[1].Type != "result" {
		t.Fatalf("events before result: %+v", events)
	}
	if res == nil || res.Text != "echo:first" || res.SessionID != "sess-implement" {
		t.Fatalf("result: %+v", res)
	}

	events = nil
	res, err = client.PromptSession(context.Background(), "second", func(ev Event) {
		events = append(events, ev)
	})
	if err != nil || res.Text != "echo:second" || len(events) != 2 {
		t.Fatalf("second turn: res=%+v events=%d err=%v", res, len(events), err)
	}
}

func TestAgentSessionPromptWithoutOpen(t *testing.T) {
	client := sessionTestAgent(t, &fakeSessionEngine{})
	_, err := client.PromptSession(context.Background(), "hi", nil)
	if err == nil || !strings.Contains(err.Error(), "no open session") {
		t.Fatalf("want no open session, got %v", err)
	}
}

func TestAgentSessionUsageLimitErrorVerbatim(t *testing.T) {
	limit := "You've hit your usage limit · resets Nov 4, 2026"
	eng := &fakeSessionEngine{start: func(Invocation) (Session, error) {
		return &fakeSession{id: "s1", prompt: func(context.Context, string, func(Event)) (*Result, error) {
			return &Result{SessionID: "s1", IsError: true, Text: limit}, errors.New(limit)
		}}, nil
	}}
	client := sessionTestAgent(t, eng)
	if _, err := client.OpenSession(context.Background(), aiv1.EngineClaude, Invocation{}); err != nil {
		t.Fatal(err)
	}
	_, err := client.PromptSession(context.Background(), "go", nil)
	if err == nil || err.Error() != limit {
		t.Fatalf("want verbatim %q, got %v", limit, err)
	}
}

func TestAgentSessionClientCancelMidTurn(t *testing.T) {
	cancelled := make(chan struct{})
	started := make(chan struct{})
	eng := &fakeSessionEngine{start: func(Invocation) (Session, error) {
		s := &fakeSession{id: "s1", cancelled: cancelled}
		s.prompt = func(ctx context.Context, text string, fn func(Event)) (*Result, error) {
			if text != "slow" {
				return &Result{SessionID: s.id, Text: "echo:" + text}, nil
			}
			if fn != nil {
				fn(Event{Type: "assistant", Line: json.RawMessage(`{"type":"assistant"}`)})
			}
			close(started)
			select {
			case <-ctx.Done():
				s.markCancelled()
				return nil, ctx.Err()
			case <-time.After(10 * time.Second):
				return nil, errors.New("prompt was not cancelled")
			}
		}
		return s, nil
	}}
	client := sessionTestAgent(t, eng)
	if _, err := client.OpenSession(context.Background(), aiv1.EngineClaude, Invocation{}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.PromptSession(ctx, "slow", nil)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("prompt did not start")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("session did not see cancel")
	}
	select {
	case err := <-done:
		// Our own cancel must not look like a lost agent container.
		var conn *agentConnError
		if !errors.Is(err, context.Canceled) || errors.As(err, &conn) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("prompt did not return")
	}

	// Same session stays open for the next prompt.
	res, err := client.PromptSession(context.Background(), "again", nil)
	if err != nil || res == nil || res.Text != "echo:again" {
		t.Fatalf("session should accept another prompt: %v %+v", err, res)
	}
}

func TestAgentSessionOpenClosesPrevious(t *testing.T) {
	var first *fakeSession
	n := 0
	eng := &fakeSessionEngine{start: func(Invocation) (Session, error) {
		n++
		s := &fakeSession{id: fmt.Sprintf("s%d", n)}
		if n == 1 {
			first = s
		}
		return s, nil
	}}
	client := sessionTestAgent(t, eng)
	if _, err := client.OpenSession(context.Background(), aiv1.EngineClaude, Invocation{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.OpenSession(context.Background(), aiv1.EngineClaude, Invocation{}); err != nil {
		t.Fatal(err)
	}
	if first == nil || !first.closed.Load() {
		t.Fatal("opening while open must close the old session")
	}
}

func TestAgentSessionFailedOpenKeepsPrevious(t *testing.T) {
	var first *fakeSession
	n := 0
	eng := &fakeSessionEngine{start: func(Invocation) (Session, error) {
		n++
		if n == 2 {
			return nil, errors.New("model not found")
		}
		first = &fakeSession{id: "s1"}
		return first, nil
	}}
	client := sessionTestAgent(t, eng)
	if _, err := client.OpenSession(context.Background(), aiv1.EngineClaude, Invocation{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.OpenSession(context.Background(), aiv1.EngineClaude, Invocation{}); err == nil {
		t.Fatal("the second open must fail")
	}
	if first.closed.Load() {
		t.Fatal("a failed open must keep the open session")
	}
	if _, err := client.PromptSession(context.Background(), "still there", nil); err != nil {
		t.Fatalf("the old session must still answer: %v", err)
	}
}

func TestAgentSessionStreamEndsEarly(t *testing.T) {
	// A stub server that writes one event line then closes: the client must
	// see an agentConnError (no final result line).
	sockDir, err := os.MkdirTemp("", "ai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	socket := filepath.Join(sockDir, "agent.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/prompt" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher := w.(http.Flusher)
		_ = json.NewEncoder(w).Encode(sessionPromptLine{
			Event: &Event{Type: "assistant", Line: json.RawMessage(`{"type":"assistant"}`)},
		})
		flusher.Flush()
		// End the stream without a final line.
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, err := hj.Hijack()
			if err == nil {
				conn.Close()
				return
			}
		}
	}), ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	client := NewAgentClient(socket)
	_, err = client.PromptSession(context.Background(), "x", nil)
	var ce *agentConnError
	if !errors.As(err, &ce) {
		t.Fatalf("want agentConnError, got %v", err)
	}
}

func TestAgentSessionCancelAndClose(t *testing.T) {
	eng := &fakeSessionEngine{}
	client := sessionTestAgent(t, eng)
	if err := client.CancelSession(context.Background()); err != nil {
		t.Fatalf("cancel with no session: %v", err)
	}
	if err := client.CloseSession(context.Background()); err != nil {
		t.Fatalf("close with no session: %v", err)
	}
	if _, err := client.OpenSession(context.Background(), aiv1.EngineClaude, Invocation{Role: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := client.CancelSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.CloseSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if eng.last == nil || !eng.last.closed.Load() {
		t.Fatal("close must close the session")
	}
	if err := client.CloseSession(context.Background()); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
}

func TestAgentSessionEngineWithoutSessions(t *testing.T) {
	tmp, err := os.MkdirTemp("", "ai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	local := &LocalAgent{
		Workspace: &Workspace{Dir: filepath.Join(tmp, "repo")},
		Exchange:  filepath.Join(tmp, "exchange"),
		Engines:   map[aiv1.Engine]Engine{aiv1.EngineClaude: &oneShotOnly{}},
	}
	client := serveAgent(t, &AgentServer{Agent: local, Log: logr.Discard()})
	_, err = client.OpenSession(context.Background(), aiv1.EngineClaude, Invocation{})
	if err == nil || !strings.Contains(err.Error(), "has no sessions") {
		t.Fatalf("want no sessions error, got %v", err)
	}
}

type oneShotOnly struct{}

func (*oneShotOnly) Run(context.Context, Invocation) (*Result, error) {
	return &Result{Text: "ok"}, nil
}
