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

	s := r.step(ctx, "read issue")
	issue, err := gl.GetIssue(ctx, glProject, task.Spec.Issue.IID)
	if err != nil {
		return out, s.fail(fmt.Errorf("getting issue: %w", err))
	}
	s.done(fmt.Sprintf("#%d %s", issue.IID, issue.State), "state", issue.State, "labels", issue.Labels)
	if issue.State != "opened" {
		out.Result = aiv1.RunResultSucceeded
		out.Summary = "issue is closed, nothing to do"
		return out, nil
	}
	var milestone *gitlab.Milestone
	if issue.Milestone != nil {
		s = r.step(ctx, "read milestone", "milestoneID", issue.Milestone.ID)
		if milestone, err = gl.GetMilestone(ctx, glProject, issue.Milestone.ID); err != nil {
			return out, s.fail(fmt.Errorf("getting milestone: %w", err))
		}
		s.done(milestone.Title)
	}
	s = r.step(ctx, "read project")
	proj, err := gl.GetProject(ctx, glProject)
	if err != nil {
		return out, s.fail(fmt.Errorf("getting project: %w", err))
	}
	base := proj.DefaultBranch
	s.done("default branch "+base, "defaultBranch", base)

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

	s = r.step(ctx, "find merge request", "branch", branch)
	mr, err := gl.FindOpenMergeRequest(ctx, glProject, branch)
	if err != nil {
		return out, s.fail(fmt.Errorf("looking up merge request: %w", err))
	}
	if mr != nil {
		s.done(fmt.Sprintf("!%d", mr.IID), "mr", mr.IID)
	} else {
		s.done("none yet")
	}

	// Context: on a resumed session only comments the agent has not seen yet
	// are sent, everything else is already in the session.
	st := r.loadState()
	s = r.step(ctx, "read comments")
	issueNotes, err := gl.ListIssueNotes(ctx, glProject, issue.IID)
	if err != nil {
		return out, s.fail(fmt.Errorf("listing issue notes: %w", err))
	}
	// The handoff note is the agent's own memory, not part of the thread.
	handoffNote, issueNotes := splitHandoff(issueNotes, botID)
	prior := parseHandoff(handoffNote)
	var mrNotes []gitlab.Note
	if mr != nil {
		if mrNotes, err = gl.ListMergeRequestNotes(ctx, glProject, mr.IID); err != nil {
			return out, s.fail(fmt.Errorf("listing merge request notes: %w", err))
		}
	}
	newIssueNotes, newMRNotes := newerThan(issueNotes, st.LastIssueNoteID), newerThan(mrNotes, st.LastMRNoteID)
	result := fmt.Sprintf("%d on the issue, %d on the MR, %d new", len(issueNotes), len(mrNotes), len(newIssueNotes)+len(newMRNotes))
	if prior.Text != "" {
		result += ", agent notes from an earlier run"
	}
	s.done(result, "issueNotes", len(issueNotes), "mrNotes", len(mrNotes), "newNotes", len(newIssueNotes)+len(newMRNotes),
		"handoffBytes", len(prior.Text))
	seenNote := max(maxNoteID(issueNotes, 0), maxNoteID(mrNotes, 0))

	role, profile := spec.ImplementRole(issue.Labels)
	inv := roleInvocation(role, implementSystemPrompt, implementTools, implementSchema, false)
	inv.DisallowedTools = implementDenied
	inv.Resume = st.session("implement", role.Engine)
	fullPrompt := buildImplementPrompt(issue, milestone, issueNotes, mrNotes, botID, false, prior)
	inv.Prompt = buildImplementPrompt(issue, milestone, newIssueNotes, newMRNotes, botID, true, handoff{})

	// A CI fix run: the operator saw the pipeline of the last push fail.
	ciPipeline := task.Spec.Source.CIFixPipelineID
	ciFix := ciPipeline != 0 && ciPipeline != task.Status.Run.CIFixedPipelineID
	if ciFix {
		out.CIFixedPipelineID = ciPipeline
		s = r.step(ctx, "read failed CI jobs", "pipeline", ciPipeline)
		ciPrompt, err := buildCIPrompt(ctx, gl, glProject, ciPipeline)
		if err != nil {
			return out, s.fail(err)
		}
		s.done(fmt.Sprintf("pipeline %d", ciPipeline))
		fullPrompt += "\n\n" + ciPrompt
		if len(newIssueNotes)+len(newMRNotes) == 0 {
			inv.Prompt = ciPrompt
		} else {
			inv.Prompt += "\n\n" + ciPrompt
		}
	}

	s = r.step(ctx, "run implementer", "profile", profile, "engine", role.Engine, "model", role.Model,
		"resume", inv.Resume != "", "ciFix", ciFix)
	res, err := r.invoke(ctx, role, inv, fullPrompt)
	if res != nil {
		out.CostUSD += res.CostUSD
	}
	if err != nil {
		return out, s.fail(err)
	}
	st.setSession("implement", role.Engine, res.SessionID)
	st.LastIssueNoteID = maxNoteID(issueNotes, st.LastIssueNoteID)
	st.LastMRNoteID = maxNoteID(mrNotes, st.LastMRNoteID)
	if err := r.saveState(st); err != nil {
		return out, s.fail(err)
	}
	var answer ImplementAnswer
	if err := res.Decode(&answer); err != nil {
		return out, s.fail(fmt.Errorf("decoding implementer answer: %w", err))
	}
	s.done(agentResult(role, res, answer.Status), "session", res.SessionID, "costUSD", res.CostUSD,
		"status", answer.Status, "questions", len(answer.Questions))
	if err := r.commit(ctx, commitMessage(answer.CommitMessage, issue)); err != nil {
		return out, err
	}

	// Review loop: read-only reviewers see the diff, blocking findings go
	// back to the (resumed) implementer, bounded by maxIterations.
	review := spec.Agents.Review
	var remaining []Finding
	var reviewNotes []string
	// CI fix runs skip the reviewers: the change is reviewed with the next
	// regular run, and CI itself is the check here.
	for iter := 0; len(review.Reviewers) > 0 && answer.Status != "needs_input" && !ciFix; iter++ {
		s = r.step(ctx, fmt.Sprintf("review round %d", iter+1), "reviewers", review.Reviewers)
		diff, stat, err := r.Agent.Diff(ctx, base)
		if err != nil {
			return out, s.fail(err)
		}
		if strings.TrimSpace(diff) == "" {
			s.done("no changes to review")
			break
		}
		findings, cost, errs := r.review(ctx, review, issue, base, stat, diff)
		out.CostUSD += cost
		reviewNotes = errs
		// Reviewers are read-only; discard anything they touched anyway.
		if err := r.Agent.Reset(ctx); err != nil {
			return out, s.fail(err)
		}
		var blocking []Finding
		for _, f := range findings {
			if f.Blocking() {
				blocking = append(blocking, f)
			}
		}
		s.done(fmt.Sprintf("%s; %d findings, %d blocking, $%.2f", diffSummary(stat), len(findings), len(blocking), cost),
			"diff", diffSummary(stat), "findings", len(findings), "blocking", len(blocking), "reviewerErrors", len(errs), "costUSD", cost)
		remaining = findings
		if len(blocking) == 0 || iter >= int(*review.MaxIterations) {
			break
		}
		s = r.step(ctx, fmt.Sprintf("fix round %d", iter+1), "blocking", len(blocking))
		fix := inv
		fix.Resume = res.SessionID
		fixPrompt := buildFixPrompt(blocking)
		fix.Prompt = fixPrompt
		res, err = r.invoke(ctx, role, fix, fullPrompt+"\n\n"+fixPrompt)
		if res != nil {
			out.CostUSD += res.CostUSD
		}
		if _, limited := AsUsageLimit(err); limited {
			// Keep what is done: publish it with the open findings instead
			// of throwing the work away until the limit resets.
			reviewNotes = append(reviewNotes, "fix round skipped: "+err.Error())
			s.done("skipped: usage limit", "error", err.Error())
			break
		}
		if err != nil {
			return out, s.fail(err)
		}
		st.setSession("implement", role.Engine, res.SessionID)
		_ = r.saveState(st)
		var fixed ImplementAnswer
		if err := res.Decode(&fixed); err == nil {
			answer.Summary = fixed.Summary
			answer.Questions = fixed.Questions
			if strings.TrimSpace(fixed.Handoff) != "" {
				answer.Handoff = fixed.Handoff
			}
			if fixed.Status != "" {
				answer.Status = fixed.Status
			}
		}
		s.done(agentResult(role, res, fixed.Status), "session", res.SessionID, "costUSD", res.CostUSD, "status", fixed.Status)
		if err := r.commit(ctx, commitMessage(fixed.CommitMessage, issue)); err != nil {
			return out, err
		}
	}

	// Push and open / update the merge request.
	s = r.step(ctx, "push", "branch", branch)
	pushed, err := r.publish(ctx, branch, base)
	if err != nil {
		return out, s.fail(err)
	}
	if pushed {
		out.PushedSHA, _ = r.Mirror.resolve(ctx, "refs/heads/"+branch)
		if ciFix {
			out.CIRound = task.Status.Run.CIRound + 1
		}
		s.done(fmt.Sprintf("%s at %s", branch, shortSHA(out.PushedSHA)), "sha", out.PushedSHA)
	} else {
		s.done("nothing new to push")
	}
	// The notes go right after the push, so a later failure (merge request,
	// comment) does not lose them. Without the bot's user ID the note could
	// not be found again.
	if botID != 0 {
		r.updateHandoff(ctx, gl, glProject, issue.IID, handoffNote, answer.Handoff, seenNote)
	}
	report := renderReport(answer, remaining, reviewNotes)
	if ciFix {
		report = "CI fix: " + report
	}
	newMR := false
	if mr == nil && (pushed || r.Mirror.HasRemoteBranch(ctx, branch)) {
		s = r.step(ctx, "open merge request", "branch", branch, "target", base)
		opts := gitlab.CreateMergeRequestOptions{
			SourceBranch:       branch,
			TargetBranch:       base,
			Title:              "Draft: " + issue.Title,
			Description:        fmt.Sprintf("Closes #%d\n\n%s", issue.IID, report),
			RemoveSourceBranch: true,
		}
		// The issue's milestone lists the MR in its "Merge requests" tab.
		if issue.Milestone != nil {
			opts.MilestoneID = issue.Milestone.ID
		}
		mr, err = gl.CreateMergeRequest(ctx, glProject, opts)
		if err != nil {
			return out, s.fail(fmt.Errorf("creating merge request: %w", err))
		}
		s.done(fmt.Sprintf("!%d", mr.IID), "mr", mr.IID, "url", mr.WebURL)
		newMR = true
	}

	// Report back and move the issue to the next column - unless another
	// run was requested meanwhile, in which case the issue stays "working".
	// The run log covers the steps up to here.
	needsInput := answer.Status == "needs_input" || len(answer.Questions) > 0
	runLogMD := r.run.markdown(out.CostUSD)
	s = r.step(ctx, "comment")
	switch {
	case mr != nil && !newMR:
		if _, err := gl.CreateMergeRequestNote(ctx, glProject, mr.IID, "🤖 "+report+runLogMD); err != nil {
			return out, s.fail(fmt.Errorf("commenting on merge request: %w", err))
		}
		s.done(fmt.Sprintf("on !%d", mr.IID))
	case mr != nil && newMR:
		note := fmt.Sprintf("🤖 Opened %s", mr.WebURL)
		if len(answer.Questions) > 0 {
			note += "\n\nBefore this can be finished I need answers to:\n\n" + numbered(answer.Questions)
		}
		if _, err := gl.CreateIssueNote(ctx, glProject, issue.IID, note+runLogMD); err != nil {
			return out, s.fail(fmt.Errorf("commenting on issue: %w", err))
		}
		s.done(fmt.Sprintf("on #%d", issue.IID))
	default:
		if _, err := gl.CreateIssueNote(ctx, glProject, issue.IID, "🤖 "+report+runLogMD); err != nil {
			return out, s.fail(fmt.Errorf("commenting on issue: %w", err))
		}
		s.done(fmt.Sprintf("on #%d", issue.IID))
	}
	// With the CI loop, a fresh push stays in "working" until the operator
	// has seen its pipeline go green (or hands it back).
	waitForCI := !spec.CI.Disabled && pushed && mr != nil && !needsInput
	s = r.step(ctx, "update issue labels")
	switch {
	case waitForCI:
		s.done(fmt.Sprintf("stays in %s until the pipeline is green", wf.WorkingLabel))
	case r.morePending(ctx, task.Spec.RunRequest):
		s.done(fmt.Sprintf("stays in %s, another run is pending", wf.WorkingLabel))
	default:
		next := wf.ReviewLabel
		if needsInput {
			next = wf.NeedsInputLabel
		}
		if _, err := gl.UpdateIssue(ctx, glProject, issue.IID, gitlab.UpdateIssueOptions{
			AddLabels: []string{next}, RemoveLabels: []string{wf.WorkingLabel, wf.ReadyLabel},
		}); err != nil {
			return out, s.fail(fmt.Errorf("updating issue labels: %w", err))
		}
		s.done("moved to "+next, "label", next)
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

// updateHandoff stores the implementer's handoff notes in the bot's handoff
// note on the issue, creating it on the first run. A failure is logged but
// does not fail the run: the work itself is pushed already.
func (r *Runner) updateHandoff(ctx context.Context, gl gitlab.API, glProject string, iid int64, note *gitlab.Note, text string, seenNote int64) {
	if strings.TrimSpace(text) == "" {
		return
	}
	s := r.step(ctx, "update agent notes")
	body := renderHandoff(text, r.run.id, seenNote)
	var err error
	if note != nil {
		_, err = gl.UpdateIssueNote(ctx, glProject, iid, note.ID, body)
	} else {
		_, err = gl.CreateIssueNote(ctx, glProject, iid, body)
	}
	if err != nil {
		_ = s.fail(fmt.Errorf("updating agent notes: %w", err))
		return
	}
	if note != nil {
		s.done(fmt.Sprintf("updated (%d bytes)", len(text)), "note", note.ID)
	} else {
		s.done(fmt.Sprintf("created (%d bytes)", len(text)))
	}
}

// commit commits the agent's changes as a step of the run.
func (r *Runner) commit(ctx context.Context, message string) error {
	s := r.step(ctx, "commit")
	committed, err := r.Agent.Commit(ctx, message)
	if err != nil {
		return s.fail(err)
	}
	if committed {
		s.done("committed", "committed", true)
	} else {
		s.done("no changes", "committed", false)
	}
	return nil
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
				r.log().Error(err, "reviewer failed", "reviewer", kind)
				errs = append(errs, fmt.Sprintf("%s reviewer failed: %s", kind, truncate(err.Error(), 300)))
				return
			}
			blocking := 0
			for _, f := range answer.Findings {
				f.Reviewer = string(kind)
				findings = append(findings, f)
				if f.Blocking() {
					blocking++
				}
			}
			r.log().Info("reviewer finished", "reviewer", kind, "findings", len(answer.Findings), "blocking", blocking, "costUSD", res.CostUSD)
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
