package problem

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

const (
	mrURL   = "https://gitlab.example/g/p/-/merge_requests/583"
	noteURL = "https://gitlab.example/g/p/-/issues/558#note_9"
)

func TestBuild(t *testing.T) {
	pushed := time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC)
	issue := IssueFacts{IID: 558, Title: "Save work", URL: "https://gitlab.example/g/p/-/issues/558", Labels: []string{"agent::needs-input"}}
	failed := func(summary string, cp *aiv1.CheckpointStatus) *aiv1.AgentTask {
		at := metav1.NewTime(pushed)
		return &aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Name: "p-implement-558"}, Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{
			MergeRequestIID: 583, MergeRequestURL: mrURL, PushedSHA: "aaa111", PushedAt: &at, CommitsAhead: 2,
			Checkpoint: cp, RunCount: 1, LastResult: aiv1.RunResultFailed, Summary: summary,
			NeedsInputCause: aiv1.NeedsInputRunFailed, NeedsInputURL: noteURL}}}
	}
	long := strings.Repeat("x", 600) + "\n" + strings.Repeat("y", 400)
	for _, tc := range []struct {
		name string
		task *aiv1.AgentTask
		want Facts
	}{
		{"no task", nil, Facts{IID: 558, Issue: issue}},
		{"task without MR or run", &aiv1.AgentTask{}, Facts{IID: 558, Issue: issue}},
		{"MR and failed run", failed("decoding implementer answer: no JSON object in agent result", nil), Facts{IID: 558, Issue: issue,
			MergeRequest: &MRFacts{IID: 583, URL: mrURL, HeadSHA: "aaa111", CommitsAhead: 2, PushedSHA: "aaa111", PushedAt: &pushed},
			Run: &RunFacts{Task: "p-implement-558", Number: 1, Result: "Failed", NoteURL: noteURL,
				Error: "decoding implementer answer: no JSON object in agent result",
				Cause: "the run failed: decoding implementer answer: no JSON object in agent result"}}},
		{"checkpoint is the head", failed("boom", &aiv1.CheckpointStatus{SHA: "bbb222", Reason: "run-failed"}), Facts{IID: 558, Issue: issue,
			MergeRequest: &MRFacts{IID: 583, URL: mrURL, HeadSHA: "bbb222", CommitsAhead: 2, PushedSHA: "aaa111", PushedAt: &pushed},
			Run:          &RunFacts{Task: "p-implement-558", Number: 1, Result: "Failed", NoteURL: noteURL, Error: "boom", Cause: "the run failed: boom"},
			Saved:        &SavedFacts{SHA: "bbb222"}}},
		{"succeeded run has no error", &aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Name: "t"}, Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{
			RunCount: 3, LastResult: aiv1.RunResultSucceeded, Summary: "done"}}},
			Facts{IID: 558, Issue: issue, Run: &RunFacts{Task: "t", Number: 3, Result: "Succeeded"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := js(t, Build(issue, tc.task, nil, nil)), js(t, tc.want); got != want {
				t.Errorf("Build = %s\nwant %s", got, want)
			}
		})
	}

	t.Run("long multi-line error", func(t *testing.T) {
		got := Build(issue, failed(long, nil), nil, nil).Run
		if n := utf8.RuneCountInString(got.Error); n > MaxError || strings.Contains(got.Error, "\n") || !strings.HasSuffix(got.Error, "…") {
			t.Errorf("Error = %q (%d characters), want one line of at most %d with …", got.Error, n, MaxError)
		}
		if want := "the run failed: " + strings.Repeat("x", 199) + "…"; got.Cause != want {
			t.Errorf("Cause = %q, want %q", got.Cause, want)
		}
	})
}

func TestBuildPipelineAndSaved(t *testing.T) {
	const pipeURL = "https://gitlab.example/g/p/-/pipelines/77"
	issue := IssueFacts{IID: 558}
	jobs := []JobFacts{{Name: "test", URL: pipeURL + "/failures"}, {Name: "lint", URL: pipeURL + "/failures"}}
	for _, tc := range []struct {
		name string
		st   aiv1.AgentTaskStatus
		want Facts
	}{
		{"MR, red pipeline and checkpoint", aiv1.AgentTaskStatus{
			Run: aiv1.RunStatus{MergeRequestIID: 583, MergeRequestURL: mrURL, PushedSHA: "aaa111",
				Checkpoint: &aiv1.CheckpointStatus{SHA: "bbb222", Reason: "run-failed", Added: 348, Deleted: 28}},
			CI: &aiv1.CIStatus{SHA: "aaa111", State: aiv1.CIStateFailed, PipelineID: 77, PipelineURL: pipeURL, FailedJobs: []string{"test", "lint"}}},
			Facts{IID: 558, Issue: issue, MergeRequest: &MRFacts{IID: 583, URL: mrURL, HeadSHA: "bbb222", PushedSHA: "aaa111"},
				Pipeline: &PipelineFacts{ID: 77, URL: pipeURL, Status: "Failed", FailedJobs: jobs},
				Saved:    &SavedFacts{SHA: "bbb222", Lines: 376}}},
		{"no MR, uncommitted lines", aiv1.AgentTaskStatus{Run: aiv1.RunStatus{UncommittedLines: 120}},
			Facts{IID: 558, Issue: issue, Saved: &SavedFacts{UncommittedLines: 120}}},
		{"checkpoint and uncommitted lines", aiv1.AgentTaskStatus{Run: aiv1.RunStatus{UncommittedLines: 4,
			Checkpoint: &aiv1.CheckpointStatus{SHA: "ccc", Added: 10}}},
			Facts{IID: 558, Issue: issue, Saved: &SavedFacts{SHA: "ccc", Lines: 10, UncommittedLines: 4}}},
		{"CI without a pipeline", aiv1.AgentTaskStatus{CI: &aiv1.CIStatus{SHA: "a", State: aiv1.CIStatePending}},
			Facts{IID: 558, Issue: issue}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := &aiv1.AgentTask{Status: tc.st}
			if got, want := js(t, Build(issue, task, nil, nil)), js(t, tc.want); got != want {
				t.Errorf("Build = %s\nwant %s", got, want)
			}
		})
	}
}

func TestBuildBlocked(t *testing.T) {
	pi := func(iid int64, deps ...int64) aiv1.PlannedIssue { return aiv1.PlannedIssue{IID: iid, DependsOn: deps} }
	all := map[int64]bool{558: true, 559: true, 560: true, 561: true, 562: true, 563: true}
	for _, tc := range []struct {
		name string
		plan []aiv1.PlannedIssue
		open map[int64]bool
		want []int64
	}{
		{"chain", []aiv1.PlannedIssue{pi(560, 559), pi(559, 558), pi(558)}, all, []int64{559, 560}},
		{"branch, breadth-first then by IID", []aiv1.PlannedIssue{pi(563, 559), pi(562, 558), pi(559, 558), pi(560, 562), pi(561)},
			all, []int64{559, 562, 560, 563}},
		{"two paths count once", []aiv1.PlannedIssue{pi(559, 558), pi(560, 558, 559)}, all, []int64{559, 560}},
		{"cycle", []aiv1.PlannedIssue{pi(558, 560), pi(559, 558), pi(560, 559)}, all, []int64{559, 560}},
		{"merged dependents are left out", []aiv1.PlannedIssue{pi(559, 558), pi(560, 559)}, map[int64]bool{558: true, 559: true}, []int64{559}},
		{"no plan", nil, all, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Build(IssueFacts{IID: 558}, nil, tc.plan, tc.open).Blocked
			if !slices.Equal(got, tc.want) {
				t.Errorf("Blocked = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIIDs(t *testing.T) {
	hold := &aiv1.ItemHold{Kind: aiv1.PauseCIRed}
	prompt := func(kind aiv1.PromptKind, iid int64, state aiv1.PromptState) aiv1.Prompt {
		return aiv1.Prompt{Source: aiv1.PromptSource{Kind: kind, IssueIID: iid}, State: state}
	}
	st := &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{
		Autopilot: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PausedFor: 558, Items: []aiv1.AutopilotItem{
			{IID: 558, State: aiv1.AutopilotItemActive, Hold: hold},
			{IID: 600, State: aiv1.AutopilotItemActive},
			{IID: 601, State: aiv1.AutopilotItemSkipped, Hold: hold},
		}},
		Prompts: []aiv1.Prompt{
			prompt(aiv1.PromptAssessment, 558, aiv1.PromptOpen),
			prompt(aiv1.PromptAssessment, 557, aiv1.PromptOpen),
			prompt(aiv1.PromptAssessment, 0, aiv1.PromptOpen),
			prompt(aiv1.PromptAssessment, 590, aiv1.PromptAnswered),
			prompt(aiv1.PromptBudget, 591, aiv1.PromptOpen),
		},
	}}
	if got, want := IIDs(st), []int64{557, 558}; !slices.Equal(got, want) {
		t.Errorf("IIDs = %v, want %v", got, want)
	}
	if got := IIDs(nil); got != nil {
		t.Errorf("IIDs(nil) = %v", got)
	}
	if got := IIDs(&aiv1.AgentTask{}); got != nil {
		t.Errorf("IIDs(empty) = %v", got)
	}
}

func TestCause(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  aiv1.RunStatus
		ci   *aiv1.CIStatus
		want string
	}{
		{"none", aiv1.RunStatus{Summary: "boom"}, nil, ""},
		{"runFailed", aiv1.RunStatus{NeedsInputCause: aiv1.NeedsInputRunFailed, Summary: "decoding implementer answer: no JSON\nmore"}, nil,
			"the run failed: decoding implementer answer: no JSON…"},
		{"ciRed", aiv1.RunStatus{NeedsInputCause: aiv1.NeedsInputCIRed}, &aiv1.CIStatus{FailedJobs: []string{"test", "lint"}, PipelineURL: "u"},
			"CI is red after the fix rounds: failing jobs `lint`, `test`"},
		{"ciRed, no CI", aiv1.RunStatus{NeedsInputCause: aiv1.NeedsInputCIRed}, nil, "CI is red after the fix rounds: failing jobs (none reported)"},
		{"agentQuestion", aiv1.RunStatus{NeedsInputCause: aiv1.NeedsInputAgentQuestion, Summary: "s",
			Questions: []aiv1.QuestionDraft{{Question: "Which DB?"}}}, nil, "the agent asks: Which DB?"},
		{"agentQuestion from summary", aiv1.RunStatus{NeedsInputCause: aiv1.NeedsInputAgentQuestion, Summary: "Which DB?"}, nil, "the agent asks: Which DB?"},
		{"nothingPushed", aiv1.RunStatus{NeedsInputCause: aiv1.NeedsInputNothingPushed, Summary: "Nothing to do."}, nil,
			"the run pushed no commits to the merge request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Run: tc.run, CI: tc.ci}}
			if got := Cause(task); got != tc.want {
				t.Errorf("Cause = %q, want %q", got, tc.want)
			}
		})
	}
	if got := Cause(nil); got != "" {
		t.Errorf("Cause(nil) = %q", got)
	}
}

func js(t *testing.T, f Facts) string {
	t.Helper()
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
