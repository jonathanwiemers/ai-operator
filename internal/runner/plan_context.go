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
// pod cannot write to GitLab. A test pins that every method is a Get, List
// or Download.
type planGitLab interface {
	GetProject(ctx context.Context, project string) (*gitlab.Project, error)
	ListMilestones(ctx context.Context, project string) ([]gitlab.Milestone, error)
	ListClosedMilestones(ctx context.Context, project string, maxPages int) ([]gitlab.Milestone, error)
	GetMilestoneByIID(ctx context.Context, project string, iid int64) (*gitlab.Milestone, error)
	ListIssues(ctx context.Context, project string, opts gitlab.ListIssuesOptions) ([]gitlab.Issue, error)
	GetMergeRequest(ctx context.Context, project string, iid int64) (*gitlab.MergeRequest, error)
	DownloadUpload(ctx context.Context, project, secret, filename string, max int64) ([]byte, error)
}

// planTools reads code; find and git grep are left out: -exec and -O run
// any command (Glob and Grep do their job).
var (
	planTools  = append(slices.Clone(readOnlyTools), "Bash(cat:*)", "Bash(wc:*)", "Bash(grep:*)", "Bash(git ls-files:*)")
	planDenied = []string{"Bash(git push:*)", "Edit", "Write", "NotebookEdit"}
)

const planContextIssues, planContextClosed, planContextMax = 50, 5, 16 << 10

// planContext is the project's milestones and open issues as markdown for
// the plan role's prompt, at most planContextMax bytes. infos are the
// milestone files of the turn: the milestones are an index of them, or
// only their numbers and titles without files.
func planContext(ctx context.Context, gl planGitLab, project string, infos []planMilestoneInfo) (string, error) {
	p, err := gl.GetProject(ctx, project)
	if err != nil {
		return "", err
	}
	infos = slices.DeleteFunc(slices.Clone(infos), func(i planMilestoneInfo) bool { return i.Named })
	if len(infos) == 0 {
		if infos, err = planContextTitles(ctx, gl, project); err != nil {
			return "", err
		}
	}
	issues, err := gl.ListIssues(ctx, project, gitlab.ListIssuesOptions{MaxPages: 1})
	if err != nil {
		return "", err
	}
	// Open in queue order, then the others by IID; closed newest first.
	rank := func(m planMilestoneInfo) [2]int64 {
		switch {
		case !m.Open:
			return [2]int64{2, -m.IID}
		case m.Pos < 0:
			return [2]int64{1, m.IID}
		}
		return [2]int64{0, int64(m.Pos)}
	}
	slices.SortFunc(infos, func(x, y planMilestoneInfo) int {
		rx, ry := rank(x), rank(y)
		return cmp.Or(cmp.Compare(rx[0], ry[0]), cmp.Compare(rx[1], ry[1]))
	})
	var b strings.Builder
	fmt.Fprintf(&b, "# GitLab project %s (default branch %s)\n\n", p.PathWithNamespace, p.DefaultBranch)
	for _, open := range []bool{true, false} {
		b.WriteString(map[bool]string{true: "## Open milestones\n\n", false: "\n## Newest closed milestones\n\n"}[open])
		for _, m := range infos {
			if m.Open == open {
				b.WriteString(planIndexLine(m))
			}
		}
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

// planIndexLine is milestone m's line in the index: with a file, its state,
// queue position, Goal line and path.
func planIndexLine(m planMilestoneInfo) string {
	parts := []string{fmt.Sprintf("%%%d %s", m.IID, m.Title)}
	if m.Path != "" {
		parts = append(parts, m.State, m.Queue)
		if m.Goal != "" {
			parts = append(parts, "Goal: "+m.Goal)
		}
		parts = append(parts, m.Path)
	}
	return "- " + strings.Join(parts, " · ") + "\n"
}

// planContextTitles are the open and the planContextClosed newest closed
// milestones without files, for an index of numbers and titles.
func planContextTitles(ctx context.Context, gl planGitLab, project string) ([]planMilestoneInfo, error) {
	open, err := gl.ListMilestones(ctx, project)
	if err != nil {
		return nil, err
	}
	closed, err := gl.ListClosedMilestones(ctx, project, 1)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(closed, func(x, y gitlab.Milestone) int { return cmp.Compare(y.IID, x.IID) })
	var infos []planMilestoneInfo
	for _, m := range append(open, closed[:min(len(closed), planContextClosed)]...) {
		infos = append(infos, planMilestoneInfo{IID: m.IID, Title: m.Title, Open: m.State != "closed", Pos: -1})
	}
	return infos, nil
}
