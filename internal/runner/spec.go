package runner

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

var keyMarker = regexp.MustCompile(`<!-- ai-operator:key=([a-z0-9][a-z0-9-]*) -->`)

// issueKey extracts the plan key the spec agent stored in an issue.
func issueKey(description string) string {
	if m := keyMarker.FindStringSubmatch(description); m != nil {
		return m[1]
	}
	return ""
}

// runSpec plans a milestone: it asks the spec agent for a set of issues and
// creates (or updates) them in GitLab as drafts.
func (r *Runner) runSpec(ctx context.Context, gl gitlab.API, task *aiv1.AgentTask, spec aiv1.AgentProjectSpec) (*Outcome, error) {
	if task.Spec.Milestone == nil {
		return nil, fmt.Errorf("spec task has no milestone")
	}
	glProject := spec.GitLab.Project
	milestone, err := gl.GetMilestone(ctx, glProject, task.Spec.Milestone.ID)
	if err != nil {
		return nil, fmt.Errorf("getting milestone: %w", err)
	}
	proj, err := gl.GetProject(ctx, glProject)
	if err != nil {
		return nil, fmt.Errorf("getting project: %w", err)
	}
	g := r.git()
	if err := g.Ensure(ctx, proj.HTTPURLToRepo); err != nil {
		return nil, err
	}
	if err := g.CheckoutBranch(ctx, proj.DefaultBranch, proj.DefaultBranch); err != nil {
		return nil, err
	}

	issues, err := gl.ListIssues(ctx, glProject, gitlab.ListIssuesOptions{Milestone: milestone.Title, State: "all"})
	if err != nil {
		return nil, fmt.Errorf("listing milestone issues: %w", err)
	}
	existing := make([]ExistingIssue, 0, len(issues))
	for _, is := range issues {
		existing = append(existing, ExistingIssue{IID: is.IID, Key: issueKey(is.Description), Title: is.Title, State: is.State, Labels: is.Labels})
	}

	st := r.loadState()
	role := spec.Agents.Spec
	inv := roleInvocation(r.repoDir(), role, specSystemPrompt, readOnlyTools, specSchema(spec.Agents.Profiles), true)
	inv.Resume = st.session("spec", role.Engine)
	prompt := buildSpecPrompt(milestone, existing, spec.Agents.Profiles, task.Status.Run.RunCount > 0)
	inv.Prompt = prompt
	res, err := r.invoke(ctx, role, inv, prompt)
	out := &Outcome{}
	if res != nil {
		out.CostUSD = res.CostUSD
	}
	if err != nil {
		return out, err
	}
	st.setSession("spec", role.Engine, res.SessionID)
	if err := r.saveState(st); err != nil {
		return out, err
	}

	var plan SpecPlan
	if err := res.Decode(&plan); err != nil {
		return out, fmt.Errorf("decoding plan: %w", err)
	}
	created, updated, skipped, err := applyPlan(ctx, gl, glProject, milestone, spec, plan, issues)
	if err != nil {
		return out, err
	}
	out.Result = aiv1.RunResultSucceeded
	out.Summary = fmt.Sprintf("%s\n\nIssues: %d created, %d updated, %d left untouched.", strings.TrimSpace(plan.Summary), created, updated, skipped)
	return out, nil
}

// applyPlan creates and updates the planned issues. Issues that have left the
// draft state (being worked on, done, or created by a human) are never
// touched again.
func applyPlan(ctx context.Context, gl gitlab.API, glProject string, m *gitlab.Milestone, spec aiv1.AgentProjectSpec, plan SpecPlan, issues []gitlab.Issue) (created, updated, skipped int, err error) {
	wf := spec.Workflow
	byIID := map[int64]*gitlab.Issue{}
	keyToIID := map[string]int64{}
	for i := range issues {
		is := &issues[i]
		byIID[is.IID] = is
		if k := issueKey(is.Description); k != "" {
			keyToIID[k] = is.IID
		}
	}

	type pending struct {
		item PlannedItem
		iid  int64
	}
	var unresolved []pending
	for _, item := range plan.Issues {
		item.Key = slugKey(item.Key, item.Title)
		iid := item.ExistingIID
		if iid == 0 {
			iid = keyToIID[item.Key]
		}
		desc, missing := renderIssue(item, keyToIID)
		if ex := byIID[iid]; ex != nil {
			if ex.State != "opened" || !ex.HasLabel(wf.DraftLabel) {
				skipped++
				keyToIID[item.Key] = iid
				continue
			}
			// A profile label set earlier (possibly by a human) is kept.
			var add []string
			if spec.ProfileOf(ex.Labels) == "" {
				add = profileLabels(spec, item)
			}
			if _, err := gl.UpdateIssue(ctx, glProject, iid, gitlab.UpdateIssueOptions{
				Title: item.Title, Description: desc, AddLabels: add,
			}); err != nil {
				return created, updated, skipped, fmt.Errorf("updating issue #%d: %w", iid, err)
			}
			updated++
		} else {
			is, err := gl.CreateIssue(ctx, glProject, gitlab.CreateIssueOptions{
				Title:       item.Title,
				Description: desc,
				Labels:      strings.Join(append([]string{wf.GeneratedLabel, wf.DraftLabel}, profileLabels(spec, item)...), ","),
				MilestoneID: m.ID,
			})
			if err != nil {
				return created, updated, skipped, fmt.Errorf("creating issue %q: %w", item.Title, err)
			}
			iid = is.IID
			created++
		}
		keyToIID[item.Key] = iid
		if missing {
			unresolved = append(unresolved, pending{item, iid})
		}
	}
	// Dependencies on issues planned later in the list are resolved now that
	// every issue has an IID.
	for _, p := range unresolved {
		desc, _ := renderIssue(p.item, keyToIID)
		if _, err := gl.UpdateIssue(ctx, glProject, p.iid, gitlab.UpdateIssueOptions{Description: desc}); err != nil {
			return created, updated, skipped, fmt.Errorf("linking dependencies of #%d: %w", p.iid, err)
		}
	}
	return created, updated, skipped, nil
}

var nonKey = regexp.MustCompile(`[^a-z0-9]+`)

func slugKey(key, title string) string {
	if key == "" {
		key = title
	}
	return strings.Trim(nonKey.ReplaceAllString(strings.ToLower(key), "-"), "-")
}

// renderIssue renders an issue description. It reports whether a dependency
// could not be resolved to an issue yet.
func renderIssue(item PlannedItem, keyToIID map[string]int64) (string, bool) {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(item.Description))
	missing := false
	if len(item.DependsOn) > 0 {
		b.WriteString("\n\n### Depends on\n\n")
		for _, dep := range item.DependsOn {
			dep = slugKey(dep, "")
			if iid, ok := keyToIID[dep]; ok {
				fmt.Fprintf(&b, "- #%d\n", iid)
			} else {
				fmt.Fprintf(&b, "- `%s` (not created yet)\n", dep)
				missing = true
			}
		}
	}
	if len(item.Questions) > 0 {
		b.WriteString("\n### Open questions\n\n")
		for i, q := range item.Questions {
			fmt.Fprintf(&b, "%d. %s\n", i+1, strings.TrimSpace(q))
		}
		b.WriteString("\n_Answer in a comment; the implementing agent reads the whole thread._\n")
	}
	fmt.Fprintf(&b, "\n<!-- ai-operator:key=%s -->\n", item.Key)
	return b.String(), missing
}

// profileLabels returns the label of the profile the planner picked. An
// unknown choice gets no label, so the issue falls back to agents.implement.
func profileLabels(spec aiv1.AgentProjectSpec, item PlannedItem) []string {
	if _, ok := spec.Profile(item.Profile); ok {
		return []string{spec.ProfileLabel(item.Profile)}
	}
	return nil
}
