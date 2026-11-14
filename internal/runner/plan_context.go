package runner

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// planGitLab is all of GitLab the plan role gets: reads only, so the plan
// pod cannot write to GitLab. A test pins that every method is a Get or List.
type planGitLab interface {
	GetProject(ctx context.Context, project string) (*gitlab.Project, error)
	ListMilestones(ctx context.Context, project string) ([]gitlab.Milestone, error)
	ListClosedMilestones(ctx context.Context, project string, maxPages int) ([]gitlab.Milestone, error)
	ListIssues(ctx context.Context, project string, opts gitlab.ListIssuesOptions) ([]gitlab.Issue, error)
	GetMergeRequest(ctx context.Context, project string, iid int64) (*gitlab.MergeRequest, error)
}

// planTools reads code; find and git grep are left out: -exec and -O run
// any command (Glob and Grep do their job).
var (
	planTools  = append(slices.Clone(readOnlyTools), "Bash(cat:*)", "Bash(wc:*)", "Bash(grep:*)", "Bash(git ls-files:*)")
	planDenied = []string{"Bash(git push:*)", "Edit", "Write", "NotebookEdit"}
)

const planContextIssues, planContextClosed, planContextMax = 50, 5, 16 << 10

// planContext is the project's milestones and open issues as markdown for
// the plan role's prompt, at most planContextMax bytes.
func planContext(ctx context.Context, gl planGitLab, project string) (string, error) {
	p, err := gl.GetProject(ctx, project)
	if err != nil {
		return "", err
	}
	open, err := gl.ListMilestones(ctx, project)
	if err != nil {
		return "", err
	}
	closed, err := gl.ListClosedMilestones(ctx, project, 1)
	if err != nil {
		return "", err
	}
	issues, err := gl.ListIssues(ctx, project, gitlab.ListIssuesOptions{MaxPages: 1})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# GitLab project %s (default branch %s)\n\n## Open milestones\n\n", p.PathWithNamespace, p.DefaultBranch)
	for _, m := range open {
		fmt.Fprintf(&b, "- %%%d %s\n", m.IID, m.Title)
		lines := slices.DeleteFunc(strings.Split(m.Description, "\n"), func(l string) bool { l = strings.TrimSpace(l); return l == "" || l[0] == '/' })
		for _, l := range lines[:min(len(lines), 3)] {
			fmt.Fprintf(&b, "  > %s\n", strings.TrimSpace(l))
		}
	}
	slices.SortFunc(closed, func(x, y gitlab.Milestone) int { return cmp.Compare(y.IID, x.IID) })
	b.WriteString("\n## Newest closed milestones\n\n")
	for _, m := range closed[:min(len(closed), planContextClosed)] {
		fmt.Fprintf(&b, "- %%%d %s\n", m.IID, m.Title)
	}
	slices.SortFunc(issues, func(x, y gitlab.Issue) int { return cmp.Compare(y.IID, x.IID) })
	b.WriteString("\n## Open issues, newest first\n\n")
	for i, is := range issues {
		if i == planContextIssues {
			fmt.Fprintf(&b, "- … and %d more\n", len(issues)-i)
			break
		}
		ms := "none"
		if is.Milestone != nil {
			ms = is.Milestone.Title
		}
		fmt.Fprintf(&b, "- #%d %s (milestone: %s; labels: %s)\n", is.IID, is.Title, ms, strings.Join(is.Labels, ", "))
	}
	if b.Len() <= planContextMax {
		return b.String(), nil
	}
	return strings.ToValidUTF8(b.String()[:planContextMax], "") + "\n\n(cut at 16 KiB)\n", nil
}
