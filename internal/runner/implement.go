package runner

import (
	"context"
	"fmt"
	"strings"
	"sync"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
)

// runImplement implements an issue: implement -> review -> fix (bounded) ->
// push -> merge request -> report back on GitLab.
func (r *Runner) runImplement(ctx context.Context, gl gitlab.API, task *aiv1.AgentTask, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) (*Outcome, error) {
	if task.Spec.Issue == nil {
		return nil, fmt.Errorf("implement task has no issue")
	}
	glProject := spec.GitLab.Project
	wf := spec.Workflow
	botID := project.Status.BotUserID
	out := &Outcome{}

	issue, err := gl.GetIssue(ctx, glProject, task.Spec.Issue.IID)
	if err != nil {
		return out, fmt.Errorf("getting issue: %w", err)
	}
	if issue.State != "opened" {
		out.Result = aiv1.RunResultSucceeded
		out.Summary = "issue is closed, nothing to do"
		return out, nil
	}
	var milestone *gitlab.Milestone
	if issue.Milestone != nil {
		if milestone, err = gl.GetMilestone(ctx, glProject, issue.Milestone.ID); err != nil {
			return out, fmt.Errorf("getting milestone: %w", err)
		}
	}
	proj, err := gl.GetProject(ctx, glProject)
	if err != nil {
		return out, fmt.Errorf("getting project: %w", err)
	}
	base := proj.DefaultBranch

	// The branch name is fixed on the first run, renaming the issue later
	// does not move the work to another branch.
	branch := task.Status.Run.Branch
	if branch == "" {
		branch = naming.Branch(issue.IID, issue.Title)
	}
	out.Branch = branch

	if err := r.syncWorkspace(ctx, proj.HTTPURLToRepo, branch, base); err != nil {
		return out, err
	}

	mr, err := gl.FindOpenMergeRequest(ctx, glProject, branch)
	if err != nil {
		return out, fmt.Errorf("looking up merge request: %w", err)
	}

	// Context: on a resumed session only comments the agent has not seen yet
	// are sent, everything else is already in the session.
	st := r.loadState()
	issueNotes, err := gl.ListIssueNotes(ctx, glProject, issue.IID)
	if err != nil {
		return out, fmt.Errorf("listing issue notes: %w", err)
	}
	var mrNotes []gitlab.Note
	if mr != nil {
		if mrNotes, err = gl.ListMergeRequestNotes(ctx, glProject, mr.IID); err != nil {
			return out, fmt.Errorf("listing merge request notes: %w", err)
		}
	}
	role, profile := spec.ImplementRole(issue.Labels)
	r.Log.Info("implementing issue", "issue", issue.IID, "profile", profile, "engine", role.Engine, "model", role.Model)
	inv := roleInvocation(role, implementSystemPrompt, implementTools, implementSchema, false)
	inv.DisallowedTools = implementDenied
	inv.Resume = st.session("implement", role.Engine)
	fullPrompt := buildImplementPrompt(issue, milestone, issueNotes, mrNotes, botID, false)
	inv.Prompt = buildImplementPrompt(issue, milestone, newerThan(issueNotes, st.LastIssueNoteID), newerThan(mrNotes, st.LastMRNoteID), botID, true)

	res, err := r.invoke(ctx, role, inv, fullPrompt)
	if res != nil {
		out.CostUSD += res.CostUSD
	}
	if err != nil {
		return out, err
	}
	st.setSession("implement", role.Engine, res.SessionID)
	st.LastIssueNoteID = maxNoteID(issueNotes, st.LastIssueNoteID)
	st.LastMRNoteID = maxNoteID(mrNotes, st.LastMRNoteID)
	if err := r.saveState(st); err != nil {
		return out, err
	}
	var answer ImplementAnswer
	if err := res.Decode(&answer); err != nil {
		return out, fmt.Errorf("decoding implementer answer: %w", err)
	}
	if _, err := r.Agent.Commit(ctx, commitMessage(answer.CommitMessage, issue)); err != nil {
		return out, err
	}

	// Review loop: read-only reviewers see the diff, blocking findings go
	// back to the (resumed) implementer, bounded by maxIterations.
	review := spec.Agents.Review
	var remaining []Finding
	var reviewNotes []string
	for iter := 0; len(review.Reviewers) > 0 && answer.Status != "needs_input"; iter++ {
		diff, stat, err := r.Agent.Diff(ctx, base)
		if err != nil {
			return out, err
		}
		if strings.TrimSpace(diff) == "" {
			break
		}
		findings, cost, errs := r.review(ctx, review, issue, base, stat, diff)
		out.CostUSD += cost
		reviewNotes = errs
		// Reviewers are read-only; discard anything they touched anyway.
		if err := r.Agent.Reset(ctx); err != nil {
			return out, err
		}
		var blocking []Finding
		for _, f := range findings {
			if f.Blocking() {
				blocking = append(blocking, f)
			}
		}
		remaining = findings
		if len(blocking) == 0 || iter >= int(*review.MaxIterations) {
			break
		}
		fix := inv
		fix.Resume = res.SessionID
		fixPrompt := buildFixPrompt(blocking)
		fix.Prompt = fixPrompt
		res, err = r.invoke(ctx, role, fix, fullPrompt+"\n\n"+fixPrompt)
		if res != nil {
			out.CostUSD += res.CostUSD
		}
		if err != nil {
			return out, err
		}
		st.setSession("implement", role.Engine, res.SessionID)
		_ = r.saveState(st)
		var fixed ImplementAnswer
		if err := res.Decode(&fixed); err == nil {
			answer.Summary = fixed.Summary
			answer.Questions = fixed.Questions
			if fixed.Status != "" {
				answer.Status = fixed.Status
			}
		}
		if _, err := r.Agent.Commit(ctx, commitMessage(fixed.CommitMessage, issue)); err != nil {
			return out, err
		}
	}

	// Push and open / update the merge request.
	pushed, err := r.publish(ctx, branch, base)
	if err != nil {
		return out, err
	}
	report := renderReport(answer, remaining, reviewNotes)
	newMR := false
	if mr == nil && (pushed || r.Mirror.HasRemoteBranch(ctx, branch)) {
		mr, err = gl.CreateMergeRequest(ctx, glProject, gitlab.CreateMergeRequestOptions{
			SourceBranch:       branch,
			TargetBranch:       base,
			Title:              "Draft: " + issue.Title,
			Description:        fmt.Sprintf("Closes #%d\n\n%s", issue.IID, report),
			RemoveSourceBranch: true,
		})
		if err != nil {
			return out, fmt.Errorf("creating merge request: %w", err)
		}
		newMR = true
	}

	// Report back and move the issue to the next column - unless another
	// run was requested meanwhile, in which case the issue stays "working".
	needsInput := answer.Status == "needs_input" || len(answer.Questions) > 0
	switch {
	case mr != nil && !newMR:
		if _, err := gl.CreateMergeRequestNote(ctx, glProject, mr.IID, "🤖 "+report); err != nil {
			return out, fmt.Errorf("commenting on merge request: %w", err)
		}
	case mr != nil && newMR:
		note := fmt.Sprintf("🤖 Opened %s", mr.WebURL)
		if len(answer.Questions) > 0 {
			note += "\n\nBefore this can be finished I need answers to:\n\n" + numbered(answer.Questions)
		}
		if _, err := gl.CreateIssueNote(ctx, glProject, issue.IID, note); err != nil {
			return out, fmt.Errorf("commenting on issue: %w", err)
		}
	default:
		if _, err := gl.CreateIssueNote(ctx, glProject, issue.IID, "🤖 "+report); err != nil {
			return out, fmt.Errorf("commenting on issue: %w", err)
		}
	}
	if !r.morePending(ctx, task.Spec.RunRequest) {
		next := wf.ReviewLabel
		if needsInput {
			next = wf.NeedsInputLabel
		}
		if _, err := gl.UpdateIssue(ctx, glProject, issue.IID, gitlab.UpdateIssueOptions{
			AddLabels: []string{next}, RemoveLabels: []string{wf.WorkingLabel, wf.ReadyLabel},
		}); err != nil {
			return out, fmt.Errorf("updating issue labels: %w", err)
		}
	}

	out.Result = aiv1.RunResultSucceeded
	if needsInput {
		out.Result = aiv1.RunResultNeedsInput
	}
	out.Summary = answer.Summary
	if mr != nil {
		out.MRIID, out.MRURL = mr.IID, mr.WebURL
	}
	return out, nil
}

// review runs all configured reviewers in parallel. Reviewer failures do not
// fail the run; they are reported alongside the result.
func (r *Runner) review(ctx context.Context, spec aiv1.ReviewSpec, issue *gitlab.Issue, base, stat, diff string) ([]Finding, float64, []string) {
	prompt := buildReviewPrompt(issue, base, stat, diff)
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		findings []Finding
		cost     float64
		errs     []string
	)
	for _, kind := range spec.Reviewers {
		wg.Add(1)
		go func(kind aiv1.ReviewerKind) {
			defer wg.Done()
			inv := roleInvocation(spec.RoleSpec, reviewerSystemPrompt(kind), readOnlyTools, reviewSchema, true)
			inv.NoPersistence = true
			res, err := r.invoke(ctx, spec.RoleSpec, inv, prompt)
			var answer ReviewAnswer
			if err == nil {
				err = res.Decode(&answer)
			}
			mu.Lock()
			defer mu.Unlock()
			if res != nil {
				cost += res.CostUSD
			}
			if err != nil {
				r.Log.Error(err, "reviewer failed", "reviewer", kind)
				errs = append(errs, fmt.Sprintf("%s reviewer failed: %s", kind, truncate(err.Error(), 300)))
				return
			}
			for _, f := range answer.Findings {
				f.Reviewer = string(kind)
				findings = append(findings, f)
			}
		}(kind)
	}
	wg.Wait()
	return findings, cost, errs
}

func renderReport(answer ImplementAnswer, findings []Finding, reviewErrs []string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(answer.Summary))
	if len(answer.Questions) > 0 {
		b.WriteString("\n\n### Questions\n\n")
		b.WriteString(numbered(answer.Questions))
	}
	if len(findings) > 0 {
		b.WriteString("\n\n### Remaining review findings\n\n")
		b.WriteString(formatFindings(findings))
	}
	for _, e := range reviewErrs {
		fmt.Fprintf(&b, "\n> ⚠️ %s\n", e)
	}
	return b.String()
}

func commitMessage(msg string, issue *gitlab.Issue) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = issue.Title
	}
	return fmt.Sprintf("%s\n\nRefs #%d", msg, issue.IID)
}

func newerThan(notes []gitlab.Note, id int64) []gitlab.Note {
	var out []gitlab.Note
	for _, n := range notes {
		if n.ID > id {
			out = append(out, n)
		}
	}
	return out
}

func maxNoteID(notes []gitlab.Note, cur int64) int64 {
	for _, n := range notes {
		if n.ID > cur {
			cur = n.ID
		}
	}
	return cur
}

func numbered(items []string) string {
	var b strings.Builder
	for i, q := range items {
		fmt.Fprintf(&b, "%d. %s\n", i+1, strings.TrimSpace(q))
	}
	return b.String()
}
