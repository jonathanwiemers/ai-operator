// Package interdiff owns the interdiff of a rebase: how the rebased version
// of a merge request's change differs from the reviewed one, file by file
// and without hunk positions, its rendering as one unified diff, and the
// check that a rebase kept the reviewed commits.
package interdiff

import (
	"fmt"
	"slices"
	"strings"

	"github.com/pmezard/go-difflib/difflib"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// interdiffFile is one file whose change differs between the reviewed and
// the rebased version.
type interdiffFile struct {
	// from and to are the file's path pair in the reviewed and the rebased
	// change; one is nil for a file added to or dropped from the change.
	from, to *filePaths
	// diff is the unified diff of the file's +/- lines; empty for a file
	// that only moved (the same lines under another path).
	diff string
	// lines counts the changed lines of diff; 1 for a moved file.
	lines int
}

// filePaths is the old and new path of a file in a change.
type filePaths struct{ old, new string }

func (p filePaths) String() string {
	if p.old == "" || p.old == p.new {
		return p.new
	}
	return p.old + " → " + p.new
}

// Interdiff is how the rebased change differs from the reviewed one.
type Interdiff struct {
	Files []interdiffFile
	// Lines is the number of changed lines of all files.
	Lines int
}

// changeLines are the +/- lines of each file (path pair) of a change, without
// positions, as diffHash sees them. ok is false if GitLab did not return the
// whole change (a too large or collapsed diff).
func changeLines(diffs []gitlab.Diff) (files map[filePaths][]string, ok bool) {
	files = map[filePaths][]string{}
	for _, d := range diffs {
		if d.TooLarge || d.Collapsed {
			return nil, false
		}
		var lines []string
		for _, line := range strings.Split(d.Diff, "\n") {
			if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "Binary") {
				lines = append(lines, line)
			}
		}
		p := filePaths{old: d.OldPath, new: d.NewPath}
		files[p] = append(files[p], lines...)
	}
	return files, true
}

// ComputeInterdiff compares the reviewed change with the rebased one, file by
// file. ok is false if either is incomplete. A file dropped from the change
// whose lines come back under another path counts as moved.
func ComputeInterdiff(reviewed, rebased []gitlab.Diff) (Interdiff, bool) {
	a, okA := changeLines(reviewed)
	b, okB := changeLines(rebased)
	if !okA || !okB {
		return Interdiff{}, false
	}
	var files []interdiffFile
	var dropped, added []filePaths
	for p, la := range a {
		lb, ok := b[p]
		switch {
		case !ok:
			dropped = append(dropped, p)
		case !slices.Equal(la, lb):
			diff, n := unifiedLines(la, lb, "reviewed: "+p.String(), "rebased: "+p.String())
			files = append(files, interdiffFile{from: &p, to: &p, diff: diff, lines: n})
		}
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			added = append(added, p)
		}
	}
	sortPaths(dropped)
	sortPaths(added)
	for _, p := range dropped {
		if i := slices.IndexFunc(added, func(q filePaths) bool { return slices.Equal(a[p], b[q]) }); i >= 0 {
			q := added[i]
			added = slices.Delete(added, i, i+1)
			files = append(files, interdiffFile{from: &p, to: &q, lines: 1})
			continue
		}
		diff, n := unifiedLines(a[p], nil, "reviewed: "+p.String(), "rebased: (not in the change)")
		files = append(files, interdiffFile{from: &p, diff: diff, lines: n})
	}
	for _, q := range added {
		diff, n := unifiedLines(nil, b[q], "reviewed: (not in the change)", "rebased: "+q.String())
		files = append(files, interdiffFile{to: &q, diff: diff, lines: n})
	}
	slices.SortFunc(files, func(x, y interdiffFile) int { return strings.Compare(x.key(), y.key()) })
	d := Interdiff{Files: files}
	for _, f := range files {
		d.Lines += f.lines
	}
	return d, true
}

func sortPaths(ps []filePaths) {
	slices.SortFunc(ps, func(x, y filePaths) int { return strings.Compare(x.String(), y.String()) })
}

// key sorts the files of an interdiff by the path they have now.
func (f interdiffFile) key() string {
	if f.to != nil {
		return f.to.new + "\x00" + f.to.old
	}
	return f.from.new + "\x00" + f.from.old
}

// paths lists the paths of the file on either side.
func (f interdiffFile) paths() []string {
	var out []string
	for _, p := range []*filePaths{f.from, f.to} {
		if p != nil {
			out = append(out, p.old, p.new)
		}
	}
	return slices.DeleteFunc(out, func(s string) bool { return s == "" })
}

// unifiedLines is the unified diff of two line lists and its number of
// changed lines.
func unifiedLines(a, b []string, from, to string) (string, int) {
	withNL := func(ls []string) []string {
		out := make([]string, len(ls))
		for i, l := range ls {
			out[i] = l + "\n"
		}
		return out
	}
	n := 0
	for _, op := range difflib.NewMatcher(a, b).GetOpCodes() {
		if op.Tag != 'e' {
			n += op.I2 - op.I1 + op.J2 - op.J1
		}
	}
	diff, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{A: withNL(a), B: withNL(b), FromFile: from, ToFile: to, Context: 3})
	return diff, n
}

// Render is the interdiff as one unified diff, at most maxLines lines; more
// is how many lines were cut.
func (d Interdiff) Render(maxLines int) (diff string, more int) {
	var b strings.Builder
	for _, f := range d.Files {
		if f.diff == "" {
			fmt.Fprintf(&b, "--- reviewed: %s\n+++ rebased: %s\n# moved: the same lines under another path\n", f.from, f.to)
			continue
		}
		b.WriteString(f.diff)
	}
	lines := strings.SplitAfter(b.String(), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= maxLines {
		return b.String(), 0
	}
	return strings.Join(lines[:maxLines], ""), len(lines) - maxLines
}

// OnlyIn reports whether every file of the interdiff whose lines changed is
// one of paths (by either of its paths). A moved file kept its lines: a
// renumbered migration does not change a file the default branch changed.
func (d Interdiff) OnlyIn(paths map[string]bool) bool {
	for _, f := range d.Files {
		if f.diff == "" {
			continue
		}
		if !slices.ContainsFunc(f.paths(), func(p string) bool { return paths[p] }) {
			return false
		}
	}
	return true
}

// SameCommits reports whether two versions have the same commits: their
// number, titles and author emails in order. A GitLab rebase keeps them; a
// push by someone else adds or changes one.
func SameCommits(a, b []gitlab.Commit) bool {
	return slices.EqualFunc(a, b, func(x, y gitlab.Commit) bool {
		return x.Title == y.Title && strings.EqualFold(x.AuthorEmail, y.AuthorEmail)
	})
}
