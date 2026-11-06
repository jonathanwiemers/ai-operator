package runner

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

const lostStatus = "Todo list:\n- [x] model\n- [>] wip.go\n- [ ] tests"

// killEngine loses the agent container in its first implementer call, after
// editing wip.go and keeping a status as the agent server does: the operator
// records the termination (reason) and the connection breaks. Later calls
// go to next; the first of them records what it finds.
type killEngine struct {
	k8s    client.Client
	origin string
	reason string
	next   Engine

	calls int
	// inFlight is the state's marker during the first call.
	inFlight *inFlight
	// sawWip and head are what the second implementer call found: wip.go in
	// the work tree, and the subject of origin's branch head.
	sawWip bool
	head   string
}

func (k *killEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	if inv.ReadOnly {
		return k.next.Run(ctx, inv)
	}
	k.calls++
	switch k.calls {
	case 1:
		if st, err := (&Runner{StateDir: filepath.Join(filepath.Dir(filepath.Dir(inv.Dir)), "runner")}).loadState(); err == nil {
			k.inFlight = st.InFlight
		}
		_ = os.WriteFile(filepath.Join(inv.Dir, "wip.go"), []byte("package app\n\nfunc wip() {}\n"), 0o644)
		_ = os.WriteFile(filepath.Join(inv.Dir, ".git", lastStatusFile), []byte(lostStatus), 0o600)
		var task aiv1.AgentTask
		_ = k.k8s.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "shop-42-add-paypal-button"}, &task)
		orig := task.DeepCopy()
		restarts := int32(1)
		if t := task.Status.AgentTermination; t != nil {
			restarts = t.Restarts + 1
		}
		task.Status.AgentTermination = &aiv1.ContainerTermination{Reason: k.reason, ExitCode: 137,
			FinishedAt: &metav1.Time{Time: time.Now()}, Restarts: restarts, MemoryLimit: "4Gi"}
		_ = k.k8s.Status().Patch(ctx, &task, client.MergeFrom(orig))
		panic(http.ErrAbortHandler)
	case 2:
		_, err := os.Stat(filepath.Join(inv.Dir, "wip.go"))
		k.sawWip = err == nil
		out, _ := exec.Command("git", "--git-dir", k.origin, "log", "-1", "--format=%s", cpBranch).Output()
		k.head = strings.TrimSpace(string(out))
	}
	return k.next.Run(ctx, inv)
}

// checkpointCommit returns origin's commit with subject, failing if there is
// none, with its full message. A checkpoint the next step folded is no
// longer on the branch, but origin still has its commit.
func (e *limitEnv) checkpointCommit(subject string) (string, string) {
	e.t.Helper()
	lines := strings.Split(e.origin1("log", "--format=%H %s", cpBranch), "\n")
	for _, line := range strings.Split(e.origin1("fsck", "--unreachable", "--no-reflogs", "--no-progress"), "\n") {
		if sha, ok := strings.CutPrefix(line, "unreachable commit "); ok {
			lines = append(lines, e.origin1("log", "-1", "--format=%H %s", sha))
		}
	}
	for _, line := range lines {
		if sha, s, _ := strings.Cut(line, " "); s == subject {
			return sha, e.origin1("log", "-1", "--format=%B", sha)
		}
	}
	e.t.Fatalf("no commit %q on %s:\n%s", subject, cpBranch, e.origin1("log", "--format=%s", cpBranch))
	return "", ""
}

// checkFolded checks that the branch head is the step commit subject that
// folded the checkpoint cp (with wip.go), on the commit before cp, pushed
// over cp with the lease. It returns the head.
func (e *limitEnv) checkFolded(cp, subject string) string {
	e.t.Helper()
	head := e.origin1("rev-parse", cpBranch)
	if got := e.origin1("log", "-1", "--format=%s", head); got != subject {
		e.t.Fatalf("head %q, want the step's commit %q", got, subject)
	}
	if got, want := e.origin1("rev-parse", head+"^"), e.origin1("rev-parse", cp+"^"); got != want {
		e.t.Fatalf("the step's commit is on %s, want %s:\n%s", got, want, e.origin1("log", "--format=%s", cpBranch))
	}
	if strings.Contains(e.origin1("log", "--format=%B", cpBranch), "Agent-Checkpoint") {
		e.t.Fatalf("a checkpoint is still on the branch:\n%s", e.origin1("log", "--format=%s", cpBranch))
	}
	if !strings.Contains(e.origin1("ls-tree", "--name-only", head), "wip.go") {
		e.t.Fatal("the step's commit misses the checkpoint's changes")
	}
	if _, res := e.stepResult("fold checkpoints"); !strings.Contains(res, "replaces the checkpoint "+shortSHA(cp)+" (--force-with-lease)") {
		e.t.Fatalf("fold checkpoints step: %q (%v)", res, stepNames(e.r.run))
	}
	if run := e.task().Status.Run; run.Checkpoint != nil || run.PushedSHA != head {
		e.t.Fatalf("a folded checkpoint is gone from the status, the head is a push: %+v %q", run.Checkpoint, run.PushedSHA)
	}
	return head
}

func (e *limitEnv) savedState() state {
	e.t.Helper()
	st, err := e.r.loadState()
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

func TestCheckpointBeforeContainerRestartReset(t *testing.T) {
	fastAgentLost(t)
	e := newLimitEnv(t, nil)
	engine := &killEngine{k8s: e.k8s, origin: e.origin, reason: "Error", next: quietEngine{}}
	e.r = newTestRunner(t, e.tmp, e.k8s, e.gl, engine, "shop-42-add-paypal-button")
	e.r.Now = func() time.Time { return e.now }
	pushes := e.recordPushes()
	if !e.step() {
		t.Fatal("the run should happen")
	}
	if want := (inFlight{Run: 1, Step: 1, Branch: cpBranch, Kind: aiv1.CallKindImplement}); engine.inFlight == nil || *engine.inFlight != want {
		t.Fatalf("in flight during the call: %+v, want %+v", engine.inFlight, want)
	}
	run := e.task().Status.Run
	if engine.calls != 2 || run.LastResult != aiv1.RunResultSucceeded {
		t.Fatalf("the crash is tried once more: %d calls, %s (%s)", engine.calls, run.LastResult, run.Summary)
	}
	subject := "chore(wip): checkpoint #42 step 1 (container-restart)"
	sha, msg := e.checkpointCommit(subject)
	if !strings.Contains(msg, "Agent-Checkpoint: 1/1 container-restart") || !strings.Contains(msg, "- [>] wip.go") || !strings.Contains(msg, "wip.go | 3") {
		t.Fatalf("checkpoint message:\n%s", msg)
	}
	if line := pushOf(t, pushes, sha); !strings.HasPrefix(line, "[ci.skip,] ") {
		t.Fatalf("pushed without CI: %s", line)
	}
	// Pushed before the reset: the retried call has the files and the branch
	// head is the checkpoint.
	if !engine.sawWip || engine.head != subject {
		t.Fatalf("the retry continues from the checkpoint: wip.go %v, origin head %q", engine.sawWip, engine.head)
	}
	// The retry's commit folds the checkpoint this run pushed.
	e.checkFolded(sha, "Nothing")
	if _, err := os.Stat(filepath.Join(e.tmp, "agent", "repo", ".git", lastStatusFile)); !os.IsNotExist(err) {
		t.Fatalf("the status file goes with the checkpoint: %v", err)
	}
	if st := e.savedState(); st.InFlight != nil {
		t.Fatalf("the implementer part ended: %+v", st.InFlight)
	}
}

// lostPodEnv has finished a first run and left the workspace as a pod lost
// in the second run's implementer call leaves it: wip.go uncommitted, the
// agent's status kept, the call in flight in the state, and the run request
// active but not observed.
func lostPodEnv(t *testing.T, next Engine) (*limitEnv, *killEngine) {
	t.Helper()
	e := newLimitEnv(t, nil)
	engine := &killEngine{k8s: e.k8s, origin: e.origin, next: next}
	e.r = newTestRunner(t, e.tmp, e.k8s, e.gl, engine, "shop-42-add-paypal-button")
	e.r.Now = func() time.Time { return e.now }
	// The first run only answers (calls past 2); the new pod's first call is 2.
	engine.calls = 2
	if !e.step() {
		t.Fatal("first run should happen")
	}
	engine.calls = 1
	e.gl.MRPipelines, e.gl.BranchPipelines = nil, nil
	ws := filepath.Join(e.tmp, "agent", "repo")
	writeFile(t, ws, "wip.go", "package app\n\nfunc wip() {}\n")
	if err := os.WriteFile(filepath.Join(ws, ".git", lastStatusFile), []byte(lostStatus), 0o600); err != nil {
		t.Fatal(err)
	}
	st := e.savedState()
	st.InFlight = &inFlight{Run: 2, Step: 3, Branch: cpBranch, Kind: aiv1.CallKindImplement}
	if err := e.r.saveState(st); err != nil {
		t.Fatal(err)
	}
	e.requestRun(nil)
	return e, engine
}

// interrupt marks the task's run request as started by a pod that is gone.
func (e *limitEnv) interrupt() {
	e.t.Helper()
	task := e.task()
	orig := task.DeepCopy()
	task.Status.Run.ActiveRunRequest = task.Spec.RunRequest
	if err := e.k8s.Status().Patch(context.Background(), &task, client.MergeFrom(orig)); err != nil {
		e.t.Fatal(err)
	}
}

func TestLostPodLeavesCheckpointBeforePrepare(t *testing.T) {
	e, engine := lostPodEnv(t, quietEngine{})
	pushes := e.recordPushes()
	e.interrupt()
	if !e.step() {
		t.Fatal("the run should start again")
	}
	subject := "chore(wip): checkpoint #42 step 3 (pod-lost)"
	sha, msg := e.checkpointCommit(subject)
	if !strings.Contains(msg, "Agent-Checkpoint: 2/3 pod-lost") || !strings.Contains(msg, "- [>] wip.go") {
		t.Fatalf("checkpoint message:\n%s", msg)
	}
	if line := pushOf(t, pushes, sha); !strings.HasPrefix(line, "[ci.skip,] ") {
		t.Fatalf("pushed without CI: %s", line)
	}
	names := stepNames(e.r.run)
	cp, prep, draft := slices.Index(names, "save checkpoint"), slices.Index(names, "prepare checkout"), slices.Index(names, "mark draft")
	// The MR is a draft already when nothing marked it ready before.
	if cp < 0 || prep < cp || draft >= 0 && draft < cp {
		t.Fatalf("the checkpoint (draft first) comes before prepare: %v", names)
	}
	if _, res := e.stepResult("save checkpoint"); !strings.Contains(res, shortSHA(sha)+" pushed without CI: 1 files, +3/−0") {
		t.Fatalf("save checkpoint: %q", res)
	}
	// Prepare ran afterwards, from the checkpoint: the call has the files.
	if !engine.sawWip || engine.head != subject {
		t.Fatalf("the run continues from the checkpoint: wip.go %v, origin head %q", engine.sawWip, engine.head)
	}
	if run := e.task().Status.Run; run.LastResult != aiv1.RunResultSucceeded {
		t.Fatalf("the run goes on: %+v", run)
	}
	e.checkFolded(sha, "Nothing")
	if st := e.savedState(); st.InFlight != nil {
		t.Fatalf("the marker is used up: %+v", st.InFlight)
	}
}

func TestLostPodSavesNothingWhenUnsafe(t *testing.T) {
	for name, mutate := range map[string]func(e *limitEnv){
		// Someone pushed to the branch after the lost pod's last push.
		"remote moved": func(e *limitEnv) {
			seed := filepath.Join(e.tmp, "seed")
			gitCmd(t, seed, "fetch", "origin", cpBranch)
			gitCmd(t, seed, "checkout", "-B", "human", "FETCH_HEAD")
			writeFile(t, seed, "human.go", "package app\n")
			gitCmd(t, seed, "add", ".")
			gitCmd(t, seed, "commit", "-m", "Human fix")
			gitCmd(t, seed, "push", "origin", "HEAD:"+cpBranch)
		},
		"other branch": func(e *limitEnv) {
			gitCmd(t, filepath.Join(e.tmp, "agent", "repo"), "checkout", "-q", "-b", "agent/7-other")
		},
		"not interrupted": func(*limitEnv) {},
		"checkpoints off": func(e *limitEnv) {
			e.updateProject(func(p *aiv1.AgentProject) { p.Spec.Checkpoint.Disabled = true })
		},
	} {
		t.Run(name, func(t *testing.T) {
			e, engine := lostPodEnv(t, quietEngine{})
			before := e.origin1("rev-parse", cpBranch)
			mutate(e)
			if name != "not interrupted" {
				e.interrupt()
			}
			moved := e.origin1("rev-parse", cpBranch)
			if !e.step() {
				t.Fatal("the run should happen")
			}
			if log := e.origin1("log", "--format=%s", cpBranch); strings.Contains(log, "checkpoint") {
				t.Fatalf("nothing is saved:\n%s", log)
			}
			if engine.sawWip {
				t.Fatal("the workspace is reset as before")
			}
			if run := e.task().Status.Run; run.LastResult != aiv1.RunResultSucceeded || run.Checkpoint != nil {
				t.Fatalf("the run continues as today: %+v", run)
			}
			_, res := e.stepResult("save checkpoint")
			switch name {
			case "remote moved":
				if moved == before || !strings.Contains(res, "nothing saved: "+cpBranch+" does not descend from the last pushed commit "+moved) {
					t.Fatalf("save checkpoint: %q", res)
				}
			case "other branch":
				if !strings.Contains(res, `nothing saved: the workspace is on "agent/7-other"`) {
					t.Fatalf("save checkpoint: %q", res)
				}
			default:
				if slices.Contains(stepNames(e.r.run), "save checkpoint") {
					t.Fatalf("no new-pod path: %v", stepNames(e.r.run))
				}
			}
			if st := e.savedState(); st.InFlight != nil {
				t.Fatalf("the marker is used up: %+v", st.InFlight)
			}
		})
	}
}

// TestLostPodPushesUnpushedStep: the lost pod committed a step but did not
// push it, and the tree is clean: the step is pushed, no checkpoint made.
func TestLostPodPushesUnpushedStep(t *testing.T) {
	e, engine := lostPodEnv(t, quietEngine{})
	ws := filepath.Join(e.tmp, "agent", "repo")
	gitCmd(t, ws, "add", "-A")
	gitCmd(t, ws, "-c", "user.name=AI", "-c", "user.email=ai@example.com", "commit", "-q", "-m", "Add wip")
	step := gitCmd(t, ws, "rev-parse", "HEAD")
	e.interrupt()
	if !e.step() {
		t.Fatal("the run should start again")
	}
	if log := e.origin1("log", "--format=%H %s", cpBranch); !strings.Contains(log, step+" Add wip") || strings.Contains(log, "checkpoint") {
		t.Fatalf("the step is pushed, no checkpoint:\n%s", log)
	}
	if _, res := e.stepResult("push unpushed steps"); res != shortSHA(step)+" pushed without CI" {
		t.Fatalf("push unpushed steps: %q", res)
	}
	if !engine.sawWip || engine.head != "Add wip" {
		t.Fatalf("the run continues from the step: wip.go %v, origin head %q", engine.sawWip, engine.head)
	}
}

// TestOOMKillLeavesCheckpointInNewPod: the agent container runs out of
// memory in the implementer call; the run starts again with more and saves
// the call's work first.
func TestOOMKillLeavesCheckpointInNewPod(t *testing.T) {
	fastAgentLost(t)
	e := newLimitEnv(t, nil)
	engine := &killEngine{k8s: e.k8s, origin: e.origin, reason: "OOMKilled", next: quietEngine{}}
	e.r = newTestRunner(t, e.tmp, e.k8s, e.gl, engine, "shop-42-add-paypal-button")
	e.r.Now = func() time.Time { return e.now }
	e.updateProject(func(p *aiv1.AgentProject) {
		p.Spec.Agents.Resources.Limits = corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("4Gi")}
	})
	if e.step() {
		t.Fatal("the run is started again, not finished")
	}
	run := e.task().Status.Run
	if run.OOMRetries != 1 || run.LastResult != aiv1.RunResultDeferred {
		t.Fatalf("expected an OOM retry: %+v", run)
	}
	if st := e.savedState(); st.InFlight == nil || st.InFlight.Step != 1 || st.InFlight.Branch != cpBranch {
		t.Fatalf("the marker stays for the new pod: %+v", st.InFlight)
	}
	if notes := strings.Join(e.issueNotes(), "\n"); !strings.Contains(notes, "saves the killed implementer call's uncommitted edits as a checkpoint") {
		t.Fatalf("the OOM note says the edits are saved:\n%s", notes)
	}

	e.now = e.now.Add(time.Minute) // past the retry delay
	if !e.step() {
		t.Fatal("the run should start again")
	}
	sha, msg := e.checkpointCommit("chore(wip): checkpoint #42 step 1 (oom-kill)")
	if !strings.Contains(msg, "Agent-Checkpoint: 1/1 oom-kill") || !strings.Contains(msg, "- [>] wip.go") {
		t.Fatalf("checkpoint message:\n%s", msg)
	}
	e.checkFolded(sha, "Nothing")
	if !engine.sawWip {
		t.Fatal("the new pod's call has the files")
	}
	if run := e.task().Status.Run; run.LastResult != aiv1.RunResultSucceeded || run.OOMRetries != 0 {
		t.Fatalf("the run finishes: %+v", run)
	}
}

func TestEndCutKeepsInFlightForALostAgent(t *testing.T) {
	r := &Runner{StateDir: t.TempDir(), Log: logr.Discard()}
	ctx := context.Background()
	mark := &inFlight{Run: 1, Step: 2, Branch: cpBranch}
	for _, tc := range []struct {
		name string
		err  error
		keep bool
	}{
		{"done", nil, false},
		{"failed", errors.New("boom"), false},
		{"agent lost", &AgentLostError{Err: errors.New("EOF")}, true},
	} {
		st := &state{}
		r.cut = &cutTarget{st: st, branch: cpBranch}
		r.setInFlight(st, mark)
		r.endCut(ctx, tc.err)
		disk, _ := r.loadState()
		if r.cut != nil || (disk.InFlight != nil) != tc.keep || (st.InFlight != nil) != tc.keep {
			t.Fatalf("%s: cut %v, in flight %+v on disk, %+v in the run", tc.name, r.cut, disk.InFlight, st.InFlight)
		}
	}
}

func TestCheckpointTakesStatusFromLastStatusFile(t *testing.T) {
	w := checkpointWorkspace(t)
	writeFile(t, w.Dir, "wip.go", "package app\n")
	if err := os.WriteFile(w.lastStatusPath(), []byte(lostStatus+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	req := checkpointReq(w, t)
	req.Status = ""
	res, err := w.Checkpoint(context.Background(), req)
	if err != nil || res.SHA == "" {
		t.Fatalf("expected a checkpoint: %+v, %v", res, err)
	}
	if msg := gitCmd(t, w.Dir, "log", "-1", "--format=%B"); !strings.Contains(msg, lostStatus+"\n\nAgent-Checkpoint:") {
		t.Fatalf("the body takes the kept status:\n%s", msg)
	}
	if _, err := os.Stat(w.lastStatusPath()); !os.IsNotExist(err) {
		t.Fatalf("the status file is deleted after a checkpoint: %v", err)
	}
	// A status from the runner wins; Prepare deletes a kept one.
	writeFile(t, w.Dir, "more.go", "package app\n")
	_ = os.WriteFile(w.lastStatusPath(), []byte("old status"), 0o600)
	req = checkpointReq(w, t)
	if _, err := w.Checkpoint(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if msg := gitCmd(t, w.Dir, "log", "-1", "--format=%B"); strings.Contains(msg, "old status") || !strings.Contains(msg, req.Status) {
		t.Fatalf("the runner's status wins:\n%s", msg)
	}
	_ = os.WriteFile(w.lastStatusPath(), []byte("old status"), 0o600)
	refs, err := w.RemoteRefs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Prepare(context.Background(), PrepareRequest{Refs: refs, Branch: "agent/7-work", Base: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.lastStatusPath()); !os.IsNotExist(err) {
		t.Fatalf("Prepare deletes the status file: %v", err)
	}
}

func TestStreamStatus(t *testing.T) {
	for _, tc := range []struct{ name, line, want string }{
		{"text", `{"type":"assistant","message":{"content":[{"type":"text","text":"  Now the tests.  "}]}}`, "Now the tests."},
		{"todos", `{"type":"assistant","message":{"content":[{"type":"text","text":"Plan"},{"type":"tool_use","name":"TodoWrite","input":{"todos":[` +
			`{"content":"model","status":"completed"},{"content":"end\npoint","status":"in_progress"},{"content":"docs","status":"pending"}]}}]}}`,
			"Todo list:\n- [x] model\n- [>] end point\n- [ ] docs"},
		{"other tool", `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test"}}]}}`, ""},
		{"subagent", `{"type":"assistant","parent_tool_use_id":"t1","message":{"content":[{"type":"text","text":"sub"}]}}`, ""},
		{"user", `{"type":"user","message":{"content":[{"type":"text","text":"hi"}]}}`, ""},
		{"not json", `npm WARN`, ""},
	} {
		if got := streamStatus([]byte(tc.line)); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	long := `{"type":"assistant","message":{"content":[{"type":"text","text":"` + strings.Repeat("a", 3000) + `"}]}}`
	if got := streamStatus([]byte(long)); len(got) > maxStatusText+len("…") {
		t.Fatalf("cut at %d, got %d bytes", maxStatusText, len(got))
	}
}

// TestTurnReaderReportsStatus: the engine stream hands the status to the sink.
func TestTurnReaderReportsStatus(t *testing.T) {
	var got []string
	tr := &turnReader{log: logr.Discard(), status: func(s string) { got = append(got, s) }}
	tr.consume([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"one"}]}}`))
	tr.consume([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{}}]}}`))
	tr.consume([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"two"}]}}`))
	if !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("got %q", got)
	}
}

// statusEngine streams status texts through the sink LocalAgent.Run sets.
type statusEngine struct{ texts []string }

func (s statusEngine) Run(ctx context.Context, inv Invocation) (*Result, error) {
	if sink := statusSink(ctx); sink != nil {
		for _, text := range s.texts {
			sink(text)
		}
	}
	return &Result{}, nil
}

func TestLocalAgentKeepsLastStatus(t *testing.T) {
	w := checkpointWorkspace(t)
	a := &LocalAgent{Workspace: w, Engines: map[aiv1.Engine]Engine{aiv1.EngineClaude: statusEngine{texts: []string{"first", "second", "third"}}}}
	if _, err := a.Run(context.Background(), aiv1.EngineClaude, Invocation{}); err != nil {
		t.Fatal(err)
	}
	// Throttled: "first" at once, the newest one at the end.
	if got := w.lastStatus(); got != "third" {
		t.Fatalf("kept status %q", got)
	}
	// A reviewer's call is read-only: its text is no status, the kept one stays.
	a.Engines[aiv1.EngineClaude] = statusEngine{texts: []string{"review"}}
	if _, err := a.Run(context.Background(), aiv1.EngineClaude, Invocation{ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	if got := w.lastStatus(); got != "third" {
		t.Fatalf("kept status %q", got)
	}
	// The next call that may edit starts without the old one.
	a.Engines[aiv1.EngineClaude] = statusEngine{}
	if _, err := a.Run(context.Background(), aiv1.EngineClaude, Invocation{}); err != nil {
		t.Fatal(err)
	}
	if got := w.lastStatus(); got != "" {
		t.Fatalf("a new call drops the old status, got %q", got)
	}
}

func TestStatusWriterThrottles(t *testing.T) {
	path := filepath.Join(t.TempDir(), lastStatusFile)
	w := &statusWriter{path: path, every: time.Hour}
	read := func() string { b, _ := os.ReadFile(path); return string(b) }
	w.set("a")
	w.set("b")
	if got := read(); got != "a" {
		t.Fatalf("first write at once, then throttled: %q", got)
	}
	w.flush()
	if got := read(); got != "b" {
		t.Fatalf("flush writes the newest: %q", got)
	}
}
