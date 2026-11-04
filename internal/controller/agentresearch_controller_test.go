package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
	"github.com/jonathanwiemers/ai-operator/internal/research"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/research/storetest"
)

// fakeResearchDB records the outcomes the controller writes.
type fakeResearchDB struct {
	ready chan struct{}
	runs  []store.Run
	calls map[string]store.Call
	// inserted counts the calls written.
	inserted int64
	// callsErr fails InsertCalls.
	callsErr error
}

func newFakeResearchDB(ready bool) *fakeResearchDB {
	db := &fakeResearchDB{ready: make(chan struct{})}
	if ready {
		close(db.ready)
	}
	return db
}

func (f *fakeResearchDB) Ready() <-chan struct{} { return f.ready }

func (f *fakeResearchDB) FinishRunOrInsert(_ context.Context, r store.Run) error {
	f.runs = append(f.runs, r)
	return nil
}

// InsertCalls skips stored keys, like the store.
func (f *fakeResearchDB) InsertCalls(_ context.Context, calls []store.Call) (int64, error) {
	if f.callsErr != nil {
		return 0, f.callsErr
	}
	if f.calls == nil {
		f.calls = map[string]store.Call{}
	}
	var n int64
	for _, c := range calls {
		if _, ok := f.calls[c.Key]; !ok {
			f.calls[c.Key] = c
			n++
		}
	}
	f.inserted += n
	return n, nil
}

type researchEnv struct {
	t   *testing.T
	c   client.Client
	r   *AgentResearchReconciler
	db  *fakeResearchDB
	now time.Time
	ctx context.Context
}

func researchProject() *aiv1.AgentProject {
	p := testProject()
	p.Spec.Research = &aiv1.ProjectResearch{WriterSecret: &aiv1.SecretLogin{Name: "writer"}}
	return p
}

func writerSecret() *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "writer", Namespace: ns},
		Data: map[string][]byte{"username": []byte("agents.research_writer"), "password": []byte("pw")}}
}

func newResearch(name, schedule string) *aiv1.AgentResearch {
	return &aiv1.AgentResearch{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: types.UID("uid-" + name), Generation: 1,
			CreationTimestamp: metav1.NewTime(t0)},
		Spec: aiv1.AgentResearchSpec{ProjectRef: "shop", Prompt: "news", View: aiv1.ResearchViewNewsflash,
			Depth: aiv1.ResearchDepthQuick, Schedule: schedule, TimeZone: map[bool]string{true: "Europe/Berlin"}[schedule != ""]},
	}
}

// newResearchEnv starts with the project and its writer Secret unless objs
// bring their own.
func newResearchEnv(t *testing.T, objs ...client.Object) *researchEnv {
	s := testScheme(t)
	b := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).
		WithStatusSubresource(&aiv1.AgentProject{}, &aiv1.AgentResearch{})
	e := &researchEnv{t: t, c: b.Build(), db: newFakeResearchDB(true), now: t0, ctx: context.Background()}
	e.r = &AgentResearchReconciler{Client: e.c, Scheme: s, DB: e.db,
		DBConn: research.DatabaseConfig{Host: "db.example", Port: 5432, Name: "ai_operator", SSLMode: "require"},
		Candidates: aiv1.ResearchCandidates{
			Quick: []aiv1.ResearchAgent{{Engine: aiv1.EngineClaude, Model: "claude-sonnet-5-5"}},
			Deep:  []aiv1.ResearchAgent{{Engine: aiv1.EngineClaude, Model: "opus", Effort: "high"}},
		},
		Now: func() time.Time { return e.now }}
	return e
}

func (e *researchEnv) reconcile(name string) ctrl.Result {
	e.t.Helper()
	res, err := e.r.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: name}})
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *researchEnv) research(name string) *aiv1.AgentResearch {
	e.t.Helper()
	var r aiv1.AgentResearch
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: name}, &r); err != nil {
		e.t.Fatal(err)
	}
	return &r
}

func (e *researchEnv) pods() []corev1.Pod {
	e.t.Helper()
	var pods corev1.PodList
	if err := e.c.List(e.ctx, &pods, client.InNamespace(ns)); err != nil {
		e.t.Fatal(err)
	}
	return pods.Items
}

// report simulates the runner recording the outcome in the status.
func (e *researchEnv) report(name string, outcome aiv1.ResearchOutcome, cost string) {
	e.t.Helper()
	r := e.research(name)
	orig := r.DeepCopy()
	r.Status.Run.Outcome, r.Status.Run.CostUSD = outcome, cost
	if err := e.c.Status().Patch(e.ctx, r, client.MergeFrom(orig)); err != nil {
		e.t.Fatal(err)
	}
}

// endPod sets the phase (and reason) of the only pod.
func (e *researchEnv) endPod(phase corev1.PodPhase, reason string) {
	e.t.Helper()
	pods := e.pods()
	if len(pods) != 1 {
		e.t.Fatalf("want one pod, got %d", len(pods))
	}
	pod := pods[0]
	pod.Status.Phase, pod.Status.Reason = phase, reason
	if err := e.c.Status().Update(e.ctx, &pod); err != nil {
		e.t.Fatal(err)
	}
}

func condition(r *aiv1.AgentResearch, typ string) *metav1.Condition {
	return meta.FindStatusCondition(r.Status.Conditions, typ)
}

func TestResearchOneOffRunsOnce(t *testing.T) {
	e := newResearchEnv(t, researchProject(), writerSecret(), newResearch("q", ""))
	e.reconcile("q")

	r := e.research("q")
	pods := e.pods()
	if len(pods) != 1 || r.Status.Run == nil {
		t.Fatalf("want one pod and a run, got %d pods, run %v", len(pods), r.Status.Run)
	}
	run := r.Status.Run
	if pods[0].Name != naming.ResearchPod("q", run.ID) || !metav1.IsControlledBy(&pods[0], r) {
		t.Errorf("pod %s, owners %v", pods[0].Name, pods[0].OwnerReferences)
	}
	if run.Engine != aiv1.EngineClaude || run.Model != "claude-sonnet-5-5" || run.Reason != "chosen: claude/claude-sonnet-5-5" ||
		!run.StartedAt.Time.Equal(t0) || r.Status.Phase != aiv1.ResearchPhaseRunning || r.Status.NextRun != nil {
		t.Errorf("status %+v, run %+v", r.Status, run)
	}
	for _, typ := range []string{ConditionValid, ConditionDatabaseReady, ConditionRunning} {
		if c := condition(r, typ); c == nil || c.Status != metav1.ConditionTrue {
			t.Errorf("condition %s: %+v", typ, c)
		}
	}

	// The runner reports and its pod ends.
	e.now = t0.Add(3 * time.Minute)
	e.report("q", aiv1.ResearchOutcomeSucceeded, "0.42")
	e.endPod(corev1.PodSucceeded, "")
	e.reconcile("q")
	r = e.research("q")
	st := r.Status
	if len(e.pods()) != 0 {
		t.Error("the pod of a finished run is deleted")
	}
	if st.Run.FinishedAt == nil || st.Run.Outcome != aiv1.ResearchOutcomeSucceeded || st.Phase != aiv1.ResearchPhaseSucceeded ||
		st.LastResultID != run.ID || st.LastEngine != aiv1.EngineClaude || st.LastModel != "claude-sonnet-5-5" ||
		st.LastRun == nil || !st.LastRun.Time.Equal(t0) {
		t.Errorf("status after the run %+v, run %+v", st, st.Run)
	}
	if c := condition(r, ConditionRunning); c.Status != metav1.ConditionFalse {
		t.Errorf("running %+v", c)
	}
	if len(e.db.runs) != 1 || e.db.runs[0].Status != store.StatusSucceeded || e.db.runs[0].CostUSD != 0.42 ||
		!e.db.runs[0].ExpiresAt.Equal(t0.Add(aiv1.DefaultResearchRetention)) {
		t.Errorf("store %+v", e.db.runs)
	}

	// It never runs again.
	e.now = t0.Add(48 * time.Hour)
	if res := e.reconcile("q"); res.RequeueAfter != 0 {
		t.Errorf("a finished one-off is not requeued, got %v", res.RequeueAfter)
	}
	if len(e.pods()) != 0 || e.research("q").Status.Run.ID != run.ID || len(e.db.runs) != 1 {
		t.Error("a one-off runs once")
	}
}

func TestResearchScheduledAtSevenBerlin(t *testing.T) {
	// t0 is 14:00 in Berlin (CEST): the first run is at 07:00 the next day.
	e := newResearchEnv(t, researchProject(), writerSecret(), newResearch("news", "0 7 * * *"))
	seven := time.Date(2026, 10, 2, 5, 0, 0, 0, time.UTC)

	res := e.reconcile("news")
	r := e.research("news")
	if len(e.pods()) != 0 || r.Status.NextRun == nil || !r.Status.NextRun.Time.Equal(seven) || r.Status.Phase != aiv1.ResearchPhaseScheduled {
		t.Fatalf("not due: pods %d, status %+v", len(e.pods()), r.Status)
	}
	if want := seven.Sub(t0) + time.Second; res.RequeueAfter != want {
		t.Errorf("requeue %v, want %v", res.RequeueAfter, want)
	}

	e.now = seven.Add(-time.Minute)
	e.reconcile("news")
	if len(e.pods()) != 0 {
		t.Fatal("started before 07:00")
	}

	e.now = seven
	e.reconcile("news")
	r = e.research("news")
	if len(e.pods()) != 1 || r.Status.Run == nil || !r.Status.Run.StartedAt.Time.Equal(seven) {
		t.Fatalf("due at 07:00: pods %d, run %+v", len(e.pods()), r.Status.Run)
	}
	if next := seven.Add(24 * time.Hour); !r.Status.NextRun.Time.Equal(next) {
		t.Errorf("nextRun %v, want %v", r.Status.NextRun, next)
	}

	// After the run the research waits for the next day.
	e.now = seven.Add(10 * time.Minute)
	e.report("news", aiv1.ResearchOutcomeDegraded, "")
	e.reconcile("news")
	r = e.research("news")
	if len(e.pods()) != 0 || r.Status.Phase != aiv1.ResearchPhaseScheduled || r.Status.LastResultID != r.Status.Run.ID {
		t.Errorf("after the run: pods %d, status %+v", len(e.pods()), r.Status)
	}
	e.reconcile("news")
	if len(e.pods()) != 0 {
		t.Error("ran again before the next scheduled time")
	}
	e.now = seven.Add(24 * time.Hour)
	e.reconcile("news")
	if len(e.pods()) != 1 {
		t.Error("the next day's run did not start")
	}
}

func TestResearchMissedRunCatchesUpOnce(t *testing.T) {
	r := newResearch("news", "0 * * * *")
	r.Spec.TimeZone = ""
	e := newResearchEnv(t, researchProject(), writerSecret(), r)
	e.now = t0.Add(5*time.Hour + 30*time.Minute)
	e.reconcile("news")
	if got := e.research("news").Status; len(e.pods()) != 1 || !got.NextRun.Time.Equal(t0.Add(6*time.Hour)) {
		t.Fatalf("one catch-up run: pods %d, nextRun %v", len(e.pods()), got.NextRun)
	}
}

func TestResearchSkipsScheduledTimeWhileRunning(t *testing.T) {
	r := newResearch("news", "0 * * * *")
	r.Spec.TimeZone = ""
	r.Spec.Depth = aiv1.ResearchDepthDeep
	e := newResearchEnv(t, researchProject(), writerSecret(), r)
	e.now = t0.Add(time.Hour)
	e.reconcile("news")
	first := e.research("news").Status.Run
	if first == nil || first.Model != "claude-opus-5-5" || first.Effort != "high" {
		t.Fatalf("run %+v", first)
	}

	// 14:00 comes while the deep run is still going.
	e.now = t0.Add(2 * time.Hour)
	e.reconcile("news")
	st := e.research("news").Status
	if len(e.pods()) != 1 || st.Run.ID != first.ID || st.SkippedReason != SkippedRunInProgress ||
		!st.NextRun.Time.Equal(t0.Add(3*time.Hour)) || st.Phase != aiv1.ResearchPhaseRunning {
		t.Fatalf("skip: pods %d, status %+v", len(e.pods()), st)
	}

	// The run ends at 14:30: the skipped time does not run late.
	e.now = t0.Add(2*time.Hour + 30*time.Minute)
	e.report("news", aiv1.ResearchOutcomeSucceeded, "")
	e.reconcile("news")
	e.reconcile("news")
	if len(e.pods()) != 0 {
		t.Fatal("the skipped time ran after the previous run")
	}
	e.now = t0.Add(3 * time.Hour)
	e.reconcile("news")
	st = e.research("news").Status
	if len(e.pods()) != 1 || st.Run.ID == first.ID || st.SkippedReason != "" {
		t.Errorf("the next time runs and clears the skip: pods %d, status %+v", len(e.pods()), st)
	}
}

func TestResearchPodFailureFailsRun(t *testing.T) {
	for name, end := range map[string]func(*researchEnv){
		"deadline": func(e *researchEnv) { e.endPod(corev1.PodFailed, "DeadlineExceeded") },
		"gone": func(e *researchEnv) {
			pod := e.pods()[0]
			if err := e.c.Delete(e.ctx, &pod); err != nil {
				t.Fatal(err)
			}
		},
		"never started": func(e *researchEnv) { e.now = t0.Add(time.Hour) },
	} {
		t.Run(name, func(t *testing.T) {
			e := newResearchEnv(t, researchProject(), writerSecret(), newResearch("q", ""))
			e.reconcile("q")
			end(e)
			e.reconcile("q")
			st := e.research("q").Status
			if st.Run.Outcome != aiv1.ResearchOutcomeFailed || !strings.HasPrefix(st.Run.Message, "no report from the runner") ||
				st.Phase != aiv1.ResearchPhaseFailed || st.LastResultID != "" || st.Run.FinishedAt == nil {
				t.Errorf("status %+v, run %+v", st, st.Run)
			}
			if len(e.db.runs) != 1 || e.db.runs[0].Status != store.StatusFailed || e.db.runs[0].Error != st.Run.Message {
				t.Errorf("store %+v", e.db.runs)
			}
			if len(e.pods()) != 0 {
				t.Error("the pod is deleted")
			}
		})
	}
}

func TestResearchRunsAreNotEndedEarly(t *testing.T) {
	e := newResearchEnv(t, researchProject(), writerSecret(), newResearch("q", ""))
	e.reconcile("q")
	e.now = t0.Add(aiv1.QuickResearchTimeout)
	res := e.reconcile("q")
	if st := e.research("q").Status; st.Run.FinishedAt != nil || len(e.pods()) != 1 || len(e.db.runs) != 0 {
		t.Fatalf("a running pod within its deadline keeps running: %+v", st.Run)
	}
	if res.RequeueAfter <= 0 || res.RequeueAfter > researchDeadlineSlack+researchRunGrace+time.Second {
		t.Errorf("requeue at the run's deadline, got %v", res.RequeueAfter)
	}
}

// TestResearchPodFailureInStore runs against Postgres: the run the runner never
// started is in the store as failed.
func TestResearchPodFailureInStore(t *testing.T) {
	s := storetest.New(t)
	e := newResearchEnv(t, researchProject(), writerSecret(), newResearch("q", ""))
	ready := make(chan struct{})
	close(ready)
	e.r.DB = storeDB{ready: ready, Store: s}
	e.reconcile("q")
	e.endPod(corev1.PodFailed, "Evicted")
	e.reconcile("q")
	run := e.research("q").Status.Run
	status, msg, ok := storetest.RunStatus(t, run.ID)
	if !ok || status != store.StatusFailed || msg != run.Message || !strings.Contains(msg, "Evicted") {
		t.Errorf("store: %v %q %q, status %q", ok, status, msg, run.Message)
	}
}

type storeDB struct {
	ready chan struct{}
	*store.Store
}

func (d storeDB) Ready() <-chan struct{} { return d.ready }

func TestResearchConditions(t *testing.T) {
	noKey := writerSecret()
	delete(noKey.Data, "password")
	noWriter := researchProject()
	noWriter.Spec.Research = nil
	badSchedule := newResearch("q", "61 * * * *")
	badZone := newResearch("q", "0 7 * * *")
	badZone.Spec.TimeZone = "Mars/Olympus"
	badAgent := newResearch("q", "")
	badAgent.Spec.Agent = &aiv1.ResearchAgent{Engine: aiv1.EngineClaude, Model: "gpt-9"}
	otherProject := newResearch("q", "")
	otherProject.Spec.ProjectRef = "missing"
	cursorOnly := researchProject()
	cursorOnly.Spec.Research.Candidates.Quick = []aiv1.ResearchAgent{{Engine: aiv1.EngineCursor, Model: "auto"}}

	for name, tc := range map[string]struct {
		objs   []client.Object
		reason string
		msg    string
	}{
		"no writerSecret":  {[]client.Object{noWriter, writerSecret(), newResearch("q", "")}, ReasonWriterSecretMissing, "spec.research.writerSecret"},
		"no Secret":        {[]client.Object{researchProject(), newResearch("q", "")}, ReasonWriterSecretMissing, `"writer" does not exist`},
		"no password key":  {[]client.Object{researchProject(), noKey, newResearch("q", "")}, ReasonWriterSecretMissing, `no key "password"`},
		"invalid schedule": {[]client.Object{researchProject(), writerSecret(), badSchedule}, "InvalidSchedule", "spec.schedule"},
		"invalid zone":     {[]client.Object{researchProject(), writerSecret(), badZone}, "InvalidSchedule", "Mars/Olympus"},
		"invalid agent":    {[]client.Object{researchProject(), writerSecret(), badAgent}, "InvalidAgent", "gpt-9"},
		"missing project":  {[]client.Object{researchProject(), writerSecret(), otherProject}, "ProjectNotFound", "missing"},
		"no usable agent":  {[]client.Object{cursorOnly, writerSecret(), newResearch("q", "")}, "NoAgent", "cursor/auto: the project does not use cursor"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newResearchEnv(t, tc.objs...)
			if res := e.reconcile("q"); res.RequeueAfter != researchInvalidRetry {
				t.Errorf("requeue %v", res.RequeueAfter)
			}
			c := condition(e.research("q"), ConditionValid)
			if c == nil || c.Status != metav1.ConditionFalse || c.Reason != tc.reason || !strings.Contains(c.Message, tc.msg) {
				t.Errorf("Valid %+v", c)
			}
			if len(e.pods()) != 0 || e.research("q").Status.Run != nil {
				t.Error("an invalid research creates no pod")
			}
		})
	}
}

func TestResearchWaitsForDatabase(t *testing.T) {
	e := newResearchEnv(t, researchProject(), writerSecret(), newResearch("q", ""))
	e.r.DB = newFakeResearchDB(false)
	if res := e.reconcile("q"); res.RequeueAfter != researchDBRetry {
		t.Errorf("requeue %v", res.RequeueAfter)
	}
	c := condition(e.research("q"), ConditionDatabaseReady)
	if c == nil || c.Status != metav1.ConditionFalse || len(e.pods()) != 0 {
		t.Errorf("DatabaseReady %+v, pods %d", c, len(e.pods()))
	}
}

func TestResearchProjectCandidatesOverride(t *testing.T) {
	p := researchProject()
	p.Spec.Research.Candidates.Quick = []aiv1.ResearchAgent{{Engine: aiv1.EngineClaude, Model: "haiku"}}
	e := newResearchEnv(t, p, writerSecret(), newResearch("q", ""))
	e.reconcile("q")
	if run := e.research("q").Status.Run; run == nil || run.Model != "claude-haiku-5-5" {
		t.Errorf("run %+v", run)
	}

	fixed := newResearch("f", "")
	// The effort is capped at agents.maxEffort (high by default).
	fixed.Spec.Agent = &aiv1.ResearchAgent{Engine: aiv1.EngineClaude, Model: "opus", Effort: "max"}
	e = newResearchEnv(t, p, writerSecret(), fixed)
	e.reconcile("f")
	if run := e.research("f").Status.Run; run == nil || run.Model != "claude-opus-5-5" || run.Effort != "high" || run.Reason != "chosen: claude/claude-opus-5-5" {
		t.Errorf("run %+v", run)
	}
}

// usageProject is a project with cursor allowed, whose claude 5-hour window
// is at percent until resets, and with the given limits.
func usageProject(percent int32, resets time.Time, limits ...aiv1.UsageLimit) *aiv1.AgentProject {
	p := researchProject()
	p.Spec.Agents.Models = append(aiv1.DefaultModels(), aiv1.ModelOption{Engine: aiv1.EngineCursor, Model: "auto"})
	p.Status.EngineUsage = []aiv1.EngineUsage{{Engine: aiv1.EngineClaude, ObservedAt: metav1.NewTime(t0.Add(-time.Minute)),
		Windows: []aiv1.UsageWindow{{Name: "five_hour", Percent: percent, ResetsAt: &metav1.Time{Time: resets}}}}}
	p.Status.UsageLimits = limits
	return p
}

func cursorLimit(until time.Time) aiv1.UsageLimit {
	return aiv1.UsageLimit{Engine: aiv1.EngineCursor, Until: metav1.NewTime(until)}
}

func newChoiceEnv(t *testing.T, p *aiv1.AgentProject, r *aiv1.AgentResearch) *researchEnv {
	e := newResearchEnv(t, p, writerSecret(), r)
	e.r.Candidates.Quick = []aiv1.ResearchAgent{{Engine: aiv1.EngineClaude, Model: "claude-sonnet-5-5"}, {Engine: aiv1.EngineCursor, Model: "auto"}}
	e.r.MaxUsagePercent = 70
	return e
}

func TestResearchChoosesNextCandidateOverThreshold(t *testing.T) {
	e := newChoiceEnv(t, usageProject(85, t0.Add(2*time.Hour)), newResearch("q", ""))
	e.reconcile("q")
	run := e.research("q").Status.Run
	want := "chosen: cursor/auto (sonnet: 5-hour window at 85 %)"
	if run == nil || run.Engine != aiv1.EngineCursor || run.Model != "auto" || run.Effort != "" || run.Reason != want {
		t.Fatalf("run %+v", run)
	}
	e.report("q", aiv1.ResearchOutcomeSucceeded, "")
	e.reconcile("q")
	if len(e.db.runs) != 1 || e.db.runs[0].ChoiceReason != want {
		t.Errorf("the choice reason is stored with the run: %+v", e.db.runs)
	}
}

func TestResearchScheduledSkipsUntilReset(t *testing.T) {
	seven := time.Date(2026, 10, 2, 5, 0, 0, 0, time.UTC) // 07:00 Berlin
	reset := seven.Add(3 * time.Hour)
	e := newChoiceEnv(t, usageProject(85, reset, cursorLimit(seven.Add(5*time.Hour))), newResearch("news", "0 7 * * *"))
	e.now = seven.Add(-time.Hour)
	e.reconcile("news")

	e.now = seven
	res := e.reconcile("news")
	st := e.research("news").Status
	if len(e.pods()) != 0 || st.Run != nil {
		t.Fatalf("every candidate is busy, nothing runs: pods %d, run %+v", len(e.pods()), st.Run)
	}
	want := "no candidate qualifies: sonnet: 5-hour window at 85 % until 2026-10-02 08:00 UTC; cursor limited until 2026-10-02 10:00 UTC"
	if st.SkippedReason != want || st.Phase != aiv1.ResearchPhaseScheduled ||
		st.Waiting == nil || st.Waiting.Engine != aiv1.EngineClaude || !st.Waiting.Until.Time.Equal(reset) {
		t.Fatalf("skip: %+v, waiting %+v", st, st.Waiting)
	}
	if !st.NextRun.Time.Equal(reset) || res.RequeueAfter != 3*time.Hour+time.Second {
		t.Errorf("retry at the reset: nextRun %v, requeue %v", st.NextRun, res.RequeueAfter)
	}

	// At the reset the stale reading no longer counts.
	e.now = reset
	e.reconcile("news")
	st = e.research("news").Status
	if len(e.pods()) != 1 || st.Run == nil || st.Run.Engine != aiv1.EngineClaude || st.SkippedReason != "" || st.Waiting != nil {
		t.Fatalf("retry at the reset: pods %d, status %+v", len(e.pods()), st)
	}
	if !st.NextRun.Time.Equal(seven.Add(24 * time.Hour)) {
		t.Errorf("nextRun %v, want the next scheduled time", st.NextRun)
	}
}

func TestResearchScheduledSkipsToNextTimeBeforeReset(t *testing.T) {
	r := newResearch("news", "0 * * * *")
	r.Spec.TimeZone = ""
	e := newChoiceEnv(t, usageProject(85, t0.Add(4*time.Hour), cursorLimit(t0.Add(5*time.Hour))), r)
	e.now = t0.Add(time.Hour)
	e.reconcile("news")
	st := e.research("news").Status
	if len(e.pods()) != 0 || !st.NextRun.Time.Equal(t0.Add(2*time.Hour)) || !st.Waiting.Until.Time.Equal(t0.Add(4*time.Hour)) {
		t.Errorf("the next scheduled time comes first: pods %d, nextRun %v, waiting %+v", len(e.pods()), st.NextRun, st.Waiting)
	}
}

func TestResearchOneOffWaitsForReset(t *testing.T) {
	claudeLimit := aiv1.UsageLimit{Engine: aiv1.EngineClaude, Until: metav1.NewTime(t0.Add(2 * time.Hour))}
	e := newChoiceEnv(t, usageProject(10, t0.Add(time.Hour), claudeLimit, cursorLimit(t0.Add(3*time.Hour))), newResearch("q", ""))
	res := e.reconcile("q")
	st := e.research("q").Status
	if len(e.pods()) != 0 || st.Run != nil || st.Phase != aiv1.ResearchPhaseWaiting || st.SkippedReason != "" ||
		st.Waiting == nil || st.Waiting.Engine != aiv1.EngineClaude || !st.Waiting.Until.Time.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("waiting: pods %d, status %+v", len(e.pods()), st)
	}
	if res.RequeueAfter != 2*time.Hour+time.Second {
		t.Errorf("requeue %v", res.RequeueAfter)
	}

	e.now = t0.Add(2 * time.Hour)
	e.reconcile("q")
	st = e.research("q").Status
	if len(e.pods()) != 1 || st.Run == nil || st.Run.Engine != aiv1.EngineClaude || st.Phase != aiv1.ResearchPhaseRunning || st.Waiting != nil {
		t.Errorf("runs after the limit: pods %d, status %+v", len(e.pods()), st)
	}
}

func TestResearchFixedAgentOverThreshold(t *testing.T) {
	sonnet := &aiv1.ResearchAgent{Engine: aiv1.EngineClaude, Model: "sonnet"}
	oneOff := newResearch("q", "")
	oneOff.Spec.Agent = sonnet
	e := newChoiceEnv(t, usageProject(90, t0.Add(time.Hour)), oneOff)
	e.reconcile("q")
	if st := e.research("q").Status; len(e.pods()) != 0 || st.Phase != aiv1.ResearchPhaseWaiting || st.Waiting == nil {
		t.Errorf("a fixed agent over the threshold waits: pods %d, status %+v", len(e.pods()), st)
	}

	scheduled := newResearch("news", "0 * * * *")
	scheduled.Spec.Agent = sonnet
	e = newChoiceEnv(t, usageProject(90, t0.Add(2*time.Hour)), scheduled)
	e.now = t0.Add(time.Hour)
	e.reconcile("news")
	st := e.research("news").Status
	if len(e.pods()) != 0 || !strings.Contains(st.SkippedReason, "sonnet: 5-hour window at 90 %") || st.Waiting == nil {
		t.Errorf("a fixed agent over the threshold is skipped: pods %d, status %+v", len(e.pods()), st)
	}
}

// reportLimited simulates the runner reporting a run that hit a limit.
func (e *researchEnv) reportLimited(name string, engine aiv1.Engine, until time.Time) {
	e.t.Helper()
	r := e.research(name)
	orig := r.DeepCopy()
	r.Status.Run.Outcome = aiv1.ResearchOutcomeLimited
	r.Status.Run.UsageLimits = []aiv1.UsageLimit{{Engine: engine, Until: metav1.NewTime(until)}}
	if err := e.c.Status().Patch(e.ctx, r, client.MergeFrom(orig)); err != nil {
		e.t.Fatal(err)
	}
}

func TestResearchLimitedRunIsRetried(t *testing.T) {
	t.Run("one-off on the next candidate", func(t *testing.T) {
		e := newChoiceEnv(t, usageProject(10, t0.Add(time.Hour)), newResearch("q", ""))
		e.reconcile("q")
		first := e.research("q").Status.Run
		e.now = t0.Add(5 * time.Minute)
		e.reportLimited("q", aiv1.EngineClaude, t0.Add(4*time.Hour))
		e.reconcile("q")
		st := e.research("q").Status
		if st.Run == nil || st.Run.ID == first.ID || st.Run.Engine != aiv1.EngineCursor ||
			st.Run.Reason != "chosen: cursor/auto (claude limited until 2026-10-01 16:00 UTC)" || len(e.pods()) != 1 {
			t.Fatalf("retried on cursor: pods %d, run %+v", len(e.pods()), st.Run)
		}
		if len(e.db.runs) != 1 || e.db.runs[0].Status != store.StatusLimited {
			t.Errorf("the limited run is recorded: %+v", e.db.runs)
		}
	})
	t.Run("scheduled, skipped until the limit ends", func(t *testing.T) {
		r := newResearch("news", "0 */6 * * *")
		r.Spec.TimeZone = ""
		e := newResearchEnv(t, researchProject(), writerSecret(), r) // claude only
		e.reconcile("news")
		e.now = t0.Add(6 * time.Hour)
		e.reconcile("news")
		if len(e.pods()) != 1 {
			t.Fatal("the 18:00 run did not start")
		}
		e.now = t0.Add(6*time.Hour + 5*time.Minute)
		e.reportLimited("news", aiv1.EngineClaude, t0.Add(8*time.Hour))
		res := e.reconcile("news")
		st := e.research("news").Status
		if len(e.pods()) != 0 || !st.NextRun.Time.Equal(t0.Add(8*time.Hour)) || st.Waiting == nil ||
			st.SkippedReason != "no candidate qualifies: claude limited until 2026-10-01 20:00 UTC" {
			t.Fatalf("skipped until the limit ends: pods %d, status %+v", len(e.pods()), st)
		}
		if res.RequeueAfter != 2*time.Hour-5*time.Minute+time.Second {
			t.Errorf("requeue %v", res.RequeueAfter)
		}
		e.now = t0.Add(8 * time.Hour)
		e.reconcile("news")
		if st := e.research("news").Status; len(e.pods()) != 1 || !st.NextRun.Time.Equal(t0.Add(12*time.Hour)) {
			t.Errorf("retried when the limit ended: pods %d, nextRun %v", len(e.pods()), st.NextRun)
		}
	})
}

func TestResearchFinishRunRecordsCalls(t *testing.T) {
	e := newResearchEnv(t, researchProject(), writerSecret(), newResearch("q", ""))
	e.r.HistoryRetention = 30 * 24 * time.Hour
	e.reconcile("q")
	r := e.research("q")
	orig := r.DeepCopy()
	r.Status.Run.Outcome = aiv1.ResearchOutcomeSucceeded
	r.Status.Run.Calls = []aiv1.AgentCall{
		{Seq: 1, At: metav1.Time{Time: t0}, Kind: aiv1.CallKindResearch, Role: "research", Engine: aiv1.EngineClaude,
			InputTokens: 10, CostUSD: "0.3000", CostSource: aiv1.CostReported},
		{Seq: 2, At: metav1.Time{Time: t0.Add(time.Minute)}, Kind: aiv1.CallKindResearch, Role: "research",
			Engine: aiv1.EngineClaude, Resumed: true, CostSource: aiv1.CostUnknown},
	}
	if err := e.c.Status().Patch(e.ctx, r, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	run := r.Status.Run

	// The first attempt fails at the calls: finishRun is retried.
	e.db.callsErr = errors.New("connection reset")
	e.endPod(corev1.PodSucceeded, "")
	if _, err := e.r.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: "q"}}); err == nil ||
		!strings.Contains(err.Error(), "agent calls") {
		t.Fatalf("the insert error must be returned, got %v", err)
	}
	if e.research("q").Status.Run.FinishedAt != nil {
		t.Fatal("the run must stay unfinished until its calls are written")
	}

	e.db.callsErr = nil
	e.reconcile("q")
	if e.research("q").Status.Run.FinishedAt == nil {
		t.Fatal("the run must be finished")
	}
	if len(e.db.calls) != 2 {
		t.Fatalf("want 2 calls, got %+v", e.db.calls)
	}
	c, ok := e.db.calls["research/uid-q/"+run.ID+"/1"]
	if !ok || c.Namespace != ns || c.Project != "shop" || c.Task != "q" || c.Run != 0 || c.Seq != 1 ||
		c.Kind != aiv1.CallKindResearch || c.InputTokens != 10 || c.CostUSD == nil || *c.CostUSD != 0.3 ||
		!c.ExpiresAt.Equal(t0.Add(30*24*time.Hour)) {
		t.Errorf("call 1: %+v", c)
	}
	if c := e.db.calls["research/uid-q/"+run.ID+"/2"]; c.CostUSD != nil || c.CostSource != aiv1.CostUnknown || !c.Resumed {
		t.Errorf("call 2: %+v", c)
	}

	// Recording the same run again writes no call twice.
	again := e.research("q")
	if err := e.r.finishRun(e.ctx, again, again.DeepCopy(), nil, "", e.now); err != nil {
		t.Fatal(err)
	}
	if e.db.inserted != 2 {
		t.Errorf("calls written twice: %d", e.db.inserted)
	}
}
