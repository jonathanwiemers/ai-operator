package interdiff

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func TestInterdiffIgnoresPositionsAndCountsChangedLines(t *testing.T) {
	reviewed := []gitlab.Diff{
		{OldPath: "app.go", NewPath: "app.go", Diff: "@@ -1,3 +1,3 @@\n ctx\n-old\n+new\n+more\n"},
		{OldPath: "same.go", NewPath: "same.go", Diff: "@@ -10 +10 @@\n-a\n+b\n"},
	}
	rebased := []gitlab.Diff{
		// Moved by the rebase: other hunk positions and context, same lines.
		{OldPath: "same.go", NewPath: "same.go", Diff: "@@ -20 +20 @@\n other ctx\n-a\n+b\n"},
		{OldPath: "app.go", NewPath: "app.go", Diff: "@@ -5,3 +5,3 @@\n-old\n+new\n+adapted\n"},
	}
	d, ok := ComputeInterdiff(reviewed, rebased)
	if !ok || len(d.Files) != 1 || d.Lines != 2 {
		t.Fatalf("one file with one line replaced: %+v %v", d, ok)
	}
	diff, more := d.Render(200)
	want := "--- reviewed: app.go\n+++ rebased: app.go\n@@ -1,3 +1,3 @@\n -old\n +new\n-+more\n++adapted\n"
	if diff != want || more != 0 {
		t.Fatalf("render:\n%s\nwant:\n%s", diff, want)
	}
}

func TestInterdiffMovedAddedAndDroppedFiles(t *testing.T) {
	migration := "@@ -0,0 +1,2 @@\n+CREATE TABLE x ();\n+CREATE INDEX y;\n"
	reviewed := []gitlab.Diff{
		{OldPath: "migrations/0010_x.sql", NewPath: "migrations/0010_x.sql", NewFile: true, Diff: migration},
		{OldPath: "gone.go", NewPath: "gone.go", Diff: "@@ -1 +1 @@\n-a\n+b\n"},
	}
	rebased := []gitlab.Diff{
		{OldPath: "migrations/0011_x.sql", NewPath: "migrations/0011_x.sql", NewFile: true, Diff: migration},
		{OldPath: "new.go", NewPath: "new.go", NewFile: true, Diff: "@@ -0,0 +1 @@\n+package x\n"},
	}
	d, ok := ComputeInterdiff(reviewed, rebased)
	if !ok || len(d.Files) != 3 {
		t.Fatalf("a moved, a dropped and an added file: %+v %v", d, ok)
	}
	// moved 1, dropped 2, added 1
	if d.Lines != 4 {
		t.Fatalf("lines = %d", d.Lines)
	}
	diff, _ := d.Render(200)
	for _, s := range []string{
		"--- reviewed: migrations/0010_x.sql\n+++ rebased: migrations/0011_x.sql\n# moved",
		"--- reviewed: gone.go\n+++ rebased: (not in the change)\n",
		"--- reviewed: (not in the change)\n+++ rebased: new.go\n",
		"++package x\n",
	} {
		if !strings.Contains(diff, s) {
			t.Fatalf("render lacks %q:\n%s", s, diff)
		}
	}
	if !d.OnlyIn(map[string]bool{"migrations/0011_x.sql": true, "gone.go": true, "new.go": true}) {
		t.Fatal("a moved file counts by either path")
	}
	if d.OnlyIn(map[string]bool{"migrations/0010_x.sql": true, "gone.go": true}) {
		t.Fatal("new.go did not change on main")
	}
}

func TestInterdiffIncompleteChange(t *testing.T) {
	ok := []gitlab.Diff{{NewPath: "a.go", Diff: "+a\n"}}
	for _, broken := range []gitlab.Diff{{NewPath: "a.go", TooLarge: true}, {NewPath: "a.go", Collapsed: true}} {
		if _, judged := ComputeInterdiff(ok, []gitlab.Diff{broken}); judged {
			t.Fatalf("%+v cannot be judged", broken)
		}
		if _, judged := ComputeInterdiff([]gitlab.Diff{broken}, ok); judged {
			t.Fatalf("%+v cannot be judged", broken)
		}
	}
}

func TestInterdiffRenderCut(t *testing.T) {
	var b strings.Builder
	b.WriteString("@@ -0,0 +1,300 @@\n")
	for i := range 300 {
		fmt.Fprintf(&b, "+line %d\n", i)
	}
	d, _ := ComputeInterdiff(nil, []gitlab.Diff{{NewPath: "big.go", NewFile: true, Diff: b.String()}})
	if d.Lines != 300 {
		t.Fatalf("lines = %d", d.Lines)
	}
	diff, more := d.Render(200)
	// 3 header lines and 300 lines, cut at 200.
	if strings.Count(diff, "\n") != 200 || more != 103 {
		t.Fatalf("cut at %d lines, %d more", strings.Count(diff, "\n"), more)
	}
}

func TestSameCommits(t *testing.T) {
	reviewed := []gitlab.Commit{{ID: "a", Title: "Add x", AuthorEmail: "bot@x"}, {ID: "b", Title: "Fix y", AuthorEmail: "bot@x"}}
	rebased := []gitlab.Commit{{ID: "c", Title: "Add x", AuthorEmail: "Bot@X"}, {ID: "d", Title: "Fix y", AuthorEmail: "bot@x"}}
	if !SameCommits(reviewed, rebased) {
		t.Fatal("a rebase keeps titles and authors, not IDs")
	}
	extra := append([]gitlab.Commit{{ID: "e", Title: "Hotfix", AuthorEmail: "human@x"}}, rebased...)
	other := []gitlab.Commit{rebased[0], {ID: "d", Title: "Fix y", AuthorEmail: "human@x"}}
	swapped := []gitlab.Commit{rebased[1], rebased[0]}
	for _, c := range [][]gitlab.Commit{extra, other, swapped} {
		if SameCommits(reviewed, c) {
			t.Fatalf("%+v are not the reviewed commits", c)
		}
	}
}
