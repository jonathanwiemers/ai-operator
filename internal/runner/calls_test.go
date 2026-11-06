package runner

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// callUsageEngine adds a claude-like CallUsage with a subagent to every
// result.
type callUsageEngine struct{ next Engine }

func (u *callUsageEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	res, err := u.next.Run(ctx, inv)
	if res != nil {
		res.CallUsage = &CallUsage{InputTokens: 10, OutputTokens: 300, CacheReadTokens: 5000, CacheWriteTokens: 700, Turns: 4, DurationMS: 61000,
			Models: []ModelUsage{
				{Model: "claude-haiku-5-5", InputTokens: 4, OutputTokens: 100, CostUSD: 0.01},
				{Model: "claude-opus-5-5", InputTokens: 6, OutputTokens: 200, CacheReadTokens: 5000, CacheWriteTokens: 700, CostUSD: 0.49},
			},
			SubagentCalls: 1, ContextSeries: []int64{1200, 3400, 5700}, SubagentTurns: []int32{1}}
	}
	return res, err
}

func callKinds(calls []aiv1.AgentCall) []string {
	var out []string
	for _, c := range calls {
		out = append(out, c.Kind+"/"+c.Role)
	}
	return out
}

func TestRunRecordsEveryCall(t *testing.T) {
	e := newLimitEnv(t, &callUsageEngine{next: &scriptedEngine{}})
	if !e.step() {
		t.Fatal("expected a run")
	}
	run := e.task().Status.Run
	if run.LastResult != aiv1.RunResultSucceeded {
		t.Fatalf("run failed: %s", run.Summary)
	}
	// The reviewers run in parallel: their order within a round is open.
	got := callKinds(run.Calls)
	for _, round := range [][2]int{{1, 3}, {4, 6}} {
		slices.Sort(got[round[0]:round[1]])
	}
	want := []string{"implement/implement", "review/bugbot", "review/security", "fix/implement", "review/bugbot", "review/security", "describe/describe"}
	if !slices.Equal(got, want) {
		t.Fatalf("calls %v, want %v", got, want)
	}
	for i, c := range run.Calls {
		if c.Seq != int64(i+1) || c.Run != 1 || c.Engine != aiv1.EngineClaude || c.At.IsZero() {
			t.Errorf("call %d: seq %d, run %d, engine %s, at %v", i, c.Seq, c.Run, c.Engine, c.At)
		}
		if c.CostUSD != "0.5000" || c.CostSource != aiv1.CostReported {
			t.Errorf("call %d: cost %q (%s), want the reported 0.5000", i, c.CostUSD, c.CostSource)
		}
		if c.OutputTokens != 300 || c.CacheReadTokens != 5000 || c.Turns != 4 || c.DurationMS != 61000 || c.SubagentCalls != 1 ||
			len(c.Models) != 2 || c.Models[1].Model != "claude-opus-5-5" || c.Models[1].CostUSD != "0.4900" ||
			!slices.Equal(c.ContextSeries, []int64{1200, 3400, 5700}) || !slices.Equal(c.SubagentTurns, []int32{1}) {
			t.Errorf("call %d does not carry the engine's usage: %+v", i, c)
		}
	}
	// The implementer's calls carry the uncommitted lines they left: a new
	// paypal.go, then its changed line. Read-only calls carry none, though
	// a reviewer wrote a file.
	for i, want := range [][2]int64{{1, 0}, {0, 0}, {0, 0}, {1, 1}, {0, 0}, {0, 0}, {0, 0}} {
		if c := run.Calls[i]; c.LinesAdded != want[0] || c.LinesDeleted != want[1] || c.Checkpoint != "" {
			t.Errorf("call %d (%s): +%d/−%d checkpoint %q; want +%d/−%d and none", i, c.Kind, c.LinesAdded, c.LinesDeleted, c.Checkpoint, want[0], want[1])
		}
	}
	if run.LastCallSeq != 7 || run.CostUSD != "3.5000" {
		t.Fatalf("lastCallSeq %d, cost %s; want 7 and the summed 3.5000", run.LastCallSeq, run.CostUSD)
	}

	// The next run goes on numbering.
	task := e.task()
	task.Spec.RunRequest = 2
	if err := e.k8s.Update(context.Background(), &task); err != nil {
		t.Fatal(err)
	}
	e.step()
	run = e.task().Status.Run
	last := run.Calls[len(run.Calls)-1]
	if run.LastCallSeq <= 7 || last.Seq != run.LastCallSeq || last.Run != 2 || run.Calls[0].Seq != 1 {
		t.Fatalf("second run: lastCallSeq %d, calls %v", run.LastCallSeq, run.Calls)
	}
}

func TestFallbackRecordsBothCalls(t *testing.T) {
	e, _, _ := cursorLimitedEnv(t, func(a *aiv1.AgentsSpec) {
		a.Implement.Engine, a.Implement.Model, a.Implement.Fallback = aiv1.EngineCursor, "auto", sonnetFallback()
	}, nil)
	if !e.step() {
		t.Fatal("expected a run")
	}
	var implement []aiv1.AgentCall
	for _, c := range e.task().Status.Run.Calls {
		if c.Role == "implement" {
			implement = append(implement, c)
		}
	}
	if len(implement) < 2 {
		t.Fatalf("expected the limited call and the fallback, got %+v", implement)
	}
	limited, fb := implement[0], implement[1]
	if limited.Engine != aiv1.EngineCursor || limited.Model != "auto" || limited.CostSource != aiv1.CostUnknown || limited.CostUSD != "" ||
		limited.FallbackFrom != "" || limited.Kind != aiv1.CallKindImplement {
		t.Errorf("limited call: %+v", limited)
	}
	if fb.Engine != aiv1.EngineClaude || fb.Model != "claude-sonnet-5-5" || fb.FallbackFrom != aiv1.EngineCursor ||
		fb.Kind != aiv1.CallKindImplement || fb.Seq != limited.Seq+1 {
		t.Errorf("fallback call: %+v", fb)
	}
}

func TestResumedStepIsRecorded(t *testing.T) {
	ctx := context.Background()
	e := newImplementEnv(t)
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, &stepEngine{gl: e.gl, lastChanges: true}, e.task.Name)
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
	var task aiv1.AgentTask
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	var steps []aiv1.AgentCall
	for _, c := range task.Status.Run.Calls {
		if c.Role == "implement" {
			steps = append(steps, c)
		}
	}
	if len(steps) != 2 || steps[0].Resumed || !steps[1].Resumed || steps[1].Kind != aiv1.CallKindImplement {
		t.Fatalf("expected a fresh first step and a resumed second one: %+v", steps)
	}
}

func TestNewCall(t *testing.T) {
	start := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	r := &Runner{Now: func() time.Time { return start.Add(90 * time.Second) }, runNumber: 3}
	inv := Invocation{Kind: aiv1.CallKindCI, Role: "implement", Effort: "high", Resume: "sess"}

	// No model configured: the main model of the split.
	c := r.newCall(aiv1.EngineClaude, inv, &Result{CostUSD: 1.23456, CallUsage: &CallUsage{OutputTokens: 9, ReadGateBlocks: 2,
		LinesAdded: 120, LinesDeleted: 7, StepGateNudges: 2,
		Models: []ModelUsage{{Model: "claude-haiku-5-5", OutputTokens: 2}, {Model: "claude-opus-5-5", OutputTokens: 7}}}}, start)
	if c.Model != "claude-opus-5-5" || c.CostUSD != "1.2346" || c.CostSource != aiv1.CostReported || !c.Resumed ||
		c.DurationMS != 90000 || c.Run != 3 || c.Effort != "high" || c.Kind != aiv1.CallKindCI || c.ReadGateBlocks != 2 ||
		c.LinesAdded != 120 || c.LinesDeleted != 7 || c.StepGateNudges != 2 {
		t.Errorf("claude call: %+v", c)
	}

	// Cursor: no reported cost, and the configured model wins.
	inv.Model = "auto"
	c = r.newCall(aiv1.EngineCursor, inv, &Result{CallUsage: &CallUsage{InputTokens: 5, DurationMS: 4000}}, start)
	if c.Model != "auto" || c.CostUSD != "" || c.CostSource != aiv1.CostUnknown || c.InputTokens != 5 || c.DurationMS != 4000 {
		t.Errorf("cursor call: %+v", c)
	}

	// A call that failed without a result still counts its time.
	c = r.newCall(aiv1.EngineClaude, inv, nil, start)
	if c.CostSource != aiv1.CostUnknown || c.CostUSD != "" || c.DurationMS != 90000 {
		t.Errorf("failed call: %+v", c)
	}
}

func TestCallsStayPendingWhenThePatchFails(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aiv1.AddToScheme(scheme)
	task := &aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Name: "t", Namespace: "agents"},
		Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{LastCallSeq: 4}}}
	fails := 1
	k8s := interceptor.NewClient(fake.NewClientBuilder().WithScheme(scheme).WithObjects(task).WithStatusSubresource(task).Build(), interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, c client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			if fails > 0 {
				fails--
				return errors.New("apiserver unavailable")
			}
			return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
		},
	})
	r := &Runner{K8s: k8s, Namespace: "agents", TaskName: "t", Log: logr.Discard()}
	ctx := context.Background()
	r.recordCall(ctx, aiv1.AgentCall{Kind: aiv1.CallKindSpec})
	r.recordCall(ctx, aiv1.AgentCall{Kind: aiv1.CallKindPilot})
	var got aiv1.AgentTask
	_ = k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "t"}, &got)
	run := got.Status.Run
	if len(run.Calls) != 2 || run.Calls[0].Kind != aiv1.CallKindSpec || run.Calls[0].Seq != 5 || run.Calls[1].Seq != 6 ||
		run.LastCallSeq != 6 || len(r.pendingCalls) != 0 {
		t.Fatalf("calls %+v, lastCallSeq %d, pending %d", run.Calls, run.LastCallSeq, len(r.pendingCalls))
	}
}
