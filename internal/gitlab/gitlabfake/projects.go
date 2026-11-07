package gitlabfake

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// Projects is a fake GitLab with several projects. Every call is routed by
// its project argument to the Fake of that project, so issues, merge
// requests, pipelines and hooks of two projects never mix. Calls without a
// project (CurrentUser, FindUser) go to Default.
type Projects struct {
	// Default is the first project; unlike the others it needs no Add.
	Default *Fake

	mu     sync.Mutex
	byPath map[string]*Fake
	order  []*Fake
}

var _ gitlab.API = (*Projects)(nil)

// NewProjects returns a fake with def as its default project.
func NewProjects(def *Fake) *Projects {
	return &Projects{Default: def, byPath: map[string]*Fake{}}
}

// Add registers another project and returns its Fake. The new Fake gets a
// copy of the Default's Bot, Users and Clock taken now (change them on the
// Default afterwards and the copies do not follow), a project ID after the
// Default's, and its own ID counters that cannot collide with the others'.
func (p *Projects) Add(path string) *Fake {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byPath == nil {
		p.byPath = map[string]*Fake{}
	}
	if f, ok := p.byPath[strings.ToLower(path)]; ok {
		return f // already registered: same Fake, no second ID
	}
	d := p.Default
	d.mu.Lock()
	bot, users, clock := d.Bot, append([]gitlab.User(nil), d.Users...), d.Clock
	d.mu.Unlock()
	n := int64(len(p.order) + 1)
	f := New()
	f.Bot, f.Users, f.Clock = bot, users, clock
	f.Project = gitlab.Project{ID: d.Project.ID + n, PathWithNamespace: path, DefaultBranch: d.Project.DefaultBranch,
		HTTPURLToRepo: "https://gitlab.example/" + path + ".git"}
	f.nextID = d.nextID + n*100000
	p.byPath[strings.ToLower(path)] = f
	p.order = append(p.order, f)
	return f
}

// route finds the Fake of a project given by path, URL-encoded path or ID.
func (p *Projects) route(project string) (*Fake, error) {
	key := strings.TrimSpace(project)
	if dec, err := url.PathUnescape(key); err == nil {
		key = dec
	}
	key = strings.ToLower(strings.Trim(key, "/"))
	p.mu.Lock()
	defer p.mu.Unlock()
	all := append([]*Fake{p.Default}, p.order...)
	for _, f := range all {
		if strings.ToLower(f.Project.PathWithNamespace) == key {
			return f, nil
		}
	}
	if id, err := strconv.ParseInt(key, 10, 64); err == nil {
		for _, f := range all {
			if f.Project.ID == id {
				return f, nil
			}
		}
	}
	return nil, &gitlab.Error{StatusCode: 404, Method: "GET", Path: "projects/" + url.PathEscape(project), Body: "404 Project Not Found"}
}

func (p *Projects) CurrentUser(ctx context.Context) (*gitlab.User, error) {
	return p.Default.CurrentUser(ctx)
}

func (p *Projects) FindUser(ctx context.Context, username string) (*gitlab.User, error) {
	return p.Default.FindUser(ctx, username)
}

func (p *Projects) GetProject(ctx context.Context, project string) (*gitlab.Project, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.GetProject(ctx, project)
}

func (p *Projects) ListMilestones(ctx context.Context, project string) ([]gitlab.Milestone, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ListMilestones(ctx, project)
}

func (p *Projects) ListClosedMilestones(ctx context.Context, project string, maxPages int) ([]gitlab.Milestone, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ListClosedMilestones(ctx, project, maxPages)
}

func (p *Projects) GetMilestone(ctx context.Context, project string, id int64) (*gitlab.Milestone, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.GetMilestone(ctx, project, id)
}

func (p *Projects) UpdateMilestoneDescription(ctx context.Context, project string, id int64, description string) (*gitlab.Milestone, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.UpdateMilestoneDescription(ctx, project, id, description)
}

func (p *Projects) CloseMilestone(ctx context.Context, project string, id int64) error {
	f, err := p.route(project)
	if err != nil {
		return err
	}
	return f.CloseMilestone(ctx, project, id)
}

func (p *Projects) CreateMilestone(ctx context.Context, project string, opts gitlab.CreateMilestoneOptions) (*gitlab.Milestone, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.CreateMilestone(ctx, project, opts)
}

func (p *Projects) ListIssues(ctx context.Context, project string, opts gitlab.ListIssuesOptions) ([]gitlab.Issue, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ListIssues(ctx, project, opts)
}

func (p *Projects) GetIssue(ctx context.Context, project string, iid int64) (*gitlab.Issue, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.GetIssue(ctx, project, iid)
}

func (p *Projects) CreateIssue(ctx context.Context, project string, opts gitlab.CreateIssueOptions) (*gitlab.Issue, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.CreateIssue(ctx, project, opts)
}

func (p *Projects) UpdateIssue(ctx context.Context, project string, iid int64, opts gitlab.UpdateIssueOptions) (*gitlab.Issue, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.UpdateIssue(ctx, project, iid, opts)
}

func (p *Projects) ListIssueNotes(ctx context.Context, project string, iid int64) ([]gitlab.Note, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ListIssueNotes(ctx, project, iid)
}

func (p *Projects) ListIssueNotesNewest(ctx context.Context, project string, iid int64, n int) ([]gitlab.Note, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ListIssueNotesNewest(ctx, project, iid, n)
}

func (p *Projects) ListIssueDiscussions(ctx context.Context, project string, iid int64) ([]gitlab.Discussion, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ListIssueDiscussions(ctx, project, iid)
}

func (p *Projects) CreateIssueNote(ctx context.Context, project string, iid int64, body string) (*gitlab.Note, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.CreateIssueNote(ctx, project, iid, body)
}

func (p *Projects) ReplyIssueDiscussion(ctx context.Context, project string, iid int64, discussion, body string) (*gitlab.Note, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ReplyIssueDiscussion(ctx, project, iid, discussion, body)
}

func (p *Projects) UpdateIssueNote(ctx context.Context, project string, iid, noteID int64, body string) (*gitlab.Note, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.UpdateIssueNote(ctx, project, iid, noteID, body)
}

func (p *Projects) GetMergeRequest(ctx context.Context, project string, iid int64) (*gitlab.MergeRequest, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.GetMergeRequest(ctx, project, iid)
}

func (p *Projects) FindOpenMergeRequest(ctx context.Context, project, sourceBranch string) (*gitlab.MergeRequest, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.FindOpenMergeRequest(ctx, project, sourceBranch)
}

func (p *Projects) CreateMergeRequest(ctx context.Context, project string, opts gitlab.CreateMergeRequestOptions) (*gitlab.MergeRequest, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.CreateMergeRequest(ctx, project, opts)
}

func (p *Projects) UpdateMergeRequest(ctx context.Context, project string, iid int64, opts gitlab.UpdateMergeRequestOptions) (*gitlab.MergeRequest, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.UpdateMergeRequest(ctx, project, iid, opts)
}

func (p *Projects) MergeMergeRequest(ctx context.Context, project string, iid int64, opts gitlab.MergeOptions) (*gitlab.MergeRequest, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.MergeMergeRequest(ctx, project, iid, opts)
}

func (p *Projects) RebaseMergeRequest(ctx context.Context, project string, iid int64) error {
	f, err := p.route(project)
	if err != nil {
		return err
	}
	return f.RebaseMergeRequest(ctx, project, iid)
}

func (p *Projects) DeleteBranch(ctx context.Context, project, branch string) error {
	f, err := p.route(project)
	if err != nil {
		return err
	}
	return f.DeleteBranch(ctx, project, branch)
}

func (p *Projects) ListMergeRequestDiffs(ctx context.Context, project string, iid int64) ([]gitlab.Diff, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ListMergeRequestDiffs(ctx, project, iid)
}

func (p *Projects) ListMergeRequestVersions(ctx context.Context, project string, iid int64) ([]gitlab.MergeRequestVersion, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ListMergeRequestVersions(ctx, project, iid)
}

func (p *Projects) GetMergeRequestVersion(ctx context.Context, project string, iid, versionID int64) (*gitlab.MergeRequestVersionDetail, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.GetMergeRequestVersion(ctx, project, iid, versionID)
}

func (p *Projects) CompareCommits(ctx context.Context, project, from, to string) (*gitlab.Compare, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.CompareCommits(ctx, project, from, to)
}

func (p *Projects) ListIssueStateEvents(ctx context.Context, project string, iid int64) ([]gitlab.StateEvent, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ListIssueStateEvents(ctx, project, iid)
}

func (p *Projects) ListIssueLabelEvents(ctx context.Context, project string, iid int64) ([]gitlab.LabelEvent, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ListIssueLabelEvents(ctx, project, iid)
}

func (p *Projects) ListIssueLinks(ctx context.Context, project string, iid int64) ([]gitlab.LinkedIssue, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ListIssueLinks(ctx, project, iid)
}

func (p *Projects) CreateIssueLink(ctx context.Context, project string, iid, targetIID int64, linkType string) error {
	f, err := p.route(project)
	if err != nil {
		return err
	}
	return f.CreateIssueLink(ctx, project, iid, targetIID, linkType)
}

func (p *Projects) DeleteIssueLink(ctx context.Context, project string, iid, linkID int64) error {
	f, err := p.route(project)
	if err != nil {
		return err
	}
	return f.DeleteIssueLink(ctx, project, iid, linkID)
}

func (p *Projects) ListMergeRequestNotes(ctx context.Context, project string, iid int64) ([]gitlab.Note, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ListMergeRequestNotes(ctx, project, iid)
}

func (p *Projects) CreateMergeRequestNote(ctx context.Context, project string, iid int64, body string) (*gitlab.Note, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.CreateMergeRequestNote(ctx, project, iid, body)
}

func (p *Projects) UpdateMergeRequestNote(ctx context.Context, project string, iid, noteID int64, body string) (*gitlab.Note, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.UpdateMergeRequestNote(ctx, project, iid, noteID, body)
}

func (p *Projects) DeleteMergeRequestNote(ctx context.Context, project string, iid, noteID int64) error {
	f, err := p.route(project)
	if err != nil {
		return err
	}
	return f.DeleteMergeRequestNote(ctx, project, iid, noteID)
}

func (p *Projects) CreateMergeRequestPipeline(ctx context.Context, project string, iid int64) error {
	f, err := p.route(project)
	if err != nil {
		return err
	}
	return f.CreateMergeRequestPipeline(ctx, project, iid)
}

func (p *Projects) CreatePipeline(ctx context.Context, project, ref string) error {
	f, err := p.route(project)
	if err != nil {
		return err
	}
	return f.CreatePipeline(ctx, project, ref)
}

func (p *Projects) ListPipelines(ctx context.Context, project string, opts gitlab.ListPipelinesOptions) ([]gitlab.Pipeline, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ListPipelines(ctx, project, opts)
}

func (p *Projects) ListPipelineJobs(ctx context.Context, project string, pipelineID int64, scope string) ([]gitlab.Job, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ListPipelineJobs(ctx, project, pipelineID, scope)
}

func (p *Projects) JobTrace(ctx context.Context, project string, jobID int64) (string, error) {
	f, err := p.route(project)
	if err != nil {
		return "", err
	}
	return f.JobTrace(ctx, project, jobID)
}

func (p *Projects) RetryPipeline(ctx context.Context, project string, pipelineID int64) (*gitlab.Pipeline, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.RetryPipeline(ctx, project, pipelineID)
}

func (p *Projects) ListProjectHooks(ctx context.Context, project string) ([]gitlab.ProjectHook, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ListProjectHooks(ctx, project)
}

func (p *Projects) AddProjectHook(ctx context.Context, project string, opts gitlab.ProjectHookOptions) (*gitlab.ProjectHook, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.AddProjectHook(ctx, project, opts)
}

func (p *Projects) EditProjectHook(ctx context.Context, project string, id int64, opts gitlab.ProjectHookOptions) (*gitlab.ProjectHook, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.EditProjectHook(ctx, project, id, opts)
}

func (p *Projects) DeleteProjectHook(ctx context.Context, project string, id int64) error {
	f, err := p.route(project)
	if err != nil {
		return err
	}
	return f.DeleteProjectHook(ctx, project, id)
}

func (p *Projects) ListRepositoryTree(ctx context.Context, project, path, ref string, recursive bool) ([]gitlab.TreeEntry, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ListRepositoryTree(ctx, project, path, ref, recursive)
}

func (p *Projects) ListTags(ctx context.Context, project string, opts gitlab.ListTagsOptions) ([]gitlab.Tag, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ListTags(ctx, project, opts)
}

func (p *Projects) ListCommitRefs(ctx context.Context, project, sha, refType string) ([]gitlab.CommitRef, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.ListCommitRefs(ctx, project, sha, refType)
}
