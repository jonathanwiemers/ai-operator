package gitlab

import "time"

// MergeRequestVersion is one pushed head of a merge request; GitLab keeps
// the diffs and commits of every version.
type MergeRequestVersion struct {
	ID             int64     `json:"id"`
	HeadCommitSHA  string    `json:"head_commit_sha"`
	BaseCommitSHA  string    `json:"base_commit_sha"`
	StartCommitSHA string    `json:"start_commit_sha"`
	CreatedAt      time.Time `json:"created_at"`
	State          string    `json:"state"`
}

// MergeRequestVersionDetail is a version with its commits and diffs.
type MergeRequestVersionDetail struct {
	MergeRequestVersion
	Commits []Commit `json:"commits"`
	Diffs   []Diff   `json:"diffs"`
}

// Compare is the result of comparing two refs of a repository.
type Compare struct {
	// Commits are oldest first: the last one is the head of to.
	Commits []Commit `json:"commits"`
	Diffs   []Diff   `json:"diffs"`
}

// Tag is a tag of a project's repository with the commit it points to.
type Tag struct {
	Name   string    `json:"name"`
	Commit TagCommit `json:"commit"`
}

// TagCommit is the commit a tag points to.
type TagCommit struct {
	ID string `json:"id"`
	// ParentIDs: more than one makes it a merge commit.
	ParentIDs []string   `json:"parent_ids"`
	CreatedAt *time.Time `json:"created_at"`
}

// ListTagsOptions selects tags; they come newest first (by update).
type ListTagsOptions struct {
	// Limit caps the number of results (default: one page of 20).
	Limit int
}

// CommitRef is a branch or tag that contains a commit.
type CommitRef struct {
	// Type is "branch" or "tag".
	Type string `json:"type"`
	Name string `json:"name"`
}

// ListIssuesOptions filters issues.
type ListIssuesOptions struct {
	// Labels the issue must all carry.
	Labels []string
	// State: opened, closed or all. Defaults to opened.
	State string
	// Milestone title.
	Milestone string
	// UpdatedAfter only returns issues updated after this time.
	UpdatedAfter *time.Time
	// MaxPages caps the pages of 100 issues read, so the requests; 0 reads
	// up to the client's limit.
	MaxPages int
}

// CreateIssueOptions creates an issue.
type CreateIssueOptions struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// Labels is a comma separated list.
	Labels      string `json:"labels,omitempty"`
	MilestoneID int64  `json:"milestone_id,omitempty"`
}

// CreateMilestoneOptions creates a milestone. Dates are YYYY-MM-DD and
// optional.
type CreateMilestoneOptions struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	StartDate   string `json:"start_date,omitempty"`
	DueDate     string `json:"due_date,omitempty"`
}

// UpdateIssueOptions updates an issue. Empty fields are left untouched.
type UpdateIssueOptions struct {
	Title        string   `json:"title,omitempty"`
	Description  string   `json:"description,omitempty"`
	AddLabels    []string `json:"-"`
	RemoveLabels []string `json:"-"`
	StateEvent   string   `json:"state_event,omitempty"`
	// AssigneeIDs replaces all assignees of the issue.
	AssigneeIDs []int64 `json:"-"`
	// MilestoneID moves the issue into a milestone (by ID, not IID); 0
	// leaves it.
	MilestoneID int64 `json:"-"`
}

// CreateMergeRequestOptions creates a merge request.
type CreateMergeRequestOptions struct {
	SourceBranch       string `json:"source_branch"`
	TargetBranch       string `json:"target_branch"`
	Title              string `json:"title"`
	Description        string `json:"description,omitempty"`
	RemoveSourceBranch bool   `json:"remove_source_branch,omitempty"`
	MilestoneID        int64  `json:"milestone_id,omitempty"`
	// Labels is a comma separated list; GitLab creates missing labels.
	Labels string `json:"labels,omitempty"`
}

// UpdateMergeRequestOptions updates a merge request. Empty fields are left
// untouched.
type UpdateMergeRequestOptions struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	// ReviewerIDs replaces all reviewers of the merge request.
	ReviewerIDs []int64 `json:"reviewer_ids,omitempty"`
	// StateEvent is "close" or "reopen"; empty leaves the state.
	StateEvent string `json:"state_event,omitempty"`
}

// MergeOptions merges a merge request.
type MergeOptions struct {
	// SHA must match the head of the source branch, so only the checked
	// commit is merged.
	SHA                      string `json:"sha,omitempty"`
	ShouldRemoveSourceBranch bool   `json:"should_remove_source_branch,omitempty"`
	// SquashCommitMessage is the message of the squash commit (GitLab
	// ignores it when the merge does not squash).
	SquashCommitMessage string `json:"squash_commit_message,omitempty"`
}

// ProjectHook is a project webhook. GitLab never returns its token.
type ProjectHook struct {
	ID                    int64  `json:"id"`
	URL                   string `json:"url"`
	IssuesEvents          bool   `json:"issues_events"`
	NoteEvents            bool   `json:"note_events"`
	MergeRequestsEvents   bool   `json:"merge_requests_events"`
	PipelineEvents        bool   `json:"pipeline_events"`
	PushEvents            bool   `json:"push_events"`
	MilestoneEvents       bool   `json:"milestone_events"`
	EnableSSLVerification bool   `json:"enable_ssl_verification"`
}

// ProjectHookOptions creates or edits a project hook. Every field is sent, so
// an edit leaves the hook with exactly these settings.
type ProjectHookOptions struct {
	URL                   string `json:"url"`
	Token                 string `json:"token"`
	IssuesEvents          bool   `json:"issues_events"`
	NoteEvents            bool   `json:"note_events"`
	MergeRequestsEvents   bool   `json:"merge_requests_events"`
	PipelineEvents        bool   `json:"pipeline_events"`
	PushEvents            bool   `json:"push_events"`
	MilestoneEvents       bool   `json:"milestone_events"`
	EnableSSLVerification bool   `json:"enable_ssl_verification"`
}
