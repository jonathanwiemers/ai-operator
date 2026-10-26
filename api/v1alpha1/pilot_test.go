package v1alpha1

import "testing"

func TestPilotAutonomy(t *testing.T) {
	for _, tc := range []struct {
		project   PilotAutonomy
		autopilot bool
		override  PilotAutonomy
		want      PilotAutonomy
	}{
		{"", true, "", PilotManaged},
		{"", false, "", PilotPropose},
		{PilotPropose, true, "", PilotPropose},
		{PilotManaged, false, "", PilotManaged},
		{"", true, PilotPropose, PilotPropose},           // /pilot stop
		{"", true, PilotManaged, PilotManaged},           // /pilot go
		{PilotPropose, true, PilotManaged, PilotPropose}, // go cannot raise it
		{"", false, PilotManaged, PilotPropose},
		{"yolo", true, "", PilotPropose},
	} {
		coord := &AgentTask{Spec: AgentTaskSpec{Autopilot: tc.autopilot}}
		if tc.override != "" {
			coord.Status.Pilot = &PilotStatus{AutonomyOverride: &PilotAutonomyOverride{Autonomy: tc.override}}
		}
		if got := (PilotSpec{Autonomy: tc.project}).AutonomyOf(coord); got != tc.want {
			t.Errorf("%+v: got %s", tc, got)
		}
	}
}
