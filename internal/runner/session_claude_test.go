package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func sessionFake(t *testing.T, script string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestClaudeSessionTwoTurns(t *testing.T) {
	bin := sessionFake(t, `#!/bin/sh
n=0
while read -r line; do
  printf '%s\n' "$line" >> "$(dirname "$0")/in"
  n=$((n+1))
  echo "{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"sid-$n\"}"
  echo "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"ok-$n\"}]}}"
  echo "{\"duration_api_ms\":0,\"subtype\":\"success\",\"is_error\":false,\"result\":\"turn-$n\",\"session_id\":\"sid-$n\",\"type\":\"result\",\"total_cost_usd\":0.1}"
done
`)
	s, err := (&ClaudeCLI{Binary: bin, Env: os.Environ()}).Start(context.Background(), Invocation{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	texts := []string{`hello "world"`, "line1\nline2"}
	for i, text := range texts {
		var events []Event
		res, err := s.Prompt(context.Background(), text, func(e Event) { events = append(events, e) })
		if err != nil {
			t.Fatalf("turn %d: %v", i+1, err)
		}
		wantID := fmt.Sprintf("sid-%d", i+1)
		if res.Text != fmt.Sprintf("turn-%d", i+1) || res.SessionID != wantID || s.ID() != wantID {
			t.Fatalf("turn %d: got text=%q id=%q session.ID=%q", i+1, res.Text, res.SessionID, s.ID())
		}
		var sawAssistant, sawResult bool
		for _, e := range events {
			if e.Type == "assistant" {
				sawAssistant = true
			}
			if e.Type == "result" {
				sawResult = true
			}
		}
		if !sawAssistant || !sawResult {
			t.Fatalf("turn %d: events=%v", i+1, events)
		}
	}

	raw, err := os.ReadFile(filepath.Join(filepath.Dir(bin), "in"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var msg struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != "user" || len(msg.Message.Content) != 1 {
			t.Fatalf("input line: %s", line)
		}
		got = append(got, msg.Message.Content[0].Text)
	}
	if len(got) != 2 || got[0] != texts[0] || got[1] != texts[1] {
		t.Fatalf("round-trip texts: %#v", got)
	}
}

func TestClaudeSessionResultTypeNotFirst(t *testing.T) {
	bin := sessionFake(t, `#!/bin/sh
while read line; do
  echo '{"duration_api_ms":0,"session_id":"s-order","is_error":false,"result":"ordered","type":"result","subtype":"success"}'
done
`)
	s, err := (&ClaudeCLI{Binary: bin, Env: os.Environ()}).Start(context.Background(), Invocation{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res, err := s.Prompt(context.Background(), "hi", nil)
	if err != nil || res.Text != "ordered" || res.SessionID != "s-order" {
		t.Fatalf("got %+v err=%v", res, err)
	}
}

func TestClaudeSessionIsErrorStillUsable(t *testing.T) {
	bin := sessionFake(t, `#!/bin/sh
n=0
while read line; do
  n=$((n+1))
  if [ "$n" -eq 1 ]; then
    echo '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"boom","session_id":"s1"}'
  else
    echo '{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"s1"}'
  fi
done
`)
	s, err := (&ClaudeCLI{Binary: bin, Env: os.Environ()}).Start(context.Background(), Invocation{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res, err := s.Prompt(context.Background(), "one", nil)
	if err == nil || res == nil || !res.IsError || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want is_error: res=%+v err=%v", res, err)
	}
	res, err = s.Prompt(context.Background(), "two", nil)
	if err != nil || res.Text != "ok" {
		t.Fatalf("second turn: %+v %v", res, err)
	}
}

func TestClaudeSessionProcessExitMidTurn(t *testing.T) {
	bin := sessionFake(t, `#!/bin/sh
while read line; do
  echo 'out of tokens' >&2
  exit 1
done
`)
	s, err := (&ClaudeCLI{Binary: bin, Env: os.Environ()}).Start(context.Background(), Invocation{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Prompt(context.Background(), "go", nil)
	if err == nil || !strings.Contains(err.Error(), "out of tokens") {
		t.Fatalf("want stderr tail: %v", err)
	}
	_, err = s.Prompt(context.Background(), "again", nil)
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("dead session must fail fast: %v", err)
	}
}

func TestClaudeSessionCancel(t *testing.T) {
	bin := sessionFake(t, `#!/bin/sh
turn=0
while read line; do
  case "$line" in
  *control_request*)
    rid=$(printf '%s' "$line" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
    echo "{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"$rid\"}}"
    echo '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"interrupted","session_id":"s1"}'
    ;;
  *)
    turn=$((turn+1))
    if [ "$turn" -eq 1 ]; then
      touch "$(dirname "$0")/waiting"
      read ctrl
      rid=$(printf '%s' "$ctrl" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
      echo "{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"$rid\"}}"
      echo '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"interrupted","session_id":"s1"}'
    else
      echo '{"type":"system","subtype":"init","session_id":"s1"}'
      echo '{"type":"result","subtype":"success","is_error":false,"result":"after-cancel","session_id":"s1"}'
    fi
    ;;
  esac
done
`)
	s, err := (&ClaudeCLI{Binary: bin, Env: os.Environ()}).Start(context.Background(), Invocation{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	done := make(chan struct{})
	var promptErr error
	go func() {
		_, promptErr = s.Prompt(context.Background(), "slow", nil)
		close(done)
	}()
	waiting := filepath.Join(filepath.Dir(bin), "waiting")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(waiting); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake never started waiting for cancel")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := s.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt did not return after Cancel")
	}
	if promptErr == nil || !strings.Contains(promptErr.Error(), "interrupted") {
		t.Fatalf("cancel turn: %v", promptErr)
	}
	res, err := s.Prompt(context.Background(), "next", nil)
	if err != nil || res.Text != "after-cancel" {
		t.Fatalf("next turn: %+v %v", res, err)
	}
}

func TestClaudeSessionConcurrentPrompt(t *testing.T) {
	bin := sessionFake(t, `#!/bin/sh
while read line; do
  touch "$(dirname "$0")/busy"
  sleep 0.5
  echo '{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"s1"}'
done
`)
	s, err := (&ClaudeCLI{Binary: bin, Env: os.Environ()}).Start(context.Background(), Invocation{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = s.Prompt(context.Background(), "first", nil)
	}()
	busy := filepath.Join(filepath.Dir(bin), "busy")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(busy); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first Prompt never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, err = s.Prompt(context.Background(), "second", nil)
	if err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("concurrent Prompt: %v", err)
	}
	wg.Wait()
}

func TestClaudeSessionCloseKills(t *testing.T) {
	prev := sessionCloseWait
	sessionCloseWait = 200 * time.Millisecond
	defer func() { sessionCloseWait = prev }()

	bin := sessionFake(t, "#!/bin/sh\ntrap '' TERM\nsleep 1000\n")
	s, err := (&ClaudeCLI{Binary: bin, Env: os.Environ()}).Start(context.Background(), Invocation{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_ = s.Close()
	if time.Since(start) > 2*time.Second {
		t.Fatalf("Close took too long: %v", time.Since(start))
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close must be idempotent: %v", err)
	}
}

func TestClaudeSessionIgnoredInterruptEndsSession(t *testing.T) {
	prevCancel, prevClose := sessionCancelWait, sessionCloseWait
	sessionCancelWait, sessionCloseWait = 200*time.Millisecond, 200*time.Millisecond
	defer func() { sessionCancelWait, sessionCloseWait = prevCancel, prevClose }()
	// Never answers a turn and ignores the interrupt; SIGINT ends it.
	bin := sessionFake(t, `#!/bin/sh
trap 'exit 130' INT
while read -r line; do :; done
`)
	s, err := (&ClaudeCLI{Binary: bin, Env: os.Environ()}).Start(context.Background(), Invocation{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := s.Prompt(ctx, "hang", nil); err == nil {
		t.Fatal("a turn without result must fail")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the turn must end after the cancel wait, took %s", d)
	}
	if _, err := s.Prompt(context.Background(), "again", nil); err == nil {
		t.Fatal("the session must be over after an ignored interrupt")
	}
}
