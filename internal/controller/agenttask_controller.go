package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
)

// queueRetry is how often a queued task re-checks for free capacity.
const queueRetry = 30 * time.Second

// AgentTaskReconciler manages the pod and workspace of an AgentTask:
//
//	run requested           -> workspace + pod exist             (Running / Queued)
//	no run pending          -> pod kept warm                      (Idle)
//	idle > IdleTimeout      -> pod deleted, workspace kept        (Hibernated)
//	idle > RetentionPeriod  -> workspace deleted                  (Archived)
//
// A new run request at any point brings the pod (and if needed a fresh
// workspace) back. Deleting the task garbage-collects everything.
type AgentTaskReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Now    func() time.Time
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
	lc := spec.Lifecycle

	pod, err := r.getPod(ctx, &task)
	if err != nil {
		return ctrl.Result{}, err
	}
	pvc, err := r.getClaim(ctx, &task)
	if err != nil {
		return ctrl.Result{}, err
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

	// A pod that has terminated for good (evicted, node lost) is replaced.
	if pod != nil && (pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded) {
		logger.Info("removing terminated agent pod", "pod", pod.Name, "phase", pod.Status.Phase)
		if err := r.Delete(ctx, pod); client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
		pod = nil
	}

	var result ctrl.Result
	switch {
	case pending && !blocked && !task.Spec.Suspend:
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
		if blocked || task.Spec.Suspend || idleFor >= lc.IdleTimeout.Duration {
			logger.Info("hibernating agent pod", "pod", pod.Name, "idleFor", idleFor.Round(time.Second))
			if err := r.Delete(ctx, pod); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
			pod = nil
			task.Status.Phase = aiv1.TaskPhaseHibernated
			task.Status.Message = "pod stopped, workspace kept"
			result.RequeueAfter = remaining(lc.RetentionPeriod.Duration, idleFor)
		} else {
			task.Status.Phase = aiv1.TaskPhaseIdle
			task.Status.Message = "waiting for input"
			result.RequeueAfter = remaining(lc.IdleTimeout.Duration, idleFor)
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

// SetupWithManager registers the controller.
func (r *AgentTaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1.AgentTask{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Named("agenttask").
		Complete(r)
}
