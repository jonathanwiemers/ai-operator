package writefail

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

const tracking = 7

// reportEnv is a stored task with a phase and a cluster client that may
// intercept calls.
func reportEnv(t *testing.T, funcs *interceptor.Funcs) (client.Client, *aiv1.AgentTask) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := aiv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	task := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-1-api", Namespace: "agents"},
		Spec:       aiv1.AgentTaskSpec{ProjectRef: "shop", Issue: &aiv1.IssueRef{IID: tracking}},
		Status:     aiv1.AgentTaskStatus{Phase: aiv1.TaskPhaseRunning},
	}
	b := fake.NewClientBuilder().WithScheme(scheme).WithObjects(task).WithStatusSubresource(&aiv1.AgentTask{})
	if funcs != nil {
		b = b.WithInterceptorFuncs(*funcs)
	}
	return b.Build(), task
}

func stored(t *testing.T, c client.Client, task *aiv1.AgentTask) aiv1.AgentTask {
	t.Helper()
	var got aiv1.AgentTask
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(task), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// A new failure is recorded with count 1 and posted once; the same
// failure again bumps the count and posts nothing.
func TestReportRecordsAndPostsOnce(t *testing.T) {
	ctx := context.Background()
	c, task := reportEnv(t, nil)
	gl := gitlabfake.New()
	// The caller's copy carries the status the API server rejected.
	task.Status.Phase = "Bogus"
	rejected := apierrors.NewBadRequest("status.phase: Unsupported value")

	for range 2 {
		if err := Report(ctx, c, gl, "group/app", task, tracking, "runner/recordRetro", rejected); err != nil {
			t.Fatal(err)
		}
	}
	got := stored(t, c, task)
	if got.Status.Phase != aiv1.TaskPhaseRunning {
		t.Errorf("Report wrote the rejected phase: %q", got.Status.Phase)
	}
	if len(got.Status.WriteFailures) != 1 {
		t.Fatalf("one entry per source and error, got %+v", got.Status.WriteFailures)
	}
	f := got.Status.WriteFailures[0]
	if f.Source != "runner/recordRetro" || f.Count != 2 || f.Error != rejected.Error() || len(f.Key) != 16 || f.At == nil {
		t.Errorf("entry %+v", f)
	}
	notes := gl.IssueNotes[tracking]
	if len(notes) != 1 || notes[0].Body != Line("runner/recordRetro", rejected) {
		t.Fatalf("want one line on the tracking issue, got %+v", notes)
	}
}

// A repeated failure moves to the end, and only the newest
// MaxWriteFailures entries are kept.
func TestReportKeepsTheNewest(t *testing.T) {
	ctx := context.Background()
	c, task := reportEnv(t, nil)
	for i := range aiv1.MaxWriteFailures + 1 {
		if err := Report(ctx, c, nil, "group/app", task, tracking, fmt.Sprintf("operator/s%d", i), errors.New("invalid")); err != nil {
			t.Fatal(err)
		}
	}
	if err := Report(ctx, c, nil, "group/app", task, tracking, "operator/s2", errors.New("invalid")); err != nil {
		t.Fatal(err)
	}
	var sources []string
	for _, f := range stored(t, c, task).Status.WriteFailures {
		sources = append(sources, fmt.Sprintf("%s:%d", f.Source, f.Count))
	}
	want := "operator/s1:1 operator/s3:1 operator/s4:1 operator/s5:1 operator/s2:2"
	if strings.Join(sources, " ") != want {
		t.Fatalf("entries %v, want %s", sources, want)
	}
}

// The stored error fits the CRD's bound, whatever the API server said.
func TestReportCutsLongErrors(t *testing.T) {
	c, task := reportEnv(t, nil)
	long := errors.New(strings.Repeat("x", 3*aiv1.MaxWriteFailureError))
	if err := Report(context.Background(), c, nil, "", task, 0, "operator/save", long); err != nil {
		t.Fatal(err)
	}
	f := stored(t, c, task).Status.WriteFailures[0]
	if len(f.Error) > aiv1.MaxWriteFailureError || !strings.HasSuffix(f.Error, "…") {
		t.Fatalf("error of %d bytes, ends %q", len(f.Error), f.Error[len(f.Error)-8:])
	}
}

// Without a GitLab client or a tracking issue the failure is recorded,
// and nothing is posted.
func TestReportWithoutTrackingIssue(t *testing.T) {
	ctx := context.Background()
	c, task := reportEnv(t, nil)
	gl := gitlabfake.New()
	if err := Report(ctx, c, gl, "group/app", task, 0, "operator/a", errors.New("invalid")); err != nil {
		t.Fatal(err)
	}
	if err := Report(ctx, c, nil, "group/app", task, tracking, "operator/b", errors.New("invalid")); err != nil {
		t.Fatal(err)
	}
	if n := len(stored(t, c, task).Status.WriteFailures); n != 2 {
		t.Errorf("want both recorded, got %d", n)
	}
	if len(gl.IssueNotes) != 0 {
		t.Errorf("posted %+v", gl.IssueNotes)
	}
}

// A conflicting write is retried on a fresh read.
func TestReportRetriesConflicts(t *testing.T) {
	conflicts := 1
	c, task := reportEnv(t, &interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, c client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			if conflicts > 0 {
				conflicts--
				return apierrors.NewConflict(schema.GroupResource{Resource: "agenttasks"}, obj.GetName(), errors.New("changed"))
			}
			return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
		},
	})
	gl := gitlabfake.New()
	if err := Report(context.Background(), c, gl, "group/app", task, tracking, "operator/save", errors.New("invalid")); err != nil {
		t.Fatal(err)
	}
	if f := stored(t, c, task).Status.WriteFailures; len(f) != 1 || f[0].Count != 1 {
		t.Errorf("entries %+v", f)
	}
	if len(gl.IssueNotes[tracking]) != 1 {
		t.Errorf("notes %+v", gl.IssueNotes)
	}
}

// A failure that could not be recorded posts nothing, so a broken status
// cannot flood the issue on every poll.
func TestReportRecordFails(t *testing.T) {
	ctx := context.Background()
	gl := gitlabfake.New()
	c, task := reportEnv(t, &interceptor.Funcs{
		SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
			return apierrors.NewInvalid(schema.GroupKind{Kind: "AgentTask"}, "shop-1-api", nil)
		},
	})
	err := Report(ctx, c, gl, "group/app", task, tracking, "operator/save", errors.New("invalid"))
	if err == nil || !strings.Contains(err.Error(), "recording the rejected status write of operator/save") {
		t.Fatalf("err %v", err)
	}
	gone := task.DeepCopy()
	gone.Name = "deleted"
	if err := Report(ctx, c, gl, "group/app", gone, tracking, "operator/save", errors.New("invalid")); !apierrors.IsNotFound(err) {
		t.Fatalf("a deleted task: want not found, got %v", err)
	}
	if len(gl.IssueNotes) != 0 {
		t.Errorf("posted %+v", gl.IssueNotes)
	}
}

// A note GitLab refuses is an error after the failure is recorded.
func TestReportNoteFails(t *testing.T) {
	c, task := reportEnv(t, nil)
	gl := gitlabfake.New()
	gl.NoteErrors = map[int64]error{tracking: errors.New("403 Forbidden")}
	err := Report(context.Background(), c, gl, "group/app", task, tracking, "operator/save", errors.New("invalid"))
	if err == nil || !strings.Contains(err.Error(), "posting the rejected status write of operator/save on #7: 403 Forbidden") {
		t.Fatalf("err %v", err)
	}
	if len(stored(t, c, task).Status.WriteFailures) != 1 {
		t.Error("the failure is recorded before the note")
	}
}
