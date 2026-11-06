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
	"github.com/jonathanwiemers/ai-operator/internal/conventional"
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
	// title is the describer's title.
	title string
}

func (d *describingEngine) Run(_ context.Context, inv Invocation) (*Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, inv)
	var v any = ReviewAnswer{Summary: "fine"}
	switch {
	case inv.Role == "describe":
		d.describes++
		if d.onDescribe != nil {
			d.onDescribe()
		}
		if d.failWith != nil {
			return nil, d.failWith
		}
		v = DescribeAnswer{Why: "w", What: "- steady", How: "- steady.go", Verification: "- none", Title: d.title}
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

func TestDescribeSchemaFor(t *testing.T) {
	if got := describeSchemaFor(nil); got != describeSchema || strings.Contains(got, "title") {
		t.Fatalf("without Conventional Commits the schema stays:\n%s", got)
	}
	var schema struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Pattern     string `json:"pattern"`
			Description string `json:"description"`
		} `json:"properties"`
	}
	types := []string{"feat", "fix", "chore"}
	if err := json.Unmarshal([]byte(describeSchemaFor(types)), &schema); err != nil {
		t.Fatal(err)
	}
	title := schema.Properties["title"]
	if !slices.Contains(schema.Required, "title") || title.Pattern != conventional.Pattern(types) ||
		!strings.Contains(title.Description, `no "Draft:" prefix`) {
		t.Fatalf("the title is required with the pattern: %+v", schema)
	}
}

func TestMRTitle(t *testing.T) {
	types := []string{"feat", "fix", "chore"}
	const issue, set = "Add PayPal button", "feat(pay): add the paypal button"
	for _, tc := range []struct {
		name, cur string
		draft     bool
		title     string
		last      string
		want, why string
	}{
		{"draft keeps its prefix", "Draft: " + issue, true, set, "", "Draft: " + set, ""},
		{"ready without prefix", issue, false, set, "", set, ""},
		{"the runner's last title is replaced", "Draft: fix(pay): old", true, set, "fix(pay): old", "Draft: " + set, ""},
		{"already set", "Draft: " + set, true, set, set, "", ""},
		{"a ready MR loses the prefix of a draft title", "Draft: " + set, false, set, set, set, ""},
		{"renamed by hand", "Draft: PayPal checkout", true, set, "", "", "renamed"},
		{"renamed by hand after the runner's title", "fix: my own title", false, set, "feat: older", "", "renamed"},
		{"not conventional", "Draft: " + issue, true, "Add the PayPal button", "", "", "invalid"},
		{"empty", "Draft: " + issue, true, "", "", "", "invalid"},
		{"with a draft prefix", "Draft: " + issue, true, "Draft: " + set, "", "", "invalid"},
		{"two lines", "Draft: " + issue, true, set + "\n\nbody", "", "", "invalid"},
		{"unknown type", "Draft: " + issue, true, "docs: add it", "", "", "invalid"},
	} {
		want, why := mrTitle(tc.cur, tc.draft, tc.title, types, issue, tc.last)
		if want != tc.want || why != tc.why {
			t.Errorf("%s: got %q %q, want %q %q", tc.name, want, why, tc.want, tc.why)
		}
	}
}

// conventionalTitleRun runs the implementer with workflow.conventionalCommits
// and the describer's title; mr sets the merge request's title and draft
// state while the describer works.
func conventionalTitleRun(t *testing.T, on bool, title string, mr func(*gitlab.MergeRequest)) (*implementEnv, *describingEngine, *Runner) {
	t.Helper()
	e := newImplementEnv(t)
	e.configure(t, func(p *aiv1.AgentProjectSpec) { p.Workflow.ConventionalCommits = &on }, func(*aiv1.AgentTaskSpec) {})
	engine := &describingEngine{title: title, answer: ImplementAnswer{Status: "done", Summary: "Done.", CommitMessage: "feat(pay): add steady", Handoff: "done"}}
	if mr != nil {
		engine.onDescribe = func() { mr(e.gl.MRs[1]) }
	}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if engine.describes != 1 {
		t.Fatalf("one describer call, got %d", engine.describes)
	}
	var got aiv1.AgentTask
	_ = e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &got)
	if got.Status.Run.LastResult != aiv1.RunResultSucceeded {
		t.Fatalf("the run succeeds: %s", got.Status.Run.LastResult)
	}
	return e, engine, r
}

func describeCall(t *testing.T, engine *describingEngine) Invocation {
	t.Helper()
	for _, c := range engine.calls {
		if c.Role == "describe" {
			return c
		}
	}
	t.Fatal("no describer call")
	return Invocation{}
}

// setTitle is the merge request as a human or an earlier step left it.
func setTitle(title string) func(*gitlab.MergeRequest) {
	return func(mr *gitlab.MergeRequest) { mr.Title, mr.Draft = title, strings.HasPrefix(title, "Draft:") }
}

func TestDescribeSetsTheConventionalTitleOfADraft(t *testing.T) {
	const title = "feat(pay): add the paypal button"
	e, engine, r := conventionalTitleRun(t, true, title, setTitle("Draft: Add PayPal button"))
	if got := e.gl.MRs[1].Title; got != "Draft: "+title {
		t.Fatalf("a draft keeps its prefix: %q", got)
	}
	inv := describeCall(t, engine)
	if !strings.Contains(inv.JSONSchema, `"title"`) || !strings.Contains(inv.Prompt, "Conventional Commits") {
		t.Fatalf("the describer is asked for the title:\n%s\n%s", inv.JSONSchema, inv.Prompt)
	}
	if st, _ := r.loadState(); st.MRTitle != title {
		t.Fatalf("the state remembers the title: %q", st.MRTitle)
	}
}

func TestDescribeSetsTheConventionalTitleOfAReadyMR(t *testing.T) {
	const title = "feat(pay): add the paypal button"
	e, _, _ := conventionalTitleRun(t, true, title, setTitle("Add PayPal button"))
	if got := e.gl.MRs[1].Title; got != title {
		t.Fatalf("a ready MR gets the title without prefix: %q", got)
	}
}

func TestDescribeKeepsATitleRenamedByHand(t *testing.T) {
	e, _, r := conventionalTitleRun(t, true, "feat(pay): add the paypal button", setTitle("Draft: PayPal checkout, take two"))
	if got := e.gl.MRs[1].Title; got != "Draft: PayPal checkout, take two" {
		t.Fatalf("a rename stays: %q", got)
	}
	if st, _ := r.loadState(); st.MRTitle != "" {
		t.Fatalf("nothing was set: %q", st.MRTitle)
	}
}

func TestDescribeKeepsTheTitleForAnInvalidOne(t *testing.T) {
	e, _, _ := conventionalTitleRun(t, true, "Add the PayPal button", setTitle("Draft: Add PayPal button"))
	if got := e.gl.MRs[1].Title; got != "Draft: Add PayPal button" {
		t.Fatalf("an invalid title keeps the current one: %q", got)
	}
	if !strings.Contains(e.gl.MRs[1].Description, "- steady") {
		t.Fatalf("the description is written anyway:\n%s", e.gl.MRs[1].Description)
	}
}

func TestDescribeTitleOff(t *testing.T) {
	e, engine, _ := conventionalTitleRun(t, false, "feat(pay): add the paypal button", setTitle("Draft: Add PayPal button"))
	if got := e.gl.MRs[1].Title; got != "Draft: Add PayPal button" {
		t.Fatalf("without the option the title stays: %q", got)
	}
	if inv := describeCall(t, engine); inv.JSONSchema != describeSchema || strings.Contains(inv.Prompt, "Conventional Commits") {
		t.Fatalf("without the option the schema and prompt stay:\n%s", inv.Prompt)
	}
}
