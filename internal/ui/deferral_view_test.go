package ui

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// A task deferred by GitLab shows its block reason and retry time on its
// milestone row and queue item; the task view carries them in its run status.
func TestDeferralViews(t *testing.T) {
	now := time.Now()
	until := metav1.NewTime(now.Add(6 * time.Minute))
	p := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "shop"}}
	st := waitingSpec()
	st.Spec.QueuedBehind = nil
	st.Spec.RunRequest = 1
	st.Status.Plan = []aiv1.PlannedIssue{{IID: 5, Title: "Five"}, {IID: 6, Title: "Six"}}
	st.Status.Autopilot.Items = []aiv1.AutopilotItem{
		{IID: 5, State: aiv1.AutopilotItemActive}, {IID: 6, State: aiv1.AutopilotItemActive}}
	impl := func(name string, iid, req, observed int64) aiv1.AgentTask {
		return aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: name},
			Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeImplement, Milestone: st.Spec.Milestone, Issue: &aiv1.IssueRef{IID: iid}, RunRequest: req},
			Status: aiv1.AgentTaskStatus{Run: aiv1.RunStatus{ObservedRunRequest: observed, BlockReason: aiv1.BlockGitLab,
				DeferredUntil: &until, Deferrals: 2}}}
	}
	deferred, idle := impl("shop-5", 5, 1, 0), impl("shop-6", 6, 1, 1)
	v := milestoneView(p, []aiv1.AgentTask{st, deferred, idle}, &st, nil, now)
	check := func(what string, d Deferral, want bool) {
		t.Helper()
		if got := d.BlockReason == aiv1.BlockGitLab && d.DeferredUntil != nil && d.Deferrals == 2; got != want {
			t.Errorf("%s: %+v, want deferral %v", what, d, want)
		}
	}
	for _, r := range v.Issues {
		check("row", r.Deferral, r.IID == 5)
	}
	for _, q := range v.Autopilot.Items {
		check("queue item", q.Deferral, q.IID == 5)
	}
	for _, tv := range v.Tasks {
		if tv.Name == "shop-5" && (tv.Status.Run.BlockReason != aiv1.BlockGitLab || tv.Status.Run.DeferredUntil == nil || tv.Status.Run.Deferrals != 2) {
			t.Errorf("task run = %+v", tv.Status.Run)
		}
	}
}

// A project near its limit carries the rate limit and the UI server's reserve.
func TestSummarizeGitLabRateLimit(t *testing.T) {
	until := metav1.NewTime(time.Now().Add(time.Minute))
	p := &aiv1.AgentProject{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "shop"}}
	if s := summarize(p, nil, time.Now()); s.GitLabStatus != nil {
		t.Fatalf("gitLab = %+v", s.GitLabStatus)
	}
	p.Status.GitLab = &aiv1.GitLabProjectStatus{RateLimit: &aiv1.GitLabRateLimitStatus{
		GitLabRateLimitReading: aiv1.GitLabRateLimitReading{Remaining: 15, Limit: 100}, LimitedUntil: &until}}
	s := summarize(p, nil, time.Now())
	if rl := s.GitLabStatus.RateLimit; rl.Remaining != 15 || rl.LimitedUntil == nil || s.GitLabReserve != 20 {
		t.Fatalf("summary = %+v reserve %d", s.GitLabStatus, s.GitLabReserve)
	}
}
