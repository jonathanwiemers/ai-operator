package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// trackingIID is the autopilot tracking issue of the spec failure tests.
const trackingIID = 9

type specFailEnv struct {
	tmp  string
	gl   *gitlabfake.Fake
	k8s  client.Client
	task *aiv1.AgentTask
}

// newSpecFailEnv sets up an autopilot milestone with its tracking issue and
// a spec task whose run request is pending.
func newSpecFailEnv(t *testing.T, mutate func(*aiv1.AgentTask)) *specFailEnv {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Cleanup(gitlab.SetRetryBackoff(time.Millisecond))
	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin.git")
	seed := filepath.Join(tmp, "seed")
	gitCmd(t, tmp, "init", "--bare", "--initial-branch=main", origin)
	gitCmd(t, tmp, "clone", origin, seed)
	_ = os.WriteFile(filepath.Join(seed, "README.md"), []byte("app\n"), 0o644)
	gitCmd(t, seed, "add", ".")
	gitCmd(t, seed, "commit", "-m", "init")
	gitCmd(t, seed, "push", "origin", "HEAD:main")

	gl := gitlabfake.New()
	gl.Project.HTTPURLToRepo = origin
	gl.Milestones = []gitlab.Milestone{{ID: 11, IID: 3, Title: "Checkout v2", Description: "/agent autopilot\nBuild it."}}
	gl.AddIssue(trackingIID, "🤖 Autopilot: Checkout v2", "ai-generated")

	scheme := runtime.NewScheme()
	_ = aiv1.AddToScheme(scheme)
	project := &aiv1.AgentProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: "agents"},
		Spec: aiv1.AgentProjectSpec{GitLab: aiv1.GitLabSpec{Project: "group/app"},
			Lifecycle: aiv1.LifecycleSpec{InfraRetryAfter: metav1.Duration{Duration: 30 * time.Minute}}},
	}
	task := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-spec-m3-checkout-v2", Namespace: "agents"},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeSpec, RunRequest: 1, Autopilot: true, Owners: []string{"alice"},
			Milestone: &aiv1.MilestoneRef{ID: 11, IID: 3, Title: "Checkout v2"}},
		Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, TrackingIssueIID: trackingIID}},
	}
	if mutate != nil {
		mutate(task)
	}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, task).
		WithStatusSubresource(&aiv1.AgentTask{}).Build()
	return &specFailEnv{tmp: tmp, gl: gl, k8s: k8s, task: task}
}

// step runs the spec task once, at a fixed time.
func (e *specFailEnv) step(t *testing.T, engine Engine) aiv1.RunStatus {
	t.Helper()
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	r.Now = func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) }
	if ran, err := r.Step(context.Background()); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	var got aiv1.AgentTask
	_ = e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &got)
	return got.Status.Run
}

// trackingNote returns the only comment on the tracking issue.
func (e *specFailEnv) trackingNote(t *testing.T) string {
	t.Helper()
	notes := e.gl.IssueNotes[trackingIID]
	if len(notes) != 1 {
		t.Fatalf("expected one comment on the tracking issue, got %d: %+v", len(notes), notes)
	}
	return notes[0].Body
}

func TestSpecRunFailingOnGitLabIsRetriedAndPings(t *testing.T) {
	e := newSpecFailEnv(t, nil)
	e.gl.CreateIssueError = timeoutErr("/projects/group%2Fapp/issues")
	run := e.step(t, &planEngine{})
	if run.LastResult != aiv1.RunResultFailed || run.BlockReason != aiv1.BlockInfra {
		t.Fatalf("a GitLab timeout is an outside failure: %+v", run)
	}
	body := e.trackingNote(t)
	for _, want := range []string{"🤖 Planning failed", "creating issue", "plans again at 2026-10-26 12:30 UTC (`lifecycle.infraRetryAfter`), once", "cc @alice"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the tracking comment misses %q:\n%s", want, body)
		}
	}
}

func TestSpecRetryThatFailsAgainSaysSo(t *testing.T) {
	e := newSpecFailEnv(t, func(task *aiv1.AgentTask) {
		// The operator's retry: the request it made.
		task.Spec.RunRequest, task.Spec.Source.InfraRetry = 2, 2
		task.Status.Run.ObservedRunRequest = 1
	})
	e.gl.CreateIssueError = timeoutErr("/projects/group%2Fapp/issues")
	if run := e.step(t, &planEngine{}); run.BlockReason != aiv1.BlockInfra {
		t.Fatalf("still an outside failure: %+v", run)
	}
	body := e.trackingNote(t)
	if !strings.Contains(body, "This was the automatic retry") || strings.Contains(body, "plans again at") || !strings.Contains(body, "cc @alice") {
		t.Fatalf("the retry's failure waits for the owners:\n%s", body)
	}
}

func TestSpecRunFailingOnThePlanIsNotRetried(t *testing.T) {
	e := newSpecFailEnv(t, nil)
	run := e.step(t, &badPlanEngine{})
	if run.LastResult != aiv1.RunResultFailed || run.BlockReason != "" {
		t.Fatalf("a plan that does not decode is no outside failure: %+v", run)
	}
	body := e.trackingNote(t)
	if !strings.Contains(body, "decoding plan") || !strings.Contains(body, "To plan again, change the milestone's spec.") ||
		strings.Contains(body, "plans again at") || !strings.Contains(body, "cc @alice") {
		t.Fatalf("the tracking comment offers no retry:\n%s", body)
	}
}

func TestSpecFailureRespectsNotifyEvents(t *testing.T) {
	e := newSpecFailEnv(t, nil)
	var project aiv1.AgentProject
	_ = e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: "shop"}, &project)
	project.Spec.Notify.Events = []aiv1.NotifyEvent{aiv1.NotifyNeedsInput}
	if err := e.k8s.Update(context.Background(), &project); err != nil {
		t.Fatal(err)
	}
	e.step(t, &badPlanEngine{})
	if body := e.trackingNote(t); strings.Contains(body, "cc @") {
		t.Fatalf("without the failed event nobody is mentioned:\n%s", body)
	}
}

func TestSpecFailureWithoutTrackingIssueOnlyLogs(t *testing.T) {
	e := newSpecFailEnv(t, func(task *aiv1.AgentTask) {
		task.Spec.Autopilot, task.Status.Autopilot = false, nil
	})
	e.gl.CreateIssueError = timeoutErr("/projects/group%2Fapp/issues")
	if run := e.step(t, &planEngine{}); run.BlockReason != aiv1.BlockInfra {
		t.Fatalf("the retry applies without autopilot too: %+v", run)
	}
	for iid, notes := range e.gl.IssueNotes {
		if len(notes) != 0 {
			t.Fatalf("no comment without a tracking issue, #%d has %+v", iid, notes)
		}
	}
}

// badPlanEngine answers with a plan that does not decode.
type badPlanEngine struct{}

func (badPlanEngine) Run(context.Context, Invocation) (*Result, error) {
	return &Result{SessionID: "sess-spec", Structured: []byte(`{"issues": "not a list"}`)}, nil
}
