package rules

import (
	"fmt"
	"regexp"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// The budget line "<prefix> budget <usd>" of a milestone is the operator's,
// like the owners and the rules: not part of the spec.

func budgetLine(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(prefix) + `[ \t]+budget(?:[ \t]+(.*?))?[ \t]*\r?(?:\n|\z)`)
}

// Budget returns the budget in USD of the first line "<prefix> budget <usd>"
// of a milestone description ("5", "12.50", "$5"), formatted for storage,
// or "" without such a line. A value that is not a decimal above 0 is an
// error, and there is no budget then.
func Budget(description, prefix string) (string, error) {
	m := budgetLine(prefix).FindStringSubmatch(description)
	if m == nil {
		return "", nil
	}
	usd, err := aiv1.ParseBudget(m[1])
	if err != nil {
		return "", fmt.Errorf("%w: write `%s budget <usd>`, e.g. `%s budget 5`", err, prefix, prefix)
	}
	return usd, nil
}
