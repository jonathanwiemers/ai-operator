package rules

import (
	"fmt"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
)

// The budget line "<prefix> budget <usd>" of a milestone is the operator's,
// like the owners and the rules: not part of the spec.

// Budget returns the budget in USD of the first line "<prefix> budget <usd>"
// of a milestone description ("5", "12.50", "$5"), formatted for storage,
// or "" without such a line. A value that is not a decimal above 0 is an
// error, and there is no budget then.
func Budget(description, prefix string) (string, error) {
	m := notify.KeywordLine(prefix, "budget").FindStringSubmatch(description)
	if m == nil {
		return "", nil
	}
	usd, err := aiv1.ParseBudget(m[1])
	if err != nil {
		return "", fmt.Errorf("%w: write `%s budget <usd>`, e.g. `%s budget 5`", err, prefix, prefix)
	}
	return usd, nil
}

// SetBudget sets the budget line of a description to usd: the first budget
// line is replaced (the one Budget reads), else one is appended after the
// last line.
func SetBudget(description, prefix, usd string) string {
	line := prefix + " budget " + usd
	re := notify.KeywordLine(prefix, "budget")
	if loc := re.FindStringIndex(description); loc != nil {
		old := description[loc[0]:loc[1]]
		end := ""
		switch {
		case strings.HasSuffix(old, "\r\n"):
			end = "\r\n"
		case strings.HasSuffix(old, "\n"):
			end = "\n"
		}
		return description[:loc[0]] + line + end + description[loc[1]:]
	}
	description = strings.TrimRight(description, " \t\r\n")
	if description == "" {
		return line
	}
	return description + "\n\n" + line
}
