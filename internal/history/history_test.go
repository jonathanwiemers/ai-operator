package history

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/pricing"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

var now = time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)

func task(rv string, mut func(*aiv1.AgentTask)) *aiv1.AgentTask {
	t := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Name: "bloop-148", Namespace: "agents", UID: "u1", ResourceVersion: rv},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "bloop", RunRequest: 3,
			Issue: &aiv1.IssueRef{IID: 148}, Milestone: &aiv1.MilestoneRef{IID: 20}},
	}
	if mut != nil {
		mut(t)
	}
	return t
}

func kinds(es []store.Event) []string {
	var ks []string
	for _, e := range es {
		ks = append(ks, e.Kind+" "+e.Key)
	}
	return ks
}

func TestDifferTransitions(t *testing.T) {
	cases := []struct {
		name     string
		old, cur func(*aiv1.AgentTask)
		want     []string
	}{
		{"nothing changed", nil, nil, nil},
		{"run starts", nil, func(t *aiv1.AgentTask) { t.Status.Phase = aiv1.TaskPhaseRunning },
			[]string{"run.started task/u1/run/3/start"}},
		{"push", nil, func(t *aiv1.AgentTask) { t.Status.Run.PushedSHA = "abcdef0123" },
			[]string{"push task/u1/push/abcdef0123"}},
		{"ci red", nil, func(t *aiv1.AgentTask) {
			t.Status.CI = &aiv1.CIStatus{State: aiv1.CIStateFailed, PipelineID: 8841, FailedJobs: []string{"test:race"}}
		}, []string{"ci task/u1/ci/8841/Failed"}},
		{"ci running is no event", nil, func(t *aiv1.AgentTask) {
			t.Status.CI = &aiv1.CIStatus{State: aiv1.CIStateRunning, PipelineID: 1}
		}, nil},
		{"review", nil, func(t *aiv1.AgentTask) {
			t.Status.Run.ReviewedSHA, t.Status.Run.BlockingFindings = "beef", 1
		}, []string{"review task/u1/review/beef"}},
		{"question", nil, func(t *aiv1.AgentTask) {
			t.Status.Run.RunCount, t.Status.Run.LastResult = 1, aiv1.RunResultNeedsInput
		}, []string{"run.finished task/u1/run/1", "needs task/u1/needs/run/1"}},
		{"merged", func(t *aiv1.AgentTask) {
			t.Status.Autopilot = &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning,
				Items: []aiv1.AutopilotItem{{IID: 142, State: aiv1.AutopilotItemActive}}}
		}, func(t *aiv1.AgentTask) {
			t.Status.Autopilot = &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning,
				Items: []aiv1.AutopilotItem{{IID: 142, State: aiv1.AutopilotItemMerged, MergeRequestIID: 209}}}
		}, []string{"merged task/u1/merged/142"}},
		{"autopilot pauses", func(t *aiv1.AgentTask) {
			t.Status.Autopilot = &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}
		}, func(t *aiv1.AgentTask) {
			t.Status.Autopilot = &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PausedReason: "CI"}
		}, []string{"autopilot task/u1/autopilot/Paused/2"}},
		{"proposal posted, then accepted", func(t *aiv1.AgentTask) {
			t.Status.Pilot = &aiv1.PilotStatus{Proposals: []aiv1.PilotProposal{{ID: 3}}}
		}, func(t *aiv1.AgentTask) {
			t.Status.Pilot = &aiv1.PilotStatus{Proposals: []aiv1.PilotProposal{{ID: 4}},
				Accepted: []aiv1.PilotProposal{{ID: 3, AcceptedBy: "jw"}}}
		}, []string{"proposal task/u1/proposal/4/posted", "proposal task/u1/proposal/3/accepted"}},
		{"prompt posted", nil, func(t *aiv1.AgentTask) {
			t.Status.Prompts = []aiv1.Prompt{{ID: "Q1", State: aiv1.PromptOpen, Question: "Fix?"}}
		}, []string{"prompt.posted task/u1/prompt/Q1/open"}},
		{"prompt answered", func(t *aiv1.AgentTask) {
			t.Status.Prompts = []aiv1.Prompt{{ID: "Q1", State: aiv1.PromptOpen}}
		}, func(t *aiv1.AgentTask) {
			t.Status.Prompts = []aiv1.Prompt{{ID: "Q1", State: aiv1.PromptAnswered,
				Options: []aiv1.PromptOption{{ID: "A", Label: "Yes"}},
				Answer:  &aiv1.PromptAnswer{OptionID: "A", By: "jw", Via: "desk"}}}
		}, []string{"prompt.answered task/u1/prompt/Q1/answered"}},
		{"prompt superseded", func(t *aiv1.AgentTask) {
			t.Status.Prompts = []aiv1.Prompt{{ID: "Q1", State: aiv1.PromptOpen}}
		}, func(t *aiv1.AgentTask) {
			t.Status.Prompts = []aiv1.Prompt{{ID: "Q1", State: aiv1.PromptSuperseded}}
		}, []string{"prompt.closed task/u1/prompt/Q1/superseded"}},
		{"prompt expired", func(t *aiv1.AgentTask) {
			t.Status.Prompts = []aiv1.Prompt{{ID: "Q1", State: aiv1.PromptOpen}}
		}, func(t *aiv1.AgentTask) {
			t.Status.Prompts = []aiv1.Prompt{{ID: "Q1", State: aiv1.PromptExpired}}
		}, []string{"prompt.closed task/u1/prompt/Q1/expired"}},
		{"prompt dropped from the list", func(t *aiv1.AgentTask) {
			t.Status.Prompts = []aiv1.Prompt{{ID: "Q1", State: aiv1.PromptOpen}}
		}, func(t *aiv1.AgentTask) {
			t.Status.Prompts = []aiv1.Prompt{{ID: "Q2", State: aiv1.PromptOpen}}
		}, []string{"prompt.posted task/u1/prompt/Q2/open"}},
		{"prompt unchanged", func(t *aiv1.AgentTask) {
			t.Status.Prompts = []aiv1.Prompt{{ID: "Q1", State: aiv1.PromptAnswered}}
		}, func(t *aiv1.AgentTask) {
			t.Status.Prompts = []aiv1.Prompt{{ID: "Q1", State: aiv1.PromptAnswered}}
			t.Status.Phase = aiv1.TaskPhaseIdle
		}, nil},
		{"write failure new", nil, func(t *aiv1.AgentTask) {
			t.Status.WriteFailures = []aiv1.WriteFailure{{Source: "runner/x", Error: "bad", Key: "k1", Count: 1}}
		}, []string{"pilot.writeFailed task/u1/writefail/k1/1"}},
		{"write failure unchanged", func(t *aiv1.AgentTask) {
			t.Status.WriteFailures = []aiv1.WriteFailure{{Source: "runner/x", Error: "bad", Key: "k1", Count: 1}}
		}, func(t *aiv1.AgentTask) {
			t.Status.WriteFailures = []aiv1.WriteFailure{{Source: "runner/x", Error: "bad", Key: "k1", Count: 1}}
			t.Status.Phase = aiv1.TaskPhaseIdle
		}, nil},
		{"write failure count bumped", func(t *aiv1.AgentTask) {
			t.Status.WriteFailures = []aiv1.WriteFailure{{Source: "runner/x", Error: "bad", Key: "k1", Count: 1}}
		}, func(t *aiv1.AgentTask) {
			t.Status.WriteFailures = []aiv1.WriteFailure{{Source: "runner/x", Error: "bad", Key: "k1", Count: 2}}
		}, []string{"pilot.writeFailed task/u1/writefail/k1/2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			old := task("1", c.old)
			cur := task("2", c.cur)
			if c.cur == nil && c.old == nil {
				cur = task("1", nil)
			}
			got := kinds(NewDiffer().Update(old, cur, now))
			if !slices.Equal(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestDifferRunDurationAndCost(t *testing.T) {
	d := NewDiffer()
	idle := task("1", func(t *aiv1.AgentTask) { t.Status.Run.CostUSD = "1.50" })
	running := task("2", func(t *aiv1.AgentTask) {
		t.Status.Phase, t.Status.Run.CostUSD = aiv1.TaskPhaseRunning, "1.50"
	})
	done := task("3", func(t *aiv1.AgentTask) {
		t.Status.Phase, t.Status.Run.CostUSD = aiv1.TaskPhaseIdle, "2.75"
		t.Status.Run.RunCount, t.Status.Run.LastResult = 1, aiv1.RunResultSucceeded
	})
	d.Update(idle, running, now)
	es := d.Update(running, done, now.Add(90*time.Second))
	if len(es) != 1 {
		t.Fatalf("events = %v", kinds(es))
	}
	e := es[0]
	if e.Data["durationSec"] != int64(90) || e.Data["costUSD"] != "1.2500" || e.IssueIID != 148 || e.MilestoneIID != 20 {
		t.Errorf("run.finished = %+v", e)
	}
	if e.Project != "bloop" || e.Namespace != "agents" || e.Task != "bloop-148" {
		t.Errorf("event names = %+v", e)
	}
}

func TestDifferDelete(t *testing.T) {
	e := NewDiffer().Delete(task("1", nil), now)
	if e.Kind != KindClosed || e.Key != "task/u1/closed" || e.Summary != "#148 closed" {
		t.Errorf("delete = %+v", e)
	}
}

// withCalls gives the task the calls first..last of run 2.
func withCalls(first, last int64) func(*aiv1.AgentTask) {
	return func(t *aiv1.AgentTask) {
		t.Status.Run.MergeRequestIID = 702
		for seq := first; seq <= last; seq++ {
			t.Status.Run.Calls = append(t.Status.Run.Calls, aiv1.AgentCall{Seq: seq, Run: 2,
				At: metav1.NewTime(now.Add(time.Duration(seq) * time.Minute)), Kind: aiv1.CallKindImplement,
				Role: "implement", Engine: aiv1.EngineClaude, Model: "claude-opus-5-5", CostUSD: "0.5000",
				CostSource: aiv1.CostReported, InputTokens: 10, OutputTokens: 20})
		}
		t.Status.Run.LastCallSeq = last
	}
}

func keys(cs []store.Call) []string {
	var ks []string
	for _, c := range cs {
		ks = append(ks, c.Key)
	}
	return ks
}

func TestCallsOfAnUpdate(t *testing.T) {
	old := task("1", withCalls(1, 2))
	cur := task("2", withCalls(1, 5))
	got := Calls(old, cur)
	if want := []string{"call/u1/3", "call/u1/4", "call/u1/5"}; !slices.Equal(keys(got), want) {
		t.Fatalf("keys = %v, want %v", keys(got), want)
	}
	c := got[0]
	if c.Namespace != "agents" || c.Project != "bloop" || c.Task != "bloop-148" || c.MilestoneIID != 20 ||
		c.IssueIID != 148 || c.MergeRequestIID != 702 || c.Run != 2 || c.Seq != 3 || !c.At.Equal(now.Add(3*time.Minute)) {
		t.Errorf("call = %+v", c)
	}
	if c.Kind != "implement" || c.Engine != "claude" || c.Model != "claude-opus-5-5" || c.CostUSD == nil ||
		*c.CostUSD != 0.5 || c.CostSource != "reported" || c.InputTokens != 10 || c.OutputTokens != 20 {
		t.Errorf("call = %+v", c)
	}
	// Calls trimmed from the front of the list are not new.
	trimmed := task("3", withCalls(4, 6))
	if got := keys(Calls(cur, trimmed)); !slices.Equal(got, []string{"call/u1/6"}) {
		t.Errorf("after trimming: %v, want call/u1/6", got)
	}
	if got := Calls(cur, cur); len(got) != 0 {
		t.Errorf("no change: %v", keys(got))
	}
}

func TestCallsOfAnAdd(t *testing.T) {
	got := Calls(nil, task("1", withCalls(7, 9)))
	if want := []string{"call/u1/7", "call/u1/8", "call/u1/9"}; !slices.Equal(keys(got), want) {
		t.Errorf("keys = %v, want %v", keys(got), want)
	}
	if got := Calls(nil, task("1", nil)); len(got) != 0 {
		t.Errorf("task without calls: %v", keys(got))
	}
}

func TestCallsCostAndDetail(t *testing.T) {
	cur := task("2", func(t *aiv1.AgentTask) {
		t.Spec.Milestone, t.Spec.Issue = nil, nil
		t.Status.Run.Calls = []aiv1.AgentCall{
			{Seq: 1, Kind: aiv1.CallKindPilot, Engine: aiv1.EngineCursor, FallbackFrom: aiv1.EngineClaude, CostSource: aiv1.CostUnknown, CostUSD: "0.0000"},
			{Seq: 2, Kind: aiv1.CallKindPilot, Engine: aiv1.EngineCursor, CostSource: aiv1.CostEstimated, CostUSD: "0.1200"},
			{Seq: 3, Kind: aiv1.CallKindPilot, Engine: aiv1.EngineCursor},
			{Seq: 4, Kind: aiv1.CallKindImplement, Engine: aiv1.EngineClaude, CostSource: aiv1.CostReported, CostUSD: "1.5000",
				Resumed: true, SubagentCalls: 2, ContextSeries: []int64{100, 200}, SubagentTurns: []int32{1},
				Models: []aiv1.CallModelUsage{{Model: "claude-opus-5-5", OutputTokens: 9, CostUSD: "1.2500"}, {Model: "claude-haiku-5-5"}}},
		}
	})
	got := Calls(nil, cur)
	if len(got) != 4 {
		t.Fatalf("calls = %v", keys(got))
	}
	if c := got[0]; c.CostUSD != nil || c.CostSource != "unknown" || c.FallbackFrom != "claude" || c.MilestoneIID != 0 || c.IssueIID != 0 {
		t.Errorf("unknown cost = %+v; want no cost and no IIDs", c)
	}
	if c := got[1]; c.CostUSD == nil || *c.CostUSD != 0.12 || c.CostSource != "estimated" {
		t.Errorf("estimated = %+v", c)
	}
	if c := got[2]; c.CostUSD != nil || c.CostSource != "unknown" {
		t.Errorf("no source = %+v; want unknown", c)
	}
	c := got[3]
	if !c.Resumed || c.SubagentCalls != 2 || !slices.Equal(c.ContextSeries, []int64{100, 200}) || !slices.Equal(c.SubagentTurns, []int32{1}) {
		t.Errorf("detail = %+v", c)
	}
	if len(c.Models) != 2 || c.Models[0].Model != "claude-opus-5-5" || c.Models[0].OutputTokens != 9 ||
		c.Models[0].CostUSD == nil || *c.Models[0].CostUSD != 1.25 || c.Models[1].CostUSD != nil {
		t.Errorf("models = %+v", c.Models)
	}
}

// The recorder estimates the cost of unknown calls with tokens from its
// pricing table; calls without tokens or price stay unknown (NULL), and
// reported costs are kept even when the model has a price.
func TestRecorderEstimatesUnknownCosts(t *testing.T) {
	prices, err := pricing.Parse([]byte(`models: {"cursor/auto": {input: 1, output: 10}, "claude-opus-5-5": {input: 100}}`))
	if err != nil {
		t.Fatal(err)
	}
	cur := task("2", func(t *aiv1.AgentTask) {
		t.Status.Run.Calls = []aiv1.AgentCall{
			{Seq: 1, Engine: aiv1.EngineCursor, Model: "auto", CostSource: aiv1.CostUnknown, InputTokens: 200000, OutputTokens: 30000},
			{Seq: 2, Engine: aiv1.EngineCursor, Model: "auto", CostSource: aiv1.CostUnknown},
			{Seq: 3, Engine: aiv1.EngineCursor, Model: "gpt-5", CostSource: aiv1.CostUnknown, InputTokens: 1000},
			{Seq: 4, Engine: aiv1.EngineClaude, Model: "claude-opus-5-5", CostSource: aiv1.CostReported, CostUSD: "1.5000", InputTokens: 1000},
		}
	})
	r := NewRecorder(nil, time.Hour, clock.RealClock{}, testLogger(t))
	r.Pricing = prices
	r.started.Store(true)
	for _, c := range Calls(nil, cur) {
		r.RecordCall(c)
	}
	close(r.calls)
	var got []store.Call
	for c := range r.calls {
		got = append(got, c)
	}
	if len(got) != 4 {
		t.Fatalf("calls = %v", keys(got))
	}
	if c := got[0]; c.CostSource != aiv1.CostEstimated || c.CostUSD == nil || *c.CostUSD != 0.5 {
		t.Errorf("cursor call with tokens = %v %v, want estimated 0.5", c.CostSource, c.CostUSD)
	}
	for _, c := range got[1:3] {
		if c.CostSource != aiv1.CostUnknown || c.CostUSD != nil {
			t.Errorf("call %d = %v %v, want unknown without cost", c.Seq, c.CostSource, c.CostUSD)
		}
	}
	if c := got[3]; c.CostSource != aiv1.CostReported || c.CostUSD == nil || *c.CostUSD != 1.5 {
		t.Errorf("claude call = %v %v, want reported 1.5", c.CostSource, c.CostUSD)
	}

	// Without a table nothing is estimated.
	call := StoreCall(cur.Status.Run.Calls[0])
	Estimate(&call, nil)
	if call.CostSource != aiv1.CostUnknown || call.CostUSD != nil {
		t.Errorf("without pricing = %v %v, want unknown", call.CostSource, call.CostUSD)
	}
}

// Each call is one row: the same update twice, and the calls of the
// existing tasks the recorder writes at its start (after an operator
// restart), add nothing.
func TestCallsAreWrittenOnce(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	old, cur := task("1", withCalls(1, 2)), task("2", withCalls(1, 5))
	r := NewRecorder(readyDB{s}, time.Hour, clock.RealClock{}, testLogger(t))
	r.started.Store(true)
	for range 2 {
		for _, c := range Calls(old, cur) {
			r.RecordCall(c)
		}
	}
	close(r.calls)
	var queued []store.Call
	for c := range r.calls {
		queued = append(queued, c)
	}
	written := func(calls []store.Call) int64 {
		t.Helper()
		n, err := s.InsertCalls(ctx, calls)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if len(queued) != 6 || !queued[0].ExpiresAt.Equal(now.Add(3*time.Minute+time.Hour)) {
		t.Fatalf("queued %d calls, the first expiring at %v; want 6, an hour after the call", len(queued), queued[0].ExpiresAt)
	}
	if n1, n2 := written(queued[:3]), written(queued[3:]); n1 != 3 || n2 != 0 {
		t.Errorf("the update twice wrote %d and %d rows, want 3 and 0", n1, n2)
	}

	// The restarted operator's recorder writes the calls of every task.
	other := task("1", func(t *aiv1.AgentTask) { t.UID = "u2"; withCalls(1, 1)(t) })
	all := append(Calls(nil, cur), Calls(nil, other)...)
	r.existing = func(context.Context) ([]store.Call, error) { return all, nil }
	r.writeExisting(ctx)
	for i := range all {
		r.prepare(&all[i])
	}
	if n := written(all); n != 0 {
		t.Errorf("%d calls were missing after the start, want all 6 written", n)
	}
	if n := written(Calls(nil, task("4", withCalls(6, 6)))); n != 1 {
		t.Errorf("a new call wrote %d rows, want 1", n)
	}
}

type readyDB struct{ s *store.Store }

func (d readyDB) Ready() <-chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

func (d readyDB) Store() *store.Store { return d.s }

type fakeGitLab struct {
	gitlab.API
	notes []gitlab.Note
}

func (f fakeGitLab) ListIssueNotes(context.Context, string, int64) ([]gitlab.Note, error) {
	return f.notes, nil
}

func TestWrapGitLabRecordsNotesOnce(t *testing.T) {
	r := NewRecorder(nil, time.Hour, clock.RealClock{}, testLogger(t))
	if _, wrapped := (*Recorder)(nil).WrapGitLab(fakeGitLab{}, Project{}).(*notesAPI); wrapped {
		t.Errorf("nil recorder wrapped the client")
	}
	r.started.Store(true)
	at := now
	api := r.WrapGitLab(fakeGitLab{notes: []gitlab.Note{
		{ID: 1, Body: "/pilot hold merges", Author: gitlab.User{Username: "jw"}, CreatedAt: &at},
		{ID: 2, Body: "changed the label", System: true},
		{ID: 3, Body: "Done.", Author: gitlab.User{Username: "ai-bot"}, CreatedAt: &at},
	}}, Project{Namespace: "agents", Name: "bloop", WebURL: "https://gitlab.com/jo/bloop/", Bot: "ai-bot"})
	for range 2 {
		if _, err := api.ListIssueNotes(t.Context(), "jo/bloop", 138); err != nil {
			t.Fatal(err)
		}
	}
	close(r.queue)
	var got []store.Event
	for e := range r.queue {
		got = append(got, e)
	}
	if len(got) != 2 {
		t.Fatalf("recorded %d events, want 2: %v", len(got), kinds(got))
	}
	if e := got[0]; e.Key != "note/1" || e.IssueIID != 138 || e.Actor != "jw" ||
		e.Data["url"] != "https://gitlab.com/jo/bloop/-/issues/138#note_1" || e.Data["bot"] != nil || !e.ExpiresAt.Equal(at.Add(time.Hour)) {
		t.Errorf("note 1 = %+v", e)
	}
	if got[1].Data["bot"] != true {
		t.Errorf("bot note = %+v", got[1])
	}
}

func TestRecordDropsWhenNotStartedOrFull(t *testing.T) {
	r := NewRecorder(nil, time.Hour, clock.RealClock{}, testLogger(t))
	r.Record(store.Event{Key: "a"})
	r.RecordCall(store.Call{Key: "a"})
	if len(r.queue) != 0 || len(r.calls) != 0 {
		t.Fatal("recorded before start")
	}
	r.started.Store(true)
	for range queueSize + 5 {
		r.Record(store.Event{Key: "a"})
		r.RecordCall(store.Call{Key: "a"})
	}
	if r.dropped.Load() != 5 || r.droppedCalls.Load() != 5 {
		t.Errorf("dropped = %d events, %d calls, want 5 each", r.dropped.Load(), r.droppedCalls.Load())
	}
}

func testLogger(t *testing.T) logr.Logger { return testr.New(t) }
