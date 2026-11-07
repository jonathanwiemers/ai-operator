package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-logr/logr"
)

var _ Session = (*ClaudeSession)(nil)

// ClaudeSession is a long-lived `claude` process using stream-json input.
type ClaudeSession struct {
	bin                  string
	stdin                io.WriteCloser
	lines                <-chan []byte
	errBuf               *boundWriter
	promptMu, writeMu    sync.Mutex
	mu                   sync.Mutex
	id                   string
	dead, closed, inTurn bool
	reqSeq               atomic.Uint64
	// prev holds the running totals of the process's previous result; nil
	// before the first. Only Prompt (under promptMu) touches it.
	prev *turnTotals
	sessionProc
}

// turnTotals are the figures of a claude result that sum up over the turns
// of one process: modelUsage and total_cost_usd (of the session) and
// subagent_stats.spawned (of the process).
type turnTotals struct {
	cost      float64
	models    map[string]ModelUsage
	subagents int32
}

// StartSession opens a ClaudeSession. It implements SessionEngine.
func (c *ClaudeCLI) StartSession(ctx context.Context, inv Invocation) (Session, error) {
	return c.Start(ctx, inv)
}

var _ SessionEngine = (*ClaudeCLI)(nil)

// Start runs claude with stream-json input. The process lives until Close;
// inv.Prompt is ignored (turns go through Prompt).
// The context does not bound the process: a session outlives the call that
// starts it.
func (c *ClaudeCLI) Start(_ context.Context, inv Invocation) (*ClaudeSession, error) {
	bin := orDefault(c.Binary, "claude")
	cmd := exec.Command(bin, append(slices.Clip(c.Args(inv)), "--input-format", "stream-json")...)
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
	lines := make(chan []byte, 16)
	go func() {
		defer close(lines)
		rd := bufio.NewReader(stdout)
		for {
			line, err := rd.ReadBytes('\n')
			if line = bytes.TrimSpace(line); len(line) > 0 {
				cp := make([]byte, len(line))
				copy(cp, line)
				lines <- cp
			}
			if err != nil {
				return
			}
		}
	}()
	return &ClaudeSession{bin: bin, stdin: stdin, lines: lines, errBuf: errBuf, sessionProc: sessionProc{cmd: cmd}}, nil
}

func (s *ClaudeSession) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// Prompt sends one user turn and reads until its result line. A cancelled
// context interrupts the turn; without a result after sessionCancelWait the
// process is killed and the session is dead.
func (s *ClaudeSession) Prompt(ctx context.Context, text string, fn func(Event)) (*Result, error) {
	if !s.promptMu.TryLock() {
		return nil, errors.New("session prompt already in progress")
	}
	defer s.promptMu.Unlock()

	s.mu.Lock()
	if s.closed || s.dead {
		s.mu.Unlock()
		return nil, errors.New("session is closed")
	}
	s.inTurn = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.inTurn = false; s.mu.Unlock() }()

	// Lines the CLI wrote after the last turn's result belong to no turn.
	for drained := false; !drained; {
		select {
		case _, ok := <-s.lines:
			drained = !ok
		default:
			drained = true
		}
	}
	if err := s.writeJSON(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": []map[string]any{{"type": "text", "text": text}},
		},
	}); err != nil {
		s.markDead()
		return nil, err
	}

	tr := &turnReader{log: logr.FromContextOrDiscard(ctx)}
	ctxCh := ctx.Done()
	var cancelTimer <-chan time.Time
	for {
		select {
		case line, ok := <-s.lines:
			if !ok {
				tr.flush()
				s.markDead()
				s.settle()
				return nil, fmt.Errorf("%s failed: %s", filepath.Base(s.bin), tail(s.errBuf.String(), 2000))
			}
			ev := parseEvent(line)
			if ev.Type == "system" && ev.Subtype == "init" {
				s.rememberID(line)
			}
			emit := func() {
				if fn != nil {
					fn(Event{Type: ev.Type, Subtype: ev.Subtype, Line: json.RawMessage(line)})
				}
			}
			if ev.Type != "control_response" && tr.consume(line) {
				s.rememberID(line)
				res, err := ParseResult(tr.result)
				if err != nil {
					return nil, err
				}
				res.Usage = tr.usage
				s.perTurn(tr.log, res)
				tr.addCallUsage(res)
				emit()
				if res.IsError {
					return res, fmt.Errorf("%s reported an error: %s", filepath.Base(s.bin), tail(res.Text, 2000))
				}
				return res, nil
			}
			emit()
		case <-ctxCh:
			_ = s.Cancel(context.Background())
			ctxCh, cancelTimer = nil, time.After(sessionCancelWait)
		case <-cancelTimer:
			// The CLI ignored the interrupt: SIGINT, then kill. The session
			// is over; the caller resumes it by ID in a new process.
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
		}
	}
}

// perTurn makes res hold this turn only (R7 of
// docs/specs/honest-reporting.spec.md). Models and cost are
// model_usage_since_last_result when the CLI sent it, else modelUsage and
// total_cost_usd minus the previous result's (a model missing there counts
// from zero, a model without change is dropped). The subagent count is
// subagent_stats minus the previous result's. A shrinking figure is clamped
// to zero and logged. The first result of a process keeps its figures:
// after --resume without model_usage_since_last_result they include the
// session's earlier calls, for which no baseline is stored.
func (s *ClaudeSession) perTurn(log logr.Logger, res *Result) {
	prev, u := s.prev, res.CallUsage
	cur := &turnTotals{cost: res.CostUSD, models: map[string]ModelUsage{}}
	if u != nil {
		cur.subagents = u.SubagentCalls
		for _, m := range u.Models {
			cur.models[m.Model] = m
		}
	}
	s.prev = cur
	since := sinceLastResult(res)
	if prev == nil {
		return
	}
	shrank := false
	d := func(a, b int64) int64 {
		if a < b {
			shrank = true
			return 0
		}
		return a - b
	}
	df := func(a, b float64) float64 {
		if a < b {
			shrank = true
			return 0
		}
		return a - b
	}
	if !since {
		res.CostUSD = df(res.CostUSD, prev.cost)
		for name := range prev.models {
			if _, ok := cur.models[name]; !ok {
				shrank = true
			}
		}
		if u != nil {
			var models []ModelUsage
			for _, m := range u.Models {
				p := prev.models[m.Model]
				m = ModelUsage{Model: m.Model, InputTokens: d(m.InputTokens, p.InputTokens), OutputTokens: d(m.OutputTokens, p.OutputTokens),
					CacheReadTokens: d(m.CacheReadTokens, p.CacheReadTokens), CacheWriteTokens: d(m.CacheWriteTokens, p.CacheWriteTokens),
					CostUSD: df(m.CostUSD, p.CostUSD)}
				if m != (ModelUsage{Model: m.Model}) {
					models = append(models, m)
				}
			}
			u.Models = models
		}
	}
	if u != nil {
		u.SubagentCalls = int32(d(int64(u.SubagentCalls), int64(prev.subagents)))
	}
	if shrank {
		log.Info("claude session totals shrank between turns, clamped to zero",
			"session", res.SessionID, "costUSD", cur.cost, "previousCostUSD", prev.cost,
			"subagents", cur.subagents, "previousSubagents", prev.subagents)
	}
}

// Cancel asks claude to interrupt the turn (a control request); if stdin
// is gone it sends SIGINT instead.
func (s *ClaudeSession) Cancel(context.Context) error {
	s.mu.Lock()
	idle := s.closed || s.dead || !s.inTurn
	s.mu.Unlock()
	if idle {
		return nil
	}
	err := s.writeJSON(map[string]any{
		"type":       "control_request",
		"request_id": fmt.Sprintf("cancel-%d", s.reqSeq.Add(1)),
		"request":    map[string]any{"subtype": "interrupt"},
	})
	if err != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(os.Interrupt)
	}
	return nil
}

// Close ends the process: stdin is closed, and after sessionCloseWait the
// process group is killed.
func (s *ClaudeSession) Close() error {
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
	case <-time.After(sessionCloseWait):
		s.kill()
		<-s.exited()
	}
	// A turn still running sees the end of the stream; then nothing reads
	// the lines any more, so the reader goroutine must not block on them.
	s.promptMu.Lock()
	defer s.promptMu.Unlock()
	go func() {
		for range s.lines {
		}
	}()
	return s.waitErr
}

func (s *ClaudeSession) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.stdin.Write(append(b, '\n'))
	return err
}

func (s *ClaudeSession) rememberID(line []byte) {
	var raw struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(line, &raw) != nil || raw.SessionID == "" {
		return
	}
	s.mu.Lock()
	s.id = raw.SessionID
	s.mu.Unlock()
}

func (s *ClaudeSession) markDead() { s.mu.Lock(); s.dead = true; s.mu.Unlock() }
