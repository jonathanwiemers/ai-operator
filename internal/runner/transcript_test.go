package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
)

// fakeClaude writes a script that prints a stream-json session.
func fakeClaude(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "claude")
	script := `#!/bin/sh
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo '{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"The endpoint needs a test first."},{"type":"text","text":"Adding the test."}]}}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./...","description":"run tests"}}]}}'
echo '{"type":"user","message":{"content":[{"type":"tool_result","content":"SECRET-OUTPUT"}]}}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/api.go","old_string":"a","new_string":"b"}}]}}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"StructuredOutput","input":{"status":"done","summary":"SECRET-SUMMARY"}}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"s1","total_cost_usd":0.5,"structured_output":{"status":"done"}}'
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestClaudeSessionIsLogged(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	log := funcr.New(func(prefix, args string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, args)
	}, funcr.Options{})
	ctx := logr.NewContext(context.Background(), log)
	res, err := (&ClaudeCLI{Binary: fakeClaude(t), Env: os.Environ()}).Run(ctx, Invocation{Dir: t.TempDir(), Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "s1" || res.CostUSD != 0.5 || string(res.Structured) != `{"status":"done"}` {
		t.Fatalf("the result line must still be parsed: %+v", res)
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{
		`"msg"="agent thinks" "text"="The endpoint needs a test first."`,
		`"msg"="agent says" "text"="Adding the test."`,
		`"tool"="Bash" "input"="go test ./..."`,
		`"tool"="Edit" "input"="/w/api.go"`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %s in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "SECRET-OUTPUT") || strings.Contains(all, "new_string") || strings.Contains(all, "SECRET-SUMMARY") {
		t.Errorf("tool output and edit contents must not be logged:\n%s", all)
	}
}

func TestClipKeepsRunes(t *testing.T) {
	if got := clip("ääää", 3); got != "ä…" {
		t.Fatalf("got %q", got)
	}
}

func TestToolInput(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"Grep", `{"pattern":"TODO","path":"internal"}`, "TODO in internal"},
		{"TodoWrite", `{"todos":[{"content":"write test","status":"completed"},{"content":"endpoint","status":"in_progress"}]}`, "[completed] write test; [in_progress] endpoint"},
		{"Bash", `{"command":"cat > a.go <<'EOF'\npackage secret\nEOF"}`, "cat > a.go …"},
		{"Bash", `{"command":"go test ./...\ngo vet ./..."}`, "go test ./... …"},
		{"mcp__x__post", `{"body":"secret comment","channel":"c"}`, "body, channel"},
	} {
		if got := toolInput(tc.name, []byte(tc.in)); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestClaudeDyingMidSessionFails(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\ncat >/dev/null\necho '{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"working\"}]}}'\necho 'out of tokens' >&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := (&ClaudeCLI{Binary: bin, Env: os.Environ()}).Run(context.Background(), Invocation{Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "out of tokens") {
		t.Fatalf("a session without a result is a failure with stderr: %v", err)
	}
}
