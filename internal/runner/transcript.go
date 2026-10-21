package runner

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/go-logr/logr"
)

// maxLogText bounds the agent's text in one log line.
const maxLogText = 4000

// logEvent logs one event of a claude stream-json session: what the agent
// thinks and says, and the tools it calls with their main argument (the
// command, the file). Tool output is not logged: it holds file contents and
// whatever commands print. It returns the event's type.
func logEvent(log logr.Logger, line []byte) string {
	var ev struct {
		Type    string `json:"type"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &ev) != nil || ev.Type != "assistant" {
		return ev.Type
	}
	var blocks []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Thinking string          `json:"thinking"`
		Name     string          `json:"name"`
		Input    json.RawMessage `json:"input"`
	}
	if json.Unmarshal(ev.Message.Content, &blocks) != nil {
		return ev.Type
	}
	for _, b := range blocks {
		switch b.Type {
		case "thinking":
			if t := strings.TrimSpace(b.Thinking); t != "" {
				log.Info("agent thinks", "text", clip(t, maxLogText))
			}
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				log.Info("agent says", "text", clip(t, maxLogText))
			}
		case "tool_use":
			if b.Name == "StructuredOutput" {
				// The final answer (--json-schema): it is posted, not logged.
				log.Info("agent answers")
				continue
			}
			log.Info("agent uses a tool", "tool", b.Name, "input", toolInput(b.Name, b.Input))
		}
	}
	return ev.Type
}

// toolInput is the part of a tool call worth a log line.
func toolInput(name string, raw json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	for _, key := range []string{"command", "file_path", "notebook_path", "pattern", "url", "query", "description", "path"} {
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
			return clip(v, 500)
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
		return clip(strings.Join(items, "; "), 1000)
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

// clip cuts s to at most n bytes, at a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
