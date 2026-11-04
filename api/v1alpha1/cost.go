package v1alpha1

import (
	"math"
	"strconv"
)

// Cost is kept as a decimal string (status.run.costUSD): stored with 4
// decimals, shown with 2.

// CostOf parses a decimal cost string; "" or an invalid string is 0.
func CostOf(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// AddCost adds usd to the cost string s, formatted for storage.
func AddCost(s string, usd float64) string {
	return FormatCost(CostOf(s) + usd)
}

// FormatCost formats usd for storage (4 decimals).
func FormatCost(usd float64) string {
	return strconv.FormatFloat(usd, 'f', 4, 64)
}

// ShowCost formats usd for display (2 decimals).
func ShowCost(usd float64) string {
	return strconv.FormatFloat(usd, 'f', 2, 64)
}

// BudgetLeft is budget - spent - extra. An unset (empty or invalid) budget
// is unlimited: +Inf.
func BudgetLeft(budget, spent string, extra float64) float64 {
	if v, err := strconv.ParseFloat(budget, 64); err != nil || math.IsNaN(v) {
		return math.Inf(1)
	}
	return CostOf(budget) - CostOf(spent) - extra
}
