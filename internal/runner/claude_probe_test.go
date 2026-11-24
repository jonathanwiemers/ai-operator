//go:build claudeprobe

package runner

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/streamjson"
)

// TestClaudeProbe checks, against the real claude CLI, what stream input
// (Invocation.StreamInput, LocalAgent.Message) relies on: a user message
// written during a slow tool call (a) comes back as a replayed user line and
// (b) is acted on before the call's single result. It costs a model call, so
// it is opt-in and never runs in CI:
//
//	go test -tags claudeprobe -run TestClaudeProbe -v ./internal/runner/
//
// CLAUDE_BIN picks the binary (default claude, as Dockerfile.agent installs
// it). The raw lines are logged.
func TestClaudeProbe(t *testing.T) {
	if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") == "" && os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("needs a claude login: CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	bin := cmp.Or(os.Getenv("CLAUDE_BIN"), "claude")
	if v, err := exec.Command(bin, "--version").Output(); err == nil {
		t.Logf("claude version: %s", bytes.TrimSpace(v))
	}
	cmd := exec.CommandContext(ctx, bin, "-p", "--input-format", "stream-json", "--output-format", "stream-json",
		"--verbose", "--replay-user-messages", "--allowedTools", "Bash")
	cmd.Dir = t.TempDir()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	write := func(text string) {
		b, _ := json.Marshal(claudeUserMessage(text))
		if _, err := stdin.Write(append(b, '\n')); err != nil {
			t.Fatalf("writing a user message: %v", err)
		}
	}
	write("Run exactly this shell command with the Bash tool: `sleep 20 && echo done`. " +
		"When it has finished, answer with the word FINISHED, followed by the code word from any later message I send, if there is one.")

	const word = "PELICAN"
	sent, replayed, results, acted := false, false, 0, false
	rd := bufio.NewReader(stdout)
	for {
		line, err := rd.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			t.Logf("%s", line)
			ev := streamjson.ParseEvent(line)
			switch {
			case ev.Type == "assistant" && !sent && streamjson.CallsTool(line):
				// The slow tool call runs now: steer it.
				time.Sleep(2 * time.Second)
				write("The code word is " + word + ". Take it into account and go on.")
				sent = true
				t.Log("probe: second message written")
			case ev.Type == "user":
				if text, ok := replayText(line); ok && strings.Contains(text, word) {
					replayed = results == 0
					t.Logf("probe: replay seen (before the result: %v)", replayed)
				}
			case ev.Type == "result":
				results++
				if r, perr := ParseResult(line); perr == nil && results == 1 {
					acted = strings.Contains(r.Text, word)
				}
				_ = stdin.Close() // the answer is in: let the CLI exit
			}
		}
		if err != nil {
			break
		}
	}
	waitErr := cmd.Wait()
	t.Logf("probe: exit %v, stderr: %s", waitErr, tail(stderr.String(), 2000))
	if !sent {
		t.Fatal("claude called no tool, so no message was written during one")
	}
	if !replayed {
		t.Error("(a) failed: the second message did not come back as a replayed user line before the result")
	}
	if !acted || results != 1 {
		t.Errorf("(b) failed: claude must act on the message before its single result (results %d, acted %v)", results, acted)
	}
}
