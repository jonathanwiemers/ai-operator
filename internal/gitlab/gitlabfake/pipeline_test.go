package gitlabfake

import (
	"context"
	"slices"
	"testing"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func TestCreatePipelineWithVariables(t *testing.T) {
	ctx := context.Background()
	f := New()
	f.BranchHeads = map[string]string{"main": "abc"}
	vars := []gitlab.PipelineVariable{{Key: "AGENT_RELEASE", Value: "true"}, {Key: "AGENT_RELEASE_NOTES", Value: "why"}}
	p, err := f.CreatePipeline(ctx, "group/app", "main", vars...)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != "created" || p.SHA != "abc" || p.Ref != "main" || p.WebURL == "" {
		t.Fatalf("pipeline = %+v", p)
	}
	if len(f.CreatedPipelines) != 1 || f.CreatedPipelines[0].ID != p.ID || !slices.Equal(f.CreatedPipelines[0].Variables, vars) {
		t.Fatalf("CreatedPipelines = %+v", f.CreatedPipelines)
	}
	if !slices.Equal(f.BranchPipelines, []string{"main"}) {
		t.Fatalf("BranchPipelines = %v", f.BranchPipelines)
	}
	f.SetPipelineStatus(p.ID, "success")
	got, err := f.GetPipeline(ctx, "group/app", p.ID)
	if err != nil || got.Status != "success" {
		t.Fatalf("GetPipeline = %+v, %v", got, err)
	}
	if l, _ := f.ListPipelines(ctx, "group/app", gitlab.ListPipelinesOptions{Ref: "main"}); len(l) != 1 {
		t.Fatalf("ListPipelines = %+v", l)
	}
	if _, err := f.GetPipeline(ctx, "group/app", 9999); !gitlab.IsNotFound(err) {
		t.Fatalf("unknown pipeline err = %v, want 404", err)
	}
	f.NoPipelines = true
	if _, err := f.CreatePipeline(ctx, "group/app", "main"); err == nil {
		t.Fatal("NoPipelines should refuse")
	}
}
