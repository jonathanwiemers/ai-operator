package runner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-logr/logr"
)

// checkpointWorkspace is a checkout of branch agent/7-work, pushed at its
// current HEAD, with app.go and a tracked .env.example.
func checkpointWorkspace(t *testing.T) *Workspace {
	t.Helper()
	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin.git")
	dir := filepath.Join(tmp, "ws")
	gitCmd(t, tmp, "init", "--bare", "--initial-branch=main", origin)
	gitCmd(t, tmp, "clone", origin, dir)
	writeFile(t, dir, "app.go", "package app\n")
	writeFile(t, dir, ".env.example", "A=1\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "init")
	gitCmd(t, dir, "push", "origin", "HEAD:main")
	gitCmd(t, dir, "checkout", "-b", "agent/7-work")
	gitCmd(t, dir, "push", "origin", "agent/7-work")
	return &Workspace{Dir: dir, AuthorName: "AI", AuthorEmail: "ai@example.com"}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func checkpointReq(w *Workspace, t *testing.T) CheckpointRequest {
	return CheckpointRequest{
		Reason: "usage limit", Issue: 7, Run: 3, Step: 2,
		Branch: "agent/7-work", Base: "main",
		Expect: gitCmd(t, w.Dir, "rev-parse", "HEAD"),
		Status: "- [x] model\n- [ ] endpoint",
	}
}

// assertNoCommit checks that HEAD is still head.
func assertNoCommit(t *testing.T, w *Workspace, head string, res CheckpointResult) {
	t.Helper()
	if res.SHA != "" {
		t.Fatalf("expected no checkpoint, got %+v", res)
	}
	if got := gitCmd(t, w.Dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved from %s to %s", head, got)
	}
}

func TestCheckpointCleanTree(t *testing.T) {
	w := checkpointWorkspace(t)
	head := gitCmd(t, w.Dir, "rev-parse", "HEAD")
	res, err := w.Checkpoint(context.Background(), checkpointReq(w, t))
	if err != nil {
		t.Fatal(err)
	}
	assertNoCommit(t, w, head, res)
	if res.Refused != "" || res.Aborted != "" || res.Files != 0 {
		t.Fatalf("a clean tree is not a refusal: %+v", res)
	}
}

func TestCheckpointDirtyTree(t *testing.T) {
	w := checkpointWorkspace(t)
	head := gitCmd(t, w.Dir, "rev-parse", "HEAD")
	writeFile(t, w.Dir, "app.go", "package app\n\nconst A = 1\nconst B = 2\n")
	writeFile(t, w.Dir, "new.go", "package app\n")
	writeFile(t, w.Dir, ".env.example", "A=2\n") // tracked: committed although it matches .env*
	// A stale lock of a killed git process does not stop the checkpoint.
	writeFile(t, w.Dir, ".git/index.lock", "")

	res, err := w.Checkpoint(context.Background(), checkpointReq(w, t))
	if err != nil {
		t.Fatal(err)
	}
	if res.SHA == "" || res.SHA != gitCmd(t, w.Dir, "rev-parse", "HEAD") {
		t.Fatalf("expected a checkpoint at HEAD: %+v", res)
	}
	if parent := gitCmd(t, w.Dir, "rev-parse", "HEAD^"); parent != head {
		t.Fatalf("the checkpoint's parent is %s, want %s", parent, head)
	}
	if gitCmd(t, w.Dir, "symbolic-ref", "--short", "HEAD") != "agent/7-work" {
		t.Fatal("the checkpoint must be on the work branch")
	}
	if res.Files != 3 || res.Added != 5 || res.Deleted != 1 || len(res.Skipped) != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	stat := gitCmd(t, w.Dir, "diff", "--stat", "HEAD^", "HEAD")
	if strings.TrimSpace(res.Stat) != stat {
		t.Fatalf("stat %q, want %q", res.Stat, stat)
	}
	want := "chore(wip): checkpoint #7 step 2 (usage limit)\n\n" + res.Stat +
		"\n\n- [x] model\n- [ ] endpoint\n\nAgent-Checkpoint: 3/2 usage limit"
	if got := gitCmd(t, w.Dir, "log", "-1", "--format=%B"); got != want {
		t.Fatalf("message\n%s\nwant\n%s", got, want)
	}
	if got := gitCmd(t, w.Dir, "log", "-1", "--format=%(trailers:key=Agent-Checkpoint,valueonly)"); got != "3/2 usage limit" {
		t.Fatalf("git does not see the trailer: %q", got)
	}
	if !IsCheckpoint(gitCmd(t, w.Dir, "log", "-1", "--format=%B")) {
		t.Fatal("IsCheckpoint must recognise the checkpoint")
	}
	if s := gitCmd(t, w.Dir, "status", "--porcelain"); s != "" {
		t.Fatalf("everything must be committed, left %q", s)
	}
}

func TestCheckpointStatusIsCut(t *testing.T) {
	w := checkpointWorkspace(t)
	writeFile(t, w.Dir, "app.go", "package app // changed\n")
	req := checkpointReq(w, t)
	req.Status = "# Todo\n" + strings.Repeat("x", 3000)
	if _, err := w.Checkpoint(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	msg := gitCmd(t, w.Dir, "log", "-1", "--format=%B")
	if !strings.Contains(msg, "\n\n# Todo\nxxx") {
		t.Fatalf("a # line of the status must be kept:\n%s", msg)
	}
	if n := strings.Count(msg, "x"); n > 1000 {
		t.Fatalf("status not cut: %d characters", n)
	}
	if !strings.HasSuffix(msg, "…\n\nAgent-Checkpoint: 3/2 usage limit") {
		t.Fatalf("message must end in the cut status and the trailer:\n%s", msg[len(msg)-100:])
	}
}

func TestCheckpointMergeInProgress(t *testing.T) {
	w := mergeWorkspace(t, true)
	gitCmd(t, w.Dir, "checkout", "-b", "agent/7-work")
	res, err := w.MergeBase(context.Background(), "main", "Merge main")
	if err != nil || len(res.Conflicts) == 0 {
		t.Fatalf("expected a conflict: %+v %v", res, err)
	}
	writeFile(t, w.Dir, "app.go", "package app\n\nconst Name = \"half\"\n")

	cp, err := w.Checkpoint(context.Background(), CheckpointRequest{
		Reason: "usage limit", Issue: 7, Step: 1, Branch: "agent/7-work", Base: "main", Expect: res.From,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cp.Aborted != "merge" {
		t.Fatalf("expected the merge to be aborted: %+v", cp)
	}
	assertNoCommit(t, w, res.From, cp)
	if w.hasRef(context.Background(), "MERGE_HEAD") {
		t.Fatal("the merge is still in progress")
	}
	if s := gitCmd(t, w.Dir, "status", "--porcelain"); s != "" {
		t.Fatalf("the tree must be back at the commit before the merge, left %q", s)
	}
}

func TestCheckpointRebaseInProgress(t *testing.T) {
	w := mergeWorkspace(t, true)
	gitCmd(t, w.Dir, "checkout", "-b", "agent/7-work")
	head := gitCmd(t, w.Dir, "rev-parse", "HEAD")
	if _, err := w.git(context.Background(), "rebase", "origin/main"); err == nil {
		t.Fatal("expected the rebase to stop on a conflict")
	}
	cp, err := w.Checkpoint(context.Background(), CheckpointRequest{
		Reason: "usage limit", Issue: 7, Step: 1, Branch: "agent/7-work", Base: "main", Expect: head,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cp.Aborted != "rebase" {
		t.Fatalf("expected the rebase to be aborted: %+v", cp)
	}
	assertNoCommit(t, w, head, cp)
	if gitCmd(t, w.Dir, "symbolic-ref", "--short", "HEAD") != "agent/7-work" {
		t.Fatal("the work branch must be checked out again")
	}
}

func TestCheckpointLeavesOutBigAndSecretFiles(t *testing.T) {
	w := checkpointWorkspace(t)
	writeFile(t, w.Dir, "app.go", "package app // changed\n")
	writeFile(t, w.Dir, "big.bin", strings.Repeat("x", 200))
	writeFile(t, w.Dir, ".env.local", "TOKEN=secret\n")
	if err := os.MkdirAll(filepath.Join(w.Dir, "certs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, w.Dir, "certs/server.pem", "-----BEGIN-----\n")
	req := checkpointReq(w, t)
	req.MaxFileBytes = 100

	res, err := w.Checkpoint(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.SHA == "" {
		t.Fatalf("app.go must still be committed: %+v", res)
	}
	files := strings.Fields(gitCmd(t, w.Dir, "show", "--name-only", "--format=", "HEAD"))
	if !slices.Equal(files, []string{"app.go"}) {
		t.Fatalf("checkpoint holds %v, want only app.go", files)
	}
	var paths []string
	for _, s := range res.Skipped {
		paths = append(paths, s.Path)
		if s.Why == "" {
			t.Fatalf("skip without a reason: %+v", s)
		}
	}
	slices.Sort(paths)
	if want := []string{".env.local", "big.bin", "certs/server.pem"}; !slices.Equal(paths, want) {
		t.Fatalf("skipped %v, want %v", paths, want)
	}
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(w.Dir, p)); err != nil {
			t.Fatalf("%s must stay in the work tree: %v", p, err)
		}
	}
}

func TestCheckpointOnlySkippedFiles(t *testing.T) {
	w := checkpointWorkspace(t)
	head := gitCmd(t, w.Dir, "rev-parse", "HEAD")
	writeFile(t, w.Dir, "id_rsa", "key\n")
	res, err := w.Checkpoint(context.Background(), checkpointReq(w, t))
	if err != nil {
		t.Fatal(err)
	}
	assertNoCommit(t, w, head, res)
	if len(res.Skipped) != 1 || res.Skipped[0].Path != "id_rsa" {
		t.Fatalf("the skipped file must still be reported: %+v", res)
	}
}

func TestCheckpointRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, w *Workspace, req *CheckpointRequest)
	}{
		{"wrong branch", func(t *testing.T, w *Workspace, req *CheckpointRequest) {
			gitCmd(t, w.Dir, "checkout", "-q", "-b", "agent/8-other")
		}},
		{"base branch", func(t *testing.T, w *Workspace, req *CheckpointRequest) {
			gitCmd(t, w.Dir, "checkout", "-q", "main")
			req.Branch = "main"
		}},
		{"not an agent branch", func(t *testing.T, w *Workspace, req *CheckpointRequest) {
			gitCmd(t, w.Dir, "checkout", "-q", "-b", "feature")
			req.Branch = "feature"
		}},
		{"no branch", func(t *testing.T, w *Workspace, req *CheckpointRequest) { req.Branch = "" }},
		{"detached", func(t *testing.T, w *Workspace, req *CheckpointRequest) {
			gitCmd(t, w.Dir, "checkout", "-q", "--detach")
		}},
		{"not a descendant", func(t *testing.T, w *Workspace, req *CheckpointRequest) {
			gitCmd(t, w.Dir, "commit", "-q", "--allow-empty", "-m", "elsewhere")
			req.Expect = gitCmd(t, w.Dir, "rev-parse", "HEAD")
			gitCmd(t, w.Dir, "reset", "-q", "--hard", "HEAD^")
		}},
		{"missing expected commit", func(t *testing.T, w *Workspace, req *CheckpointRequest) {
			req.Expect = strings.Repeat("ab", 20)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := checkpointWorkspace(t)
			req := checkpointReq(w, t)
			tc.setup(t, w, &req)
			head := gitCmd(t, w.Dir, "rev-parse", "HEAD")
			writeFile(t, w.Dir, "app.go", "package app // changed\n")
			res, err := w.Checkpoint(context.Background(), req)
			if err != nil {
				t.Fatalf("a refusal is not an error: %v", err)
			}
			if res.Refused == "" {
				t.Fatalf("expected a refusal: %+v", res)
			}
			assertNoCommit(t, w, head, res)
			if s := gitCmd(t, w.Dir, "status", "--porcelain"); s != "M app.go" {
				t.Fatalf("a refusal must leave the tree alone, status %q", s)
			}
		})
	}
}

func TestIsCheckpoint(t *testing.T) {
	for msg, want := range map[string]bool{
		"chore(wip): checkpoint #7 step 2 (usage limit)\n\nstat\n\nAgent-Checkpoint: 3/2 usage limit\n": true,
		"chore(wip): x\n\nRefs #7\nAgent-Checkpoint: 3/2 budget stop":                                   true,
		"feat: add x\n\nRefs #7":                                         false,
		"feat: add x\n\nAgent-Checkpoint: 1/1 x\n\nRefs #7":              false,
		"Agent-Checkpoint: in the subject only is not a trailer\n\nbody": false,
		"": false,
	} {
		if got := IsCheckpoint(msg); got != want {
			t.Errorf("IsCheckpoint(%q) = %v, want %v", msg, got, want)
		}
	}
}

// TestCheckpointThroughAgentServer checks the RPC wiring.
func TestCheckpointThroughAgentServer(t *testing.T) {
	w := checkpointWorkspace(t)
	writeFile(t, w.Dir, "app.go", "package app // changed\n")
	writeFile(t, w.Dir, "key.pem", "secret\n")
	client := serveAgent(t, &AgentServer{Log: logr.Discard(), Agent: &LocalAgent{Workspace: w, Exchange: filepath.Join(t.TempDir(), "exchange")}})
	res, err := client.Checkpoint(context.Background(), checkpointReq(w, t))
	if err != nil {
		t.Fatal(err)
	}
	if res.SHA != gitCmd(t, w.Dir, "rev-parse", "HEAD") || res.Files != 1 || res.Added != 1 || res.Deleted != 1 {
		t.Fatalf("unexpected result over the socket: %+v", res)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Path != "key.pem" {
		t.Fatalf("skipped files must come back: %+v", res.Skipped)
	}

	req := checkpointReq(w, t)
	req.Branch = "main"
	res, err = client.Checkpoint(context.Background(), req)
	if err != nil || res.Refused == "" {
		t.Fatalf("a refusal must come back without an error: %+v %v", res, err)
	}
}
