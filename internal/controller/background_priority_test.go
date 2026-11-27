package controller

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// priorityAPI records the priority of the context of each issue listing.
type priorityAPI struct {
	gitlab.API
	seen *[]gitlab.Priority
}

func (p priorityAPI) ListIssues(ctx context.Context, project string, opts gitlab.ListIssuesOptions) ([]gitlab.Issue, error) {
	*p.seen = append(*p.seen, gitlab.PriorityOf(ctx))
	return p.API.ListIssues(ctx, project, opts)
}

func TestProjectPollIsBackground(t *testing.T) {
	e := newProjectEnv(t)
	var seen []gitlab.Priority
	gl := priorityAPI{API: e.gl, seen: &seen}
	e.r.NewGitLab = func(string, string) gitlab.API { return gl }
	if _, err := e.r.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: "shop"}}); err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 {
		t.Fatal("the poll listed no issues")
	}
	for _, p := range seen {
		if p != gitlab.Background {
			t.Errorf("poll read has priority %v, want background", p)
		}
	}
}
