package runner

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// sessionsEngine gives every implementer call its own session, sess-1,
// sess-2, …, and records the implementer's calls.
type sessionsEngine struct {
	next  Engine
	mu    sync.Mutex
	calls []Invocation
}

func (s *sessionsEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	res, err := s.next.Run(ctx, inv)
	if inv.Role != "implement" {
		return res, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, inv)
	if res != nil && res.SessionID != "" {
		res.SessionID = "sess-" + strconv.Itoa(len(s.calls))
	}
	return res, err
}

func (s *sessionsEngine) implementCalls() []Invocation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Invocation(nil), s.calls...)
}

func withConflictRole(c *aiv1.ConflictRoleSpec) func(*aiv1.AgentProject) {
	return func(p *aiv1.AgentProject) { p.Spec.Agents.Implement.Conflict = c }
}

// conflictCalls are the run's recorded calls of kind conflict.
func conflictCalls(task aiv1.AgentTask) []aiv1.AgentCall {
	var out []aiv1.AgentCall
	for _, c := range task.Status.Run.Calls {
		if c.Kind == aiv1.CallKindConflict {
			out = append(out, c)
		}
	}
	return out
}

// rerun starts another run of the task, without a merge.
func rerun(t *testing.T, e *implementEnv, r *Runner, request int64) {
	t.Helper()
	ctx := context.Background()
	var got aiv1.AgentTask
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &got)
	orig := got.DeepCopy()
	got.Spec.RunRequest = request
	if err := e.k8s.Patch(ctx, &got, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestConflictWithoutConflictRoleResumesTheImplementer(t *testing.T) {
	engine := &sessionsEngine{next: &conflictEngine{resolution: "package app // both\n"}}
	_, task, r := conflictRunWith(t, engine, true, func(*aiv1.AgentProject) {})
	calls := engine.implementCalls()
	if len(calls) != 2 || calls[1].Model != aiv1.DefaultModel || calls[1].Resume != "sess-1" || !strings.HasPrefix(calls[1].Prompt, noStepsNote) {
		t.Fatalf("the implementer resolves in its own session: %+v", calls)
	}
	if st := savedState(t, r); st.session("implement", aiv1.EngineClaude) != "sess-2" {
		t.Fatalf("the resumed session goes on: %+v", st.Sessions)
	}
	if c := conflictCalls(task); len(c) != 1 || !c[0].Resumed || c[0].Model != aiv1.DefaultModel {
		t.Fatalf("one resumed conflict call on the implementer's model: %+v", c)
	}
}

func TestConflictRoleRunsInAFreshSession(t *testing.T) {
	engine := &sessionsEngine{next: &conflictEngine{resolution: "package app // both\n"}}
	e, task, r := conflictRunWith(t, engine, true, withConflictRole(&aiv1.ConflictRoleSpec{RoleSpec: aiv1.RoleSpec{Model: "claude-sonnet-5-5"}}))
	if got := task.Status.Run.LastResult; got != aiv1.RunResultSucceeded {
		t.Fatalf("the conflicts are resolved: %s", got)
	}
	calls := engine.implementCalls()
	if len(calls) != 2 {
		t.Fatalf("one implement call, one conflict call: %+v", calls)
	}
	c := calls[1]
	if c.Model != "claude-sonnet-5-5" || c.Resume != "" {
		t.Fatalf("the conflict call runs on sonnet in a new session: model %q, resume %q", c.Model, c.Resume)
	}
	for _, want := range []string{"Resolve the merge conflicts of the merge request for issue #42", "## Add PayPal button",
		"## Changes of the merge request", "steady.go | ", "- `steady.go`", "the runner concludes the merge"} {
		if !strings.Contains(c.Prompt, want) {
			t.Errorf("the fresh prompt misses %q:\n%s", want, c.Prompt)
		}
	}
	if strings.Contains(c.Prompt, noStepsNote) || !strings.HasPrefix(c.SystemPrompt, implementSystemPrompt) {
		t.Errorf("a fresh call gets the implementer's system prompt, not the resume note:\n%s", c.Prompt)
	}
	cc := conflictCalls(task)
	if len(cc) != 1 || cc[0].Model != "claude-sonnet-5-5" || cc[0].Resumed || cc[0].Engine != aiv1.EngineClaude {
		t.Fatalf("agent_calls: one conflict call on sonnet, not resumed: %+v", cc)
	}
	st := savedState(t, r)
	if st.session("implement", aiv1.EngineClaude) != "sess-1" || st.ImplementEngine != aiv1.EngineClaude {
		t.Fatalf("the implementer's session stays: %+v", st.Sessions)
	}
	var found bool
	for _, s := range r.run.steps {
		if s.name == "run implementer" && strings.Contains(s.result, "claude/claude-sonnet-5-5") {
			found = true
		}
	}
	if !found {
		t.Errorf("the run log names the conflict role's model: %+v", r.run.steps)
	}

	// The next implement call resumes the implementer's session.
	rerun(t, e, r, 3)
	calls = engine.implementCalls()
	if len(calls) != 3 || calls[2].Resume != "sess-1" || calls[2].Model != aiv1.DefaultModel {
		t.Fatalf("the next run resumes sess-1 on the implementer's model: %+v", calls[len(calls)-1])
	}
}

func TestConflictRoleResumesWithoutFreshSession(t *testing.T) {
	engine := &sessionsEngine{next: &conflictEngine{resolution: "package app // both\n"}}
	no := false
	_, task, r := conflictRunWith(t, engine, true, withConflictRole(&aiv1.ConflictRoleSpec{FreshSession: &no, RoleSpec: aiv1.RoleSpec{Model: "claude-sonnet-5-5"}}))
	calls := engine.implementCalls()
	if len(calls) != 2 || calls[1].Model != "claude-sonnet-5-5" || calls[1].Resume != "sess-1" {
		t.Fatalf("freshSession false resumes the implementer's session on sonnet: %+v", calls)
	}
	if st := savedState(t, r); st.session("implement", aiv1.EngineClaude) != "sess-2" {
		t.Fatalf("the resumed session goes on: %+v", st.Sessions)
	}
	if c := conflictCalls(task); len(c) != 1 || !c[0].Resumed {
		t.Fatalf("one resumed conflict call: %+v", c)
	}
}

func TestConflictRoleFallsBackWhileItsEngineIsLimited(t *testing.T) {
	sessions := &sessionsEngine{next: &conflictEngine{resolution: "package app // both\n"}}
	engine := &limitedEngine{next: sessions, message: cursorLimit, limitIf: func(inv Invocation) bool { return inv.Model == "auto" }}
	_, task, r := conflictRunWith(t, engine, true, withConflictRole(&aiv1.ConflictRoleSpec{RoleSpec: aiv1.RoleSpec{
		Engine: aiv1.EngineCursor, Model: "auto", Fallback: sonnetFallback()}}))
	if got := task.Status.Run.LastResult; got != aiv1.RunResultSucceeded {
		t.Fatalf("the fallback resolves the conflicts: %s %+v", got, task.Status.Run)
	}
	if engine.limited != 1 {
		t.Fatalf("cursor is refused once: %d", engine.limited)
	}
	calls := sessions.implementCalls()
	if len(calls) != 2 || calls[1].Model != "claude-sonnet-5-5" || calls[1].Resume != "" ||
		!strings.Contains(calls[1].Prompt, "Resolve the merge conflicts of the merge request") {
		t.Fatalf("the conflict call runs on the claude fallback, fresh: %+v", calls)
	}
	cc := conflictCalls(task)
	if len(cc) != 2 || cc[0].Engine != aiv1.EngineCursor || cc[1].Engine != aiv1.EngineClaude || cc[1].FallbackFrom != aiv1.EngineCursor {
		t.Fatalf("both conflict calls are recorded, the second as the fallback: %+v", cc)
	}
	if st := savedState(t, r); st.session("implement", aiv1.EngineClaude) != "sess-1" || st.ImplementEngine != aiv1.EngineClaude {
		t.Fatalf("the implementer's session stays: %+v %s", st.Sessions, st.ImplementEngine)
	}
}

func TestConflictRoleCutOffLeavesACheckpoint(t *testing.T) {
	reset := time.Now().Add(time.Hour).Truncate(time.Second)
	sessions := &sessionsEngine{next: &conflictEngine{resolution: "package app // both\n"}}
	engine := &cutEngine{next: sessions, cutIf: func(_ int, inv Invocation) bool { return strings.Contains(inv.Prompt, "## Merge conflicts") },
		fail: errors.New("claude reported an error: Claude AI usage limit reached|" + strconv.FormatInt(reset.Unix(), 10))}
	e, task, r := conflictRunWith(t, engine, true, withConflictRole(&aiv1.ConflictRoleSpec{RoleSpec: aiv1.RoleSpec{Model: "claude-sonnet-5-5"}}))
	run := task.Status.Run
	if run.LastResult != aiv1.RunResultDeferred {
		t.Fatalf("the usage limit defers the run: %+v", run)
	}
	// As for the implementer: the checkpoint aborts the merge in progress
	// and commits nothing, half-resolved conflicts are never committed.
	var saved string
	for _, s := range r.run.steps {
		if s.name == "save checkpoint" {
			saved = s.result
		}
	}
	if saved != "nothing saved: the merge in progress was aborted" || run.Checkpoint != nil {
		t.Fatalf("the cut-off conflict call checkpoints: %q, %+v", saved, run.Checkpoint)
	}
	if !strings.Contains(lastMRNote(e), "A merge was in progress; it was aborted and nothing was saved") {
		t.Fatalf("the report names the aborted merge:\n%s", lastMRNote(e))
	}
	cc := conflictCalls(task)
	if len(cc) != 1 || cc[0].Model != "claude-sonnet-5-5" || cc[0].Resumed {
		t.Fatalf("one fresh conflict call on sonnet: %+v", cc)
	}
	if st := savedState(t, r); st.session("implement", aiv1.EngineClaude) != "sess-1" {
		t.Fatalf("the implementer's session stays: %+v", st.Sessions)
	}
}

func TestConflictRoleNotForRunsWithMoreInput(t *testing.T) {
	ctx := context.Background()
	_, run := conflictPhaseRun(t, &conflictEngine{resolution: "package app // both\n"})
	run.spec.Agents.Implement.Conflict = &aiv1.ConflictRoleSpec{RoleSpec: aiv1.RoleSpec{Model: "claude-sonnet-5-5"}}
	phases(t, run, upToPrompt(run)...)
	if _, _, _, _, ok := run.conflictCall(ctx); !ok {
		t.Fatal("a conflict-only run uses the conflict role")
	}
	run.conflictOnly = false // e.g. a new comment came with the conflict
	if _, _, _, _, ok := run.conflictCall(ctx); ok {
		t.Fatal("a run with more input for the implementer keeps the implementer")
	}
}
