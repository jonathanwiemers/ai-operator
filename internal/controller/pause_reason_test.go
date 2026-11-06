package controller

import (
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

const (
	testPipelineURL = "https://gitlab.example/g/p/-/pipelines/77"
	testNoteURL     = "https://gitlab.example/g/p/-/issues/1#note_9"
)

func TestNeedsInputReason(t *testing.T) {
	ended := metav1.NewTime(time.Date(2026, 11, 6, 13, 10, 0, 0, time.UTC))
	limit := func(engine aiv1.Engine, until time.Time) aiv1.UsageLimit {
		return aiv1.UsageLimit{Engine: engine, Until: metav1.NewTime(until)}
	}
	long := strings.Repeat("ä", 250)
	for _, tc := range []struct {
		name string
		task *aiv1.AgentTask
		want string
	}{
		{"no task", nil, ""},
		{"no cause (an older release)", &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{Summary: "boom"}}}, ""},
		{"ciRed, jobs sorted", &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{
			Run: aiv1.RunStatus{NeedsInputCause: aiv1.NeedsInputCIRed, CIRound: 2},
			CI:  &aiv1.CIStatus{State: aiv1.CIStateFailed, FailedJobs: []string{"test", "lint", "build"}, PipelineURL: testPipelineURL}}},
			"#7 needs input: CI is red after the fix rounds: failing jobs `build`, `lint`, `test` (" + testPipelineURL + ")"},
		{"ciRed without a CI status", &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{NeedsInputCause: aiv1.NeedsInputCIRed}}},
			"#7 needs input: CI is red after the fix rounds: failing jobs (none reported)"},
		{"runFailed, multi-line error", &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{
			NeedsInputCause: aiv1.NeedsInputRunFailed, NeedsInputURL: testNoteURL,
			Summary: "\n  git push   failed:\texit 1\nremote: rejected\n"}}},
			"#7 needs input: the run failed: git push failed: exit 1… (" + testNoteURL + ")"},
		{"runFailed, long error", &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{
			NeedsInputCause: aiv1.NeedsInputRunFailed, Summary: long}}},
			"#7 needs input: the run failed: " + strings.Repeat("ä", 199) + "…"},
		{"runFailed, usage limit today", &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{
			NeedsInputCause: aiv1.NeedsInputRunFailed, NeedsInputURL: testNoteURL, LastActivityTime: &ended,
			Summary:     "giving up after 20 usage limit deferrals: claude usage limit until 2026-11-06T16:00:00Z: 5-hour limit reached",
			UsageLimits: []aiv1.UsageLimit{limit(aiv1.EngineClaude, time.Date(2026, 11, 6, 16, 0, 0, 0, time.UTC))}}}},
			"#7 needs input: the run failed: claude usage limit, resets 16:00 UTC (" + testNoteURL + ")"},
		{"runFailed, usage limit another day, the other engine's ignored", &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{
			NeedsInputCause: aiv1.NeedsInputRunFailed, LastActivityTime: &ended,
			Summary: "cursor usage limit until 2026-11-13T00:00:00Z: monthly",
			UsageLimits: []aiv1.UsageLimit{limit(aiv1.EngineClaude, time.Date(2026, 11, 6, 16, 0, 0, 0, time.UTC)),
				limit(aiv1.EngineCursor, time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))}}}},
			"#7 needs input: the run failed: cursor usage limit, resets 2026-11-13 00:00 UTC"},
		{"runFailed, a limit the run went around", &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{
			NeedsInputCause: aiv1.NeedsInputRunFailed, Summary: "tests failed",
			UsageLimits: []aiv1.UsageLimit{limit(aiv1.EngineClaude, time.Date(2026, 11, 6, 16, 0, 0, 0, time.UTC))}}}},
			"#7 needs input: the run failed: tests failed"},
		{"agentQuestion, the first question", &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{
			NeedsInputCause: aiv1.NeedsInputAgentQuestion, NeedsInputURL: testNoteURL, Summary: "Implemented half.",
			Questions: []aiv1.QuestionDraft{{Question: "REST or gRPC?\nREST is simpler."}, {Question: "Which port?"}}}}},
			"#7 needs input: the agent asks: REST or gRPC?… (" + testNoteURL + ")"},
		{"agentQuestion without a structured one", &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{
			NeedsInputCause: aiv1.NeedsInputAgentQuestion, Summary: "Which database should it use?"}}},
			"#7 needs input: the agent asks: Which database should it use?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := needsInputReason(7, tc.task)
			if ok != (tc.want != "") || got != tc.want {
				t.Fatalf("needsInputReason = %q, %v\nwant %q", got, ok, tc.want)
			}
		})
	}
}

func TestOneLine(t *testing.T) {
	for in, want := range map[string]string{
		"":                      "",
		"  a \t b  ":            "a b",
		"a\n\n":                 "a",
		"a\nb":                  "a…",
		strings.Repeat("x", 10): "xxxxxxxxxx",
		strings.Repeat("x", 11): "xxxxxxxxx…",
		"xxxx xxxx\nmore":       "xxxx xxxx…",
		"xxxx xxxxx\nmore":      "xxxx xxxx…",
	} {
		got := oneLine(in, 10)
		if got != want || utf8.RuneCountInString(got) > 10 {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// needsInputPause pauses autopilot over #1 in needs-input, with mutate
// recording the cause on its task, and polls twice more.
func needsInputPause(t *testing.T, mutate func(*aiv1.AgentTaskStatus)) *projectEnv {
	t.Helper()
	e := newAutopilotEnv(t)
	e.reconcile()
	e.reconcile()
	name := e.finishRun(1, "sha-api", nil)
	e.patchStatus(name, mutate)
	e.gl.SetLabels(1, "ai-generated", "agent::needs-input")
	for range 3 {
		e.gl.Clock = e.gl.Clock.Add(time.Minute)
		e.reconcile()
	}
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotPaused || ap.PausedFor != 1 || ap.PauseKind != aiv1.PauseNeedsInput {
		t.Fatalf("expected a needs-input pause over #1: %+v", ap)
	}
	return e
}

// Every cause names itself in the pause, and a pause comments once: its
// reason holds nothing that changes between polls.
func TestAutopilotPauseNamesTheNeedsInputCause(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*aiv1.AgentTaskStatus)
		want   string
	}{
		{"ciRed", func(s *aiv1.AgentTaskStatus) {
			s.Run.NeedsInputCause, s.Run.CIRound = aiv1.NeedsInputCIRed, 2
			s.CI = &aiv1.CIStatus{SHA: "sha-api", State: aiv1.CIStateFailed, FailedJobs: []string{"test", "lint"}, PipelineURL: testPipelineURL}
		}, "#1 needs input: CI is red after the fix rounds: failing jobs `lint`, `test` (" + testPipelineURL + ")"},
		{"runFailed", func(s *aiv1.AgentTaskStatus) {
			s.Run.NeedsInputCause, s.Run.NeedsInputURL = aiv1.NeedsInputRunFailed, testNoteURL
			s.Run.LastResult, s.Run.Summary = aiv1.RunResultFailed, "agent exited with 1\nstack trace"
		}, "#1 needs input: the run failed: agent exited with 1… (" + testNoteURL + ")"},
		{"agentQuestion", func(s *aiv1.AgentTaskStatus) {
			s.Run.NeedsInputCause, s.Run.NeedsInputURL = aiv1.NeedsInputAgentQuestion, testNoteURL
			s.Run.Questions = []aiv1.QuestionDraft{{Question: "REST or gRPC?"}}
		}, "#1 needs input: the agent asks: REST or gRPC? (" + testNoteURL + ")"},
		{"no cause, from an older release", func(s *aiv1.AgentTaskStatus) {},
			"#1 needs input (see its last comments): CI is still red, the run failed or the agent asked a question"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := needsInputPause(t, tc.mutate)
			if got := e.autopilot().PausedReason; got != tc.want {
				t.Fatalf("paused for %q, want %q", got, tc.want)
			}
			comments := trackingComments(e)
			if n := strings.Count(comments, "⏸️ Autopilot paused"); n != 1 {
				t.Fatalf("one pause comments once, got %d:\n%s", n, comments)
			}
			if !strings.Contains(comments, "⏸️ Autopilot paused: ") {
				t.Fatalf("the comment carries the reason:\n%s", comments)
			}
		})
	}
}

func TestPilotEventNamesTheNeedsInputCause(t *testing.T) {
	spec := testProject().Spec.WithDefaults()
	m := &gitlab.Milestone{IID: 3}
	st := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Milestone: &aiv1.MilestoneRef{IID: 3}}, Status: aiv1.AgentTaskStatus{Plan: []aiv1.PlannedIssue{{IID: 2}}}}
	issues := map[int64]gitlab.Issue{2: {IID: 2, Milestone: m, Labels: []string{"agent::needs-input"}}}
	for _, tc := range []struct {
		name string
		run  aiv1.RunStatus
		want string
	}{
		{"agentQuestion", aiv1.RunStatus{NeedsInputCause: aiv1.NeedsInputAgentQuestion, NeedsInputURL: testNoteURL, Questions: []aiv1.QuestionDraft{{Question: "Which port?"}}},
			"#2 needs input: the agent asks: Which port? (" + testNoteURL + ")"},
		{"no cause", aiv1.RunStatus{},
			"#2 needs input: CI is still red after the fix rounds, the run failed or the agent asked a question"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.run.PushedSHA = "sha2"
			evs := pilotEvents(spec, nil, st, issues, map[int64]*aiv1.AgentTask{2: {Status: aiv1.AgentTaskStatus{Run: tc.run}}}, true, time.Time{})
			if !slices.EqualFunc(evs, []aiv1.PilotEvent{{Key: "stuck/2/sha2", IssueIID: 2, Text: tc.want}}, func(a, b aiv1.PilotEvent) bool { return a == b }) {
				t.Fatalf("got %+v, want the text %q", evs, tc.want)
			}
		})
	}
}
