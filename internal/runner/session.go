package runner

import (
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Timeouts for Session implementations; tests shorten them.
var (
	sessionCloseWait      = 10 * time.Second
	sessionCancelWait     = 30 * time.Second
	sessionStartupTimeout = 60 * time.Second
	sessionStdinCloseWait = 2 * time.Second // cursor-agent ignores stdin EOF
)

// boundWriter keeps the last max bytes written (stderr tail).
type boundWriter struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (w *boundWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.max {
		w.buf = append([]byte(nil), w.buf[len(w.buf)-w.max:]...)
	}
	return len(p), nil
}

func (w *boundWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf)
}

// sessionProc waits for and kills a session process group.
type sessionProc struct {
	cmd      *exec.Cmd
	waitOnce sync.Once
	done     chan struct{}
	waitErr  error
}

func (p *sessionProc) exited() <-chan struct{} {
	p.waitOnce.Do(func() {
		p.done = make(chan struct{})
		go func() {
			p.waitErr = p.cmd.Wait()
			close(p.done)
		}()
	})
	return p.done
}

// stderrSettleWait bounds how long a session whose stdout closed waits for
// its process to exit, so the stderr tail of its error is complete.
const stderrSettleWait = 2 * time.Second

// settle waits, at most stderrSettleWait, for the process to exit. Its
// stderr is copied into the session's buffer by exec until cmd.Wait
// returns, so stdout's EOF can come before the last stderr lines are in.
func (p *sessionProc) settle() {
	select {
	case <-p.exited():
	case <-time.After(stderrSettleWait):
	}
}

func (p *sessionProc) kill() {
	if p.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	_ = p.cmd.Process.Kill()
}

func (p *sessionProc) signalTerm() {
	if p.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
}
