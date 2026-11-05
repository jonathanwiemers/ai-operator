package controller

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
	"github.com/jonathanwiemers/ai-operator/internal/command"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/history"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
	"github.com/jonathanwiemers/ai-operator/internal/webhook"
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
	// DeskURL is the Desk's base URL (the UI's externalURL) for links in
	// comments. Empty: comments name the Desk without a link.
	DeskURL string
	// Wakes, when set, syncs the AgentProject of every event at once (the
	// webhook receiver's wakes).
	Wakes <-chan event.GenericEvent
	// Deliveries, when set, tells when the receiver last accepted a delivery
	// for a project; while they arrive, the project is polled lightly.
	Deliveries *webhook.Deliveries
	// History, set with a research database, records the comments the
	// project's GitLab client lists.
	History *history.Recorder
	// Research, set with a research database, adds the usage readings and
	// limits of the project's AgentResearch runs to its status.
	Research bool
	// Spend, set with a research database, is the milestone spend the
	// pilot's retrospective reports. Nil: it sums the live tasks.
	Spend SpendSource

	mu sync.Mutex
	// synced is the last delivery each project's last full sync covered.
	synced map[types.NamespacedName]time.Time
}

// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentprojects,verbs=get;list;watch
// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentprojects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agenttasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentresearches,verbs=get;list;watch
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

	var prevDelivery *metav1.Time
	if st := project.Status.Webhook; st != nil {
		prevDelivery = st.LastDeliveryTime
	}
	started := r.now()
	lastDelivery, receiving := r.webhookDeliveries(&project, spec, started)
	// While deliveries arrive, they tell of every change but the milestones'.
	// Steps that only time advances (infra retries, auto-resume, the main
	// health interval, the hook recheck) run on full syncs, so at most
	// webhook.fallbackInterval late then.
	full := !receiving || r.fullSyncDue(&project, spec, lastDelivery, started)

	// A slow or rate-limiting GitLab must not hold the worker for long: the
	// client's retries stop at this deadline, and the next poll goes on.
	sctx, cancel := context.WithTimeout(ctx, max(spec.PollInterval.Duration, syncTimeout))
	var syncErr error
	if !full {
		full, syncErr = r.milestonesChanged(sctx, &project, spec)
		if syncErr == nil && !full {
			logger.V(1).Info("Light poll: no milestone changed, full sync skipped")
		}
	}
	if full {
		syncErr = r.sync(sctx, &project, spec)
	}
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
			Type: "Synced", Status: metav1.ConditionFalse, Reason: "SyncFailed", Message: textutil.Truncate(syncErr.Error(), 1024),
		})
	} else if full {
		r.covered(req.NamespacedName, lastDelivery)
		now := metav1.NewTime(r.now())
		project.Status.LastSyncTime = &now
		meta.SetStatusCondition(&project.Status.Conditions, metav1.Condition{
			Type: "Synced", Status: metav1.ConditionTrue, Reason: "Synced", Message: "GitLab state is in sync",
			ObservedGeneration: project.Generation,
		})
	}
	// After the sync, which may replace status.webhook.
	reportDeliveries(&project, spec, prevDelivery, lastDelivery, receiving)
	reportFallbacks(&project, spec)
	if err := r.Status().Patch(ctx, &project, client.MergeFrom(orig)); err != nil {
		return ctrl.Result{}, err
	}
	// Errors are reported in the status; retry on the regular poll interval
	// instead of hammering GitLab with exponential backoff.
	return ctrl.Result{RequeueAfter: spec.PollInterval.Duration}, nil
}

// fullSyncDue reports whether a project that receives deliveries needs a full
// sync now: a delivery came that the last one did not cover, it is
// webhook.fallbackInterval old, it failed, or the spec changed since.
func (r *AgentProjectReconciler) fullSyncDue(project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, lastDelivery, now time.Time) bool {
	last := project.Status.LastSyncTime
	synced := meta.FindStatusCondition(project.Status.Conditions, "Synced")
	r.mu.Lock()
	seen, ok := r.synced[types.NamespacedName{Namespace: project.Namespace, Name: project.Name}]
	r.mu.Unlock()
	return last == nil || synced == nil || synced.Status != metav1.ConditionTrue || synced.ObservedGeneration != project.Generation ||
		!ok || lastDelivery.After(seen) || now.Sub(last.Time) >= spec.Webhook.FallbackInterval.Duration
}

// covered notes that a full sync of n, started when delivery was the last
// one, succeeded. It is kept in memory, like the deliveries (the status
// keeps whole seconds only), so a delivery during the sync asks for the next.
func (r *AgentProjectReconciler) covered(n types.NamespacedName, delivery time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.synced == nil {
		r.synced = map[types.NamespacedName]time.Time{}
	}
	r.synced[n] = delivery
}

// milestonesChanged is the light poll between full syncs while deliveries
// arrive (milestone changes cause none): it lists the milestones and reports
// whether a planned one appeared or vanished or its spec task would change.
func (r *AgentProjectReconciler) milestonesChanged(ctx context.Context, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) (bool, error) {
	titleRE, err := titlePattern(spec)
	if err != nil {
		return true, nil // the full sync reports it
	}
	var tasks aiv1.AgentTaskList
	if err := r.List(ctx, &tasks, client.InNamespace(project.Namespace), client.MatchingLabels{LabelProject: project.Name}); err != nil {
		return false, err
	}
	planned := map[int64]*aiv1.AgentTask{}
	for i := range tasks.Items {
		if t := &tasks.Items[i]; t.Spec.Type == aiv1.TaskTypeSpec && t.Spec.Milestone != nil {
			planned[t.Spec.Milestone.IID] = t
		}
	}
	gl, err := r.gitlabClient(ctx, project, spec)
	if err != nil {
		return false, err
	}
	milestones, err := gl.ListMilestones(ctx, spec.GitLab.Project)
	if err != nil {
		return false, fmt.Errorf("listing milestones: %w", err)
	}
	selected := map[int64]bool{}
	for _, m := range milestones {
		ok, autopilot := milestoneSelection(spec, titleRE, m)
		if !ok {
			continue
		}
		task := planned[m.IID]
		owners := notify.Resolve(m.Description, spec.Workflow.CommandPrefix, spec.Notify.Users, r.DefaultOwners)
		subjects := notify.MilestoneOwnerSubjects(m.Description, spec.Workflow.CommandPrefix)
		if task == nil || !specTaskCurrent(spec, m, task, autopilot, owners, subjects) || r.specRetryDue(task, spec.Lifecycle.InfraRetryAfter.Duration) {
			return true, nil
		}
		selected[m.IID] = true
	}
	for iid, t := range planned {
		if !selected[iid] && t.DeletionTimestamp.IsZero() {
			return true, nil
		}
	}
	return false, nil
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
	base := strings.TrimRight(spec.GitLab.URL, "/")
	if base == "" {
		base = "https://gitlab.com"
	}
	return r.History.WrapGitLab(r.NewGitLab(spec.GitLab.URL, token), history.Project{
		Namespace: project.Namespace, Name: project.Name,
		WebURL: base + "/" + spec.GitLab.Project,
		Bot:    project.Status.BotUsername,
	}), nil
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
	r.syncWebhook(ctx, gl, project, spec)

	gate := r.commandGate(gl, project, spec)

	titleRE, err := titlePattern(spec)
	if err != nil {
		return err
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
	ownerSubjects := func(description string) []string {
		return notify.MilestoneOwnerSubjects(description, prefix)
	}
	descriptions := map[int64]string{}
	for _, m := range milestones {
		descriptions[m.ID] = m.Description
	}
	for _, m := range milestones {
		selected, autopilot := milestoneSelection(spec, titleRE, m)
		if !selected {
			continue
		}
		active++
		task, err := r.syncSpecTask(ctx, project, spec, m, byMilestone[m.IID], autopilot, owners(m.Description), ownerSubjects(m.Description))
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
	// One failing issue does not hold up the other issues or the steps
	// below: its error is collected and returned at the end.
	var errs []error
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
		task, mr, err := r.syncImplementTask(ctx, gl, gate, project, spec, is, byIssue[is.IID], st, autopilot, pause, owners(description), ownerSubjects(description))
		if err != nil {
			errs = append(errs, fmt.Errorf("issue #%d: %w", is.IID, err))
			// Keep the existing task, so the cleanup below does not delete
			// it and the autopilot still sees it.
			if t := byIssue[is.IID]; t != nil {
				keep[t.Name] = true
				implTasks[is.IID] = t
			}
			continue
		}
		keep[task.Name] = true
		implTasks[is.IID] = task
		if err := r.syncRunBlock(ctx, gl, spec, is, task); err != nil {
			errs = append(errs, fmt.Errorf("issue #%d: %w", is.IID, err))
			continue
		}
		if err := r.syncBlocked(ctx, gl, spec, is, task); err != nil {
			errs = append(errs, fmt.Errorf("issue #%d: %w", is.IID, err))
			continue
		}
		if task.Status.Blocked != nil {
			continue // its CI is followed again once the block is lifted
		}
		// With the milestone's switches (standing rules).
		if err := r.syncCI(ctx, gl, rules.For(spec, description, prefix), project.Status.MainHealth, is, task, mr); err != nil {
			errs = append(errs, fmt.Errorf("issue #%d: %w", is.IID, err))
		}
	}

	// The default branch's health, revisits of decisions whose issues are
	// merged, then the autopilot milestones: queue, merge gate, tracking
	// issue. One failing step does not hold up the others or the cleanup
	// below.
	if err := r.syncMainHealth(ctx, gl, project, spec, lane); err != nil {
		errs = append(errs, fmt.Errorf("main health: %w", err))
	}
	for _, st := range specTasks {
		if err := r.syncRevisits(ctx, gl, gate, project, spec, st); err != nil {
			errs = append(errs, fmt.Errorf("revisits of milestone %q: %w", st.Spec.Milestone.Title, err))
		}
	}
	for _, st := range autopilots {
		if err := r.syncAutopilot(ctx, gl, gate, project, spec, st, implTasks, pilots[st.Spec.Milestone.IID], st == lane); err != nil {
			errs = append(errs, fmt.Errorf("autopilot of milestone %q: %w", st.Spec.Milestone.Title, err))
		}
	}
	// Prompts whose source resolved close, on the state the steps above
	// left, before the pilots see them.
	if err := r.syncOpenPrompts(ctx, gl, project, spec, specTasks, byMilestone, issues, implTasks); err != nil {
		errs = append(errs, err)
	}
	if err := r.syncPilots(ctx, gl, project, spec, specTasks, lane, pilots, keep, issues, implTasks); err != nil {
		errs = append(errs, err)
	}
	// The prompts' GitLab comments, once answers have their outcome.
	for _, st := range specTasks {
		tracking := trackingIssue(st, pilots[st.Spec.Milestone.IID])
		if ap := st.Status.Autopilot; tracking == 0 && ap != nil {
			tracking = ap.TrackingIssueIID
		}
		if err := r.mirrorPrompts(ctx, gl, project, spec, st, tracking); err != nil {
			errs = append(errs, err)
		}
	}
	for _, t := range implTasks {
		if err := r.mirrorPrompts(ctx, gl, project, spec, t, 0); err != nil {
			errs = append(errs, err)
		}
	}

	// Everything else is done, closed, unlabeled or out of scope: delete the
	// task, which garbage collects its pod and workspace. Tasks of issues
	// that failed above are in keep too, so only tasks whose issue is gone
	// are deleted, never one because it or a sibling failed.
	for i := range tasks.Items {
		t := &tasks.Items[i]
		if keep[t.Name] || !t.DeletionTimestamp.IsZero() {
			continue
		}
		if t.Spec.Type == aiv1.TaskTypeSpec && t.Status.Autopilot != nil {
			// The milestone is no longer planned (closed, or every marker
			// removed): switch its autopilot off before the state is gone.
			t.Spec.Autopilot = false
			if err := r.syncAutopilot(ctx, gl, gate, project, spec, t, nil, nil, false); err != nil {
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
	project.Status.EngineUsage = engineUsage(project.Status.EngineUsage, tasks.Items)
	if r.Research {
		var researches aiv1.AgentResearchList
		if err := r.List(ctx, &researches, client.InNamespace(project.Namespace)); err != nil {
			errs = append(errs, fmt.Errorf("listing the researches: %w", err))
		} else {
			addResearchUsage(&project.Status, project.Name, researches.Items, r.now())
		}
	}
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
	// Limits runs went around with a fallback hold for the others too.
	for _, t := range tasks {
		for _, l := range t.Status.Run.UsageLimits {
			if cur, ok := byEngine[l.Engine]; l.Until.After(now) && (!ok || l.Until.After(cur.Until.Time)) {
				byEngine[l.Engine] = l
			}
		}
	}
	limits := make([]aiv1.UsageLimit, 0, len(byEngine))
	for _, l := range byEngine {
		limits = append(limits, l)
	}
	sort.Slice(limits, func(i, j int) bool { return limits[i].Engine < limits[j].Engine })
	return limits
}

// reportFallbacks sets FallbacksValid: a broken fallback shows before a
// usage limit needs it.
func reportFallbacks(project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) {
	cond := metav1.Condition{Type: aiv1.ConditionFallbacksValid, Status: metav1.ConditionTrue, Reason: "Valid",
		Message: "no fallback is broken", ObservedGeneration: project.Generation}
	if errs := spec.FallbackErrors(); len(errs) > 0 {
		cond.Status, cond.Reason, cond.Message = metav1.ConditionFalse, "InvalidFallback", textutil.Truncate(strings.Join(errs, "; "), 1024)
	}
	meta.SetStatusCondition(&project.Status.Conditions, cond)
}

// engineUsage keeps the freshest usage reading per engine of the tasks and
// of the project's previous status, so a reading outlives its task.
func engineUsage(prev []aiv1.EngineUsage, tasks []aiv1.AgentTask) []aiv1.EngineUsage {
	for _, t := range tasks {
		for _, u := range t.Status.Run.EngineUsage {
			prev = aiv1.MergeEngineUsage(prev, u)
		}
	}
	return prev
}

// addResearchUsage adds the usage readings and active limits the current
// runs of the project's researches reported, like a task's, so factory and
// research share one view of the budget.
func addResearchUsage(st *aiv1.AgentProjectStatus, project string, researches []aiv1.AgentResearch, now time.Time) {
	for _, res := range researches {
		run := res.Status.Run
		if res.Spec.ProjectRef != project || run == nil {
			continue
		}
		for _, u := range run.EngineUsage {
			st.EngineUsage = aiv1.MergeEngineUsage(st.EngineUsage, u)
		}
		for _, l := range run.UsageLimits {
			i := slices.IndexFunc(st.UsageLimits, func(cur aiv1.UsageLimit) bool { return cur.Engine == l.Engine })
			switch {
			case !l.Until.After(now):
			case i < 0:
				st.UsageLimits = append(st.UsageLimits, l)
			case l.Until.After(st.UsageLimits[i].Until.Time):
				st.UsageLimits[i] = l
			}
		}
	}
	sort.Slice(st.UsageLimits, func(i, j int) bool { return st.UsageLimits[i].Engine < st.UsageLimits[j].Engine })
}

// titlePattern compiles milestones.titlePattern; nil when unset.
func titlePattern(spec aiv1.AgentProjectSpec) (*regexp.Regexp, error) {
	if spec.Milestones.TitlePattern == "" {
		return nil, nil
	}
	re, err := regexp.Compile(spec.Milestones.TitlePattern)
	if err != nil {
		return nil, fmt.Errorf("invalid milestones.titlePattern: %w", err)
	}
	return re, nil
}

// milestoneSelection reports whether a milestone is planned, and whether on
// autopilot.
func milestoneSelection(spec aiv1.AgentProjectSpec, titleRE *regexp.Regexp, m gitlab.Milestone) (selected, autopilot bool) {
	// An owners or rule line neither selects a milestone nor is a spec.
	sel := m
	sel.Description = rules.Spec(m.Description, spec.Workflow.CommandPrefix)
	autopilot = spec.Milestones.Autopilot() != "" && milestoneSelected(sel, titleRE, spec.Milestones.Autopilot())
	return autopilot || milestoneSelected(sel, titleRE, spec.Milestones.Marker()), autopilot
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
func (r *AgentProjectReconciler) syncSpecTask(ctx context.Context, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, m gitlab.Milestone, task *aiv1.AgentTask, autopilot bool, owners, ownerSubjects []string) (*aiv1.AgentTask, error) {
	prefix := spec.Workflow.CommandPrefix
	// The owners, owner-subjects, rules and budget are no part of the spec:
	// changing them re-plans nothing.
	hash := hashString(rules.Spec(m.Description, prefix))
	ruleLines := rules.Lines(m.Description, prefix)
	budget, err := rules.Budget(m.Description, prefix)
	if err != nil {
		log.FromContext(ctx).Info("ignoring the milestone's budget line: no budget", "milestone", m.Title, "error", err.Error())
	}
	ref := &aiv1.MilestoneRef{ID: m.ID, IID: m.IID, Title: m.Title}
	if task == nil {
		task, err = r.newTask(project, naming.SpecTask(project.Name, m.IID, m.Title), aiv1.TaskTypeSpec)
		if err != nil {
			return nil, err
		}
		task.Labels[LabelMilestoneIID] = strconv.FormatInt(m.IID, 10)
		task.Spec.Milestone = ref
		task.Spec.RunRequest = 1
		task.Spec.Source.DescriptionHash = hash
		task.Spec.Autopilot = autopilot
		task.Spec.Owners, task.Spec.OwnerSubjects, task.Spec.Rules = owners, ownerSubjects, ruleLines
		task.Spec.BudgetUSD = budget
		log.FromContext(ctx).Info("planning milestone", "milestone", m.Title, "task", task.Name, "autopilot", autopilot)
		return task, r.Create(ctx, task)
	}
	retry := r.specRetryDue(task, spec.Lifecycle.InfraRetryAfter.Duration)
	if specTaskCurrent(spec, m, task, autopilot, owners, ownerSubjects) && !retry {
		return task, nil
	}
	task.Spec.Autopilot = autopilot
	task.Spec.Owners, task.Spec.OwnerSubjects, task.Spec.Rules = owners, ownerSubjects, ruleLines
	task.Spec.BudgetUSD = budget
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

// specTaskCurrent reports whether a milestone's spec task matches it: the
// spec's hash, the milestone, autopilot, owners, owner-subjects, rules and
// budget.
func specTaskCurrent(spec aiv1.AgentProjectSpec, m gitlab.Milestone, task *aiv1.AgentTask, autopilot bool, owners, ownerSubjects []string) bool {
	prefix := spec.Workflow.CommandPrefix
	budget, _ := rules.Budget(m.Description, prefix)
	return task.Spec.Source.DescriptionHash == hashString(rules.Spec(m.Description, prefix)) &&
		*task.Spec.Milestone == aiv1.MilestoneRef{ID: m.ID, IID: m.IID, Title: m.Title} && task.Spec.Autopilot == autopilot &&
		slices.Equal(task.Spec.Owners, owners) && slices.Equal(task.Spec.OwnerSubjects, ownerSubjects) &&
		slices.Equal(task.Spec.Rules, rules.Lines(m.Description, prefix)) && task.Spec.BudgetUSD == budget
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
	is gitlab.Issue, task, st *aiv1.AgentTask, autopilot bool, pause string, owners, ownerSubjects []string) (*aiv1.AgentTask, *gitlab.MergeRequest, error) {
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
	task.Spec.Owners, task.Spec.OwnerSubjects = owners, ownerSubjects
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
		// "/agent use" and "/agent effort" become labels at once, "/agent
		// budget" the task's budget, also the ones written before the task
		// existed; the answer follows once the task is saved.
		if reply, err = r.applyRouting(ctx, gl, spec, &is, cmdIssue, cmdMR, &task.Spec, project.Status.BotUserID); err != nil {
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

	// Desk chat commands (kind "command") are applied on every sync, not
	// only when the issue or MR changed: they arrive via the dispatcher.
	chatRun, err := r.applyChatCommands(ctx, gl, spec, &is, task, mr)
	if err != nil {
		return nil, nil, err
	}

	requested := ready || command || conflict || chatRun
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

const maxChatAnswers = 20

// applyChatCommands applies Desk chat commands newer than
// src.LastChatCommandID (Implement tasks). It records answers in
// src.ChatAnswers and advances the mark in the same write as a RunRequest
// bump. Returns whether a run was requested (retry or a successful review).
func (r *AgentProjectReconciler) applyChatCommands(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec,
	is *gitlab.Issue, task *aiv1.AgentTask, mr *gitlab.MergeRequest) (bool, error) {
	src := &task.Spec.Source
	var pending []aiv1.ChatMessage
	for _, m := range src.ChatMessages {
		if m.Kind == "command" && m.ID > src.LastChatCommandID {
			pending = append(pending, m)
		}
	}
	if len(pending) == 0 {
		return false, nil
	}
	slices.SortFunc(pending, func(a, b aiv1.ChatMessage) int { return cmp.Compare(a.ID, b.ID) })
	logger := log.FromContext(ctx)
	now := metav1.Now()
	run := false
	answer := func(id int64, text string) {
		src.ChatAnswers = append(src.ChatAnswers, aiv1.ChatReply{MessageID: id, Text: text, At: now})
		src.LastChatCommandID = id
	}
	for _, m := range pending {
		cmd, err := chatcmd.Parse(m.Text)
		if err != nil {
			answer(m.ID, err.Error())
			logger.Info("chat command skipped", "id", m.ID, "author", m.Author, "error", err.Error())
			continue
		}
		if !cmd.ImplementOnly() {
			answer(m.ID, "not supported on this task yet")
			logger.Info("chat command not supported yet", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author)
			continue
		}
		logger.Info("applying chat command", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author)
		switch cmd.Cmd {
		case "retry":
			run = true
			answer(m.ID, "🤖 A new run is requested.")
		case "review":
			text := "There is no pushed head of the agent to review."
			if mr != nil && mr.SHA != "" && mr.SHA == task.Status.Run.PushedSHA {
				if err := r.askReview(ctx, task, mr.SHA); err != nil {
					return false, err
				}
				run = true
				text = "🤖 The reviewers will run again on the pushed head."
			}
			answer(m.ID, text)
		case "use", "effort", "budget":
			rt, ok := cmd.Routing()
			if !ok {
				answer(m.ID, "not a routing command")
				continue
			}
			ch := command.Apply(spec, is.Labels, []command.Routing{rt})
			if len(ch.Add)+len(ch.Remove) > 0 {
				if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, is.IID, gitlab.UpdateIssueOptions{AddLabels: ch.Add, RemoveLabels: ch.Remove}); err != nil {
					return false, fmt.Errorf("setting the model and effort labels of issue #%d: %w", is.IID, err)
				}
				is.Labels = append(slices.DeleteFunc(is.Labels, func(l string) bool { return slices.Contains(ch.Remove, l) }), ch.Add...)
			}
			if ch.Budget != nil {
				task.Spec.BudgetUSD = *ch.Budget
			}
			answer(m.ID, ch.Reply())
		}
	}
	if n := len(src.ChatAnswers); n > maxChatAnswers {
		src.ChatAnswers = append([]aiv1.ChatReply(nil), src.ChatAnswers[n-maxChatAnswers:]...)
	}
	return run, nil
}

// applyRouting turns the "/agent use", "/agent effort" and "/agent budget"
// comments of humans that are newer than the last applied one into the
// issue's model and effort labels and the task's budget (ts). It returns
// the answer, to be posted once the task is saved, where the last command
// was written.
func (r *AgentProjectReconciler) applyRouting(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec, is *gitlab.Issue,
	issueNotes, mrNotes []gitlab.Note, ts *aiv1.AgentTaskSpec, botID int64) (*routingReply, error) {
	prefix := spec.Workflow.CommandPrefix
	src := &ts.Source
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
	if ch.Budget != nil {
		ts.BudgetUSD = *ch.Budget
	}
	log.FromContext(ctx).Info("model, effort and budget commands applied", "issue", is.IID, "add", ch.Add, "remove", ch.Remove, "budgetUSD", ts.BudgetUSD)
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

// SetupWithManager registers the controller.
func (r *AgentProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&aiv1.AgentProject{}, builder.WithPredicates(predicate.GenerationChangedPredicate{}))
	if r.Wakes != nil {
		b = b.WatchesRawSource(source.Channel(r.Wakes, &handler.EnqueueRequestForObject{}))
	}
	return b.Named("agentproject").Complete(r)
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
