package textutil

import (
	"regexp"
	"strings"
)

// untrustedHTML matches a code span or fenced block, kept as text, or HTML:
// a comment (unclosed to the end) or a tag. Autolinks (<https://…>) are no
// tags.
var untrustedHTML = regexp.MustCompile("(?s)```.*?```|`[^`\n]*`|<!--.*?(?:-->|$)|</?[A-Za-z][A-Za-z0-9-]*(?:[\\s/][^<>]*)?>")

// UntrustedMarkdown is agent text for status and the Desk: markdown without
// HTML (code spans and blocks stay as text), at most limit bytes with an
// ellipsis beyond. The runner stores a retrospective with it, and the
// operator's backfill a retrospective read from GitLab, so both read alike.
func UntrustedMarkdown(text string, limit int) string {
	s := untrustedHTML.ReplaceAllStringFunc(text, func(m string) string {
		if strings.HasPrefix(m, "`") {
			return m
		}
		return ""
	})
	if s = strings.TrimSpace(s); len(s) > limit {
		s = Truncate(s, limit-len("…"))
	}
	return s
}
