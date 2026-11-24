package controller

import (
	"testing"

	"github.com/go-logr/logr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// A run that pushed nothing to its merge request leaves the issue in
// needs-input: the gate pauses for it, by run, with the cause. The config
// pause stays for a review label a human set by hand.
func TestGateNothingPushed(t *testing.T) {
	a := &autopilot{spec: aiv1.AgentProjectSpec{}.WithDefaults(), ap: &aiv1.AutopilotStatus{}, log: logr.Discard()}
	it := &aiv1.AutopilotItem{IID: 4, State: aiv1.AutopilotItemActive, MergeRequestIID: 9}
	run := aiv1.RunStatus{LastResult: aiv1.RunResultSucceeded, NeedsInputCause: aiv1.NeedsInputNothingPushed, NeedsInputURL: testNoteURL}
	task := &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Run: run}}
	for _, tc := range []struct {
		label  string
		kind   aiv1.PauseKind
		byRun  bool
		reason string
	}{
		{"agent::needs-input", aiv1.PauseNeedsInput, true, "#4 needs input: the run pushed no commits to the merge request (" + testNoteURL + ")"},
		{"agent::review", aiv1.PauseConfig, false, "#4 is in review without a pushed merge request"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			got := a.gateCheck(it, gitlab.Issue{IID: 4, Labels: []string{tc.label}}, task)
			if got.state != gateFailed || got.kind != tc.kind || got.byRun != tc.byRun || got.reason != tc.reason {
				t.Fatalf("got %+v, want %s (byRun %t) %q", got, tc.kind, tc.byRun, tc.reason)
			}
		})
	}
}
