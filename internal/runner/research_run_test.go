package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/feeds"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

func (a *researchAgent) Ready(context.Context) error { return nil }

var researchNow = time.Date(2026, 10, 29, 7, 0, 0, 0, time.UTC)

type researchRunEnv struct {
	k8s   client.Client
	owner *store.Store
	rr    *ResearchRun
	runID string
}

// newResearchRunEnv is a news flash research whose current run the operator
// started on claude, and a runner for it. With db the store is a real one,
// connected as a writer role; without, connecting fails the test.
func newResearchRunEnv(t *testing.T, db bool, agent Agent, opts ...interceptor.Funcs) *researchRunEnv {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = aiv1.AddToScheme(scheme)
	runID := uuid.NewString()
	started := metav1.Time{Time: researchNow.Add(-time.Minute)}
	res := &aiv1.AgentResearch{
		ObjectMeta: metav1.ObjectMeta{Name: "news", Namespace: "agents"},
		Spec: aiv1.AgentResearchSpec{ProjectRef: "shop", Prompt: "The latest news.", Topics: []string{"Go"},
			View: aiv1.ResearchViewNewsflash, Depth: aiv1.ResearchDepthQuick},
		Status: aiv1.AgentResearchStatus{Phase: aiv1.ResearchPhaseRunning, Run: &aiv1.ResearchRunStatus{
			ID: runID, StartedAt: &started, Engine: aiv1.EngineClaude, Model: "claude-sonnet-5-5", Effort: "medium",
			Reason: "chosen first"}},
	}
	b := fake.NewClientBuilder().WithScheme(scheme).WithObjects(res).WithStatusSubresource(&aiv1.AgentResearch{})
	for _, o := range opts {
		b = b.WithInterceptorFuncs(o)
	}
	e := &researchRunEnv{k8s: b.Build(), runID: runID}
	open := func(context.Context) (*store.Store, error) {
		t.Error("the runner must not connect to the database")
		return nil, errors.New("no database")
	}
	if db {
		e.owner = storetest.New(t)
		role := "agents.research_writer_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
		writer := storetest.Role(t, role)
		if err := e.owner.Migrate(t.Context(), store.Grants{Writers: []string{role}}); err != nil {
			t.Fatal(err)
		}
		open = func(context.Context) (*store.Store, error) { return writer, nil }
	}
	e.rr = &ResearchRun{K8s: e.k8s, Namespace: "agents", Name: "news", RunID: runID, Agent: agent,
		OpenStore: open, Log: logr.Discard(), now: func() time.Time { return researchNow }}
	return e
}

func (e *researchRunEnv) run(t *testing.T) *aiv1.ResearchRunStatus {
	t.Helper()
	if err := e.rr.Run(t.Context()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var res aiv1.AgentResearch
	if err := e.k8s.Get(t.Context(), types.NamespacedName{Namespace: "agents", Name: "news"}, &res); err != nil {
		t.Fatal(err)
	}
	return res.Status.Run
}

func TestResearchRunSucceeded(t *testing.T) {
	usage := &aiv1.EngineUsage{Status: "allowed", Windows: []aiv1.UsageWindow{{Name: "five_hour", Percent: 40}}}
	agent := &researchAgent{answers: []func(Invocation) (*Result, error){func(Invocation) (*Result, error) {
		return &Result{Text: validAnswer, CostUSD: 0.4, Usage: usage}, nil
	}}}
	e := newResearchRunEnv(t, true, agent)
	run := e.run(t)

	if status, msg, ok := storetest.RunStatus(t, e.runID); !ok || status != store.StatusSucceeded || msg != "" {
		t.Errorf("store run = %q %q (found %v), want succeeded", status, msg, ok)
	}
	if n := storetest.Count(t, "research_results", e.runID); n != 1 {
		t.Errorf("%d results, want 1", n)
	}
	if n := storetest.Count(t, "research_items", e.runID); n != 1 {
		t.Errorf("%d items, want 1", n)
	}
	if run.Outcome != aiv1.ResearchOutcomeSucceeded || run.Message != "" || run.CostUSD != "0.4000" {
		t.Errorf("status.run = %+v, want succeeded at 0.4 USD", run)
	}
	if run.FinishedAt != nil || run.StartedAt == nil || run.Engine != aiv1.EngineClaude || run.Reason != "chosen first" {
		t.Errorf("the operator's fields must stay as they are: %+v", run)
	}
	want := []aiv1.EngineUsage{{Engine: aiv1.EngineClaude, Status: "allowed", Windows: usage.Windows,
		ObservedAt: metav1.Time{Time: researchNow}}}
	if len(run.EngineUsage) != 1 || run.EngineUsage[0].Engine != want[0].Engine ||
		!run.EngineUsage[0].ObservedAt.Equal(&want[0].ObservedAt) || run.EngineUsage[0].Windows[0].Percent != 40 {
		t.Errorf("engineUsage = %+v, want %+v", run.EngineUsage, want)
	}
	if len(run.UsageLimits) != 0 {
		t.Errorf("no limit expected: %+v", run.UsageLimits)
	}
	// The prompt names the previous flash's URLs: none for the first.
	if strings.Contains(agent.calls[0].Prompt, "previous flash") {
		t.Error("the first run has no previous URLs")
	}
}

func TestResearchRunSecondFlashGetsPreviousURLs(t *testing.T) {
	agent := &researchAgent{answers: []func(Invocation) (*Result, error){textAnswer(validAnswer)}}
	e := newResearchRunEnv(t, true, agent)
	prev := store.Run{ID: uuid.NewString(), Namespace: "agents", Research: "news", StartedAt: researchNow.Add(-24 * time.Hour),
		Engine: "claude", ExpiresAt: researchNow.Add(time.Hour)}
	if err := e.owner.StartRun(t.Context(), prev); err != nil {
		t.Fatal(err)
	}
	old, err := RunResearch(t.Context(), &researchAgent{answers: []func(Invocation) (*Result, error){textAnswer(validAnswer)}}, researchRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.owner.SaveResult(t.Context(), prev.ID, old.Result, prev.StartedAt); err != nil {
		t.Fatal(err)
	}
	e.run(t)
	if !strings.Contains(agent.calls[0].Prompt, "- https://go.dev/blog/rc\n") {
		t.Errorf("the previous run's URLs must be in the prompt:\n%s", agent.calls[0].Prompt)
	}
}

func TestResearchRunDegraded(t *testing.T) {
	agent := &researchAgent{answers: []func(Invocation) (*Result, error){textAnswer(invalidAnswer), textAnswer(invalidAnswer)}}
	e := newResearchRunEnv(t, true, agent)
	run := e.run(t)
	if status, msg, _ := storetest.RunStatus(t, e.runID); status != store.StatusDegraded || msg != "" {
		t.Errorf("store run = %q %q, want degraded without an error", status, msg)
	}
	if n := storetest.Count(t, "research_results", e.runID); n != 1 {
		t.Errorf("a degraded run keeps its answer: %d results", n)
	}
	if run.Outcome != aiv1.ResearchOutcomeDegraded || !strings.HasPrefix(run.Message, "invalid answer: ") || run.CostUSD != "0.5000" {
		t.Errorf("status.run = %+v, want degraded with the validation errors", run)
	}
}

func TestResearchRunUsageLimit(t *testing.T) {
	reset := researchNow.Add(3 * time.Hour)
	for name, tc := range map[string]struct {
		msg  string
		want time.Time
	}{
		"with a reset":    {fmt.Sprintf("Claude AI usage limit reached|%d", reset.Unix()), reset},
		"without a reset": {"You've hit your usage limit", researchNow.Add(researchLimitBackoff)},
	} {
		t.Run(name, func(t *testing.T) {
			agent := &researchAgent{answers: []func(Invocation) (*Result, error){func(Invocation) (*Result, error) {
				return nil, errors.New(tc.msg)
			}}}
			e := newResearchRunEnv(t, true, agent)
			run := e.run(t)
			if status, msg, _ := storetest.RunStatus(t, e.runID); status != store.StatusLimited || !strings.Contains(msg, "usage limit") {
				t.Errorf("store run = %q %q, want limited", status, msg)
			}
			if n := storetest.Count(t, "research_results", e.runID); n != 0 {
				t.Errorf("a limited run has no result: %d", n)
			}
			if run.Outcome != aiv1.ResearchOutcomeLimited || len(run.UsageLimits) != 1 ||
				run.UsageLimits[0].Engine != aiv1.EngineClaude || !run.UsageLimits[0].Until.Time.Equal(tc.want) {
				t.Errorf("status.run = %+v, want limited until %s", run, tc.want)
			}
		})
	}
}

func TestResearchRunFailed(t *testing.T) {
	agent := &researchAgent{answers: []func(Invocation) (*Result, error){func(Invocation) (*Result, error) {
		return &Result{CostUSD: 0.1}, errors.New("claude exited with code 1")
	}}}
	e := newResearchRunEnv(t, true, agent)
	run := e.run(t)
	if status, msg, _ := storetest.RunStatus(t, e.runID); status != store.StatusFailed || !strings.Contains(msg, "exited with code 1") {
		t.Errorf("store run = %q %q, want failed with the error", status, msg)
	}
	if run.Outcome != aiv1.ResearchOutcomeFailed || !strings.Contains(run.Message, "exited with code 1") || run.CostUSD != "0.1000" {
		t.Errorf("status.run = %+v, want failed", run)
	}
}

func TestResearchRunStalePod(t *testing.T) {
	for name, mutate := range map[string]func(*aiv1.ResearchRunStatus){
		"another run":      func(r *aiv1.ResearchRunStatus) { r.ID = uuid.NewString() },
		"already reported": func(r *aiv1.ResearchRunStatus) { r.Outcome = aiv1.ResearchOutcomeSucceeded },
		"finished":         func(r *aiv1.ResearchRunStatus) { r.FinishedAt = &metav1.Time{Time: researchNow} },
	} {
		t.Run(name, func(t *testing.T) {
			agent := &researchAgent{}
			e := newResearchRunEnv(t, false, agent)
			var res aiv1.AgentResearch
			if err := e.k8s.Get(t.Context(), types.NamespacedName{Namespace: "agents", Name: "news"}, &res); err != nil {
				t.Fatal(err)
			}
			mutate(res.Status.Run)
			if err := e.k8s.Status().Update(t.Context(), &res); err != nil {
				t.Fatal(err)
			}
			before := *res.Status.Run
			run := e.run(t)
			if len(agent.calls) != 0 {
				t.Error("a stale pod must not call the agent")
			}
			if run.Outcome != before.Outcome || run.Message != "" || run.CostUSD != "" {
				t.Errorf("a stale pod must not write the status: %+v", run)
			}
		})
	}
}

func TestResearchRunWithoutDatabaseReportsInStatus(t *testing.T) {
	agent := &researchAgent{}
	e := newResearchRunEnv(t, false, agent)
	e.rr.OpenStore = func(context.Context) (*store.Store, error) {
		return nil, errors.New("research database: connection refused")
	}
	run := e.run(t)
	if len(agent.calls) != 0 {
		t.Error("without the database the agent must not run")
	}
	if run.Outcome != aiv1.ResearchOutcomeFailed || !strings.Contains(run.Message, "connection refused") {
		t.Errorf("status.run = %+v, want failed with the database error", run)
	}
}

func TestResearchRunNotRecorded(t *testing.T) {
	e := newResearchRunEnv(t, false, &researchAgent{}, interceptor.Funcs{
		SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
			return errors.New("forbidden")
		},
	})
	e.rr.OpenStore = func(context.Context) (*store.Store, error) { return nil, errors.New("down") }
	if err := e.rr.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("Run = %v, want an error when nothing could be recorded", err)
	}
}

type fakeFeeds struct {
	results []feeds.Result
	urls    []string
}

func (f *fakeFeeds) FetchAll(_ context.Context, urls []string) []feeds.Result {
	f.urls = append([]string{}, urls...)
	return f.results
}

func TestResearchRunFetchesFeeds(t *testing.T) {
	agent := &researchAgent{answers: []func(Invocation) (*Result, error){textAnswer(invalidAnswer), textAnswer(invalidAnswer)}}
	e := newResearchRunEnv(t, true, agent)
	var res aiv1.AgentResearch
	if err := e.k8s.Get(t.Context(), types.NamespacedName{Namespace: "agents", Name: "news"}, &res); err != nil {
		t.Fatal(err)
	}
	res.Spec.Sources = []aiv1.ResearchSource{
		{Name: "k8s", URL: "https://kubernetes.io/feed.xml", Topic: "Kubernetes"},
		{Name: "go", URL: "https://go.dev/blog/feed.atom"},
	}
	if err := e.k8s.Update(t.Context(), &res); err != nil {
		t.Fatal(err)
	}
	fake := &fakeFeeds{results: []feeds.Result{
		{Entries: []feeds.Entry{{Title: "Release", URL: "https://kubernetes.io/blog/x",
			Published: researchNow.Add(-time.Hour), Summary: "notes"}}},
		{Err: errors.New("HTTP 503")},
	}}
	e.rr.Feeds = fake
	run := e.run(t)
	if len(fake.urls) != 2 || fake.urls[0] != "https://kubernetes.io/feed.xml" {
		t.Fatalf("fetched %v", fake.urls)
	}
	prompt := agent.calls[0].Prompt
	if !strings.Contains(prompt, "## Feed entries") || !strings.Contains(prompt, "### k8s (topic: Kubernetes)") ||
		!strings.Contains(prompt, "https://kubernetes.io/blog/x") || !strings.Contains(prompt, "Not available this run: go (HTTP 503)") {
		t.Fatalf("feeds must reach the prompt:\n%s", prompt)
	}
	if run.Outcome != aiv1.ResearchOutcomeDegraded || !strings.HasPrefix(run.Message, "invalid answer: ") {
		t.Errorf("degraded message must stay the validation error: %+v", run)
	}
	warnings := storetest.Warnings(t, e.runID)
	if len(warnings) < 2 || warnings[0] != `feed "go": HTTP 503` || !strings.HasPrefix(warnings[len(warnings)-1], "invalid answer:") {
		t.Errorf("warnings = %q, want feed warning first and validation last", warnings)
	}
}
