package problem

import (
	"fmt"
	"slices"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// MaxWhy bounds, in characters, the line Why returns.
const MaxWhy = 300

// Why is one line on why iid (0: the milestone) is stuck in st: its hold,
// the pause over it, or the open question about it.
func Why(st *aiv1.AgentTask, iid int64) string {
	ap := st.Status.Autopilot
	if ap != nil {
		for _, it := range ap.Items {
			if it.IID == iid && iid != 0 && it.Hold != nil {
				return line(fmt.Sprintf("held (%s): %s", it.Hold.Kind, it.Hold.Reason))
			}
		}
		if ap.Phase == aiv1.AutopilotPaused && aiv1.ProblemIssue(ap) == iid {
			kind := string(ap.PauseKind)
			if kind == "" {
				kind = "unknown"
			}
			return line(fmt.Sprintf("paused (%s): %s", kind, ap.PausedReason))
		}
	}
	for _, p := range slices.Backward(st.Status.Prompts) {
		if p.State == aiv1.PromptOpen && p.Source.IssueIID == iid {
			return line("question: " + p.Question)
		}
	}
	return "needs attention"
}

func line(s string) string {
	return textutil.Truncate(strings.Join(strings.Fields(s), " "), MaxWhy)
}
