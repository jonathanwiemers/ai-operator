package cursorusage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeAPI is a fake api2.cursor.sh: the key "key" exchanges for the
// token "tok", and status overrides the answer of a path.
type fakeAPI struct {
	slow   bool
	cycle  string
	status map[string]int
	calls  []string
}

func (a *fakeAPI) client(t *testing.T) *CursorUsage {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.calls = append(a.calls, r.URL.Path+" "+r.Header.Get("Authorization"))
		if r.Method != http.MethodPost || r.Header.Get("Connect-Protocol-Version") != "1" {
			http.Error(w, "not a Connect call", http.StatusBadRequest)
			return
		}
		if code := a.status[r.URL.Path]; code != 0 {
			w.WriteHeader(code)
			return
		}
		switch r.URL.Path {
		case "/auth/exchange_user_api_key":
			_, _ = w.Write([]byte(`{"accessToken":"tok"}`))
		case "/aiserver.v1.DashboardService/GetUsageLimitPolicyStatus":
			if a.slow {
				_, _ = w.Write([]byte(`{"isInSlowPool":true}`))
			} else {
				_, _ = w.Write([]byte(`{}`))
			}
		case "/aiserver.v1.DashboardService/GetCurrentPeriodUsage":
			_, _ = w.Write([]byte(`{"billingCycleEnd":"` + a.cycle + `"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := NewCursorUsage("key")
	c.Base = srv.URL
	return c
}

func TestNoKey(t *testing.T) {
	c := NewCursorUsage("")
	if c != nil {
		t.Fatalf("no key gives no client, got %+v", c)
	}
	if slow, reset, err := c.SlowPool(context.Background(), time.Now()); slow || !reset.IsZero() || err != nil {
		t.Fatalf("a nil client is not slow: %v %v %v", slow, reset, err)
	}
}

// The API key is exchanged for a token, which the dashboard calls carry;
// a slow account reports when its billing cycle ends.
func TestSlowPool(t *testing.T) {
	api := &fakeAPI{slow: true, cycle: "1767225600000"}
	slow, reset, err := api.client(t).SlowPool(context.Background(), time.Now())
	if err != nil || !slow || !reset.Equal(time.UnixMilli(1767225600000)) {
		t.Fatalf("slow %v reset %v err %v", slow, reset, err)
	}
	want := "/auth/exchange_user_api_key Bearer key|" +
		"/aiserver.v1.DashboardService/GetUsageLimitPolicyStatus Bearer tok|" +
		"/aiserver.v1.DashboardService/GetCurrentPeriodUsage Bearer tok"
	if got := strings.Join(api.calls, "|"); got != want {
		t.Fatalf("calls %s", got)
	}
}

// An account outside the slow pool needs no billing cycle.
func TestNotSlow(t *testing.T) {
	api := &fakeAPI{}
	slow, reset, err := api.client(t).SlowPool(context.Background(), time.Now())
	if err != nil || slow || !reset.IsZero() || len(api.calls) != 2 {
		t.Fatalf("slow %v reset %v err %v calls %v", slow, reset, err, api.calls)
	}
}

// A slow account whose billing cycle cannot be read is still slow, with
// an unknown reset.
func TestSlowWithoutCycleEnd(t *testing.T) {
	for name, api := range map[string]*fakeAPI{
		"unparsable": {slow: true, cycle: "soon"},
		"failing": {slow: true, status: map[string]int{
			"/aiserver.v1.DashboardService/GetCurrentPeriodUsage": http.StatusInternalServerError}},
	} {
		slow, reset, err := api.client(t).SlowPool(context.Background(), time.Now())
		if err != nil || !slow || !reset.IsZero() {
			t.Errorf("%s: slow %v reset %v err %v", name, slow, reset, err)
		}
	}
}

// A failed call reports its error once; until CursorRecheck has passed
// the account counts as not slow without asking again.
func TestFailureIsKept(t *testing.T) {
	for _, path := range []string{"/auth/exchange_user_api_key", "/aiserver.v1.DashboardService/GetUsageLimitPolicyStatus"} {
		api := &fakeAPI{slow: true, status: map[string]int{path: http.StatusUnauthorized}}
		c := api.client(t)
		now := time.Now()
		slow, _, err := c.SlowPool(context.Background(), now)
		if slow || err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "401") {
			t.Fatalf("%s: slow %v err %v", path, slow, err)
		}
		n := len(api.calls)
		if slow, _, err := c.SlowPool(context.Background(), now.Add(CursorRecheck-time.Second)); slow || err != nil || len(api.calls) != n {
			t.Fatalf("%s: within CursorRecheck: slow %v err %v, %d calls after %d", path, slow, err, len(api.calls), n)
		}
		delete(api.status, path)
		if slow, _, err := c.SlowPool(context.Background(), now.Add(CursorRecheck)); !slow || err != nil {
			t.Fatalf("%s: after CursorRecheck: slow %v err %v", path, slow, err)
		}
	}
}

// A slow answer is kept for CursorRecheck, then asked again.
func TestAnswerIsKept(t *testing.T) {
	api := &fakeAPI{slow: true, cycle: "1767225600000"}
	c := api.client(t)
	now := time.Now()
	if _, _, err := c.SlowPool(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	api.slow = false
	slow, reset, err := c.SlowPool(context.Background(), now.Add(CursorRecheck-time.Second))
	if !slow || reset.IsZero() || err != nil || len(api.calls) != 3 {
		t.Fatalf("kept answer: slow %v reset %v err %v calls %d", slow, reset, err, len(api.calls))
	}
	if slow, reset, _ := c.SlowPool(context.Background(), now.Add(CursorRecheck)); slow || !reset.IsZero() {
		t.Fatalf("fresh answer: slow %v reset %v", slow, reset)
	}
}

// Without an answer the call fails instead of blocking the run.
func TestUnreachable(t *testing.T) {
	c := NewCursorUsage("key")
	c.Base = "http://127.0.0.1:1"
	if slow, _, err := c.SlowPool(context.Background(), time.Now()); slow || err == nil {
		t.Fatalf("slow %v err %v", slow, err)
	}
	c = NewCursorUsage("key")
	c.Base = "://bad"
	if _, _, err := c.SlowPool(context.Background(), time.Now()); err == nil {
		t.Fatal("a bad base URL is an error")
	}
}
