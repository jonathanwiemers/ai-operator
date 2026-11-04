package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-logr/logr"
)

var _ Session = (*CursorSession)(nil)

// CursorSession is a long-lived `cursor-agent acp` process speaking ACP
// (JSON-RPC 2.0, one JSON object per line on stdin/stdout).
type CursorSession struct {
	bin                  string
	stdin                io.WriteCloser
	errBuf               *boundWriter
	promptMu, writeMu    sync.Mutex
	mu                   sync.Mutex
	id                   string
	readOnly             bool
	dead, closed, inTurn bool
	loading              bool // drop session/update during session/load
	reqID                atomic.Uint64
	pending              map[uint64]chan acpResponse
	// The turn's updates: the reader appends and signals, the turn takes
	// them. The reader must never block, or responses stop arriving.
	inTurnUpdates bool
	updates       []acpUpdate
	updSignal     chan struct{}
	sessionProc
}

type acpResponse struct {
	Result json.RawMessage
	Error  *acpRPCError
}

type acpRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *acpRPCError) Error() string {
	if e == nil {
		return ""
	}
	if len(e.Data) > 0 && string(e.Data) != "null" {
		return fmt.Sprintf("%s: %s", e.Message, string(e.Data))
	}
	return e.Message
}

type acpUpdate struct {
	Line   json.RawMessage
	Update json.RawMessage // params.update
	Kind   string          // sessionUpdate
}

// StartSession opens a CursorSession. It implements SessionEngine.
func (c *CursorCLI) StartSession(ctx context.Context, inv Invocation) (Session, error) {
	return c.Start(ctx, inv)
}

var _ SessionEngine = (*CursorCLI)(nil)

// Start runs cursor-agent acp and completes initialize/authenticate/session
// setup. The process lives until Close; inv.Prompt is ignored.
func (c *CursorCLI) Start(_ context.Context, inv Invocation) (*CursorSession, error) {
	bin := orDefault(c.Binary, "cursor-agent")
	cmd := exec.Command(bin, "acp")
	cmd.Dir, cmd.Env = inv.Dir, append(slices.Clip(c.Env), inv.Env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	errBuf := &boundWriter{max: 8 << 10}
	cmd.Stderr = errBuf
	if err := cmd.Start(); err != nil {
		stdin.Close()
		return nil, fmt.Errorf("%s failed: %w", filepath.Base(bin), err)
	}
	s := &CursorSession{
		bin:         bin,
		stdin:       stdin,
		errBuf:      errBuf,
		readOnly:    inv.ReadOnly,
		pending:     make(map[uint64]chan acpResponse),
		updSignal:   make(chan struct{}, 1),
		sessionProc: sessionProc{cmd: cmd},
	}
	go s.readLoop(stdout)

	ctx, cancel := context.WithTimeout(context.Background(), sessionStartupTimeout)
	defer cancel()
	if err := s.handshake(ctx, inv); err != nil {
		s.kill()
		<-s.exited()
		return nil, err
	}
	return s, nil
}

func (s *CursorSession) handshake(ctx context.Context, inv Invocation) error {
	if _, err := s.call(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{
				"readTextFile":  false,
				"writeTextFile": false,
			},
			"terminal": false,
		},
		"clientInfo": map[string]any{
			"name":    "ai-operator",
			"version": "",
		},
	}); err != nil {
		return err
	}
	if _, err := s.call(ctx, "authenticate", map[string]any{
		"methodId": "cursor_login",
	}); err != nil {
		return err
	}

	var sessionResult json.RawMessage
	if inv.Resume != "" {
		s.mu.Lock()
		s.loading = true
		s.id = inv.Resume
		s.mu.Unlock()
		res, err := s.call(ctx, "session/load", map[string]any{
			"sessionId":  inv.Resume,
			"cwd":        inv.Dir,
			"mcpServers": []any{},
		})
		s.mu.Lock()
		s.loading = false
		s.mu.Unlock()
		if err != nil {
			return err
		}
		sessionResult = res
	} else {
		res, err := s.call(ctx, "session/new", map[string]any{
			"cwd":        inv.Dir,
			"mcpServers": []any{},
		})
		if err != nil {
			return err
		}
		var out struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(res, &out); err != nil || out.SessionID == "" {
			return fmt.Errorf("session/new: missing sessionId")
		}
		s.mu.Lock()
		s.id = out.SessionID
		s.mu.Unlock()
		sessionResult = res
	}

	if inv.ReadOnly {
		if _, err := s.call(ctx, "session/set_mode", map[string]any{
			"sessionId": s.ID(),
			"modeId":    "ask",
		}); err != nil {
			return err
		}
	}

	if inv.Model != "" && inv.Model != "auto" {
		modelID, err := pickACPModel(sessionResult, inv.Model)
		if err != nil {
			return err
		}
		if modelID != "" {
			if _, err := s.call(ctx, "session/set_model", map[string]any{
				"sessionId": s.ID(),
				"modelId":   modelID,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// pickACPModel finds a modelId by exact name or modelId prefix "name[".
// An empty result with nil error means no models list (skip selection).
func pickACPModel(sessionResult json.RawMessage, want string) (string, error) {
	var out struct {
		Models *struct {
			AvailableModels []struct {
				ModelID string `json:"modelId"`
				Name    string `json:"name"`
			} `json:"availableModels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(sessionResult, &out); err != nil {
		return "", nil
	}
	if out.Models == nil || len(out.Models.AvailableModels) == 0 {
		return "", nil
	}
	prefix := want + "["
	for _, m := range out.Models.AvailableModels {
		if m.Name == want || strings.HasPrefix(m.ModelID, prefix) {
			return m.ModelID, nil
		}
	}
	return "", fmt.Errorf("model %q not found", want)
}

func (s *CursorSession) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

func (s *CursorSession) readLoop(stdout io.Reader) {
	rd := bufio.NewReader(stdout)
	for {
		line, err := rd.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			s.handleLine(line)
		}
		if err != nil {
			return
		}
	}
}

func (s *CursorSession) handleLine(line []byte) {
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  *acpRPCError    `json:"error"`
	}
	if json.Unmarshal(line, &msg) != nil {
		return
	}
	hasID := len(msg.ID) > 0 && string(msg.ID) != "null"
	switch {
	case hasID && msg.Method != "":
		s.answerAgent(msg.ID, msg.Method, msg.Params)
	case hasID:
		var id uint64
		if json.Unmarshal(msg.ID, &id) != nil {
			return
		}
		s.mu.Lock()
		ch := s.pending[id]
		delete(s.pending, id)
		s.mu.Unlock()
		if ch != nil {
			ch <- acpResponse{Result: msg.Result, Error: msg.Error}
		}
	case msg.Method == "session/update":
		s.routeUpdate(line, msg.Params)
	}
}

func (s *CursorSession) routeUpdate(line, params json.RawMessage) {
	var p struct {
		Update json.RawMessage `json:"update"`
	}
	_ = json.Unmarshal(params, &p)
	var u struct {
		Kind string `json:"sessionUpdate"`
	}
	_ = json.Unmarshal(p.Update, &u)
	upd := acpUpdate{Line: json.RawMessage(append([]byte(nil), line...)), Update: p.Update, Kind: u.Kind}

	s.mu.Lock()
	if s.loading || !s.inTurnUpdates {
		s.mu.Unlock()
		return
	}
	s.updates = append(s.updates, upd)
	s.mu.Unlock()
	select {
	case s.updSignal <- struct{}{}:
	default:
	}
}

// takeUpdates returns the updates queued for the turn.
func (s *CursorSession) takeUpdates() []acpUpdate {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.updates
	s.updates = nil
	return u
}

func (s *CursorSession) answerAgent(id json.RawMessage, method string, params json.RawMessage) {
	switch method {
	case "session/request_permission":
		optionID := s.permissionOption(params)
		_ = s.writeJSON(map[string]any{
			"jsonrpc": "2.0",
			"id":      jsonRaw(id),
			"result": map[string]any{
				"outcome": map[string]any{
					"outcome":  "selected",
					"optionId": optionID,
				},
			},
		})
	default:
		_ = s.writeJSON(map[string]any{
			"jsonrpc": "2.0",
			"id":      jsonRaw(id),
			"error": map[string]any{
				"code":    -32601,
				"message": "method not found",
			},
		})
	}
}

func jsonRaw(raw json.RawMessage) any {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
}

func (s *CursorSession) permissionOption(params json.RawMessage) string {
	wantKind, wantID := "allow_once", "allow-once"
	if s.readOnly {
		wantKind, wantID = "reject_once", "reject-once"
	}
	var p struct {
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	_ = json.Unmarshal(params, &p)
	for _, o := range p.Options {
		if o.Kind == wantKind {
			return o.OptionID
		}
	}
	return wantID
}

func (s *CursorSession) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := s.reqID.Add(1)
	ch := make(chan acpResponse, 1)
	s.mu.Lock()
	if s.closed || s.dead {
		s.mu.Unlock()
		return nil, errors.New("session is closed")
	}
	s.pending[id] = ch
	s.mu.Unlock()

	if err := s.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, err
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, ctx.Err()
	case <-s.exited():
		// The response may have arrived just before the exit.
		select {
		case resp := <-ch:
			if resp.Error != nil {
				return nil, resp.Error
			}
			return resp.Result, nil
		default:
		}
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, fmt.Errorf("%s failed: %s", filepath.Base(s.bin), tail(s.errBuf.String(), 2000))
	}
}

func (s *CursorSession) notify(method string, params any) error {
	return s.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (s *CursorSession) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.stdin.Write(append(b, '\n'))
	return err
}

// Prompt sends one user turn and streams session/update events to fn.
func (s *CursorSession) Prompt(ctx context.Context, text string, fn func(Event)) (*Result, error) {
	if !s.promptMu.TryLock() {
		return nil, errors.New("session prompt already in progress")
	}
	defer s.promptMu.Unlock()

	s.mu.Lock()
	if s.closed || s.dead {
		s.mu.Unlock()
		return nil, errors.New("session is closed")
	}
	s.inTurn, s.inTurnUpdates, s.updates = true, true, nil
	sessionID := s.id
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.inTurn, s.inTurnUpdates, s.updates = false, false, nil
		s.mu.Unlock()
	}()

	id := s.reqID.Add(1)
	ch := make(chan acpResponse, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()

	if err := s.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "session/prompt",
		"params": map[string]any{
			"sessionId": sessionID,
			"prompt":    []map[string]any{{"type": "text", "text": text}},
		},
	}); err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		s.markDead()
		return nil, err
	}

	start := time.Now()
	log := logr.FromContextOrDiscard(ctx)
	tr := &acpTurnLog{log: log}
	handle := func() {
		for _, upd := range s.takeUpdates() {
			if fn != nil {
				fn(Event{Type: "session/update", Subtype: upd.Kind, Line: upd.Line})
			}
			tr.consume(upd)
		}
	}
	finish := func(resp acpResponse) (*Result, error) {
		// Updates are written before the JSON-RPC response, so the queue
		// holds the rest of the answer.
		handle()
		tr.flushEnd()
		res := &Result{SessionID: sessionID, Text: tr.answer}
		if resp.Error != nil {
			return res, fmt.Errorf("%s", resp.Error.Error())
		}
		return s.finishPrompt(res, resp.Result, time.Since(start))
	}
	ctxCh := ctx.Done()
	var cancelTimer <-chan time.Time
	for {
		select {
		case <-s.updSignal:
			handle()
		case resp := <-ch:
			return finish(resp)
		case <-ctxCh:
			_ = s.Cancel(context.Background())
			ctxCh, cancelTimer = nil, time.After(sessionCancelWait)
		case <-cancelTimer:
			s.markDead()
			if s.cmd.Process != nil {
				_ = s.cmd.Process.Signal(os.Interrupt)
			}
			select {
			case <-s.exited():
			case <-time.After(sessionCloseWait):
				s.kill()
			}
			return nil, ctx.Err()
		case <-s.exited():
			select {
			case resp := <-ch:
				return finish(resp)
			default:
			}
			s.mu.Lock()
			delete(s.pending, id)
			s.mu.Unlock()
			tr.flushEnd()
			s.markDead()
			return nil, fmt.Errorf("%s failed: %s", filepath.Base(s.bin), tail(s.errBuf.String(), 2000))
		}
	}
}

// finishPrompt reads the PromptResponse. Its optional usage (ACP schema:
// inputTokens, outputTokens, cachedReadTokens, cachedWriteTokens) becomes
// CallUsage with the duration the runner measured (ACP reports no
// duration). cursor-agent 2026.10.22 sends no usage, see the spend spec.
func (s *CursorSession) finishPrompt(res *Result, result json.RawMessage, took time.Duration) (*Result, error) {
	var out struct {
		StopReason string `json:"stopReason"`
		Usage      *struct {
			InputTokens       int64 `json:"inputTokens"`
			OutputTokens      int64 `json:"outputTokens"`
			CachedReadTokens  int64 `json:"cachedReadTokens"`
			CachedWriteTokens int64 `json:"cachedWriteTokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(result, &out)
	if u := out.Usage; u != nil {
		res.CallUsage = &CallUsage{
			InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
			CacheReadTokens: u.CachedReadTokens, CacheWriteTokens: u.CachedWriteTokens,
			DurationMS: took.Milliseconds(),
		}
	}
	switch out.StopReason {
	case "end_turn", "":
		return res, nil
	case "cancelled":
		return res, errors.New("turn was cancelled")
	default:
		res.IsError = true
		return res, fmt.Errorf("%s stopReason %s", filepath.Base(s.bin), out.StopReason)
	}
}

// Cancel sends session/cancel when a turn is in progress.
func (s *CursorSession) Cancel(context.Context) error {
	s.mu.Lock()
	idle := s.closed || s.dead || !s.inTurn
	id := s.id
	s.mu.Unlock()
	if idle {
		return nil
	}
	return s.notify("session/cancel", map[string]any{"sessionId": id})
}

// Close ends the process: stdin is closed, then SIGTERM, then kill.
func (s *CursorSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed, s.dead = true, true
	s.mu.Unlock()

	s.writeMu.Lock()
	_ = s.stdin.Close()
	s.writeMu.Unlock()

	select {
	case <-s.exited():
	case <-time.After(sessionStdinCloseWait):
		s.signalTerm()
		select {
		case <-s.exited():
		case <-time.After(sessionCloseWait):
			s.kill()
			<-s.exited()
		}
	}

	s.promptMu.Lock()
	defer s.promptMu.Unlock()
	return s.waitErr
}

func (s *CursorSession) markDead() { s.mu.Lock(); s.dead = true; s.mu.Unlock() }

// acpTurnLog aggregates thinking/message chunks for transcript-style logs.
type acpTurnLog struct {
	log     logr.Logger
	thought strings.Builder
	message strings.Builder
	answer  string
}

func (t *acpTurnLog) consume(upd acpUpdate) {
	switch upd.Kind {
	case "agent_thought_chunk":
		t.logMessage("agent says")
		t.thought.WriteString(acpChunkText(upd.Update))
	case "agent_message_chunk":
		t.flushThought()
		t.message.WriteString(acpChunkText(upd.Update))
	case "tool_call":
		t.flushThought()
		t.logMessage("agent says") // message before a tool is not the answer
		title, kind, rawIn := acpToolFields(upd.Update)
		tool := title
		if tool == "" {
			tool = kind
		}
		input := ""
		if len(rawIn) > 0 && string(rawIn) != "null" {
			input = textutil.Truncate(string(rawIn), 500)
			if s := toolInput("", rawIn); s != "" {
				input = s
			}
		}
		t.log.Info("agent uses a tool", "tool", tool, "input", input)
	case "available_commands_update", "session_info_update", "tool_call_update":
		// ignored
	}
}

func (t *acpTurnLog) flushThought() {
	if t.thought.Len() == 0 {
		return
	}
	if text := strings.TrimSpace(t.thought.String()); text != "" {
		t.log.Info("agent thinks", "text", textutil.Truncate(text, maxLogText))
	}
	t.thought.Reset()
}

func (t *acpTurnLog) logMessage(msg string) {
	if t.message.Len() == 0 {
		return
	}
	if text := strings.TrimSpace(t.message.String()); text != "" {
		t.log.Info(msg, "text", textutil.Truncate(text, maxLogText))
	}
	t.message.Reset()
}

func (t *acpTurnLog) flushEnd() {
	t.flushThought()
	if t.message.Len() == 0 {
		return
	}
	t.answer = t.message.String()
	t.message.Reset()
	t.log.Info("agent answers")
}

func acpChunkText(update json.RawMessage) string {
	var u struct {
		Content struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	_ = json.Unmarshal(update, &u)
	return u.Content.Text
}

func acpToolFields(update json.RawMessage) (title, kind string, rawInput json.RawMessage) {
	var u struct {
		Title    string          `json:"title"`
		Kind     string          `json:"kind"`
		RawInput json.RawMessage `json:"rawInput"`
	}
	_ = json.Unmarshal(update, &u)
	return u.Title, u.Kind, u.RawInput
}
