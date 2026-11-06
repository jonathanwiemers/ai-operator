package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// These tests pin what claude's result line holds (R5 in
// docs/specs/honest-reporting.spec.md). They change no code: a CLI update
// that changes the relations fails here instead of skewing spend silently.
// testdata/README.md says which fixtures are recorded and which are
// reconstructed.

// tokens is input, output, cache read and cache write of one usage.
type tokens [4]int64

func (a tokens) plus(b tokens) tokens {
	return tokens{a[0] + b[0], a[1] + b[1], a[2] + b[2], a[3] + b[3]}
}

func (a tokens) sum() int64 { return a[0] + a[1] + a[2] + a[3] }

func usageTokens(u *CallUsage) tokens {
	return tokens{u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens}
}

func modelsTokens(u *CallUsage) tokens {
	var t tokens
	for _, m := range u.Models {
		t = t.plus(tokens{m.InputTokens, m.OutputTokens, m.CacheReadTokens, m.CacheWriteTokens})
	}
	return t
}

// basisLine is the part of a fixture line these tests read.
type basisLine struct {
	Type            string `json:"type"`
	Source          string `json:"source"`
	ParentToolUseID string `json:"parent_tool_use_id"`
	Message         struct {
		ID    string `json:"id"`
		Usage *struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ResultIndex *int `json:"result_index"`
	SinceLast   map[string]struct {
		InputTokens              int64   `json:"inputTokens"`
		OutputTokens             int64   `json:"outputTokens"`
		CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
		CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
		CostUSD                  float64 `json:"costUSD"`
	} `json:"model_usage_since_last_result"`
	// cost-state (the transcript entry --resume restores)
	TotalCostUSD float64 `json:"totalCostUSD"`
	ModelUsage   map[string]struct {
		InputTokens              int64   `json:"inputTokens"`
		OutputTokens             int64   `json:"outputTokens"`
		CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
		CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
		CostUSD                  float64 `json:"costUSD"`
	} `json:"modelUsage"`
}

func (l basisLine) sinceLast() tokens {
	var t tokens
	for _, m := range l.SinceLast {
		t = t.plus(tokens{m.InputTokens, m.OutputTokens, m.CacheReadInputTokens, m.CacheCreationInputTokens})
	}
	return t
}

// basisFixture reads a fixture and checks its header names the source.
func basisFixture(t *testing.T, name, source string) (path string, lines []basisLine) {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var l basisLine
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		lines = append(lines, l)
	}
	if lines[0].Type != "fixture" || lines[0].Source != source {
		t.Fatalf("%s: header %+v, want a fixture line with source %q", name, lines[0], source)
	}
	return path, lines
}

// messageTokens sums the assistant messages of lines whose
// thread matches main (parent_tool_use_id null) or not, once per message id.
func messageTokens(lines []basisLine, main bool) tokens {
	var t tokens
	seen := map[string]bool{}
	for _, l := range lines {
		u := l.Message.Usage
		if l.Type != "assistant" || u == nil || seen[l.Message.ID] || (l.ParentToolUseID == "") != main {
			continue
		}
		seen[l.Message.ID] = true
		t = t.plus(tokens{u.InputTokens, u.OutputTokens, u.CacheReadInputTokens, u.CacheCreationInputTokens})
	}
	return t
}

func runFixture(t *testing.T, path string) *Result {
	t.Helper()
	bin := sessionFake(t, "#!/bin/sh\ncat "+path+"\n")
	res, err := (&ClaudeCLI{Binary: bin, Env: os.Environ()}).Run(context.Background(), Invocation{Dir: t.TempDir()})
	if err != nil && (res == nil || !res.IsError) {
		t.Fatal(err)
	}
	if res.CallUsage == nil {
		t.Fatal("CallUsage is nil")
	}
	return res
}

// TestBasisUsageExcludesSubagents: a general-purpose subagent runs on the
// main model. usage holds the main thread only; modelUsage holds both, so
// the sum of the models exceeds usage by exactly the subagent's tokens,
// all on the same model.
func TestBasisUsageExcludesSubagents(t *testing.T) {
	path, lines := basisFixture(t, "claude-stream-subagent-main-model.jsonl", "reconstructed")
	u := runFixture(t, path).CallUsage
	main, sub := messageTokens(lines, true), messageTokens(lines, false)
	if sub.sum() == 0 {
		t.Fatal("the fixture has no subagent turns")
	}
	if got := usageTokens(u); got != main {
		t.Errorf("usage %v, want the main thread's %v", got, main)
	}
	if got := modelsTokens(u); got != main.plus(sub) {
		t.Errorf("sum of modelUsage %v, want main %v + subagent %v", got, main, sub)
	}
	if len(u.Models) != 1 || u.Models[0].Model != "claude-opus-5-5" {
		t.Errorf("models %+v, want one: the main model", u.Models)
	}
	if u.SubagentCalls != 1 {
		t.Errorf("SubagentCalls %d, want 1", u.SubagentCalls)
	}
	// A one-shot call's model_usage_since_last_result is its modelUsage.
	if got := lines[len(lines)-1].sinceLast(); got != modelsTokens(u) {
		t.Errorf("model_usage_since_last_result %v, want modelUsage %v", got, modelsTokens(u))
	}
}

// TestBasisChatRunningTotals: two turns in one process. Turn 2's usage
// holds turn 2 only; its modelUsage and total_cost_usd hold both turns.
// model_usage_since_last_result holds turn 2 only.
func TestBasisChatRunningTotals(t *testing.T) {
	path, lines := basisFixture(t, "claude-chat-two-turns.jsonl", "reconstructed")
	var cut int // the index of turn 1's result line
	for i, l := range lines {
		if l.Type == "result" {
			cut = i
			break
		}
	}
	turn1, turn2 := messageTokens(lines[:cut], true), messageTokens(lines[cut+1:], true)
	results := sessionTurns(t, path, cut+1, 2)
	r1, r2 := results[0], results[1]

	if got := usageTokens(r1.CallUsage); got != turn1 {
		t.Errorf("turn 1 usage %v, want %v", got, turn1)
	}
	if got := modelsTokens(r1.CallUsage); got != turn1 {
		t.Errorf("turn 1 modelUsage %v, want %v", got, turn1)
	}
	if got := usageTokens(r2.CallUsage); got != turn2 {
		t.Errorf("turn 2 usage %v, want turn 2 alone %v", got, turn2)
	}
	if got := modelsTokens(r2.CallUsage); got != turn1.plus(turn2) {
		t.Errorf("turn 2 modelUsage %v, want the running total %v", got, turn1.plus(turn2))
	}
	if r2.CostUSD <= r1.CostUSD {
		t.Errorf("turn 2 total_cost_usd %v not above turn 1's %v: not a running total", r2.CostUSD, r1.CostUSD)
	}
	last := lines[len(lines)-1]
	if got := last.sinceLast(); got != turn2 {
		t.Errorf("turn 2 model_usage_since_last_result %v, want turn 2 alone %v", got, turn2)
	}
	var since float64
	for _, m := range last.SinceLast {
		since += m.CostUSD
	}
	if d := r2.CostUSD - r1.CostUSD; !closeTo(since, d) {
		t.Errorf("turn 2 cost since last result %v, want the difference of the running totals %v", since, d)
	}
}

// TestBasisResumeCarriesTotals (recorded): a -p --resume of a session whose
// transcript holds a cost-state starts its modelUsage and total_cost_usd at
// the saved totals. usage and subagent_stats start at zero, and
// model_usage_since_last_result holds only what this process counted.
func TestBasisResumeCarriesTotals(t *testing.T) {
	path, lines := basisFixture(t, "claude-resume-carryover.jsonl", "recorded")
	res := runFixture(t, path)
	checkCarried(t, lines[1], res, lines[len(lines)-1])
}

// TestBasisResumedChatKeepsTotals (recorded): a resumed stream-json session
// with two turns. Both results carry the restored totals; nothing is spent
// in between, so neither modelUsage nor total_cost_usd is per turn.
func TestBasisResumedChatKeepsTotals(t *testing.T) {
	path, lines := basisFixture(t, "claude-chat-resume-two-turns.jsonl", "recorded")
	var resultLines []basisLine
	var cut int
	for i, l := range lines {
		if l.Type == "result" {
			if cut == 0 {
				cut = i
			}
			resultLines = append(resultLines, l)
		}
	}
	results := sessionTurns(t, path, cut+1, 2)
	for i, res := range results {
		checkCarried(t, lines[1], res, resultLines[i])
		if idx := resultLines[i].ResultIndex; idx == nil || *idx != i {
			t.Errorf("result %d: result_index %v", i, idx)
		}
	}
}

// checkCarried compares a result after --resume with the seeded cost-state.
func checkCarried(t *testing.T, seed basisLine, res *Result, line basisLine) {
	t.Helper()
	if seed.Type != "cost-state" {
		t.Fatalf("line 1 is %q, want the seeded cost-state", seed.Type)
	}
	want := make([]ModelUsage, 0, len(seed.ModelUsage))
	for name, m := range seed.ModelUsage {
		want = append(want, ModelUsage{Model: name, InputTokens: m.InputTokens, OutputTokens: m.OutputTokens,
			CacheReadTokens: m.CacheReadInputTokens, CacheWriteTokens: m.CacheCreationInputTokens, CostUSD: m.CostUSD})
	}
	u := res.CallUsage
	if len(want) == 0 || !sameModels(u.Models, want) {
		t.Errorf("modelUsage %+v, want the saved totals %+v", u.Models, want)
	}
	if res.CostUSD != seed.TotalCostUSD {
		t.Errorf("total_cost_usd %v, want the saved %v", res.CostUSD, seed.TotalCostUSD)
	}
	if got := usageTokens(u); got != (tokens{}) {
		t.Errorf("usage %v, want zero: no model call in this process", got)
	}
	if u.SubagentCalls != 0 {
		t.Errorf("SubagentCalls %d, want 0", u.SubagentCalls)
	}
	if line.SinceLast == nil || len(line.SinceLast) != 0 {
		t.Errorf("model_usage_since_last_result %v, want {} (present, nothing counted)", line.SinceLast)
	}
}

func sameModels(got, want []ModelUsage) bool {
	m := map[string]ModelUsage{}
	for _, w := range want {
		m[w.Model] = w
	}
	if len(got) != len(m) {
		return false
	}
	for _, g := range got {
		if !reflect.DeepEqual(g, m[g.Model]) {
			return false
		}
	}
	return true
}

func closeTo(a, b float64) bool {
	d := a - b
	return d < 1e-9 && d > -1e-9
}

// sessionTurns runs a ClaudeSession whose fake prints the fixture's lines up
// to line cut (exclusive) for the first turn and the rest for the second.
func sessionTurns(t *testing.T, path string, cut, turns int) []*Result {
	t.Helper()
	script := "#!/bin/sh\nn=0\nwhile read -r line; do\n  n=$((n+1))\n" +
		"  if [ $n = 1 ]; then head -n " + strconv.Itoa(cut) + " " + path + "; else tail -n +" + strconv.Itoa(cut+1) + " " + path + "; fi\ndone\n"
	s, err := (&ClaudeCLI{Binary: sessionFake(t, script), Env: os.Environ()}).Start(context.Background(), Invocation{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var out []*Result
	for i := range turns {
		res, err := s.Prompt(context.Background(), "turn", nil)
		if err != nil && (res == nil || !res.IsError) {
			t.Fatalf("turn %d: %v", i+1, err)
		}
		if res.CallUsage == nil {
			t.Fatalf("turn %d: CallUsage is nil", i+1)
		}
		out = append(out, res)
	}
	return out
}
