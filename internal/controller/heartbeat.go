package controller

import (
	"context"
	"hash/fnv"
	"net/http"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// heartbeatTriggers are the test deliveries a heartbeat asks GitLab for, in
// the order they are tried: a push test needs a commit, an issues test an
// issue, a milestone test a milestone.
var heartbeatTriggers = []string{"push_events", "issues_events", "milestone_events"}

// heartbeatDue reports whether the project's hook should get a test delivery:
// its last delivery, the last heartbeat and the operator's start are all
// older than half the silence threshold, plus a jitter by project of up to
// an eighth, so the projects do not all ask at once.
func (r *AgentProjectReconciler) heartbeatDue(project *aiv1.AgentProject, spec aiv1.AgentProjectSpec, live, now time.Time) bool {
	st := project.Status.Webhook
	if !spec.Webhook.Enabled || spec.Webhook.SecretRef == nil || r.Deliveries == nil || st == nil || st.HookID == 0 {
		return false
	}
	threshold := spec.Webhook.SilenceThreshold.Duration
	since := r.Deliveries.Started()
	if live.After(since) {
		since = live
	}
	if st.HeartbeatTime != nil && st.HeartbeatTime.After(since) {
		since = st.HeartbeatTime.Time
	}
	return now.Sub(since) >= threshold/2+jitter(project.Namespace+"/"+project.Name, threshold/8)
}

// jitter spreads key over [0, span).
func jitter(key string, span time.Duration) time.Duration {
	if span <= 0 {
		return 0
	}
	h := fnv.New64a()
	h.Write([]byte(key))                           // #nosec G104 -- a hash.Hash's Write never returns an error (package hash doc)
	return time.Duration(h.Sum64() % uint64(span)) // #nosec G115 -- span > 0 is checked above, so the remainder is below span and fits int64
}

// heartbeat asks GitLab for a test delivery to the project's hook, which
// keeps an idle project receiving. It tries the stored trigger first, then
// the others on a 4xx (a repository without commits cannot test pushes),
// and records the time even when all fail, so it waits for the next turn.
func (r *AgentProjectReconciler) heartbeat(ctx context.Context, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) {
	st := project.Status.Webhook
	gl, err := r.gitlabClient(ctx, project, spec)
	if err != nil {
		log.FromContext(ctx).Info("Webhook heartbeat skipped", "error", err.Error())
		return
	}
	defer countGitLabRequests(project.Name, gl)
	n := types.NamespacedName{Namespace: project.Namespace, Name: project.Name}
	triggers := heartbeatTriggers
	if i := slices.Index(triggers, st.HeartbeatTrigger); i > 0 {
		triggers = append([]string{st.HeartbeatTrigger}, slices.Delete(slices.Clone(triggers), i, i+1)...)
	}
	st.HeartbeatTime = &metav1.Time{Time: r.now()}
	for _, trigger := range triggers {
		r.Deliveries.ExpectHeartbeat(n)
		err = gl.TestProjectHook(ctx, spec.GitLab.Project, st.HookID, trigger)
		if err == nil {
			st.HeartbeatTrigger = trigger
			return
		}
		if c := gitlab.StatusCode(err); c < 400 || c >= 500 || c == http.StatusTooManyRequests || c == http.StatusNotFound {
			break // GitLab, the limit or a deleted hook: the next turn or the hook recheck
		}
	}
	log.FromContext(ctx).Info("Webhook heartbeat failed, the project may turn Silent", "hook", st.HookID, "error", err.Error())
}
