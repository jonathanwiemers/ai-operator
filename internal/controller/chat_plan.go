package controller

import (
	"cmp"
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentplans,verbs=get;list;watch;patch

// chatPlanReply is the Chat reply reconciler for AgentPlans: it copies
// status.chat.replies into chat_messages and drops the answered messages
// from spec.chatMessages. It also copies status.decisions into the decision
// history (new versions and sign-offs only).
type chatPlanReply struct{ *Chat }

// Reconcile copies the plan's replies like Chat.Reconcile does a task's.
func (c chatPlanReply) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	s := c.readyStore()
	if s == nil {
		return ctrl.Result{RequeueAfter: chatDBRetry}, nil
	}
	var plan aiv1.AgentPlan
	if err := c.Get(ctx, req.NamespacedName, &plan); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// The history first: a turn's decisions are kept also when no reply is
	// left to copy.
	if len(plan.Status.Decisions) > 0 {
		key := store.PlanKey{Namespace: plan.Namespace, Project: plan.Spec.ProjectRef, Plan: plan.Name}
		if err := s.SavePlanDecisions(ctx, key, planDecisionRows(plan.Status.Decisions), c.now().Add(c.Retention)); err != nil {
			return ctrl.Result{}, fmt.Errorf("saving the plan's decisions: %w", err)
		}
	}
	row := store.ChatMessage{Namespace: plan.Namespace, Project: plan.Spec.ProjectRef, Plan: plan.Name}
	if n := len(plan.Status.Calls); n > 0 {
		row.AuthorName = string(plan.Status.Calls[n-1].Engine)
	}
	orig := plan.DeepCopy()
	keep, dropped, err := c.copyReplies(ctx, s, plan.Spec.ChatMessages, plan.Status.Chat, nil, row)
	if err != nil || !dropped {
		return ctrl.Result{}, err
	}
	plan.Spec.ChatMessages = keep
	// Optimistic lock, as for tasks: never drop a message dispatched meanwhile.
	return ctrl.Result{}, c.Patch(ctx, &plan, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{}))
}

// planDecisionRows converts the plan's current decisions into store rows. A
// sign-off without a time is not stored, as the time is part of its key.
func planDecisionRows(list []aiv1.PlanDecision) []store.PlanDecisionRow {
	rows := make([]store.PlanDecisionRow, 0, len(list))
	for _, d := range list {
		r := store.PlanDecisionRow{PlanDecision: store.PlanDecision{
			ID: d.ID, Version: d.Version, Title: d.Title, Question: d.Question, Options: d.Options,
			Recommended: int(d.Recommended), Why: d.Why, State: string(d.State),
			DecidedBy: d.DecidedBy, DecidedByName: d.DecidedByName, DecidedAt: d.DecidedAt.Time,
		}}
		if d.SignedOffBy != "" && d.SignedOffAt != nil {
			r.SignOff = &store.PlanDecisionSignOff{By: d.SignedOffBy, ByName: d.SignedOffByName, At: d.SignedOffAt.Time}
		}
		rows = append(rows, r)
	}
	return rows
}

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
	case c.planWaiting(ctx, &plan, m, fail): // failed or left queued
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

// planWaiting reports whether plan waits for its project set to create its
// project. Its message m then stays queued for a later poll, or fails with
// the plan's status message once the plan failed. A read error leaves m
// queued too.
func (c *Chat) planWaiting(ctx context.Context, plan *aiv1.AgentPlan, m store.ChatMessage, fail func(string)) bool {
	if plan.Labels[aiv1.LabelProjectSet] == "" {
		return false
	}
	err := c.Get(ctx, types.NamespacedName{Namespace: m.Namespace, Name: m.Project}, &aiv1.AgentProject{})
	switch {
	case err == nil:
		return false
	case !apierrors.IsNotFound(err):
		c.Log.Error(err, "getting project for chat message", "id", m.ID, "project", m.Project)
	case plan.Status.Phase == aiv1.AgentPlanFailed:
		fail(cmp.Or(plan.Status.Message, "the plan failed"))
	}
	return true
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
