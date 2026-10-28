package runner

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// cursorLimit is what cursor-agent says when the monthly budget is used up.
const cursorLimit = "ActionRequiredError: You've hit your usage limit You've saved $496 on API model usage this month with Pro. " +
	"Switch to a different model or set a Spend Limit to continue with Auto. Your usage limits will reset when your monthly cycle ends on 11/13/2026."

// recordedEngine records which model each role ran with.
type recordedEngine struct {
	mu    sync.Mutex
	next  Engine
	calls []Invocation
}

func (r *recordedEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	r.mu.Lock()
	r.calls = append(r.calls, inv)
	r.mu.Unlock()
	return r.next.Run(ctx, inv)
}

// models returns the models the role ran with, in order.
func (r *recordedEngine) models(role string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.calls {
		if c.Role == role {
			out = append(out, c.Model)
		}
	}
	return out
}

// resumes returns the sessions the role's calls resumed, in order.
func (r *recordedEngine) resumes(role string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.calls {
		if c.Role == role {
			out = append(out, c.Resume)
		}
	}
	return out
}

// cursorLimitedEnv is a limit env whose engine refuses every cursor call
// (model "auto") with cursor's monthly limit.
func cursorLimitedEnv(t *testing.T, agents func(*aiv1.AgentsSpec), status func(*aiv1.AgentProjectStatus)) (*limitEnv, *limitedEngine, *recordedEngine) {
	t.Helper()
	rec := &recordedEngine{next: &scriptedEngine{}}
	engine := &limitedEngine{next: rec, message: cursorLimit, limitIf: func(inv Invocation) bool { return inv.Model == "auto" }}
	e := newLimitEnv(t, engine)
	var project aiv1.AgentProject
	ctx := context.Background()
	if err := e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "shop"}, &project); err != nil {
		t.Fatal(err)
	}
	agents(&project.Spec.Agents)
	if err := e.k8s.Update(ctx, &project); err != nil {
		t.Fatal(err)
	}
	if status != nil {
		status(&project.Status)
		if err := e.k8s.Status().Update(ctx, &project); err != nil {
			t.Fatal(err)
		}
	}
	return e, engine, rec
}

func sonnetFallback() *aiv1.RoleFallback {
	return &aiv1.RoleFallback{Engine: aiv1.EngineClaude, Model: "sonnet"}
}

// allNotes are the issue's and the merge request's comments.
func (e *limitEnv) allNotes() string {
	var b strings.Builder
	for _, n := range e.gl.IssueNotes[42] {
		b.WriteString(n.Body + "\n")
	}
	for _, n := range e.gl.MRNotes[1] {
		b.WriteString(n.Body + "\n")
	}
	return b.String()
}

func TestCursorLimitedReviewContinuesOnClaude(t *testing.T) {
	e, engine, rec := cursorLimitedEnv(t, func(a *aiv1.AgentsSpec) {
		a.Review.Engine, a.Review.Model, a.Review.Fallback = aiv1.EngineCursor, "auto", sonnetFallback()
	}, nil)
	if !e.step() {
		t.Fatal("the run must not be deferred: the reviewers have a fallback")
	}
	run := e.task().Status.Run
	if run.LastResult == aiv1.RunResultDeferred || run.DeferredUntil != nil || run.RunCount != 1 {
		t.Fatalf("expected a finished run, got %+v", run)
	}
	for _, reviewer := range []string{"bugbot", "security"} {
		models := rec.models(reviewer)
		if len(models) == 0 || models[len(models)-1] != "claude-sonnet-5-5" {
			t.Errorf("%s must review on claude-sonnet-5-5, ran %v", reviewer, models)
		}
	}
	// Both reviewers start in parallel, so both may meet the limit once;
	// the fix round's reviews know it and skip cursor.
	if engine.limited == 0 || engine.limited > 2 {
		t.Errorf("cursor must be refused once or twice, then skipped: %d", engine.limited)
	}
	if run.ReviewErrors != 0 || run.BlockingFindings != 0 {
		t.Errorf("the reviews on the fallback count: %d errors, %d blocking", run.ReviewErrors, run.BlockingFindings)
	}
	reset := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	if l := run.UsageLimits; len(l) != 1 || l[0].Engine != aiv1.EngineCursor || !l[0].Until.Time.Equal(reset) {
		t.Fatalf("the task must publish cursor's limit until its reset, got %+v", l)
	}
	if notes := e.allNotes(); !strings.Contains(notes, "↪️ `cursor` reached its usage limit (until 2026-11-13 00:00 UTC), so bugbot ran on `claude/claude-sonnet-5-5` instead.") {
		t.Fatalf("the report must say which reviewer ran on the fallback:\n%s", notes)
	}
}

func TestFastProfileImplementsOnClaudeWhileCursorIsLimited(t *testing.T) {
	e, _, rec := cursorLimitedEnv(t, func(a *aiv1.AgentsSpec) {
		a.Profiles = []aiv1.AgentProfile{{Name: "fast", RoleSpec: aiv1.RoleSpec{Engine: aiv1.EngineCursor, Model: "auto", Fallback: sonnetFallback()}}}
	}, nil)
	e.gl.SetLabels(42, "agent::working", "agent-profile::fast")
	// An earlier run left a cursor session; claude cannot resume it.
	var st state
	st.setSession("implement", aiv1.EngineCursor, "sess-cursor")
	st.ImplementEngine = aiv1.EngineCursor
	if err := e.r.saveState(st); err != nil {
		t.Fatal(err)
	}
	if !e.step() {
		t.Fatal("the run must not be deferred: the profile has a fallback")
	}
	run := e.task().Status.Run
	if run.LastResult == aiv1.RunResultDeferred || run.PushedSHA == "" || run.MergeRequestIID == 0 {
		t.Fatalf("expected the implementation pushed, got %+v", run)
	}
	if models := rec.models("implement"); len(models) != 2 || models[0] != "claude-sonnet-5-5" || models[1] != "claude-sonnet-5-5" {
		t.Fatalf("the implementer and its review fix round must run on claude-sonnet-5-5, ran %v", models)
	}
	// The fallback starts fresh instead of resuming cursor's session; the
	// fix round resumes the fallback's own session.
	if resumes := rec.resumes("implement"); resumes[0] != "" || resumes[1] != "sess-impl" {
		t.Fatalf("expected a fresh claude session, then its resume, got %q", resumes)
	}
	st = e.r.loadState()
	// claude continued the work: back on cursor, its old session is dropped.
	if st.ImplementEngine != aiv1.EngineClaude || st.session("implement", aiv1.EngineClaude) != "sess-impl" {
		t.Fatalf("the session belongs to claude: %+v", st)
	}
	if notes := e.allNotes(); !strings.Contains(notes, "so implement ran on `claude/claude-sonnet-5-5` instead") ||
		!strings.Contains(notes, "claude/claude-sonnet-5-5 (fallback for cursor)") {
		t.Fatalf("the report must name the fallback:\n%s", notes)
	}
}

func TestFallbackWhoseEngineIsLimitedDefers(t *testing.T) {
	claudeReset := time.Date(2026, 10, 16, 15, 0, 0, 0, time.UTC)
	e, _, rec := cursorLimitedEnv(t, func(a *aiv1.AgentsSpec) {
		a.Implement = aiv1.RoleSpec{Engine: aiv1.EngineCursor, Model: "auto", Fallback: sonnetFallback()}
	}, func(s *aiv1.AgentProjectStatus) {
		s.UsageLimits = []aiv1.UsageLimit{{Engine: aiv1.EngineClaude, Until: metav1.NewTime(claudeReset)}}
	})
	if e.step() {
		t.Fatal("with both engines limited the run is deferred")
	}
	run := e.task().Status.Run
	if run.LastResult != aiv1.RunResultDeferred || run.LimitedEngine != aiv1.EngineCursor || run.DeferredUntil == nil {
		t.Fatalf("expected a deferral on cursor's limit, got %+v", run)
	}
	if models := rec.models("implement"); len(models) != 0 {
		t.Fatalf("claude is limited: nothing may run, ran %v", models)
	}
}

func TestProjectWideLimitGoesStraightToTheFallback(t *testing.T) {
	e, engine, rec := cursorLimitedEnv(t, func(a *aiv1.AgentsSpec) {
		a.Implement = aiv1.RoleSpec{Engine: aiv1.EngineCursor, Model: "auto", Fallback: sonnetFallback()}
	}, func(s *aiv1.AgentProjectStatus) {
		s.UsageLimits = []aiv1.UsageLimit{{Engine: aiv1.EngineCursor, Until: metav1.NewTime(time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)), Message: "monthly"}}
	})
	if !e.step() {
		t.Fatal("expected a run on the fallback")
	}
	if engine.limited != 0 {
		t.Fatalf("a known limit must not be hit again: cursor refused %d calls", engine.limited)
	}
	if models := rec.models("implement"); len(models) == 0 || models[0] != "claude-sonnet-5-5" {
		t.Fatalf("expected the implementer on the fallback, ran %v", models)
	}
}

func TestInvalidFallbackIsIgnored(t *testing.T) {
	e, _, _ := cursorLimitedEnv(t, func(a *aiv1.AgentsSpec) {
		a.Implement = aiv1.RoleSpec{Engine: aiv1.EngineCursor, Model: "auto", Fallback: &aiv1.RoleFallback{Engine: aiv1.EngineClaude, Model: "gpt-9"}}
	}, nil)
	if e.step() {
		t.Fatal("a fallback that names no allowed model is ignored: the run is deferred")
	}
	if run := e.task().Status.Run; run.LimitedEngine != aiv1.EngineCursor {
		t.Fatalf("expected a deferral on cursor's limit, got %+v", run)
	}
}
