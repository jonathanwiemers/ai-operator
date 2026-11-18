package projectglob

import (
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		path    string
		want    bool
	}{
		{"jo.wiemers/**", "jo.wiemers", false},
		{"jo.wiemers/**", "jo.wiemers/a", true},
		{"jo.wiemers/**", "jo.wiemers/a/b", true},
		{"jo.wiemers/**", "JO.Wiemers/A", true},
		{"jo.wiemers/**", "/jo.wiemers/a/", true},
		{"jo.wiemers/**", "other/x", false},
		{"jo.wiemers/**", "other/jo.wiemers/x", false},
		{"JO.Wiemers/**", "jo.wiemers/infra/cluster", true},
		{"/jo.wiemers/**/", "jo.wiemers/x", true},
		{"jo.wiemers/*", "jo.wiemers/a", true},
		{"jo.wiemers/*", "jo.wiemers/a/b", false},
		{"jo.wiemers/*", "jo.wiemers", false},
		{"jo.wiemers/dev*/x", "jo.wiemers/devops/x", true},
		{"jo.wiemers/dev*/x", "jo.wiemers/dev/x", true},
		{"jo.wiemers/dev*/x", "jo.wiemers/ops/x", false},
		{"jo.wiemers/dev*/x", "jo.wiemers/devops/y/x", false},
		{"jo.wiemers/*ops*", "jo.wiemers/devops-tools", true},
		{"a/**/b", "a/b", true},
		{"a/**/b", "a/x/y/b", true},
		{"a/**/b", "a/x/y/c", false},
		{"a/**/b", "b", false},
		{"**/b", "b", true},
		{"**/b", "x/y/b", true},
		{"**", "a", true},
		{"**", "a/b/c", true},
		{"a/**/**", "a", false},
		{"a/**/**", "a/b", true},
		{"jo.wiemers/secret*", "jo.wiemers/secrets", true},
		{"jo.wiemers/secret*", "jo.wiemers/secret", true},
		{"jo.wiemers/secret*", "jo.wiemers/my-secrets", false},
		{"jo.wiemers/infra/cluster", "jo.wiemers/infra/cluster", true},
		{"jo.wiemers/infra/cluster", "jo.wiemers/infra/cluster2", false},
		{"jo.wiemers/infra", "jo.wiemers/infra/cluster", false},
		{"jo.wiemers/**", "", false},
		{"jo.wiemers/**", "/", false},
	} {
		m, err := Compile([]string{tc.pattern})
		if err != nil {
			t.Fatalf("Compile(%q): %v", tc.pattern, err)
		}
		if got := m.Match(tc.path); got != tc.want {
			t.Errorf("%q matches %q = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
		if got := MatchAny([]string{tc.pattern}, tc.path); got != tc.want {
			t.Errorf("MatchAny(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestMatchSeveralPatterns(t *testing.T) {
	m, err := Compile([]string{"a/x", "b/**"})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{"a/x": true, "b/c/d": true, "a/y": false, "b": false} {
		if got := m.Match(path); got != want {
			t.Errorf("Match(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestEmptyMatcherMatchesNothing(t *testing.T) {
	var nilMatcher *Matcher
	empty, err := Compile(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []*Matcher{nilMatcher, empty} {
		if m.Match("a/b") {
			t.Errorf("%v matches a/b", m)
		}
	}
	if MatchAny(nil, "a/b") {
		t.Error("MatchAny(nil) matches a/b")
	}
}

func TestRefused(t *testing.T) {
	for _, tc := range []struct{ pattern, want string }{
		{"", "empty pattern"},
		{"/", "empty pattern"},
		{"a//b", "empty segment"},
		{"a**b", `"**" inside a segment`},
		{"a/**x", `"**" inside a segment`},
		{"a/b?", `'?' is not supported`},
		{"a/[bc]", `'[' is not supported`},
		{"a/b]", `']' is not supported`},
		{"!a/b", `'!' is not supported`},
		{`a\b`, `'\\' is not supported`},
		{"a/./b", `"." segment`},
		{"a/../b", `".." segment`},
		{"..", `".." segment`},
		{"a/b c", "whitespace"},
		{" a/b", "whitespace"},
		{"a/\tb", "whitespace"},
	} {
		err := Validate(tc.pattern)
		if err == nil {
			t.Errorf("Validate(%q) = nil, want an error", tc.pattern)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), `"`) {
			t.Errorf("Validate(%q) = %q, want it to contain %q", tc.pattern, err, tc.want)
		}
		if !strings.Contains(err.Error(), "pattern ") {
			t.Errorf("Validate(%q) = %q, want it to name the pattern", tc.pattern, err)
		}
		if _, err := Compile([]string{"ok/**", tc.pattern}); err == nil {
			t.Errorf("Compile with %q = nil error", tc.pattern)
		}
	}
	for _, ok := range []string{"a", "a/b", "jo.wiemers/**", "a/**/b", "dev*", "*", "**", "my-group/sub_group/x.y", "/a/b/"} {
		if err := Validate(ok); err != nil {
			t.Errorf("Validate(%q) = %v", ok, err)
		}
	}
}

func TestMatchAnyExactFallback(t *testing.T) {
	for _, tc := range []struct {
		patterns []string
		path     string
		want     bool
	}{
		// Invalid patterns are compared exactly, in normalized form.
		{[]string{" /EXAMPLE/Secrets/ "}, "example/secrets", true},
		{[]string{"a/b?"}, "a/b?", true},
		{[]string{"a/b?"}, "a/bc", false},
		{[]string{"a**b"}, "a**b", true},
		{[]string{"a**b"}, "axb", false},
		// An invalid pattern does not stop the valid ones.
		{[]string{"a/[x", "b/*"}, "b/c", true},
		// Empty entries match nothing, not even an empty path.
		{[]string{"", " "}, "", false},
		{[]string{""}, "a", false},
	} {
		if got := MatchAny(tc.patterns, tc.path); got != tc.want {
			t.Errorf("MatchAny(%q, %q) = %v, want %v", tc.patterns, tc.path, got, tc.want)
		}
	}
}
