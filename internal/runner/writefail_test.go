package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// A proposal the API server rejects (e.g. "actions: null") is recorded on
// the Coordinate task and posted once on its tracking issue.
func TestRecordProposalRejectedReported(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aiv1.AddToScheme(scheme)
	task := &aiv1.AgentTask{ObjectMeta: metav1.ObjectMeta{Name: "shop-pilot", Namespace: "agents"},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeCoordinate, Issue: &aiv1.IssueRef{IID: 5}}}
	k8s := interceptor.NewClient(fake.NewClientBuilder().WithScheme(scheme).WithObjects(task).WithStatusSubresource(task).Build(), interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, c client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			if data, _ := patch.Data(obj); !strings.Contains(string(data), `"writeFailures"`) {
				return apierrors.NewInvalid(schema.GroupKind{Group: "ai.wiemers.me", Kind: "AgentTask"}, "shop-pilot",
					field.ErrorList{field.Invalid(field.NewPath("status", "pilot", "proposed").Index(0).Child("actions"), nil, "must be of type array")})
			}
			return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
		},
	})
	gl := gitlabfake.New()
	r := &Runner{K8s: k8s, Namespace: "agents", TaskName: "shop-pilot", Log: logr.Discard(),
		NewGitLab:   func(string, string) gitlab.API { return gl },
		projectSpec: aiv1.AgentProjectSpec{GitLab: aiv1.GitLabSpec{Project: "group/app"}}}
	ctx := context.Background()
	for _, src := range []string{"note:1", "note:2"} {
		if err := r.recordProposal(ctx, task, aiv1.PilotProposal{Summary: "Pause", Source: src}); !apierrors.IsInvalid(err) {
			t.Fatalf("the original error is returned: %v", err)
		}
	}
	var got aiv1.AgentTask
	_ = k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "shop-pilot"}, &got)
	if wf := got.Status.WriteFailures; len(wf) != 1 || wf[0].Source != "runner/recordProposal" || wf[0].Count != 2 {
		t.Fatalf("one entry, counted twice: %+v", wf)
	}
	if got.Status.Pilot != nil {
		t.Fatalf("the proposal is not sent again: %+v", got.Status.Pilot)
	}
	if n := gl.IssueNotes[5]; len(n) != 1 || !strings.HasPrefix(n[0].Body, "⚠️ The API server rejected a status write of the pilot (`runner/recordProposal`): ") {
		t.Fatalf("one line on the tracking issue: %+v", n)
	}
}
