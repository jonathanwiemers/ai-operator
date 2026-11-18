// Package projectglob matches GitLab project paths ("group/sub/project")
// against path globs: the AgentProjectSet's include and exclude lists and
// the cluster's cross-repo flags (--cross-repo-allowed-projects,
// --cross-repo-never-projects) share it.
//
// The syntax is small and exactly defined:
//
//   - A pattern is a list of "/"-separated segments, anchored at the root:
//     it matches the whole path, never a part of it.
//   - `*` matches any run of characters within one segment and may stand
//     inside a segment (`dev*`). It never matches "/".
//   - A segment that is exactly `**` matches one or more segments when it
//     is the last segment (`a/**` is everything inside a, not a itself) and
//     zero or more otherwise (`a/**/b` matches `a/b`).
//   - Matching is case-insensitive, as GitLab paths are: both sides are
//     lowercased and trimmed of leading and trailing "/".
//
// Refused: empty patterns and segments, `**` inside a segment (`a**b`),
// `?`, `[`, `]`, `!`, `\`, whitespace, and `.` and `..` segments.
//
// It is not internal/pathspec, the gitignore-style matcher for file paths in
// companion repositories: that one has unanchored and directory-only
// patterns and `?`, which are wrong for project paths.
package projectglob

import (
	"fmt"
	"path"
	"strings"
	"unicode"
)

// Matcher matches project paths against compiled patterns. A nil or empty
// Matcher matches nothing.
type Matcher struct {
	patterns [][]string
}

// normalize is the form patterns and paths are compared in, as
// v1alpha1.NormalizeProject (kept local: v1alpha1 imports this package).
func normalize(p string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(p), "/"))
}

// Validate returns an error naming the pattern when it is not a valid
// project glob.
func Validate(pattern string) error {
	_, err := compile(pattern)
	return err
}

// Compile compiles the patterns. The error names the first invalid one.
func Compile(patterns []string) (*Matcher, error) {
	m := &Matcher{}
	for _, p := range patterns {
		segs, err := compile(p)
		if err != nil {
			return nil, err
		}
		m.patterns = append(m.patterns, segs)
	}
	return m, nil
}

// Match reports whether the path matches one of the patterns.
func (m *Matcher) Match(p string) bool {
	if m == nil {
		return false
	}
	p = normalize(p)
	if p == "" {
		return false
	}
	segs := strings.Split(p, "/")
	for _, pat := range m.patterns {
		if match(pat, segs) {
			return true
		}
	}
	return false
}

// MatchAny reports whether the path matches one of the patterns, each
// matched on its own: an invalid pattern does not stop the others and is
// compared exactly, in normalized form, with the path.
func MatchAny(patterns []string, p string) bool {
	np := normalize(p)
	if np == "" {
		return false
	}
	segs := strings.Split(np, "/")
	for _, pattern := range patterns {
		pat, err := compile(pattern)
		if err != nil {
			if normalize(pattern) == np {
				return true
			}
			continue
		}
		if match(pat, segs) {
			return true
		}
	}
	return false
}

// compile checks a pattern and returns its normalized segments.
func compile(pattern string) ([]string, error) {
	fail := func(why string) ([]string, error) {
		return nil, fmt.Errorf("pattern %q: %s", pattern, why)
	}
	if strings.IndexFunc(pattern, unicode.IsSpace) >= 0 {
		return fail("whitespace")
	}
	if i := strings.IndexAny(pattern, `?[]!\`); i >= 0 {
		return fail(fmt.Sprintf("%q is not supported", pattern[i]))
	}
	n := normalize(pattern)
	if n == "" {
		return fail("empty pattern")
	}
	segs := strings.Split(n, "/")
	for _, s := range segs {
		switch {
		case s == "":
			return fail("empty segment")
		case s == "." || s == "..":
			return fail(fmt.Sprintf("%q segment", s))
		case s != "**" && strings.Contains(s, "**"):
			return fail(`"**" inside a segment`)
		}
	}
	return segs, nil
}

// match reports whether the pattern's segments match the path's.
func match(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		if len(pat) == 1 {
			return len(segs) > 0
		}
		for i := 0; i <= len(segs); i++ {
			if match(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	// The segment holds only literals and "*" (compile refuses the rest of
	// path.Match's syntax), so path.Match cannot fail.
	ok, _ := path.Match(pat[0], segs[0])
	return ok && match(pat[1:], segs[1:])
}
