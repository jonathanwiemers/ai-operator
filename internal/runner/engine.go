package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
}

// Engine runs invocations. Implementations wrap one agent CLI.
type Engine interface {
	Run(ctx context.Context, inv Invocation) (*Result, error)
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
	args := []string{"-p", "--output-format", "json", "--permission-mode", "acceptEdits"}
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
	return runCLI(ctx, orDefault(c.Binary, "claude"), c.Args(inv), inv.Dir, c.Env, inv.Prompt)
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
	args := []string{"-p", "--output-format", "json"}
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
	return runCLI(ctx, orDefault(c.Binary, "cursor-agent"), c.Args(inv, prompt), inv.Dir, c.Env, "")
}

func runCLI(ctx context.Context, bin string, args []string, dir string, env []string, stdin string) (*Result, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	res, parseErr := ParseResult(stdout.Bytes())
	if parseErr != nil {
		if runErr != nil {
			return nil, fmt.Errorf("%s failed: %w: %s", filepath.Base(bin), runErr, tail(stderr.String(), 2000))
		}
		return nil, parseErr
	}
	if res.IsError {
		return res, fmt.Errorf("%s reported an error: %s", filepath.Base(bin), tail(res.Text, 2000))
	}
	return res, nil
}

// ParseResult parses the final JSON object printed by `claude -p
// --output-format json` and `cursor-agent -p --output-format json`; both
// share the session_id / is_error / result fields.
func ParseResult(out []byte) (*Result, error) {
	var raw struct {
		SessionID        string          `json:"session_id"`
		TotalCostUSD     float64         `json:"total_cost_usd"`
		IsError          bool            `json:"is_error"`
		Result           string          `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
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
		if j := strings.Index(text, "```"); j >= 0 {
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
