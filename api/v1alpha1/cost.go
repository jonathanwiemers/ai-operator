package v1alpha1

import (
	"fmt"
	"math"
	"regexp"
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

var budgetRE = regexp.MustCompile(`^\$?([0-9]{1,9}(?:\.[0-9]+)?)$`)

// ParseBudget reads a budget in USD as an owner writes it ("5", "12.50",
// "$5"), formatted for storage. A value that is not a decimal above 0 is an
// error.
func ParseBudget(s string) (string, error) {
	v := budgetRE.FindStringSubmatch(s)
	if v == nil {
		return "", fmt.Errorf("%q is no budget in USD, e.g. 5 or 12.50", s)
	}
	usd, err := strconv.ParseFloat(v[1], 64)
	if err != nil || usd <= 0 {
		return "", fmt.Errorf("%q is no budget: it must be above 0", s)
	}
	return FormatCost(usd), nil
}

// CursorAutoModel is the model of cursor calls that leave the choice to
// cursor (the fast profile, the bugbot and security reviewers).
const CursorAutoModel = "auto"

// CursorAutoLabel names CursorAutoModel in spend reports when its calls
// report nothing: cursor's ACP tells neither the model it picked
// (currentModelId "default[]") nor the usage, so they are unmetered
// (docs/usage.md).
const CursorAutoLabel = "cursor auto (unmetered)"

// ModelLabel names a model in spend reports: CursorAutoModel as
// CursorAutoLabel when unmetered, as "cursor auto" otherwise, any other
// model by its name.
func ModelLabel(model string, unmetered bool) string {
	switch {
	case model != CursorAutoModel:
		return model
	case unmetered:
		return CursorAutoLabel
	}
	return "cursor auto"
}
