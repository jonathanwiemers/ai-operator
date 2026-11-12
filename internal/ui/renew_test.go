package ui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
)

// renewLogin logs in through f and returns the UI, the login's refresh
// state and principal, and a function returning the UI's log so far.
func renewLogin(t *testing.T, f *fakeOIDC, mutate func(*Config)) (*Server, RefreshState, Principal, func() string) {
	t.Helper()
	s, c := newOIDCTestServer(t, f, mutate)
	var lines []string
	s.oidc.log = funcr.New(func(_, args string) { lines = append(lines, args) }, funcr.Options{Verbosity: 9})
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
	return s, *st, sess.Principal, func() string { return strings.Join(lines, "\n") }
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
	if next.Subject != st.Subject || next.Nonce != st.Nonce || next.Since != st.Since ||
		next.Renewed != now.Unix() || next.IdleUntil != now.Add(s.cfg.Auth.IdleTimeout.Duration).Unix() {
		t.Errorf("state = since %d, renewed %d, idle %d (now %d)", next.Since, next.Renewed, next.IdleUntil, now.Unix())
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
		// The login started 25h ago; its idle deadline is still ahead.
		st.Since = time.Now().Add(-25 * time.Hour).Unix()
		if _, _, err := s.oidc.redeem(t.Context(), st, prev); !errors.Is(err, errRenewRefused) {
			t.Errorf("redeem: %v, want a refusal", err)
		}
		if got := f.refreshGrants(1); got != 0 {
			t.Errorf("refresh grants = %d, want 0", got)
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
