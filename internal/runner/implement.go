package runner

import (
	"context"
	"fmt"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
	"slices"
	"strings"
	"sync"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/desklink"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// runImplement implements an issue: implement -> review -> fix (bounded) ->
// push -> merge request -> report back on GitLab.
func (r *Runner) runImplement(ctx context.Context, gl gitlab.API, task *aiv1.AgentTask, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) (*Outcome, error) {
	if task.Spec.Issue == nil {
		return nil, fmt.Errorf("implement task has no issue")
	}
	run := r.newImplementRun(gl, task, project, spec)
	if closed, err := run.prepare(ctx); err != nil || closed {
		return run.out, err
	}
	for _, phase := range []func(context.Context) error{
		run.syncBranch, run.readComments, run.buildPrompt, run.addInputs, run.mergeBase, run.implement,
		run.concludeMerge, run.reviewLoop, run.push, run.postDecisionsAndHandoff, run.report,
	} {
		if err := phase(ctx); err != nil {
			return run.out, err
		}
	}
	return run.outcome(ctx), nil
}

// implementRun is one run of an implement task. The phases of runImplement
// are its methods, in that order; they share what they read and decide
// through its fields. The runner's persisted state is embedded (loaded by
// readComments), the other fields live only for the run.
type implementRun struct {
	*state
	r    *Runner
	gl   gitlab.API
	task *aiv1.AgentTask
	// spec has the milestone's rule switches applied, wf is the workflow as
	// the project configures it.
	spec      aiv1.AgentProjectSpec
	wf        aiv1.WorkflowSpec
	glProject string
	botID     int64
	out       *Outcome

	issue       *gitlab.Issue
	milestone   *gitlab.Milestone
	ownersRules string
	proj        *gitlab.Project
	base        string
	branch      string
	mr          *gitlab.MergeRequest

	// The thread: all comments, those the agent has not seen yet, its notes
	// from earlier runs and the decisions to revisit.
	issueNotes, mrNotes       []gitlab.Note
	newIssueNotes, newMRNotes []gitlab.Note
	// deskOnly: the project takes no GitLab input, the issue thread is no
	// context.
	deskOnly          bool
	handoffNote       *gitlab.Note
	prior             handoff
	seenNote          int64
	revisits, ignored []revisit
	notices           []string

	// The implementer's call and what this run asks of it.
	role        aiv1.RoleSpec
	profile     string
	inv         Invocation
	plainSystem string
	fullPrompt  string
	stepwise    bool
	ciFix       bool
	humanInput  int
	// answers are the owners' prompt answers this session has not seen.
	answers      []aiv1.PromptAnswerDelivery
	merge        MergeResult
	mergeMessage string
	reviewFor    string
	reviewAsked  bool
	reReview     bool
	runAgent     bool

	// What the agents did.
	res         *Result
	answer      ImplementAnswer
	outOfSteps  bool
	remaining   []Finding
	reviewNotes []string
	// reviewedLast is false when a fix round came after the last review.
	reviewedLast bool
	pushed       bool
	needsInput   bool
}

func (r *Runner) newImplementRun(gl gitlab.API, task *aiv1.AgentTask, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) *implementRun {
	return &implementRun{r: r, gl: gl, task: task, spec: spec, wf: spec.Workflow,
		glProject: spec.GitLab.Project, botID: project.Status.BotUserID, out: &Outcome{}}
}

// prepare reads the issue, its milestone and the project. It reports whether
// the issue is closed, which ends the run.
func (run *implementRun) prepare(ctx context.Context) (closed bool, err error) {
	r, gl, glProject, out := run.r, run.gl, run.glProject, run.out
	s := r.step(ctx, "read issue")
	issue, err := gl.GetIssue(ctx, glProject, run.task.Spec.Issue.IID)
	if err != nil {
		return false, s.fail(fmt.Errorf("getting issue: %w", err))
	}
	run.issue = issue
	s.done(fmt.Sprintf("#%d %s", issue.IID, issue.State), "state", issue.State, "labels", issue.Labels)
	if issue.State != "opened" {
		out.Result = aiv1.RunResultSucceeded
		out.Summary = "issue is closed, nothing to do"
		return true, nil
	}
	if issue.Milestone != nil {
		s = r.step(ctx, "read milestone", "milestoneID", issue.Milestone.ID)
		if run.milestone, err = gl.GetMilestone(ctx, glProject, issue.Milestone.ID); err != nil {
			return false, s.fail(fmt.Errorf("getting milestone: %w", err))
		}
		// The owners and rule lines are for the operator, not the agent:
		// it gets the switches applied and the prompt rules marked.
		rs := rules.Parse(rules.Lines(run.milestone.Description, run.wf.CommandPrefix))
		run.spec, run.ownersRules = rules.Apply(run.spec, rs), promptRules(rs)
		run.milestone.Description = rules.Spec(run.milestone.Description, run.wf.CommandPrefix)
		s.done(run.milestone.Title)
	}
	s = r.step(ctx, "read project")
	if run.proj, err = gl.GetProject(ctx, glProject); err != nil {
		return false, s.fail(fmt.Errorf("getting project: %w", err))
	}
	run.base = run.proj.DefaultBranch
	s.done("default branch "+run.base, "defaultBranch", run.base)
	return false, nil
}

// syncBranch brings the workspace to the work branch and finds its merge
// request, opening the draft on the first run.
func (run *implementRun) syncBranch(ctx context.Context) error {
	r, gl, glProject, out, issue, base := run.r, run.gl, run.glProject, run.out, run.issue, run.base
	// The branch name is fixed on the first run, renaming the issue later
	// does not move the work to another branch.
	branch := run.task.Status.Run.Branch
	if branch == "" {
		branch = naming.Branch(issue.IID, issue.Title)
	}
	run.branch, out.Branch = branch, branch

	if err := r.syncWorkspace(ctx, run.proj.HTTPURLToRepo, branch, base); err != nil {
		return err
	}

	s := r.step(ctx, "find merge request", "branch", branch)
	mr, err := gl.FindOpenMergeRequest(ctx, glProject, branch)
	if err != nil {
		return s.fail(fmt.Errorf("looking up merge request: %w", err))
	}
	if mr != nil {
		s.done(fmt.Sprintf("!%d", mr.IID), "mr", mr.IID)
	} else {
		s.done("none yet")
		// Open the draft merge request now, so the whole run (and every
		// later one) can be followed there.
		if mr, err = r.openMergeRequest(ctx, gl, glProject, issue, branch, base); err != nil {
			return err
		}
		// Recorded at once: a run deferred or failing from here on must not
		// open a second MR, and the operator reads commands on this one.
		if err := r.patchRun(ctx, func(s *aiv1.RunStatus) {
			s.Branch, s.MergeRequestIID, s.MergeRequestURL = branch, mr.IID, mr.WebURL
		}); err != nil {
			return err
		}
	}
	run.mr = mr
	out.MRIID, out.MRURL = mr.IID, mr.WebURL
	r.startProgress(ctx, gl, glProject, mr.IID, run.botID)
	return nil
}

// readComments loads the runner state and reads the thread: the comments on
// the issue and the MR, the agent notes and the decisions to revisit.
func (run *implementRun) readComments(ctx context.Context) error {
	r, gl, glProject, issue, mr, botID := run.r, run.gl, run.glProject, run.issue, run.mr, run.botID
	// Context: on a resumed session only comments the agent has not seen yet
	// are sent, everything else is already in the session.
	st, err := r.loadState()
	if err != nil {
		// An empty state would post the notes and decisions again.
		return err
	}
	run.state = &st
	s := r.step(ctx, "read comments")
	issueNotes, err := gl.ListIssueNotes(ctx, glProject, issue.IID)
	if err != nil {
		return s.fail(fmt.Errorf("listing issue notes: %w", err))
	}
	// The handoff note is the agent's own memory, not part of the thread.
	handoffNote, issueNotes := splitHandoff(issueNotes, botID)
	prior := parseHandoff(handoffNote)
	issueNotes = r.withoutPilot(issueNotes)
	// Without GitLab input the issue thread is no context: the session (or
	// for a fresh one, the Desk chat) is. The agent's own comments stay, and
	// human review threads on the MR are code review, not chat.
	run.deskOnly = !run.spec.Workflow.Takes(aiv1.InputGitLab)
	deskOnly := run.deskOnly
	if deskOnly {
		issueNotes = ownNotes(issueNotes, botID)
	}
	var mrNotes []gitlab.Note
	if mr != nil {
		if mrNotes, err = gl.ListMergeRequestNotes(ctx, glProject, mr.IID); err != nil {
			return s.fail(fmt.Errorf("listing merge request notes: %w", err))
		}
		// Progress comments are the runner's, not part of the thread.
		mrNotes = r.withoutPilot(slices.DeleteFunc(mrNotes, func(n gitlab.Note) bool { return isProgress(n, botID) }))
	}
	newIssueNotes, newMRNotes := newerThan(issueNotes, st.LastIssueNoteID), newerThan(mrNotes, st.LastMRNoteID)
	if deskOnly {
		// A resumed session wrote the agent's comments itself: it gets the
		// human review threads on the MR only.
		newIssueNotes = nil
		newMRNotes = slices.DeleteFunc(newMRNotes, func(n gitlab.Note) bool { return botID != 0 && n.Author.ID == botID })
	}
	result := fmt.Sprintf("%d on the issue, %d on the MR, %d new", len(issueNotes), len(mrNotes), len(newIssueNotes)+len(newMRNotes))
	if prior.Text != "" {
		result += ", agent notes from an earlier run"
	}
	s.done(result, "issueNotes", len(issueNotes), "mrNotes", len(mrNotes), "newNotes", len(newIssueNotes)+len(newMRNotes),
		"handoffBytes", len(prior.Text))
	run.seenNote = max(maxNoteID(issueNotes, 0), maxNoteID(mrNotes, 0))
	// "/agent revisit <id> …" comments since the last run name decisions to
	// change; the implementer gets them with the decision they refer to.
	// Without runner state (archived workspace) the agent notes say which
	// comments were already handled, so old revisits are not applied again.
	since := max(st.RevisitNoteID, prior.AfterNote)
	run.revisits = findRevisits(issue, issueNotes, append(newerThan(issueNotes, since), newerThan(mrNotes, since)...), botID, run.wf.CommandPrefix)
	run.issueNotes, run.mrNotes, run.newIssueNotes, run.newMRNotes = issueNotes, mrNotes, newIssueNotes, newMRNotes
	run.handoffNote, run.prior = handoffNote, prior
	return nil
}

// buildPrompt picks the implementer's role and session and builds its
// prompts: the whole thread for a new session, what is new for a resumed
// one, both with the decisions to revisit.
func (run *implementRun) buildPrompt(ctx context.Context) error {
	r, st, task, wf, issue, milestone, botID := run.r, run.state, run.task, run.wf, run.issue, run.milestone, run.botID
	// The model and effort labels ("/agent use", "/agent effort", which the
	// operator turns into labels) override the profile.
	role, profile, roleNotes := run.spec.IssueRole(issue.Labels)
	inv := roleInvocation(role, implementSystemPrompt, implementTools, implementSchema, false)
	inv.Role, inv.Kind = "implement", aiv1.CallKindImplement
	inv.SystemPrompt += resourcesNote(r.AgentMemoryLimit, r.AgentCPUs) + toolchainsNote(run.base, r.Mirror.PinFiles(ctx, run.base), r.changedPins)
	// While the MR is a draft, the work is pushed step by step so a human can
	// follow it and steer with comments (not while concluding a merge, whose
	// conflicts must be resolved before anything is committed).
	stepwise := *wf.StepCommits && run.mr.Draft && task.Spec.Source.MergeBaseFor == task.Status.Run.MergedBaseFor
	plainSystem := inv.SystemPrompt
	if stepwise {
		inv.SystemPrompt += stepsNote
		inv.JSONSchema = implementStepSchema
	}
	inv.DisallowedTools = implementDenied
	// A session another engine continued meanwhile has missed that work:
	// the implementer starts fresh, with the whole thread and the notes.
	if st.ImplementEngine != "" && st.ImplementEngine != role.Engine {
		st.setSession("implement", role.Engine, "")
	}
	inv.Resume = st.session("implement", role.Engine)
	// Comments that are only a model or effort command are the operator's.
	prefix := wf.CommandPrefix
	fullPrompt := buildImplementPrompt(issue, milestone, command.WithoutRouting(run.issueNotes, prefix), command.WithoutRouting(run.mrNotes, prefix), botID, false, run.prior)
	inv.Prompt = buildImplementPrompt(issue, milestone, command.WithoutRouting(run.newIssueNotes, prefix), command.WithoutRouting(run.newMRNotes, prefix), botID, true, handoff{})
	fullPrompt, inv.Prompt = fullPrompt+run.ownersRules, inv.Prompt+run.ownersRules
	if chat := buildChatContext(task.Spec.Source.ChatContext); run.deskOnly && chat != "" {
		fullPrompt += "\n\n" + chat
	}
	if task.Spec.Autopilot {
		fullPrompt = autopilotImplementNote + "\n\n" + fullPrompt
		inv.Prompt = autopilotImplementNote + "\n\n" + inv.Prompt
	}
	if reason := task.Spec.Source.AutopilotPause; reason != "" {
		fullPrompt = autopilotPauseNote(reason) + "\n\n" + fullPrompt
		inv.Prompt = autopilotPauseNote(reason) + "\n\n" + inv.Prompt
	}
	notices := roleNotes
	var active, ignored []revisit
	for _, rv := range run.revisits {
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
	revisits := active
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
	run.role, run.profile, run.inv, run.stepwise, run.plainSystem, run.fullPrompt = role, profile, inv, stepwise, plainSystem, fullPrompt
	run.notices, run.revisits, run.ignored = notices, revisits, ignored
	return nil
}

// addInputs adds what the operator handed this run to the prompts: the
// failed jobs of a CI fix run and an instruction accepted from the pilot.
func (run *implementRun) addInputs(ctx context.Context) error {
	r, task, inv, botID := run.r, run.task, &run.inv, run.botID
	// A CI fix run: the operator saw the pipeline of the last push fail.
	ciPipeline := task.Spec.Source.CIFixPipelineID
	run.ciFix = ciPipeline != 0 && ciPipeline != task.Status.Run.CIFixedPipelineID
	if run.ciFix {
		run.out.CIFixedPipelineID = ciPipeline
		s := r.step(ctx, "read failed CI jobs", "pipeline", ciPipeline)
		ciPrompt, err := buildCIPrompt(ctx, run.gl, run.glProject, ciPipeline)
		if err != nil {
			return s.fail(err)
		}
		s.done(fmt.Sprintf("pipeline %d", ciPipeline))
		run.fullPrompt += "\n\n" + ciPrompt
		if len(run.newIssueNotes)+len(run.newMRNotes) == 0 {
			inv.Prompt = ciPrompt
		} else {
			inv.Prompt += "\n\n" + ciPrompt
		}
	}

	// An instruction an owner accepted from the pilot counts as human input.
	run.humanInput = humanNotes(run.newIssueNotes, botID) + humanNotes(run.newMRNotes, botID)
	if instruction := pilotInstruction(task); instruction != "" {
		run.fullPrompt += "\n\n" + instruction
		inv.Prompt = instruction + "\n\n" + inv.Prompt
		run.humanInput++
	}
	// Owners' answers to the agent's questions: each reaches the session
	// once, a fresh session gets them all.
	run.answers = promptAnswers(task, *run.state, false)
	if all := promptAnswers(task, *run.state, true); len(all) > 0 {
		run.fullPrompt += "\n\n" + buildPromptAnswers(all)
	}
	if len(run.answers) > 0 {
		inv.Prompt = buildPromptAnswers(run.answers) + "\n\n" + inv.Prompt
		run.humanInput++
	}
	return nil
}

// mergeBase merges the default branch into the branch of a conflict run and
// decides whether the implementer runs at all: a clean merge and a re-review
// need no agent.
func (run *implementRun) mergeBase(ctx context.Context) error {
	r, task, inv, base, branch, botID := run.r, run.task, &run.inv, run.base, run.branch, run.botID
	// A conflict run: the operator saw the merge request conflict with the
	// default branch. The default branch is merged in; the implementer
	// resolves the conflicts. A clean merge needs no agent.
	mergeFor := task.Spec.Source.MergeBaseFor
	mergeBase := mergeFor != "" && mergeFor != task.Status.Run.MergedBaseFor
	run.mergeMessage = fmt.Sprintf("Merge branch '%s' into %s", base, branch)
	run.runAgent = true
	if mergeBase {
		run.out.MergedBaseFor = mergeFor
		s := r.step(ctx, "merge "+base, "base", base)
		merge, err := r.Agent.MergeBase(ctx, base, run.mergeMessage)
		if err != nil {
			return s.fail(fmt.Errorf("merging %s: %w", base, err))
		}
		run.merge = merge
		switch {
		case merge.UpToDate:
			s.done("the branch already contains " + base)
		case len(merge.Conflicts) == 0:
			s.done("merged without conflicts")
			run.notices = append(run.notices, fmt.Sprintf("Merged `%s` into the branch; there were no conflicts.", base))
		default:
			s.done(fmt.Sprintf("conflicts in %d files", len(merge.Conflicts)), "files", merge.Conflicts)
			conflictPrompt := buildConflictPrompt(base, merge.Conflicts)
			run.fullPrompt += "\n\n" + conflictPrompt
			if run.humanInput == 0 && !run.ciFix {
				inv.Prompt = conflictPrompt
			} else {
				inv.Prompt = conflictPrompt + "\n\n" + inv.Prompt
			}
		}
		run.runAgent = len(merge.Conflicts) > 0 || run.humanInput > 0 || run.ciFix || len(run.revisits) > 0 || len(run.ignored) > 0
	}
	// A re-review run: only the reviewers run again on the head they failed
	// on (or "/agent review" asked for), unless the branch moved on since or
	// humans wrote something for the implementer.
	run.reviewFor = task.Spec.Source.ReviewFor
	run.reviewAsked = run.reviewFor != "" && run.reviewFor != task.Status.Run.ReviewedFor
	if run.reviewAsked {
		prefix := run.wf.CommandPrefix
		head, _ := r.Mirror.resolve(ctx, "refs/remotes/origin/"+branch)
		forImplementer := humanNotes(command.WithoutRouting(run.newIssueNotes, prefix), botID) + humanNotes(command.WithoutRouting(run.newMRNotes, prefix), botID)
		run.reReview = head == run.reviewFor && forImplementer == 0 && !run.ciFix && !mergeBase && len(run.revisits) == 0 && pilotInstruction(task) == "" && len(run.answers) == 0
		run.runAgent = run.runAgent && !run.reReview
	}
	return nil
}

// implement runs the implementer, step by step while the MR is a draft,
// unless only the merge or the review is due.
func (run *implementRun) implement(ctx context.Context) error {
	r, st, out, inv, answer, role, botID := run.r, run.state, run.out, &run.inv, &run.answer, run.role, run.botID
	if !run.stepwise && inv.Resume != "" {
		inv.Prompt = noStepsNote + "\n\n" + inv.Prompt
	}
	if run.runAgent {
		s := r.step(ctx, "run implementer", "profile", run.profile, "engine", role.Engine, "model", role.Model, "effort", role.Effort,
			"resume", inv.Resume != "", "ciFix", run.ciFix, "conflicts", len(run.merge.Conflicts))
		switch {
		case len(run.merge.Conflicts) > 0:
			inv.Kind = aiv1.CallKindConflict
		case run.ciFix:
			inv.Kind = aiv1.CallKindCI
		}
		res, err := r.invoke(ctx, role, *inv, run.fullPrompt)
		run.res = res
		if res != nil {
			out.CostUSD += res.CostUSD
		}
		if err != nil {
			return s.fail(err)
		}
		st.setSession("implement", ranOn(role, res), res.SessionID)
		st.ImplementEngine = ranOn(role, res)
		st.deliveredPrompts(run.answers)
		st.LastIssueNoteID = maxNoteID(run.issueNotes, st.LastIssueNoteID)
		st.LastMRNoteID = maxNoteID(run.mrNotes, st.LastMRNoteID)
		if err := r.saveState(*st); err != nil {
			return s.fail(err)
		}
		if err := res.Decode(answer); err != nil {
			return s.fail(fmt.Errorf("decoding implementer answer: %w", err))
		}
		s.done(agentResult(role, res, answer.Status), "session", res.SessionID, "costUSD", res.CostUSD,
			"status", answer.Status, "questions", len(answer.Questions), "prompts", len(answer.Prompts), "decisions", len(answer.Decisions))
	} else {
		// Only the merge or the review: nothing for the agent to do; review
		// and CI follow.
		run.res = &Result{SessionID: inv.Resume}
		*answer = ImplementAnswer{Status: "done", Summary: fmt.Sprintf("Merged `%s` into the branch.", run.base)}
		switch {
		case run.reReview:
			answer.Summary = fmt.Sprintf("🔁 Reviewed `%s` again; the implementer did not run.", shortSHA(run.reviewFor))
		case run.merge.UpToDate:
			answer.Summary = fmt.Sprintf("The branch already contains `%s`; nothing to merge.", run.base)
		}
	}
	if run.stepwise && run.runAgent {
		res, seen, err := r.runSteps(ctx, run.gl, run.glProject, run.issue, run.mr.IID, botID, run.wf.CommandPrefix, run.branch, run.base, role,
			run.spec.Agents.Implement.StepSession == aiv1.StepSessionFresh, *inv, run.fullPrompt, run.res, answer, st, out)
		run.res = res
		if err != nil {
			// The steps pushed so far are on the branch: their decisions
			// must not be lost with the run.
			if botID != 0 {
				r.postDecisions(ctx, run.gl, run.glProject, run.issue, run.issueNotes, botID, run.wf.CommandPrefix, answer.Decisions, run.revisits)
			}
			return err
		}
		run.seenNote = max(run.seenNote, seen)
		if answer.Status == "in_progress" {
			run.outOfSteps = true
			run.notices = append(run.notices, fmt.Sprintf("Stopped after %d steps although the agent was not done, so the MR stays a draft. %s.", maxSteps,
				r.steer(run.spec, run.task, fmt.Sprintf("Comment `%s continue` to go on", run.wf.CommandPrefix), "Ask for a new run")))
		}
	}
	if answer.Status == "in_progress" {
		answer.Status = "done" // an engine that ignored the schema
	}
	// Fix rounds and later calls finish their work in one go.
	inv.SystemPrompt, inv.JSONSchema = run.plainSystem, implementSchema
	return nil
}

// concludeMerge concludes the merge of a conflict run, or undoes it when the
// conflicts are not resolved, and commits the implementer's work.
func (run *implementRun) concludeMerge(ctx context.Context) error {
	r, answer, merge, base := run.r, &run.answer, run.merge, run.base
	if len(merge.Conflicts) > 0 {
		// Conclude the merge before anything else is committed: a merge
		// with conflict markers left must never be committed.
		s := r.step(ctx, "conclude merge", "files", len(merge.Conflicts))
		giveUp := answer.Status == "needs_input" || answer.asks()
		fin, err := r.Agent.FinishMerge(ctx, FinishMerge{Base: base, Message: run.mergeMessage, From: merge.From, Files: merge.Conflicts, Abort: giveUp})
		if err != nil {
			return s.fail(fmt.Errorf("concluding the merge: %w", err))
		}
		if fin.Merged {
			s.done("conflicts resolved")
			run.notices = append(run.notices, fmt.Sprintf("Merged `%s` into the branch and resolved the conflicts in %s.", base, codeList(merge.Conflicts)))
		} else {
			// Back to the branch as it was: a human has to look.
			left := fin.Conflicts
			if len(left) == 0 {
				left = merge.Conflicts
			}
			s.done("not resolved, merge undone", "files", left)
			answer.Status = "needs_input"
			run.notices = append(run.notices, fmt.Sprintf("Could not resolve the merge conflicts with `%s` in %s, so the merge was undone. "+
				"Merge `%s` into the branch by hand, or %s.", base, codeList(left), base, r.steer(run.spec, run.task, fmt.Sprintf("comment `%s …` with guidance", run.wf.CommandPrefix), "give guidance")))
		}
	}
	return r.commit(ctx, commitMessage(answer.CommitMessage, run.issue))
}

// reviewLoop has read-only reviewers see the diff; blocking findings go back
// to the (resumed) implementer, bounded by maxIterations.
func (run *implementRun) reviewLoop(ctx context.Context) error {
	r, st, out, answer, role, issue, base := run.r, run.state, run.out, &run.answer, run.role, run.issue, run.base
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
		diff, stat, err := r.Agent.Diff(ctx, base)
		if err != nil {
			return s.fail(err)
		}
		if strings.TrimSpace(diff) == "" {
			out.Reviewed, out.BlockingFindings, out.ReviewErrors, out.ReviewErrorsTransient, out.ReviewErrorKinds = true, 0, 0, false, nil
			s.done("no changes to review")
			break
		}
		findings, cost, errs, kinds := r.review(ctx, review, issue, planDecisionsText(run.issueNotes, run.botID), base, stat, diff, run.ownersRules)
		out.CostUSD += cost
		run.reviewNotes = errs
		transient := 0
		for _, k := range kinds {
			if k.Kind == aiv1.ReviewErrorTransient {
				transient++
			}
		}
		out.Reviewed, out.ReviewErrors, out.ReviewErrorsTransient, out.ReviewErrorKinds = true, int32(len(errs)), len(errs) > 0 && transient == len(errs), kinds
		run.reviewedLast = true
		// Reviewers are read-only; discard anything they touched anyway.
		if err := r.Agent.Reset(ctx); err != nil {
			return s.fail(err)
		}
		var blocking []Finding
		for _, f := range findings {
			if f.Blocking() {
				blocking = append(blocking, f)
			}
		}
		s.done(fmt.Sprintf("%s; %d findings, %d blocking, $%.2f", diffSummary(stat), len(findings), len(blocking), cost),
			"diff", diffSummary(stat), "findings", len(findings), "blocking", len(blocking), "reviewerErrors", len(errs), "costUSD", cost)
		run.remaining = findings
		out.BlockingFindings = int32(len(blocking))
		if len(blocking) == 0 || iter >= int(*review.MaxIterations) {
			break
		}
		s = r.step(ctx, fmt.Sprintf("fix round %d", iter+1), "blocking", len(blocking))
		fix := run.inv
		fix.Kind = aiv1.CallKindFix
		fix.Resume = run.res.SessionID
		fixPrompt := buildFixPrompt(blocking)
		fix.Prompt = fixPrompt
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
		if err := r.commit(ctx, commitMessage(fixed.CommitMessage, issue)); err != nil {
			return err
		}
		run.reviewedLast = false
	}
	return nil
}

// push marks the merge request ready or draft and pushes the branch, and
// starts a pipeline where the head has none yet.
func (run *implementRun) push(ctx context.Context) error {
	r, gl, glProject, st, out, answer, task, mr, branch, base := run.r, run.gl, run.glProject, run.state, run.out, &run.answer, run.task, run.mr, run.branch, run.base
	// Ready means reviewed and clean. The MR leaves draft before the push, so
	// the push gets its pipeline also where drafts run none; a run that
	// leaves problems puts it back to draft.
	readied := false
	switch {
	case answer.Status == "needs_input" || answer.asks() || run.outOfSteps || out.Reviewed && (out.BlockingFindings > 0 || out.ReviewErrors > 0):
		r.setDraft(ctx, gl, glProject, mr, st, true)
	case (out.Reviewed && run.reviewedLast || len(run.spec.Agents.Review.Reviewers) == 0) && r.hasChanges(ctx, base):
		readied = r.setDraft(ctx, gl, glProject, mr, st, false)
	}

	// Push and open / update the merge request.
	s := r.step(ctx, "push", "branch", branch)
	pushed, err := r.publish(ctx, branch, base, false)
	if err != nil {
		if readied {
			// Ready would vouch for a head that is not this run's work.
			r.setDraft(ctx, gl, glProject, mr, st, true)
		}
		return s.fail(err)
	}
	run.pushed = pushed
	if pushed {
		out.PushedSHA, _ = r.Mirror.resolve(ctx, "refs/heads/"+branch)
		out.CIRound = ciRound(task, run.ciFix)
		s.done(fmt.Sprintf("%s at %s", branch, shortSHA(out.PushedSHA)), "sha", out.PushedSHA)
	} else if r.pushedHead != "" {
		// The last step pushed it all already, without CI: that head is the
		// one CI has to check, and it needs a pipeline started.
		out.PushedSHA = r.pushedHead
		out.CIRound = ciRound(task, run.ciFix)
		s.done(fmt.Sprintf("the last step pushed %s already", shortSHA(out.PushedSHA)), "sha", out.PushedSHA)
	} else if head, _ := r.Mirror.resolve(ctx, "refs/remotes/origin/"+branch); head != "" && head != task.Status.Run.PushedSHA && r.Mirror.HasChanges(ctx, base, head) {
		// Someone else pushed to the branch (or made it) since the last
		// recorded push, and this run took it over unchanged: that head is
		// the one CI has to check.
		out.PushedSHA = head
		s.done(fmt.Sprintf("nothing new to push; the branch moved to %s", shortSHA(head)), "sha", head)
	} else {
		s.done("nothing new to push")
	}
	if !pushed && (readied || r.pushedHead != "") {
		// Marking ready starts no pipeline, projects that skip pipelines for
		// drafts may have run none for the head yet, and steps are pushed
		// without CI.
		head := r.pushedHead
		if head == "" {
			head, _ = r.Mirror.resolve(ctx, "refs/remotes/origin/"+branch)
		}
		r.startPipeline(ctx, gl, glProject, mr, task, head)
	}
	if out.Reviewed && run.reviewedLast {
		// The last review saw the branch as it is now.
		out.ReviewedSHA = out.PushedSHA
		if out.ReviewedSHA == "" {
			out.ReviewedSHA, _ = r.Mirror.resolve(ctx, "refs/remotes/origin/"+branch)
		}
	}
	return nil
}

// postDecisionsAndHandoff stores the agent notes and posts the decisions.
func (run *implementRun) postDecisionsAndHandoff(ctx context.Context) error {
	r, st, botID := run.r, run.state, run.botID
	// The notes go right after the push, so a later failure (merge request,
	// comment) does not lose them. Without the bot's user ID the note could
	// not be found again.
	if botID != 0 {
		r.updateHandoff(ctx, run.gl, run.glProject, run.issue.IID, run.handoffNote, run.answer.Handoff, run.seenNote)
		run.notices = append(run.notices, r.postDecisions(ctx, run.gl, run.glProject, run.issue, run.issueNotes, botID, run.wf.CommandPrefix, run.answer.Decisions, run.revisits)...)
		// Revisits count as handled once their decisions are posted; a run
		// that fails before applies them again.
		st.RevisitNoteID = run.seenNote
		if err := r.saveState(*st); err != nil {
			r.log().Error(err, "saving the state after posting decisions")
			return err
		}
	}
	return nil
}

// report describes the change in the merge request, comments the run's
// report and moves the issue to the next column.
func (run *implementRun) report(ctx context.Context) error {
	r, gl, glProject, out, answer, task, spec, wf, issue, mr := run.r, run.gl, run.glProject, run.out, &run.answer, run.task, run.spec, run.wf, run.issue, run.mr
	// The description covers the whole change; the first one replaces the
	// first report's summary (the task status keeps it).
	// An agent may leave the summary empty (cursor does when it changed
	// nothing): the report and the task status still say what the run did.
	if strings.TrimSpace(answer.Summary) == "" {
		answer.Summary = emptySummary(run.pushed || r.pushedHead != "", out.PushedSHA)
	}
	reported := *answer
	if !spec.Agents.Describe.Disabled {
		first, notice, err := r.describe(ctx, gl, glProject, spec.Agents.Describe, issue, mr.IID, run.base, answer.Summary, run.state, out)
		if err != nil {
			return err
		}
		if notice != "" {
			run.notices = append(run.notices, notice)
		}
		// A run that stops for input keeps saying why.
		if first && answer.Status != "needs_input" && !answer.asks() {
			reported.Summary = "📝 Described the change in the merge request description."
		}
	}
	drafts := questionDrafts(*answer)
	out.Questions = drafts[:min(len(drafts), aiv1.MaxQuestionDrafts)]
	desk := desklink.Task(r.DeskURL, task.Namespace, task.Spec.ProjectRef, task.Name)
	report := renderReport(reported, run.remaining, append(run.reviewNotes, run.notices...), desk)
	if run.ciFix {
		report = "CI fix: " + report
	}
	// Report back and move the issue to the next column - unless another
	// run was requested meanwhile, in which case the issue stays "working".
	// The run log covers the steps up to here.
	needsInput := answer.Status == "needs_input" || answer.asks()
	run.needsInput = needsInput
	// With the CI loop, a fresh push stays in "working" until the operator
	// has seen its pipeline go green (or hands it back).
	waitForCI := !spec.CI.Disabled && out.PushedSHA != "" && !needsInput
	pending := !waitForCI && r.morePending(ctx, task.Spec.RunRequest)
	// The owners are mentioned where the answer is wanted: questions on the
	// issue, by the operator's comments of their prompts, everything else on
	// the MR. On autopilot nobody is: it merges what reaches review, and its
	// pause on the tracking issue mentions them for the rest.
	toReview := !needsInput && !waitForCI && !pending
	var mrMention string
	switch {
	case task.Spec.Autopilot:
	case len(drafts) > 0:
	case needsInput && spec.Notify.Wants(aiv1.NotifyNeedsInput),
		toReview && spec.Notify.Wants(aiv1.NotifyReview):
		mrMention = notify.Mention(task.Spec.Owners)
	}
	runLogMD := r.run.markdown(out.CostUSD)
	s := r.step(ctx, "comment")
	if _, err := gl.CreateMergeRequestNote(ctx, glProject, mr.IID, "🤖 "+report+mrMention+runLogMD); err != nil {
		return s.fail(fmt.Errorf("commenting on merge request: %w", err))
	}
	// The questions go on the issue as prompts: the operator posts one
	// comment per question from out.Questions.
	s.done(fmt.Sprintf("on !%d", mr.IID))
	s = r.step(ctx, "update issue labels")
	switch {
	case waitForCI:
		s.done(fmt.Sprintf("stays in %s until the pipeline is green", wf.WorkingLabel))
	case pending:
		s.done(fmt.Sprintf("stays in %s, another run is pending", wf.WorkingLabel))
	default:
		next := wf.ReviewLabel
		if needsInput {
			next = wf.NeedsInputLabel
		}
		if _, err := gl.UpdateIssue(ctx, glProject, issue.IID, gitlab.UpdateIssueOptions{
			AddLabels: []string{next}, RemoveLabels: []string{wf.WorkingLabel, wf.ReadyLabel},
		}); err != nil {
			return s.fail(fmt.Errorf("updating issue labels: %w", err))
		}
		s.done("moved to "+next, "label", next)
		if next == wf.ReviewLabel && !task.Spec.Autopilot && *spec.Notify.RequestReview && len(task.Spec.Owners) > 0 {
			s = r.step(ctx, "request review")
			if names, err := notify.RequestReview(ctx, gl, glProject, mr.IID, task.Spec.Owners); err != nil {
				s.done("failed: "+textutil.Truncate(err.Error(), 300), "error", err.Error())
			} else if len(names) == 0 {
				s.done("none requested: the merge request has reviewers, or the owners are unknown")
			} else {
				s.done("@"+strings.Join(names, ", @"), "reviewers", names)
			}
		}
	}
	return nil
}

// outcome completes the run's outcome once everything is reported.
func (run *implementRun) outcome(ctx context.Context) *Outcome {
	out := run.out
	out.Result = aiv1.RunResultSucceeded
	if run.needsInput {
		out.Result = aiv1.RunResultNeedsInput
	}
	if run.reviewAsked && !run.r.morePending(ctx, run.task.Spec.RunRequest) {
		// Handled: a re-review, or a normal run since the head moved on. A
		// request that came meanwhile (another "/agent review") still sees it.
		out.ReviewedFor = run.reviewFor
	}
	out.Summary = run.answer.Summary
	return out
}

// setDraft marks the MR draft or ready, if it is not already, and reports
// whether it marked it ready. A draft the runner did not make after it last
// marked the MR ready is a human holding it, and stays. A failure is reported
// in the run log but does not fail the run: the work is done.
func (r *Runner) setDraft(ctx context.Context, gl gitlab.API, glProject string, mr *gitlab.MergeRequest, st *state, draft bool) bool {
	// The run can take long: draft state and title as they are now, so a
	// rename in the meantime is not undone.
	if cur, err := gl.GetMergeRequest(ctx, glProject, mr.IID); err == nil {
		mr.Draft, mr.Title = cur.Draft, cur.Title
	}
	defer func() {
		if err := r.saveState(*st); err != nil {
			r.log().Error(err, "saving the draft state")
		}
	}()
	if mr.Draft == draft {
		st.Ready = !draft
		return false
	}
	name, title := "mark ready", gitlab.ReadyTitle(mr.Title)
	if draft {
		name, title = "mark draft", gitlab.DraftTitle(mr.Title)
	}
	s := r.step(ctx, name, "mr", mr.IID)
	if !draft && st.Ready {
		s.done(fmt.Sprintf("!%d stays a draft: marked draft by hand", mr.IID))
		return false
	}
	res, err := gl.UpdateMergeRequest(ctx, glProject, mr.IID, gitlab.UpdateMergeRequestOptions{Title: title})
	if err != nil {
		_ = s.fail(fmt.Errorf("updating merge request !%d: %w", mr.IID, err))
		return false
	}
	mr.Draft, mr.Title = res.Draft, res.Title
	st.Ready = !res.Draft
	s.done(fmt.Sprintf("!%d %q", mr.IID, res.Title))
	return !res.Draft
}

// startPipeline starts a merge request pipeline for the head of a MR this run
// marked ready without pushing, unless CI already checked that head. A failure
// only means the head goes without CI, so it is not returned.
func (r *Runner) startPipeline(ctx context.Context, gl gitlab.API, glProject string, mr *gitlab.MergeRequest, task *aiv1.AgentTask, head string) {
	if ci := task.Status.CI; ci != nil && ci.SHA == head && ci.State != aiv1.CIStateNone {
		return
	}
	// Merge request pipelines also run for pushes with ci.skip: one may be
	// running for the head already. A skipped one does not count.
	if ps, err := gl.ListPipelines(ctx, glProject, gitlab.ListPipelinesOptions{SHA: head, Limit: 5}); err == nil {
		for _, p := range ps {
			if p.Status != "skipped" {
				return
			}
		}
	}
	s := r.step(ctx, "start pipeline", "mr", mr.IID)
	err := gl.CreateMergeRequestPipeline(ctx, glProject, mr.IID)
	if code := gitlab.StatusCode(err); code >= 400 && code < 500 {
		// No merge request pipelines in this project: a branch pipeline.
		if berr := gl.CreatePipeline(ctx, glProject, mr.SourceBranch); berr == nil {
			s.done("branch pipeline for " + mr.SourceBranch)
			return
		}
	}
	if err != nil {
		s.done("none: "+err.Error(), "error", err.Error())
		return
	}
	s.done(fmt.Sprintf("for !%d", mr.IID))
}

// hasChanges reports whether the work branch changes anything against the
// default branch; an MR with only the start commit is not ready.
func (r *Runner) hasChanges(ctx context.Context, base string) bool {
	diff, _, err := r.Agent.Diff(ctx, base)
	return err != nil || strings.TrimSpace(diff) != ""
}

// openMergeRequest opens the draft merge request of an issue before the
// agent starts. A new branch gets an empty start commit, pushed without CI;
// the agent's checkout then continues from it.
func (r *Runner) openMergeRequest(ctx context.Context, gl gitlab.API, glProject string, issue *gitlab.Issue, branch, base string) (*gitlab.MergeRequest, error) {
	s := r.step(ctx, "open merge request", "branch", branch, "target", base)
	started := false
	if !r.Mirror.HasRemoteBranch(ctx, branch) {
		// One line, no "[skip ci]": merge commit templates may copy it to
		// the default branch. The push itself skips CI.
		if _, err := r.Mirror.StartBranch(ctx, branch, base, fmt.Sprintf("Start work on #%d", issue.IID)); err != nil {
			return nil, s.fail(fmt.Errorf("creating branch %s: %w", branch, err))
		}
		started = true
	}
	opts := gitlab.CreateMergeRequestOptions{
		SourceBranch:       branch,
		TargetBranch:       base,
		Title:              "Draft: " + issue.Title,
		Description:        newDescription(issue.IID),
		RemoveSourceBranch: true,
	}
	// The issue's milestone lists the MR in its "Merge requests" tab.
	if issue.Milestone != nil {
		opts.MilestoneID = issue.Milestone.ID
	}
	mr, err := gl.CreateMergeRequest(ctx, glProject, opts)
	if err != nil {
		return nil, s.fail(fmt.Errorf("creating merge request: %w", err))
	}
	s.done(fmt.Sprintf("!%d", mr.IID), "mr", mr.IID, "url", mr.WebURL, "newBranch", started)
	if _, err := gl.CreateIssueNote(ctx, glProject, issue.IID, fmt.Sprintf("🤖 Working on this in %s; progress and reports are there.", mr.WebURL)); err != nil {
		r.log().Info("could not point the issue to the merge request", "error", err.Error())
	}
	if started {
		// The checkout was made before the branch existed.
		if err := r.prepareCheckout(ctx, branch, base); err != nil {
			return nil, err
		}
	}
	return mr, nil
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

// review runs all configured reviewers in parallel. A reviewer that fails
// transiently is called again (review.retries). Reviewer failures do not
// fail the run; they are reported alongside the result, with why each
// reviewer failed. ownersRules (the milestone's prompt rules)
// end every reviewer's prompt.
func (r *Runner) review(ctx context.Context, spec aiv1.ReviewSpec, issue *gitlab.Issue, planDecisions, base, stat, diff, ownersRules string) ([]Finding, float64, []string, []aiv1.ReviewError) {
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
			prompt := buildReviewPrompt(issue, planDecisions, base, stat, diff, spec.MaxInlineDiff(kind)) + ownersRules
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
	return notify.Defuse(renderReportText(answer, findings, reviewErrs, desk))
}

func renderReportText(answer ImplementAnswer, findings []Finding, reviewErrs []string, desk string) string {
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
	return b.String()
}

// steer is how an owner steers task, for a report: viaComment for a project
// that takes GitLab input, else what to do in the Desk, with the task's link.
func (r *Runner) steer(spec aiv1.AgentProjectSpec, task *aiv1.AgentTask, viaComment, inDesk string) string {
	if spec.Workflow.Takes(aiv1.InputGitLab) {
		return viaComment
	}
	if link := desklink.Task(r.DeskURL, task.Namespace, task.Spec.ProjectRef, task.Name); link != "" {
		return inDesk + " in the Desk: " + link
	}
	return inDesk + " in the Desk"
}

// emptySummary stands in for a summary the agent did not write.
func emptySummary(pushed bool, sha string) string {
	if pushed {
		return fmt.Sprintf("The agent left no summary. This run pushed its changes (up to `%s`); the diff shows them.", shortSHA(sha))
	}
	return "The agent left no summary and changed nothing in this run."
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

// ownNotes keeps the bot's own notes (reports, decisions, plan decisions).
func ownNotes(notes []gitlab.Note, botID int64) []gitlab.Note {
	return slices.DeleteFunc(notes, func(n gitlab.Note) bool { return n.System || botID == 0 || n.Author.ID != botID })
}

// maxSteps bounds the steps of one run.
var maxSteps = 25

// runSteps continues an implementer that returned "in_progress": the step is
// committed and pushed without CI, and the session resumed with the comments
// written in the meantime, until the agent is done (or out of steps). answer
// collects the steps: decisions add up, the rest is the last step's. It
// returns the newest comment handed over.
//
// A command ("/agent …") is not handed over: the operator starts a run for
// it, which handles it (a revisit with its decision) once this one is done.
// So comments are read up to the first command only.
//
// With freshSteps (stepSession: fresh) every step after the first starts a
// new session with the run's prompt, the earlier steps' summaries, the agent
// notes and the comments handed over so far. Only the last step's session
// becomes the implementer's, once the steps are done.
func (r *Runner) runSteps(ctx context.Context, gl gitlab.API, glProject string, issue *gitlab.Issue, mrIID, botID int64, prefix, branch, base string,
	role aiv1.RoleSpec, freshSteps bool, inv Invocation, fullPrompt string, res *Result, answer *ImplementAnswer, st *state, out *Outcome) (*Result, int64, error) {
	var seen int64
	pushSteps := true
	summaries := []string{answer.Summary}
	var handedIssue, handedMR []gitlab.Note
	// last is the last fresh step that ran: the next run resumes it.
	var last *Result
	defer func() {
		if last == nil {
			return
		}
		st.setSession("implement", ranOn(role, last), last.SessionID)
		st.ImplementEngine = ranOn(role, last)
		if err := r.saveState(*st); err != nil {
			r.log().Error(err, "saving the implementer's session")
		}
	}()
	for step := 1; answer.Status == "in_progress" && step < maxSteps; step++ {
		if err := r.commit(ctx, commitMessage(answer.CommitMessage, issue)); err != nil {
			return res, seen, err
		}
		if pushSteps {
			s := r.step(ctx, fmt.Sprintf("push step %d", step), "branch", branch)
			switch pushed, err := r.publish(ctx, branch, base, true); {
			case err != nil:
				// Someone pushed to the branch meanwhile: the steps are pushed
				// with the run, which reports a conflict then.
				pushSteps = false
				s.done("not pushed, the next steps neither: "+err.Error(), "error", err.Error())
			case pushed:
				s.done(fmt.Sprintf("%s at %s, without CI", branch, shortSHA(r.pushedHead)), "sha", r.pushedHead)
			default:
				s.done("nothing new to push")
			}
		}

		// Comments written during the step steer the next one.
		// Without GitLab input only the MR's review threads do.
		s := r.step(ctx, "read comments")
		var issueNotes []gitlab.Note
		if r.projectSpec.Workflow.Takes(aiv1.InputGitLab) {
			var err error
			if issueNotes, err = gl.ListIssueNotes(ctx, glProject, issue.IID); err != nil {
				return res, seen, s.fail(fmt.Errorf("listing issue notes: %w", err))
			}
			_, issueNotes = splitHandoff(issueNotes, botID)
			issueNotes = r.withoutPilot(issueNotes)
		}
		mrNotes, err := gl.ListMergeRequestNotes(ctx, glProject, mrIID)
		if err != nil {
			return res, seen, s.fail(fmt.Errorf("listing merge request notes: %w", err))
		}
		mrNotes = r.withoutPilot(slices.DeleteFunc(mrNotes, func(n gitlab.Note) bool { return isProgress(n, botID) }))
		newIssue, newMR := newerThan(issueNotes, st.LastIssueNoteID), newerThan(mrNotes, st.LastMRNoteID)
		if cut := firstCommand(append(slices.Clone(newIssue), newMR...), botID, prefix); cut != 0 {
			fromCut := func(n gitlab.Note) bool { return n.ID >= cut }
			newIssue, newMR = slices.DeleteFunc(newIssue, fromCut), slices.DeleteFunc(newMR, fromCut)
		}
		fresh := humanNotes(newIssue, botID) + humanNotes(newMR, botID)
		s.done(fmt.Sprintf("%d new", fresh), "newNotes", fresh)
		next := inv
		next.Kind = aiv1.CallKindImplement
		next.Resume = res.SessionID
		next.Prompt = stepContinue
		if fresh > 0 {
			next.Prompt = stepContinue + "\n\n" + thread("New comments on the issue", newIssue, botID) + thread("New comments on the merge request", newMR, botID)
		}
		// Should the session be gone, a fresh one also gets the new comments.
		stepPrompt := fullPrompt + "\n\n" + next.Prompt
		if freshSteps {
			next.Resume = ""
			stepPrompt = fullPrompt + "\n\n" + buildFreshStepPrompt(summaries, answer.Handoff, handedIssue, handedMR, botID) + "\n\n" + next.Prompt
		}

		s = r.step(ctx, fmt.Sprintf("run implementer, step %d", step+1), "engine", role.Engine, "model", role.Model, "newNotes", fresh, "resume", next.Resume != "")
		res, err = r.invoke(ctx, role, next, stepPrompt)
		if res != nil {
			out.CostUSD += res.CostUSD
		}
		if err != nil {
			return res, seen, s.fail(err)
		}
		if freshSteps {
			last = res
			handedIssue, handedMR = append(handedIssue, newIssue...), append(handedMR, newMR...)
		} else {
			st.setSession("implement", ranOn(role, res), res.SessionID)
			st.ImplementEngine = ranOn(role, res)
		}
		st.LastIssueNoteID = maxNoteID(newIssue, st.LastIssueNoteID)
		st.LastMRNoteID = maxNoteID(newMR, st.LastMRNoteID)
		seen = max(seen, maxNoteID(newIssue, 0), maxNoteID(newMR, 0))
		if err := r.saveState(*st); err != nil {
			return res, seen, s.fail(err)
		}
		var more ImplementAnswer
		if err := res.Decode(&more); err != nil {
			return res, seen, s.fail(fmt.Errorf("decoding implementer answer: %w", err))
		}
		more.Decisions = append(answer.Decisions, more.Decisions...)
		if strings.TrimSpace(more.Handoff) == "" {
			more.Handoff = answer.Handoff
		}
		*answer = more
		summaries = append(summaries, more.Summary)
		s.done(agentResult(role, res, answer.Status), "session", res.SessionID, "costUSD", res.CostUSD, "status", answer.Status)
	}
	return res, seen, nil
}

// firstCommand returns the ID of the oldest human comment that is a command,
// 0 if there is none.
func firstCommand(notes []gitlab.Note, botID int64, prefix string) int64 {
	var first int64
	for _, n := range notes {
		// A pure model or effort command starts no run: it is no cut.
		if n.System || (botID != 0 && n.Author.ID == botID) || !strings.HasPrefix(strings.TrimSpace(n.Body), prefix) || command.IsRoutingOnly(n.Body, prefix) {
			continue
		}
		if first == 0 || n.ID < first {
			first = n.ID
		}
	}
	return first
}

// ciRound is the CI fix round a push of this run counts as: a CI fix run
// is one more, a run the pilot started keeps the count (so it cannot start
// another full set of fix rounds), any other run starts over.
func ciRound(task *aiv1.AgentTask, ciFix bool) int32 {
	switch {
	case ciFix:
		return task.Status.Run.CIRound + 1
	case task.Spec.Source.PilotRun != 0 && task.Spec.Source.PilotRun == task.Spec.RunRequest:
		return task.Status.Run.CIRound
	}
	return 0
}
