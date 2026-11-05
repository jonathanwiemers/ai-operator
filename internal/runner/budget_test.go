package runner

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// stopEngine wraps the two-step implementer: after implementer call stopAt
// it sets spec.budgetStop (the call itself finishes), or with limit the
// call after it hits a usage limit instead.
type stopEngine struct {
	next   *stepEngine
	e      *implementEnv
	stopAt int
	limit  bool

	mu        sync.Mutex
	calls     int
	reviewers int
	resumes   []string
}

func (s *stopEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	s.mu.Lock()
	if inv.ReadOnly {
		s.reviewers++
		s.mu.Unlock()
		return s.next.Run(ctx, inv)
	}
	s.calls++
	n := s.calls
	s.resumes = append(s.resumes, inv.Resume)
	s.mu.Unlock()
	if s.limit && n == s.stopAt+1 {
		return nil, errors.New("Claude AI usage limit reached")
	}
	res, err := s.next.Run(ctx, inv)
	if !s.limit && n == s.stopAt {
		s.e.setBudgetStop(ctx, true)
	}
	return res, err
}

func (e *implementEnv) setBudgetStop(ctx context.Context, stop bool) {
	var task aiv1.AgentTask
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	task.Spec.BudgetStop = stop
	if err := e.k8s.Update(ctx, &task); err != nil {
		panic(err)
	}
}

func (e *implementEnv) run(ctx context.Context) aiv1.RunStatus {
	var task aiv1.AgentTask
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	return task.Status.Run
}

// stoppedAfterStep runs the two-step implementer until it is stopped after
// its first step, by the budget or by a usage limit.
func stoppedAfterStep(t *testing.T, limit bool) (*implementEnv, *Runner, *stopEngine) {
	t.Helper()
	e := newImplementEnv(t)
	engine := &stopEngine{next: &stepEngine{gl: e.gl, lastChanges: true}, e: e, stopAt: 1, limit: limit}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if ran, err := r.Step(context.Background()); ran || err != nil {
		t.Fatalf("a stopped run must not count: ran=%v err=%v", ran, err)
	}
	return e, r, engine
}

func TestBudgetStopEndsTheRunBetweenSteps(t *testing.T) {
	ctx := context.Background()
	e, r, engine := stoppedAfterStep(t, false)
	if engine.calls != 1 || engine.reviewers != 0 {
		t.Fatalf("no call after the budget stop: %d implementer calls, %d reviewer calls", engine.calls, engine.reviewers)
	}
	run := e.run(ctx)
	if run.LastResult != aiv1.RunResultDeferred || !strings.Contains(run.Summary, "budget is used") ||
		run.DeferredUntil != nil || run.BlockReason != "" || run.Deferrals != 0 {
		t.Fatalf("the run must end deferred for the budget: %+v", run)
	}
	if run.ObservedRunRequest != 0 || run.RunCount != 0 || run.ActiveRunRequest != 0 {
		t.Fatalf("the request must stay pending and nothing in progress: %+v", run)
	}
	if notes := e.gl.MRNotes[1]; !slices.ContainsFunc(notes, func(n gitlab.Note) bool { return strings.Contains(n.Body, "the budget is used") }) {
		t.Fatalf("the progress comment should say the budget stopped the run: %+v", notes)
	}

	// The same branch and session a usage limit in the middle of the steps
	// leaves for the next run.
	le, lr, _ := stoppedAfterStep(t, true)
	if lrun := le.run(ctx); lrun.LastResult != aiv1.RunResultDeferred || lrun.LimitedEngine == "" {
		t.Fatalf("the usage limit must defer the run: %+v", lrun)
	}
	if got, want := e.originLog(t, run.Branch), le.originLog(t, run.Branch); !slices.Equal(got, want) || got[0] != "Add the payment model" {
		t.Fatalf("the pushed step must stay like after a usage limit: %q, usage limit %q", got, want)
	}
	st, lst := savedState(t, r), savedState(t, lr)
	if got, want := st.session("implement", aiv1.EngineClaude), lst.session("implement", aiv1.EngineClaude); got != want || got != "sess-steps" {
		t.Fatalf("the session must stay like after a usage limit: %q, usage limit %q", got, want)
	}

	// Nothing runs while the flag is set; once cleared the run continues
	// the session from the step it stopped after.
	if ran, _ := r.Step(ctx); ran || engine.calls != 1 {
		t.Fatalf("no run while the budget is used: ran=%v, %d calls", ran, engine.calls)
	}
	e.setBudgetStop(ctx, false)
	if ran, err := r.Step(ctx); !ran || err != nil {
		t.Fatalf("the run should continue once the budget is raised: ran=%v err=%v", ran, err)
	}
	if engine.resumes[1] != "sess-steps" {
		t.Fatalf("the next run must resume the session: %q", engine.resumes)
	}
	run = e.run(ctx)
	if run.LastResult != aiv1.RunResultSucceeded || run.ObservedRunRequest != 1 {
		t.Fatalf("the continued run should succeed: %+v", run)
	}
	if log := e.originLog(t, run.Branch); !slices.Contains(log, "Add the payment model") {
		t.Fatalf("the continued run builds on the pushed step: %q", log)
	}
}

func TestBudgetStopBeforeTheReview(t *testing.T) {
	ctx := context.Background()
	e := newImplementEnv(t)
	engine := &stopEngine{next: &stepEngine{gl: e.gl, lastChanges: true}, e: e, stopAt: 2}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if ran, err := r.Step(ctx); ran || err != nil {
		t.Fatalf("a stopped run must not count: ran=%v err=%v", ran, err)
	}
	if engine.calls != 2 || engine.reviewers != 0 {
		t.Fatalf("the reviewers must not run after the budget stop: %d implementer calls, %d reviewer calls", engine.calls, engine.reviewers)
	}
	if run := e.run(ctx); run.LastResult != aiv1.RunResultDeferred || run.ReviewErrors != 0 {
		t.Fatalf("the run must end deferred, not with reviewer errors: %+v", run)
	}
}
