package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// logout posts to the logout endpoint and checks that it answers 200 with
// the provider's end-session URL and clears both cookies.
func (e *renewEnv) logout() {
	e.t.Helper()
	resp, err := e.c.Post(e.s.cfg.ExternalURL+pathLogout, "", nil)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out LoginRedirect
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK {
		e.t.Fatalf("logout: status %d, %v", resp.StatusCode, err)
	}
	if !strings.HasPrefix(out.Redirect, e.f.issuer()+"/logout?") {
		e.t.Errorf("logout redirect = %q", out.Redirect)
	}
	if !e.cleared(resp.Cookies()) {
		e.t.Error("logout did not clear both cookies")
	}
}

func TestLogoutRevokesRefreshToken(t *testing.T) {
	e := newRenewEnv(t, nil, nil)
	token := currentToken(e.f)
	e.logout()
	reqs := e.f.revocationRequests()
	if len(reqs) != 1 || !e.f.loginRevoked(1) {
		t.Fatalf("revocations %d, revoked %v; want one", len(reqs), e.f.loginRevoked(1))
	}
	if r := reqs[0]; !r.basic || r.clientID != "" || r.secret || r.hint != "refresh_token" {
		t.Errorf("a confidential client: %+v, want Basic auth and the refresh_token hint", r)
	}
	p, err := e.s.oidc.discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.s.oidc.oauth2Config(p).TokenSource(context.Background(), &oauth2.Token{RefreshToken: token}).Token()
	if !isOAuthError(err, "invalid_grant") {
		t.Errorf("refresh with the revoked token: %v, want invalid_grant", err)
	}
}

func TestLogoutRevokesAsPublicClient(t *testing.T) {
	e := newRenewEnv(t, func(sw *fakeOIDCSwitches) { sw.public = true }, func(c *Config) { c.ClientSecret = "" })
	e.logout()
	reqs := e.f.revocationRequests()
	if len(reqs) != 1 || !e.f.loginRevoked(1) {
		t.Fatalf("revocations %d, revoked %v; want one", len(reqs), e.f.loginRevoked(1))
	}
	if r := reqs[0]; r.basic || r.clientID != fakeClientID || r.secret {
		t.Errorf("a public client: %+v, want client_id in the form", r)
	}
}

// TestLogoutRevokesExpiredSession: the refresh cookie counts after the
// session expired, and not after its idle deadline.
func TestLogoutRevokesExpiredSession(t *testing.T) {
	e := newRenewEnv(t, nil, nil)
	e.at(e.s.cfg.Auth.RenewAfter.Duration + time.Second)
	e.logout()
	if n := len(e.f.revocationRequests()); n != 1 || !e.f.loginRevoked(1) {
		t.Errorf("expired session: revocations %d, want one", n)
	}

	e = newRenewEnv(t, nil, nil)
	e.at(e.s.cfg.Auth.IdleTimeout.Duration)
	e.logout()
	if n := len(e.f.revocationRequests()); n != 0 {
		t.Errorf("idle refresh cookie: revocations %d, want none", n)
	}
}

func TestLogoutWithoutRevocationEndpoint(t *testing.T) {
	e := newRenewEnv(t, func(sw *fakeOIDCSwitches) { sw.noRevocation = true }, nil)
	e.logout()
	if n := len(e.f.revocationRequests()); n != 0 {
		t.Errorf("revocations %d, want none", n)
	}
}

func TestLogoutWhenRevocationFails(t *testing.T) {
	old := revokeTimeout
	revokeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { revokeTimeout = old })
	for name, tc := range map[string]struct {
		delay time.Duration
		log   string
	}{
		"503":  {0, `"status"=503`},
		"hang": {time.Minute, "cannot revoke the refresh token"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newRenewEnv(t, func(sw *fakeOIDCSwitches) { sw.revokeDown, sw.revokeDelay = true, tc.delay }, nil)
			start := time.Now()
			e.logout()
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("logout took %v", d)
			}
			if n := len(e.f.revocationRequests()); n != 1 || e.f.loginRevoked(1) {
				t.Errorf("revocations %d, revoked %v", n, e.f.loginRevoked(1))
			}
			if !strings.Contains(e.logs(), tc.log) {
				t.Errorf("log lacks %q:\n%s", tc.log, e.logs())
			}
		})
	}
}
