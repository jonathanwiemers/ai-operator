package controller

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// newMainEnv returns a project env whose clock moves only with advance.
func newMainEnv(t *testing.T) (*projectEnv, func(time.Duration)) {
	e := newProjectEnv(t)
	now := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	e.r.Now = func() time.Time { return now }
	return e, func(d time.Duration) { now = now.Add(d) }
}

func (e *projectEnv) mainHealth() aiv1.MainHealthStatus {
	e.t.Helper()
	var p aiv1.AgentProject
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &p); err != nil {
		e.t.Fatal(err)
	}
	if p.Status.MainHealth == nil {
		e.t.Fatal("no main health status")
	}
	return *p.Status.MainHealth
}

// issuesWith returns the open issues with label, by IID.
func (e *projectEnv) issuesWith(label string) []*gitlab.Issue {
	var out []*gitlab.Issue
	for _, is := range e.gl.Issues {
		if is.State == "opened" && is.HasLabel(label) {
			out = append(out, is)
		}
	}
	slices.SortFunc(out, func(a, b *gitlab.Issue) int { return int(a.IID - b.IID) })
	return out
}

// check advances the clock by one interval and polls.
func (e *projectEnv) check(advance func(time.Duration)) {
	e.t.Helper()
	advance(aiv1.DefaultMainHealthInterval)
	e.reconcile()
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

func coverageJob() gitlab.Job {
	return gitlab.Job{Name: "test:coverage", Stage: "test", Status: "failed"}
}

func TestMainHealthGreenStaysQuiet(t *testing.T) {
	e, advance := newMainEnv(t)
	e.gl.AddPipeline("sha1", "main", "success")
	e.gl.AddPipeline("other", "feature", "failed", coverageJob())
	e.reconcile()
	e.check(advance)
	if st := e.mainHealth(); st.State != aiv1.MainHealthGreen {
		t.Fatalf("state %s", st.State)
	}
	if len(e.gl.Retried) != 0 || len(e.gl.Issues) != 0 {
		t.Fatalf("a green default branch needs nothing, retried %v, issues %d", e.gl.Retried, len(e.gl.Issues))
	}
}

func TestMainHealthFlakyJob(t *testing.T) {
	e, advance := newMainEnv(t)
	lint := gitlab.Job{Name: "lint", Status: "failed", AllowFailure: true}
	pid := e.gl.AddPipeline("sha1", "main", "failed", coverageJob(), lint)
	e.reconcile()
	if st := e.mainHealth(); st.State != aiv1.MainHealthRetrying || !slices.Equal(st.FailedJobs, []string{"test:coverage"}) {
		t.Fatalf("a red pipeline is retried first, status %+v", st)
	}
	if !slices.Equal(e.gl.Retried, []int64{pid}) {
		t.Fatalf("retried %v", e.gl.Retried)
	}
	e.gl.SetPipelineStatus(pid, "success")
	e.check(advance)
	if st := e.mainHealth(); st.State != aiv1.MainHealthFlaky {
		t.Fatalf("green after the retry is flaky, state %s", st.State)
	}
	if len(e.issuesWith("ci::main-red")) != 0 {
		t.Fatal("a flaky job is no red default branch")
	}
}

func TestMainHealthRedIssue(t *testing.T) {
	e, advance := newMainEnv(t)
	e.setNotify(aiv1.NotifySpec{Users: []string{"alice"}})
	var log strings.Builder
	log.WriteString("early line\n")
	for range 40 {
		log.WriteString("\x1b[31mFAIL\x1b[0m replays logged shots\n")
	}
	log.WriteString("```\n@bob see\n/close\n")
	job := coverageJob()
	job.ID = 7
	e.gl.Traces[7] = log.String()
	pid := e.gl.AddPipeline("sha1", "main", "failed", job)
	e.reconcile()
	e.gl.SetPipelineStatus(pid, "failed")
	e.check(advance)

	st := e.mainHealth()
	red := e.issuesWith("ci::main-red")
	if st.State != aiv1.MainHealthRed || len(red) != 1 || st.IssueIID != red[0].IID {
		t.Fatalf("still red after the retry opens the main-red issue, status %+v, issues %+v", st, red)
	}
	is := red[0]
	if is.Title != "🚑 `main` is red: `test:coverage`" || !is.HasLabel("ai-generated") {
		t.Fatalf("issue %q %v", is.Title, is.Labels)
	}
	for _, want := range []string{"/pipelines/" + itoa(pid), "`sha1`", "FAIL replays logged shots", "cc @alice"} {
		if !strings.Contains(is.Description, want) {
			t.Errorf("description lacks %q:\n%s", want, is.Description)
		}
	}
	for _, bad := range []string{"early line", "\x1b", "```\n@bob", "@bob", "\n/close"} {
		if strings.Contains(is.Description, bad) {
			t.Errorf("description contains %q:\n%s", bad, is.Description)
		}
	}
	if len(e.gl.IssueNotes[is.IID]) != 0 {
		t.Fatal("a new issue needs no comment")
	}

	// Another red pipeline with the same jobs: nothing new to say.
	pid2 := e.gl.AddPipeline("sha2", "main", "failed", coverageJob())
	e.check(advance)
	e.gl.SetPipelineStatus(pid2, "failed")
	e.check(advance)
	if st := e.mainHealth(); st.State != aiv1.MainHealthRed || st.PipelineID != pid2 {
		t.Fatalf("status %+v", st)
	}
	if n := len(e.gl.IssueNotes[is.IID]); n != 0 {
		t.Fatalf("same failing jobs must not comment, got %d comments", n)
	}

	// Other jobs fail: a comment, without a new ping.
	e.gl.AddPipeline("sha3", "main", "failed", gitlab.Job{Name: "build", Status: "failed"})
	e.setMainHealth(aiv1.MainHealthSpec{RetryFailedJobs: new(false)})
	e.check(advance)
	notes := e.gl.IssueNotes[is.IID]
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "`build`") || strings.Contains(notes[0].Body, "@alice") {
		t.Fatalf("expected one comment on the new failing job, got %+v", notes)
	}
	if is.Title != "🚑 `main` is red: `build`" {
		t.Fatalf("title %q", is.Title)
	}

	// Green again closes the issue.
	e.gl.AddPipeline("sha4", "main", "success")
	e.check(advance)
	if st := e.mainHealth(); st.State != aiv1.MainHealthGreen || st.IssueIID != 0 {
		t.Fatalf("status %+v", st)
	}
	notes = e.gl.IssueNotes[is.IID]
	if is.State != "closed" || !strings.Contains(notes[len(notes)-1].Body, "✅ green again at `sha4`") {
		t.Fatalf("issue %s, last comment %q", is.State, notes[len(notes)-1].Body)
	}
}

func (e *projectEnv) setMainHealth(m aiv1.MainHealthSpec) {
	e.t.Helper()
	var p aiv1.AgentProject
	if err := e.c.Get(e.ctx, types.NamespacedName{Namespace: ns, Name: "shop"}, &p); err != nil {
		e.t.Fatal(err)
	}
	p.Spec.MainHealth = m
	if err := e.c.Update(e.ctx, &p); err != nil {
		e.t.Fatal(err)
	}
}

func TestMainHealthInterval(t *testing.T) {
	e, advance := newMainEnv(t)
	e.gl.AddPipeline("sha1", "main", "success")
	e.reconcile()
	e.gl.AddPipeline("sha2", "main", "failed", coverageJob())
	advance(4 * time.Minute)
	e.reconcile()
	if len(e.gl.Retried) != 0 {
		t.Fatal("checked again before the interval passed")
	}
	advance(time.Minute)
	e.reconcile()
	if len(e.gl.Retried) != 1 {
		t.Fatal("not checked after the interval")
	}
}

func TestMainHealthFindsItsIssue(t *testing.T) {
	e, advance := newMainEnv(t)
	e.reconcile() // resolves the bot user
	human := e.gl.AddIssue(5, "main is broken", "ci::main-red")
	human.Description = mainRedMarker + " copied by a human"
	own := e.gl.AddIssue(6, "🚑 `main` is red: `test:coverage`", "ci::main-red")
	own.Description, own.Author = mainRedMarker+"\nolder report", e.gl.Bot
	pid := e.gl.AddPipeline("sha1", "main", "failed", coverageJob())
	e.check(advance)
	e.gl.SetPipelineStatus(pid, "failed")
	e.check(advance)
	if st := e.mainHealth(); st.IssueIID != 6 {
		t.Fatalf("expected the bot's issue #6, status %+v", st)
	}
	if len(e.gl.Issues) != 2 || len(e.gl.IssueNotes[6]) != 1 || len(e.gl.IssueNotes[5]) != 0 {
		t.Fatalf("expected a comment on #6 only, issues %d, notes %v", len(e.gl.Issues), e.gl.IssueNotes)
	}
}

func TestMainHealthRetriesAFailedReport(t *testing.T) {
	e, advance := newMainEnv(t)
	e.gl.CreateIssueError = &gitlab.Error{StatusCode: 500, Method: "POST", Path: "issues"}
	e.setMainHealth(aiv1.MainHealthSpec{RetryFailedJobs: new(false)})
	e.gl.AddPipeline("sha1", "main", "failed", coverageJob())
	advance(time.Minute)
	if _, err := e.r.Reconcile(e.ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: "shop"}}); err != nil {
		t.Fatal(err)
	}
	if len(e.gl.Issues) != 0 {
		t.Fatal("the issue was created")
	}
	e.gl.CreateIssueError = nil
	e.check(advance)
	if st := e.mainHealth(); st.State != aiv1.MainHealthRed || len(e.issuesWith("ci::main-red")) != 1 {
		t.Fatalf("the report is tried again on the next check, status %+v", st)
	}
}

func TestJobName(t *testing.T) {
	for in, want := range map[string]string{
		"test:coverage":              "test:coverage",
		"a`@b <!-- x --> /close":     "ab -- x -- /close",
		strings.Repeat("ab", 40):     strings.Repeat("ab", 30),
		"build [linux, 1/2] ünicode": "build linux 1/2 nicode",
	} {
		if got := jobName(in); got != want {
			t.Errorf("jobName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMainHealthRetryRefusedIsRed(t *testing.T) {
	e, _ := newMainEnv(t)
	e.gl.RetryError = &gitlab.Error{StatusCode: 403, Method: "POST", Path: "retry"}
	e.gl.AddPipeline("sha1", "main", "failed", coverageJob())
	e.reconcile()
	if st := e.mainHealth(); st.State != aiv1.MainHealthRed || len(e.issuesWith("ci::main-red")) != 1 {
		t.Fatalf("a pipeline that cannot be retried is red right away, status %+v", st)
	}
}

func TestMainHealthIssueSize(t *testing.T) {
	e, _ := newMainEnv(t)
	e.setMainHealth(aiv1.MainHealthSpec{RetryFailedJobs: new(false)})
	var jobs []gitlab.Job
	for i := range 5 {
		j := gitlab.Job{ID: int64(100 + i), Name: strings.Repeat("x", 59) + strconv.Itoa(i), Status: "failed"}
		e.gl.Traces[j.ID] = "log of " + strconv.Itoa(i) + "\n" + strings.Repeat("é", 1<<20)
		jobs = append(jobs, j)
	}
	e.gl.AddPipeline("sha1", "main", "failed", jobs...)
	e.reconcile()
	red := e.issuesWith("ci::main-red")
	if len(red) != 1 {
		t.Fatal("no main-red issue")
	}
	if n := len([]rune(red[0].Title)); n > 255 || !strings.HasSuffix(red[0].Title, " and 2 more") {
		t.Fatalf("title of %d characters: %q", n, red[0].Title)
	}
	if d := red[0].Description; len(d) > 20000 || strings.Count(d, "```text") != 3 || !utf8.ValidString(d) {
		t.Fatalf("description of %d bytes with %d logs", len(d), strings.Count(d, "```text"))
	}
}
