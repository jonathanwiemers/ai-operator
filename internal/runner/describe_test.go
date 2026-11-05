package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func TestWithDescription(t *testing.T) {
	block := descriptionBlock("new")
	for _, tc := range []struct{ name, desc, want string }{
		{"opened by the runner", newDescription(42), block + "\n\nCloses #42"},
		{"block replaced, the rest kept", "Human intro\n\n" + descriptionBlock("old") + "\n\nCloses #42\n\nhuman notes",
			"Human intro\n\n" + block + "\n\nCloses #42\n\nhuman notes"},
		{"legacy placeholder", "Closes #42\n\nThe agent works on this; its progress, reports and decisions are in the comments.", block + "\n\nCloses #42"},
		{"empty", "", block + "\n\nCloses #42"},
		{"written by a human", "Closes #42\n\nMy notes.", block + "\n\nCloses #42\n\nMy notes."},
		{"begin without end", descriptionBegin + " broken", block + "\n\n" + descriptionBegin + " broken"},
	} {
		if got := withDescription(tc.desc, "new", 42); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
	if body, ok := managedDescription("x " + descriptionBlock("  body\n") + " y"); !ok || body != "body" {
		t.Errorf("managed description: %q %v", body, ok)
	}
}

func TestCleanDiagram(t *testing.T) {
	for in, want := range map[string]string{
		"flowchart LR\n  A --> B":                          "flowchart LR\n  A --> B",
		"```mermaid\nsequenceDiagram\n  A->>B: x\n```":     "sequenceDiagram\n  A->>B: x",
		"```\nstateDiagram-v2\n  [*] --> A\n```":           "stateDiagram-v2\n  [*] --> A",
		"":                                                 "",
		"pie title Pets\n  \"Dogs\" : 1":                   "",
		"flowchart LR\n  A --> B\n```\nhi":                 "",
		"flowchart LR\n  A[<!-- x -->] --> B":              "",
		"flowchart LR\n" + strings.Repeat("A-->B\n", 1000): "",
	} {
		if got := cleanDiagram(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestRenderDescription(t *testing.T) {
	if _, err := renderDescription(DescribeAnswer{Why: "x"}); err == nil {
		t.Fatal("an answer without what is no description")
	}
	got, err := renderDescription(DescribeAnswer{Why: "Because <!-- /ai-operator:description --> @bob.", What: "- a", Diagram: "pie"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, descriptionEnd) || strings.Contains(got, "@bob") || strings.Contains(got, "### How") || strings.Contains(got, "Diagram") {
		t.Fatalf("unexpected description:\n%s", got)
	}
}

// describingEngine changes a file on the implementer's first call only and
// answers the describer, or fails it.
type describingEngine struct {
	mu        sync.Mutex
	calls     []Invocation
	describes int
	failWith  error
	answer    ImplementAnswer
	runErr    error
	// onDescribe runs while the describer works, e.g. a human's edit.
	onDescribe func()
}

func (d *describingEngine) Run(_ context.Context, inv Invocation) (*Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, inv)
	var v any = ReviewAnswer{Summary: "fine"}
	switch {
	case inv.JSONSchema == describeSchema:
		d.describes++
		if d.onDescribe != nil {
			d.onDescribe()
		}
		if d.failWith != nil {
			return nil, d.failWith
		}
		v = DescribeAnswer{Why: "w", What: "- steady", How: "- steady.go", Verification: "- none"}
	case !inv.ReadOnly:
		if d.runErr != nil {
			return nil, d.runErr
		}
		_ = os.WriteFile(filepath.Join(inv.Dir, "steady.go"), []byte("package app\n"), 0o644)
		v = ImplementAnswer{Status: "done", Summary: "Done.", CommitMessage: "Add steady", Handoff: "done"}
		if d.answer.Status != "" {
			v = d.answer
		}
	}
	b, _ := json.Marshal(v)
	return &Result{SessionID: "sess-d", Structured: b}, nil
}

func (e *implementEnv) rerun(t *testing.T, r *Runner) {
	t.Helper()
	ctx := context.Background()
	var got aiv1.AgentTask
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &got)
	orig := got.DeepCopy()
	got.Spec.RunRequest++
	if err := e.k8s.Patch(ctx, &got, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	if ran, err := r.Step(ctx); err != nil || !ran {
		t.Fatalf("rerun: ran=%v err=%v", ran, err)
	}
}

// configure changes the project and the task before a run.
func (e *implementEnv) configure(t *testing.T, project func(*aiv1.AgentProjectSpec), task func(*aiv1.AgentTaskSpec)) {
	t.Helper()
	ctx := context.Background()
	var p aiv1.AgentProject
	if err := e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "shop"}, &p); err != nil {
		t.Fatal(err)
	}
	project(&p.Spec)
	if err := e.k8s.Update(ctx, &p); err != nil {
		t.Fatal(err)
	}
	var tk aiv1.AgentTask
	if err := e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &tk); err != nil {
		t.Fatal(err)
	}
	task(&tk.Spec)
	if err := e.k8s.Update(ctx, &tk); err != nil {
		t.Fatal(err)
	}
}

func TestDescribeSkipsAnUnchangedDiff(t *testing.T) {
	e := newImplementEnv(t)
	engine := &describingEngine{}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if engine.describes != 1 || !strings.Contains(e.gl.MRs[1].Description, "- steady") {
		t.Fatalf("the first push is described: %d calls, %q", engine.describes, e.gl.MRs[1].Description)
	}
	// A human edits around the block; the next run changes nothing.
	e.gl.MRs[1].Description += "\n\nPlease check the logs."
	e.rerun(t, r)
	if engine.describes != 1 {
		t.Fatalf("an unchanged diff is not described again, %d calls", engine.describes)
	}
	if !strings.HasSuffix(e.gl.MRs[1].Description, "Please check the logs.") {
		t.Fatalf("the human's text must stay:\n%s", e.gl.MRs[1].Description)
	}
}

func TestDescribeKeepsAnEditMadeMeanwhile(t *testing.T) {
	e := newImplementEnv(t)
	engine := &describingEngine{}
	engine.onDescribe = func() { e.gl.MRs[1].Description = "Edited while the agent wrote.\n\n" + e.gl.MRs[1].Description }
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	desc := e.gl.MRs[1].Description
	if !strings.HasPrefix(desc, "Edited while the agent wrote.") || !strings.Contains(desc, "- steady") {
		t.Fatalf("the edit made during the describer's call must stay:\n%s", desc)
	}
}

func TestDescriberFailureIsANotice(t *testing.T) {
	e := newImplementEnv(t)
	engine := &describingEngine{failWith: errors.New("boom")}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	report := e.gl.MRNotes[1][len(e.gl.MRNotes[1])-1].Body
	if !strings.Contains(report, "Done.") || !strings.Contains(report, "description was not updated: the describer failed: boom") {
		t.Fatalf("the report keeps its summary and says why:\n%s", report)
	}
	if body, _ := managedDescription(e.gl.MRs[1].Description); body != descriptionPlaceholder {
		t.Fatalf("the placeholder stays: %q", e.gl.MRs[1].Description)
	}
	var got aiv1.AgentTask
	_ = e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &got)
	if got.Status.Run.LastResult != aiv1.RunResultSucceeded {
		t.Fatalf("the run succeeds: %s", got.Status.Run.LastResult)
	}
}

func TestDescribeDisabled(t *testing.T) {
	e := newImplementEnv(t)
	engine := &describingEngine{}
	e.configure(t, func(p *aiv1.AgentProjectSpec) { p.Agents.Describe.Disabled = true }, func(*aiv1.AgentTaskSpec) {})
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if engine.describes != 0 || slices.Contains(stepNames(r.run), "describe merge request") {
		t.Fatalf("disabled: no describer, %d calls", engine.describes)
	}
}

func TestQuestionsLeaveTheMentionToTheirPrompts(t *testing.T) {
	e := newImplementEnv(t)
	engine := &describingEngine{answer: ImplementAnswer{Status: "needs_input", Summary: "Stuck.", Questions: []string{"Which provider?"}}}
	e.configure(t, func(*aiv1.AgentProjectSpec) {}, func(s *aiv1.AgentTaskSpec) { s.Owners = []string{"alice"} })
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, n := range e.gl.IssueNotes[42] {
		if strings.Contains(n.Body, "Which provider?") || strings.Contains(n.Body, "cc @") {
			t.Fatalf("the operator's prompt comments ask and mention, not the runner:\n%s", n.Body)
		}
	}
	report := e.gl.MRNotes[1][len(e.gl.MRNotes[1])-1].Body
	if strings.Contains(report, "cc @") || !strings.Contains(report, "Which provider?") {
		t.Fatalf("the report lists the questions without a mention:\n%s", report)
	}
	if !strings.Contains(report, "Stuck.") {
		t.Fatalf("a run that stops for input keeps its summary, also with the first description:\n%s", report)
	}
}

func TestReviewMentionsAndRequestsTheOwners(t *testing.T) {
	e := newImplementEnv(t)
	e.gl.Users = []gitlab.User{{ID: 7, Username: "alice"}}
	e.configure(t, func(p *aiv1.AgentProjectSpec) { p.CI.Disabled = true }, func(s *aiv1.AgentTaskSpec) { s.Owners = []string{"alice"} })
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, &describingEngine{}, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::review"}) {
		t.Fatalf("without CI the issue goes to review, has %v", labels)
	}
	if report := e.gl.MRNotes[1][len(e.gl.MRNotes[1])-1].Body; !strings.Contains(report, "cc @alice") {
		t.Fatalf("the report asks the owners to review:\n%s", report)
	}
	if got := e.gl.MRs[1].Reviewers; len(got) != 1 || got[0].Username != "alice" {
		t.Fatalf("reviewers %+v", got)
	}
	if !slices.Contains(stepNames(r.run), "request review") {
		t.Fatalf("steps %v", stepNames(r.run))
	}
}

func TestReviewOnAutopilotPingsNobody(t *testing.T) {
	e := newImplementEnv(t)
	e.gl.Users = []gitlab.User{{ID: 7, Username: "alice"}}
	e.configure(t, func(p *aiv1.AgentProjectSpec) { p.CI.Disabled = true },
		func(s *aiv1.AgentTaskSpec) { s.Owners, s.Autopilot = []string{"alice"}, true })
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, &describingEngine{}, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if report := e.gl.MRNotes[1][len(e.gl.MRNotes[1])-1].Body; strings.Contains(report, "cc @") || len(e.gl.MRs[1].Reviewers) != 0 {
		t.Fatalf("autopilot merges itself; nobody is asked:\n%s", report)
	}
}

func TestFailureOnAutopilotLeavesThePingToThePause(t *testing.T) {
	e := newImplementEnv(t)
	e.configure(t, func(*aiv1.AgentProjectSpec) {}, func(s *aiv1.AgentTaskSpec) { s.Owners, s.Autopilot = []string{"alice"}, true })
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, &describingEngine{runErr: errors.New("engine exploded")}, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	notes := e.gl.IssueNotes[42]
	if body := notes[len(notes)-1].Body; !strings.Contains(body, "The agent run failed") || strings.Contains(body, "cc @") {
		t.Fatalf("on autopilot the failure pings nobody:\n%s", body)
	}
}

func TestReportRunsNoQuickActions(t *testing.T) {
	got := renderReport(ImplementAnswer{Summary: "Done.\n/merge\nping @alice"}, nil, nil, "")
	if strings.Contains(got, "\n/merge") || strings.Contains(got, "@alice") {
		t.Fatalf("agent text must be inert:\n%q", got)
	}
}

func TestFailureMentionsTheOwners(t *testing.T) {
	e := newImplementEnv(t)
	e.configure(t, func(*aiv1.AgentProjectSpec) {}, func(s *aiv1.AgentTaskSpec) { s.Owners = []string{"alice", "bob"} })
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, &describingEngine{runErr: errors.New("engine exploded")}, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	notes := e.gl.IssueNotes[42]
	body := notes[len(notes)-1].Body
	if !strings.Contains(body, "The agent run failed") || !strings.Contains(body, "cc @alice @bob") {
		t.Fatalf("the failure mentions the owners:\n%s", body)
	}
	var task aiv1.AgentTask
	_ = e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	if !slices.Equal(e.gl.Labels(42), []string{"agent::needs-input"}) || task.Status.Run.BlockReason != "" {
		t.Fatalf("a real failure needs input, it does not block: %v, %+v", e.gl.Labels(42), task.Status.Run)
	}
}

func TestFailureQuotesTheErrorInert(t *testing.T) {
	e := newImplementEnv(t)
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, &describingEngine{runErr: errors.New("boom\n```\n/close\ncc @mallory")}, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	notes := e.gl.IssueNotes[42]
	body := notes[len(notes)-1].Body
	if !strings.Contains(body, "boom") || strings.Contains(body, "```\n/close") || strings.Contains(body, "\n/close") || strings.Contains(body, "@mallory") {
		t.Fatalf("the error must not leave its code block or run quick actions:\n%s", body)
	}
}

func TestEmptySummaryGetsAFallback(t *testing.T) {
	e := newImplementEnv(t)
	e.configure(t, func(p *aiv1.AgentProjectSpec) { p.Agents.Describe.Disabled = true }, func(*aiv1.AgentTaskSpec) {})
	engine := &describingEngine{answer: ImplementAnswer{Status: "done", CommitMessage: "Add steady"}}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	last := func() string { return e.gl.MRNotes[1][len(e.gl.MRNotes[1])-1].Body }
	if !strings.Contains(last(), "The agent left no summary. This run pushed its changes") {
		t.Fatalf("a run that pushed says so:\n%s", last())
	}
	e.rerun(t, r) // writes the same file again: nothing changes
	if !strings.Contains(last(), "The agent left no summary and changed nothing in this run.") {
		t.Fatalf("a run without changes says so:\n%s", last())
	}
	var got aiv1.AgentTask
	_ = e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &got)
	if got.Status.Run.Summary != "The agent left no summary and changed nothing in this run." {
		t.Fatalf("the task status carries it too: %q", got.Status.Run.Summary)
	}
}
