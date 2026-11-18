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
// by kind (rest, graphql) and owner (the AgentProject or AgentProjectSet name).
var gitlabRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "ai_operator_gitlab_requests_total",
	Help: "HTTP requests sent to GitLab, retries included, by kind (rest, graphql) and owner",
}, []string{"kind", "owner"})

func init() {
	metrics.Registry.MustRegister(taskStalled, gitlabRequests)
}

// countGitLabRequests adds what a client sent to the counter. Call it once
// per client, after its last use. Values that are 0 create no series.
func countGitLabRequests(owner string, gl any) {
	c, ok := gl.(interface{ Stats() gitlab.Stats })
	if !ok {
		return
	}
	st := c.Stats()
	if rest := st.Requests - st.GraphQL; rest > 0 {
		gitlabRequests.WithLabelValues("rest", owner).Add(float64(rest))
	}
	if st.GraphQL > 0 {
		gitlabRequests.WithLabelValues("graphql", owner).Add(float64(st.GraphQL))
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
