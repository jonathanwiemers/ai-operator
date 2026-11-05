package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Entry is one line of a run's live transcript as the browser gets it: the
// runner's steps and the agent's session. Only these fields ever leave the
// server; the log lines they come from are never passed on.
type Entry struct {
	Time string `json:"time,omitempty"`
	Run  string `json:"run,omitempty"`
	// Kind: step, cli, thinks, says, tool, answers, prompt.
	Kind string `json:"kind" tstype:"'step' | 'cli' | 'thinks' | 'says' | 'tool' | 'answers' | 'prompt'"`
	// Step: its number, name and status (started, done, failed, deferred)
	// and result.
	StepNo int    `json:"stepNo,omitempty"`
	Step   string `json:"step,omitempty"`
	Status string `json:"status,omitempty" tstype:"'started' | 'done' | 'failed' | 'deferred'"`
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
	// Subagents (claude): a tool line that starts one has ToolUseID and
	// Subagent (its type); the subagent's thinks, says, tool and answers
	// lines have Parent (that ToolUseID), Model and, once per message,
	// Tokens (input + output + cache).
	Parent    string `json:"parent,omitempty"`
	ToolUseID string `json:"toolUseID,omitempty"`
	Subagent  string `json:"subagent,omitempty"`
	Tokens    int64  `json:"tokens,omitempty"`
}

// maxLogLine skips log lines longer than this (a logged prompt is at most
// 2×64 KiB plus escaping); they are never held in memory whole.
const maxLogLine = 1 << 20

// readLine returns the next line of rd. A line longer than max is read to
// its end but not kept: tooLong is set and line is nil.
func readLine(rd *bufio.Reader, max int) (line []byte, tooLong bool, err error) {
	for {
		chunk, err := rd.ReadSlice('\n')
		// chunk points into rd's buffer: copy it before the next read.
		if !tooLong {
			if len(line)+len(chunk) > max {
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if err != bufio.ErrBufferFull {
			return line, tooLong, err
		}
	}
}

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
	switch e.Kind {
	case "thinks", "says", "tool", "answers":
		if e.Parent = str("parent"); e.Parent != "" {
			e.Model = str("model")
			e.Tokens, _ = strconv.ParseInt(str("tokens"), 10, 64)
		}
		if e.Kind == "tool" {
			e.ToolUseID, e.Subagent = str("toolUseID"), str("subagent")
		}
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

// reopenDelay and maxReopens: a container that restarts (out of memory) ends
// its log stream; while the pod runs, it is followed again.
var (
	reopenDelay = 2 * time.Second
	maxReopens  = 20
)

// transcript streams the live transcript of a task's pod (server-sent
// events): what the current run logged so far, then live, until the pod
// stops, the client leaves or the session expires.
func (s *Server) transcript(w http.ResponseWriter, r *http.Request) {
	ap, tasks, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	var pod, ns, run string
	found := false
	for i := range tasks {
		if t := &tasks[i]; t.Name == r.PathValue("task") {
			found, ns = true, t.Namespace
			// Pods are named after their task. The status only says whether
			// one exists: the agent pods may write it, so it must not decide
			// whose logs are read.
			if t.Status.PodName != "" {
				pod = t.Name
			}
			// The run in progress, else the last one. A deferred run (usage
			// limit, out-of-memory retry) sets activeRunRequest back to the
			// last finished one; the requested run is the one to show.
			req := t.Status.Run.ActiveRunRequest
			if t.Spec.RunRequest > t.Status.Run.ObservedRunRequest {
				req = t.Spec.RunRequest
			}
			if req > 0 {
				run = fmt.Sprintf("%s#%d", t.Name, req)
			}
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
	// No automatic reconnect: the app opens a new stream when the task's
	// run changes.
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
	var failedMu sync.Mutex
	failed := 0
	for _, c := range transcriptContainers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !s.followContainer(ctx, ns, pod, c, run, entries) {
				failedMu.Lock()
				failed++
				failedMu.Unlock()
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	send := func(e Entry) {
		b, _ := json.Marshal(e)
		fmt.Fprintf(w, "event: entry\ndata: %s\n\n", b)
	}
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
			send(e)
		case <-done:
			// Entries still buffered go out first.
			for drained := false; !drained; {
				select {
				case e := <-entries:
					send(e)
				default:
					drained = true
				}
			}
			failedMu.Lock()
			all := failed == len(transcriptContainers)
			failedMu.Unlock()
			if all {
				end("pod not available")
			} else {
				end("pod stopped")
			}
			return
		}
		flusher.Flush()
	}
}

// followContainer sends a container's transcript entries of run (all runs
// if empty) until its log ends for good: the pod stopped or ctx ended. A
// log that ends while the pod runs (a restarted container) is followed again
// from the last entry's time. It reports whether the log could be read at
// all.
func (s *Server) followContainer(ctx context.Context, ns, pod, container, run string, out chan<- Entry) bool {
	opened := false
	var since *time.Time
	last := ""
	for reopen := 0; reopen <= maxReopens; reopen++ {
		rc, err := s.logs.Stream(ctx, ns, pod, container, since)
		if err != nil {
			s.log.V(1).Info("cannot read a container log", "pod", pod, "container", container, "error", err.Error())
			return opened
		}
		opened = true
		rd := bufio.NewReaderSize(rc, 64<<10)
		for {
			line, tooLong, err := readLine(rd, maxLogLine)
			if !tooLong {
				// Entries of other runs, and entries seen before a reopen, are
				// skipped (log times have whole seconds at worst, so a reopen
				// from the last time repeats some).
				if e, ok := parseLogLine(line); ok && (run == "" || e.Run == "" || e.Run == run) && (e.Time == "" || e.Time > last || since == nil) {
					if e.Time != "" {
						last = e.Time
					}
					select {
					case out <- e:
					case <-ctx.Done():
						rc.Close()
						return true
					}
				}
			}
			if err != nil {
				break
			}
		}
		rc.Close()
		select {
		case <-ctx.Done():
			return true
		case <-time.After(reopenDelay):
		}
		if !s.logs.Running(ctx, ns, pod) {
			return true
		}
		// Without a time seen, the restarted container's log is read from
		// its start (since nil reads the current container only).
		if t, err := time.Parse(time.RFC3339Nano, last); err == nil {
			since = &t
		}
	}
	return true
}
