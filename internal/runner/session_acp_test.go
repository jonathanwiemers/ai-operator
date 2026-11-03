package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func TestMain(m *testing.M) {
	if os.Getenv("AIO_TEST_ACP") == "1" {
		runACPFake()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func acpCLI(t *testing.T, mode string, extra ...string) *CursorCLI {
	t.Helper()
	env := append(os.Environ(),
		"AIO_TEST_ACP=1",
		"AIO_TEST_ACP_MODE="+mode,
		"AIO_TEST_ACP_LOG="+filepath.Join(t.TempDir(), "client.jsonl"),
	)
	env = append(env, extra...)
	return &CursorCLI{Binary: os.Args[0], Env: env}
}

func acpClientLog(t *testing.T, cli *CursorCLI) string {
	t.Helper()
	for _, e := range cli.Env {
		if k, v, ok := strings.Cut(e, "="); ok && k == "AIO_TEST_ACP_LOG" {
			b, err := os.ReadFile(v)
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
	}
	t.Fatal("no client log path")
	return ""
}

func TestCursorSessionTwoTurns(t *testing.T) {
	var buf bytes.Buffer
	log := logr.New(&bufLogger{buf: &buf})
	cli := acpCLI(t, "basic")
	s, err := cli.Start(context.Background(), Invocation{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.ID() == "" {
		t.Fatal("ID must be set right after Start")
	}
	wantID := s.ID()

	ctx := logr.NewContext(context.Background(), log)
	var events []Event
	res, err := s.Prompt(ctx, "Reply with just the word one.", func(e Event) { events = append(events, e) })
	if err != nil || res.Text != "one" || res.SessionID != wantID {
		t.Fatalf("turn 1: text=%q id=%q err=%v", res.Text, res.SessionID, err)
	}
	if len(events) == 0 {
		t.Fatal("expected session/update events")
	}
	for _, e := range events {
		if e.Type != "session/update" {
			t.Fatalf("event type: %s", e.Type)
		}
	}

	events = nil
	res, err = s.Prompt(ctx, "Reply with just the word two.", func(e Event) { events = append(events, e) })
	if err != nil || res.Text != "two" {
		t.Fatalf("turn 2: text=%q err=%v", res.Text, err)
	}

	out := buf.String()
	if !strings.Contains(out, "agent thinks") {
		t.Fatalf("want agent thinks in log: %s", out)
	}
	if strings.Contains(out, `"msg"="agent says"`) || strings.Contains(out, "agent says") {
		// logr text sink uses "msg"="…"
		if strings.Contains(out, "agent says") && !strings.Contains(out, "agent answers") {
			t.Fatalf("answer must not be logged as agent says: %s", out)
		}
	}
	if !strings.Contains(out, "agent answers") {
		t.Fatalf("want agent answers: %s", out)
	}
	// Final answer text must not appear as "agent says".
	if strings.Contains(out, `agent says`) && strings.Contains(out, `"one"`) {
		t.Fatalf("answer logged as says: %s", out)
	}
}

func TestCursorSessionSlowConsumerDoesNotBlockReader(t *testing.T) {
	s, err := acpCLI(t, "flood").Start(context.Background(), Invocation{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	n := 0
	res, err := s.Prompt(ctx, "go", func(Event) { n++; time.Sleep(time.Millisecond) })
	if err != nil || res.Text != "done" {
		t.Fatalf("text=%q err=%v", res.Text, err)
	}
	if n < 200 {
		t.Fatalf("every update must reach the turn, got %d", n)
	}
}

func TestCursorSessionCancel(t *testing.T) {
	waitFile := filepath.Join(t.TempDir(), "waiting")
	cli := acpCLI(t, "cancel", "AIO_TEST_ACP_WAIT="+waitFile)
	s, err := cli.Start(context.Background(), Invocation{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	done := make(chan struct{})
	var promptErr error
	var res *Result
	go func() {
		res, promptErr = s.Prompt(context.Background(), "slow", nil)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(waitFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("prompt never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := s.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt did not return after Cancel")
	}
	if promptErr == nil || !strings.Contains(promptErr.Error(), "cancelled") {
		t.Fatalf("want cancelled: res=%+v err=%v", res, promptErr)
	}
	res, err = s.Prompt(context.Background(), "next", nil)
	if err != nil || res.Text != "two" {
		t.Fatalf("next turn: %+v %v", res, err)
	}
}

func TestCursorSessionReadOnlyPermission(t *testing.T) {
	cli := acpCLI(t, "permission")
	s, err := cli.Start(context.Background(), Invocation{Dir: t.TempDir(), ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	log := acpClientLog(t, cli)
	if !strings.Contains(log, `"method":"session/set_mode"`) || !strings.Contains(log, `"modeId":"ask"`) {
		t.Fatalf("want set_mode ask: %s", log)
	}
	res, err := s.Prompt(context.Background(), "check", nil)
	if err != nil || res.Text != "rejected" {
		t.Fatalf("got %+v %v", res, err)
	}
	log = acpClientLog(t, cli)
	if !strings.Contains(log, `"optionId":"reject-once"`) {
		t.Fatalf("want reject-once: %s", log)
	}
}

func TestCursorSessionWritePermission(t *testing.T) {
	cli := acpCLI(t, "permission")
	s, err := cli.Start(context.Background(), Invocation{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res, err := s.Prompt(context.Background(), "check", nil)
	if err != nil || res.Text != "allowed" {
		t.Fatalf("got %+v %v", res, err)
	}
	log := acpClientLog(t, cli)
	if strings.Contains(log, `"method":"session/set_mode"`) {
		t.Fatalf("writing session must not set_mode: %s", log)
	}
	if !strings.Contains(log, `"optionId":"allow-once"`) {
		t.Fatalf("want allow-once: %s", log)
	}
}

func TestCursorSessionModelSelection(t *testing.T) {
	t.Run("name", func(t *testing.T) {
		cli := acpCLI(t, "basic")
		s, err := cli.Start(context.Background(), Invocation{Dir: t.TempDir(), Model: "composer-2.5"})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		log := acpClientLog(t, cli)
		if !strings.Contains(log, `"method":"session/set_model"`) || !strings.Contains(log, "composer-2.5[fast=true]") {
			t.Fatalf("want set_model by name: %s", log)
		}
	})
	t.Run("prefix", func(t *testing.T) {
		cli := acpCLI(t, "basic")
		s, err := cli.Start(context.Background(), Invocation{Dir: t.TempDir(), Model: "grok-4.7"})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		log := acpClientLog(t, cli)
		if !strings.Contains(log, "grok-4.7[context=256k") {
			t.Fatalf("want prefix match: %s", log)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		cli := acpCLI(t, "basic")
		_, err := cli.Start(context.Background(), Invocation{Dir: t.TempDir(), Model: "no-such-model"})
		if err == nil || !strings.Contains(err.Error(), "no-such-model") {
			t.Fatalf("want model error: %v", err)
		}
	})
	t.Run("auto", func(t *testing.T) {
		cli := acpCLI(t, "basic")
		s, err := cli.Start(context.Background(), Invocation{Dir: t.TempDir(), Model: "auto"})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if strings.Contains(acpClientLog(t, cli), "session/set_model") {
			t.Fatal("auto must not set_model")
		}
	})
}

func TestCursorSessionResume(t *testing.T) {
	cli := acpCLI(t, "resume")
	var events []Event
	s, err := cli.Start(context.Background(), Invocation{Dir: t.TempDir(), Resume: "resume-sid"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.ID() != "resume-sid" {
		t.Fatalf("id=%q", s.ID())
	}
	log := acpClientLog(t, cli)
	if !strings.Contains(log, `"method":"session/load"`) || !strings.Contains(log, `"sessionId":"resume-sid"`) {
		t.Fatalf("want session/load: %s", log)
	}
	if strings.Contains(log, `"method":"session/new"`) {
		t.Fatal("resume must not session/new")
	}
	res, err := s.Prompt(context.Background(), "hi", func(e Event) { events = append(events, e) })
	if err != nil || res.Text != "resumed" {
		t.Fatalf("got %+v %v", res, err)
	}
	for _, e := range events {
		if bytes.Contains(e.Line, []byte(`"old"`)) || bytes.Contains(e.Line, []byte("replay")) {
			t.Fatalf("replayed load update emitted: %s", e.Line)
		}
	}
}

func TestCursorSessionRPCError(t *testing.T) {
	cli := acpCLI(t, "rpc_error")
	s, err := cli.Start(context.Background(), Invocation{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Prompt(context.Background(), "go", nil)
	if err == nil || !strings.Contains(err.Error(), "You've hit your usage limit") {
		t.Fatalf("want verbatim usage limit: %v", err)
	}
}

func TestCursorSessionProcessExitMidTurn(t *testing.T) {
	cli := acpCLI(t, "exit")
	s, err := cli.Start(context.Background(), Invocation{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Prompt(context.Background(), "go", nil)
	if err == nil || !strings.Contains(err.Error(), "agent crashed") {
		t.Fatalf("want stderr: %v", err)
	}
	_, err = s.Prompt(context.Background(), "again", nil)
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("dead session: %v", err)
	}
}

func TestCursorSessionCloseKills(t *testing.T) {
	prevClose, prevStdin := sessionCloseWait, sessionStdinCloseWait
	sessionCloseWait, sessionStdinCloseWait = 200*time.Millisecond, 100*time.Millisecond
	defer func() { sessionCloseWait, sessionStdinCloseWait = prevClose, prevStdin }()

	cli := acpCLI(t, "hang")
	s, err := cli.Start(context.Background(), Invocation{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_ = s.Close()
	if time.Since(start) > 2*time.Second {
		t.Fatalf("Close took too long: %v", time.Since(start))
	}
	if err := s.Close(); err != nil {
		t.Fatalf("idempotent Close: %v", err)
	}
}

func TestCursorSessionLoadSkipsModel(t *testing.T) {
	cli := acpCLI(t, "resume_nomodels")
	s, err := cli.Start(context.Background(), Invocation{Dir: t.TempDir(), Resume: "sid", Model: "composer-2.5"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if strings.Contains(acpClientLog(t, cli), "session/set_model") {
		t.Fatal("load without models must skip set_model")
	}
}

// bufLogger is a tiny logr sink for asserting Info messages.
type bufLogger struct {
	buf *bytes.Buffer
	mu  sync.Mutex
}

func (l *bufLogger) Init(logr.RuntimeInfo) {}
func (l *bufLogger) Enabled(int) bool      { return true }
func (l *bufLogger) Info(level int, msg string, keys ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.buf, "msg=%q", msg)
	for i := 0; i+1 < len(keys); i += 2 {
		fmt.Fprintf(l.buf, " %v=%q", keys[i], keys[i+1])
	}
	l.buf.WriteByte('\n')
}
func (l *bufLogger) Error(err error, msg string, keys ...any) {
	l.Info(0, msg, append(keys, "err", err)...)
}
func (l *bufLogger) WithValues(...any) logr.LogSink { return l }
func (l *bufLogger) WithName(string) logr.LogSink   { return l }

// --- fake cursor-agent acp -------------------------------------------------

func runACPFake() {
	mode := os.Getenv("AIO_TEST_ACP_MODE")
	logPath := os.Getenv("AIO_TEST_ACP_LOG")
	var logFile *os.File
	if logPath != "" {
		logFile, _ = os.Create(logPath)
		defer logFile.Close()
	}
	logLine := func(prefix string, b []byte) {
		if logFile != nil {
			fmt.Fprintf(logFile, "%s %s\n", prefix, b)
		}
	}

	modelsObj := map[string]any{
		"currentModelId": "default[]",
		"availableModels": []map[string]any{
			{"modelId": "default[]", "name": "Auto"},
			{"modelId": "grok-4.7[context=256k,reasoning_effort=high,fast=true]", "name": "grok-4.7"},
			{"modelId": "composer-2.5[fast=true]", "name": "composer-2.5"},
		},
	}
	sessionID := "test-session-1"
	promptN := 0
	write := func(v any) {
		b, _ := json.Marshal(v)
		logLine("agent", b)
		_, _ = os.Stdout.Write(append(b, '\n'))
	}
	reply := func(id any, result any) {
		write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	replyErr := func(id any, msg string) {
		write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32000, "message": msg}})
	}
	update := func(kind string, extra map[string]any) {
		u := map[string]any{"sessionUpdate": kind}
		for k, v := range extra {
			u[k] = v
		}
		write(map[string]any{
			"jsonrpc": "2.0",
			"method":  "session/update",
			"params":  map[string]any{"sessionId": sessionID, "update": u},
		})
	}
	thoughtThen := func(thought, text string) {
		update("agent_thought_chunk", map[string]any{"content": map[string]any{"type": "text", "text": thought}})
		update("agent_message_chunk", map[string]any{"content": map[string]any{"type": "text", "text": text}})
	}

	rd := bufio.NewReader(os.Stdin)
	for {
		line, err := rd.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			logLine("client", bytes.TrimSpace(line))
			var msg struct {
				ID     any             `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(line, &msg) != nil {
				continue
			}
			switch msg.Method {
			case "initialize":
				reply(msg.ID, map[string]any{"protocolVersion": 1})
			case "authenticate":
				reply(msg.ID, map[string]any{})
			case "session/new":
				reply(msg.ID, map[string]any{
					"sessionId": sessionID,
					"models":    modelsObj,
				})
			case "session/load":
				var p struct {
					SessionID string `json:"sessionId"`
				}
				_ = json.Unmarshal(msg.Params, &p)
				sessionID = p.SessionID
				// Replay history; client must drop these.
				update("agent_message_chunk", map[string]any{"content": map[string]any{"type": "text", "text": "old"}})
				update("agent_thought_chunk", map[string]any{"content": map[string]any{"type": "text", "text": "replay"}})
				result := map[string]any{"sessionId": sessionID}
				if mode != "resume_nomodels" {
					result["models"] = modelsObj
				}
				reply(msg.ID, result)
			case "session/set_mode", "session/set_model":
				reply(msg.ID, map[string]any{})
			case "session/cancel":
				// handled while waiting in cancel mode
			case "session/prompt":
				promptN++
				switch mode {
				case "cancel":
					if promptN == 1 {
						if w := os.Getenv("AIO_TEST_ACP_WAIT"); w != "" {
							_ = os.WriteFile(w, []byte("1"), 0o644)
						}
						// Wait until a cancel notification arrives on stdin.
						for {
							line2, err2 := rd.ReadBytes('\n')
							if len(bytes.TrimSpace(line2)) > 0 {
								logLine("client", bytes.TrimSpace(line2))
								var m2 struct {
									Method string `json:"method"`
								}
								_ = json.Unmarshal(line2, &m2)
								if m2.Method == "session/cancel" {
									break
								}
							}
							if err2 != nil {
								return
							}
						}
						reply(msg.ID, map[string]any{"stopReason": "cancelled"})
					} else {
						thoughtThen("next", "two")
						reply(msg.ID, map[string]any{"stopReason": "end_turn"})
					}
				case "flood":
					// Far more updates than any buffer, then the answer.
					for i := 0; i < 200; i++ {
						write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
							"sessionId": sessionID,
							"update":    map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "x"}},
						}})
					}
					thoughtThen("last", "done")
					reply(msg.ID, map[string]any{"stopReason": "end_turn"})
				case "permission":
					optAllow, optReject := "allow-once", "reject-once"
					reqID := float64(9000 + promptN)
					write(map[string]any{
						"jsonrpc": "2.0",
						"id":      reqID,
						"method":  "session/request_permission",
						"params": map[string]any{
							"sessionId": sessionID,
							"options": []map[string]any{
								{"optionId": optAllow, "name": "Allow", "kind": "allow_once"},
								{"optionId": optReject, "name": "Reject", "kind": "reject_once"},
							},
						},
					})
					// Read the client's permission answer.
					for {
						line2, err2 := rd.ReadBytes('\n')
						if len(bytes.TrimSpace(line2)) > 0 {
							logLine("client", bytes.TrimSpace(line2))
							var m2 struct {
								ID     any `json:"id"`
								Result *struct {
									Outcome struct {
										OptionID string `json:"optionId"`
									} `json:"outcome"`
								} `json:"result"`
							}
							_ = json.Unmarshal(line2, &m2)
							if m2.Result != nil {
								text := "allowed"
								if m2.Result.Outcome.OptionID == optReject {
									text = "rejected"
								}
								thoughtThen("perm", text)
								reply(msg.ID, map[string]any{"stopReason": "end_turn"})
								break
							}
						}
						if err2 != nil {
							return
						}
					}
				case "rpc_error":
					replyErr(msg.ID, "You've hit your usage limit. Your usage limits will reset when your monthly cycle ends on 11/15/2026.")
				case "exit":
					fmt.Fprintln(os.Stderr, "agent crashed")
					os.Exit(1)
				case "resume", "resume_nomodels":
					thoughtThen("r", "resumed")
					reply(msg.ID, map[string]any{"stopReason": "end_turn"})
				default: // basic, hang
					text := "one"
					if promptN > 1 {
						text = "two"
					}
					update("available_commands_update", map[string]any{"availableCommands": []any{}})
					update("session_info_update", map[string]any{"title": "t"})
					thoughtThen("thinking about "+text, text)
					reply(msg.ID, map[string]any{"stopReason": "end_turn"})
				}
			}
		}
		if err != nil {
			if mode == "hang" {
				// Ignore stdin EOF; stay alive until killed.
				select {}
			}
			if err == io.EOF {
				return
			}
			return
		}
	}
}
