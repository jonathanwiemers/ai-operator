package gitlabfake

import (
	"context"
	"testing"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func TestMergeRequestVersions(t *testing.T) {
	ctx := context.Background()
	f := New()
	f.MainSHA = "main1"
	mr, _ := f.CreateMergeRequest(ctx, "group/app", gitlab.CreateMergeRequestOptions{Title: "x", SourceBranch: "b", TargetBranch: "main"})
	f.MRCommits = map[int64][]gitlab.Commit{mr.IID: {{ID: "c1", Title: "first"}}}
	f.SetHead(mr.IID, "h1")
	f.MRDiffs = map[int64][]gitlab.Diff{mr.IID: {{NewPath: "a.go", Diff: "+rebased\n"}}}
	if err := f.RebaseMergeRequest(ctx, "group/app", mr.IID); err != nil {
		t.Fatal(err)
	}
	vs, err := f.ListMergeRequestVersions(ctx, "group/app", mr.IID)
	if err != nil || len(vs) != 3 {
		t.Fatalf("versions %+v %v", vs, err)
	}
	if vs[0].HeadCommitSHA != "h1-rebased" || vs[1].HeadCommitSHA != "h1" || vs[2].HeadCommitSHA != "" || vs[0].BaseCommitSHA != "main1" {
		t.Fatalf("versions %+v", vs)
	}
	d, err := f.GetMergeRequestVersion(ctx, "group/app", mr.IID, vs[1].ID)
	if err != nil || len(d.Commits) != 1 || d.Commits[0].ID != "c1" || len(d.Diffs) != 1 || d.Diffs[0].Diff == "+rebased\n" {
		t.Fatalf("v2 %+v %v", d, err)
	}
	d, _ = f.GetMergeRequestVersion(ctx, "group/app", mr.IID, vs[0].ID)
	if d.Diffs[0].Diff != "+rebased\n" {
		t.Fatalf("v3 %+v", d)
	}
	if _, err := f.GetMergeRequestVersion(ctx, "group/app", mr.IID, 12345); gitlab.StatusCode(err) != 404 {
		t.Fatal(err)
	}
	f.Compares = map[string]*gitlab.Compare{"a..b": {Commits: []gitlab.Commit{{ID: "x"}}}}
	if c, err := f.CompareCommits(ctx, "group/app", "a", "b"); err != nil || len(c.Commits) != 1 {
		t.Fatal(c, err)
	}
	if _, err := f.CompareCommits(ctx, "group/app", "a", "z"); gitlab.StatusCode(err) != 404 {
		t.Fatal(err)
	}
}
