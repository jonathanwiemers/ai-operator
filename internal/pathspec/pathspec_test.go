package pathspec

import (
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		match   []string
		miss    []string
	}{
		// A plain name matches the base name, or a directory, at any depth.
		{"values.yaml", []string{"values.yaml", "a/values.yaml", "a/b/values.yaml", "values.yaml/x"}, []string{"values.yml", "a/values.yaml.bak", "xvalues.yaml"}},
		{"orbit", []string{"orbit", "clusters/orbit/x.yaml", "orbit/a/b"}, []string{"orbits", "clusters/orbit-x/y"}},
		// * and ? stay within one segment.
		{"*.yaml", []string{"a.yaml", "x/y/.yaml", "dir/b.yaml"}, []string{"a.yml", "a.yaml.bak"}},
		{"a?c", []string{"abc", "x/axc"}, []string{"ac", "abbc", "a/c"}},
		{"/apps/*", []string{"apps/x", "apps/x/y"}, []string{"apps", "other/apps/x"}},
		{"/apps/*.yaml", []string{"apps/a.yaml"}, []string{"apps/sub/a.yaml", "apps/a.yml"}},
		{"/a*b", []string{"ab", "axxb", "axxb/c"}, []string{"a/b", "x/ab"}},
		{"/a**b", []string{"ab", "axb"}, []string{"a/b", "ax/yb"}},
		// A trailing / is a directory and everything under it.
		{"clusters/my-cluster/applications/ai-operator/", []string{"clusters/my-cluster/applications/ai-operator/values.yaml", "clusters/my-cluster/applications/ai-operator/a/b.yaml"},
			[]string{"clusters/my-cluster/applications/ai-operator", "clusters/my-cluster/applications/ai-operator-x/a", "clusters/my-cluster/applications/orbit/a", "x/clusters/my-cluster/applications/ai-operator/a"}},
		{"docs/", []string{"docs/a.md", "docs/x/y.md", "a/docs/x"}, []string{"docs", "docsx/a"}},
		{"build/", []string{"build/x", "a/build/x", "a/b/build/c/d"}, []string{"build", "a/build"}},
		// A leading or inner / anchors to the root.
		{"/Makefile", []string{"Makefile"}, []string{"a/Makefile"}},
		{"a/b", []string{"a/b", "a/b/c"}, []string{"x/a/b", "a/bc"}},
		{"/a/b", []string{"a/b", "a/b/c"}, []string{"x/a/b"}},
		// ** matches any number of segments.
		{"**/values.yaml", []string{"values.yaml", "a/values.yaml", "a/b/c/values.yaml"}, []string{"a/values.yml"}},
		{"apps/**/values.yaml", []string{"apps/values.yaml", "apps/a/values.yaml", "apps/a/b/values.yaml"}, []string{"values.yaml", "x/apps/values.yaml"}},
		{"apps/**", []string{"apps/a", "apps/a/b"}, []string{"apps", "x/apps/a", "appsx/a"}},
		{"**", []string{"a", "a/b", ".gitlab-ci.yml"}, nil},
		{"/**", []string{"a", "a/b"}, nil},
		{"**/*", []string{"a", "a/b"}, nil},
		{"*", []string{"a", "a/b", ".hidden"}, nil},
		{"*/", []string{"a/b", "a/b/c"}, []string{"a"}},
	} {
		m, err := Compile([]string{tc.pattern})
		if err != nil {
			t.Errorf("%q: %v", tc.pattern, err)
			continue
		}
		for _, p := range tc.match {
			if !m.Match(p) {
				t.Errorf("%q does not match %q", tc.pattern, p)
			}
		}
		for _, p := range tc.miss {
			if m.Match(p) {
				t.Errorf("%q matches %q", tc.pattern, p)
			}
		}
	}
}

func TestMatchAnyPattern(t *testing.T) {
	m, err := Compile([]string{"/docs/", "*.md"})
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{"docs/a.txt": true, "a/README.md": true, "src/main.go": false} {
		if got := m.Match(p); got != want {
			t.Errorf("Match(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestMatchCaseSensitive(t *testing.T) {
	m, _ := Compile([]string{"/Docs/"})
	if m.Match("docs/a") || !m.Match("Docs/a") {
		t.Error("matching must be case-sensitive, like git paths")
	}
}

func TestMatchUncleanPaths(t *testing.T) {
	m, _ := Compile([]string{"**"})
	for _, p := range []string{"", "/a", "a//b", "./a", "a/../b", "..", "a/."} {
		if m.Match(p) {
			t.Errorf("unclean path %q matches", p)
		}
	}
	var none *Matcher
	if none.Match("a") {
		t.Error("nil matcher matches")
	}
}

func TestCompileRefuses(t *testing.T) {
	for p, want := range map[string]string{
		"":          "empty pattern",
		"/":         "empty pattern",
		"!a":        "negation",
		"!/docs/":   "negation",
		"#a":        "comment",
		" a":        "whitespace",
		"a ":        "whitespace",
		`a\b`:       "backslash",
		`\!a`:       "backslash",
		"[ab]":      "character classes",
		"//etc":     "absolute",
		"C:/x":      "absolute",
		"..":        `".."`,
		"../x":      `".."`,
		"a/../b":    `".."`,
		"a/..":      `".."`,
		"./a":       `"."`,
		"a//b":      "empty path segment",
		"a/b//":     "empty path segment",
		"docs//":    "empty path segment",
		"/docs/x//": "empty path segment",
	} {
		_, err := Compile([]string{"docs/", p})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Compile(%q) = %v, want an error with %q", p, err, want)
		}
	}
	if _, err := Compile(nil); err == nil {
		t.Error("an empty list compiles")
	}
}
