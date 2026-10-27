// Package gitlabfake is an in-memory implementation of gitlab.API for tests.
package gitlabfake

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// Fake is a minimal in-memory GitLab.
type Fake struct {
	mu         sync.Mutex
	Bot        gitlab.User
	Project    gitlab.Project
	Milestones []gitlab.Milestone
	Issues     map[int64]*gitlab.Issue
	IssueNotes map[int64][]gitlab.Note
	MRs        map[int64]*gitlab.MergeRequest
	MRNotes    map[int64][]gitlab.Note
	// Pipelines, newest last; Jobs and Traces by pipeline / job ID.
	Pipelines []gitlab.Pipeline
	Jobs      map[int64][]gitlab.Job
	Traces    map[int64]string
	Retried   []int64
	// RetryError makes RetryPipeline fail.
	RetryError error
	nextID     int64
	lastIID    int64
	Clock      time.Time
	// IssueUpdates records the options of every UpdateIssue call, oldest first.
	IssueUpdates []gitlab.UpdateIssueOptions
	// MergeErrors makes MergeMergeRequest of an MR fail.
	MergeErrors map[int64]error
	// NoteErrors makes CreateIssueNote on an issue fail.
	NoteErrors map[int64]error
	// NoteErrorsAfterWrite makes the next CreateIssueNote on an issue store
	// the note and then fail, like a write whose answer timed out. Each entry
	// is used once.
	NoteErrorsAfterWrite map[int64]error
	// UpdateErrors makes UpdateIssue of an issue fail.
	UpdateErrors map[int64]error
	// Merged and Rebased record the MRs merged and rebased, in order.
	Merged, Rebased []int64
	// RebaseConflict makes rebases end in a conflict.
	RebaseConflict bool
	// MRDiffs are the diffs of an MR; without an entry an MR has one file
	// whose change is the same before and after a rebase.
	MRDiffs map[int64][]gitlab.Diff
	// CreateIssueError makes CreateIssue fail.
	CreateIssueError error
	// CreateErrorsAfterWrite makes the next CreateIssue calls create the
	// issue and then fail with these errors, one per call, oldest first.
	CreateErrorsAfterWrite []error
	// Links between issues; FreeTier refuses blocking links like GitLab
	// Free does.
	Links    []Link
	FreeTier bool
	// ForeignLinks are links of an issue to issues of other projects.
	ForeignLinks map[int64][]gitlab.LinkedIssue
	// MRPipelines records the MRs a merge request pipeline was started for;
	// NoMRPipelines refuses them like a project without such pipelines.
	MRPipelines   []int64
	NoMRPipelines bool
	// BranchPipelines records the branches a pipeline was started for;
	// NoPipelines refuses them.
	BranchPipelines []string
	NoPipelines     bool
	// StateEvents of issues, oldest first.
	StateEvents map[int64][]gitlab.StateEvent
	// Users FindUser knows, besides the bot; FindUserError makes it fail.
	Users         []gitlab.User
	FindUserError error
	// Replies maps a reply in a thread to the note that started it;
	// DiscussionLists counts ListIssueDiscussions calls.
	Replies         map[int64]int64
	DiscussionLists int
	// Hooks are the project hooks, oldest first; HookWrites records every
	// write ("add 1001", "edit 1001", "delete 1001"); HookError makes every
	// hook call fail, e.g. a 403 for a bot without the Maintainer role;
	// HookLists counts ListProjectHooks calls.
	Hooks      []Hook
	HookWrites []string
	HookError  error
	HookLists  int
}

var _ gitlab.API = (*Fake)(nil)

// New returns an empty fake.
func New() *Fake {
	return &Fake{
		Bot:        gitlab.User{ID: 999, Username: "ai-bot"},
		Project:    gitlab.Project{ID: 1, PathWithNamespace: "group/app", DefaultBranch: "main", HTTPURLToRepo: "https://gitlab.example/group/app.git"},
		Issues:     map[int64]*gitlab.Issue{},
		IssueNotes: map[int64][]gitlab.Note{},
		MRs:        map[int64]*gitlab.MergeRequest{},
		MRNotes:    map[int64][]gitlab.Note{},
		Jobs:       map[int64][]gitlab.Job{},
		Traces:     map[int64]string{},
		nextID:     1000,
		Clock:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func (f *Fake) tick() *time.Time {
	f.Clock = f.Clock.Add(time.Second)
	t := f.Clock
	return &t
}

func (f *Fake) id() int64 { f.nextID++; return f.nextID }

// AddIssue adds an issue and returns it.
func (f *Fake) AddIssue(iid int64, title string, labels ...string) *gitlab.Issue {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.tick()
	is := &gitlab.Issue{ID: f.id(), IID: iid, Title: title, State: "opened", Labels: labels, CreatedAt: now, UpdatedAt: now,
		WebURL: fmt.Sprintf("https://gitlab.example/group/app/-/issues/%d", iid)}
	f.Issues[iid] = is
	return is
}

// AddNote adds a human or bot note to an issue and bumps updated_at.
func (f *Fake) AddNote(iid int64, author gitlab.User, body string) gitlab.Note {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := gitlab.Note{ID: f.id(), Body: body, Author: author, CreatedAt: f.tick()}
	f.IssueNotes[iid] = append(f.IssueNotes[iid], n)
	if is := f.Issues[iid]; is != nil {
		is.UpdatedAt = f.tick()
	}
	return n
}

// AddMRNote adds a comment by author to a merge request.
func (f *Fake) AddMRNote(iid int64, author gitlab.User, body string) gitlab.Note {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := gitlab.Note{ID: f.id(), Body: body, Author: author, CreatedAt: f.tick()}
	f.MRNotes[iid] = append(f.MRNotes[iid], n)
	if mr := f.MRs[iid]; mr != nil {
		mr.UpdatedAt = f.tick()
	}
	return n
}

// SetLabels replaces the labels of an issue.
func (f *Fake) SetLabels(iid int64, labels ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Issues[iid].Labels = labels
	f.Issues[iid].UpdatedAt = f.tick()
}

// Labels returns the labels of an issue.
func (f *Fake) Labels(iid int64) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.Issues[iid].Labels)
}

func (f *Fake) CurrentUser(context.Context) (*gitlab.User, error) { u := f.Bot; return &u, nil }

func (f *Fake) FindUser(_ context.Context, username string) (*gitlab.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FindUserError != nil {
		return nil, f.FindUserError
	}
	for _, u := range append([]gitlab.User{f.Bot}, f.Users...) {
		if u.Username == username {
			return &u, nil
		}
	}
	return nil, nil
}

func (f *Fake) GetProject(context.Context, string) (*gitlab.Project, error) {
	p := f.Project
	return &p, nil
}

func (f *Fake) ListMilestones(context.Context, string) ([]gitlab.Milestone, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.Milestones), nil
}

func (f *Fake) GetMilestone(_ context.Context, _ string, id int64) (*gitlab.Milestone, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.Milestones {
		if m.ID == id {
			return &m, nil
		}
	}
	return nil, &gitlab.Error{StatusCode: 404, Method: "GET", Path: "milestone"}
}

func (f *Fake) UpdateMilestoneDescription(_ context.Context, _ string, id int64, description string) (*gitlab.Milestone, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.Milestones {
		if f.Milestones[i].ID == id {
			f.Milestones[i].Description = description
			m := f.Milestones[i]
			return &m, nil
		}
	}
	return nil, &gitlab.Error{StatusCode: 404, Method: "PUT", Path: "milestone"}
}

func (f *Fake) ListIssues(_ context.Context, _ string, opts gitlab.ListIssuesOptions) ([]gitlab.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := opts.State
	if state == "" {
		state = "opened"
	}
	var out []gitlab.Issue
	for _, is := range f.Issues {
		if state != "all" && is.State != state {
			continue
		}
		if opts.Milestone != "" && (is.Milestone == nil || is.Milestone.Title != opts.Milestone) {
			continue
		}
		if opts.UpdatedAfter != nil && (is.UpdatedAt == nil || !is.UpdatedAt.After(*opts.UpdatedAfter)) {
			continue
		}
		match := true
		for _, l := range opts.Labels {
			if !is.HasLabel(l) {
				match = false
			}
		}
		if match {
			out = append(out, *is)
		}
	}
	slices.SortFunc(out, func(a, b gitlab.Issue) int { return int(a.IID - b.IID) })
	return out, nil
}

func (f *Fake) GetIssue(_ context.Context, _ string, iid int64) (*gitlab.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	is, ok := f.Issues[iid]
	if !ok {
		return nil, &gitlab.Error{StatusCode: 404, Method: "GET", Path: "issue"}
	}
	c := *is
	return &c, nil
}

func (f *Fake) CreateIssue(_ context.Context, _ string, opts gitlab.CreateIssueOptions) (*gitlab.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CreateIssueError != nil {
		return nil, f.CreateIssueError
	}
	// Like GitLab, IIDs of deleted issues are not used again.
	iid := f.lastIID + 1
	for k := range f.Issues {
		if k >= iid {
			iid = k + 1
		}
	}
	f.lastIID = iid
	now := f.tick()
	is := &gitlab.Issue{ID: f.id(), IID: iid, Title: opts.Title, Description: opts.Description, State: "opened", CreatedAt: now, UpdatedAt: now, Author: f.Bot}
	if opts.Labels != "" {
		is.Labels = strings.Split(opts.Labels, ",")
	}
	for _, m := range f.Milestones {
		if m.ID == opts.MilestoneID {
			mc := m
			is.Milestone = &mc
		}
	}
	f.Issues[iid] = is
	if len(f.CreateErrorsAfterWrite) > 0 {
		err := f.CreateErrorsAfterWrite[0]
		f.CreateErrorsAfterWrite = f.CreateErrorsAfterWrite[1:]
		return nil, err
	}
	c := *is
	return &c, nil
}

func (f *Fake) UpdateIssue(_ context.Context, _ string, iid int64, opts gitlab.UpdateIssueOptions) (*gitlab.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	is, ok := f.Issues[iid]
	if !ok {
		return nil, &gitlab.Error{StatusCode: 404, Method: "PUT", Path: "issue"}
	}
	if err := f.UpdateErrors[iid]; err != nil {
		return nil, err
	}
	f.IssueUpdates = append(f.IssueUpdates, opts)
	if opts.Title != "" {
		is.Title = opts.Title
	}
	if opts.Description != "" {
		is.Description = opts.Description
	}
	var labels []string
	for _, l := range is.Labels {
		if !slices.Contains(opts.RemoveLabels, l) {
			labels = append(labels, l)
		}
	}
	for _, l := range opts.AddLabels {
		if !slices.Contains(labels, l) {
			labels = append(labels, l)
		}
	}
	is.Labels = labels
	if len(opts.AssigneeIDs) > 0 {
		is.Assignees = nil
		for _, id := range opts.AssigneeIDs {
			u := gitlab.User{ID: id}
			if id == f.Bot.ID {
				u = f.Bot
			}
			is.Assignees = append(is.Assignees, u)
		}
	}
	if opts.StateEvent == "close" {
		is.State = "closed"
		is.ClosedAt = f.tick()
	}
	for _, m := range f.Milestones {
		if opts.MilestoneID != 0 && m.ID == opts.MilestoneID {
			mc := m
			is.Milestone = &mc
		}
	}
	is.UpdatedAt = f.tick()
	c := *is
	return &c, nil
}

// AddReply adds a comment by author to an issue as a reply in the thread
// of the note root.
func (f *Fake) AddReply(iid, root int64, author gitlab.User, body string) gitlab.Note {
	n := f.AddNote(iid, author, body)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Replies == nil {
		f.Replies = map[int64]int64{}
	}
	f.Replies[n.ID] = root
	return n
}

// ListIssueDiscussions groups an issue's notes into threads: a reply goes
// with its root, every other note is a thread of its own.
func (f *Fake) ListIssueDiscussions(_ context.Context, _ string, iid int64) ([]gitlab.Discussion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DiscussionLists++
	var out []gitlab.Discussion
	at := map[int64]int{}
	for _, n := range f.IssueNotes[iid] {
		if i, ok := at[f.Replies[n.ID]]; ok {
			out[i].Notes = append(out[i].Notes, n)
			continue
		}
		at[n.ID] = len(out)
		out = append(out, gitlab.Discussion{ID: fmt.Sprint(n.ID), Notes: []gitlab.Note{n}})
	}
	return out, nil
}

// ReplyIssueDiscussion adds the bot's reply to the thread ListIssueDiscussions
// named discussion (its root note's ID).
func (f *Fake) ReplyIssueDiscussion(ctx context.Context, project string, iid int64, discussion, body string) (*gitlab.Note, error) {
	root, err := strconv.ParseInt(discussion, 10, 64)
	f.mu.Lock()
	known := err == nil && slices.ContainsFunc(f.IssueNotes[iid], func(n gitlab.Note) bool { return n.ID == root && f.Replies[root] == 0 })
	f.mu.Unlock()
	if !known {
		return nil, &gitlab.Error{StatusCode: 404, Method: "POST", Path: "discussion"}
	}
	n, err := f.CreateIssueNote(ctx, project, iid, body)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Replies == nil {
		f.Replies = map[int64]int64{}
	}
	f.Replies[n.ID] = root
	return n, nil
}

func (f *Fake) ListIssueNotes(_ context.Context, _ string, iid int64) ([]gitlab.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.IssueNotes[iid]), nil
}

func (f *Fake) CreateIssueNote(_ context.Context, _ string, iid int64, body string) (*gitlab.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.NoteErrors[iid]; err != nil {
		return nil, err
	}
	n := gitlab.Note{ID: f.id(), Body: body, Author: f.Bot, CreatedAt: f.tick()}
	f.IssueNotes[iid] = append(f.IssueNotes[iid], n)
	if is := f.Issues[iid]; is != nil {
		is.UpdatedAt = f.tick()
	}
	if err := f.NoteErrorsAfterWrite[iid]; err != nil {
		delete(f.NoteErrorsAfterWrite, iid)
		return nil, err
	}
	return &n, nil
}

func (f *Fake) UpdateIssueNote(_ context.Context, _ string, iid, noteID int64, body string) (*gitlab.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.IssueNotes[iid] {
		if n := &f.IssueNotes[iid][i]; n.ID == noteID {
			n.Body = body
			if is := f.Issues[iid]; is != nil {
				is.UpdatedAt = f.tick()
			}
			out := *n
			return &out, nil
		}
	}
	return nil, &gitlab.Error{StatusCode: 404, Method: "PUT", Path: "note"}
}

func (f *Fake) GetMergeRequest(_ context.Context, _ string, iid int64) (*gitlab.MergeRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mr, ok := f.MRs[iid]
	if !ok {
		return nil, &gitlab.Error{StatusCode: 404, Method: "GET", Path: "mr"}
	}
	c := *mr
	if c.HeadPipeline != nil {
		// A head pipeline added with AddPipeline shows its current status.
		hp := *c.HeadPipeline
		for _, p := range f.Pipelines {
			if p.ID == hp.ID {
				hp = p
			}
		}
		c.HeadPipeline = &hp
	}
	return &c, nil
}

func (f *Fake) FindOpenMergeRequest(_ context.Context, _ string, branch string) (*gitlab.MergeRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, mr := range f.MRs {
		if mr.SourceBranch == branch && mr.State == "opened" {
			c := *mr
			return &c, nil
		}
	}
	return nil, nil
}

func (f *Fake) CreateMergeRequest(_ context.Context, _ string, opts gitlab.CreateMergeRequestOptions) (*gitlab.MergeRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	iid := int64(len(f.MRs) + 1)
	mr := &gitlab.MergeRequest{ID: f.id(), IID: iid, Title: opts.Title, Description: opts.Description, State: "opened", SourceBranch: opts.SourceBranch,
		TargetBranch: opts.TargetBranch, WebURL: fmt.Sprintf("https://gitlab.example/group/app/-/merge_requests/%d", iid), UpdatedAt: f.tick(),
		Draft: strings.HasPrefix(opts.Title, "Draft:"), DetailedMergeStatus: "mergeable"}
	if mr.Draft {
		mr.DetailedMergeStatus = "draft_status"
	}
	for _, m := range f.Milestones {
		if m.ID == opts.MilestoneID {
			mc := m
			mr.Milestone = &mc
		}
	}
	f.MRs[iid] = mr
	c := *mr
	return &c, nil
}

func (f *Fake) UpdateMergeRequest(_ context.Context, _ string, iid int64, opts gitlab.UpdateMergeRequestOptions) (*gitlab.MergeRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mr, ok := f.MRs[iid]
	if !ok {
		return nil, &gitlab.Error{StatusCode: 404, Method: "PUT", Path: "mr"}
	}
	if opts.Title != "" {
		mr.Title = opts.Title
		mr.Draft = strings.HasPrefix(opts.Title, "Draft:")
		if !mr.Draft && mr.DetailedMergeStatus == "draft_status" {
			mr.DetailedMergeStatus = "mergeable"
		}
	}
	if opts.Description != "" {
		mr.Description = opts.Description
	}
	if opts.ReviewerIDs != nil {
		mr.Reviewers = nil
		for _, id := range opts.ReviewerIDs {
			for _, u := range append([]gitlab.User{f.Bot}, f.Users...) {
				if u.ID == id {
					mr.Reviewers = append(mr.Reviewers, u)
				}
			}
		}
	}
	mr.UpdatedAt = f.tick()
	c := *mr
	return &c, nil
}

// MergeMergeRequest merges a mergeable MR whose head matches opts.SHA, like
// GitLab: 405 for drafts and other blocks, 406 for conflicts and needed
// rebases, 409 for a SHA mismatch.
func (f *Fake) MergeMergeRequest(_ context.Context, _ string, iid int64, opts gitlab.MergeOptions) (*gitlab.MergeRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mr, ok := f.MRs[iid]
	if !ok {
		return nil, &gitlab.Error{StatusCode: 404, Method: "PUT", Path: "merge"}
	}
	if err := f.MergeErrors[iid]; err != nil {
		return nil, err
	}
	switch {
	case opts.SHA != "" && opts.SHA != mr.SHA:
		return nil, &gitlab.Error{StatusCode: 409, Method: "PUT", Path: "merge", Body: "SHA does not match HEAD of source branch"}
	case mr.DetailedMergeStatus == "need_rebase" || mr.DetailedMergeStatus == "conflict":
		return nil, &gitlab.Error{StatusCode: 406, Method: "PUT", Path: "merge", Body: "Branch cannot be merged"}
	case mr.DetailedMergeStatus != "mergeable" || mr.State != "opened":
		return nil, &gitlab.Error{StatusCode: 405, Method: "PUT", Path: "merge", Body: "Method Not Allowed"}
	}
	mr.State = "merged"
	mr.MergeCommitSHA = "merge-" + mr.SHA
	mr.UpdatedAt = f.tick()
	f.Merged = append(f.Merged, iid)
	c := *mr
	return &c, nil
}

// RebaseMergeRequest rebases the MR at once: its head gets a new SHA, or it
// ends in a conflict when RebaseConflict is set.
func (f *Fake) RebaseMergeRequest(_ context.Context, _ string, iid int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	mr, ok := f.MRs[iid]
	if !ok {
		return &gitlab.Error{StatusCode: 404, Method: "PUT", Path: "rebase"}
	}
	f.Rebased = append(f.Rebased, iid)
	if f.RebaseConflict {
		mr.DetailedMergeStatus = "conflict"
	} else {
		mr.SHA += "-rebased"
		mr.DetailedMergeStatus = "mergeable"
	}
	mr.UpdatedAt = f.tick()
	return nil
}

func (f *Fake) ListMergeRequestDiffs(_ context.Context, _ string, iid int64) ([]gitlab.Diff, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.MRs[iid]; !ok {
		return nil, &gitlab.Error{StatusCode: 404, Method: "GET", Path: "diffs"}
	}
	if ds, ok := f.MRDiffs[iid]; ok {
		return slices.Clone(ds), nil
	}
	return []gitlab.Diff{{OldPath: "app.go", NewPath: "app.go", Diff: fmt.Sprintf("@@ -1 +1 @@\n-old\n+change of !%d\n", iid)}}, nil
}

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

func (f *Fake) ListMergeRequestNotes(_ context.Context, _ string, iid int64) ([]gitlab.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.MRNotes[iid]), nil
}

func (f *Fake) UpdateMergeRequestNote(_ context.Context, _ string, iid, noteID int64, body string) (*gitlab.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.MRNotes[iid] {
		if n := &f.MRNotes[iid][i]; n.ID == noteID {
			n.Body = body
			c := *n
			return &c, nil
		}
	}
	return nil, &gitlab.Error{StatusCode: 404, Method: "PUT", Path: "note"}
}

// CreatePipeline records the branch a pipeline was started for; with
// NoPipelines it is refused like a project without CI for it.
func (f *Fake) CreatePipeline(_ context.Context, _ string, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.NoPipelines {
		return &gitlab.Error{StatusCode: 400, Method: "POST", Path: "pipeline", Body: `{"message":{"base":["Pipeline filtered out by workflow rules."]}}`}
	}
	f.BranchPipelines = append(f.BranchPipelines, ref)
	return nil
}

func (f *Fake) CreateMergeRequestPipeline(_ context.Context, _ string, iid int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.MRs[iid]; !ok {
		return &gitlab.Error{StatusCode: 404, Method: "POST", Path: "pipelines"}
	}
	if f.NoMRPipelines {
		return &gitlab.Error{StatusCode: 400, Method: "POST", Path: "pipelines", Body: `{"message":{"base":["No stages / jobs for this pipeline."]}}`}
	}
	f.MRPipelines = append(f.MRPipelines, iid)
	return nil
}

func (f *Fake) DeleteMergeRequestNote(_ context.Context, _ string, iid, noteID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	notes := f.MRNotes[iid]
	for i := range notes {
		if notes[i].ID == noteID {
			f.MRNotes[iid] = append(notes[:i], notes[i+1:]...)
			return nil
		}
	}
	return &gitlab.Error{StatusCode: 404, Method: "DELETE", Path: "note"}
}

func (f *Fake) CreateMergeRequestNote(_ context.Context, _ string, iid int64, body string) (*gitlab.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := gitlab.Note{ID: f.id(), Body: body, Author: f.Bot, CreatedAt: f.tick()}
	f.MRNotes[iid] = append(f.MRNotes[iid], n)
	if mr := f.MRs[iid]; mr != nil {
		mr.UpdatedAt = f.tick()
	}
	return &n, nil
}

// AddPipeline adds a pipeline and returns its ID.
func (f *Fake) AddPipeline(sha, ref, status string, jobs ...gitlab.Job) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id()
	f.Pipelines = append(f.Pipelines, gitlab.Pipeline{ID: id, SHA: sha, Ref: ref, Status: status,
		WebURL: fmt.Sprintf("https://gitlab.example/group/app/-/pipelines/%d", id)})
	for i := range jobs {
		if jobs[i].ID == 0 {
			jobs[i].ID = f.id()
		}
	}
	f.Jobs[id] = jobs
	return id
}

// SetPipelineStatus changes the status of a pipeline.
func (f *Fake) SetPipelineStatus(id int64, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.Pipelines {
		if f.Pipelines[i].ID == id {
			f.Pipelines[i].Status = status
		}
	}
}

func (f *Fake) ListPipelines(_ context.Context, _ string, opts gitlab.ListPipelinesOptions) ([]gitlab.Pipeline, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []gitlab.Pipeline
	for i := len(f.Pipelines) - 1; i >= 0; i-- {
		p := f.Pipelines[i]
		if opts.SHA != "" && p.SHA != opts.SHA {
			continue
		}
		if opts.Ref != "" && p.Ref != opts.Ref {
			continue
		}
		if opts.Scope == "finished" && (p.Status == "running" || p.Status == "pending" || p.Status == "created") {
			continue
		}
		out = append(out, p)
		if opts.Limit > 0 && len(out) == opts.Limit {
			break
		}
	}
	return out, nil
}

func (f *Fake) ListPipelineJobs(_ context.Context, _ string, pipelineID int64, scope string) ([]gitlab.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []gitlab.Job
	for _, j := range f.Jobs[pipelineID] {
		if scope == "" || j.Status == scope {
			out = append(out, j)
		}
	}
	return out, nil
}

func (f *Fake) JobTrace(_ context.Context, _ string, jobID int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Traces[jobID], nil
}

func (f *Fake) RetryPipeline(_ context.Context, _ string, pipelineID int64) (*gitlab.Pipeline, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.RetryError != nil {
		return nil, f.RetryError
	}
	f.Retried = append(f.Retried, pipelineID)
	for i := range f.Pipelines {
		if f.Pipelines[i].ID == pipelineID {
			f.Pipelines[i].Status = "running"
			p := f.Pipelines[i]
			return &p, nil
		}
	}
	return nil, &gitlab.Error{StatusCode: 404, Method: "POST", Path: "retry"}
}

// Hook is a project hook with the token GitLab never returns.
type Hook struct {
	gitlab.ProjectHook
	Token string
}

func hookFrom(id int64, opts gitlab.ProjectHookOptions) Hook {
	return Hook{ProjectHook: gitlab.ProjectHook{ID: id, URL: opts.URL, IssuesEvents: opts.IssuesEvents, NoteEvents: opts.NoteEvents,
		MergeRequestsEvents: opts.MergeRequestsEvents, PipelineEvents: opts.PipelineEvents, PushEvents: opts.PushEvents,
		EnableSSLVerification: opts.EnableSSLVerification}, Token: opts.Token}
}

func (f *Fake) ListProjectHooks(context.Context, string) ([]gitlab.ProjectHook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.HookLists++
	if f.HookError != nil {
		return nil, f.HookError
	}
	out := []gitlab.ProjectHook{}
	for _, h := range f.Hooks {
		out = append(out, h.ProjectHook)
	}
	return out, nil
}

func (f *Fake) AddProjectHook(_ context.Context, _ string, opts gitlab.ProjectHookOptions) (*gitlab.ProjectHook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.HookError != nil {
		return nil, f.HookError
	}
	h := hookFrom(f.id(), opts)
	f.Hooks = append(f.Hooks, h)
	f.HookWrites = append(f.HookWrites, fmt.Sprintf("add %d", h.ID))
	return &h.ProjectHook, nil
}

func (f *Fake) EditProjectHook(_ context.Context, _ string, id int64, opts gitlab.ProjectHookOptions) (*gitlab.ProjectHook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.HookError != nil {
		return nil, f.HookError
	}
	for i := range f.Hooks {
		if f.Hooks[i].ID == id {
			f.Hooks[i] = hookFrom(id, opts)
			f.HookWrites = append(f.HookWrites, fmt.Sprintf("edit %d", id))
			h := f.Hooks[i].ProjectHook
			return &h, nil
		}
	}
	return nil, &gitlab.Error{StatusCode: 404, Method: "PUT", Path: "hooks"}
}

func (f *Fake) DeleteProjectHook(_ context.Context, _ string, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.HookError != nil {
		return f.HookError
	}
	for i := range f.Hooks {
		if f.Hooks[i].ID == id {
			f.Hooks = slices.Delete(f.Hooks, i, i+1)
			f.HookWrites = append(f.HookWrites, fmt.Sprintf("delete %d", id))
			return nil
		}
	}
	return &gitlab.Error{StatusCode: 404, Method: "DELETE", Path: "hooks"}
}
