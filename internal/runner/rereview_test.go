package runner

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// reviewerFailures fails the reviewers named in messages with their message
// and delegates everything else.
type reviewerFailures struct {
	next     Engine
	messages map[string]string
}

func (f *reviewerFailures) Run(ctx context.Context, inv Invocation) (*Result, error) {
	if m, ok := f.messages[inv.Role]; ok {
		return &Result{IsError: true, Text: m}, errors.New("cursor-agent failed: exit status 1: " + m)
	}
	return f.next.Run(ctx, inv)
}

const unavailable = "RetriableError: [unavailable] Error"

func TestReviewErrorsTransientOnlyWhenAllFailuresAre(t *testing.T) {
	fastReviewRetries(t)
	for _, tc := range []struct {
		name      string
		messages  map[string]string
		transient bool
	}{
		{"one outage", map[string]string{"bugbot": unavailable}, true},
		{"both outages", map[string]string{"bugbot": unavailable, "security": unavailable}, true},
		{"outage and real failure", map[string]string{"bugbot": unavailable, "security": "invalid model: nope"}, false},
		{"real failure", map[string]string{"bugbot": "invalid model: nope"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newLimitEnv(t, &reviewerFailures{next: &scriptedEngine{}, messages: tc.messages})
			if !e.step() {
				t.Fatal("the run should complete")
			}
			run := e.task().Status.Run
			if int(run.ReviewErrors) != len(tc.messages) || run.ReviewErrorsTransient != tc.transient {
				t.Fatalf("want %d errors, transient %v: %+v", len(tc.messages), tc.transient, run)
			}
		})
	}
}

// reReview runs once with bugbot down, then asks for a re-review of sha with
// bugbot back; blocker makes its first finding a blocker. before runs before
// the second run.
func reReview(t *testing.T, sha func(aiv1.RunStatus) string, blocker bool, before ...func(*limitEnv, *reviewerFailures)) (*limitEnv, *scriptedEngine, aiv1.RunStatus) {
	t.Helper()
	fastReviewRetries(t)
	scripted := &scriptedEngine{}
	engine := &reviewerFailures{next: scripted, messages: map[string]string{"bugbot": unavailable}}
	e := newLimitEnv(t, engine)
	if !e.step() {
		t.Fatal("the first run should complete")
	}
	first := e.task().Status.Run
	if !first.ReviewErrorsTransient || first.PushedSHA == "" {
		t.Fatalf("the first run should push and fail its review transiently: %+v", first)
	}
	delete(engine.messages, "bugbot")
	if !blocker {
		scripted.reviewRound = 1 // past the scripted blocker
	}
	scripted.calls = nil
	task := e.task()
	orig := task.DeepCopy()
	task.Spec.RunRequest = 2
	task.Spec.Source.ReviewFor = sha(first)
	if err := e.k8s.Patch(context.Background(), &task, client.MergeFrom(orig)); err != nil {
		t.Fatal(err)
	}
	for _, f := range before {
		f(e, engine)
	}
	if !e.step() {
		t.Fatal("the second run should complete")
	}
	return e, scripted, first
}

func implementerCalls(s *scriptedEngine) int {
	return len(slices.DeleteFunc(slices.Clone(s.calls), func(inv Invocation) bool {
		return inv.ReadOnly || inv.JSONSchema == describeSchema
	}))
}

func TestReReviewRunSkipsTheImplementerAndPushesNothing(t *testing.T) {
	e, scripted, first := reReview(t, func(r aiv1.RunStatus) string { return r.PushedSHA }, false)
	run := e.task().Status.Run
	head := gitCmd(t, e.tmp, "--git-dir", e.origin, "rev-parse", "agent/42-add-paypal-button")
	if n := implementerCalls(scripted); n != 0 {
		t.Fatalf("the implementer must not run, got %d calls", n)
	}
	if head != first.PushedSHA || run.PushedSHA != first.PushedSHA {
		t.Fatalf("nothing may be pushed: head %s, %+v", head, run)
	}
	if run.ReviewedSHA != head || run.ReviewedFor != head || run.ReviewErrors != 0 || run.ReviewErrorsTransient {
		t.Fatalf("the re-review should be recorded as a full review of the head: %+v", run)
	}
	if !slices.Equal(e.gl.Labels(42), []string{"agent::review"}) {
		t.Fatalf("a clean re-review goes to review: %v", e.gl.Labels(42))
	}
	notes := e.gl.MRNotes[1]
	if last := notes[len(notes)-1].Body; !strings.Contains(last, "Reviewed `"+shortSHA(head)+"` again") {
		t.Fatalf("the report should say it was a re-review: %s", last)
	}
}

func TestReReviewBlockingFindingsGoToTheImplementer(t *testing.T) {
	e, scripted, first := reReview(t, func(r aiv1.RunStatus) string { return r.PushedSHA }, true)
	run := e.task().Status.Run
	if n := implementerCalls(scripted); n != 1 {
		t.Fatalf("the blocker should get one fix round, got %d implementer calls", n)
	}
	if run.PushedSHA == first.PushedSHA || run.ReviewedSHA != run.PushedSHA || run.BlockingFindings != 0 {
		t.Fatalf("the fix should be pushed and reviewed: %+v", run)
	}
}

func TestReReviewOfAnOldHeadIsANormalRun(t *testing.T) {
	e, scripted, _ := reReview(t, func(aiv1.RunStatus) string { return "0123456789abcdef" }, false)
	if n := implementerCalls(scripted); n == 0 {
		t.Fatal("the branch moved on: the implementer should run")
	}
	if run := e.task().Status.Run; run.ReviewedFor != "0123456789abcdef" {
		t.Fatalf("the request should count as handled: %+v", run)
	}
}

func TestReReviewWithAnInstructionRunsTheImplementer(t *testing.T) {
	_, scripted, _ := reReview(t, func(r aiv1.RunStatus) string { return r.PushedSHA }, false, func(e *limitEnv, _ *reviewerFailures) {
		e.gl.AddMRNote(1, gitlab.User{ID: 7, Username: "jonathan"}, "/agent also handle refunds")
	})
	if n := implementerCalls(scripted); n == 0 {
		t.Fatal("a human instruction since the last run needs the implementer")
	}
}

func TestFailedRunDoesNotUseUpTheReReview(t *testing.T) {
	e, _, _ := reReview(t, func(aiv1.RunStatus) string { return "0123456789abcdef" }, false, func(_ *limitEnv, f *reviewerFailures) {
		f.messages["implement"] = "invalid model: nope"
	})
	if run := e.task().Status.Run; run.LastResult != aiv1.RunResultFailed || run.ReviewedFor != "" {
		t.Fatalf("a failed run must not count as the re-review: %+v", run)
	}
}
