package gitlabfake

import (
	"context"
	"fmt"
	"slices"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// Hook is a project hook with the token GitLab never returns.
type Hook struct {
	gitlab.ProjectHook
	Token string
}

func hookFrom(id int64, opts gitlab.ProjectHookOptions) Hook {
	return Hook{ProjectHook: gitlab.ProjectHook{ID: id, URL: opts.URL, IssuesEvents: opts.IssuesEvents, NoteEvents: opts.NoteEvents,
		MergeRequestsEvents: opts.MergeRequestsEvents, PipelineEvents: opts.PipelineEvents, PushEvents: opts.PushEvents,
		MilestoneEvents: opts.MilestoneEvents, EnableSSLVerification: opts.EnableSSLVerification}, Token: opts.Token}
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

// TestProjectHook records "test <id> <trigger>" in HookWrites. A trigger in
// HookTestErrors fails with its error; HookError fails every trigger.
func (f *Fake) TestProjectHook(ctx context.Context, project string, id int64, trigger string) error {
	err := f.testProjectHook(id, trigger)
	if err == nil && f.OnHookTest != nil {
		f.OnHookTest()
	}
	return err
}

func (f *Fake) testProjectHook(id int64, trigger string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.HookError != nil {
		return f.HookError
	}
	f.HookWrites = append(f.HookWrites, fmt.Sprintf("test %d %s", id, trigger))
	if err := f.HookTestErrors[trigger]; err != nil {
		return err
	}
	for _, h := range f.Hooks {
		if h.ID == id {
			return nil
		}
	}
	return &gitlab.Error{StatusCode: 404, Method: "POST", Path: "hooks"}
}
