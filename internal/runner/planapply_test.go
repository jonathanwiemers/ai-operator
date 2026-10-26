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
	created, _, _, err := applyPlan(context.Background(), logr.Discard(), gl, "g/p", &m, spec, plan, nil, planComment{botID: gl.Bot.ID, runID: "shop-spec#1"})
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
	created, _, _, err := applyPlan(context.Background(), logr.Discard(), gl, "g/p", &m, spec, plan, nil, planComment{botID: gl.Bot.ID, runID: "shop-spec#1"})
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
		if _, _, _, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, plan, nil, planComment{botID: gl.Bot.ID, runID: "r#1"}); statusOf(err) != status || gl.creates != 1 {
			t.Fatalf("%d on create: %v after %d creates", status, err, gl.creates)
		}

		gl, m, spec, plan = planApplyEnv(t)
		gl.NoteErrors = map[int64]error{1: &gitlab.Error{StatusCode: status, Method: "POST", Path: "notes"}}
		if _, _, _, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, plan, nil, planComment{botID: gl.Bot.ID, runID: "r#1"}); statusOf(err) != status || gl.notes != 1 {
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
	if _, _, _, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, plan, nil, planComment{botID: gl.Bot.ID, runID: "shop-spec#1"}); !gitlab.IsTransient(err) {
		t.Fatalf("the run fails after the retries: %v", err)
	}
	if len(gl.Issues) != 2 || len(gl.IssueNotes[2]) != 0 {
		t.Fatalf("the failed run keeps what it created: %d issues, notes of #2 %+v", len(gl.Issues), gl.IssueNotes[2])
	}

	gl.NoteErrors = nil
	issues, _ := gl.ListIssues(ctx, "g/p", gitlab.ListIssuesOptions{Milestone: m.Title, State: "all"})
	created, updated, _, err := applyPlan(ctx, logr.Discard(), gl, "g/p", &m, spec, plan, issues, planComment{botID: gl.Bot.ID, runID: "shop-spec#2"})
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
