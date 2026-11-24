package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
)

// errNoRunningCall is a stop with no call to stop: none runs, or it answered.
var errNoRunningCall = errors.New("no call is running")

// Stop asks the call in flight (LocalAgent.Run) to stop gracefully: its CLI
// gets SIGINT, and Run returns a partial result with Stopped and the session
// ID seen so far. An error when no call runs or it answered already.
func (a *LocalAgent) Stop(context.Context) error {
	a.liveMu.Lock()
	c := a.running
	a.liveMu.Unlock()
	if c == nil || !c.interrupt() {
		return errNoRunningCall
	}
	return nil
}

// setRunning fills or (nil) empties the slot of the call in flight; calls on
// the agent are sequential, so one slot does.
func (a *LocalAgent) setRunning(c *runningCall) {
	a.liveMu.Lock()
	defer a.liveMu.Unlock()
	a.running = c
}

// runningKey holds the *runningCall runCLI reports its process and session
// to.
type runningKey struct{}

// runningCall is the CLI process of the call in flight, for Stop.
type runningCall struct {
	mu      sync.Mutex
	proc    *os.Process
	closeIn func() // a stream-input call's stdin; nil for others
	session string
	// stop: Stop was asked before the result line came; answered: the
	// result line came first, the call is no longer stopped.
	stop, answered bool
}

// started hands the process (and closeIn, maybe nil) over; a stop asked
// before reaches it now.
func (c *runningCall) started(p *os.Process, closeIn func()) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.proc, c.closeIn = p, closeIn
	if c.stop {
		c.signal()
	}
}

// interrupt sends SIGINT unless the call answered; false then.
func (c *runningCall) interrupt() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.answered {
		return false
	}
	c.stop = true
	if c.proc != nil {
		c.signal()
	}
	return true
}

// signal sends SIGINT and closes stdin, so neither a CLI waiting for more
// input nor a child of it holding stdout keeps the call alive. c.mu is held.
func (c *runningCall) signal() {
	_ = c.proc.Signal(os.Interrupt)
	if c.closeIn != nil {
		c.closeIn()
	}
}

// line notes the session ID of a stream line (the CLI's init line has it)
// and whether it is the result.
func (c *runningCall) line(line []byte, result bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if result && !c.stop {
		c.answered = true
	}
	if c.session != "" || !bytes.Contains(line, []byte(`"session_id"`)) {
		return
	}
	var ev struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(line, &ev) == nil {
		c.session = ev.SessionID
	}
}

// stopped returns the session ID seen and whether a stop was sent.
func (c *runningCall) stopped() (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session, c.stop
}

// stoppedResult is the partial result of a call stopped before its answer:
// the error result the CLI may print on SIGINT, or an empty one, with the
// usage the stream showed. It is no error: the runner asked for the stop.
func stoppedResult(last *Result, usage *CallUsage, tr *turnReader, session string) *Result {
	res := &Result{}
	if last != nil {
		res = last
	}
	res.Stopped, res.IsError = true, false
	if res.SessionID == "" {
		res.SessionID = session
	}
	res.CallUsage, res.Usage = usage, tr.usage
	tr.addCallUsage(res)
	return res
}
