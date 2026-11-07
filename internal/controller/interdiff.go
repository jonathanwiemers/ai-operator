package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// A rebase that changed the reviewed change (rebased: the hash differs) is
// judged by its interdiff, the reviewed change against the rebased one. With
// the reviewed commits and a mechanical interdiff the reviewers check the
// rebased head again without a pause, at most maxAutoReReviews times per
// item.
const (
	// autoReReviewMaxLines is the largest interdiff (changed lines) that is
	// mechanical wherever it is.
	autoReReviewMaxLines = 40
	// maxAutoReReviews is how often one item may be re-reviewed after a
	// rebase without a pause.
	maxAutoReReviews = 2
	// maxInterdiffNoteLines bounds the interdiff shown in the MR note.
	maxInterdiffNoteLines = 200
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

// interdiff is how the rebased change differs from the reviewed one.
type interdiff struct {
	files []interdiffFile
	// lines is the number of changed lines of all files.
	lines int
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

// computeInterdiff compares the reviewed change with the rebased one, file by
// file. ok is false if either is incomplete. A file dropped from the change
// whose lines come back under another path counts as moved.
func computeInterdiff(reviewed, rebased []gitlab.Diff) (interdiff, bool) {
	a, okA := changeLines(reviewed)
	b, okB := changeLines(rebased)
	if !okA || !okB {
		return interdiff{}, false
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
	d := interdiff{files: files}
	for _, f := range files {
		d.lines += f.lines
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

// render is the interdiff as one unified diff, at most maxLines lines; more
// is how many lines were cut.
func (d interdiff) render(maxLines int) (diff string, more int) {
	var b strings.Builder
	for _, f := range d.files {
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

// onlyIn reports whether every file of the interdiff whose lines changed is
// one of paths (by either of its paths). A moved file kept its lines: a
// renumbered migration does not change a file the default branch changed.
func (d interdiff) onlyIn(paths map[string]bool) bool {
	for _, f := range d.files {
		if f.diff == "" {
			continue
		}
		if !slices.ContainsFunc(f.paths(), func(p string) bool { return paths[p] }) {
			return false
		}
	}
	return true
}

// sameCommits reports whether two versions have the same commits: their
// number, titles and author emails in order. A GitLab rebase keeps them; a
// push by someone else adds or changes one.
func sameCommits(a, b []gitlab.Commit) bool {
	return slices.EqualFunc(a, b, func(x, y gitlab.Commit) bool {
		return x.Title == y.Title && strings.EqualFold(x.AuthorEmail, y.AuthorEmail)
	})
}

// rebaseCheck is the verdict on a rebase that changed the reviewed change.
type rebaseCheck struct {
	// mechanical: the reviewers may check the rebased head again without a
	// pause (the cap aside).
	mechanical bool
	// why says why it is or is not mechanical, for the MR note.
	why string
	// diff is the interdiff, nil if it could not be computed.
	diff *interdiff
	// reviewed and rebased are the heads compared.
	reviewed, rebased string
}

// checkRebase judges the rebase of the item's MR from it.RebasedFrom (the
// reviewed head) to mr.SHA by the MR's versions. An error is GitLab's,
// tried again on the next poll; a version GitLab does not have counts as
// someone's push.
func (a *autopilot) checkRebase(ctx context.Context, it *aiv1.AutopilotItem, mr *gitlab.MergeRequest) (rebaseCheck, error) {
	glProject := a.spec.GitLab.Project
	c := rebaseCheck{reviewed: it.RebasedFrom, rebased: mr.SHA}
	vs, err := a.gl.ListMergeRequestVersions(ctx, glProject, mr.IID)
	switch {
	case gitlab.IsNotFound(err):
		c.why = fmt.Sprintf("GitLab lists no versions of !%d, so it is unknown whether someone pushed", mr.IID)
		return c, nil
	case err != nil:
		return c, fmt.Errorf("listing the versions of !%d: %w", mr.IID, err)
	}
	version := func(head string) (*gitlab.MergeRequestVersionDetail, error) {
		i := slices.IndexFunc(vs, func(v gitlab.MergeRequestVersion) bool { return v.HeadCommitSHA == head })
		if i < 0 {
			return nil, nil
		}
		v, err := a.gl.GetMergeRequestVersion(ctx, glProject, mr.IID, vs[i].ID)
		switch {
		case gitlab.IsNotFound(err):
			return nil, nil
		case err != nil:
			return nil, fmt.Errorf("getting version %d of !%d: %w", vs[i].ID, mr.IID, err)
		}
		return v, nil
	}
	reviewed, err := version(it.RebasedFrom)
	if err != nil {
		return c, err
	}
	rebased, err := version(mr.SHA)
	if err != nil {
		return c, err
	}
	switch {
	case reviewed == nil:
		c.why = fmt.Sprintf("GitLab has no version of the reviewed head %s, so it is unknown whether someone pushed", short(it.RebasedFrom))
		return c, nil
	case rebased == nil:
		c.why = fmt.Sprintf("GitLab has no version of the rebased head %s, so it is unknown whether someone pushed", short(mr.SHA))
		return c, nil
	}
	d, ok := computeInterdiff(reviewed.Diffs, rebased.Diffs)
	if ok {
		c.diff = &d
	}
	switch {
	case !sameCommits(reviewed.Commits, rebased.Commits):
		c.why = fmt.Sprintf("the commits of %s are not the reviewed ones (%d before, %d now, or another title or author): someone pushed to the branch",
			short(mr.SHA), len(reviewed.Commits), len(rebased.Commits))
		return c, nil
	case !ok:
		c.why = "GitLab did not return the whole change (a diff is too large or collapsed), so the interdiff cannot be judged"
		return c, nil
	case d.lines <= autoReReviewMaxLines:
		c.mechanical = true
		c.why = fmt.Sprintf("the commits are the reviewed ones, and the interdiff has %s (at most %d)", changedLines(d.lines), autoReReviewMaxLines)
		return c, nil
	}
	c.why = fmt.Sprintf("the interdiff has %s (more than %d)", changedLines(d.lines), autoReReviewMaxLines)
	base := a.baseName(ctx)
	if reviewed.BaseCommitSHA == "" || rebased.BaseCommitSHA == "" || reviewed.BaseCommitSHA == rebased.BaseCommitSHA {
		c.why += fmt.Sprintf(", and the rebase did not move it onto a newer `%s`", base)
		return c, nil
	}
	cmp, err := a.gl.CompareCommits(ctx, glProject, reviewed.BaseCommitSHA, rebased.BaseCommitSHA)
	switch {
	case gitlab.IsNotFound(err):
		c.why += fmt.Sprintf(", and GitLab cannot compare the two bases on `%s`", base)
		return c, nil
	case err != nil:
		return c, fmt.Errorf("comparing %s and %s: %w", short(reviewed.BaseCommitSHA), short(rebased.BaseCommitSHA), err)
	}
	onMain := map[string]bool{}
	for _, f := range cmp.Diffs {
		onMain[f.OldPath], onMain[f.NewPath] = true, true
	}
	if d.onlyIn(onMain) {
		c.mechanical = true
		c.why = fmt.Sprintf("the commits are the reviewed ones, and every file of the interdiff (%s) also changed on `%s` between the two bases", changedLines(d.lines), base)
		return c, nil
	}
	c.why += fmt.Sprintf(", in files `%s` did not change between the two bases", base)
	return c, nil
}

// reReviewRebased takes the rebased head over without a pause after a
// mechanical rebase (check): like the hold path it becomes the pushed head
// (so the gate never takes it for someone's push), but it stays unreviewed
// (ReviewedSHA is the old head) until the review-only run asked for here
// lands. Findings then go the usual way, a clean review and green CI merge.
func (a *autopilot) reReviewRebased(ctx context.Context, it *aiv1.AutopilotItem, mr *gitlab.MergeRequest, task *aiv1.AgentTask, check rebaseCheck, companion string) error {
	a.log.Info("autopilot re-reviews the rebased head", "issue", it.IID, "mr", mr.IID, "from", it.RebasedFrom, "to", mr.SHA,
		"interdiffLines", check.diff.lines, "reReview", it.AutoReReviews+1, "why", check.why)
	orig := task.DeepCopy()
	task.Status.Run.PushedSHA = mr.SHA
	task.Status.Run.PushedAt = &metav1.Time{Time: a.r.now()}
	task.Status.CI = homeCIReset(task.Status.CI, mr.SHA)
	if err := a.r.Status().Patch(ctx, task, client.MergeFrom(orig)); err != nil {
		return err
	}
	if err := a.r.askReview(ctx, task, aiv1.ReviewHeads(mr.SHA, companion)); err != nil {
		return err
	}
	if err := a.r.requestRun(ctx, a.gl, a.spec, task, it.IID); err != nil {
		return err
	}
	it.AutoReReviews++
	it.AutoReReviewHead = mr.SHA
	it.RebasedFrom, it.RebasedDiff, it.RebasedAt = "", "", nil
	return nil
}

// baseName is the default branch for a text, "the default branch" if it
// cannot be read.
func (a *autopilot) baseName(ctx context.Context) string {
	if base, err := a.base(ctx); err == nil && base != "" {
		return base
	}
	return "the default branch"
}

func changedLines(n int) string {
	if n == 1 {
		return "1 changed line"
	}
	return fmt.Sprintf("%d changed lines", n)
}

// rebaseNote is the MR note about a rebase that changed the reviewed
// change: why it is or is not mechanical, what follows, and the interdiff.
// It is "" without an interdiff. The interdiff is defused (showDiff), the
// rest is the operator's.
func rebaseNote(mrIID int64, base string, c rebaseCheck, reReview bool) string {
	if c.diff == nil {
		return ""
	}
	next := "so autopilot pauses until the reviewers or a run take the new head over"
	if reReview {
		next = "so the reviewers check the rebased head again; autopilot goes on once it is reviewed and green, without a pause"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "🔁 The rebase of !%d onto `%s` changed the reviewed change: %s, %s.\n\n", mrIID, base, c.why, next)
	diff, more := c.diff.render(maxInterdiffNoteLines)
	fmt.Fprintf(&b, "<details><summary>Interdiff (reviewed → rebased)</summary>\n\nReviewed %s, rebased %s: %s in %d files.%s\n",
		short(c.reviewed), short(c.rebased), changedLines(c.diff.lines), len(c.diff.files), showDiff(diff))
	if more > 0 {
		fmt.Fprintf(&b, "\n\n…%d more lines", more)
	}
	b.WriteString("\n\n</details>")
	return b.String()
}
