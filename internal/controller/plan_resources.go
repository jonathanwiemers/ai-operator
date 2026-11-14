package controller

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
)

// planLabels are on the pod and PVC of a plan. app.kubernetes.io/name is
// what the agent NetworkPolicy selects.
func planLabels(plan *aiv1.AgentPlan) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "ai-agent",
		"app.kubernetes.io/managed-by": "ai-operator",
		LabelProject:                   plan.Spec.ProjectRef,
		LabelPlan:                      plan.Name,
	}
}

// planMeta names an object of plan, owned by it so deleting the plan
// deletes it.
func planMeta(plan *aiv1.AgentPlan, name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:            name,
		Namespace:       plan.Namespace,
		Labels:          planLabels(plan),
		OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(plan, aiv1.GroupVersion.WithKind("AgentPlan"))},
	}
}

// buildPlanClaim returns the plan's workspace PVC: the checkout and the
// engine session survive an idle stop on it.
func buildPlanClaim(plan *aiv1.AgentPlan, spec aiv1.AgentProjectSpec) *corev1.PersistentVolumeClaim {
	pvc := buildWorkspaceClaim(&aiv1.AgentTask{}, spec)
	pvc.ObjectMeta = planMeta(plan, naming.Workspace(plan.Name))
	return pvc
}

// buildPlanPod returns the pod of an active plan, laid out like a task's
// (buildPod): the GitLab token in the runner only, never a companion token.
// The runner picks the engine and model of the project's spec role.
func buildPlanPod(plan *aiv1.AgentPlan, spec aiv1.AgentProjectSpec, cfg PlanConfig) *corev1.Pod {
	own := []corev1.EnvVar{
		{Name: "AI_PLAN_NAME", Value: plan.Name},
		namespaceEnv("AI_PLAN_NAMESPACE"),
		{Name: "AI_PLAN_MAX_BUDGET_USD", Value: plan.BudgetUSD(cfg.MaxBudgetUSD)},
	}
	meta := planMeta(plan, plan.Name)
	meta.Annotations = map[string]string{}
	return workspacePod(meta, spec, "", []string{"ai-runner", "plan"}, own, nil, *spec.Agents.Resources.DeepCopy())
}
