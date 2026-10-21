package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	cmd := exec.CommandContext(ctx, "git", args...)
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
	if strings.TrimSpace(message) == "" {
		message = "WIP: agent changes"
	}
	_, err = w.git(ctx, "commit", "--quiet", "--no-verify", "-m", message)
	return err == nil, err
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
