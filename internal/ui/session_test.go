package ui

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newTestSessions(t *testing.T, key string) *Sessions {
	t.Helper()
	s, err := NewSessions(key, true, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// roundTrip returns a request carrying the cookies w set.
func roundTrip(w *httptest.ResponseRecorder) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}
	return r
}

func TestSessionRoundTrip(t *testing.T) {
	s := newTestSessions(t, strings.Repeat("a", 32))
	w := httptest.NewRecorder()
	p := Principal{Username: "ada", Roles: []string{"admin"}}
	if err := s.Start(w, p); err != nil {
		t.Fatal(err)
	}
	c := w.Result().Cookies()[0]
	if c.Name != "__Host-ai-operator-session" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Errorf("cookie attributes: %+v", c)
	}
	if strings.Contains(c.Value, "ada") {
		t.Error("cookie value is not encrypted")
	}
	got, ok := s.Get(roundTrip(w))
	if !ok || !reflect.DeepEqual(got.Principal, p) {
		t.Fatalf("Get = %+v, %v", got, ok)
	}
}

func TestSessionRejectsForeignAndExpiredCookies(t *testing.T) {
	s := newTestSessions(t, strings.Repeat("a", 32))
	w := httptest.NewRecorder()
	if err := s.Start(w, Principal{Username: "ada"}); err != nil {
		t.Fatal(err)
	}

	other := newTestSessions(t, strings.Repeat("b", 32))
	if _, ok := other.Get(roundTrip(w)); ok {
		t.Error("a cookie sealed with another key was accepted")
	}

	r := roundTrip(w)
	c, _ := r.Cookie(s.SessionCookie())
	sealed, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 1 // always a change, unlike overwriting characters
	tampered := httptest.NewRequest(http.MethodGet, "/", nil)
	tampered.AddCookie(&http.Cookie{Name: c.Name, Value: base64.RawURLEncoding.EncodeToString(sealed)})
	if _, ok := s.Get(tampered); ok {
		t.Error("a tampered cookie was accepted")
	}

	s.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, ok := s.Get(roundTrip(w)); ok {
		t.Error("an expired session was accepted")
	}
}

func TestLoginCookieIsNoSessionCookie(t *testing.T) {
	s := newTestSessions(t, strings.Repeat("a", 32))
	w := httptest.NewRecorder()
	if err := s.StartLogin(w, LoginState{State: "st"}); err != nil {
		t.Fatal(err)
	}
	// Present the login cookie's value under the session cookie's name.
	login := w.Result().Cookies()[0]
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: s.SessionCookie(), Value: login.Value})
	if _, ok := s.Get(r); ok {
		t.Error("the login cookie was accepted as a session")
	}

	w2 := httptest.NewRecorder()
	st, ok := s.Login(w2, roundTrip(w))
	if !ok || st.State != "st" {
		t.Fatalf("Login = %+v, %v", st, ok)
	}
	if c := w2.Result().Cookies()[0]; c.MaxAge >= 0 {
		t.Error("the login cookie was not cleared")
	}
}

func TestSessionTooManyRoles(t *testing.T) {
	s := newTestSessions(t, strings.Repeat("a", 32))
	roles := make([]string, 200)
	for i := range roles {
		roles[i] = strings.Repeat("r", 20)
	}
	if err := s.Start(httptest.NewRecorder(), Principal{Username: "ada", Roles: roles}); err == nil {
		t.Fatal("want an error for a session that does not fit into a cookie")
	}
}

func TestSafeReturn(t *testing.T) {
	for in, want := range map[string]string{
		"":                         "/",
		"/projects/a/b":            "/projects/a/b",
		"/projects/a/b?x=1":        "/projects/a/b?x=1",
		"//evil.example.com":       "/",
		"/\\evil.example.com":      "/",
		"https://evil.example.com": "/",
		"/api/v1/projects":         "/",
		"/oauth2/login":            "/",
		"/\t/evil.example.com":     "/",
		"/\x0b/evil.example.com":   "/",
		"/%2F/evil.example.com":    "/",
		"/projects/a/shop.v2":      "/projects/a/shop.v2",
		"/projects/a%20b":          "/projects/a%20b",
	} {
		if got := safeReturn(in); got != want {
			t.Errorf("safeReturn(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRefreshCookieRoundTrip(t *testing.T) {
	s := newTestSessions(t, strings.Repeat("a", 32))
	now := time.Now()
	st := RefreshState{Token: "rt-1", Subject: "sub-1", Nonce: "n", Until: now.Add(24 * time.Hour).Unix(), Renewed: now.Unix(), IdleUntil: now.Add(time.Hour).Unix()}
	w := httptest.NewRecorder()
	if err := s.StartRefresh(w, st); err != nil {
		t.Fatal(err)
	}
	c := w.Result().Cookies()[0]
	if c.Name != "__Host-ai-operator-refresh" || !c.HttpOnly || !c.Secure || c.Path != "/" {
		t.Errorf("cookie attributes: %+v", c)
	}
	if strings.Contains(c.Value, "rt-1") {
		t.Error("cookie value is not encrypted")
	}
	if c.MaxAge < 3590 || c.MaxAge > 3600 {
		t.Errorf("MaxAge = %d, want about an hour", c.MaxAge)
	}
	got, ok := s.Refresh(roundTrip(w))
	if !ok || *got != st {
		t.Fatalf("Refresh = %+v, %v", got, ok)
	}

	// Idle: refused once IdleUntil has passed.
	s.now = func() time.Time { return now.Add(time.Hour) }
	if _, ok := s.Refresh(roundTrip(w)); ok {
		t.Error("an idle refresh cookie was accepted")
	}

	w2 := httptest.NewRecorder()
	s.Clear(w2, s.RefreshCookie())
	if c := w2.Result().Cookies()[0]; c.Name != s.RefreshCookie() || c.MaxAge >= 0 {
		t.Errorf("clear: %+v", c)
	}
}

func TestRefreshCookieName(t *testing.T) {
	for secure, want := range map[bool]string{true: "__Host-ai-operator-refresh", false: "ai-operator-refresh"} {
		s, err := NewSessions("k", secure, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.RefreshCookie(); got != want {
			t.Errorf("secure=%v: RefreshCookie() = %q, want %q", secure, got, want)
		}
	}
}

func TestRefreshCookieIsBoundToItsName(t *testing.T) {
	s := newTestSessions(t, strings.Repeat("a", 32))
	w := httptest.NewRecorder()
	if err := s.StartRefresh(w, RefreshState{Token: "rt", IdleUntil: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(w, Principal{Username: "ada"}); err != nil {
		t.Fatal(err)
	}
	if err := s.StartLogin(w, LoginState{State: "st"}); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, c := range w.Result().Cookies() {
		values[c.Name] = c.Value
	}
	// Each cookie's value under each other cookie's name.
	with := func(name, value string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(&http.Cookie{Name: name, Value: value})
		return r
	}
	for _, from := range []string{s.SessionCookie(), s.LoginCookie()} {
		if _, ok := s.Refresh(with(s.RefreshCookie(), values[from])); ok {
			t.Errorf("%s was accepted as the refresh cookie", from)
		}
	}
	if _, ok := s.Get(with(s.SessionCookie(), values[s.RefreshCookie()])); ok {
		t.Error("the refresh cookie was accepted as a session")
	}
	if _, ok := s.Login(httptest.NewRecorder(), with(s.LoginCookie(), values[s.RefreshCookie()])); ok {
		t.Error("the refresh cookie was accepted as a login")
	}
}

func TestRefreshCookieTooLarge(t *testing.T) {
	s := newTestSessions(t, strings.Repeat("a", 32))
	st := RefreshState{Token: strings.Repeat("t", 4000), IdleUntil: time.Now().Add(time.Hour).Unix()}
	if err := s.StartRefresh(httptest.NewRecorder(), st); err == nil {
		t.Fatal("want an error for a refresh cookie over the size limit")
	}
}

func TestRefreshStateDeadlines(t *testing.T) {
	since := time.Unix(1_800_000_000, 0)
	week := 7 * 24 * time.Hour
	st := RefreshState{IdleUntil: since.Add(week).Unix()}

	if got := st.Deadline(); !got.Equal(since.Add(week)) {
		t.Errorf("Deadline without Until = %v", got)
	}
	capped := st
	capped.Until = since.Add(24 * time.Hour).Unix()
	if got := capped.Deadline(); !got.Equal(since.Add(24 * time.Hour)) {
		t.Errorf("Deadline with Until 24h = %v", got)
	}
	late := st
	late.Until = since.Add(30 * 24 * time.Hour).Unix()
	if got := late.Deadline(); !got.Equal(since.Add(week)) {
		t.Errorf("Deadline with Until 30d = %v", got)
	}

	at := since.Add(2 * 24 * time.Hour)
	if !st.Live(at) || capped.Live(at) {
		t.Error("Live: want live without a cap and past a 24h cap")
	}
	if st.Live(since.Add(week)) {
		t.Error("Live at the idle deadline")
	}

	// Slide moves the idle deadline from now.
	st.Slide(at, week)
	if st.Renewed != at.Unix() || st.IdleUntil != at.Add(week).Unix() {
		t.Errorf("Slide without cap: %+v", st)
	}
	// ... but never past Until, which it keeps.
	st.Until = since.Add(5 * 24 * time.Hour).Unix()
	st.Slide(at, week)
	if st.Renewed != at.Unix() || st.IdleUntil != st.Until || st.Until != since.Add(5*24*time.Hour).Unix() {
		t.Errorf("Slide with cap: %+v", st)
	}
}

func TestStartUntil(t *testing.T) {
	s := newTestSessions(t, strings.Repeat("a", 32))
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }
	exp := now.Add(15 * time.Minute)
	w := httptest.NewRecorder()
	if err := s.StartUntil(w, Principal{Username: "ada"}, exp); err != nil {
		t.Fatal(err)
	}
	c := w.Result().Cookies()[0]
	if c.MaxAge != 900 || !c.Expires.Equal(exp) {
		t.Errorf("MaxAge = %d, Expires = %v", c.MaxAge, c.Expires)
	}
	got, ok := s.Get(roundTrip(w))
	if !ok || got.Expires != exp.Unix() || got.Username != "ada" {
		t.Fatalf("Get = %+v, %v", got, ok)
	}
}
