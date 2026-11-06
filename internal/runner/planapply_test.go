package runner

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// timeoutErr is what the client returns when GitLab's answer timed out.
func timeoutErr(path string) error {
	return &url.Error{Op: "Post", URL: "https://gitlab.example/api/v4" + path, Err: context.DeadlineExceeded}
}

// countingGL counts the writes applyPlan makes.
type countingGL struct {
	*gitlabfake.Fake
	creates, notes int
}

func (c *countingGL) CreateIssue(ctx context.Context, p string, opts gitlab.CreateIssueOptions) (*gitlab.Issue, error) {
	c.creates++
	return c.Fake.CreateIssue(ctx, p, opts)
}

func (c *countingGL) CreateIssueNote(ctx context.Context, p string, iid int64, body string) (*gitlab.Note, error) {
	c.notes++
	return c.Fake.CreateIssueNote(ctx, p, iid, body)
}

func planApplyEnv(t *testing.T) (*countingGL, gitlab.Milestone, aiv1.AgentProjectSpec, SpecPlan) {
	t.Helper()
	t.Cleanup(gitlab.SetRetryBackoff(time.Millisecond))
	gl := &countingGL{Fake: gitlabfake.New()}
	m := gitlab.Milestone{ID: 11, IID: 3, Title: "Checkout v2"}
	gl.Milestones = []gitlab.Milestone{m}
	decide := func(choice string) []Decision {
		return []Decision{{Title: "Protocol", Question: "Which protocol?", Choice: choice, Why: "w", Alternatives: []string{"x"}}}
	}
	plan := SpecPlan{Issues: []PlannedItem{
		{Key: "ui", Title: "Checkout UI", Description: "Build the UI.", DependsOn: []string{"api"}, Decisions: decide("Vue")},
		{Key: "api", Title: "Checkout API", Description: "Build the API.", Decisions: decide("REST")},
	}}
	return gl, m, aiv1.AgentProjectSpec{}.WithDefaults(), plan
}

// assertOnePerKey checks that every planned key has one issue with one
// decisions note.
func assertOnePerKey(t *testing.T, gl *countingGL, plan SpecPlan) {
	t.Helper()
	if len(gl.Issues) != len(plan.Issues) {
		t.Fatalf("expected %d issues, got %d", len(plan.Issues), len(gl.Issues))
	}
	for _, item := range plan.Issues {
		var found []int64
		for iid, is := range gl.Issues {
			if issueKey(is.Description) == item.Key {
				found = append(found, iid)
			}
		}
		if len(found) != 1 {
			t.Fatalf("key %q: issues %v", item.Key, found)
		}
		n := 0
		for _, note := range gl.IssueNotes[found[0]] {
			if isPlanDecisions(note, gl.Bot.ID) {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("issue #%d (%s) has %d decisions notes: %+v", found[0], item.Key, n, gl.IssueNotes[found[0]])
		}
	}
}

func TestPlanApplyRetriesADecisionsNoteThatWasStored(t *testing.T) {
	gl, m, spec, plan := planApplyEnv(t)
	gl.NoteErrorsAfterWrite = map[int64]error{1: timeoutErr("/projects/g%2Fp/issues/1/notes")}
	created, _, _, err := applyPlan(context.Background(), logr.Discard(), gl, "g/p", &m, spec, nil, plan, nil, planComment{botID: gl.Bot.ID, runID: "shop-spec#1"})
	if err != nil || created != 2 {
		t.Fatalf("a timed out note is retried: created=%d err=%v", created, err)
	}
	assertOnePerKey(t, gl, plan)
	if gl.notes != 2 {
		t.Fatalf("the stored note is found, not posted again: %d posts", gl.notes)
	}
}

func TestPlanApplyFindsATimedOutCreate(t *testing.T) {
	gl, m, spec, plan := planApplyEnv(t)
	gl.CreateErrorsAfterWrite = []error{timeoutErr("/projects/g%2Fp/issues"), &gitlab.Error{StatusCode: http.StatusBadGateway, Method: "POST", Path: "issues"}}
	created, _, _, err := applyPlan(context.Background(), logr.Discard(), gl, "g/p", &m, spec, nil, plan, nil, planComment{botID: gl.Bot.ID, runID: "shop-spec#1"})
	if err != nil || created != 2 || gl.creates != 2 {
		t.Fatalf("a create applied before its timeout is found by its key: created=%d creates=%d err=%v", created, gl.creates, err)
	}
	assertOnePerKey(t, gl, plan)
	if !strings.Contains(gl.Issues[1].Description, "- #2") {
		t.Fatalf("the dependency on the found issue is linked:\n%s", gl.Issues[1].Description)
	}
}

func TestPlanApplyDoesNotRetryPermanentErrors(t *testing.T) {
	ctx := context.Background()
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity} {
		gl, m, spec, plan := planApplyEnv(t)
		gl.CreateIssueError = &gitlab.Error{StatusCode: status, Method: "POST", Path: "issues"}
		if _, _, _, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, nil, plan, nil, planComment{botID: gl.Bot.ID, runID: "r#1"}); statusOf(err) != status || gl.creates != 1 {
			t.Fatalf("%d on create: %v after %d creates", status, err, gl.creates)
		}

		gl, m, spec, plan = planApplyEnv(t)
		gl.NoteErrors = map[int64]error{1: &gitlab.Error{StatusCode: status, Method: "POST", Path: "notes"}}
		if _, _, _, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, nil, plan, nil, planComment{botID: gl.Bot.ID, runID: "r#1"}); statusOf(err) != status || gl.notes != 1 {
			t.Fatalf("%d on a note: %v after %d posts", status, err, gl.notes)
		}
	}
}

func TestPlanApplyResumesAfterAFailedRun(t *testing.T) {
	ctx := context.Background()
	gl, m, spec, plan := planApplyEnv(t)
	// The first issue's decisions are stored but time out; GitLab stays
	// unreachable for the second issue's.
	gl.NoteErrorsAfterWrite = map[int64]error{1: timeoutErr("/projects/g%2Fp/issues/1/notes")}
	gl.NoteErrors = map[int64]error{2: timeoutErr("/projects/g%2Fp/issues/2/notes")}
	if _, _, _, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, nil, plan, nil, planComment{botID: gl.Bot.ID, runID: "shop-spec#1"}); !gitlab.IsTransient(err) {
		t.Fatalf("the run fails after the retries: %v", err)
	}
	if len(gl.Issues) != 2 || len(gl.IssueNotes[2]) != 0 {
		t.Fatalf("the failed run keeps what it created: %d issues, notes of #2 %+v", len(gl.Issues), gl.IssueNotes[2])
	}

	gl.NoteErrors = nil
	issues, _ := gl.ListIssues(ctx, "g/p", gitlab.ListIssuesOptions{Milestone: m.Title, State: "all"})
	created, updated, _, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, nil, plan, issues, planComment{botID: gl.Bot.ID, runID: "shop-spec#2"})
	if err != nil || created != 0 || updated != 2 {
		t.Fatalf("the next run matches the issues by key: created=%d updated=%d err=%v", created, updated, err)
	}
	assertOnePerKey(t, gl, plan)
}

func statusOf(err error) int {
	var ge *gitlab.Error
	if errors.As(err, &ge) {
		return ge.StatusCode
	}
	return 0
}

const infraLabel = "agent-repo::group/infra"

// repoPlan is a plan whose first item changes the companion group/infra.
func repoPlan(repo string) SpecPlan {
	return SpecPlan{Issues: []PlannedItem{
		{Key: "infra", Title: "Turn it on", Description: "Config.", Repo: repo},
		{Key: "code", Title: "The feature", Description: "Code."},
	}}
}

func applyRepoPlan(t *testing.T, gl *countingGL, m gitlab.Milestone, spec aiv1.AgentProjectSpec, plan SpecPlan, run string) {
	t.Helper()
	ctx := context.Background()
	issues, _ := gl.ListIssues(ctx, "g/p", gitlab.ListIssuesOptions{Milestone: m.Title, State: "all"})
	if _, _, _, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, []string{"group/infra"}, plan, issues, planComment{botID: gl.Bot.ID, runID: run}); err != nil {
		t.Fatal(err)
	}
}

func TestPlanApplySetsTheRepoLabelOnCreateAndUpdate(t *testing.T) {
	gl, m, spec, _ := planApplyEnv(t)
	applyRepoPlan(t, gl, m, spec, repoPlan("https://gitlab.com/Group/Infra"), "s#1")
	if !gl.Issues[1].HasLabel(infraLabel) || repoLabel(gl.Issues[2].Labels) != "" {
		t.Fatalf("created: #1 %v, #2 %v", gl.Issues[1].Labels, gl.Issues[2].Labels)
	}
	if !strings.Contains(gl.Issues[1].Description, "<!-- ai-operator:plan-repo=group/infra -->") || strings.Contains(gl.Issues[2].Description, "plan-repo") {
		t.Fatalf("the description records the plan's companion:\n%s", gl.Issues[1].Description)
	}

	// The re-plan moves the companion to the second issue.
	plan := repoPlan("")
	plan.Issues[1].Repo = "group/infra"
	applyRepoPlan(t, gl, m, spec, plan, "s#2")
	if repoLabel(gl.Issues[1].Labels) != "" || strings.Contains(gl.Issues[1].Description, "plan-repo") {
		t.Fatalf("the plan-set label is removed with repo: %v\n%s", gl.Issues[1].Labels, gl.Issues[1].Description)
	}
	if !gl.Issues[2].HasLabel(infraLabel) || !strings.Contains(gl.Issues[2].Description, "plan-repo=group/infra") {
		t.Fatalf("updated: #2 %v", gl.Issues[2].Labels)
	}
	// The same plan again changes no label.
	before := len(gl.LabelEvents[2])
	applyRepoPlan(t, gl, m, spec, plan, "s#3")
	if len(gl.LabelEvents[2]) != before || !gl.Issues[2].HasLabel(infraLabel) {
		t.Fatalf("an unchanged plan touches no label: %+v", gl.LabelEvents[2])
	}
}

func TestPlanApplyKeepsAnOwnersRepoLabel(t *testing.T) {
	gl, m, spec, _ := planApplyEnv(t)
	applyRepoPlan(t, gl, m, spec, repoPlan(""), "s#1")
	owner := gitlab.User{ID: 7, Username: "alice"}
	// An owner gives #2 the companion, and re-adds the plan's label on #1
	// after the plan set it.
	gl.LabelBy(2, owner, infraLabel)
	applyRepoPlan(t, gl, m, spec, repoPlan("group/infra"), "s#2")
	if _, err := gl.UpdateIssue(context.Background(), "g/p", 1, gitlab.UpdateIssueOptions{RemoveLabels: []string{infraLabel}}); err != nil {
		t.Fatal(err)
	}
	gl.LabelBy(1, owner, infraLabel)

	applyRepoPlan(t, gl, m, spec, repoPlan(""), "s#3")
	if !gl.Issues[1].HasLabel(infraLabel) {
		t.Errorf("a label an owner added again stays: %v", gl.Issues[1].Labels)
	}
	if !gl.Issues[2].HasLabel(infraLabel) || strings.Contains(gl.Issues[2].Description, "plan-repo") {
		t.Errorf("a label an owner set stays and is not the plan's: %v\n%s", gl.Issues[2].Labels, gl.Issues[2].Description)
	}
}

func TestPlanApplyDropsAnUnknownRepo(t *testing.T) {
	gl, m, spec, _ := planApplyEnv(t)
	applyRepoPlan(t, gl, m, spec, repoPlan("group/secrets"), "s#1")
	if l := repoLabel(gl.Issues[1].Labels); l != "" || strings.Contains(gl.Issues[1].Description, "plan-repo") {
		t.Fatalf("an unknown repo gives no label: %q", l)
	}
	var plan *gitlab.Note
	for i, n := range gl.IssueNotes[1] {
		if isPlanDecisions(n, gl.Bot.ID) {
			plan = &gl.IssueNotes[1][i]
		}
	}
	if plan == nil || strings.Count(plan.Body, "group/secrets") != 1 || !strings.Contains(plan.Body, "none of the milestone's (they are `group/infra`)") {
		t.Fatalf("the plan comment names the dropped repo once: %+v", plan)
	}
	if len(gl.IssueNotes[2]) != 0 {
		t.Errorf("an item without repo and decisions gets no comment: %+v", gl.IssueNotes[2])
	}

	// Without repo lines, every repo is dropped.
	gl, m, spec, _ = planApplyEnv(t)
	if _, _, _, err := applyPlan(context.Background(), logr.Discard(), gl, "g/p", &m, spec, nil, repoPlan("group/infra"), nil, planComment{botID: gl.Bot.ID, runID: "s#1"}); err != nil {
		t.Fatal(err)
	}
	if repoLabel(gl.Issues[1].Labels) != "" || !strings.Contains(gl.IssueNotes[1][0].Body, "it has none") {
		t.Fatalf("no companion without repo lines: %v %+v", gl.Issues[1].Labels, gl.IssueNotes[1])
	}
}
