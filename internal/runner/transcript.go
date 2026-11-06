package runner

import (
	"encoding/json"
	"fmt"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// maxLogText bounds the agent's text in one log line.
const maxLogText = 4000

// maxLogPrompt bounds a logged system prompt or prompt (agents.logPrompts).
const maxLogPrompt = 64 << 10

// logEvent logs one event of a stream-json session of claude or
// cursor-agent: what the agent thinks (claude only) and says, and the tools
// it calls with their main argument (the command, the file). Tool output is
// not logged: it holds file contents and whatever commands print. It returns
// the event's type.
//
// A subagent's lines (claude: parent_tool_use_id set) carry parent, the id
// of the Task/Agent tool_use that started it, and model; the first logged
// line of each of its messages carries tokens (input + output + cache), so
// the UI can fold them under the tool line, which carries toolUseID. counted
// holds the message ids whose tokens were logged (claude repeats a message
// once per content block); nil logs no tokens.
func logEvent(log logr.Logger, line []byte, counted map[string]bool) string {
	var ev struct {
		Type            string `json:"type"`
		Subtype         string `json:"subtype"`
		ParentToolUseID string `json:"parent_tool_use_id"`
		Message         struct {
			ID      string          `json:"id"`
			Model   string          `json:"model"`
			Content json.RawMessage `json:"content"`
			Usage   *struct {
				InputTokens              int64 `json:"input_tokens"`
				OutputTokens             int64 `json:"output_tokens"`
				CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
				CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
		// cursor-agent: one key per tool kind, e.g. readToolCall.
		ToolCall map[string]json.RawMessage `json:"tool_call"`
	}
	if json.Unmarshal(line, &ev) != nil {
		return ""
	}
	if ev.Type == "tool_call" && ev.Subtype == "started" {
		for kind, raw := range ev.ToolCall {
			// Next to the tool, cursor-agent sends fields about the call
			// (toolCallId, startedAtMs, hookAdditionalContexts): no tools.
			if kind != "function" && !strings.HasSuffix(kind, "ToolCall") {
				continue
			}
			name, input := cursorTool(kind, raw)
			log.Info("agent uses a tool", "tool", name, "input", input)
		}
		return ev.Type
	}
	if ev.Type != "assistant" {
		return ev.Type
	}
	var blocks []struct {
		Type     string          `json:"type"`
		ID       string          `json:"id"`
		Text     string          `json:"text"`
		Thinking string          `json:"thinking"`
		Name     string          `json:"name"`
		Input    json.RawMessage `json:"input"`
	}
	if json.Unmarshal(ev.Message.Content, &blocks) != nil {
		return ev.Type
	}
	var sub []any
	if p := ev.ParentToolUseID; p != "" {
		sub = []any{"parent", p}
		if ev.Message.Model != "" {
			sub = append(sub, "model", ev.Message.Model)
		}
	}
	// subKeys are the subagent keys of the next logged line; tokens go on
	// the first line of a message only.
	subKeys := func() []any {
		u, id := ev.Message.Usage, ev.Message.ID
		if sub == nil || u == nil || counted == nil || id != "" && counted[id] {
			return sub
		}
		if id != "" {
			counted[id] = true
		}
		n := u.InputTokens + u.OutputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
		return append(slices.Clip(sub), "tokens", n)
	}
	for _, b := range blocks {
		switch b.Type {
		case "thinking":
			if t := strings.TrimSpace(b.Thinking); t != "" {
				log.Info("agent thinks", append([]any{"text", textutil.Truncate(t, maxLogText)}, subKeys()...)...)
			}
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				log.Info("agent says", append([]any{"text", textutil.Truncate(t, maxLogText)}, subKeys()...)...)
			}
		case "tool_use":
			if b.Name == "StructuredOutput" {
				// The final answer (--json-schema): it is posted, not logged.
				log.Info("agent answers", subKeys()...)
				continue
			}
			kv := []any{"tool", b.Name, "input", toolInput(b.Name, b.Input)}
			if (b.Name == "Task" || b.Name == "Agent") && b.ID != "" {
				// It starts a subagent, whose lines name this id as parent.
				kv = append(kv, "toolUseID", b.ID)
				if a := subagentName(b.Input); a != "" {
					kv = append(kv, "subagent", a)
				}
			}
			log.Info("agent uses a tool", append(kv, subKeys()...)...)
		}
	}
	return ev.Type
}

// subagentName is the subagent a Task/Agent tool_use starts: its type, else
// its name.
func subagentName(raw json.RawMessage) string {
	var in struct {
		Type string `json:"subagent_type"`
		Name string `json:"name"`
	}
	_ = json.Unmarshal(raw, &in)
	if in.Type == "" {
		in.Type = in.Name
	}
	return textutil.Truncate(in.Type, 100)
}

// event is what runCLI needs to know about a stream-json line.
type event struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
}

// callsTool reports whether a claude assistant message calls a tool other
// than StructuredOutput, which carries the answer.
func callsTool(line []byte) bool {
	var ev struct {
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"content"`
		} `json:"message"`
	}
	_ = json.Unmarshal(line, &ev)
	for _, b := range ev.Message.Content {
		if b.Type == "tool_use" && b.Name != "StructuredOutput" {
			return true
		}
	}
	return false
}

// usageReading reads claude's rate_limit_event: the status and, per usage
// window, how much of it is used and when it resets. Other or malformed lines
// give nil.
func usageReading(line []byte) *aiv1.EngineUsage {
	var ev struct {
		Info struct {
			Status  string `json:"status"`
			Windows map[string]struct {
				Utilization float64 `json:"utilization"`
				ResetsAt    int64   `json:"resetsAt"`
			} `json:"unifiedWindows"`
		} `json:"rate_limit_info"`
	}
	if json.Unmarshal(line, &ev) != nil || ev.Info.Status == "" {
		return nil
	}
	u := &aiv1.EngineUsage{Status: ev.Info.Status}
	for name, w := range ev.Info.Windows {
		win := aiv1.UsageWindow{Name: name, Percent: int32(math.Round(w.Utilization * 100))}
		if w.ResetsAt > 0 {
			win.ResetsAt = &metav1.Time{Time: time.Unix(w.ResetsAt, 0)}
		}
		u.Windows = append(u.Windows, win)
	}
	sort.Slice(u.Windows, func(i, j int) bool { return u.Windows[i].Name < u.Windows[j].Name })
	return u
}

// parseEvent reads the type of a stream-json line; other output has none.
func parseEvent(line []byte) event {
	var ev event
	_ = json.Unmarshal(line, &ev)
	return ev
}

// cursorTool names a cursor-agent tool call and picks its main argument:
// {"readToolCall": {"args": {"path": …}}} is the tool "read", other tools
// come as {"function": {"name": …, "arguments": "<json>"}}.
func cursorTool(kind string, raw json.RawMessage) (string, string) {
	if kind == "function" {
		var f struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		_ = json.Unmarshal(raw, &f)
		return f.Name, toolInput(f.Name, json.RawMessage(f.Arguments))
	}
	var c struct {
		Args json.RawMessage `json:"args"`
	}
	_ = json.Unmarshal(raw, &c)
	name := strings.TrimSuffix(kind, "ToolCall")
	return name, toolInput(name, c.Args)
}

// toolInput is the part of a tool call worth a log line.
func toolInput(name string, raw json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	for _, key := range []string{"command", "file_path", "notebook_path", "pattern", "globPattern", "url", "query", "description", "path"} {
		if v, ok := in[key].(string); ok && v != "" {
			if key == "command" {
				// A heredoc or a multi-line script carries file contents:
				// the first line says what runs.
				first, rest, _ := strings.Cut(v, "\n")
				if i := strings.Index(first, "<<"); i >= 0 {
					first, rest = first[:i], "…"
				}
				if rest != "" {
					v = strings.TrimSpace(first) + " …"
				}
			}
			if key == "pattern" {
				if p, ok := in["path"].(string); ok && p != "" {
					v += " in " + p
				}
			}
			return textutil.Truncate(v, 500)
		}
	}
	if todos, ok := in["todos"].([]any); ok {
		var items []string
		for _, t := range todos {
			if m, ok := t.(map[string]any); ok {
				s, _ := m["status"].(string)
				c, _ := m["content"].(string)
				items = append(items, "["+s+"] "+c)
			}
		}
		return textutil.Truncate(strings.Join(items, "; "), 1000)
	}
	// Unknown tools: only which arguments they got, their values may hold
	// anything (contents, comment bodies).
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// maxStatusText caps the status text a checkpoint body takes.
const maxStatusText = 1000

// streamStatus is the agent's status in a main-thread assistant line: its
// todo list (TodoWrite) or its text, whichever the line ends with; "" for
// other lines. Subagents' lines are no status of the call.
func streamStatus(line []byte) string {
	var ev struct {
		Type            string `json:"type"`
		ParentToolUseID string `json:"parent_tool_use_id"`
		Message         struct {
			Content []struct {
				Type  string `json:"type"`
				Text  string `json:"text"`
				Name  string `json:"name"`
				Input struct {
					Todos []struct {
						Content string `json:"content"`
						Status  string `json:"status"`
					} `json:"todos"`
				} `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &ev) != nil || ev.Type != "assistant" || ev.ParentToolUseID != "" {
		return ""
	}
	status := ""
	for _, b := range ev.Message.Content {
		switch {
		case b.Type == "text" && strings.TrimSpace(b.Text) != "":
			status = strings.TrimSpace(b.Text)
		case b.Type == "tool_use" && b.Name == "TodoWrite" && len(b.Input.Todos) > 0:
			lines := []string{"Todo list:"}
			for _, todo := range b.Input.Todos {
				mark := " "
				switch todo.Status {
				case "completed":
					mark = "x"
				case "in_progress":
					mark = ">"
				}
				lines = append(lines, fmt.Sprintf("- [%s] %s", mark, strings.Join(strings.Fields(todo.Content), " ")))
			}
			status = strings.Join(lines, "\n")
		}
	}
	return textutil.Truncate(status, maxStatusText)
}
