package runner

import (
	"context"
	"fmt"
	"strings"
	"sync"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
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
	// "/agent revisit <id> …" comments since the last run name decisions to
	// change; the implementer gets them with the decision they refer to.
	// Without runner state (archived workspace) the agent notes say which
	// comments were already handled, so old revisits are not applied again.
	since := max(st.RevisitNoteID, prior.AfterNote)
	revisits := findRevisits(issue, issueNotes, append(newerThan(issueNotes, since), newerThan(mrNotes, since)...), botID, wf.CommandPrefix)

	role, profile := spec.ImplementRole(issue.Labels)
	inv := roleInvocation(role, implementSystemPrompt, implementTools, implementSchema, false)
	inv.DisallowedTools = implementDenied
	inv.Resume = st.session("implement", role.Engine)
	fullPrompt := buildImplementPrompt(issue, milestone, issueNotes, mrNotes, botID, false, prior)
	inv.Prompt = buildImplementPrompt(issue, milestone, newIssueNotes, newMRNotes, botID, true, handoff{})
	if task.Spec.Autopilot {
		fullPrompt = autopilotImplementNote + "\n\n" + fullPrompt
		inv.Prompt = autopilotImplementNote + "\n\n" + inv.Prompt
	}
	var notices []string
	var active, ignored []revisit
	for _, rv := range revisits {
		switch {
		case rv.Malformed:
			notices = append(notices, fmt.Sprintf("%q is not a revisit: use `%s revisit D%d-<n> <what you want instead>`.", rv.Instruction, wf.CommandPrefix, issue.IID))
			ignored = append(ignored, rv)
		case rv.Superseded:
			ignored = append(ignored, rv)
			notices = append(notices, fmt.Sprintf("Decision %s was already superseded; revisit the decision that replaced it.", rv.ID))
		case rv.Decision == "":
			notices = append(notices, fmt.Sprintf("Decision %s was not found on this issue; the instruction was passed on as a comment.", rv.ID))
			active = append(active, rv)
		default:
			active = append(active, rv)
		}
	}
	revisits = active
	if len(ignored) > 0 {
		ignoredPrompt := buildIgnoredRevisitsPrompt(ignored)
		fullPrompt += "\n\n" + ignoredPrompt
		inv.Prompt = ignoredPrompt + "\n\n" + inv.Prompt
	}
	if len(revisits) > 0 {
		revisitPrompt := buildRevisitPrompt(revisits)
		fullPrompt += "\n\n" + revisitPrompt
		inv.Prompt = revisitPrompt + "\n\n" + inv.Prompt
		r.log().Info("revisiting decisions", "decisions", len(revisits), "notices", len(notices))
	}

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

	// A conflict run: the operator saw the merge request conflict with the
	// default branch. The default branch is merged in; the implementer
	// resolves the conflicts. A clean merge needs no agent.
	mergeFor := task.Spec.Source.MergeBaseFor
	mergeBase := mergeFor != "" && mergeFor != task.Status.Run.MergedBaseFor
	humanInput := humanNotes(newIssueNotes, botID) + humanNotes(newMRNotes, botID)
	var merge MergeResult
	mergeMessage := fmt.Sprintf("Merge branch '%s' into %s", base, branch)
	runAgent := true
	if mergeBase {
		out.MergedBaseFor = mergeFor
		s = r.step(ctx, "merge "+base, "base", base)
		if merge, err = r.Agent.MergeBase(ctx, base, mergeMessage); err != nil {
			return out, s.fail(fmt.Errorf("merging %s: %w", base, err))
		}
		switch {
		case merge.UpToDate:
			s.done("the branch already contains " + base)
		case len(merge.Conflicts) == 0:
			s.done("merged without conflicts")
			notices = append(notices, fmt.Sprintf("Merged `%s` into the branch; there were no conflicts.", base))
		default:
			s.done(fmt.Sprintf("conflicts in %d files", len(merge.Conflicts)), "files", merge.Conflicts)
			conflictPrompt := buildConflictPrompt(base, merge.Conflicts)
			fullPrompt += "\n\n" + conflictPrompt
			if humanInput == 0 && !ciFix {
				inv.Prompt = conflictPrompt
			} else {
				inv.Prompt = conflictPrompt + "\n\n" + inv.Prompt
			}
		}
		runAgent = len(merge.Conflicts) > 0 || humanInput > 0 || ciFix || len(revisits) > 0 || len(ignored) > 0
	}

	var res *Result
	var answer ImplementAnswer
	if runAgent {
		s = r.step(ctx, "run implementer", "profile", profile, "engine", role.Engine, "model", role.Model,
			"resume", inv.Resume != "", "ciFix", ciFix, "conflicts", len(merge.Conflicts))
		res, err = r.invoke(ctx, role, inv, fullPrompt)
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
		if err := res.Decode(&answer); err != nil {
			return out, s.fail(fmt.Errorf("decoding implementer answer: %w", err))
		}
		s.done(agentResult(role, res, answer.Status), "session", res.SessionID, "costUSD", res.CostUSD,
			"status", answer.Status, "questions", len(answer.Questions), "decisions", len(answer.Decisions))
	} else {
		// Only the merge: nothing for the agent to do; review and CI follow.
		res = &Result{SessionID: inv.Resume}
		answer = ImplementAnswer{Status: "done", Summary: fmt.Sprintf("Merged `%s` into the branch.", base)}
		if merge.UpToDate {
			answer.Summary = fmt.Sprintf("The branch already contains `%s`; nothing to merge.", base)
		}
	}
	if len(merge.Conflicts) > 0 {
		// Conclude the merge before anything else is committed: a merge
		// with conflict markers left must never be committed.
		s = r.step(ctx, "conclude merge", "files", len(merge.Conflicts))
		giveUp := answer.Status == "needs_input" || len(answer.Questions) > 0
		fin, err := r.Agent.FinishMerge(ctx, FinishMerge{Base: base, Message: mergeMessage, From: merge.From, Files: merge.Conflicts, Abort: giveUp})
		if err != nil {
			return out, s.fail(fmt.Errorf("concluding the merge: %w", err))
		}
		if fin.Merged {
			s.done("conflicts resolved")
			notices = append(notices, fmt.Sprintf("Merged `%s` into the branch and resolved the conflicts in %s.", base, codeList(merge.Conflicts)))
		} else {
			// Back to the branch as it was: a human has to look.
			left := fin.Conflicts
			if len(left) == 0 {
				left = merge.Conflicts
			}
			s.done("not resolved, merge undone", "files", left)
			answer.Status = "needs_input"
			notices = append(notices, fmt.Sprintf("Could not resolve the merge conflicts with `%s` in %s, so the merge was undone. "+
				"Merge `%s` into the branch by hand, or comment `%s …` with guidance.", base, codeList(left), base, wf.CommandPrefix))
		}
	}
	if err := r.commit(ctx, commitMessage(answer.CommitMessage, issue)); err != nil {
		return out, err
	}

	// Review loop: read-only reviewers see the diff, blocking findings go
	// back to the (resumed) implementer, bounded by maxIterations.
	review := spec.Agents.Review
	var remaining []Finding
	var reviewNotes []string
	// reviewedLast is false when a fix round came after the last review.
	reviewedLast := false
	// CI fix runs skip the reviewers: the change is reviewed with the next
	// regular run, and CI itself is the check here. On autopilot nobody
	// reviews before the merge, so CI fixes are reviewed as well.
	for iter := 0; len(review.Reviewers) > 0 && answer.Status != "needs_input" && (!ciFix || task.Spec.Autopilot); iter++ {
		s = r.step(ctx, fmt.Sprintf("review round %d", iter+1), "reviewers", review.Reviewers)
		diff, stat, err := r.Agent.Diff(ctx, base)
		if err != nil {
			return out, s.fail(err)
		}
		if strings.TrimSpace(diff) == "" {
			out.Reviewed, out.BlockingFindings, out.ReviewErrors = true, 0, 0
			s.done("no changes to review")
			break
		}
		findings, cost, errs := r.review(ctx, review, issue, planDecisionsText(issueNotes, botID), base, stat, diff)
		out.CostUSD += cost
		reviewNotes = errs
		out.Reviewed, out.ReviewErrors = true, int32(len(errs))
		reviewedLast = true
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
		out.BlockingFindings = int32(len(blocking))
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
			answer.Decisions = append(answer.Decisions, fixed.Decisions...)
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
		reviewedLast = false
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
	} else if head, _ := r.Mirror.resolve(ctx, "refs/remotes/origin/"+branch); head != "" && head != task.Status.Run.PushedSHA {
		// Someone else pushed to the branch (or made it) since the last
		// recorded push, and this run took it over unchanged: that head is
		// the one CI has to check.
		out.PushedSHA = head
		s.done(fmt.Sprintf("nothing new to push; the branch moved to %s", shortSHA(head)), "sha", head)
	} else {
		s.done("nothing new to push")
	}
	if out.Reviewed && reviewedLast {
		// The last review saw the branch as it is now.
		out.ReviewedSHA = out.PushedSHA
		if out.ReviewedSHA == "" {
			out.ReviewedSHA, _ = r.Mirror.resolve(ctx, "refs/remotes/origin/"+branch)
		}
	}
	// The notes go right after the push, so a later failure (merge request,
	// comment) does not lose them. Without the bot's user ID the note could
	// not be found again.
	if botID != 0 {
		r.updateHandoff(ctx, gl, glProject, issue.IID, handoffNote, answer.Handoff, seenNote)
		notices = append(notices, r.postDecisions(ctx, gl, glProject, issue, issueNotes, botID, wf.CommandPrefix, answer.Decisions, revisits)...)
		// Revisits count as handled once their decisions are posted; a run
		// that fails before applies them again.
		st.RevisitNoteID = seenNote
		_ = r.saveState(st)
	}
	report := renderReport(answer, remaining, append(reviewNotes, notices...))
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
	waitForCI := !spec.CI.Disabled && out.PushedSHA != "" && mr != nil && !needsInput
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

// postDecisions posts the run's decisions on the issue, numbered after the
// highest decision ID there, and marks the revisited decisions that a new
// decision supersedes. Like the notes it is best effort and never fails the
// run; it returns notices for the run's report.
func (r *Runner) postDecisions(ctx context.Context, gl gitlab.API, glProject string, issue *gitlab.Issue, notes []gitlab.Note, botID int64, prefix string, ds []Decision, revisits []revisit) []string {
	numbered := numberDecisions(ds, issue.IID, false, nextDecisionNumber(issue, notes, botID))
	if len(numbered) == 0 && len(revisits) == 0 {
		return nil
	}
	s := r.step(ctx, "post decisions", "decisions", len(numbered), "revisits", len(revisits))
	if len(numbered) > 0 {
		for _, body := range renderDecisionsComments(r.run.id, prefix, numbered) {
			if _, err := gl.CreateIssueNote(ctx, glProject, issue.IID, body); err != nil {
				_ = s.fail(fmt.Errorf("posting decisions: %w", err))
				return nil
			}
		}
	}

	// A revisited decision is only marked when a new decision replaces it.
	// All marks on one comment (or the description) go into one update.
	var notices []string
	bodies := map[int64]string{}
	var planned []revisit
	marked := 0
	for _, rv := range revisits {
		by := ""
		for _, d := range numbered {
			if strings.EqualFold(strings.TrimSpace(d.Supersedes), rv.ID) {
				by = d.ID
				break
			}
		}
		switch {
		case rv.Decision == "":
		case by == "":
			notices = append(notices, fmt.Sprintf("Decision %s was revisited, but no new decision says it replaces it, so it is not marked superseded.", rv.ID))
		case rv.Note != nil:
			body, ok := bodies[rv.Note.ID]
			if !ok {
				body = rv.Note.Body
			}
			bodies[rv.Note.ID] = markSuperseded(body, rv.ID, by, r.run.id)
			marked++
		default:
			rv.Instruction = by // the successor, for the description below
			planned = append(planned, rv)
			marked++
		}
	}
	for id, body := range bodies {
		if _, err := gl.UpdateIssueNote(ctx, glProject, issue.IID, id, body); err != nil {
			_ = s.fail(fmt.Errorf("marking decisions superseded: %w", err))
			return notices
		}
	}
	if len(planned) > 0 {
		// Planning decisions of older issues live in the description, which
		// a human may have edited during the run: mark them on the current
		// version.
		fresh, err := gl.GetIssue(ctx, glProject, issue.IID)
		if err != nil {
			_ = s.fail(fmt.Errorf("reading the issue description: %w", err))
			return notices
		}
		desc := fresh.Description
		for _, rv := range planned {
			desc = markSuperseded(desc, rv.ID, rv.Instruction, r.run.id)
		}
		if desc != fresh.Description {
			if _, err := gl.UpdateIssue(ctx, glProject, issue.IID, gitlab.UpdateIssueOptions{Description: desc}); err != nil {
				_ = s.fail(fmt.Errorf("marking planning decisions superseded: %w", err))
				return notices
			}
		}
	}
	// A follow-up issue (created by the bot) revisits a decision of a merged
	// issue: the new decision supersedes that one, on the other issue.
	if m := marker.RevisitOf.FindStringSubmatch(issue.Description); m != nil && botID != 0 && issue.Author.ID == botID {
		for _, d := range numbered {
			if !strings.EqualFold(strings.TrimSpace(d.Supersedes), m[1]) {
				continue
			}
			if err := r.supersedeElsewhere(ctx, gl, glProject, botID, m[1], d.ID); err != nil {
				_ = s.fail(err)
				return notices
			}
			marked++
			break
		}
	}
	result := "none posted"
	if len(numbered) > 0 {
		result = fmt.Sprintf("%s to %s posted", numbered[0].ID, numbered[len(numbered)-1].ID)
	}
	if marked > 0 {
		result += fmt.Sprintf(", %d superseded", marked)
	}
	s.done(result)
	return notices
}

// supersedeElsewhere marks decision id on its own issue as superseded by the
// decision by of this run: in the bot's decision comment that holds it, or in
// the description for planning decisions of older issues.
func (r *Runner) supersedeElsewhere(ctx context.Context, gl gitlab.API, glProject string, botID int64, id, by string) error {
	iid := marker.DecisionIssue(id)
	notes, err := gl.ListIssueNotes(ctx, glProject, iid)
	if err != nil {
		return fmt.Errorf("reading #%d to mark %s superseded: %w", iid, id, err)
	}
	for _, n := range notes {
		if !isDecisionComment(n, botID) || decisionSection(n.Body, id) == "" {
			continue
		}
		if body := markSuperseded(n.Body, id, by, r.run.id); body != n.Body {
			if _, err := gl.UpdateIssueNote(ctx, glProject, iid, n.ID, body); err != nil {
				return fmt.Errorf("marking %s superseded on #%d: %w", id, iid, err)
			}
		}
		return nil
	}
	if !strings.Contains(id, "-P") {
		return nil
	}
	target, err := gl.GetIssue(ctx, glProject, iid)
	if err != nil {
		return fmt.Errorf("reading #%d to mark %s superseded: %w", iid, id, err)
	}
	if desc := markSuperseded(target.Description, id, by, r.run.id); desc != target.Description {
		if _, err := gl.UpdateIssue(ctx, glProject, iid, gitlab.UpdateIssueOptions{Description: desc}); err != nil {
			return fmt.Errorf("marking %s superseded on #%d: %w", id, iid, err)
		}
	}
	return nil
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
func (r *Runner) review(ctx context.Context, spec aiv1.ReviewSpec, issue *gitlab.Issue, planDecisions, base, stat, diff string) ([]Finding, float64, []string) {
	prompt := buildReviewPrompt(issue, planDecisions, base, stat, diff)
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

// codeList renders file names as inline code.
func codeList(files []string) string {
	parts := make([]string, len(files))
	for i, f := range files {
		parts[i] = "`" + strings.ReplaceAll(f, "`", "'") + "`"
	}
	return strings.Join(parts, ", ")
}

// humanNotes counts the notes written by humans.
func humanNotes(notes []gitlab.Note, botID int64) int {
	n := 0
	for _, note := range notes {
		if !note.System && (botID == 0 || note.Author.ID != botID) {
			n++
		}
	}
	return n
}
