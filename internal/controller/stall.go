package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// containerLogs reads the end of a container's log for the stall check and
// the pilot's evidence.
type containerLogs interface {
	// LastLine is the time of the newest line; ok=false: no line yet.
	LastLine(ctx context.Context, ns, pod, container string) (t time.Time, ok bool, err error)
	// Tail is the last lines without timestamps, at most maxBytes from the end.
	Tail(ctx context.Context, ns, pod, container string, lines int64, maxBytes int) (string, error)
}

// podLogs implements containerLogs with the Kubernetes API.
type podLogs struct{ client kubernetes.Interface }

func (p podLogs) read(ctx context.Context, ns, pod, container string, lines int64) (string, error) {
	opts := &corev1.PodLogOptions{Container: container, TailLines: &lines, Timestamps: true}
	b, err := p.client.CoreV1().Pods(ns).GetLogs(pod, opts).DoRaw(ctx)
	return string(b), err
}

func (p podLogs) LastLine(ctx context.Context, ns, pod, container string) (time.Time, bool, error) {
	body, err := p.read(ctx, ns, pod, container, 1)
	t, _, ok := cutStamp(strings.TrimSpace(body))
	return t, ok && err == nil, err
}

func (p podLogs) Tail(ctx context.Context, ns, pod, container string, lines int64, maxBytes int) (string, error) {
	body, err := p.read(ctx, ns, pod, container, lines)
	var b strings.Builder
	for line := range strings.Lines(body) {
		_, rest, _ := cutStamp(line)
		b.WriteString(rest)
	}
	s := b.String()
	return strings.ToValidUTF8(s[max(0, len(s)-maxBytes):], ""), err
}

// cutStamp splits off the RFC3339Nano timestamp Kubernetes puts before a
// log line; ok=false leaves the line as it is.
func cutStamp(line string) (t time.Time, rest string, ok bool) {
	stamp, rest, _ := strings.Cut(line, " ")
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return t, line, false
	}
	return t, rest, true
}

// stuckWaits are container waiting reasons that do not heal on their own.
var stuckWaits = []string{"CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull"}

// stallOf decides whether a run's pod is stalled. since is the run start;
// the pod's creation counts when it is later. last holds the newest log line
// of the runner and agent containers (nil: none yet, counts from since).
func stallOf(pod *corev1.Pod, last map[string]*time.Time, since, now time.Time, after time.Duration, step string) (reason, message string, stalled bool) {
	if c := pod.CreationTimestamp.Time; c.After(since) {
		since = c
	}
	for _, cs := range slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses) {
		if w := cs.State.Waiting; w != nil && slices.Contains(stuckWaits, w.Reason) && now.Sub(since) > after {
			return aiv1.StallReasonCrashLoop, fmt.Sprintf("container %s in %s, %s into the run", cs.Name, w.Reason, minutes(now.Sub(since))), true
		}
	}
	if pod.Status.Phase == corev1.PodPending && now.Sub(since) > after {
		return aiv1.StallReasonPodPending, "pod pending for " + minutes(now.Sub(since)), true
	}
	newest := since
	for _, c := range []string{RunnerContainer, AgentContainer} {
		if l := last[c]; l != nil && l.After(newest) {
			newest = *l
		}
	}
	if pod.Status.Phase != corev1.PodRunning || now.Sub(newest) <= after {
		return "", "", false
	}
	return aiv1.StallReasonSilent, fmt.Sprintf("no log line for %s in step %s", minutes(now.Sub(newest)), step), true
}

// minutes formats d rounded to the minute without a trailing 0s: "42m", "1h5m".
func minutes(d time.Duration) string {
	return strings.TrimSuffix(d.Round(time.Minute).String(), "0s")
}
