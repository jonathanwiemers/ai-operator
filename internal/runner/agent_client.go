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

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

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

// Message writes a user message into the agent's running stream-input call
// (LocalAgent.Message).
func (c *AgentClient) Message(ctx context.Context, req MessageRequest) (MessageResult, error) {
	var resp messageResponse
	if err := c.call(ctx, "message", req, &resp); err != nil {
		return MessageResult{}, err
	}
	return resp.MessageResult, remoteErr(resp.Error)
}

// Stop stops the agent's call in flight gracefully (LocalAgent.Stop).
func (c *AgentClient) Stop(ctx context.Context) error {
	var resp errorResponse
	if err := c.call(ctx, "stop", nil, &resp); err != nil {
		return err
	}
	return remoteErr(resp.Error)
}

func (c *AgentClient) OpenSession(ctx context.Context, engine aiv1.Engine, inv Invocation) (string, error) {
	var resp openSessionResponse
	if err := c.call(ctx, "session/open", openSessionRequest{Engine: engine, Invocation: inv}, &resp); err != nil {
		return "", err
	}
	return resp.SessionID, remoteErr(resp.Error)
}

// PromptSession drives one turn and streams events to fn. A stream that ends
// without a final line is an agentConnError (the agent container was lost).
func (c *AgentClient) PromptSession(ctx context.Context, text string, fn func(Event)) (*Result, error) {
	body, err := json.Marshal(promptSessionRequest{Text: text})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://agent/session/prompt", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if id := runIDFrom(ctx); id != "" {
		httpReq.Header.Set(runHeader, id)
	}
	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			// Our own cancel, not a lost agent container.
			return nil, ctx.Err()
		}
		return nil, &agentConnError{method: "session/prompt", err: err}
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return nil, fmt.Errorf("agent session/prompt: %s: %s", httpResp.Status, b)
	}

	// A turn streams for as long as it runs: no limit on the whole body,
	// the events are bounded by what the engines print per line.
	dec := json.NewDecoder(httpResp.Body)
	for {
		var line sessionPromptLine
		if err := dec.Decode(&line); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(err, io.EOF) {
				return nil, &agentConnError{method: "session/prompt", err: io.ErrUnexpectedEOF}
			}
			var syntax *json.SyntaxError
			var typ *json.UnmarshalTypeError
			if errors.As(err, &syntax) || errors.As(err, &typ) {
				return nil, err
			}
			return nil, &agentConnError{method: "session/prompt", err: err}
		}
		if line.Event != nil {
			if fn != nil {
				fn(*line.Event)
			}
			continue
		}
		return line.Result, remoteErr(line.Error)
	}
}

func (c *AgentClient) CancelSession(ctx context.Context) error {
	var resp errorResponse
	if err := c.call(ctx, "session/cancel", nil, &resp); err != nil {
		return err
	}
	return remoteErr(resp.Error)
}

func (c *AgentClient) CloseSession(ctx context.Context) error {
	var resp errorResponse
	if err := c.call(ctx, "session/close", nil, &resp); err != nil {
		return err
	}
	return remoteErr(resp.Error)
}

func (c *AgentClient) RemoteRefs(ctx context.Context, repo string) (map[string]string, error) {
	var resp refsResponse
	if err := c.call(ctx, "refs", repoRequest{Repo: repo}, &resp); err != nil {
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

func (c *AgentClient) Commit(ctx context.Context, repo, message string) (bool, error) {
	var resp commitResponse
	if err := c.call(ctx, "commit", commitRequest{Repo: repo, Message: message}, &resp); err != nil {
		return false, err
	}
	return resp.Committed, remoteErr(resp.Error)
}

func (c *AgentClient) CommitStep(ctx context.Context, req CommitRequest) (CommitResult, error) {
	var resp commitStepResponse
	if err := c.call(ctx, "commit-step", req, &resp); err != nil {
		return CommitResult{}, err
	}
	return resp.Result, remoteErr(resp.Error)
}

func (c *AgentClient) Unfold(ctx context.Context, req UnfoldRequest) (string, error) {
	var resp unfoldResponse
	if err := c.call(ctx, "unfold", req, &resp); err != nil {
		return "", err
	}
	return resp.Head, remoteErr(resp.Error)
}

func (c *AgentClient) Diff(ctx context.Context, repo, base string) (string, string, error) {
	var resp diffResponse
	if err := c.call(ctx, "diff", diffRequest{Repo: repo, Base: base}, &resp); err != nil {
		return "", "", err
	}
	return resp.Diff, resp.Stat, remoteErr(resp.Error)
}

func (c *AgentClient) Reset(ctx context.Context, repo string) error {
	var resp errorResponse
	if err := c.call(ctx, "reset", repoRequest{Repo: repo}, &resp); err != nil {
		return err
	}
	return remoteErr(resp.Error)
}

func (c *AgentClient) Bundle(ctx context.Context, req BundleRequest) (bool, error) {
	var resp bundleResponse
	if err := c.call(ctx, "bundle", req, &resp); err != nil {
		return false, err
	}
	return resp.Written, remoteErr(resp.Error)
}

func (c *AgentClient) MergeBase(ctx context.Context, req MergeRequest) (MergeResult, error) {
	var resp mergeResponse
	if err := c.call(ctx, "merge-base", req, &resp); err != nil {
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

func (c *AgentClient) Checkpoint(ctx context.Context, req CheckpointRequest) (CheckpointResult, error) {
	var resp checkpointResponse
	if err := c.call(ctx, "checkpoint", req, &resp); err != nil {
		return CheckpointResult{}, err
	}
	return resp.Result, remoteErr(resp.Error)
}

// planContextRequest names the folder PlanContext copies.
type planContextRequest struct {
	Src string `json:"src"`
}

func (c *AgentClient) PlanContext(ctx context.Context, src string) error {
	var resp errorResponse
	if err := c.call(ctx, "plan-context", planContextRequest{Src: src}, &resp); err != nil {
		return err
	}
	return remoteErr(resp.Error)
}
