package ui

import (
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// warmDebounce is how long a warm request makes the next ones no-ops: card
// re-renders and several open tabs must not patch the task on every view.
const warmDebounce = time.Minute

// warmTask wakes a pilot without a turn: it sets the Coordinate task's
// spec.warmRequestedAt, which keeps its pod up for lifecycle.chatIdleTimeout.
// Needs what posting to the task's chat needs; 202 also when the last request
// is under a minute old (no patch then).
func (s *Server) warmTask(w http.ResponseWriter, r *http.Request) {
	ns, name := r.PathValue("namespace"), r.PathValue("name")
	p := principalOf(r)
	switch {
	case !s.cfg.ChatEnabled() || s.writer == nil:
		notFound(w)
		return
	case !s.authz.CanChat(p, ns, name):
		if !s.authz.CanView(p, ns, name) {
			notFound(w)
			return
		}
		writeError(w, http.StatusForbidden, "no access to chat")
		return
	}
	_, task, ok := s.loadTask(w, r)
	if !ok {
		return
	}
	if task.Spec.Type != aiv1.TaskTypeCoordinate {
		notFound(w)
		return
	}
	now := time.Now()
	if last := task.Spec.WarmRequestedAt; last == nil || now.Sub(last.Time) >= warmDebounce {
		base := task.DeepCopy()
		task.Spec.WarmRequestedAt = &metav1.Time{Time: now}
		if err := s.writer.Patch(r.Context(), task, client.MergeFrom(base)); client.IgnoreNotFound(err) != nil {
			s.internalError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusAccepted)
}
