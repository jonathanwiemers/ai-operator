package runner

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// The real client fits the plan's view of GitLab.
var _ planGitLab = gitlab.API(nil)

type fakePlanGitLab struct {
	milestones []gitlab.Milestone
	issues     []gitlab.Issue
	byIID      []gitlab.Milestone // only GetMilestoneByIID returns them
	fetched    []int64            // the IIDs GetMilestoneByIID was asked for
}

func (f *fakePlanGitLab) GetProject(context.Context, string) (*gitlab.Project, error) {
	return &gitlab.Project{PathWithNamespace: "group/app", DefaultBranch: "main"}, nil
}

func (f *fakePlanGitLab) ListMilestones(context.Context, string) ([]gitlab.Milestone, error) {
	return slices.DeleteFunc(slices.Clone(f.milestones), func(m gitlab.Milestone) bool { return m.State == "closed" }), nil
}

func (f *fakePlanGitLab) ListClosedMilestones(context.Context, string, int) ([]gitlab.Milestone, error) {
	return slices.DeleteFunc(slices.Clone(f.milestones), func(m gitlab.Milestone) bool { return m.State != "closed" }), nil
}

// GetMilestoneByIID finds one of milestones or byIID, which no listing
// returns, and records the IID.
func (f *fakePlanGitLab) GetMilestoneByIID(_ context.Context, _ string, iid int64) (*gitlab.Milestone, error) {
	f.fetched = append(f.fetched, iid)
	for _, m := range append(slices.Clone(f.milestones), f.byIID...) {
		if m.IID == iid {
			return &m, nil
		}
	}
	return nil, &gitlab.Error{StatusCode: 404}
}

func (f *fakePlanGitLab) ListIssues(context.Context, string, gitlab.ListIssuesOptions) ([]gitlab.Issue, error) {
	return slices.Clone(f.issues), nil
}

func (f *fakePlanGitLab) GetMergeRequest(context.Context, string, int64) (*gitlab.MergeRequest, error) {
	return nil, &gitlab.Error{StatusCode: 404}
}

// The plan pod never writes to GitLab: its client has reads only.
func TestPlanGitLabIsReadOnly(t *testing.T) {
	typ := reflect.TypeFor[planGitLab]()
	if typ.NumMethod() == 0 {
		t.Fatal("planGitLab has no methods")
	}
	for i := range typ.NumMethod() {
		m := typ.Method(i)
		if !strings.HasPrefix(m.Name, "Get") && !strings.HasPrefix(m.Name, "List") {
			t.Errorf("planGitLab.%s is not a read (Get or List)", m.Name)
		}
	}
}

// The plan pod never writes to the repository: no edit tools, no
// unrestricted shell, no git command that changes or publishes a branch.
func TestPlanToolsAreReadOnly(t *testing.T) {
	for _, tool := range planTools {
		switch tool {
		case "Edit", "MultiEdit", "Write", "NotebookEdit", "Bash", "Bash(*)", "Bash(:*)":
			t.Errorf("planTools has %q", tool)
		}
		if strings.HasPrefix(tool, "Bash") && (!strings.HasPrefix(tool, "Bash(") || !strings.HasSuffix(tool, ":*)") || len(tool) <= len("Bash(:*)")) {
			t.Errorf("planTools has %q, not a single command prefix", tool)
		}
		for _, bad := range []string{"git push", "git commit", "git checkout", "git grep", "find"} {
			if strings.Contains(tool, bad) {
				t.Errorf("planTools has %q", tool)
			}
		}
	}
	for _, want := range []string{"Read", "Grep", "Glob", "Bash(cat:*)", "Bash(git ls-files:*)"} {
		if !slices.Contains(planTools, want) {
			t.Errorf("planTools lacks %q", want)
		}
	}
	for _, want := range []string{"Bash(git push:*)", "Edit", "Write", "NotebookEdit"} {
		if !slices.Contains(planDenied, want) {
			t.Errorf("planDenied lacks %q", want)
		}
	}
	if !slices.Equal(planTools[:len(readOnlyTools)], readOnlyTools) {
		t.Errorf("planTools = %v, want readOnlyTools first", planTools)
	}
}

// planContextGitLab has open milestones, 7 closed ones and two issues.
func planContextGitLab() *fakePlanGitLab {
	m3 := gitlab.Milestone{IID: 3, Title: "Desk: plans", Description: "/agent spec\n/agent autopilot\n\n# Desk: plans\n\n## Goal\n  Plan milestones.\nMore."}
	gl := &fakePlanGitLab{milestones: []gitlab.Milestone{m3, {IID: 4, Title: "Later"}}}
	for iid := int64(1); iid <= 7; iid++ {
		gl.milestones = append(gl.milestones, gitlab.Milestone{IID: 100 + iid%7, Title: fmt.Sprintf("Old %d", 100+iid%7), State: "closed"})
	}
	gl.issues = []gitlab.Issue{
		{IID: 8, Title: "Plan CRD", Labels: []string{"agent::ready", "go"}, Milestone: &m3},
		{IID: 9, Title: "Loose end"},
	}
	return gl
}

// TestPlanContext pins the index of the milestone files byte for byte
// (golden): open ones in queue order, then the others by IID; closed ones
// newest first; a milestone fetched only because the owner names it is
// left out.
func TestPlanContext(t *testing.T) {
	gl := planContextGitLab()
	gl.milestones = append(gl.milestones, gitlab.Milestone{IID: 5, Title: "Next"}, gitlab.Milestone{IID: 2, Title: "Side"})
	gl.byIID = []gitlab.Milestone{{IID: 90, Title: "Named", State: "closed"}}
	tasks := []aiv1.AgentTask{
		planSpecTask(3, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: planItems(7, 3)}),
		planSpecTask(5, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}),
	}
	infos, err := writePlanMilestones(context.Background(), t.TempDir(), gl, "group/app", tasks, nil, []int64{90})
	if err != nil || len(infos) != 10 {
		t.Fatalf("%d infos, %v", len(infos), err)
	}
	got, err := planContext(context.Background(), gl, "group/app", infos)
	if err != nil {
		t.Fatal(err)
	}
	want := `# GitLab project group/app (default branch main)

## Open milestones

- %3 Desk: plans · running, 3 of 7 issues merged · 1st of 2 in the autopilot queue · Goal: Plan milestones. · .plan-context/milestones/3.md
- %5 Next · waiting for its turn · 2nd of 2 in the autopilot queue · .plan-context/milestones/5.md
- %2 Side · no autopilot · not in the autopilot queue · .plan-context/milestones/2.md
- %4 Later · no autopilot · not in the autopilot queue · .plan-context/milestones/4.md

## Newest closed milestones

- %106 Old 106 · no autopilot · not in the autopilot queue · .plan-context/milestones/106.md
- %105 Old 105 · no autopilot · not in the autopilot queue · .plan-context/milestones/105.md
- %104 Old 104 · no autopilot · not in the autopilot queue · .plan-context/milestones/104.md
- %103 Old 103 · no autopilot · not in the autopilot queue · .plan-context/milestones/103.md
- %102 Old 102 · no autopilot · not in the autopilot queue · .plan-context/milestones/102.md

## Open issues, newest first

- #9 Loose end (milestone: none; labels: )
- #8 Plan CRD (milestone: Desk: plans; labels: agent::ready, go)
`
	if got != want {
		t.Errorf("planContext =\n%s\nwant\n%s", got, want)
	}
}

// Without milestone files the index has numbers and titles only.
func TestPlanContextWithoutFiles(t *testing.T) {
	got, err := planContext(context.Background(), planContextGitLab(), "group/app", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `# GitLab project group/app (default branch main)

## Open milestones

- %3 Desk: plans
- %4 Later

## Newest closed milestones

- %106 Old 106
- %105 Old 105
- %104 Old 104
- %103 Old 103
- %102 Old 102

## Open issues, newest first

- #9 Loose end (milestone: none; labels: )
- #8 Plan CRD (milestone: Desk: plans; labels: agent::ready, go)
`
	if got != want {
		t.Errorf("planContext =\n%s\nwant\n%s", got, want)
	}
}

func TestPlanContextBounds(t *testing.T) {
	gl := &fakePlanGitLab{}
	for iid := int64(1); iid <= 60; iid++ {
		gl.issues = append(gl.issues, gitlab.Issue{IID: iid, Title: fmt.Sprintf("Issue %d", iid)})
	}
	got, err := planContext(context.Background(), gl, "group/app", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "- #11 Issue 11 (") || strings.Contains(got, "- #10 Issue 10 (") || !strings.HasSuffix(got, "- … and 10 more\n") {
		t.Errorf("planContext does not keep the 50 newest issues:\n%s", got)
	}

	for i := range gl.issues {
		gl.issues[i].Title = strings.Repeat("ä", 400)
	}
	got, err = planContext(context.Background(), gl, "group/app", nil)
	if err != nil {
		t.Fatal(err)
	}
	const note = "\n\n(cut at 16 KiB)\n"
	if !strings.HasSuffix(got, note) || len(got) > planContextMax+len(note) || !strings.Contains(got, "#60 ") {
		t.Errorf("planContext is %d bytes, ends %q", len(got), got[len(got)-40:])
	}
	if !strings.Contains(got, "ä") || strings.ContainsRune(got, '�') || !strings.HasPrefix(got, "# GitLab project") {
		t.Error("planContext cut a character in half or lost its head")
	}
}

func TestPlanContextError(t *testing.T) {
	gl := &failingPlanGitLab{}
	if _, err := planContext(context.Background(), gl, "group/app", nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("planContext error = %v, want the ListIssues error", err)
	}
}

type failingPlanGitLab struct{ fakePlanGitLab }

func (f *failingPlanGitLab) ListIssues(context.Context, string, gitlab.ListIssuesOptions) ([]gitlab.Issue, error) {
	return nil, fmt.Errorf("boom")
}
