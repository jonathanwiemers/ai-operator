package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// Agent is everything the runner needs from the agent container: running
// agent CLIs and operating on the workspace checkout. The runner treats all
// answers as untrusted data.
type Agent interface {
	Ready(ctx context.Context) error
	Run(ctx context.Context, engine aiv1.Engine, inv Invocation) (*Result, error)
	RemoteRefs(ctx context.Context) (map[string]string, error)
	Prepare(ctx context.Context, req PrepareRequest) error
	Commit(ctx context.Context, message string) (bool, error)
	Diff(ctx context.Context, base string) (diff, stat string, err error)
	Reset(ctx context.Context) error
	Bundle(ctx context.Context, branch, base, path string) (bool, error)
	MergeBase(ctx context.Context, base, message string) (MergeResult, error)
	FinishMerge(ctx context.Context, req FinishMerge) (MergeResult, error)
}

// LocalAgent implements Agent in-process. The agent server wraps it; the
// runner uses it directly only in single-container mode (development).
type LocalAgent struct {
	Workspace *Workspace
	Engines   map[aiv1.Engine]Engine
	// Exchange is the only directory bundles may be read from or written to.
	Exchange string
}

var _ Agent = (*LocalAgent)(nil)

func (a *LocalAgent) Ready(context.Context) error { return nil }

// Run runs an agent CLI in the workspace checkout.
func (a *LocalAgent) Run(ctx context.Context, engine aiv1.Engine, inv Invocation) (*Result, error) {
	eng, ok := a.Engines[engine]
	if !ok {
		return nil, fmt.Errorf("engine %q is not available in the agent image", engine)
	}
	if err := os.MkdirAll(a.Workspace.Dir, 0o755); err != nil {
		return nil, err
	}
	inv.Dir = a.Workspace.Dir
	return eng.Run(ctx, inv)
}

func (a *LocalAgent) RemoteRefs(ctx context.Context) (map[string]string, error) {
	return a.Workspace.RemoteRefs(ctx)
}

func (a *LocalAgent) Prepare(ctx context.Context, req PrepareRequest) error {
	if req.Bundle != "" {
		if err := a.checkPath(req.Bundle); err != nil {
			return err
		}
	}
	return a.Workspace.Prepare(ctx, req)
}

func (a *LocalAgent) Commit(ctx context.Context, message string) (bool, error) {
	return a.Workspace.Commit(ctx, message)
}

func (a *LocalAgent) Diff(ctx context.Context, base string) (string, string, error) {
	return a.Workspace.Diff(ctx, base)
}

func (a *LocalAgent) Reset(ctx context.Context) error { return a.Workspace.Reset(ctx) }

func (a *LocalAgent) Bundle(ctx context.Context, branch, base, path string) (bool, error) {
	if err := a.checkPath(path); err != nil {
		return false, err
	}
	return a.Workspace.Bundle(ctx, branch, base, path)
}

func (a *LocalAgent) MergeBase(ctx context.Context, base, message string) (MergeResult, error) {
	return a.Workspace.MergeBase(ctx, base, message)
}

func (a *LocalAgent) FinishMerge(ctx context.Context, req FinishMerge) (MergeResult, error) {
	return a.Workspace.FinishMerge(ctx, req)
}

func (a *LocalAgent) checkPath(path string) error {
	rel, err := filepath.Rel(a.Exchange, filepath.Clean(path))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("path %q is outside the exchange directory", path)
	}
	return nil
}

// The RPC protocol: POST /<method> with a JSON request, JSON response with an
// optional error. It runs over a unix socket on the volume shared by the
// runner and agent containers.

type runRequest struct {
	Engine     aiv1.Engine `json:"engine"`
	Invocation Invocation  `json:"invocation"`
}

type runResponse struct {
	Result *Result `json:"result,omitempty"`
	Error  string  `json:"error,omitempty"`
}

type commitResponse struct {
	Committed bool   `json:"committed"`
	Error     string `json:"error,omitempty"`
}

type diffResponse struct {
	Diff  string `json:"diff"`
	Stat  string `json:"stat"`
	Error string `json:"error,omitempty"`
}

type bundleRequest struct {
	Branch string `json:"branch"`
	Base   string `json:"base"`
	Path   string `json:"path"`
}

type bundleResponse struct {
	Written bool   `json:"written"`
	Error   string `json:"error,omitempty"`
}

type mergeRequest struct {
	Base    string `json:"base"`
	Message string `json:"message"`
}

type mergeResponse struct {
	Result MergeResult `json:"result"`
	Error  string      `json:"error,omitempty"`
}

type refsResponse struct {
	Refs  map[string]string `json:"refs"`
	Error string            `json:"error,omitempty"`
}

type errorResponse struct {
	Error string `json:"error,omitempty"`
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// AgentServer exposes a LocalAgent over a unix socket. It runs in the agent
// container.
type AgentServer struct {
	Agent *LocalAgent
	// Log receives one line per call (the readiness probe only at debug
	// level). Prompts, diffs and commit messages are never logged.
	Log logr.Logger
}

// reply is the outcome of one call: the response body, the error (already
// part of the body) and extra fields for the log line.
type reply struct {
	body any
	err  error
	kv   []any
}

// runHeader carries the runner's run ID, so agent log lines can be matched
// with the runner's.
const runHeader = "X-AI-Run"

// Handler returns the HTTP handler of the RPC protocol.
func (s *AgentServer) Handler() http.Handler {
	mux := http.NewServeMux()
	handle := func(name string, fn func(ctx context.Context, body []byte) reply) {
		mux.HandleFunc("POST /"+name, func(w http.ResponseWriter, r *http.Request) {
			log := s.Log.WithValues("rpc", name)
			if id := r.Header.Get(runHeader); id != "" {
				log = log.WithValues("run", id)
			}
			start := time.Now()
			body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
			if err != nil {
				log.Error(err, "reading request")
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			rep := fn(logr.NewContext(r.Context(), log), body)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rep.body)
			kv := append([]any{"durationMs", time.Since(start).Milliseconds()}, rep.kv...)
			switch {
			case rep.err != nil:
				log.Error(rep.err, "call failed", kv...)
			case name == "ready":
				log.V(1).Info("call done", kv...)
			default:
				log.Info("call done", kv...)
			}
		})
	}
	handle("ready", func(context.Context, []byte) reply { return reply{body: errorResponse{}} })
	handle("run", func(ctx context.Context, body []byte) reply {
		var req runRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{runResponse{Error: err.Error()}, err, nil}
		}
		inv := req.Invocation
		logr.FromContextOrDiscard(ctx).Info("agent CLI started", "engine", req.Engine, "model", inv.Model,
			"readOnly", inv.ReadOnly, "resume", inv.Resume != "", "promptBytes", len(inv.Prompt))
		res, err := s.Agent.Run(ctx, req.Engine, inv)
		kv := []any{"engine", req.Engine, "model", inv.Model}
		if res != nil {
			kv = append(kv, "session", res.SessionID, "costUSD", res.CostUSD, "isError", res.IsError)
		}
		return reply{runResponse{Result: res, Error: errString(err)}, err, kv}
	})
	handle("refs", func(ctx context.Context, _ []byte) reply {
		refs, err := s.Agent.RemoteRefs(ctx)
		return reply{refsResponse{Refs: refs, Error: errString(err)}, err, []any{"refs", len(refs)}}
	})
	handle("prepare", func(ctx context.Context, body []byte) reply {
		var req PrepareRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{errorResponse{Error: err.Error()}, err, nil}
		}
		err := s.Agent.Prepare(ctx, req)
		return reply{errorResponse{Error: errString(err)}, err, []any{"branch", req.Branch, "base", req.Base, "bundle", req.Bundle != ""}}
	})
	handle("commit", func(ctx context.Context, body []byte) reply {
		var msg string
		if err := json.Unmarshal(body, &msg); err != nil {
			return reply{commitResponse{Error: err.Error()}, err, nil}
		}
		ok, err := s.Agent.Commit(ctx, msg)
		return reply{commitResponse{Committed: ok, Error: errString(err)}, err, []any{"committed", ok}}
	})
	handle("diff", func(ctx context.Context, body []byte) reply {
		var base string
		if err := json.Unmarshal(body, &base); err != nil {
			return reply{diffResponse{Error: err.Error()}, err, nil}
		}
		diff, stat, err := s.Agent.Diff(ctx, base)
		return reply{diffResponse{Diff: diff, Stat: stat, Error: errString(err)}, err,
			[]any{"base", base, "diffBytes", len(diff), "summary", diffSummary(stat)}}
	})
	handle("reset", func(ctx context.Context, _ []byte) reply {
		err := s.Agent.Reset(ctx)
		return reply{errorResponse{Error: errString(err)}, err, nil}
	})
	handle("bundle", func(ctx context.Context, body []byte) reply {
		var req bundleRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{bundleResponse{Error: err.Error()}, err, nil}
		}
		ok, err := s.Agent.Bundle(ctx, req.Branch, req.Base, req.Path)
		return reply{bundleResponse{Written: ok, Error: errString(err)}, err, []any{"branch", req.Branch, "written", ok}}
	})
	handle("merge-base", func(ctx context.Context, body []byte) reply {
		var req mergeRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{mergeResponse{Error: err.Error()}, err, nil}
		}
		res, err := s.Agent.MergeBase(ctx, req.Base, req.Message)
		return reply{mergeResponse{Result: res, Error: errString(err)}, err,
			[]any{"base", req.Base, "upToDate", res.UpToDate, "merged", res.Merged, "conflicts", len(res.Conflicts)}}
	})
	handle("finish-merge", func(ctx context.Context, body []byte) reply {
		var req FinishMerge
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{mergeResponse{Error: err.Error()}, err, nil}
		}
		res, err := s.Agent.FinishMerge(ctx, req)
		return reply{mergeResponse{Result: res, Error: errString(err)}, err,
			[]any{"base", req.Base, "abort", req.Abort, "merged", res.Merged, "conflicts", len(res.Conflicts)}}
	})
	return mux
}

// Serve listens on the unix socket until ctx is cancelled.
func (s *AgentServer) Serve(ctx context.Context, socket string) error {
	_ = os.Remove(socket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		ln.Close()
		return err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// AgentClient talks to an AgentServer over its unix socket.
type AgentClient struct {
	http *http.Client
}

var _ Agent = (*AgentClient)(nil)

// NewAgentClient returns a client for the server listening on socket.
func NewAgentClient(socket string) *AgentClient {
	return &AgentClient{http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}}}
}

func (c *AgentClient) call(ctx context.Context, method string, req, resp any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://agent/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if id := runIDFrom(ctx); id != "" {
		httpReq.Header.Set(runHeader, id)
	}
	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return &agentConnError{method: method, err: err}
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return fmt.Errorf("agent %s: %s: %s", method, httpResp.Status, b)
	}
	if err := json.NewDecoder(io.LimitReader(httpResp.Body, 64<<20)).Decode(resp); err != nil {
		var syntax *json.SyntaxError
		var typ *json.UnmarshalTypeError
		if errors.As(err, &syntax) || errors.As(err, &typ) {
			return err
		}
		// The body broke off: the server went away while answering.
		return &agentConnError{method: method, err: err}
	}
	return nil
}

// agentConnError is a call that got no answer from the agent server, e.g.
// because its container was killed and restarted.
type agentConnError struct {
	method string
	err    error
}

func (e *agentConnError) Error() string { return fmt.Sprintf("agent %s: %v", e.method, e.err) }
func (e *agentConnError) Unwrap() error { return e.err }

// notSent reports whether the request never reached the server (it was down
// when the call was made), so making it again cannot repeat any work.
func (e *agentConnError) notSent() bool {
	var oe *net.OpError
	return errors.As(e.err, &oe) && oe.Op == "dial"
}

func remoteErr(s string) error {
	if s == "" {
		return nil
	}
	return errors.New(s)
}

func (c *AgentClient) Ready(ctx context.Context) error {
	var resp errorResponse
	if err := c.call(ctx, "ready", nil, &resp); err != nil {
		return err
	}
	return remoteErr(resp.Error)
}

func (c *AgentClient) Run(ctx context.Context, engine aiv1.Engine, inv Invocation) (*Result, error) {
	var resp runResponse
	if err := c.call(ctx, "run", runRequest{Engine: engine, Invocation: inv}, &resp); err != nil {
		return nil, err
	}
	return resp.Result, remoteErr(resp.Error)
}

func (c *AgentClient) RemoteRefs(ctx context.Context) (map[string]string, error) {
	var resp refsResponse
	if err := c.call(ctx, "refs", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Refs, remoteErr(resp.Error)
}

func (c *AgentClient) Prepare(ctx context.Context, req PrepareRequest) error {
	var resp errorResponse
	if err := c.call(ctx, "prepare", req, &resp); err != nil {
		return err
	}
	return remoteErr(resp.Error)
}

func (c *AgentClient) Commit(ctx context.Context, message string) (bool, error) {
	var resp commitResponse
	if err := c.call(ctx, "commit", message, &resp); err != nil {
		return false, err
	}
	return resp.Committed, remoteErr(resp.Error)
}

func (c *AgentClient) Diff(ctx context.Context, base string) (string, string, error) {
	var resp diffResponse
	if err := c.call(ctx, "diff", base, &resp); err != nil {
		return "", "", err
	}
	return resp.Diff, resp.Stat, remoteErr(resp.Error)
}

func (c *AgentClient) Reset(ctx context.Context) error {
	var resp errorResponse
	if err := c.call(ctx, "reset", nil, &resp); err != nil {
		return err
	}
	return remoteErr(resp.Error)
}

func (c *AgentClient) Bundle(ctx context.Context, branch, base, path string) (bool, error) {
	var resp bundleResponse
	if err := c.call(ctx, "bundle", bundleRequest{Branch: branch, Base: base, Path: path}, &resp); err != nil {
		return false, err
	}
	return resp.Written, remoteErr(resp.Error)
}

func (c *AgentClient) MergeBase(ctx context.Context, base, message string) (MergeResult, error) {
	var resp mergeResponse
	if err := c.call(ctx, "merge-base", mergeRequest{Base: base, Message: message}, &resp); err != nil {
		return MergeResult{}, err
	}
	return resp.Result, remoteErr(resp.Error)
}

func (c *AgentClient) FinishMerge(ctx context.Context, req FinishMerge) (MergeResult, error) {
	var resp mergeResponse
	if err := c.call(ctx, "finish-merge", req, &resp); err != nil {
		return MergeResult{}, err
	}
	return resp.Result, remoteErr(resp.Error)
}
