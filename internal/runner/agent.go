package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/agentlanguage"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
)

// Agent is everything the runner needs from the agent container: running
// agent CLIs and operating on the workspace checkout. The runner treats all
// answers as untrusted data.
type Agent interface {
	Ready(ctx context.Context) error
	Run(ctx context.Context, engine aiv1.Engine, inv Invocation) (*Result, error)
	// OpenSession starts a long-lived CLI session. At most one session is
	// open: opening while one is open closes the old one first. The returned
	// ID is the engine's (empty until the CLI reports it).
	OpenSession(ctx context.Context, engine aiv1.Engine, inv Invocation) (sessionID string, err error)
	// PromptSession sends one turn on the open session.
	PromptSession(ctx context.Context, text string, fn func(Event)) (*Result, error)
	// CancelSession stops the turn in progress; no-op without a turn.
	CancelSession(ctx context.Context) error
	// CloseSession ends the open session; idempotent.
	CloseSession(ctx context.Context) error
	// The calls on a tree take its repo: homeTree ("") for the home
	// checkout, else the companion's project slug (naming.ProjectSlug). The
	// agent side maps it to a directory itself and refuses anything else.
	RemoteRefs(ctx context.Context, repo string) (map[string]string, error)
	Prepare(ctx context.Context, req PrepareRequest) error
	Commit(ctx context.Context, repo, message string) (bool, error)
	// CommitStep commits a step of the run in the home tree, folding the
	// checkpoints at the tip of the branch (Workspace.CommitStep).
	CommitStep(ctx context.Context, req CommitRequest) (CommitResult, error)
	// Unfold undoes a fold in the home tree whose push was refused.
	Unfold(ctx context.Context, req UnfoldRequest) (string, error)
	Diff(ctx context.Context, repo, base string) (diff, stat string, err error)
	Reset(ctx context.Context, repo string) error
	Bundle(ctx context.Context, req BundleRequest) (bool, error)
	// MergeBase is for the home tree only: a companion request is refused.
	MergeBase(ctx context.Context, req MergeRequest) (MergeResult, error)
	FinishMerge(ctx context.Context, req FinishMerge) (MergeResult, error)
	// Checkpoint commits the home work tree of a run that was cut off.
	Checkpoint(ctx context.Context, req CheckpointRequest) (CheckpointResult, error)
	// PlanContext replaces the home checkout's .plan-context folder with a
	// copy of src, a directory inside the exchange directory.
	PlanContext(ctx context.Context, src string) error
	// Message writes a user message into the running stream-input call
	// (Invocation.StreamInput) and says whether its replay showed up; an
	// error without such a call.
	Message(ctx context.Context, req MessageRequest) (MessageResult, error)
	// Stop sends SIGINT to the call in flight (Run): Run then returns a
	// partial Result with Stopped and the session ID seen so far, without an
	// error. An error when no call runs or it answered already.
	Stop(ctx context.Context) error
}

// LocalAgent implements Agent in-process. The agent server wraps it; the
// runner uses it directly only in single-container mode (development).
type LocalAgent struct {
	// Workspace is the home checkout (/workspace/repo).
	Workspace *Workspace
	// Workspaces is the root of the agent's trees (AI_WORKSPACE,
	// "/workspace"): a companion's checkout is <Workspaces>/repos/<slug>.
	// Empty: every companion request is refused.
	Workspaces string
	Engines    map[aiv1.Engine]Engine
	// Exchange is the only directory bundles may be read from or written to.
	Exchange string
	// StepGateDir is where the step-size gate keeps its per-session state
	// (ai-runner step-gate --state-dir); empty is the temp dir, the gate's
	// default.
	StepGateDir string
	// Language is the language tag every call writes in (AI_AGENT_LANGUAGE,
	// the operator's --agent-language): prepareInvocation appends its rule
	// (agentlanguage.WithLanguageRule) to the system prompt. Empty: no rule.
	Language string

	mu      sync.Mutex
	session Session
	sessEng aiv1.Engine

	// live is the stream-input call in flight, for Message; running is the
	// call in flight on any engine, for Stop.
	liveMu  sync.Mutex
	live    *liveInput
	running *runningCall
}

var _ Agent = (*LocalAgent)(nil)

func (a *LocalAgent) Ready(context.Context) error { return nil }

// Run runs an agent CLI in the workspace checkout.
func (a *LocalAgent) Run(ctx context.Context, engine aiv1.Engine, inv Invocation) (*Result, error) {
	eng, ok := a.Engines[engine]
	if !ok {
		return nil, fmt.Errorf("engine %q is not available in the agent image", engine)
	}
	if err := a.prepareInvocation(ctx, engine, &inv); err != nil {
		return nil, err
	}
	if !inv.ReadOnly && a.Workspace.exists() {
		// A call that may edit keeps its status where a checkpoint finds it,
		// also when this container or its pod does not survive the call.
		w := &statusWriter{path: a.Workspace.lastStatusPath(), every: statusInterval}
		_ = os.Remove(w.path)
		ctx = context.WithValue(ctx, statusSinkKey{}, w.set)
		defer w.flush()
	}
	// Sessions are resumed across calls and the gate's count is per
	// session: the call's nudges are the difference.
	nudgesBefore := StepGateNudges(a.StepGateDir, inv.Resume)
	if inv.StreamInput {
		ctx = context.WithValue(ctx, liveInputKey{}, a.setLive)
		defer a.setLive(nil)
	}
	rc := &runningCall{}
	a.setRunning(rc)
	defer a.setRunning(nil)
	res, err := eng.Run(context.WithValue(ctx, runningKey{}, rc), inv)
	if inv.CountLines && !inv.ReadOnly && res != nil && a.Workspace.exists() {
		a.countLines(ctx, res)
	}
	if inv.CountLines && res != nil {
		a.countNudges(res, inv.Resume, nudgesBefore)
	}
	return res, err
}

// countNudges adds the step-size gate's nudges of the call to
// res.CallUsage: the count of the result's session minus before, the count
// of the resumed session before the call (only when the call kept that
// session; a new session starts at zero).
func (a *LocalAgent) countNudges(res *Result, resumed string, before int) {
	if res.SessionID != resumed {
		before = 0
	}
	n := StepGateNudges(a.StepGateDir, res.SessionID) - before
	if n <= 0 {
		return
	}
	if res.CallUsage == nil {
		res.CallUsage = &CallUsage{}
	}
	// The count comes from a state file in the agent container, which the
	// model can write: saturate instead of wrapping.
	if n > math.MaxInt32 {
		n = math.MaxInt32
	}
	res.CallUsage.StepGateNudges = int32(n)
}

// countLinesTimeout bounds the count, which also runs after a call that
// timed out.
const countLinesTimeout = 30 * time.Second

// countLines adds the uncommitted lines the call left in the home tree to
// res.CallUsage. A failed count leaves them zero: it is a measurement.
func (a *LocalAgent) countLines(ctx context.Context, res *Result) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), countLinesTimeout)
	defer cancel()
	added, deleted, err := a.Workspace.UncommittedLines(ctx)
	if err != nil {
		logr.FromContextOrDiscard(ctx).Info("could not count the uncommitted lines", "error", err.Error())
		return
	}
	if res.CallUsage == nil {
		res.CallUsage = &CallUsage{}
	}
	res.CallUsage.LinesAdded, res.CallUsage.LinesDeleted = added, deleted
}

// statusInterval is how often at most the last status is written.
var statusInterval = 5 * time.Second

// statusWriter writes the newest status text of a call to path, at most
// once per interval; flush writes what is left. One goroutine uses it.
type statusWriter struct {
	path    string
	every   time.Duration
	last    time.Time
	pending string
}

func (w *statusWriter) set(text string) {
	w.pending = text
	if time.Since(w.last) >= w.every {
		w.flush()
	}
}

func (w *statusWriter) flush() {
	if w.pending == "" {
		return
	}
	_ = os.WriteFile(w.path, []byte(w.pending), 0o600)
	w.pending, w.last = "", time.Now()
}

// statusSinkKey holds the function the engine calls with the agent's status
// text (streamStatus) as the call streams.
type statusSinkKey struct{}

// statusSink is the function under statusSinkKey, or nil.
func statusSink(ctx context.Context) func(string) {
	fn, _ := ctx.Value(statusSinkKey{}).(func(string))
	return fn
}

// prepareInvocation sets Dir, AddDirs and Env and appends the language rule like Run, for one-shot calls
// and sessions. The companion slug resolves to its checkout like the repo
// of a tree call; only claude can work on a second checkout.
func (a *LocalAgent) prepareInvocation(ctx context.Context, engine aiv1.Engine, inv *Invocation) error {
	inv.AddDirs = nil
	if inv.Companion != "" {
		if engine != aiv1.EngineClaude {
			return fmt.Errorf("engine %q cannot work on the companion checkout %q: only claude can (--add-dir)", engine, inv.Companion)
		}
		w, err := a.tree(inv.Companion)
		if err != nil {
			return fmt.Errorf("companion: %w", err)
		}
		inv.AddDirs = []string{w.Dir}
	}
	if err := os.MkdirAll(a.Workspace.Dir, 0o755); err != nil { // #nosec G301 -- the checkout, git's own mode for a work tree; the pod runs every container as uid/gid 1000
		return err
	}
	inv.Dir = a.Workspace.Dir
	env, err := perRunToolchains(ctx, inv.PerRunToolchains)
	if err != nil {
		return err
	}
	// The role's cap on Bash output (bashMaxOutputChars), claude only:
	// per call, since all roles of a task share the pod.
	if engine == aiv1.EngineClaude && inv.BashMaxOutputChars > 0 {
		env = append(env, fmt.Sprintf("BASH_MAX_OUTPUT_LENGTH=%d", inv.BashMaxOutputChars))
	}
	// Last, so the chart setting wins over the project's instructions.
	inv.SystemPrompt = agentlanguage.WithLanguageRule(inv.SystemPrompt, a.Language)
	inv.Env = env
	return nil
}

// OpenSession starts an engine session in the workspace. Engines without
// SessionEngine return an error.
func (a *LocalAgent) OpenSession(ctx context.Context, engine aiv1.Engine, inv Invocation) (string, error) {
	eng, ok := a.Engines[engine]
	if !ok {
		return "", fmt.Errorf("engine %q is not available in the agent image", engine)
	}
	se, ok := eng.(SessionEngine)
	if !ok {
		return "", fmt.Errorf("engine %q has no sessions", engine)
	}
	if err := a.prepareInvocation(ctx, engine, &inv); err != nil {
		return "", err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	// The old session stays until the new one runs, so a failed open (auth,
	// model, handshake) leaves the agent as it was.
	sess, err := se.StartSession(ctx, inv)
	if err != nil {
		return "", err
	}
	if a.session != nil {
		_ = a.session.Close()
	}
	a.session, a.sessEng = sess, engine
	return sess.ID(), nil
}

// PromptSession runs one turn on the open session.
func (a *LocalAgent) PromptSession(ctx context.Context, text string, fn func(Event)) (*Result, error) {
	a.mu.Lock()
	sess := a.session
	a.mu.Unlock()
	if sess == nil {
		return nil, errors.New("no open session")
	}
	return sess.Prompt(ctx, text, fn)
}

// CancelSession interrupts the turn in progress; no-op without a turn.
func (a *LocalAgent) CancelSession(ctx context.Context) error {
	a.mu.Lock()
	sess := a.session
	a.mu.Unlock()
	if sess == nil {
		return nil
	}
	return sess.Cancel(ctx)
}

// CloseSession ends the open session; idempotent.
func (a *LocalAgent) CloseSession(context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil {
		return nil
	}
	err := a.session.Close()
	a.session, a.sessEng = nil, ""
	return err
}

// sessionEngine returns the engine of the open session (for logging).
func (a *LocalAgent) sessionEngine() aiv1.Engine {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessEng
}

var toolchainsName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`)

// perRunToolchains returns the environment that points the toolchain
// installers at <tmp>/ai-toolchains/<name> (nothing for an empty name),
// after removing the directories of earlier runs: they are not kept.
func perRunToolchains(ctx context.Context, name string) ([]string, error) {
	if name != "" && !toolchainsName.MatchString(name) {
		return nil, fmt.Errorf("invalid name %q for per-run toolchains", name)
	}
	root := filepath.Join(os.TempDir(), "ai-toolchains")
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.Name() != name {
			if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
				logr.FromContextOrDiscard(ctx).Info("could not remove toolchains of an earlier run", "error", err.Error())
			}
		}
	}
	if name == "" {
		return nil, nil
	}
	var env []string
	for _, v := range [][2]string{{"MISE_DATA_DIR", "mise"}, {"RUSTUP_HOME", "rustup"}, {"CARGO_HOME", "cargo"}} {
		dir := filepath.Join(root, name, v[1])
		if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- per-run toolchain installs, no secret; the pod runs every container as uid/gid 1000
			return nil, err
		}
		env = append(env, v[0]+"="+dir)
	}
	return env, nil
}

// homeTree is the repo of the home checkout in the calls on a tree.
const homeTree = ""

// tree resolves the repo of a call to its checkout: homeTree is the home
// Workspace, a valid project slug is <Workspaces>/repos/<slug>. Anything
// else (a path, "..", an invalid slug) is refused, so the agent server
// only ever works in those two kinds of directories.
func (a *LocalAgent) tree(repo string) (*Workspace, error) {
	if repo == homeTree {
		return a.Workspace, nil
	}
	if !naming.ValidProjectSlug(repo) {
		return nil, fmt.Errorf("repo %q is not a valid project slug", repo)
	}
	if a.Workspaces == "" {
		return nil, fmt.Errorf("repo %q: the agent has no workspace root for companion checkouts", repo)
	}
	return &Workspace{Dir: CompanionDir(a.Workspaces, repo), AuthorName: a.Workspace.AuthorName, AuthorEmail: a.Workspace.AuthorEmail}, nil
}

// CompanionDir is the checkout of the companion with the given (valid)
// slug under the agent's workspace root.
func CompanionDir(root, slug string) string { return filepath.Join(root, "repos", slug) }

func (a *LocalAgent) RemoteRefs(ctx context.Context, repo string) (map[string]string, error) {
	w, err := a.tree(repo)
	if err != nil {
		return nil, err
	}
	return w.RemoteRefs(ctx)
}

func (a *LocalAgent) Prepare(ctx context.Context, req PrepareRequest) error {
	w, err := a.tree(req.Repo)
	if err != nil {
		return err
	}
	if req.Bundle != "" {
		if err := a.checkPath(req.Bundle); err != nil {
			return err
		}
	}
	return w.Prepare(ctx, req)
}

func (a *LocalAgent) Commit(ctx context.Context, repo, message string) (bool, error) {
	w, err := a.tree(repo)
	if err != nil {
		return false, err
	}
	return w.Commit(ctx, message)
}

func (a *LocalAgent) CommitStep(ctx context.Context, req CommitRequest) (CommitResult, error) {
	return a.Workspace.CommitStep(ctx, req)
}

func (a *LocalAgent) Unfold(ctx context.Context, req UnfoldRequest) (string, error) {
	return a.Workspace.Unfold(ctx, req)
}

func (a *LocalAgent) Diff(ctx context.Context, repo, base string) (string, string, error) {
	w, err := a.tree(repo)
	if err != nil {
		return "", "", err
	}
	return w.Diff(ctx, base)
}

func (a *LocalAgent) Reset(ctx context.Context, repo string) error {
	w, err := a.tree(repo)
	if err != nil {
		return err
	}
	return w.Reset(ctx)
}

func (a *LocalAgent) Bundle(ctx context.Context, req BundleRequest) (bool, error) {
	w, err := a.tree(req.Repo)
	if err != nil {
		return false, err
	}
	if err := a.checkPath(req.Path); err != nil {
		return false, err
	}
	return w.Bundle(ctx, req.Branch, req.Base, req.Path)
}

// MergeBase merges the default branch into the home tree. A companion has
// no use for it, so any other repo is refused.
func (a *LocalAgent) MergeBase(ctx context.Context, req MergeRequest) (MergeResult, error) {
	if req.Repo != homeTree {
		return MergeResult{}, fmt.Errorf("repo %q: merging the base branch is for the home checkout only", req.Repo)
	}
	return a.Workspace.MergeBase(ctx, req.Base, req.Message)
}

func (a *LocalAgent) FinishMerge(ctx context.Context, req FinishMerge) (MergeResult, error) {
	w, err := a.tree(req.Repo)
	if err != nil {
		return MergeResult{}, err
	}
	return w.FinishMerge(ctx, req)
}

func (a *LocalAgent) Checkpoint(ctx context.Context, req CheckpointRequest) (CheckpointResult, error) {
	return a.Workspace.Checkpoint(ctx, req)
}

// PlanContext copies the folder the runner wrote: the exchange is another
// volume, so a rename would fail. git status does not show it (planExclude).
func (a *LocalAgent) PlanContext(_ context.Context, src string) error {
	if err := a.checkPath(src); err != nil {
		return err
	}
	dst := filepath.Join(a.Workspace.Dir, planContextDir)
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		return err
	}
	return planExclude(a.Workspace.Dir)
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

type messageResponse struct {
	MessageResult
	Error string `json:"error,omitempty"`
}

type commitResponse struct {
	Committed bool   `json:"committed"`
	Error     string `json:"error,omitempty"`
}

type commitStepResponse struct {
	Result CommitResult `json:"result"`
	Error  string       `json:"error,omitempty"`
}

type unfoldResponse struct {
	Head  string `json:"head"`
	Error string `json:"error,omitempty"`
}

type diffResponse struct {
	Diff  string `json:"diff"`
	Stat  string `json:"stat"`
	Error string `json:"error,omitempty"`
}

// repoRequest is the request of the calls on a tree that take nothing
// else (refs, reset).
type repoRequest struct {
	Repo string `json:"repo,omitempty"`
}

type commitRequest struct {
	Repo    string `json:"repo,omitempty"`
	Message string `json:"message"`
}

type diffRequest struct {
	Repo string `json:"repo,omitempty"`
	Base string `json:"base"`
}

// BundleRequest asks for the commits of Branch that origin does not have
// yet, written to Path inside the exchange directory.
type BundleRequest struct {
	// Repo is the tree: homeTree or a companion's project slug.
	Repo   string `json:"repo,omitempty"`
	Branch string `json:"branch"`
	Base   string `json:"base"`
	Path   string `json:"path"`
}

type bundleResponse struct {
	Written bool   `json:"written"`
	Error   string `json:"error,omitempty"`
}

// MergeRequest asks to merge origin/<Base> into the work branch.
type MergeRequest struct {
	// Repo must be homeTree: a companion request is refused.
	Repo    string `json:"repo,omitempty"`
	Base    string `json:"base"`
	Message string `json:"message"`
}

type mergeResponse struct {
	Result MergeResult `json:"result"`
	Error  string      `json:"error,omitempty"`
}

type checkpointResponse struct {
	Result CheckpointResult `json:"result"`
	Error  string           `json:"error,omitempty"`
}

type refsResponse struct {
	Refs  map[string]string `json:"refs"`
	Error string            `json:"error,omitempty"`
}

type errorResponse struct {
	Error string `json:"error,omitempty"`
}

type openSessionRequest struct {
	Engine     aiv1.Engine `json:"engine"`
	Invocation Invocation  `json:"invocation"`
}

type openSessionResponse struct {
	SessionID string `json:"sessionID"`
	Error     string `json:"error,omitempty"`
}

type promptSessionRequest struct {
	Text string `json:"text"`
}

// sessionPromptLine is one NDJSON line of session/prompt: an event as it
// happens, then a final result/error line.
type sessionPromptLine struct {
	Event  *Event  `json:"event,omitempty"`
	Result *Result `json:"result,omitempty"`
	Error  string  `json:"error,omitempty"`
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
	// Secrets are redacted from every response (writeJSON): the agent
	// container's credentials, textutil.EnvSecrets of its environment.
	Secrets []string
}

// writeJSON writes v as one JSON line with Secrets, known tokens and URL
// credentials redacted (textutil.Redact). Errors, answers and events carry
// what the agent and its CLIs printed, which may be a key the agent read
// with `env`; the runner posts them to GitLab and puts them into prompts.
// HTML is not escaped, so a secret holding <, > or & is found as it is.
func (s *AgentServer) writeJSON(w io.Writer, v any) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(v) == nil {
		_, _ = io.WriteString(w, textutil.Redact(b.String(), s.Secrets...))
	}
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
			s.writeJSON(w, rep.body)
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
		log := logr.FromContextOrDiscard(ctx)
		log.Info("agent CLI started", "role", inv.Role, "engine", req.Engine, "model", inv.Model,
			"readOnly", inv.ReadOnly, "resume", inv.Resume != "", "promptBytes", len(inv.Prompt), "perRunToolchains", inv.PerRunToolchains, "companion", inv.Companion)
		if inv.LogPrompt {
			// agents.logPrompts: what the agent is told, for the web UI.
			log.Info("agent prompt", "role", inv.Role, "engine", req.Engine, "model", inv.Model,
				"system", textutil.Truncate(inv.SystemPrompt, maxLogPrompt), "prompt", textutil.Truncate(inv.Prompt, maxLogPrompt))
		}
		res, err := s.Agent.Run(ctx, req.Engine, inv)
		kv := []any{"engine", req.Engine, "model", inv.Model}
		if res != nil {
			kv = append(kv, "session", res.SessionID, "costUSD", res.CostUSD, "isError", res.IsError)
		}
		return reply{runResponse{Result: res, Error: errString(err)}, err, kv}
	})
	handle("message", func(ctx context.Context, body []byte) reply {
		var req MessageRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{messageResponse{Error: err.Error()}, err, nil}
		}
		// The text is the owner's: only its ID and size are logged.
		res, err := s.Agent.Message(ctx, req)
		return reply{messageResponse{MessageResult: res, Error: errString(err)}, err,
			[]any{"message", req.ID, "bytes", len(req.Text), "delivered", res.Delivered, "reason", res.Reason}}
	})
	handle("stop", func(ctx context.Context, _ []byte) reply {
		err := s.Agent.Stop(ctx)
		return reply{errorResponse{Error: errString(err)}, err, nil}
	})
	handle("refs", func(ctx context.Context, body []byte) reply {
		var req repoRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{refsResponse{Error: err.Error()}, err, nil}
		}
		refs, err := s.Agent.RemoteRefs(ctx, req.Repo)
		return reply{refsResponse{Refs: refs, Error: errString(err)}, err, []any{"repo", req.Repo, "refs", len(refs)}}
	})
	handle("prepare", func(ctx context.Context, body []byte) reply {
		var req PrepareRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{errorResponse{Error: err.Error()}, err, nil}
		}
		err := s.Agent.Prepare(ctx, req)
		return reply{errorResponse{Error: errString(err)}, err, []any{"repo", req.Repo, "branch", req.Branch, "base", req.Base, "bundle", req.Bundle != ""}}
	})
	handle("commit", func(ctx context.Context, body []byte) reply {
		var req commitRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{commitResponse{Error: err.Error()}, err, nil}
		}
		ok, err := s.Agent.Commit(ctx, req.Repo, req.Message)
		return reply{commitResponse{Committed: ok, Error: errString(err)}, err, []any{"repo", req.Repo, "committed", ok}}
	})
	handle("commit-step", func(ctx context.Context, body []byte) reply {
		var req CommitRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{commitStepResponse{Error: err.Error()}, err, nil}
		}
		res, err := s.Agent.CommitStep(ctx, req)
		return reply{commitStepResponse{Result: res, Error: errString(err)}, err,
			[]any{"branch", req.Branch, "committed", res.Committed, "folded", res.Folded, "foldedFrom", res.FoldedFrom}}
	})
	handle("unfold", func(ctx context.Context, body []byte) reply {
		var req UnfoldRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{unfoldResponse{Error: err.Error()}, err, nil}
		}
		head, err := s.Agent.Unfold(ctx, req)
		return reply{unfoldResponse{Head: head, Error: errString(err)}, err, []any{"tip", req.Tip, "fold", req.Fold, "head", head}}
	})
	handle("diff", func(ctx context.Context, body []byte) reply {
		var req diffRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{diffResponse{Error: err.Error()}, err, nil}
		}
		diff, stat, err := s.Agent.Diff(ctx, req.Repo, req.Base)
		return reply{diffResponse{Diff: diff, Stat: stat, Error: errString(err)}, err,
			[]any{"repo", req.Repo, "base", req.Base, "diffBytes", len(diff), "summary", diffSummary(stat)}}
	})
	handle("reset", func(ctx context.Context, body []byte) reply {
		var req repoRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{errorResponse{Error: err.Error()}, err, nil}
		}
		err := s.Agent.Reset(ctx, req.Repo)
		return reply{errorResponse{Error: errString(err)}, err, []any{"repo", req.Repo}}
	})
	handle("bundle", func(ctx context.Context, body []byte) reply {
		var req BundleRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{bundleResponse{Error: err.Error()}, err, nil}
		}
		ok, err := s.Agent.Bundle(ctx, req)
		return reply{bundleResponse{Written: ok, Error: errString(err)}, err, []any{"repo", req.Repo, "branch", req.Branch, "written", ok}}
	})
	handle("merge-base", func(ctx context.Context, body []byte) reply {
		var req MergeRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{mergeResponse{Error: err.Error()}, err, nil}
		}
		res, err := s.Agent.MergeBase(ctx, req)
		return reply{mergeResponse{Result: res, Error: errString(err)}, err,
			[]any{"repo", req.Repo, "base", req.Base, "upToDate", res.UpToDate, "merged", res.Merged, "conflicts", len(res.Conflicts)}}
	})
	handle("finish-merge", func(ctx context.Context, body []byte) reply {
		var req FinishMerge
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{mergeResponse{Error: err.Error()}, err, nil}
		}
		res, err := s.Agent.FinishMerge(ctx, req)
		return reply{mergeResponse{Result: res, Error: errString(err)}, err,
			[]any{"repo", req.Repo, "base", req.Base, "abort", req.Abort, "merged", res.Merged, "conflicts", len(res.Conflicts)}}
	})
	handle("checkpoint", func(ctx context.Context, body []byte) reply {
		var req CheckpointRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{checkpointResponse{Error: err.Error()}, err, nil}
		}
		res, err := s.Agent.Checkpoint(ctx, req)
		return reply{checkpointResponse{Result: res, Error: errString(err)}, err,
			[]any{"branch", req.Branch, "reason", req.Reason, "sha", res.SHA, "files", res.Files,
				"skipped", len(res.Skipped), "aborted", res.Aborted, "refused", res.Refused}}
	})
	handle("plan-context", func(ctx context.Context, body []byte) reply {
		var req planContextRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{errorResponse{Error: err.Error()}, err, nil}
		}
		err := s.Agent.PlanContext(ctx, req.Src)
		return reply{errorResponse{Error: errString(err)}, err, []any{"src", req.Src}}
	})
	handle("session/open", func(ctx context.Context, body []byte) reply {
		var req openSessionRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return reply{openSessionResponse{Error: err.Error()}, err, nil}
		}
		id, err := s.Agent.OpenSession(ctx, req.Engine, req.Invocation)
		return reply{openSessionResponse{SessionID: id, Error: errString(err)}, err,
			[]any{"engine", req.Engine, "session", id}}
	})
	handle("session/cancel", func(ctx context.Context, _ []byte) reply {
		err := s.Agent.CancelSession(ctx)
		return reply{errorResponse{Error: errString(err)}, err, []any{"engine", s.Agent.sessionEngine()}}
	})
	handle("session/close", func(ctx context.Context, _ []byte) reply {
		eng := s.Agent.sessionEngine()
		err := s.Agent.CloseSession(ctx)
		return reply{errorResponse{Error: errString(err)}, err, []any{"engine", eng}}
	})
	mux.HandleFunc("POST /session/prompt", s.handleSessionPrompt)
	return mux
}

// handleSessionPrompt streams events as NDJSON, then a final result/error
// line. The request context bounds the turn: if the runner goes away the
// turn is cancelled, but the session stays open.
func (s *AgentServer) handleSessionPrompt(w http.ResponseWriter, r *http.Request) {
	log := s.Log.WithValues("rpc", "session/prompt")
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
	var req promptSessionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		s.writeJSON(w, sessionPromptLine{Error: err.Error()})
		log.Error(err, "call failed", "durationMs", time.Since(start).Milliseconds())
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	flusher, _ := w.(http.Flusher)
	write := func(line sessionPromptLine) {
		s.writeJSON(w, line)
		if flusher != nil {
			flusher.Flush()
		}
	}

	eng := s.Agent.sessionEngine()
	res, err := s.Agent.PromptSession(logr.NewContext(r.Context(), log), req.Text, func(ev Event) {
		write(sessionPromptLine{Event: &ev})
	})
	write(sessionPromptLine{Result: res, Error: errString(err)})

	kv := []any{"durationMs", time.Since(start).Milliseconds(), "engine", eng}
	if res != nil {
		kv = append(kv, "session", res.SessionID, "costUSD", res.CostUSD, "isError", res.IsError)
	}
	if err != nil {
		log.Error(err, "call failed", kv...)
	} else {
		log.Info("call done", kv...)
	}
}

// Serve listens on the unix socket until ctx is cancelled.
func (s *AgentServer) Serve(ctx context.Context, socket string) error {
	_ = os.Remove(socket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		ln.Close() // #nosec G104 -- cleanup on an error path; the Chmod error is returned
		return err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	// #nosec G118 -- the shutdown starts once ctx is done, so it needs a context of its own, bounded by its timeout
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
