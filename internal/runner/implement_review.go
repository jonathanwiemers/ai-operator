package runner

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// reviewLoop has read-only reviewers see the diff; blocking findings go back
// to the (resumed) implementer, bounded by maxIterations.
func (run *implementRun) reviewLoop(ctx context.Context) error {
	r, st, out, answer, role, issue := run.r, run.state, run.out, &run.answer, run.role, run.issue
	review := run.spec.Agents.Review
	// CI fix runs skip the reviewers: the change is reviewed with the next
	// regular run, and CI itself is the check here. On autopilot nobody
	// reviews before the merge, so CI fixes are reviewed as well.
	for iter := 0; len(review.Reviewers) > 0 && answer.Status != "needs_input" && (!run.ciFix || run.task.Spec.Autopilot); iter++ {
		s := r.step(ctx, fmt.Sprintf("review round %d", iter+1), "reviewers", review.Reviewers)
		// A budget stop ends the run here, not as reviewer errors.
		if err := r.budgetStop(ctx); err != nil {
			return s.fail(err)
		}
		in, err := run.reviewInput(ctx)
		if err != nil {
			return s.fail(err)
		}
		if strings.TrimSpace(in.diff) == "" && in.companion == nil {
			out.Reviewed, out.BlockingFindings, out.ReviewErrors, out.ReviewErrorsTransient, out.ReviewErrorKinds = true, 0, 0, false, nil
			s.done("no changes to review")
			break
		}
		summary := diffSummary(in.stat)
		if in.companion != nil {
			summary = fmt.Sprintf("%s; companion: %s", cmp.Or(summary, "no home changes"), diffSummary(in.companion.stat))
		}
		findings, cost, errs, kinds := r.review(ctx, review, issue, planDecisionsText(run.issueNotes, run.botID), in, run.ownersRules)
		out.CostUSD += cost
		run.reviewNotes = errs
		transient := 0
		for _, k := range kinds {
			if k.Kind == aiv1.ReviewErrorTransient {
				transient++
			}
		}
		out.Reviewed, out.ReviewErrors, out.ReviewErrorsTransient, out.ReviewErrorKinds = true, int32(len(errs)), len(errs) > 0 && transient == len(errs), kinds
		run.reviewedLast, run.reviewedCompanion = true, in.companion != nil
		// Reviewers are read-only; discard anything they touched anyway.
		if err := r.Agent.Reset(ctx, homeTree); err != nil {
			return s.fail(err)
		}
		if in.companion != nil {
			if err := r.Agent.Reset(ctx, run.companion.slug); err != nil {
				return s.fail(err)
			}
		}
		var blocking []Finding
		for _, f := range findings {
			if f.Blocking() {
				blocking = append(blocking, f)
			}
		}
		s.done(fmt.Sprintf("%s; %d findings, %d blocking, $%.2f", summary, len(findings), len(blocking), cost),
			"diff", summary, "findings", len(findings), "blocking", len(blocking), "reviewerErrors", len(errs), "costUSD", cost)
		run.remaining = findings
		out.BlockingFindings = int32(len(blocking))
		if len(blocking) == 0 || iter >= int(*review.MaxIterations) {
			break
		}
		s = r.step(ctx, fmt.Sprintf("fix round %d", iter+1), "blocking", len(blocking))
		fix := run.inv
		fix.Kind = aiv1.CallKindFix
		fix.Resume = run.res.SessionID
		fixPrompt := buildFixPrompt(blocking) + conventionalPointer(run.types)
		fix.Prompt = fixPrompt
		// A usage limit keeps the work below, without a checkpoint.
		r.cutStep(true)
		res, err := r.invoke(ctx, role, fix, run.fullPrompt+"\n\n"+fixPrompt)
		run.res = res
		if res != nil {
			out.CostUSD += res.CostUSD
		}
		if _, limited := AsUsageLimit(err); limited {
			// Keep what is done: publish it with the open findings instead
			// of throwing the work away until the limit resets.
			run.reviewNotes = append(run.reviewNotes, "fix round skipped: "+err.Error())
			s.done("skipped: usage limit", "error", err.Error())
			break
		}
		if err != nil {
			return s.fail(err)
		}
		st.setSession("implement", ranOn(role, res), res.SessionID)
		st.ImplementEngine = ranOn(role, res)
		if err := r.saveState(*st); err != nil {
			r.log().Error(err, "saving the implementer's session")
		}
		var fixed ImplementAnswer
		if err := res.Decode(&fixed); err == nil {
			answer.Summary = fixed.Summary
			answer.Questions, answer.Prompts = fixed.Questions, fixed.Prompts
			answer.Decisions = append(answer.Decisions, fixed.Decisions...)
			if strings.TrimSpace(fixed.Handoff) != "" {
				answer.Handoff = fixed.Handoff
			}
			if fixed.Status != "" {
				answer.Status = fixed.Status
			}
		}
		s.done(agentResult(role, res, fixed.Status), "session", res.SessionID, "costUSD", res.CostUSD, "status", fixed.Status)
		if err := r.commit(ctx, run.branch, run.base, commitMessage(fixed.CommitMessage, issue, run.types)); err != nil {
			return err
		}
		if c := run.companion; c != nil {
			// The next round, and the push, see the companion's fixes too.
			if err := r.commitCompanion(ctx, c, commitMessage(fixed.CommitMessage, issue, run.types)); err != nil {
				return err
			}
		}
		run.reviewedLast = false
	}
	return nil
}
