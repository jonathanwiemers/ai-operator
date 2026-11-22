package runner

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// review runs all configured reviewers in parallel. A reviewer that fails
// transiently is called again (review.retries). Reviewer failures do not
// fail the run; they are reported alongside the result, with why each
// reviewer failed. ownersRules (the milestone's prompt rules)
// end every reviewer's prompt.
func (r *Runner) review(ctx context.Context, spec aiv1.ReviewSpec, issue *gitlab.Issue, planDecisions string, in reviewInput, ownersRules string) ([]Finding, float64, []string, []aiv1.ReviewError) {
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		findings []Finding
		cost     float64
		errs     []string
		kinds    []aiv1.ReviewError
	)
	for _, kind := range spec.Reviewers {
		wg.Add(1)
		go func(kind aiv1.ReviewerKind) {
			defer wg.Done()
			prompt := buildReviewPrompt(issue, planDecisions, in, spec.MaxInlineDiff(kind)) + reviewerNote(kind, in) + ownersRules
			inv := roleInvocation(spec.RoleSpec, reviewerSystemPrompt(kind), readOnlyTools, reviewSchema, true)
			inv.Role, inv.Kind = string(kind), aiv1.CallKindReview
			inv.NoPersistence = true
			var (
				res    *Result
				err    error
				answer ReviewAnswer
				spent  float64
			)
			retries, attempts := 0, 0
			if spec.Retries != nil {
				retries = int(*spec.Retries)
			}
			for {
				attempts++
				res, err = r.invoke(ctx, spec.RoleSpec, inv, prompt)
				if res != nil {
					spent += res.CostUSD
				}
				if err == nil {
					err = res.Decode(&answer)
				}
				if attempts > retries || !isTransient(err) {
					break
				}
				wait := reviewRetryBackoff[min(attempts, len(reviewRetryBackoff))-1]
				r.log().Info("reviewer failed transiently, trying again", "reviewer", kind, "attempt", attempts, "wait", wait, "error", textutil.Truncate(err.Error(), 300))
				select {
				case <-ctx.Done():
				case <-time.After(wait):
				}
				if ctx.Err() != nil {
					err = fmt.Errorf("stopped while waiting to retry: %w", ctx.Err())
					break
				}
			}
			mu.Lock()
			defer mu.Unlock()
			cost += spent
			if err != nil {
				r.log().Error(err, "reviewer failed", "reviewer", kind, "attempts", attempts)
				kinds = append(kinds, reviewErrorOf(kind, err))
				msg := textutil.Truncate(err.Error(), 300)
				if attempts > 1 {
					msg = fmt.Sprintf("%s (after %d attempts)", msg, attempts)
				}
				errs = append(errs, fmt.Sprintf("%s reviewer failed: %s", kind, msg))
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
	slices.SortFunc(kinds, func(a, b aiv1.ReviewError) int { return strings.Compare(string(a.Reviewer), string(b.Reviewer)) })
	return findings, cost, errs, kinds
}

// reviewErrorOf says why a reviewer failed: its engine's usage limit (with
// the reset, if known), an outage, or anything else.
func reviewErrorOf(reviewer aiv1.ReviewerKind, err error) aiv1.ReviewError {
	e := aiv1.ReviewError{Reviewer: reviewer, Kind: aiv1.ReviewErrorOther}
	switch lim, ok := AsUsageLimit(err); {
	case ok:
		e.Kind, e.Engine = aiv1.ReviewErrorUsageLimit, lim.Engine
		if !lim.ResetAt.IsZero() {
			e.ResetAt = &metav1.Time{Time: lim.ResetAt}
		}
	case isTransient(err):
		e.Kind = aiv1.ReviewErrorTransient
	}
	return e
}

// renderReport renders the run's report; agent-written text in it pings
// nobody and runs no quick actions. desk is the task's Desk page, where the
// questions are answered ("": no link).
func renderReport(answer ImplementAnswer, findings []Finding, reviewErrs []string, desk string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(answer.Summary))
	if drafts := questionDrafts(answer); len(drafts) > 0 {
		b.WriteString("\n\n### Questions\n\n")
		b.WriteString(renderQuestions(drafts))
		if desk != "" {
			b.WriteString("\nAnswer in the Desk: " + desk + "\n")
		}
	}
	if len(findings) > 0 {
		b.WriteString("\n\n### Remaining review findings\n\n")
		b.WriteString(formatFindings(findings))
	}
	for _, e := range reviewErrs {
		fmt.Fprintf(&b, "\n> ⚠️ %s\n", e)
	}
	return notify.Defuse(b.String())
}
