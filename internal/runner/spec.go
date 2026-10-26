package runner

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/go-logr/logr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
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
func (r *Runner) runSpec(ctx context.Context, gl gitlab.API, task *aiv1.AgentTask, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) (*Outcome, error) {
	if task.Spec.Milestone == nil {
		return nil, fmt.Errorf("spec task has no milestone")
	}
	glProject := spec.GitLab.Project
	s := r.step(ctx, "read milestone", "milestoneID", task.Spec.Milestone.ID)
	milestone, err := gl.GetMilestone(ctx, glProject, task.Spec.Milestone.ID)
	if err != nil {
		return nil, s.fail(fmt.Errorf("getting milestone: %w", err))
	}
	// The owners line is for the operator; the planner could copy the
	// mentions into the issues it writes.
	milestone.Description = rules.Spec(milestone.Description, spec.Workflow.CommandPrefix)
	s.done(milestone.Title, "specBytes", len(milestone.Description))
	s = r.step(ctx, "read project")
	proj, err := gl.GetProject(ctx, glProject)
	if err != nil {
		return nil, s.fail(fmt.Errorf("getting project: %w", err))
	}
	s.done("default branch "+proj.DefaultBranch, "defaultBranch", proj.DefaultBranch)
	if err := r.syncWorkspace(ctx, proj.HTTPURLToRepo, proj.DefaultBranch, proj.DefaultBranch); err != nil {
		return nil, err
	}

	s = r.step(ctx, "list milestone issues")
	issues, err := gl.ListIssues(ctx, glProject, gitlab.ListIssuesOptions{Milestone: milestone.Title, State: "all"})
	if err != nil {
		return nil, s.fail(fmt.Errorf("listing milestone issues: %w", err))
	}
	existing := make([]ExistingIssue, 0, len(issues))
	for _, is := range issues {
		if marker.IsTrackingIssue(is.Description) {
			continue // the operator's autopilot tracking issue is not work
		}
		e := ExistingIssue{IID: is.IID, Key: issueKey(is.Description), Title: is.Title, State: is.State, Labels: is.Labels}
		if m := marker.RevisitOf.FindStringSubmatch(is.Description); m != nil {
			e.Revisits = m[1]
		}
		existing = append(existing, e)
	}
	s.done(fmt.Sprintf("%d issues", len(issues)), "issues", len(issues))

	st := r.loadState()
	role := spec.Agents.Spec
	inv := roleInvocation(role, specSystemPrompt, readOnlyTools, specSchema(spec.Agents.Profiles), true)
	inv.Role = "spec"
	inv.Resume = st.session("spec", role.Engine)
	prompt := buildSpecPrompt(milestone, existing, spec.Agents.Profiles, task.Status.Run.RunCount > 0)
	if task.Spec.Autopilot {
		prompt = autopilotSpecNote + "\n\n" + prompt
	}
	inv.Prompt = prompt
	s = r.step(ctx, "run spec agent", "engine", role.Engine, "model", role.Model, "resume", inv.Resume != "")
	res, err := r.invoke(ctx, role, inv, prompt)
	out := &Outcome{}
	if res != nil {
		out.CostUSD = res.CostUSD
	}
	if err != nil {
		return out, s.fail(err)
	}
	st.setSession("spec", role.Engine, res.SessionID)
	if err := r.saveState(st); err != nil {
		return out, s.fail(err)
	}

	var plan SpecPlan
	if err := res.Decode(&plan); err != nil {
		return out, s.fail(fmt.Errorf("decoding plan: %w", err))
	}
	s.done(agentResult(role, res, fmt.Sprintf("%d issues planned", len(plan.Issues))),
		"session", res.SessionID, "costUSD", res.CostUSD, "planned", len(plan.Issues))
	s = r.step(ctx, "apply plan", "planned", len(plan.Issues))
	created, updated, skipped, err := applyPlan(ctx, s.log, gl, glProject, milestone, spec, plan, issues,
		planComment{botID: project.Status.BotUserID, runID: r.run.id})
	if err != nil {
		return out, s.fail(err)
	}
	s.done(fmt.Sprintf("%d created, %d updated, %d left untouched", created, updated, skipped),
		"created", created, "updated", updated, "skipped", skipped)

	// The plan in dependency terms, for the autopilot queue.
	s = r.step(ctx, "record plan")
	after, err := gl.ListIssues(ctx, glProject, gitlab.ListIssuesOptions{Milestone: milestone.Title, State: "all"})
	if err != nil {
		return out, s.fail(fmt.Errorf("listing milestone issues: %w", err))
	}
	out.Plan = planOrder(plan, after)
	s.done(fmt.Sprintf("%d issues in plan order", len(out.Plan)), "issues", len(out.Plan))
	r.linkDependencies(ctx, gl, glProject, proj.ID, spec.Workflow, out.Plan, task.Status.Plan, after)
	out.Result = aiv1.RunResultSucceeded
	out.Summary = fmt.Sprintf("%s\n\nIssues: %d created, %d updated, %d left untouched.", strings.TrimSpace(plan.Summary), created, updated, skipped)
	return out, nil
}

// planOrder maps the plan onto the milestone's issues: plan order, with the
// dependencies as IIDs. Like applyPlan, an item is matched by its existing
// IID first, then by its key. Planned issues that could not be found are left
// out.
func planOrder(plan SpecPlan, issues []gitlab.Issue) []aiv1.PlannedIssue {
	inMilestone := map[int64]bool{}
	keyToIID := map[string]int64{}
	for _, is := range issues {
		inMilestone[is.IID] = true
		if k := issueKey(is.Description); k != "" {
			keyToIID[k] = is.IID
		}
	}
	iids := make([]int64, len(plan.Issues))
	for i, item := range plan.Issues {
		key := slugKey(item.Key, item.Title)
		iid := item.ExistingIID
		if !inMilestone[iid] {
			// Like applyPlan: an IID not in the milestone falls back to the
			// key, under which the issue was created.
			iid = keyToIID[key]
		}
		if inMilestone[iid] {
			iids[i] = iid
			keyToIID[key] = iid
		}
	}
	out := []aiv1.PlannedIssue{}
	for i, item := range plan.Issues {
		iid := iids[i]
		if iid == 0 {
			continue
		}
		p := aiv1.PlannedIssue{Key: slugKey(item.Key, item.Title), IID: iid, Title: item.Title}
		for _, dep := range item.DependsOn {
			if d, ok := keyToIID[slugKey(dep, "")]; ok && d != iid {
				p.DependsOn = append(p.DependsOn, d)
			}
		}
		out = append(out, p)
	}
	return out
}

// applyPlan creates and updates the planned issues. Issues that have left the
// draft state (being worked on, done, or created by a human) are never
// touched again.
//
// The planner's decisions for an issue go into a comment of the bot on it
// (planComment), with the reasons and alternatives, so they are discussed and
// revisited where the rest of the conversation is.
func applyPlan(ctx context.Context, log logr.Logger, gl gitlab.API, glProject string, m *gitlab.Milestone, spec aiv1.AgentProjectSpec, plan SpecPlan, issues []gitlab.Issue, pc planComment) (created, updated, skipped int, err error) {
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
		if byIID[iid] == nil {
			// No IID, or one not in the milestone: the key decides.
			iid = keyToIID[item.Key]
		}
		desc, missing := renderIssue(item, keyToIID)
		isNew := false
		if ex := byIID[iid]; ex != nil {
			// Drafts only: an issue that was handed over keeps its plan (and
			// its decisions, which a run may have superseded), also with
			// labels that are not scoped, where the draft label can stay.
			if ex.State != "opened" || !ex.HasLabel(wf.DraftLabel) || hasAnyLabel(ex, wf.ActiveLabels()) || marker.RevisitOf.MatchString(ex.Description) {
				skipped++
				keyToIID[item.Key] = iid
				log.Info("issue left untouched", "iid", iid, "key", item.Key, "reason", "no longer a draft")
				continue
			}
			// Profile and effort labels set earlier (possibly by a human)
			// are kept.
			var add []string
			if spec.ProfileOf(ex.Labels) == "" {
				add = profileLabels(spec, item)
			}
			if !hasLabelPrefix(ex.Labels, wf.EffortLabelPrefix) {
				add = append(add, effortLabels(spec, item)...)
			}
			if err := gitlab.RetryTransient(ctx, func() error {
				_, err := gl.UpdateIssue(ctx, glProject, iid, gitlab.UpdateIssueOptions{Title: item.Title, Description: desc, AddLabels: add})
				return err
			}); err != nil {
				return created, updated, skipped, fmt.Errorf("updating issue #%d: %w", iid, err)
			}
			updated++
			log.Info("issue updated", "iid", iid, "key", item.Key)
		} else {
			var is *gitlab.Issue
			retry := false
			if err := gitlab.RetryTransient(ctx, func() (err error) {
				// A create that failed transiently may have been applied: its
				// key marker finds it.
				if retry {
					if is, err = findPlanned(ctx, gl, glProject, m, item.Key); is != nil || err != nil {
						return err
					}
				}
				retry = true
				is, err = gl.CreateIssue(ctx, glProject, gitlab.CreateIssueOptions{
					Title:       item.Title,
					Description: desc,
					Labels:      strings.Join(slices.Concat([]string{wf.GeneratedLabel, wf.DraftLabel}, profileLabels(spec, item), effortLabels(spec, item)), ","),
					MilestoneID: m.ID,
				})
				isNew = err == nil
				return err
			}); err != nil {
				return created, updated, skipped, fmt.Errorf("creating issue %q: %w", item.Title, err)
			}
			iid = is.IID
			created++
			log.Info("issue created", "iid", iid, "key", item.Key, "profile", item.Profile, "effort", item.Effort)
		}
		// Decision IDs carry the IID, which is known now. A post that failed
		// transiently may have been stored: a retry looks for the note.
		if err := gitlab.RetryTransient(ctx, func() error {
			err := pc.post(ctx, gl, glProject, iid, isNew, item.Decisions, wf.CommandPrefix)
			isNew = false
			return err
		}); err != nil {
			return created, updated, skipped, err
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
		if err := gitlab.RetryTransient(ctx, func() error {
			_, err := gl.UpdateIssue(ctx, glProject, p.iid, gitlab.UpdateIssueOptions{Description: desc})
			return err
		}); err != nil {
			return created, updated, skipped, fmt.Errorf("linking dependencies of #%d: %w", p.iid, err)
		}
	}
	return created, updated, skipped, nil
}

// findPlanned returns the milestone's issue with the plan key, or nil.
func findPlanned(ctx context.Context, gl gitlab.API, glProject string, m *gitlab.Milestone, key string) (*gitlab.Issue, error) {
	issues, err := gl.ListIssues(ctx, glProject, gitlab.ListIssuesOptions{Milestone: m.Title, State: "all"})
	if err != nil {
		return nil, fmt.Errorf("looking for issue %q: %w", key, err)
	}
	for i := range issues {
		if k := issueKey(issues[i].Description); k != "" && k == key {
			return &issues[i], nil
		}
	}
	return nil, nil
}

var nonKey = regexp.MustCompile(`[^a-z0-9]+`)

func slugKey(key, title string) string {
	if key == "" {
		key = title
	}
	return strings.Trim(nonKey.ReplaceAllString(strings.ToLower(key), "-"), "-")
}

// planComment posts the planner's decisions for an issue as a comment of the
// bot.
type planComment struct {
	botID int64
	runID string
}

// post creates or updates the plan decisions comment of issue iid. Planning
// IDs are numbered from 1 again whenever the plan for a draft changes;
// nothing has been built on them yet. Without decisions no comment is
// created, and an existing one says there are none any more.
func (pc planComment) post(ctx context.Context, gl gitlab.API, glProject string, iid int64, isNew bool, ds []Decision, prefix string) error {
	if pc.botID == 0 {
		// Without the bot's ID the comment could not be found again (and
		// would not be trusted): post nothing rather than duplicates.
		return nil
	}
	numbered := numberDecisions(ds, iid, true, 1)
	var existing *gitlab.Note
	if !isNew {
		notes, err := gl.ListIssueNotes(ctx, glProject, iid)
		if err != nil {
			return fmt.Errorf("listing notes of issue #%d: %w", iid, err)
		}
		for i := range notes {
			if isPlanDecisions(notes[i], pc.botID) {
				existing = &notes[i]
				break
			}
		}
	}
	switch {
	case existing == nil && len(numbered) == 0:
		return nil
	case existing == nil:
		if _, err := gl.CreateIssueNote(ctx, glProject, iid, renderPlanDecisions(pc.runID, prefix, numbered)); err != nil {
			return fmt.Errorf("posting the decisions of #%d: %w", iid, err)
		}
	case decisionsOf(existing.Body) != decisionsOf(renderPlanDecisions("", prefix, numbered)):
		if _, err := gl.UpdateIssueNote(ctx, glProject, iid, existing.ID, renderPlanDecisions(pc.runID, prefix, numbered)); err != nil {
			return fmt.Errorf("updating the decisions of #%d: %w", iid, err)
		}
	}
	return nil
}

// decisionsOf is a plan decisions comment without its first line (which names
// the run), to tell whether the decisions changed.
func decisionsOf(body string) string {
	_, rest, _ := strings.Cut(body, "\n")
	return strings.TrimSpace(rest)
}

func hasAnyLabel(is *gitlab.Issue, labels []string) bool {
	for _, l := range labels {
		if is.HasLabel(l) {
			return true
		}
	}
	return false
}

// renderIssue renders the description of a planned issue. It reports whether
// it must be rendered again once every issue has an IID: a dependency could
// not be resolved.
func renderIssue(item PlannedItem, keyToIID map[string]int64) (string, bool) {
	var b strings.Builder
	// The planner's text must not carry the operator's markers.
	b.WriteString(strings.ReplaceAll(strings.TrimSpace(item.Description), "<!-- ai-operator:", "<!-- ai-operator :"))
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
	if len(item.Decisions) > 0 {
		b.WriteString("\n_The planning agent's decisions are in a comment below._\n")
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

// effortLabels returns the label of the effort the planner picked, at most
// agents.maxEffort. No or an unknown choice, or a profile on an engine
// without efforts, gets no label: the profile's effort applies.
func effortLabels(spec aiv1.AgentProjectSpec, item PlannedItem) []string {
	if !aiv1.ValidEffort(item.Effort) {
		return nil
	}
	// Only claude has an effort setting.
	role, _ := spec.ImplementRole(profileLabels(spec, item))
	if role.Engine != aiv1.EngineClaude {
		return nil
	}
	effort, _ := spec.CapEffort(item.Effort)
	return []string{spec.EffortLabel(effort)}
}

func hasLabelPrefix(labels []string, prefix string) bool {
	return slices.ContainsFunc(labels, func(l string) bool { return strings.HasPrefix(l, prefix) })
}

// linkDependencies mirrors the plan's dependencies as GitLab issue links, so
// GitLab shows them on the issues and boards: "is blocked by" where the
// instance has blocking links (Premium), "relates to" otherwise (Free). Only
// draft issues are touched. The links the operator made are recorded in the
// plan (Linked), and only those are removed when the plan drops a
// dependency: links humans made, or removed, are theirs. It is best effort:
// the plan is applied already, and the description lists the dependencies.
func (r *Runner) linkDependencies(ctx context.Context, gl gitlab.API, glProject string, projectID int64, wf aiv1.WorkflowSpec, plan, prev []aiv1.PlannedIssue, issues []gitlab.Issue) {
	s := r.step(ctx, "link dependencies")
	res := syncLinks(ctx, s.log, gl, glProject, projectID, wf, plan, prev, issues)
	result := fmt.Sprintf("%d created, %d removed", res.created, res.removed)
	if res.created > 0 {
		result += " (" + res.linkType + ")"
	}
	if len(res.errs) > 0 {
		_ = s.fail(fmt.Errorf("%s, failed: %s", result, strings.Join(res.errs, "; ")))
		return
	}
	s.done(result, "created", res.created, "removed", res.removed, "type", res.linkType)
}

type linkResult struct {
	created, removed int
	linkType         string
	errs             []string
}

// syncLinks creates and removes the links and records them in plan[i].Linked.
func syncLinks(ctx context.Context, log logr.Logger, gl gitlab.API, glProject string, projectID int64, wf aiv1.WorkflowSpec, plan, prev []aiv1.PlannedIssue, issues []gitlab.Issue) linkResult {
	byIID := map[int64]gitlab.Issue{}
	for _, is := range issues {
		byIID[is.IID] = is
	}
	before := map[int64]aiv1.PlannedIssue{}
	for _, p := range prev {
		before[p.IID] = p
	}
	res := linkResult{linkType: gitlab.LinkIsBlockedBy}
	for i := range plan {
		p := &plan[i]
		old := before[p.IID]
		if is, ok := byIID[p.IID]; !ok || !plannedDraft(is, wf) {
			p.Linked = old.Linked // not touched; what was linked stays recorded
			continue
		}
		// Dependencies linked before stay linked (or unlinked, if a human
		// removed the link); those the plan dropped are unlinked.
		var stale, wanted []int64
		for _, d := range old.Linked {
			if !slices.Contains(p.DependsOn, d) {
				stale = append(stale, d)
			}
		}
		for _, d := range p.DependsOn {
			if slices.Contains(old.Linked, d) {
				p.Linked = append(p.Linked, d)
			} else {
				wanted = append(wanted, d)
			}
		}
		if len(wanted) == 0 && len(stale) == 0 {
			continue
		}
		links, err := gl.ListIssueLinks(ctx, glProject, p.IID)
		if err != nil {
			res.errs = append(res.errs, fmt.Sprintf("#%d: %v", p.IID, err))
			p.Linked = append(p.Linked, stale...) // try again next time
			continue
		}
		linked := map[int64]gitlab.LinkedIssue{}
		for _, l := range links {
			if l.ProjectID == projectID {
				linked[l.IID] = l
			}
		}
		for _, d := range wanted {
			if _, ok := linked[d]; ok {
				continue // a human linked it already: their link
			}
			err := gl.CreateIssueLink(ctx, glProject, p.IID, d, res.linkType)
			if res.linkType == gitlab.LinkIsBlockedBy && noBlockingLinks(err) {
				log.Info("blocking issue links not available, using relates_to", "error", err.Error())
				res.linkType = gitlab.LinkRelatesTo
				err = gl.CreateIssueLink(ctx, glProject, p.IID, d, res.linkType)
			}
			switch {
			case gitlab.StatusCode(err) == 409: // linked meanwhile, not by the operator
			case err != nil:
				res.errs = append(res.errs, fmt.Sprintf("#%d → #%d: %v", p.IID, d, err))
			default:
				res.created++
				p.Linked = append(p.Linked, d)
			}
		}
		for _, d := range stale {
			l, ok := linked[d]
			if !ok {
				continue // removed by a human already
			}
			if err := gl.DeleteIssueLink(ctx, glProject, p.IID, l.IssueLinkID); err != nil && !gitlab.IsNotFound(err) {
				res.errs = append(res.errs, fmt.Sprintf("unlinking #%d from #%d: %v", p.IID, d, err))
				p.Linked = append(p.Linked, d) // try again next time
				continue
			}
			res.removed++
		}
	}
	return res
}

// noBlockingLinks reports whether GitLab refused a blocking link because the
// instance has none (GitLab Free), as opposed to, say, a missing permission.
func noBlockingLinks(err error) bool {
	var gerr *gitlab.Error
	if !errors.As(err, &gerr) {
		return false
	}
	switch gerr.StatusCode {
	case 400, 422:
		return true
	case 403:
		body := strings.ToLower(gerr.Body)
		return strings.Contains(body, "blocked issues") || strings.Contains(body, "subscription") || strings.Contains(body, "licen")
	}
	return false
}

// plannedDraft reports whether a planned issue is still a draft the plan may
// change: open, with the draft label and no active label, not a follow-up.
func plannedDraft(is gitlab.Issue, wf aiv1.WorkflowSpec) bool {
	if is.State != "opened" || !is.HasLabel(wf.DraftLabel) || marker.RevisitOf.MatchString(is.Description) {
		return false
	}
	for _, l := range wf.ActiveLabels() {
		if is.HasLabel(l) {
			return false
		}
	}
	return true
}
