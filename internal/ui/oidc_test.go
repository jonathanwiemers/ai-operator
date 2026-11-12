package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
)

func TestOIDCLoginCallback(t *testing.T) {
	t.Run("login", func(t *testing.T) {
		f := newFakeOIDC(t)
		f.set(func(sw *fakeOIDCSwitches) { sw.roles = []string{"ai-operator-admin", "shop-devs"} })
		s, c := newOIDCTestServer(t, f, nil)

		next, cookies := loginThrough(t, s, c, "/projects/team-a/shop?tab=costs")
		if next != "/projects/team-a/shop?tab=costs" {
			t.Errorf("after login the browser goes to %q", next)
		}
		if sc := cookieNamed(cookies, s.sessions.SessionCookie()); sc == nil || sc.Value == "" || !sc.HttpOnly {
			t.Errorf("no session cookie: %v", sc)
		}
		if lc := cookieNamed(cookies, s.sessions.LoginCookie()); lc == nil || lc.MaxAge >= 0 {
			t.Errorf("the login cookie is not cleared: %v", lc)
		}
		if f.loginCount() != 1 {
			t.Errorf("code grants = %d, want 1", f.loginCount())
		}

		resp, err := c.Get(s.cfg.ExternalURL + "/api/v1/me")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var me Me
		if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&me) != nil {
			t.Fatalf("/api/v1/me: status %d", resp.StatusCode)
		}
		if me.Username != fakeEmail || !slices.Equal(me.Roles, []string{"ai-operator-admin", "shop-devs"}) || me.AuthMode != AuthOIDC {
			t.Errorf("me = %+v", me)
		}
	})

	failures := []struct {
		name   string
		mutate func(*Config)
		deny   bool
		// callback changes the URL the provider redirects back to.
		callback func(string) string
		want     string
	}{
		{name: "wrong state", callback: func(u string) string { return replaceParam(t, u, "state", "forged") }, want: errState},
		{name: "refused", deny: true, want: errRefused},
		{name: "claim mapping fails", mutate: func(c *Config) { c.OIDC.Claims.Username = "claims.nickname" }, want: errClaims},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeOIDC(t)
			f.set(func(sw *fakeOIDCSwitches) { sw.deny = tc.deny })
			s, c := newOIDCTestServer(t, f, tc.mutate)

			callback, _ := redirectOf(t, c, startLogin(t, s, c, "/projects"))
			if tc.callback != nil {
				callback = tc.callback(callback)
			}
			next, cookies := redirectOf(t, c, callback)
			if want := "/?login_error=" + tc.want; next != want {
				t.Errorf("callback redirects to %q, want %q", next, want)
			}
			if sc := cookieNamed(cookies, s.sessions.SessionCookie()); sc != nil {
				t.Error("a failed login set the session cookie")
			}
			resp, err := c.Get(s.cfg.ExternalURL + "/api/v1/me")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("/api/v1/me after a failed login: status %d", resp.StatusCode)
			}
		})
	}
}

func TestOIDCCallbackRefreshCookie(t *testing.T) {
	const token = "stale-token-from-another-login"
	run := func(t *testing.T, sw func(*fakeOIDCSwitches), mutate func(*Config), preset bool) (*fakeOIDC, *Server, []*http.Cookie, string, time.Time) {
		f := newFakeOIDC(t)
		f.set(sw)
		s, c := newOIDCTestServer(t, f, mutate)
		var lines []string
		s.oidc.log = funcr.New(func(_, args string) { lines = append(lines, args) }, funcr.Options{Verbosity: 9})
		now := time.Now().Truncate(time.Second)
		s.sessions.now = func() time.Time { return now }
		if preset {
			u, _ := url.Parse(s.cfg.ExternalURL)
			c.Jar.SetCookies(u, []*http.Cookie{{Name: s.sessions.RefreshCookie(), Value: token, Path: "/"}})
		}
		_, cookies := loginThrough(t, s, c, "/")
		return f, s, cookies, strings.Join(lines, "\n"), now
	}
	decode := func(t *testing.T, s *Server, c *http.Cookie) *RefreshState {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
		st, ok := s.sessions.Refresh(req)
		if !ok {
			t.Fatal("the refresh cookie does not decode")
		}
		return st
	}

	t.Run("refresh token", func(t *testing.T) {
		f, s, cookies, logs, now := run(t, func(*fakeOIDCSwitches) {}, nil, false)
		sc, rc := cookieNamed(cookies, s.sessions.SessionCookie()), cookieNamed(cookies, s.sessions.RefreshCookie())
		if sc == nil || rc == nil || rc.Value == "" {
			t.Fatalf("session %v, refresh %v", sc, rc)
		}
		if want := now.Add(s.cfg.Auth.SessionTTL.Duration); !sc.Expires.Equal(want) {
			t.Errorf("session expires %v, want %v (sessionTTL)", sc.Expires, want)
		}
		f.mu.Lock()
		tok, nonce := f.logins[0].current, f.logins[0].nonce
		f.mu.Unlock()
		st := decode(t, s, rc)
		if st.Token != tok || st.Subject != fakeSubject || st.Nonce != nonce || st.Since != now.Unix() ||
			st.IdleUntil != now.Add(s.cfg.Auth.IdleTimeout.Duration).Unix() {
			t.Errorf("refresh state = %+v", st)
		}
		if strings.Contains(logs, tok) || strings.Contains(logs, "no refresh token") {
			t.Errorf("logs: %s", logs)
		}
	})
	t.Run("max lifetime caps the idle deadline", func(t *testing.T) {
		_, s, cookies, _, now := run(t, func(*fakeOIDCSwitches) {}, func(c *Config) {
			c.Auth.MaxLifetime.Duration = 24 * time.Hour
		}, false)
		st := decode(t, s, cookieNamed(cookies, s.sessions.RefreshCookie()))
		if want := now.Add(24 * time.Hour).Unix(); st.IdleUntil != want {
			t.Errorf("IdleUntil = %d, want %d", st.IdleUntil, want)
		}
	})
	t.Run("no refresh token", func(t *testing.T) {
		_, s, cookies, logs, _ := run(t, func(sw *fakeOIDCSwitches) { sw.noRefresh = true }, nil, true)
		if sc := cookieNamed(cookies, s.sessions.SessionCookie()); sc == nil || sc.Value == "" {
			t.Error("no session cookie")
		}
		if rc := cookieNamed(cookies, s.sessions.RefreshCookie()); rc == nil || rc.MaxAge >= 0 {
			t.Errorf("the old refresh cookie is not cleared: %v", rc)
		}
		if !strings.Contains(logs, "no refresh token from the provider") || !strings.Contains(logs, "offline_access") {
			t.Errorf("logs: %s", logs)
		}
		if strings.Contains(logs, token) {
			t.Error("the old token is in the logs")
		}
	})
}

// replaceParam sets a query parameter of rawURL.
func replaceParam(t *testing.T, rawURL, name, value string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set(name, value)
	u.RawQuery = q.Encode()
	return u.String()
}
