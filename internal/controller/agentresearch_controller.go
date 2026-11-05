package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/history"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
	"github.com/jonathanwiemers/ai-operator/internal/pricing"
	"github.com/jonathanwiemers/ai-operator/internal/research"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// Conditions of an AgentResearch.
const (
	ConditionValid         = "Valid"
	ConditionDatabaseReady = "DatabaseReady"
	ConditionRunning       = "Running"

	ReasonWriterSecretMissing = "WriterSecretMissing"

	// SkippedRunInProgress is the skippedReason of a scheduled time that
	// came while the previous run was still going.
	SkippedRunInProgress = "previous run still in progress"
)

const (
	// researchDBRetry is how often a research checks the database again
	// while it is not ready.
	researchDBRetry = 30 * time.Second
	// researchInvalidRetry is how often an invalid research is checked
	// again; the writer Secret is not watched.
	researchInvalidRetry = time.Minute
	// researchRunGrace is how long after its pod's deadline a run without
	// an end is ended by the operator, e.g. when its pod never started.
	researchRunGrace = 5 * time.Minute
)

// ResearchDB is the research database as the controller uses it.
type ResearchDB interface {
	// Ready is closed once the database is migrated.
	Ready() <-chan struct{}
	// FinishRunOrInsert records a run's outcome; the first one recorded
	// wins.
	FinishRunOrInsert(context.Context, store.Run) error
	// InsertCalls writes agent calls, skipping keys already stored.
	InsertCalls(context.Context, []store.Call) (int64, error)
}

// AgentResearchReconciler runs AgentResearches, one pod per run:
//
//	due (one-off: once; scheduled: at nextRun) -> research.Choose
//	  an agent qualifies                         -> status.run, then the pod  (Running)
//	  none: one-off                              -> waiting until the reset   (Waiting)
//	  none: scheduled                            -> skipped, nextRun at the
//	                                                reset if that is earlier  (Scheduled)
//	runner reported, or the pod ended or is gone -> outcome in the store and
//	                                                 status, pod deleted      (Scheduled / Succeeded / Failed)
//
// A run that ended limited is due again at once, with its limit known. A
// scheduled time that comes while a run is in progress is skipped.
type AgentResearchReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader reads without the cache, so a pod created just now is not
	// taken for gone. Optional.
	APIReader client.Reader
	DB        ResearchDB
	// DBConn is where research pods connect to, with their project's
	// writer login.
	DBConn research.DatabaseConfig
	// DefaultAgentImage and DefaultAgentLogLevel are used for projects that
	// do not set agents.image and agents.logLevel.
	DefaultAgentImage    string
	DefaultAgentLogLevel aiv1.LogLevel
	// Candidates are the agents of runs without spec.agent, per depth, for
	// projects that do not set spec.research.candidates.
	Candidates aiv1.ResearchCandidates
	// MaxUsagePercent is the usage at which a candidate is passed over;
	// 0 means aiv1.DefaultResearchMaxUsagePercent.
	MaxUsagePercent int32
	// HistoryRetention is how long a run's agent calls are kept; 0 means
	// history.DefaultRetention.
	HistoryRetention time.Duration
	// Pricing estimates the cost of agent calls the engine reported none
	// for; nil estimates none.
	Pricing *pricing.Table
	Now     func() time.Time
}

// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentresearches,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentresearches/status,verbs=get;update;patch

func (r *AgentResearchReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var res aiv1.AgentResearch
	if err := r.Get(ctx, req.NamespacedName, &res); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !res.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	orig := res.DeepCopy()
	st := &res.Status

	select {
	case <-r.DB.Ready():
		setResearchCondition(&res, ConditionDatabaseReady, true, "Ready", "the research database is migrated")
	default:
		setResearchCondition(&res, ConditionDatabaseReady, false, "NotReady", "waiting for the research database")
		return ctrl.Result{RequeueAfter: researchDBRetry}, r.patchStatus(ctx, &res, orig)
	}
	now := r.now()

	// A run in progress ends when the runner reported, its pod ended or is
	// gone, or long after its deadline.
	var runDeadline time.Time
	if run := st.Run; run != nil && run.FinishedAt == nil {
		pod, err := r.runPod(ctx, &res)
		if err != nil {
			return ctrl.Result{}, err
		}
		runDeadline = now
		if run.StartedAt != nil {
			runDeadline = run.StartedAt.Add(res.Spec.Timeout() + researchDeadlineSlack + researchRunGrace)
		}
		if run.Outcome != "" || pod == nil || podEnded(pod) || !now.Before(runDeadline) {
			if err := r.finishRun(ctx, &res, orig, pod, runEnd(pod), now); err != nil {
				return ctrl.Result{}, err
			}
			orig = res.DeepCopy()
		}
	}
	running := st.Run != nil && st.Run.FinishedAt == nil
	if running {
		setResearchCondition(&res, ConditionRunning, true, "RunInProgress", fmt.Sprintf("run %s on %s/%s", st.Run.ID, st.Run.Engine, st.Run.Model))
	} else {
		setResearchCondition(&res, ConditionRunning, false, "Idle", "no run in progress")
	}

	plan, reason, msg, err := r.validate(ctx, &res, now)
	if err != nil {
		return ctrl.Result{}, err
	}
	if reason != "" {
		st.ObservedGeneration = res.Generation
		setResearchCondition(&res, ConditionValid, false, reason, msg)
		return ctrl.Result{RequeueAfter: researchInvalidRetry}, r.patchStatus(ctx, &res, orig)
	}
	setResearchCondition(&res, ConditionValid, true, "Valid", "the research can run")

	due, next := researchDue(&res, plan.schedule, now)
	st.ObservedGeneration = res.Generation
	var result ctrl.Result
	requeueAt := func(t time.Time) {
		if d := t.Sub(now) + time.Second; t.After(now) && (result.RequeueAfter == 0 || d < result.RequeueAfter) {
			result.RequeueAfter = d
		}
	}
	if plan.schedule != nil {
		st.NextRun = &metav1.Time{Time: next}
		requeueAt(next)
	} else {
		st.NextRun = nil
	}

	switch {
	case running:
		if due && plan.schedule != nil {
			logger.Info("skipping a scheduled run, the previous one is still in progress", "run", st.Run.ID)
			st.SkippedReason = SkippedRunInProgress
		}
		st.Phase = aiv1.ResearchPhaseRunning
		requeueAt(runDeadline)
		return result, r.patchStatus(ctx, &res, orig)
	case !due:
		if plan.schedule != nil {
			st.Phase = aiv1.ResearchPhaseScheduled
		}
		return result, r.patchStatus(ctx, &res, orig)
	}

	// No agent has usage left: a one-off waits for the earliest reset, a
	// scheduled run is skipped and tried again then, if that comes before
	// the next scheduled time.
	if choice := plan.choice; choice.Agent == nil {
		st.Waiting = &aiv1.ResearchWaiting{Engine: choice.WaitEngine, Until: metav1.NewTime(choice.WaitUntil)}
		if plan.schedule == nil {
			st.Phase = aiv1.ResearchPhaseWaiting
		} else {
			st.Phase, st.SkippedReason = aiv1.ResearchPhaseScheduled, choice.Reason
			if choice.WaitUntil.Before(next) {
				st.NextRun = &metav1.Time{Time: choice.WaitUntil}
			}
		}
		logger.Info("no research agent has usage left, waiting", "engine", choice.WaitEngine, "until", choice.WaitUntil.UTC(), "reason", choice.Reason)
		requeueAt(choice.WaitUntil)
		return result, r.patchStatus(ctx, &res, orig)
	}

	// Start a run: status.run first, so the runner finds its agent there,
	// then the pod.
	start := metav1.NewTime(now)
	agent := *plan.choice.Agent
	st.Run = &aiv1.ResearchRunStatus{ID: uuid.NewString(), StartedAt: &start,
		Engine: agent.Engine, Model: agent.Model, Effort: agent.Effort, Reason: plan.choice.Reason}
	st.Phase = aiv1.ResearchPhaseRunning
	st.SkippedReason, st.Waiting = "", nil
	setResearchCondition(&res, ConditionRunning, true, "RunInProgress", fmt.Sprintf("run %s on %s/%s", st.Run.ID, st.Run.Engine, st.Run.Model))
	if err := r.patchStatus(ctx, &res, orig); err != nil {
		return ctrl.Result{}, err
	}
	pod := buildResearchPod(&res, plan.spec, st.Run.ID, r.DBConn)
	if err := controllerutil.SetControllerReference(&res, pod, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("starting research run", "run", st.Run.ID, "pod", pod.Name, "engine", st.Run.Engine, "model", st.Run.Model)
	if err := r.Create(ctx, pod); err != nil && !apierrors.IsAlreadyExists(err) {
		// Recorded as failed, so a one-off does not start again and again.
		logger.Error(err, "creating the research pod", "pod", pod.Name)
		if ferr := r.finishRun(ctx, &res, res.DeepCopy(), nil, "creating the pod: "+err.Error(), now); ferr != nil {
			return ctrl.Result{}, ferr
		}
		return result, nil
	}
	requeueAt(start.Add(res.Spec.Timeout() + researchDeadlineSlack + researchRunGrace))
	return result, nil
}

// researchPlan is what a valid research runs with.
type researchPlan struct {
	spec aiv1.AgentProjectSpec
	// schedule is nil for one-offs.
	schedule *research.Schedule
	// choice is the agent a run started now would get.
	choice research.Choice
}

// validate returns what the research runs with, or the reason and message of
// Valid=False.
func (r *AgentResearchReconciler) validate(ctx context.Context, res *aiv1.AgentResearch, now time.Time) (plan researchPlan, reason, msg string, err error) {
	var project aiv1.AgentProject
	if err := r.Get(ctx, types.NamespacedName{Namespace: res.Namespace, Name: res.Spec.ProjectRef}, &project); err != nil {
		if apierrors.IsNotFound(err) {
			return plan, "ProjectNotFound", fmt.Sprintf("AgentProject %q not found", res.Spec.ProjectRef), nil
		}
		return plan, "", "", err
	}
	plan.spec = project.Spec.WithDefaults()
	if plan.spec.Agents.Image == "" {
		plan.spec.Agents.Image = r.DefaultAgentImage
	}
	if plan.spec.Agents.LogLevel == "" {
		plan.spec.Agents.LogLevel = r.DefaultAgentLogLevel
	}

	if !res.OneOff() {
		s, err := research.ParseSchedule(res.Spec.Schedule, res.Spec.TimeZone)
		if err != nil {
			return plan, "InvalidSchedule", err.Error(), nil
		}
		plan.schedule = &s
	}

	// A fixed agent is the only candidate: the usage guard applies to it too.
	var candidates []aiv1.ResearchAgent
	if a := res.Spec.Agent; a != nil {
		if _, err := plan.spec.ResolveResearchAgent(*a); err != nil {
			return plan, "InvalidAgent", "spec.agent: " + err.Error(), nil
		}
		candidates = []aiv1.ResearchAgent{*a}
	} else {
		depth := res.Spec.Depth
		if depth == "" {
			depth = aiv1.ResearchDepthQuick
		}
		if p := plan.spec.Research; p != nil {
			candidates = candidatesFor(p.Candidates, depth)
		}
		if len(candidates) == 0 {
			candidates = candidatesFor(r.Candidates, depth)
		}
		if len(candidates) == 0 {
			return plan, "NoAgent", fmt.Sprintf("no spec.agent and no %s research candidates", depth), nil
		}
	}
	// The usage the run of this research reported counts at once, before
	// the project collects it: a run that ended limited is retried on
	// another candidate or after the limit.
	usage := project.DeepCopy()
	own := []aiv1.AgentResearch{*res}
	addResearchUsage(&usage.Status, project.Name, own)
	for _, l := range aiv1.ProjectUsageLimits(nil, own, project.Name, now) {
		usage.Status.UsageLimits = aiv1.MergeUsageLimit(usage.Status.UsageLimits, l)
	}
	plan.choice = research.Choose(usage, candidates, r.maxUsagePercent(), now)
	if plan.choice.Agent == nil && plan.choice.WaitUntil.IsZero() {
		return plan, "NoAgent", plan.choice.Reason, nil
	}

	if plan.spec.Agents.Image == "" {
		return plan, "NoAgentImage", "set agents.image on the AgentProject or --default-agent-image on the operator", nil
	}

	if plan.spec.Research == nil || plan.spec.Research.WriterSecret == nil {
		return plan, ReasonWriterSecretMissing, fmt.Sprintf("the AgentProject %q has no spec.research.writerSecret", res.Spec.ProjectRef), nil
	}
	login := plan.spec.Research.WriterSecret.WithDefaults()
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: res.Namespace, Name: login.Name}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return plan, ReasonWriterSecretMissing, fmt.Sprintf("the writer Secret %q does not exist", login.Name), nil
		}
		return plan, "", "", err
	}
	for _, key := range []string{login.UsernameKey, login.PasswordKey} {
		if len(secret.Data[key]) == 0 {
			return plan, ReasonWriterSecretMissing, fmt.Sprintf("the writer Secret %q has no key %q", login.Name, key), nil
		}
	}
	return plan, "", "", nil
}

func candidatesFor(c aiv1.ResearchCandidates, depth aiv1.ResearchDepth) []aiv1.ResearchAgent {
	if depth == aiv1.ResearchDepthDeep {
		return c.Deep
	}
	return c.Quick
}

// researchDue reports whether a run is due at now and, for a schedule, the
// scheduled time after it. A one-off is due once, and again after a run that
// ended limited. A scheduled research is due at status.nextRun; without one
// (new, or its spec changed) at the first scheduled time after its last run
// or creation, missed ones run once.
func researchDue(res *aiv1.AgentResearch, s *research.Schedule, now time.Time) (bool, time.Time) {
	st := res.Status
	if s == nil {
		return st.Run == nil || st.Run.FinishedAt != nil && st.Run.Outcome == aiv1.ResearchOutcomeLimited, time.Time{}
	}
	if st.NextRun != nil && st.ObservedGeneration == res.Generation {
		if now.Before(st.NextRun.Time) {
			return false, st.NextRun.Time
		}
		return true, s.Next(now)
	}
	since := res.CreationTimestamp.Time
	for _, t := range []*metav1.Time{st.LastRun, runStart(st.Run)} {
		if t != nil && t.After(since) {
			since = t.Time
		}
	}
	return research.NextRun(*s, since, now)
}

func runStart(run *aiv1.ResearchRunStatus) *metav1.Time {
	if run == nil {
		return nil
	}
	return run.StartedAt
}

// finishRun records the run's outcome in the store and the status, then
// deletes its pod. Without the runner's report the run failed with why.
func (r *AgentResearchReconciler) finishRun(ctx context.Context, res, orig *aiv1.AgentResearch, pod *corev1.Pod, why string, now time.Time) error {
	st := &res.Status
	run := st.Run
	if run.Outcome == "" {
		run.Outcome, run.Message = aiv1.ResearchOutcomeFailed, why
	}
	if run.StartedAt == nil {
		run.StartedAt = &metav1.Time{Time: now}
	}
	rec := store.Run{ID: run.ID, Namespace: res.Namespace, Research: res.Name, StartedAt: run.StartedAt.Time,
		FinishedAt: &now, Status: string(run.Outcome), Engine: string(run.Engine), Model: run.Model,
		Effort: run.Effort, ChoiceReason: run.Reason, ExpiresAt: run.StartedAt.Add(res.Spec.RetentionOrDefault())}
	if run.Outcome != aiv1.ResearchOutcomeSucceeded {
		rec.Error = run.Message
	}
	if run.CostUSD != "" {
		rec.CostUSD = aiv1.CostOf(run.CostUSD)
	}
	if err := r.DB.FinishRunOrInsert(ctx, rec); err != nil {
		return fmt.Errorf("recording the outcome of run %s: %w", run.ID, err)
	}
	if err := r.insertCalls(ctx, res, run, now); err != nil {
		return fmt.Errorf("recording the agent calls of run %s: %w", run.ID, err)
	}

	run.FinishedAt = &metav1.Time{Time: now}
	st.LastRun = run.StartedAt.DeepCopy()
	st.LastEngine, st.LastModel = run.Engine, run.Model
	ok := run.Outcome == aiv1.ResearchOutcomeSucceeded || run.Outcome == aiv1.ResearchOutcomeDegraded
	if ok {
		st.LastResultID = run.ID
	}
	switch {
	case !res.OneOff() && run.Outcome == aiv1.ResearchOutcomeLimited:
		// Due again at once: another candidate, or a skip until the reset.
		st.Phase, st.NextRun = aiv1.ResearchPhaseScheduled, &metav1.Time{Time: now}
	case !res.OneOff():
		st.Phase = aiv1.ResearchPhaseScheduled
	case ok:
		st.Phase = aiv1.ResearchPhaseSucceeded
	default:
		st.Phase = aiv1.ResearchPhaseFailed
	}
	setResearchCondition(res, ConditionRunning, false, "Idle", "no run in progress")
	if err := r.patchStatus(ctx, res, orig); err != nil {
		return err
	}
	log.FromContext(ctx).Info("research run finished", "run", run.ID, "outcome", run.Outcome, "message", run.Message)
	if pod != nil {
		if err := r.Delete(ctx, pod); client.IgnoreNotFound(err) != nil {
			return err
		}
	}
	return nil
}

// runPod returns the pod of the research's current run, nil if it is gone.
func (r *AgentResearchReconciler) runPod(ctx context.Context, res *aiv1.AgentResearch) (*corev1.Pod, error) {
	key := types.NamespacedName{Namespace: res.Namespace, Name: naming.ResearchPod(res.Name, res.Status.Run.ID)}
	var pod corev1.Pod
	err := r.Get(ctx, key, &pod)
	if apierrors.IsNotFound(err) && r.APIReader != nil {
		err = r.APIReader.Get(ctx, key, &pod)
	}
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !metav1.IsControlledBy(&pod, res) {
		return nil, fmt.Errorf("pod %s exists but is not owned by research %s", pod.Name, res.Name)
	}
	return &pod, nil
}

func podEnded(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
}

// runEnd says how a run ended without the runner's report.
func runEnd(pod *corev1.Pod) string {
	const prefix = "no report from the runner: "
	switch {
	case pod == nil:
		return prefix + "the pod is gone"
	case !podEnded(pod):
		return prefix + fmt.Sprintf("the pod did not end in time (%s)", pod.Status.Phase)
	case pod.Status.Reason != "":
		return prefix + fmt.Sprintf("pod %s: %s", pod.Status.Reason, pod.Status.Message)
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if t := cs.State.Terminated; cs.Name == RunnerContainer && t != nil {
			return prefix + fmt.Sprintf("the runner exited with code %d (%s)", t.ExitCode, t.Reason)
		}
	}
	return prefix + fmt.Sprintf("the pod %s", pod.Status.Phase)
}

func setResearchCondition(res *aiv1.AgentResearch, typ string, status bool, reason, msg string) {
	meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{Type: typ, Status: boolCondition(status),
		Reason: reason, Message: msg, ObservedGeneration: res.Generation})
}

func (r *AgentResearchReconciler) maxUsagePercent() int32 {
	if r.MaxUsagePercent > 0 {
		return r.MaxUsagePercent
	}
	return aiv1.DefaultResearchMaxUsagePercent
}

func (r *AgentResearchReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// patchStatus fails on a conflict, so a report the runner just wrote is not
// overwritten from a stale copy.
func (r *AgentResearchReconciler) patchStatus(ctx context.Context, res, orig *aiv1.AgentResearch) error {
	return client.IgnoreNotFound(r.Status().Patch(ctx, res, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})))
}

// researchesOfProject maps an AgentProject to the researches that reference
// it.
func (r *AgentResearchReconciler) researchesOfProject(ctx context.Context, obj client.Object) []reconcile.Request {
	var list aiv1.AgentResearchList
	if err := r.List(ctx, &list, client.InNamespace(obj.GetNamespace())); err != nil {
		log.FromContext(ctx).Error(err, "listing researches of project", "project", obj.GetName())
		return nil
	}
	var reqs []reconcile.Request
	for _, res := range list.Items {
		if res.Spec.ProjectRef == obj.GetName() {
			reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: res.Namespace, Name: res.Name}})
		}
	}
	return reqs
}

// SetupWithManager registers the controller.
func (r *AgentResearchReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1.AgentResearch{}).
		Owns(&corev1.Pod{}).
		Watches(&aiv1.AgentProject{}, handler.EnqueueRequestsFromMapFunc(r.researchesOfProject),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("agentresearch").
		Complete(r)
}

// insertCalls writes the run's agent calls to the history under the
// research's project, keyed by research UID, run ID and seq, so a finishRun
// that is retried writes none twice.
func (r *AgentResearchReconciler) insertCalls(ctx context.Context, res *aiv1.AgentResearch, run *aiv1.ResearchRunStatus, now time.Time) error {
	if len(run.Calls) == 0 {
		return nil
	}
	retention := r.HistoryRetention
	if retention <= 0 {
		retention = history.DefaultRetention
	}
	calls := make([]store.Call, 0, len(run.Calls))
	for _, c := range run.Calls {
		call := history.StoreCall(c)
		call.Key = fmt.Sprintf("research/%s/%s/%d", res.UID, run.ID, c.Seq)
		call.Namespace, call.Project, call.Task, call.Run = res.Namespace, res.Spec.ProjectRef, res.Name, 0
		if call.At.IsZero() {
			call.At = now
		}
		call.ExpiresAt = call.At.Add(retention)
		history.Estimate(&call, r.Pricing)
		calls = append(calls, call)
	}
	_, err := r.DB.InsertCalls(ctx, calls)
	return err
}
