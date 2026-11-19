package runner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// These tests pin how a one-shot claude run treats several result lines: the
// agent answered, a pending wakeup fired and the CLI ended with a later turn
// in plain text (the #504 incident).

const (
	answerLine = `{"type":"result","subtype":"success","is_error":false,"session_id":"s1","result":"done",` +
		`"structured_output":{"status":"done"},"total_cost_usd":1.0,"num_turns":3,"duration_ms":100,` +
		`"usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":30,"cache_creation_input_tokens":40},` +
		`"modelUsage":{"m-a":{"inputTokens":10,"outputTokens":20,"cacheReadInputTokens":30,"cacheCreationInputTokens":40,"costUSD":1.0}},` +
		`"model_usage_since_last_result":{"m-a":{"inputTokens":10,"outputTokens":20,"cacheReadInputTokens":30,"cacheCreationInputTokens":40,"costUSD":1.0}}}`
	laterLine = `{"type":"result","subtype":"success","is_error":false,"session_id":"s1",` +
		`"result":"final status and handoff notes have already been submitted","total_cost_usd":1.5,"num_turns":1,"duration_ms":50,` +
		`"usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":3,"cache_creation_input_tokens":4},` +
		`"modelUsage":{"m-a":{"inputTokens":11,"outputTokens":22,"cacheReadInputTokens":33,"cacheCreationInputTokens":44,"costUSD":1.25},` +
		`"m-b":{"inputTokens":5,"outputTokens":6,"cacheReadInputTokens":7,"cacheCreationInputTokens":8,"costUSD":0.25}},` +
		`"model_usage_since_last_result":{"m-a":{"inputTokens":1,"outputTokens":2,"cacheReadInputTokens":3,"cacheCreationInputTokens":4,"costUSD":0.25},` +
		`"m-b":{"inputTokens":5,"outputTokens":6,"cacheReadInputTokens":7,"cacheCreationInputTokens":8,"costUSD":0.25}}}`
	laterErrorLine = `{"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"s1","result":"boom",` +
		`"total_cost_usd":1.5,"model_usage_since_last_result":{}}`
)

// runStream runs ClaudeCLI against a fake claude that prints lines.
func runStream(t *testing.T, lines ...string) (*Result, error) {
	t.Helper()
	fixture := filepath.Join(t.TempDir(), "stream.jsonl")
	if err := os.WriteFile(fixture, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := sessionFake(t, "#!/bin/sh\ncat "+fixture+"\n")
	return (&ClaudeCLI{Binary: bin, Env: os.Environ()}).Run(context.Background(), Invocation{Dir: t.TempDir()})
}

func TestClaudeRunKeepsAnswerOverLaterTurn(t *testing.T) {
	res, err := runStream(t, `{"type":"system","subtype":"init","session_id":"s1"}`, answerLine, laterLine)
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Status string }
	if err := res.Decode(&got); err != nil || got.Status != "done" {
		t.Fatalf("Decode = %+v, %v; want the first answer", got, err)
	}
	if res.CostUSD != 1.5 {
		t.Errorf("CostUSD = %v, want 1.5 (1.0 + 0.5 since the last result)", res.CostUSD)
	}
	u := res.CallUsage
	want := []ModelUsage{
		{Model: "m-a", InputTokens: 11, OutputTokens: 22, CacheReadTokens: 33, CacheWriteTokens: 44, CostUSD: 1.25},
		{Model: "m-b", InputTokens: 5, OutputTokens: 6, CacheReadTokens: 7, CacheWriteTokens: 8, CostUSD: 0.25},
	}
	if !reflect.DeepEqual(u.Models, want) {
		t.Errorf("Models = %+v, want %+v", u.Models, want)
	}
	if usageTokens(u) != (tokens{11, 22, 33, 44}) || u.Turns != 4 || u.DurationMS != 150 {
		t.Errorf("main thread %v, turns %d, duration %d; want the sum of both lines", usageTokens(u), u.Turns, u.DurationMS)
	}
}

func TestClaudeRunKeepsLastAnswer(t *testing.T) {
	second := strings.Replace(answerLine, `{"status":"done"}`, `{"status":"needs_input"}`, 1)
	res, err := runStream(t, answerLine, second, laterLine)
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Status string }
	if err := res.Decode(&got); err != nil || got.Status != "needs_input" {
		t.Fatalf("Decode = %+v, %v; want the last answer", got, err)
	}
}

func TestClaudeRunFailsOnErrorAfterAnswer(t *testing.T) {
	res, err := runStream(t, answerLine, laterErrorLine)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the later error", err)
	}
	if res == nil || !res.IsError || res.CostUSD != 1.0 {
		t.Errorf("res = %+v, want the error result with the summed cost", res)
	}
}

func TestClaudeRunPlainTextResult(t *testing.T) {
	res, err := runStream(t, `{"type":"result","subtype":"success","is_error":false,"result":"Here: {\"status\":\"done\"}","total_cost_usd":0.5}`)
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Status string }
	if err := res.Decode(&got); err != nil || got.Status != "done" {
		t.Fatalf("Decode = %+v, %v; want the JSON in the text", got, err)
	}
	if res.CostUSD != 0.5 {
		t.Errorf("CostUSD = %v, want 0.5", res.CostUSD)
	}
}

func TestClaudeRunWithoutSinceLastUsesLastTotal(t *testing.T) {
	strip := func(line string) string {
		return line[:strings.Index(line, `,"model_usage_since_last_result"`)] + "}"
	}
	res, err := runStream(t, strip(answerLine), strip(laterLine))
	if err != nil {
		t.Fatal(err)
	}
	if res.CostUSD != 1.5 {
		t.Errorf("CostUSD = %v, want the last line's total_cost_usd 1.5", res.CostUSD)
	}
	if len(res.CallUsage.Models) != 2 || res.CallUsage.Models[0].CostUSD != 1.25 {
		t.Errorf("Models = %+v, want the last line's modelUsage", res.CallUsage.Models)
	}
}

func TestClaudeRunNoResult(t *testing.T) {
	if _, err := runStream(t, `{"type":"system","subtype":"init"}`); err == nil {
		t.Fatal("a stream without a result line must fail")
	}
}

func TestClaudeArgsDenySchedulingTools(t *testing.T) {
	denied := func(inv Invocation) string {
		args := (&ClaudeCLI{}).Args(inv)
		for i, a := range args {
			if a == "--disallowedTools" && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	for _, tc := range []struct {
		name string
		list []string
		want string
	}{
		{"no role list", nil, "ScheduleWakeup,CronCreate,CronDelete,RemoteTrigger"},
		{"implement", implementDenied, "Bash(git push:*),ScheduleWakeup,CronCreate,CronDelete,RemoteTrigger"},
		{"overlap", append(append([]string{}, implementDenied...), "CronCreate"), "Bash(git push:*),CronCreate,ScheduleWakeup,CronDelete,RemoteTrigger"},
	} {
		if got := denied(Invocation{DisallowedTools: tc.list}); got != tc.want {
			t.Errorf("%s: --disallowedTools %q, want %q", tc.name, got, tc.want)
		}
	}
	// The role's list stays as it is, also with room to append.
	list := append(make([]string, 0, 8), "Bash(git push:*)")
	denied(Invocation{DisallowedTools: list})
	if spare := list[:2]; spare[1] != "" {
		t.Errorf("Args wrote %q into the role's list", spare[1])
	}
}
