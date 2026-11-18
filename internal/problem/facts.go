// Package problem holds the facts of a stuck issue, collected by the
// operator from CRD objects, never written by an LLM: the Desk shows them and
// the pilot gets them as JSON. No Kubernetes client, no GitLab calls.
package problem

import (
	"slices"
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

// Build is the facts of issue from its task (nil without one); plan and
// open, the IIDs not merged or done, give the issues blocked on it.
func Build(issue IssueFacts, task *aiv1.AgentTask, plan []aiv1.PlannedIssue, open map[int64]bool) Facts {
	f := Facts{IID: issue.IID, Issue: issue, Blocked: blocked(issue.IID, plan, open)}
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
	if ci := task.Status.CI; ci != nil && ci.PipelineID != 0 {
		f.Pipeline = &PipelineFacts{ID: ci.PipelineID, URL: ci.PipelineURL, Status: string(ci.State)}
		for _, j := range ci.FailedJobs { // status keeps no job IDs: link the failures page
			f.Pipeline.FailedJobs = append(f.Pipeline.FailedJobs, JobFacts{Name: j, URL: ci.PipelineURL + "/failures"})
		}
	}
	if cp := run.Checkpoint; cp != nil || run.UncommittedLines > 0 {
		f.Saved = &SavedFacts{UncommittedLines: run.UncommittedLines}
		if cp != nil {
			f.Saved.SHA, f.Saved.Lines = cp.SHA, cp.Added+cp.Deleted
		}
	}
	return f
}

// blocked lists the open issues that depend on iid, directly or not,
// breadth-first and then by IID.
func blocked(iid int64, plan []aiv1.PlannedIssue, open map[int64]bool) []int64 {
	var out []int64
	seen := map[int64]bool{iid: true}
	for level := []int64{iid}; len(level) > 0; {
		var next []int64
		for _, p := range plan {
			if !seen[p.IID] && open[p.IID] && slices.ContainsFunc(p.DependsOn, func(d int64) bool { return slices.Contains(level, d) }) {
				seen[p.IID] = true
				next = append(next, p.IID)
			}
		}
		slices.Sort(next)
		out, level = append(out, next...), next
	}
	return out
}

// IIDs are the issues of st's milestone with an open problem: held items,
// the issue of a pause, and those of open assessment prompts; sorted, unique.
func IIDs(st *aiv1.AgentTask) []int64 {
	if st == nil {
		return nil
	}
	var out []int64
	if ap := st.Status.Autopilot; ap != nil {
		for _, it := range ap.Items {
			if it.State == aiv1.AutopilotItemActive && it.Hold != nil {
				out = append(out, it.IID)
			}
		}
		if iid := aiv1.ProblemIssue(ap); iid != 0 {
			out = append(out, iid)
		}
	}
	for _, p := range st.Status.Prompts {
		if p.Source.Kind == aiv1.PromptAssessment && p.State == aiv1.PromptOpen && p.Source.IssueIID != 0 {
			out = append(out, p.Source.IssueIID)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
