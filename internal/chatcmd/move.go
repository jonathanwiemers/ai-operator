package chatcmd

import (
	"fmt"
	"strconv"
	"strings"
)

// Move is a change of the project's milestone run order for the milestone
// the command is sent to: exactly one of To ("top", "bottom"), Before and
// After (another milestone's iid) is set.
type Move struct {
	To     string
	Before int64
	After  int64
}

// String is the move in the tracking issue's words: "top", "bottom",
// "before %6" or "after %6".
func (m Move) String() string {
	switch {
	case m.Before > 0:
		return fmt.Sprintf("before %%%d", m.Before)
	case m.After > 0:
		return fmt.Sprintf("after %%%d", m.After)
	default:
		return m.To
	}
}

// moveUsage is the error of a malformed move, in either form.
const moveUsage = "move needs exactly one of top, bottom, before %N or after %N"

// ParseMove reads the tracking issue's words after `move`: `top`, `bottom`,
// `before %N` or `after %N`, case-insensitive, the `%` optional. Empty words
// (the comment parser's padding) are ignored.
func ParseMove(args []string) (Move, error) {
	var words []string
	for _, w := range args {
		if w = strings.TrimSpace(w); w != "" {
			words = append(words, strings.ToLower(w))
		}
	}
	switch {
	case len(words) == 1 && (words[0] == "top" || words[0] == "bottom"):
		return Move{To: words[0]}, nil
	case len(words) == 2 && (words[0] == "before" || words[0] == "after"):
		n, err := strconv.ParseInt(strings.TrimPrefix(words[1], "%"), 10, 64)
		if err != nil || n <= 0 {
			return Move{}, fmt.Errorf("%s: %q is not a milestone %%N", moveUsage, words[1])
		}
		if words[0] == "before" {
			return Move{Before: n}, nil
		}
		return Move{After: n}, nil
	}
	return Move{}, fmt.Errorf("%s", moveUsage)
}

// Move returns the move of a move command.
func (c Command) Move() (Move, bool) {
	if c.Cmd != "move" {
		return Move{}, false
	}
	return Move{To: c.To, Before: c.Before, After: c.After}, true
}

// parseMove checks a move command: exactly one of to ("top", "bottom"),
// before and after.
func (c *Command) parseMove() error {
	set := 0
	for _, ok := range []bool{c.To != "", c.Before != 0, c.After != 0} {
		if ok {
			set++
		}
	}
	switch {
	case set != 1:
		return fmt.Errorf("%s", moveUsage)
	case c.To != "" && c.To != "top" && c.To != "bottom":
		return fmt.Errorf("move to %q is not top or bottom", c.To)
	case c.Before < 0 || c.After < 0:
		return fmt.Errorf("move before or after needs a milestone > 0")
	case c.noArgs() != nil:
		return fmt.Errorf("move takes only to, before or after")
	}
	return nil
}
