package controller

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

// infraJobs adds a failed pipeline of sha whose jobs log traces[i].
func infraJobs(gl *gitlabfake.Fake, sha, ref string, jobs []gitlab.Job, traces ...string) int64 {
	pid := gl.AddPipeline(sha, ref, "failed", jobs...)
	for i, tr := range traces {
		gl.Traces[gl.Jobs[pid][i].ID] = tr
	}
	return pid
}

func failedJob(name, reason string) gitlab.Job {
	return gitlab.Job{Name: name, Status: "failed", FailureReason: reason}
}

// setInfra changes spec.ci.infraFailures of the project.
func (e *projectEnv) setInfra(f func(*aiv1.CIInfraSpec)) {
	e.t.Helper()
	var project aiv1.AgentProject
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &project); err != nil {
		e.t.Fatal(err)
	}
	if project.Spec.CI.InfraFailures == nil {
		project.Spec.CI.InfraFailures = &aiv1.CIInfraSpec{}
	}
	f(project.Spec.CI.InfraFailures)
	if err := e.c.Update(e.ctx, &project); err != nil {
		e.t.Fatal(err)
	}
}

func (e *projectEnv) after(d time.Duration) {
	now := e.r.Now().Add(d)
	e.r.Now = func() time.Time { return now }
}

func (e *projectEnv) infraNotes() int {
	return strings.Count(e.notes(), "🔁")
}

// The incident: build:client red on EAI_AGAIN is retried after the backoff,
// with no fix round and one note; the next red, a code failure, is a fix
// round without one more flaky retry.
func TestCIInfraRetryThenCodeFailureIsAFixRound(t *testing.T) {
	e := newCIEnv(t, "sha1")
	pid := infraJobs(e.gl, "sha1", "agent/42",
		[]gitlab.Job{failedJob("build:client", "script_failure"), {Name: "lint", Status: "failed", AllowFailure: true}},
		"npm ERR! request to https://registry.npmjs.org failed, reason: getaddrinfo eai_again registry.npmjs.org")

	e.reconcile()
	ci := e.ciTask().Status.CI
	if ci.State != aiv1.CIStateRunning || len(e.gl.Retried) != 0 || ci.Infra == nil || ci.Infra.DueAt == nil ||
		!ci.Infra.DueAt.Time.Equal(e.r.Now().Add(2*time.Minute)) || ci.Infra.Signature != "build:client: EAI_AGAIN" {
		t.Fatalf("an infra failure waits for the backoff: retried=%v %+v", e.gl.Retried, ci)
	}
	if e.infraNotes() != 1 || !strings.Contains(e.notes(), "retries the failed jobs in 2m; this uses no fix round") {
		t.Fatalf("one note on the first infra retry: %q", e.notes())
	}
	if len(e.gl.TraceReads) != 1 {
		t.Fatalf("only the failing job's log is read: %v", e.gl.TraceReads)
	}

	// Polls before the retry is due make no calls and read no log.
	e.after(time.Minute)
	e.reconcile()
	if len(e.gl.Retried) != 0 || len(e.gl.TraceReads) != 1 {
		t.Fatalf("no retry and no log read before dueAt: retried=%v reads=%v", e.gl.Retried, e.gl.TraceReads)
	}
	e.after(time.Minute)
	e.reconcile()
	ci = e.ciTask().Status.CI
	if !slices.Equal(e.gl.Retried, []int64{pid}) || ci.Infra.Retries != 1 || ci.Infra.DueAt != nil || !ci.Retried {
		t.Fatalf("retried once due: retried=%v %+v", e.gl.Retried, ci.Infra)
	}
	task := e.ciTask()
	if task.Spec.RunRequest != 0 || task.Status.Run.CIRound != 0 {
		t.Fatalf("an infra retry uses no fix round: request=%d round=%d", task.Spec.RunRequest, task.Status.Run.CIRound)
	}

	// The retried job fails in the code.
	e.gl.Jobs[pid] = []gitlab.Job{{ID: 9001, Name: "build:client", Status: "failed", FailureReason: "script_failure"}}
	e.gl.Traces[9001] = "error TS2322: Type 'string' is not assignable to type 'number'."
	e.gl.SetPipelineStatus(pid, "failed")
	e.reconcile()
	task = e.ciTask()
	if task.Spec.RunRequest != 1 || task.Spec.Source.CIFixPipelineID != pid || e.ciState() != aiv1.CIStateFixing {
		t.Fatalf("a code failure is a fix round: %+v state=%s", task.Spec, e.ciState())
	}
	if len(e.gl.Retried) != 1 || e.infraNotes() != 1 {
		t.Fatalf("no flaky retry after the infra retry, no second note: retried=%v notes=%q", e.gl.Retried, e.notes())
	}
}

// A runner failure reason needs no log; when the retries run out, the issue
// is held for red CI with the runner note, no fix run, and lifts once the
// head pipeline is green.
func TestCIInfraRetriesRunOut(t *testing.T) {
	e := newCIEnv(t, "sha1")
	one := int32(1)
	e.setInfra(func(c *aiv1.CIInfraSpec) { c.MaxRetries = &one })
	pid := infraJobs(e.gl, "sha1", "agent/42", []gitlab.Job{failedJob("test", "runner_system_failure")})

	e.reconcile() // judged: retry due
	e.after(2 * time.Minute)
	e.reconcile() // retried
	e.gl.SetPipelineStatus(pid, "failed")
	e.reconcile() // red again, retries used up
	if len(e.gl.TraceReads) != 0 {
		t.Fatalf("a runner failure reason reads no log: %v", e.gl.TraceReads)
	}
	task := e.ciTask()
	if e.ciState() != aiv1.CIStateFailed || task.Spec.RunRequest != 0 || task.Status.Run.NeedsInputCause != aiv1.NeedsInputCIRed {
		t.Fatalf("held for red CI, no fix run: state=%s request=%d cause=%q", e.ciState(), task.Spec.RunRequest, task.Status.Run.NeedsInputCause)
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::needs-input"}) {
		t.Fatalf("expected needs-input, has %v", labels)
	}
	if n := e.notes(); !strings.Contains(n, "CI is still red because of the runner, not the code, after 1 retries") || !strings.Contains(n, "test: runner_system_failure") {
		t.Fatalf("unexpected note %q", n)
	}

	// Someone fixes the runner and retries the pipeline: the hold lifts.
	mr := e.ciMR("sha1")
	mr.HeadPipeline = &gitlab.Pipeline{ID: pid}
	e.gl.SetPipelineStatus(pid, "success")
	e.reconcile()
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::review"}) {
		t.Fatalf("a green head pipeline lifts the hold, has %v", labels)
	}
}

// One infra and one code failure is a normal red pipeline; judging stops at
// the code failure, before reading the other log.
func TestCIInfraMixedFailureIsNormal(t *testing.T) {
	e := newCIEnv(t, "sha1")
	pid := infraJobs(e.gl, "sha1", "agent/42", []gitlab.Job{failedJob("test", "script_failure"), failedJob("build:client", "script_failure")},
		"--- FAIL: TestCheckout", "getaddrinfo EAI_AGAIN registry.npmjs.org")
	e.reconcile()
	if !slices.Equal(e.gl.Retried, []int64{pid}) || e.ciTask().Status.CI.Infra != nil || len(e.gl.TraceReads) != 1 {
		t.Fatalf("the flaky retry, no infra retry, one log read: retried=%v %+v reads=%v", e.gl.Retried, e.ciTask().Status.CI.Infra, e.gl.TraceReads)
	}
	if e.infraNotes() != 0 {
		t.Fatalf("no infra note: %q", e.notes())
	}
}

// maxRetries 0 turns the check off; custom log patterns replace the defaults.
func TestCIInfraOffAndCustomPatterns(t *testing.T) {
	for name, set := range map[string]func(*aiv1.CIInfraSpec){
		"off":    func(c *aiv1.CIInfraSpec) { zero := int32(0); c.MaxRetries = &zero },
		"custom": func(c *aiv1.CIInfraSpec) { c.LogPatterns = []string{"quota exceeded"} },
	} {
		t.Run(name, func(t *testing.T) {
			e := newCIEnv(t, "sha1")
			e.setInfra(set)
			pid := infraJobs(e.gl, "sha1", "agent/42", []gitlab.Job{failedJob("build:client", "script_failure")}, "getaddrinfo EAI_AGAIN")
			e.reconcile()
			if !slices.Equal(e.gl.Retried, []int64{pid}) || e.ciTask().Status.CI.Infra != nil {
				t.Fatalf("today's flaky retry: retried=%v %+v", e.gl.Retried, e.ciTask().Status.CI.Infra)
			}
			if name == "off" && len(e.gl.TraceReads) != 0 {
				t.Fatalf("off reads no log: %v", e.gl.TraceReads)
			}
		})
	}
}

// The companion's pipeline gets the same handling, with a note naming it.
func TestCIInfraCompanion(t *testing.T) {
	e := newCIEnv(t, "sha1")
	infra := withCompanion(e)
	e.companionPushed(ciTask, "isha1", 0, true)
	e.gl.AddPipeline("sha1", "agent/42", "success")
	ipid := infraJobs(infra, "isha1", "agent/app-42-add-paypal-button", []gitlab.Job{failedJob("plan", "script_failure")},
		"dial tcp: lookup registry.terraform.io: Temporary failure in name resolution")
	e.reconcile()
	cc := e.ciTask().Status.CI.Companion
	if cc.Infra == nil || cc.Infra.DueAt == nil || len(infra.Retried) != 0 {
		t.Fatalf("the companion's infra retry waits: %+v retried=%v", cc, infra.Retried)
	}
	if n := e.notes(); e.infraNotes() != 1 || !strings.Contains(n, "of the companion repository group/infra failed because of the runner") {
		t.Fatalf("unexpected notes %q", n)
	}
	e.after(2 * time.Minute)
	e.reconcile()
	if !slices.Equal(infra.Retried, []int64{ipid}) || e.ciTask().Spec.RunRequest != 0 {
		t.Fatalf("retried in the companion project, no fix run: retried=%v request=%d", infra.Retried, e.ciTask().Spec.RunRequest)
	}
}

func TestInfraFailureMatchesTheTailCaseInsensitively(t *testing.T) {
	gl := gitlabfake.New()
	spec := *aiv1.CISpec{}.WithDefaults().InfraFailures
	pad := strings.Repeat("x", infraLogTail)
	for _, c := range []struct {
		trace string
		want  bool
	}{
		{pad + "Error Resolving DNS for runner", true},
		{"error resolving DNS\n" + pad, false}, // before the tail
		{"exit code 1", false},
	} {
		pid := gl.AddPipeline("s", "r", "failed", failedJob("job", "script_failure"))
		gl.Traces[gl.Jobs[pid][0].ID] = c.trace
		sig, ok, err := infraFailure(context.Background(), gl, spec, "g/p", gl.Jobs[pid])
		if err != nil || ok != c.want || ok && sig != "job: error resolving DNS" {
			t.Errorf("trace %.30q…: %q %v %v, want %v", c.trace[len(c.trace)-min(len(c.trace), 30):], sig, ok, err, c.want)
		}
	}
}

// A due retry belongs to the pipeline that was judged: a newer red pipeline
// of the commit is judged itself, not retried as a runner failure.
func TestCIInfraDueRetryIsForTheJudgedPipeline(t *testing.T) {
	e := newCIEnv(t, "sha1")
	infraJobs(e.gl, "sha1", "agent/42", []gitlab.Job{failedJob("build:client", "script_failure")}, "getaddrinfo EAI_AGAIN")
	e.reconcile() // judged: retry due
	pid2 := infraJobs(e.gl, "sha1", "agent/42", []gitlab.Job{failedJob("test", "script_failure")}, "--- FAIL: TestCheckout")
	e.after(2 * time.Minute)
	e.reconcile()
	in := e.ciTask().Status.CI.Infra
	if !slices.Equal(e.gl.Retried, []int64{pid2}) || in.Retries != 0 || in.DueAt != nil || len(e.gl.TraceReads) != 2 {
		t.Fatalf("the new pipeline is judged a code failure (flaky retry): retried=%v %+v reads=%v", e.gl.Retried, in, e.gl.TraceReads)
	}
}

// A companion held for the runner waits for the home pipeline, without
// reading its logs again, and the loop ends in needs-input once home is done.
func TestCIInfraHeldCompanionWaitsForHome(t *testing.T) {
	e := newCIEnv(t, "sha1")
	infra := withCompanion(e)
	one := int32(1)
	e.setInfra(func(c *aiv1.CIInfraSpec) { c.MaxRetries = &one })
	e.companionPushed(ciTask, "isha1", 0, true)
	hpid := e.gl.AddPipeline("sha1", "agent/42", "running")
	ipid := infraJobs(infra, "isha1", "agent/app-42-add-paypal-button", []gitlab.Job{failedJob("plan", "script_failure")}, "Temporary failure in name resolution")
	e.reconcile() // judged: retry due
	e.after(2 * time.Minute)
	e.reconcile() // retried
	infra.SetPipelineStatus(ipid, "failed")
	e.reconcile() // retries used up, home still runs
	e.reconcile()
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::working"}) || len(infra.TraceReads) != 2 {
		t.Fatalf("held companion waits for home, log read once per judgement: labels %v reads %v", labels, infra.TraceReads)
	}
	e.gl.SetPipelineStatus(hpid, "success")
	e.reconcile()
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::needs-input"}) || e.ciTask().Spec.RunRequest != 0 {
		t.Fatalf("held for the runner once home is green: labels %v request %d", labels, e.ciTask().Spec.RunRequest)
	}
	if n := e.notes(); !strings.Contains(n, "CI of the companion repository group/infra is still red because of the runner") || len(infra.TraceReads) != 2 {
		t.Fatalf("notes %q reads %v", n, infra.TraceReads)
	}
}
