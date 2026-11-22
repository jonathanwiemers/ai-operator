package gitlabfake

import (
	"context"
	"slices"
	"strings"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// labelEvent records a label change by a user; f.mu is held.
func (f *Fake) labelEvent(iid int64, by gitlab.User, label, action string) {
	if f.LabelEvents == nil {
		f.LabelEvents = map[int64][]gitlab.LabelEvent{}
	}
	// Own IDs and no tick: label events do not shift the IDs and times
	// other tests expect.
	f.labelEventID++
	at := f.Clock
	ev := gitlab.LabelEvent{ID: f.labelEventID, User: by, Action: action, CreatedAt: &at}
	ev.Label = &struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}{Name: label}
	f.LabelEvents[iid] = append(f.LabelEvents[iid], ev)
}

// LabelBy adds a label to an issue by hand, as user by: the label, its
// label event and a new updated_at.
func (f *Fake) LabelBy(iid int64, by gitlab.User, label string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	is := f.Issues[iid]
	if !slices.Contains(is.Labels, label) {
		is.Labels = append(is.Labels, label)
	}
	f.labelEvent(iid, by, label, "add")
	is.UpdatedAt = f.tick()
}

func (f *Fake) ListIssueLabelEvents(_ context.Context, _ string, iid int64) ([]gitlab.LabelEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.LabelEventLists++
	if _, ok := f.Issues[iid]; !ok {
		return nil, &gitlab.Error{StatusCode: 404, Method: "GET", Path: "resource_label_events"}
	}
	return slices.Clone(f.LabelEvents[iid]), nil
}

// ListRepositoryTree lists f.Files and their directories under path, all
// of them with recursive, else the direct children. A path with nothing
// under it is a 404, like GitLab's "Tree Not Found". ref is ignored.
func (f *Fake) ListRepositoryTree(_ context.Context, _ string, path, _ string, recursive bool) ([]gitlab.TreeEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TreeLists = append(f.TreeLists, path)
	if f.TreeError != nil {
		return nil, f.TreeError
	}
	path = strings.Trim(path, "/")
	prefix := ""
	if path != "" {
		prefix = path + "/"
	}
	seen := map[string]bool{}
	var out []gitlab.TreeEntry
	add := func(p, typ string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, gitlab.TreeEntry{Path: p, Name: p[strings.LastIndex(p, "/")+1:], Type: typ})
		}
	}
	for _, file := range slices.Sorted(slices.Values(f.Files)) {
		rest, ok := strings.CutPrefix(file, prefix)
		if !ok || rest == "" {
			continue
		}
		segs := strings.Split(rest, "/")
		depth := len(segs)
		if !recursive {
			segs = segs[:1]
		}
		for i := range segs {
			typ := "tree"
			if i == depth-1 {
				typ = "blob"
			}
			add(prefix+strings.Join(segs[:i+1], "/"), typ)
		}
	}
	if path != "" && len(out) == 0 {
		return nil, &gitlab.Error{StatusCode: 404, Method: "GET", Path: "repository/tree", Body: `{"message":"404 Tree Not Found"}`}
	}
	return out, nil
}

// AddTag adds a tag at commit, as the newest one, and makes it a ref that
// contains commit.ID and each of contains (older commits it includes).
func (f *Fake) AddTag(name string, commit gitlab.TagCommit, contains ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Tags = append([]gitlab.Tag{{Name: name, Commit: commit}}, f.Tags...)
	if f.CommitRefs == nil {
		f.CommitRefs = map[string][]gitlab.CommitRef{}
	}
	for _, sha := range append([]string{commit.ID}, contains...) {
		f.CommitRefs[sha] = append(f.CommitRefs[sha], gitlab.CommitRef{Type: "tag", Name: name})
	}
}

func (f *Fake) ListTags(_ context.Context, _ string, opts gitlab.ListTagsOptions) ([]gitlab.Tag, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TagLists++
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	return slices.Clone(f.Tags[:min(limit, len(f.Tags))]), nil
}

// ListCommitRefs returns the refs of sha of refType ("all" or "": every
// type).
func (f *Fake) ListCommitRefs(_ context.Context, _ string, sha, refType string) ([]gitlab.CommitRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.RefLists = append(f.RefLists, sha)
	var out []gitlab.CommitRef
	for _, r := range f.CommitRefs[sha] {
		if refType == "" || refType == "all" || r.Type == refType {
			out = append(out, r)
		}
	}
	return out, nil
}

// SetHead moves the head of an MR to sha and records a version. Setting
// MRs[iid].SHA directly records a version at the next rebase or version read.
func (f *Fake) SetHead(iid int64, sha string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if mr, ok := f.MRs[iid]; ok {
		mr.SHA = sha
		f.syncVersions(iid)
	}
}
