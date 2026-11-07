package rules

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// The line "<prefix> followup-of <iid> <numbers>" of a milestone names the
// follow-ups of milestone <iid>'s retrospective that it took over. Like the
// owners line it is the operator's, not part of the spec.

// maxFollowUps bounds the follow-ups of a retrospective (the runner's).
const maxFollowUps = 9

func followUpLine(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(prefix) + `[ \t]+followup-of(?:[ \t]+(.*?))?[ \t]*\r?(?:\n|\z)`)
}

// FollowUpOf reads the first line "<prefix> followup-of <iid> <numbers>" of
// a milestone description ("12 1 3", "%12 1,3"): the milestone's IID and the
// follow-ups' numbers, space- or comma-separated, each 1..9, deduplicated
// and sorted. Nil without such a line. A malformed line is an error, and
// there is no value then.
func FollowUpOf(description, prefix string) (*aiv1.FollowUpOf, error) {
	m := followUpLine(prefix).FindStringSubmatch(description)
	if m == nil {
		return nil, nil
	}
	bad := func(why string) error {
		return fmt.Errorf("%s: write `%s followup-of <milestone iid> <numbers>`, e.g. `%s followup-of 12 1 3`", why, prefix, prefix)
	}
	fields := strings.FieldsFunc(m[1], func(r rune) bool { return r == ' ' || r == '\t' || r == ',' })
	if len(fields) < 2 {
		return nil, bad("a milestone and at least one follow-up are needed")
	}
	iid, err := strconv.ParseInt(strings.TrimPrefix(fields[0], "%"), 10, 64)
	if err != nil || iid < 1 {
		return nil, bad(fmt.Sprintf("%q is no milestone IID", fields[0]))
	}
	var numbers []int
	for _, f := range fields[1:] {
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 || n > maxFollowUps {
			return nil, bad(fmt.Sprintf("%q is no follow-up number (1 to %d)", f, maxFollowUps))
		}
		if !slices.Contains(numbers, n) {
			numbers = append(numbers, n)
		}
	}
	slices.Sort(numbers)
	return &aiv1.FollowUpOf{MilestoneIID: iid, Numbers: numbers}, nil
}

// FollowUpOfLine renders the line FollowUpOf reads, e.g.
// "/agent followup-of 12 1 3".
func FollowUpOfLine(prefix string, iid int64, numbers []int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s followup-of %d", prefix, iid)
	for _, n := range numbers {
		fmt.Fprintf(&b, " %d", n)
	}
	return b.String()
}
