package gitlabfake

import (
	"context"
	"slices"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// Link is a link between two issues; Type is seen from From.
type Link struct {
	ID       int64
	From, To int64
	Type     string
}

func inverseLink(t string) string {
	switch t {
	case gitlab.LinkBlocks:
		return gitlab.LinkIsBlockedBy
	case gitlab.LinkIsBlockedBy:
		return gitlab.LinkBlocks
	}
	return t
}

func (f *Fake) ListIssueLinks(_ context.Context, _ string, iid int64) ([]gitlab.LinkedIssue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.Issues[iid]; !ok {
		return nil, &gitlab.Error{StatusCode: 404, Method: "GET", Path: "links"}
	}
	var out []gitlab.LinkedIssue
	add := func(other int64, typ string, id int64) {
		li := gitlab.LinkedIssue{IID: other, ProjectID: f.Project.ID, IssueLinkID: id, LinkType: typ}
		if is := f.Issues[other]; is != nil {
			li.State, li.Title = is.State, is.Title
		}
		out = append(out, li)
	}
	for _, l := range f.Links {
		switch iid {
		case l.From:
			add(l.To, l.Type, l.ID)
		case l.To:
			add(l.From, inverseLink(l.Type), l.ID)
		}
	}
	return append(out, f.ForeignLinks[iid]...), nil
}

func (f *Fake) CreateIssueLink(_ context.Context, _ string, iid, target int64, linkType string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Issues[iid] == nil || f.Issues[target] == nil {
		return &gitlab.Error{StatusCode: 404, Method: "POST", Path: "links"}
	}
	if f.FreeTier && linkType != gitlab.LinkRelatesTo {
		return &gitlab.Error{StatusCode: 403, Method: "POST", Path: "links", Body: `{"message":"Blocked issues not available for current license"}`}
	}
	for _, l := range f.Links {
		if (l.From == iid && l.To == target) || (l.From == target && l.To == iid) {
			return &gitlab.Error{StatusCode: 409, Method: "POST", Path: "links", Body: `{"message":"Issue(s) already assigned"}`}
		}
	}
	f.Links = append(f.Links, Link{ID: f.id(), From: iid, To: target, Type: linkType})
	return nil
}

func (f *Fake) DeleteIssueLink(_ context.Context, _ string, iid, linkID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, l := range f.Links {
		if l.ID == linkID && (l.From == iid || l.To == iid) {
			f.Links = append(f.Links[:i], f.Links[i+1:]...)
			return nil
		}
	}
	return &gitlab.Error{StatusCode: 404, Method: "DELETE", Path: "links"}
}

func (f *Fake) ListIssueStateEvents(_ context.Context, _ string, iid int64) ([]gitlab.StateEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.StateEvents[iid]), nil
}

// ClosedBy records that merging mr closed issue iid.
func (f *Fake) ClosedBy(iid, mr int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ev := gitlab.StateEvent{ID: f.id(), State: "closed", SourceMergeRequest: &gitlab.MergeRequestRef{IID: mr, ProjectID: f.Project.ID}}
	if f.StateEvents == nil {
		f.StateEvents = map[int64][]gitlab.StateEvent{}
	}
	f.StateEvents[iid] = append(f.StateEvents[iid], ev)
}
