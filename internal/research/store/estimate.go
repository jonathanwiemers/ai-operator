package store

import (
	"math"
	"slices"
)

// EstimateUnit is what an estimate counts: requirements or planned issues.
type EstimateUnit string

const (
	UnitRequirement EstimateUnit = "requirement"
	UnitIssue       EstimateUnit = "issue"
)

// EstimateResult is the cost of Count units from the spend per unit of
// Milestones finished milestones: low, typical and high are its first
// quartile, median and third quartile. Costs are unknown with
// NotEnoughHistory (fewer than 3 such milestones).
type EstimateResult struct {
	Unit             EstimateUnit `json:"unit"`
	Count            int          `json:"count"`
	LowUSD           Money        `json:"lowUSD"`
	TypicalUSD       Money        `json:"typicalUSD"`
	HighUSD          Money        `json:"highUSD"`
	LowTokens        int64        `json:"lowTokens"`
	TypicalTokens    int64        `json:"typicalTokens"`
	HighTokens       int64        `json:"highTokens"`
	Milestones       int          `json:"milestones"`
	NotEnoughHistory bool         `json:"notEnoughHistory"`
}

// Estimate is the cost of n units from hist's milestones with a known cost
// and that unit. Quartiles interpolate linearly between the sorted rates
// (position p·(len−1), as numpy's default and Excel's QUARTILE.INC).
func Estimate(hist []MilestoneSpend, unit EstimateUnit, n int) EstimateResult {
	var usd, tokens []float64
	for _, m := range hist {
		units := m.Issues
		if unit == UnitRequirement {
			units = m.Requirements
		}
		if units > 0 && m.CostUSD.Known {
			usd = append(usd, m.CostUSD.USD/float64(units))
			tokens = append(tokens, float64(m.Tokens)/float64(units))
		}
	}
	r := EstimateResult{Unit: unit, Count: n, Milestones: len(usd), NotEnoughHistory: len(usd) < 3}
	if r.NotEnoughHistory {
		return r
	}
	slices.Sort(usd)
	slices.Sort(tokens)
	at := func(sorted []float64, p float64) float64 {
		pos := p * float64(len(sorted)-1)
		lo, hi := sorted[int(math.Floor(pos))], sorted[int(math.Ceil(pos))]
		return (lo + (hi-lo)*(pos-math.Floor(pos))) * float64(n)
	}
	r.LowUSD, r.TypicalUSD, r.HighUSD = USD(at(usd, 0.25)), USD(at(usd, 0.5)), USD(at(usd, 0.75))
	tok := func(p float64) int64 { return int64(math.Round(at(tokens, p))) }
	r.LowTokens, r.TypicalTokens, r.HighTokens = tok(0.25), tok(0.5), tok(0.75)
	return r
}
