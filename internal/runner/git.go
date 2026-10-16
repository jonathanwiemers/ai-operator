package runner

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git runs git commands in a repository. Credentials are passed through
// GIT_CONFIG_* environment variables of the individual git process, so they
// never end up in .git/config, the remote URL or the model's environment.
type Git struct {
	Dir         string
	Token       string
	AuthorName  string
	AuthorEmail string
}

func (g *Git) env(withAuth bool) []string {
	env := append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME="+g.AuthorName, "GIT_AUTHOR_EMAIL="+g.AuthorEmail,
		"GIT_COMMITTER_NAME="+g.AuthorName, "GIT_COMMITTER_EMAIL="+g.AuthorEmail,
	)
	if withAuth && g.Token != "" {
		basic := base64.StdEncoding.EncodeToString([]byte("oauth2:" + g.Token))
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.extraHeader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic "+basic,
		)
	}
	return env
}

func (g *Git) run(ctx context.Context, withAuth bool, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = g.env(withAuth)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, tail(strings.TrimSpace(stderr.String()), 1000))
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// Run runs a local git command.
func (g *Git) Run(ctx context.Context, args ...string) (string, error) {
	return g.run(ctx, false, g.Dir, args...)
}

// Remote runs a git command that talks to the remote.
func (g *Git) Remote(ctx context.Context, args ...string) (string, error) {
	return g.run(ctx, true, g.Dir, args...)
}

// Ensure clones the repository if the workspace does not have it yet and
// fetches the latest state otherwise. Keeping the checkout on the workspace
// PVC means follow-up runs only fetch the delta.
func (g *Git) Ensure(ctx context.Context, url string) error {
	if _, err := os.Stat(filepath.Join(g.Dir, ".git")); err == nil {
		if _, err := g.Run(ctx, "remote", "set-url", "origin", url); err != nil {
			return err
		}
		_, err := g.Remote(ctx, "fetch", "--prune", "origin")
		return err
	}
	if err := os.MkdirAll(filepath.Dir(g.Dir), 0o755); err != nil {
		return err
	}
	_, err := g.run(ctx, true, filepath.Dir(g.Dir), "clone", "--no-tags", url, filepath.Base(g.Dir))
	return err
}

// RemoteBranchExists reports whether origin/<branch> exists after a fetch.
func (g *Git) RemoteBranchExists(ctx context.Context, branch string) bool {
	_, err := g.Run(ctx, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	return err == nil
}

// CheckoutBranch checks out branch, based on origin/<branch> if it exists
// (so human pushes to the agent's branch are picked up) or on origin/<base>.
// Local leftovers of an interrupted run are discarded.
func (g *Git) CheckoutBranch(ctx context.Context, branch, base string) error {
	start := "origin/" + base
	if g.RemoteBranchExists(ctx, branch) {
		start = "origin/" + branch
	}
	if _, err := g.Run(ctx, "checkout", "--force", "-B", branch, start); err != nil {
		return err
	}
	if _, err := g.Run(ctx, "reset", "--hard", start); err != nil {
		return err
	}
	_, err := g.Run(ctx, "clean", "-fd")
	return err
}

// CommitAll commits every change in the work tree. It returns false if there
// was nothing to commit.
func (g *Git) CommitAll(ctx context.Context, message string) (bool, error) {
	if _, err := g.Run(ctx, "add", "-A"); err != nil {
		return false, err
	}
	status, err := g.Run(ctx, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(status) == "" {
		return false, nil
	}
	if strings.TrimSpace(message) == "" {
		message = "WIP: agent changes"
	}
	_, err = g.Run(ctx, "commit", "--no-verify", "-m", message)
	return err == nil, err
}

// Diff returns the diff of HEAD against the merge base with origin/<base>.
func (g *Git) Diff(ctx context.Context, base string) (string, error) {
	return g.Run(ctx, "diff", "--no-color", "origin/"+base+"...HEAD")
}

// DiffStat returns a short summary of the diff against origin/<base>.
func (g *Git) DiffStat(ctx context.Context, base string) (string, error) {
	return g.Run(ctx, "diff", "--stat", "origin/"+base+"...HEAD")
}

// Ahead reports whether HEAD has commits that origin/<branch> (or, if the
// branch was never pushed, origin/<base>) does not have.
func (g *Git) Ahead(ctx context.Context, branch, base string) (bool, error) {
	ref := "origin/" + base
	if g.RemoteBranchExists(ctx, branch) {
		ref = "origin/" + branch
	}
	out, err := g.Run(ctx, "rev-list", "--count", ref+"..HEAD")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "0", nil
}

// Push pushes HEAD to the branch.
func (g *Git) Push(ctx context.Context, branch string) error {
	_, err := g.Remote(ctx, "push", "origin", "HEAD:refs/heads/"+branch)
	return err
}
