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
	"github.com/jonathanwiemers/ai-operator/internal/milestoneorder"
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
	// project's GitLab client lists.
	History *history.Recorder
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

	requirements requirementCounts
	mu           sync.Mutex
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
	reportDeliveries(&project, spec, prevDelivery, lastDelivery, receiving)
	reportFallbacks(&project, spec)
	r.reportCrossRepo(&project, spec)
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
	ahead := queuedAhead(spec, titleRE, milestones, planned)
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
	poll, err := r.newProjectPoll(ctx, gl, project, spec, started)
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
	// The owners, owner-subjects, rules, budget and followup-of are no part
	// of the spec: changing them re-plans nothing.
	hash := hashString(rules.Spec(m.Description, prefix))
	ruleLines := rules.Lines(m.Description, prefix)
	budget, err := rules.Budget(m.Description, prefix)
	if err != nil {
		log.FromContext(ctx).Info("ignoring the milestone's budget line: no budget", "milestone", m.Title, "error", err.Error())
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
		task.Spec.BudgetUSD, task.Spec.FollowUpOf = budget, followUpOf
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
	task.Spec.BudgetUSD, task.Spec.FollowUpOf = budget, followUpOf
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
// budget, followup-of, repo lines and, while it waits for its turn, the
// milestones ahead. A waiting task whose turn has come is not current.
func specTaskCurrent(spec aiv1.AgentProjectSpec, m gitlab.Milestone, task *aiv1.AgentTask, autopilot bool, owners, ownerSubjects []string, repos []aiv1.MilestoneRepo, ahead []int64) bool {
	prefix := spec.Workflow.CommandPrefix
	budget, _ := rules.Budget(m.Description, prefix)
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
		equality.Semantic.DeepEqual(task.Spec.FollowUpOf, followUpOf) &&
		slices.Equal(task.Spec.Repos, repos)
}

// specTurn reports whether a spec task that has never asked for a run (or a
// new one) may ask for its first: nothing is ahead of it and no owner paused
// it (their `resume` lets it plan), or an owner asked to start it now
// (`start`, AutopilotStatus.StartRequestedBy), which leaves a pause as it is.
func specTurn(task *aiv1.AgentTask, ahead []int64) bool {
	ap := task.Status.Autopilot
	if ap == nil {
		return len(ahead) == 0
	}
	return ap.StartRequestedBy != "" || len(ahead) == 0 && (ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseOwner)
}

// queuedAhead returns, for every selected autopilot milestone, the iids of
// the selected autopilot milestones ranked before it that are still open, in
// run order (milestoneorder: run-next marks first, then age): their spec task
// is missing or not done (a paused one counts). A milestone off autopilot or
// closed (not listed) holds back nothing.
func queuedAhead(spec aiv1.AgentProjectSpec, titleRE *regexp.Regexp, milestones []gitlab.Milestone, byMilestone map[int64]*aiv1.AgentTask) map[int64][]int64 {
	var entries []milestoneorder.Entry
	for _, m := range milestones {
		if _, autopilot := milestoneSelection(spec, titleRE, m); autopilot {
			entries = append(entries, milestoneorder.EntryOf(m.IID, byMilestone[m.IID]))
		}
	}
	return milestoneorder.Ahead(entries)
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

// syncImplementTask makes sure an issue handed to the agent has a task and
// turns "moved to ready" and "/agent" comments into run requests. It returns
// the task's merge request too, if it has one. st is the spec task of the
// autopilot whose queue has the issue (nil: none): a command's run of the
// issue it is paused for is recorded there (commandRun).
func (r *AgentProjectReconciler) syncImplementTask(ctx context.Context, gl gitlab.API, gate *commandGate, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	is gitlab.Issue, task *aiv1.AgentTask, autopilot bool, pause string, budgetStop, urgent bool, ms issueMilestone, owners, ownerSubjects []string, st *aiv1.AgentTask) (*aiv1.AgentTask, *gitlab.MergeRequest, error) {
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
	task.Spec.BudgetStop = budgetStop
	// The main-red fix goes ahead in the run queue; the lane is cleared on
	// the poll after the fix is over.
	task.Spec.QueueLane = ""
	if urgent {
		task.Spec.QueueLane = aiv1.QueueLaneUrgent
	}

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
	commandBy := "" // the author of the newest command, "@name"
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
		denied = append(denied, r.pilotIssueNotes(ctx, gl, project, spec, is, mr, task, issueNotes, mrNotes)...)
		if created {
			// Comments written before the task existed are context, not commands.
			src.LastCommandNoteID = latest
		} else if latest > src.LastCommandNoteID {
			review = reviewOnly(slices.Concat(cmdIssue, cmdMR), src.LastCommandNoteID, project.Status.BotUserID, wf.CommandPrefix)
			commandBy = commandAuthor(slices.Concat(cmdIssue, cmdMR), latest)
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
		if err := r.askReview(ctx, task, task.Status.Run.ReviewKey()); err != nil {
			return nil, nil, err
		}
	}

	// Desk chat commands (kind "command") are applied on every sync, not
	// only when the issue or MR changed: they arrive via the dispatcher.
	chatRun, err := r.applyChatCommands(ctx, gl, spec, &is, task, mr)
	if err != nil {
		return nil, nil, err
	}

	// The companion repository, from the labels as the commands left them.
	// It requests no run by itself.
	companionNotice, err := r.syncCompanion(ctx, gl, gate, spec, is, task, ms, owners)
	if err != nil {
		return nil, nil, err
	}

	requested := ready || command || conflict || chatRun
	if requested {
		task.Spec.RunRequest++
		r.setChatContext(ctx, spec, task)
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

	if requested && command && commandBy != "" {
		// An owner's comment took the paused issue over (status.autopilot.pauseRunBy).
		r.commandRun(ctx, st, task, commandBy)
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
	if companionNotice != "" {
		// Not tried again: the notice is recorded (source.companionNotice).
		if _, err := gl.CreateIssueNote(ctx, glProject, is.IID, companionNotice); err != nil {
			log.FromContext(ctx).Info("could not tell the issue its companion label is ignored", "issue", is.IID, "error", err.Error())
		}
	}
	link := ""
	if gate.desk && len(denied) > 0 {
		st, _, err := r.milestoneTasks(ctx, project, is.Milestone)
		if err != nil {
			log.FromContext(ctx).Info("replying without the milestone's prompts", "issue", is.IID, "error", err.Error())
		}
		link = r.deskLink(spec, is.IID, trackingIssue(st, nil), task, st)
	}
	for _, n := range denied {
		gate.deny(ctx, n, owners, link)
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

// commandAuthor is the author of the note id among notes, "@name", "" if it
// is not there.
func commandAuthor(notes []gitlab.Note, id int64) string {
	for _, n := range notes {
		if n.ID == id && n.Author.Username != "" {
			return "@" + n.Author.Username
		}
	}
	return ""
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
// bump. Returns whether a run was requested (retry, a successful review, or
// the answer to the last open question of the agent).
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
		if cmd.PromptCmd() {
			// The agent's questions: the owners' answers go to its session.
			if !slices.Contains(task.Spec.OwnerSubjects, m.Author) {
				answer(m.ID, "Only the milestone's owners can answer prompts.")
				logger.Info("chat command refused", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author, "reason", "not an owner subject")
				continue
			}
			who := m.Author
			if m.AuthorName != "" {
				who = m.AuthorName
			}
			logger.Info("applying chat command", "id", m.ID, "cmd", cmd.Cmd, "author", m.Author, "prompt", cmd.Prompt)
			reply, due := answerQuestion(task, who, cmd)
			run = run || due
			answer(m.ID, reply)
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
				if err := r.askReview(ctx, task, task.Status.Run.ReviewKey()); err != nil {
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
			ch := command.Apply(spec, r.crossRepoTargets(spec), is.Labels, []command.Routing{rt})
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

// applyRouting turns the "/agent use", "/agent effort", "/agent budget" and
// "/agent repo" comments of humans that are newer than the last applied one
// into the issue's model, effort and companion labels and the task's budget
// (ts). It returns the answer, to be posted once the task is saved, where
// the last command was written.
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
	ch := command.Apply(spec, r.crossRepoTargets(spec), is.Labels, cmds)
	if len(ch.Add)+len(ch.Remove) > 0 {
		if _, err := gl.UpdateIssue(ctx, spec.GitLab.Project, is.IID, gitlab.UpdateIssueOptions{AddLabels: ch.Add, RemoveLabels: ch.Remove}); err != nil {
			return nil, fmt.Errorf("setting the model and effort labels of issue #%d: %w", is.IID, err)
		}
		is.Labels = append(slices.DeleteFunc(is.Labels, func(l string) bool { return slices.Contains(ch.Remove, l) }), ch.Add...)
	}
	if ch.Budget != nil {
		ts.BudgetUSD = *ch.Budget
	}
	log.FromContext(ctx).Info("model, effort, budget and repo commands applied", "issue", is.IID, "add", ch.Add, "remove", ch.Remove, "budgetUSD", ts.BudgetUSD)
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
