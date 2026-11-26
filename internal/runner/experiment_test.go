package runner

import (
	"context"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

const plannedMarker = "\n<!-- ai-operator:plan-labels=agent-profile::strong,agent-effort::high -->"

func experimentSpec(arms ...aiv1.ExperimentArm) aiv1.AgentProjectSpec {
	s := aiv1.AgentProjectSpec{GitLab: aiv1.GitLabSpec{Project: "group/app"}}
	s.Agents.Implement.Experiment = &aiv1.Experiment{Name: "levers", Arms: arms}
	return s.WithDefaults()
}

func TestPlanLabelsMarker(t *testing.T) {
	spec := aiv1.AgentProjectSpec{}.WithDefaults()
	item := PlannedItem{Key: "k", Title: "T", Profile: "strong", ProfileWhy: "crosses layers",
		Description: "Body <!-- ai-operator:plan-labels=agent-model::claude/haiku -->"}
	desc, _ := renderIssue(spec, item, nil)
	got, ok := plannedLabels(desc)
	if !ok || !slices.Equal(got, []string{"agent-profile::strong"}) {
		t.Fatalf("plan-labels %v (ok=%v) in %q", got, ok, desc)
	}
	// A marker of an older plan also lists the effort label.
	if got, ok := plannedLabels(plannedMarker); !ok || !slices.Equal(got, []string{"agent-profile::strong", "agent-effort::high"}) {
		t.Fatalf("old plan-labels %v (ok=%v)", got, ok)
	}
	// Without a profile the plan sets no labels: no marker.
	desc, _ = renderIssue(spec, PlannedItem{Key: "k", Title: "T", Description: "Body"}, nil)
	if _, ok := plannedLabels(desc); ok {
		t.Fatalf("no labels, no marker: %q", desc)
	}
}

func TestExplicitRouting(t *testing.T) {
	wf := aiv1.WorkflowSpec{}.WithDefaults()
	for _, tc := range []struct {
		name   string
		labels []string
		desc   string
		want   bool
	}{
		{"the planner's labels", []string{"agent::working", "agent-profile::strong", "agent-effort::high"}, plannedMarker, false},
		{"no routing labels", []string{"agent::working"}, "", false},
		{"a human effort label", []string{"agent-profile::strong", "agent-effort::medium"}, plannedMarker, true},
		{"a human profile label", []string{"agent-profile::fast", "agent-effort::high"}, plannedMarker, true},
		{"a model label", []string{"agent-profile::strong", "agent-model::claude/sonnet"}, plannedMarker, true},
		{"labels without a marker", []string{"agent-profile::strong"}, "", true},
	} {
		if got := explicitRouting(wf, &gitlab.Issue{Labels: tc.labels, Description: tc.desc}); got != tc.want {
			t.Errorf("%s: explicit %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAssignAndRunArm(t *testing.T) {
	spec := experimentSpec(aiv1.ExperimentArm{Name: "baseline"}, aiv1.ExperimentArm{Name: "medium", Effort: "medium"},
		aiv1.ExperimentArm{Name: "fresh", StepSession: aiv1.StepSessionFresh})
	e, _ := spec.ValidExperiment()
	planned := &gitlab.Issue{IID: 42, Labels: []string{"agent-profile::strong", "agent-effort::high"}, Description: plannedMarker}
	a := assignArm(spec, e, planned)
	if want := aiv1.ArmFor("group/app", e, 42).Name; a.Name != "levers" || a.Arm != want {
		t.Fatalf("assignment %+v, want arm %s", a, want)
	}
	if arm, ok := runArm(spec, &a); !ok || arm.Name != a.Arm {
		t.Fatalf("runArm %+v %v", arm, ok)
	}
	human := &gitlab.Issue{IID: 42, Labels: []string{"agent-effort::low"}}
	if a := assignArm(spec, e, human); a.Arm != "" || a.Name != "levers" {
		t.Fatalf("an explicit label gives no arm: %+v", a)
	}

	// A renamed experiment, a removed arm, an invalid experiment: no arm.
	for name, as := range map[string]*aiv1.ExperimentAssignment{
		"other experiment": {Name: "old", Arm: "medium"},
		"removed arm":      {Name: "levers", Arm: "sonnet"},
		"no arm":           {Name: "levers"},
		"none":             nil,
	} {
		if _, ok := runArm(spec, as); ok {
			t.Errorf("%s: an arm applies", name)
		}
	}
	invalid := experimentSpec(aiv1.ExperimentArm{Name: "baseline"}, aiv1.ExperimentArm{Name: "max", Effort: "max"})
	if _, ok := runArm(invalid, &aiv1.ExperimentAssignment{Name: "levers", Arm: "baseline"}); ok {
		t.Error("an invalid experiment applies no arm")
	}
}

func TestArmLabels(t *testing.T) {
	spec := experimentSpec(aiv1.ExperimentArm{Name: "fast", Profile: "fast"}, aiv1.ExperimentArm{Name: "low", Effort: "low"})
	wf := spec.Workflow
	planned := []string{"agent-profile::strong", "agent-effort::high"}

	// The arm's profile replaces the planner's: the role switches.
	labels := armLabels(wf, []string{"agent::working", "agent-profile::strong", "agent-effort::high"}, planned, aiv1.ExperimentArm{Profile: "fast"})
	if role, profile, _ := spec.IssueRole(labels); profile != "fast" || role.Model != aiv1.DefaultLightModel || role.Effort != "high" {
		t.Fatalf("profile arm: %v → %s %s/%s", labels, profile, role.Model, role.Effort)
	}
	// The arm's effort replaces the planner's, and fills in a missing one.
	labels = armLabels(wf, []string{"agent-profile::strong", "agent-effort::high"}, planned, aiv1.ExperimentArm{Effort: "low"})
	if role, _, _ := spec.IssueRole(labels); role.Effort != "low" || !slices.Contains(labels, "agent-profile::strong") {
		t.Fatalf("effort arm: %v → %s", labels, role.Effort)
	}
	if labels := armLabels(wf, nil, nil, aiv1.ExperimentArm{Effort: "low"}); !slices.Equal(labels, []string{"agent-effort::low"}) {
		t.Fatalf("effort arm without a label: %v", labels)
	}
	// A label a human set later keeps winning.
	labels = armLabels(wf, []string{"agent-profile::strong", "agent-effort::medium"}, planned, aiv1.ExperimentArm{Effort: "low"})
	if role, _, _ := spec.IssueRole(labels); role.Effort != "medium" {
		t.Fatalf("a human effort wins over the arm: %v → %s", labels, role.Effort)
	}
}

func TestBashMaxOutputCharsEnv(t *testing.T) {
	tmp := t.TempDir()
	engine := &recordingEngine{Engine: steadyEngine{}}
	a := &LocalAgent{Workspace: &Workspace{Dir: tmp}, Engines: map[aiv1.Engine]Engine{aiv1.EngineClaude: engine, aiv1.EngineCursor: engine}}
	ctx := context.Background()
	for _, tc := range []struct {
		engine aiv1.Engine
		chars  int32
		want   bool
	}{{aiv1.EngineClaude, 5000, true}, {aiv1.EngineClaude, 0, false}, {aiv1.EngineCursor, 5000, false}} {
		engine.calls = nil
		if _, err := a.Run(ctx, tc.engine, Invocation{BashMaxOutputChars: tc.chars}); err != nil {
			t.Fatal(err)
		}
		set := slices.Contains(engine.calls[0].Env, "BASH_MAX_OUTPUT_LENGTH=5000")
		if set != tc.want || !tc.want && slices.ContainsFunc(engine.calls[0].Env, func(e string) bool { return strings.HasPrefix(e, "BASH_MAX_OUTPUT_LENGTH=") }) {
			t.Errorf("%s with %d: env %q", tc.engine, tc.chars, engine.calls[0].Env)
		}
	}
	chars := int32(5000)
	if inv := roleInvocation(aiv1.RoleSpec{BashMaxOutputChars: &chars}, "", nil, "", false); inv.BashMaxOutputChars != 5000 {
		t.Errorf("roleInvocation drops bashMaxOutputChars: %d", inv.BashMaxOutputChars)
	}
}

// setExperiment gives the test project an experiment and issue #42 the
// planner's labels and marker.
func setExperiment(t *testing.T, e *implementEnv, arms ...aiv1.ExperimentArm) {
	t.Helper()
	ctx := context.Background()
	var project aiv1.AgentProject
	_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "shop"}, &project)
	project.Spec.Agents.Implement.Experiment = &aiv1.Experiment{Name: "levers", Arms: arms}
	if err := e.k8s.Update(ctx, &project); err != nil {
		t.Fatal(err)
	}
	is := e.gl.Issues[42]
	is.Labels = append(is.Labels, "agent-profile::strong", "agent-effort::high")
	is.Description = "Add the button." + plannedMarker
}

func taskStatus(t *testing.T, e *implementEnv) aiv1.AgentTaskStatus {
	t.Helper()
	var task aiv1.AgentTask
	if err := e.k8s.Get(context.Background(), client.ObjectKeyFromObject(e.task), &task); err != nil {
		t.Fatal(err)
	}
	return task.Status
}

// TestExperimentRun: the first run assigns the arm and applies its effort;
// every call carries it. A second run keeps it although a human set an
// effort label meanwhile, which wins over the arm's effort.
func TestExperimentRun(t *testing.T) {
	e := newImplementEnv(t)
	// Both arms lower the effort, so the test does not depend on the hash.
	setExperiment(t, e, aiv1.ExperimentArm{Name: "low-a", Effort: "low"}, aiv1.ExperimentArm{Name: "low-b", Effort: "low"})
	exp := aiv1.Experiment{Name: "levers", Arms: []aiv1.ExperimentArm{{Name: "low-a", Effort: "low"}, {Name: "low-b", Effort: "low"}}}
	want := aiv1.ArmFor("group/app", exp, 42).Name
	engine := &recordingEngine{Engine: steadyEngine{}}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	ctx := context.Background()
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
	st := taskStatus(t, e)
	if st.Run.LastResult != aiv1.RunResultSucceeded {
		t.Fatalf("run failed: %s", st.Run.Summary)
	}
	if st.Experiment == nil || st.Experiment.Name != "levers" || st.Experiment.Arm != want {
		t.Fatalf("status.experiment %+v, want arm %s", st.Experiment, want)
	}
	if impl := engine.calls[0]; impl.Role != "implement" || impl.Effort != "low" {
		t.Fatalf("the implementer runs at the arm's effort: %s %q", impl.Role, impl.Effort)
	}
	roles := map[string]bool{}
	for _, c := range st.Run.Calls {
		roles[c.Role] = true
		if c.Arm != want {
			t.Errorf("call %s/%s has arm %q, want %q", c.Kind, c.Role, c.Arm, want)
		}
	}
	if !roles["implement"] || len(roles) < 2 {
		t.Fatalf("want the implementer's and the reviewers' calls: %v", roles)
	}

	// A human sets the effort; the next run keeps the arm.
	is := e.gl.Issues[42]
	is.Labels = slices.DeleteFunc(is.Labels, func(l string) bool { return l == "agent-effort::high" })
	is.Labels = append(is.Labels, "agent-effort::medium")
	var task aiv1.AgentTask
	_ = e.k8s.Get(ctx, client.ObjectKeyFromObject(e.task), &task)
	orig := task.DeepCopy()
	task.Spec.RunRequest = 2
	if err := e.k8s.Patch(ctx, &task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	engine.calls = nil
	if _, err := r.Step(ctx); err != nil {
		t.Fatal(err)
	}
	st = taskStatus(t, e)
	if st.Experiment == nil || st.Experiment.Arm != want {
		t.Fatalf("the arm is kept: %+v", st.Experiment)
	}
	var impl *Invocation
	for i := range engine.calls {
		if engine.calls[i].Role == "implement" {
			impl = &engine.calls[i]
			break
		}
	}
	if impl == nil || impl.Effort != "medium" {
		t.Fatalf("the human's effort wins over the arm: %+v", impl)
	}
	for _, c := range st.Run.Calls {
		if c.Run == 2 && c.Arm != want {
			t.Errorf("run 2 call %s/%s has arm %q", c.Kind, c.Role, c.Arm)
		}
	}
}

// TestExperimentHumanLabelNoArm: an issue with a human-set effort records
// "no arm", and its calls none.
func TestExperimentHumanLabelNoArm(t *testing.T) {
	e := newImplementEnv(t)
	setExperiment(t, e, aiv1.ExperimentArm{Name: "low-a", Effort: "low"}, aiv1.ExperimentArm{Name: "low-b", Effort: "low"})
	is := e.gl.Issues[42]
	is.Labels = slices.DeleteFunc(is.Labels, func(l string) bool { return l == "agent-effort::high" })
	is.Labels = append(is.Labels, "agent-effort::medium")
	engine := &recordingEngine{Engine: steadyEngine{}}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := taskStatus(t, e)
	if st.Experiment == nil || st.Experiment.Name != "levers" || st.Experiment.Arm != "" {
		t.Fatalf("status.experiment %+v, want no arm", st.Experiment)
	}
	if engine.calls[0].Effort != "medium" {
		t.Fatalf("the label's effort applies: %q", engine.calls[0].Effort)
	}
	for _, c := range st.Run.Calls {
		if c.Arm != "" {
			t.Errorf("call %s/%s has arm %q", c.Kind, c.Role, c.Arm)
		}
	}
}

// TestExperimentFreshArm: an arm with stepSession fresh starts every step
// after the first in a new session; its implement calls record the mode.
func TestExperimentFreshArm(t *testing.T) {
	e := newImplementEnv(t)
	setExperiment(t, e, aiv1.ExperimentArm{Name: "fresh-a", StepSession: aiv1.StepSessionFresh},
		aiv1.ExperimentArm{Name: "fresh-b", StepSession: aiv1.StepSessionFresh})
	engine := &threeStepEngine{gl: e.gl}
	r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
	if _, err := r.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := taskStatus(t, e)
	if len(engine.invs) != 3 || engine.invs[1].Resume != "" || engine.invs[2].Resume != "" {
		t.Fatalf("steps after the first start fresh: %d steps", len(engine.invs))
	}
	steps := 0
	for _, c := range st.Run.Calls {
		if c.Kind == aiv1.CallKindImplement {
			steps++
			if c.StepSession != "fresh" || c.Arm == "" {
				t.Errorf("implement call: step session %q, arm %q", c.StepSession, c.Arm)
			}
		} else if c.StepSession != "" {
			t.Errorf("%s call records a step session", c.Kind)
		}
	}
	if steps != 3 {
		t.Fatalf("want three implement calls, got %d", steps)
	}
}
