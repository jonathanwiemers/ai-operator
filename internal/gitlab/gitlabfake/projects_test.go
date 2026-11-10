package gitlabfake

import (
	"context"
	"slices"
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
	if _, err := p.CreatePipeline(ctx, "other/repo", "main"); !gitlab.IsNotFound(err) {
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

func TestLabelEvents(t *testing.T) {
	ctx := context.Background()
	p := NewProjects(New())
	b := p.Add("group/b")
	b.AddIssue(1, "x")
	p.Default.AddIssue(1, "y")
	human := gitlab.User{ID: 7, Username: "alice"}
	b.LabelBy(1, human, "agent-repo::group/c")
	if _, err := p.UpdateIssue(ctx, "group/b", 1, gitlab.UpdateIssueOptions{AddLabels: []string{"agent::ready"}, RemoveLabels: []string{"agent-repo::group/c"}}); err != nil {
		t.Fatal(err)
	}
	evs, err := p.ListIssueLabelEvents(ctx, "group/b", 1)
	if err != nil || len(evs) != 3 || evs[0].User != human || evs[0].Action != "add" || evs[1].User.ID != b.Bot.ID || evs[1].Action != "remove" ||
		evs[2].LabelName() != "agent::ready" {
		t.Fatalf("label events: %+v %v", evs, err)
	}
	if evs, _ := p.ListIssueLabelEvents(ctx, "group/app", 1); len(evs) != 0 {
		t.Errorf("label events of b visible in a: %+v", evs)
	}
}

func TestListRepositoryTree(t *testing.T) {
	ctx := context.Background()
	p := NewProjects(New())
	b := p.Add("group/infra")
	b.Files = []string{"clusters/a/x.yaml", "clusters/a/sub/y.yaml", "README.md"}
	es, err := p.ListRepositoryTree(ctx, "group/infra", "clusters", "", true)
	var got []string
	for _, e := range es {
		got = append(got, e.Type+" "+e.Path)
	}
	if err != nil || !slices.Equal(got, []string{"tree clusters/a", "tree clusters/a/sub", "blob clusters/a/sub/y.yaml", "blob clusters/a/x.yaml"}) {
		t.Fatalf("recursive tree %v %v", got, err)
	}
	if es, _ := p.ListRepositoryTree(ctx, "group/infra", "", "", false); len(es) != 2 || es[0].Path != "README.md" || es[1].Path != "clusters" || es[1].Type != "tree" {
		t.Fatalf("root %+v", es)
	}
	if _, err := p.ListRepositoryTree(ctx, "group/infra", "nope", "", true); gitlab.StatusCode(err) != 404 {
		t.Fatalf("missing path: %v", err)
	}
	if es, _ := p.ListRepositoryTree(ctx, "group/app", "", "", true); len(es) != 0 {
		t.Fatalf("tree of infra visible in app: %+v", es)
	}
}

func TestTagsAndCommitRefs(t *testing.T) {
	ctx := context.Background()
	p := NewProjects(New())
	b := p.Add("group/b")
	b.AddTag("2026.11.1", gitlab.TagCommit{ID: "m1", ParentIDs: []string{"a", "b"}}, "c0")
	b.AddTag("2026.11.2", gitlab.TagCommit{ID: "m2", ParentIDs: []string{"m1", "c"}}, "m1", "c0")

	tags, err := p.ListTags(ctx, "group/b", gitlab.ListTagsOptions{Limit: 1})
	if err != nil || len(tags) != 1 || tags[0].Name != "2026.11.2" {
		t.Fatalf("the newest tag first, within the limit: %+v %v", tags, err)
	}
	if tags, _ := p.ListTags(ctx, "group/app", gitlab.ListTagsOptions{}); len(tags) != 0 {
		t.Fatalf("the default project has no tags: %+v", tags)
	}
	refs, err := p.ListCommitRefs(ctx, "group/b", "c0", "tag")
	if err != nil || !slices.Equal(refs, []gitlab.CommitRef{{Type: "tag", Name: "2026.11.1"}, {Type: "tag", Name: "2026.11.2"}}) {
		t.Fatalf("both tags contain c0: %+v %v", refs, err)
	}
	if refs, _ := p.ListCommitRefs(ctx, "group/b", "m2", "branch"); len(refs) != 0 {
		t.Fatalf("no branch refs: %+v", refs)
	}
	if !slices.Equal(b.RefLists, []string{"c0", "m2"}) || b.TagLists != 1 {
		t.Fatalf("calls recorded: %v %d", b.RefLists, b.TagLists)
	}
}

func TestCreateMilestoneRoundTrip(t *testing.T) {
	ctx := context.Background()
	f := New()
	f.Milestones = []gitlab.Milestone{{ID: 5, IID: 3, Title: "old", State: "closed"}}
	m, err := f.CreateMilestone(ctx, "group/app", gitlab.CreateMilestoneOptions{Title: "new"})
	if err != nil || m.IID != 4 || m.ID == 5 {
		t.Fatalf("CreateMilestone = %+v, %v", m, err)
	}
	list, _ := f.ListMilestones(ctx, "group/app")
	if len(list) != 1 || list[0].Title != "new" {
		t.Fatalf("list = %+v", list)
	}
	got, err := f.GetMilestone(ctx, "group/app", m.ID)
	if err != nil || got.Title != "new" {
		t.Fatalf("get = %+v, %v", got, err)
	}
}
