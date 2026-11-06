// Package conventional checks and repairs Conventional Commits messages and
// titles. The runner (commits, MR title) and the operator (squash message)
// share it, so the JSON-schema pattern and the checks cannot drift apart.
package conventional

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxSubject is the longest subject, after "type(scope): ".
const MaxSubject = 72

const fallbackPrefix = "chore(agent): "

// emptySubject stands in when a message has no first line.
const emptySubject = "update"

// Pattern is the regular expression a message must match. It is valid as a
// JSON-schema pattern and as a Go regexp.
func Pattern(types []string) string {
	q := make([]string, len(types))
	for i, t := range types {
		q[i] = regexp.QuoteMeta(t)
	}
	return `^(` + strings.Join(q, "|") + `)(\([a-z0-9._/-]+\))?!?: .{1,72}(\n\n[\s\S]*)?$`
}

// Valid reports whether the trimmed msg follows the pattern.
func Valid(msg string, types []string) bool {
	re, err := regexp.Compile(Pattern(types))
	if err != nil {
		return false
	}
	return re.MatchString(strings.TrimSpace(msg))
}

// Fallback turns msg into "chore(agent): <first line>": the first line is
// trimmed, its first letter lowercased and the subject cut to MaxSubject
// characters. The rest of the message (the body) is kept.
func Fallback(msg string) string {
	msg = strings.TrimSpace(msg)
	first, rest, _ := strings.Cut(msg, "\n")
	first = strings.TrimSpace(first)
	if r, n := utf8.DecodeRuneInString(first); n > 0 {
		first = string(unicode.ToLower(r)) + first[n:]
	}
	if utf8.RuneCountInString(first) > MaxSubject {
		first = strings.TrimSpace(string([]rune(first)[:MaxSubject]))
	}
	if first == "" {
		first = emptySubject
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return fallbackPrefix + first
	}
	return fallbackPrefix + first + "\n\n" + rest
}

// Title returns title when it is valid, else its Fallback.
func Title(title string, types []string) string {
	if Valid(title, types) {
		return strings.TrimSpace(title)
	}
	return Fallback(title)
}
