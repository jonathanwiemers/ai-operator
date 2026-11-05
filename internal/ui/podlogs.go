package ui

import (
	"context"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// LogSource streams a container's log, following it until the container
// stops or ctx ends, and tells whether the pod still runs.
type LogSource interface {
	// Stream follows the log from since (nil: from the container's start).
	Stream(ctx context.Context, namespace, pod, container string, since *time.Time) (io.ReadCloser, error)
	Running(ctx context.Context, namespace, pod string) bool
}

// PodLogs reads container logs through the Kubernetes API.
type PodLogs struct{ Client kubernetes.Interface }

// Stream implements LogSource.
func (p PodLogs) Stream(ctx context.Context, namespace, pod, container string, since *time.Time) (io.ReadCloser, error) {
	opts := &corev1.PodLogOptions{Container: container, Follow: true}
	if since != nil {
		opts.SinceTime = &metav1.Time{Time: *since}
	}
	return p.Client.CoreV1().Pods(namespace).GetLogs(pod, opts).Stream(ctx)
}

// Running implements LogSource.
func (p PodLogs) Running(ctx context.Context, namespace, pod string) bool {
	po, err := p.Client.CoreV1().Pods(namespace).Get(ctx, pod, metav1.GetOptions{})
	return err == nil && po.DeletionTimestamp == nil && po.Status.Phase == corev1.PodRunning
}
