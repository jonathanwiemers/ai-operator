package gitlabfake

import (
	"context"
	"testing"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func TestProjectsRouting(t *testing.T) {
	ctx := context.Background()
	p := NewProjects(New())
	b := p.Add("Jo/Infra/Cluster")

	for _, tc := range []struct {
		name, project string
		want          *Fake
	}{
		{"default path", "group/app", p.Default},
		{"default case", "GROUP/App", p.Default},
		{"default id", "1", p.Default},
		{"added path", "jo/infra/cluster", b},
		{"added encoded", "jo%2Finfra%2Fcluster", b},
		{"added id", "2", b},
	} {
		f, err := p.route(tc.project)
		if err != nil || f != tc.want {
			t.Errorf("%s: route(%q) = %p, %v", tc.name, tc.project, f, err)
		}
		pr, err := p.GetProject(ctx, tc.project)
		if err != nil || pr.ID != tc.want.Project.ID {
			t.Errorf("%s: GetProject = %+v, %v", tc.name, pr, err)
		}
	}
}

func TestProjectsUnknownIs404(t *testing.T) {
	p := NewProjects(New())
	ctx := context.Background()
	for _, project := range []string{"other/repo", "99", ""} {
		if _, err := p.GetIssue(ctx, project, 1); !gitlab.IsNotFound(err) {
			t.Errorf("GetIssue(%q) err = %v, want 404", project, err)
		}
	}
	if err := p.CreatePipeline(ctx, "other/repo", "main"); !gitlab.IsNotFound(err) {
		t.Errorf("CreatePipeline err = %v, want 404", err)
	}
}

func TestProjectsIsolation(t *testing.T) {
	ctx := context.Background()
	p := NewProjects(New())
	p.Add("group/b")

	// Issues.
	if _, err := p.CreateIssue(ctx, "group/b", gitlab.CreateIssueOptions{Title: "in b"}); err != nil {
		t.Fatal(err)
	}
	if is, _ := p.ListIssues(ctx, "group/app", gitlab.ListIssuesOptions{State: "all"}); len(is) != 0 {
		t.Errorf("issue of b visible in a: %v", is)
	}
	if is, _ := p.ListIssues(ctx, "group/b", gitlab.ListIssuesOptions{}); len(is) != 1 {
		t.Errorf("issue missing in b: %v", is)
	}

	// MRs, both ways.
	mrB, err := p.CreateMergeRequest(ctx, "group/b", gitlab.CreateMergeRequestOptions{Title: "mr b", SourceBranch: "agent/x", TargetBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if mr, _ := p.FindOpenMergeRequest(ctx, "group/app", "agent/x"); mr != nil {
		t.Errorf("MR of b visible in a: %+v", mr)
	}
	if _, err := p.GetMergeRequest(ctx, "group/app", mrB.IID); !gitlab.IsNotFound(err) {
		t.Errorf("GetMergeRequest in a: %v", err)
	}
	if _, err := p.CreateMergeRequest(ctx, "group/app", gitlab.CreateMergeRequestOptions{Title: "mr a", SourceBranch: "agent/y", TargetBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if mr, _ := p.FindOpenMergeRequest(ctx, "group/b", "agent/y"); mr != nil {
		t.Errorf("MR of a visible in b: %+v", mr)
	}
	if mr, _ := p.FindOpenMergeRequest(ctx, "group/b", "agent/x"); mr == nil {
		t.Error("MR missing in b")
	}

	// Pipelines.
	p.Default.AddPipeline("sha1", "main", "success")
	if ps, _ := p.ListPipelines(ctx, "group/b", gitlab.ListPipelinesOptions{}); len(ps) != 0 {
		t.Errorf("pipeline of a visible in b: %v", ps)
	}
	if ps, _ := p.ListPipelines(ctx, "group/app", gitlab.ListPipelinesOptions{}); len(ps) != 1 {
		t.Errorf("pipeline missing in a: %v", ps)
	}
}

func TestProjectsSharedBot(t *testing.T) {
	d := New()
	d.Users = []gitlab.User{{ID: 5, Username: "owner"}}
	p := NewProjects(d)
	b := p.Add("group/b")
	if b.Bot != d.Bot || len(b.Users) != 1 || !b.Clock.Equal(d.Clock) {
		t.Errorf("added fake did not copy bot, users and clock: %+v", b)
	}
	if u, _ := p.FindUser(context.Background(), "owner"); u == nil || u.ID != 5 {
		t.Errorf("FindUser = %v", u)
	}
	if u, _ := p.CurrentUser(context.Background()); u.ID != d.Bot.ID {
		t.Errorf("CurrentUser = %v", u)
	}
}

func TestProjectsAddTwiceReturnsSameFake(t *testing.T) {
	p := NewProjects(New())
	a := p.Add("g/b")
	if b := p.Add("G/B"); b != a {
		t.Fatal("second Add of the same path must return the registered Fake")
	}
}
