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
