package ui

import (
	"context"
	"net/http"
	"slices"
	"strconv"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// maxLogTail is the most lines rawLogs returns; follow shows what comes after.
const maxLogTail = 500

// rawLogs returns the raw log of the runner or agent container of the pod
// named after the task (not status.podName, see transcript): the last tail
// lines (1..500, default 500), with follow=1 also what comes after, until
// the container stops, the client leaves or the session expires. Only the
// task's owner subjects with the steer grant may read it.
func (s *Server) rawLogs(w http.ResponseWriter, r *http.Request) {
	if !s.mayPost(w, r) {
		return
	}
	_, tasks, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	i := slices.IndexFunc(tasks, func(t aiv1.AgentTask) bool { return t.Name == r.PathValue("task") })
	q, p := r.URL.Query(), principalOf(r)
	tail, err := int64(maxLogTail), error(nil)
	if q.Has("tail") {
		tail, err = strconv.ParseInt(q.Get("tail"), 10, 64)
	}
	switch {
	case i < 0:
		notFound(w)
	case !slices.Contains(tasks[i].Spec.OwnerSubjects, p.Username):
		writeError(w, http.StatusForbidden, "only the task's owners may read its logs")
	case !slices.Contains(transcriptContainers, q.Get("container")) || err != nil:
		writeError(w, http.StatusBadRequest, "container must be runner or agent, tail a number")
	case s.logs == nil:
		writeError(w, http.StatusNotImplemented, "logs are not available")
	default:
		s.writeLog(w, r, &tasks[i], q.Get("container"), min(max(tail, 1), maxLogTail), q.Get("follow") == "1")
	}
}

// writeLog copies the log to w, flushing each chunk when it follows.
func (s *Server) writeLog(w http.ResponseWriter, r *http.Request, t *aiv1.AgentTask, container string, tail int64, follow bool) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if exp := sessionExpiry(r); follow && !exp.IsZero() {
		ctx, cancel = context.WithDeadline(ctx, exp)
		defer cancel()
	}
	if follow && s.sessions != nil {
		release, ok := s.acquireStream(principalOf(r).Username)
		if !ok {
			writeError(w, http.StatusTooManyRequests, "too many open event streams")
			return
		}
		defer release()
	}
	rc, err := s.logs.Tail(ctx, t.Namespace, t.Name, container, tail, follow)
	if apierrors.IsNotFound(err) {
		writeError(w, http.StatusNotFound, "the task has no pod")
		return
	} else if err != nil {
		s.log.V(1).Info("cannot read a container log", "task", t.Name, "container", container, "error", err.Error())
		writeError(w, http.StatusBadGateway, "the log cannot be read")
		return
	}
	defer rc.Close()
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	// Following, the headers go out at once: the next line may take long.
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if follow && flusher != nil {
		flusher.Flush()
	}
	buf := make([]byte, 32<<10)
	for {
		n, err := rc.Read(buf)
		if _, werr := w.Write(buf[:n]); werr != nil || err != nil {
			return
		}
		if follow && flusher != nil {
			flusher.Flush()
		}
	}
}
