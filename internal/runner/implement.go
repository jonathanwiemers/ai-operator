package runner

import (
	"context"
	"fmt"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
	"slices"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/desklink"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// runImplement implements an issue: implement -> review -> fix (bounded) ->
// push -> merge request -> report back on GitLab.
func (r *Runner) runImplement(ctx context.Context, gl gitlab.API, task *aiv1.AgentTask, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) (_ *Outcome, err error) {
	if task.Spec.Issue == nil {
		return nil, fmt.Errorf("implement task has no issue")
	}
	run := r.newImplementRun(gl, task, project, spec)
	defer func() { r.endCut(ctx, err) }()
	if closed, err := run.prepare(ctx); err != nil || closed {
		return run.out, err
	}
	for _, phase := range []func(context.Context) error{
		run.syncBranch, run.readComments, run.buildPrompt, run.addInputs, run.mergeBase, run.implement,
		run.concludeMerge, run.companionChange, run.reviewLoop, run.push, run.postDecisionsAndHandoff, run.report,
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
	// companion is the task's companion repository; nil without one, or
	// when the pod has no token for it.
	companion *companionTree
	// companionMR is the companion's merge request once the push found or
	// opened it; homeLink is the home merge request's companion line, ""
	// without one.
	companionMR *gitlab.MergeRequest
	homeLink    string

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
	// systemNotes follow the role's system prompt (resources, toolchains,
	// Conventional Commits): a conflict call on its own role gets them too.
	systemNotes string
	fullPrompt  string
	stepwise    bool
	// stepSession is the effective step-session mode: the arm's, else
	// agents.implement.stepSession.
	stepSession aiv1.StepSessionMode
	ciFix       bool
	humanInput  int
	// answers are the owners' prompt answers this session has not seen.
	answers      []aiv1.PromptAnswerDelivery
	merge        MergeResult
	mergeMessage string
	// conflictPrompt asks to resolve the merge's conflicts; conflictOnly is
	// set when that is all the implementer is asked to do in this run.
	conflictPrompt string
	conflictOnly   bool
	reviewFor      string
	reviewAsked    bool
	reReview       bool
	runAgent       bool
	// types are the Conventional Commits types, nil while the option is off.
	types []string

	// What the agents did.
	res         *Result
	answer      ImplementAnswer
	outOfSteps  bool
	remaining   []Finding
	reviewNotes []string
	// reviewedLast is false when a fix round came after the last review;
	// reviewedCompanion is set when the last review covered a companion
	// change as well.
	reviewedLast      bool
	reviewedCompanion bool
	pushed            bool
	needsInput        bool
	// checkpointHead is set when the branch head after the push is a
	// checkpoint: nobody is asked to review it.
	checkpointHead bool
}

func (r *Runner) newImplementRun(gl gitlab.API, task *aiv1.AgentTask, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) *implementRun {
	return &implementRun{r: r, gl: gl, task: task, spec: spec, wf: spec.Workflow,
		glProject: spec.GitLab.Project, botID: project.Status.BotUserID, out: &Outcome{}, types: commitTypes(spec.Workflow)}
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
	r.Mirror.Policy = homePushPolicy(glProject, run.proj)
	s.done("default branch "+run.base, "defaultBranch", run.base)
	return false, run.readCompanion(ctx)
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
	run.branch, out.Branch, out.Base = branch, branch, base

	if err := r.syncWorkspace(ctx, run.proj.HTTPURLToRepo, branch, base, run.checkpointLostAttempt); err != nil {
		return err
	}
	if run.companion != nil {
		if err := r.syncCompanion(ctx, run.companion); err != nil {
			return err
		}
		// From here on every home push checks the companion first.
		r.companion = run.companion
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
		if mr, err = r.openMergeRequest(ctx, gl, glProject, issue, branch, base, run.types); err != nil {
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
	// operator turns into labels) override the profile. An experiment arm
	// stands in for the planner's profile and effort labels.
	labels := issue.Labels
	run.stepSession = run.spec.Agents.Implement.StepSession
	if arm, ok := run.experimentArm(ctx); ok {
		planned, _ := plannedLabels(issue.Description)
		labels = armLabels(wf, labels, planned, arm)
		if arm.StepSession != "" {
			run.stepSession = arm.StepSession
		}
	}
	role, profile, roleNotes := run.spec.IssueRole(labels)
	role, routeNotes, err := run.routeForCompanion(ctx, role)
	if err != nil {
		return err
	}
	roleNotes = append(routeNotes, roleNotes...)
	inv := roleInvocation(role, implementSystemPrompt, implementTools, implementSchemaFor(run.types), false)
	inv.Role, inv.Kind = "implement", aiv1.CallKindImplement
	if run.companion != nil {
		inv.Companion = run.companion.slug
	}
	// The notes after the role's system prompt, which a conflict call on its
	// own role gets as well.
	systemNotes := resourcesNote(r.AgentMemoryLimit, r.AgentCPUs) + toolchainsNote(run.base, r.Mirror.PinFiles(ctx, run.base), r.changedPins)
	if run.types != nil {
		systemNotes += conventionalNote(run.types)
	}
	inv.SystemPrompt += systemNotes
	// While the MR is a draft, the work is pushed step by step so a human can
	// follow it and steer with comments (not while concluding a merge, whose
	// conflicts must be resolved before anything is committed).
	stepwise := *wf.StepCommits && run.mr.Draft && task.Spec.Source.MergeBaseFor == task.Status.Run.MergedBaseFor
	plainSystem := inv.SystemPrompt
	if stepwise {
		// The step-size gate tells the implementer to close a step that
		// grows too big (claude only, see withStepGate).
		inv.StepGateMaxLines = run.spec.MaxStepLines(profile)
		inv.SystemPrompt += stepsNote(inv.StepGateMaxLines) + conventionalPointer(run.types)
		inv.JSONSchema = implementStepSchemaFor(run.types)
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
	// The branch still ends in the checkpoint: the files are what the cut-off
	// call left. A head that moved on (a human push, a later step) is not.
	if cp := task.Status.Run.Checkpoint; cp != nil {
		if head, _ := r.Mirror.resolve(ctx, "refs/remotes/origin/"+run.branch); head != "" && head == cp.SHA {
			note := checkpointResumeNote(cp.Reason, cp.Step)
			fullPrompt = note + "\n\n" + fullPrompt
			inv.Prompt = note + "\n\n" + inv.Prompt
		}
	}
	// The notices of prepare (a companion left out) come first.
	notices := append(run.notices, roleNotes...)
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
	r.stepSession = ""
	if stepwise {
		r.stepSession = string(run.stepSession)
	}
	run.systemNotes = systemNotes
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
		// The companion's pipeline is read with the companion's client.
		gl, project, companion := run.gl, run.glProject, task.Spec.Source.CIFixProject != ""
		if companion {
			project = task.Spec.Source.CIFixProject
			if cgl := run.companionGL(); cgl != nil {
				gl = cgl
			}
		}
		s := r.step(ctx, "read failed CI jobs", "pipeline", ciPipeline, "project", project)
		ciPrompt, err := buildCIPrompt(ctx, gl, project, ciPipeline, companion, conventionalPointer(run.types))
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
	run.mergeMessage = mergeMessage(base, branch, run.types)
	run.runAgent = true
	if mergeBase {
		run.out.MergedBaseFor = mergeFor
		s := r.step(ctx, "merge "+base, "base", base)
		merge, err := r.Agent.MergeBase(ctx, MergeRequest{Base: base, Message: run.mergeMessage})
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
			conflictPrompt := buildConflictPrompt(base, merge.Conflicts) + conventionalPointer(run.types)
			run.fullPrompt += "\n\n" + conflictPrompt
			if run.humanInput == 0 && !run.ciFix {
				inv.Prompt = conflictPrompt
			} else {
				inv.Prompt = conflictPrompt + "\n\n" + inv.Prompt
			}
			run.conflictPrompt = conflictPrompt
			run.conflictOnly = run.humanInput == 0 && !run.ciFix && len(run.revisits) == 0 && len(run.ignored) == 0
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
		if c := run.companion; c != nil {
			// A re-review covers both trees as they are pushed.
			companion, _ := c.mirror.resolve(ctx, "refs/remotes/origin/"+c.spec.Branch)
			head = aiv1.ReviewHeads(head, companion)
		}
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
	run.withCompanionPrompt()
	if !run.stepwise && inv.Resume != "" {
		inv.Prompt = noStepsNote + "\n\n" + inv.Prompt
	}
	// Until the push, a call that is cut off leaves a checkpoint.
	if !run.spec.Checkpoint.Disabled {
		r.cut = &cutTarget{gl: run.gl, glProject: run.glProject, branch: run.branch, base: run.base, issue: run.issue.IID, mr: run.mr, st: st}
	}
	if run.runAgent {
		r.cutStep(false)
		switch {
		case len(run.merge.Conflicts) > 0:
			inv.Kind = aiv1.CallKindConflict
		case run.ciFix:
			inv.Kind = aiv1.CallKindCI
		}
		// A conflict role (agents.implement.conflict) may resolve the
		// conflicts instead; a fresh call keeps the implementer's session.
		callRole, call, callFull := role, *inv, run.fullPrompt
		conflictRole, conflictCall, conflictFull, fresh, ok := run.conflictCall(ctx)
		if ok {
			callRole, call, callFull = conflictRole, conflictCall, conflictFull
		}
		s := r.step(ctx, "run implementer", "profile", run.profile, "engine", callRole.Engine, "model", callRole.Model, "effort", callRole.Effort,
			"resume", call.Resume != "", "ciFix", run.ciFix, "conflicts", len(run.merge.Conflicts), "conflictRole", ok, "arm", r.arm)
		res, err := r.invoke(ctx, callRole, call, callFull)
		run.res = res
		if res != nil {
			out.CostUSD += res.CostUSD
		}
		if err != nil {
			return s.fail(err)
		}
		if fresh {
			// Fix rounds go on in the implementer's session, not in the
			// conflict call's.
			kept := *res
			kept.SessionID = inv.Resume
			run.res = &kept
		} else {
			st.setSession("implement", ranOn(callRole, res), res.SessionID)
			st.ImplementEngine = ranOn(callRole, res)
		}
		st.deliveredPrompts(run.answers)
		st.LastIssueNoteID = maxNoteID(run.issueNotes, st.LastIssueNoteID)
		st.LastMRNoteID = maxNoteID(run.mrNotes, st.LastMRNoteID)
		if err := r.saveState(*st); err != nil {
			return s.fail(err)
		}
		if err := res.Decode(answer); err != nil {
			return s.fail(fmt.Errorf("decoding implementer answer: %w", err))
		}
		s.done(agentResult(callRole, res, answer.Status), "session", res.SessionID, "costUSD", res.CostUSD,
			"status", answer.Status, "questions", len(answer.Questions), "prompts", len(answer.Prompts), "decisions", len(answer.Decisions))
	} else {
		// Only the merge or the review: nothing for the agent to do; review
		// and CI follow.
		run.res = &Result{SessionID: inv.Resume}
		*answer = ImplementAnswer{Status: "done", Summary: fmt.Sprintf("Merged `%s` into the branch.", run.base)}
		switch {
		case run.reReview:
			answer.Summary = fmt.Sprintf("🔁 Reviewed %s again; the implementer did not run.", reviewedHeads(run.reviewFor))
		case run.merge.UpToDate:
			answer.Summary = fmt.Sprintf("The branch already contains `%s`; nothing to merge.", run.base)
		}
	}
	if run.stepwise && run.runAgent {
		res, seen, err := r.runSteps(ctx, run.gl, run.glProject, run.issue, run.mr.IID, botID, run.wf.CommandPrefix, run.branch, run.base, run.types, role,
			run.stepSession == aiv1.StepSessionFresh, *inv, run.fullPrompt, run.res, answer, st, out)
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
	inv.SystemPrompt, inv.JSONSchema, inv.StepGateMaxLines = run.plainSystem, implementSchemaFor(run.types), 0
	return nil
}

// conflictCall returns the call that resolves the conflicts of a run on the
// conflict role (agents.implement.conflict, see ConflictRole); ok is false
// without one, or when the run asks more of the implementer than the
// conflicts (comments, a CI fix, revisits): the implementer resolves them in
// its session as before. A fresh call (freshSession, or the conflict role on
// another engine) starts a new session with a self-contained prompt and
// leaves the implementer's session alone; otherwise it resumes the
// implementer's session with its prompt. With a companion only a claude
// conflict role is used, the only engine that works on a second checkout.
func (run *implementRun) conflictCall(ctx context.Context) (role aiv1.RoleSpec, inv Invocation, fullPrompt string, fresh, ok bool) {
	if len(run.merge.Conflicts) == 0 || !run.conflictOnly {
		return aiv1.RoleSpec{}, Invocation{}, "", false, false
	}
	role, fresh, ok = run.spec.ConflictRole(run.role)
	if !ok {
		return aiv1.RoleSpec{}, Invocation{}, "", false, false
	}
	if run.companion != nil {
		if role.Engine != aiv1.EngineClaude {
			run.r.log().Info("conflict role ignored: the companion needs claude", "engine", role.Engine, "model", role.Model)
			return aiv1.RoleSpec{}, Invocation{}, "", false, false
		}
		if role.Fallback != nil && role.Fallback.Engine != aiv1.EngineClaude {
			role.Fallback = nil
		}
	}
	// The implementer's call with the conflict role's model, effort, budget,
	// tools and instructions.
	own := roleInvocation(role, implementSystemPrompt, implementTools, run.inv.JSONSchema, false)
	inv = run.inv
	inv.SystemPrompt = own.SystemPrompt + run.systemNotes
	inv.Model, inv.Effort, inv.MaxBudgetUSD, inv.AllowedTools = own.Model, own.Effort, own.MaxBudgetUSD, own.AllowedTools
	inv.BashMaxOutputChars = own.BashMaxOutputChars
	inv.Kind = aiv1.CallKindConflict
	if fresh = fresh || role.Engine != run.role.Engine; !fresh {
		return role, inv, run.fullPrompt, false, true
	}
	// During the merge HEAD is still the branch: the stat is the MR's.
	_, stat, err := run.r.Agent.Diff(ctx, homeTree, run.base)
	if err != nil {
		run.r.log().Info("conflict prompt without the diff stat", "error", err.Error())
	}
	prompt := buildConflictFreshPrompt(run.issue, run.prior.Text, stat, run.conflictPrompt)
	if run.companion != nil {
		prompt = companionPrompt(run.companion) + "\n\n" + prompt
	}
	inv.Resume, inv.Prompt = "", prompt
	return role, inv, prompt, true, true
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
	if !run.runAgent {
		// No step of the agent ends here: nothing replaces a checkpoint.
		return r.commitTree(ctx, homeTree, commitMessage(answer.CommitMessage, run.issue, run.types))
	}
	return r.commit(ctx, run.branch, base, commitMessage(answer.CommitMessage, run.issue, run.types))
}

// push marks the merge request ready or draft and pushes the branch, and
// starts a pipeline where the head has none yet.
func (run *implementRun) push(ctx context.Context) error {
	r, gl, glProject, st, out, answer, task, mr, branch, base := run.r, run.gl, run.glProject, run.state, run.out, &run.answer, run.task, run.mr, run.branch, run.base
	r.endCut(ctx, nil) // the implementer is done
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
	// A checkpoint counts as no push: no pipeline, never PushedSHA or
	// ReviewedSHA, no reviewers, and the MR stays a draft. It is one this run
	// pushed, or one an earlier run left as the head and this run added
	// nothing to.
	stepHead := r.stepHead()
	head := r.pushedHead
	if head == "" {
		head, _ = r.Mirror.resolve(ctx, "refs/remotes/origin/"+branch)
	}
	if run.checkpointHead = run.isCheckpoint(head); readied && run.checkpointHead {
		r.setDraft(ctx, gl, glProject, mr, st, true)
		readied = false
	}
	if pushed {
		out.PushedSHA, _ = r.Mirror.resolve(ctx, "refs/heads/"+branch)
		out.CIRound = ciRound(task, run.ciFix)
		s.done(fmt.Sprintf("%s at %s", branch, shortSHA(out.PushedSHA)), "sha", out.PushedSHA)
	} else if stepHead != "" {
		// The last step pushed it all already, without CI: that head is the
		// one CI has to check, and it needs a pipeline started.
		out.PushedSHA = stepHead
		out.CIRound = ciRound(task, run.ciFix)
		s.done(fmt.Sprintf("the last step pushed %s already", shortSHA(out.PushedSHA)), "sha", out.PushedSHA)
	} else if head, _ := r.Mirror.resolve(ctx, "refs/remotes/origin/"+branch); head != "" && !run.isCheckpoint(head) && head != task.Status.Run.PushedSHA && r.Mirror.HasChanges(ctx, base, head) {
		// Someone else pushed to the branch (or made it) since the last
		// recorded push, and this run took it over unchanged: that head is
		// the one CI has to check.
		out.PushedSHA = head
		s.done(fmt.Sprintf("nothing new to push; the branch moved to %s", shortSHA(head)), "sha", head)
	} else {
		s.done("nothing new to push")
	}
	if !pushed && (readied || stepHead != "") {
		// Marking ready starts no pipeline, projects that skip pipelines for
		// drafts may have run none for the head yet, and steps are pushed
		// without CI.
		head := stepHead
		if head == "" {
			head, _ = r.Mirror.resolve(ctx, "refs/remotes/origin/"+branch)
		}
		if !run.isCheckpoint(head) {
			r.startPipeline(ctx, gl, glProject, mr, task, head)
		}
	}
	if out.Reviewed && run.reviewedLast {
		// The last review saw the branch as it is now.
		out.ReviewedSHA = out.PushedSHA
		if out.ReviewedSHA == "" {
			out.ReviewedSHA, _ = r.Mirror.resolve(ctx, "refs/remotes/origin/"+branch)
		}
		if run.isCheckpoint(out.ReviewedSHA) {
			out.ReviewedSHA = ""
		}
	}
	// The companion goes after the home branch (D157-P1); the merge gate
	// holds the home merge request until the companion is green anyway.
	return run.pushCompanion(ctx)
}

// isCheckpoint reports whether sha is a checkpoint: the one this run pushed,
// or the task's last one.
func (run *implementRun) isCheckpoint(sha string) bool {
	return sha != "" && (sha == run.r.checkpointHead || run.task.Status.Run.CheckpointHead(sha))
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
		answer.Summary = emptySummary(run.pushed || r.stepHead() != "", out.PushedSHA)
	}
	reported := *answer
	if !spec.Agents.Describe.Disabled {
		first, notice, err := r.describe(ctx, spec.Agents.Describe, issue, answer.Summary, describeTarget{
			gl: gl, project: glProject, mrIID: mr.IID, repo: homeTree, base: run.base, described: &run.state.DescribedDiff, links: []string{run.homeLink},
			types: run.types, title: &run.state.MRTitle,
		}, run.state, out)
		if err != nil {
			return err
		}
		if notice != "" {
			run.notices = append(run.notices, notice)
		}
		if notice, err = run.describeCompanion(ctx); err != nil {
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
	notices := append(append(run.reviewNotes, run.notices...), r.run.keptNotices()...)
	if n := r.run.rewriteNotice(); n != "" {
		notices = append(notices, n)
	}
	report := renderReport(reported, run.remaining, notices, desk)
	if run.ciFix {
		report = "CI fix: " + report
	}
	// Report back and move the issue to the next column - unless another
	// run was requested meanwhile, in which case the issue stays "working".
	// The run log covers the steps up to here.
	needsInput := answer.Status == "needs_input" || answer.asks()
	run.needsInput = needsInput
	// With the CI loop, a fresh push stays in "working" until the operator
	// has seen its pipeline go green (or hands it back); so does a fresh
	// companion push next to a pushed home head.
	pushedCI := out.PushedSHA != "" || out.CompanionPushed && task.Status.Run.PushedSHA != ""
	waitForCI := !spec.CI.Disabled && pushedCI && !needsInput
	pending := !waitForCI && r.morePending(ctx, task.Spec.RunRequest)
	// The owners are mentioned where the answer is wanted: questions on the
	// issue, by the operator's comments of their prompts, everything else on
	// the MR. On autopilot nobody is: it merges what reaches review, and its
	// pause on the tracking issue mentions them for the rest.
	toReview := !needsInput && !waitForCI && !pending && !run.checkpointHead
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
	note, err := gl.CreateMergeRequestNote(ctx, glProject, mr.IID, "🤖 "+report+mrMention+runLogMD)
	if err != nil {
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
			// The cause goes before the label; the final patch keeps it.
			out.NeedsInputCause, out.NeedsInputURL = aiv1.NeedsInputAgentQuestion, noteURL(mr.WebURL, note)
			if err := r.patchRun(ctx, func(s *aiv1.RunStatus) {
				s.NeedsInputCause, s.NeedsInputURL = out.NeedsInputCause, out.NeedsInputURL
			}); err != nil {
				return s.fail(fmt.Errorf("recording the needs-input cause: %w", err))
			}
		}
		if _, err := gl.UpdateIssue(ctx, glProject, issue.IID, gitlab.UpdateIssueOptions{
			AddLabels: []string{next}, RemoveLabels: []string{wf.WorkingLabel, wf.ReadyLabel},
		}); err != nil {
			return s.fail(fmt.Errorf("updating issue labels: %w", err))
		}
		s.done("moved to "+next, "label", next)
		if next == wf.ReviewLabel && !task.Spec.Autopilot && *spec.Notify.RequestReview && len(task.Spec.Owners) > 0 {
			s = r.step(ctx, "request review")
			if run.checkpointHead {
				s.done("none requested: the head is a checkpoint")
			} else if names, err := notify.RequestReview(ctx, gl, glProject, mr.IID, task.Spec.Owners); err != nil {
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

// commit commits the agent's changes in the home tree as a step of the run
// on branch. A commit that folds the checkpoints at the tip of the branch
// (CommitStep) leaves the lease for the next publish.
func (r *Runner) commit(ctx context.Context, branch, base string, message agentMessage) error {
	s := r.step(ctx, "commit")
	res, err := r.Agent.CommitStep(ctx, CommitRequest{Message: message.text, Branch: branch, Base: base, Pushed: r.pushedHead})
	switch {
	case err != nil:
		return s.fail(err)
	case res.FoldedFrom != "":
		r.committed(message)
		r.noteFold(res)
		s.done(fmt.Sprintf("committed, folding %d checkpoint(s) up to %s", res.Folded, shortSHA(res.FoldedFrom)),
			"committed", true, "folded", res.Folded, "foldedFrom", res.FoldedFrom)
	case res.Committed:
		r.committed(message)
		s.done("committed", "committed", true)
	default:
		s.done("no changes", "committed", false)
	}
	return nil
}

// commitTree commits the agent's changes in the checkout of repo (homeTree
// or a companion's slug) as a step of the run.
func (r *Runner) commitTree(ctx context.Context, repo string, message agentMessage) error {
	name, kv := "commit", []any(nil)
	if repo != homeTree {
		name, kv = "commit companion", []any{"repo", repo}
	}
	s := r.step(ctx, name, kv...)
	committed, err := r.Agent.Commit(ctx, repo, message.text)
	if err != nil {
		return s.fail(err)
	}
	if committed {
		if repo == homeTree {
			r.committed(message) // a companion commit repeats the home commit's message
		}
		s.done("committed", "committed", true)
	} else {
		s.done("no changes", "committed", false)
	}
	return nil
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
