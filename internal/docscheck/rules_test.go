package docscheck

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// tree is the repository the table tests resolve links against.
var tree = fstest.MapFS{
	"README.md":                      {Data: []byte("# ai-operator\n\n## Quick start\n")},
	"docs/README.md":                 {Data: []byte("# Documentation\n")},
	"docs/usage.md":                  {Data: []byte("# Usage\n\n## Releases\n\n```\n## In a fence\n```\n<a id=\"explicit\"></a>\n")},
	"docs/assets/logo.svg":           {Data: []byte("<svg/>")},
	"docs/guides/autopilot.md":       {Data: []byte("# Autopilot\n\n## Pauses\n")},
	"internal/controller/pilot.go":   {Data: []byte("package controller\n")},
	"charts/ai-operator/README.md":   {Data: []byte("# Chart\n")},
	"docs/specs/docs-revamp.spec.md": {Data: []byte("# Docs revamp\n\n## Inventory\n")},
}

func strs(fs []finding) []string {
	out := []string{}
	for _, f := range fs {
		out = append(out, f.String())
	}
	return out
}

func lines(n int) string { return strings.Repeat("line\n", n) }

func TestMarkdownRules(t *testing.T) {
	for _, tc := range []struct {
		name, path, content string
		want                []string
	}{
		{"link ok", "docs/a.md", "[u](usage.md) [r](usage.md#releases) [d](guides/) [img](assets/logo.svg)\n", nil},
		{"broken link", "docs/a.md", "intro\n\nsee [x](missing.md)\n",
			[]string{"docs/a.md:3: link missing.md: docs/missing.md does not exist"}},
		{"missing anchor", "docs/a.md", "x\n[x](usage.md#no-such-heading)\n",
			[]string{"docs/a.md:2: link usage.md#no-such-heading: no heading or anchor #no-such-heading in docs/usage.md"}},
		{"heading in a fence is no anchor", "docs/a.md", "[x](usage.md#in-a-fence)\n",
			[]string{"docs/a.md:1: link usage.md#in-a-fence: no heading or anchor #in-a-fence in docs/usage.md"}},
		{"explicit anchor", "docs/a.md", "[x](usage.md#explicit)\n", nil},
		{"same page anchor", "docs/a.md", "# Title\n\n[t](#title) [n](#nope)\n",
			[]string{"docs/a.md:3: link #nope: no heading or anchor #nope in docs/a.md"}},
		{"html href", "docs/a.md", "<a href=\"#x\">x</a>\n<img src=\"assets/none.png\">\n",
			[]string{
				"docs/a.md:1: link #x: no heading or anchor #x in docs/a.md",
				"docs/a.md:2: link assets/none.png: docs/assets/none.png does not exist",
			}},
		{"html href to explicit anchor", "docs/a.md", "<a id=\"x\"></a>\n<a href=\"#x\">x</a>\n", nil},
		{"reference definition", "docs/a.md", "[x][y]\n\n[y]: gone.md\n",
			[]string{"docs/a.md:3: link gone.md: docs/gone.md does not exist"}},
		{"link in a code fence is ignored", "docs/a.md", "```md\n[x](missing.md)\n```\n~~~\n[x](missing.md)\n~~~\n", nil},
		{"link in inline code is ignored", "docs/a.md", "write `[x](missing.md)` like this\n", nil},
		{"external links are skipped", "docs/a.md", "[a](https://x.example/y.md) [b](mailto:a@b.c) [c](//cdn/x)\n", nil},
		{"root relative and up", "charts/ai-operator/README.md", "[d](../../docs/usage.md#releases) [r](/README.md#quick-start) [o](../../../x.md)\n",
			[]string{"charts/ai-operator/README.md:1: link ../../../x.md: it points outside the repository"}},
		{"anchor into a non-Markdown file is not checked", "docs/a.md", "[p](../internal/controller/pilot.go#L1)\n", nil},
		{"400 lines pass", "docs/guides/long.md", lines(400), nil},
		{"401 lines fail", "docs/guides/long.md", lines(401),
			[]string{"docs/guides/long.md:401: page has 401 lines, the cap is 400: split it into several pages"}},
		{"401 lines without a final newline fail", "docs/reference/long.md", lines(400) + "end",
			[]string{"docs/reference/long.md:401: page has 401 lines, the cap is 400: split it into several pages"}},
		{"README under docs is capped", "docs/specs/README.md", lines(401),
			[]string{"docs/specs/README.md:401: page has 401 lines, the cap is 400: split it into several pages"}},
		{"contributing is not capped", "docs/contributing/design.md", lines(401), nil},
		{"Go file in a guide", "docs/guides/pilot.md", "# Pilot\n\nThe pilot lives in internal/controller/pilot.go.\n",
			[]string{`docs/guides/pilot.md:3: source ref "internal/controller/pilot.go" in a user page: name no source files or directories, internals go to docs/contributing/`}},
		{"Go file in contributing", "docs/contributing/pilot.md", "The pilot lives in internal/controller/pilot.go.\n", nil},
		{"source file with a line in a code block", "docs/concepts/x.md", "```\nsee pilot.go:42\n```\n",
			[]string{`docs/concepts/x.md:2: source ref "pilot.go:42" in a user page: name no source files or directories, internals go to docs/contributing/`}},
		{"source directory and other extensions", "docs/reference/x.md", "`web/src` and App.vue, run.sh, 0001_init.sql\n",
			[]string{
				`docs/reference/x.md:1: source ref "App.vue" in a user page: name no source files or directories, internals go to docs/contributing/`,
				`docs/reference/x.md:1: source ref "run.sh" in a user page: name no source files or directories, internals go to docs/contributing/`,
				`docs/reference/x.md:1: source ref "0001_init.sql" in a user page: name no source files or directories, internals go to docs/contributing/`,
				`docs/reference/x.md:1: source ref "web/src" in a user page: name no source files or directories, internals go to docs/contributing/`,
			}},
		{"commands, fields, values, URLs and page links pass", "docs/guides/ok.md",
			"Run `/agent release`, set `spec.gitlab.url` and `ui.enabled`, label `agent::ready`.\n" +
				"`GET /api/v1/projects` on https://gitlab.com/x/y/-/blob/main/internal/a.go, Node.js and Vue.js.\n" +
				"[Usage](../usage.md) ![logo](../assets/logo.svg) [spec](../specs/docs-revamp.spec.md#inventory)\n" +
				"`kubectl get agenttasks -n ai-operator-system` and `values.yaml`\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strs(newChecker(tree).markdown(tc.path, tc.content))
			want := tc.want
			if want == nil {
				want = []string{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
			}
		})
	}
}

func TestSlugs(t *testing.T) {
	for _, tc := range []struct{ heading, want string }{
		{"## `spec.gitlab`", "specgitlab"},
		{"## Releases v1.2.3", "releases-v123"},
		{"## 🚀 Launch", "-launch"},
		{"### Step 1: Write the spec", "step-1-write-the-spec"},
		{"## [Usage](usage.md) and `x`", "usage-and-x"},
		{"## Q & A", "q--a"},
		{"## snake_case and dash-es", "snake_case-and-dash-es"},
		{"## Closing hashes ##", "closing-hashes"},
		{"#### `emptyPlan`", "emptyplan"},
		{"## <a id=\"old\"></a>New name", "new-name"},
		{"## Ünïcödé Straße", "ünïcödé-straße"},
	} {
		got := anchorsOf(tc.heading + "\n")
		if !got[tc.want] || len(got) != 1+strings.Count(tc.heading, "<a ") {
			t.Errorf("%q: got anchors %v, want %q", tc.heading, got, tc.want)
		}
	}
}

func TestDuplicateSlugs(t *testing.T) {
	got := anchorsOf("# A\n## A\n```\n## A\n```\n### A\n## A 1\n")
	want := map[string]bool{"a": true, "a-1": true, "a-2": true, "a-1-1": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExplicitAndQuotedAnchors(t *testing.T) {
	got := anchorsOf("> #### D42-3: How tags are stored\n> > ## Nested\n" +
		"<h2 id=\"setup\">Setup</h2>\n<h1 align=\"center\">ai-operator</h1>\n" +
		"<a name=\"old-name\"></a>\n<a data-id=\"no\" href=\"#x\">x</a>\n")
	want := map[string]bool{"d42-3-how-tags-are-stored": true, "nested": true, "setup": true, "old-name": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNonMarkdownRefs(t *testing.T) {
	for _, tc := range []struct {
		name, path, content string
		want                []string
	}{
		{"resolves", "api/v1alpha1/types.go", "// See docs/usage.md#releases and docs/README.md.\n", nil},
		{"missing page", "api/v1alpha1/types.go", "package v1alpha1\n\n// See docs/x.md#y.\n",
			[]string{"api/v1alpha1/types.go:3: docs ref docs/x.md#y: docs/x.md does not exist"}},
		{"missing anchor", "Makefile", "# docs/usage.md#gone\n",
			[]string{"Makefile:1: docs ref docs/usage.md#gone: no heading or anchor #gone in docs/usage.md"}},
		{"missing page without anchor", "web/src/App.vue", "<!-- docs/ui.md \"Checks\" -->\n",
			[]string{"web/src/App.vue:1: docs ref docs/ui.md: docs/ui.md does not exist"}},
		{"URL to the repo", ".gitlab-ci.yml", "# https://gitlab.com/x/y/-/blob/main/docs/usage.md#releases\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strs(newChecker(tree).nonMarkdown(tc.path, tc.content))
			want := tc.want
			if want == nil {
				want = []string{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
			}
		})
	}
}

func TestEntryPoints(t *testing.T) {
	if got := newChecker(tree).entryPointFindings(entryPoints); len(got) != 0 {
		t.Errorf("entry points of the test tree: %v", strs(got))
	}
	broken := fstest.MapFS{"README.md": {Data: []byte("# ai-operator\n\n## Install\n")}}
	got := strs(newChecker(broken).entryPointFindings(entryPoints))
	want := []string{
		"docs/README.md:1: entry point docs/README.md: docs/README.md does not exist (linked from outside the repository, keep it working)",
		"README.md:1: entry point README.md#quick-start: no heading or anchor #quick-start in README.md (linked from outside the repository, keep it working)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestCheckTreeSkips(t *testing.T) {
	fsys := fstest.MapFS{
		"README.md":                         {Data: []byte("# ai-operator\n\n## Quick start\n")},
		"docs/README.md":                    {Data: []byte("# Docs\n[x](gone.md)\n")},
		"internal/a/a_test.go":              {Data: []byte("// docs/old.md\n")},
		"internal/a/testdata/x.go":          {Data: []byte("// docs/old.md\n")},
		"web/node_modules/p/x.js":           {Data: []byte("// docs/old.md\n")},
		"web/test-results/x.txt":            {Data: []byte("docs/old.md\n")},
		"docs/assets/x.png":                 {Data: []byte("docs/old.md")},
		"docs/assets/x.svg":                 {Data: []byte("<!-- docs/old.md -->")},
		".cache/go-build/ab/x-d":            {Data: []byte("docs/old.md\n")},
		".github/workflows/ci.yml":          {Data: []byte("# docs/gone.md\n")},
		"internal/a/blob.bin":               {Data: []byte("docs/old.md\x00")},
		"hack/sandbox/README.md":            {Data: []byte("[x](gone.md)\n")},
		"web/README.md":                     {Data: []byte("[x](gone.md)\n")},
		"skills/milestone-spec/SKILL.md":    {Data: []byte("x\n[x](gone.md)\n")},
		"internal/a/a.go":                   {Data: []byte("package a\n\n// docs/old.md\n")},
		"charts/ai-operator/values.yaml":    {Data: []byte("# docs/README.md\n")},
		"charts/ai-operator/templates/x.md": {Data: []byte("[x](gone.md)\n")},
	}
	got, err := checkTree(fsys)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		".github/workflows/ci.yml:1: docs ref docs/gone.md: docs/gone.md does not exist",
		"docs/README.md:2: link gone.md: docs/gone.md does not exist",
		"internal/a/a.go:3: docs ref docs/old.md: docs/old.md does not exist",
		"skills/milestone-spec/SKILL.md:2: link gone.md: skills/milestone-spec/gone.md does not exist",
	}
	if !reflect.DeepEqual(strs(got), want) {
		t.Errorf("got\n  %s\nwant\n  %s", strings.Join(strs(got), "\n  "), strings.Join(want, "\n  "))
	}
}
