package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-logr/logr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// Invocation is one headless call of an agent CLI. It is engine neutral; each
// engine maps the fields it supports onto its own flags.
type Invocation struct {
	// Dir is the working directory (the repository checkout). It is set by
	// the agent side, never by the runner.
	Dir string
	// Prompt is the task for this turn. It may be large (diffs, threads).
	Prompt string
	// SystemPrompt carries the role instructions. Keep it stable between
	// runs: for claude it is part of the cached prompt prefix.
	SystemPrompt string
	Model        string
	// Effort and MaxBudgetUSD are claude only.
	Effort       string
	MaxBudgetUSD string
	// AllowedTools / DisallowedTools are claude only.
	AllowedTools    []string
	DisallowedTools []string
	// ReadOnly marks roles that must not modify the work tree (reviewers,
	// planner). Engines without fine-grained tool control use it to withhold
	// write/command permissions; the runner also resets the work tree after
	// read-only roles.
	ReadOnly bool
	// JSONSchema requests structured output matching the schema.
	JSONSchema string
	// Resume continues an earlier session of the same engine, which keeps
	// the context (and for claude the prompt cache) instead of re-sending
	// the whole issue thread on follow-up runs.
	Resume string
	// NoPersistence skips writing a session to disk (one-shot reviewers).
	NoPersistence bool
	// Role names the agent for the logs: implement, spec, bugbot,
	// security, describe.
	Role string
	// LogPrompt makes the agent server log the system prompt and the prompt
	// (agents.logPrompts).
	LogPrompt bool
	// PerRunToolchains names the toolchain directories of this run, set when
	// the branch changes a pin file. The runner sends only the name; the
	// agent side validates it and turns it into Env.
	PerRunToolchains string `json:"perRunToolchains,omitempty"`
	// Env overrides variables of the engine's environment. It is set by the
	// agent side and never crosses the socket.
	Env []string `json:"-"`
}

// Result is the parsed outcome of an invocation.
type Result struct {
	SessionID string
	// CostUSD as reported by the engine (0 if unknown). With a subscription
	// this is the notional API price, useful to compare roles and tasks.
	CostUSD    float64
	IsError    bool
	Text       string
	Structured json.RawMessage
	// Usage is the engine's last report on its usage windows during the
	// call (claude's rate_limit_event); nil if it sent none.
	Usage *aiv1.EngineUsage
	// CallUsage is the call's token usage; nil if the engine reported none.
	CallUsage *CallUsage
	// Engine and Model ran the call (set by the runner); FallbackFrom is the
	// limited engine a fallback stood in for.
	Engine       aiv1.Engine `json:"-"`
	Model        string      `json:"-"`
	FallbackFrom aiv1.Engine `json:"-"`
}

// CallUsage is what one engine call consumed, as the engine reported it.
type CallUsage struct {
	InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens int64
	Turns                                                        int32
	DurationMS                                                   int64
	// Models is the per-model split as reported (subagent models included),
	// sorted by name.
	Models []ModelUsage
}

// ModelUsage is one model's share of a call.
type ModelUsage struct {
	Model                                                        string
	InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens int64
	CostUSD                                                      float64
}

// MainModel is the model with the most output tokens, for calls whose
// invocation set no model; "" if the engine reported no split.
func (u *CallUsage) MainModel() string {
	if u == nil {
		return ""
	}
	main, most := "", int64(-1)
	for _, m := range u.Models {
		if m.OutputTokens > most {
			main, most = m.Model, m.OutputTokens
		}
	}
	return main
}

// Engine runs invocations. Implementations wrap one agent CLI.
type Engine interface {
	Run(ctx context.Context, inv Invocation) (*Result, error)
}

// Session is a long-lived conversation with one agent CLI process.
type Session interface {
	// Prompt sends one user turn and streams the agent's events to fn
	// until the turn ends. One turn at a time.
	Prompt(ctx context.Context, text string, fn func(Event)) (*Result, error)
	// Cancel stops the turn in progress; Prompt then returns its result.
	Cancel(ctx context.Context) error
	// ID is the engine's session ID (for --resume), set as soon as the CLI
	// reports it.
	ID() string
	Close() error
}

// SessionEngine opens long-lived sessions. Engines that only support one-shot
// Run do not implement it.
type SessionEngine interface {
	StartSession(ctx context.Context, inv Invocation) (Session, error)
}

// Event is one line of the engine's event stream.
type Event struct {
	Type    string // assistant, user, tool_call, result, system, rate_limit_event, ...
	Subtype string
	Line    json.RawMessage // the raw line
}

// ClaudeCLI runs the Claude Code CLI. Authentication comes from the
// environment: CLAUDE_CODE_OAUTH_TOKEN (subscription) or ANTHROPIC_API_KEY.
type ClaudeCLI struct {
	// Binary defaults to "claude".
	Binary string
	// Env of the process; callers strip credentials the model should not see.
	Env []string
}

// Args returns the command line for an invocation (without the binary).
func (c *ClaudeCLI) Args(inv Invocation) []string {
	// stream-json prints the session as it happens (thinking, messages,
	// tool calls), so it can be logged; the last line is the result.
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits"}
	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}
	if inv.Effort != "" {
		args = append(args, "--effort", inv.Effort)
	}
	if inv.MaxBudgetUSD != "" {
		args = append(args, "--max-budget-usd", inv.MaxBudgetUSD)
	}
	if inv.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", inv.SystemPrompt)
	}
	if len(inv.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(inv.AllowedTools, ","))
	}
	if len(inv.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(inv.DisallowedTools, ","))
	}
	if inv.JSONSchema != "" {
		args = append(args, "--json-schema", inv.JSONSchema)
	}
	if inv.Resume != "" {
		args = append(args, "--resume", inv.Resume)
	}
	if inv.NoPersistence {
		args = append(args, "--no-session-persistence")
	}
	return args
}

// Run executes the invocation. The prompt goes through stdin, so it is not
// subject to command line length limits.
func (c *ClaudeCLI) Run(ctx context.Context, inv Invocation) (*Result, error) {
	return runCLI(ctx, orDefault(c.Binary, "claude"), c.Args(inv), inv.Dir, append(slices.Clip(c.Env), inv.Env...), inv.Prompt, true)
}

// CursorCLI runs the Cursor CLI (cursor-agent). Authentication comes from
// CURSOR_API_KEY in the environment.
//
// cursor-agent has no flags for a system prompt, tool allow-lists or JSON
// schemas, so those are folded into the prompt and the JSON answer is parsed
// from the text result.
type CursorCLI struct {
	// Binary defaults to "cursor-agent".
	Binary string
	Env    []string
	// PromptDir receives prompts that are too long for a command line
	// argument; defaults to <Dir>/../.ai-operator/prompts.
	PromptDir string
}

// maxArgPrompt stays well below Linux' 128 KiB limit for a single argument.
const maxArgPrompt = 96 << 10

// Args returns the command line for an invocation (without the binary).
func (c *CursorCLI) Args(inv Invocation, prompt string) []string {
	// stream-json prints the session as it happens (messages, tool calls;
	// cursor-agent keeps its thinking to itself in print mode), so it can be
	// logged; the last line is the result.
	// --trust: the checkout is a fresh workspace the CLI would ask about;
	// without it read-only runs (no --force) stop at "Workspace Trust
	// Required".
	args := []string{"-p", "--output-format", "stream-json", "--trust"}
	if !inv.ReadOnly {
		// Allow shell commands (build, tests) without interactive approval.
		args = append(args, "--force")
	}
	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}
	if inv.Resume != "" {
		args = append(args, "--resume", inv.Resume)
	}
	return append(args, prompt)
}

// Prompt folds the role instructions and the output format into the prompt.
func (c *CursorCLI) Prompt(inv Invocation) string {
	var b strings.Builder
	if inv.SystemPrompt != "" && inv.Resume == "" {
		fmt.Fprintf(&b, "<instructions>\n%s\n</instructions>\n\n", inv.SystemPrompt)
	}
	if inv.ReadOnly {
		b.WriteString("You are read-only for this task: do not modify any files.\n\n")
	}
	b.WriteString(inv.Prompt)
	if inv.JSONSchema != "" {
		fmt.Fprintf(&b, "\n\nWhen you are done, reply with only a JSON object (no prose, no code fence) that matches this JSON schema:\n%s\n", inv.JSONSchema)
	}
	return b.String()
}

// Run executes the invocation.
func (c *CursorCLI) Run(ctx context.Context, inv Invocation) (*Result, error) {
	prompt := c.Prompt(inv)
	if len(prompt) > maxArgPrompt {
		dir := c.PromptDir
		if dir == "" {
			dir = filepath.Join(filepath.Dir(inv.Dir), ".ai-operator", "prompts")
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		f, err := os.CreateTemp(dir, "prompt-*.md")
		if err != nil {
			return nil, err
		}
		defer os.Remove(f.Name())
		if _, err := f.WriteString(prompt); err != nil {
			f.Close()
			return nil, err
		}
		f.Close()
		prompt = fmt.Sprintf("Your full task is in the file %s. Read it completely and follow it exactly.", f.Name())
	}
	return runCLI(ctx, orDefault(c.Binary, "cursor-agent"), c.Args(inv, prompt), inv.Dir, append(slices.Clip(c.Env), inv.Env...), "", true)
}

// turnReader consumes stream-json lines for one agent turn: held-message
// logging, rate_limit_event usage, and the result line.
type turnReader struct {
	log    logr.Logger
	held   [][]byte
	usage  *aiv1.EngineUsage
	result []byte
}

func (t *turnReader) flush() {
	for _, h := range t.held {
		logEvent(t.log, h)
	}
	t.held = nil
}

// consume handles one non-empty stream-json line. It reports whether the
// line was the turn's result.
func (t *turnReader) consume(line []byte) bool {
	switch ev := parseEvent(line); ev.Type {
	case "assistant":
		t.held = append(t.held, line)
		if callsTool(line) {
			// A claude message that calls a tool is no answer.
			t.flush()
		}
	case "tool_call", "user":
		t.flush()
		if ev.Type == "tool_call" && ev.Subtype == "started" {
			logEvent(t.log, line)
		}
	case "rate_limit_event":
		if u := usageReading(line); u != nil {
			t.usage = u
		}
	case "result":
		if ev.IsError || ev.Subtype != "" && ev.Subtype != "success" {
			t.flush()
		} else if len(t.held) > 0 {
			t.log.Info("agent answers")
			t.held = nil
		}
		t.result = append([]byte(nil), line...)
		return true
	}
	return false
}

// runCLI runs an agent CLI. With stream, its output is a line per event: each
// is logged (see logEvent) and only the last one, the result, is kept.
func runCLI(ctx context.Context, bin string, args []string, dir string, env []string, stdin string, stream bool) (*Result, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stderr = &stderr
	var runErr error
	var usage *aiv1.EngineUsage
	if stream {
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("%s failed: %w", filepath.Base(bin), err)
		}
		log := logr.FromContextOrDiscard(ctx)
		rd := bufio.NewReader(pipe)
		tr := &turnReader{log: log}
		for {
			line, err := rd.ReadBytes('\n')
			if line = bytes.TrimSpace(line); len(line) > 0 {
				tr.consume(line)
			}
			if err != nil {
				break
			}
		}
		// No result: the stream ended early, the messages are no answer.
		tr.flush()
		runErr = cmd.Wait()
		stdout.Reset()
		stdout.Write(tr.result)
		usage = tr.usage
	} else {
		cmd.Stdout = &stdout
		runErr = cmd.Run()
	}
	res, parseErr := ParseResult(stdout.Bytes())
	if parseErr != nil {
		if runErr != nil {
			return nil, fmt.Errorf("%s failed: %w: %s", filepath.Base(bin), runErr, tail(stderr.String(), 2000))
		}
		return nil, parseErr
	}
	res.Usage = usage
	if res.IsError {
		return res, fmt.Errorf("%s reported an error: %s", filepath.Base(bin), tail(res.Text, 2000))
	}
	return res, nil
}

// ParseResult parses the result event of `claude -p` and `cursor-agent -p`
// (the last line of stream-json, or the object of --output-format json);
// both share the session_id / is_error / result fields.
func ParseResult(out []byte) (*Result, error) {
	var raw struct {
		SessionID        string          `json:"session_id"`
		TotalCostUSD     float64         `json:"total_cost_usd"`
		IsError          bool            `json:"is_error"`
		Result           string          `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
		Usage            *struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
		ModelUsage map[string]struct {
			InputTokens              int64   `json:"inputTokens"`
			OutputTokens             int64   `json:"outputTokens"`
			CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
			CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
			CostUSD                  float64 `json:"costUSD"`
		} `json:"modelUsage"`
		NumTurns   *int32 `json:"num_turns"`
		DurationMS *int64 `json:"duration_ms"`
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil, errors.New("agent CLI produced no output")
	}
	// Be tolerant of log lines before the result object.
	if i := bytes.LastIndex(out, []byte("\n{")); i >= 0 && out[0] != '{' {
		out = out[i+1:]
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing agent CLI output: %w", err)
	}
	res := &Result{
		SessionID: raw.SessionID,
		CostUSD:   raw.TotalCostUSD,
		IsError:   raw.IsError,
		Text:      raw.Result,
	}
	if len(raw.StructuredOutput) > 0 && string(raw.StructuredOutput) != "null" {
		res.Structured = raw.StructuredOutput
	}
	if raw.Usage != nil || len(raw.ModelUsage) > 0 || raw.NumTurns != nil || raw.DurationMS != nil {
		u := &CallUsage{}
		if raw.Usage != nil {
			u.InputTokens, u.OutputTokens = raw.Usage.InputTokens, raw.Usage.OutputTokens
			u.CacheReadTokens, u.CacheWriteTokens = raw.Usage.CacheReadInputTokens, raw.Usage.CacheCreationInputTokens
		}
		if raw.NumTurns != nil {
			u.Turns = *raw.NumTurns
		}
		if raw.DurationMS != nil {
			u.DurationMS = *raw.DurationMS
		}
		for name, m := range raw.ModelUsage {
			u.Models = append(u.Models, ModelUsage{
				Model:       name,
				InputTokens: m.InputTokens, OutputTokens: m.OutputTokens,
				CacheReadTokens: m.CacheReadInputTokens, CacheWriteTokens: m.CacheCreationInputTokens,
				CostUSD: m.CostUSD,
			})
		}
		slices.SortFunc(u.Models, func(a, b ModelUsage) int { return strings.Compare(a.Model, b.Model) })
		res.CallUsage = u
	}
	return res, nil
}

// Decode unmarshals the structured output into v. It falls back to the first
// JSON object in the text result, for engines without schema support.
func (r *Result) Decode(v any) error {
	if len(r.Structured) > 0 {
		return json.Unmarshal(r.Structured, v)
	}
	text := strings.TrimSpace(r.Text)
	if i := strings.Index(text, "```json"); i >= 0 {
		text = text[i+len("```json"):]
		// The last fence closes the block: string values (summaries, notes)
		// may contain code blocks of their own.
		if j := strings.LastIndex(text, "```"); j >= 0 {
			text = text[:j]
		}
	}
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return fmt.Errorf("no JSON object in agent result: %s", tail(r.Text, 500))
	}
	return json.Unmarshal([]byte(text[start:end+1]), v)
}

// scrubEnv removes credentials that only the runner needs (GitLab token,
// Kubernetes service discovery) from the environment of the agent CLIs.
// Engine credentials (CLAUDE_CODE_OAUTH_TOKEN, ANTHROPIC_API_KEY,
// CURSOR_API_KEY) stay, the CLIs need them.
func scrubEnv(env []string) []string {
	var out []string
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if k == "GITLAB_TOKEN" || strings.HasPrefix(k, "AI_") || strings.HasPrefix(k, "KUBERNETES_") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
}

// DefaultEnv is the scrubbed process environment.
func DefaultEnv() []string { return scrubEnv(os.Environ()) }

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
