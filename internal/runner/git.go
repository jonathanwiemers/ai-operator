package runner

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Mirror is the runner's bare copy of the repository. It is the only git
// repository that talks to GitLab, it lives on a volume the agent container
// cannot see, and it is only ever touched by the runner, so its config and
// hooks are trusted. Commits travel to and from the agent's Workspace as git
// bundles: fetching from a bundle cannot execute anything.
type Mirror struct {
	Dir   string
	Token string
}

// env is deliberately minimal: no GITLAB_TOKEN, no Kubernetes variables, no
// user or system git config. Credentials are added per remote command via
// GIT_CONFIG_* variables, so they never end up in a config file or argv.
func (m *Mirror) env(withAuth bool) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Dir(m.Dir),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
	}
	if v := os.Getenv("SSL_CERT_FILE"); v != "" {
		env = append(env, "SSL_CERT_FILE="+v)
	}
	if withAuth && m.Token != "" {
		basic := base64.StdEncoding.EncodeToString([]byte("oauth2:" + m.Token))
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.extraHeader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic "+basic,
		)
	}
	return env
}

func (m *Mirror) run(ctx context.Context, withAuth bool, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "--git-dir", m.Dir}, args...)...)
	cmd.Env = m.env(withAuth)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, tail(strings.TrimSpace(stderr.String()), 1000))
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// Sync makes refs/remotes/origin/<b> match the remote for each given branch
// (a branch missing on the remote is removed locally). Only these branches
// are fetched; the mirror stays on the persistent volume, so follow-up runs
// only transfer the delta.
func (m *Mirror) Sync(ctx context.Context, url string, branches ...string) error {
	if _, err := os.Stat(filepath.Join(m.Dir, "HEAD")); err != nil {
		if err := os.MkdirAll(m.Dir, 0o755); err != nil {
			return err
		}
		if _, err := m.run(ctx, false, "init", "--quiet", "--bare"); err != nil {
			return err
		}
		if _, err := m.run(ctx, false, "remote", "add", "origin", url); err != nil {
			return err
		}
	} else if _, err := m.run(ctx, false, "remote", "set-url", "origin", url); err != nil {
		return err
	}

	heads := make([]string, 0, len(branches))
	for _, b := range branches {
		heads = append(heads, "refs/heads/"+b)
	}
	out, err := m.run(ctx, true, append([]string{"ls-remote", "origin"}, heads...)...)
	if err != nil {
		return err
	}
	remote := map[string]bool{}
	var refspecs []string
	for _, line := range strings.Split(out, "\n") {
		if _, ref, ok := strings.Cut(strings.TrimSpace(line), "\t"); ok {
			b := strings.TrimPrefix(ref, "refs/heads/")
			remote[b] = true
			refspecs = append(refspecs, fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", b, b))
		}
	}
	if len(refspecs) > 0 {
		if _, err := m.run(ctx, true, append([]string{"fetch", "--quiet", "--no-tags", "origin"}, refspecs...)...); err != nil {
			return err
		}
	}
	for _, b := range branches {
		if !remote[b] && m.HasRemoteBranch(ctx, b) {
			if _, err := m.run(ctx, false, "update-ref", "-d", "refs/remotes/origin/"+b); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Mirror) resolve(ctx context.Context, ref string) (string, bool) {
	sha, err := m.run(ctx, false, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return sha, err == nil
}

// HasRemoteBranch reports whether origin/<branch> exists after the last Sync.
func (m *Mirror) HasRemoteBranch(ctx context.Context, branch string) bool {
	_, ok := m.resolve(ctx, "refs/remotes/origin/"+branch)
	return ok
}

// Bundle prepares the transfer of origin/<b> for the given branches to the
// workspace. known are the commits the workspace already has; they are left
// out of the bundle. It returns the refs the workspace must end up with and
// whether a bundle was written (false if the workspace already has every
// commit it needs).
func (m *Mirror) Bundle(ctx context.Context, path string, known map[string]string, branches ...string) (map[string]string, bool, error) {
	refs := map[string]string{}
	var include []string
	for _, b := range branches {
		ref := "refs/remotes/origin/" + b
		if sha, ok := m.resolve(ctx, ref); ok {
			refs[ref] = sha
			include = append(include, ref)
		}
	}
	if len(include) == 0 {
		return refs, false, fmt.Errorf("none of %v exists on the remote", branches)
	}
	// Only exclude commits the mirror has, otherwise bundle creation fails.
	var exclude []string
	shas := make([]string, 0, len(known))
	for _, sha := range known {
		shas = append(shas, sha)
	}
	sort.Strings(shas)
	for _, sha := range shas {
		if _, ok := m.resolve(ctx, sha); ok {
			exclude = append(exclude, "^"+sha)
		}
	}
	args := append(append([]string{}, include...), exclude...)
	count, err := m.run(ctx, false, append([]string{"rev-list", "--count"}, args...)...)
	if err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(count) == "0" {
		return refs, false, nil
	}
	_ = os.Remove(path)
	if _, err := m.run(ctx, false, append([]string{"bundle", "create", "--quiet", path}, args...)...); err != nil {
		return nil, false, err
	}
	return refs, true, nil
}

// Import fetches refs/heads/<branch> from a bundle produced by the workspace.
func (m *Mirror) Import(ctx context.Context, path, branch string) error {
	ref := "refs/heads/" + branch
	_, err := m.run(ctx, false, "fetch", "--quiet", "--no-tags", path, "+"+ref+":"+ref)
	return err
}

// Push pushes the imported branch. It is not forced: if someone pushed to
// the branch during the run, the push fails instead of losing their work.
func (m *Mirror) Push(ctx context.Context, branch string) error {
	ref := "refs/heads/" + branch
	_, err := m.run(ctx, true, "push", "--quiet", "origin", ref+":"+ref)
	return err
}
