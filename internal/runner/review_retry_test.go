package runner

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestIsTransient(t *testing.T) {
	for _, msg := range []string{
		"cursor-agent failed: exit status 1: RetriableError: [unavailable] Error",
		"read tcp 10.0.0.1:443: connection reset by peer",
		"API Error: 503 Service Unavailable",
	} {
		if !isTransient(errors.New(msg)) {
			t.Errorf("%q should be transient", msg)
		}
	}
	for _, err := range []error{
		nil,
		errors.New("exit status 1: tests failed"),
		errors.New("decoding the answer: unexpected end of JSON input"),
		&UsageLimitError{Engine: aiv1.EngineCursor, Message: "RetriableError: usage limit"},
	} {
		if isTransient(err) {
			t.Errorf("%v must not be transient", err)
		}
	}
}

// failingReviewer fails the first `fails` calls of the bugbot reviewer with
// `message` and delegates everything else to the scripted engine.
type failingReviewer struct {
	mu      sync.Mutex
	next    Engine
	message string
	fails   int
	calls   int
}

func (f *failingReviewer) Run(ctx context.Context, inv Invocation) (*Result, error) {
	if inv.Role == string(aiv1.ReviewerBugbot) {
		f.mu.Lock()
		f.calls++
		fail := f.calls <= f.fails
		f.mu.Unlock()
		if fail {
			return &Result{IsError: true, Text: f.message}, errors.New("cursor-agent failed: exit status 1: " + f.message)
		}
	}
	return f.next.Run(ctx, inv)
}

func fastReviewRetries(t *testing.T) {
	t.Helper()
	prev := reviewRetryBackoff
	reviewRetryBackoff = []time.Duration{time.Millisecond}
	t.Cleanup(func() { reviewRetryBackoff = prev })
}

func TestReviewerRetriesTransientFailures(t *testing.T) {
	fastReviewRetries(t)
	engine := &failingReviewer{next: &scriptedEngine{}, message: "RetriableError: [unavailable] Error", fails: 2}
	e := newLimitEnv(t, engine)

	if !e.step() {
		t.Fatal("the run should complete")
	}
	// Round 1: two transient failures and a success; the scripted findings
	// then get fixed and round 2 calls bugbot once more.
	if engine.calls != 4 {
		t.Fatalf("bugbot should be called 3 times in round 1 and once in round 2, got %d", engine.calls)
	}
	run := e.task().Status.Run
	if run.LastResult != aiv1.RunResultSucceeded || run.ReviewErrors != 0 || run.ReviewedSHA != run.PushedSHA {
		t.Fatalf("the retried review should count as a full review: %+v", run)
	}
}

func TestReviewerGivesUpAfterRetries(t *testing.T) {
	fastReviewRetries(t)
	engine := &failingReviewer{next: &scriptedEngine{}, message: "RetriableError: [unavailable] Error", fails: 100}
	e := newLimitEnv(t, engine)

	if !e.step() {
		t.Fatal("the run should complete")
	}
	if engine.calls != 3 {
		t.Fatalf("bugbot should be called 1 + review.retries (2) times, got %d", engine.calls)
	}
	if run := e.task().Status.Run; run.ReviewErrors != 1 {
		t.Fatalf("one reviewer should have failed, got %d", run.ReviewErrors)
	}
	b, _ := json.Marshal(e.gl.MRNotes[1])
	if !strings.Contains(string(b), "bugbot reviewer failed") || !strings.Contains(string(b), "(after 3 attempts)") {
		t.Fatalf("the report should say the reviewer failed after 3 attempts: %s", b)
	}
}

func TestReviewerDoesNotRetryOtherFailures(t *testing.T) {
	fastReviewRetries(t)
	engine := &failingReviewer{next: &scriptedEngine{}, message: "invalid model: does-not-exist", fails: 100}
	e := newLimitEnv(t, engine)

	if !e.step() {
		t.Fatal("the run should complete")
	}
	if engine.calls != 1 {
		t.Fatalf("a non-transient failure must not be retried, got %d calls", engine.calls)
	}
	b, _ := json.Marshal(e.gl.MRNotes[1])
	if strings.Contains(string(b), "attempts)") {
		t.Fatalf("a single attempt should not mention attempts: %s", b)
	}
}
