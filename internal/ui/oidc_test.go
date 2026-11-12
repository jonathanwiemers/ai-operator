package ui

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"testing"
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
