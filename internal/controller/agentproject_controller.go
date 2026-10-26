package controller

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// GitLabFactory creates a GitLab client; replaced in tests.
type GitLabFactory func(baseURL, token string) gitlab.API

// AgentProjectReconciler polls GitLab for an AgentProject and keeps one
// AgentTask per planned milestone and per issue that is handed to the agent.
type AgentProjectReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	NewGitLab GitLabFactory
	Now       func() time.Time
	// DefaultOwners are mentioned for projects that set no notify.users.
	DefaultOwners []string
}

// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentprojects,verbs=get;list;watch
// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentprojects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agenttasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *AgentProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var project aiv1.AgentProject
	if err := r.Get(ctx, req.NamespacedName, &project); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	spec := project.Spec.WithDefaults()
	orig := project.DeepCopy()

	if spec.Suspend {
		meta.SetStatusCondition(&project.Status.Conditions, metav1.Condition{
			Type: "Synced", Status: metav1.ConditionFalse, Reason: "Suspended", Message: "project is suspended",
		})
		return ctrl.Result{}, r.Status().Patch(ctx, &project, client.MergeFrom(orig))
	}

	// A slow or rate-limiting GitLab must not hold the worker for long: the
	// client's retries stop at this deadline, and the next poll goes on.
	sctx, cancel := context.WithTimeout(ctx, max(spec.PollInterval.Duration, syncTimeout))
	syncErr := r.sync(sctx, &project, spec)
	cancel()
	switch {
	case syncErr != nil && gitlab.IsTransient(syncErr):
		// Rate limits, server errors and timeouts pass; the client already
		// retried, and the next poll tries again. No stack trace for those.
		logger.Info("GitLab sync failed, trying again on the next poll", "error", syncErr.Error())
	case syncErr != nil:
		logger.Error(syncErr, "GitLab sync failed")
	}
	if syncErr != nil {
		meta.SetStatusCondition(&project.Status.Conditions, metav1.Condition{
			Type: "Synced", Status: metav1.ConditionFalse, Reason: "SyncFailed", Message: truncateMsg(syncErr.Error()),
		})
	} else {
		now := metav1.NewTime(r.now())
		project.Status.LastSyncTime = &now
		meta.SetStatusCondition(&project.Status.Conditions, metav1.Condition{
			Type: "Synced", Status: metav1.ConditionTrue, Reason: "Synced", Message: "GitLab state is in sync",
		})
	}
	if err := r.Status().Patch(ctx, &project, client.MergeFrom(orig)); err != nil {
		return ctrl.Result{}, err
	}
	// Errors are reported in the status; retry on the regular poll interval
	// instead of hammering GitLab with exponential backoff.
	return ctrl.Result{RequeueAfter: spec.PollInterval.Duration}, nil
}

// syncTimeout is the least time a poll of GitLab may take.
const syncTimeout = 2 * time.Minute

func (r *AgentProjectReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *AgentProjectReconciler) gitlabClient(ctx context.Context, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) (gitlab.API, error) {
	ref := spec.GitLab.TokenSecretRef
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: project.Namespace, Name: ref.Name}, &secret); err != nil {
		return nil, fmt.Errorf("reading GitLab token secret: %w", err)
	}
	token := strings.TrimSpace(string(secret.Data[ref.Key]))
	if token == "" {
		return nil, fmt.Errorf("secret %s has no key %q", ref.Name, ref.Key)
	}
	return r.NewGitLab(spec.GitLab.URL, token), nil
}

// sync performs one GitLab poll.
func (r *AgentProjectReconciler) sync(ctx context.Context, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) error {
	gl, err := r.gitlabClient(ctx, project, spec)
	if err != nil {
		return err
	}
	if c, ok := gl.(interface{ Stats() gitlab.Stats }); ok {
		// What a poll costs GitLab; retries mean it pushed back.
		defer func() {
			st := c.Stats()
			lvl := 1
			if st.Retries > 0 {
				lvl = 0
			}
			log.FromContext(ctx).V(lvl).Info("GitLab poll done", "requests", st.Requests, "retries", st.Retries)
		}()
	}
	// The username on every poll, for mentions of a renamed bot.
	if project.Status.BotUserID == 0 || *spec.Pilot.Mention {
		u, err := gl.CurrentUser(ctx)
		if err != nil {
			return fmt.Errorf("resolving bot user: %w", err)
		}
		if project.Status.BotUserID == 0 {
			project.Status.BotUserID = u.ID
		}
		project.Status.BotUsername = u.Username
	}

	gate := r.commandGate(gl, project, spec)

	var titleRE *regexp.Regexp
	if spec.Milestones.TitlePattern != "" {
		if titleRE, err = regexp.Compile(spec.Milestones.TitlePattern); err != nil {
			return fmt.Errorf("invalid milestones.titlePattern: %w", err)
		}
	}

	var tasks aiv1.AgentTaskList
	if err := r.List(ctx, &tasks, client.InNamespace(project.Namespace), client.MatchingLabels{LabelProject: project.Name}); err != nil {
		return err
	}
	byMilestone := map[int64]*aiv1.AgentTask{}
	byIssue := map[int64]*aiv1.AgentTask{}
	pilots := map[int64]*aiv1.AgentTask{}
	for i := range tasks.Items {
		t := &tasks.Items[i]
		switch t.Spec.Type {
		case aiv1.TaskTypeSpec:
			if t.Spec.Milestone != nil {
				byMilestone[t.Spec.Milestone.IID] = t
			}
		case aiv1.TaskTypeImplement:
			if t.Spec.Issue != nil {
				byIssue[t.Spec.Issue.IID] = t
			}
		case aiv1.TaskTypeCoordinate:
			if t.Spec.Milestone != nil {
				pilots[t.Spec.Milestone.IID] = t
			}
		}
	}
	keep := map[string]bool{}

	// Milestones -> spec tasks.
	milestones, err := gl.ListMilestones(ctx, spec.GitLab.Project)
	if err != nil {
		return fmt.Errorf("listing milestones: %w", err)
	}
	var active int32
	// Autopilot milestones by IID, with their spec task.
	autopilots := map[int64]*aiv1.AgentTask{}
	var specTasks []*aiv1.AgentTask
	// The owners of a milestone and its issues.
	prefix := spec.Workflow.CommandPrefix
	owners := func(description string) []string {
		return notify.Resolve(description, prefix, spec.Notify.Users, r.DefaultOwners)
	}
	descriptions := map[int64]string{}
	for _, m := range milestones {
		descriptions[m.ID] = m.Description
	}
	for _, m := range milestones {
		// An owners or rule line neither selects a milestone nor is a spec.
		sel := m
		sel.Description = rules.Spec(m.Description, prefix)
		autopilot := spec.Milestones.Autopilot() != "" && milestoneSelected(sel, titleRE, spec.Milestones.Autopilot())
		if !autopilot && !milestoneSelected(sel, titleRE, spec.Milestones.Marker()) {
			continue
		}
		active++
		task, err := r.syncSpecTask(ctx, project, spec, m, byMilestone[m.IID], autopilot, owners(m.Description))
		if err != nil {
			return err
		}
		keep[task.Name] = true
		byMilestone[m.IID] = task
		specTasks = append(specTasks, task)
		if autopilot || task.Status.Autopilot != nil {
			autopilots[m.IID] = task
		}
	}
	project.Status.ActiveMilestones = active
	// One autopilot drives the main-red issue (the main-red lane): the
	// oldest milestone that is not done.
	var lane *aiv1.AgentTask
	for _, st := range autopilots {
		if st.Spec.Autopilot && (st.Status.Autopilot == nil || st.Status.Autopilot.Phase != aiv1.AutopilotDone) && (lane == nil || st.Spec.Milestone.IID < lane.Spec.Milestone.IID) {
			lane = st
		}
	}
	mainRed := int64(0)
	if mh := r.freshMainHealth(spec, project.Status.MainHealth, mainHealthStale); lane != nil && spec.MainHealth.AutoFix && *spec.MainHealth.FreezeAutopilot && mh != nil {
		mainRed = mh.IssueIID
	}

	// Issues with an agent label -> implement tasks.
	issues := map[int64]gitlab.Issue{}
	for _, label := range spec.Workflow.ActiveLabels() {
		list, err := gl.ListIssues(ctx, spec.GitLab.Project, gitlab.ListIssuesOptions{Labels: []string{label}})
		if err != nil {
			return fmt.Errorf("listing issues with label %q: %w", label, err)
		}
		for _, is := range list {
			// The main-red issue handed to the agent has no milestone, nor
			// may a flaky one.
			autoFix := project.Status.MainHealth != nil && project.Status.MainHealth.AutoFixIssueIID == is.IID || flakyLatch(project.Status.MainHealth, is.IID) != nil
			if titleRE != nil && !autoFix && (is.Milestone == nil || !titleRE.MatchString(is.Milestone.Title)) {
				continue
			}
			issues[is.IID] = is
		}
	}
	implTasks := map[int64]*aiv1.AgentTask{}
	for _, is := range issues {
		autopilot, pause := false, ""
		if is.Milestone != nil {
			if st := autopilots[is.Milestone.IID]; st != nil {
				// Only issues of the queue are merged by autopilot; others
				// in the milestone follow the normal flow.
				autopilot = st.Spec.Autopilot && onAutopilot(st, is.IID)
				// The pause reason goes in the same update as a run request,
				// so the run sees it; a pause from this poll is copied next poll.
				if ap := st.Status.Autopilot; autopilot && ap != nil && ap.Phase == aiv1.AutopilotPaused && ap.PausedFor == is.IID {
					pause = ap.PausedReason
				}
			}
		}
		if is.IID == mainRed {
			autopilot = true // merged by the lane
		}
		description := ""
		if is.Milestone != nil {
			// Closed milestones are not listed; the issue carries its own.
			var ok bool
			if description, ok = descriptions[is.Milestone.ID]; !ok {
				description = is.Milestone.Description
			}
		}
		var st *aiv1.AgentTask
		if is.Milestone != nil {
			st = byMilestone[is.Milestone.IID]
		}
		task, mr, err := r.syncImplementTask(ctx, gl, gate, project, spec, is, byIssue[is.IID], st, autopilot, pause, owners(description))
		if err != nil {
			return err
		}
		keep[task.Name] = true
		implTasks[is.IID] = task
		if err := r.syncRunBlock(ctx, gl, spec, is, task); err != nil {
			return err
		}
		if err := r.syncBlocked(ctx, gl, spec, is, task); err != nil {
			return err
		}
		if task.Status.Blocked != nil {
			continue // its CI is followed again once the block is lifted
		}
		// With the milestone's switches (standing rules).
		if err := r.syncCI(ctx, gl, rules.For(spec, description, prefix), project.Status.MainHealth, is, task, mr); err != nil {
			return err
		}
	}

	// The default branch's health, revisits of decisions whose issues are
	// merged, then the autopilot milestones: queue, merge gate, tracking
	// issue. One failing step does not hold up the others or the cleanup
	// below.
	var errs []error
	if err := r.syncMainHealth(ctx, gl, project, spec, lane); err != nil {
		errs = append(errs, fmt.Errorf("main health: %w", err))
	}
	for _, st := range specTasks {
		if err := r.syncRevisits(ctx, gl, gate, project, spec, st); err != nil {
			errs = append(errs, fmt.Errorf("revisits of milestone %q: %w", st.Spec.Milestone.Title, err))
		}
	}
	for _, st := range autopilots {
		if err := r.syncAutopilot(ctx, gl, gate, project, spec, st, implTasks, st == lane); err != nil {
			errs = append(errs, fmt.Errorf("autopilot of milestone %q: %w", st.Spec.Milestone.Title, err))
		}
	}
	if err := r.syncPilots(ctx, gl, project, spec, specTasks, pilots, keep, issues, implTasks); err != nil {
		errs = append(errs, err)
	}

	// Everything else is done, closed, unlabeled or out of scope: delete the
	// task, which garbage collects its pod and workspace.
	for i := range tasks.Items {
		t := &tasks.Items[i]
		if keep[t.Name] || !t.DeletionTimestamp.IsZero() {
			continue
		}
		if t.Spec.Type == aiv1.TaskTypeSpec && t.Status.Autopilot != nil {
			// The milestone is no longer planned (closed, or every marker
			// removed): switch its autopilot off before the state is gone.
			t.Spec.Autopilot = false
			if err := r.syncAutopilot(ctx, gl, gate, project, spec, t, nil, false); err != nil {
				errs = append(errs, fmt.Errorf("switching off the autopilot of task %s: %w", t.Name, err))
				continue // tried again on the next poll
			}
		}
		log.FromContext(ctx).Info("deleting finished task", "task", t.Name)
		if err := r.Delete(ctx, t); client.IgnoreNotFound(err) != nil {
			return err
		}
	}
	project.Status.ActiveTasks = int32(len(keep))
	project.Status.UsageLimits = usageLimits(tasks.Items, r.now())
	return errors.Join(errs...)
}

// onAutopilot reports whether an issue is in the autopilot queue of a
// milestone: planned, or queued later.
func onAutopilot(st *aiv1.AgentTask, iid int64) bool {
	for _, p := range st.Status.Plan {
		if p.IID == iid {
			return true
		}
	}
	if ap := st.Status.Autopilot; ap != nil {
		for _, it := range ap.Items {
			if it.IID == iid {
				return true
			}
		}
	}
	return false
}

// usageLimits collects the engines whose limits currently defer runs. The
// runners read them from the project status, so a limit one task ran into
// makes the other tasks wait instead of hitting it again.
func usageLimits(tasks []aiv1.AgentTask, now time.Time) []aiv1.UsageLimit {
	byEngine := map[aiv1.Engine]aiv1.UsageLimit{}
	for _, t := range tasks {
		run := t.Status.Run
		if run.LimitedEngine == "" || run.DeferredUntil == nil || !run.DeferredUntil.After(now) {
			continue
		}
		if cur, ok := byEngine[run.LimitedEngine]; !ok || run.DeferredUntil.After(cur.Until.Time) {
			byEngine[run.LimitedEngine] = aiv1.UsageLimit{Engine: run.LimitedEngine, Until: *run.DeferredUntil, Message: run.Summary}
		}
	}
	limits := make([]aiv1.UsageLimit, 0, len(byEngine))
	for _, l := range byEngine {
		limits = append(limits, l)
	}
	sort.Slice(limits, func(i, j int) bool { return limits[i].Engine < limits[j].Engine })
	return limits
}

func milestoneSelected(m gitlab.Milestone, titleRE *regexp.Regexp, marker string) bool {
	if m.State != "" && m.State != "active" {
		return false
	}
	if strings.TrimSpace(m.Description) == "" {
		return false
	}
	if titleRE != nil && !titleRE.MatchString(m.Title) {
		return false
	}
	return marker == "" || strings.Contains(m.Description, marker)
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(s)))
	return hex.EncodeToString(sum[:])[:16]
}

func (r *AgentProjectReconciler) newTask(project *aiv1.AgentProject, name string, typ aiv1.TaskType) (*aiv1.AgentTask, error) {
	t := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: project.Namespace,
			Labels: map[string]string{
				LabelProject: project.Name,
				LabelType:    strings.ToLower(string(typ)),
			},
		},
		Spec: aiv1.AgentTaskSpec{ProjectRef: project.Name, Type: typ},
	}
	return t, controllerutil.SetControllerReference(project, t, r.Scheme)
}

// syncSpecTask makes sure a planned milestone has a spec task and requests a
// new run whenever the milestone description changes, and once after a run
// that failed for an outside reason (specRetryDue).
func (r *AgentProjectReconciler) syncSpecTask(ctx context.Context, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, m gitlab.Milestone, task *aiv1.AgentTask, autopilot bool, owners []string) (*aiv1.AgentTask, error) {
	prefix := spec.Workflow.CommandPrefix
	// The owners and rules are no part of the spec: changing them re-plans
	// nothing.
	hash := hashString(rules.Spec(m.Description, prefix))
	ruleLines := rules.Lines(m.Description, prefix)
	ref := &aiv1.MilestoneRef{ID: m.ID, IID: m.IID, Title: m.Title}
	if task == nil {
		var err error
		task, err = r.newTask(project, naming.SpecTask(project.Name, m.IID, m.Title), aiv1.TaskTypeSpec)
		if err != nil {
			return nil, err
		}
		task.Labels[LabelMilestoneIID] = strconv.FormatInt(m.IID, 10)
		task.Spec.Milestone = ref
		task.Spec.RunRequest = 1
		task.Spec.Source.DescriptionHash = hash
		task.Spec.Autopilot = autopilot
		task.Spec.Owners, task.Spec.Rules = owners, ruleLines
		log.FromContext(ctx).Info("planning milestone", "milestone", m.Title, "task", task.Name, "autopilot", autopilot)
		return task, r.Create(ctx, task)
	}
	retry := r.specRetryDue(task, spec.Lifecycle.InfraRetryAfter.Duration)
	if task.Spec.Source.DescriptionHash == hash && *task.Spec.Milestone == *ref && task.Spec.Autopilot == autopilot && slices.Equal(task.Spec.Owners, owners) &&
		slices.Equal(task.Spec.Rules, ruleLines) && !retry {
		return task, nil
	}
	task.Spec.Autopilot = autopilot
	task.Spec.Owners, task.Spec.Rules = owners, ruleLines
	if task.Spec.Source.DescriptionHash != hash {
		log.FromContext(ctx).Info("milestone spec changed, re-planning", "milestone", m.Title, "task", task.Name)
		task.Spec.RunRequest++
		task.Spec.Source.DescriptionHash = hash
	} else if retry {
		log.FromContext(ctx).Info("planning again after a failure for an outside reason", "milestone", m.Title, "task", task.Name)
		task.Spec.Source.InfraRetry = task.Spec.RunRequest + 1
		task.Spec.RunRequest++
	}
	task.Spec.Milestone = ref
	return task, r.Update(ctx, task)
}

// specRetryDue reports whether a planning run that failed for an outside
// reason (run.blockReason infra) is due for its automatic retry: once per run
// request, lifecycle.infraRetryAfter after it ended, like an implement run's
// (syncRunBlock).
func (r *AgentProjectReconciler) specRetryDue(task *aiv1.AgentTask, after time.Duration) bool {
	run := task.Status.Run
	return !task.RunPending() && run.LastResult == aiv1.RunResultFailed && run.BlockReason == aiv1.BlockInfra &&
		task.Spec.Source.InfraRetry != run.ObservedRunRequest && run.LastActivityTime != nil && !r.now().Before(run.LastActivityTime.Add(after))
}

// syncImplementTask makes sure an issue handed to the agent has a task and
// turns "moved to ready" and "/agent" comments into run requests. It returns
// the task's merge request too, if it has one. st is the spec task of the
// issue's milestone, if there is one.
func (r *AgentProjectReconciler) syncImplementTask(ctx context.Context, gl gitlab.API, gate *commandGate, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	is gitlab.Issue, task, st *aiv1.AgentTask, autopilot bool, pause string, owners []string) (*aiv1.AgentTask, *gitlab.MergeRequest, error) {
	wf := spec.Workflow
	glProject := spec.GitLab.Project
	created := false
	if task == nil {
		var err error
		task, err = r.newTask(project, naming.ImplementTask(project.Name, is.IID, is.Title), aiv1.TaskTypeImplement)
		if err != nil {
			return nil, nil, err
		}
		task.Labels[LabelIssueIID] = strconv.FormatInt(is.IID, 10)
		created = true
	}
	before := task.Spec.DeepCopy()
	task.Spec.Issue = &aiv1.IssueRef{ID: is.ID, IID: is.IID, Title: is.Title, WebURL: is.WebURL}
	if is.Milestone != nil {
		task.Spec.Milestone = &aiv1.MilestoneRef{ID: is.Milestone.ID, IID: is.Milestone.IID, Title: is.Milestone.Title}
	}
	task.Spec.Autopilot = autopilot
	task.Spec.Owners = owners
	task.Spec.Source.AutopilotPause = pause

	ready := is.HasLabel(wf.ReadyLabel)
	src := &task.Spec.Source

	// Only look at comments when something changed since the last poll.
	issueChanged := is.UpdatedAt == nil || src.IssueUpdatedAt == nil || is.UpdatedAt.After(src.IssueUpdatedAt.Time)
	var mr *gitlab.MergeRequest
	if iid := task.Status.Run.MergeRequestIID; iid != 0 {
		var err error
		if mr, err = gl.GetMergeRequest(ctx, glProject, iid); err != nil && !gitlab.IsNotFound(err) {
			return nil, nil, fmt.Errorf("getting merge request !%d: %w", iid, err)
		}
	}
	mrChanged := mr != nil && (mr.UpdatedAt == nil || src.MergeRequestUpdatedAt == nil || mr.UpdatedAt.After(src.MergeRequestUpdatedAt.Time))

	command, review := false, false
	var reply *routingReply
	var denied []pilotNote
	if ready || issueChanged || mrChanged {
		var latest int64
		var issueNotes, mrNotes []gitlab.Note
		var err error
		latest, issueNotes, mrNotes, denied, err = commandNotes(ctx, gl, gate, glProject, is.IID, mr, owners)
		if err != nil {
			return nil, nil, err
		}
		// Commands that do not count request, route and review nothing.
		// Those written since the notes were last read are answered once
		// the task is saved.
		cmdIssue, cmdMR := withoutNotes(issueNotes, denied), withoutNotes(mrNotes, denied)
		denied = slices.DeleteFunc(denied, func(n pilotNote) bool { return src.LastPilotNoteID == 0 || n.ID <= src.LastPilotNoteID })
		// "/agent use" and "/agent effort" become labels at once, also the
		// ones written before the task existed; the answer follows once the
		// task is saved.
		if reply, err = r.applyRouting(ctx, gl, spec, &is, cmdIssue, cmdMR, src, project.Status.BotUserID); err != nil {
			return nil, nil, err
		}
		r.pilotIssueNotes(ctx, gl, project, spec, is, mr, task, issueNotes, mrNotes)
		if created {
			// Comments written before the task existed are context, not commands.
			src.LastCommandNoteID = latest
		} else if latest > src.LastCommandNoteID {
			review = reviewOnly(slices.Concat(cmdIssue, cmdMR), src.LastCommandNoteID, project.Status.BotUserID, wf.CommandPrefix)
			src.LastCommandNoteID = latest
			command = true
		}
	}
	if is.UpdatedAt != nil {
		src.IssueUpdatedAt = &metav1.Time{Time: *is.UpdatedAt}
	}
	if mr != nil && mr.UpdatedAt != nil {
		src.MergeRequestUpdatedAt = &metav1.Time{Time: *mr.UpdatedAt}
	}

	// The merge request of an issue in review conflicts with the default
	// branch: the next run merges it in and the agent resolves the
	// conflicts, once per head.
	conflict := !created && *wf.ResolveConflicts && mr != nil && mr.State == "opened" && hasConflicts(mr) && mr.SHA != "" &&
		is.HasLabel(wf.ReviewLabel) && !task.RunPending() && src.MergeBaseFor != mr.SHA
	if conflict {
		log.FromContext(ctx).Info("merge request conflicts with the default branch, asking the agent to merge it", "issue", is.IID, "mr", mr.IID, "sha", mr.SHA)
		src.MergeBaseFor = mr.SHA
	}

	// "/agent review": the next run reviews the agent's pushed head again.
	// A head someone else pushed is taken over by a normal run instead.
	if review && mr != nil && mr.SHA != "" && mr.SHA == task.Status.Run.PushedSHA {
		if err := r.askReview(ctx, task, mr.SHA); err != nil {
			return nil, nil, err
		}
	}

	requested := ready || command || conflict
	if requested {
		task.Spec.RunRequest++
	}

	switch {
	case created:
		log.FromContext(ctx).Info("issue handed to agent", "issue", is.IID, "task", task.Name)
		if err := r.Create(ctx, task); err != nil {
			return nil, nil, err
		}
	case !equalSpec(before, &task.Spec):
		if err := r.Update(ctx, task); err != nil {
			return nil, nil, err
		}
	}

	if reply != nil {
		// A failed answer is not tried again: the labels are set.
		var err error
		if reply.onMR && mr != nil {
			_, err = gl.CreateMergeRequestNote(ctx, glProject, mr.IID, reply.body)
		} else {
			_, err = gl.CreateIssueNote(ctx, glProject, is.IID, reply.body)
		}
		if err != nil {
			log.FromContext(ctx).Info("could not answer the model and effort commands", "issue", is.IID, "error", err.Error())
		}
	}
	// Recorded on the milestone's spec task, shared with the pilot's
	// answers; outside a planned milestone on the task itself.
	if st == nil {
		st = task
	}
	for _, n := range denied {
		if err := gate.deny(ctx, st, n, owners); err != nil {
			return nil, nil, err
		}
	}

	// Acknowledge in GitLab only after the run request is persisted. If this
	// fails the next poll sees the ready label again and requests one more
	// run, which the runner simply coalesces.
	if requested {
		opts := gitlab.UpdateIssueOptions{
			AddLabels:    []string{wf.WorkingLabel},
			RemoveLabels: []string{wf.ReadyLabel, wf.NeedsInputLabel, wf.ReviewLabel, wf.BlockedLabel},
		}
		// Assigning the bot makes GitLab's milestone page list the issue as
		// ongoing. assignee_ids replaces all assignees, so an issue that is
		// already assigned (to a human or to the bot) is left alone.
		if *wf.AssignBot && len(is.Assignees) == 0 && project.Status.BotUserID != 0 {
			opts.AssigneeIDs = []int64{project.Status.BotUserID}
		}
		if _, err := gl.UpdateIssue(ctx, glProject, is.IID, opts); err != nil {
			return nil, nil, fmt.Errorf("moving issue #%d to %q: %w", is.IID, wf.WorkingLabel, err)
		}
	}
	return task, mr, nil
}

func equalSpec(a, b *aiv1.AgentTaskSpec) bool {
	return equality.Semantic.DeepEqual(a, b)
}

// commandNotes returns the ID of the newest command on the issue or its
// merge request that asks for a run (a comment that only changes the model
// or effort does not), the notes, and the commands that do not count
// (workflow.commandsFrom).
func commandNotes(ctx context.Context, gl gitlab.API, gate *commandGate, glProject string, issueIID int64, mr *gitlab.MergeRequest,
	owners []string) (int64, []gitlab.Note, []gitlab.Note, []pilotNote, error) {
	notes, err := gl.ListIssueNotes(ctx, glProject, issueIID)
	if err != nil {
		return 0, nil, nil, nil, fmt.Errorf("listing notes of issue #%d: %w", issueIID, err)
	}
	var mrNotes []gitlab.Note
	if mr != nil {
		if mrNotes, err = gl.ListMergeRequestNotes(ctx, glProject, mr.IID); err != nil {
			return 0, nil, nil, nil, fmt.Errorf("listing notes of merge request !%d: %w", mr.IID, err)
		}
	}
	prefix := gate.spec.Workflow.CommandPrefix
	var latest int64
	var denied []pilotNote
	for i, list := range [][]gitlab.Note{notes, mrNotes} {
		kept, others, err := gate.split(ctx, list, owners)
		if err != nil {
			return 0, nil, nil, nil, err
		}
		for _, n := range kept {
			if IsCommand(n, gate.botID, prefix) && !command.IsRoutingOnly(n.Body, prefix) && n.ID > latest {
				latest = n.ID
			}
		}
		for _, n := range others {
			if i == 0 {
				denied = append(denied, pilotNote{Note: n, issue: issueIID})
			} else {
				denied = append(denied, pilotNote{Note: n, mr: mr.IID})
			}
		}
	}
	return latest, notes, mrNotes, denied, nil
}

// withoutNotes returns notes without those in drop.
func withoutNotes(notes []gitlab.Note, drop []pilotNote) []gitlab.Note {
	return slices.DeleteFunc(slices.Clone(notes), func(n gitlab.Note) bool {
		return slices.ContainsFunc(drop, func(d pilotNote) bool { return d.ID == n.ID })
	})
}

// reviewOnly reports whether all commands newer than since are "/agent
// review".
func reviewOnly(notes []gitlab.Note, since, botID int64, prefix string) bool {
	for _, n := range notes {
		if n.ID > since && IsCommand(n, botID, prefix) && !command.IsRoutingOnly(n.Body, prefix) && !command.IsReview(n.Body, prefix) {
			return false
		}
	}
	return true
}

// routingReply is the operator's answer to model and effort commands, and
// where it goes.
type routingReply struct {
	body string
	onMR bool
}

// applyRouting turns the "/agent use" and "/agent effort" comments of
// humans that are newer than the last applied one into the issue's model
// and effort labels. It returns the answer, to be posted once the task is
// saved, where the last command was written.
func (r *AgentProjectReconciler) applyRouting(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, is *gitlab.Issue,
	issueNotes, mrNotes []gitlab.Note, src *aiv1.SourceState, botID int64) (*routingReply, error) {
	prefix := spec.Workflow.CommandPrefix
	since := src.LastRoutingNoteID
	if since == 0 {
		// A new task (also one made again for the same issue): commands the
		// operator answered before are not applied again.
		for _, n := range append(slices.Clone(issueNotes), mrNotes...) {
			if botID != 0 && n.Author.ID == botID && strings.Contains(n.Body, marker.RoutingReply) {
				since = max(since, n.ID)
			}
		}
	}
	type note struct {
		gitlab.Note
		onMR bool
	}
	var found []note
	for i, list := range [][]gitlab.Note{issueNotes, mrNotes} {
		for _, n := range list {
			if n.ID <= since || !IsCommand(n, botID, prefix) {
				continue
			}
			if _, ok := command.ParseRouting(n.Body, prefix); ok {
				found = append(found, note{n, i == 1})
			}
		}
	}
	if len(found) == 0 {
		src.LastRoutingNoteID = since
		return nil, nil
	}
	slices.SortFunc(found, func(a, b note) int { return cmp.Compare(a.ID, b.ID) })
	cmds := make([]command.Routing, len(found))
	for i, n := range found {
		cmds[i], _ = command.ParseRouting(n.Body, prefix)
	}
	ch := command.Apply(spec, is.Labels, cmds)
	if len(ch.Add)+len(ch.Remove) > 0 {
		if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, is.IID, gitlab.UpdateIssueOptions{AddLabels: ch.Add, RemoveLabels: ch.Remove}); err != nil {
			return nil, fmt.Errorf("setting the model and effort labels of issue #%d: %w", is.IID, err)
		}
		is.Labels = append(slices.DeleteFunc(is.Labels, func(l string) bool { return slices.Contains(ch.Remove, l) }), ch.Add...)
	}
	log.FromContext(ctx).Info("model and effort commands applied", "issue", is.IID, "add", ch.Add, "remove", ch.Remove)
	last := found[len(found)-1]
	src.LastRoutingNoteID = last.ID
	return &routingReply{body: ch.Reply(), onMR: last.onMR}, nil
}

// IsCommand reports whether a note is a human "/agent ..." command.
func IsCommand(n gitlab.Note, botID int64, prefix string) bool {
	if n.System || n.Author.ID == botID || prefix == "" {
		return false
	}
	body := strings.TrimSpace(n.Body)
	if !strings.HasPrefix(body, prefix) {
		return false
	}
	// "/agent" must be a word on its own: "/agents are cool" is not a command.
	rest := body[len(prefix):]
	return rest == "" || rest[0] == ' ' || rest[0] == '\n' || rest[0] == '\t' || rest[0] == ':'
}

func truncateMsg(s string) string {
	if len(s) > 1024 {
		return s[:1024]
	}
	return s
}

// SetupWithManager registers the controller.
func (r *AgentProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1.AgentProject{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("agentproject").
		Complete(r)
}

// hasConflicts reports whether GitLab sees conflicts between the MR and its
// target branch.
func hasConflicts(mr *gitlab.MergeRequest) bool {
	return mr.HasConflicts || mr.DetailedMergeStatus == "conflict"
}

// requestMergeBase asks the next run of task to merge the default branch into
// the work branch and resolve the conflicts (for MR head sha), and moves the
// issue to working.
func (r *AgentProjectReconciler) requestMergeBase(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, task *aiv1.AgentTask, iid int64, sha string) error {
	task.Spec.Source.MergeBaseFor = sha
	return r.requestRun(ctx, gl, spec, task, iid)
}

// requestRun requests the next run of task and moves the issue to working.
func (r *AgentProjectReconciler) requestRun(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, task *aiv1.AgentTask, iid int64) error {
	task.Spec.RunRequest++
	if err := r.Update(ctx, task); err != nil {
		return err
	}
	wf := spec.Workflow
	if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, iid, gitlab.UpdateIssueOptions{
		AddLabels: []string{wf.WorkingLabel}, RemoveLabels: []string{wf.ReviewLabel, wf.ReadyLabel, wf.NeedsInputLabel, wf.BlockedLabel},
	}); err != nil {
		return fmt.Errorf("moving issue #%d to %q: %w", iid, wf.WorkingLabel, err)
	}
	return nil
}
