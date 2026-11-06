package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
	"github.com/jonathanwiemers/ai-operator/internal/pathspec"
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
	// head is the companion branch head this run imported into the
	// mirror, "" before the first import; checked is the last head
	// checkCompanion passed.
	head, checked string
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
func (r *Runner) commitCompanion(ctx context.Context, c *companionTree, message agentMessage) error {
	return r.commitTree(ctx, c.slug, message)
}

// companionDiff is the diff of the companion checkout against its default
// branch, with a short stat.
func (r *Runner) companionDiff(ctx context.Context, c *companionTree) (diff, stat string, err error) {
	return r.Agent.Diff(ctx, c.slug, c.base)
}

// importCompanion moves the companion checkout's new commits into the
// companion mirror's refs/heads/<branch>. It does not push. It returns the
// companion head this run imported, "" while it has imported none: a
// refs/heads/<branch> left by an earlier run is not this run's change.
func (r *Runner) importCompanion(ctx context.Context, c *companionTree) (string, error) {
	s := r.step(ctx, "import companion", "repo", c.slug, "branch", c.spec.Branch)
	imported, err := r.importTree(ctx, c.mirror, c.slug, c.spec.Branch, c.base)
	if err != nil {
		return "", s.fail(err)
	}
	if !imported {
		s.done("no new commits", "imported", false)
		return c.head, nil
	}
	head, ok := c.mirror.resolve(ctx, "refs/heads/"+c.spec.Branch)
	if !ok {
		return "", s.fail(fmt.Errorf("companion %s: refs/heads/%s is missing after the import", c.spec.Project, c.spec.Branch))
	}
	c.head = head
	s.done(fmt.Sprintf("%s at %s, not pushed", c.spec.Branch, shortSHA(head)), "imported", true, "sha", head)
	return head, nil
}

// checkCompanion imports the companion checkout's commits into the
// companion mirror and checks the companion branch there
// (Mirror.CheckCompanion). Every push of the home branch of a companion
// task runs it first (publish): steps, the final push, CI-fix and
// merge-base runs, checkpoints. A violation is a *CompanionViolation, which
// fails the run before either tree is pushed. Without a companion, or
// without a companion change in this run, it does nothing.
func (r *Runner) checkCompanion(ctx context.Context) error {
	c := r.companion
	if c == nil {
		return nil
	}
	head, err := r.importCompanion(ctx, c)
	if err != nil || head == "" || head == c.checked {
		return err
	}
	s := r.step(ctx, "check companion", "repo", c.slug, "sha", head)
	paths, err := pathspec.Compile(c.spec.Paths)
	if err != nil {
		return s.fail(fmt.Errorf("companion %s: its allowed paths do not compile: %w", c.spec.Project, err))
	}
	if err := c.mirror.CheckCompanion(ctx, c.base, head, paths, companionMaxBytes); err != nil {
		if v, ok := AsCompanionViolation(err); ok {
			r.log().Info("companion change refused", "project", c.spec.Project, "path", v.Path, "reason", v.Reason, "commit", v.Commit)
		}
		return s.fail(err)
	}
	c.checked = head
	s.done(fmt.Sprintf("%s at %s is within the allowed paths", c.spec.Branch, shortSHA(head)), "sha", head)
	return nil
}

// companionChange commits the companion's work of the run. The push of
// the home branch right after it imports and checks it (checkCompanion);
// pushing the companion is not part of this phase.
func (run *implementRun) companionChange(ctx context.Context) error {
	c := run.companion
	if c == nil {
		return nil
	}
	return run.r.commitCompanion(ctx, c, commitMessage(run.answer.CommitMessage, run.issue, run.types))
}

// agentWorkspaceRoot is the agent container's workspace mount
// (controller.WorkspaceMountPath): the prompt names the companion checkout
// as the agent sees it, CompanionDir(agentWorkspaceRoot, slug).
const agentWorkspaceRoot = "/workspace"

// companionPrompt is the "Companion repository" section of the
// implementer's user prompt. The system prompts stay the same with and
// without a companion (prompt cache).
func companionPrompt(c *companionTree) string {
	var b strings.Builder
	b.WriteString("## Companion repository\n\n")
	fmt.Fprintf(&b, "This issue also changes the companion repository `%s`, in this run and this session. "+
		"Its checkout is `%s`, on the branch `%s`; edit it there, next to this repository.\n\n",
		c.spec.Project, CompanionDir(agentWorkspaceRoot, c.slug), c.spec.Branch)
	fmt.Fprintf(&b, "Allowed paths (gitignore-style): %s\n\n", inlineCodeList(c.spec.Paths))
	b.WriteString("Change only files under these paths. A change outside them, to CI configuration (`.gitlab-ci.yml`, `.gitlab/`), " +
		"or one that adds a symlink or a submodule fails the run, and neither repository is pushed.\n\n")
	b.WriteString("The companion's merge request lands only after the change in this repository is merged and released, " +
		"so the companion change may rely on it.\n\n")
	b.WriteString("Commit, push and open merge requests in neither checkout: the runner commits and pushes both, " +
		"as it does for this repository. If the issue needs no change in the companion, leave its checkout as it is.")
	return b.String()
}

// withCompanionPrompt puts the companion section at the head of the
// implementer's prompts, the fresh session's and this turn's, once the
// operator's inputs (CI failures, conflicts) are in: a CI fix or conflict
// turn replaces the turn's prompt. Fix rounds and steps that start fresh
// get it with the full prompt.
func (run *implementRun) withCompanionPrompt() {
	if run.companion == nil {
		return
	}
	section := companionPrompt(run.companion)
	run.fullPrompt = section + "\n\n" + run.fullPrompt
	run.inv.Prompt = section + "\n\n" + run.inv.Prompt
}

// routeForCompanion moves the implementer of a run with a companion to
// claude, the only engine that works on a second checkout (--add-dir): the
// role's claude fallback, else agents.implement (CompanionRole). The notice
// names the engine and model. Without a claude option the run fails before
// any agent call.
func (run *implementRun) routeForCompanion(ctx context.Context, role aiv1.RoleSpec) (aiv1.RoleSpec, []string, error) {
	c := run.companion
	if c == nil {
		return role, nil, nil
	}
	routed, via, err := run.spec.CompanionRole(role)
	if err == nil && via == aiv1.CompanionRoleOwn {
		return routed, nil, nil
	}
	s := run.r.step(ctx, "route implementer for the companion", "project", c.spec.Project, "engine", role.Engine, "model", role.Model)
	if err != nil {
		return role, nil, s.fail(fmt.Errorf("the companion repository %s needs a claude implementer: %w. Give the profile a claude fallback or agents.implement a claude engine, or remove the companion with `%s repo none`",
			c.spec.Project, err, run.wf.CommandPrefix))
	}
	from := "its fallback"
	if via == aiv1.CompanionRoleImplement {
		from = "`agents.implement`"
	}
	s.done(fmt.Sprintf("%s/%s (%s)", routed.Engine, routed.Model, via), "via", via, "routedEngine", routed.Engine, "routedModel", routed.Model)
	return routed, []string{fmt.Sprintf("↪️ The implementer runs on `%s/%s` (%s) instead of `%s/%s`: the companion repository `%s` needs claude.",
		routed.Engine, routed.Model, from, role.Engine, role.Model, c.spec.Project)}, nil
}
