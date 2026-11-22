package v1alpha1

// SpendSummary is a scope's spend with its biggest cost drivers, each list
// the top 3 by USD.
type SpendSummary struct {
	// CostUSD sums the known costs (4 decimals).
	// +optional
	CostUSD string `json:"costUSD,omitempty"`
	// CostKnown is false when no call reported or estimated a cost.
	// +optional
	CostKnown bool `json:"costKnown,omitempty"`
	// +optional
	Tokens SpendTokens `json:"tokens,omitempty"`
	// SubagentCalls is how often the calls delegated to a subagent.
	// +optional
	SubagentCalls int64 `json:"subagentCalls,omitempty"`
	// UnmeteredCalls are the calls with neither a cost nor tokens (cursor
	// reports no usage), a part of the total that costKnown does not cover.
	// +optional
	UnmeteredCalls int64 `json:"unmeteredCalls,omitempty"`
	// Calls is the number of agent calls (runs from before agent calls and
	// checkpoint events are none).
	// +optional
	Calls int64 `json:"calls,omitempty"`
	// OldBasisCalls are the calls recorded before the token basis was
	// stored (token_basis 0): their tokens are not comparable with newer
	// calls.
	// +optional
	OldBasisCalls int64 `json:"oldBasisCalls,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=3
	TopIssues []SpendDriver `json:"topIssues,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=3
	TopKinds []SpendDriver `json:"topKinds,omitempty"`
	// TopModels counts subagent models as their own entries.
	// +optional
	// +kubebuilder:validation:MaxItems=3
	TopModels []SpendDriver `json:"topModels,omitempty"`
}

// SpendTokens are tokens by type.
type SpendTokens struct {
	// +optional
	Input int64 `json:"input,omitempty"`
	// +optional
	Output int64 `json:"output,omitempty"`
	// +optional
	CacheRead int64 `json:"cacheRead,omitempty"`
	// +optional
	CacheWrite int64 `json:"cacheWrite,omitempty"`
}

// Total is the sum of all tokens.
func (t SpendTokens) Total() int64 { return t.Input + t.Output + t.CacheRead + t.CacheWrite }

// SpendDriver is one issue, kind or model of a SpendSummary: Key is the
// issue IID, the kind or the model, Label how to name it.
type SpendDriver struct {
	Key string `json:"key"`
	// +optional
	Label string `json:"label,omitempty"`
	// CostUSD is empty when none of its calls has a known cost.
	// +optional
	CostUSD string `json:"costUSD,omitempty"`
	// +optional
	Tokens int64 `json:"tokens,omitempty"`
	// Unmetered is set when all its calls reported neither cost nor tokens.
	// +optional
	Unmetered bool `json:"unmetered,omitempty"`
}
