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
	"github.com/jonathanwiemers/ai-operator/internal/readgate"
)

// Invocation is one headless call of an agent CLI. It is engine neutral; each
// engine maps the fields it supports onto its own flags.
type Invocation struct {
	// Dir is the working directory (the repository checkout). It is set by
	// the agent side, never by the runner.
	Dir string
	// Companion is the project slug of the companion checkout the call may
	// work on too (implement runs with a companion). The runner sends only
	// the slug; the agent side validates it and turns it into AddDirs.
	Companion string `json:"companion,omitempty"`
	// AddDirs are more directories the engine may work in, claude's
	// --add-dir (claude only). Set by the agent side, like Dir; it never
	// crosses the socket.
	AddDirs []string `json:"-"`
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
	// Agents is the role's subagents as claude's --agents JSON (claude
	// only, see withSubagents).
	Agents string
	// Settings is claude's --settings JSON, the hooks of the read gate and
	// the step-size gate (claude only, see withReadGate and withStepGate).
	Settings string
	// StepGateMaxLines turns on the step-size gate (agents.implement or the
	// profile's maxStepLines) for a stepwise implement call; 0 is off. The
	// runner turns it into Settings; it never crosses the socket.
	StepGateMaxLines int `json:"-"`
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
	// CountLines asks the agent side to measure the uncommitted diff of the
	// home tree when the engine returns (CallUsage.LinesAdded/Deleted). The
	// runner sets it for implementer calls.
	CountLines bool `json:"countLines,omitempty"`
	// Env overrides variables of the engine's environment. It is set by the
	// agent side and never crosses the socket.
	Env []string `json:"-"`
	// Kind is what the call is for (aiv1.CallKind*), for status.run.calls;
	// FallbackFrom is the limited engine a fallback call stands in for.
	// Neither crosses the socket.
	Kind         string      `json:"-"`
	FallbackFrom aiv1.Engine `json:"-"`
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
	// InputTokens … CacheWriteTokens are the call's usage as reported
	// (claude: the main thread only); the row records tokens().
	InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens int64
	Turns                                                        int32
	DurationMS                                                   int64
	// Models is the per-model split as reported (subagent models included),
	// sorted by name.
	Models []ModelUsage
	// SubagentCalls is how many subagents claude started (subagent_stats,
	// else the distinct parent_tool_use_ids of the stream).
	SubagentCalls int32
	// sinceLast is claude's model_usage_since_last_result, sorted by name:
	// non-nil (maybe empty) when the result carried it. ClaudeSession reads
	// it (perTurn); one-shot calls record Models.
	sinceLast []ModelUsage
	// ContextSeries is the context size (input + cache tokens) of each
	// main-thread assistant turn, thinned to maxContextPoints.
	ContextSeries []int64
	// SubagentTurns are indexes into ContextSeries: the main turns after
	// which a subagent ran, ascending, without duplicates.
	SubagentTurns []int32
	// ReadGateBlocks is how many tool calls the read gate denied: error
	// tool_results whose text contains readgate.Marker, in every thread.
	ReadGateBlocks int32
	// LinesAdded and LinesDeleted are the uncommitted diff an implementer
	// call left in the home tree (Workspace.UncommittedLines), measured by
	// the agent container when the engine returned.
	LinesAdded, LinesDeleted int64
	// StepGateNudges is how often the step-size gate nudged the call's
	// session during the call, read by the agent container from the gate's
	// state file (with the lines, see Invocation.CountLines).
	StepGateNudges int32
}

// ModelUsage is one model's share of a call.
type ModelUsage struct {
	Model                                                        string
	InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens int64
	CostUSD                                                      float64
}

// tokens is what the call's row records: input, output, cache read and
// cache write on the basis of Models, so a call's tokens, its by-model split
// and every sum over them agree (decision 4 of
// docs/specs/honest-reporting.spec.md). claude's usage holds the main
// thread only, its modelUsage subagents and compaction too. Without a split
// (cursor) it is the call's own usage.
func (u *CallUsage) tokens() (in, out, cacheRead, cacheWrite int64) {
	if len(u.Models) == 0 {
		return u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens
	}
	for _, m := range u.Models {
		in, out = in+m.InputTokens, out+m.OutputTokens
		cacheRead, cacheWrite = cacheRead+m.CacheReadTokens, cacheWrite+m.CacheWriteTokens
	}
	return in, out, cacheRead, cacheWrite
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
	if inv.Agents != "" {
		args = append(args, "--agents", inv.Agents)
	}
	if inv.Settings != "" {
		args = append(args, "--settings", inv.Settings)
	}
	for _, dir := range inv.AddDirs {
		args = append(args, "--add-dir", dir)
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
// subject to command line length limits. The result holds this call only
// (sinceLastResult): after --resume, modelUsage and total_cost_usd also
// carry the session's earlier calls.
func (c *ClaudeCLI) Run(ctx context.Context, inv Invocation) (*Result, error) {
	res, err := runCLI(ctx, orDefault(c.Binary, "claude"), c.Args(inv), inv.Dir, append(slices.Clip(c.Env), inv.Env...), inv.Prompt, true)
	if res != nil {
		sinceLastResult(res)
	}
	return res, err
}

// sinceLastResult makes res's models and cost what its process counted
// since its previous result (model_usage_since_last_result, cost the sum of
// its costUSD) and reports whether the result carried that field. Without
// it res stays as reported: modelUsage and total_cost_usd, which after
// --resume include the session's earlier calls (R5 of
// docs/specs/honest-reporting.spec.md). The row's tokens are the sum of
// these models (CallUsage.tokens), so both hold the same call.
func sinceLastResult(res *Result) bool {
	u := res.CallUsage
	if u == nil || u.sinceLast == nil {
		return false
	}
	u.Models, res.CostUSD = u.sinceLast, 0
	for _, m := range u.Models {
		res.CostUSD += m.CostUSD
	}
	return true
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
// logging, rate_limit_event usage, claude's per-turn context sizes and
// subagents, and the result line.
type turnReader struct {
	log logr.Logger
	// status, if set, gets the agent's status text as it changes (see
	// streamStatus).
	status func(string)
	held   [][]byte
	usage  *aiv1.EngineUsage
	result []byte
	// messages are the main-thread message ids seen; claude repeats a
	// message once per content block.
	messages map[string]bool
	series   []int64
	// subagents are the parent_tool_use_ids seen, one per subagent run.
	subagents map[string]bool
	subTurns  []int32
	// counted are the subagent message ids whose tokens were logged.
	counted map[string]bool
	// gateBlocks counts the read gate's denials (see readGateBlocks).
	gateBlocks int32
}

func (t *turnReader) flush() {
	if t.counted == nil {
		t.counted = map[string]bool{}
	}
	for _, h := range t.held {
		logEvent(t.log, h, t.counted)
	}
	t.held = nil
}

// consume handles one non-empty stream-json line. It reports whether the
// line was the turn's result.
func (t *turnReader) consume(line []byte) bool {
	switch ev := parseEvent(line); ev.Type {
	case "assistant":
		t.track(line)
		if t.status != nil {
			if s := streamStatus(line); s != "" {
				t.status(s)
			}
		}
		t.held = append(t.held, line)
		if callsTool(line) {
			// A claude message that calls a tool is no answer.
			t.flush()
		}
	case "tool_call", "user":
		if ev.Type == "user" {
			t.track(line)
			t.gateBlocks += readGateBlocks(line)
		}
		t.flush()
		if ev.Type == "tool_call" && ev.Subtype == "started" {
			logEvent(t.log, line, nil)
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

// track reads a claude assistant or user line: a main-thread assistant
// message adds its context size to the series, a line with a new
// parent_tool_use_id marks a subagent run after the last main turn. Lines
// without these fields (cursor) add nothing.
func (t *turnReader) track(line []byte) {
	var ev struct {
		ParentToolUseID string `json:"parent_tool_use_id"`
		Message         struct {
			ID    string `json:"id"`
			Usage *struct {
				InputTokens              int64 `json:"input_tokens"`
				CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
				CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &ev) != nil {
		return
	}
	if id := ev.ParentToolUseID; id != "" {
		if t.subagents[id] {
			return
		}
		if t.subagents == nil {
			t.subagents = map[string]bool{}
		}
		t.subagents[id] = true
		if turn := int32(len(t.series) - 1); turn >= 0 && !slices.Contains(t.subTurns, turn) {
			t.subTurns = append(t.subTurns, turn)
		}
		return
	}
	u, id := ev.Message.Usage, ev.Message.ID
	if u == nil || id == "" || t.messages[id] {
		return
	}
	if t.messages == nil {
		t.messages = map[string]bool{}
	}
	t.messages[id] = true
	t.series = append(t.series, u.InputTokens+u.CacheReadInputTokens+u.CacheCreationInputTokens)
}

// readGateBlocks counts the tool_result blocks of a claude user line that
// are errors whose text contains readgate.Marker. Claude wraps the hook's
// reason ("PreToolUse:Read hook error: …"), so the marker may be anywhere;
// a block's content is a string or a list of text blocks.
func readGateBlocks(line []byte) int32 {
	if !bytes.Contains(line, []byte(readgate.Marker)) {
		return 0
	}
	var ev struct {
		Message struct {
			Content []struct {
				Type    string          `json:"type"`
				IsError bool            `json:"is_error"`
				Content json.RawMessage `json:"content"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &ev) != nil {
		return 0
	}
	var n int32
	for _, b := range ev.Message.Content {
		if b.Type == "tool_result" && b.IsError && strings.Contains(toolResultText(b.Content), readgate.Marker) {
			n++
		}
	}
	return n
}

// toolResultText is the text of a tool_result's content: the string, or
// its text blocks joined by newlines.
func toolResultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	texts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == "text" {
			texts = append(texts, b.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// maxContextPoints caps CallUsage.ContextSeries.
const maxContextPoints = 500

// addCallUsage merges what the stream showed into the result's CallUsage.
func (t *turnReader) addCallUsage(res *Result) {
	if len(t.series) == 0 && len(t.subagents) == 0 && t.gateBlocks == 0 {
		return
	}
	if res.CallUsage == nil {
		res.CallUsage = &CallUsage{}
	}
	u := res.CallUsage
	u.ContextSeries, u.SubagentTurns = thinSeries(t.series, t.subTurns, maxContextPoints)
	if u.SubagentCalls == 0 {
		u.SubagentCalls = int32(len(t.subagents))
	}
	u.ReadGateBlocks = t.gateBlocks
}

// thinSeries keeps at most max evenly spaced points of series, always the
// first and the last, and maps turns (indexes into series) to the nearest
// kept index, without duplicates.
func thinSeries(series []int64, turns []int32, max int) ([]int64, []int32) {
	n := len(series)
	if n <= max || max < 2 {
		return series, turns
	}
	// Kept point k is series[round(k*(n-1)/(max-1))].
	kept := make([]int64, max)
	for k := range kept {
		kept[k] = series[(2*k*(n-1)+max-1)/(2*(max-1))]
	}
	var mapped []int32
	for _, i := range turns {
		k := int32((2*int(i)*(max-1) + n - 1) / (2 * (n - 1)))
		if !slices.Contains(mapped, k) {
			mapped = append(mapped, k)
		}
	}
	return kept, mapped
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
	tr := &turnReader{log: logr.FromContextOrDiscard(ctx), status: statusSink(ctx)}
	if stream {
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("%s failed: %w", filepath.Base(bin), err)
		}
		rd := bufio.NewReader(pipe)
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
	res.Usage = tr.usage
	tr.addCallUsage(res)
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
		ModelUsage map[string]modelFigures `json:"modelUsage"`
		// SinceLast (stream-json, @internal in the CLI) is what the process
		// counted since its previous result.
		SinceLast  map[string]modelFigures `json:"model_usage_since_last_result"`
		NumTurns   *int32                  `json:"num_turns"`
		DurationMS *int64                  `json:"duration_ms"`
		// SubagentStats has more (by_type, completed, failed, …); spawned
		// counts every subagent started, nested ones included.
		SubagentStats *struct {
			Spawned int32 `json:"spawned"`
		} `json:"subagent_stats"`
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
		if raw.SubagentStats != nil {
			u.SubagentCalls = raw.SubagentStats.Spawned
		}
		u.Models = modelList(raw.ModelUsage)
		if raw.SinceLast != nil {
			u.sinceLast = append([]ModelUsage{}, modelList(raw.SinceLast)...)
		}
		res.CallUsage = u
	}
	return res, nil
}

// modelFigures is one model's entry in claude's modelUsage.
type modelFigures struct {
	InputTokens              int64   `json:"inputTokens"`
	OutputTokens             int64   `json:"outputTokens"`
	CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
	CostUSD                  float64 `json:"costUSD"`
}

// modelList is ms sorted by model name, nil if ms is empty.
func modelList(ms map[string]modelFigures) []ModelUsage {
	var out []ModelUsage
	for name, m := range ms {
		out = append(out, ModelUsage{
			Model:       name,
			InputTokens: m.InputTokens, OutputTokens: m.OutputTokens,
			CacheReadTokens: m.CacheReadInputTokens, CacheWriteTokens: m.CacheCreationInputTokens,
			CostUSD: m.CostUSD,
		})
	}
	slices.SortFunc(out, func(a, b ModelUsage) int { return strings.Compare(a.Model, b.Model) })
	return out
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
