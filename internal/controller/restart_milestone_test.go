package controller

import (
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// startRun puts a run of task name in progress in step 03 plan for 42
// minutes, with its pod.
func (e *projectEnv) startRun(name string) {
	e.t.Helper()
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: types.UID(name + "-uid")}}
	if err := e.c.Create(e.ctx, pod); err != nil {
		e.t.Fatal(err)
	}
	since := metav1.NewTime(e.r.now().Add(-42 * time.Minute))
	e.patchStatus(name, func(s *aiv1.AgentTaskStatus) {
		s.Run.ActiveRunRequest = s.Run.ObservedRunRequest + 1
		s.Run.CurrentStep, s.Run.CurrentStepSince, s.PodName = "03 plan", &since, name
	})
}

func (e *projectEnv) podExists(name string) bool {
	e.t.Helper()
	var pod corev1.Pod
	return e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: name}, &pod) == nil
}

// sendCommand hands a Desk command to task name and reconciles.
func (e *projectEnv) sendCommand(name string, id int64, author, body string) {
	e.t.Helper()
	task := e.tasks()[name]
	task.Spec.Source.ChatMessages = append(task.Spec.Source.ChatMessages, aiv1.ChatMessage{ID: id, Kind: "command", Text: body, Author: author, AuthorName: strings.Split(author, "@")[0]})
	if err := e.c.Update(e.ctx, &task); err != nil {
		e.t.Fatal(err)
	}
	e.reconcile()
}

// checkRestart runs the acceptance of a restart on task name: refused for
// mallory, nothing to restart without a run, an owner's restart deletes the
// pod and leaves one note on issue iid (none when iid is 0).
func checkRestart(t *testing.T, e *projectEnv, name, owner string, iid int64) {
	t.Helper()
	const restart = `{"cmd":"restart"}`
	notes := func() []string {
		return slices.DeleteFunc(botComments(e, iid), func(b string) bool { return !strings.Contains(b, "Run restarted") })
	}
	e.sendCommand(name, 301, owner, restart)
	if got := chatAnswer(e.tasks()[name], 301); got != nothingToRestart {
		t.Fatalf("no run: answer %q", got)
	}

	e.startRun(name)
	e.sendCommand(name, 302, "mallory@example.com", restart)
	if got := chatAnswer(e.tasks()[name], 302); got != notOwnerRestart || !e.podExists(name) {
		t.Fatalf("non-owner: answer %q, pod kept %v", got, e.podExists(name))
	}

	e.sendCommand(name, 303, owner, restart)
	if got := chatAnswer(e.tasks()[name], 303); got != "🔄 The run is restarted (restart 1 of at most 3)." {
		t.Fatalf("owner: answer %q", got)
	}
	if e.podExists(name) {
		t.Fatal("the run's pod must be deleted")
	}
	who := strings.Split(owner, "@")[0]
	if iid == 0 {
		return
	}
	if n := notes(); len(n) != 1 || !strings.HasPrefix(n[0], "🔄 Run restarted by "+who+" via the Desk in step `03 plan` after 42m (restart 1 of at most 3).") {
		t.Fatalf("restart notes on #%d: %q", iid, n)
	}
	e.reconcile()
	if n := notes(); len(n) != 1 {
		t.Fatalf("a second sync restarts nothing: %q", n)
	}
}

func TestRestartSpecTaskAutopilot(t *testing.T) {
	e := newAutopilotEnv(t)
	e.gl.Milestones[0].Description += "\n/agent owner-subjects ada@example.com"
	e.reconcile()
	checkRestart(t, e, specTaskName, "ada@example.com", e.autopilot().TrackingIssueIID)
}

func TestRestartCoordinateTask(t *testing.T) {
	e := newPilotEnv(t)
	e.gl.Milestones[0].Description += "\n/agent owner-subjects alice@example.com"
	e.reconcile()
	checkRestart(t, e, pilotTaskName, "alice@example.com", e.autopilot().TrackingIssueIID)
}

func TestRestartSpecTaskWithoutAutopilot(t *testing.T) {
	e := newProjectEnv(t)
	e.r.Now = func() time.Time { return e.gl.Clock }
	e.gl.Milestones = []gitlab.Milestone{{ID: 11, IID: 3, Title: "Checkout v2", State: "active",
		Description: "/agent spec\n/agent owner-subjects ada@example.com\nBuild a new checkout."}}
	e.reconcile()
	if st := e.tasks()[specTaskName]; st.Spec.Autopilot || !slices.Equal(st.Spec.OwnerSubjects, []string{"ada@example.com"}) {
		t.Fatalf("a planned milestone without autopilot: %+v", st.Spec)
	}
	checkRestart(t, e, specTaskName, "ada@example.com", 0)
	// Other commands still get the autopilot's answer.
	e.sendCommand(specTaskName, 304, "ada@example.com", `{"cmd":"pause"}`)
	if got := chatAnswer(e.tasks()[specTaskName], 304); got != "The autopilot is off for this milestone." {
		t.Fatalf("pause: answer %q", got)
	}
}

// The pilot kept for the retrospective and drafts after the autopilot is
// done can be restarted too; the note goes to the tracking issue.
func TestRestartCoordinateTaskAfterAutopilotDone(t *testing.T) {
	e := newDraftEnv(t)
	if st := e.tasks()[specTaskName]; !autopilotDone(&st) {
		t.Fatal("expected a finished autopilot")
	}
	checkRestart(t, e, pilotTaskName, "alice@example.com", e.autopilot().TrackingIssueIID)
}
