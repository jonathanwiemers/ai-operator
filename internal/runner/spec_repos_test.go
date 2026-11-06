package runner

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// repoPlanEngine answers every planner call with a plan whose first item
// changes group/infra.
type repoPlanEngine struct{ calls []Invocation }

func (p *repoPlanEngine) Run(_ context.Context, inv Invocation) (*Result, error) {
	p.calls = append(p.calls, inv)
	b, _ := json.Marshal(repoPlan("group/infra"))
	return &Result{SessionID: "sess-spec", Structured: b}, nil
}

// runCompanionSpec runs a spec task of group/app whose milestone has the
// given repo lines, with group/infra a target, and returns the planner's
// prompt.
func runCompanionSpec(t *testing.T, repos []aiv1.MilestoneRepo) (string, *gitlabfake.Projects, *Runner) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin.git")
	seed := filepath.Join(tmp, "seed")
	gitCmd(t, tmp, "init", "--bare", "--initial-branch=main", origin)
	gitCmd(t, tmp, "clone", origin, seed)
	_ = os.WriteFile(filepath.Join(seed, "README.md"), []byte("app\n"), 0o644)
	gitCmd(t, seed, "add", ".")
	gitCmd(t, seed, "commit", "-m", "init")
	gitCmd(t, seed, "push", "origin", "HEAD:main")

	home := gitlabfake.New()
	home.Project.HTTPURLToRepo = origin
	home.Milestones = []gitlab.Milestone{{ID: 11, IID: 3, Title: "Turn it on",
		Description: "/agent spec\n/agent repo group/infra merge\nBuild it and turn it on."}}
	gl := gitlabfake.NewProjects(home)
	infra := gl.Add("group/infra")
	infra.Project.DefaultBranch = "main"
	infra.Files = []string{"clusters/app/release.yaml", "clusters/db/x.yaml", "README.md"}

	scheme := runtime.NewScheme()
	_ = aiv1.AddToScheme(scheme)
	project := &aiv1.AgentProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: "agents"},
		Spec: aiv1.AgentProjectSpec{GitLab: aiv1.GitLabSpec{Project: "group/app"},
			CrossRepo: &aiv1.CrossRepoSpec{Targets: []aiv1.CrossRepoTarget{{Project: "group/infra", Paths: []string{"clusters/app/"}}}}},
	}
	task := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-spec-m3-turn-it-on", Namespace: "agents"},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeSpec, RunRequest: 1,
			Milestone: &aiv1.MilestoneRef{ID: 11, IID: 3, Title: "Turn it on"}, Repos: repos},
	}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, task).
		WithStatusSubresource(&aiv1.AgentTask{}).Build()
	engine := &repoPlanEngine{}
	r := newTestRunner(t, tmp, k8s, gl, engine, task.Name)
	if ran, err := r.Step(context.Background()); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if len(engine.calls) != 1 {
		t.Fatalf("planner calls: %d", len(engine.calls))
	}
	return engine.calls[0].Prompt, gl, r
}

func TestSpecGivesThePlannerTheCompanionAndLabelsItsItems(t *testing.T) {
	prompt, gl, r := runCompanionSpec(t, []aiv1.MilestoneRepo{{Project: "group/infra", Merge: true}})
	for _, want := range []string{"## Companion repositories", "### group/infra", "Allowed paths: `clusters/app/`", "clusters/app/release.yaml"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "clusters/db/x.yaml") {
		t.Errorf("a file outside the allowed paths is shown:\n%s", prompt)
	}
	if !strings.Contains(stepsText(r), "read companion trees") {
		t.Errorf("steps: %v", stepNames(r.run))
	}
	home := gl.Default
	if !home.Issues[1].HasLabel(infraLabel) || repoLabel(home.Issues[2].Labels) != "" {
		t.Fatalf("labels: #1 %v, #2 %v", home.Issues[1].Labels, home.Issues[2].Labels)
	}
}

func TestSpecPromptWithoutRepoLinesIsUnchanged(t *testing.T) {
	want := buildSpecPrompt(&gitlab.Milestone{Title: "Turn it on",
		Description: rules.Spec("/agent spec\n/agent repo group/infra merge\nBuild it and turn it on.", "/agent")},
		nil, aiv1.DefaultProfiles(), false)
	for name, repos := range map[string][]aiv1.MilestoneRepo{
		"no lines":       nil,
		"only bad lines": {{Project: "group/infra", Problem: "no valid target", Line: "/agent repo group/infra"}},
		"no such target": {{Project: "group/other"}},
	} {
		t.Run(name, func(t *testing.T) {
			prompt, gl, _ := runCompanionSpec(t, repos)
			if prompt != want {
				t.Fatalf("prompt changed:\n got %q\nwant %q", prompt, want)
			}
			if l := repoLabel(gl.Default.Issues[1].Labels); l != "" {
				t.Fatalf("a repo outside the milestone's targets gave label %q", l)
			}
		})
	}
}

func stepsText(r *Runner) string { return strings.Join(stepNames(r.run), ",") }
