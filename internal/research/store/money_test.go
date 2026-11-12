package store_test

import (
	"encoding/json"
	"testing"

	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

func TestMoneyJSON(t *testing.T) {
	for _, c := range []struct {
		m    store.Money
		want string
	}{
		{store.Money{}, `""`},
		// An unknown amount hides what it carries.
		{store.Money{USD: 1.5}, `""`},
		{store.USD(0), `"0.00"`},
		{store.USD(1.234), `"1.23"`},
		{store.USD(1.235), `"1.24"`},
		{store.USD(12345.6), `"12345.60"`},
	} {
		b, err := json.Marshal(c.m)
		if err != nil || string(b) != c.want {
			t.Errorf("Marshal(%+v) = %s, %v, want %s", c.m, b, err, c.want)
		}
	}
	for _, c := range []struct {
		in   string
		want store.Money
	}{
		{`""`, store.Money{}},
		{`"0.00"`, store.USD(0)},
		{`"1.23"`, store.USD(1.23)},
	} {
		var m store.Money
		if err := json.Unmarshal([]byte(c.in), &m); err != nil || m != c.want {
			t.Errorf("Unmarshal(%s) = %+v, %v, want %+v", c.in, m, err, c.want)
		}
	}
	for _, in := range []string{`1.5`, `"x"`, `null`} {
		var m store.Money
		if err := json.Unmarshal([]byte(in), &m); err == nil && in != `null` {
			t.Errorf("Unmarshal(%s) = %+v, want an error", in, m)
		}
	}
}

// TestSpendTotalsJSON compares SpendTotals with the JSON of the UI's former
// SpendTotalsView, byte for byte.
func TestSpendTotalsJSON(t *testing.T) {
	for _, c := range []struct {
		name string
		t    store.SpendTotals
		want string
	}{
		{"known", store.SpendTotals{CostUSD: store.USD(1.234), UnknownCalls: 1, UnmeteredCalls: 1, OldBasisCalls: 2,
			InputTokens: 10, OutputTokens: 20, CacheReadTokens: 30, CacheWriteTokens: 40, Calls: 3, SubagentCalls: 1,
			DurationMS: 500, Checkpoints: 2},
			`{"costUSD":"1.23","costKnown":true,"unknownCalls":1,"unmeteredCalls":1,"oldBasisCalls":2,"inputTokens":10,"outputTokens":20,"cacheReadTokens":30,"cacheWriteTokens":40,"calls":3,"subagentCalls":1,"durationMS":500,"checkpoints":2}`},
		{"unknown", store.SpendTotals{UnknownCalls: 2, UnmeteredCalls: 1, InputTokens: 5, Calls: 2},
			`{"costUSD":"","costKnown":false,"unknownCalls":2,"unmeteredCalls":1,"oldBasisCalls":0,"inputTokens":5,"outputTokens":0,"cacheReadTokens":0,"cacheWriteTokens":0,"calls":2,"subagentCalls":0,"durationMS":0,"checkpoints":0}`},
		{"known zero", store.SpendTotals{CostUSD: store.USD(0)},
			`{"costUSD":"0.00","costKnown":true,"unknownCalls":0,"unmeteredCalls":0,"oldBasisCalls":0,"inputTokens":0,"outputTokens":0,"cacheReadTokens":0,"cacheWriteTokens":0,"calls":0,"subagentCalls":0,"durationMS":0,"checkpoints":0}`},
		{"legacy", store.SpendTotals{CostUSD: store.USD(2.5), LegacyCostUSD: store.USD(2), Calls: 1, InputTokens: 7},
			`{"costUSD":"2.50","costKnown":true,"unknownCalls":0,"unmeteredCalls":0,"oldBasisCalls":0,"inputTokens":7,"outputTokens":0,"cacheReadTokens":0,"cacheWriteTokens":0,"calls":1,"subagentCalls":0,"durationMS":0,"legacyCostUSD":"2.00","checkpoints":0}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			// As a value and behind a pointer (Cost.Spend).
			for _, v := range []any{c.t, &c.t} {
				b, err := json.Marshal(v)
				if err != nil || string(b) != c.want {
					t.Errorf("Marshal = %s, %v\nwant %s", b, err, c.want)
				}
			}
			var back store.SpendTotals
			if err := json.Unmarshal([]byte(c.want), &back); err != nil || back.CostUSD.String() != c.t.CostUSD.String() ||
				back.CostUSD.Known != c.t.CostUSD.Known || back.LegacyCostUSD != c.t.LegacyCostUSD || back.Calls != c.t.Calls {
				t.Errorf("Unmarshal = %+v, %v, want %+v", back, err, c.t)
			}
		})
	}
}

func TestSpendTotalsAdd(t *testing.T) {
	known := store.SpendTotals{CostUSD: store.USD(1.5), LegacyCostUSD: store.USD(0.5), UnknownCalls: 1, UnmeteredCalls: 1,
		OldBasisCalls: 1, InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4, Calls: 3,
		SubagentCalls: 1, DurationMS: 100, Checkpoints: 1}
	unknown := store.SpendTotals{UnknownCalls: 2, InputTokens: 10, Calls: 2, DurationMS: 50}
	want := store.SpendTotals{CostUSD: store.USD(1.5), LegacyCostUSD: store.USD(0.5), UnknownCalls: 3, UnmeteredCalls: 1,
		OldBasisCalls: 1, InputTokens: 11, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4, Calls: 5,
		SubagentCalls: 1, DurationMS: 150, Checkpoints: 1}
	// Unknown costs add nothing, in either order.
	if got := known.Add(unknown); got != want {
		t.Errorf("known + unknown = %+v\nwant %+v", got, want)
	}
	if got := unknown.Add(known); got != want {
		t.Errorf("unknown + known = %+v\nwant %+v", got, want)
	}
	// Two unknowns stay unknown, never $0.
	if got := unknown.Add(unknown); got.CostUSD.Known || got.LegacyCostUSD.Known || got.Calls != 4 {
		t.Errorf("unknown + unknown = %+v", got)
	}
	if got := known.Add(known); got.CostUSD != store.USD(3) || got.LegacyCostUSD != store.USD(1) {
		t.Errorf("known + known = %+v", got)
	}
}
