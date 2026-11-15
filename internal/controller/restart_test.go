package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

const restartTask = "shop-42-add-paypal"

// restartEnv is a task whose run 1 is in progress in step 18 push for 42
// minutes, its pod made by the AgentTask reconciler, and the project
// reconciler on the same client. Pods get a UID, as on a real API server.
func restartEnv(t *testing.T) (*taskEnv, *AgentProjectReconciler, *gitlabfake.Fake) {
	t.Helper()
	e := newTaskEnv(t, newImplTask(restartTask, 1))
	uids := 0
	e.r.Client = interceptor.NewClient(e.c.(client.WithWatch), interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			uids++
			obj.SetUID(types.UID(fmt.Sprintf("pod-uid-%d", uids)))
			return c.Create(ctx, obj, opts...)
		},
	})
	e.reconcile(restartTask)
	task := e.task(restartTask)
	orig := task.DeepCopy()
	task.Status.Run.ActiveRunRequest = 1
	task.Status.Run.CurrentStep = "18 push"
	task.Status.Run.CurrentStepSince = &metav1.Time{Time: e.now}
	if err := e.c.Status().Patch(e.ctx, task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(42 * time.Minute)
	gl := gitlabfake.New()
	gl.AddIssue(42, "Add PayPal", "agent::working")
	r := &AgentProjectReconciler{Client: e.r.Client, Scheme: e.r.Scheme, Now: func() time.Time { return e.now }}
	return e, r, gl
}

// restartCommand stores a restart chat command written by author and
// applies it.
func restartCommand(t *testing.T, e *taskEnv, r *AgentProjectReconciler, gl *gitlabfake.Fake, author string) (string, error) {
	t.Helper()
	task := e.task(restartTask)
	task.Spec.OwnerSubjects = []string{"ada"}
	task.Spec.Source.ChatMessages = []aiv1.ChatMessage{{ID: 3, Kind: "command", Text: `{"cmd":"restart"}`, Author: author, AuthorName: author}}
	if err := e.c.Update(e.ctx, task); err != nil {
		t.Fatal(err)
	}
	return applyStored(t, e, r, gl)
}

// applyStored applies the chat commands of the stored task, like a sync
// after one whose update of the task was lost, and returns the answer.
func applyStored(t *testing.T, e *taskEnv, r *AgentProjectReconciler, gl *gitlabfake.Fake) (string, error) {
	t.Helper()
	task := e.task(restartTask)
	run, err := r.applyChatCommands(e.ctx, gl, testProject().Spec.WithDefaults(), gl.Issues[42], task, nil, gl.Bot.ID)
	if err != nil {
		return "", err
	}
	if run {
		t.Fatal("a restart requests no new run")
	}
	return task.Spec.Source.ChatAnswers[0].Text, nil
}

func (e *taskEnv) podUID() types.UID {
	e.t.Helper()
	var pod corev1.Pod
	if !e.exists(&pod, restartTask) {
		return ""
	}
	return pod.UID
}

func TestRestartRunMidRun(t *testing.T) {
	e, r, gl := restartEnv(t)
	old := e.podUID()
	if old == "" {
		t.Fatal("expected the run's pod")
	}

	got, err := restartCommand(t, e, r, gl, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if want := "🔄 The run is restarted (restart 1 of at most 3)."; got != want {
		t.Fatalf("answer = %q, want %q", got, want)
	}
	notes := gl.IssueNotes[42]
	want := "🔄 Run restarted by ada via the Desk in step `18 push` after 42m (restart 1 of at most 3).\n\n" +
		"<!-- ai-operator:restart=chat/" + restartTask + "/3 pod=" + string(old) + " -->"
	if len(notes) != 1 || notes[0].Body != want {
		t.Fatalf("notes = %+v, want %q", notes, want)
	}
	if e.podUID() != "" {
		t.Fatal("the pod must be deleted")
	}

	// The AgentTask reconciler starts a new pod for the same request; the
	// runner then counts the interruption (ActiveRunRequest > Observed).
	e.reconcile(restartTask)
	if uid := e.podUID(); uid == "" || uid == old {
		t.Fatalf("expected a new pod for the active request, uid %q", uid)
	}
	if run := e.task(restartTask).Status.Run; run.ActiveRunRequest != 1 || run.ObservedRunRequest != 0 {
		t.Fatalf("active %d, observed %d: the request must stay in progress", run.ActiveRunRequest, run.ObservedRunRequest)
	}

	// The sync's update of the task was lost (the stored task has no
	// answer): the command is applied again and finds its note, so the new
	// pod stays and no second note is written.
	fresh := e.podUID()
	if got, err := applyStored(t, e, r, gl); err != nil || got != "🔄 The run is restarted." {
		t.Fatalf("replay: answer %q, err %v", got, err)
	}
	if len(gl.IssueNotes[42]) != 1 || e.podUID() != fresh {
		t.Fatalf("replayed restart: notes %+v", gl.IssueNotes[42])
	}
}

func TestRestartRunDeleteFails(t *testing.T) {
	e, r, gl := restartEnv(t)
	ok := r.Client
	r.Client = interceptor.NewClient(e.c.(client.WithWatch), interceptor.Funcs{
		Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
			return errors.New("apiserver down")
		},
	})
	if _, err := restartCommand(t, e, r, gl, "ada"); err == nil {
		t.Fatal("expected the delete error")
	}
	// The next sync applies the command again: the noted pod is still
	// there, so it is deleted, without a second note.
	r.Client = ok
	if got, err := applyStored(t, e, r, gl); err != nil || got != "🔄 The run is restarted." {
		t.Fatalf("retry: answer %q, err %v", got, err)
	}
	if e.podUID() != "" || len(gl.IssueNotes[42]) != 1 {
		t.Fatalf("the retry deletes the pod once noted: notes %+v", gl.IssueNotes[42])
	}
}

func TestRestartRunIgnoresForgedNote(t *testing.T) {
	e, r, gl := restartEnv(t)
	gl.IssueNotes[42] = []gitlab.Note{{ID: 1, Author: gitlab.User{ID: 5, Username: "mallory"},
		Body: "<!-- ai-operator:restart=chat/" + restartTask + "/3 pod=other -->"}}
	if got, err := restartCommand(t, e, r, gl, "ada"); err != nil || !strings.HasPrefix(got, "🔄 The run is restarted (") {
		t.Fatalf("answer %q, err %v", got, err)
	}
	if e.podUID() != "" || len(gl.IssueNotes[42]) != 2 {
		t.Fatalf("only the bot's note counts: notes %+v", gl.IssueNotes[42])
	}
}

func TestRestartRunNothingInProgress(t *testing.T) {
	e, r, gl := restartEnv(t)
	task := e.task(restartTask)
	orig := task.DeepCopy()
	task.Status.Run.ObservedRunRequest = 1
	if err := e.c.Status().Patch(e.ctx, task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	got, err := restartCommand(t, e, r, gl, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if got != nothingToRestart || len(gl.IssueNotes[42]) != 0 || e.podUID() == "" {
		t.Fatalf("answer %q, notes %+v: nothing may happen", got, gl.IssueNotes[42])
	}
}

func TestRestartRunNotOwner(t *testing.T) {
	e, r, gl := restartEnv(t)
	got, err := restartCommand(t, e, r, gl, "mallory")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Only the milestone's owners can restart a run." || len(gl.IssueNotes[42]) != 0 || e.podUID() == "" {
		t.Fatalf("answer %q, notes %+v: a non-owner restarts nothing", got, gl.IssueNotes[42])
	}
}

func TestRestartRunNoteFails(t *testing.T) {
	e, r, gl := restartEnv(t)
	gl.NoteErrors = map[int64]error{42: errors.New("gitlab down")}
	if _, err := restartCommand(t, e, r, gl, "ada"); err == nil {
		t.Fatal("expected the note error")
	}
	if e.podUID() == "" {
		t.Fatal("the pod must stay when the note fails")
	}
}
