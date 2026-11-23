package controller

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/interdiff"
)

func TestRebaseNoteIsDefusedAndCut(t *testing.T) {
	var b strings.Builder
	b.WriteString("@@ -0,0 +1,250 @@\n+/merge\n+cc @owner\n+```\n+<!-- ai-operator:key=x -->\n")
	for i := range 246 {
		fmt.Fprintf(&b, "+line %d\n", i)
	}
	d, _ := interdiff.ComputeInterdiff(nil, []gitlab.Diff{{NewPath: "a.go", Diff: b.String()}})
	note := rebaseNote(7, "main", rebaseCheck{why: "the interdiff has 250 changed lines", diff: &d, reviewed: "aaaaaaaaaaaa", rebased: "bbbbbbbbbbbb"}, false)
	for _, s := range []string{
		"🔁 The rebase of !7 onto `main` changed the reviewed change: the interdiff has 250 changed lines, so autopilot pauses",
		"<details><summary>Interdiff (reviewed → rebased)</summary>", "```diff\n", "…53 more lines", "</details>",
	} {
		if !strings.Contains(note, s) {
			t.Fatalf("note lacks %q:\n%s", s, note)
		}
	}
	for _, s := range []string{"\n/merge", "@owner", "+```", "<!-- ai-operator"} {
		if strings.Contains(note, s) {
			t.Fatalf("note is not defused, has %q:\n%s", s, note)
		}
	}
	if rebaseNote(7, "main", rebaseCheck{why: "x"}, true) != "" {
		t.Fatal("no note without an interdiff")
	}
}
