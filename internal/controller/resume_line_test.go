package controller

import (
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// Every resume comment names who ended the pause and how; a person's name
// is defused, so the comment pings nobody.
func TestResumeLineNamesWhoEndedThePause(t *testing.T) {
	laterRun := func(via aiv1.PauseResumeVia, runBy string) resumption {
		return resumption{by: aiv1.ResolvedByLaterRun, via: via, who: "#3", runBy: runBy}
	}
	for _, c := range []struct {
		res  resumption
		why  string
		want string
	}{
		{byCommand("@jonathan"), "resumed", "▶️ Autopilot continues: resumed (ended by @\u200bjonathan)."},
		{byCommand("Ada via the Desk"), "resumed", "▶️ Autopilot continues: resumed (ended by Ada via the Desk)."},
		{byCommand("@alice via the pilot"), "resumed", "▶️ Autopilot continues: resumed (ended by @\u200balice via the pilot)."},
		{resumption{by: aiv1.ResolvedByOwner, via: aiv1.ResumedViaCommand}, "resumed", "▶️ Autopilot continues: resumed (ended by an owner)."},
		{byCommand(pilotSelf), "resumed", "▶️ Autopilot continues: resumed (ended by the pilot)."},
		{byOperator, "`main` is green again", "▶️ Autopilot continues: `main` is green again (ended by the operator: the cause went away)."},
		{resumption{by: aiv1.ResolvedByOperator, via: aiv1.ResumedViaMergedByHand, who: "#3"}, "!4 was merged",
			"▶️ Autopilot continues: !4 was merged (ended by hand: the merge request was merged)."},
		{resumption{by: aiv1.ResolvedByOperator, via: aiv1.ResumedViaClosedByHand, who: "#3"}, "#3 was closed",
			"▶️ Autopilot continues: #3 was closed (ended by hand: the issue was closed)."},
		{laterRun(aiv1.ResumedViaReReview, aiv1.PauseRunByAutopilot), "#3 is reviewed and green again",
			"▶️ Autopilot continues: #3 is reviewed and green again (ended by a later run of #3, a review-only run the autopilot started)."},
		{laterRun(aiv1.ResumedViaTakeover, "@alice"), "#3 is reviewed and green again",
			"▶️ Autopilot continues: #3 is reviewed and green again (ended by a later run of #3, which @\u200balice's `/agent` comment started)."},
		{laterRun(aiv1.ResumedViaTakeover, "@alice via the pilot"), "#3 is reviewed and green again",
			"▶️ Autopilot continues: #3 is reviewed and green again (ended by a later run of #3, which @\u200balice via the pilot started)."},
		{laterRun(aiv1.ResumedViaTakeover, pilotSelf), "#3 is reviewed and green again",
			"▶️ Autopilot continues: #3 is reviewed and green again (ended by a later run of #3, which the pilot started)."},
		{laterRun(aiv1.ResumedViaPush, ""), "#3 is reviewed and green again",
			"▶️ Autopilot continues: #3 is reviewed and green again (ended by a later run of #3, which reviewed a head someone else pushed)."},
		{laterRun(aiv1.ResumedViaCauseGone, ""), "#3 is reviewed and green again",
			"▶️ Autopilot continues: #3 is reviewed and green again (ended by a later run of #3)."},
		{resumption{by: aiv1.ResolvedBySelfHealed, via: aiv1.ResumedViaSelfHealed}, "`main` is green",
			"▶️ Autopilot continues: `main` is green (ended by the operator: it rebased the merge request)."},
	} {
		if got := resumeLine(c.res, c.why, "/agent"); got != c.want {
			t.Errorf("resumeLine(%+v, %q) = %q, want %q", c.res, c.why, got, c.want)
		}
	}
}

// A later run's via comes from who asked for a run during the pause and the
// head the gate passed on.
func TestLaterRunVia(t *testing.T) {
	for _, c := range []struct {
		name        string
		runBy, from string
		head        string
		want        aiv1.PauseResumeVia
	}{
		{"the autopilot's review-only run on its head", aiv1.PauseRunByAutopilot, "rebased", "rebased", aiv1.ResumedViaReReview},
		{"a push after the autopilot's run was asked for", aiv1.PauseRunByAutopilot, "rebased", "pushed", aiv1.ResumedViaPush},
		{"an owner's command, on a new head", "@alice", "old", "fixed", aiv1.ResumedViaTakeover},
		{"an owner's command, on the same head", "@alice", "old", "old", aiv1.ResumedViaTakeover},
		{"the pilot's run", pilotSelf, "old", "old", aiv1.ResumedViaTakeover},
		{"nobody asked, a new head", "", "old", "pushed", aiv1.ResumedViaPush},
		{"nobody asked, the same head (a retried pipeline)", "", "old", "old", aiv1.ResumedViaCauseGone},
		{"a pause from before pauseRunBy", "", "", "old", aiv1.ResumedViaCauseGone},
	} {
		ap := &aiv1.AutopilotStatus{PauseRunBy: c.runBy, PauseRunHead: c.from}
		if got := laterRunVia(ap, c.head); got != c.want {
			t.Errorf("%s: laterRunVia = %q, want %q", c.name, got, c.want)
		}
	}
}

// Only a run of the issue the autopilot is paused for is recorded.
func TestNotePauseRun(t *testing.T) {
	ap := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PausedFor: 3}
	if notePauseRun(ap, 4, "@alice", "abc") || ap.PauseRunBy != "" {
		t.Fatalf("a run of another issue recorded: %+v", ap)
	}
	if !notePauseRun(ap, 3, "@alice", "abc") || ap.PauseRunBy != "@alice" || ap.PauseRunHead != "abc" {
		t.Fatalf("the paused issue's run not recorded: %+v", ap)
	}
	if notePauseRun(ap, 3, "@alice", "abc") {
		t.Error("the same run reported as a change")
	}
	running := &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, PausedFor: 3}
	if notePauseRun(running, 3, "@alice", "abc") || notePauseRun(nil, 3, "@alice", "abc") {
		t.Error("a run recorded without a pause")
	}
}
