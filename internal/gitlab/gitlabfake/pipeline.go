package gitlabfake

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// CreatePipeline records the branch a pipeline was started for and adds a
// pipeline with status created, whose SHA is BranchHeads[ref]; with
// NoPipelines it is refused like a project without CI for it.
func (f *Fake) CreatePipeline(_ context.Context, _ string, ref string, vars ...gitlab.PipelineVariable) (*gitlab.Pipeline, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.NoPipelines {
		return nil, &gitlab.Error{StatusCode: 400, Method: "POST", Path: "pipeline", Body: `{"message":{"base":["Pipeline filtered out by workflow rules."]}}`}
	}
	f.BranchPipelines = append(f.BranchPipelines, ref)
	id := f.id()
	p := gitlab.Pipeline{ID: id, SHA: f.BranchHeads[ref], Ref: ref, Status: "created",
		WebURL: fmt.Sprintf("https://gitlab.example/group/app/-/pipelines/%d", id)}
	f.Pipelines = append(f.Pipelines, p)
	f.CreatedPipelines = append(f.CreatedPipelines, CreatedPipeline{ID: id, Ref: ref, Variables: slices.Clone(vars)})
	return &p, nil
}

// CreateRelease records the release and tags ref with it (the tag contains
// ref). An existing tag is a 409.
func (f *Fake) CreateRelease(_ context.Context, _ string, tag, ref, description string) error {
	f.mu.Lock()
	if slices.ContainsFunc(f.Tags, func(t gitlab.Tag) bool { return t.Name == tag }) {
		f.mu.Unlock()
		return &gitlab.Error{StatusCode: 409, Method: "POST", Path: "releases", Body: `{"message":"Release already exists"}`}
	}
	rel := CreatedRelease{Tag: tag, Ref: ref, Description: description}
	created, tagCI := f.Clock, !f.NoTagPipelines
	// The tag contains what ref contains: an empty compare ref..sha.
	var contains []string
	for k, c := range f.Compares {
		if from, sha, ok := strings.Cut(k, ".."); ok && from == ref && len(c.Commits) == 0 {
			contains = append(contains, sha)
		}
	}
	slices.Sort(contains)
	f.mu.Unlock()
	f.AddTag(tag, gitlab.TagCommit{ID: ref, ParentIDs: []string{"p"}, CreatedAt: &created}, contains...)
	if tagCI {
		rel.PipelineID = f.AddPipeline(ref, tag, "running")
	}
	f.mu.Lock()
	f.Releases = append(f.Releases, rel)
	f.mu.Unlock()
	return nil
}

// GetPipeline returns a pipeline, 404 for an unknown ID.
func (f *Fake) GetPipeline(_ context.Context, _ string, id int64) (*gitlab.Pipeline, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.Pipelines {
		if p.ID == id {
			return &p, nil
		}
	}
	return nil, &gitlab.Error{StatusCode: 404, Method: "GET", Path: "pipelines"}
}

func (f *Fake) CreateMergeRequestPipeline(_ context.Context, _ string, iid int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.MRs[iid]; !ok {
		return &gitlab.Error{StatusCode: 404, Method: "POST", Path: "pipelines"}
	}
	if f.MRPipelineError != nil {
		return f.MRPipelineError
	}
	if f.NoMRPipelines {
		return &gitlab.Error{StatusCode: 400, Method: "POST", Path: "pipelines", Body: `{"message":{"base":["No stages / jobs for this pipeline."]}}`}
	}
	f.MRPipelines = append(f.MRPipelines, iid)
	return nil
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
	f.TraceReads = append(f.TraceReads, jobID)
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
