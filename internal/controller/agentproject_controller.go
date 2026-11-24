package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/history"
	"github.com/jonathanwiemers/ai-operator/internal/milestoneorder"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
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
	// CrossRepoAllowed is the cluster's allow-list of companion projects
	// (--cross-repo-allowed-projects). Empty: cross-repo changes are off.
	// Read the targets through crossRepoTargets only.
	CrossRepoAllowed []string
	// CrossRepoNever is the cluster's never-list of companion projects
	// (--cross-repo-never-projects): never a target, even when allowed.
	CrossRepoNever []string
	// SelfProject is the GitLab project the operator is built from
	// (--self-project), normalized. A companion of its merge request lands
	// once Version is a release that contains the merge. Empty: that
	// project follows the tag rule like any other.
	SelfProject string
	// Version is the running operator's release (main.version); "dev" or
	// empty is no release, so SelfProject's companions wait.
	Version string
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
	// project's GitLab client lists, and the milestones' decisions.
	History *history.Recorder
	// recordDecisions, when set, records decisions instead of History
	// (tests).
	recordDecisions func(store.IssueDecisions)
	// recordEvent, when set, records the reconciler's history events
	// instead of History (tests).
	recordEvent func(store.Event)
	// Research, set with a research database, adds the usage readings and
	// limits of the project's AgentResearch runs to its status.
	Research bool
	// Spend, set with a research database, is the milestone spend the
	// pilot's retrospective reports. Nil: it sums the live tasks.
	Spend SpendSource
	// FollowUps, set with a research database, reads the follow-ups of
	// milestones without a spec task (closed ones) that a line
	// "/agent followup-of" names. Nil: such milestones are unknown.
	FollowUps FollowUpSource
	// ChatContext, set with a research database, is the Desk chat a run
	// request hands a fresh session on a project without GitLab input.
	ChatContext ChatContextSource
	// Backfill, set with a research database, fills the Desk's snapshot
	// with the closed milestones the recorder never saw, one step after
	// each poll.
	Backfill *history.Backfill
	// Requirements, set with a research database, stores the requirement
	// counts of the open and newest closed milestones after each poll.
	Requirements RequirementsStore
	// Logs reads the ends of a stalled run's container logs for the pilot's
	// run-stalled event. Nil: the event comes without logs.
	Logs containerLogs

	requirements requirementCounts
	mu           sync.Mutex
	// synced is the last delivery each project's last full sync covered.
	synced map[types.NamespacedName]time.Time
}

// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentprojects,verbs=get;list;watch
// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentprojects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agenttasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentresearches,verbs=get;list;watch
// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentprojectsets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *AgentProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var project aiv1.AgentProject
	if err := r.Get(ctx, req.NamespacedName, &project); err != nil {
		if apierrors.IsNotFound(err) {
			projectSyncs.DeletePartialMatch(prometheus.Labels{"namespace": req.Namespace, "project": req.Name})
		}
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
	lastDelivery, liveDelivery, receiving := r.webhookDeliveries(&project, spec, started)
	// A set child's milestones are watched by its set, which records a
	// delivery and wakes the child when they change.
	viaSet := ""
	if spec.Webhook.SecretRef != nil {
		if name, current := r.setDiscoveryCurrent(ctx, &project, started); current {
			viaSet, receiving = name, true
		}
	}
	// While deliveries arrive, they tell of every change but the milestones'.
	// Steps that only time advances (infra retries, auto-resume, the main
	// health interval, the hook recheck) run on full syncs, so at most
	// webhook.fallbackInterval late then.
	full := !receiving || r.fullSyncDue(&project, spec, lastDelivery, started)

	// A slow or rate-limiting GitLab must not hold the worker for long: the
	// client's retries stop at this deadline, and the next poll goes on.
	sctx, cancel := context.WithTimeout(ctx, max(spec.PollInterval.Duration, syncTimeout))
	var syncErr error
	if !full && viaSet != "" {
		logger.V(1).Info("Set watches the milestones: poll skipped", "set", viaSet)
	} else if !full {
		full, syncErr = r.milestonesChanged(sctx, &project, spec)
		if syncErr == nil && !full {
			logger.V(1).Info("Light poll: no milestone changed, full sync skipped")
		}
	}
	if full {
		syncErr = r.sync(sctx, &project, spec)
	}
	// An idle project's hook gets a test delivery, which keeps it receiving.
	// A project its set covers needs none.
	if viaSet == "" && r.heartbeatDue(&project, spec, liveDelivery, started) {
		r.heartbeat(sctx, &project, spec)
	}
	cancel()
	projectSyncs.WithLabelValues(project.Namespace, project.Name, syncKind(full, viaSet)).Inc()
	// While GitLab gives out, the backfill waits too.
	if syncErr == nil || !gitlab.IsTransient(syncErr) {
		r.backfill(ctx, &project, spec)
	}
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
	reportDeliveries(&project, spec, prevDelivery, liveDelivery, receiving, viaSet)
	reportFallbacks(&project, spec)
	reportExperiment(&project, spec)
	r.reportCrossRepo(&project, spec)
	if err := r.Status().Patch(ctx, &project, client.MergeFrom(orig)); err != nil {
		return ctrl.Result{}, err
	}
	// Errors are reported in the status; retry on the regular poll interval
	// instead of hammering GitLab with exponential backoff.
	return ctrl.Result{RequeueAfter: spec.PollInterval.Duration}, nil
}

// syncKind names what a reconcile read from GitLab for the syncs metric.
func syncKind(full bool, viaSet string) string {
	switch {
	case full:
		return "full"
	case viaSet != "":
		return "skipped"
	}
	return "light"
}

// fullSyncDue reports whether a project that receives deliveries needs a full
// sync now: a delivery came that the last one did not cover, it is
// webhook.fallbackInterval old, it failed, or the spec changed since. With
// only settling milestones, the fallback is at least settleInterval.
func (r *AgentProjectReconciler) fullSyncDue(project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, lastDelivery, now time.Time) bool {
	fallback := spec.Webhook.FallbackInterval.Duration
	if onlySettling(project) {
		fallback = max(fallback, settleInterval)
	}
	last := project.Status.LastSyncTime
	synced := meta.FindStatusCondition(project.Status.Conditions, "Synced")
	r.mu.Lock()
	seen, ok := r.synced[types.NamespacedName{Namespace: project.Namespace, Name: project.Name}]
	r.mu.Unlock()
	return last == nil || synced == nil || synced.Status != metav1.ConditionTrue || synced.ObservedGeneration != project.Generation ||
		!ok || lastDelivery.After(seen) || now.Sub(last.Time) >= fallback
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

// setDiscoveryCurrent returns the name of the AgentProjectSet that watches
// the project's milestones, and whether that set discovered its group within
// twice its pollInterval and holds a hash of all the project's milestones:
// then the project need not list them itself. A child is watched by the set
// that controls it; an AgentProject no set controls by any set that lists
// its project as explicit. A missing or unreadable set is not current, and
// the project polls on its own.
func (r *AgentProjectReconciler) setDiscoveryCurrent(ctx context.Context, project *aiv1.AgentProject, now time.Time) (string, bool) {
	ref := metav1.GetControllerOf(project)
	if ref != nil {
		if ref.Kind != "AgentProjectSet" || !strings.HasPrefix(ref.APIVersion, aiv1.GroupVersion.Group+"/") {
			return "", false
		}
		var set aiv1.AgentProjectSet
		if err := r.Get(ctx, types.NamespacedName{Namespace: project.Namespace, Name: ref.Name}, &set); err != nil {
			if !apierrors.IsNotFound(err) {
				log.FromContext(ctx).V(1).Info("Reading the project's set failed, polling on its own", "set", ref.Name, "error", err.Error())
			}
			return ref.Name, false
		}
		if set.UID != ref.UID {
			return ref.Name, false
		}
		return ref.Name, setWatches(&set, func(e aiv1.ProjectSetProjectStatus) bool { return e.Child == project.Name }, now)
	}
	var sets aiv1.AgentProjectSetList
	if err := r.List(ctx, &sets); err != nil {
		log.FromContext(ctx).V(1).Info("Listing project sets failed, polling on its own", "error", err.Error())
		return "", false
	}
	path := aiv1.NormalizeProject(project.Spec.GitLab.Project)
	for i := range sets.Items {
		set := &sets.Items[i]
		if setWatches(set, func(e aiv1.ProjectSetProjectStatus) bool {
			return e.State == aiv1.ProjectSetExplicit && aiv1.NormalizeProject(e.Path) == path
		}, now) {
			return set.Name, true
		}
	}
	return "", false
}

// setWatches reports whether the set discovered its group within twice its
// pollInterval and its entry that match picks holds a milestones hash.
func setWatches(set *aiv1.AgentProjectSet, match func(aiv1.ProjectSetProjectStatus) bool, now time.Time) bool {
	last := set.Status.LastDiscoveryTime
	if last == nil || now.Sub(last.Time) >= 2*set.Spec.WithDefaults().PollInterval.Duration {
		return false
	}
	i := slices.IndexFunc(set.Status.Projects, match)
	return i >= 0 && set.Status.Projects[i].Milestones != "" && !set.Status.Projects[i].MilestonesTruncated
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
	defer countGitLabRequests(project.Name, gl)
	milestones, err := gl.ListMilestones(ctx, spec.GitLab.Project)
	if err != nil {
		return false, fmt.Errorf("listing milestones: %w", err)
	}
	selected := map[int64]bool{}
	ahead := queuedAhead(spec, titleRE, milestones, planned, project.Status.MilestoneOrder)
	for _, m := range milestones {
		ok, autopilot := milestoneSelection(spec, titleRE, m)
		if !ok {
			continue
		}
		task := planned[m.IID]
		owners := notify.Resolve(m.Description, spec.Workflow.CommandPrefix, spec.Notify.Users, r.DefaultOwners)
		subjects := notify.MilestoneOwnerSubjects(m.Description, spec.Workflow.CommandPrefix)
		if task == nil || !specTaskCurrent(spec, m, task, autopilot, owners, subjects, r.milestoneRepos(spec, m.Description), ahead[m.IID]) || r.specRetryDue(task, spec.Lifecycle.InfraRetryAfter.Duration) {
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

func (r *AgentProjectReconciler) now() time.Time { return nowOr(r.Now) }

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
	// The merge/push guard: the bot's token reaches the whole group, the
	// operator merges and writes merge requests only at home and in the
	// valid companion targets.
	guarded := gitlab.Guard(r.NewGitLab(spec.GitLab.URL, token), gitlab.NewPolicy(spec, r.CrossRepoAllowed, r.CrossRepoNever),
		log.FromContext(ctx).WithName("guard"))
	return r.History.WrapGitLab(guarded, history.Project{
		Namespace: project.Namespace, Name: project.Name,
		WebURL: base + "/" + spec.GitLab.Project,
		Bot:    project.Status.BotUsername,
	}), nil
}

// sync performs one GitLab poll.
func (r *AgentProjectReconciler) sync(ctx context.Context, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) error {
	started := r.now()
	gl, err := r.gitlabClient(ctx, project, spec)
	if err != nil {
		return err
	}
	defer countGitLabRequests(project.Name, gl)
	var poll *projectPoll
	if c, ok := gl.(interface{ Stats() gitlab.Stats }); ok {
		// What a poll costs GitLab; retries mean it pushed back.
		defer func() {
			st := c.Stats()
			lvl := 1
			if st.Retries > 0 {
				lvl = 0
			}
			settled := 0
			if poll != nil {
				settled = poll.settled
			}
			log.FromContext(ctx).V(lvl).Info("GitLab poll done", "requests", st.Requests, "retries", st.Retries, "settling", settled)
		}()
	}
	poll, err = r.newProjectPoll(ctx, gl, project, spec, started)
	if err != nil {
		return err
	}
	if err := poll.syncMilestones(ctx); err != nil {
		return err
	}
	if err := poll.listIssues(ctx); err != nil {
		return err
	}
	issuePhases{poll}.run(ctx)
	// One failing step does not hold up the others or the cleanup: its
	// error is collected in poll.errs.
	mainHealthPhase{poll}.run(ctx)
	autopilotPhase{poll}.run(ctx)
	releasePhase{poll}.run(ctx)
	pilotPhase{poll}.run(ctx)
	if err := (cleanupPhase{poll}).run(ctx); err != nil {
		return err
	}
	return errors.Join(poll.errs...)
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

// reportExperiment sets ExperimentValid: True for a valid
// agents.implement.experiment, False with its problems (the runner then
// assigns no arms), absent without one.
func reportExperiment(project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) {
	if spec.Agents.Implement.Experiment == nil {
		meta.RemoveStatusCondition(&project.Status.Conditions, aiv1.ConditionExperimentValid)
		return
	}
	cond := metav1.Condition{Type: aiv1.ConditionExperimentValid, Status: metav1.ConditionTrue, Reason: "Valid",
		Message: "the experiment is valid", ObservedGeneration: project.Generation}
	if errs := spec.ExperimentErrors(); len(errs) > 0 {
		cond.Status, cond.Reason, cond.Message = metav1.ConditionFalse, "InvalidExperiment", textutil.Truncate(strings.Join(errs, "; "), 1024)
	}
	meta.SetStatusCondition(&project.Status.Conditions, cond)
}

// reportCrossRepo sets CrossRepoReady: True when every spec.crossRepo
// target is valid, False naming the dropped ones, absent without
// spec.crossRepo.
func (r *AgentProjectReconciler) reportCrossRepo(project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) {
	if spec.CrossRepo == nil {
		meta.RemoveStatusCondition(&project.Status.Conditions, aiv1.ConditionCrossRepoReady)
		return
	}
	cond := metav1.Condition{Type: aiv1.ConditionCrossRepoReady, Status: metav1.ConditionTrue, Reason: "TargetsValid",
		Message: "every companion target is valid", ObservedGeneration: project.Generation}
	if _, problems := aiv1.ValidateCrossRepo(spec, r.CrossRepoAllowed, r.CrossRepoNever); len(problems) > 0 {
		cond.Status, cond.Reason = metav1.ConditionFalse, "InvalidTargets"
		cond.Message = textutil.Truncate("dropped: "+strings.Join(problems, "; "), 4096)
	}
	meta.SetStatusCondition(&project.Status.Conditions, cond)
}

// crossRepoTargets returns the spec's valid companion targets: the cluster
// allows them and they pass ValidateCrossRepo. Every use of the targets
// goes through it.
func (r *AgentProjectReconciler) crossRepoTargets(spec aiv1.AgentProjectSpec) []aiv1.CrossRepoTarget {
	return crossRepoTargets(spec, r.CrossRepoAllowed, r.CrossRepoNever)
}

// crossRepoTargets returns the spec's valid companion targets under the
// cluster's allow-list and never-list (both reconcilers).
func crossRepoTargets(spec aiv1.AgentProjectSpec, allowed, never []string) []aiv1.CrossRepoTarget {
	valid, _ := aiv1.ValidateCrossRepo(spec, allowed, never)
	return valid
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

// addResearchUsage adds the usage readings the current runs of the
// project's researches reported, like a task's, so factory and research
// share one view of the budget. Their limits are in aiv1.ProjectUsageLimits.
func addResearchUsage(st *aiv1.AgentProjectStatus, project string, researches []aiv1.AgentResearch) {
	for _, res := range researches {
		run := res.Status.Run
		if res.Spec.ProjectRef != project || run == nil {
			continue
		}
		for _, u := range run.EngineUsage {
			st.EngineUsage = aiv1.MergeEngineUsage(st.EngineUsage, u)
		}
	}
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
// that failed for an outside reason (specRetryDue). An autopilot milestone
// with older ones still open (ahead) waits with runRequest 0 until its turn
// comes (specTurn), then asks for its first run with the hash of that moment.
func (r *AgentProjectReconciler) syncSpecTask(ctx context.Context, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, m gitlab.Milestone, task *aiv1.AgentTask, autopilot bool, ahead []int64, owners, ownerSubjects []string) (*aiv1.AgentTask, error) {
	prefix := spec.Workflow.CommandPrefix
	// The owners, owner-subjects, rules, budget, parallel and followup-of
	// are no part of the spec: changing them re-plans nothing.
	hash := hashString(rules.Spec(m.Description, prefix))
	ruleLines := rules.Lines(m.Description, prefix)
	budget, err := rules.Budget(m.Description, prefix)
	if err != nil {
		log.FromContext(ctx).Info("ignoring the milestone's budget line: no budget", "milestone", m.Title, "error", err.Error())
	}
	maxParallel, err := rules.Parallel(m.Description, prefix)
	if err != nil {
		log.FromContext(ctx).Info("ignoring the milestone's parallel line", "milestone", m.Title, "error", err.Error())
	}
	// A malformed followup-of line has no value; syncFollowUpOf notices it.
	followUpOf, err := rules.FollowUpOf(m.Description, prefix)
	if err != nil {
		log.FromContext(ctx).Info("ignoring the milestone's followup-of line", "milestone", m.Title, "error", err.Error())
	}
	// The repo lines are part of the spec (and its hash); which of them
	// name a valid target is the project's config.
	repos := r.milestoneRepos(spec, m.Description)
	ref := &aiv1.MilestoneRef{ID: m.ID, IID: m.IID, Title: m.Title}
	if task == nil {
		task, err = r.newTask(project, naming.SpecTask(project.Name, m.IID, m.Title), aiv1.TaskTypeSpec)
		if err != nil {
			return nil, err
		}
		task.Labels[LabelMilestoneIID] = strconv.FormatInt(m.IID, 10)
		task.Spec.Milestone = ref
		task.Spec.RunRequest = 1
		if !specTurn(task, ahead) {
			task.Spec.RunRequest, task.Spec.QueuedBehind = 0, ahead
			log.FromContext(ctx).Info("milestone waits for its turn", "milestone", m.Title, "task", task.Name, "queuedBehind", ahead)
		}
		task.Spec.Source.DescriptionHash = hash
		task.Spec.Autopilot = autopilot
		task.Spec.Owners, task.Spec.OwnerSubjects, task.Spec.Rules = owners, ownerSubjects, ruleLines
		task.Spec.Repos = repos
		logRepoProblems(ctx, m.Title, repos)
		task.Spec.BudgetUSD, task.Spec.MaxParallel, task.Spec.FollowUpOf = budget, maxParallel, followUpOf
		if task.Spec.RunRequest > 0 {
			log.FromContext(ctx).Info("planning milestone", "milestone", m.Title, "task", task.Name, "autopilot", autopilot)
		}
		return task, r.Create(ctx, task)
	}
	retry := r.specRetryDue(task, spec.Lifecycle.InfraRetryAfter.Duration)
	if specTaskCurrent(spec, m, task, autopilot, owners, ownerSubjects, repos, ahead) && !retry {
		return task, nil
	}
	task.Spec.Autopilot = autopilot
	task.Spec.Owners, task.Spec.OwnerSubjects, task.Spec.Rules = owners, ownerSubjects, ruleLines
	if !slices.Equal(task.Spec.Repos, repos) {
		logRepoProblems(ctx, m.Title, repos)
	}
	task.Spec.Repos = repos
	task.Spec.BudgetUSD, task.Spec.MaxParallel, task.Spec.FollowUpOf = budget, maxParallel, followUpOf
	switch waiting := task.Spec.RunRequest == 0; {
	case waiting && specTurn(task, ahead):
		// The first run plans the description of this moment.
		log.FromContext(ctx).Info("its turn has come, planning", "milestone", m.Title, "task", task.Name)
		task.Spec.RunRequest, task.Spec.Source.DescriptionHash, task.Spec.QueuedBehind = 1, hash, nil
	case waiting:
		// An edit while waiting plans nothing.
		if !slices.Equal(task.Spec.QueuedBehind, ahead) {
			log.FromContext(ctx).Info("milestone waits for its turn", "milestone", m.Title, "task", task.Name, "queuedBehind", ahead)
		}
		task.Spec.Source.DescriptionHash, task.Spec.QueuedBehind = hash, ahead
	case task.Spec.Source.DescriptionHash != hash:
		log.FromContext(ctx).Info("milestone spec changed, re-planning", "milestone", m.Title, "task", task.Name)
		task.Spec.RunRequest++
		task.Spec.Source.DescriptionHash = hash
	case retry:
		log.FromContext(ctx).Info("planning again after a failure for an outside reason", "milestone", m.Title, "task", task.Name)
		task.Spec.Source.InfraRetry = task.Spec.RunRequest + 1
		task.Spec.RunRequest++
	}
	task.Spec.Milestone = ref
	return task, r.Update(ctx, task)
}

// specTaskCurrent reports whether a milestone's spec task matches it: the
// spec's hash, the milestone, autopilot, owners, owner-subjects, rules,
// budget, parallel, followup-of, repo lines and, while it waits for its
// turn, the milestones ahead. A waiting task whose turn has come is not
// current.
func specTaskCurrent(spec aiv1.AgentProjectSpec, m gitlab.Milestone, task *aiv1.AgentTask, autopilot bool, owners, ownerSubjects []string, repos []aiv1.MilestoneRepo, ahead []int64) bool {
	prefix := spec.Workflow.CommandPrefix
	budget, _ := rules.Budget(m.Description, prefix)
	maxParallel, _ := rules.Parallel(m.Description, prefix)
	followUpOf, _ := rules.FollowUpOf(m.Description, prefix)
	queued := ahead
	if task.Spec.RunRequest > 0 {
		queued = nil // a task that has asked for a run waits for nothing
	} else if specTurn(task, ahead) {
		return false
	}
	return slices.Equal(task.Spec.QueuedBehind, queued) && task.Spec.Source.DescriptionHash == hashString(rules.Spec(m.Description, prefix)) &&
		*task.Spec.Milestone == aiv1.MilestoneRef{ID: m.ID, IID: m.IID, Title: m.Title} && task.Spec.Autopilot == autopilot &&
		slices.Equal(task.Spec.Owners, owners) && slices.Equal(task.Spec.OwnerSubjects, ownerSubjects) &&
		slices.Equal(task.Spec.Rules, rules.Lines(m.Description, prefix)) && task.Spec.BudgetUSD == budget &&
		task.Spec.MaxParallel == maxParallel &&
		equality.Semantic.DeepEqual(task.Spec.FollowUpOf, followUpOf) &&
		slices.Equal(task.Spec.Repos, repos)
}

// specTurn reports whether a spec task that has never asked for a run (or a
// new one) may ask for its first: nothing is ahead of it in the stored run
// order and no owner paused it (their `resume` lets it plan).
func specTurn(task *aiv1.AgentTask, ahead []int64) bool {
	ap := task.Status.Autopilot
	if ap == nil {
		return len(ahead) == 0
	}
	return len(ahead) == 0 && (ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseOwner)
}

// autopilotEntries returns the milestoneorder entries of the selected
// autopilot milestones. A milestone off autopilot or closed (not listed) has
// none.
func autopilotEntries(spec aiv1.AgentProjectSpec, titleRE *regexp.Regexp, milestones []gitlab.Milestone, byMilestone map[int64]*aiv1.AgentTask) []milestoneorder.Entry {
	var entries []milestoneorder.Entry
	for _, m := range milestones {
		if _, autopilot := milestoneSelection(spec, titleRE, m); autopilot {
			entries = append(entries, milestoneorder.EntryOf(m.IID, byMilestone[m.IID]))
		}
	}
	return entries
}

// queuedAhead returns, for every selected autopilot milestone, the iids of
// the selected autopilot milestones ranked before it that are still open, in
// run order (the project's stored order, status.milestoneOrder): their spec
// task is missing or not done (a paused one counts). A milestone off
// autopilot or closed (not listed) holds back nothing.
func queuedAhead(spec aiv1.AgentProjectSpec, titleRE *regexp.Regexp, milestones []gitlab.Milestone, byMilestone map[int64]*aiv1.AgentTask, order []int64) map[int64][]int64 {
	return milestoneorder.Ahead(order, autopilotEntries(spec, titleRE, milestones, byMilestone))
}

// specRetryDue reports whether a planning run that failed for an outside
// reason (run.blockReason infra) is due for its automatic retry: once per run
// request, lifecycle.infraRetryAfter after it ended, like an implement run's
// (syncRunBlock). A spec task that has never run (it waits for its turn) has
// nothing to retry.
func (r *AgentProjectReconciler) specRetryDue(task *aiv1.AgentTask, after time.Duration) bool {
	run := task.Status.Run
	return run.ObservedRunRequest > 0 && !task.RunPending() && run.LastResult == aiv1.RunResultFailed && run.BlockReason == aiv1.BlockInfra &&
		task.Spec.Source.InfraRetry != run.ObservedRunRequest && run.LastActivityTime != nil && !r.now().Before(run.LastActivityTime.Add(after))
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
	r.setChatContext(ctx, spec, task)
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
