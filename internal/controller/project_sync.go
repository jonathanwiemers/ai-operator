package controller

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// projectPoll is the state of one GitLab poll of a project (sync): what the
// setup read, and what the phases fill in for the steps after them.
type projectPoll struct {
	r       *AgentProjectReconciler
	gl      gitlab.API
	gate    *commandGate
	project *aiv1.AgentProject
	spec    aiv1.AgentProjectSpec
	started time.Time
	titleRE *regexp.Regexp
	prefix  string

	tasks       aiv1.AgentTaskList
	byMilestone map[int64]*aiv1.AgentTask
	byIssue     map[int64]*aiv1.AgentTask
	pilots      map[int64]*aiv1.AgentTask
	keep        map[string]bool

	// Milestone descriptions by milestone ID.
	descriptions map[int64]string
	// The older milestones still open, by milestone IID.
	ahead map[int64][]int64
	// Autopilot milestones by IID, with their spec task.
	autopilots map[int64]*aiv1.AgentTask
	specTasks  []*aiv1.AgentTask
	// The autopilot that drives the main-red issue, if any.
	lane                 *aiv1.AgentTask
	mainRed, urgentIssue int64

	// The agent-labelled issues of the poll.
	issues    map[int64]gitlab.Issue
	implTasks map[int64]*aiv1.AgentTask
	// The home merge requests the issue sync read, by task name.
	homeMRs map[string]*gitlab.MergeRequest
	// One failing issue does not hold up the other issues or the steps
	// below: its error is collected and returned at the end.
	errs []error
}

// newProjectPoll starts a poll with gl: the bot user, the webhook, the
// command gate and the project's tasks.
func (r *AgentProjectReconciler) newProjectPoll(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, started time.Time) (*projectPoll, error) {
	// The username on every poll, for mentions of a renamed bot.
	if project.Status.BotUserID == 0 || *spec.Pilot.Mention {
		u, err := gl.CurrentUser(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolving bot user: %w", err)
		}
		if project.Status.BotUserID == 0 {
			project.Status.BotUserID = u.ID
		}
		project.Status.BotUsername = u.Username
	}
	r.syncWebhook(ctx, gl, project, spec)

	gate := r.commandGate(gl, project, spec)

	titleRE, err := titlePattern(spec)
	if err != nil {
		return nil, err
	}

	p := &projectPoll{
		r: r, gl: gl, gate: gate, project: project, spec: spec, started: started, titleRE: titleRE,
		prefix:      spec.Workflow.CommandPrefix,
		byMilestone: map[int64]*aiv1.AgentTask{},
		byIssue:     map[int64]*aiv1.AgentTask{},
		pilots:      map[int64]*aiv1.AgentTask{},
		keep:        map[string]bool{},
		autopilots:  map[int64]*aiv1.AgentTask{},
		issues:      map[int64]gitlab.Issue{},
		implTasks:   map[int64]*aiv1.AgentTask{},
		homeMRs:     map[string]*gitlab.MergeRequest{},
	}
	if err := r.List(ctx, &p.tasks, client.InNamespace(project.Namespace), client.MatchingLabels{LabelProject: project.Name}); err != nil {
		return nil, err
	}
	for i := range p.tasks.Items {
		t := &p.tasks.Items[i]
		switch t.Spec.Type {
		case aiv1.TaskTypeSpec:
			if t.Spec.Milestone != nil {
				p.byMilestone[t.Spec.Milestone.IID] = t
			}
		case aiv1.TaskTypeImplement:
			if t.Spec.Issue != nil {
				p.byIssue[t.Spec.Issue.IID] = t
			}
		case aiv1.TaskTypeCoordinate:
			if t.Spec.Milestone != nil {
				p.pilots[t.Spec.Milestone.IID] = t
			}
		}
	}
	return p, nil
}

// The owners of a milestone and its issues.
func (p *projectPoll) owners(description string) []string {
	return notify.Resolve(description, p.prefix, p.spec.Notify.Users, p.r.DefaultOwners)
}

func (p *projectPoll) ownerSubjects(description string) []string {
	return notify.MilestoneOwnerSubjects(description, p.prefix)
}

// syncMilestones keeps one spec task per selected milestone and picks the
// lane and the main-red issue.
func (p *projectPoll) syncMilestones(ctx context.Context) error {
	r, project, spec := p.r, p.project, p.spec
	// Milestones -> spec tasks.
	milestones, err := p.gl.ListMilestones(ctx, spec.GitLab.Project)
	if err != nil {
		return fmt.Errorf("listing milestones: %w", err)
	}
	var active int32
	p.descriptions = map[int64]string{}
	for _, m := range milestones {
		p.descriptions[m.ID] = m.Description
	}
	// Before any spec task changes: the older milestones still open.
	p.ahead = queuedAhead(spec, p.titleRE, milestones, p.byMilestone)
	for _, m := range milestones {
		selected, autopilot := milestoneSelection(spec, p.titleRE, m)
		if !selected {
			continue
		}
		active++
		task, err := r.syncSpecTask(ctx, project, spec, m, p.byMilestone[m.IID], autopilot, p.ahead[m.IID], p.owners(m.Description), p.ownerSubjects(m.Description))
		if err != nil {
			return err
		}
		p.keep[task.Name] = true
		p.byMilestone[m.IID] = task
		p.specTasks = append(p.specTasks, task)
		if autopilot || task.Status.Autopilot != nil {
			p.autopilots[m.IID] = task
		}
	}
	project.Status.ActiveMilestones = active
	// One autopilot drives the main-red issue (the main-red lane): the
	// oldest milestone that is not done.
	for _, st := range p.autopilots {
		if st.Spec.Autopilot && (st.Status.Autopilot == nil || st.Status.Autopilot.Phase != aiv1.AutopilotDone) && (p.lane == nil || st.Spec.Milestone.IID < p.lane.Spec.Milestone.IID) {
			p.lane = st
		}
	}
	// Handed over by autoFix or by an owner: only agent-labelled issues are
	// listed below. The fix goes first in the run queue with or without a
	// lane.
	if mh := r.freshMainHealth(spec, project.Status.MainHealth, mainHealthStale); mh != nil {
		p.urgentIssue = mh.IssueIID
		if p.lane != nil && *spec.MainHealth.FreezeAutopilot {
			p.mainRed = mh.IssueIID
		}
	}
	return nil
}

// listIssues reads the issues with an agent label.
func (p *projectPoll) listIssues(ctx context.Context) error {
	project, spec := p.project, p.spec
	// Issues with an agent label -> implement tasks.
	for _, label := range spec.Workflow.ActiveLabels() {
		list, err := p.gl.ListIssues(ctx, spec.GitLab.Project, gitlab.ListIssuesOptions{Labels: []string{label}})
		if err != nil {
			return fmt.Errorf("listing issues with label %q: %w", label, err)
		}
		for _, is := range list {
			// The main-red issue handed to the agent (by autoFix or an
			// owner) has no milestone, nor may a flaky one.
			mh := project.Status.MainHealth
			autoFix := mh != nil && (mh.AutoFixIssueIID == is.IID || mh.IssueIID == is.IID) || flakyLatch(mh, is.IID) != nil
			if p.titleRE != nil && !autoFix && (is.Milestone == nil || !p.titleRE.MatchString(is.Milestone.Title)) {
				continue
			}
			p.issues[is.IID] = is
		}
	}
	return nil
}

// issueDescription is the description of the issue's milestone, empty
// without one.
func (p *projectPoll) issueDescription(is gitlab.Issue) string {
	description := ""
	if is.Milestone != nil {
		// Closed milestones are not listed; the issue carries its own.
		var ok bool
		if description, ok = p.descriptions[is.Milestone.ID]; !ok {
			description = is.Milestone.Description
		}
	}
	return description
}

// issuePhases are the per-issue steps of a poll: the implement task, its
// questions, run block, blocked state and CI.
type issuePhases struct {
	*projectPoll
}

// run runs the phases issue by issue, in this order: implement, questions,
// run block, blocked, CI. An issue's error is collected in errs; a failed
// implement, run block or blocked phase skips the issue's later phases.
func (ph issuePhases) run(ctx context.Context) {
	for _, is := range ph.issues {
		task, mr, err := ph.implement(ctx, is)
		if err != nil {
			ph.errs = append(ph.errs, fmt.Errorf("issue #%d: %w", is.IID, err))
			continue
		}
		if err := ph.questions(ctx, is, task); err != nil {
			ph.errs = append(ph.errs, fmt.Errorf("issue #%d: %w", is.IID, err))
		}
		if err := ph.runBlock(ctx, is, task); err != nil {
			ph.errs = append(ph.errs, fmt.Errorf("issue #%d: %w", is.IID, err))
			continue
		}
		if err := ph.blocked(ctx, is, task); err != nil {
			ph.errs = append(ph.errs, fmt.Errorf("issue #%d: %w", is.IID, err))
			continue
		}
		if err := ph.ci(ctx, is, task, mr); err != nil {
			ph.errs = append(ph.errs, fmt.Errorf("issue #%d: %w", is.IID, err))
		}
	}
}

// implement keeps the issue's implement task and returns it with its home
// merge request. On an error the existing task is kept.
func (ph issuePhases) implement(ctx context.Context, is gitlab.Issue) (*aiv1.AgentTask, *gitlab.MergeRequest, error) {
	autopilot, pause, budgetStop := false, "", false
	var ms issueMilestone
	if is.Milestone != nil {
		if st := ph.autopilots[is.Milestone.IID]; st != nil {
			ms.autopilot = st.Spec.Autopilot
			// Only issues of the queue are merged by autopilot; others
			// in the milestone follow the normal flow.
			autopilot = st.Spec.Autopilot && onAutopilot(st, is.IID)
			// The pause reason goes in the same update as a run request,
			// so the run sees it; a pause from this poll is copied next poll.
			if ap := st.Status.Autopilot; autopilot && ap != nil && ap.Phase == aiv1.AutopilotPaused && ap.PausedFor == is.IID {
				pause = ap.PausedReason
			}
			budgetStop = budgetStopped(st, is.IID)
		}
	}
	if is.IID == ph.mainRed {
		autopilot = true // merged by the lane
	}
	description := ph.issueDescription(is)
	if is.Milestone != nil {
		ms.description = description
	}
	var ap *aiv1.AgentTask // the autopilot whose queue has the issue
	if autopilot && is.Milestone != nil {
		ap = ph.autopilots[is.Milestone.IID]
	}
	task, mr, err := ph.r.syncImplementTask(ctx, ph.gl, ph.gate, ph.project, ph.spec, is, ph.byIssue[is.IID], autopilot, pause, budgetStop, is.IID == ph.urgentIssue, ms, ph.owners(description), ph.ownerSubjects(description), ap)
	if err != nil {
		// Keep the existing task, so the cleanup below does not delete
		// it and the autopilot still sees it.
		if t := ph.byIssue[is.IID]; t != nil {
			ph.keep[t.Name] = true
			ph.implTasks[is.IID] = t
		}
		return nil, nil, err
	}
	ph.keep[task.Name] = true
	ph.implTasks[is.IID] = task
	ph.homeMRs[task.Name] = mr
	return task, mr, nil
}

// questions syncs the questions of the issue's task.
func (ph issuePhases) questions(ctx context.Context, is gitlab.Issue, task *aiv1.AgentTask) error {
	var st *aiv1.AgentTask
	if is.Milestone != nil {
		st = ph.byMilestone[is.Milestone.IID]
	}
	return ph.r.syncQuestions(ctx, ph.gl, ph.spec, st, task)
}

// runBlock blocks the issue while its run waits (a usage limit).
func (ph issuePhases) runBlock(ctx context.Context, is gitlab.Issue, task *aiv1.AgentTask) error {
	return ph.r.syncRunBlock(ctx, ph.gl, ph.spec, is, task)
}

// blocked syncs the issue's blocked label with its task.
func (ph issuePhases) blocked(ctx context.Context, is gitlab.Issue, task *aiv1.AgentTask) error {
	return ph.r.syncBlocked(ctx, ph.gl, ph.spec, is, task)
}

// ci follows the pipeline of the issue's merge request.
func (ph issuePhases) ci(ctx context.Context, is gitlab.Issue, task *aiv1.AgentTask, mr *gitlab.MergeRequest) error {
	if task.Status.Blocked != nil {
		return nil // its CI is followed again once the block is lifted
	}
	// With the milestone's switches (standing rules).
	return ph.r.syncCI(ctx, ph.gl, rules.For(ph.spec, ph.issueDescription(is), ph.prefix), ph.project.Status.MainHealth, is, task, mr)
}
