package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// removingReason is the entry reason while a child's hook is not gone yet.
const removingReason = "removing: waiting for the project hook to be deleted"

// removeChildren removes the set's children whose entry is not active and
// that have no AgentTask and no AgentPlan: dormant ones, and those whose project left the
// set's scope. A child's webhook is switched off first, and the child is
// deleted only once its GitLab hook is gone, because deleting an
// AgentProject leaves its hook behind. Known limit: an AgentProject a human
// deletes still leaves its GitLab hook, as it always did.
//
// counts and planCounts are the cached AgentTasks and AgentPlans per child;
// the delete re-reads them live, because both go with the child.
func (r *AgentProjectSetReconciler) removeChildren(ctx context.Context, set *aiv1.AgentProjectSet, plan *projectSetPlan,
	existing map[string]*aiv1.AgentProject, counts, planCounts map[string]int, now time.Time) {
	for i := range plan.Entries {
		e := &plan.Entries[i]
		cur := existing[e.Child]
		if e.Child == "" || e.State == aiv1.ProjectSetActive || cur == nil || !metav1.IsControlledBy(cur, set) {
			continue
		}
		if tasks, plans := counts[e.Child], planCounts[e.Child]; tasks > 0 || plans > 0 {
			e.Reason = keptReason(tasks, plans, e.Reason)
			continue
		}
		deleted, err := r.removeChild(ctx, cur, e, now)
		if err != nil {
			// The next poll tries again from where the child is.
			log.FromContext(ctx).Error(err, "Removing child AgentProject failed", "child", cur.Name)
			continue
		}
		if deleted {
			e.Child = ""
		}
	}
	plan.Counts = projectSetCounts(plan.Entries)
}

// removeChild takes the next removal step of a child without cached tasks
// and plans and reports whether it deleted the child.
func (r *AgentProjectSetReconciler) removeChild(ctx context.Context, p *aiv1.AgentProject, e *aiv1.ProjectSetProjectStatus, now time.Time) (bool, error) {
	// Start: webhook off. The spec change makes the child full-sync, which
	// deletes the hook. The delete waits for a later poll at the earliest.
	if p.Spec.Webhook.Enabled || p.Annotations[aiv1.AnnotationRemoving] == "" {
		patch := client.MergeFrom(p.DeepCopy())
		p.Spec.Webhook.Enabled = false
		if p.Annotations[aiv1.AnnotationRemoving] == "" {
			metav1.SetMetaDataAnnotation(&p.ObjectMeta, aiv1.AnnotationRemoving, now.UTC().Format(time.RFC3339))
		}
		e.Reason = removingReason
		return false, r.Patch(ctx, p, patch)
	}
	// Wait: a hook that cannot be deleted keeps the child, without timeout.
	if c := meta.FindStatusCondition(p.Status.Conditions, aiv1.ConditionWebhookRegistered); c != nil ||
		(p.Status.Webhook != nil && p.Status.Webhook.HookID != 0) {
		e.Reason = removingReason
		if c != nil && (c.Reason == "Failed" || c.Reason == "Forbidden") {
			e.Reason += ": " + c.Message
		}
		return false, nil
	}
	// Delete: the child's tasks and plans go with it, so the cache, which may
	// lag, is not enough.
	var tasks aiv1.AgentTaskList
	if err := r.APIReader.List(ctx, &tasks, client.InNamespace(p.Namespace), client.MatchingLabels{LabelProject: p.Name}); err != nil {
		return false, fmt.Errorf("listing the tasks of %s: %w", p.Name, err)
	}
	var plans aiv1.AgentPlanList
	if err := r.APIReader.List(ctx, &plans, client.InNamespace(p.Namespace)); err != nil {
		return false, fmt.Errorf("listing the plans of %s: %w", p.Name, err)
	}
	if n, m := len(tasks.Items), countPlans(plans.Items)[p.Name]; n > 0 || m > 0 {
		e.Reason = keptReason(n, m, e.Reason)
		return false, nil
	}
	// The preconditions refuse a child that changed since it was read, e.g.
	// one whose hook a sync still running registered.
	err := r.Delete(ctx, p, client.Preconditions{UID: &p.UID, ResourceVersion: &p.ResourceVersion})
	switch {
	case apierrors.IsConflict(err):
		log.FromContext(ctx).V(1).Info("Child changed since it was read, deleting it on the next poll", "child", p.Name)
		e.Reason = removingReason
		return false, nil
	case err != nil && !apierrors.IsNotFound(err):
		return false, err
	}
	log.FromContext(ctx).Info("Removed child AgentProject", "child", p.Name, "project", p.Spec.GitLab.Project)
	return true, nil
}

// countPlans counts the plans per spec.projectRef. A plan being deleted
// goes with or without its project and does not count.
func countPlans(plans []aiv1.AgentPlan) map[string]int {
	counts := map[string]int{}
	for i := range plans {
		if p := &plans[i]; p.DeletionTimestamp.IsZero() {
			counts[p.Spec.ProjectRef]++
		}
	}
	return counts
}

// keptReason says why a child the set would remove stays, e.g.
// "kept: 1 open task, 2 plans; no work for 168h".
func keptReason(tasks, plans int, reason string) string {
	var parts []string
	if tasks > 0 {
		parts = append(parts, plural(tasks, "open task"))
	}
	if plans > 0 {
		parts = append(parts, plural(plans, "plan"))
	}
	s := "kept: " + strings.Join(parts, ", ")
	if reason != "" {
		s += "; " + reason
	}
	return s
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
