package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
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
	"github.com/jonathanwiemers/ai-operator/internal/naming"
)

// queueRetry is how often a queued task re-checks for free capacity.
const queueRetry = 30 * time.Second

// failedRunIdleTimeout is the minimum time a pod stays after a failed run, so
// its runner and agent logs can still be read.
const failedRunIdleTimeout = time.Hour

// AgentTaskReconciler manages the pod and workspace of an AgentTask:
//
//	run requested           -> workspace + pod exist             (Running / Queued)
//	no run pending          -> pod kept for follow-up requests    (Idle)
//	idle > IdleTimeout      -> pod deleted, workspace kept        (Hibernated)
//	idle > RetentionPeriod  -> workspace deleted                  (Archived)
//
// A new run request at any point brings the pod (and if needed a fresh
// workspace) back. Deleting the task garbage-collects everything.
type AgentTaskReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Now    func() time.Time
	// DefaultAgentImage is used for projects that do not set agents.image.
	DefaultAgentImage string
	// APIReader reads without the cache; used to re-check a task before its
	// pod is stopped. Optional.
	APIReader client.Reader
	// DefaultAgentLogLevel is used for projects that do not set
	// agents.logLevel.
	DefaultAgentLogLevel aiv1.LogLevel
}

// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agenttasks,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agenttasks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;delete

func (r *AgentTaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var task aiv1.AgentTask
	if err := r.Get(ctx, req.NamespacedName, &task); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !task.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	orig := task.DeepCopy()

	var project aiv1.AgentProject
	if err := r.Get(ctx, types.NamespacedName{Namespace: task.Namespace, Name: task.Spec.ProjectRef}, &project); err != nil {
		if apierrors.IsNotFound(err) {
			task.Status.Message = fmt.Sprintf("AgentProject %q not found", task.Spec.ProjectRef)
			return ctrl.Result{RequeueAfter: time.Minute}, r.patchStatus(ctx, &task, orig)
		}
		return ctrl.Result{}, err
	}
	spec := project.Spec.WithDefaults()
	if spec.Agents.Image == "" {
		spec.Agents.Image = r.DefaultAgentImage
	}
	if spec.Agents.LogLevel == "" {
		spec.Agents.LogLevel = r.DefaultAgentLogLevel
	}
	lc := spec.Lifecycle

	pod, err := r.getPod(ctx, &task)
	if err != nil {
		return ctrl.Result{}, err
	}
	pvc, err := r.getClaim(ctx, &task)
	if err != nil {
		return ctrl.Result{}, err
	}
	if t := agentTermination(pod); t != nil {
		if task.Status.AgentTermination == nil || !equality.Semantic.DeepEqual(*task.Status.AgentTermination, *t) {
			logger.Info("agent container stopped", "pod", pod.Name, "reason", t.Reason, "exitCode", t.ExitCode, "restarts", t.Restarts, "memoryLimit", t.MemoryLimit)
			// Out of memory: the task's next pod gets more (agents.maxMemory).
			if q, err := resource.ParseQuantity(t.MemoryLimit); t.OOMKilled() && err == nil {
				if next, ok := spec.Agents.RaisedMemory(q); ok {
					logger.Info("raising the agent container's memory limit", "from", t.MemoryLimit, "to", next.String())
					task.Status.AgentMemoryLimit = next.String()
				}
			}
		}
		task.Status.AgentTermination = t
	}

	now := r.now()
	lastActivity := task.CreationTimestamp.Time
	if t := task.Status.Run.LastActivityTime; t != nil && t.After(lastActivity) {
		lastActivity = t.Time
	}
	idleFor := now.Sub(lastActivity)
	pending := task.RunPending()
	// The runner bumps RunCount when a run finishes, so a run in progress is
	// never cut off by the limit.
	inProgress := task.Status.Run.ActiveRunRequest > task.Status.Run.ObservedRunRequest
	blocked := pending && !inProgress && task.Status.Run.RunCount >= lc.MaxRunsPerTask
	// A run deferred because of a usage limit waits until its retry time. In
	// the meantime the pod follows the normal idle / hibernate rules and the
	// task does not take one of the project's run slots.
	var waitFor time.Duration
	if d := task.Status.Run.DeferredUntil; pending && !inProgress && d != nil && d.After(now) {
		waitFor = d.Sub(now)
	}
	waiting := waitFor > 0

	// A pod that has terminated for good (evicted, node lost) is replaced.
	if pod != nil && (pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded) {
		logger.Info("removing terminated agent pod", "pod", pod.Name, "phase", pod.Status.Phase)
		if err := r.Delete(ctx, pod); client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
		pod = nil
	}

	// A pod started with other agent resources (raised after an OOM kill) is
	// replaced between runs, so the next run gets them.
	if pod != nil && !inProgress && podOutdated(pod, &task, spec) {
		if busy, err := r.runInProgress(ctx, &task); err != nil {
			return ctrl.Result{}, err
		} else if !busy {
			logger.Info("replacing agent pod, its resources changed", "pod", pod.Name)
			if err := r.Delete(ctx, pod); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
			pod = nil
		}
	}

	var result ctrl.Result
	switch {
	case pending && !blocked && !waiting && !task.Spec.Suspend:
		if pvc == nil {
			pvc = buildWorkspaceClaim(&task, spec)
			if err := controllerutil.SetControllerReference(&task, pvc, r.Scheme); err != nil {
				return ctrl.Result{}, err
			}
			logger.Info("creating workspace", "pvc", pvc.Name)
			if err := r.Create(ctx, pvc); err != nil && !apierrors.IsAlreadyExists(err) {
				return ctrl.Result{}, err
			}
		}
		if pod == nil && spec.Agents.Image == "" {
			task.Status.Phase = aiv1.TaskPhaseQueued
			task.Status.Message = "no agent image: set agents.image on the AgentProject or --default-agent-image on the operator"
			result.RequeueAfter = time.Minute
			break
		}
		if pod == nil {
			running, err := r.runningTasks(ctx, &task)
			if err != nil {
				return ctrl.Result{}, err
			}
			if running >= int(spec.MaxConcurrentRuns) {
				task.Status.Phase = aiv1.TaskPhaseQueued
				task.Status.Message = fmt.Sprintf("waiting for capacity (%d/%d runs in flight)", running, spec.MaxConcurrentRuns)
				result.RequeueAfter = queueRetry
				break
			}
			pod = buildPod(&task, spec)
			if err := controllerutil.SetControllerReference(&task, pod, r.Scheme); err != nil {
				return ctrl.Result{}, err
			}
			logger.Info("starting agent pod", "pod", pod.Name)
			if err := r.Create(ctx, pod); err != nil && !apierrors.IsAlreadyExists(err) {
				return ctrl.Result{}, err
			}
		}
		task.Status.Phase = aiv1.TaskPhaseRunning
		task.Status.Message = fmt.Sprintf("run %d requested", task.Spec.RunRequest)

	case pod != nil:
		// After a failed run the pod stays longer, so its logs can be read.
		idleTimeout := lc.IdleTimeout.Duration
		if task.Status.Run.LastResult == aiv1.RunResultFailed && idleTimeout < failedRunIdleTimeout {
			idleTimeout = failedRunIdleTimeout
		}
		if !blocked && !task.Spec.Suspend && idleFor >= idleTimeout {
			// The cache can lag behind a run request that came in just now,
			// and the runner may already be working on it: check the API
			// server before stopping the pod.
			if fresh, err := r.freshRunPending(ctx, &task); err != nil {
				return ctrl.Result{}, err
			} else if fresh {
				return ctrl.Result{RequeueAfter: time.Second}, nil
			}
		}
		if blocked || task.Spec.Suspend || idleFor >= idleTimeout {
			logger.Info("hibernating agent pod", "pod", pod.Name, "idleFor", idleFor.Round(time.Second))
			if err := r.Delete(ctx, pod); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
			pod = nil
			if inProgress {
				// Suspended mid-run on purpose: the run starts again when
				// resumed, but not as an interruption.
				task.Status.Run.ActiveRunRequest = task.Status.Run.ObservedRunRequest
			}
			task.Status.Phase = aiv1.TaskPhaseHibernated
			task.Status.Message = "pod stopped, workspace kept"
			result.RequeueAfter = remaining(lc.RetentionPeriod.Duration, idleFor)
		} else {
			task.Status.Phase = aiv1.TaskPhaseIdle
			task.Status.Message = "waiting for input"
			result.RequeueAfter = remaining(idleTimeout, idleFor)
		}

	case pvc != nil:
		if idleFor >= lc.RetentionPeriod.Duration {
			logger.Info("archiving workspace", "pvc", pvc.Name, "idleFor", idleFor.Round(time.Second))
			if err := r.Delete(ctx, pvc); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
			pvc = nil
			task.Status.Phase = aiv1.TaskPhaseArchived
			task.Status.Message = "workspace deleted; the next run starts from a fresh clone"
		} else {
			task.Status.Phase = aiv1.TaskPhaseHibernated
			task.Status.Message = "pod stopped, workspace kept"
			result.RequeueAfter = remaining(lc.RetentionPeriod.Duration, idleFor)
		}

	default:
		if task.Status.Run.RunCount > 0 {
			task.Status.Phase = aiv1.TaskPhaseArchived
		} else {
			task.Status.Phase = aiv1.TaskPhasePending
		}
	}
	// Only the runner sets the current step; a run cut off together with its
	// pod (suspend, eviction) would leave it behind.
	if pod == nil {
		task.Status.Run.CurrentStep = ""
	}

	if waiting && !blocked && !task.Spec.Suspend {
		task.Status.Phase = aiv1.TaskPhaseWaiting
		reason := fmt.Sprintf("%s usage limit", task.Status.Run.LimitedEngine)
		if task.Status.Run.LimitedEngine == "" {
			reason = "agent ran out of memory, more memory"
		}
		task.Status.Message = fmt.Sprintf("%s; retrying at %s", reason, task.Status.Run.DeferredUntil.UTC().Format(time.RFC3339))
		if retry := waitFor + time.Second; result.RequeueAfter == 0 || retry < result.RequeueAfter {
			result.RequeueAfter = retry
		}
	}
	if blocked {
		task.Status.Phase = aiv1.TaskPhaseBlocked
		task.Status.Message = fmt.Sprintf("reached maxRunsPerTask (%d); raise the limit or reset the task", lc.MaxRunsPerTask)
	}
	meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
		Type:    "Blocked",
		Status:  boolCondition(blocked),
		Reason:  map[bool]string{true: "MaxRunsReached", false: "WithinLimits"}[blocked],
		Message: fmt.Sprintf("%d of %d runs used", task.Status.Run.RunCount, lc.MaxRunsPerTask),
	})

	if pod != nil && pod.Status.Phase == corev1.PodPending {
		// A raised memory request may fit no node: say so instead of waiting
		// silently.
		for _, c := range pod.Status.Conditions {
			if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Reason == corev1.PodReasonUnschedulable {
				task.Status.Message = "agent pod cannot be scheduled: " + c.Message
			}
		}
	}
	task.Status.PodName = ""
	if pod != nil {
		task.Status.PodName = pod.Name
	}
	task.Status.WorkspaceClaimName = ""
	if pvc != nil {
		task.Status.WorkspaceClaimName = pvc.Name
	}
	return result, r.patchStatus(ctx, &task, orig)
}

func (r *AgentTaskReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// remaining returns how long until limit is reached, with a small floor so the
// controller never spins.
func remaining(limit, elapsed time.Duration) time.Duration {
	d := limit - elapsed
	if d < 5*time.Second {
		d = 5 * time.Second
	}
	return d + time.Second
}

func boolCondition(b bool) metav1.ConditionStatus {
	if b {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}

func (r *AgentTaskReconciler) patchStatus(ctx context.Context, task, orig *aiv1.AgentTask) error {
	return client.IgnoreNotFound(r.Status().Patch(ctx, task, client.MergeFrom(orig)))
}

func (r *AgentTaskReconciler) getPod(ctx context.Context, task *aiv1.AgentTask) (*corev1.Pod, error) {
	var pod corev1.Pod
	err := r.Get(ctx, types.NamespacedName{Namespace: task.Namespace, Name: task.Name}, &pod)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !metav1.IsControlledBy(&pod, task) {
		return nil, fmt.Errorf("pod %s exists but is not owned by task %s", pod.Name, task.Name)
	}
	if !pod.DeletionTimestamp.IsZero() {
		return nil, nil
	}
	return &pod, nil
}

func (r *AgentTaskReconciler) getClaim(ctx context.Context, task *aiv1.AgentTask) (*corev1.PersistentVolumeClaim, error) {
	var pvc corev1.PersistentVolumeClaim
	err := r.Get(ctx, types.NamespacedName{Namespace: task.Namespace, Name: naming.Workspace(task.Name)}, &pvc)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !pvc.DeletionTimestamp.IsZero() {
		return nil, nil
	}
	return &pvc, nil
}

// runningTasks counts the other tasks of the same project that currently have
// a run in flight.
// freshRunPending reports whether the task, read from the API server, has a
// run pending or in progress.
func (r *AgentTaskReconciler) freshRunPending(ctx context.Context, task *aiv1.AgentTask) (bool, error) {
	if r.APIReader == nil {
		return false, nil
	}
	var fresh aiv1.AgentTask
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(task), &fresh); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	return fresh.RunPending() || fresh.Status.Run.ActiveRunRequest > fresh.Status.Run.ObservedRunRequest, nil
}

// runInProgress checks with the API server whether the runner is in a run;
// the cache can lag behind the runner starting one.
func (r *AgentTaskReconciler) runInProgress(ctx context.Context, task *aiv1.AgentTask) (bool, error) {
	if r.APIReader == nil {
		return task.Status.Run.ActiveRunRequest > task.Status.Run.ObservedRunRequest, nil
	}
	var fresh aiv1.AgentTask
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(task), &fresh); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	return fresh.Status.Run.ActiveRunRequest > fresh.Status.Run.ObservedRunRequest, nil
}

func (r *AgentTaskReconciler) runningTasks(ctx context.Context, task *aiv1.AgentTask) (int, error) {
	var tasks aiv1.AgentTaskList
	if err := r.List(ctx, &tasks, client.InNamespace(task.Namespace), client.MatchingLabels{LabelProject: task.Spec.ProjectRef}); err != nil {
		return 0, err
	}
	n := 0
	for i := range tasks.Items {
		t := &tasks.Items[i]
		if t.Name != task.Name && t.Status.Phase == aiv1.TaskPhaseRunning {
			n++
		}
	}
	return n, nil
}

// tasksOfProject maps an AgentProject to its tasks, so changes to the project
// (limits, suspend, images) take effect without waiting for a task event.
func (r *AgentTaskReconciler) tasksOfProject(ctx context.Context, obj client.Object) []reconcile.Request {
	var tasks aiv1.AgentTaskList
	if err := r.List(ctx, &tasks, client.InNamespace(obj.GetNamespace()), client.MatchingLabels{LabelProject: obj.GetName()}); err != nil {
		log.FromContext(ctx).Error(err, "listing tasks of project", "project", obj.GetName())
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(tasks.Items))
	for _, t := range tasks.Items {
		reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: t.Namespace, Name: t.Name}})
	}
	return reqs
}

// SetupWithManager registers the controller.
func (r *AgentTaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1.AgentTask{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Watches(&aiv1.AgentProject{}, handler.EnqueueRequestsFromMapFunc(r.tasksOfProject),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("agenttask").
		Complete(r)
}

// agentTermination returns how the agent container of pod last stopped, if
// it did.
func agentTermination(pod *corev1.Pod) *aiv1.ContainerTermination {
	if pod == nil {
		return nil
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name != AgentContainer {
			continue
		}
		term := cs.State.Terminated
		if term == nil {
			term = cs.LastTerminationState.Terminated
		}
		if term == nil {
			return nil
		}
		t := &aiv1.ContainerTermination{Reason: term.Reason, ExitCode: term.ExitCode, Restarts: cs.RestartCount}
		if !term.FinishedAt.IsZero() {
			t.FinishedAt = &metav1.Time{Time: term.FinishedAt.Time}
		}
		for _, c := range pod.Spec.Containers {
			if q, ok := c.Resources.Limits[corev1.ResourceMemory]; ok && c.Name == AgentContainer {
				t.MemoryLimit = q.String()
			}
		}
		return t
	}
	return nil
}
