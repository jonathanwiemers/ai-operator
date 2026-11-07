package rules

import (
	"reflect"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestFollowUpOf(t *testing.T) {
	for _, c := range []struct {
		desc string
		want *aiv1.FollowUpOf
		bad  bool
	}{
		{desc: spec},
		{desc: spec + "\n/agent followup-of 12 1 3", want: &aiv1.FollowUpOf{MilestoneIID: 12, Numbers: []int{1, 3}}},
		{desc: "/agent followup-of %12 3,1\n" + spec, want: &aiv1.FollowUpOf{MilestoneIID: 12, Numbers: []int{1, 3}}},
		{desc: spec + "\n  /agent followup-of 7 2, 9 ,1  \r\n", want: &aiv1.FollowUpOf{MilestoneIID: 7, Numbers: []int{1, 2, 9}}},
		{desc: spec + "\n/agent followup-of 7 3 3 1 3", want: &aiv1.FollowUpOf{MilestoneIID: 7, Numbers: []int{1, 3}}},                // deduplicated
		{desc: spec + "\n/agent followup-of 7 1\n/agent followup-of 8 2", want: &aiv1.FollowUpOf{MilestoneIID: 7, Numbers: []int{1}}}, // the first line counts
		{desc: spec + "\n/agent followup-of 7 x\n/agent followup-of 8 2", bad: true},                                                  // even when malformed
		{desc: spec + "\n/agent followups-of 7 1"},           // not the command
		{desc: spec + "\nSee /agent followup-of 7 1 there"},  // not at the start of a line
		{desc: spec + "\n/agent followup-of", bad: true},     // nothing
		{desc: spec + "\n/agent followup-of 7", bad: true},   // no numbers
		{desc: spec + "\n/agent followup-of 7,", bad: true},  // no numbers
		{desc: spec + "\n/agent followup-of 0 1", bad: true}, // no milestone 0
		{desc: spec + "\n/agent followup-of x 1", bad: true},
		{desc: spec + "\n/agent followup-of 7 0", bad: true},
		{desc: spec + "\n/agent followup-of 7 10", bad: true},
		{desc: spec + "\n/agent followup-of 7 -1", bad: true},
		{desc: spec + "\n/agent followup-of 7 1 two", bad: true},
		{desc: spec + "\n/agent followup-of 7 #1", bad: true},
	} {
		got, err := FollowUpOf(c.desc, "/agent")
		if !reflect.DeepEqual(got, c.want) || (err != nil) != c.bad {
			t.Errorf("FollowUpOf(%q) = %+v, %v; want %+v, error %v", c.desc, got, err, c.want, c.bad)
		}
	}
}

func TestFollowUpOfLine(t *testing.T) {
	line := FollowUpOfLine("/agent", 12, []int{1, 3})
	if line != "/agent followup-of 12 1 3" {
		t.Fatalf("FollowUpOfLine = %q", line)
	}
	got, err := FollowUpOf(spec+"\n\n"+line, "/agent")
	if err != nil || !reflect.DeepEqual(got, &aiv1.FollowUpOf{MilestoneIID: 12, Numbers: []int{1, 3}}) {
		t.Fatalf("the rendered line reads back: %+v, %v", got, err)
	}
}

func TestFollowUpOfLineIsNoPartOfTheSpec(t *testing.T) {
	for _, desc := range []string{
		spec + "\n/agent followup-of 12 1 3",
		spec + "\n\n/agent followup-of 12 1",
		"/agent autopilot\nBuild a new checkout.\n\n/agent followup-of 4 2\n\nPay with PayPal.",
		spec + "\n/agent followup-of nonsense", // an invalid line is still the operator's
		spec + "\n/agent owners @alice\n/agent followup-of 3 1\n/agent budget 5\n/agent rule R1: prompt x",
	} {
		if got := strings.TrimSpace(Spec(desc, "/agent")); got != spec {
			t.Errorf("the followup-of line must not change the spec:\n%q\ngot\n%q", desc, got)
		}
	}
}

func TestWithoutFollowUpOf(t *testing.T) {
	desc := spec + "\n\n/agent followup-of 12 1\n\n/agent owners @alice"
	if got := WithoutFollowUpOf(desc, "/agent"); got != spec+"\n\n/agent owners @alice" {
		t.Errorf("WithoutFollowUpOf = %q", got)
	}
}
