package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// fakeCursorAPI answers like api2.cursor.sh for the key "key"; requests
// counts the calls.
func fakeCursorAPI(t *testing.T, slow bool, requests *atomic.Int32) *CursorUsage {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		auth := r.Header.Get("Authorization")
		switch {
		case r.URL.Path == "/auth/exchange_user_api_key" && auth == "Bearer key":
			_, _ = w.Write([]byte(`{"accessToken":"access","refreshToken":"refresh"}`))
		case auth != "Bearer access" || r.Header.Get("Connect-Protocol-Version") != "1":
			w.WriteHeader(http.StatusUnauthorized)
		case strings.HasSuffix(r.URL.Path, "/GetUsageLimitPolicyStatus"):
			if slow {
				_, _ = w.Write([]byte(`{"isInSlowPool":true,"slownessMs":5000}`))
			} else {
				_, _ = w.Write([]byte(`{}`))
			}
		case strings.HasSuffix(r.URL.Path, "/GetCurrentPeriodUsage"):
			// 2026-12-13 20:29:59 UTC
			_, _ = w.Write([]byte(`{"billingCycleEnd":"1797193799000","planUsage":{"totalPercentUsed":100}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	c := NewCursorUsage("key")
	c.Base = srv.URL
	return c
}

func cursorReview(fallback *aiv1.RoleFallback) aiv1.RoleSpec {
	return aiv1.RoleSpec{Engine: aiv1.EngineCursor, Model: "auto", Fallback: fallback}
}

func TestSlowPoolIsASoftLimitForCursorRolesWithAFallback(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	var requests atomic.Int32
	r := &Runner{Now: func() time.Time { return now }, Cursor: fakeCursorAPI(t, true, &requests)}
	lim := r.nearLimit(context.Background(), cursorReview(sonnetFallback()))
	if lim == nil || !lim.Soft || lim.Engine != aiv1.EngineCursor {
		t.Fatalf("the slow pool must be a soft cursor limit, got %+v", lim)
	}
	if want := time.Date(2026, 12, 13, 20, 29, 59, 0, time.UTC); !lim.ResetAt.Equal(want) {
		t.Errorf("the limit lasts until the billing cycle ends %s, got %s", want, lim.ResetAt.UTC())
	}
	if want := "plan is used up (slow pool until 2026-12-13 20:29 UTC)"; lim.Message != want {
		t.Errorf("message %q, want %q", lim.Message, want)
	}

	// Parallel reviewers and later calls reuse the answer for a while.
	n := requests.Load()
	now = now.Add(cursorRecheck - time.Second)
	if r.nearLimit(context.Background(), cursorReview(sonnetFallback())) == nil || requests.Load() != n {
		t.Fatalf("a fresh answer is kept: %d requests after %d", requests.Load(), n)
	}
	now = now.Add(2 * time.Second)
	if r.nearLimit(context.Background(), cursorReview(sonnetFallback())); requests.Load() == n {
		t.Fatal("an old answer is asked again")
	}
}

func TestSlowPoolCheckRunsOnlyWhereItCanRouteAround(t *testing.T) {
	var requests atomic.Int32
	r := &Runner{Now: time.Now, Cursor: fakeCursorAPI(t, true, &requests)}
	for name, role := range map[string]aiv1.RoleSpec{
		"cursor without a fallback": cursorReview(nil),
		"claude with a fallback":    {Engine: aiv1.EngineClaude, Model: "claude-opus-5-5", Fallback: &aiv1.RoleFallback{Engine: aiv1.EngineCursor, Model: "auto"}},
	} {
		if lim := r.nearLimit(context.Background(), role); lim != nil {
			t.Errorf("%s: no soft limit expected, got %+v", name, lim)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("Cursor's usage is read only for cursor roles with a fallback, got %d requests", n)
	}
}

func TestCursorOutsideTheSlowPoolOrUnreadableRunsOnCursor(t *testing.T) {
	var requests atomic.Int32
	fast := &Runner{Now: time.Now, Cursor: fakeCursorAPI(t, false, &requests)}
	if lim := fast.nearLimit(context.Background(), cursorReview(sonnetFallback())); lim != nil {
		t.Errorf("outside the slow pool cursor runs, got %+v", lim)
	}
	badKey := fakeCursorAPI(t, true, &requests)
	badKey.Key = "revoked"
	for name, r := range map[string]*Runner{
		"rejected key": {Now: time.Now, Cursor: badKey},
		"no key":       {Now: time.Now, Cursor: NewCursorUsage("")},
	} {
		if lim := r.nearLimit(context.Background(), cursorReview(sonnetFallback())); lim != nil {
			t.Errorf("%s: unknown usage must not route around cursor, got %+v", name, lim)
		}
	}
}

func TestSlowPoolReviewRunsOnTheFallback(t *testing.T) {
	e, engine, rec := cursorLimitedEnv(t, func(a *aiv1.AgentsSpec) {
		a.Review.Engine, a.Review.Model, a.Review.Fallback = aiv1.EngineCursor, "auto", sonnetFallback()
	}, nil)
	engine.limitIf = func(Invocation) bool { return false }
	var requests atomic.Int32
	e.r.Cursor = fakeCursorAPI(t, true, &requests)
	if !e.step() {
		t.Fatal("the slow pool never defers a run")
	}
	for _, reviewer := range []string{"bugbot", "security"} {
		if models := rec.models(reviewer); len(models) == 0 || models[0] != "claude-sonnet-5-5" {
			t.Errorf("%s must review on claude-sonnet-5-5, ran %v", reviewer, models)
		}
	}
	if run := e.task().Status.Run; len(run.UsageLimits) != 0 || run.LastResult == aiv1.RunResultDeferred {
		t.Fatalf("the slow pool is no limit: nothing recorded or deferred, got %+v", run)
	}
	line := "↪️ `cursor`'s plan is used up (slow pool until 2026-12-13 20:29 UTC), so bugbot ran on `claude/claude-sonnet-5-5` instead."
	if !strings.Contains(e.allNotes(), line) {
		t.Fatalf("the report must say why the reviewer ran on the fallback:\n%s", e.allNotes())
	}
}
