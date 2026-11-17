package runner

import (
	"maps"
	"os"
	"strings"
	"testing"

	"github.com/go-logr/logr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// replayStream feeds a fixture's lines to a turnReader and returns the
// result with the stream's usage merged in, as runCLI does.
func replayStream(t *testing.T, name string) *Result {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	tr := &turnReader{log: logr.Discard()}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		tr.consume([]byte(line))
	}
	res, err := ParseResult(tr.result)
	if err != nil {
		t.Fatal(err)
	}
	tr.addCallUsage(res)
	if res.CallUsage == nil {
		t.Fatal("CallUsage is nil")
	}
	return res
}

func TestToolResultsByTool(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		want    map[string]aiv1.ToolResultUsage
		base    int64
	}{
		// Read: "0123456789" + "abcde\nfgh"; Bash: "ok\n" + an error
		// "Exit code 1"; Grep: "a.go:1\nb.go:2".
		{"claude-stream-tools.jsonl", map[string]aiv1.ToolResultUsage{
			"Read": {Calls: 2, Bytes: 19}, "Bash": {Calls: 2, Bytes: 14}, "Grep": {Calls: 1, Bytes: 13}}, 15003},
		// The subagent's Read and Grep count under the tool that started
		// it, with its own result; the main thread's Read stays Read.
		{"claude-stream-tools-subagent.jsonl", map[string]aiv1.ToolResultUsage{
			"Agent": {Calls: 3, Bytes: 11 + 6 + 17}, "Read": {Calls: 1, Bytes: 9}}, 15003},
		// MCP tools keep their full name; a result without its tool_use
		// counts under unknown.
		{"claude-stream-tools-mcp.jsonl", map[string]aiv1.ToolResultUsage{
			"mcp__gitlab__get_issue": {Calls: 1, Bytes: 9}, "unknown": {Calls: 1, Bytes: 6}}, 15003},
		// The existing subagent fixture: an Explore subagent's Grep.
		{"claude-stream-subagent.jsonl", map[string]aiv1.ToolResultUsage{
			"Read": {Calls: 1, Bytes: 18}, "Agent": {Calls: 2, Bytes: 68 + 44}, "Edit": {Calls: 1, Bytes: 26}}, 20003},
	} {
		u := replayStream(t, tc.fixture).CallUsage
		if !maps.Equal(u.ToolResults, tc.want) {
			t.Errorf("%s: tool results %v, want %v", tc.fixture, u.ToolResults, tc.want)
		}
		if u.BaseContextTokens != tc.base || len(u.ContextSeries) == 0 || u.ContextSeries[0] != tc.base {
			t.Errorf("%s: base context %d (series %v), want %d", tc.fixture, u.BaseContextTokens, u.ContextSeries, tc.base)
		}
	}
}

// TestToolResultsAlone: a call whose stream shows only tool results (no
// usage per message) still gets them.
func TestToolResultsAlone(t *testing.T) {
	tr := &turnReader{log: logr.Discard()}
	tr.consume([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash"}]}}`))
	tr.consume([]byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"hello"}]}}`))
	res := &Result{}
	tr.addCallUsage(res)
	if res.CallUsage == nil || res.CallUsage.ToolResults["Bash"] != (aiv1.ToolResultUsage{Calls: 1, Bytes: 5}) || res.CallUsage.BaseContextTokens != 0 {
		t.Fatalf("CallUsage %+v", res.CallUsage)
	}
}
