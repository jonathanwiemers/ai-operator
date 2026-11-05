// Package pathspec matches repository paths against a gitignore-style
// allow-list. It is a security gate (the paths a companion change may touch,
// see docs/specs/cross-repo-changes.spec.md), so it supports a small,
// exactly defined subset of the gitignore syntax and refuses the rest
// instead of guessing:
//
//   - `*` matches any run of characters within one path segment, `?` one
//     character; neither matches `/`.
//   - A segment `**` matches any number of segments: `**/x` (x at any
//     depth), `a/**/x` (zero or more directories between), `a/**`
//     (everything inside a, not a itself). Asterisks within a segment
//     (`a**b`) are plain `*`, as in gitignore.
//   - A trailing `/` matches only a directory: everything under it.
//   - A pattern with a leading or an inner `/` is anchored to the root of
//     the repository. A pattern without one matches the base name, or a
//     directory of that name, at any depth.
//   - A pattern that matches a directory matches everything under it.
//
// Refused: `!` negation, absolute paths (more than one leading `/` or a
// drive letter), `.` and `..` segments, empty patterns and segments,
// backslashes, character classes (`[`, `]`), a leading `#` and leading or
// trailing whitespace.
package pathspec

import (
	"fmt"
	"strings"
)

// Matcher matches paths against a list of compiled patterns.
type Matcher struct {
	patterns []pattern
}

type pattern struct {
	// segs are the pattern's segments; an unanchored pattern starts with
	// "**".
	segs []string
	// dirOnly is set by a trailing "/": the pattern matches directories
	// only, so a path matches through one of its parents.
	dirOnly bool
}

// Compile compiles an allow-list. It fails on an empty list and names the
// first pattern it refuses.
func Compile(patterns []string) (*Matcher, error) {
	if len(patterns) == 0 {
		return nil, fmt.Errorf("no patterns")
	}
	m := &Matcher{}
	for _, p := range patterns {
		c, err := compile(p)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", p, err)
		}
		m.patterns = append(m.patterns, c)
	}
	return m, nil
}

func compile(p string) (pattern, error) {
	switch {
	case p == "":
		return pattern{}, fmt.Errorf("empty pattern")
	case strings.TrimSpace(p) != p:
		return pattern{}, fmt.Errorf("leading or trailing whitespace")
	case strings.HasPrefix(p, "!"):
		return pattern{}, fmt.Errorf("negation (!) is not supported")
	case strings.HasPrefix(p, "#"):
		return pattern{}, fmt.Errorf("a leading # is a comment in gitignore")
	case strings.Contains(p, `\`):
		return pattern{}, fmt.Errorf("backslashes are not supported")
	case strings.ContainsAny(p, "[]"):
		return pattern{}, fmt.Errorf("character classes ([ ]) are not supported")
	case strings.HasPrefix(p, "//") || hasDriveLetter(p):
		return pattern{}, fmt.Errorf("absolute paths are not allowed")
	}
	body := p
	anchored := strings.HasPrefix(body, "/")
	body = strings.TrimPrefix(body, "/")
	dirOnly := strings.HasSuffix(body, "/")
	body = strings.TrimSuffix(body, "/")
	if body == "" {
		return pattern{}, fmt.Errorf("empty pattern")
	}
	// An inner "/" anchors too; a trailing one does not.
	anchored = anchored || strings.Contains(body, "/")
	segs := strings.Split(body, "/")
	for _, s := range segs {
		switch s {
		case "":
			return pattern{}, fmt.Errorf("empty path segment")
		case ".", "..":
			return pattern{}, fmt.Errorf("%q segments are not allowed", s)
		}
	}
	if !anchored {
		segs = append([]string{"**"}, segs...)
	}
	return pattern{segs: segs, dirOnly: dirOnly}, nil
}

// hasDriveLetter reports whether p starts like a Windows path ("C:").
func hasDriveLetter(p string) bool {
	return len(p) >= 2 && p[1] == ':' && (p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z')
}

// Match reports whether a repository path (relative, "/"-separated, as git
// lists it) is allowed: a pattern matches the path itself or one of its
// parent directories. Paths that are not clean (empty, absolute, with "."
// or ".." or empty segments) never match.
func (m *Matcher) Match(path string) bool {
	if m == nil || path == "" || strings.HasPrefix(path, "/") {
		return false
	}
	segs := strings.Split(strings.TrimSuffix(path, "/"), "/")
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return false
		}
	}
	for _, p := range m.patterns {
		// The path itself (unless the pattern wants a directory), or a
		// parent directory.
		n := len(segs)
		if p.dirOnly {
			n--
		}
		for k := 1; k <= n; k++ {
			if matchSegs(p.segs, segs[:k]) {
				return true
			}
		}
	}
	return false
}

// matchSegs reports whether the pattern segments match all path segments.
// A "**" matches zero or more segments, a trailing one at least one.
func matchSegs(ps, xs []string) bool {
	if len(ps) == 0 {
		return len(xs) == 0
	}
	if ps[0] == "**" {
		min := 0
		if len(ps) == 1 {
			min = 1
		}
		for i := min; i <= len(xs); i++ {
			if matchSegs(ps[1:], xs[i:]) {
				return true
			}
		}
		return false
	}
	return len(xs) > 0 && matchSeg(ps[0], xs[0]) && matchSegs(ps[1:], xs[1:])
}

// matchSeg matches one segment against a pattern of literals, "*" and "?".
func matchSeg(p, s string) bool {
	pr, sr := []rune(p), []rune(s)
	// Iterative wildcard matching with backtracking to the last "*".
	pi, si, star, mark := 0, 0, -1, 0
	for si < len(sr) {
		switch {
		case pi < len(pr) && pr[pi] == '*':
			star, mark = pi, si
			pi++
		case pi < len(pr) && (pr[pi] == '?' || pr[pi] == sr[si]):
			pi++
			si++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(pr) && pr[pi] == '*' {
		pi++
	}
	return pi == len(pr)
}
