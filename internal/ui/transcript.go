package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

// LogSource streams a container's log, following it until the container
// stops or ctx ends.
type LogSource interface {
	Stream(ctx context.Context, namespace, pod, container string) (io.ReadCloser, error)
}

// PodLogs reads container logs through the Kubernetes API.
type PodLogs struct{ Client kubernetes.Interface }

// Stream implements LogSource.
func (p PodLogs) Stream(ctx context.Context, namespace, pod, container string) (io.ReadCloser, error) {
	return p.Client.CoreV1().Pods(namespace).GetLogs(pod, &corev1.PodLogOptions{Container: container, Follow: true}).Stream(ctx)
}

// Entry is one line of a run's live transcript as the browser gets it: the
// runner's steps and the agent's session. Only these fields ever leave the
// server; the log lines they come from are never passed on.
type Entry struct {
	Time string `json:"time,omitempty"`
	Run  string `json:"run,omitempty"`
	// Kind: step, cli, thinks, says, tool, answers, prompt.
	Kind string `json:"kind"`
	// Step: its number, name and status (started, done, failed, deferred)
	// and result.
	StepNo int    `json:"stepNo,omitempty"`
	Step   string `json:"step,omitempty"`
	Status string `json:"status,omitempty"`
	Result string `json:"result,omitempty"`
	// Agent calls (cli, prompt): which agent.
	Role     string `json:"role,omitempty"`
	Engine   string `json:"engine,omitempty"`
	Model    string `json:"model,omitempty"`
	ReadOnly bool   `json:"readOnly,omitempty"`
	// Text of thinks and says; tool and its main argument.
	Text  string `json:"text,omitempty"`
	Tool  string `json:"tool,omitempty"`
	Input string `json:"input,omitempty"`
	// System prompt and prompt (prompt, with agents.logPrompts).
	System string `json:"system,omitempty"`
	Prompt string `json:"prompt,omitempty"`
}

// maxLogLine skips log lines longer than this (a logged prompt is at most
// 2×64 KiB plus escaping).
const maxLogLine = 1 << 20

// parseLogLine turns a runner or agent container log line into an entry.
// Lines of other kinds, and lines that are no JSON, give none.
func parseLogLine(line []byte) (Entry, bool) {
	var l map[string]any
	if json.Unmarshal(line, &l) != nil {
		return Entry{}, false
	}
	str := func(k string) string {
		switch v := l[k].(type) {
		case string:
			return v
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			return strconv.FormatBool(v)
		}
		return ""
	}
	e := Entry{Time: str("ts"), Run: str("run")}
	switch msg := str("msg"); msg {
	case "step started", "step done", "step failed", "step deferred":
		e.Kind, e.Step, e.Status = "step", str("name"), msg[len("step "):]
		e.StepNo, _ = strconv.Atoi(str("step"))
		e.Result = str("result")
		if e.Result == "" {
			e.Result = firstOf(str("error"), str("reason"))
		}
	case "agent CLI started":
		e.Kind, e.Role, e.Engine, e.Model, e.ReadOnly = "cli", str("role"), str("engine"), str("model"), str("readOnly") == "true"
	case "agent thinks":
		e.Kind, e.Text = "thinks", str("text")
	case "agent says":
		e.Kind, e.Text = "says", str("text")
	case "agent uses a tool":
		e.Kind, e.Tool, e.Input = "tool", str("tool"), str("input")
	case "agent answers":
		e.Kind = "answers"
	case "agent prompt":
		e.Kind, e.Role, e.Engine, e.Model = "prompt", str("role"), str("engine"), str("model")
		e.System, e.Prompt = str("system"), str("prompt")
	default:
		return Entry{}, false
	}
	return e, true
}

func firstOf(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// transcriptContainers are read for a task's pod: the runner's steps and
// the agent's session.
var transcriptContainers = []string{"runner", "agent"}

// transcript streams the live transcript of a task's pod (server-sent
// events): what the containers logged so far, then live, until the pod's
// containers stop, the client leaves or the session expires.
func (s *Server) transcript(w http.ResponseWriter, r *http.Request) {
	ap, tasks, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	var pod, ns string
	found := false
	for i := range tasks {
		if tasks[i].Name == r.PathValue("task") {
			found, pod, ns = true, tasks[i].Status.PodName, tasks[i].Namespace
		}
	}
	p := principalOf(r)
	switch {
	case !found:
		notFound(w)
		return
	case !s.authz.CanSeeTranscript(p, ap.Namespace, ap.Name):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "no access to transcripts"})
		return
	case s.logs == nil:
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "transcripts are not available"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.internalError(w, fmt.Errorf("streaming not supported"))
		return
	}
	if s.sessions != nil {
		release, ok := s.acquireStream(p.Username)
		if !ok {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many open event streams"})
			return
		}
		defer release()
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// No automatic reconnect: the app opens a new stream when the task's pod
	// changes.
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	end := func(reason string) {
		b, _ := json.Marshal(map[string]string{"reason": reason})
		fmt.Fprintf(w, "event: end\ndata: %s\n\n", b)
		flusher.Flush()
	}
	if pod == "" {
		end("no pod")
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	entries := make(chan Entry, 64)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var streamErr error
	for _, c := range transcriptContainers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rc, err := s.logs.Stream(ctx, ns, pod, c)
			if err != nil {
				errMu.Lock()
				streamErr = err
				errMu.Unlock()
				return
			}
			defer rc.Close()
			rd := bufio.NewReaderSize(rc, 64<<10)
			for {
				line, err := rd.ReadSlice('\n')
				if err == bufio.ErrBufferFull {
					// A long line (a prompt): read it whole, unless it is
					// absurdly long.
					rest, rerr := rd.ReadBytes('\n')
					line = append(append([]byte{}, line...), rest...)
					err = rerr
				}
				if len(line) <= maxLogLine {
					if e, ok := parseLogLine(line); ok {
						select {
						case entries <- e:
						case <-ctx.Done():
							return
						}
					}
				}
				if err != nil {
					return
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	var expired <-chan time.Time
	if exp := sessionExpiry(r); !exp.IsZero() {
		t := time.NewTimer(time.Until(exp))
		defer t.Stop()
		expired = t.C
	}
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-expired:
			end("session expired")
			return
		case <-tick.C:
			fmt.Fprint(w, ": ping\n\n")
		case e := <-entries:
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "event: entry\ndata: %s\n\n", b)
		case <-done:
			// Entries still buffered go out first.
			for {
				select {
				case e := <-entries:
					b, _ := json.Marshal(e)
					fmt.Fprintf(w, "event: entry\ndata: %s\n\n", b)
					continue
				default:
				}
				break
			}
			errMu.Lock()
			err := streamErr
			errMu.Unlock()
			if err != nil {
				s.log.V(1).Info("transcript stream ended", "pod", pod, "error", err.Error())
				end("pod not available")
			} else {
				end("pod stopped")
			}
			return
		}
		flusher.Flush()
	}
}
