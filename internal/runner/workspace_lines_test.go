package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUncommittedLines(t *testing.T) {
	w := checkpointWorkspace(t)
	ctx := t.Context()
	count := func() (int64, int64) {
		t.Helper()
		a, d, err := w.UncommittedLines(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return a, d
	}
	if a, d := count(); a != 0 || d != 0 {
		t.Fatalf("clean tree: +%d/−%d, want +0/−0", a, d)
	}

	// A tracked edit (one line changed, two added), a staged deletion, a new
	// text file without a final newline, a new binary file, an ignored file
	// and a new file in a new directory.
	writeFile(t, w.Dir, "app.go", "package main\n\nfunc main() {}\n")
	gitCmd(t, w.Dir, "rm", "--quiet", ".env.example")
	writeFile(t, w.Dir, "notes.txt", "one\ntwo\nthree")
	writeFile(t, w.Dir, "logo.png", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\n\n\n")
	writeFile(t, w.Dir, ".gitignore", "build/\n")
	if err := os.MkdirAll(filepath.Join(w.Dir, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, w.Dir, "build/out.js", "a\nb\nc\nd\n")
	if err := os.MkdirAll(filepath.Join(w.Dir, "pkg", "pay"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, w.Dir, "pkg/pay/pay.go", "package pay\n")
	writeFile(t, w.Dir, "empty.txt", "")

	// app.go +3/−1, .env.example −1, notes.txt +3, .gitignore +1, pay.go +1.
	if a, d := count(); a != 8 || d != 2 {
		t.Fatalf("dirty tree: +%d/−%d, want +8/−2 (binary, ignored and empty files count nothing)", a, d)
	}
}

func TestTextLines(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name, content string
		want          int64
	}{
		{"empty", "", 0},
		{"one", "x", 1},
		{"newline", "x\n", 1},
		{"two", "x\ny", 2},
		{"nul", "x\n\x00y\n", 0},
		// A NUL after the first 8000 bytes: text, as git decides it.
		{"late-nul", strings.Repeat("a\n", 4500) + "\x00\n", 4501},
	} {
		writeFile(t, dir, tc.name, tc.content)
		if got := textLines(filepath.Join(dir, tc.name)); got != tc.want {
			t.Errorf("%s: %d lines, want %d", tc.name, got, tc.want)
		}
	}
	if got := textLines(filepath.Join(dir, "missing")); got != 0 {
		t.Errorf("missing file: %d lines", got)
	}
}
