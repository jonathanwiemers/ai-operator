package ui

import (
	"context"
	"net/http"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// fakePods is a log source without logs that serves fixed pods.
type fakePods struct {
	fakeLogs
	pods map[string]*corev1.Pod
}

func (f fakePods) Pod(ctx context.Context, ns, name string) (*corev1.Pod, error) {
	if p, ok := f.pods[ns+"/"+name]; ok {
		return p, nil
	}
	return f.fakeLogs.Pod(ctx, ns, name)
}

func terminated(reason string, at time.Time) corev1.ContainerState {
	return corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: reason, FinishedAt: metav1.NewTime(at)}}
}

func TestTaskHealth(t *testing.T) {
	env := newTestEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}})
	ctx := context.Background()
	const name = "shop-42-add-paypal"
	since := metav1.NewTime(time.Date(2026, 11, 12, 18, 51, 54, 0, time.UTC))
	var task aiv1.AgentTask
	if err := env.reader.Get(ctx, client.ObjectKey{Namespace: "team-a", Name: name}, &task); err != nil {
		t.Fatal(err)
	}
	task.Status.Run.CurrentStep, task.Status.Run.CurrentStepSince = "18 push", &since
	apimeta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
		Type: aiv1.ConditionStalled, Status: metav1.ConditionTrue, Reason: "CrashLoop", Message: "the pod crash loops in step 18 push",
	})
	if err := env.reader.Update(ctx, &task); err != nil {
		t.Fatal(err)
	}
	get := func() *TaskHealth {
		t.Helper()
		var tr TaskResponse
		if code := env.get(t, shopTask, &tr); code != http.StatusOK || tr.Health == nil {
			t.Fatalf("task: status %d, health %+v", code, tr.Health)
		}
		return tr.Health
	}
	const stall = `"step":"18 push","stepSince":"2026-11-12T18:51:54Z","stalled":true,"stalledReason":"CrashLoop","stalledMessage":"the pod crash loops in step 18 push"`

	// Without a log source the pod fields stay empty.
	sameJSON(t, get(), `{`+stall+`}`)

	// The pod: restarts are summed, the newest termination wins.
	t0 := since.Time
	env.server.SetLogSource(fakePods{pods: map[string]*corev1.Pod{"team-a/" + name: {Status: corev1.PodStatus{
		Phase:                 corev1.PodRunning,
		InitContainerStatuses: []corev1.ContainerStatus{{Name: "init", RestartCount: 1, LastTerminationState: terminated("OOMKilled", t0.Add(time.Minute))}},
		ContainerStatuses: []corev1.ContainerStatus{
			{Name: "runner", RestartCount: 4, LastTerminationState: terminated("CrashLoopBackOff", t0.Add(3*time.Minute)),
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}},
			{Name: "agent", RestartCount: 2, LastTerminationState: terminated("Error", t0.Add(2*time.Minute))},
		},
	}}}})
	sameJSON(t, get(), `{"podPhase":"Running","restarts":7,"lastTermination":"CrashLoopBackOff",`+stall+`}`)

	// No pod: the pod fields stay empty; a cleared stall is not shown.
	env.server.SetLogSource(fakeLogs{})
	apimeta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{Type: aiv1.ConditionStalled, Status: metav1.ConditionFalse, Reason: "Moving"})
	if err := env.reader.Update(ctx, &task); err != nil {
		t.Fatal(err)
	}
	sameJSON(t, get(), `{"step":"18 push","stepSince":"2026-11-12T18:51:54Z"}`)
}
