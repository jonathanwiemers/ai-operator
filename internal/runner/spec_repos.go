package runner

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/go-logr/logr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/pathspec"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// The planner's side of companion repositories (docs/specs/
// cross-repo-changes.spec.md, decision 4): it sees the milestone's targets
// and sets "repo" on the plan items that change one, and applyPlan gives
// those issues the label agent-repo::<project>.

// maxTreeEntries caps the files the planner is shown of all companion
// trees together.
const maxTreeEntries = 200

// planTarget is a companion repository the planner may set on plan items.
type planTarget struct {
	// Project is the companion's full path, as the target names it.
	Project string
	// Paths is the target's allow-list.
	Paths []string
}

// planTargets returns the milestone's companion repositories: its repo
// lines that count (the operator checked them against the valid targets
// and left no problem) with their target in spec.crossRepo.
func planTargets(task *aiv1.AgentTask, spec aiv1.AgentProjectSpec) []planTarget {
	if spec.CrossRepo == nil {
		return nil
	}
	var out []planTarget
	for _, rp := range task.Spec.Repos {
		if rp.Problem != "" || rp.Project == "" {
			continue
		}
		t, ok := aiv1.FindCrossRepoTarget(spec.CrossRepo.Targets, rp.Project)
		if !ok || slices.ContainsFunc(out, func(o planTarget) bool { return aiv1.NormalizeProject(o.Project) == aiv1.NormalizeProject(t.Project) }) {
			continue
		}
		out = append(out, planTarget{Project: t.Project, Paths: slices.Clone(t.Paths)})
	}
	return out
}

// projects returns the targets' projects.
func targetProjects(targets []planTarget) []string {
	out := make([]string, len(targets))
	for i, t := range targets {
		out[i] = t.Project
	}
	return out
}

// targetTree is what the planner is shown of a target's default branch.
type targetTree struct {
	planTarget
	Branch string
	// Files are the files under the allowed paths, sorted, at most what
	// is left of maxTreeEntries; More counts the ones left out.
	Files []string
	More  int
	// Problem says why there is no tree.
	Problem string
}

// readTargetTrees lists the files under the allowed paths of each target,
// maxTreeEntries in total. The tree is best effort: a target whose tree
// cannot be read is shown with its paths and the problem.
//
// gl carries the home project's token. The companion's own token
// (tokenSecretRef) reaches the pod with the companion checkout; until
// then the home token reads the tree, which works because the bot is a
// Maintainer of the group both projects are in, and it only reads.
func readTargetTrees(ctx context.Context, log logr.Logger, gl gitlab.API, targets []planTarget) []targetTree {
	out := make([]targetTree, 0, len(targets))
	left := maxTreeEntries
	for _, t := range targets {
		tt := targetTree{planTarget: t}
		files, branch, err := allowedFiles(ctx, gl, t)
		tt.Branch = branch
		if err != nil {
			log.Info("could not read a companion's tree", "project", t.Project, "error", err.Error())
			tt.Problem = textutil.Truncate(err.Error(), 300)
		}
		n := min(len(files), left)
		tt.Files, tt.More = files[:n], len(files)-n
		left -= n
		out = append(out, tt)
	}
	return out
}

// allowedFiles lists the files of the target's default branch that its
// paths allow, sorted, and names the branch.
func allowedFiles(ctx context.Context, gl gitlab.API, t planTarget) ([]string, string, error) {
	m, err := pathspec.Compile(t.Paths)
	if err != nil {
		return nil, "", err
	}
	proj, err := gl.GetProject(ctx, t.Project)
	if err != nil {
		return nil, "", fmt.Errorf("reading the project: %w", err)
	}
	seen := map[string]bool{}
	var files []string
	for _, dir := range treeDirs(t.Paths) {
		entries, err := gl.ListRepositoryTree(ctx, t.Project, dir, proj.DefaultBranch, true)
		if gitlab.IsNotFound(err) {
			continue // nothing there yet
		}
		if err != nil {
			return files, proj.DefaultBranch, fmt.Errorf("listing %q: %w", "/"+dir, err)
		}
		for _, e := range entries {
			if e.Type == "blob" && !seen[e.Path] && m.Match(e.Path) {
				seen[e.Path] = true
				files = append(files, e.Path)
			}
		}
	}
	slices.Sort(files)
	return files, proj.DefaultBranch, nil
}

// treeDirs returns the directories to list for the patterns: each
// pattern's literal prefix, without those inside another one. "" is the
// root.
func treeDirs(patterns []string) []string {
	var dirs []string
	for _, p := range patterns {
		dirs = append(dirs, literalDir(p))
	}
	slices.Sort(dirs)
	var out []string
	for _, d := range slices.Compact(dirs) {
		if !slices.ContainsFunc(out, func(o string) bool { return o == "" || strings.HasPrefix(d, o+"/") }) {
			out = append(out, d)
		}
	}
	return out
}

// literalDir is the directory a pattern's matches are all under: its
// segments up to the first one with a wildcard. The last segment of a
// pattern without one may be a file, so its parent is listed. An
// unanchored pattern matches at any depth: the root.
func literalDir(p string) string {
	body := strings.TrimSuffix(strings.TrimPrefix(p, "/"), "/")
	if !strings.HasPrefix(p, "/") && !strings.Contains(body, "/") {
		return ""
	}
	segs := strings.Split(body, "/")
	n := 0
	for n < len(segs) && !strings.ContainsAny(segs[n], "*?") {
		n++
	}
	if n == len(segs) && !strings.HasSuffix(p, "/") {
		n--
	}
	return strings.Join(segs[:n], "/")
}

// companionSection is the "Companion repositories" section of the spec
// prompt.
func companionSection(trees []targetTree) string {
	var b strings.Builder
	b.WriteString("## Companion repositories\n\n")
	b.WriteString("Besides this project, an issue of this milestone may change one companion repository below, in the same run. " +
		"Set `repo` on an item to the companion's project, exactly as named below, only when that issue must change the companion; " +
		"leave it empty otherwise. The companion change lands after the home change is released.\n")
	for _, t := range trees {
		fmt.Fprintf(&b, "\n### %s\n\nAllowed paths: %s\n", t.Project, inlineCodeList(t.Paths))
		switch {
		case t.Problem != "" && len(t.Files) == 0:
			fmt.Fprintf(&b, "\nIts files could not be listed (%s).\n", inertText(t.Problem))
			continue
		case len(t.Files) == 0 && t.More == 0:
			fmt.Fprintf(&b, "\nNo files under the allowed paths on `%s` yet.\n", t.Branch)
			continue
		}
		fmt.Fprintf(&b, "\nFiles under the allowed paths on `%s`:\n\n```\n", t.Branch)
		for _, f := range t.Files {
			b.WriteString(f + "\n")
		}
		if t.More > 0 {
			fmt.Fprintf(&b, "… %d more\n", t.More)
		}
		b.WriteString("```\n")
		if t.Problem != "" {
			fmt.Fprintf(&b, "\nThe list is incomplete (%s).\n", inertText(t.Problem))
		}
	}
	return b.String()
}

func inlineCodeList(items []string) string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = "`" + strings.ReplaceAll(it, "`", "") + "`"
	}
	return strings.Join(out, ", ")
}

// inertText makes text safe for one line of markdown.
func inertText(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "`", "")), " ")
}

// planRepoMarker records in a planned issue's description the companion
// the plan gave it, so a re-plan tells its own label from an owner's.
var planRepoMarker = regexp.MustCompile(`<!-- ai-operator:plan-repo=(\S+) -->`)

// resolveRepo checks the item's repo against the milestone's companions:
// one of them, it is set to the companion's project as named there;
// otherwise it is cleared, and the returned line for the plan comment says
// so.
func resolveRepo(item *PlannedItem, companions []string) string {
	repo := strings.TrimSpace(item.Repo)
	item.Repo = ""
	if repo == "" {
		return ""
	}
	if p, ok := rules.ProjectPath(repo); ok {
		for _, c := range companions {
			if aiv1.NormalizeProject(c) == p {
				item.Repo = c
				return ""
			}
		}
	}
	known := "it has none"
	if len(companions) > 0 {
		known = "they are " + inlineCodeList(companions)
	}
	return fmt.Sprintf("⚠️ The plan named `%s` as the companion repository, which is none of the milestone's (%s): the issue gets no companion from the plan.",
		textutil.Truncate(inertText(repo), 100), known)
}

// repoLabel returns the issue's agent-repo label, "" without one.
func repoLabel(labels []string) string {
	for _, l := range labels {
		if strings.HasPrefix(l, aiv1.RepoLabelPrefix) {
			return l
		}
	}
	return ""
}

// planSetRepoLabel reports whether label is the agent-repo label the plan
// gave the issue (its description's plan-repo marker), and no human added
// it again since: the last time the label was added, the bot added it.
func planSetRepoLabel(ctx context.Context, gl gitlab.API, glProject string, is *gitlab.Issue, label string, botID int64) (bool, error) {
	m := planRepoMarker.FindStringSubmatch(is.Description)
	if m == nil || aiv1.NormalizeProject(m[1]) != aiv1.NormalizeProject(strings.TrimPrefix(label, aiv1.RepoLabelPrefix)) {
		return false, nil
	}
	if botID == 0 {
		return true, nil // the marker is all there is to go by
	}
	var events []gitlab.LabelEvent
	if err := gitlab.RetryTransient(ctx, func() (err error) {
		events, err = gl.ListIssueLabelEvents(ctx, glProject, is.IID)
		return err
	}); err != nil {
		return false, fmt.Errorf("listing the label events of #%d: %w", is.IID, err)
	}
	var by int64
	for _, ev := range events {
		if ev.Action == "add" && strings.EqualFold(ev.LabelName(), label) {
			by = ev.User.ID
		}
	}
	return by == 0 || by == botID, nil
}

// repoLabels is the label of the plan item's companion, for a new issue.
func repoLabels(item PlannedItem) []string {
	if item.Repo == "" {
		return nil
	}
	return []string{aiv1.RepoLabelPrefix + item.Repo}
}

// repoLabelChange returns the labels to add and remove on a planned draft
// so its agent-repo label follows the plan's repo: a label the plan set is
// replaced or removed, a label an owner set stays. planOwned reports
// whether the issue's label afterwards is the plan's: its description
// records it.
func repoLabelChange(ctx context.Context, gl gitlab.API, glProject string, is *gitlab.Issue, repo string, botID int64) (add, remove []string, planOwned bool, err error) {
	want := ""
	if repo != "" {
		want = aiv1.RepoLabelPrefix + repo
	}
	cur := repoLabel(is.Labels)
	switch {
	case cur == "" && want == "":
		return nil, nil, false, nil
	case cur == "":
		return []string{want}, nil, true, nil
	}
	planSet, err := planSetRepoLabel(ctx, gl, glProject, is, cur, botID)
	switch {
	case err != nil:
		return nil, nil, false, err
	case strings.EqualFold(cur, want):
		return nil, nil, planSet, nil
	case !planSet:
		return nil, nil, false, nil // an owner's label stays
	case want == "":
		return nil, []string{cur}, false, nil
	}
	return []string{want}, []string{cur}, true, nil
}
