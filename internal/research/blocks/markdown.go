package blocks

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// fenceOpen is a fenced code block's opening line; a backtick fence's info
// string has no backticks.
var fenceOpen = regexp.MustCompile("^( {0,3})(`{3,}|~{3,})(.*)$")

// markdown checks a markdown block: outside fenced code blocks and code
// spans no raw HTML ('<' and a letter, '/', '!' or '?'), no images and only
// http(s) links. The checks err on the strict side of CommonMark: code
// spans count only within a line, and a fence ends at the first line that
// is indented less than its opening line (the end of a list item).
func (c *checker) markdown(path, s string) {
	if len(s) > MaxMarkdownBytes {
		c.fail(path, "%d bytes, at most %d", len(s), MaxMarkdownBytes)
		return
	}
	c.text(path, s, MaxMarkdownBytes, true, true)
	fence, indent := "", 0
	for n, line := range strings.Split(s, "\n") {
		if fence != "" {
			lineIndent := len(line) - len(strings.TrimLeft(line, " "))
			rest := strings.TrimRight(line[lineIndent:], " \t")
			if rest != "" && lineIndent < indent {
				fence = ""
			} else {
				if lineIndent <= indent+3 && len(rest) >= len(fence) && strings.Trim(rest, fence[:1]) == "" {
					fence = ""
				}
				continue
			}
		}
		if m := fenceOpen.FindStringSubmatch(line); m != nil && !(m[2][0] == '`' && strings.Contains(m[3], "`")) {
			fence, indent = m[2], len(m[1])
			continue
		}
		if err := checkLine(line); err != nil {
			c.fail(path, "line %d: %v", n+1, err)
			return
		}
	}
}

// checkLine checks one line outside fenced code blocks.
func checkLine(line string) error {
	for i := 0; i < len(line); i++ {
		next := ""
		if i+1 < len(line) {
			next = line[i+1 : i+2]
		}
		switch ch := line[i]; {
		case ch == '\\':
			i++ // an escaped character is literal
		case ch == '`':
			n := len(line[i:]) - len(strings.TrimLeft(line[i:], "`"))
			if end := closingRun(line[i+n:], n); end >= 0 {
				i += n + end + n - 1 // skip the code span
			} else {
				i += n - 1
			}
		case ch == '<' && next != "" && (isLetter(next[0]) || strings.Contains("/!?", next)):
			return fmt.Errorf("raw HTML or autolink %.20q is not allowed", line[i:])
		case ch == '!' && next == "[":
			return errors.New("images are not allowed")
		case ch == ']' && (next == "(" || next == ":"):
			// An inline link's or a link reference definition's destination.
			if dest := linkDest(line[i+2:]); checkURL(dest) != nil {
				return fmt.Errorf("link %.80q is not an absolute http or https URL", dest)
			}
		}
	}
	return nil
}

// closingRun returns the index of the first run of exactly n backticks in s,
// or -1.
func closingRun(s string, n int) int {
	for i := 0; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		run := len(s[i:]) - len(strings.TrimLeft(s[i:], "`"))
		if run == n {
			return i
		}
		i += run
	}
	return -1
}

// linkDest returns a link destination: up to whitespace or an unbalanced ')'.
func linkDest(s string) string {
	s = strings.TrimLeft(s, " \t")
	depth := 0
	for i, r := range s {
		switch {
		case r == ' ' || r == '\t':
			return s[:i]
		case r == '(':
			depth++
		case r == ')':
			if depth == 0 {
				return s[:i]
			}
			depth--
		}
	}
	return s
}

func isLetter(b byte) bool { return 'a' <= b|0x20 && b|0x20 <= 'z' }
