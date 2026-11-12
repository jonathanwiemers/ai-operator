package ui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
)

// renewLogin logs in through f and returns the UI, the login's refresh
// state and principal, and a function returning the UI's log so far.
func renewLogin(t *testing.T, f *fakeOIDC, mutate func(*Config)) (*Server, RefreshState, Principal, func() string) {
	t.Helper()
	s, c := newOIDCTestServer(t, f, mutate)
	var (
		mu    sync.Mutex
		lines []string
	)
	s.oidc.log = funcr.New(func(_, args string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, args)
	}, funcr.Options{Verbosity: 9})
	_, cookies := loginThrough(t, s, c, "/")
	rc := cookieNamed(cookies, s.sessions.RefreshCookie())
	sc := cookieNamed(cookies, s.sessions.SessionCookie())
	if rc == nil || sc == nil {
		t.Fatal("the login set no refresh or session cookie")
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: rc.Name, Value: rc.Value})
	req.AddCookie(&http.Cookie{Name: sc.Name, Value: sc.Value})
	st, ok := s.sessions.Refresh(req)
	sess, ok2 := s.sessions.Get(req)
	if !ok || !ok2 {
		t.Fatal("the login's cookies do not decode")
	}
	return s, *st, sess.Principal, func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(lines, "\n")
	}
}

// noToken fails the test if any refresh token f issued is in text.
func noToken(t *testing.T, f *fakeOIDC, what, text string) {
	t.Helper()
	for _, tok := range f.refreshTokens() {
		if strings.Contains(text, tok) {
			t.Errorf("a refresh token is in the %s", what)
		}
	}
}

func TestRedeemRotates(t *testing.T) {
	f := newFakeOIDC(t)
	s, st, prev, logs := renewLogin(t, f, nil)
	now := time.Now().Truncate(time.Second)
	s.sessions.now = func() time.Time { return now }

	p, next, err := s.oidc.redeem(t.Context(), st, prev)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	f.mu.Lock()
	current := f.logins[0].current
	f.mu.Unlock()
	if next.Token != current || next.Token == st.Token {
		t.Error("the state does not carry the rotated refresh token")
	}
	if next.Subject != st.Subject || next.Nonce != st.Nonce || next.Until != st.Until ||
		next.Renewed != now.Unix() || next.IdleUntil != now.Add(s.cfg.Auth.IdleTimeout.Duration).Unix() {
		t.Errorf("state = until %d, renewed %d, idle %d (now %d)", next.Until, next.Renewed, next.IdleUntil, now.Unix())
	}
	if p.Username != fakeEmail || !slices.Equal(p.Roles, prev.Roles) {
		t.Errorf("principal = %+v, want %+v", p, prev)
	}
	if got := f.refreshGrants(1); got != 1 {
		t.Errorf("refresh grants = %d, want 1", got)
	}

	_, _, err = s.oidc.redeem(t.Context(), st, prev)
	if !errors.Is(err, errRenewRefused) {
		t.Errorf("the old token again: %v, want a refusal", err)
	}
	noToken(t, f, "error", err.Error())
	if _, _, err := s.oidc.redeem(t.Context(), next, prev); err != nil {
		t.Errorf("the rotated token: %v", err)
	}
	noToken(t, f, "log", logs())
}

func TestRedeemRoles(t *testing.T) {
	t.Run("changed roles apply", func(t *testing.T) {
		f := newFakeOIDC(t)
		s, st, prev, _ := renewLogin(t, f, nil)
		f.set(func(sw *fakeOIDCSwitches) { sw.roles = []string{"shop-devs"} })
		p, _, err := s.oidc.redeem(t.Context(), st, prev)
		if err != nil || !slices.Equal(p.Roles, []string{"shop-devs"}) {
			t.Errorf("redeem = %+v, %v", p, err)
		}
	})
	t.Run("from userinfo without an ID token", func(t *testing.T) {
		f := newFakeOIDC(t)
		s, st, prev, _ := renewLogin(t, f, nil)
		f.set(func(sw *fakeOIDCSwitches) {
			sw.noIDTokenOnRefresh = true
			sw.roles = []string{"ai-operator-admin", "shop-devs"}
		})
		p, _, err := s.oidc.redeem(t.Context(), st, prev)
		if err != nil || p.Username != fakeEmail || !slices.Equal(p.Roles, []string{"ai-operator-admin", "shop-devs"}) {
			t.Errorf("redeem = %+v, %v", p, err)
		}
	})
	t.Run("no ID token and userinfo down keeps the principal", func(t *testing.T) {
		f := newFakeOIDC(t)
		s, st, prev, logs := renewLogin(t, f, nil)
		f.set(func(sw *fakeOIDCSwitches) {
			sw.noIDTokenOnRefresh = true
			sw.userinfoDown = true
			sw.roles = []string{"shop-devs"}
		})
		p, next, err := s.oidc.redeem(t.Context(), st, prev)
		if err != nil {
			t.Fatalf("redeem: %v", err)
		}
		f.mu.Lock()
		current := f.logins[0].current
		f.mu.Unlock()
		if p.Username != prev.Username || !slices.Equal(p.Roles, prev.Roles) || next.Token != current || next.Token == st.Token {
			t.Errorf("principal %+v (want %+v), rotated token kept: %v", p, prev, next.Token == current)
		}
		if !strings.Contains(logs(), "roles were not refreshed") {
			t.Errorf("logs: %s", logs())
		}
		noToken(t, f, "log", logs())
	})
}

func TestRedeemRefused(t *testing.T) {
	cases := []struct {
		name string
		sw   func(*fakeOIDCSwitches)
		// after changes the UI after the login.
		after func(*testing.T, *Server)
	}{
		{name: "invalid_grant", sw: func(sw *fakeOIDCSwitches) { sw.refuse = true }},
		{name: "subject changed", sw: func(sw *fakeOIDCSwitches) { sw.subject = "user-2" }},
		{name: "nonce changed", sw: func(sw *fakeOIDCSwitches) { sw.nonce = "other-nonce" }},
		{name: "claim mapping fails", after: func(t *testing.T, s *Server) {
			m, err := NewClaimMapper(Claims{Username: s.cfg.OIDC.Claims.Username, Roles: "42"})
			if err != nil {
				t.Fatal(err)
			}
			s.oidc.claims = m
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeOIDC(t)
			s, st, prev, logs := renewLogin(t, f, nil)
			if tc.sw != nil {
				f.set(tc.sw)
			}
			if tc.after != nil {
				tc.after(t, s)
			}
			_, _, err := s.oidc.redeem(t.Context(), st, prev)
			if !errors.Is(err, errRenewRefused) || errors.Is(err, errRenewUnavailable) {
				t.Fatalf("redeem: %v, want a refusal", err)
			}
			noToken(t, f, "error", err.Error())
			noToken(t, f, "log", logs())
		})
	}
}

func TestRedeemPastDeadline(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		f := newFakeOIDC(t)
		s, st, prev, _ := renewLogin(t, f, nil)
		s.sessions.now = func() time.Time { return time.Unix(st.IdleUntil, 0).Add(time.Second) }
		if _, _, err := s.oidc.redeem(t.Context(), st, prev); !errors.Is(err, errRenewRefused) {
			t.Errorf("redeem: %v, want a refusal", err)
		}
		if got := f.refreshGrants(1); got != 0 {
			t.Errorf("refresh grants = %d, want 0", got)
		}
	})
	t.Run("max lifetime", func(t *testing.T) {
		f := newFakeOIDC(t)
		s, st, prev, _ := renewLogin(t, f, func(c *Config) { c.Auth.MaxLifetime.Duration = 24 * time.Hour })
		// The login's absolute deadline passed an hour ago; its idle deadline
		// is still ahead.
		st.Until = time.Now().Add(-time.Hour).Unix()
		if _, _, err := s.oidc.redeem(t.Context(), st, prev); !errors.Is(err, errRenewRefused) {
			t.Errorf("redeem: %v, want a refusal", err)
		}
		if got := f.refreshGrants(1); got != 0 {
			t.Errorf("refresh grants = %d, want 0", got)
		}
	})
	t.Run("deadline fixed at login", func(t *testing.T) {
		// A later change of auth.maxLifetime does not apply to a login that
		// already has its deadline, nor give one to a login without.
		f := newFakeOIDC(t)
		s, st, prev, _ := renewLogin(t, f, nil)
		s.oidc.cfg.Auth.MaxLifetime.Duration = time.Hour
		st.Until = 0
		_, next, err := s.oidc.redeem(t.Context(), st, prev)
		if err != nil || next.Until != 0 {
			t.Errorf("redeem: %v, until %d; want a renewal without a deadline", err, next.Until)
		}
	})
}

func TestRedeemUnavailable(t *testing.T) {
	t.Run("503", func(t *testing.T) {
		f := newFakeOIDC(t)
		s, st, prev, _ := renewLogin(t, f, nil)
		f.set(func(sw *fakeOIDCSwitches) { sw.down = true })
		if _, _, err := s.oidc.redeem(t.Context(), st, prev); !errors.Is(err, errRenewUnavailable) {
			t.Errorf("redeem: %v, want unavailable", err)
		}
	})
	t.Run("hang past the timeout", func(t *testing.T) {
		old := renewTimeout
		renewTimeout = 200 * time.Millisecond
		t.Cleanup(func() { renewTimeout = old })
		f := newFakeOIDC(t)
		s, st, prev, _ := renewLogin(t, f, nil)
		f.set(func(sw *fakeOIDCSwitches) { sw.down, sw.downDelay = true, time.Minute })
		start := time.Now()
		_, _, err := s.oidc.redeem(t.Context(), st, prev)
		if !errors.Is(err, errRenewUnavailable) {
			t.Errorf("redeem: %v, want unavailable", err)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("redeem took %v", d)
		}
		// The provider is back: the token was not used up.
		f.set(func(sw *fakeOIDCSwitches) { sw.down = false })
		if _, _, err := s.oidc.redeem(t.Context(), st, prev); err != nil {
			t.Errorf("redeem after the outage: %v", err)
		}
	})
	t.Run("discovery fails", func(t *testing.T) {
		f := newFakeOIDC(t)
		s, _ := newOIDCTestServer(t, f, nil)
		f.srv.Close()
		st := RefreshState{Token: "t", Subject: fakeSubject, IdleUntil: time.Now().Add(time.Hour).Unix()}
		if _, _, err := s.oidc.redeem(t.Context(), st, Principal{}); !errors.Is(err, errRenewUnavailable) {
			t.Errorf("redeem: %v, want unavailable", err)
		}
	})
}

// renewResult is what one renew call returned.
type renewResult struct {
	p   Principal
	rs  RefreshState
	err error
}

// waitGrants waits until login 1 made n refresh grants.
func waitGrants(t *testing.T, f *fakeOIDC, n int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); f.refreshGrants(1) < n; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("refresh grants = %d, want %d", f.refreshGrants(1), n)
		}
	}
}

// currentToken is login 1's only working refresh token.
func currentToken(f *fakeOIDC) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logins[0].current
}

// checkShared fails unless every result is the same successful renewal to
// f's current token.
func checkShared(t *testing.T, f *fakeOIDC, results []renewResult) {
	t.Helper()
	current := currentToken(f)
	for i, r := range results {
		if r.err != nil || r.rs.Token != current || r.p.Username != fakeEmail || !slices.Equal(r.p.Roles, results[0].p.Roles) {
			t.Errorf("call %d: %+v, rotated token: %v, error %v", i, r.p, r.rs.Token == current, r.err)
		}
	}
}

func TestRenewSingleFlight(t *testing.T) {
	f := newFakeOIDC(t)
	s, st, prev, logs := renewLogin(t, f, nil)
	hold := make(chan struct{})
	f.set(func(sw *fakeOIDCSwitches) { sw.hold = hold })

	results := make([]renewResult, 20)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			<-start
			p, rs, err := s.oidc.renew(t.Context(), st, prev)
			results[i] = renewResult{p, rs, err}
		})
	}
	close(start)
	waitGrants(t, f, 1)
	time.Sleep(50 * time.Millisecond) // the others join the flight
	close(hold)
	wg.Wait()

	if got := f.refreshGrants(1); got != 1 {
		t.Errorf("refresh grants = %d, want 1", got)
	}
	checkShared(t, f, results)
	if got := strings.Count(logs(), `"session renewed"`); got != 1 {
		t.Errorf("%d \"session renewed\" lines, want 1:\n%s", got, logs())
	}
	noToken(t, f, "log", logs())
}

func TestRenewCache(t *testing.T) {
	f := newFakeOIDC(t)
	s, st, prev, _ := renewLogin(t, f, nil)
	now := time.Now()
	s.oidc.renewals.now = func() time.Time { return now }

	p, first, err := s.oidc.renew(t.Context(), st, prev)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	now = now.Add(renewCache - time.Second)
	p2, again, err := s.oidc.renew(t.Context(), st, prev)
	if err != nil || again.Token != first.Token || p2.Username != p.Username || !slices.Equal(p2.Roles, p.Roles) {
		t.Errorf("within the window: %+v, same token %v, error %v", p2, again.Token == first.Token, err)
	}
	if got := f.refreshGrants(1); got != 1 {
		t.Errorf("refresh grants = %d, want 1", got)
	}

	now = now.Add(2 * time.Second)
	if _, _, err := s.oidc.renew(t.Context(), st, prev); !errors.Is(err, errRenewRefused) {
		t.Errorf("the old token after the window: %v, want a refusal", err)
	}
	// A failed grant counts twice: oauth2 retries it with the other client
	// authentication.
	if got := f.refreshGrants(1); got < 2 {
		t.Errorf("refresh grants = %d, want the old token at the provider", got)
	}
	if _, _, err := s.oidc.renew(t.Context(), first, prev); err != nil {
		t.Errorf("the rotated token: %v", err)
	}
	s.oidc.renewals.mu.Lock()
	defer s.oidc.renewals.mu.Unlock()
	if len(s.oidc.renewals.m) != 1 {
		t.Errorf("%d renewals kept, want 1 (the expired and the refused one are gone)", len(s.oidc.renewals.m))
	}
}

func TestRenewRetriesFailure(t *testing.T) {
	f := newFakeOIDC(t)
	s, st, prev, logs := renewLogin(t, f, nil)
	f.set(func(sw *fakeOIDCSwitches) { sw.down = true })
	if _, _, err := s.oidc.renew(t.Context(), st, prev); !errors.Is(err, errRenewUnavailable) {
		t.Errorf("renew while down: %v, want unavailable", err)
	}
	f.set(func(sw *fakeOIDCSwitches) { sw.down = false })
	failed := f.refreshGrants(1)
	if _, _, err := s.oidc.renew(t.Context(), st, prev); err != nil {
		t.Errorf("renew after the outage: %v", err)
	}
	if got := f.refreshGrants(1); got != failed+1 {
		t.Errorf("refresh grants = %d, want %d", got, failed+1)
	}
	if got := strings.Count(logs(), `"session renewed"`); got != 1 {
		t.Errorf("%d \"session renewed\" lines, want 1:\n%s", got, logs())
	}
}

func TestRenewLeaderCancelled(t *testing.T) {
	f := newFakeOIDC(t)
	s, st, prev, _ := renewLogin(t, f, nil)
	hold := make(chan struct{})
	f.set(func(sw *fakeOIDCSwitches) { sw.hold = hold })

	ctx, cancel := context.WithCancel(t.Context())
	leader := make(chan error, 1)
	go func() {
		_, _, err := s.oidc.renew(ctx, st, prev)
		leader <- err
	}()
	waitGrants(t, f, 1)

	results := make([]renewResult, 5)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			p, rs, err := s.oidc.renew(t.Context(), st, prev)
			results[i] = renewResult{p, rs, err}
		})
	}
	time.Sleep(50 * time.Millisecond) // the waiters join the flight
	cancel()
	time.Sleep(50 * time.Millisecond)
	close(hold)
	wg.Wait()
	<-leader

	if got := f.refreshGrants(1); got != 1 {
		t.Errorf("refresh grants = %d, want 1", got)
	}
	checkShared(t, f, results)
}

func TestRenewWaiterCancelled(t *testing.T) {
	f := newFakeOIDC(t)
	s, st, prev, _ := renewLogin(t, f, nil)
	hold := make(chan struct{})
	f.set(func(sw *fakeOIDCSwitches) { sw.hold = hold })
	leader := make(chan renewResult, 1)
	go func() {
		p, rs, err := s.oidc.renew(t.Context(), st, prev)
		leader <- renewResult{p, rs, err}
	}()
	waitGrants(t, f, 1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := s.oidc.renew(ctx, st, prev); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled waiter: %v, want context.Canceled", err)
	}
	close(hold)
	checkShared(t, f, []renewResult{<-leader})
}
