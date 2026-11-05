package v1alpha1

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func limitAt(engine Engine, until time.Time, message string) UsageLimit {
	return UsageLimit{Engine: engine, Until: metav1.NewTime(until), Message: message}
}

func TestMergeUsageLimit(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	limits := MergeUsageLimit(nil, limitAt(EngineCursor, now.Add(time.Hour), "cursor"))
	limits = MergeUsageLimit(limits, limitAt(EngineClaude, now.Add(time.Hour), "first"))
	if len(limits) != 2 || limits[0].Engine != EngineClaude || limits[1].Engine != EngineCursor {
		t.Fatalf("one limit per engine, sorted by engine: %+v", limits)
	}
	merged := MergeUsageLimit(limits, limitAt(EngineClaude, now.Add(2*time.Hour), "later"))
	if len(merged) != 2 || merged[0].Message != "later" {
		t.Fatalf("the later until wins: %+v", merged)
	}
	if limits[0].Message != "first" {
		t.Fatalf("the input must stay as it was: %+v", limits)
	}
	if merged = MergeUsageLimit(merged, limitAt(EngineClaude, now.Add(time.Minute), "earlier")); merged[0].Message != "later" {
		t.Fatalf("an earlier until loses: %+v", merged)
	}
	if merged = MergeUsageLimit(merged, limitAt(EngineClaude, now.Add(2*time.Hour), "tie")); merged[0].Message != "tie" {
		t.Fatalf("on a tie the new limit wins: %+v", merged)
	}
}

func TestActiveUsageLimits(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	run := RunStatus{UsageLimits: []UsageLimit{
		limitAt(EngineCursor, now.Add(time.Hour), "monthly"),
		limitAt(EngineClaude, now, "expired"),
	}}
	if got := run.ActiveUsageLimits(now); len(got) != 1 || got[0].Engine != EngineCursor {
		t.Fatalf("only limits after now are active: %+v", got)
	}
	project := AgentProjectStatus{UsageLimits: run.UsageLimits}
	if _, ok := project.ActiveUsageLimit(EngineClaude, now); ok {
		t.Error("an expired project limit is inactive")
	}
	if l, ok := project.ActiveUsageLimit(EngineCursor, now); !ok || l.Message != "monthly" {
		t.Errorf("cursor's limit is active: %+v", l)
	}
}

func TestDeferringLimit(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	var run RunStatus
	if _, ok := run.DeferringLimit(); ok {
		t.Fatal("a run without a deferral has no deferring limit")
	}
	run.UsageLimits = []UsageLimit{limitAt(EngineCursor, now.Add(24*time.Hour), "monthly")}
	run.DeferredUntil = &metav1.Time{Time: now.Add(time.Hour)}
	run.SetDeferringLimit(limitAt(EngineClaude, now.Add(time.Hour), "five-hour"))
	l, ok := run.DeferringLimit()
	if !ok || l.Engine != EngineClaude || l.Message != "five-hour" || !l.Deferred || run.LimitedEngine != EngineClaude {
		t.Fatalf("the new entry defers the run (and LimitedEngine is still written): %+v, %+v", l, run)
	}
	if len(run.UsageLimits) != 2 || run.UsageLimits[1].Deferred {
		t.Fatalf("the routed limit stays unmarked: %+v", run.UsageLimits)
	}
	// A deferral by another engine replaces the mark.
	run.SetDeferringLimit(limitAt(EngineCursor, now.Add(time.Hour), "again"))
	if l, _ := run.DeferringLimit(); l.Engine != EngineCursor || l.Message != "monthly" || run.UsageLimits[0].Deferred {
		t.Fatalf("cursor's (later) limit defers the run now: %+v", run.UsageLimits)
	}

	marked := run.UsageLimits
	run.ClearDeferringLimit()
	if _, ok := run.DeferringLimit(); ok || run.LimitedEngine != "" || len(run.UsageLimits) != 2 {
		t.Fatalf("cleared: no deferring limit, the limits stay until they expire: %+v", run)
	}
	if !marked[1].Deferred {
		t.Fatal("clearing must not change a slice shared with the caller")
	}

	// A task deferred by the previous release has LimitedEngine only.
	legacy := RunStatus{LimitedEngine: EngineClaude, DeferredUntil: &metav1.Time{Time: now.Add(time.Hour)}, Summary: "claude usage limit"}
	l, ok = legacy.DeferringLimit()
	if !ok || l.Engine != EngineClaude || !l.Until.Time.Equal(now.Add(time.Hour)) || l.Message != "claude usage limit" {
		t.Fatalf("LimitedEngine until DeferredUntil defers the run: %+v", l)
	}
	if got := legacy.ActiveUsageLimits(now); len(got) != 1 || got[0].Engine != EngineClaude {
		t.Fatalf("the legacy deferral is an active limit: %+v", got)
	}
	if got := legacy.ActiveUsageLimits(now.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("and expires with DeferredUntil: %+v", got)
	}
	legacy.DeferredUntil = nil
	if _, ok := legacy.DeferringLimit(); ok {
		t.Fatal("LimitedEngine without DeferredUntil defers nothing")
	}
}

func TestProjectUsageLimits(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	legacy := func(engine Engine, until time.Time) AgentTask {
		var task AgentTask
		task.Status.Run.LimitedEngine = engine
		task.Status.Run.DeferredUntil = &metav1.Time{Time: until}
		task.Status.Run.Summary = string(engine) + " limit"
		return task
	}
	limits := ProjectUsageLimits([]AgentTask{
		legacy(EngineClaude, now.Add(time.Hour)),
		legacy(EngineClaude, now.Add(2*time.Hour)),
		legacy(EngineCursor, now.Add(-time.Minute)), // expired
		{},
	}, nil, "p", now)
	if len(limits) != 1 || limits[0].Engine != EngineClaude || !limits[0].Until.Time.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("expected the latest active claude limit only, got %+v", limits)
	}

	// A limit a run went around with a fallback holds for the others too.
	var routed AgentTask
	routed.Status.Run.UsageLimits = []UsageLimit{
		limitAt(EngineCursor, now.Add(24*time.Hour), "monthly"),
		limitAt(EngineClaude, now.Add(-time.Hour), ""), // expired
	}
	var deferred AgentTask
	deferred.Status.Run.SetDeferringLimit(limitAt(EngineClaude, now.Add(30*time.Minute), "five-hour"))
	limits = ProjectUsageLimits([]AgentTask{legacy(EngineClaude, now.Add(time.Hour)), routed, deferred}, nil, "p", now)
	if len(limits) != 2 || limits[0].Engine != EngineClaude || !limits[0].Until.Time.Equal(now.Add(time.Hour)) ||
		limits[1].Engine != EngineCursor || limits[1].Message != "monthly" {
		t.Fatalf("expected claude's deferral and cursor's routed limit, got %+v", limits)
	}

	researches := []AgentResearch{
		{Spec: AgentResearchSpec{ProjectRef: "p"}, Status: AgentResearchStatus{Run: &ResearchRunStatus{
			UsageLimits: []UsageLimit{limitAt(EngineCursor, now.Add(48*time.Hour), "research")}}}},
		{Spec: AgentResearchSpec{ProjectRef: "other"}, Status: AgentResearchStatus{Run: &ResearchRunStatus{
			UsageLimits: []UsageLimit{limitAt(EngineClaude, now.Add(48*time.Hour), "other project")}}}},
		{Spec: AgentResearchSpec{ProjectRef: "p"}}, // never ran
	}
	limits = ProjectUsageLimits([]AgentTask{deferred}, researches, "p", now)
	if len(limits) != 2 || limits[0].Message != "five-hour" || limits[1].Message != "research" {
		t.Fatalf("expected the task's claude and the project's research cursor limit, got %+v", limits)
	}
	for _, l := range limits {
		if l.Deferred {
			t.Fatalf("project limits carry no deferred mark: %+v", limits)
		}
	}
	if deferred.Status.Run.UsageLimits[0].Deferred != true {
		t.Fatal("aggregation must not change the task's limits")
	}
}
