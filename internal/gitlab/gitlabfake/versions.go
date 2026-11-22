package gitlabfake

import (
	"context"
	"fmt"
	"slices"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// syncVersions records a version of the MR if its head SHA is not the head
// of the newest version. The caller holds f.mu.
func (f *Fake) syncVersions(iid int64) {
	mr, ok := f.MRs[iid]
	if !ok {
		return
	}
	vs := f.versions[iid]
	if len(vs) > 0 && vs[len(vs)-1].HeadCommitSHA == mr.SHA {
		return
	}
	if f.versions == nil {
		f.versions = map[int64][]mrVersion{}
	}
	f.nextVersionID++
	v := mrVersion{commits: slices.Clone(f.MRCommits[iid]), diffs: f.diffsOf(iid)}
	v.ID, v.HeadCommitSHA, v.State, v.CreatedAt = f.nextVersionID, mr.SHA, "collected", *f.tick()
	v.BaseCommitSHA, v.StartCommitSHA = f.MainSHA, f.MainSHA
	if mr.DiffRefs != nil {
		v.BaseCommitSHA, v.StartCommitSHA = mr.DiffRefs.BaseSHA, mr.DiffRefs.StartSHA
	}
	f.versions[iid] = append(vs, v)
}

func (f *Fake) diffsOf(iid int64) []gitlab.Diff {
	if ds, ok := f.MRDiffs[iid]; ok {
		return slices.Clone(ds)
	}
	return []gitlab.Diff{{OldPath: "app.go", NewPath: "app.go", Diff: fmt.Sprintf("@@ -1 +1 @@\n-old\n+change of !%d\n", iid)}}
}

// ListMergeRequestVersions lists the versions of the MR, newest first.
func (f *Fake) ListMergeRequestVersions(_ context.Context, _ string, iid int64) ([]gitlab.MergeRequestVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.VersionLists++
	if _, ok := f.MRs[iid]; !ok {
		return nil, &gitlab.Error{StatusCode: 404, Method: "GET", Path: "versions"}
	}
	f.syncVersions(iid)
	vs := f.versions[iid]
	out := make([]gitlab.MergeRequestVersion, 0, len(vs))
	for i := len(vs) - 1; i >= 0; i-- {
		out = append(out, vs[i].MergeRequestVersion)
	}
	return out, nil
}

// GetMergeRequestVersion returns a version with the commits and diffs it
// had at its head.
func (f *Fake) GetMergeRequestVersion(_ context.Context, _ string, iid, versionID int64) (*gitlab.MergeRequestVersionDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.MRs[iid]; !ok {
		return nil, &gitlab.Error{StatusCode: 404, Method: "GET", Path: "versions"}
	}
	f.syncVersions(iid)
	for _, v := range f.versions[iid] {
		if v.ID == versionID {
			return &gitlab.MergeRequestVersionDetail{MergeRequestVersion: v.MergeRequestVersion,
				Commits: slices.Clone(v.commits), Diffs: slices.Clone(v.diffs)}, nil
		}
	}
	return nil, &gitlab.Error{StatusCode: 404, Method: "GET", Path: "versions"}
}

// CompareCommits returns Compares["from..to"], or 404.
func (f *Fake) CompareCommits(_ context.Context, _ string, from, to string) (*gitlab.Compare, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.Compares[from+".."+to]
	if !ok {
		return nil, &gitlab.Error{StatusCode: 404, Method: "GET", Path: "compare"}
	}
	return &gitlab.Compare{Commits: slices.Clone(c.Commits), Diffs: slices.Clone(c.Diffs)}, nil
}

// GetBranch returns the branch with its head from BranchHeads, or 404.
func (f *Fake) GetBranch(_ context.Context, _ string, branch string) (*gitlab.Branch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.BranchGets = append(f.BranchGets, branch)
	head, ok := f.BranchHeads[branch]
	if !ok {
		return nil, &gitlab.Error{StatusCode: 404, Method: "GET", Path: "branches"}
	}
	return &gitlab.Branch{Name: branch, Commit: gitlab.Commit{ID: head}}, nil
}

// DeleteBranch records the branch in DeletedBranches; the fake keeps no
// branches otherwise.
func (f *Fake) DeleteBranch(_ context.Context, _ string, branch string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DeletedBranches = append(f.DeletedBranches, branch)
	return nil
}

func (f *Fake) ListMergeRequestDiffs(_ context.Context, _ string, iid int64) ([]gitlab.Diff, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DiffLists == nil {
		f.DiffLists = map[int64]int{}
	}
	f.DiffLists[iid]++
	if err := f.DiffErrors[iid]; err != nil {
		return nil, err
	}
	if _, ok := f.MRs[iid]; !ok {
		return nil, &gitlab.Error{StatusCode: 404, Method: "GET", Path: "diffs"}
	}
	return f.diffsOf(iid), nil
}
