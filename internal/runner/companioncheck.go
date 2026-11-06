package runner

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/jonathanwiemers/ai-operator/internal/pathspec"
)

// The path check of a companion change (docs/specs/cross-repo-changes.spec.md,
// decision 5): before any push of either tree, the runner checks the
// companion branch in its own, trusted mirror. Every commit is checked, not
// only the net diff, because a push publishes them all.

// companionMaxBytes caps the new blob bytes of a companion range: infra
// changes are small YAML, so 1 MiB is generous and still stops dumps.
const companionMaxBytes int64 = 1 << 20

// CompanionViolation is a companion change the runner refuses to push.
// Path is empty for a reason that concerns the whole range (a merge
// commit, the size cap). Commit is the commit that makes the change, empty
// for the net diff.
type CompanionViolation struct {
	Path   string
	Reason string
	Commit string
}

func (v *CompanionViolation) Error() string {
	var b strings.Builder
	b.WriteString("companion change refused")
	if v.Path != "" {
		fmt.Fprintf(&b, ": %s", v.Path)
	}
	if v.Commit != "" {
		fmt.Fprintf(&b, " (commit %s)", shortSHA(v.Commit))
	}
	fmt.Fprintf(&b, ": %s. Neither repository was pushed", v.Reason)
	return b.String()
}

// AsCompanionViolation returns the violation in err's chain, if any.
func AsCompanionViolation(err error) (*CompanionViolation, bool) {
	var v *CompanionViolation
	ok := errors.As(err, &v)
	return v, ok
}

const (
	modeSymlink   = "120000"
	modeSubmodule = "160000"
)

// treeChange is one entry of git diff-tree --raw: with --no-renames a
// rename is a delete and an add, so every entry has one path.
type treeChange struct {
	oldMode, newMode string
	newSHA           string
	status           string
	path             string
}

// CheckCompanion checks the companion range merge-base(origin/<base>,
// head)..head of the mirror: no merge commits, and in every commit and in
// the net diff only paths m matches, no symlinks or submodules, nothing of
// the CI configuration (.gitlab-ci.yml, .gitlab/) or .gitmodules, and at
// most maxBytes of new blobs. A refusal is a *CompanionViolation; any other
// error means the check could not run, which refuses the push as well.
func (m *Mirror) CheckCompanion(ctx context.Context, base, head string, paths *pathspec.Matcher, maxBytes int64) error {
	if paths == nil {
		return fmt.Errorf("companion check: no allowed paths")
	}
	headSHA, ok := m.resolve(ctx, head)
	if !ok {
		return fmt.Errorf("companion check: %s is not a commit in the mirror", head)
	}
	baseSHA, ok := m.resolve(ctx, "refs/remotes/origin/"+base)
	if !ok {
		return fmt.Errorf("companion check: the mirror has no origin/%s", base)
	}
	mb, err := m.run(ctx, false, "merge-base", baseSHA, headSHA)
	if err != nil || mb == "" {
		return &CompanionViolation{Reason: fmt.Sprintf("the branch has no common history with %s", base)}
	}
	out, err := m.run(ctx, false, "rev-list", "--parents", "--reverse", mb+".."+headSHA)
	if err != nil {
		return err
	}
	newBlobs := map[string]bool{}
	check := func(from, to, commit string) error {
		changes, err := m.diffTree(ctx, from, to)
		if err != nil {
			return err
		}
		for _, c := range changes {
			if reason := companionChangeRefusal(c, paths); reason != "" {
				return &CompanionViolation{Path: c.path, Reason: reason, Commit: commit}
			}
			if c.status != "D" && !isZeroSHA(c.newSHA) {
				newBlobs[c.newSHA] = true
			}
		}
		return nil
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 0:
			continue
		case len(f) > 2:
			return &CompanionViolation{Reason: "it contains a merge commit; the companion branch takes plain commits only", Commit: f[0]}
		case len(f) < 2:
			return &CompanionViolation{Reason: "it contains a root commit", Commit: f[0]}
		}
		if err := check(f[1], f[0], f[0]); err != nil {
			return err
		}
	}
	if err := check(mb, headSHA, ""); err != nil {
		return err
	}
	size, err := m.blobBytes(ctx, newBlobs)
	if err != nil {
		return err
	}
	if size > maxBytes {
		return &CompanionViolation{Reason: fmt.Sprintf("its new files add up to %d bytes, more than the cap of %d bytes", size, maxBytes)}
	}
	return nil
}

// companionChangeRefusal says why a change is refused, "" if it is allowed.
func companionChangeRefusal(c treeChange, paths *pathspec.Matcher) string {
	switch {
	case isCIOrSubmoduleConfig(c.path):
		return "CI configuration (.gitlab-ci.yml, .gitlab/) and .gitmodules are never part of a companion change"
	case c.status != "D" && c.newMode == modeSymlink:
		return "it adds a symlink"
	case c.status != "D" && c.newMode == modeSubmodule:
		return "it adds a submodule"
	case !paths.Match(c.path):
		return "outside the allowed paths"
	}
	return ""
}

// isCIOrSubmoduleConfig reports whether a path is .gitlab-ci.yml or
// .gitmodules (at any depth: an include may name a nested one) or lies in
// a .gitlab directory. Validation already keeps such patterns out of the
// allowed paths; this is defence in depth.
func isCIOrSubmoduleConfig(path string) bool {
	segs := strings.Split(path, "/")
	base := segs[len(segs)-1]
	return base == ".gitlab-ci.yml" || base == ".gitmodules" || slices.Contains(segs, ".gitlab")
}

// diffTree lists the changes from one commit to another, recursively and
// without rename detection, so a rename names both sides.
func (m *Mirror) diffTree(ctx context.Context, from, to string) ([]treeChange, error) {
	out, err := m.run(ctx, false, "diff-tree", "-r", "-z", "--raw", "--no-renames", "--no-abbrev", from, to)
	if err != nil {
		return nil, err
	}
	return parseRawDiff(out)
}

// parseRawDiff parses git's -z --raw output without renames:
// ":<old mode> <new mode> <old sha> <new sha> <status>\0<path>\0".
func parseRawDiff(out string) ([]treeChange, error) {
	fields := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	var changes []treeChange
	for i := 0; i < len(fields); i++ {
		meta := strings.TrimSpace(fields[i])
		if meta == "" {
			continue
		}
		f := strings.Fields(strings.TrimPrefix(meta, ":"))
		if !strings.HasPrefix(meta, ":") || len(f) != 5 || i+1 >= len(fields) {
			return nil, fmt.Errorf("companion check: unexpected diff-tree output %q", meta)
		}
		status := f[4][:1]
		if status == "R" || status == "C" {
			return nil, fmt.Errorf("companion check: unexpected rename or copy in diff-tree output")
		}
		i++
		changes = append(changes, treeChange{oldMode: f[0], newMode: f[1], newSHA: f[3], status: status, path: fields[i]})
	}
	return changes, nil
}

func isZeroSHA(sha string) bool {
	return strings.Trim(sha, "0") == ""
}

// blobBytes sums the sizes of the given objects (git cat-file
// --batch-check). Gitlinks are refused before, so all are blobs.
func (m *Mirror) blobBytes(ctx context.Context, shas map[string]bool) (int64, error) {
	if len(shas) == 0 {
		return 0, nil
	}
	list := make([]string, 0, len(shas))
	for sha := range shas {
		list = append(list, sha)
	}
	slices.Sort(list)
	out, err := m.runInput(ctx, false, strings.Join(list, "\n")+"\n", "cat-file", "--batch-check=%(objectname) %(objecttype) %(objectsize)")
	if err != nil {
		return 0, err
	}
	var total int64
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			return 0, fmt.Errorf("companion check: object %s: %s", strings.TrimSpace(line), "missing in the mirror")
		}
		n, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("companion check: size of %s: %w", f[0], err)
		}
		total += n
	}
	return total, nil
}
