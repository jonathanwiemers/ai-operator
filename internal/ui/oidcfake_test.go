package ui

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-logr/logr"
	"golang.org/x/oauth2"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// The fake provider's client and user.
const (
	fakeClientID     = "desk-client"
	fakeClientSecret = "desk-secret"
	fakeSubject      = "user-1"
	fakeEmail        = "ada@example.com"
	fakeKeyID        = "fake-key-1"
	// zitadelRolesClaim is where Zitadel puts the project roles: a map from
	// role to {orgID: orgDomain}.
	zitadelRolesClaim = "urn:zitadel:iam:org:project:roles"
)

// fakeOIDC is an OpenID provider for the login, refresh and logout tests:
// discovery, JWKS, the authorization endpoint (it logs the user in without a
// page), the token endpoint with the authorization_code (PKCE S256) and
// refresh_token grants, userinfo and revocation (RFC 7009). Refresh tokens
// rotate like Zitadel's: only the latest one of a login works.
//
// It never logs or prints a token: later tests assert that refresh tokens
// stay out of the UI's logs.
type fakeOIDC struct {
	srv *httptest.Server
	key *rsa.PrivateKey

	mu sync.Mutex
	sw fakeOIDCSwitches
	// codes are the authorization codes not yet redeemed.
	codes map[string]fakeCode
	// logins in the order of their code grants; login n is logins[n-1].
	logins []*fakeLogin
	// refresh maps every refresh token ever issued to its login.
	refresh map[string]*fakeLogin
	// access maps the access tokens to the subject they were issued for.
	access  map[string]fakeAccess
	revoked map[string]bool
}

// fakeOIDCSwitches change how the fake answers. Tests change them with set
// while requests run.
type fakeOIDCSwitches struct {
	// deny makes the authorization endpoint refuse the login
	// (error=access_denied).
	deny bool
	// refuse makes every refresh grant answer 400 invalid_grant.
	refuse bool
	// down makes the token endpoint answer 503, after downDelay (or until
	// the client gives up) when that is set.
	down      bool
	downDelay time.Duration
	// noRefresh leaves the refresh token out of the code grant's answer.
	noRefresh bool
	// noIDTokenOnRefresh leaves the ID token out of refresh answers.
	noIDTokenOnRefresh bool
	// noRevocation leaves revocation_endpoint out of the discovery
	// document. The UI discovers once, so set it before the first login.
	noRevocation bool
	// roles are the user's roles in the next ID token or userinfo answer.
	// None leaves the roles claim out, as Zitadel does.
	roles []string
	// subject, when set, is the sub of the ID tokens of refresh answers.
	subject string
	// nonce, when set, is the nonce of the ID tokens of refresh answers
	// instead of the login's.
	nonce string
}

type fakeCode struct {
	nonce, challenge, redirectURI string
}

// fakeLogin is one code grant and the refresh tokens that followed it.
type fakeLogin struct {
	nonce string
	// current is the only refresh token of the login that still works.
	current string
	// grants counts the refresh grant requests with any of the login's
	// refresh tokens, answered or refused.
	grants int
	// revoked is set once the current refresh token was revoked.
	revoked bool
}

type fakeAccess struct {
	subject string
}

func newFakeOIDC(t *testing.T) *fakeOIDC {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeOIDC{
		key:     key,
		sw:      fakeOIDCSwitches{roles: []string{"ai-operator-admin"}},
		codes:   map[string]fakeCode{},
		refresh: map[string]*fakeLogin{},
		access:  map[string]fakeAccess{},
		revoked: map[string]bool{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", f.discovery)
	mux.HandleFunc("GET /keys", f.jwks)
	mux.HandleFunc("GET /authorize", f.authorize)
	mux.HandleFunc("POST /token", f.token)
	mux.HandleFunc("GET /userinfo", f.userinfo)
	mux.HandleFunc("POST /revoke", f.revoke)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// issuer is the fake's issuer URL.
func (f *fakeOIDC) issuer() string { return f.srv.URL }

// set changes the switches.
func (f *fakeOIDC) set(change func(*fakeOIDCSwitches)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(&f.sw)
}

// loginCount is the number of code grants so far.
func (f *fakeOIDC) loginCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.logins)
}

// refreshGrants counts the refresh grant requests of login n (from 1).
func (f *fakeOIDC) refreshGrants(n int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logins[n-1].grants
}

// loginRevoked reports whether the current refresh token of login n (from
// 1) was revoked.
func (f *fakeOIDC) loginRevoked(n int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logins[n-1].revoked
}

// refreshTokens returns every refresh token issued so far, for tests that
// check they appear nowhere they should not.
func (f *fakeOIDC) refreshTokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.refresh))
	for tok := range f.refresh {
		out = append(out, tok)
	}
	return out
}

func (f *fakeOIDC) discovery(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	noRevocation := f.sw.noRevocation
	f.mu.Unlock()
	iss := f.issuer()
	doc := map[string]any{
		"issuer":                                iss,
		"authorization_endpoint":                iss + "/authorize",
		"token_endpoint":                        iss + "/token",
		"userinfo_endpoint":                     iss + "/userinfo",
		"jwks_uri":                              iss + "/keys",
		"end_session_endpoint":                  iss + "/logout",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
	}
	if !noRevocation {
		doc["revocation_endpoint"] = iss + "/revoke"
	}
	fakeJSON(w, http.StatusOK, doc)
}

func (f *fakeOIDC) jwks(w http.ResponseWriter, _ *http.Request) {
	pub := f.key.PublicKey
	fakeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": fakeKeyID,
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

// authorize logs the user in at once and redirects back with a code bound
// to the request's nonce, PKCE challenge and redirect URI.
func (f *fakeOIDC) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || redirect.Host == "" || q.Get("client_id") != fakeClientID ||
		q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" ||
		q.Get("code_challenge") == "" || q.Get("state") == "" {
		http.Error(w, "bad authorization request", http.StatusBadRequest)
		return
	}
	back := url.Values{"state": {q.Get("state")}}
	f.mu.Lock()
	if f.sw.deny {
		back.Set("error", "access_denied")
		back.Set("error_description", "the user refused")
	} else {
		code := rand.Text()
		f.codes[code] = fakeCode{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirectURI: q.Get("redirect_uri")}
		back.Set("code", code)
	}
	f.mu.Unlock()
	redirect.RawQuery = back.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

// token serves the authorization_code and refresh_token grants.
func (f *fakeOIDC) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		fakeOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if !fakeClientAuth(r) {
		fakeOAuthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	grant := r.PostForm.Get("grant_type")
	var login *fakeLogin
	if grant == "refresh_token" {
		f.mu.Lock()
		if login = f.refresh[r.PostForm.Get("refresh_token")]; login != nil {
			login.grants++
		}
		f.mu.Unlock()
	}
	f.mu.Lock()
	down, delay := f.sw.down, f.sw.downDelay
	f.mu.Unlock()
	if down {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		fakeOAuthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	switch grant {
	case "authorization_code":
		f.codeGrant(w, r)
	case "refresh_token":
		f.refreshGrant(w, r, login)
	default:
		fakeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type")
	}
}

func (f *fakeOIDC) codeGrant(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	code := r.PostForm.Get("code")
	pending, ok := f.codes[code]
	delete(f.codes, code)
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || r.PostForm.Get("redirect_uri") != pending.redirectURI ||
		base64.RawURLEncoding.EncodeToString(sum[:]) != pending.challenge {
		fakeOAuthError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	login := &fakeLogin{nonce: pending.nonce}
	f.logins = append(f.logins, login)
	out := f.issue(fakeSubject, pending.nonce, true)
	if !f.sw.noRefresh {
		out["refresh_token"] = f.rotate(login)
	}
	fakeJSON(w, http.StatusOK, out)
}

func (f *fakeOIDC) refreshGrant(w http.ResponseWriter, r *http.Request, login *fakeLogin) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tok := r.PostForm.Get("refresh_token")
	if login == nil || f.sw.refuse || login.current != tok || f.revoked[tok] {
		fakeOAuthError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	subject, nonce := fakeSubject, login.nonce
	if f.sw.subject != "" {
		subject = f.sw.subject
	}
	if f.sw.nonce != "" {
		nonce = f.sw.nonce
	}
	out := f.issue(subject, nonce, !f.sw.noIDTokenOnRefresh)
	out["refresh_token"] = f.rotate(login)
	fakeJSON(w, http.StatusOK, out)
}

// issue returns a token answer with a new access token and, if withID, an
// ID token. f.mu is held.
func (f *fakeOIDC) issue(subject, nonce string, withID bool) map[string]any {
	access := rand.Text()
	f.access[access] = fakeAccess{subject: subject}
	out := map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 3600}
	if withID {
		now := time.Now()
		claims := map[string]any{
			"iss": f.issuer(), "aud": fakeClientID, "sub": subject,
			"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
			"nonce": nonce, "email": fakeEmail,
		}
		if roles := f.rolesClaim(); roles != nil {
			claims[zitadelRolesClaim] = roles
		}
		out["id_token"] = f.sign(claims)
	}
	return out
}

// rotate gives login a new refresh token; the one before stops working.
// f.mu is held.
func (f *fakeOIDC) rotate(login *fakeLogin) string {
	tok := rand.Text()
	login.current = tok
	f.refresh[tok] = login
	return tok
}

// rolesClaim is the roles in Zitadel's shape, nil without roles. f.mu is
// held.
func (f *fakeOIDC) rolesClaim() map[string]any {
	if len(f.sw.roles) == 0 {
		return nil
	}
	out := map[string]any{}
	for _, role := range f.sw.roles {
		out[role] = map[string]any{"1001": "acme.example.com"}
	}
	return out
}

// sign makes an RS256 JWT by hand (RFC 7515 compact serialization).
func (f *fakeOIDC) sign(claims map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": fakeKeyID})
	payload, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		panic(err) // a 2048 bit key signs any SHA-256 sum
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (f *fakeOIDC) userinfo(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	grant, known := f.access[tok]
	if !ok || !known || f.revoked[tok] {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		fakeOAuthError(w, http.StatusUnauthorized, "invalid_token")
		return
	}
	out := map[string]any{"sub": grant.subject, "email": fakeEmail}
	if roles := f.rolesClaim(); roles != nil {
		out[zitadelRolesClaim] = roles
	}
	fakeJSON(w, http.StatusOK, out)
}

// revoke records the revoked token. Like RFC 7009 says, unknown tokens get
// 200 too.
func (f *fakeOIDC) revoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || r.PostForm.Get("token") == "" {
		fakeOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if !fakeClientAuth(r) {
		fakeOAuthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	f.mu.Lock()
	tok := r.PostForm.Get("token")
	f.revoked[tok] = true
	if login := f.refresh[tok]; login != nil && login.current == tok {
		login.revoked = true
	}
	f.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

// fakeClientAuth checks the client's credentials, sent with basic
// authentication (form-encoded, RFC 6749 2.3.1) or in the form.
func fakeClientAuth(r *http.Request) bool {
	id, secret, ok := r.BasicAuth()
	if ok {
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	} else {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	return id == fakeClientID && secret == fakeClientSecret
}

func fakeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fakeOAuthError answers with an OAuth error code; the description never
// carries a token.
func fakeOAuthError(w http.ResponseWriter, status int, code string) {
	fakeJSON(w, status, map[string]string{"error": code, "error_description": "fake provider: " + code})
}

// newOIDCTestServer starts the UI with OIDC login against f, on its own
// httptest server. mutate (may be nil) changes the config before defaults
// and validation. The client keeps cookies and does not follow redirects,
// so tests step through them.
func newOIDCTestServer(t *testing.T, f *fakeOIDC, mutate func(*Config)) (*Server, *http.Client) {
	t.Helper()
	var handler http.Handler
	ui := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(ui.Close)
	cfg := &Config{
		ExternalURL: ui.URL,
		OIDC: OIDCConfig{
			IssuerURL: f.issuer(),
			ClientID:  fakeClientID,
			Claims:    Claims{Roles: `claims["` + zitadelRolesClaim + `"]`},
		},
		SessionKey:   strings.Repeat("k", 32),
		ClientSecret: fakeClientSecret,
	}
	if mutate != nil {
		mutate(cfg)
	}
	cfg.Default()
	if err := cfg.Validate("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().Build()
	s, err := NewServer(cfg, reader, NewHub(), fstest.MapFS{}, logr.Discard())
	if err != nil {
		t.Fatal(err)
	}
	handler = s.Handler()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, &http.Client{
		Jar:           jar,
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// redirectOf requests target and returns where it redirects to. It fails
// the test unless the answer is a redirect. Neither the target nor the
// redirect are printed: they carry codes and state.
func redirectOf(t *testing.T, c *http.Client, target string) (string, []*http.Cookie) {
	t.Helper()
	resp, err := c.Get(target)
	if err != nil {
		t.Fatal("request failed") // the error would quote the URL
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status %d, want a redirect", resp.StatusCode)
	}
	return resp.Header.Get("Location"), resp.Cookies()
}

// startLogin opens the UI's login with the return path and returns the
// provider's authorization URL it redirects to.
func startLogin(t *testing.T, s *Server, c *http.Client, returnTo string) string {
	t.Helper()
	authURL, _ := redirectOf(t, c, s.cfg.ExternalURL+pathLogin+"?return="+url.QueryEscape(returnTo))
	return authURL
}

// loginThrough runs a whole login: /oauth2/login, the fake's authorization
// endpoint and /oauth2/callback. It returns where the callback sends the
// browser and the cookies it set.
func loginThrough(t *testing.T, s *Server, c *http.Client, returnTo string) (string, []*http.Cookie) {
	t.Helper()
	callback, _ := redirectOf(t, c, startLogin(t, s, c, returnTo))
	if !strings.HasPrefix(callback, s.cfg.ExternalURL+pathCallback+"?") {
		t.Fatal("the provider did not redirect to the callback")
	}
	return redirectOf(t, c, callback)
}

// cookieNamed returns the cookie called name, nil if there is none.
func cookieNamed(cookies []*http.Cookie, name string) *http.Cookie {
	i := slices.IndexFunc(cookies, func(c *http.Cookie) bool { return c.Name == name })
	if i < 0 {
		return nil
	}
	return cookies[i]
}

// TestFakeOIDCRotatesRefreshTokens checks the fake itself: the code grant
// gives a verifiable ID token, each refresh token works once, and a revoked
// one not at all.
func TestFakeOIDCRotatesRefreshTokens(t *testing.T) {
	f := newFakeOIDC(t)
	ctx := t.Context()
	p, err := oidc.NewProvider(ctx, f.issuer())
	if err != nil {
		t.Fatal(err)
	}
	conf := &oauth2.Config{ClientID: fakeClientID, ClientSecret: fakeClientSecret, Endpoint: p.Endpoint(),
		RedirectURL: "http://127.0.0.1/cb", Scopes: []string{"openid", "offline_access"}}
	verifier := oauth2.GenerateVerifier()
	browser := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	back, _ := redirectOf(t, browser, conf.AuthCodeURL("st", oidc.Nonce("n1"), oauth2.S256ChallengeOption(verifier)))
	u, err := url.Parse(back)
	if err != nil || u.Query().Get("state") != "st" {
		t.Fatal("authorization did not redirect back with the state")
	}
	if _, err := conf.Exchange(ctx, u.Query().Get("code"), oauth2.VerifierOption("wrong")); !isOAuthError(err, "invalid_grant") {
		t.Fatalf("code with a wrong PKCE verifier: %v", err)
	}
	back, _ = redirectOf(t, browser, conf.AuthCodeURL("st", oidc.Nonce("n1"), oauth2.S256ChallengeOption(verifier)))
	u, _ = url.Parse(back)
	tok, err := conf.Exchange(ctx, u.Query().Get("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		t.Fatalf("code grant: %v", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	id, err := p.Verifier(&oidc.Config{ClientID: fakeClientID}).Verify(ctx, raw)
	if err != nil {
		t.Fatalf("ID token: %v", err)
	}
	var claims map[string]any
	if err := id.Claims(&claims); err != nil {
		t.Fatal(err)
	}
	if id.Subject != fakeSubject || id.Nonce != "n1" || claims["email"] != fakeEmail || claims[zitadelRolesClaim] == nil {
		t.Errorf("ID token claims: sub %q, nonce %q, %v", id.Subject, id.Nonce, claims)
	}

	refresh := func(rt string) (*oauth2.Token, error) {
		return conf.TokenSource(ctx, &oauth2.Token{RefreshToken: rt}).Token()
	}
	first := tok.RefreshToken
	if first == "" {
		t.Fatal("no refresh token from the code grant")
	}
	second, err := refresh(first)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if second.RefreshToken == "" || second.RefreshToken == first {
		t.Error("the refresh token was not rotated")
	}
	if _, err := refresh(first); !isOAuthError(err, "invalid_grant") {
		t.Errorf("second use of a refresh token: %v, want invalid_grant", err)
	}
	third, err := refresh(second.RefreshToken)
	if err != nil {
		t.Fatalf("refresh with the rotated token: %v", err)
	}
	if got := f.refreshGrants(1); got != 3 {
		t.Errorf("refresh grants = %d, want 3", got)
	}

	form := url.Values{"token": {third.RefreshToken}, "token_type_hint": {"refresh_token"}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.issuer()+"/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(fakeClientID, fakeClientSecret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !f.loginRevoked(1) {
		t.Fatalf("revocation: status %d, revoked %v", resp.StatusCode, f.loginRevoked(1))
	}
	if _, err := refresh(third.RefreshToken); !isOAuthError(err, "invalid_grant") {
		t.Errorf("refresh with a revoked token: %v, want invalid_grant", err)
	}
}

// isOAuthError reports whether err is the token endpoint's error code.
func isOAuthError(err error, code string) bool {
	var re *oauth2.RetrieveError
	return errors.As(err, &re) && re.ErrorCode == code
}
