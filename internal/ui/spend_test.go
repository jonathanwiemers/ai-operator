package ui

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// spendEnv logs in a user who may see team-a/shop only, with the research
// database configured and reader (if not nil) connected.
func spendEnv(t *testing.T, reader *store.Store) *testEnv {
	t.Helper()
	env := newTestEnv(t, withResearch(oidcConfig(Rule{Roles: []string{"shop-devs"}, Projects: []string{"team-a/shop"}}), false))
	if reader != nil {
		env.server.SetResearchStore(reader)
	}
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{"shop-devs": map[string]any{"1": "acme"}}}
	env.login(t, "/")
	return env
}

func TestSpendGating(t *testing.T) {
	off := newTestEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}})
	if code := off.get(t, "/api/v1/spend?namespace=team-a&project=shop", nil); code != http.StatusNotFound {
		t.Errorf("without a database: status %d, want 404", code)
	}

	env := spendEnv(t, nil)
	if code := env.get(t, "/api/v1/spend?namespace=team-b&project=billing", nil); code != http.StatusNotFound {
		t.Errorf("another project: status %d, want 404", code)
	}
	for _, bad := range []string{"namespace=team-a", "project=shop", "namespace=team-a&project=shop&milestone=x",
		"namespace=team-a&project=shop&issue=0", "namespace=team-a&project=shop&run=-1&task=t",
		"namespace=team-a&project=shop&run=2"} {
		if code := env.get(t, "/api/v1/spend?"+bad, nil); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", bad, code)
		}
	}
	if code := env.get(t, "/api/v1/spend?namespace=team-a&project=shop", nil); code != http.StatusServiceUnavailable {
		t.Errorf("not connected yet: status %d, want 503", code)
	}
}

func TestSpendViewUnknownCost(t *testing.T) {
	v := spendView(store.Spend{
		Totals: store.SpendTotals{CostUSD: 0, CostKnown: false, UnknownCalls: 1, Calls: 1},
		Children: []store.SpendChild{{Level: store.SpendCall, Key: "1", Totals: store.SpendTotals{Calls: 1, UnknownCalls: 1},
			Models: []store.ModelSplit{{Model: "gpt-5", Main: true, Tokens: 10}},
			Call:   &store.Call{Kind: "fix", Engine: "cursor", CostSource: "unknown"}}},
	})
	if v.Totals.CostUSD != "" || v.Totals.CostKnown {
		t.Errorf("totals = %+v, want an unknown cost", v.Totals)
	}
	c := v.Children[0]
	if c.Totals.CostUSD != "" || c.Models[0].CostUSD != "" || c.SpendCallView == nil || c.ContextSeries == nil ||
		c.SubagentTurns == nil || v.ByKind == nil || v.ByReviewRound == nil || v.BySession == nil || v.ByCheckpoint == nil || v.BySubagents == nil {
		t.Errorf("child = %+v, want unknown costs and empty lists", c)
	}
}

// TestSpendViewUnmetered (R8): the unmetered count and a model split
// without cost or tokens reach the views next to a known cost.
func TestSpendViewUnmetered(t *testing.T) {
	v := spendView(store.Spend{
		Totals: store.SpendTotals{CostUSD: 0.5, CostKnown: true, UnknownCalls: 1, UnmeteredCalls: 1, Calls: 2},
		Children: []store.SpendChild{{Level: store.SpendIssue, Key: "402",
			Totals: store.SpendTotals{Calls: 1, UnknownCalls: 1, UnmeteredCalls: 1},
			Models: []store.ModelSplit{{Model: "auto", Main: true, Unmetered: true}}}},
	})
	if v.Totals.UnmeteredCalls != 1 || !v.Totals.CostKnown || v.Totals.CostUSD != "0.50" {
		t.Errorf("totals = %+v, want $0.50 known and 1 unmetered call", v.Totals)
	}
	if m := v.Children[0].Models[0]; !m.Unmetered || m.CostUSD != "" || v.Children[0].Totals.UnmeteredCalls != 1 {
		t.Errorf("child = %+v, want an unmetered auto split", v.Children[0])
	}
}

func TestSpend(t *testing.T) {
	owner, reader := readerStore(t)
	base := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	usd := func(v float64) *float64 { return &v }
	call := func(key string, issue int64, task string, run, seq int64, kind, model string, cost *float64) store.Call {
		source := "reported"
		if cost == nil {
			source = "unknown"
		}
		return store.Call{Key: key, At: base.Add(time.Duration(seq) * time.Minute), Namespace: "team-a", Project: "shop",
			MilestoneIID: 3, IssueIID: issue, Task: task, Run: run, Seq: seq, Kind: kind, Role: kind, Engine: "claude",
			Model: model, InputTokens: 10, OutputTokens: 20, CostUSD: cost, CostSource: source, DurationMS: 1000,
			ExpiresAt: base.Add(720 * time.Hour)}
	}
	impl := call("c1", 42, "shop-42", 2, 1, "implement", "claude-opus-5-5", usd(1.5))
	impl.Models = []store.CallModel{
		{Model: "claude-opus-5-5", InputTokens: 8, OutputTokens: 15, CostUSD: usd(1.4)},
		{Model: "claude-haiku-5-5", InputTokens: 2, OutputTokens: 5, CostUSD: usd(0.1)},
	}
	impl.SubagentCalls, impl.ContextSeries, impl.SubagentTurns, impl.ReadGateBlocks = 1, []int64{1000, 2500, 4000}, []int32{1}, 4
	impl.Checkpoint, impl.CheckpointSHA, impl.LinesAdded, impl.LinesDeleted = "usage-limit", "abc123", 84, 9
	fix := call("c2", 42, "shop-42", 2, 2, "fix", "gpt-5", nil)
	fix.Engine = "cursor"
	other := call("c3", 43, "shop-43", 1, 1, "implement", "claude-opus-5-5", usd(0.25))
	if _, err := owner.InsertCalls(t.Context(), []store.Call{impl, fix, other}); err != nil {
		t.Fatal(err)
	}
	// The new pod after a lost one saved run 2's work without a call.
	if _, err := owner.InsertEvents(t.Context(), []store.Event{{Key: "task/u42/checkpoint/2/def456", At: base.Add(3 * time.Minute),
		Namespace: "team-a", Project: "shop", MilestoneIID: 3, IssueIID: 42, Task: "shop-42", Kind: "checkpoint",
		Data: map[string]any{"reason": "pod-lost", "sha": "def456", "run": 2}, ExpiresAt: base.Add(720 * time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	env := spendEnv(t, reader)

	var ms SpendView
	if code := env.get(t, "/api/v1/spend?namespace=team-a&project=shop&milestone=3", &ms); code != http.StatusOK {
		t.Fatalf("milestone: status %d", code)
	}
	if ms.Totals.CostUSD != "1.75" || ms.Totals.Calls != 3 || ms.Totals.UnknownCalls != 1 || ms.Totals.Checkpoints != 2 {
		t.Errorf("milestone totals = %+v", ms.Totals)
	}
	var keys []string
	for _, c := range ms.Children {
		keys = append(keys, c.Level+":"+c.Key)
	}
	if !slices.Equal(keys, []string{"issue:42", "issue:43"}) {
		t.Fatalf("milestone children = %v, want issues 42 and 43", keys)
	}
	models := map[string]ModelSplitView{}
	for _, m := range ms.Children[0].Models {
		models[m.Model] = m
	}
	want := map[string]ModelSplitView{
		"claude-opus-5-5":  {Model: "claude-opus-5-5", Main: true, Tokens: 23, CostUSD: "1.40", CostKnown: true},
		"claude-haiku-5-5": {Model: "claude-haiku-5-5", Tokens: 7, CostUSD: "0.10", CostKnown: true},
		"gpt-5":            {Model: "gpt-5", Main: true, Tokens: 30},
	}
	if len(models) != len(want) {
		t.Errorf("#42 models = %+v", ms.Children[0].Models)
	}
	for k, w := range want {
		if models[k] != w {
			t.Errorf("#42 %s = %+v, want %+v", k, models[k], w)
		}
	}
	if ms.Children[0].Totals.Checkpoints != 2 || ms.Children[1].Totals.Checkpoints != 0 {
		t.Errorf("issue checkpoints = %d, %d; want 2 and 0", ms.Children[0].Totals.Checkpoints, ms.Children[1].Totals.Checkpoints)
	}
	if ms.Children[0].SpendCallView != nil || len(ms.ByKind) != 2 || len(ms.ByModel) != 3 {
		t.Errorf("milestone = %+v", ms)
	}
	if s := ms.BySession; len(s) != 1 || s[0].Key != "fresh" || s[0].Totals.Calls != 2 || s[0].Totals.CostUSD != "1.75" {
		t.Errorf("milestone by session = %+v, want two fresh implement calls", s)
	}
	var sub []SubagentBreakdownView
	for _, b := range ms.BySubagents {
		if b.WithSubagents {
			sub = append(sub, b)
		}
	}
	if len(sub) != 1 || sub[0].Role != "implement" || sub[0].Totals.Calls != 1 || sub[0].SubagentTokens != 7 || sub[0].ReadGateBlocks != 4 || ms.ByReviewRound == nil {
		t.Errorf("milestone by subagents = %+v, want #42's implement call with 7 subagent tokens", ms.BySubagents)
	}

	var run SpendView
	if code := env.get(t, "/api/v1/spend?namespace=team-a&project=shop&task=shop-42&run=2", &run); code != http.StatusOK {
		t.Fatalf("run: status %d", code)
	}
	if len(run.Children) != 2 {
		t.Fatalf("run children = %+v, want two calls", run.Children)
	}
	c := run.Children[0]
	if c.Level != "call" || c.Key != "1" || c.SpendCallView == nil || c.Kind != "implement" ||
		!slices.Equal(c.ContextSeries, []int64{1000, 2500, 4000}) || !slices.Equal(c.SubagentTurns, []int32{1}) || c.ReadGateBlocks != 4 ||
		c.Checkpoint != "usage-limit" || c.CheckpointSHA != "abc123" || c.LinesAdded != 84 || c.LinesDeleted != 9 {
		t.Errorf("first call = %+v", c)
	}
	if f := run.Children[1]; f.Checkpoint != "" || f.LinesAdded != 0 {
		t.Errorf("fix call = %+v, want no checkpoint or lines", f)
	}
	if b := run.ByCheckpoint; len(b) != 2 || b[0].Key != "pod-lost" || b[0].Count != 1 || b[0].Totals.Calls != 0 ||
		b[1].Key != "usage-limit" || b[1].Count != 1 || b[1].Totals.Calls != 1 || run.Totals.Checkpoints != 2 || run.Totals.Calls != 2 {
		t.Errorf("run by checkpoint = %+v (totals %+v), want pod-lost without a call and one usage-limit call", b, run.Totals)
	}
	if f := run.Children[1]; f.Totals.CostUSD != "" || f.Totals.CostKnown || f.CostSource != "unknown" {
		t.Errorf("cursor call = %+v, want an unknown cost", f)
	}
}

// The call view carries the checkpoint and the uncommitted lines, and the
// checkpoints of the scope come by reason.
func TestSpendViewCheckpoints(t *testing.T) {
	v := spendView(store.Spend{
		Children: []store.SpendChild{
			{Level: store.SpendCall, Key: "1", Call: &store.Call{Kind: "implement", Engine: "claude", CostSource: "reported",
				Checkpoint: "usage-limit", CheckpointSHA: "abc123", LinesAdded: 412, LinesDeleted: 38}},
			{Level: store.SpendCall, Key: "2", Call: &store.Call{Kind: "review", Engine: "claude", CostSource: "reported"}},
		},
		Totals: store.SpendTotals{Calls: 3, Checkpoints: 4, CostKnown: true},
		// pod-lost was saved without a call: a count, no calls.
		ByCheckpoint: []store.SpendBreakdown{
			{Key: "usage-limit", Totals: store.SpendTotals{Calls: 2, Checkpoints: 2, CostKnown: true}, Count: 2},
			{Key: "engine-error", Totals: store.SpendTotals{Calls: 1, Checkpoints: 1, CostKnown: true}, Count: 1},
			{Key: "pod-lost", Totals: store.SpendTotals{Checkpoints: 1, CostKnown: true}, Count: 1},
		},
	})
	if c := v.Children[0]; c.Checkpoint != "usage-limit" || c.CheckpointSHA != "abc123" || c.LinesAdded != 412 || c.LinesDeleted != 38 {
		t.Errorf("implement call = %+v", c.SpendCallView)
	}
	if c := v.Children[1]; c.Checkpoint != "" || c.CheckpointSHA != "" || c.LinesAdded != 0 || c.LinesDeleted != 0 {
		t.Errorf("review call = %+v, want no checkpoint or lines", c.SpendCallView)
	}
	if b := v.ByCheckpoint; len(b) != 3 || b[0].Key != "usage-limit" || b[0].Totals.Calls != 2 || b[0].Count != 2 || b[1].Key != "engine-error" ||
		b[2].Key != "pod-lost" || b[2].Count != 1 || b[2].Totals.Calls != 0 || b[2].Totals.Checkpoints != 1 {
		t.Errorf("by checkpoint = %+v", b)
	}
	if v.Totals.Checkpoints != 4 || v.Totals.Calls != 3 {
		t.Errorf("totals = %+v, want 4 checkpoints and 3 calls", v.Totals)
	}
}

// The view carries the scope's step lines and each call's nudges; without
// step data both read 0.
func TestSpendViewStepLines(t *testing.T) {
	v := spendView(store.Spend{
		Children: []store.SpendChild{
			{Level: store.SpendCall, Key: "1", Call: &store.Call{Kind: "implement", Engine: "claude", CostSource: "reported",
				LinesAdded: 380, LinesDeleted: 40, StepGateNudges: 2}},
			{Level: store.SpendCall, Key: "2", Call: &store.Call{Kind: "review", Engine: "claude", CostSource: "reported"}},
		},
		StepLines: store.StepLines{Count: 4, Median: 175, P90: 775, Max: 1000, Nudges: 2},
	})
	if want := (StepLinesView{Count: 4, Median: 175, P90: 775, Max: 1000, Nudges: 2}); v.StepLines != want {
		t.Errorf("step lines = %+v, want %+v", v.StepLines, want)
	}
	if c := v.Children[0]; c.StepGateNudges != 2 || c.LinesAdded != 380 {
		t.Errorf("implement call = %+v", c.SpendCallView)
	}
	if c := v.Children[1]; c.StepGateNudges != 0 {
		t.Errorf("review call = %+v, want no nudges", c.SpendCallView)
	}
	if v := spendView(store.Spend{}); v.StepLines != (StepLinesView{}) {
		t.Errorf("no step data: %+v, want zeros", v.StepLines)
	}
}
