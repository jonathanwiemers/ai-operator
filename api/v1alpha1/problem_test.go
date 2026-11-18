package v1alpha1

import "testing"

func TestProblemIssue(t *testing.T) {
	held := func(iid int64) AutopilotItem {
		return AutopilotItem{IID: iid, State: AutopilotItemActive, Hold: &ItemHold{Kind: PauseCIRed, Reason: "red"}}
	}
	free := AutopilotItem{IID: 7, State: AutopilotItemActive}
	skippedHeld := AutopilotItem{IID: 8, State: AutopilotItemSkipped, Hold: &ItemHold{Kind: PauseCIRed}}
	for _, c := range []struct {
		name string
		ap   *AutopilotStatus
		want int64
	}{
		{"no autopilot", nil, 0},
		{"not paused", &AutopilotStatus{Phase: AutopilotRunning, Items: []AutopilotItem{held(558)}}, 0},
		{"paused for an issue", &AutopilotStatus{Phase: AutopilotPaused, PausedFor: 560, Items: []AutopilotItem{held(558)}}, 560},
		{"one held item", &AutopilotStatus{Phase: AutopilotPaused, Items: []AutopilotItem{free, held(558), skippedHeld}}, 558},
		{"two held items", &AutopilotStatus{Phase: AutopilotPaused, Items: []AutopilotItem{held(558), free, held(559)}}, 0},
		{"none held", &AutopilotStatus{Phase: AutopilotPaused, Items: []AutopilotItem{free, skippedHeld}}, 0},
	} {
		if got := ProblemIssue(c.ap); got != c.want {
			t.Errorf("%s: ProblemIssue = %d, want %d", c.name, got, c.want)
		}
	}
}
