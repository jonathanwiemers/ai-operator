package runner

import (
	"context"
	"encoding/json"
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
			v = ImplementAnswer{Status: "in_progress", Summary: "Model done, endpoint next.", CommitMessage: "Add the payment model", Handoff: "model",
				Decisions: []Decision{{Title: "Storage", Choice: "a table", Why: "simple"}}}
		default:
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
	if st := r.loadState(); st.LastMRNoteID == 0 {
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
