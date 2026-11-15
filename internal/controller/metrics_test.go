package controller

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
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
