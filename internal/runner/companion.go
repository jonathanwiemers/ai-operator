package runner

import (
	"context"
	"fmt"
	"path/filepath"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
)

// The companion repository of an implement run
// (docs/specs/cross-repo-changes.spec.md, decision 5): the runner keeps its
// own mirror of it (/runner/mirrors/<slug>.git), the agent container its own
// checkout (/workspace/repos/<slug>), and commits move between the two by
// git bundle like the home tree's. The operator writes spec.companion; the
// runner only reads it.

// companionTree is the companion of the current implement run.
type companionTree struct {
	spec aiv1.CompanionSpec
	// slug names the companion's mirror, checkout and bundles, and is the
	// repo of its agent calls.
	slug   string
	proj   *gitlab.Project
	mirror *Mirror
	// base is the companion project's default branch.
	base string
}

// CompanionMirror returns the runner's mirror of the companion with the
// given project slug, /runner/mirrors/<slug>.git, with the companion token
// (GITLAB_COMPANION_TOKEN) and policy as what it may push. An invalid slug
// is refused: it names a directory.
func (r *Runner) CompanionMirror(slug string, policy *PushPolicy) (*Mirror, error) {
	if !naming.ValidProjectSlug(slug) {
		return nil, fmt.Errorf("companion mirror: %q is not a valid project slug", slug)
	}
	m := &Mirror{Dir: filepath.Join(r.StateDir, "mirrors", slug+".git"), Token: r.CompanionToken, Policy: policy,
		Log: r.Log.WithName("mirror").WithValues("repo", slug)}
	if r.Mirror != nil {
		m.AuthorName, m.AuthorEmail = r.Mirror.AuthorName, r.Mirror.AuthorEmail
	}
	return m, nil
}

// companionGitLab is the GitLab client for the companion project: the
// companion token, behind the same merge/push guard as the run's client.
func (r *Runner) companionGitLab(spec aiv1.AgentProjectSpec, task *aiv1.AgentTask) gitlab.API {
	return r.guardedGitLabWith(spec, task, r.CompanionToken)
}

// readCompanion reads the task's companion project and sets up its mirror.
// Without the companion token the operator left the companion out of the
// pod (its target is no longer valid): the run goes on without it, with a
// notice.
func (run *implementRun) readCompanion(ctx context.Context) error {
	r, c := run.r, run.task.Spec.Companion
	if c == nil {
		return nil
	}
	if r.CompanionToken == "" {
		r.log().Info("companion left out: this pod has no companion token", "project", c.Project)
		run.notices = append(run.notices, fmt.Sprintf("The companion repository `%s` is left out of this run: it is no longer a valid target of the project, so the agent pod has no token for it.",
			c.Project))
		return nil
	}
	slug := naming.ProjectSlug(c.Project)
	s := r.step(ctx, "read companion project", "project", c.Project, "repo", slug)
	if !gitlab.IsAgentBranch(c.Branch) {
		return s.fail(fmt.Errorf("companion %s: branch %q is not an %s branch", c.Project, c.Branch, gitlab.AgentBranchPrefix))
	}
	proj, err := r.companionGitLab(run.spec, run.task).GetProject(ctx, c.Project)
	if err != nil {
		return s.fail(fmt.Errorf("getting companion project %s: %w", c.Project, err))
	}
	if proj.DefaultBranch == "" {
		return s.fail(fmt.Errorf("companion project %s has no default branch", c.Project))
	}
	m, err := r.CompanionMirror(slug, &PushPolicy{Project: c.Project, DefaultBranch: proj.DefaultBranch, URL: proj.HTTPURLToRepo})
	if err != nil {
		return s.fail(err)
	}
	run.companion = &companionTree{spec: *c, slug: slug, proj: proj, mirror: m, base: proj.DefaultBranch}
	s.done(fmt.Sprintf("%s, default branch %s", c.Project, proj.DefaultBranch), "defaultBranch", proj.DefaultBranch)
	return nil
}

// syncCompanion brings the companion mirror up to date with the companion
// branch and default branch and checks out the companion branch in the
// agent's companion checkout: on origin/<branch> if it exists, else on the
// default branch.
func (r *Runner) syncCompanion(ctx context.Context, c *companionTree) error {
	branches := []string{c.base, c.spec.Branch}
	name := "fetch companion"
	if !c.mirror.exists() {
		name = "clone companion"
	}
	s := r.step(ctx, name, "repo", c.slug, "branches", branches)
	if err := c.mirror.Sync(ctx, c.proj.HTTPURLToRepo, branches...); err != nil {
		return s.fail(err)
	}
	baseSHA, _ := c.mirror.resolve(ctx, "refs/remotes/origin/"+c.base)
	result := fmt.Sprintf("%s at %s", c.base, shortSHA(baseSHA))
	kv := []any{"baseSHA", baseSHA}
	if sha, ok := c.mirror.resolve(ctx, "refs/remotes/origin/"+c.spec.Branch); ok {
		result += fmt.Sprintf(", %s at %s", c.spec.Branch, shortSHA(sha))
		kv = append(kv, "branchSHA", sha)
	} else {
		result += fmt.Sprintf(", %s is new", c.spec.Branch)
	}
	s.done(result, kv...)
	return r.prepareCompanion(ctx, c)
}

// prepareCompanion hands the companion checkout the commits of the
// companion mirror it lacks and checks out the companion branch.
func (r *Runner) prepareCompanion(ctx context.Context, c *companionTree) error {
	return r.prepareTree(ctx, c.mirror, c.slug, c.spec.Branch, c.base)
}

// commitCompanion commits the agent's changes in the companion checkout.
func (r *Runner) commitCompanion(ctx context.Context, c *companionTree, message string) error {
	return r.commitTree(ctx, c.slug, message)
}

// companionDiff is the diff of the companion checkout against its default
// branch, with a short stat.
func (r *Runner) companionDiff(ctx context.Context, c *companionTree) (diff, stat string, err error) {
	return r.Agent.Diff(ctx, c.slug, c.base)
}

// importCompanion moves the companion checkout's new commits into the
// companion mirror's refs/heads/<branch>. It does not push.
func (r *Runner) importCompanion(ctx context.Context, c *companionTree) error {
	s := r.step(ctx, "import companion", "repo", c.slug, "branch", c.spec.Branch)
	imported, err := r.importTree(ctx, c.mirror, c.slug, c.spec.Branch, c.base)
	if err != nil {
		return s.fail(err)
	}
	if !imported {
		s.done("no new commits", "imported", false)
		return nil
	}
	head, _ := c.mirror.resolve(ctx, "refs/heads/"+c.spec.Branch)
	s.done(fmt.Sprintf("%s at %s, not pushed", c.spec.Branch, shortSHA(head)), "imported", true, "sha", head)
	return nil
}

// companionChange commits and imports the companion's work of the run
// into its mirror. Pushing it is not part of this phase.
func (run *implementRun) companionChange(ctx context.Context) error {
	c := run.companion
	if c == nil {
		return nil
	}
	if err := run.r.commitCompanion(ctx, c, commitMessage(run.answer.CommitMessage, run.issue)); err != nil {
		return err
	}
	return run.r.importCompanion(ctx, c)
}
