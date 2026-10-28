package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

func TestClassifyLimit(t *testing.T) {
	limited := []string{
		"claude reported an error: Claude AI usage limit reached|1760000000",
		"5-hour limit reached ∙ resets 3pm",
		`API Error: 429 {"type":"error","error":{"type":"rate_limit_error"}}`,
		`API Error: 529 {"type":"error","error":{"type":"overloaded_error"}}`,
		"You've hit your usage limit for this billing period",
		"Too Many Requests",
	}
	for _, msg := range limited {
		err := classifyLimit(aiv1.EngineClaude, errors.New(msg), nil)
		if _, ok := AsUsageLimit(err); !ok {
			t.Errorf("%q should be a usage limit", msg)
		}
	}
	for _, msg := range []string{"exit status 1: tests failed", "invalid model: does-not-exist", "context deadline exceeded", "server.js:429: unexpected token"} {
		if _, ok := AsUsageLimit(classifyLimit(aiv1.EngineClaude, errors.New(msg), nil)); ok {
			t.Errorf("%q must not be a usage limit", msg)
		}
	}
	// A successful answer that talks about rate limits is not a limit.
	if err := classifyLimit(aiv1.EngineClaude, nil, &Result{Text: "added rate limiting (429)"}); err != nil {
		t.Errorf("successful results are never limits: %v", err)
	}
	// The limit text may only be in the result of a failed invocation.
	err := classifyLimit(aiv1.EngineCursor, errors.New("cursor-agent reported an error"), &Result{Text: "Usage limit reached"})
	lim, ok := AsUsageLimit(err)
	if !ok || lim.Engine != aiv1.EngineCursor {
		t.Fatalf("expected cursor limit, got %v", err)
	}
	// Claude's reset epoch is parsed.
	lim, _ = AsUsageLimit(classifyLimit(aiv1.EngineClaude, errors.New("Claude AI usage limit reached|1760000000"), nil))
	if !lim.ResetAt.Equal(time.Unix(1760000000, 0)) {
		t.Fatalf("reset time not parsed: %v", lim.ResetAt)
	}
	// Cursor's reset date (its real message) is the start of that day, UTC.
	cursor := "cursor-agent failed: exit status 1: ActionRequiredError: You've hit your usage limit You've saved $496 on API model usage this month with Pro. " +
		"Switch to a different model or set a Spend Limit to continue with Auto. Your usage limits will reset when your monthly cycle ends on 10/13/2026."
	lim, _ = AsUsageLimit(classifyLimit(aiv1.EngineCursor, errors.New(cursor), nil))
	if want := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC); !lim.ResetAt.Equal(want) {
		t.Fatalf("cursor reset date: got %v, want %v", lim.ResetAt, want)
	}
	// Claude's epoch wins over a date in the text.
	lim, _ = AsUsageLimit(classifyLimit(aiv1.EngineClaude, errors.New("usage limit reached|1760000000, resets 11/01/2026"), nil))
	if !lim.ResetAt.Equal(time.Unix(1760000000, 0)) {
		t.Fatalf("the epoch must win: %v", lim.ResetAt)
	}
	// A limit without a date has no reset time.
	lim, _ = AsUsageLimit(classifyLimit(aiv1.EngineCursor, errors.New("You've hit your usage limit"), nil))
	if !lim.ResetAt.IsZero() {
		t.Fatalf("no date, no reset time: %v", lim.ResetAt)
	}
}

// limitedEngine fails with a usage limit for selected invocations and
// delegates everything else to the scripted engine.
type limitedEngine struct {
	mu      sync.Mutex
	next    Engine
	limitIf func(inv Invocation) bool
	message string
	calls   int
	limited int
}

func (l *limitedEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	l.mu.Lock()
	l.calls++
	hit := l.limitIf(inv)
	if hit {
		l.limited++
	}
	l.mu.Unlock()
	if hit {
		return &Result{IsError: true, Text: l.message}, errors.New("claude reported an error: " + l.message)
	}
	return l.next.Run(ctx, inv)
}

type limitEnv struct {
	t      *testing.T
	k8s    client.Client
	gl     *gitlabfake.Fake
	r      *Runner
	now    time.Time
	origin string
	tmp    string
}

func newLimitEnv(t *testing.T, engine Engine) *limitEnv {
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

	gl := gitlabfake.New()
	gl.Project.HTTPURLToRepo = origin
	gl.AddIssue(42, "Add PayPal button", "agent::working")

	scheme := runtime.NewScheme()
	_ = aiv1.AddToScheme(scheme)
	project := &aiv1.AgentProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: "agents"},
		Spec: aiv1.AgentProjectSpec{
			GitLab: aiv1.GitLabSpec{Project: "group/app", TokenSecretRef: corev1.SecretKeySelector{Key: "t"}},
		},
		Status: aiv1.AgentProjectStatus{BotUserID: gl.Bot.ID},
	}
	task := &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-42-add-paypal-button", Namespace: "agents"},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "shop", Type: aiv1.TaskTypeImplement, RunRequest: 1,
			Issue: &aiv1.IssueRef{IID: 42, Title: "Add PayPal button"}},
	}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, task).
		WithStatusSubresource(&aiv1.AgentTask{}, &aiv1.AgentProject{}).Build()
	e := &limitEnv{t: t, k8s: k8s, gl: gl, origin: origin, tmp: tmp, now: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
	e.r = newTestRunner(t, tmp, k8s, gl, engine, task.Name)
	e.r.Now = func() time.Time { return e.now }
	return e
}

func (e *limitEnv) task() aiv1.AgentTask {
	e.t.Helper()
	var task aiv1.AgentTask
	if err := e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: "shop-42-add-paypal-button"}, &task); err != nil {
		e.t.Fatal(err)
	}
	return task
}

func (e *limitEnv) step() bool {
	e.t.Helper()
	ran, err := e.r.Step(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return ran
}

// issueNotes are the notes on the issue, without the pointer to the merge
// request every first run posts.
func (e *limitEnv) issueNotes() []string {
	var out []string
	for _, n := range e.gl.IssueNotes[42] {
		if !strings.Contains(n.Body, "Working on this in") {
			out = append(out, n.Body)
		}
	}
	return out
}

// usageEngine reports a usage reading with every result, like claude.
type usageEngine struct {
	next    Engine
	percent int32
}

func (u *usageEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	res, err := u.next.Run(ctx, inv)
	if res != nil {
		res.Usage = &aiv1.EngineUsage{Status: "allowed", Windows: []aiv1.UsageWindow{{Name: "five_hour", Percent: u.percent}}}
	}
	return res, err
}

func TestRunRecordsEngineUsage(t *testing.T) {
	engine := &usageEngine{next: &scriptedEngine{}, percent: 15}
	e := newLimitEnv(t, engine)
	if !e.step() {
		t.Fatal("expected a run")
	}
	usage := e.task().Status.Run.EngineUsage
	if len(usage) != 1 || usage[0].Engine != aiv1.EngineClaude || usage[0].Status != "allowed" ||
		usage[0].Windows[0].Percent != 15 || !usage[0].ObservedAt.Time.Equal(e.now) {
		t.Fatalf("expected claude's reading on the task, got %+v", usage)
	}
	if got := e.r.projectStatus.EngineUsage; len(got) != 1 || got[0].Windows[0].Percent != 15 {
		t.Fatalf("the run's project status must have the reading too, got %+v", got)
	}
}

func TestUsageLimitDefersRun(t *testing.T) {
	reset := time.Date(2026, 10, 16, 15, 0, 0, 0, time.UTC)
	engine := &limitedEngine{
		next:    &scriptedEngine{},
		message: "Claude AI usage limit reached|" + strconv.FormatInt(reset.Unix(), 10),
	}
	engine.limitIf = func(Invocation) bool { return engine.limited == 0 }
	e := newLimitEnv(t, engine)

	if e.step() {
		t.Fatal("a deferred run must not count as a run")
	}
	run := e.task().Status.Run
	if run.LastResult != aiv1.RunResultDeferred || run.DeferredUntil == nil || !run.DeferredUntil.Time.Equal(reset) ||
		run.LimitedEngine != aiv1.EngineClaude || run.Deferrals != 1 || run.BlockReason != aiv1.BlockQuota {
		t.Fatalf("unexpected run status %+v", run)
	}
	if run.ObservedRunRequest != 0 || run.RunCount != 0 || run.ActiveRunRequest != 0 {
		t.Fatalf("the request must stay pending and nothing in progress: %+v", run)
	}
	// The MR opened before the limit is recorded, so the retry reuses it.
	if run.MergeRequestIID != 1 || run.Branch != "agent/42-add-paypal-button" {
		t.Fatalf("the early MR must be recorded: %+v", run)
	}
	if notes := e.gl.MRNotes[1]; len(notes) != 1 || !strings.Contains(notes[0].Body, "⏸️ Paused by a usage limit") {
		t.Fatalf("the progress comment should say the run is paused: %+v", notes)
	}
	notes := e.issueNotes()
	if len(notes) != 1 || !strings.Contains(notes[0], "usage limit") || !strings.Contains(notes[0], "2026-10-16 15:00") ||
		!strings.Contains(notes[0], "blocked (`agent-blocked::quota`)") {
		t.Fatalf("expected one waiting note, got %q", notes)
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::working"}) {
		t.Fatalf("the operator blocks the issue, the runner leaves the labels: has %v", labels)
	}

	// Before the reset nothing is attempted.
	calls := engine.calls
	e.now = reset.Add(-time.Minute)
	if e.step() || engine.calls != calls {
		t.Fatal("the runner must wait for the reset time")
	}

	// After the reset the run happens and the deferral is cleared.
	e.now = reset.Add(time.Minute)
	if !e.step() {
		t.Fatal("the run should happen after the reset")
	}
	run = e.task().Status.Run
	if run.LastResult != aiv1.RunResultSucceeded || run.DeferredUntil != nil || run.Deferrals != 0 || run.LimitedEngine != "" || run.RunCount != 1 ||
		run.BlockReason != "" {
		t.Fatalf("deferral not cleared after a successful run: %+v", run)
	}
	if len(e.gl.MRs) != 1 {
		t.Fatal("the run should have opened a merge request")
	}
}

func TestUsageLimitBackoffWithoutResetTime(t *testing.T) {
	engine := &limitedEngine{next: &scriptedEngine{}, message: `API Error: 429 {"error":{"type":"rate_limit_error"}}`}
	engine.limitIf = func(Invocation) bool { return true }
	e := newLimitEnv(t, engine)

	start := e.now
	e.step()
	if got := e.task().Status.Run.DeferredUntil.Time; !got.Equal(start.Add(5 * time.Minute)) {
		t.Fatalf("first backoff should be 5m, got %v", got.Sub(start))
	}
	e.now = start.Add(6 * time.Minute)
	e.step()
	run := e.task().Status.Run
	if got := run.DeferredUntil.Time.Sub(e.now); got != 10*time.Minute || run.Deferrals != 2 {
		t.Fatalf("second backoff should be 10m, got %v (deferrals %d)", got, run.Deferrals)
	}
	if n := len(e.issueNotes()); n != 1 {
		t.Fatalf("only the first deferral is announced on the issue, got %d notes", n)
	}
}

func TestProjectWideLimitSkipsEngine(t *testing.T) {
	engine := &limitedEngine{next: &scriptedEngine{}, limitIf: func(Invocation) bool { return false }}
	e := newLimitEnv(t, engine)
	var project aiv1.AgentProject
	_ = e.k8s.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: "shop"}, &project)
	until := metav1.NewTime(e.now.Add(2 * time.Hour))
	project.Status.UsageLimits = []aiv1.UsageLimit{{Engine: aiv1.EngineClaude, Until: until, Message: "limit"}}
	if err := e.k8s.Status().Update(context.Background(), &project); err != nil {
		t.Fatal(err)
	}

	e.step()
	run := e.task().Status.Run
	if engine.calls != 0 {
		t.Fatalf("a limited engine must not be called, got %d calls", engine.calls)
	}
	if run.LastResult != aiv1.RunResultDeferred || !run.DeferredUntil.Time.Equal(until.Time) || run.BlockReason != aiv1.BlockQuota {
		t.Fatalf("run should wait for the project-wide limit: %+v", run)
	}
}

func TestUsageLimitDuringFixPublishesWork(t *testing.T) {
	engine := &limitedEngine{next: &scriptedEngine{}, message: "Claude AI usage limit reached"}
	engine.limitIf = func(inv Invocation) bool { return strings.Contains(inv.Prompt, "Automated reviewers found") }
	e := newLimitEnv(t, engine)

	if !e.step() {
		t.Fatal("the run should complete")
	}
	files := gitCmd(t, e.tmp, "--git-dir", e.origin, "ls-tree", "--name-only", "agent/42-add-paypal-button")
	if !strings.Contains(files, "paypal.go") {
		t.Fatalf("the implementation must be pushed despite the limit: %s", files)
	}
	if engine.limited != 1 {
		t.Fatalf("the fix round should have hit the limit once, got %d", engine.limited)
	}
	mr := e.gl.MRs[1]
	if mr == nil {
		t.Fatal("expected a merge request")
	}
	run := e.task().Status.Run
	if run.LastResult != aiv1.RunResultSucceeded || run.DeferredUntil != nil {
		t.Fatalf("unexpected run status %+v", run)
	}
	b, _ := json.Marshal(e.gl.IssueNotes[42])
	if !strings.Contains(string(b), "Working on this in https://gitlab.example/group/app/-/merge_requests/1") {
		t.Fatalf("issue should link the MR: %s", b)
	}
}

func TestTooManyDeferralsFail(t *testing.T) {
	engine := &limitedEngine{next: &scriptedEngine{}, message: "Claude AI usage limit reached"}
	engine.limitIf = func(Invocation) bool { return true }
	e := newLimitEnv(t, engine)
	task := e.task()
	orig := task.DeepCopy()
	task.Status.Run.Deferrals = MaxDeferrals
	if err := e.k8s.Status().Patch(context.Background(), &task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	orig = task.DeepCopy()
	task.Spec.Owners = []string{"alice"}
	if err := e.k8s.Patch(context.Background(), &task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	if !e.step() {
		t.Fatal("after too many deferrals the run is reported as failed")
	}
	run := e.task().Status.Run
	if run.LastResult != aiv1.RunResultFailed || run.DeferredUntil != nil || run.Deferrals != 0 || !strings.Contains(run.Summary, "giving up") ||
		run.BlockReason != aiv1.BlockQuota {
		t.Fatalf("unexpected run status %+v", run)
	}
	if labels := e.gl.Labels(42); !slices.Equal(labels, []string{"agent::working"}) {
		t.Fatalf("the operator keeps the issue blocked (quota), not needs-input; has %v", labels)
	}
	notes := e.issueNotes()
	if len(notes) != 1 || strings.Count(notes[0], "cc @alice") != 1 || !strings.Contains(notes[0], "blocked (`agent-blocked::quota`) and waits for you") {
		t.Fatalf("one comment that says it stays blocked and pings once: %q", notes)
	}
}
