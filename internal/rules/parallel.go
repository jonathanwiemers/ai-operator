package rules

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MaxParallelLine is the highest value a milestone's parallel line may set.
const MaxParallelLine = 10

// The parallel line "<prefix> parallel <n>" of a milestone is the operator's,
// like the budget: not part of the spec.

func parallelLine(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(prefix) + `[ \t]+parallel(?:[ \t]+(.*?))?[ \t]*\r?(?:\n|\z)`)
}

// Parallel returns the parallel-issue limit of the first line
// "<prefix> parallel <n>" of a milestone description, or 0 without such a
// line. n must be an integer from 1 to MaxParallelLine; anything else is an
// error, and there is no override then.
func Parallel(description, prefix string) (int32, error) {
	m := parallelLine(prefix).FindStringSubmatch(description)
	if m == nil {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(m[1]))
	if err != nil || n < 1 || n > MaxParallelLine {
		return 0, fmt.Errorf("write `%s parallel <n>`, e.g. `%s parallel 2` (1 to %d)", prefix, prefix, MaxParallelLine)
	}
	return int32(n), nil
}

// SetParallel sets the parallel line of a description to n: the first
// parallel line is replaced (the one Parallel reads), else one is appended
// after the last line.
func SetParallel(description, prefix string, n int32) string {
	line := prefix + " parallel " + strconv.FormatInt(int64(n), 10)
	re := parallelLine(prefix)
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
