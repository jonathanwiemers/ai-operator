package gitlab

import "time"

// PipelineVariable is a variable passed to a new pipeline.
type PipelineVariable struct{ Key, Value string }

// Pipeline is a CI pipeline.
type Pipeline struct {
	ID     int64  `json:"id"`
	SHA    string `json:"sha"`
	Ref    string `json:"ref"`
	Status string `json:"status"`
	WebURL string `json:"web_url"`
}

// Job is a CI job.
type Job struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Stage  string `json:"stage"`
	Status string `json:"status"`
	WebURL string `json:"web_url"`
	// AllowFailure jobs do not fail the pipeline.
	AllowFailure bool `json:"allow_failure"`
}

// ListPipelinesOptions filters pipelines; results are newest first.
type ListPipelinesOptions struct {
	SHA string
	Ref string
	// Scope: running, pending, finished, branches, tags.
	Scope string
	// Limit caps the number of results (default: one page of 20).
	Limit int
}

// User is a GitLab user.
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	// Name is the display name.
	Name string `json:"name"`
	// CommitEmail is the email the user commits with in GitLab; empty when
	// GitLab does not return it.
	CommitEmail string `json:"commit_email"`
}

// Project is a GitLab project.
type Project struct {
	ID                int64  `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	DefaultBranch     string `json:"default_branch"`
	HTTPURLToRepo     string `json:"http_url_to_repo"`
	WebURL            string `json:"web_url"`
}

// Milestone is a GitLab project milestone.
type Milestone struct {
	ID          int64      `json:"id"`
	IID         int64      `json:"iid"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"`
	WebURL      string     `json:"web_url"`
	UpdatedAt   *time.Time `json:"updated_at"`
	// DueDate is YYYY-MM-DD, empty without one.
	DueDate string `json:"due_date"`
}

// Issue is a GitLab issue.
type Issue struct {
	ID          int64      `json:"id"`
	IID         int64      `json:"iid"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"`
	Labels      []string   `json:"labels"`
	Milestone   *Milestone `json:"milestone"`
	Assignees   []User     `json:"assignees"`
	WebURL      string     `json:"web_url"`
	CreatedAt   *time.Time `json:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at"`
	Author      User       `json:"author"`
}

// HasLabel reports whether the issue carries the label.
func (i *Issue) HasLabel(label string) bool {
	for _, l := range i.Labels {
		if l == label {
			return true
		}
	}
	return false
}

// Note is a comment on an issue or merge request.
type Note struct {
	ID        int64      `json:"id"`
	Body      string     `json:"body"`
	System    bool       `json:"system"`
	Author    User       `json:"author"`
	CreatedAt *time.Time `json:"created_at"`
}

// Discussion is a thread of comments; its first note started it.
type Discussion struct {
	ID    string `json:"id"`
	Notes []Note `json:"notes"`
}

// MergeRequest is a GitLab merge request.
type MergeRequest struct {
	ID           int64  `json:"id"`
	IID          int64  `json:"iid"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	State        string `json:"state"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	// SourceProjectID differs from ProjectID for an MR from a fork.
	ProjectID       int64      `json:"project_id"`
	SourceProjectID int64      `json:"source_project_id"`
	Milestone       *Milestone `json:"milestone"`
	WebURL          string     `json:"web_url"`
	UpdatedAt       *time.Time `json:"updated_at"`
	Reviewers       []User     `json:"reviewers"`
	Labels          []string   `json:"labels"`
	// SHA is the head commit of the source branch.
	SHA   string `json:"sha"`
	Draft bool   `json:"draft"`
	// DetailedMergeStatus is GitLab's view of whether the MR can be merged:
	// mergeable, need_rebase, conflict, draft_status, ci_still_running, …
	DetailedMergeStatus string `json:"detailed_merge_status"`
	HasConflicts        bool   `json:"has_conflicts"`
	// HeadPipeline is the latest pipeline of the MR, also a merged-results
	// or merge request pipeline whose SHA is not the head's.
	HeadPipeline     *Pipeline `json:"head_pipeline"`
	MergeCommitSHA   string    `json:"merge_commit_sha"`
	SquashCommitSHA  string    `json:"squash_commit_sha"`
	RebaseInProgress bool      `json:"rebase_in_progress"`
	MergeError       string    `json:"merge_error"`
	// DiffRefs are the commits the MR's diff is between; only a single MR
	// GET returns them.
	DiffRefs *DiffRefs `json:"diff_refs"`
}

// DiffRefs of a merge request: BaseSHA is the commit of the target branch
// its change is based on.
type DiffRefs struct {
	BaseSHA  string `json:"base_sha"`
	HeadSHA  string `json:"head_sha"`
	StartSHA string `json:"start_sha"`
}

// Issue link types. blocks and is_blocked_by need GitLab Premium; on Free
// only relates_to exists.
const (
	LinkRelatesTo   = "relates_to"
	LinkBlocks      = "blocks"
	LinkIsBlockedBy = "is_blocked_by"
)

// LinkedIssue is an issue linked to another one, as the links API lists it:
// LinkType is seen from the issue whose links were listed.
type LinkedIssue struct {
	IID         int64  `json:"iid"`
	ProjectID   int64  `json:"project_id"`
	State       string `json:"state"`
	Title       string `json:"title"`
	IssueLinkID int64  `json:"issue_link_id"`
	LinkType    string `json:"link_type"`
	References  struct {
		Full string `json:"full"`
	} `json:"references"`
}

// StateEvent is a state change of an issue: closed, reopened.
type StateEvent struct {
	ID    int64  `json:"id"`
	State string `json:"state"`
	// SourceMergeRequest is the MR whose merge closed the issue.
	SourceMergeRequest *MergeRequestRef `json:"source_merge_request"`
}

// LabelEvent is a label added to or removed from an issue, and by whom.
type LabelEvent struct {
	ID   int64 `json:"id"`
	User User  `json:"user"`
	// Label is nil for a label deleted since.
	Label *struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"label"`
	// Action is "add" or "remove".
	Action    string     `json:"action"`
	CreatedAt *time.Time `json:"created_at"`
}

// LabelName is the event's label, "" for a deleted label.
func (e LabelEvent) LabelName() string {
	if e.Label == nil {
		return ""
	}
	return e.Label.Name
}

// TreeEntry is a file ("blob") or directory ("tree") of a repository tree.
type TreeEntry struct {
	// Path is relative to the repository root.
	Path string `json:"path"`
	Name string `json:"name"`
	// Type is "blob", "tree" or "commit" (a submodule).
	Type string `json:"type"`
}

// MergeRequestRef identifies a merge request, possibly of another project.
type MergeRequestRef struct {
	IID       int64 `json:"iid"`
	ProjectID int64 `json:"project_id"`
}

// Diff is the change of one file in a merge request.
type Diff struct {
	OldPath     string `json:"old_path"`
	NewPath     string `json:"new_path"`
	Diff        string `json:"diff"`
	NewFile     bool   `json:"new_file"`
	RenamedFile bool   `json:"renamed_file"`
	DeletedFile bool   `json:"deleted_file"`
	// Collapsed and TooLarge diffs come without their content.
	Collapsed bool `json:"collapsed"`
	TooLarge  bool `json:"too_large"`
}

// Branch is a branch of a project's repository.
type Branch struct {
	Name   string `json:"name"`
	Commit Commit `json:"commit"`
}

// Commit is a commit of a merge request version or a repository compare.
type Commit struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	AuthorName     string `json:"author_name"`
	AuthorEmail    string `json:"author_email"`
	CommitterEmail string `json:"committer_email"`
	// ParentIDs, the first parent first.
	ParentIDs []string `json:"parent_ids,omitempty"`
	// CommittedDate is when the commit was made: for a merge commit, when
	// it was merged.
	CommittedDate *time.Time `json:"committed_date,omitempty"`
}

// Upload is GitLab's answer to a file upload: the link to put into text.
type Upload struct {
	Alt      string `json:"alt"`
	URL      string `json:"url"`
	FullPath string `json:"full_path"`
	Markdown string `json:"markdown"`
}
