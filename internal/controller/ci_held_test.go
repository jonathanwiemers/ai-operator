package controller

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/equality"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// newHeldEnv replays the incident up to the hold: the pipeline of sha1, the
// MR's head, stays red through the retry and a fix run that pushed nothing,
// so #42 is in needs-input with cause ciRed. The notes so far are dropped.
func newHeldEnv(t *testing.T) (*projectEnv, *gitlab.MergeRequest, int64) {
	t.Helper()
	e := newCIEnv(t, "sha1")
	mr := e.ciMR("sha1")
	pid := e.gl.AddPipeline("sha1", "agent/42", "failed", failedTest())
	mr.HeadPipeline = &gitlab.Pipeline{ID: pid}
	e.reconcile() // retry
	e.gl.SetPipelineStatus(pid, "failed")
	e.reconcile() // fix requested
	e.agentPushed("sha1", 0, pid)
	e.reconcile() // could not fix
	if !e.wantHeldCI() || !strings.Contains(e.notes(), "could not fix") {
		t.Fatalf("setup: labels %v cause %q notes %q", e.gl.Labels(42), e.ciTask().Status.Run.NeedsInputCause, e.notes())
	}
	e.gl.IssueNotes[42] = nil
	return e, mr, pid
}

// wantHeldCI reports whether #42 is still held for red CI.
func (e *projectEnv) wantHeldCI() bool {
	return slices.Equal(e.gl.Labels(42), []string{"agent::needs-input"}) && e.ciTask().Status.Run.NeedsInputCause == aiv1.NeedsInputCIRed
}

// wantLifted fails unless #42 moved to review with green CI of pipeline id
// and the one note naming url.
func (e *projectEnv) wantLifted(what string, id int64, url string) {
	e.t.Helper()
	task := e.ciTask()
	ci := task.Status.CI
	if !slices.Equal(e.gl.Labels(42), []string{"agent::review"}) || task.Status.Run.NeedsInputCause != "" {
		e.t.Fatalf("%s: want review and no cause: labels %v cause %q", what, e.gl.Labels(42), task.Status.Run.NeedsInputCause)
	}
	if ci.State != aiv1.CIStateSuccess || ci.PipelineID != id || ci.FailedJobs != nil || ci.RecoveredAt == nil {
		e.t.Fatalf("%s: status.ci %+v", what, ci)
	}
	note := fmt.Sprintf("✅ CI is green again (pipeline %s), so #42 moves to review.", url)
	if n := e.notes(); strings.Count(n, "CI is green again") != 1 || !strings.Contains(n, note) {
		e.t.Fatalf("%s: want %q once, notes %q", what, note, n)
	}
}

func TestHeldCIRetriedPipelineGreenLiftsTheHold(t *testing.T) {
	e, _, pid := newHeldEnv(t)
	e.gl.SetPipelineStatus(pid, "success")
	e.reconcile()
	e.wantLifted("same pipeline retried", pid, fmt.Sprintf("https://gitlab.example/group/app/-/pipelines/%d", pid))
	for range 2 {
		e.reconcile()
	}
	e.wantLifted("a later sync", pid, fmt.Sprintf("https://gitlab.example/group/app/-/pipelines/%d", pid))
}

func TestHeldCINewPipelineOfTheSameCommitLiftsTheHold(t *testing.T) {
	for _, status := range []string{"success", "manual"} {
		e, mr, _ := newHeldEnv(t)
		pid := e.gl.AddPipeline("sha1", "agent/42", status)
		mr.HeadPipeline = &gitlab.Pipeline{ID: pid}
		e.reconcile()
		e.wantLifted(status, pid, fmt.Sprintf("https://gitlab.example/group/app/-/pipelines/%d", pid))
	}
}

func TestHeldCIKeepsTheHoldFor(t *testing.T) {
	for name, change := range map[string]func(*projectEnv, *gitlab.MergeRequest){
		"a commit by someone else": func(e *projectEnv, mr *gitlab.MergeRequest) {
			mr.SHA = "sha-human"
			mr.HeadPipeline = &gitlab.Pipeline{ID: e.gl.AddPipeline("sha-human", "agent/42", "success")}
		},
		"a skipped pipeline": func(e *projectEnv, mr *gitlab.MergeRequest) {
			mr.HeadPipeline = &gitlab.Pipeline{ID: e.gl.AddPipeline("sha1", "agent/42", "skipped")}
		},
		"another cause": func(e *projectEnv, mr *gitlab.MergeRequest) {
			e.patchStatus(ciTask, func(s *aiv1.AgentTaskStatus) { s.Run.NeedsInputCause = aiv1.NeedsInputAgentQuestion })
			mr.HeadPipeline = &gitlab.Pipeline{ID: e.gl.AddPipeline("sha1", "agent/42", "success")}
		},
	} {
		t.Run(name, func(t *testing.T) {
			e, mr, _ := newHeldEnv(t)
			change(e, mr)
			before := e.ciTask().Status.CI
			e.reconcile()
			if !slices.Equal(e.gl.Labels(42), []string{"agent::needs-input"}) || e.notes() != "" {
				t.Fatalf("held: labels %v notes %q", e.gl.Labels(42), e.notes())
			}
			if after := e.ciTask().Status.CI; !equality.Semantic.DeepEqual(before, after) {
				t.Fatalf("status.ci changed: %+v -> %+v", before, after)
			}
		})
	}
}

func TestHeldCIFollowsTheRetriedPipelineWhileHeld(t *testing.T) {
	e, _, pid := newHeldEnv(t)
	e.gl.SetPipelineStatus(pid, "running")
	e.reconcile()
	ci := e.ciTask().Status.CI
	if !e.wantHeldCI() || ci.State != aiv1.CIStateRunning || !slices.Equal(ci.FailedJobs, []string{"test"}) || e.notes() != "" {
		t.Fatalf("running: held with the old jobs: %+v labels %v notes %q", ci, e.gl.Labels(42), e.notes())
	}
	e.gl.Jobs[pid] = []gitlab.Job{{ID: 900, Name: "lint", Status: "failed"}}
	e.gl.SetPipelineStatus(pid, "failed")
	e.reconcile()
	ci = e.ciTask().Status.CI
	if !e.wantHeldCI() || ci.State != aiv1.CIStateFailed || !slices.Equal(ci.FailedJobs, []string{"lint"}) || e.notes() != "" {
		t.Fatalf("failed again: held with the jobs listed again: %+v labels %v notes %q", ci, e.gl.Labels(42), e.notes())
	}
	if e.ciTask().Spec.RunRequest != 1 {
		t.Fatal("a hold starts no fix run")
	}
}

// heldCompanion holds #42 for red CI with the home tree in home and the
// companion's pipeline (its MR's head) failed; it returns the companion's
// fake and pipeline.
func heldCompanion(t *testing.T, home aiv1.CIState) (*projectEnv, *gitlabfake.Fake, int64) {
	t.Helper()
	e := newCIEnv(t, "sha1")
	infra := withCompanion(e)
	cmr, _ := infra.CreateMergeRequest(e.ctx, "group/infra", gitlab.CreateMergeRequestOptions{SourceBranch: "agent/app-42-add-paypal-button"})
	cmr = infra.MRs[cmr.IID]
	cmr.SHA = "isha1"
	ipid := infra.AddPipeline("isha1", "agent/app-42-add-paypal-button", "failed", failedTest())
	cmr.HeadPipeline = &gitlab.Pipeline{ID: ipid}
	e.companionPushed(ciTask, "isha1", cmr.IID, true)
	mr := e.ciMR("sha1")
	mr.HeadPipeline = &gitlab.Pipeline{ID: e.gl.AddPipeline("sha1", "agent/42", "success")}
	e.patchStatus(ciTask, func(s *aiv1.AgentTaskStatus) {
		s.Run.NeedsInputCause = aiv1.NeedsInputCIRed
		s.CI = &aiv1.CIStatus{SHA: "sha1", State: home, PipelineID: 1, FailedJobs: []string{"test"},
			Companion: &aiv1.CompanionCI{SHA: "isha1", State: aiv1.CIStateFailed, PipelineID: ipid, FailedJobs: []string{"test"}}}
		if home == aiv1.CIStateSuccess {
			s.CI.FailedJobs = nil
		}
	})
	e.gl.SetLabels(42, "agent::needs-input")
	return e, infra, ipid
}

func TestHeldCIGreenCompanionLiftsTheHold(t *testing.T) {
	e, infra, ipid := heldCompanion(t, aiv1.CIStateSuccess)
	e.reconcile()
	if !e.wantHeldCI() || e.companionCIState() != aiv1.CIStateFailed {
		t.Fatalf("the companion is still red: %+v labels %v", e.ciTask().Status.CI, e.gl.Labels(42))
	}
	infra.SetPipelineStatus(ipid, "success")
	e.reconcile()
	e.wantLifted("companion green", 1, fmt.Sprintf("https://gitlab.example/group/app/-/pipelines/%d", ipid))
	if e.companionCIState() != aiv1.CIStateSuccess {
		t.Fatalf("companion %+v", e.ciTask().Status.CI.Companion)
	}
}

func TestHeldCIGreenHomeWithARedCompanionStaysHeld(t *testing.T) {
	e, _, _ := heldCompanion(t, aiv1.CIStateFailed)
	e.reconcile()
	ci := e.ciTask().Status.CI
	if !e.wantHeldCI() || ci.State != aiv1.CIStateSuccess || ci.Companion.State != aiv1.CIStateFailed || e.notes() != "" {
		t.Fatalf("home green, companion red: held: %+v labels %v notes %q", ci, e.gl.Labels(42), e.notes())
	}
}
