package controller

import (
	"cmp"
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

const (
	// planProjectWait is how long after its creation a plan of a project
	// set waits for the set to create its project before it fails.
	planProjectWait = 10 * time.Minute
	// planProjectRetry is how often a waiting plan looks for its project.
	planProjectRetry = 10 * time.Second
	// planWaitMessage is the status message of a plan waiting for its set
	// project.
	planWaitMessage = "Waiting for the project set to create the project"
)

// waitForSetProject handles a plan whose project is missing. A plan without
// the project-set label fails at once. A labelled plan is Starting for
// planProjectWait after its creation, retried every planProjectRetry, and
// then fails with the set's reason. Either way it is deleted Retention after
// its last activity.
func (r *AgentPlanReconciler) waitForSetProject(ctx context.Context, plan, orig *aiv1.AgentPlan) (ctrl.Result, error) {
	st, set := &plan.Status, plan.Labels[aiv1.LabelProjectSet]
	if set == "" {
		st.Phase, st.Message = aiv1.AgentPlanFailed, "project not found"
		return ctrl.Result{}, r.patchStatus(ctx, plan, orig)
	}
	now, last := r.now(), lastActivity(plan)
	if now.Sub(last) >= r.Config.Retention {
		return ctrl.Result{}, client.IgnoreNotFound(r.Delete(ctx, plan))
	}
	res := ctrl.Result{RequeueAfter: last.Add(r.Config.Retention).Sub(now)}
	switch {
	case st.Phase == aiv1.AgentPlanFailed:
	case now.Sub(plan.CreationTimestamp.Time) < planProjectWait:
		st.Phase, st.Message = aiv1.AgentPlanStarting, planWaitMessage
		res.RequeueAfter = min(planProjectRetry, res.RequeueAfter)
	default:
		reason, err := r.setProjectReason(ctx, plan.Namespace, set, plan.Spec.ProjectRef)
		if err != nil {
			return ctrl.Result{}, err
		}
		st.Phase, st.Message = aiv1.AgentPlanFailed, fmt.Sprintf("The project set did not create %s: %s", plan.Spec.ProjectRef, reason)
	}
	return res, r.patchStatus(ctx, plan, orig)
}

// setProjectReason says why the set name did not create the child project:
// the message of its failing Discovered or Ready condition, else the reason
// of the set's entry for child.
func (r *AgentPlanReconciler) setProjectReason(ctx context.Context, namespace, name, child string) (string, error) {
	var set aiv1.AgentProjectSet
	if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &set); apierrors.IsNotFound(err) {
		return "set " + name + " not found", nil
	} else if err != nil {
		return "", err
	}
	for _, t := range []string{aiv1.ConditionDiscovered, aiv1.ConditionReady} {
		if c := meta.FindStatusCondition(set.Status.Conditions, t); c != nil && c.Status == metav1.ConditionFalse {
			return cmp.Or(c.Message, c.Reason), nil
		}
	}
	for _, e := range set.Status.Projects {
		if e.Child == child || aiv1.ProjectSetChildName(set.Spec.GitLab.Group, e.Path) == child {
			return cmp.Or(e.Reason, "the entry is "+string(e.State)), nil
		}
	}
	return "the set has no entry for it", nil
}
