package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

func reading(observed int32, at time.Time) *aiv1.GitLabRateLimitReading {
	return &aiv1.GitLabRateLimitReading{Observed: observed, Remaining: 100 - observed, Limit: 100, ReadAt: &metav1.Time{Time: at}}
}

func gitlabDeferredTask(reason aiv1.BlockReason, until time.Time) aiv1.AgentTask {
	var t aiv1.AgentTask
	t.Status.Run.BlockReason = reason
	t.Status.Run.DeferredUntil = &metav1.Time{Time: until}
	return t
}

func TestProjectLimitedUntilFromGitLabDeferredTasks(t *testing.T) {
	now := time.Date(2026, 11, 26, 23, 35, 0, 0, time.UTC)
	tasks := []aiv1.AgentTask{
		gitlabDeferredTask(aiv1.BlockGitLab, now.Add(4*time.Minute)),
		gitlabDeferredTask(aiv1.BlockGitLab, now.Add(6*time.Minute)),
		gitlabDeferredTask(aiv1.BlockQuota, now.Add(time.Hour)), // an engine's limit
		gitlabDeferredTask(aiv1.BlockGitLab, now.Add(-time.Minute)),
	}
	rl := projectGitLabRateLimit(nil, nil, tasks, now)
	if rl == nil || rl.LimitedUntil == nil || !rl.LimitedUntil.Time.Equal(now.Add(6*time.Minute)) {
		t.Fatalf("limitedUntil %+v, want the latest GitLab deferral", rl)
	}
	if got := projectGitLabRateLimit(rl, nil, tasks, now.Add(7*time.Minute)); got != nil {
		t.Fatalf("a passed limit is cleared: %+v", got)
	}
}

func TestProjectRateLimitTakesTheFreshestReadingAndKeepsThePeak(t *testing.T) {
	now := time.Date(2026, 11, 26, 23, 35, 0, 0, time.UTC)
	var task aiv1.AgentTask
	task.Status.Run.GitLabRateLimit = reading(90, now.Add(-10*time.Second))
	rl := projectGitLabRateLimit(nil, reading(40, now.Add(-30*time.Second)), []aiv1.AgentTask{task}, now)
	if rl == nil || rl.Observed != 90 || rl.Remaining != 10 || rl.PeakObserved != 90 || !rl.PeakAt.Time.Equal(now.Add(-10*time.Second)) {
		t.Fatalf("want the task's fresher reading and its peak: %+v", rl)
	}
	// Later and lower: the reading follows, the peak stays within the hour.
	later := now.Add(50 * time.Minute)
	rl = projectGitLabRateLimit(rl, reading(20, later), nil, later)
	if rl.Observed != 20 || rl.PeakObserved != 90 {
		t.Fatalf("want reading 20 and peak 90: %+v", rl)
	}
	// After an hour the peak is the newest reading's.
	last := now.Add(70 * time.Minute)
	rl = projectGitLabRateLimit(rl, reading(30, last), nil, last)
	if rl.PeakObserved != 30 || !rl.PeakAt.Time.Equal(last) {
		t.Fatalf("an hour-old peak is replaced: %+v", rl)
	}
	// Without new readings an expired peak falls back to the last reading,
	// while it is within the hour.
	if got := projectGitLabRateLimit(&aiv1.GitLabRateLimitStatus{GitLabRateLimitReading: *reading(30, last),
		PeakObserved: 90, PeakAt: &metav1.Time{Time: last.Add(-61 * time.Minute)}}, nil, nil, last.Add(time.Minute)); got.PeakObserved != 30 || !got.PeakAt.Time.Equal(last) {
		t.Fatalf("the expired peak is seeded from the last reading: %+v", got)
	}
	// An older reading never replaces a newer one.
	if got := projectGitLabRateLimit(rl, reading(99, now), nil, last); got.Observed != 30 {
		t.Fatalf("an older reading replaced the newer: %+v", got)
	}
}

// readingGL is a fake whose rate limit reading is the real client's of srv.
type readingGL struct {
	*gitlabfake.Fake
	real *gitlab.Client
}

func (g *readingGL) RateLimit() (gitlab.RateLimitReading, bool) { return g.real.RateLimit() }

func TestPollWritesTheRateLimitFromHeadersOnce(t *testing.T) {
	reset := time.Now().Add(40 * time.Second).Truncate(time.Second)
	var remaining atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := remaining.Load()
		w.Header().Set("RateLimit-Limit", "100")
		w.Header().Set("RateLimit-Observed", strconv.FormatInt(100-n, 10))
		w.Header().Set("RateLimit-Remaining", strconv.FormatInt(n, 10))
		w.Header().Set("RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		_, _ = w.Write([]byte(`{"id":1,"username":"operator-ai"}`))
	}))
	defer srv.Close()
	real := gitlab.New(srv.URL, "rate-limit-poll-tok")
	// Two reads in the poll's time: the second one's headers count.
	for _, n := range []int64{70, 63} {
		remaining.Store(n)
		if _, err := real.CurrentUser(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	var statusWrites atomic.Int64
	s := testScheme(t)
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "gitlab", Namespace: ns}, Data: map[string][]byte{"token": []byte("tok")}}
	countWrite := func(obj client.Object) {
		if _, ok := obj.(*aiv1.AgentProject); ok {
			statusWrites.Add(1)
		}
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(testProject(), secret).
		WithStatusSubresource(&aiv1.AgentProject{}, &aiv1.AgentTask{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				countWrite(obj)
				return c.SubResource(sub).Update(ctx, obj, opts...)
			},
			SubResourcePatch: func(ctx context.Context, c client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				countWrite(obj)
				return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
			},
		}).Build()
	gl := &readingGL{Fake: gitlabfake.New(), real: real}
	gl.Bot.Username = "operator-ai"
	e := &projectEnv{t: t, c: c, gl: gl.Fake, ctx: context.Background(),
		r: &AgentProjectReconciler{Client: c, Scheme: s, NewGitLab: func(string, string) gitlab.API { return gl }}}
	e.reconcile()

	rl := e.project().Status.GitLab.RateLimit
	if rl == nil || rl.Observed != 37 || rl.Remaining != 63 || rl.Limit != 100 || rl.ResetAt == nil || !rl.ResetAt.Time.Equal(reset) {
		t.Fatalf("status.gitLab.rateLimit %+v, want the last response's headers", rl)
	}
	if n := statusWrites.Load(); n != 1 {
		t.Fatalf("the project status was written %d times in one poll, want 1", n)
	}
	if got := testutil.ToFloat64(gitlabRateLimitRemaining.WithLabelValues("operator-ai")); got != 63 {
		t.Fatalf("ai_operator_gitlab_ratelimit_remaining{user=operator-ai} = %v, want 63", got)
	}
}

func TestPacedSecondsMetric(t *testing.T) {
	m := gitlabPacedSeconds(gitlab.Background)
	if got, want := testutil.ToFloat64(m), gitlab.PacedSeconds(gitlab.Background); got != want {
		t.Fatalf("paced_seconds_total{priority=background} = %v, want %v", got, want)
	}
	if n := testutil.CollectAndCount(m, "ai_operator_gitlab_paced_seconds_total"); n != 1 {
		t.Fatalf("want one series, got %d", n)
	}
	// A background read at the reserve waits for the reset: the counter rises.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("RateLimit-Remaining", "5")
		w.Header().Set("RateLimit-Reset", strconv.FormatInt(time.Now().Add(2*time.Second).Unix(), 10))
		_, _ = w.Write([]byte(`{"id":1,"username":"operator-ai"}`))
	}))
	defer srv.Close()
	cl := gitlab.New(srv.URL, "paced-metric-tok")
	ctx := gitlab.WithPriority(context.Background(), gitlab.Background)
	before := testutil.ToFloat64(m)
	for range 2 {
		if _, err := cl.CurrentUser(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := testutil.ToFloat64(m); got <= before {
		t.Fatalf("paced_seconds_total{priority=background} stayed at %v after a paced call", got)
	}
}
