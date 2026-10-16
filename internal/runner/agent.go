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
}

// Handler returns the HTTP handler of the RPC protocol.
func (s *AgentServer) Handler() http.Handler {
	mux := http.NewServeMux()
	handle := func(name string, fn func(ctx context.Context, body []byte) any) {
		mux.HandleFunc("POST /"+name, func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(fn(r.Context(), body))
		})
	}
	handle("ready", func(context.Context, []byte) any { return errorResponse{} })
	handle("run", func(ctx context.Context, body []byte) any {
		var req runRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return runResponse{Error: err.Error()}
		}
		res, err := s.Agent.Run(ctx, req.Engine, req.Invocation)
		return runResponse{Result: res, Error: errString(err)}
	})
	handle("refs", func(ctx context.Context, _ []byte) any {
		refs, err := s.Agent.RemoteRefs(ctx)
		return refsResponse{Refs: refs, Error: errString(err)}
	})
	handle("prepare", func(ctx context.Context, body []byte) any {
		var req PrepareRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return errorResponse{Error: err.Error()}
		}
		return errorResponse{Error: errString(s.Agent.Prepare(ctx, req))}
	})
	handle("commit", func(ctx context.Context, body []byte) any {
		var msg string
		if err := json.Unmarshal(body, &msg); err != nil {
			return commitResponse{Error: err.Error()}
		}
		ok, err := s.Agent.Commit(ctx, msg)
		return commitResponse{Committed: ok, Error: errString(err)}
	})
	handle("diff", func(ctx context.Context, body []byte) any {
		var base string
		if err := json.Unmarshal(body, &base); err != nil {
			return diffResponse{Error: err.Error()}
		}
		diff, stat, err := s.Agent.Diff(ctx, base)
		return diffResponse{Diff: diff, Stat: stat, Error: errString(err)}
	})
	handle("reset", func(ctx context.Context, _ []byte) any {
		return errorResponse{Error: errString(s.Agent.Reset(ctx))}
	})
	handle("bundle", func(ctx context.Context, body []byte) any {
		var req bundleRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return bundleResponse{Error: err.Error()}
		}
		ok, err := s.Agent.Bundle(ctx, req.Branch, req.Base, req.Path)
		return bundleResponse{Written: ok, Error: errString(err)}
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
	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("agent %s: %w", method, err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return fmt.Errorf("agent %s: %s: %s", method, httpResp.Status, b)
	}
	return json.NewDecoder(io.LimitReader(httpResp.Body, 64<<20)).Decode(resp)
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
