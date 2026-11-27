package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

func TestGitLabTransient(t *testing.T) {
	limited := &gitlab.Error{StatusCode: 429, Method: "GET", Path: "/projects/x/issues/22/notes", Body: "Rate limit exceeded"}
	for _, c := range []struct {
		name string
		err  error
		want bool
	}{
		{"429", fmt.Errorf("listing issue notes: %w", limited), true},
		{"502", &gitlab.Error{StatusCode: 502}, true},
		{"paced", &gitlab.PacedError{Method: "GET", Path: "/x", Wait: time.Minute}, true},
		{"git push 429", errors.New("git push origin agent/1: exit status 128: fatal: unable to access 'https://gitlab.com/x.git/': The requested URL returned error: 429"), true},
		{"git fetch 503", errors.New("git fetch: exit status 128: The requested URL returned error: 503"), true},
		{"404", &gitlab.Error{StatusCode: 404}, false},
		{"git 403", errors.New("git push: The requested URL returned error: 403"), false},
		{"engine timeout", fmt.Errorf("agent call: %w", context.DeadlineExceeded), false},
		{"timeout without a status", timeoutErr("/projects/x/issues"), false},
		{"EOF", io.ErrUnexpectedEOF, false},
		{"429 joined with a decode error", errors.Join(limited, errors.New("decoding")), false},
		{"nil", nil, false},
	} {
		if got := gitLabTransient(c.err); got != c.want {
			t.Errorf("%s: gitLabTransient = %v, want %v", c.name, got, c.want)
		}
	}
}

// notesFailingGL fails reading the issue's notes with err.
type notesFailingGL struct {
	*gitlabfake.Fake
	err error
}

func (g *notesFailingGL) ListIssueNotes(context.Context, string, int64) ([]gitlab.Note, error) {
	return nil, g.err
}

// gitLabFailEnv is a limitEnv whose run meets err from GitLab when it reads
// the comments, with a checkpoint from an earlier run and deferrals done.
func gitLabFailEnv(t *testing.T, err error, deferrals int32) *limitEnv {
	t.Helper()
	e := newLimitEnv(t, &scriptedEngine{})
	e.r = newTestRunner(t, e.tmp, e.k8s, &notesFailingGL{Fake: e.gl, err: err}, &scriptedEngine{}, "shop-42-add-paypal-button")
	e.r.Now = func() time.Time { return e.now }
	task := e.task()
	orig := task.DeepCopy()
	task.Status.Run.Checkpoint = &aiv1.CheckpointStatus{SHA: "c0ffee", Branch: cpBranch, Reason: "pod-lost", Run: 1, Step: 2}
	task.Status.Run.Deferrals = deferrals
	if err := e.k8s.Status().Patch(context.Background(), &task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	return e
}

// checkGitLabDeferred checks a run deferred on GitLab until a time in
// [from, to], that left GitLab alone.
func (e *limitEnv) checkGitLabDeferred(from, to time.Time) aiv1.RunStatus {
	e.t.Helper()
	if e.step() {
		e.t.Fatal("a deferred run must not count as a run")
	}
	task := e.task()
	run := task.Status.Run
	if run.LastResult != aiv1.RunResultDeferred || run.BlockReason != aiv1.BlockGitLab || run.Deferrals != 1 || run.FailureKind != "" {
		e.t.Fatalf("expected a GitLab deferral: %+v", run)
	}
	if run.DeferredUntil == nil || run.DeferredUntil.Time.Before(from) || run.DeferredUntil.Time.After(to) {
		e.t.Fatalf("deferredUntil %v, want in [%v, %v]", run.DeferredUntil, from, to)
	}
	if !task.RunPending() || run.ObservedRunRequest != 0 || run.ActiveRunRequest != 0 || run.RunCount != 0 {
		e.t.Fatalf("the request must stay pending and nothing in progress: %+v", run)
	}
	if run.Checkpoint == nil || run.Checkpoint.SHA != "c0ffee" {
		e.t.Fatalf("the checkpoint must be kept: %+v", run.Checkpoint)
	}
	if run.GitLabLimitedAt == nil || !run.GitLabLimitedAt.Time.Equal(e.now) {
		e.t.Fatalf("gitLabLimitedAt %v, want %v", run.GitLabLimitedAt, e.now)
	}
	if notes := e.issueNotes(); len(notes) != 0 {
		e.t.Fatalf("nothing is posted while the limit holds: %q", notes)
	}
	if all := e.allNotes(); strings.Contains(all, "❌") || strings.Contains(all, "failed") {
		e.t.Fatalf("no failure headline:\n%s", all)
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::working"}) {
		e.t.Fatalf("the operator blocks the issue, the runner leaves the labels: has %v", labels)
	}
	return run
}

func TestGitLabRateLimitDefersImplementRun(t *testing.T) {
	reset := time.Date(2026, 10, 16, 12, 6, 0, 0, time.UTC)
	e := gitLabFailEnv(t, &gitlab.Error{StatusCode: 429, Method: "GET", Path: "/projects/group%2Fapp/issues/42/notes", Body: "Rate limit exceeded", Reset: reset}, 0)
	run := e.checkGitLabDeferred(reset.Add(10*time.Second), reset.Add(40*time.Second))
	if !strings.Contains(run.Summary, "429") {
		t.Fatalf("the summary says why: %q", run.Summary)
	}
}

func TestGitLabServerErrorDefersByBackoff(t *testing.T) {
	e := gitLabFailEnv(t, &gitlab.Error{StatusCode: 502, Method: "GET", Path: "/projects/group%2Fapp/issues/42/notes", Body: "Bad Gateway"}, 0)
	until := e.now.Add(aiv1.AgentProjectSpec{}.WithDefaults().Lifecycle.Backoff(1))
	e.checkGitLabDeferred(until, until)
}

func TestGitLabDeferralsCapFailsAsInfra(t *testing.T) {
	e := gitLabFailEnv(t, &gitlab.Error{StatusCode: 429, Method: "GET", Path: "/projects/group%2Fapp/issues/42/notes", Body: "Rate limit exceeded"}, MaxDeferrals)
	if !e.step() {
		t.Fatal("the run at the cap ends")
	}
	run := e.task().Status.Run
	if run.LastResult != aiv1.RunResultFailed || run.BlockReason != aiv1.BlockInfra || run.FailureKind != aiv1.FailureGitLab ||
		run.DeferredUntil != nil || run.Deferrals != 0 || !strings.Contains(run.Summary, "giving up after") {
		t.Fatalf("expected a failed infra run with failureKind gitlab: %+v", run)
	}
}

// failingEngine fails every call with err.
type failingEngine struct{ err error }

func (f failingEngine) Run(context.Context, Invocation) (*Result, error) { return nil, f.err }

func TestEngineTimeoutIsNoGitLabDeferral(t *testing.T) {
	e := newLimitEnv(t, failingEngine{err: fmt.Errorf("agent call: %w", context.DeadlineExceeded)})
	if !e.step() {
		t.Fatal("the run ends")
	}
	run := e.task().Status.Run
	if run.LastResult != aiv1.RunResultFailed || run.BlockReason != "" || run.FailureKind != "" {
		t.Fatalf("an engine timeout is an ordinary failure: %+v", run)
	}
}

func TestSpecRunOnGitLabRateLimitDefers(t *testing.T) {
	e := newSpecFailEnv(t, nil)
	e.gl.CreateIssueError = &url.Error{Op: "Post", URL: "https://gitlab.example/api/v4/projects/group%2Fapp/issues",
		Err: &gitlab.Error{StatusCode: 429, Method: "POST", Path: "/projects/group%2Fapp/issues", Body: "Rate limit exceeded"}}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, &planEngine{}, e.task.Name)
	r.Now = func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) }
	if ran, err := r.Step(context.Background()); err != nil || ran {
		t.Fatalf("ran=%v err=%v, want a deferral", ran, err)
	}
	var got aiv1.AgentTask
	if err := e.k8s.Get(context.Background(), client.ObjectKeyFromObject(e.task), &got); err != nil {
		t.Fatal(err)
	}
	run := got.Status.Run
	if run.LastResult != aiv1.RunResultDeferred || run.BlockReason != aiv1.BlockGitLab || run.DeferredUntil == nil {
		t.Fatalf("a Spec run on a GitLab limit defers, not BlockInfra: %+v", run)
	}
	if notes := e.gl.IssueNotes[trackingIID]; len(notes) != 0 {
		t.Fatalf("nothing is posted while the limit holds: %+v", notes)
	}
}
