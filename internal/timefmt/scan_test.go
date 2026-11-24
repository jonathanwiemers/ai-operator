package timefmt

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// layoutHits is every string literal in the Go source src, interpreted or
// raw, whose value contains the clock layout "15:04". Comments are not
// literals, so they never hit.
func layoutHits(path string, src []byte) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var hits []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		v, err := strconv.Unquote(lit.Value)
		if err == nil && strings.Contains(v, "15:04") {
			hits = append(hits, v)
		}
		return true
	})
	return hits, nil
}

// layoutKey is one allowed literal in one file, the path relative to the
// repository root with forward slashes.
type layoutKey struct{ path, literal string }

// allowedLayouts are the clock layouts that do not go through UTCMinute,
// each with its reason.
var allowedLayouts = map[layoutKey]string{
	{"internal/runner/research.go", "Monday, 2 January 2006, 15:04 MST"}: "the agent's prompt, in the request's zone",
	{"internal/runner/research.go", "2006-01-02 15:04"}:                  "feed entries in the prompt, in the request's zone",
}

// TestNoOtherClockLayouts keeps every clock time in server text in
// UTCMinute's form: a new literal with "15:04" under internal/ or cmd/
// fails, unless allowedLayouts lists it.
func TestNoOtherClockLayouts(t *testing.T) {
	root := filepath.Join("..", "..")
	used := map[layoutKey]bool{}
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if rel == "internal/timefmt" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(p) // #nosec G304 -- a source file of this repository
			if err != nil {
				return err
			}
			hits, err := layoutHits(rel, src)
			if err != nil {
				return err
			}
			for _, h := range hits {
				k := layoutKey{rel, h}
				if _, ok := allowedLayouts[k]; ok {
					used[k] = true
					continue
				}
				t.Errorf("%s: clock layout %q: write the time with timefmt.UTCMinute, or allow it in allowedLayouts with a reason", rel, h)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for k := range allowedLayouts {
		if !used[k] {
			t.Errorf("allowedLayouts entry %s %q matches nothing any more: remove it", k.path, k.literal)
		}
	}
}

func TestLayoutHits(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      []string
	}{
		{"format call", "package p\nimport \"time\"\nvar s = time.Now().Format(\"15:04 UTC\")\n", []string{"15:04 UTC"}},
		{"raw string", "package p\nconst l = `2006-01-02 15:04`\n", []string{"2006-01-02 15:04"}},
		{"comment", "package p\n// Writes 15:04 UTC.\n/* or \"15:04\" */\nvar s = \"14:05\"\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := layoutHits("p.go", []byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("layoutHits = %q, want %q", got, tc.want)
			}
		})
	}
	if _, err := layoutHits("bad.go", []byte("not go")); err == nil {
		t.Error("layoutHits on invalid source: no error")
	}
}
