package runner

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/conventional"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

var testTypes = aiv1.WorkflowSpec{}.WithDefaults().CommitTypes

func TestCommitMessage(t *testing.T) {
	issue := &gitlab.Issue{IID: 42, Title: "Add PayPal button"}
	for _, c := range []struct {
		name, msg string
		types     []string
		want      string
		rewritten bool
	}{
		{"off: valid", "feat(ui): add the button", nil, "feat(ui): add the button\n\nRefs #42", false},
		{"off: not conventional", "Add thing", nil, "Add thing\n\nRefs #42", false},
		{"off: empty takes the issue title", "  ", nil, "Add PayPal button\n\nRefs #42", false},
		{"off: multi-line", "Add thing\n\nBecause.", nil, "Add thing\n\nBecause.\n\nRefs #42", false},
		{"on: valid", " feat(ui): add the button \n", testTypes, "feat(ui): add the button\n\nRefs #42", false},
		{"on: valid multi-line", "fix(runner): keep the lock\n\nIt was lost.", testTypes, "fix(runner): keep the lock\n\nIt was lost.\n\nRefs #42", false},
		{"on: not conventional", "Add thing", testTypes, "chore(agent): add thing\n\nRefs #42", true},
		{"on: unknown type", "wip: thing", testTypes, "chore(agent): wip: thing\n\nRefs #42", true},
		{"on: empty takes the issue title", "", testTypes, "chore(agent): add PayPal button\n\nRefs #42", true},
		{"on: multi-line keeps the body", "Add thing\nBecause.", testTypes, "chore(agent): add thing\n\nBecause.\n\nRefs #42", true},
	} {
		got := commitMessage(c.msg, issue, c.types)
		if got.text != c.want || got.rewritten != c.rewritten {
			t.Errorf("%s: got %q (rewritten %v), want %q (rewritten %v)", c.name, got.text, got.rewritten, c.want, c.rewritten)
		}
		if c.types != nil && !conventional.Valid(got.text, c.types) {
			t.Errorf("%s: %q does not follow Conventional Commits", c.name, got.text)
		}
	}
}

func TestCommitTypes(t *testing.T) {
	on, off := true, false
	if commitTypes(aiv1.WorkflowSpec{}) != nil || commitTypes(aiv1.WorkflowSpec{ConventionalCommits: &off, CommitTypes: []string{"feat"}}) != nil {
		t.Fatal("off means no types")
	}
	if got := commitTypes(aiv1.WorkflowSpec{ConventionalCommits: &on, CommitTypes: []string{"feat", "chore"}}); strings.Join(got, ",") != "feat,chore" {
		t.Fatalf("the configured types: %v", got)
	}
	if got := commitTypes(aiv1.WorkflowSpec{ConventionalCommits: &on}); strings.Join(got, ",") != strings.Join(testTypes, ",") {
		t.Fatalf("the default types: %v", got)
	}
}

func TestImplementSchemaFor(t *testing.T) {
	// The schemas as they are on main: off must not change a byte.
	for _, c := range []struct {
		name, got, sum string
	}{
		{"implement", implementSchemaFor(nil), "e3573de24b4c383d85b92d5653e2429262143d5a8380536b228d42ead2c035f8"},
		{"step", implementStepSchemaFor(nil), "4dffdb5a91b7f5b5a44803d9a44c10bd38ac00a0313a6f6b5e5944311a168a2a"},
	} {
		if sum := fmt.Sprintf("%x", sha256.Sum256([]byte(c.got))); sum != c.sum {
			t.Errorf("%s: the schema without Conventional Commits changed (sha256 %s)", c.name, sum)
		}
	}
	if implementSchemaFor(nil) != implementSchema || implementStepSchemaFor(nil) != implementStepSchema {
		t.Fatal("off returns the schemas unchanged")
	}
	for name, schema := range map[string]string{"implement": implementSchemaFor(testTypes), "step": implementStepSchemaFor(testTypes)} {
		var s struct {
			Properties struct {
				Status        struct{ Enum []string }
				CommitMessage struct{ Pattern, Description string }
			}
		}
		if err := json.Unmarshal([]byte(schema), &s); err != nil {
			t.Fatalf("%s: not JSON: %v", name, err)
		}
		cm := s.Properties.CommitMessage
		if cm.Pattern != conventional.Pattern(testTypes) || !strings.Contains(cm.Description, "Conventional Commits") || !strings.Contains(cm.Description, "feat, fix") {
			t.Fatalf("%s: commitMessage %+v", name, cm)
		}
		if steps := strings.Contains(strings.Join(s.Properties.Status.Enum, ","), "in_progress"); steps != (name == "step") {
			t.Fatalf("%s: status %v", name, s.Properties.Status.Enum)
		}
	}
}

func TestConventionalPrompts(t *testing.T) {
	note := conventionalNote(testTypes)
	for _, want := range []string{"Conventional Commits", "feat (new behaviour)", "fix (a bug fix)", "revert", "scope", "lowercase", "72 characters"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note must mention %q:\n%s", want, note)
		}
	}
	if conventionalPointer(nil) != "" || !strings.Contains(conventionalPointer(testTypes), "Conventional Commits") {
		t.Fatal("the pointer is there only while the option is on")
	}
	if got := mergeMessage("main", "agent/42-x", testTypes); got != "chore: merge main into agent/42-x" {
		t.Fatalf("merge message %q", got)
	}
	long := "agent/42-" + strings.Repeat("x", 80)
	if got := mergeMessage("main", long, testTypes); got != "chore: merge main into the branch" || !conventional.Valid(got, testTypes) {
		t.Fatalf("a long branch is left out: %q", got)
	}
	if got := mergeMessage("main", "agent/42-x", nil); got != "Merge branch 'main' into agent/42-x" {
		t.Fatalf("merge message off %q", got)
	}
	if startMessage(42, nil) != "Start work on #42" || startMessage(42, testTypes) != "chore: start work on #42" {
		t.Fatal("start messages")
	}
}

func TestImplementBuildPromptConventional(t *testing.T) {
	for _, on := range []bool{false, true} {
		t.Run(fmt.Sprintf("on=%v", on), func(t *testing.T) {
			e := newImplementEnv(t)
			e.configure(t, func(p *aiv1.AgentProjectSpec) { p.Workflow.ConventionalCommits = &on }, func(*aiv1.AgentTaskSpec) {})
			run := phaseRun(t, e, &scriptedEngine{})
			phases(t, run, run.syncBranch, run.readComments, run.buildPrompt)
			mentions := strings.Contains(run.inv.SystemPrompt, "Conventional Commits")
			if !run.stepwise || mentions != on || strings.Contains(run.plainSystem, "Conventional Commits") != on {
				t.Fatalf("stepwise %v, the system prompts mention the format: %v", run.stepwise, mentions)
			}
			want := implementStepSchema
			if on {
				want = implementStepSchemaFor(testTypes)
			}
			if run.inv.JSONSchema != want {
				t.Fatalf("schema:\n%s", run.inv.JSONSchema)
			}
		})
	}
}

// thingEngine implements in one go with the message "Add thing";
// reviewers find nothing.
type thingEngine struct {
	mu    sync.Mutex
	calls []Invocation
}

func (s *thingEngine) Run(_ context.Context, inv Invocation) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v any = ReviewAnswer{Summary: "fine"}
	if !inv.ReadOnly {
		s.calls = append(s.calls, inv)
		_ = os.WriteFile(filepath.Join(inv.Dir, "thing.go"), []byte("package app\n"), 0o644)
		v = ImplementAnswer{Status: "done", Summary: "Added the thing.", CommitMessage: "Add thing", Handoff: "thing"}
	}
	b, _ := json.Marshal(v)
	return &Result{SessionID: "sess-thing", Structured: b}, nil
}

func TestRunConventionalCommits(t *testing.T) {
	e := newImplementEnv(t)
	on := true
	e.configure(t, func(p *aiv1.AgentProjectSpec) { p.Workflow.ConventionalCommits = &on }, func(*aiv1.AgentTaskSpec) {})
	engine := &thingEngine{}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if ran, err := r.Step(context.Background()); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	branch := "agent/42-add-paypal-button"
	bodies := gitCmd(t, e.tmp, "--git-dir", e.origin, "log", "--reverse", "--format=%B%x00", "main.."+branch)
	var msgs []string
	for _, m := range strings.Split(bodies, "\x00") {
		if m = strings.TrimSpace(m); m != "" {
			msgs = append(msgs, m)
		}
	}
	if len(msgs) != 2 || msgs[0] != "chore: start work on #42" || msgs[1] != "chore(agent): add thing\n\nRefs #42" {
		t.Fatalf("commits %q", msgs)
	}
	if len(engine.calls) != 1 || !strings.Contains(engine.calls[0].JSONSchema, `"pattern"`) || !strings.Contains(engine.calls[0].SystemPrompt, "Conventional Commits") {
		t.Fatalf("the implementer must be asked for the format: %d calls", len(engine.calls))
	}
	var report string
	for _, n := range e.gl.MRNotes[1] {
		report += n.Body
	}
	if !strings.Contains(report, "1 commit message did not follow Conventional Commits and was rewritten as `chore(agent): …`.") {
		t.Fatalf("the report must count the rewrite:\n%s", report)
	}
}

func TestRunConventionalOffKeepsMessages(t *testing.T) {
	e := newImplementEnv(t)
	engine := &thingEngine{}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if ran, err := r.Step(context.Background()); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if log := e.originLog(t, "agent/42-add-paypal-button"); len(log) < 3 || log[0] != "Add thing" || log[1] != "Start work on #42" {
		t.Fatalf("commits %q", log)
	}
	if strings.Contains(engine.calls[0].JSONSchema, `"pattern"`) || strings.Contains(engine.calls[0].SystemPrompt, "Conventional Commits") {
		t.Fatal("nothing changes while the option is off")
	}
	for _, n := range e.gl.MRNotes[1] {
		if strings.Contains(n.Body, "did not follow Conventional Commits") {
			t.Fatalf("no notice while off: %s", n.Body)
		}
	}
}

func TestConflictRunConventionalMergeMessage(t *testing.T) {
	ctx := context.Background()
	e := newImplementEnv(t)
	on := true
	e.configure(t, func(p *aiv1.AgentProjectSpec) { p.Workflow.ConventionalCommits = &on }, func(*aiv1.AgentTaskSpec) {})
	engine := &conflictEngine{resolution: "package app // both\n"}
	if _, err := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name).Step(ctx); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(e.seed, "steady.go"), []byte("package app // main\n"), 0o644)
	gitCmd(t, e.seed, "add", ".")
	gitCmd(t, e.seed, "commit", "-m", "main moves on")
	gitCmd(t, e.seed, "push", "origin", "HEAD:main")
	run := phaseRun(t, e, engine)
	run.task.Spec.Source.MergeBaseFor = run.task.Status.Run.PushedSHA
	phases(t, run, append(upToPrompt(run), run.implement, run.concludeMerge)...)
	if !strings.Contains(engine.prompts[len(engine.prompts)-1], "Conventional Commits format") {
		t.Fatalf("the conflict prompt points to the format:\n%s", engine.prompts[len(engine.prompts)-1])
	}
	checkout := filepath.Join(e.tmp, "agent", "repo")
	if got := gitCmd(t, checkout, "log", "--merges", "-1", "--format=%B"); got != "chore: merge main into agent/42-add-paypal-button" {
		t.Fatalf("merge commit %q", got)
	}
}
