package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// The companion repository of an issue (docs/specs/cross-repo-changes.spec.md,
// decisions 3 and 4): the milestone's "/agent repo <project> [merge]" lines,
// the issue's agent-repo label, and the AgentTask.spec.companion the
// operator writes from them for the runner.

// milestoneRepos reads the repo lines of a milestone description for its
// spec task: each line with the problem that makes it ignored (malformed,
// repeated, or no valid target of the project).
func (r *AgentProjectReconciler) milestoneRepos(spec aiv1.AgentProjectSpec, description string) []aiv1.MilestoneRepo {
	prefix := spec.Workflow.CommandPrefix
	lines := rules.Repos(description, prefix)
	if len(lines) == 0 {
		return nil
	}
	targets := r.crossRepoTargets(spec)
	out := make([]aiv1.MilestoneRepo, 0, len(lines))
	for _, l := range lines {
		if l.Problem != "" {
			out = append(out, aiv1.MilestoneRepo{Problem: l.Problem, Line: l.Raw})
			continue
		}
		t, ok := aiv1.FindCrossRepoTarget(targets, l.Project)
		if !ok {
			out = append(out, aiv1.MilestoneRepo{Project: l.Project, Merge: l.Merge, Line: l.Raw,
				Problem: fmt.Sprintf("`%s` is no valid companion target of this project; %s", l.Project, targetList(targets))})
			continue
		}
		out = append(out, aiv1.MilestoneRepo{Project: t.Project, Merge: l.Merge})
	}
	return out
}

// targetList names the project's valid companion targets.
func targetList(targets []aiv1.CrossRepoTarget) string {
	if len(targets) == 0 {
		return "it has none (`spec.crossRepo.targets`, allowed by the cluster's `--cross-repo-allowed-projects`)"
	}
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = "`" + t.Project + "`"
	}
	return "its targets are " + strings.Join(names, ", ")
}

// logRepoProblems logs the ignored repo lines of a milestone; without a
// tracking issue that is all that names them.
func logRepoProblems(ctx context.Context, milestone string, repos []aiv1.MilestoneRepo) {
	for _, rp := range repos {
		if rp.Problem != "" {
			log.FromContext(ctx).Info("ignoring a repo line of the milestone", "milestone", milestone, "line", rp.Line, "problem", rp.Problem)
		}
	}
}

// repoNotices names the milestone's ignored repo lines on the tracking
// issue, each once (status.autopilot.repoNotices).
func (a *autopilot) repoNotices(ctx context.Context) error {
	if a.ap.TrackingIssueIID == 0 {
		return nil // named once there is one
	}
	var kept, fresh []string
	var text []string
	for _, rp := range a.st.Spec.Repos {
		switch {
		case rp.Problem == "":
		case slices.Contains(a.ap.RepoNotices, rp.Line):
			kept = append(kept, rp.Line)
		case !slices.Contains(fresh, rp.Line):
			fresh = append(fresh, rp.Line)
			text = append(text, fmt.Sprintf("- `%s`: %s.", strings.ReplaceAll(rp.Line, "`", ""), rp.Problem))
		}
	}
	if len(fresh) > 0 {
		body := "⚠️ The milestone's companion repository lines below are ignored; no issue gets that companion from them:\n\n" + strings.Join(text, "\n")
		if err := a.comment(ctx, body); err != nil {
			return err
		}
		a.log.Info("named the ignored repo lines on the tracking issue", "lines", fresh)
	}
	// A fixed or removed line leaves the list: broken again, it is named again.
	a.ap.RepoNotices = append(kept, fresh...)
	return nil
}

// issueMilestone is what the companion rule needs of an issue's milestone.
type issueMilestone struct {
	description string
	// autopilot: the milestone has the autopilot marker.
	autopilot bool
}

// syncCompanion sets task.spec.companion from the issue's agent-repo label.
// The label counts for a valid target when the milestone has a repo line
// for it, or when an owner or the bot added it (its label event; the bot
// adds it only on an owner's "/agent repo" or from the plan). A label that
// does not count is ignored, and the issue is told once: the returned
// notice is to be posted once the task is saved.
func (r *AgentProjectReconciler) syncCompanion(ctx context.Context, gl gitlab.API, gate *commandGate, spec aiv1.AgentProjectSpec,
	is gitlab.Issue, task *aiv1.AgentTask, ms issueMilestone, owners []string) (string, error) {
	src := &task.Spec.Source
	label := ""
	for _, l := range is.Labels {
		if strings.HasPrefix(l, aiv1.RepoLabelPrefix) {
			label = l
			break
		}
	}
	if label == "" {
		task.Spec.Companion, src.CompanionNotice = nil, ""
		return "", nil
	}
	prefix := spec.Workflow.CommandPrefix
	project := strings.TrimPrefix(label, aiv1.RepoLabelPrefix)
	ignore := func(why string) string {
		task.Spec.Companion = nil
		if src.CompanionNotice == label {
			return ""
		}
		src.CompanionNotice = label
		log.FromContext(ctx).Info("ignoring the issue's companion label", "issue", is.IID, "label", label, "reason", why)
		return fmt.Sprintf("🤖 The label `%s` is ignored, the issue gets no companion repository from it: %s.", strings.ReplaceAll(label, "`", ""), why)
	}
	targets := r.crossRepoTargets(spec)
	target, ok := aiv1.FindCrossRepoTarget(targets, project)
	if !ok {
		return ignore(fmt.Sprintf("`%s` is no valid companion target of this project; %s", strings.ReplaceAll(project, "`", ""), targetList(targets))), nil
	}
	var line *rules.RepoLine
	for _, l := range rules.Repos(ms.description, prefix) {
		if l.Problem == "" && l.Project == aiv1.NormalizeProject(target.Project) {
			line = &l
			break
		}
	}
	if line == nil {
		by, err := r.labelAddedByOwner(ctx, gl, gate, spec, is.IID, label, owners)
		if err != nil {
			return "", err
		}
		if !by {
			return ignore(fmt.Sprintf("only a milestone line `%s repo %s` or an owner can give an issue a companion repository. "+
				"An owner can write `%s repo %s`, or set the label by hand", prefix, target.Project, prefix, target.Project)), nil
		}
	}
	src.CompanionNotice = ""
	task.Spec.Companion = &aiv1.CompanionSpec{
		Project: target.Project,
		Paths:   slices.Clone(target.Paths),
		Branch:  naming.CompanionBranch(spec.GitLab.Project, is.IID, is.Title),
		// All three switches; a companion an owner gave the issue outside
		// such a line is never merged by autopilot.
		AutoMerge: line != nil && line.Merge && ms.autopilot && target.AutoMerge,
	}
	return "", nil
}

// labelAddedByOwner reports whether the last time label was added to the
// issue, an owner or the bot added it.
func (r *AgentProjectReconciler) labelAddedByOwner(ctx context.Context, gl gitlab.API, gate *commandGate, spec aiv1.AgentProjectSpec, iid int64,
	label string, owners []string) (bool, error) {
	events, err := gl.ListIssueLabelEvents(ctx, spec.GitLab.Project, iid)
	if err != nil {
		return false, fmt.Errorf("listing the label events of issue #%d: %w", iid, err)
	}
	var by *gitlab.User
	for i := range events {
		if ev := events[i]; ev.Action == "add" && strings.EqualFold(ev.LabelName(), label) {
			by = &events[i].User
		}
	}
	switch {
	case by == nil:
		return false, nil
	case gate.botID != 0 && by.ID == gate.botID:
		return true, nil
	}
	return gate.isOwner(ctx, by.ID, owners)
}
