package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// stepEngine implements in two steps. During the first one a human comments
// on the MR; the last step changes files only if lastChanges is set.
type stepEngine struct {
	gl          *gitlabfake.Fake
	lastChanges bool
	// command also posts an "/agent" comment during step 1; failLast fails
	// the second call; endless never finishes.
	command, failLast, endless bool
	// issueComment also posts a comment on the issue during step 1.
	issueComment bool

	mu      sync.Mutex
	prompts []string
	n       int
}

func (s *stepEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v any = ReviewAnswer{Summary: "fine"}
	if !inv.ReadOnly {
		s.n++
		s.prompts = append(s.prompts, inv.Prompt)
		switch s.n {
		case 1:
			_ = os.WriteFile(filepath.Join(inv.Dir, "model.go"), []byte("package app\n"), 0o644)
			s.gl.AddMRNote(1, gitlab.User{ID: 7, Username: "jonathan"}, "Call it Payment, not PayPalPayment.")
			if s.issueComment {
				s.gl.AddNote(42, gitlab.User{ID: 7, Username: "jonathan"}, "Make it green.")
			}
			if s.command {
				s.gl.AddMRNote(1, gitlab.User{ID: 7, Username: "jonathan"}, "/agent revisit D42-1 use a file")
				s.gl.AddMRNote(1, gitlab.User{ID: 7, Username: "jonathan"}, "and after the command")
			}
			v = ImplementAnswer{Status: "in_progress", Summary: "Model done, endpoint next.", CommitMessage: "Add the payment model", Handoff: "model",
				Decisions: []Decision{{Title: "Storage", Choice: "a table", Why: "simple"}}}
		default:
			if s.failLast {
				return nil, errors.New("engine broke")
			}
			if s.endless {
				_ = os.WriteFile(filepath.Join(inv.Dir, fmt.Sprintf("part%d.go", s.n)), []byte("package app\n"), 0o644)
				v = ImplementAnswer{Status: "in_progress", Summary: "More.", CommitMessage: fmt.Sprintf("Part %d", s.n), Handoff: "more"}
				break
			}
			if s.lastChanges {
				_ = os.WriteFile(filepath.Join(inv.Dir, "endpoint.go"), []byte("package app\n"), 0o644)
			}
			v = ImplementAnswer{Status: "done", Summary: "Model and endpoint.", CommitMessage: "Add the payment endpoint", Handoff: "done",
				Decisions: []Decision{{Title: "Route", Choice: "/payments", Why: "REST"}}}
		}
	}
	b, _ := json.Marshal(v)
	return &Result{SessionID: "sess-steps", Structured: b}, nil
}

func (e *implementEnv) originLog(t *testing.T, branch string) []string {
	t.Helper()
	out := gitCmd(t, e.tmp, "--git-dir", filepath.Join(e.tmp, "origin.git"), "log", "--format=%s", branch)
	return strings.Split(out, "\n")
}

func TestDraftWorkIsPushedInSteps(t *testing.T) {
	ctx := context.Background()
	e := newImplementEnv(t)
	engine := &stepEngine{gl: e.gl, lastChanges: true}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
	var task aiv1.AgentTask
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	run := task.Status.Run
	if run.LastResult != aiv1.RunResultSucceeded {
		t.Fatalf("run failed: %s", run.Summary)
	}
	// Each step is its own commit on the remote branch.
	log := e.originLog(t, run.Branch)
	if len(log) < 3 || log[0] != "Add the payment endpoint" || log[1] != "Add the payment model" {
		t.Fatalf("expected a commit per step: %q", log)
	}
	if engine.n != 2 || !strings.Contains(engine.prompts[1], "Continue with the next step") ||
		!strings.Contains(engine.prompts[1], "Call it Payment, not PayPalPayment.") {
		t.Fatalf("the comment written during the step must reach the next one: %q", engine.prompts)
	}
	// The report has both steps' decisions; the comment counts as seen.
	var report string
	for _, n := range e.gl.MRNotes[1] {
		report += n.Body
	}
	if !strings.Contains(report, "Model and endpoint.") {
		t.Fatalf("expected the final summary: %q", report)
	}
	var decisions string
	for _, n := range e.gl.IssueNotes[42] {
		decisions += n.Body
	}
	if !strings.Contains(decisions, "Storage") || !strings.Contains(decisions, "Route") {
		t.Fatalf("decisions of every step are posted: %q", decisions)
	}
	if st := savedState(t, r); st.LastMRNoteID == 0 {
		t.Fatal("the comment read between steps must count as seen")
	}
}

func TestLastStepPushedAlreadyGetsAPipeline(t *testing.T) {
	ctx := context.Background()
	e := newImplementEnv(t)
	e.gl.NoMRPipelines = true // only branch pipelines
	engine := &stepEngine{gl: e.gl}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
	var task aiv1.AgentTask
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	run := task.Status.Run
	head := gitCmd(t, e.tmp, "--git-dir", filepath.Join(e.tmp, "origin.git"), "rev-parse", run.Branch)
	if run.PushedSHA != head || !slices.Equal(e.gl.BranchPipelines, []string{run.Branch}) {
		t.Fatalf("the head the last step pushed without CI must be followed and get a pipeline: pushed %q head %q pipelines %v",
			run.PushedSHA, head, e.gl.BranchPipelines)
	}
}

func TestStepsOff(t *testing.T) {
	ctx := context.Background()
	e := newImplementEnv(t)
	var project aiv1.AgentProject
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "shop"}, &project)
	off := false
	project.Spec.Workflow.StepCommits = &off
	if err := e.k8s.Update(ctx, &project); err != nil {
		t.Fatal(err)
	}
	engine := &stepEngine{gl: e.gl, lastChanges: true}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
	var task aiv1.AgentTask
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	if engine.n != 1 || task.Status.Run.LastResult != aiv1.RunResultSucceeded {
		t.Fatalf("without steps \"in_progress\" counts as done: %d calls, %s", engine.n, task.Status.Run.Summary)
	}
}

func TestStepsStopAtACommand(t *testing.T) {
	ctx := context.Background()
	e := newImplementEnv(t)
	engine := &stepEngine{gl: e.gl, lastChanges: true, command: true}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
	p := engine.prompts[1]
	if !strings.Contains(p, "Call it Payment") || strings.Contains(p, "/agent revisit") || strings.Contains(p, "after the command") {
		t.Fatalf("comments are handed over up to the first command only: %q", p)
	}
	var cmd int64
	for _, n := range e.gl.MRNotes[1] {
		if strings.HasPrefix(n.Body, "/agent revisit") {
			cmd = n.ID
		}
	}
	if st := savedState(t, r); st.LastMRNoteID == 0 || st.LastMRNoteID >= cmd {
		t.Fatalf("the command is left for the run it starts: last seen %d, command %d", st.LastMRNoteID, cmd)
	}
}

func TestStepDecisionsSurviveAFailedStep(t *testing.T) {
	ctx := context.Background()
	e := newImplementEnv(t)
	engine := &stepEngine{gl: e.gl, failLast: true}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	_, _ = r.Step(ctx)
	var notes string
	for _, n := range e.gl.IssueNotes[42] {
		notes += n.Body
	}
	if !strings.Contains(notes, "Storage") || !strings.Contains(notes, "run failed") {
		t.Fatalf("the pushed step's decisions must be posted despite the failure: %q", notes)
	}
}

func TestOutOfStepsStaysDraft(t *testing.T) {
	ctx := context.Background()
	old := maxSteps
	maxSteps = 3
	t.Cleanup(func() { maxSteps = old })
	e := newImplementEnv(t)
	engine := &stepEngine{gl: e.gl, endless: true}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
	var report string
	for _, n := range e.gl.MRNotes[1] {
		report += n.Body
	}
	if engine.n != 3 || !e.gl.MRs[1].Draft || !strings.Contains(report, "Stopped after 3 steps") {
		t.Fatalf("out of steps: %d calls, draft %v, report %q", engine.n, e.gl.MRs[1].Draft, report)
	}
}

// threeStepEngine implements in three steps, each in a session of its own
// ID. A human comments on the MR during the first step.
type threeStepEngine struct {
	gl *gitlabfake.Fake

	mu   sync.Mutex
	invs []Invocation
}

func (s *threeStepEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v any = ReviewAnswer{Summary: "fine"}
	if inv.Role == "implement" {
		s.invs = append(s.invs, inv)
		n := len(s.invs)
		_ = os.WriteFile(filepath.Join(inv.Dir, fmt.Sprintf("part%d.go", n)), []byte("package app\n"), 0o644)
		a := ImplementAnswer{Status: "in_progress", Summary: fmt.Sprintf("Did part %d, part %d next.", n, n+1),
			CommitMessage: fmt.Sprintf("Add part %d", n), Handoff: fmt.Sprintf("notes after step %d", n)}
		switch n {
		case 1:
			s.gl.AddMRNote(1, gitlab.User{ID: 7, Username: "jonathan"}, "Name the parts in English.")
		case 3:
			a.Status = "done"
		}
		v = a
	}
	b, _ := json.Marshal(v)
	return &Result{SessionID: fmt.Sprintf("sess-%d", len(s.invs)), Structured: b}, nil
}

func runThreeSteps(t *testing.T, mode aiv1.StepSessionMode) (*threeStepEngine, *Runner, aiv1.RunStatus) {
	t.Helper()
	ctx := context.Background()
	e := newImplementEnv(t)
	if mode != "" {
		var project aiv1.AgentProject
		_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "shop"}, &project)
		project.Spec.Agents.Implement.StepSession = mode
		if err := e.k8s.Update(ctx, &project); err != nil {
			t.Fatal(err)
		}
	}
	engine := &threeStepEngine{gl: e.gl}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
	var task aiv1.AgentTask
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	if task.Status.Run.LastResult != aiv1.RunResultSucceeded {
		t.Fatalf("run failed: %s", task.Status.Run.Summary)
	}
	if len(engine.invs) != 3 {
		t.Fatalf("expected three steps, got %d", len(engine.invs))
	}
	return engine, r, task.Status.Run
}

func TestFreshStepSessions(t *testing.T) {
	engine, r, run := runThreeSteps(t, aiv1.StepSessionFresh)
	for i, inv := range engine.invs[1:] {
		step := i + 2
		if inv.Resume != "" {
			t.Errorf("step %d must start a new session, resumes %q", step, inv.Resume)
		}
		p := inv.Prompt
		if !strings.Contains(p, "Implement issue #42") || !strings.Contains(p, "Your earlier steps in this run") ||
			!strings.Contains(p, fmt.Sprintf("notes after step %d", step-1)) ||
			!strings.Contains(p, fmt.Sprintf("Did part %d, part %d next.", step-1, step)) ||
			!strings.Contains(p, "Name the parts in English.") || !strings.Contains(p, "Continue with the next step") {
			t.Errorf("step %d needs the issue, the notes, the earlier steps' summaries and the comments: %q", step, p)
		}
	}
	if p := engine.invs[2].Prompt; !strings.Contains(p, "Did part 1, part 2 next.") || strings.Contains(p, "notes after step 1") {
		t.Errorf("step 3 gets every earlier summary and only the newest notes: %q", p)
	}
	steps := 0
	for _, c := range run.Calls {
		if c.Role == "implement" {
			steps++
			if c.Resumed {
				t.Errorf("a fresh step is recorded as resumed: %+v", c)
			}
		}
	}
	if steps != 3 {
		t.Errorf("expected three recorded implement calls, got %d", steps)
	}
	st := savedState(t, r)
	if got := st.session("implement", aiv1.EngineClaude); got != "sess-3" {
		t.Errorf("the next run resumes the last step's session, state has %q", got)
	}
}

func TestResumedStepSessionsByDefault(t *testing.T) {
	engine, _, run := runThreeSteps(t, "")
	if engine.invs[1].Resume != "sess-1" || engine.invs[2].Resume != "sess-2" {
		t.Fatalf("steps resume the session of the step before: %q, %q", engine.invs[1].Resume, engine.invs[2].Resume)
	}
	if strings.Contains(engine.invs[1].Prompt, "Your earlier steps in this run") {
		t.Errorf("a resumed step gets no summaries: %q", engine.invs[1].Prompt)
	}
	resumed := 0
	for _, c := range run.Calls {
		if c.Role == "implement" && c.Resumed {
			resumed++
		}
	}
	if resumed != 2 {
		t.Errorf("expected the two later steps recorded as resumed, got %d: %+v", resumed, run.Calls)
	}
}
