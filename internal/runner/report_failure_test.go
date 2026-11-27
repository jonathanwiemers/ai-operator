package runner

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// limitedOnce answers the first note and the first label update with a 429,
// as a rate-limited GitLab does, and passes every later call on.
type limitedOnce struct {
	*gitlabfake.Fake
	notes, updates int
}

var errLimited = &gitlab.Error{StatusCode: http.StatusTooManyRequests, Method: "POST", Path: "/notes"}

func (l *limitedOnce) CreateIssueNote(ctx context.Context, project string, iid int64, body string) (*gitlab.Note, error) {
	if l.notes++; l.notes == 1 {
		return nil, errLimited
	}
	return l.Fake.CreateIssueNote(ctx, project, iid, body)
}

func (l *limitedOnce) UpdateIssue(ctx context.Context, project string, iid int64, opts gitlab.UpdateIssueOptions) (*gitlab.Issue, error) {
	if l.updates++; l.updates == 1 {
		return nil, errLimited
	}
	return l.Fake.UpdateIssue(ctx, project, iid, opts)
}

func TestReportFailureRetriesTheNoteAndTheLabelOnA429(t *testing.T) {
	defer gitlab.SetRetryBackoff(time.Millisecond)()
	e := newImplementEnv(t)
	e.configure(t, func(*aiv1.AgentProjectSpec) {}, func(s *aiv1.AgentTaskSpec) { s.Issue.WebURL = issueURL })
	e.gl.SetLabels(42, "agent::working")
	gl := &limitedOnce{Fake: e.gl}
	r := newTestRunner(t, e.tmp, e.k8s, gl, &describingEngine{}, e.task.Name)
	r.run = newRunLog(r.Log, e.task)
	out, task := &Outcome{}, e.task.DeepCopy()
	task.Spec.Issue.WebURL = issueURL
	r.reportFailure(context.Background(), gl, e.k8sProjectSpec(t), task, out, errors.New("engine exploded"), "")
	notes := e.gl.IssueNotes[42]
	if gl.notes != 2 || len(notes) == 0 || !strings.Contains(notes[len(notes)-1].Body, "The agent run failed") {
		t.Fatalf("the failure note is posted on the retry: %d calls, notes %+v", gl.notes, notes)
	}
	if out.NeedsInputURL == "" {
		t.Fatalf("the retried note's URL is recorded: %+v", out)
	}
	if labels := e.gl.Issues[42].Labels; gl.updates != 2 || !slices.Contains(labels, "agent::needs-input") || slices.Contains(labels, "agent::working") {
		t.Fatalf("the label moves on the retry: %d calls, labels %v", gl.updates, labels)
	}
}

func TestReportSpecFailureRetriesTheNoteOnA429(t *testing.T) {
	defer gitlab.SetRetryBackoff(time.Millisecond)()
	e := newImplementEnv(t)
	gl := &limitedOnce{Fake: e.gl}
	r := newTestRunner(t, e.tmp, e.k8s, gl, &describingEngine{}, e.task.Name)
	r.run = newRunLog(r.Log, e.task)
	task := e.task.DeepCopy()
	task.Status.Autopilot = &aiv1.AutopilotStatus{TrackingIssueIID: 42}
	r.reportSpecFailure(context.Background(), gl, e.k8sProjectSpec(t), task, &Outcome{}, errors.New("planner exploded"), "")
	notes := e.gl.IssueNotes[42]
	if gl.notes != 2 || len(notes) == 0 || !strings.Contains(notes[len(notes)-1].Body, "Planning failed") {
		t.Fatalf("the planning failure note is posted on the retry: %d calls, notes %+v", gl.notes, notes)
	}
}
