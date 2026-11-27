package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// setProjectLimit sets the project's shared GitLab limit.
func (e *limitEnv) setProjectLimit(until time.Time) {
	e.t.Helper()
	var project aiv1.AgentProject
	if err := e.k8s.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: "shop"}, &project); err != nil {
		e.t.Fatal(err)
	}
	project.Status.GitLab = &aiv1.GitLabProjectStatus{RateLimit: &aiv1.GitLabRateLimitStatus{LimitedUntil: &metav1.Time{Time: until}}}
	if err := e.k8s.Status().Update(context.Background(), &project); err != nil {
		e.t.Fatal(err)
	}
}

// setLimitedAt marks the task as deferred on GitLab at at, earlier in its chain.
func (e *limitEnv) setLimitedAt(at time.Time) {
	e.t.Helper()
	task := e.task()
	orig := task.DeepCopy()
	task.Status.Run.GitLabLimitedAt = &metav1.Time{Time: at}
	if err := e.k8s.Status().Patch(context.Background(), &task, client.MergeFrom(orig)); err != nil {
		e.t.Fatal(err)
	}
}

// limitNotes are the issue's late ⏳ notes.
func (e *limitEnv) limitNotes() []string {
	var out []string
	for _, n := range e.issueNotes() {
		if strings.Contains(n, "GitLab rate-limited the run") {
			out = append(out, n)
		}
	}
	return out
}

func TestRunInsideTheProjectLimitDefersWithoutGitLab(t *testing.T) {
	e := newLimitEnv(t, &scriptedEngine{})
	until := e.now.Add(6 * time.Minute)
	e.setProjectLimit(until)
	e.r.NewGitLab = func(string, string) gitlab.API {
		t.Fatal("no GitLab client may be made inside the project's limit")
		return nil
	}
	if e.step() {
		t.Fatal("a deferred run must not count as a run")
	}
	task := e.task()
	run := task.Status.Run
	if run.DeferredUntil == nil || !run.DeferredUntil.Time.Equal(until) || run.BlockReason != aiv1.BlockGitLab ||
		run.LastResult != aiv1.RunResultDeferred || run.Deferrals != 0 || !task.RunPending() {
		t.Fatalf("expected a deferral to the project's limit that does not count: %+v", run)
	}
	if run.GitLabLimitedAt == nil || !run.GitLabLimitedAt.Time.Equal(e.now) {
		t.Fatalf("gitLabLimitedAt %v, want %v", run.GitLabLimitedAt, e.now)
	}
}

func TestGitLabLimitNoteIsPostedOncePerChain(t *testing.T) {
	e := newLimitEnv(t, &scriptedEngine{})
	limited := e.now.Add(-6 * time.Minute)
	e.setLimitedAt(limited)
	// A run that defers again keeps gitLabLimitedAt and posts nothing.
	e.setProjectLimit(e.now.Add(time.Minute))
	if e.step() {
		t.Fatal("inside the limit the run defers")
	}
	if at := e.task().Status.Run.GitLabLimitedAt; at == nil || !at.Time.Equal(limited) {
		t.Fatalf("the chain's start must be kept: %v", at)
	}
	if notes := e.limitNotes(); len(notes) != 0 {
		t.Fatalf("nothing is posted while the limit holds: %q", notes)
	}

	e.now = e.now.Add(2 * time.Minute)
	if !e.step() {
		t.Fatal("after the limit the run starts")
	}
	notes := e.limitNotes()
	want := "⏳ GitLab rate-limited the run at 2026-10-16 11:54 UTC; it waited until 2026-10-16 12:02 UTC and runs again."
	if len(notes) != 1 || notes[0] != want {
		t.Fatalf("notes %q, want one %q", notes, want)
	}
	if strings.Contains(notes[0], "@") {
		t.Fatalf("the note pings no one: %q", notes[0])
	}
	if at := e.task().Status.Run.GitLabLimitedAt; at != nil {
		t.Fatalf("gitLabLimitedAt must be cleared after the note: %v", at)
	}

	e.requestRun(nil)
	e.step()
	if notes := e.limitNotes(); len(notes) != 1 {
		t.Fatalf("a second run posts no note again: %q", notes)
	}
}

// readingGL is a fake that knows a rate limit reading.
type readingGL struct {
	*gitlabfake.Fake
	reading gitlab.RateLimitReading
}

func (g *readingGL) RateLimit() (gitlab.RateLimitReading, bool) { return g.reading, true }

func TestRunStatusCarriesTheGitLabReading(t *testing.T) {
	e := newLimitEnv(t, &scriptedEngine{})
	reset := e.now.Add(40 * time.Second)
	gl := &readingGL{Fake: e.gl, reading: gitlab.RateLimitReading{Limit: 100, Remaining: 37, Observed: 63, ResetAt: reset, ReadAt: e.now}}
	e.r.NewGitLab = func(string, string) gitlab.API { return gl }
	if !e.step() {
		t.Fatal("the run happens")
	}
	got := e.task().Status.Run.GitLabRateLimit
	if got == nil || got.Remaining != 37 || got.Observed != 63 || got.Limit != 100 ||
		got.ResetAt == nil || !got.ResetAt.Time.Equal(reset) || got.ReadAt == nil || !got.ReadAt.Time.Equal(e.now) {
		t.Fatalf("gitLabRateLimit %+v, want the client's reading", got)
	}

	// An older reading does not replace a newer one.
	gl.reading.Remaining, gl.reading.ReadAt = 90, e.now.Add(-time.Minute)
	e.r.recordCall(context.Background(), aiv1.AgentCall{Kind: aiv1.CallKindImplement})
	if got := e.task().Status.Run.GitLabRateLimit; got == nil || got.Remaining != 37 {
		t.Fatalf("an older reading replaced the newer: %+v", got)
	}
}
