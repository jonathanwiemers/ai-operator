package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
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

// TestClaudeSessionTurnDeltas (R7): on the two-turn chat fixture the second
// result holds only turn 2: tokens, models and cost. The first is as is.
// One-shot calls keep the result's figures (TestBasisResumeCarriesTotals,
// TestBasisUsageExcludesSubagents).
func TestClaudeSessionTurnDeltas(t *testing.T) {
	path, lines := basisFixture(t, "claude-chat-two-turns.jsonl", "reconstructed")
	cut := slices.IndexFunc(lines, func(l basisLine) bool { return l.Type == "result" })
	turn1, turn2 := messageTokens(lines[:cut], true), messageTokens(lines[cut+1:], true)
	raw := rawResults(t, path)
	results := sessionTurns(t, path, cut+1, 2)

	if got := modelsTokens(results[0].CallUsage); got != turn1 || results[0].CostUSD != raw[0].CostUSD {
		t.Errorf("turn 1: models %v cost %v, want as reported %v %v", got, results[0].CostUSD, turn1, raw[0].CostUSD)
	}
	r2 := results[1]
	if got := modelsTokens(r2.CallUsage); got != turn2 {
		t.Errorf("turn 2 models %v, want turn 2 alone %v", got, turn2)
	}
	if want := raw[1].CostUSD - raw[0].CostUSD; !closeTo(r2.CostUSD, want) {
		t.Errorf("turn 2 cost %v, want %v", r2.CostUSD, want)
	}
	if ms := r2.CallUsage.Models; len(ms) != 1 || ms[0].Model != "claude-opus-5-5" || !closeTo(ms[0].CostUSD, r2.CostUSD) {
		t.Errorf("turn 2 models %+v", ms)
	}
	c := agentCall(aiv1.EngineClaude, Invocation{}, r2, time.Now(), time.Now())
	if got := (tokens{c.InputTokens, c.OutputTokens, c.CacheReadTokens, c.CacheWriteTokens}); got != turn2 || c.CostUSD != aiv1.FormatCost(r2.CostUSD) {
		t.Errorf("turn 2 row: tokens %v cost %s, want %v %s", got, c.CostUSD, turn2, aiv1.FormatCost(r2.CostUSD))
	}
}

// TestClaudeSessionResumedTurnsSinceLast (recorded): a resumed process whose
// turns made no model call records nothing, although modelUsage and
// total_cost_usd carry the saved totals: model_usage_since_last_result is {}.
func TestClaudeSessionResumedTurnsSinceLast(t *testing.T) {
	path, lines := basisFixture(t, "claude-chat-resume-two-turns.jsonl", "recorded")
	cut := slices.IndexFunc(lines, func(l basisLine) bool { return l.Type == "result" })
	for i, res := range sessionTurns(t, path, cut+1, 2) {
		if res.CostUSD != 0 || len(res.CallUsage.Models) != 0 {
			t.Errorf("turn %d: cost %v models %+v, want nothing", i+1, res.CostUSD, res.CallUsage.Models)
		}
	}
}

// TestClaudeSessionTurnDeltasWithoutSinceLast: without
// model_usage_since_last_result each result's running totals minus the
// previous one's. A new model counts from zero, an unchanged one is dropped,
// and a shrinking figure is clamped to zero and logged.
func TestClaudeSessionTurnDeltasWithoutSinceLast(t *testing.T) {
	bin := sessionFake(t, `#!/bin/sh
n=0
while read -r line; do
  n=$((n+1))
  case $n in
  1) echo '{"type":"result","subtype":"success","session_id":"s","num_turns":1,"total_cost_usd":0.5,"usage":{"input_tokens":10,"output_tokens":100},"modelUsage":{"opus":{"inputTokens":10,"outputTokens":100,"cacheReadInputTokens":1000,"cacheCreationInputTokens":200,"costUSD":0.5}},"subagent_stats":{"spawned":1}}' ;;
  2) echo '{"type":"result","subtype":"success","session_id":"s","num_turns":2,"total_cost_usd":0.8,"usage":{"input_tokens":5,"output_tokens":60},"modelUsage":{"opus":{"inputTokens":15,"outputTokens":160,"cacheReadInputTokens":2000,"cacheCreationInputTokens":300,"costUSD":0.6},"haiku":{"inputTokens":5,"outputTokens":50,"cacheReadInputTokens":0,"cacheCreationInputTokens":500,"costUSD":0.2}},"subagent_stats":{"spawned":3}}' ;;
  *) echo '{"type":"result","subtype":"success","session_id":"s","num_turns":1,"total_cost_usd":0.7,"usage":{"input_tokens":1,"output_tokens":2},"modelUsage":{"opus":{"inputTokens":15,"outputTokens":160,"cacheReadInputTokens":2000,"cacheCreationInputTokens":300,"costUSD":0.6},"haiku":{"inputTokens":5,"outputTokens":40,"cacheReadInputTokens":0,"cacheCreationInputTokens":500,"costUSD":0.1}},"subagent_stats":{"spawned":3}}' ;;
  esac
done
`)
	s, err := (&ClaudeCLI{Binary: bin, Env: os.Environ()}).Start(context.Background(), Invocation{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var logs []string
	ctx := logr.NewContext(context.Background(), funcr.New(func(_, args string) { logs = append(logs, args) }, funcr.Options{}))
	var results []*Result
	logsAfter2 := -1
	for i := range 3 {
		res, err := s.Prompt(ctx, "turn", nil)
		if err != nil {
			t.Fatalf("turn %d: %v", i+1, err)
		}
		results = append(results, res)
		if i == 1 {
			logsAfter2 = len(logs)
		}
	}

	if u := results[0].CallUsage; results[0].CostUSD != 0.5 || u.SubagentCalls != 1 || len(u.Models) != 1 || u.Models[0].OutputTokens != 100 {
		t.Errorf("turn 1 not as reported: cost %v %+v", results[0].CostUSD, u)
	}
	u := results[1].CallUsage
	want := []ModelUsage{
		{Model: "haiku", InputTokens: 5, OutputTokens: 50, CacheWriteTokens: 500, CostUSD: 0.2},
		{Model: "opus", InputTokens: 5, OutputTokens: 60, CacheReadTokens: 1000, CacheWriteTokens: 100, CostUSD: 0.1},
	}
	if len(u.Models) != 2 || !closeTo(results[1].CostUSD, 0.3) || u.SubagentCalls != 2 || u.Turns != 2 || u.OutputTokens != 60 {
		t.Fatalf("turn 2: cost %v %+v", results[1].CostUSD, u)
	}
	for i, m := range u.Models {
		w := want[i]
		if !closeTo(m.CostUSD, w.CostUSD) {
			t.Errorf("turn 2 %s cost %v, want %v", m.Model, m.CostUSD, w.CostUSD)
		}
		m.CostUSD, w.CostUSD = 0, 0
		if m != w {
			t.Errorf("turn 2 model %+v, want %+v", m, w)
		}
	}
	if logsAfter2 != 0 {
		t.Errorf("logged before anything shrank: %v", logs)
	}
	u = results[2].CallUsage
	if results[2].CostUSD != 0 || len(u.Models) != 0 || u.SubagentCalls != 0 || u.OutputTokens != 2 {
		t.Errorf("turn 3 (shrank): cost %v %+v, want zero and usage as reported", results[2].CostUSD, u)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "shrank") {
		t.Errorf("logs %v, want one about the shrink", logs)
	}
}
