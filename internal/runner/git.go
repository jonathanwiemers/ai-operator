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
	"time"

	"github.com/go-logr/logr"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// Mirror is the runner's bare copy of the repository. It is the only git
// repository that talks to GitLab, it lives on a volume the agent container
// cannot see, and it is only ever touched by the runner, so its config and
// hooks are trusted. Commits travel to and from the agent's Workspace as git
// bundles: fetching from a bundle cannot execute anything.
type Mirror struct {
	Dir   string
	Token string
	// AuthorName and AuthorEmail sign the start commit of a new branch.
	AuthorName, AuthorEmail string
	// Log receives every git command at debug level (V(1)). The token is
	// never part of the arguments.
	Log logr.Logger
	// Policy is what the mirror may push (the merge/push guard). Without
	// one, every push is refused.
	Policy *PushPolicy

	// remoteURL is origin's URL as the last Sync set it.
	remoteURL string
}

// PushPolicy is the push side of the merge/push guard: only agent/ branches
// of the project the mirror belongs to, never its default branch, no tags,
// no deletes, no forced pushes.
type PushPolicy struct {
	// Project is the GitLab project the mirror's remote belongs to.
	Project string
	// DefaultBranch is the project's default branch.
	DefaultBranch string
	// URL is the project's repository URL (GitLab's http_url_to_repo): a
	// push to a mirror whose origin is another URL is refused.
	URL string
}

// homePushPolicy is the push policy of the home mirror, from the project
// as GitLab returns it.
func homePushPolicy(project string, proj *gitlab.Project) *PushPolicy {
	return &PushPolicy{Project: project, DefaultBranch: proj.DefaultBranch, URL: proj.HTTPURLToRepo}
}

// checkPush refuses a push the policy does not allow; dst is the
// destination of the refspec src:dst.
func (m *Mirror) checkPush(ctx context.Context, src, dst string) error {
	p := m.Policy
	project, op := "", "push "+src+":"+dst
	if p != nil {
		project = p.Project
	}
	refuse := func(reason string) error { return gitlab.Refuse(m.Log, project, op, reason) }
	if p == nil {
		return refuse("the mirror has no push policy")
	}
	branch := strings.TrimPrefix(dst, "refs/heads/")
	switch {
	case src == "" || strings.HasPrefix(src, "+") || strings.HasPrefix(src, "-") || strings.Contains(src, ":"):
		return refuse("not a plain, unforced push of a commit (a delete, a forced push or a refspec)")
	case strings.HasPrefix(dst, "refs/") && !strings.HasPrefix(dst, "refs/heads/"),
		// A tag as the source of a bare destination would create a tag.
		strings.HasPrefix(src, "refs/") && !strings.HasPrefix(src, "refs/heads/"):
		return refuse("not a branch (e.g. a tag)")
	case !gitlab.IsAgentBranch(branch):
		return refuse("not an " + gitlab.AgentBranchPrefix + " branch")
	case p.DefaultBranch == "" || branch == p.DefaultBranch:
		return refuse("the project's default branch, or the default branch is unknown")
	}
	url := m.remoteURL
	if url == "" {
		// Synced by an earlier process: the mirror's own config says it.
		url, _ = m.run(ctx, false, "config", "--get", "remote.origin.url")
	}
	if p.URL == "" || url != p.URL {
		return refuse(fmt.Sprintf("the mirror's remote %q is not the project's repository", url))
	}
	return nil
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
	// Automatic gc and maintenance run in the foreground: a detached one
	// would still write to the mirror after the command returned (and
	// break the cleanup of test directories).
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "gc.autoDetach=false",
		"-c", "maintenance.autoDetach=false", "--git-dir", m.Dir}, args...)...)
	cmd.Env = m.env(withAuth)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	err := cmd.Run()
	m.Log.V(1).Info("git", "args", args, "durationMs", time.Since(start).Milliseconds(), "ok", err == nil)
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, tail(strings.TrimSpace(stderr.String()), 1000))
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// Sync makes refs/remotes/origin/<b> match the remote for each given branch
// (a branch missing on the remote is removed locally). Only these branches
// are fetched; the mirror stays on the persistent volume, so follow-up runs
// only transfer the delta.
func (m *Mirror) Sync(ctx context.Context, url string, branches ...string) error {
	if !m.exists() {
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
	m.remoteURL = url

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

// exists reports whether the mirror has been initialised; the first Sync
// clones, later ones fetch the delta.
func (m *Mirror) exists() bool {
	_, err := os.Stat(filepath.Join(m.Dir, "HEAD"))
	return err == nil
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
func (m *Mirror) Push(ctx context.Context, branch string, skipCI bool) error {
	ref := "refs/heads/" + branch
	return m.push(ctx, ref, ref, skipCI)
}

// push pushes src to dst, with ci.skip if skipCI. A remote that takes no
// push options gets a plain push. The policy is checked first (checkPush).
func (m *Mirror) push(ctx context.Context, src, dst string, skipCI bool) error {
	if err := m.checkPush(ctx, src, dst); err != nil {
		return err
	}
	if skipCI {
		_, err := m.run(ctx, true, "push", "--quiet", "-o", "ci.skip", "origin", src+":"+dst)
		if err == nil || !strings.Contains(err.Error(), "push options") {
			return err
		}
	}
	_, err := m.run(ctx, true, "push", "--quiet", "origin", src+":"+dst)
	return err
}

// StartBranch creates branch on the remote with one empty commit on top of
// origin/<base>, so a merge request can be opened before the agent changed
// anything. The push skips CI (ci.skip): there is nothing to check yet.
func (m *Mirror) StartBranch(ctx context.Context, branch, base, message string) (string, error) {
	baseRef := "refs/remotes/origin/" + base
	name, email := m.AuthorName, m.AuthorEmail
	if name == "" {
		name = "AI Operator"
	}
	if email == "" {
		email = "ai-operator@noreply.local"
	}
	sha, err := m.run(ctx, false, "-c", "user.name="+name, "-c", "user.email="+email,
		"commit-tree", baseRef+"^{tree}", "-p", baseRef, "-m", message)
	if err != nil {
		return "", err
	}
	// Without push options (GitLab over HTTPS always takes them) a pipeline
	// for an empty commit does no harm.
	if err := m.push(ctx, sha, "refs/heads/"+branch, true); err != nil {
		return "", err
	}
	if _, err := m.run(ctx, false, "update-ref", "refs/remotes/origin/"+branch, sha); err != nil {
		return "", err
	}
	return sha, nil
}

// HasChanges reports whether head changes anything compared with where it
// branched off origin/<base>. git diff --quiet exits non-zero on changes; any
// other error counts as a change too.
func (m *Mirror) HasChanges(ctx context.Context, base, head string) bool {
	_, err := m.run(ctx, false, "diff", "--quiet", "refs/remotes/origin/"+base+"..."+head)
	return err != nil
}

// pinFiles are the toolchain pins the implementer installs (toolchainsNote).
var pinFiles = []string{".mise.toml", "mise.toml", ".tool-versions", "rust-toolchain.toml", "rust-toolchain"}

// PinFiles returns the toolchain pin files at the root of origin/<base>.
func (m *Mirror) PinFiles(ctx context.Context, base string) []string {
	out, err := m.run(ctx, false, append([]string{"ls-tree", "--name-only", "refs/remotes/origin/" + base, "--"}, pinFiles...)...)
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// ChangedPins returns the toolchain pin files that origin/<branch> changes
// (adds, edits or deletes) compared with where it branched off origin/<base>.
// A branch that does not exist yet changes nothing.
func (m *Mirror) ChangedPins(ctx context.Context, base, branch string) []string {
	out, err := m.run(ctx, false, append([]string{"diff", "--name-only",
		"refs/remotes/origin/" + base + "...refs/remotes/origin/" + branch, "--"}, pinFiles...)...)
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}
