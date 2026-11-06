package runner

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestCheckpointResumeNote(t *testing.T) {
	got := checkpointResumeNote(cutUsageLimit, 3)
	want := "The previous attempt was cut off (the usage limit was reached) after step 3; its uncommitted work was saved as a checkpoint commit. Check `git show --stat HEAD` before you continue."
	if got != want {
		t.Fatalf("got %q", got)
	}
	if got := checkpointResumeNote("odd\nreason", 1); !strings.Contains(got, "(odd reason)") {
		t.Fatalf("an unknown reason stays on one line: %q", got)
	}
}

func TestCheckpointNoteInPrompt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		moveOn bool
		want   bool
	}{{"head is the checkpoint", false, true}, {"head moved on", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			e := newImplementEnv(t)
			branch := "agent/42-add-paypal-button"
			gitCmd(t, e.seed, "checkout", "-b", branch)
			gitCmd(t, e.seed, "commit", "--allow-empty", "-m", "chore(wip): checkpoint #42 step 2 (usage-limit)")
			gitCmd(t, e.seed, "push", "origin", branch)
			cp := strings.TrimSpace(gitCmd(t, e.seed, "rev-parse", "HEAD"))
			if tc.moveOn {
				gitCmd(t, e.seed, "commit", "--allow-empty", "-m", "human push")
				gitCmd(t, e.seed, "push", "origin", branch)
			}
			ctx := context.Background()
			var task aiv1.AgentTask
			_ = e.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: e.task.Name}, &task)
			task.Status.Run.Branch = branch
			task.Status.Run.Checkpoint = &aiv1.CheckpointStatus{SHA: cp, Branch: branch, Reason: cutUsageLimit, Step: 2}
			if err := e.k8s.Status().Update(ctx, &task); err != nil {
				t.Fatal(err)
			}
			engine := &decisionEngine{}
			r := newTestRunner(t, e.tmp, e.k8s, e.gl, engine, e.task.Name)
			if _, err := r.Step(ctx); err != nil {
				t.Fatal(err)
			}
			p := engine.calls[0].Prompt
			has := strings.HasPrefix(p, checkpointResumeNote(cutUsageLimit, 2)+"\n\n")
			if has != tc.want || (!tc.want && strings.Contains(p, "checkpoint commit")) {
				t.Fatalf("note expected %v:\n%s", tc.want, p)
			}
		})
	}
}
