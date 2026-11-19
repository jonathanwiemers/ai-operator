package controller

import (
	"slices"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// taskStalled is 1 while a task's run is stalled (condition Stalled=True).
// The series is deleted when the stall clears or the task is gone, so
// cardinality stays at the stalled tasks.
var taskStalled = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: "ai_operator_task_stalled",
	Help: "1 while the task's run is stalled (condition Stalled=True)",
}, []string{"namespace", "task", "reason"})

// gitlabRequests counts the HTTP requests sent to GitLab, retries included,
// by kind (rest, graphql), owner (the AgentProject or AgentProjectSet name)
// and op (the client method, e.g. ListIssues; "unknown" when the client does
// not say).
var gitlabRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "ai_operator_gitlab_requests_total",
	Help: "HTTP requests sent to GitLab, retries included, by kind (rest, graphql), owner and op (client method)",
}, []string{"kind", "owner", "op"})

// projectSyncs counts AgentProject reconciles by what they read from
// GitLab: full (every issue and MR), light (milestones only) or skipped
// (a webhook or the project set covered it, nothing read).
var projectSyncs = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "ai_operator_project_syncs_total",
	Help: "AgentProject reconciles by kind: full, light (milestones only) or skipped (covered by webhooks or the set)",
}, []string{"namespace", "project", "kind"})

func init() {
	metrics.Registry.MustRegister(taskStalled, gitlabRequests, projectSyncs)
}

// countGitLabRequests adds what a client sent to the counter. Call it once
// per client, after its last use. Values that are 0 create no series.
func countGitLabRequests(owner string, gl any) {
	c, ok := gl.(interface{ Stats() gitlab.Stats })
	if !ok {
		return
	}
	st := c.Stats()
	byKind := map[string]int64{"rest": st.Requests - st.GraphQL, "graphql": st.GraphQL}
	for k, n := range st.Ops {
		if n > 0 {
			gitlabRequests.WithLabelValues(k.Kind, owner, k.Op).Add(float64(n))
			byKind[k.Kind] -= n
		}
	}
	for kind, n := range byKind {
		if n > 0 {
			gitlabRequests.WithLabelValues(kind, owner, "unknown").Add(float64(n))
		}
	}
}

// stallMetricReasons are the only Stalled=True reasons exported as a series.
var stallMetricReasons = []string{
	aiv1.StallReasonSilent,
	aiv1.StallReasonPodPending,
	aiv1.StallReasonCrashLoop,
}

// setTaskStalled replaces any series for the task with reason=1.
func setTaskStalled(namespace, task, reason string) {
	clearTaskStalled(namespace, task)
	taskStalled.WithLabelValues(namespace, task, reason).Set(1)
}

// clearTaskStalled removes every stalled series for the task.
func clearTaskStalled(namespace, task string) {
	taskStalled.DeletePartialMatch(prometheus.Labels{"namespace": namespace, "task": task})
}

// syncTaskStalledMetric drives the gauge from the persisted Stalled
// condition after a successful status patch. Unknown reasons are cleared
// so a status write cannot grow cardinality.
func syncTaskStalledMetric(task *aiv1.AgentTask) {
	if c := meta.FindStatusCondition(task.Status.Conditions, aiv1.ConditionStalled); c != nil && c.Status == metav1.ConditionTrue &&
		slices.Contains(stallMetricReasons, c.Reason) {
		setTaskStalled(task.Namespace, task.Name, c.Reason)
		return
	}
	clearTaskStalled(task.Namespace, task.Name)
}
