package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
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
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
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

	syncErr := r.sync(ctx, &project, spec)
	if syncErr != nil {
		logger.Error(syncErr, "GitLab sync failed")
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
	if project.Status.BotUserID == 0 {
		u, err := gl.CurrentUser(ctx)
		if err != nil {
			return fmt.Errorf("resolving bot user: %w", err)
		}
		project.Status.BotUserID = u.ID
	}

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
	for _, m := range milestones {
		autopilot := spec.Milestones.Autopilot() != "" && milestoneSelected(m, titleRE, spec.Milestones.Autopilot())
		if !autopilot && !milestoneSelected(m, titleRE, spec.Milestones.Marker()) {
			continue
		}
		active++
		task, err := r.syncSpecTask(ctx, project, m, byMilestone[m.IID], autopilot)
		if err != nil {
			return err
		}
		keep[task.Name] = true
		specTasks = append(specTasks, task)
		if autopilot || task.Status.Autopilot != nil {
			autopilots[m.IID] = task
		}
	}
	project.Status.ActiveMilestones = active

	// Issues with an agent label -> implement tasks.
	issues := map[int64]gitlab.Issue{}
	for _, label := range spec.Workflow.ActiveLabels() {
		list, err := gl.ListIssues(ctx, spec.GitLab.Project, gitlab.ListIssuesOptions{Labels: []string{label}})
		if err != nil {
			return fmt.Errorf("listing issues with label %q: %w", label, err)
		}
		for _, is := range list {
			if titleRE != nil && (is.Milestone == nil || !titleRE.MatchString(is.Milestone.Title)) {
				continue
			}
			issues[is.IID] = is
		}
	}
	implTasks := map[int64]*aiv1.AgentTask{}
	for _, is := range issues {
		autopilot := false
		if is.Milestone != nil {
			if st := autopilots[is.Milestone.IID]; st != nil {
				// Only issues of the queue are merged by autopilot; others
				// in the milestone follow the normal flow.
				autopilot = st.Spec.Autopilot && onAutopilot(st, is.IID)
			}
		}
		task, err := r.syncImplementTask(ctx, gl, project, spec, is, byIssue[is.IID], autopilot)
		if err != nil {
			return err
		}
		keep[task.Name] = true
		implTasks[is.IID] = task
		if err := r.syncCI(ctx, gl, spec, is, task); err != nil {
			return err
		}
	}

	// Revisits of decisions whose issues are merged, then the autopilot
	// milestones: queue, merge gate, tracking issue. One failing milestone
	// does not hold up the others or the cleanup below.
	var errs []error
	for _, st := range specTasks {
		if err := r.syncRevisits(ctx, gl, project, spec, st); err != nil {
			errs = append(errs, fmt.Errorf("revisits of milestone %q: %w", st.Spec.Milestone.Title, err))
		}
	}
	for _, st := range autopilots {
		if err := r.syncAutopilot(ctx, gl, project, spec, st, implTasks); err != nil {
			errs = append(errs, fmt.Errorf("autopilot of milestone %q: %w", st.Spec.Milestone.Title, err))
		}
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
			if err := r.syncAutopilot(ctx, gl, project, spec, t, nil); err != nil {
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
// new run whenever the milestone description changes.
func (r *AgentProjectReconciler) syncSpecTask(ctx context.Context, project *aiv1.AgentProject, m gitlab.Milestone, task *aiv1.AgentTask, autopilot bool) (*aiv1.AgentTask, error) {
	hash := hashString(m.Description)
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
		log.FromContext(ctx).Info("planning milestone", "milestone", m.Title, "task", task.Name, "autopilot", autopilot)
		return task, r.Create(ctx, task)
	}
	if task.Spec.Source.DescriptionHash == hash && *task.Spec.Milestone == *ref && task.Spec.Autopilot == autopilot {
		return task, nil
	}
	task.Spec.Autopilot = autopilot
	if task.Spec.Source.DescriptionHash != hash {
		log.FromContext(ctx).Info("milestone spec changed, re-planning", "milestone", m.Title, "task", task.Name)
		task.Spec.RunRequest++
		task.Spec.Source.DescriptionHash = hash
	}
	task.Spec.Milestone = ref
	return task, r.Update(ctx, task)
}

// syncImplementTask makes sure an issue handed to the agent has a task and
// turns "moved to ready" and "/agent" comments into run requests.
func (r *AgentProjectReconciler) syncImplementTask(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, is gitlab.Issue, task *aiv1.AgentTask, autopilot bool) (*aiv1.AgentTask, error) {
	wf := spec.Workflow
	glProject := spec.GitLab.Project
	created := false
	if task == nil {
		var err error
		task, err = r.newTask(project, naming.ImplementTask(project.Name, is.IID, is.Title), aiv1.TaskTypeImplement)
		if err != nil {
			return nil, err
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

	ready := is.HasLabel(wf.ReadyLabel)
	src := &task.Spec.Source

	// Only look at comments when something changed since the last poll.
	issueChanged := is.UpdatedAt == nil || src.IssueUpdatedAt == nil || is.UpdatedAt.After(src.IssueUpdatedAt.Time)
	var mr *gitlab.MergeRequest
	if iid := task.Status.Run.MergeRequestIID; iid != 0 {
		var err error
		if mr, err = gl.GetMergeRequest(ctx, glProject, iid); err != nil && !gitlab.IsNotFound(err) {
			return nil, fmt.Errorf("getting merge request !%d: %w", iid, err)
		}
	}
	mrChanged := mr != nil && (mr.UpdatedAt == nil || src.MergeRequestUpdatedAt == nil || mr.UpdatedAt.After(src.MergeRequestUpdatedAt.Time))

	command := false
	if ready || issueChanged || mrChanged {
		latest, err := latestCommandNote(ctx, gl, glProject, is.IID, mr, project.Status.BotUserID, wf.CommandPrefix)
		if err != nil {
			return nil, err
		}
		if created {
			// Comments written before the task existed are context, not commands.
			src.LastCommandNoteID = latest
		} else if latest > src.LastCommandNoteID {
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

	requested := ready || command || conflict
	if requested {
		task.Spec.RunRequest++
	}

	switch {
	case created:
		log.FromContext(ctx).Info("issue handed to agent", "issue", is.IID, "task", task.Name)
		if err := r.Create(ctx, task); err != nil {
			return nil, err
		}
	case !equalSpec(before, &task.Spec):
		if err := r.Update(ctx, task); err != nil {
			return nil, err
		}
	}

	// Acknowledge in GitLab only after the run request is persisted. If this
	// fails the next poll sees the ready label again and requests one more
	// run, which the runner simply coalesces.
	if requested {
		opts := gitlab.UpdateIssueOptions{
			AddLabels:    []string{wf.WorkingLabel},
			RemoveLabels: []string{wf.ReadyLabel, wf.NeedsInputLabel, wf.ReviewLabel},
		}
		// Assigning the bot makes GitLab's milestone page list the issue as
		// ongoing. assignee_ids replaces all assignees, so an issue that is
		// already assigned (to a human or to the bot) is left alone.
		if *wf.AssignBot && len(is.Assignees) == 0 && project.Status.BotUserID != 0 {
			opts.AssigneeIDs = []int64{project.Status.BotUserID}
		}
		if _, err := gl.UpdateIssue(ctx, glProject, is.IID, opts); err != nil {
			return nil, fmt.Errorf("moving issue #%d to %q: %w", is.IID, wf.WorkingLabel, err)
		}
	}
	return task, nil
}

func equalSpec(a, b *aiv1.AgentTaskSpec) bool {
	return equality.Semantic.DeepEqual(a, b)
}

// latestCommandNote returns the ID of the newest human comment on the issue or
// its merge request that starts with the command prefix.
func latestCommandNote(ctx context.Context, gl gitlab.API, glProject string, issueIID int64, mr *gitlab.MergeRequest, botID int64, prefix string) (int64, error) {
	notes, err := gl.ListIssueNotes(ctx, glProject, issueIID)
	if err != nil {
		return 0, fmt.Errorf("listing notes of issue #%d: %w", issueIID, err)
	}
	if mr != nil {
		mrNotes, err := gl.ListMergeRequestNotes(ctx, glProject, mr.IID)
		if err != nil {
			return 0, fmt.Errorf("listing notes of merge request !%d: %w", mr.IID, err)
		}
		notes = append(notes, mrNotes...)
	}
	var latest int64
	for _, n := range notes {
		if IsCommand(n, botID, prefix) && n.ID > latest {
			latest = n.ID
		}
	}
	return latest, nil
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
	task.Spec.RunRequest++
	if err := r.Update(ctx, task); err != nil {
		return err
	}
	wf := spec.Workflow
	if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, iid, gitlab.UpdateIssueOptions{
		AddLabels: []string{wf.WorkingLabel}, RemoveLabels: []string{wf.ReviewLabel, wf.ReadyLabel, wf.NeedsInputLabel},
	}); err != nil {
		return fmt.Errorf("moving issue #%d to %q: %w", iid, wf.WorkingLabel, err)
	}
	return nil
}
