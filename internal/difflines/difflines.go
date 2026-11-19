// Package difflines counts the lines of a merge request's diff, split into
// four categories by the path of each file: generated, tests, docs and
// production. The list of categories is fixed here, in code, and applies to
// every repository (home and companions).
package difflines

import (
	"strconv"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/pathspec"
)

// The categories of a path.
const (
	Generated  = "generated"
	Tests      = "tests"
	Docs       = "docs"
	Production = "production"
)

// categories are tried in order; the first match wins. A path that matches
// none is Production.
var categories = []struct {
	name string
	m    *pathspec.Matcher
}{
	{Generated, mustCompile(
		"zz_generated*", "web/src/generated/**", "config/crd/**", "config/rbac/**",
		"charts/*/files/**", "*.pb.go", "go.sum", "package-lock.json", "pnpm-lock.yaml", "yarn.lock",
	)},
	{Tests, mustCompile("*_test.go", "**/test/**", "**/tests/**", "**/testdata/**", "*.test.*", "*.spec.ts")},
	{Docs, mustCompile("docs/**", "*.md")},
}

func mustCompile(patterns ...string) *pathspec.Matcher {
	m, err := pathspec.Compile(patterns)
	if err != nil {
		panic(err)
	}
	return m
}

// Category returns the category of a repository path: Generated, Tests, Docs
// or Production.
func Category(path string) string {
	for _, c := range categories {
		if c.m.Match(path) {
			return c.name
		}
	}
	return Production
}

// Count counts the added and deleted lines of diffs by category. A file is
// classified by its new path, or its old path when it was deleted. Binary
// files count 0. A diff without its content (collapsed, too large, or empty
// without being a pure rename) is not counted and sets Partial.
func Count(diffs []gitlab.Diff) aiv1.MergedLines {
	var out aiv1.MergedLines
	for _, d := range diffs {
		if d.Collapsed || d.TooLarge || (d.Diff == "" && !pureRename(d)) {
			out.Partial = true
			continue
		}
		p := d.NewPath
		if d.DeletedFile {
			p = d.OldPath
		}
		added, deleted := countPatch(d.Diff)
		c := bucket(&out, Category(p))
		c.Added += added
		c.Deleted += deleted
	}
	return out
}

// pureRename is a renamed file whose content did not change: GitLab
// returns it with an empty diff.
func pureRename(d gitlab.Diff) bool {
	return d.RenamedFile && !d.NewFile && !d.DeletedFile
}

func bucket(ml *aiv1.MergedLines, category string) *aiv1.LineCount {
	switch category {
	case Generated:
		return &ml.Generated
	case Tests:
		return &ml.Tests
	case Docs:
		return &ml.Docs
	}
	return &ml.Production
}

// countPatch counts the added and deleted lines of a unified diff's hunks.
// Only lines inside a hunk count, as long as its header says: so the
// ---/+++ file headers and "Binary files … differ" never count, while a
// deleted "-- comment" (a "--- comment" line) inside a hunk does. A hunk
// header without line counts counts up to the next hunk.
func countPatch(patch string) (added, deleted int64) {
	var inHunk, sized bool
	var oldLeft, newLeft int64
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			oldLeft, newLeft, sized = hunkSize(line)
			inHunk = true
			continue
		}
		if !inHunk {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			added++
			newLeft--
		case strings.HasPrefix(line, "-"):
			deleted++
			oldLeft--
		case strings.HasPrefix(line, `\`):
			// "\ No newline at end of file"
		default:
			oldLeft--
			newLeft--
		}
		if sized && oldLeft <= 0 && newLeft <= 0 {
			inHunk = false
		}
	}
	return added, deleted
}

// hunkSize parses "@@ -l,s +l,s @@": the old and new line counts (1 when
// omitted). ok is false when the header does not parse.
func hunkSize(header string) (oldN, newN int64, ok bool) {
	f := strings.Fields(header)
	if len(f) < 4 || f[3] != "@@" || !strings.HasPrefix(f[1], "-") || !strings.HasPrefix(f[2], "+") {
		return 0, 0, false
	}
	oldN, ok1 := rangeLen(f[1][1:])
	newN, ok2 := rangeLen(f[2][1:])
	return oldN, newN, ok1 && ok2
}

// rangeLen is the length of a hunk range "l,s" or "l".
func rangeLen(r string) (int64, bool) {
	start, size, found := strings.Cut(r, ",")
	if _, err := strconv.ParseInt(start, 10, 64); err != nil {
		return 0, false
	}
	if !found {
		return 1, true
	}
	n, err := strconv.ParseInt(size, 10, 64)
	return n, err == nil && n >= 0
}
