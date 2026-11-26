package chatcmd

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// MaxTake is the most issues one take puts into the loose issues queue.
const MaxTake = 20

// takeUsage is the error of a malformed take, in either form.
var takeUsage = fmt.Sprintf("take needs 1 to %d issues, e.g. #350 #421", MaxTake)

// Queue reports whether the command is a verb of the loose issues queue
// (take, drop) for its Queue task.
func (c Command) Queue() bool {
	return c.Cmd == "take" || c.Cmd == "drop"
}

// ParseTake reads the tracking issue's words after `take`: 1 to MaxTake
// issues `#N`, the `#` optional, separated by spaces or commas. Empty words
// (the comment parser's padding) are ignored. The issues are deduplicated in
// the given order.
func ParseTake(args []string) ([]int64, error) {
	var iids []int64
	for _, w := range args {
		for f := range strings.FieldsFuncSeq(w, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
			n, err := strconv.ParseInt(strings.TrimPrefix(f, "#"), 10, 64)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("%s: %q is not an issue #N", takeUsage, f)
			}
			iids = append(iids, n)
		}
	}
	return checkTake(iids)
}

// checkTake deduplicates iids in their order and checks there are 1 to
// MaxTake, each above 0.
func checkTake(iids []int64) ([]int64, error) {
	var out []int64
	for _, n := range iids {
		if n <= 0 {
			return nil, fmt.Errorf("%s: %d is not an issue > 0", takeUsage, n)
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	if len(out) == 0 || len(out) > MaxTake {
		return nil, fmt.Errorf("%s", takeUsage)
	}
	return out, nil
}

// parseTake checks a take command: issues only.
func (c *Command) parseTake() error {
	if c.noArgs() != nil {
		return fmt.Errorf("take takes only issues")
	}
	iids, err := checkTake(c.Issues)
	if err != nil {
		return err
	}
	c.Issues = iids
	return nil
}
