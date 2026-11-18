package controller

import (
	"fmt"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/problem"
)

var (
	oneLine   = problem.OneLine
	resetTime = problem.ResetTime
	jobList   = problem.JobList
)

// needsInputReason is "#N needs input: <cause> (link)" for issue iid, with
// problem.Cause's wording. It holds only what stays the same while the cause
// does, so a pause over it comments once. False without a cause (a run of an
// older release): the caller keeps its generic text. The quoted question or
// error is the agent's: the caller defuses it.
func needsInputReason(iid int64, task *aiv1.AgentTask) (string, bool) {
	why := problem.Cause(task)
	if why == "" {
		return "", false
	}
	url := task.Status.Run.NeedsInputURL
	if task.Status.Run.NeedsInputCause == aiv1.NeedsInputCIRed {
		url = ""
		if ci := task.Status.CI; ci != nil {
			url = ci.PipelineURL
		}
	}
	return fmt.Sprintf("#%d needs input: %s%s", iid, why, linked(url)), true
}

// linked is " (url)", or nothing without a URL.
func linked(url string) string {
	if url == "" {
		return ""
	}
	return " (" + url + ")"
}
