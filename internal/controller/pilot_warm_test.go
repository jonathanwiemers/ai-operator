package controller

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func heldAt(iid int64, state aiv1.AutopilotItemState, since time.Time) aiv1.AutopilotItem {
	return aiv1.AutopilotItem{IID: iid, State: state, Hold: &aiv1.ItemHold{Kind: aiv1.PauseNeedsInput, Reason: "held", Since: metav1.NewTime(since)}}
}

func TestProblemWarmUntil(t *testing.T) {
	max := 2 * time.Hour
	paused := metav1.NewTime(t0.Add(10 * time.Minute))
	cases := []struct {
		name string
		ap   *aiv1.AutopilotStatus
		want time.Time // zero: nil
	}{
		{"no autopilot", nil, time.Time{}},
		{"running, no hold", &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning,
			Items: []aiv1.AutopilotItem{{IID: 1, State: aiv1.AutopilotItemActive}}}, time.Time{}},
		{"pause", &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PausedAt: &paused}, paused.Add(max)},
		{"pausedAt left after the pause", &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, PausedAt: &paused}, time.Time{}},
		{"hold", &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning,
			Items: []aiv1.AutopilotItem{heldAt(1, aiv1.AutopilotItemActive, t0)}}, t0.Add(max)},
		{"hold of an item no longer active", &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning,
			Items: []aiv1.AutopilotItem{heldAt(1, aiv1.AutopilotItemMerged, t0)}}, time.Time{}},
		{"pause and holds: the earliest", &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PausedAt: &paused,
			Items: []aiv1.AutopilotItem{heldAt(1, aiv1.AutopilotItemActive, t0.Add(time.Hour)), heldAt(2, aiv1.AutopilotItemActive, t0)}}, t0.Add(max)},
		{"pause before the hold", &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PausedAt: &paused,
			Items: []aiv1.AutopilotItem{heldAt(1, aiv1.AutopilotItemActive, t0.Add(time.Hour))}}, paused.Add(max)},
	}
	for _, tc := range cases {
		got := problemWarmUntil(tc.ap, max)
		switch {
		case tc.want.IsZero() && got != nil:
			t.Errorf("%s: got %v, want nil", tc.name, *got)
		case !tc.want.IsZero() && (got == nil || !got.Equal(tc.want)):
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The pilot is kept up while a hold is open, hibernates after
// problemWarmMax from its start although it is still open, and its
// warmUntil is cleared once it closes. Unchanged values write nothing.
func TestWarmPilotWhileAProblemIsOpen(t *testing.T) {
	e := newTaskEnv(t, warmPilot("p", nil, nil))
	pr := &AgentProjectReconciler{Client: e.c, Now: func() time.Time { return e.now }}
	max := aiv1.DefaultProblemWarmMax
	ap := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning,
		Items: []aiv1.AutopilotItem{heldAt(558, aiv1.AutopilotItemActive, t0)}}
	warm := func() *aiv1.AgentTask {
		t.Helper()
		coord := e.task("p")
		if err := pr.warmPilot(e.ctx, coord, ap, max); err != nil {
			t.Fatal(err)
		}
		return e.task("p")
	}

	got := warm()
	if w := got.Spec.WarmUntil; w == nil || !w.Time.Equal(t0.Add(max)) {
		t.Fatalf("warmUntil = %v, want %v", w, t0.Add(max))
	}
	if again := warm(); again.ResourceVersion != got.ResourceVersion {
		t.Fatal("an unchanged warmUntil is written again")
	}

	var pod corev1.Pod
	e.reconcile("p")
	if !e.exists(&pod, "p") {
		t.Fatal("the pilot gets a pod while a problem is open")
	}
	e.now = t0.Add(max - time.Minute)
	e.reconcile("p")
	if !e.exists(&pod, "p") {
		t.Fatal("the pilot's pod stays up while a problem is open")
	}

	// Past the cap: the hold is still open, the poll writes nothing, and
	// the pod hibernates.
	e.now = t0.Add(max + time.Second)
	before := e.task("p").ResourceVersion
	if again := warm(); again.ResourceVersion != before || !again.Spec.WarmUntil.Time.Equal(t0.Add(max)) {
		t.Fatal("a poll past the cap renews warmUntil")
	}
	e.reconcile("p")
	if e.exists(&pod, "p") {
		t.Fatal("the pilot hibernates after problemWarmMax")
	}

	// The hold ends: warmUntil goes.
	ap.Items[0].Hold = nil
	if w := warm().Spec.WarmUntil; w != nil {
		t.Fatalf("warmUntil = %v after the problem closed", w)
	}
}
