// Package problem holds the facts of a stuck issue, collected by the
// operator from CRD objects, never written by an LLM: the Desk shows them and
// the pilot gets them as JSON. No Kubernetes client, no GitLab calls.
package problem

import (
	"strings"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// MaxError bounds, in characters, a failed run's error.
const MaxError = 300

// Facts are the facts of one problem.
type Facts struct {
	IID          int64          `json:"iid"`
	Issue        IssueFacts     `json:"issue"`
	MergeRequest *MRFacts       `json:"mergeRequest,omitempty"`
	Pipeline     *PipelineFacts `json:"pipeline,omitempty"`
	Run          *RunFacts      `json:"run,omitempty"`
	Saved        *SavedFacts    `json:"saved,omitempty"`
	Blocked      []int64        `json:"blocked,omitempty"` // issues waiting on this one, as a chain
}

type IssueFacts struct {
	IID    int64    `json:"iid"`
	Title  string   `json:"title"`
	URL    string   `json:"url"`
	Labels []string `json:"labels,omitempty"`
}

type MRFacts struct {
	IID          int64      `json:"iid"`
	URL          string     `json:"url"`
	HeadSHA      string     `json:"headSHA,omitempty"`
	CommitsAhead int32      `json:"commitsAhead,omitempty"`
	PushedSHA    string     `json:"pushedSHA,omitempty"`
	PushedAt     *time.Time `json:"pushedAt,omitempty"`
}

type PipelineFacts struct {
	ID         int64      `json:"id"`
	URL        string     `json:"url"`
	Status     string     `json:"status"`
	FailedJobs []JobFacts `json:"failedJobs,omitempty"`
}

type JobFacts struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// RunFacts is the task's last run; Task names the task the web links the
// log of, Error is set for a failed run only.
type RunFacts struct {
	Task    string `json:"task"`
	Number  int32  `json:"number"`
	Result  string `json:"result,omitempty"`
	Error   string `json:"error,omitempty"`
	Cause   string `json:"cause,omitempty"`
	NoteURL string `json:"noteURL,omitempty"`
}

type SavedFacts struct {
	SHA              string `json:"sha,omitempty"`
	Lines            int64  `json:"lines,omitempty"`
	UncommittedLines int64  `json:"uncommittedLines,omitempty"`
}

// Build is the facts of issue from its task (nil without one).
func Build(issue IssueFacts, task *aiv1.AgentTask) Facts {
	f := Facts{IID: issue.IID, Issue: issue}
	if task == nil {
		return f
	}
	run := task.Status.Run
	if run.MergeRequestIID != 0 {
		mr := &MRFacts{IID: run.MergeRequestIID, URL: run.MergeRequestURL, HeadSHA: run.PushedSHA,
			CommitsAhead: run.CommitsAhead, PushedSHA: run.PushedSHA}
		if run.Checkpoint != nil && run.Checkpoint.SHA != "" {
			mr.HeadSHA = run.Checkpoint.SHA // cleared when a newer regular head is pushed
		}
		if run.PushedAt != nil {
			at := run.PushedAt.Time
			mr.PushedAt = &at
		}
		f.MergeRequest = mr
	}
	if run.RunCount > 0 {
		f.Run = &RunFacts{Task: task.Name, Number: run.RunCount, Result: string(run.LastResult),
			Cause: Cause(task), NoteURL: run.NeedsInputURL}
		if run.LastResult == aiv1.RunResultFailed {
			f.Run.Error = textutil.Truncate(strings.Join(strings.Fields(run.Summary), " "), MaxError-1)
		}
	}
	return f
}
