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
	if _, err := w.git(ctx, "reset", "--quiet", "--hard", "HEAD"); err != nil {
		return err
	}
	_, err := w.git(ctx, "clean", "-fdq")
	return err
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
