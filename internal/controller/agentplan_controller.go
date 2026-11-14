package controller

import (
	"context"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// AgentPlanReconciler runs a plan's pod while one of its messages waits for
// a reply: Starting until the pod's containers are ready, then Ready (the
// runner sets Working during a turn). A pod that ended is deleted, so the
// next reconcile starts a new one; MaxPlanPodFailures failed pods in a row
// fail the plan.
type AgentPlanReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Config PlanConfig
	// DefaultAgentImage and DefaultAgentLogLevel are used for projects that
	// do not set agents.image and agents.logLevel.
	DefaultAgentImage    string
	DefaultAgentLogLevel aiv1.LogLevel

	wakes chan event.GenericEvent
}

// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentplans,verbs=get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentplans/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;delete

func (r *AgentPlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var plan aiv1.AgentPlan
	if err := r.Get(ctx, req.NamespacedName, &plan); err != nil || !plan.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	orig, st := plan.DeepCopy(), &plan.Status
	var project aiv1.AgentProject
	if err := r.Get(ctx, types.NamespacedName{Namespace: plan.Namespace, Name: plan.Spec.ProjectRef}, &project); apierrors.IsNotFound(err) {
		st.Phase, st.Message = aiv1.AgentPlanFailed, "project not found"
		return ctrl.Result{}, r.patchStatus(ctx, &plan, orig)
	} else if err != nil {
		return ctrl.Result{}, err
	}
	// The project owns its plans, so deleting it deletes them.
	if err := controllerutil.SetOwnerReference(&project, &plan, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	if !equality.Semantic.DeepEqual(plan.OwnerReferences, orig.OwnerReferences) {
		if err := r.Update(ctx, &plan); err != nil {
			return ctrl.Result{}, err
		}
		orig = plan.DeepCopy()
	}
	if !plan.Active() || !planPending(&plan) {
		return ctrl.Result{}, nil
	}

	spec := project.Spec.WithDefaults()
	if spec.Agents.Image == "" {
		spec.Agents.Image = r.DefaultAgentImage
	}
	if spec.Agents.LogLevel == "" {
		spec.Agents.LogLevel = r.DefaultAgentLogLevel
	}
	if err := r.getOrCreate(ctx, buildPlanClaim(&plan, spec)); err != nil {
		return ctrl.Result{}, err
	}
	pod := buildPlanPod(&plan, spec, r.Config)
	if err := r.getOrCreate(ctx, pod); err != nil {
		return ctrl.Result{}, err
	}
	st.PodName = pod.Name
	switch {
	case podEnded(pod):
		if err := client.IgnoreNotFound(r.Delete(ctx, pod)); err != nil {
			return ctrl.Result{}, err
		}
		if pod.Status.Phase == corev1.PodFailed {
			st.PodFailures++
		}
		st.Phase, st.Message = aiv1.AgentPlanStarting, podEnd(pod)
		if st.PodFailures >= aiv1.MaxPlanPodFailures {
			st.Phase = aiv1.AgentPlanFailed
		}
	case st.Phase == aiv1.AgentPlanWorking: // the runner's turn
	case containersReady(pod):
		st.Phase, st.Message, st.PodFailures = aiv1.AgentPlanReady, "", 0
	default:
		st.Phase = aiv1.AgentPlanStarting
	}
	return ctrl.Result{}, r.patchStatus(ctx, &plan, orig)
}

// planPending reports whether a message of plan waits for a reply.
func planPending(plan *aiv1.AgentPlan) bool {
	var replies []aiv1.ChatReply
	if plan.Status.Chat != nil {
		replies = plan.Status.Chat.Replies
	}
	return slices.ContainsFunc(plan.Spec.ChatMessages, func(m aiv1.ChatMessage) bool {
		return m.Kind == "message" && !slices.ContainsFunc(replies, func(r aiv1.ChatReply) bool { return r.MessageID == m.ID })
	})
}

func containersReady(pod *corev1.Pod) bool {
	return slices.ContainsFunc(pod.Status.Conditions, func(c corev1.PodCondition) bool {
		return c.Type == corev1.ContainersReady && c.Status == corev1.ConditionTrue
	})
}

// getOrCreate reads obj by its name, or creates it as built when it is not
// there.
func (r *AgentPlanReconciler) getOrCreate(ctx context.Context, obj client.Object) error {
	if err := r.Get(ctx, client.ObjectKeyFromObject(obj), obj); !apierrors.IsNotFound(err) {
		return err
	}
	return r.Create(ctx, obj)
}

// patchStatus fails on a conflict, so a phase the runner just wrote is not
// overwritten from a stale copy.
func (r *AgentPlanReconciler) patchStatus(ctx context.Context, plan, orig *aiv1.AgentPlan) error {
	return client.IgnoreNotFound(r.Status().Patch(ctx, plan, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})))
}

// Wake asks for a reconcile of the plan n, e.g. after a message was handed
// to it. It never blocks; a wake while the queue is full is dropped.
func (r *AgentPlanReconciler) Wake(n types.NamespacedName) {
	select {
	case r.wakes <- event.GenericEvent{Object: &aiv1.AgentPlan{ObjectMeta: metav1.ObjectMeta{Namespace: n.Namespace, Name: n.Name}}}:
	default:
	}
}

// SetupWithManager registers the controller.
func (r *AgentPlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.wakes = make(chan event.GenericEvent, 64)
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1.AgentPlan{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		WatchesRawSource(source.Channel(r.wakes, &handler.EnqueueRequestForObject{})).
		Named("agentplan").
		Complete(r)
}
