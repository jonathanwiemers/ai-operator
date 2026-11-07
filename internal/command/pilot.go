package command

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Pilot reads a comment addressed to the milestone's pilot: it starts with
// prefix ("/pilot") or with a mention of the bot ("@" + bot; "" turns the
// mention off). It returns the message without the address. An address
// anywhere else (mid-text, in a quote, in code) does not count.
func Pilot(body, prefix, bot string) (string, bool) {
	// Leading blank lines go; an indented first line is a code block.
	for {
		line, rest, ok := strings.Cut(body, "\n")
		if !ok || strings.TrimSpace(line) != "" {
			break
		}
		body = rest
	}
	if strings.HasPrefix(body, "    ") || strings.HasPrefix(body, "\t") {
		return "", false
	}
	body = strings.TrimSpace(body)
	var rest string
	switch {
	case prefix != "" && strings.HasPrefix(body, prefix):
		rest = body[len(prefix):]
	case bot != "" && len(body) > len(bot) && body[0] == '@' && strings.EqualFold(body[1:len(bot)+1], bot):
		rest = body[len(bot)+1:]
		// "@bot." ends the mention, "@bot.x" names someone else.
		if strings.HasPrefix(rest, ".") && len(rest) > 1 && nameChar(rest[1]) {
			return "", false
		}
	default:
		return "", false
	}
	if rest != "" && (nameChar(rest[0]) || rest[0] == '/') {
		return "", false // "/pilots", "@bot2"
	}
	return strings.TrimSpace(strings.TrimLeft(rest, ":,.")), true
}

func nameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

var (
	answerRE = regexp.MustCompile(`(?i)^(yes|no)(?:\s+P(\d+))?\s*[.!]?$`)
	tellRE   = regexp.MustCompile(`(?is)^tell\s+#(\d+)\s*:\s*(\S.*)$`)
)

// PilotAnswer reads an answer to a proposal: "yes" or "no", with "P<n>"
// (id) or without (0). The whole text must be the answer, so a quoted
// "yes" or one in a sentence is none.
func PilotAnswer(s string) (yes bool, id int64, ok bool) {
	m := answerRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return false, 0, false
	}
	id, _ = strconv.ParseInt(m[2], 10, 64)
	return strings.EqualFold(m[1], "yes"), id, true
}

// PilotTell reads "tell #N: <text>", an owner's words for the
// implementer of issue N.
func PilotTell(s string) (iid int64, text string, ok bool) {
	m := tellRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, "", false
	}
	iid, _ = strconv.ParseInt(m[1], 10, 64)
	return iid, strings.TrimSpace(m[2]), true
}

var retroCreateRE = regexp.MustCompile(`(?i)^create\s+(all|\d{1,4}(?:(?:\s*,\s*|\s+)\d{1,4})*)\s*[.!]?$`)

// RetroCreate reads an owner's "create 1 2", "create 1, 3" or "create all"
// in the thread of a retrospective: the numbers of the follow-ups to open,
// in order and once each, or nil for all. The whole comment must be it.
func RetroCreate(body string) ([]int, bool) {
	m := retroCreateRE.FindStringSubmatch(strings.TrimSpace(body))
	if m == nil {
		return nil, false
	}
	if strings.EqualFold(m[1], "all") {
		return nil, true
	}
	var nums []int
	for _, f := range strings.FieldsFunc(m[1], func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		if n, _ := strconv.Atoi(f); !slices.Contains(nums, n) {
			nums = append(nums, n)
		}
	}
	return nums, true
}

var retroNoneRE = regexp.MustCompile(`(?i)^create\s+none\s*[.!]?$`)

// RetroNone reads an owner's "create none" in the thread of a
// retrospective: the remaining follow-ups are dismissed. The whole comment
// must be it.
func RetroNone(body string) bool {
	return retroNoneRE.MatchString(strings.TrimSpace(body))
}
