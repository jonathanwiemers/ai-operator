package controller

import (
	"cmp"
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentplans,verbs=get;patch

// dispatchPlan hands the plan message m to its AgentPlan's spec and wakes
// the plan reconciler, so the pod starts without waiting for its requeue.
func (c *Chat) dispatchPlan(ctx context.Context, s *store.Store, m store.ChatMessage, fail func(string)) {
	var plan aiv1.AgentPlan
	if err := c.reader().Get(ctx, types.NamespacedName{Namespace: m.Namespace, Name: m.Plan}, &plan); apierrors.IsNotFound(err) {
		fail("plan not found")
		return
	} else if err != nil {
		c.Log.Error(err, "getting plan for chat message", "id", m.ID, "plan", m.Plan)
		return
	}
	switch {
	case plan.Spec.ProjectRef != m.Project:
		fail("plan belongs to another project")
	case !c.chatEnabled(ctx, m, fail): // failed or left queued
	case plan.Status.Phase == aiv1.AgentPlanCreated:
		fail("the milestone was already created from this plan")
	case plan.Status.Phase == aiv1.AgentPlanFailed:
		fail(cmp.Or(plan.Status.Message, "the plan failed"))
	case m.Kind == "command":
		fail("commands are not supported on a plan")
	case m.Kind == "message" && aiv1.BudgetLeft(plan.BudgetUSD(c.Plan.MaxBudgetUSD), plan.Status.SpendUSD, 0) <= 0:
		fail("the plan's budget is used; raise it on the Desk")
	case c.deliverChat(ctx, s, m, &plan, &plan.Spec.ChatMessages) && c.WakePlan != nil:
		c.WakePlan(types.NamespacedName{Namespace: m.Namespace, Name: m.Plan})
	}
}

// chatList reads the target ("task" or "plan") of m and the chat messages
// on its spec; a removed target has none.
func (c *Chat) chatList(ctx context.Context, m store.ChatMessage) (string, []aiv1.ChatMessage, error) {
	key := types.NamespacedName{Namespace: m.Namespace, Name: m.Task}
	if m.Plan != "" {
		key.Name = m.Plan
		var plan aiv1.AgentPlan
		err := c.reader().Get(ctx, key, &plan)
		return "plan", plan.Spec.ChatMessages, client.IgnoreNotFound(err)
	}
	var task aiv1.AgentTask
	err := c.reader().Get(ctx, key, &task)
	return "task", task.Spec.Source.ChatMessages, client.IgnoreNotFound(err)
}
