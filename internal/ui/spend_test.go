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
		c.SubagentTurns == nil || v.ByKind == nil || v.ByReviewRound == nil || v.BySession == nil || v.BySubagents == nil {
		t.Errorf("child = %+v, want unknown costs and empty lists", c)
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
	impl.SubagentCalls, impl.ContextSeries, impl.SubagentTurns = 1, []int64{1000, 2500, 4000}, []int32{1}
	fix := call("c2", 42, "shop-42", 2, 2, "fix", "gpt-5", nil)
	fix.Engine = "cursor"
	other := call("c3", 43, "shop-43", 1, 1, "implement", "claude-opus-5-5", usd(0.25))
	if _, err := owner.InsertCalls(t.Context(), []store.Call{impl, fix, other}); err != nil {
		t.Fatal(err)
	}
	env := spendEnv(t, reader)

	var ms SpendView
	if code := env.get(t, "/api/v1/spend?namespace=team-a&project=shop&milestone=3", &ms); code != http.StatusOK {
		t.Fatalf("milestone: status %d", code)
	}
	if ms.Totals.CostUSD != "1.75" || ms.Totals.Calls != 3 || ms.Totals.UnknownCalls != 1 {
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
	if len(sub) != 1 || sub[0].Role != "implement" || sub[0].Totals.Calls != 1 || sub[0].SubagentTokens != 7 || ms.ByReviewRound == nil {
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
		!slices.Equal(c.ContextSeries, []int64{1000, 2500, 4000}) || !slices.Equal(c.SubagentTurns, []int32{1}) {
		t.Errorf("first call = %+v", c)
	}
	if f := run.Children[1]; f.Totals.CostUSD != "" || f.Totals.CostKnown || f.CostSource != "unknown" {
		t.Errorf("cursor call = %+v, want an unknown cost", f)
	}
}
