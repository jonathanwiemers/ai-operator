package runner

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func TestPlanRefs(t *testing.T) {
	for msg, want := range map[string][]int64{
		"build on %38":                    {38},
		"build on %38, and %12":           {38, 12},
		"(%38) and %38. again %38":        {38},
		"see %draft-1, 50% done, %38abc":  nil,
		"%7\n%0 %12;%7":                   {7, 12},
		"%99999999999999999999 overflows": nil,
	} {
		if got := planRefs(msg); !slices.Equal(got, want) {
			t.Errorf("planRefs(%q) = %v, want %v", msg, got, want)
		}
	}
}

// A named milestone without a file is fetched and written, at most
// planRefFetches per turn; the rest get the "could not be read" line.
func TestPlanRefsFetched(t *testing.T) {
	gl := &fakePlanGitLab{milestones: []gitlab.Milestone{{IID: 38, Title: "Agent language", State: "active"}}}
	for iid := int64(20); iid < 27; iid++ {
		gl.byIID = append(gl.byIID, gitlab.Milestone{IID: iid, Title: fmt.Sprintf("Old %d", iid), State: "closed"})
	}
	refs := planRefs("build on %38, and %20 %21 %404 %22 %23 %24 %25")
	dir := t.TempDir()
	infos, err := writePlanMilestones(context.Background(), dir, gl, "group/app", nil, nil, refs)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{20, 21, 404, 22, 23}; !slices.Equal(gl.fetched, want) {
		t.Errorf("fetched %v, want %v", gl.fetched, want)
	}
	if f := milestoneFile(t, dir, 20); !strings.Contains(f, "# %20 Old 20\n\nState: no autopilot (milestone closed)") {
		t.Errorf("file of %%20:\n%s", f)
	}
	for _, info := range infos {
		if info.Named != (info.IID != 38) {
			t.Errorf("%%%d: Named = %v", info.IID, info.Named)
		}
	}
	got := renderPlanRefs(planRefList(refs, infos))
	want := "## Milestones the owner names\n\n" +
		"- The owner names %38: read `.plan-context/milestones/38.md` before you answer.\n" +
		"- The owner names %20: read `.plan-context/milestones/20.md` before you answer.\n" +
		"- The owner names %21: read `.plan-context/milestones/21.md` before you answer.\n" +
		"- The owner names %404, which the project does not have or could not be read.\n" +
		"- The owner names %22: read `.plan-context/milestones/22.md` before you answer.\n" +
		"- The owner names %23: read `.plan-context/milestones/23.md` before you answer.\n" +
		"- The owner names %24, which the project does not have or could not be read.\n" +
		"- The owner names %25, which the project does not have or could not be read.\n\n"
	if got != want {
		t.Errorf("refs =\n%s\nwant\n%s", got, want)
	}
	if renderPlanRefs(nil) != "" {
		t.Error("no refs render a section")
	}
}

// The section stands before the owner's message.
func TestPlanPromptRefs(t *testing.T) {
	got := buildPlanPrompt(planTurnInput{Message: "build on %38", Branch: "main", SHA: "ab7d54a",
		Refs: []planRef{{IID: 38, Path: ".plan-context/milestones/38.md"}}})
	i, j := strings.Index(got, "## Milestones the owner names\n\n- The owner names %38: read"), strings.Index(got, "## The owner's message")
	if i < 0 || j < i {
		t.Errorf("prompt:\n%s", got)
	}
}
