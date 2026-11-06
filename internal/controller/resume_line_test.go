package controller

import (
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// Every resume comment names who ended the pause; a person's name is
// defused, so the comment pings nobody.
func TestResumeLineNamesWhoEndedThePause(t *testing.T) {
	for _, c := range []struct {
		by       aiv1.PauseResolver
		who, why string
		want     string
	}{
		{aiv1.ResolvedByOwner, "@jonathan", "resumed", "▶️ Autopilot continues: resumed (ended by @\u200bjonathan)."},
		{aiv1.ResolvedByOwner, "Ada via the Desk", "resumed", "▶️ Autopilot continues: resumed (ended by Ada via the Desk)."},
		{aiv1.ResolvedByOwner, "@alice via the pilot", "resumed", "▶️ Autopilot continues: resumed (ended by @\u200balice via the pilot)."},
		{aiv1.ResolvedByOwner, "", "resumed", "▶️ Autopilot continues: resumed (ended by an owner)."},
		{aiv1.ResolvedByPilot, pilotSelf, "resumed", "▶️ Autopilot continues: resumed (ended by the pilot)."},
		{aiv1.ResolvedByOperator, "#3", "!4 was merged", "▶️ Autopilot continues: !4 was merged (ended by the operator: the cause went away)."},
		{aiv1.ResolvedByLaterRun, "#3", "#3 is reviewed and green again", "▶️ Autopilot continues: #3 is reviewed and green again (ended by a later run of #3)."},
		{aiv1.ResolvedBySelfHealed, "", "`main` is green", "▶️ Autopilot continues: `main` is green (ended by the operator: it rebased the merge request)."},
	} {
		if got := resumeLine(c.by, c.who, c.why); got != c.want {
			t.Errorf("resumeLine(%q, %q) = %q, want %q", c.by, c.who, got, c.want)
		}
	}
}
