package controller

import (
	"reflect"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestFollowUpOfLineIsCarriedAndPlansNothing(t *testing.T) {
	e := newAutopilotEnv(t)
	base := "/agent autopilot\nBuild a new checkout."
	e.gl.Milestones[0].Description = base
	e.reconcile()
	e.reconcile()
	planned := e.tasks()[specTaskName].Spec
	if planned.FollowUpOf != nil {
		t.Fatalf("without the line there is no value: %+v", planned.FollowUpOf)
	}

	for _, c := range []struct {
		line string
		want *aiv1.FollowUpOf
	}{
		{line: "\n\n/agent followup-of 12 3, 1 3", want: &aiv1.FollowUpOf{MilestoneIID: 12, Numbers: []int{1, 3}}},
		{line: "\n\n/agent followup-of 12 2", want: &aiv1.FollowUpOf{MilestoneIID: 12, Numbers: []int{2}}},
		{line: "\n\n/agent followup-of 12 10"}, // malformed: no value
		{line: ""},                             // removed
	} {
		e.gl.Milestones[0].Description = base + c.line
		e.reconcile()
		st := e.tasks()[specTaskName].Spec
		if !reflect.DeepEqual(st.FollowUpOf, c.want) {
			t.Fatalf("%q: the spec task carries %+v, want %+v", c.line, st.FollowUpOf, c.want)
		}
		if st.RunRequest != planned.RunRequest || st.Source.DescriptionHash != planned.Source.DescriptionHash {
			t.Fatalf("%q: the line re-plans nothing: run %d hash %s, was run %d hash %s", c.line,
				st.RunRequest, st.Source.DescriptionHash, planned.RunRequest, planned.Source.DescriptionHash)
		}
	}
}
