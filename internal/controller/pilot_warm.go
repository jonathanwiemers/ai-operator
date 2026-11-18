package controller

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// problemWarmUntil is until when a milestone's pilot is kept warm: max
// after its earliest open problem (the pause, an active item's hold)
// opened, nil without one. It counts from the opening, not from now, so a
// problem left open stops keeping the pod after max.
func problemWarmUntil(ap *aiv1.AutopilotStatus, max time.Duration) *time.Time {
	if ap == nil {
		return nil
	}
	var opened time.Time
	open := func(t time.Time) {
		if opened.IsZero() || t.Before(opened) {
			opened = t
		}
	}
	if ap.Phase == aiv1.AutopilotPaused && ap.PausedAt != nil {
		open(ap.PausedAt.Time)
	}
	for _, it := range ap.Items {
		if it.State == aiv1.AutopilotItemActive && it.Hold != nil {
			open(it.Hold.Since.Time)
		}
	}
	if opened.IsZero() {
		return nil
	}
	until := opened.Add(max)
	return &until
}

// warmPilot sets the pilot's spec.warmUntil to problemWarmUntil, or clears
// it, and writes only when it changes.
func (r *AgentProjectReconciler) warmPilot(ctx context.Context, coord *aiv1.AgentTask, ap *aiv1.AutopilotStatus, max time.Duration) error {
	until, old := problemWarmUntil(ap, max), coord.Spec.WarmUntil
	if until == nil && old == nil || until != nil && old != nil && until.Unix() == old.Unix() {
		return nil
	}
	base := coord.DeepCopy()
	coord.Spec.WarmUntil = nil
	if until != nil {
		coord.Spec.WarmUntil = &metav1.Time{Time: *until}
	}
	return r.Patch(ctx, coord, client.MergeFrom(base))
}
