package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// savedState is the runner's state as saved; a load error fails the test.
func savedState(t *testing.T, r *Runner) state {
	t.Helper()
	st, err := r.loadState()
	if err != nil {
		t.Error(err)
	}
	return st
}

func TestLoadStateMissingIsEmpty(t *testing.T) {
	r := &Runner{StateDir: filepath.Join(t.TempDir(), "runner"), Log: logr.Discard()}
	st, err := r.loadState()
	if err != nil || st.LastIssueNoteID != 0 || st.Sessions != nil {
		t.Fatalf("a missing state file is an empty state: %+v, %v", st, err)
	}
}

func TestLoadStateCorruptIsAnError(t *testing.T) {
	r := &Runner{StateDir: t.TempDir(), Log: logr.Discard()}
	if err := os.WriteFile(r.stateFile(), []byte(`{"lastIssueNoteID": 7`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.loadState(); err == nil || !strings.Contains(err.Error(), r.stateFile()) {
		t.Fatalf("a corrupt state file is an error naming the file, got %v", err)
	}
}

func TestImplementWithCorruptStateFails(t *testing.T) {
	e := newImplementEnv(t)
	engine := &decisionEngine{}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if err := os.MkdirAll(r.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.stateFile(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _ = r.Step(context.Background())

	if len(engine.calls) != 0 {
		t.Fatalf("the agent must not run on a broken state: %d calls", len(engine.calls))
	}
	for _, n := range e.gl.IssueNotes[42] {
		if strings.HasPrefix(n.Body, decisionsHeader) || strings.Contains(n.Body, handoffBegin) {
			t.Fatalf("nothing is posted on a broken state: %q", n.Body)
		}
	}
	var task aiv1.AgentTask
	_ = e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	if run := task.Status.Run; run.LastResult != aiv1.RunResultFailed || !strings.Contains(run.Summary, "parsing the runner state") {
		t.Fatalf("the run fails with the state error: %+v", run)
	}
}

// breakStateAfterDecisions makes the runner's state unwritable once the
// decisions are posted.
type breakStateAfterDecisions struct {
	*gitlabfake.Fake
	stateFile string
}

func (b *breakStateAfterDecisions) CreateIssueNote(ctx context.Context, project string, iid int64, body string) (*gitlab.Note, error) {
	n, err := b.Fake.CreateIssueNote(ctx, project, iid, body)
	if err == nil && strings.HasPrefix(body, decisionsHeader) {
		// A directory where the save writes its temporary file.
		_ = os.Mkdir(b.stateFile+".tmp", 0o755)
	}
	return n, err
}

func TestImplementFailsWhenTheStateIsNotSavedAfterDecisions(t *testing.T) {
	e := newImplementEnv(t)
	gl := &breakStateAfterDecisions{Fake: e.gl, stateFile: filepath.Join(e.tmp, "runner", "state.json")}
	r := newTestRunner(t, e.tmp, e.k8s, gl, &decisionEngine{}, e.task.Name)
	_, _ = r.Step(context.Background())

	posted := false
	for _, n := range e.gl.IssueNotes[42] {
		posted = posted || strings.HasPrefix(n.Body, decisionsHeader)
	}
	var task aiv1.AgentTask
	_ = e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
	if run := task.Status.Run; !posted || run.LastResult != aiv1.RunResultFailed || !strings.Contains(run.Summary, "state.json") {
		t.Fatalf("the run fails when the state after the decisions is not saved (posted=%v): %+v", posted, run)
	}
}
