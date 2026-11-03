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
