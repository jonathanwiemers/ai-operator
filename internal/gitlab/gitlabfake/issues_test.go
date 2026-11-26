package gitlabfake

import (
	"context"
	"errors"
	"testing"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func TestListIssuesNoMilestone(t *testing.T) {
	ctx := context.Background()
	f := New()
	f.Issues[1] = &gitlab.Issue{IID: 1, State: "opened"}
	f.Issues[2] = &gitlab.Issue{IID: 2, State: "opened", Milestone: &gitlab.Milestone{Title: "M1"}}
	f.Issues[3] = &gitlab.Issue{IID: 3, State: "opened"}
	f.Issues[4] = &gitlab.Issue{IID: 4, State: "closed"}

	got, err := f.ListIssues(ctx, "group/app", gitlab.ListIssuesOptions{NoMilestone: true, NewestFirst: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].IID != 3 || got[1].IID != 1 {
		t.Fatalf("NoMilestone, newest first = %+v; want #3, #1", got)
	}
	if _, err := f.ListIssues(ctx, "group/app", gitlab.ListIssuesOptions{NoMilestone: true, Milestone: "M1"}); !errors.Is(err, gitlab.ErrMilestoneFilter) {
		t.Fatalf("both filters: err = %v", err)
	}
}

func TestListIssuesMaxPages(t *testing.T) {
	f := New()
	for i := int64(1); i <= 150; i++ {
		f.Issues[i] = &gitlab.Issue{IID: i, State: "opened"}
	}
	got, err := f.ListIssues(context.Background(), "group/app", gitlab.ListIssuesOptions{MaxPages: 1, NewestFirst: true})
	if err != nil || len(got) != 100 || got[0].IID != 150 {
		t.Fatalf("MaxPages 1 = %d issues (first #%d), %v", len(got), got[0].IID, err)
	}
}
