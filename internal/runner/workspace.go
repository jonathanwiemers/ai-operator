package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// Workspace is the agent's checkout. It lives in the agent container, which
// holds no GitLab or Kubernetes credentials, and it never talks to the remote:
// commits arrive from and leave to the runner's Mirror as git bundles.
//
// The model can modify everything in here, including .git/config, hooks and
// .gitattributes, which git may turn into command execution. That is why
// only code running in the agent container ever runs git in this repository.
type Workspace struct {
	Dir         string
	AuthorName  string
	AuthorEmail string
}

func (w *Workspace) git(ctx context.Context, args ...string) (string, error) {
	// Automatic gc and maintenance run in the foreground, as in Mirror.run:
	// a detached one (the default for maintenance since git 2.47) can hold
	// the output pipes, so Run would not return. WaitDelay bounds the wait
	// for pipes a leftover child still holds once git itself has exited.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "gc.autoDetach=false", "-c", "maintenance.autoDetach=false"}, args...)...)
	cmd.WaitDelay = 10 * time.Second
	cmd.Dir = w.Dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME="+w.AuthorName, "GIT_AUTHOR_EMAIL="+w.AuthorEmail,
		"GIT_COMMITTER_NAME="+w.AuthorName, "GIT_COMMITTER_EMAIL="+w.AuthorEmail,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, tail(strings.TrimSpace(stderr.String()), 1000))
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

func (w *Workspace) exists() bool {
	_, err := os.Stat(filepath.Join(w.Dir, ".git"))
	return err == nil
}

func (w *Workspace) hasRef(ctx context.Context, ref string) bool {
	_, err := w.git(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// RemoteRefs returns the commits the workspace has for refs/remotes/origin/*,
// so the runner only needs to bundle what is new.
func (w *Workspace) RemoteRefs(ctx context.Context) (map[string]string, error) {
	refs := map[string]string{}
	if !w.exists() {
		return refs, nil
	}
	out, err := w.git(ctx, "for-each-ref", "--format=%(refname) %(objectname)", "refs/remotes/origin/")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(out, "\n") {
		if name, sha, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			refs[name] = sha
		}
	}
	return refs, nil
}

// PrepareRequest describes the state the workspace must be in before a run.
type PrepareRequest struct {
	// Repo is the tree: homeTree or a companion's project slug (the agent
	// side resolves it, see LocalAgent.tree).
	Repo string `json:"repo,omitempty"`
	// Bundle with new objects from the mirror; empty if nothing is new.
	Bundle string `json:"bundle,omitempty"`
	// Refs is the authoritative set of refs/remotes/origin/* refs.
	Refs   map[string]string `json:"refs"`
	Branch string            `json:"branch"`
	Base   string            `json:"base"`
}

// Prepare imports the mirror's state and checks out the work branch, based on
// origin/<branch> if it exists (so human pushes are picked up) or on
// origin/<base>. Leftovers of an interrupted run are discarded.
func (w *Workspace) Prepare(ctx context.Context, req PrepareRequest) error {
	w.removeStaleLock()
	_ = os.Remove(w.lastStatusPath())
	if !w.exists() {
		if err := os.MkdirAll(w.Dir, 0o755); err != nil {
			return err
		}
		if _, err := w.git(ctx, "init", "--quiet", "--initial-branch="+req.Base); err != nil {
			return err
		}
	}
	if req.Bundle != "" {
		if _, err := w.git(ctx, "fetch", "--quiet", "--no-tags", req.Bundle, "+refs/remotes/origin/*:refs/remotes/origin/*"); err != nil {
			return err
		}
	}
	current, err := w.RemoteRefs(ctx)
	if err != nil {
		return err
	}
	for name, sha := range req.Refs {
		if !strings.HasPrefix(name, "refs/remotes/origin/") {
			return fmt.Errorf("unexpected ref %q", name)
		}
		if current[name] != sha {
			if _, err := w.git(ctx, "update-ref", name, sha); err != nil {
				return err
			}
		}
	}
	for name := range current {
		if _, ok := req.Refs[name]; !ok {
			if _, err := w.git(ctx, "update-ref", "-d", name); err != nil {
				return err
			}
		}
	}

	start := "refs/remotes/origin/" + req.Base
	if _, ok := req.Refs["refs/remotes/origin/"+req.Branch]; ok {
		start = "refs/remotes/origin/" + req.Branch
	}
	if _, err := w.git(ctx, "checkout", "--quiet", "--force", "-B", req.Branch, start); err != nil {
		return err
	}
	return w.Reset(ctx)
}

// Reset discards uncommitted changes and untracked files (ignored files such
// as build caches are kept).
func (w *Workspace) Reset(ctx context.Context) error {
	w.removeStaleLock()
	if _, err := w.git(ctx, "reset", "--quiet", "--hard", "HEAD"); err != nil {
		return err
	}
	_, err := w.git(ctx, "clean", "-fdq")
	return err
}

// removeStaleLock removes the index lock a git command leaves behind when its
// container is killed. Git runs in the checkout only on the agent server's
// request, one call at a time, so a lock between calls is always stale.
func (w *Workspace) removeStaleLock() {
	_ = os.Remove(filepath.Join(w.Dir, ".git", "index.lock"))
}

// lastStatusFile, in .git so it is never committed, holds the last status
// text of the agent's call in progress (LocalAgent.Run).
const lastStatusFile = "ai-last-status"

func (w *Workspace) lastStatusPath() string { return filepath.Join(w.Dir, ".git", lastStatusFile) }

// lastStatus reads the status text LocalAgent.Run kept, "" if there is none.
func (w *Workspace) lastStatus() string {
	f, err := os.Open(w.lastStatusPath())
	if err != nil {
		return ""
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, 4*maxStatusText))
	return strings.TrimSpace(string(b))
}

// Commit commits every change in the work tree. It reports false if there
// was nothing to commit.
func (w *Workspace) Commit(ctx context.Context, message string) (bool, error) {
	if _, err := w.git(ctx, "add", "-A"); err != nil {
		return false, err
	}
	status, err := w.git(ctx, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(status) == "" {
		return false, nil
	}
	_, err = w.git(ctx, "commit", "--quiet", "--no-verify", "-m", orWIP(message))
	return err == nil, err
}

func orWIP(message string) string {
	if strings.TrimSpace(message) == "" {
		return "WIP: agent changes"
	}
	return message
}

// CommitRequest asks for the commit of a step of the run on the home tree's
// work branch.
type CommitRequest struct {
	Message string `json:"message"`
	Branch  string `json:"branch"`
	Base    string `json:"base"`
	// Pushed, if set, is the head the runner pushed last in this run: the
	// checkout's refs/remotes/origin/<branch> does not learn about it.
	Pushed string `json:"pushed,omitempty"`
}

// CommitResult is the outcome of CommitStep. FoldedFrom is set when the
// commit folded Folded checkpoints: it is the checkpoint that was the tip,
// SHA the new commit that replaces them.
type CommitResult struct {
	Committed  bool   `json:"committed"`
	SHA        string `json:"sha,omitempty"`
	FoldedFrom string `json:"foldedFrom,omitempty"`
	Folded     int    `json:"folded,omitempty"`
}

// CommitStep commits a step like Commit. When the tip of req.Branch is a
// pushed checkpoint, every checkpoint at the tip is folded into the step's
// commit, which carries req.Message: the branch is reset (soft) to the
// commit before them, so the commit holds their changes and the step's. It
// folds even when the step changed nothing itself, so the agent's message
// replaces the checkpoint's placeholder.
func (w *Workspace) CommitStep(ctx context.Context, req CommitRequest) (CommitResult, error) {
	tip, keep, n := w.foldTarget(ctx, req)
	if n > 0 {
		if _, err := w.git(ctx, "add", "-A"); err != nil {
			return CommitResult{}, err
		}
		// Checkpoints that cancel out leave nothing to fold into.
		if _, err := w.git(ctx, "diff", "--cached", "--quiet", keep); err == nil {
			n = 0
		}
	}
	if n == 0 {
		ok, err := w.Commit(ctx, req.Message)
		return CommitResult{Committed: ok}, err
	}
	if _, err := w.git(ctx, "reset", "--quiet", "--soft", keep); err != nil {
		return CommitResult{}, err
	}
	if _, err := w.git(ctx, "commit", "--quiet", "--no-verify", "-m", orWIP(req.Message)); err != nil {
		_, _ = w.git(ctx, "reset", "--quiet", "--soft", tip) // back to the checkpoint
		return CommitResult{}, err
	}
	sha, err := w.git(ctx, "rev-parse", "HEAD")
	return CommitResult{Committed: true, SHA: sha, FoldedFrom: tip, Folded: n}, err
}

// foldTarget returns what CommitStep folds: the tip, the commit before the
// checkpoints at the tip and their count. n is 0 (no fold) unless req.Branch
// is an agent branch other than the base, checked out, without a merge or
// rebase in progress, and its tip is a checkpoint that was pushed.
func (w *Workspace) foldTarget(ctx context.Context, req CommitRequest) (tip, keep string, n int) {
	if req.Branch == req.Base || !strings.HasPrefix(req.Branch, "agent/") || w.hasRef(ctx, "MERGE_HEAD") || w.rebaseDir() != "" {
		return "", "", 0
	}
	if cur, err := w.git(ctx, "symbolic-ref", "--quiet", "HEAD"); err != nil || cur != "refs/heads/"+req.Branch {
		return "", "", 0
	}
	tip, err := w.git(ctx, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return "", "", 0
	}
	if pushed, _ := w.git(ctx, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+req.Branch+"^{commit}"); tip != pushed && tip != req.Pushed {
		return "", "", 0
	}
	args := []string{"log", "--first-parent", "--format=" + checkpointLogFormat, "HEAD"}
	if mb, err := w.git(ctx, "merge-base", "refs/remotes/origin/"+req.Base, "HEAD"); err == nil {
		args = append(args, "^"+mb)
	}
	out, err := w.git(ctx, args...)
	if err != nil {
		return "", "", 0
	}
	keep, n, _ = checkpointRun(out, tip)
	if n == 0 {
		return "", "", 0
	}
	return tip, keep, n
}

// checkpointLogFormat is the git log format checkpointRun reads: the commit,
// its parents and the values of its Agent-Checkpoint trailers.
const checkpointLogFormat = "%H %P%x00%(trailers:key=Agent-Checkpoint,valueonly,separator=%x2C)"

// checkpointRun reads git log output in checkpointLogFormat, newest first,
// from tip. It counts the checkpoints in a row from tip: each with one
// parent, the next line's commit, and the trailer. keep is the parent of the
// last one. all reports whether every line was such a checkpoint.
func checkpointRun(out, tip string) (keep string, n int, all bool) {
	keep = tip
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, line := range lines {
		commit, trailer, _ := strings.Cut(line, "\x00")
		f := strings.Fields(commit)
		if len(f) != 2 || f[0] != keep || strings.TrimSpace(trailer) == "" {
			return keep, n, false
		}
		keep = f[1]
		n++
	}
	return keep, n, n > 0
}

// UnfoldRequest asks to undo a fold whose push was refused.
type UnfoldRequest struct {
	// Tip is the checkpoint that was the tip (CommitResult.FoldedFrom).
	Tip string `json:"tip"`
	// Fold is the commit that folded the checkpoints (CommitResult.SHA).
	Fold string `json:"fold"`
}

// Unfold puts the fold commit, and the commits on top of it, back on top of
// the checkpoint req.Tip, with their trees and messages: the step's work
// becomes a normal commit on the checkpoint. The index and the work tree
// stay as they are. It returns the new HEAD.
func (w *Workspace) Unfold(ctx context.Context, req UnfoldRequest) (string, error) {
	_, errFold := w.git(ctx, "merge-base", "--is-ancestor", req.Fold, "HEAD")
	keep, errKeep := w.git(ctx, "rev-parse", "--verify", "--quiet", req.Fold+"^1")
	if _, err := w.git(ctx, "merge-base", "--is-ancestor", keep, req.Tip); errFold != nil || errKeep != nil || err != nil {
		return "", fmt.Errorf("%s on HEAD is not a fold of the checkpoint %s", req.Fold, req.Tip)
	}
	out, err := w.git(ctx, "rev-list", "--reverse", "--parents", keep+"..HEAD")
	if err != nil {
		return "", err
	}
	head, prev := req.Tip, keep
	for _, line := range strings.Split(out, "\n") {
		// The commits from the fold to HEAD, each on the one before.
		f := strings.Fields(line)
		if len(f) != 2 || f[1] != prev || prev == keep && f[0] != req.Fold {
			return "", fmt.Errorf("the commits from %s to HEAD are not a line of single-parent commits", req.Fold)
		}
		msg, err := w.git(ctx, "log", "-1", "--format=%B", f[0])
		if err != nil {
			return "", err
		}
		if head, err = w.git(ctx, "commit-tree", f[0]+"^{tree}", "-p", head, "-m", msg); err != nil {
			return "", err
		}
		prev = f[0]
	}
	_, err = w.git(ctx, "reset", "--quiet", "--soft", head)
	return head, err
}

// Diff returns the diff of HEAD against the merge base with origin/<base> and
// a short stat of it.
func (w *Workspace) Diff(ctx context.Context, base string) (diff, stat string, err error) {
	if diff, err = w.git(ctx, "diff", "--no-color", "--no-ext-diff", "origin/"+base+"...HEAD"); err != nil {
		return "", "", err
	}
	stat, err = w.git(ctx, "diff", "--stat", "--no-ext-diff", "origin/"+base+"...HEAD")
	return diff, stat, err
}

// UncommittedLines counts the lines the work tree changes against HEAD:
// git diff --numstat HEAD (binary files count nothing) plus every line of
// the untracked, not ignored text files. A tree without commits counts its
// untracked files only.
func (w *Workspace) UncommittedLines(ctx context.Context) (added, deleted int64, err error) {
	if w.hasRef(ctx, "HEAD") {
		out, err := w.git(ctx, "diff", "--numstat", "--no-renames", "--no-ext-diff", "HEAD")
		if err != nil {
			return 0, 0, err
		}
		for _, line := range strings.Split(out, "\n") {
			f := strings.Fields(line)
			if len(f) < 3 {
				continue
			}
			a, _ := strconv.ParseInt(f[0], 10, 64) // "-" for binary files
			d, _ := strconv.ParseInt(f[1], 10, 64)
			added, deleted = added+a, deleted+d
		}
	}
	out, err := w.git(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return 0, 0, err
	}
	for _, path := range strings.Split(out, "\x00") {
		if path != "" {
			added += textLines(filepath.Join(w.Dir, path))
		}
	}
	return added, deleted, nil
}

// textLines counts the lines of the regular file at path, a last line
// without a newline included; 0 for a binary file (a NUL byte in its first
// 8000 bytes, as git decides it) or one it cannot read.
func textLines(path string) int64 {
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	const probe = 8000
	buf := make([]byte, 64*1024)
	var lines, read int64
	var last byte
	for {
		n, err := f.Read(buf)
		chunk := buf[:n]
		if read < probe && bytes.IndexByte(chunk[:min(int64(n), probe-read)], 0) >= 0 {
			return 0
		}
		if n > 0 {
			lines += int64(bytes.Count(chunk, []byte{'\n'}))
			read, last = read+int64(n), chunk[n-1]
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0
		}
	}
	if read > 0 && last != '\n' {
		lines++
	}
	return lines
}

// Bundle writes the commits of branch that origin does not have yet into a
// bundle at path. It reports false (and writes nothing) if there are none.
func (w *Workspace) Bundle(ctx context.Context, branch, base, path string) (bool, error) {
	args := []string{"refs/heads/" + branch}
	for _, ref := range []string{"refs/remotes/origin/" + base, "refs/remotes/origin/" + branch} {
		if w.hasRef(ctx, ref) {
			args = append(args, "^"+ref)
		}
	}
	count, err := w.git(ctx, append([]string{"rev-list", "--count"}, args...)...)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(count) == "0" {
		return false, nil
	}
	_ = os.Remove(path)
	_, err = w.git(ctx, append([]string{"bundle", "create", "--quiet", path}, args...)...)
	return err == nil, err
}

// MergeResult is the outcome of merging the base branch into the work branch.
type MergeResult struct {
	// UpToDate: the work branch already contains the base branch.
	UpToDate bool `json:"upToDate,omitempty"`
	// Merged: the merge commit was made.
	Merged bool `json:"merged,omitempty"`
	// Conflicts are the files with conflicts; the merge stays in progress.
	Conflicts []string `json:"conflicts,omitempty"`
	// From is HEAD before the merge, to go back to if it is not resolved.
	From string `json:"from,omitempty"`
}

// MergeBase merges origin/<base> into the work branch. Without conflicts it
// commits the merge. With conflicts it leaves the merge in progress, with
// conflict markers in the files, for the agent to resolve.
func (w *Workspace) MergeBase(ctx context.Context, base, message string) (MergeResult, error) {
	upstream := "refs/remotes/origin/" + base
	if _, err := w.git(ctx, "merge-base", "--is-ancestor", upstream, "HEAD"); err == nil {
		return MergeResult{UpToDate: true}, nil
	}
	from, err := w.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return MergeResult{}, err
	}
	_, err = w.git(ctx, "merge", "--no-ff", "--no-verify", "--no-edit", "-m", message, upstream)
	if err == nil {
		return MergeResult{Merged: true, From: from}, nil
	}
	files, uerr := w.unmerged(ctx)
	if uerr != nil || len(files) == 0 {
		_, _ = w.git(ctx, "reset", "--quiet", "--hard", from)
		return MergeResult{}, err
	}
	return MergeResult{Conflicts: files, From: from}, nil
}

// FinishMerge describes how to end a merge with conflicts.
type FinishMerge struct {
	// Repo is the tree: homeTree or a companion's project slug.
	Repo    string `json:"repo,omitempty"`
	Base    string `json:"base"`
	Message string `json:"message"`
	// From is HEAD before the merge (MergeResult.From).
	From string `json:"from"`
	// Files are the files that had conflicts.
	Files []string `json:"files"`
	// Abort goes back to From without checking anything.
	Abort bool `json:"abort,omitempty"`
}

// FinishMerge concludes a merge the agent resolved. If a file still carries
// conflict markers, or the merge was aborted, the branch goes back to the
// commit before the merge and the files are returned as conflicts: a merge
// that is not fully resolved is never committed. If the agent concluded the
// merge itself, HEAD must contain origin/<base>.
func (w *Workspace) FinishMerge(ctx context.Context, req FinishMerge) (MergeResult, error) {
	back := func(files []string, cause error) (MergeResult, error) {
		if _, err := w.git(ctx, "reset", "--quiet", "--hard", req.From); err != nil {
			return MergeResult{}, err
		}
		if _, err := w.git(ctx, "clean", "-fdq"); err != nil {
			return MergeResult{}, err
		}
		return MergeResult{Conflicts: files, From: req.From}, cause
	}
	if req.Abort {
		return back(req.Files, nil)
	}
	if left := w.withMarkers(req.Files); len(left) > 0 {
		return back(left, nil)
	}
	if !w.hasRef(ctx, "MERGE_HEAD") {
		// The agent concluded the merge itself, or aborted it.
		if _, err := w.git(ctx, "merge-base", "--is-ancestor", "refs/remotes/origin/"+req.Base, "HEAD"); err != nil {
			return back(req.Files, nil)
		}
		return MergeResult{Merged: true, From: req.From}, nil
	}
	if _, err := w.git(ctx, "add", "-A"); err != nil {
		return back(req.Files, err)
	}
	if left, err := w.unmerged(ctx); err != nil || len(left) > 0 {
		return back(left, err)
	}
	if _, err := w.git(ctx, "commit", "--quiet", "--no-verify", "--no-edit", "-m", req.Message); err != nil {
		return back(req.Files, err)
	}
	return MergeResult{Merged: true, From: req.From}, nil
}

// DefaultCheckpointMaxFileBytes is the largest file a checkpoint commits
// when CheckpointRequest.MaxFileBytes is 0.
const DefaultCheckpointMaxFileBytes = 5 << 20

// checkpointTrailer marks a checkpoint commit.
const checkpointTrailer = "Agent-Checkpoint:"

// checkpointSecrets are the base names of new files a checkpoint leaves out.
var checkpointSecrets = []string{".env*", "*.pem", "*.key", "id_rsa*", "*.p12"}

// CheckpointRequest asks for the work tree to be saved as a checkpoint
// commit on the task's work branch.
type CheckpointRequest struct {
	// Reason is why the run was cut off (subject and trailer).
	Reason string `json:"reason"`
	Issue  int64  `json:"issue"`
	Run    int64  `json:"run"`
	Step   int    `json:"step"`
	Branch string `json:"branch"`
	Base   string `json:"base"`
	// Expect, if set, is the last pushed commit: HEAD must be it or descend
	// from it.
	Expect string `json:"expect,omitempty"`
	// MaxFileBytes is the largest file committed; 0 means
	// DefaultCheckpointMaxFileBytes.
	MaxFileBytes int64 `json:"maxFileBytes,omitempty"`
	// Status is the agent's last status text, cut at 1 000 characters.
	Status string `json:"status,omitempty"`
}

// CheckpointSkip is a file a checkpoint left out; it stays in the work tree.
type CheckpointSkip struct {
	Path string `json:"path"`
	Why  string `json:"why"`
}

// CheckpointResult is the outcome of a checkpoint. SHA is empty if no commit
// was made: the tree was clean, a merge or rebase was aborted (Aborted), or
// the workspace was not safe to commit in (Refused).
type CheckpointResult struct {
	SHA     string           `json:"sha,omitempty"`
	Files   int              `json:"files,omitempty"`
	Added   int64            `json:"added,omitempty"`
	Deleted int64            `json:"deleted,omitempty"`
	Stat    string           `json:"stat,omitempty"`
	Skipped []CheckpointSkip `json:"skipped,omitempty"`
	Aborted string           `json:"aborted,omitempty"`
	Refused string           `json:"refused,omitempty"`
}

// Checkpoint commits the work tree on req.Branch so that a run that was cut
// off loses nothing. It refuses (Refused, no error) to commit anywhere but
// the task's agent branch, aborts a merge or rebase in progress instead of
// committing it, and leaves out big files and new files that look like
// secrets. Errors are failed git commands.
func (w *Workspace) Checkpoint(ctx context.Context, req CheckpointRequest) (CheckpointResult, error) {
	w.removeStaleLock()
	// A restarted container or a lost pod has no result to take it from.
	if strings.TrimSpace(req.Status) == "" {
		req.Status = w.lastStatus()
	}
	defer os.Remove(w.lastStatusPath())
	if why := w.checkpointRefusal(ctx, req); why != "" {
		return CheckpointResult{Refused: why}, nil
	}
	if aborted, err := w.abortInProgress(ctx); aborted != "" || err != nil {
		return CheckpointResult{Aborted: aborted}, err
	}
	if _, err := w.git(ctx, "add", "-A"); err != nil {
		return CheckpointResult{}, err
	}
	skipped, err := w.unstageUnsafe(ctx, req.MaxFileBytes)
	if err != nil {
		return CheckpointResult{}, err
	}
	res := CheckpointResult{Skipped: skipped}
	numstat, err := w.git(ctx, "diff", "--cached", "--numstat", "--no-renames")
	if err != nil || strings.TrimSpace(numstat) == "" {
		return res, err
	}
	if res.Stat, err = w.git(ctx, "diff", "--cached", "--stat", "--no-renames"); err != nil {
		return res, err
	}
	for _, line := range strings.Split(numstat, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		res.Files++
		a, _ := strconv.ParseInt(f[0], 10, 64) // "-" for binary files
		d, _ := strconv.ParseInt(f[1], 10, 64)
		res.Added, res.Deleted = res.Added+a, res.Deleted+d
	}
	if _, err := w.git(ctx, "commit", "--quiet", "--no-verify", "--cleanup=whitespace", "-m", checkpointMessage(req, res.Stat)); err != nil {
		return res, err
	}
	res.SHA, err = w.git(ctx, "rev-parse", "HEAD")
	return res, err
}

// checkpointRefusal returns why the workspace is not safe for a checkpoint
// of req, or "".
func (w *Workspace) checkpointRefusal(ctx context.Context, req CheckpointRequest) string {
	switch {
	case req.Branch == "":
		return "no work branch given"
	case req.Branch == req.Base:
		return fmt.Sprintf("%s is the base branch", req.Branch)
	case !strings.HasPrefix(req.Branch, "agent/"):
		return fmt.Sprintf("%s is not an agent branch", req.Branch)
	}
	// In a rebase HEAD is detached; the branch being rebased is in head-name.
	current, err := w.git(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if dir := w.rebaseDir(); dir != "" {
		var name []byte
		name, err = os.ReadFile(filepath.Join(dir, "head-name"))
		current = strings.TrimPrefix(strings.TrimSpace(string(name)), "refs/heads/")
	}
	if err != nil || current != req.Branch {
		return fmt.Sprintf("the workspace is on %q, not on %s", current, req.Branch)
	}
	if req.Expect != "" {
		// A missing object fails like a commit that is not an ancestor.
		if _, err := w.git(ctx, "merge-base", "--is-ancestor", req.Expect, "refs/heads/"+req.Branch); err != nil {
			return fmt.Sprintf("%s does not descend from the last pushed commit %s", req.Branch, req.Expect)
		}
	}
	return ""
}

// rebaseDir returns the state directory of a rebase in progress, or "".
func (w *Workspace) rebaseDir() string {
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		dir := filepath.Join(w.Dir, ".git", name)
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
	}
	return ""
}

// abortInProgress aborts a merge (back to ORIG_HEAD, the commit before it)
// or a rebase in progress and says which one it was.
func (w *Workspace) abortInProgress(ctx context.Context) (string, error) {
	if w.hasRef(ctx, "MERGE_HEAD") {
		// The reset also ends the merge if the abort failed.
		_, _ = w.git(ctx, "merge", "--abort")
		_, err := w.git(ctx, "reset", "--quiet", "--hard", "ORIG_HEAD")
		return "merge", err
	}
	if w.rebaseDir() != "" {
		_, err := w.git(ctx, "rebase", "--abort")
		return "rebase", err
	}
	return "", nil
}

// unstageUnsafe takes staged files over maxBytes and new files that look
// like secrets out of the index. They stay in the work tree.
func (w *Workspace) unstageUnsafe(ctx context.Context, maxBytes int64) ([]CheckpointSkip, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultCheckpointMaxFileBytes
	}
	out, err := w.git(ctx, "diff", "--cached", "--name-status", "--no-renames", "-z")
	if err != nil {
		return nil, err
	}
	fields := strings.Split(out, "\x00")
	var skipped []CheckpointSkip
	var paths []string
	for i := 0; i+1 < len(fields); i += 2 {
		status, path := fields[i], fields[i+1]
		why := ""
		if status == "A" && isSecretName(filepath.Base(path)) {
			why = "looks like a secret"
		} else if fi, err := os.Lstat(filepath.Join(w.Dir, path)); err == nil && status != "D" && fi.Size() > maxBytes {
			why = fmt.Sprintf("larger than %d bytes", maxBytes)
		}
		if why != "" {
			skipped = append(skipped, CheckpointSkip{Path: path, Why: why})
			paths = append(paths, ":(literal)"+path)
		}
	}
	if len(paths) > 0 {
		if _, err := w.git(ctx, append([]string{"reset", "--quiet", "--"}, paths...)...); err != nil {
			return nil, err
		}
	}
	return skipped, nil
}

func isSecretName(name string) bool {
	for _, p := range checkpointSecrets {
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}

// checkpointMessage is the message of a checkpoint commit: the fixed subject,
// the diff stat, the agent's status and the Agent-Checkpoint trailer.
func checkpointMessage(req CheckpointRequest, stat string) string {
	reason := strings.Join(strings.Fields(req.Reason), " ")
	parts := []string{
		fmt.Sprintf("chore(wip): checkpoint #%d step %d (%s)", req.Issue, req.Step, reason),
		stat,
	}
	if status := strings.TrimSpace(req.Status); status != "" {
		parts = append(parts, textutil.Truncate(status, 999))
	}
	parts = append(parts, fmt.Sprintf("%s %d/%d %s", checkpointTrailer, req.Run, req.Step, reason))
	return strings.Join(parts, "\n\n")
}

// IsCheckpoint reports whether a commit message carries the Agent-Checkpoint
// trailer (in its last paragraph).
func IsCheckpoint(message string) bool {
	paras := strings.Split(strings.TrimSpace(strings.ReplaceAll(message, "\r\n", "\n")), "\n\n")
	for _, line := range strings.Split(paras[len(paras)-1], "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), checkpointTrailer) {
			return true
		}
	}
	return false
}

// unmerged lists the files with unresolved conflicts in the index.
func (w *Workspace) unmerged(ctx context.Context) ([]string, error) {
	out, err := w.git(ctx, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(out, "\n") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// withMarkers returns the files that still contain conflict markers.
func (w *Workspace) withMarkers(files []string) []string {
	var out []string
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(w.Dir, filepath.Clean("/"+f)))
		if err != nil {
			continue // deleted as the resolution
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "<<<<<<< ") || strings.HasPrefix(line, ">>>>>>> ") {
				out = append(out, f)
				break
			}
		}
	}
	return out
}
