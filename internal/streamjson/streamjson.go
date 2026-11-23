// Package streamjson reads single lines of the stream-json output of claude
// and cursor-agent: a line's type and result flags, whether a claude
// assistant message calls a tool, and claude's rate-limit readings.
package streamjson

import (
	"encoding/json"
	"math"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// Event is what runCLI needs to know about a stream-json line.
type Event struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
}

// CallsTool reports whether a claude assistant message calls a tool other
// than StructuredOutput, which carries the answer.
func CallsTool(line []byte) bool {
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

// UsageReading reads claude's rate_limit_event: the status and, per usage
// window, how much of it is used and when it resets. Other or malformed lines
// give nil.
func UsageReading(line []byte) *aiv1.EngineUsage {
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

// ParseEvent reads the type of a stream-json line; other output has none.
func ParseEvent(line []byte) Event {
	var ev Event
	_ = json.Unmarshal(line, &ev)
	return ev
}
