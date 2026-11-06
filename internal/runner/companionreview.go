package runner

import (
	"context"
	"fmt"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// The reviewers of a task with a companion change see both trees in one
// input, under tree headers (docs/specs/cross-repo-changes.spec.md,
// decision 6): the home tree first, then the companion. The security
// reviewer gets a note on what the companion means for production. Without
// a companion change the input is the home tree's alone, as before.

// reviewInput is what the reviewers see: the home tree's diff against its
// default branch and, with a companion change, the companion's.
type reviewInput struct {
	// project is the home project, base its default branch.
	project, base string
	stat, diff    string
	// companion is nil without a companion change.
	companion *companionReview
}

// companionReview is the companion tree's part of the reviewer input.
type companionReview struct {
	project string
	paths   []string
	// dir is the companion checkout as the agent sees it, base its default
	// branch.
	dir, base  string
	stat, diff string
}

// reviewInput reads the diffs of both trees. A companion tree without
// changes leaves the input the home tree's alone.
func (run *implementRun) reviewInput(ctx context.Context) (reviewInput, error) {
	r := run.r
	diff, stat, err := r.Agent.Diff(ctx, homeTree, run.base)
	if err != nil {
		return reviewInput{}, err
	}
	in := reviewInput{project: run.glProject, base: run.base, stat: stat, diff: diff}
	c := run.companion
	if c == nil {
		return in, nil
	}
	cdiff, cstat, err := r.companionDiff(ctx, c)
	if err != nil {
		return reviewInput{}, fmt.Errorf("companion %s: %w", c.spec.Project, err)
	}
	if strings.TrimSpace(cdiff) != "" {
		in.companion = &companionReview{project: c.spec.Project, paths: c.spec.Paths,
			dir: CompanionDir(agentWorkspaceRoot, c.slug), base: c.base, stat: cstat, diff: cdiff}
	}
	return in, nil
}

// buildTreesReviewPrompt renders the diffs of both trees under their tree
// headers. maxInline caps the combined diff: when both together exceed it,
// both trees are listed by their stat only, so the companion is never cut
// off silently.
func buildTreesReviewPrompt(b *strings.Builder, in reviewInput, maxInline int) {
	c := in.companion
	inline := len(in.diff)+len(c.diff) <= maxInline
	fmt.Fprintf(b, "## Tree: home (%s)\n\n", inertText(in.project))
	fmt.Fprintf(b, "### Changed files\n\n```\n%s\n```\n\n", in.stat)
	if inline {
		fmt.Fprintf(b, "### Diff\n\n```diff\n%s\n```\n\n", in.diff)
	} else {
		fmt.Fprintf(b, "The diffs of both trees are too large to include. Inspect this one with `git diff origin/%s...HEAD -- <file>`.\n\n", in.base)
	}
	fmt.Fprintf(b, "## Tree: companion (%s, allowed paths: %s; lands after the home change is released)\n\n",
		inertText(c.project), inlineCodeList(c.paths))
	fmt.Fprintf(b, "Its checkout is `%s`.\n\n", c.dir)
	fmt.Fprintf(b, "### Changed files\n\n```\n%s\n```\n", c.stat)
	if inline {
		fmt.Fprintf(b, "\n### Diff\n\n```diff\n%s\n```\n", c.diff)
	} else {
		fmt.Fprintf(b, "\nThe diffs of both trees are too large to include. Inspect this one with `git -C %s diff origin/%s...HEAD -- <file>`.\n", c.dir, c.base)
	}
}

// securityCompanionNote is the security reviewer's extra note when the
// change has a companion. It goes into the user prompt: the system prompts
// stay the same (prompt cache).
func securityCompanionNote(c *companionReview) string {
	return fmt.Sprintf("\n## Companion repository\n\n"+
		"The companion repository `%s` is applied to production by Flux when its merge request is merged. "+
		"In its diff, flag secrets and plaintext credentials (encrypted SealedSecrets are fine), RBAC widening "+
		"(new or broader roles, cluster roles, bindings or service account permissions), and changes that would break "+
		"when applied before the home change is released (fields or values the running release does not know yet).\n",
		inertText(c.project))
}

// reviewerNote is the extra text of a reviewer's prompt: the companion note
// for the security reviewer of a change with a companion, else nothing.
func reviewerNote(kind aiv1.ReviewerKind, in reviewInput) string {
	if kind != aiv1.ReviewerSecurity || in.companion == nil {
		return ""
	}
	return securityCompanionNote(in.companion)
}

// reviewedHeads names the heads of a review key (aiv1.ReviewHeads) for the
// run's summary.
func reviewedHeads(key string) string {
	home, companion, ok := strings.Cut(key, "+")
	if !ok {
		return "`" + shortSHA(home) + "`"
	}
	return fmt.Sprintf("`%s` and the companion's `%s`", shortSHA(home), shortSHA(companion))
}
