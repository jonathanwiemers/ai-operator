package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/history"
)

func TestTaskStalledGauge(t *testing.T) {
	const ns, name = "shop", "metric-stall-1"
	clearTaskStalled(ns, name)
	t.Cleanup(func() { clearTaskStalled(ns, name) })

	before := testutil.CollectAndCount(taskStalled)

	setTaskStalled(ns, name, aiv1.StallReasonSilent)
	if got := testutil.ToFloat64(taskStalled.WithLabelValues(ns, name, aiv1.StallReasonSilent)); got != 1 {
		t.Fatalf("stalled Silent want 1, got %v", got)
	}
	if n := testutil.CollectAndCount(taskStalled); n != before+1 {
		t.Fatalf("set adds one series, before=%d got=%d", before, n)
	}

	setTaskStalled(ns, name, aiv1.StallReasonPodPending)
	if got := testutil.ToFloat64(taskStalled.WithLabelValues(ns, name, aiv1.StallReasonPodPending)); got != 1 {
		t.Fatalf("stalled PodPending want 1, got %v", got)
	}
	if taskStalled.DeleteLabelValues(ns, name, aiv1.StallReasonSilent) {
		t.Fatal("changing reason must drop the previous series")
	}
	if n := testutil.CollectAndCount(taskStalled); n != before+1 {
		t.Fatalf("reason change keeps one series for the task, before=%d got=%d", before, n)
	}

	clearTaskStalled(ns, name)
	if n := testutil.CollectAndCount(taskStalled); n != before {
		t.Fatalf("clear must remove the task's series, before=%d got=%d", before, n)
	}
}

func TestSyncTaskStalledMetric(t *testing.T) {
	task := &aiv1.AgentTask{}
	task.Namespace, task.Name = "shop", "metric-stall-sync"
	clearTaskStalled(task.Namespace, task.Name)
	t.Cleanup(func() { clearTaskStalled(task.Namespace, task.Name) })

	task.Status.Conditions = []metav1.Condition{{
		Type: aiv1.ConditionStalled, Status: metav1.ConditionTrue, Reason: aiv1.StallReasonSilent,
	}}
	syncTaskStalledMetric(task)
	if got := testutil.ToFloat64(taskStalled.WithLabelValues(task.Namespace, task.Name, aiv1.StallReasonSilent)); got != 1 {
		t.Fatalf("Stalled=True syncs the gauge, got %v", got)
	}

	task.Status.Conditions[0].Reason = aiv1.StallReasonCrashLoop
	syncTaskStalledMetric(task)
	if got := testutil.ToFloat64(taskStalled.WithLabelValues(task.Namespace, task.Name, aiv1.StallReasonCrashLoop)); got != 1 {
		t.Fatalf("reason change syncs the gauge, got %v", got)
	}
	if taskStalled.DeleteLabelValues(task.Namespace, task.Name, aiv1.StallReasonSilent) {
		t.Fatal("previous reason series must be gone")
	}

	task.Status.Conditions[0].Status = metav1.ConditionFalse
	task.Status.Conditions[0].Reason = aiv1.StallReasonActive
	syncTaskStalledMetric(task)
	if taskStalled.DeleteLabelValues(task.Namespace, task.Name, aiv1.StallReasonCrashLoop) {
		t.Fatal("Stalled=False must clear the series")
	}

	task.Status.Conditions = nil
	setTaskStalled(task.Namespace, task.Name, aiv1.StallReasonPodPending)
	syncTaskStalledMetric(task)
	if taskStalled.DeleteLabelValues(task.Namespace, task.Name, aiv1.StallReasonPodPending) {
		t.Fatal("missing Stalled condition must clear the series")
	}

	// A compromised status write with a unique reason must not export a series.
	const junk = "x-attack-1"
	task.Status.Conditions = []metav1.Condition{{
		Type: aiv1.ConditionStalled, Status: metav1.ConditionTrue, Reason: junk,
	}}
	taskStalled.WithLabelValues(task.Namespace, task.Name, junk).Set(1)
	syncTaskStalledMetric(task)
	if taskStalled.DeleteLabelValues(task.Namespace, task.Name, junk) {
		t.Fatal("unknown Stalled reason must clear, not export, the series")
	}
}

type statsSource struct{ st gitlab.Stats }

func (s statsSource) Stats() gitlab.Stats { return s.st }

func TestCountGitLabRequests(t *testing.T) {
	const a, b = "metric-req-a", "metric-req-b"
	t.Cleanup(func() {
		gitlabRequests.DeletePartialMatch(prometheus.Labels{"owner": a})
		gitlabRequests.DeletePartialMatch(prometheus.Labels{"owner": b})
	})
	val := func(kind, owner string) float64 {
		return testutil.ToFloat64(gitlabRequests.WithLabelValues(kind, owner, "unknown"))
	}

	before := testutil.CollectAndCount(gitlabRequests)
	countGitLabRequests(a, statsSource{})
	countGitLabRequests(a, "not a client")
	countGitLabRequests(a, nil)
	if n := testutil.CollectAndCount(gitlabRequests); n != before {
		t.Fatalf("series = %d, want %d: nothing was sent", n, before)
	}

	countGitLabRequests(a, statsSource{gitlab.Stats{Requests: 5, GraphQL: 2}})
	if val("rest", a) != 3 || val("graphql", a) != 2 {
		t.Fatalf("a: rest %v graphql %v, want 3 and 2", val("rest", a), val("graphql", a))
	}
	countGitLabRequests(b, statsSource{gitlab.Stats{Requests: 4}})
	if val("rest", b) != 4 || val("rest", a) != 3 {
		t.Fatalf("owners mixed: a %v, b %v", val("rest", a), val("rest", b))
	}
	if n := testutil.CollectAndCount(gitlabRequests); n != before+3 {
		t.Fatalf("series = %d, want %d: no graphql series for b", n, before+3)
	}

	// A client that splits by method gets one series per method; requests
	// it did not attribute stay unknown.
	countGitLabRequests(b, statsSource{gitlab.Stats{Requests: 6, GraphQL: 1, Ops: map[gitlab.OpKey]int64{
		{Kind: "rest", Op: "ListIssues"}: 3, {Kind: "graphql", Op: "DiscoverGroup"}: 1,
	}}})
	if v := testutil.ToFloat64(gitlabRequests.WithLabelValues("rest", b, "ListIssues")); v != 3 {
		t.Errorf("ListIssues = %v, want 3", v)
	}
	if v := testutil.ToFloat64(gitlabRequests.WithLabelValues("graphql", b, "DiscoverGroup")); v != 1 {
		t.Errorf("DiscoverGroup = %v, want 1", v)
	}
	if val("rest", b) != 6 || val("graphql", b) != 0 {
		t.Errorf("unknown: rest %v graphql %v, want 4+2 and 0", val("rest", b), val("graphql", b))
	}
}

// A poll counts every request the real client sent to the server.
func TestPollCountsGitLabRequests(t *testing.T) {
	e := newProjectEnv(t)
	t.Cleanup(func() { gitlabRequests.DeletePartialMatch(prometheus.Labels{"owner": "shop"}) })
	var seen atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/user") {
			_, _ = w.Write([]byte(`{"id":1,"username":"bot"}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	e.r.NewGitLab = func(_, token string) gitlab.API { return gitlab.New(srv.URL, token) }
	e.r.History = history.NewRecorder(nil, time.Hour, clock.RealClock{}, logr.Discard())

	restShop := func() float64 {
		mfs, _ := metrics.Registry.Gather()
		var sum float64
		for _, mf := range mfs {
			if mf.GetName() != "ai_operator_gitlab_requests_total" {
				continue
			}
			for _, m := range mf.GetMetric() {
				l := map[string]string{}
				for _, p := range m.GetLabel() {
					l[p.GetName()] = p.GetValue()
				}
				if l["kind"] == "rest" && l["owner"] == "shop" {
					sum += m.GetCounter().GetValue()
				}
			}
		}
		return sum
	}
	before := restShop()
	e.reconcileFailing()
	got := restShop() - before
	if seen.Load() == 0 || got != float64(seen.Load()) {
		t.Fatalf("counter grew by %v, the server saw %d requests", got, seen.Load())
	}
	if n := testutil.CollectAndCount(gitlabRequests, "ai_operator_gitlab_requests_total"); n < 1 {
		t.Fatalf("no series")
	}
}
