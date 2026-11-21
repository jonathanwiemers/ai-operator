package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// planSpecTask is milestone iid's spec task; ap nil: no autopilot.
func planSpecTask(iid int64, ap *aiv1.AutopilotStatus) aiv1.AgentTask {
	return aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("shop-spec-m%d", iid), Namespace: "agents", Labels: map[string]string{labelProject: "shop"}},
		Spec: aiv1.AgentTaskSpec{Type: aiv1.TaskTypeSpec, ProjectRef: "shop", Autopilot: ap != nil,
			Milestone: &aiv1.MilestoneRef{IID: iid, Title: fmt.Sprintf("M%d", iid)}},
		Status: aiv1.AgentTaskStatus{Autopilot: ap},
	}
}

// items are n autopilot items, the first merged of them merged.
func planItems(n, merged int) []aiv1.AutopilotItem {
	var items []aiv1.AutopilotItem
	for i := range n {
		it := aiv1.AutopilotItem{IID: int64(100 + i), State: aiv1.AutopilotItemPending}
		if i < merged {
			it.State, it.MergeRequestIID, it.MergeRequestURL, it.MergedSHA = aiv1.AutopilotItemMerged, int64(500+i),
				fmt.Sprintf("https://gitlab.example/group/app/-/merge_requests/%d", 500+i), fmt.Sprintf("sha%d", i)
		}
		items = append(items, it)
	}
	return items
}

func milestoneFile(t *testing.T, dir string, iid int64) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, planContextDir, "milestones", fmt.Sprintf("%d.md", iid)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPlanMilestoneStatesAndQueue(t *testing.T) {
	gl := gitlabfake.New()
	for _, m := range []struct {
		iid   int64
		state string
	}{{2, "closed"}, {3, "active"}, {4, "active"}, {5, "active"}, {6, "active"}, {7, "active"}, {8, "active"}, {9, "active"}} {
		gl.Milestones = append(gl.Milestones, gitlab.Milestone{IID: m.iid, Title: fmt.Sprintf("M%d", m.iid), State: m.state})
	}
	mark, settled := metav1.NewTime(time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)), metav1.Now()
	tasks := []aiv1.AgentTask{
		planSpecTask(2, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotDone, Items: planItems(2, 2)}),
		planSpecTask(3, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: planItems(7, 3)}),
		planSpecTask(4, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, StartRequestedBy: "@alice", StartRequestedAt: &mark}),
		planSpecTask(5, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, PausedReason: "CI on main\nis red", Items: planItems(2, 0)}),
		planSpecTask(6, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}),
		planSpecTask(7, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotDone, SettleSyncedAt: &settled, Items: planItems(1, 1)}),
		planSpecTask(9, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotOff}),
	}
	dir := t.TempDir()
	infos, err := writePlanMilestones(context.Background(), dir, gl, "group/app", tasks, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64][2]string{
		2: {"done", "not in the autopilot queue"},
		3: {"running, 3 of 7 issues merged", "1st of 4 in the autopilot queue"},
		5: {"paused: CI on main is red", "2nd of 4 in the autopilot queue"},
		4: {"waiting for its turn", "3rd of 4 in the autopilot queue"},
		6: {"waiting for its turn", "4th of 4 in the autopilot queue"},
		7: {"settling", "not in the autopilot queue"},
		8: {"no autopilot", "not in the autopilot queue"},
		9: {"no autopilot", "not in the autopilot queue"},
	}
	if len(infos) != len(want) {
		t.Fatalf("infos: %+v", infos)
	}
	for _, info := range infos {
		w := want[info.IID]
		if info.State != w[0] || info.Queue != w[1] || info.Path != fmt.Sprintf(".plan-context/milestones/%d.md", info.IID) || info.Open != (info.IID != 2) {
			t.Errorf("%%%d: %+v, want %v", info.IID, info, w)
		}
		f := milestoneFile(t, dir, info.IID)
		if !strings.Contains(f, "\nState: "+w[0]+" (milestone ") || !strings.Contains(f, "\nQueue: "+w[1]+"\n") {
			t.Errorf("%%%d file:\n%s", info.IID, f)
		}
	}

	// A stored run order ranks, the old mark of %4 not: started ones first,
	// in stored order, then the rest in stored order.
	infos, err = writePlanMilestones(context.Background(), dir, gl, "group/app", tasks, []int64{6, 5, 4, 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	queue := map[int64]string{}
	for _, info := range infos {
		queue[info.IID] = info.Queue
	}
	for iid, w := range map[int64]string{5: "1st of 4", 3: "2nd of 4", 6: "3rd of 4", 4: "4th of 4"} {
		if !strings.HasPrefix(queue[iid], w) {
			t.Errorf("stored order: %%%d is %q, want %q", iid, queue[iid], w)
		}
	}

	// The only autopilot milestone without items is being planned.
	gl.Milestones = []gitlab.Milestone{{IID: 10, Title: "M10", State: "active"}}
	infos, err = writePlanMilestones(context.Background(), dir, gl, "group/app", []aiv1.AgentTask{planSpecTask(10, nil), planSpecTask(10, &aiv1.AutopilotStatus{})}, nil, nil)
	if err != nil || len(infos) != 1 || infos[0].State != "planning" || infos[0].Queue != "1st of 1 in the autopilot queue" {
		t.Fatalf("planning: %+v %v", infos, err)
	}
}

func TestPlanQueueText(t *testing.T) {
	for pos, want := range map[int]string{-1: "not in the autopilot queue", 0: "1st of 30", 1: "2nd of 30", 2: "3rd of 30",
		3: "4th of 30", 10: "11th of 30", 11: "12th of 30", 12: "13th of 30", 20: "21st of 30", 21: "22nd of 30"} {
		if got := planQueueText(pos, 30); !strings.HasPrefix(got, want) {
			t.Errorf("%d: %q, want %q", pos, got, want)
		}
	}
}

func TestPlanMilestoneFileContent(t *testing.T) {
	gl := gitlabfake.New()
	m := gitlab.Milestone{IID: 3, Title: "Payments", State: "active",
		Description: "/agent spec\n# Payments\n\n## Goal\n\n  Pay with PayPal.\n\n## Requirements\n\n  /agent autopilot\nR1. A button."}
	gl.Milestones = []gitlab.Milestone{m}
	merged := gl.AddIssue(100, "Add the button")
	merged.Milestone, merged.State, merged.Description = &m, "closed", "Short."
	long := gl.AddIssue(101, "Long one")
	long.Milestone, long.Description = &m, strings.Repeat("ä", 3000) // 6000 bytes
	other := gl.AddIssue(102, "Not in this milestone")
	other.Milestone = &gitlab.Milestone{IID: 4, Title: "Other"}
	dir := t.TempDir()
	infos, err := writePlanMilestones(context.Background(), dir, gl, "group/app", []aiv1.AgentTask{
		planSpecTask(3, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: planItems(1, 1)}),
	}, nil, nil)
	if err != nil || len(infos) != 1 || infos[0].Goal != "Pay with PayPal." {
		t.Fatalf("%+v %v", infos, err)
	}
	f := milestoneFile(t, dir, 3)
	if !strings.HasPrefix(f, planUntrusted+"\n\n# %3 Payments\n\nState: running, 1 of 1 issues merged (milestone active)\n") {
		t.Fatalf("head:\n%s", f)
	}
	if strings.Contains(f, "/agent") || !strings.Contains(f, "R1. A button.") {
		t.Fatalf("operator lines:\n%s", f)
	}
	if !strings.Contains(f, "### #100 Add the button\n\nState: closed; autopilot: Merged; merge request !500 https://gitlab.example/group/app/-/merge_requests/500; merged as sha0\n\nShort.") {
		t.Fatalf("merged issue:\n%s", f)
	}
	if strings.Contains(f, "#102") {
		t.Fatalf("an issue of another milestone:\n%s", f)
	}
	_, desc, _ := strings.Cut(f, "### #101 Long one\n\nState: opened\n\n")
	desc = strings.TrimSuffix(desc, "\n\n")
	if len(desc) > planIssueMax || !strings.HasSuffix(desc, "ä…") {
		t.Fatalf("the long description has %d bytes, ends %q", len(desc), desc[max(0, len(desc)-10):])
	}
}

func TestPlanMilestoneFileCutAtAnIssue(t *testing.T) {
	gl := gitlabfake.New()
	m := gitlab.Milestone{IID: 3, Title: "Big", State: "active"}
	gl.Milestones = []gitlab.Milestone{m}
	for i := range 30 {
		is := gl.AddIssue(int64(100+i), fmt.Sprintf("Issue %d", i))
		is.Milestone, is.Description = &m, strings.Repeat("x", 5000)
	}
	dir := t.TempDir()
	if _, err := writePlanMilestones(context.Background(), dir, gl, "group/app", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	f := milestoneFile(t, dir, 3)
	n := strings.Count(f, "\n### #")
	if len(f) > planMilestoneMax || n < 10 || n == 30 {
		t.Fatalf("%d bytes, %d issues", len(f), n)
	}
	if !strings.HasSuffix(f, strings.Repeat("x", planIssueMax-len("…"))+"…\n\n"+fmt.Sprintf("(%d of 30 issues left out: the file is cut at 64 KiB.)\n", 30-n)) {
		t.Fatalf("the end:\n%s", f[len(f)-200:])
	}
}

func TestPlanMilestonesRewritten(t *testing.T) {
	gl := gitlabfake.New()
	for i := range 8 {
		gl.Milestones = append(gl.Milestones, gitlab.Milestone{IID: int64(i + 1), Title: fmt.Sprintf("M%d", i+1), State: "closed"})
	}
	gl.Milestones = append(gl.Milestones, gitlab.Milestone{IID: 20, Title: "Open", State: "active"})
	dir := t.TempDir()
	if _, err := writePlanMilestones(context.Background(), dir, gl, "group/app", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	list := func() string {
		entries, _ := os.ReadDir(filepath.Join(dir, planContextDir, "milestones"))
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return strings.Join(names, " ")
	}
	if got := list(); got != "20.md 4.md 5.md 6.md 7.md 8.md" {
		t.Fatalf("open and the 5 newest closed: %s", got)
	}
	gl.Milestones = gl.Milestones[:7] // %8 and %20 are gone
	if _, err := writePlanMilestones(context.Background(), dir, gl, "group/app", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := list(); got != "3.md 4.md 5.md 6.md 7.md" {
		t.Fatalf("second turn: %s", got)
	}
	gl.ListMilestonesError = errors.New("boom")
	if _, err := writePlanMilestones(context.Background(), dir, gl, "group/app", nil, nil, nil); err == nil || list() != "" {
		t.Fatalf("a failed list: %v, files %q", err, list())
	}
}

func TestPlanExcludeOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".git", "info", "exclude")
	if err := planExclude(dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "/.plan-context/\n" {
		t.Fatalf("new file: %q", b)
	}
	_ = os.WriteFile(path, []byte("*.log"), 0o644)
	for range 2 {
		if err := planExclude(dir); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(path); string(b) != "*.log\n/.plan-context/\n" {
		t.Fatalf("appended once: %q", b)
	}
}

func TestLocalAgentPlanContext(t *testing.T) {
	tmp := t.TempDir()
	a := &LocalAgent{Workspace: &Workspace{Dir: filepath.Join(tmp, "repo")}, Exchange: filepath.Join(tmp, "exchange")}
	src := filepath.Join(a.Exchange, "plan", planContextDir)
	_ = os.MkdirAll(filepath.Join(src, "milestones"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "milestones", "3.md"), []byte("new"), 0o644)
	stale := filepath.Join(a.Workspace.Dir, planContextDir, "milestones", "2.md")
	_ = os.MkdirAll(filepath.Dir(stale), 0o755)
	_ = os.WriteFile(stale, []byte("old"), 0o644)
	if err := a.PlanContext(context.Background(), filepath.Join(tmp, "elsewhere")); err == nil {
		t.Fatal("a source outside the exchange was copied")
	}
	if err := a.PlanContext(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the stale file is still there: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(a.Workspace.Dir, planContextDir, "milestones", "3.md")); string(b) != "new" {
		t.Fatalf("copied: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(a.Workspace.Dir, ".git", "info", "exclude")); string(b) != "/.plan-context/\n" {
		t.Fatalf("exclude: %q", b)
	}
}

// A turn leaves the files in the agent's checkout, out of git status.
func TestPlanTurnWritesMilestoneFiles(t *testing.T) {
	e := newPlanEnv(t, planAnswer("s", "ok", ""), planAnswer("s", "still ok", ""), planAnswer("s", "and again", ""))
	gl := e.run.GitLab.(*gitlabfake.Fake)
	gl.Milestones = []gitlab.Milestone{{IID: 3, Title: "M3", State: "active", Description: "## Goal\n\nPay."}}
	task := planSpecTask(3, &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, Items: planItems(2, 1)})
	if err := e.k8s.Create(context.Background(), &task); err != nil {
		t.Fatal(err)
	}
	e.turn(t, 1, "hi")
	repo := filepath.Join(filepath.Dir(e.run.StateDir), "agent", "repo")
	f := milestoneFile(t, repo, 3)
	if !strings.HasPrefix(f, planUntrusted) || !strings.Contains(f, "State: running, 1 of 2 issues merged") {
		t.Fatalf("file:\n%s", f)
	}
	if out := gitCmd(t, repo, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("git status shows: %s", out)
	}
	if _, err := os.Stat(filepath.Join(e.run.Exchange, "plan")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the exchange copy is left: %v", err)
	}

	// A failing task list or GitLab list does not fail the turn.
	e.run.K8s = interceptor.NewClient(e.k8s.(client.WithWatch), interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*aiv1.AgentTaskList); ok {
				return errors.New("forbidden")
			}
			return c.List(ctx, list, opts...)
		},
	})
	if rep := lastReply(t, e.turn(t, 2, "again"), 2); rep.Text != "still ok" || rep.Error != "" {
		t.Fatalf("reply: %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(repo, planContextDir, "milestones", "3.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a file without the autopilot state: %v", err)
	}
	e.run.K8s = e.k8s
	gl.ListMilestonesError = errors.New("boom")
	if rep := lastReply(t, e.turn(t, 3, "once more"), 3); rep.Text != "and again" || rep.Error != "" {
		t.Fatalf("reply: %+v", rep)
	}
}
