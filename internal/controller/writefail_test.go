package controller

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// rejectStatus makes the API server reject every status patch of the
// reconciler but the write-failure record with reject's error.
func (e *projectEnv) rejectStatus(reject func() error) {
	e.r.Client = interceptor.NewClient(e.c.(client.WithWatch), interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, c client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			data, err := patch.Data(obj)
			if err != nil {
				return err
			}
			if !strings.Contains(string(data), `"writeFailures"`) {
				return reject()
			}
			return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
		},
	})
}

func invalidActions() error {
	return apierrors.NewInvalid(schema.GroupKind{Group: "ai.wiemers.me", Kind: "AgentTask"}, "shop-pilot",
		field.ErrorList{field.Invalid(field.NewPath("status", "pilot", "proposed").Index(0).Child("actions"), nil, "must be of type array")})
}

func (e *projectEnv) createTask(task *aiv1.AgentTask) *aiv1.AgentTask {
	e.t.Helper()
	status := task.Status
	if err := e.c.Create(e.ctx, task); err != nil {
		e.t.Fatal(err)
	}
	task.Status = status
	if err := e.c.Status().Update(e.ctx, task); err != nil {
		e.t.Fatal(err)
	}
	return e.task(task.Name)
}

// A rejected status patch of the operator is recorded on the task and
// posted once on the tracking issue; the same failure again only counts.
func TestRejectedStatusWriteRecordedOnce(t *testing.T) {
	e := newProjectEnv(t)
	st := e.createTask(&aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Name: specTaskName, Namespace: ns},
		Spec:   aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeSpec},
		Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{TrackingIssueIID: 7}}})
	e.rejectStatus(invalidActions)

	for i := range 2 {
		err := e.r.patchPilot(e.ctx, e.task(specTaskName), func(ps *aiv1.PilotStatus) { ps.LastNoteID = 5 })
		if !apierrors.IsInvalid(err) {
			t.Fatalf("patch %d: the original error is returned, got %v", i, err)
		}
	}
	st = e.task(specTaskName)
	if got := st.Status.WriteFailures; len(got) != 1 || got[0].Source != "operator/patchPilot" || got[0].Count != 2 ||
		!strings.Contains(got[0].Error, "must be of type array") || got[0].Key == "" || got[0].At == nil {
		t.Fatalf("one entry, counted twice: %+v", got)
	}
	if st.Status.Pilot != nil {
		t.Fatalf("the rejected pilot status is not sent again: %+v", st.Status.Pilot)
	}
	notes := e.gl.IssueNotes[7]
	if len(notes) != 1 || !strings.HasPrefix(notes[0].Body, "⚠️ The API server rejected a status write of the operator (`operator/patchPilot`): ") ||
		!strings.Contains(notes[0].Body, "must be of type array") {
		t.Fatalf("one line on the tracking issue: %+v", notes)
	}

	// Another source is another failure.
	if err := e.r.queueCommand(e.ctx, st, aiv1.PilotCommand{Action: aiv1.PilotAction{Tool: "pause"}}); !apierrors.IsInvalid(err) {
		t.Fatalf("queueCommand: %v", err)
	}
	if got := e.task(specTaskName).Status.WriteFailures; len(got) != 2 || got[1].Source != "operator/queueCommand" || len(e.gl.IssueNotes[7]) != 2 {
		t.Fatalf("a second source: %+v, %d notes", got, len(e.gl.IssueNotes[7]))
	}
}

// A Coordinate task's failure goes to its issue, the pilot's tracking issue.
func TestRejectedProposalsPatchOnCoordinateIssue(t *testing.T) {
	e := newProjectEnv(t)
	coord := e.createTask(&aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Name: "shop-pilot", Namespace: ns},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeCoordinate, Issue: &aiv1.IssueRef{IID: 9}}})
	e.rejectStatus(invalidActions)
	if err := e.r.patchProposals(e.ctx, coord, func(ps *aiv1.PilotStatus) { ps.LastNoteID = 3 }); !apierrors.IsInvalid(err) {
		t.Fatal(err)
	}
	if got := e.task("shop-pilot").Status.WriteFailures; len(got) != 1 || got[0].Source != "operator/patchProposals" || len(e.gl.IssueNotes[9]) != 1 {
		t.Fatalf("recorded on the Coordinate task, posted on #9: %+v, %v", got, e.gl.IssueNotes)
	}
}

// Conflicts and missing tasks are retried or transient: not reported.
func TestConflictNotReported(t *testing.T) {
	e := newProjectEnv(t)
	e.createTask(&aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Name: specTaskName, Namespace: ns},
		Spec:   aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeSpec},
		Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{TrackingIssueIID: 7}}})
	e.rejectStatus(func() error {
		return apierrors.NewConflict(schema.GroupResource{Group: "ai.wiemers.me", Resource: "agenttasks"}, specTaskName, nil)
	})
	if err := e.r.patchPrompts(e.ctx, e.task(specTaskName), func(l []aiv1.Prompt) []aiv1.Prompt { return l }); !apierrors.IsConflict(err) {
		t.Fatalf("the conflict is returned: %v", err)
	}
	if got := e.task(specTaskName).Status.WriteFailures; len(got) != 0 || len(e.gl.IssueNotes[7]) != 0 {
		t.Fatalf("a conflict is not reported: %+v, %v", got, e.gl.IssueNotes)
	}
}
