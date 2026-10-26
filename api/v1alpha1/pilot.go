package v1alpha1

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// PilotState hashes what a proposal of the pilot is made on: the milestone
// description (with its rules) and the autopilot's queue and pause (spec task st) and the
// heads of the issues its actions are about (tasks by issue IID). The
// proposal no longer applies once it changes.
func PilotState(st *AgentTask, tasks map[int64]*AgentTask, actions []PilotAction) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\n", st.Spec.Source.DescriptionHash)
	for _, r := range st.Spec.Rules {
		fmt.Fprintf(h, "rule %s\n", r)
	}
	if ap := st.Status.Autopilot; ap != nil {
		fmt.Fprintf(h, "%s %d %q\n", ap.Phase, ap.PausedFor, ap.PausedReason)
		for _, it := range ap.Items {
			fmt.Fprintf(h, "%d %s\n", it.IID, it.State)
		}
	}
	for _, a := range actions {
		if t := tasks[a.IssueIID]; t != nil && a.IssueIID != 0 {
			fmt.Fprintf(h, "#%d %s\n", a.IssueIID, t.Status.Run.PushedSHA)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// AutonomyOf is the autonomy of a milestone's pilot (its Coordinate task):
// the project's pilot.autonomy, else managed on autopilot milestones. An
// owner's "/pilot stop" lowers it to propose; nothing raises it.
func (s PilotSpec) AutonomyOf(coord *AgentTask) PilotAutonomy {
	a := s.Autonomy
	if a == "" && coord.Spec.Autopilot {
		a = PilotManaged
	}
	if ps := coord.Status.Pilot; a != PilotManaged || ps != nil && ps.AutonomyOverride != nil && ps.AutonomyOverride.Autonomy != PilotManaged {
		return PilotPropose
	}
	return PilotManaged
}
