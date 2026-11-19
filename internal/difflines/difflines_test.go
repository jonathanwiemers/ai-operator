package difflines

import (
	"fmt"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func TestCategory(t *testing.T) {
	for path, want := range map[string]string{
		// generated
		"api/v1alpha1/zz_generated.deepcopy.go": Generated,
		"zz_generated_foo.go":                   Generated,
		"web/src/generated/api.ts":              Generated,
		"web/src/generated/a.md":                Generated,
		"config/crd/bases/x.yaml":               Generated,
		"config/rbac/role.yaml":                 Generated,
		"charts/ai-operator/files/x.yaml":       Generated,
		"charts/other/files/deep/y.yaml":        Generated,
		"proto/x.pb.go":                         Generated,
		"go.sum":                                Generated,
		"tools/go.sum":                          Generated,
		"web/package-lock.json":                 Generated,
		"pnpm-lock.yaml":                        Generated,
		"yarn.lock":                             Generated,
		"web/src/generated/api_test.go":         Generated,
		"config/crd/bases/README.md":            Generated,
		// tests
		"internal/foo_test.go":    Tests,
		"docs/x_test.go":          Tests,
		"test/e2e/main.go":        Tests,
		"web/test/a.ts":           Tests,
		"tests/x.py":              Tests,
		"internal/x/testdata/a.y": Tests,
		"web/src/a.test.ts":       Tests,
		"web/e2e/b.spec.ts":       Tests,
		"test/README.md":          Tests,
		// docs
		"docs/foo.md":        Docs,
		"docs/img/a.png":     Docs,
		"README.md":          Docs,
		"charts/x/README.md": Docs,
		// production
		"cmd/main.go":                    Production,
		"internal/foo.go":                Production,
		"charts/ai-operator/values.yaml": Production,
		"config/manager/manager.yaml":    Production,
		"web/src/a.spec.js":              Production,
		"go.mod":                         Production,
		"internal/testing/x.go":          Production,
	} {
		if got := Category(path); got != want {
			t.Errorf("Category(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestCountAcceptance(t *testing.T) {
	got := Count([]gitlab.Diff{
		{OldPath: "internal/foo.go", NewPath: "internal/foo.go", Diff: hunk(40, 5)},
		{NewPath: "internal/foo_test.go", NewFile: true, Diff: hunk(60, 0)},
		{NewPath: "docs/foo.md", NewFile: true, Diff: hunk(10, 0)},
	})
	want := aiv1.MergedLines{
		Production: aiv1.LineCount{Added: 40, Deleted: 5},
		Tests:      aiv1.LineCount{Added: 60},
		Docs:       aiv1.LineCount{Added: 10},
	}
	if got != want {
		t.Errorf("Count = %+v, want %+v", got, want)
	}
}

func TestCountDeletedUsesOldPath(t *testing.T) {
	got := Count([]gitlab.Diff{{OldPath: "internal/old_test.go", NewPath: "internal/old_test.go", DeletedFile: true, Diff: hunk(0, 7)}})
	if want := (aiv1.LineCount{Deleted: 7}); got.Tests != want || got.Production != (aiv1.LineCount{}) {
		t.Errorf("Count = %+v, want tests %+v", got, want)
	}
	// A deleted file whose new path GitLab reports differently.
	got = Count([]gitlab.Diff{{OldPath: "docs/a.md", NewPath: "cmd/a.go", DeletedFile: true, Diff: hunk(0, 2)}})
	if got.Docs.Deleted != 2 || got.Production.Deleted != 0 {
		t.Errorf("Count = %+v, want docs -2", got)
	}
}

func TestCountPartial(t *testing.T) {
	for name, d := range map[string]gitlab.Diff{
		"collapsed": {NewPath: "a.go", Collapsed: true},
		"too large": {NewPath: "a.go", TooLarge: true},
		"empty":     {NewPath: "a.go"},
		// Flags win over content GitLab might still send.
		"collapsed with content": {NewPath: "a.go", Collapsed: true, Diff: hunk(3, 0)},
	} {
		got := Count([]gitlab.Diff{d, {NewPath: "b.go", Diff: hunk(2, 1)}})
		want := aiv1.MergedLines{Production: aiv1.LineCount{Added: 2, Deleted: 1}, Partial: true}
		if got != want {
			t.Errorf("%s: Count = %+v, want %+v", name, got, want)
		}
	}
	if got := Count([]gitlab.Diff{{OldPath: "a.go", NewPath: "b.go", RenamedFile: true}}); got != (aiv1.MergedLines{}) {
		t.Errorf("pure rename: Count = %+v, want zero and not partial", got)
	}
}

func TestCountBinary(t *testing.T) {
	got := Count([]gitlab.Diff{
		{NewPath: "web/logo.png", NewFile: true, Diff: "Binary files /dev/null and b/web/logo.png differ\n"},
		{NewPath: "a.bin", Diff: "--- a/a.bin\n+++ b/a.bin\nBinary files a/a.bin and b/a.bin differ\n"},
	})
	if got != (aiv1.MergedLines{}) {
		t.Errorf("Count = %+v, want zero", got)
	}
}

func TestCountHeaders(t *testing.T) {
	patch := "--- a/x.sql\n+++ b/x.sql\n@@ -1,3 +1,3 @@\n ctx\n--- old comment\n+++ new comment\n ctx\n\\ No newline at end of file\n"
	got := Count([]gitlab.Diff{{OldPath: "x.sql", NewPath: "x.sql", Diff: patch}})
	if want := (aiv1.LineCount{Added: 1, Deleted: 1}); got.Production != want {
		t.Errorf("Count = %+v, want %+v", got.Production, want)
	}
	// Two hunks, the second without counts; trailing lines after a sized
	// hunk do not count.
	patch = "@@ -1 +1 @@\n-a\n+b\n@@ -10 +10,2 @@\n x\n+y\n"
	if got := Count([]gitlab.Diff{{NewPath: "x.go", Diff: patch}}); got.Production != (aiv1.LineCount{Added: 2, Deleted: 1}) {
		t.Errorf("Count = %+v, want +2/-1", got.Production)
	}
	patch = "@@ -1,1 +1,1 @@\n-a\n+b\n--- a/next\n+++ b/next\n"
	if got := Count([]gitlab.Diff{{NewPath: "x.go", Diff: patch}}); got.Production != (aiv1.LineCount{Added: 1, Deleted: 1}) {
		t.Errorf("Count = %+v, want +1/-1", got.Production)
	}
}

// hunk is one hunk that deletes del lines and adds add lines.
func hunk(add, del int) string {
	s := fmt.Sprintf("@@ -1,%d +1,%d @@\n", del, add)
	for range del {
		s += "-old\n"
	}
	for range add {
		s += "+new\n"
	}
	return s
}
