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

// fakeCursor writes a script that prints a cursor-agent stream-json session
// (events as Cursor documents them).
func fakeCursor(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cursor-agent")
	script := `#!/bin/sh
echo "$@" > "$(dirname "$0")/args"
echo '{"type":"system","subtype":"init","apiKeySource":"env","cwd":"/w","session_id":"c1","model":"Auto","permissionMode":"default"}'
echo '{"type":"user","message":{"role":"user","content":[{"type":"text","text":"SECRET-PROMPT"}]},"session_id":"c1"}'
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Reading the handler first."}]},"session_id":"c1"}'
echo '{"type":"tool_call","subtype":"started","call_id":"1","tool_call":{"readToolCall":{"args":{"path":"api/handler.go"}},"toolCallId":"t1","startedAtMs":1761231000000,"hookAdditionalContexts":[]},"session_id":"c1"}'
echo '{"type":"tool_call","subtype":"started","call_id":"4","tool_call":{"globToolCall":{"args":{"globPattern":"**/*.go","targetDirectory":"/w"}},"toolCallId":"t4"},"session_id":"c1"}'
echo '{"type":"tool_call","subtype":"completed","call_id":"1","tool_call":{"readToolCall":{"args":{"path":"api/handler.go"},"result":{"success":{"content":"SECRET-FILE"}}}},"session_id":"c1"}'
echo '{"type":"tool_call","subtype":"started","call_id":"2","tool_call":{"writeToolCall":{"args":{"path":"api/handler_test.go","fileText":"SECRET-WRITTEN","toolCallId":"2"}}},"session_id":"c1"}'
echo '{"type":"tool_call","subtype":"started","call_id":"3","tool_call":{"function":{"name":"run_terminal_cmd","arguments":"{\"command\":\"go test ./api/...\"}"}},"session_id":"c1"}'
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"{\"status\":\"done\",\"summary\":\"SECRET-SUMMARY\"}"}]},"session_id":"c1"}'
echo '{"type":"result","subtype":"success","duration_ms":1200,"is_error":false,"result":"{\"status\":\"done\",\"summary\":\"SECRET-SUMMARY\"}","session_id":"c1"}'
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestCursorSessionIsLogged(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	log := funcr.New(func(prefix, args string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, args)
	}, funcr.Options{})
	ctx := logr.NewContext(context.Background(), log)
	bin := fakeCursor(t)
	res, err := (&CursorCLI{Binary: bin, Env: os.Environ()}).Run(ctx, Invocation{Dir: t.TempDir(), Prompt: "go", JSONSchema: `{"type":"object"}`})
	if err != nil {
		t.Fatal(err)
	}
	var answer struct{ Status string }
	if res.SessionID != "c1" || res.Decode(&answer) != nil || answer.Status != "done" {
		t.Fatalf("the result line must still be parsed: %+v", res)
	}
	if args, _ := os.ReadFile(filepath.Join(filepath.Dir(bin), "args")); !strings.Contains(string(args), "--output-format stream-json") {
		t.Fatalf("cursor-agent must stream: %s", args)
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{
		`"msg"="agent says" "text"="Reading the handler first."`,
		`"tool"="read" "input"="api/handler.go"`,
		`"tool"="write" "input"="api/handler_test.go"`,
		`"tool"="run_terminal_cmd" "input"="go test ./api/..."`,
		`"tool"="glob" "input"="**/*.go"`,
		`"msg"="agent answers"`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %s in:\n%s", want, all)
		}
	}
	for _, secret := range []string{"SECRET-PROMPT", "SECRET-FILE", "SECRET-WRITTEN", "SECRET-SUMMARY"} {
		if strings.Contains(all, secret) {
			t.Errorf("%s must not be logged:\n%s", secret, all)
		}
	}
	for _, field := range []string{"toolCallId", "startedAtMs", "hookAdditionalContexts"} {
		if strings.Contains(all, `"tool"="`+field+`"`) {
			t.Errorf("%s is a field of the call, not a tool:\n%s", field, all)
		}
	}
	if n := strings.Count(all, `"tool"="read"`); n != 1 {
		t.Errorf("a tool call is logged when it starts, not again when it completes: %d", n)
	}
}

func TestCursorDyingMidSessionFails(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "cursor-agent")
	script := "#!/bin/sh\necho '{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"working\"}]}}'\necho \"You've hit your usage limit\" >&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := (&CursorCLI{Binary: bin, Env: os.Environ()}).Run(context.Background(), Invocation{Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "usage limit") {
		t.Fatalf("a session without a result is a failure with stderr: %v", err)
	}
}

// streamCursor runs a fake cursor-agent that prints the given lines and
// returns the log.
func streamCursor(t *testing.T, events ...string) (string, error) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cursor-agent")
	var b strings.Builder
	b.WriteString("#!/bin/sh\ncat <<'EOF'\n")
	for _, e := range events {
		b.WriteString(e + "\n")
	}
	b.WriteString("EOF\n")
	if err := os.WriteFile(bin, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var lines []string
	log := funcr.New(func(_, args string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, args)
	}, funcr.Options{})
	_, err := (&CursorCLI{Binary: bin, Env: os.Environ()}).Run(logr.NewContext(context.Background(), log), Invocation{Dir: t.TempDir()})
	return strings.Join(lines, "\n"), err
}

func TestCursorAnswerSplitOverMessagesIsHeld(t *testing.T) {
	all, err := streamCursor(t,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"working on it"}]}}`,
		`{"type":"tool_call","subtype":"started","tool_call":{"readToolCall":{"args":{"path":"a.go"}}}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"{\"summary\":\"SECRET-1"}]}}`,
		`not json: update available`,
		`{"type":"thinking","text":"x"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"SECRET-2\"}"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"{}","session_id":"c1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all, "working on it") || strings.Contains(all, "SECRET") || strings.Count(all, "agent answers") != 1 {
		t.Fatalf("every message after the last tool call is the answer, also across stray lines:\n%s", all)
	}
}

func TestCursorFailedResultKeepsTheLastMessage(t *testing.T) {
	all, err := streamCursor(t,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"cannot reach the registry"}]}}`,
		`{"type":"result","subtype":"error","is_error":true,"result":"failed","session_id":"c1"}`)
	if err == nil {
		t.Fatal("an error result is a failure")
	}
	if !strings.Contains(all, "cannot reach the registry") || strings.Contains(all, "agent answers") {
		t.Fatalf("a failed run logs its last message:\n%s", all)
	}
}
