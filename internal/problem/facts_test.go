package problem

import (
	"encoding/json"
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
			Run:          &RunFacts{Task: "p-implement-558", Number: 1, Result: "Failed", NoteURL: noteURL, Error: "boom", Cause: "the run failed: boom"}}},
		{"succeeded run has no error", &aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Name: "t"}, Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{
			RunCount: 3, LastResult: aiv1.RunResultSucceeded, Summary: "done"}}},
			Facts{IID: 558, Issue: issue, Run: &RunFacts{Task: "t", Number: 3, Result: "Succeeded"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := js(t, Build(issue, tc.task)), js(t, tc.want); got != want {
				t.Errorf("Build = %s\nwant %s", got, want)
			}
		})
	}

	t.Run("long multi-line error", func(t *testing.T) {
		got := Build(issue, failed(long, nil)).Run
		if n := utf8.RuneCountInString(got.Error); n > MaxError || strings.Contains(got.Error, "\n") || !strings.HasSuffix(got.Error, "…") {
			t.Errorf("Error = %q (%d characters), want one line of at most %d with …", got.Error, n, MaxError)
		}
		if want := "the run failed: " + strings.Repeat("x", 199) + "…"; got.Cause != want {
			t.Errorf("Cause = %q, want %q", got.Cause, want)
		}
	})
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
