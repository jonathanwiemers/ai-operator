package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Attachments call over the agent RPC: copy, replace, remove, a source
// outside the exchange refused, and the exclude line written once.
func TestAgentAttachments(t *testing.T) {
	tmp := t.TempDir()
	local := &LocalAgent{Workspace: &Workspace{Dir: filepath.Join(tmp, "repo")}, Exchange: filepath.Join(tmp, "exchange")}
	a := serveAgent(t, &AgentServer{Agent: local})
	ctx := context.Background()
	dst := filepath.Join(local.Workspace.Dir, attachmentsDir)
	src := filepath.Join(local.Exchange, "attachments")
	_ = os.MkdirAll(src, 0o755)
	writeFile(t, src, "abc00001-shot.png", "one")

	if err := a.Attachments(ctx, filepath.Join(tmp, "elsewhere")); err == nil {
		t.Fatal("a source outside the exchange was copied")
	}
	if err := a.Attachments(ctx, src); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "abc00001-shot.png")); string(b) != "one" {
		t.Fatalf("copied: %q", b)
	}

	_ = os.RemoveAll(src)
	_ = os.MkdirAll(src, 0o755)
	writeFile(t, src, "abc00002-log.txt", "two")
	if err := a.Attachments(ctx, src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "abc00001-shot.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the old file is still there: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "abc00002-log.txt")); string(b) != "two" {
		t.Fatalf("replaced: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(local.Workspace.Dir, ".git", "info", "exclude")); string(b) != "/.attachments/\n" {
		t.Fatalf("exclude after two calls: %q", b)
	}

	if err := a.Attachments(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the folder is still there: %v", err)
	}
	if err := a.Attachments(ctx, ""); err != nil {
		t.Fatalf("removing a missing folder: %v", err)
	}
}

// Neither a checkpoint nor a step commit takes the attachments along.
func TestAttachmentsAreNotCommitted(t *testing.T) {
	w := checkpointWorkspace(t)
	exchange := filepath.Join(t.TempDir(), "exchange")
	a := &LocalAgent{Workspace: w, Exchange: exchange}
	src := filepath.Join(exchange, "attachments")
	_ = os.MkdirAll(src, 0o755)
	writeFile(t, src, "abc00001-shot.png", "png")
	ctx := context.Background()
	if err := a.Attachments(ctx, src); err != nil {
		t.Fatal(err)
	}
	if out := gitCmd(t, w.Dir, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("git status shows the attachments: %q", out)
	}

	writeFile(t, w.Dir, "app.go", "package app\n\nconst A = 1\n")
	cp, err := w.Checkpoint(ctx, checkpointReq(w, t))
	if err != nil || cp.SHA == "" {
		t.Fatalf("checkpoint %+v, %v", cp, err)
	}
	assertNoAttachments(t, w, "HEAD")

	writeFile(t, w.Dir, "new.go", "package app\n")
	res, err := w.CommitStep(ctx, stepReq("feat: add new.go"))
	if err != nil || !res.Committed {
		t.Fatalf("step commit %+v, %v", res, err)
	}
	assertNoAttachments(t, w, "HEAD")
	if _, err := os.Stat(filepath.Join(w.Dir, attachmentsDir, "abc00001-shot.png")); err != nil {
		t.Fatalf("the commits removed the attachments: %v", err)
	}
}

func assertNoAttachments(t *testing.T, w *Workspace, rev string) {
	t.Helper()
	files := gitCmd(t, w.Dir, "ls-tree", "-r", "--name-only", rev)
	if strings.Contains(files, attachmentsDir) {
		t.Fatalf("%s holds the attachments:\n%s", rev, files)
	}
}
