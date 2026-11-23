package controller

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// holdItems holds the given issues of the queue for red CI.
func (e *projectEnv) holdItems(iids ...int64) {
	e.t.Helper()
	e.patchStatus(specTaskName, func(s *aiv1.AgentTaskStatus) {
		for i := range s.Autopilot.Items {
			for _, iid := range iids {
				if it := &s.Autopilot.Items[i]; it.IID == iid {
					// Not resumeWhenReady: only resume ends it.
					it.Hold = &aiv1.ItemHold{Kind: aiv1.PauseCIRed, Reason: fmt.Sprintf("CI of !%d is red", iid)}
				}
			}
		}
	})
}

func TestAutopilotResumeLiftsEveryHold(t *testing.T) {
	e := newParallelEnv(t, 3, false)
	e.reconcile()
	e.reconcile()
	e.wantStates("three roots run", map[int64]aiv1.AutopilotItemState{1: active, 2: active, 4: active})
	e.holdItems(1, 2)
	e.reconcile()
	tracking := e.autopilot().TrackingIssueIID
	body := e.gl.Issues[tracking].Description
	for _, want := range []string{
		"▶️ running.\n\nHeld:\n- #1 API: CI of !1 is red (`/agent resume #1`)\n- #2 UI: CI of !2 is red (`/agent resume #2`)\n",
		"- [ ] #1 API: **held** (ciRed)", "- [ ] #2 UI: **held** (ciRed)", "- [ ] #4 Docs: **in progress**",
		"up to 3 at a time (`/agent parallel <n>` in the milestone description changes it)", "one after another",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("tracking issue misses %q:\n%s", want, body)
		}
	}

	e.gl.AddNote(tracking, gitlab.User{ID: 1, Username: "jonathan"}, "/agent resume")
	e.reconcile()
	ap := e.autopilot()
	for _, it := range ap.Items {
		if it.Hold != nil {
			t.Fatalf("resume lifts every hold: %+v", ap.Items)
		}
	}
	c := trackingComments(e)
	if strings.Count(c, "▶️") != 1 || !strings.Contains(c, "▶️ #1, #2 go on: resumed (ended by @") || ap.Phase != aiv1.AutopilotRunning {
		t.Fatalf("one comment for both holds, the milestone runs: %+v\n%s", ap, c)
	}
	if strings.Contains(e.gl.Issues[tracking].Description, "Held:") {
		t.Fatalf("no holds listed any more:\n%s", e.gl.Issues[tracking].Description)
	}
	// Driven again: #1's green run lands.
	e.mergeItem(1)
}

func TestAutopilotResumeLiftsThePauseAndTheHolds(t *testing.T) {
	e := newParallelEnv(t, 2, false)
	e.reconcile()
	e.reconcile()
	e.holdItems(1)
	tracking := e.autopilot().TrackingIssueIID
	human := gitlab.User{ID: 1, Username: "jonathan"}
	e.gl.AddNote(tracking, human, "/agent pause")
	e.reconcile()
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PauseKind != aiv1.PauseOwner {
		t.Fatalf("paused by the owner: %+v", ap)
	}
	e.gl.AddNote(tracking, human, "/agent resume")
	e.reconcile()
	ap := e.autopilot()
	c := trackingComments(e)
	if ap.Phase != aiv1.AutopilotRunning || ap.Items[0].Hold != nil || strings.Count(c, "▶️") != 1 ||
		!strings.Contains(c, "▶️ Autopilot continues: resumed; #1 goes on (ended by @") {
		t.Fatalf("resume lifts the pause and the hold with one comment: %+v\n%s", ap, c)
	}
}

// The tracking issue shows each active item's lane above a limit of 1, and
// a pending issue whose dependencies are merged waits for a free slot.
func TestTrackingLanes(t *testing.T) {
	ready := metav1.Now()
	st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: 9, Title: "M9"}}}
	st.Status.Plan = []aiv1.PlannedIssue{{IID: 1}, {IID: 2}, {IID: 3}, {IID: 4}, {IID: 5, DependsOn: []int64{6}}, {IID: 6}}
	issues := map[int64]gitlab.Issue{}
	for iid, title := range map[int64]string{1: "A", 2: "B", 3: "C", 4: "D", 5: "E", 6: "F"} {
		issues[iid] = gitlab.Issue{IID: iid, Title: title}
	}
	render := func(limit int32) string {
		ap := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, MaxParallel: limit, Landing: 1, Items: []aiv1.AutopilotItem{
			{IID: 1, State: active, ReadyAt: &ready}, {IID: 2, State: active, ReadyAt: &ready},
			{IID: 3, State: active, Hold: &aiv1.ItemHold{Kind: aiv1.PauseFindings, Reason: "!3 has findings"}},
			{IID: 4, State: active}, {IID: 5, State: pending}, {IID: 6, State: merged},
		}}
		a := &autopilot{st: st, ap: ap, issues: issues, log: logr.Discard(), spec: aiv1.AgentProjectSpec{
			Workflow: aiv1.WorkflowSpec{CommandPrefix: "/agent"}, Autopilot: &aiv1.AutopilotSpec{MaxParallel: limit}}}
		return a.renderTracking()
	}
	body := render(2)
	for _, want := range []string{
		"- [ ] #1 A: **landing** (queued)", "- [ ] #2 B: ready to land (queued)", "- [ ] #3 C: **held** (findings) (queued)",
		"- [ ] #4 D: **in progress** (queued)", "- [ ] #5 E: pending, after #6, waits for a free slot",
		"Held:\n- #3 C: !3 has findings (`/agent resume #3`)\n", "up to 2 at a time",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("tracking issue misses %q:\n%s", want, body)
		}
	}

	// At limit 1 the text is as before.
	body = render(1)
	for _, want := range []string{
		"- [ ] #1 A: **in progress** (queued)", "- [ ] #2 B: **in progress** (queued)", "- [ ] #5 E: pending, after #6\n",
		"The operator works through the planned issues one at a time, in dependency order. It merges each merge request " +
			"when its pipeline is green and its review is clean, then starts the next issue. The agents take their defaults",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("limit 1 tracking issue misses %q:\n%s", want, body)
		}
	}
}

// When maxParallel exceeds maxConcurrentRuns the tracking description warns
// that further agent runs queue (#467 / Decision 7).
func TestTrackingMaxParallelWarn(t *testing.T) {
	st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeSpec, Milestone: &aiv1.MilestoneRef{IID: 9, Title: "M9"}}}
	render := func(maxParallel, maxConcurrent int32) string {
		a := &autopilot{
			st: st, ap: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, MaxParallel: maxParallel},
			log: logr.Discard(),
			spec: aiv1.AgentProjectSpec{
				MaxConcurrentRuns: maxConcurrent,
				Workflow:          aiv1.WorkflowSpec{CommandPrefix: "/agent"},
				Autopilot:         &aiv1.AutopilotSpec{MaxParallel: maxParallel},
			},
		}
		return a.renderTracking()
	}
	warn := "⚠️ Up to 3 issues may run at the same time, but the project allows 1 agent runs at a time (`maxConcurrentRuns`): further runs queue."
	if body := render(3, 1); !strings.Contains(body, warn) {
		t.Fatalf("want warning in:\n%s", body)
	}
	for _, tc := range []struct{ parallel, concurrent int32 }{{1, 1}, {3, 4}} {
		if body := render(tc.parallel, tc.concurrent); strings.Contains(body, "maxConcurrentRuns") {
			t.Fatalf("no warning when maxParallel=%d maxConcurrentRuns=%d:\n%s", tc.parallel, tc.concurrent, body)
		}
	}
}
