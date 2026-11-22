// Package textutil holds small text helpers shared by the operator, the
// runner, the history differ and the webhook receiver.
package textutil

import "unicode/utf8"

// Truncate returns s unchanged when it has at most n bytes. Otherwise it cuts
// s to at most n bytes at a rune boundary and appends "…". It never splits a
// UTF-8 character. A caller with a character limit passes the limit minus one:
// the ellipsis is one character.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return Cut(s, max(n, 0)) + "…"
}

// Cut is Truncate without the ellipsis: s unchanged when it has at most n
// bytes, else cut to at most n bytes at a rune boundary. n must not be
// negative.
func Cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
