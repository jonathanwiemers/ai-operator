package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// fakeClaudeStream is the fake claude of testdata/fake-claude-stream, with
// its knobs in env.
func fakeClaudeStream(t *testing.T, env ...string) *ClaudeCLI {
	t.Helper()
	bin, err := filepath.Abs("testdata/fake-claude-stream")
	if err != nil {
		t.Fatal(err)
	}
	return &ClaudeCLI{Binary: bin, Env: append(os.Environ(), env...)}
}

func TestClaudeArgsStreamInput(t *testing.T) {
	c := &ClaudeCLI{}
	inv := Invocation{Model: "opus", JSONSchema: `{"type":"object"}`, Resume: "s1"}
	plain := c.Args(inv)
	if slices.Contains(plain, "--input-format") || slices.Contains(plain, "--replay-user-messages") {
		t.Fatalf("a call without stream input keeps its arguments: %q", plain)
	}
	inv.StreamInput = true
	want := append(slices.Clone(plain), "--input-format", "stream-json", "--replay-user-messages")
	if got := c.Args(inv); !slices.Equal(got, want) {
		t.Fatalf("stream input adds only its flags:\n got %q\nwant %q", got, want)
	}
}

func TestStreamInputCallHasTheSameResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	argsFile := filepath.Join(t.TempDir(), "args")
	c := fakeClaudeStream(t, "FAKE_CLAUDE_WORK=0", "FAKE_CLAUDE_ARGS="+argsFile)
	inv := Invocation{Dir: t.TempDir(), Prompt: "implement #1", JSONSchema: `{"type":"object"}`}
	plain, err := c.Run(ctx, inv)
	if err != nil {
		t.Fatal(err)
	}
	inv.StreamInput = true
	streamed, err := c.Run(ctx, inv)
	if err != nil {
		t.Fatalf("the call must end once its result is in (stdin closed): %v", err)
	}
	if args, _ := os.ReadFile(argsFile); !strings.Contains(string(args), "--input-format stream-json --replay-user-messages") {
		t.Fatalf("stream input must reach the CLI: %s", args)
	}
	if !reflect.DeepEqual(plain, streamed) {
		t.Fatalf("a call without messages must have the same result:\n plain %+v\nstream %+v", plain, streamed)
	}
	if streamed.CostUSD != 0.25 || string(streamed.Structured) != `{"status":"done","messages":0}` {
		t.Fatalf("cost and structured output stay as reported: %+v", streamed)
	}
}

// streamAgent is a LocalAgent whose claude is the fake claude.
func streamAgent(t *testing.T, env ...string) *LocalAgent {
	t.Helper()
	tmp := t.TempDir()
	return &LocalAgent{
		Workspace: &Workspace{Dir: filepath.Join(tmp, "repo")},
		Exchange:  filepath.Join(tmp, "exchange"),
		Engines:   map[aiv1.Engine]Engine{aiv1.EngineClaude: fakeClaudeStream(t, env...)},
	}
}

// startStreamCall starts a stream-input call; the channel gets its outcome.
func startStreamCall(ctx context.Context, a Agent) <-chan runResponse {
	out := make(chan runResponse, 1)
	go func() {
		res, err := a.Run(ctx, aiv1.EngineClaude, Invocation{Prompt: "implement #1", JSONSchema: `{"type":"object"}`, StreamInput: true})
		out <- runResponse{Result: res, Error: errString(err)}
	}()
	return out
}

// messageWhenLive sends req as soon as the call is in the slot.
func messageWhenLive(ctx context.Context, t *testing.T, send func(context.Context, MessageRequest) (MessageResult, error), req MessageRequest) (MessageResult, error) {
	t.Helper()
	for {
		res, err := send(ctx, req)
		if err == nil || !strings.Contains(err.Error(), errNoStreamCall.Error()) || ctx.Err() != nil {
			return res, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMessageIntoRunningCall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var mu sync.Mutex
	var lines []string
	log := funcr.New(func(_, args string) { mu.Lock(); defer mu.Unlock(); lines = append(lines, args) }, funcr.Options{})
	client := serveAgent(t, &AgentServer{Log: log, Agent: streamAgent(t, "FAKE_CLAUDE_WORK=3")})
	done := startStreamCall(ctx, client)
	const text = "Use the existing retry helper."
	got, err := messageWhenLive(ctx, t, client.Message, MessageRequest{ID: "m1", Text: text})
	if err != nil || !got.Delivered {
		t.Fatalf("a replayed message is delivered: %+v, %v", got, err)
	}
	run := <-done
	if run.Error != "" || run.Result == nil || string(run.Result.Structured) != `{"status":"done","messages":1}` || run.Result.CostUSD != 0.25 {
		t.Fatalf("the call takes the message in and ends with one result: %+v", run)
	}
	if _, err := client.Message(ctx, MessageRequest{ID: "m2", Text: "late"}); err == nil {
		t.Fatal("after the call, a message has nowhere to go")
	}
	mu.Lock()
	defer mu.Unlock()
	users := 0
	for _, l := range lines {
		if strings.Contains(l, `"rpc"="message"`) && strings.Contains(l, "retry helper") {
			t.Errorf("the agent server must not log a message's text: %s", l)
		}
		if strings.Contains(l, `"msg"="user says"`) {
			users++
			if !strings.Contains(l, `"text"="`+text+`"`) {
				t.Errorf("the transcript's user turn carries the text: %s", l)
			}
		}
		if strings.Contains(l, "implement #1") {
			t.Errorf("the prompt's replay is not logged: %s", l)
		}
	}
	if users != 1 {
		t.Errorf("one user turn for the message, got %d:\n%s", users, strings.Join(lines, "\n"))
	}
}

func TestMessageWithoutReplay(t *testing.T) {
	for _, tc := range []struct {
		name, work string
		wait       time.Duration
		reason     string
	}{
		{"timeout", "3", 200 * time.Millisecond, "no replay within 200ms"},
		{"result first", "1", 20 * time.Second, "the call answered before the message was replayed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func(w time.Duration) { messageWait = w }(messageWait)
			messageWait = tc.wait
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			a := streamAgent(t, "FAKE_CLAUDE_NO_REPLAY=1", "FAKE_CLAUDE_WORK="+tc.work)
			done := startStreamCall(ctx, a)
			got, err := messageWhenLive(ctx, t, a.Message, MessageRequest{ID: "m1", Text: "hello"})
			if err != nil || got.Delivered || got.Reason != tc.reason {
				t.Fatalf("got %+v, %v; want not delivered: %s", got, err, tc.reason)
			}
			if run := <-done; run.Error != "" || run.Result == nil || run.Result.CostUSD != 0.25 {
				t.Fatalf("the call still ends with its result: %+v", run)
			}
		})
	}
}

func TestMessageWithoutCall(t *testing.T) {
	start := time.Now()
	if _, err := (&LocalAgent{}).Message(context.Background(), MessageRequest{ID: "m1", Text: "hi"}); !errors.Is(err, errNoStreamCall) {
		t.Fatalf("no call in flight is an error: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("Message must not block without a call")
	}
}

func TestReplayIsAUserTurn(t *testing.T) {
	var lines []string
	tr := &turnReader{log: funcr.New(func(_, args string) { lines = append(lines, args) }, funcr.Options{}), prompt: "the task"}
	var replayed []string
	tr.replayed = func(s string) { replayed = append(replayed, s) }
	for _, l := range []string{
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"the task"}]},"session_id":"s","isReplay":true}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"also do X"}]},"session_id":"s","isReplay":true}`,
		`{"type":"user","message":{"role":"user","content":"and Y"},"isReplay":true}`,
	} {
		if tr.consume([]byte(l)) {
			t.Fatal("a replay is no result")
		}
	}
	if !slices.Equal(replayed, []string{"also do X", "and Y"}) {
		t.Fatalf("replays other than the prompt's: %q", replayed)
	}
	if len(lines) != 2 || !strings.Contains(lines[0], `"msg"="user says" "text"="also do X"`) {
		t.Fatalf("each later replay is one user turn: %q", lines)
	}
	if tr.toolResults != nil {
		t.Fatalf("a replay is not a tool result: %+v", tr.toolResults)
	}
}
