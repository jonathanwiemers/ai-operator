package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

func TestOneOffGitTransient(t *testing.T) {
	for _, tc := range []struct {
		err  string
		want bool
	}{
		{"git push --quiet origin x: error: RPC failed; curl 28 Operation timed out after 300000 milliseconds", true},
		{"git push timed out after 5m", true},
		{"error: RPC failed; HTTP 502 curl 22 The requested URL returned error: 502", true},
		{"fatal: unable to access 'https://gitlab.com/g/p.git/': Could not resolve host: gitlab.com", true},
		{"fatal: unable to access 'https://gitlab.com/g/p.git/': The requested URL returned error: 429", true},
		{"error: RPC failed; HTTP 429", true},
		{"fatal: the remote end hung up unexpectedly\nfatal: early EOF", true},
		{"send-pack: unexpected disconnect while reading sideband packet", true},
		{"fatal: unable to access 'https://gitlab.com/g/p.git/': Recv failure: Connection reset by peer", true},
		{"remote: You are not allowed to push code to this project.\nfatal: unable to access 'https://gitlab.com/g/p.git/': The requested URL returned error: 403", false},
		{"! [rejected] agent/1-x -> agent/1-x (non-fast-forward)", false},
		{"remote: HTTP Basic: Access denied. fatal: Authentication failed", false},
		{(&gitlab.GuardError{Project: "g/p", Op: "push main", Reason: "the default branch"}).Error(), false},
	} {
		if got := gitTransient(errors.New(tc.err)); got != tc.want {
			t.Errorf("gitTransient(%q) = %v, want %v", tc.err, got, tc.want)
		}
	}
	// Typed errors are classified by gitlab.IsTransient.
	if !gitTransient(fmt.Errorf("x: %w", &gitlab.Error{StatusCode: 503})) || gitTransient(&gitlab.GuardError{}) || gitTransient(nil) {
		t.Fatal("typed errors misclassified")
	}
}

func TestRetryOneOff(t *testing.T) {
	ctx := context.Background()
	timeout := errors.New("git push timed out after 5m")
	forbidden := errors.New("The requested URL returned error: 403")
	for _, tc := range []struct {
		name  string
		errs  []error
		want  error
		calls int
	}{
		{"timeout, timeout, success", []error{timeout, timeout, nil}, nil, 3},
		{"three timeouts", []error{timeout, timeout, timeout, nil}, timeout, 3},
		{"403 at once", []error{forbidden, nil}, forbidden, 1},
		{"success", []error{nil}, nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := retryOneOff(ctx, logr.Discard(), "push", gitTransient, func() error {
				calls++
				return tc.errs[calls-1]
			})
			if !errors.Is(err, tc.want) || calls != tc.calls {
				t.Fatalf("err=%v calls=%d, want %v after %d", err, calls, tc.want, tc.calls)
			}
		})
	}
	// A done context stops the retries.
	done, cancel := context.WithCancel(ctx)
	cancel()
	calls := 0
	if err := retryOneOff(done, logr.Discard(), "push", gitTransient, func() error { calls++; return timeout }); err != timeout || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	// So does a ctx that ends during an attempt, even with the zero waits
	// TestMain sets: a select between a done ctx and an elapsed wait picks
	// at random, so the done ctx must be checked before it.
	for i := 0; i < 100; i++ {
		during, cancel := context.WithCancel(ctx)
		calls = 0
		err := retryOneOff(during, logr.Discard(), "push", gitTransient, func() error { calls++; cancel(); return timeout })
		if err != timeout || calls != 1 {
			t.Fatalf("ctx ended during the attempt, run %d: err=%v calls=%d", i, err, calls)
		}
	}
}

// failingHook makes the remote refuse the next n pushes with a 502 in the
// text git prints, like a proxy in front of GitLab that times out.
func failingHook(t *testing.T, origin string, n int) {
	t.Helper()
	count := filepath.Join(t.TempDir(), "count")
	script := fmt.Sprintf("#!/bin/sh\necho x >> %q\nif [ $(wc -l < %q) -le %d ]; then echo 'error: RPC failed; HTTP 502 curl 22 The requested URL returned error: 502' >&2; exit 1; fi\n", count, count, n)
	if err := os.WriteFile(filepath.Join(origin, "hooks", "pre-receive"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestOneOffMirrorPushRetries(t *testing.T) {
	ctx := context.Background()
	// Two one-off errors, then the push lands.
	m, origin, _ := guardedMirror(t)
	failingHook(t, origin, 2)
	if err := m.Push(ctx, "agent/42-x", false); err != nil {
		t.Fatal(err)
	}
	want, _ := m.resolve(ctx, "refs/heads/agent/42-x")
	if got := gitCmd(t, filepath.Dir(origin), "--git-dir", origin, "rev-parse", "agent/42-x"); got != want {
		t.Fatalf("remote agent/42-x at %s, want %s", got, want)
	}
	// Three fail as before.
	m, origin, _ = guardedMirror(t)
	failingHook(t, origin, 3)
	if err := m.Push(ctx, "agent/42-x", false); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("want the 502, got %v", err)
	}
}

// flakyMRs fails CreateMergeRequest: before without creating the MR, after
// once it created it (a response lost on the way back).
type flakyMRs struct {
	*gitlabfake.Fake
	before, after []error
	creates       int
}

func (f *flakyMRs) CreateMergeRequest(ctx context.Context, project string, opts gitlab.CreateMergeRequestOptions) (*gitlab.MergeRequest, error) {
	f.creates++
	if len(f.before) > 0 {
		err := f.before[0]
		f.before = f.before[1:]
		return nil, err
	}
	mr, err := f.Fake.CreateMergeRequest(ctx, project, opts)
	if len(f.after) > 0 {
		err = f.after[0]
		f.after = f.after[1:]
		return nil, err
	}
	return mr, err
}

func TestOpenMergeRequestRetries(t *testing.T) {
	badGateway := &gitlab.Error{Method: "POST", Path: "/merge_requests", StatusCode: 502}
	forbidden := &gitlab.Error{Method: "POST", Path: "/merge_requests", StatusCode: 403}
	for _, tc := range []struct {
		name          string
		before, after []error
		creates, mrs  int
		fails         bool
	}{
		{"502 after the MR was created", nil, []error{badGateway}, 1, 1, false},
		{"502 before", []error{badGateway}, nil, 2, 1, false},
		{"403 at once", []error{forbidden}, nil, 1, 0, true},
		{"three 502", []error{badGateway, badGateway, badGateway}, nil, 3, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newImplementEnv(t)
			gitCmd(t, e.seed, "push", "-q", "origin", "HEAD:agent/42-x")
			m := &Mirror{AuthorName: "Bot", AuthorEmail: "bot@example.com", Dir: filepath.Join(e.tmp, "mirror.git"), Policy: &PushPolicy{Project: "group/app", DefaultBranch: "main", URL: e.origin}}
			ctx := context.Background()
			if err := m.Sync(ctx, e.origin, "main", "agent/42-x"); err != nil {
				t.Fatal(err)
			}
			r := &Runner{K8s: e.k8s, Namespace: "agents", TaskName: e.task.Name, Mirror: m, Log: logr.Discard()}
			gl := &flakyMRs{Fake: e.gl, before: tc.before, after: tc.after}
			mr, err := r.openMergeRequest(ctx, gl, "group/app", &gitlab.Issue{IID: 42, Title: "X"}, "agent/42-x", "main", nil)
			if (err != nil) != tc.fails || gl.creates != tc.creates || len(e.gl.MRs) != tc.mrs {
				t.Fatalf("err=%v creates=%d mrs=%d", err, gl.creates, len(e.gl.MRs))
			}
			if !tc.fails && (mr == nil || mr.IID != 1 || mr.SourceBranch != "agent/42-x") {
				t.Fatalf("mr=%+v", mr)
			}
		})
	}
}
