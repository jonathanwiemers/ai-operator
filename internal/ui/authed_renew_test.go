package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
)

// renewEnv is a UI logged in through the fake provider, with a clock the
// test moves and the UI's log and response bodies kept for the token check.
type renewEnv struct {
	t     *testing.T
	f     *fakeOIDC
	s     *Server
	c     *http.Client
	start time.Time
	clock atomic.Int64 // unix seconds

	mu     sync.Mutex
	lines  []string
	bodies []string
}

func newRenewEnv(t *testing.T, sw func(*fakeOIDCSwitches), mutate func(*Config)) *renewEnv {
	t.Helper()
	e := &renewEnv{t: t, f: newFakeOIDC(t), start: time.Now().Truncate(time.Second)}
	if sw != nil {
		e.f.set(sw)
	}
	e.s, e.c = newOIDCTestServer(t, e.f, mutate)
	e.clock.Store(e.start.Unix())
	e.s.sessions.now = func() time.Time { return time.Unix(e.clock.Load(), 0) }
	e.s.oidc.log = funcr.New(func(_, args string) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.lines = append(e.lines, args)
	}, funcr.Options{Verbosity: 9})
	loginThrough(t, e.s, e.c, "/")
	t.Cleanup(func() {
		noToken(t, e.f, "log", e.logs())
		e.mu.Lock()
		defer e.mu.Unlock()
		noToken(t, e.f, "response bodies", strings.Join(e.bodies, "\n"))
	})
	return e
}

// at sets the clock to the login's start plus d.
func (e *renewEnv) at(d time.Duration) time.Time {
	now := e.start.Add(d)
	e.clock.Store(now.Unix())
	return now
}

func (e *renewEnv) logs() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return strings.Join(e.lines, "\n")
}

// me requests /api/v1/me and returns the answer, its decoded body when it
// is 200, and the cookies it set.
func (e *renewEnv) me() (int, Me, []*http.Cookie) {
	e.t.Helper()
	resp, err := e.c.Get(e.s.cfg.ExternalURL + "/api/v1/me")
	if err != nil {
		e.t.Error(err)
		return 0, Me{}, nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	e.mu.Lock()
	e.bodies = append(e.bodies, string(b))
	e.mu.Unlock()
	var me Me
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(b, &me); err != nil {
			e.t.Error(err)
		}
	}
	return resp.StatusCode, me, resp.Cookies()
}

// cookie returns the value of the jar's cookie called name.
func (e *renewEnv) cookie(name string) *http.Cookie {
	u, _ := url.Parse(e.s.cfg.ExternalURL)
	return cookieNamed(e.c.Jar.Cookies(u), name)
}

// request is a request carrying the jar's cookies.
func (e *renewEnv) request() *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	u, _ := url.Parse(e.s.cfg.ExternalURL)
	for _, c := range e.c.Jar.Cookies(u) {
		req.AddCookie(c)
	}
	return req
}

// session decodes the jar's session cookie, expired or not.
func (e *renewEnv) session() *Session {
	e.t.Helper()
	sess, ok := e.s.sessions.Stale(e.request())
	if !ok {
		e.t.Fatal("no session cookie")
	}
	return sess
}

func (e *renewEnv) grants() int { return e.f.refreshGrants(1) }

// cleared reports whether cookies clear both the session and the refresh
// cookie.
func (e *renewEnv) cleared(cookies []*http.Cookie) bool {
	for _, name := range []string{e.s.sessions.SessionCookie(), e.s.sessions.RefreshCookie()} {
		if c := cookieNamed(cookies, name); c == nil || c.MaxAge >= 0 {
			return false
		}
	}
	return true
}

func TestAuthedLoginStartsShortSession(t *testing.T) {
	e := newRenewEnv(t, nil, nil)
	if got, want := e.session().Expires, e.start.Add(e.s.cfg.Auth.RenewAfter.Duration).Unix(); got != want {
		t.Errorf("session expires %d, want %d (renewAfter)", got, want)
	}
	e.at(e.s.cfg.Auth.RenewAfter.Duration - time.Second)
	if status, _, cookies := e.me(); status != http.StatusOK || len(cookies) != 0 || e.grants() != 0 {
		t.Errorf("before renewAfter: status %d, cookies %v, grants %d", status, cookies, e.grants())
	}
}

func TestAuthedRenews(t *testing.T) {
	e := newRenewEnv(t, nil, nil)
	old := e.cookie(e.s.sessions.RefreshCookie()).Value
	now := e.at(e.s.cfg.Auth.RenewAfter.Duration + time.Second)
	status, me, cookies := e.me()
	if status != http.StatusOK || me.Username != fakeEmail {
		t.Fatalf("status %d, me %+v", status, me)
	}
	sc, rc := cookieNamed(cookies, e.s.sessions.SessionCookie()), cookieNamed(cookies, e.s.sessions.RefreshCookie())
	if sc == nil || rc == nil || rc.Value == old {
		t.Fatalf("session cookie %v, new refresh cookie %v", sc != nil, rc != nil && rc.Value != old)
	}
	if got, want := e.session().Expires, now.Add(e.s.cfg.Auth.RenewAfter.Duration).Unix(); got != want {
		t.Errorf("session expires %d, want %d", got, want)
	}
	if want := now.Add(e.s.cfg.Auth.IdleTimeout.Duration); !sc.Expires.Equal(want) {
		t.Errorf("the browser keeps the session cookie until %v, want the idle deadline %v", sc.Expires, want)
	}
	if e.grants() != 1 {
		t.Errorf("grants = %d, want 1", e.grants())
	}
	if status, _, _ := e.me(); status != http.StatusOK || e.grants() != 1 {
		t.Errorf("the renewed session: status %d, grants %d", status, e.grants())
	}
	// The next renewal redeems the rotated token.
	e.at(2*e.s.cfg.Auth.RenewAfter.Duration + 2*time.Second)
	if status, _, _ := e.me(); status != http.StatusOK || e.grants() != 2 {
		t.Errorf("second renewal: status %d, grants %d", status, e.grants())
	}
}

func TestAuthedRenewsOncePerToken(t *testing.T) {
	e := newRenewEnv(t, nil, nil)
	e.at(e.s.cfg.Auth.RenewAfter.Duration + time.Second)
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if status, _, _ := e.me(); status != http.StatusOK {
				t.Errorf("status %d", status)
			}
		})
	}
	wg.Wait()
	if e.grants() != 1 {
		t.Errorf("grants = %d, want 1", e.grants())
	}
}

func TestAuthedEndsLogin(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		// after changes the provider or the UI after the login and returns
		// how long after the login's start the request is made.
		after func(*renewEnv) time.Duration
		log   string
	}{
		{name: "idle", after: func(e *renewEnv) time.Duration {
			return e.s.cfg.Auth.IdleTimeout.Duration + time.Second
		}},
		{name: "max lifetime", mutate: func(c *Config) { c.Auth.MaxLifetime.Duration = time.Hour },
			after: func(*renewEnv) time.Duration { return time.Hour }},
		{name: "refused", log: "renewal ended the login", after: func(e *renewEnv) time.Duration {
			e.f.set(func(sw *fakeOIDCSwitches) { sw.refuse = true })
			return e.s.cfg.Auth.RenewAfter.Duration + time.Second
		}},
		{name: "claims no longer map", log: "claim mapping", after: func(e *renewEnv) time.Duration {
			m, err := NewClaimMapper(Claims{Username: e.s.cfg.OIDC.Claims.Username, Roles: "42"})
			if err != nil {
				e.t.Fatal(err)
			}
			e.s.oidc.claims = m
			return e.s.cfg.Auth.RenewAfter.Duration + time.Second
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newRenewEnv(t, nil, tc.mutate)
			e.at(tc.after(e))
			status, _, cookies := e.me()
			if status != http.StatusUnauthorized || !e.cleared(cookies) {
				t.Errorf("status %d, cookies cleared %v", status, e.cleared(cookies))
			}
			if tc.log != "" && !strings.Contains(e.logs(), tc.log) {
				t.Errorf("logs: %s", e.logs())
			}
			if status, _, _ := e.me(); status != http.StatusUnauthorized {
				t.Errorf("next request: status %d", status)
			}
		})
	}
}

func TestAuthedCapsAtDeadline(t *testing.T) {
	e := newRenewEnv(t, nil, func(c *Config) { c.Auth.MaxLifetime.Duration = time.Hour })
	e.at(50 * time.Minute)
	if status, _, _ := e.me(); status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if got, want := e.session().Expires, e.start.Add(time.Hour).Unix(); got != want {
		t.Errorf("session expires %d, want the max lifetime %d", got, want)
	}
}

func TestAuthedProviderDown(t *testing.T) {
	e := newRenewEnv(t, nil, nil)
	old := e.cookie(e.s.sessions.RefreshCookie()).Value
	e.f.set(func(sw *fakeOIDCSwitches) { sw.down = true })
	now := e.at(e.s.cfg.Auth.RenewAfter.Duration + time.Second)
	status, me, cookies := e.me()
	if status != http.StatusOK || me.Username != fakeEmail || !slices.Equal(me.Roles, []string{"ai-operator-admin"}) {
		t.Fatalf("status %d, me %+v", status, me)
	}
	if cookieNamed(cookies, e.s.sessions.RefreshCookie()) != nil || e.cookie(e.s.sessions.RefreshCookie()).Value != old {
		t.Error("the refresh cookie was rewritten")
	}
	if got, want := e.session().Expires, now.Add(e.s.cfg.Auth.RenewAfter.Duration).Unix(); got != want {
		t.Errorf("session expires %d, want %d", got, want)
	}
	if !strings.Contains(e.logs(), "renewal failed, session kept") {
		t.Errorf("logs: %s", e.logs())
	}

	// Back up: the next renewal redeems the old token.
	e.f.set(func(sw *fakeOIDCSwitches) { sw.down = false })
	e.at(2*e.s.cfg.Auth.RenewAfter.Duration + 2*time.Second)
	if status, _, cookies := e.me(); status != http.StatusOK || cookieNamed(cookies, e.s.sessions.RefreshCookie()) == nil {
		t.Errorf("after the outage: status %d", status)
	}
}

func TestAuthedProviderDownNearIdleDeadline(t *testing.T) {
	e := newRenewEnv(t, nil, nil)
	e.f.set(func(sw *fakeOIDCSwitches) { sw.down = true })
	idle := e.start.Add(e.s.cfg.Auth.IdleTimeout.Duration)
	e.at(e.s.cfg.Auth.IdleTimeout.Duration - 5*time.Minute)
	if status, _, _ := e.me(); status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if got := e.session().Expires; got != idle.Unix() {
		t.Errorf("session expires %d, want the idle deadline %d", got, idle.Unix())
	}
}

func TestAuthedRenewalAppliesRoles(t *testing.T) {
	e := newRenewEnv(t, nil, nil)
	e.f.set(func(sw *fakeOIDCSwitches) { sw.roles = []string{"shop-devs"} })
	if _, me, _ := e.me(); !slices.Equal(me.Roles, []string{"ai-operator-admin"}) {
		t.Errorf("before the renewal: roles %v", me.Roles)
	}
	e.at(e.s.cfg.Auth.RenewAfter.Duration + time.Second)
	if status, me, _ := e.me(); status != http.StatusOK || !slices.Equal(me.Roles, []string{"shop-devs"}) {
		t.Errorf("status %d, roles %v", status, me.Roles)
	}
}

// TestAuthedContext checks what handlers (the streams, /api/v1/me) see: the
// renewed expiry and the effective refresh state.
func TestAuthedContext(t *testing.T) {
	var (
		exp time.Time
		rs  *RefreshState
	)
	capture := func(_ http.ResponseWriter, r *http.Request) { exp, rs = sessionExpiry(r), refreshOf(r) }
	serve := func(e *renewEnv) {
		e.t.Helper()
		exp, rs = time.Time{}, nil
		rec := httptest.NewRecorder()
		e.s.authed(capture).ServeHTTP(rec, e.request())
		if rec.Code != http.StatusOK {
			e.t.Fatalf("status %d", rec.Code)
		}
	}

	e := newRenewEnv(t, nil, nil)
	serve(e)
	if rs == nil || rs.Token != currentToken(e.f) || !exp.Equal(e.start.Add(e.s.cfg.Auth.RenewAfter.Duration)) {
		t.Errorf("valid session: expiry %v, refresh state from the cookie %v", exp, rs != nil)
	}
	now := e.at(e.s.cfg.Auth.RenewAfter.Duration + time.Second)
	serve(e)
	if rs == nil || rs.Token != currentToken(e.f) || rs.Renewed != now.Unix() {
		t.Error("the context lacks the renewed refresh state")
	}
	if want := now.Add(e.s.cfg.Auth.RenewAfter.Duration); !exp.Equal(want) {
		t.Errorf("stream deadline %v, want the renewed expiry %v", exp, want)
	}

	e = newRenewEnv(t, func(sw *fakeOIDCSwitches) { sw.noRefresh = true }, nil)
	serve(e)
	if rs != nil || !exp.Equal(e.start.Add(e.s.cfg.Auth.SessionTTL.Duration)) {
		t.Errorf("without a refresh token: expiry %v, refresh state %v", exp, rs != nil)
	}
}

func TestAuthedWithoutRefreshToken(t *testing.T) {
	e := newRenewEnv(t, func(sw *fakeOIDCSwitches) { sw.noRefresh = true }, nil)
	if got, want := e.session().Expires, e.start.Add(e.s.cfg.Auth.SessionTTL.Duration).Unix(); got != want {
		t.Errorf("session expires %d, want %d (sessionTTL)", got, want)
	}
	e.at(e.s.cfg.Auth.RenewAfter.Duration + time.Second)
	if status, _, cookies := e.me(); status != http.StatusOK || len(cookies) != 0 {
		t.Errorf("after renewAfter: status %d, cookies %v", status, cookies)
	}
	e.at(e.s.cfg.Auth.SessionTTL.Duration)
	if status, _, cookies := e.me(); status != http.StatusUnauthorized || len(cookies) != 0 {
		t.Errorf("after sessionTTL: status %d, cookies %v", status, cookies)
	}
}

func TestLogoutEndsRenewal(t *testing.T) {
	e := newRenewEnv(t, nil, nil)
	resp, err := e.c.Post(e.s.cfg.ExternalURL+pathLogout, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !e.cleared(resp.Cookies()) {
		t.Error("logout did not clear both cookies")
	}
	e.at(e.s.cfg.Auth.RenewAfter.Duration + time.Second)
	if status, _, _ := e.me(); status != http.StatusUnauthorized || e.grants() != 0 {
		t.Errorf("after logout: status %d, grants %d", status, e.grants())
	}
}
