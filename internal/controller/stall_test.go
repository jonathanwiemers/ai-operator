package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestStallOf(t *testing.T) {
	now := time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	pod := func(phase corev1.PodPhase, created time.Duration, waiting ...string) *corev1.Pod {
		p := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(*ago(created))},
			Status:     corev1.PodStatus{Phase: phase},
		}
		for _, r := range waiting {
			p.Status.ContainerStatuses = append(p.Status.ContainerStatuses, corev1.ContainerStatus{
				Name: AgentContainer, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: r}},
			})
		}
		return p
	}
	after := 30 * time.Minute
	cases := []struct {
		name        string
		pod         *corev1.Pod
		last        map[string]*time.Time
		since       *time.Time
		wantReason  string
		wantMessage string
	}{
		{"both silent", pod(corev1.PodRunning, 2*time.Hour),
			map[string]*time.Time{RunnerContainer: ago(50 * time.Minute), AgentContainer: ago(42*time.Minute + 20*time.Second)},
			ago(time.Hour), aiv1.StallReasonSilent, "no log line for 42m in step 18 push"},
		{"silent over an hour", pod(corev1.PodRunning, 3*time.Hour),
			map[string]*time.Time{RunnerContainer: ago(65 * time.Minute), AgentContainer: ago(2 * time.Hour)},
			ago(3 * time.Hour), aiv1.StallReasonSilent, "no log line for 1h5m in step 18 push"},
		{"runner silent, agent recent", pod(corev1.PodRunning, 2*time.Hour),
			map[string]*time.Time{RunnerContainer: ago(50 * time.Minute), AgentContainer: ago(time.Minute)},
			ago(time.Hour), "", ""},
		{"agent silent, runner recent", pod(corev1.PodRunning, 2*time.Hour),
			map[string]*time.Time{RunnerContainer: ago(10 * time.Second), AgentContainer: ago(time.Hour)},
			ago(time.Hour), "", ""},
		{"no lines yet, run started long ago", pod(corev1.PodRunning, 2*time.Hour),
			map[string]*time.Time{}, ago(40 * time.Minute), aiv1.StallReasonSilent, "no log line for 40m in step 18 push"},
		{"no lines yet, run just started", pod(corev1.PodRunning, 2*time.Hour),
			map[string]*time.Time{RunnerContainer: nil}, ago(10 * time.Minute), "", ""},
		{"no agent line yet, runner silent, pod recreated", pod(corev1.PodRunning, 5*time.Minute),
			map[string]*time.Time{RunnerContainer: ago(time.Hour)}, ago(2 * time.Hour), "", ""},
		{"old lines from before the run start", pod(corev1.PodRunning, 3*time.Hour),
			map[string]*time.Time{RunnerContainer: ago(2 * time.Hour), AgentContainer: ago(2 * time.Hour)},
			ago(5 * time.Minute), "", ""},
		{"pending long", pod(corev1.PodPending, 42*time.Minute), nil, ago(time.Hour),
			aiv1.StallReasonPodPending, "pod pending for 42m"},
		{"pending young", pod(corev1.PodPending, 10*time.Minute), nil, ago(time.Hour), "", ""},
		{"crash loop long", pod(corev1.PodRunning, time.Hour, "CrashLoopBackOff"), nil, ago(45 * time.Minute),
			aiv1.StallReasonCrashLoop, "container agent in CrashLoopBackOff, 45m into the run"},
		{"image pull while pending", pod(corev1.PodPending, time.Hour, "ImagePullBackOff"), nil, ago(2 * time.Hour),
			aiv1.StallReasonCrashLoop, "container agent in ImagePullBackOff, 1h0m into the run"},
		{"crash loop young", pod(corev1.PodRunning, time.Hour, "CrashLoopBackOff"), nil, ago(20 * time.Minute), "", ""},
		{"waiting to start is not stuck", pod(corev1.PodPending, 5*time.Minute, "ContainerCreating"), nil, ago(time.Hour), "", ""},
		{"pod finished", pod(corev1.PodSucceeded, 2*time.Hour), nil, ago(2 * time.Hour), "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reason, message, stalled := stallOf(c.pod, c.last, *c.since, now, after, "18 push")
			if reason != c.wantReason || message != c.wantMessage || stalled != (c.wantReason != "") {
				t.Errorf("stallOf = %q, %q, %v; want %q, %q", reason, message, stalled, c.wantReason, c.wantMessage)
			}
		})
	}
}

func TestStallClearsOnNewLine(t *testing.T) {
	now := time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC)
	p := &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	old, fresh := now.Add(-time.Hour), now.Add(-time.Second)
	last := map[string]*time.Time{RunnerContainer: &old, AgentContainer: &old}
	if _, _, stalled := stallOf(p, last, now.Add(-2*time.Hour), now, 30*time.Minute, "5 agent"); !stalled {
		t.Fatal("silent pod not stalled")
	}
	last[AgentContainer] = &fresh
	if _, _, stalled := stallOf(p, last, now.Add(-2*time.Hour), now, 30*time.Minute, "5 agent"); stalled {
		t.Fatal("stalled after a new agent line")
	}
}

func TestCutStamp(t *testing.T) {
	ts, rest, ok := cutStamp("2026-11-12T18:51:57.123456789Z step started 18 push\n")
	if !ok || !ts.Equal(time.Date(2026, 11, 12, 18, 51, 57, 123456789, time.UTC)) || rest != "step started 18 push\n" {
		t.Errorf("cutStamp = %v, %q, %v", ts, rest, ok)
	}
	if _, rest, ok := cutStamp("no stamp here\n"); ok || rest != "no stamp here\n" {
		t.Errorf("cutStamp without a stamp = %q, %v", rest, ok)
	}
}

func TestPodLogsFakeBody(t *testing.T) {
	// The fake clientset answers every GetLogs with "fake logs", no timestamp.
	logs := podLogs{client: fake.NewClientset()}
	if _, ok, err := logs.LastLine(context.Background(), "ns", "pod", RunnerContainer); err != nil || ok {
		t.Errorf("LastLine = %v, %v; want no line", ok, err)
	}
	if got, err := logs.Tail(context.Background(), "ns", "pod", AgentContainer, 40, 4); err != nil || got != "logs" {
		t.Errorf("Tail = %q, %v", got, err)
	}
}

func TestPodLogsAPI(t *testing.T) {
	var query url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		if query.Get("tailLines") != "1" {
			fmt.Fprint(w, "2026-11-12T18:51:54.5Z step started 18 push\n")
		}
		fmt.Fprint(w, "2026-11-12T18:51:57.25Z grüße\n")
	}))
	defer srv.Close()
	logs := podLogs{client: kubernetes.NewForConfigOrDie(&rest.Config{Host: srv.URL})}
	ctx := context.Background()

	ts, ok, err := logs.LastLine(ctx, "ns", "pod", RunnerContainer)
	if err != nil || !ok || !ts.Equal(time.Date(2026, 11, 12, 18, 51, 57, 250000000, time.UTC)) {
		t.Errorf("LastLine = %v, %v, %v", ts, ok, err)
	}
	if query.Get("container") != RunnerContainer || query.Get("timestamps") != "true" {
		t.Errorf("query = %v", query)
	}

	if got, err := logs.Tail(ctx, "ns", "pod", AgentContainer, 40, 4096); err != nil || got != "step started 18 push\ngrüße\n" {
		t.Errorf("Tail = %q, %v", got, err)
	}
	if query.Get("tailLines") != "40" || query.Get("container") != AgentContainer {
		t.Errorf("query = %v", query)
	}
	// The last 5 bytes split the ü of "üße\n"; the half rune is dropped.
	if got, _ := logs.Tail(ctx, "ns", "pod", AgentContainer, 40, 5); got != "ße\n" {
		t.Errorf("Tail cut = %q", got)
	}
}
