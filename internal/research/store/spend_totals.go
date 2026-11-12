package store

import "encoding/json"

// SpendTotals sums calls. CostUSD sums the known costs, LegacyCostUSD (the
// part of CostUSD from runs without calls) included; UnknownCalls are the
// calls without a cost, which never add $0. CostUSD is unknown only when
// every call is unknown and there is no legacy cost. UnmeteredCalls are
// the calls with neither a cost nor tokens (cursor's ACP reports no
// usage), a part of UnknownCalls. OldBasisCalls are the calls recorded
// before the token basis was stored (token_basis 0): their tokens are not
// comparable with newer calls', see MixedBasis and OldBasis; runs without
// calls and checkpoint events are no calls and never count. Checkpoints
// are the calls that saved a checkpoint plus the checkpoints no call
// carries (checkpoint events: budget stop, lost pod, OOM kill); those add
// no call to Calls.
//
// It is also the UI API's JSON (web/src/generated/store.ts; tygo reads
// this file only): costUSD is "" when no cost is known, never "0.00".
type SpendTotals struct {
	CostUSD Money `json:"costUSD"`
	// costKnown is CostUSD.Known, written by MarshalJSON; Go code reads
	// CostUSD.Known, never this field.
	CostKnownJSON    bool  `json:"costKnown"`
	UnknownCalls     int64 `json:"unknownCalls"`
	UnmeteredCalls   int64 `json:"unmeteredCalls"`
	OldBasisCalls    int64 `json:"oldBasisCalls"`
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
	Calls            int64 `json:"calls"`
	SubagentCalls    int64 `json:"subagentCalls"`
	DurationMS       int64 `json:"durationMS"`
	// LegacyCostUSD is the part of CostUSD from runs before agent_calls,
	// unknown (left out of the JSON) when there is none.
	LegacyCostUSD Money `json:"legacyCostUSD,omitzero"`
	Checkpoints   int64 `json:"checkpoints"`
}

// MarshalJSON writes t with costKnown from CostUSD.Known.
func (t SpendTotals) MarshalJSON() ([]byte, error) {
	type plain SpendTotals
	p := plain(t)
	p.CostKnownJSON = t.CostUSD.Known
	return json.Marshal(p)
}

// Add is the sum of t and u: the cost is known when either's is, as for a
// query over both's calls.
func (t SpendTotals) Add(u SpendTotals) SpendTotals {
	return SpendTotals{
		CostUSD:          t.CostUSD.Add(u.CostUSD),
		UnknownCalls:     t.UnknownCalls + u.UnknownCalls,
		UnmeteredCalls:   t.UnmeteredCalls + u.UnmeteredCalls,
		OldBasisCalls:    t.OldBasisCalls + u.OldBasisCalls,
		InputTokens:      t.InputTokens + u.InputTokens,
		OutputTokens:     t.OutputTokens + u.OutputTokens,
		CacheReadTokens:  t.CacheReadTokens + u.CacheReadTokens,
		CacheWriteTokens: t.CacheWriteTokens + u.CacheWriteTokens,
		Calls:            t.Calls + u.Calls,
		SubagentCalls:    t.SubagentCalls + u.SubagentCalls,
		DurationMS:       t.DurationMS + u.DurationMS,
		LegacyCostUSD:    t.LegacyCostUSD.Add(u.LegacyCostUSD),
		Checkpoints:      t.Checkpoints + u.Checkpoints,
	}
}

// MixedBasis is true when some but not all calls are on the old token
// basis (OldBasisCalls).
func (t SpendTotals) MixedBasis() bool { return t.OldBasisCalls > 0 && t.OldBasisCalls < t.Calls }

// OldBasis is true when there are calls and all are on the old token basis.
func (t SpendTotals) OldBasis() bool { return t.Calls > 0 && t.OldBasisCalls == t.Calls }

// Tokens is the sum of all tokens: input, output and cache.
func (t SpendTotals) Tokens() int64 {
	return t.InputTokens + t.OutputTokens + t.CacheReadTokens + t.CacheWriteTokens
}
