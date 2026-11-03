package controller

import (
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// frozenEnv is an autopilot milestone whose first issue passed review and CI
// while main is red, reported by #99.
func frozenEnv(t *testing.T) *projectEnv {
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthRed, IssueIID: 99, FailedJobs: []string{"test"}})
	e.finishRun(1, "sha-api", nil)
	e.reconcile()
	return e
}

const frozenReason = "`main` is red (#99): merging waits until it is green"

func TestAutopilotFreezesWhileMainIsRed(t *testing.T) {
	e := frozenEnv(t)
	ap := e.autopilot()
	blocked := e.tasks()["shop-1-api"].Status.Blocked
	if ap.Phase != aiv1.AutopilotPaused || ap.PausedReason != frozenReason || ap.PauseKind != aiv1.PauseMainRed || ap.PausedFor != 1 || len(e.gl.Merged) != 0 {
		t.Fatalf("a red main must hold the merge and pause: merged %v, %+v", e.gl.Merged, ap)
	}
	if blocked == nil || blocked.Reason != aiv1.BlockMainRed || blocked.RestoreLabel != "agent::review" {
		t.Fatalf("the active issue must be blocked with main-red: %+v", blocked)
	}
	e.reconcile()
	if l := e.gl.Labels(1); !slices.Contains(l, "agent::blocked") || !slices.Contains(l, "agent-blocked::main-red") || slices.Contains(l, "agent::review") {
		t.Fatalf("the issue must show the block: %v", l)
	}

	// The review label put back by hand while blocked: the gate still
	// waits, no continue / pause comments every poll.
	e.gl.SetLabels(1, append(e.gl.Labels(1), "agent::review")...)
	e.reconcile()
	e.reconcile()
	if c := trackingComments(e); strings.Count(c, "Autopilot paused") != 1 || strings.Contains(c, "Autopilot continues") || len(e.gl.Merged) != 0 {
		t.Fatalf("a blocked issue must not pass the gate: merged %v\n%s", e.gl.Merged, c)
	}
	e.gl.SetLabels(1, slices.DeleteFunc(e.gl.Labels(1), func(l string) bool { return l == "agent::review" })...)

	// The next red pipeline is retried: still frozen, nothing said again.
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthRetrying, IssueIID: 99})
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || len(e.gl.Merged) != 0 || strings.Count(trackingComments(e), "Autopilot paused") != 1 {
		t.Fatalf("the freeze must hold while main is retried: merged %v, %+v\n%s", e.gl.Merged, ap, trackingComments(e))
	}

	// Green: the block is lifted, autopilot continues and merges.
	e.setMainHealthStatus(aiv1.MainHealthStatus{State: aiv1.MainHealthGreen})
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || !strings.Contains(trackingComments(e), "▶️ Autopilot continues: `main` is green again.") {
		t.Fatalf("green must continue by itself: %+v\n%s", ap, trackingComments(e))
	}
	if b := e.tasks()["shop-1-api"].Status.Blocked; b != nil || !slices.Equal(e.gl.Labels(1), []string{"ai-generated", "agent::review"}) {
		t.Fatalf("the block must be lifted: %+v %v", b, e.gl.Labels(1))
	}
	e.reconcile() // marked ready
	e.reconcile()
	if !slices.Equal(e.gl.Merged, []int64{1}) {
		t.Fatalf("expected !1 merged after the freeze, merged %v", e.gl.Merged)
	}
}

func TestAutopilotFreezeLiftsWithoutMainHealth(t *testing.T) {
	e := frozenEnv(t)
	e.reconcile()
	e.setMainHealth(aiv1.MainHealthSpec{Disabled: true})
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotRunning || !strings.Contains(trackingComments(e), "the health of `main` is not watched any more") {
		t.Fatalf("disabling main health must lift the freeze: %+v\n%s", ap, trackingComments(e))
	}
	e.reconcile()
	e.reconcile()
	if !slices.Equal(e.gl.Merged, []int64{1}) {
		t.Fatalf("expected !1 merged, merged %v", e.gl.Merged)
	}
}

func TestAutopilotFreezeSwitchedOff(t *testing.T) {
	e := frozenEnv(t)
	e.reconcile()
	off := false
	e.setMainHealth(aiv1.MainHealthSpec{FreezeAutopilot: &off})
	e.reconcile()
	if c := trackingComments(e); !strings.Contains(c, "the merge freeze is switched off") || strings.Contains(c, "green again") {
		t.Fatalf("switching the freeze off must say so, not claim green:\n%s", c)
	}
	e.reconcile()
	e.reconcile()
	if !slices.Equal(e.gl.Merged, []int64{1}) {
		t.Fatalf("expected !1 merged, merged %v", e.gl.Merged)
	}
}

func TestAutopilotMergesWithoutAFreeze(t *testing.T) {
	off := false
	for name, tc := range map[string]struct {
		spec   aiv1.MainHealthSpec
		status aiv1.MainHealthState
	}{
		"flaky":       {status: aiv1.MainHealthFlaky},
		"first retry": {status: aiv1.MainHealthRetrying},
		"disabled":    {spec: aiv1.MainHealthSpec{Disabled: true}, status: aiv1.MainHealthRed},
		"freeze off":  {spec: aiv1.MainHealthSpec{FreezeAutopilot: &off}, status: aiv1.MainHealthRed},
	} {
		t.Run(name, func(t *testing.T) {
			e := newAutopilotEnv(t)
			e.setMainHealth(tc.spec)
			e.reconcile()
			e.reconcile()
			e.setMainHealthStatus(aiv1.MainHealthStatus{State: tc.status, FailedJobs: []string{"test"}})
			e.finishRun(1, "sha-api", nil)
			e.reconcile()
			e.reconcile()
			if !slices.Equal(e.gl.Merged, []int64{1}) || e.tasks()["shop-1-api"].Status.Blocked != nil {
				t.Fatalf("expected !1 merged without a block, merged %v, %+v", e.gl.Merged, e.autopilot())
			}
		})
	}
}
