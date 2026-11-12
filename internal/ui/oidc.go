package ui

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-logr/logr"
	"golang.org/x/oauth2"
)

// Paths of the login flow.
const (
	pathLogin    = "/oauth2/login"
	pathCallback = "/oauth2/callback"
	pathLogout   = "/oauth2/logout"
)

// oidcClient runs the authorization code flow with PKCE. The provider is
// discovered on first use, so the UI starts while the provider is down.
type oidcClient struct {
	cfg      *Config
	sessions *Sessions
	claims   *ClaimMapper
	log      logr.Logger

	mu       sync.Mutex
	provider *oidc.Provider
	// endSession is the provider's RP-initiated logout endpoint, if any.
	endSession string

	renewals renewals
}

func (o *oidcClient) discover(ctx context.Context) (*oidc.Provider, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider != nil {
		return o.provider, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	p, err := oidc.NewProvider(ctx, o.cfg.OIDC.IssuerURL)
	if err != nil {
		return nil, err
	}
	var meta struct {
		EndSession string `json:"end_session_endpoint"`
	}
	_ = p.Claims(&meta)
	o.provider, o.endSession = p, meta.EndSession
	return p, nil
}

func (o *oidcClient) oauth2Config(p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     o.cfg.OIDC.ClientID,
		ClientSecret: o.cfg.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  o.cfg.ExternalURL + pathCallback,
		Scopes:       o.cfg.OIDC.Scopes,
	}
}

// login redirects to the provider.
func (o *oidcClient) login(w http.ResponseWriter, r *http.Request) {
	p, err := o.discover(r.Context())
	if err != nil {
		o.log.Error(err, "OIDC discovery failed", "issuer", o.cfg.OIDC.IssuerURL)
		o.fail(w, r, errUnreachable)
		return
	}
	st := LoginState{
		State:    rand.Text(),
		Nonce:    rand.Text(),
		Verifier: oauth2.GenerateVerifier(),
		Return:   safeReturn(r.URL.Query().Get("return")),
	}
	if err := o.sessions.StartLogin(w, st); err != nil {
		o.log.Error(err, "cannot start login")
		o.fail(w, r, errSession)
		return
	}
	u := o.oauth2Config(p).AuthCodeURL(st.State, oidc.Nonce(st.Nonce), oauth2.S256ChallengeOption(st.Verifier))
	http.Redirect(w, r, u, http.StatusFound)
}

// callback finishes the login: it checks the state, redeems the code,
// verifies the ID token and maps its claims to a principal.
func (o *oidcClient) callback(w http.ResponseWriter, r *http.Request) {
	st, ok := o.sessions.Login(w, r)
	q := r.URL.Query()
	switch {
	case !ok:
		o.fail(w, r, errExpired)
		return
	case q.Get("state") != st.State:
		o.fail(w, r, errState)
		return
	case q.Get("error") != "":
		o.log.Info("provider refused the login", "error", q.Get("error"), "description", q.Get("error_description"))
		o.fail(w, r, errRefused)
		return
	}
	p, err := o.discover(r.Context())
	if err != nil {
		o.fail(w, r, errUnreachable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	conf := o.oauth2Config(p)
	tok, err := conf.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		o.log.Error(err, "code exchange failed")
		o.fail(w, r, errToken)
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		o.fail(w, r, errToken)
		return
	}
	idToken, err := p.Verifier(&oidc.Config{ClientID: o.cfg.OIDC.ClientID}).Verify(ctx, raw)
	if err != nil {
		o.log.Error(err, "ID token verification failed")
		o.fail(w, r, errToken)
		return
	}
	if idToken.Nonce != st.Nonce {
		o.fail(w, r, errToken)
		return
	}
	claims := map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		o.fail(w, r, errToken)
		return
	}
	_ = o.mergeUserinfo(ctx, p, tok, idToken.Subject, claims)
	username, roles, err := o.claims.Map(claims)
	if err != nil {
		o.log.Error(err, "claim mapping failed", "subject", idToken.Subject)
		o.fail(w, r, errClaims)
		return
	}
	if err := o.sessions.Start(w, Principal{Username: username, Roles: roles}); err != nil {
		o.log.Error(err, "cannot start session", "user", username)
		o.fail(w, r, errSession)
		return
	}
	// The session lasts auth.sessionTTL either way until renewal exists.
	if rs, ok := o.refreshState(w, tok, idToken.Subject, st.Nonce, username); ok {
		if err := o.sessions.StartRefresh(w, rs); err != nil {
			o.log.Error(err, "cannot start refresh cookie", "user", username)
			o.fail(w, r, errSession)
			return
		}
	}
	o.log.Info("login", "user", username, "roles", roles)
	http.Redirect(w, r, st.Return, http.StatusFound)
}

// refreshState returns the refresh state for a login's token response. Without
// a refresh token it clears a stale cookie, so an earlier login's token is
// never paired with this user, and reports false.
func (o *oidcClient) refreshState(w http.ResponseWriter, tok *oauth2.Token, subject, nonce, user string) (RefreshState, bool) {
	if tok.RefreshToken == "" {
		o.sessions.Clear(w, o.sessions.RefreshCookie())
		o.log.Info("no refresh token from the provider; the login lasts auth.sessionTTL and is not renewed — check that oidc.scopes includes offline_access and the client allows the refresh_token grant", "user", user)
		return RefreshState{}, false
	}
	now := o.sessions.now()
	rs := RefreshState{Token: tok.RefreshToken, Subject: subject, Nonce: nonce, Since: now.Unix()}
	rs.Slide(now, o.cfg.Auth.IdleTimeout.Duration, o.cfg.Auth.MaxLifetime.Duration)
	return rs, true
}

// LoginRedirect is where the browser goes after logging out.
type LoginRedirect struct {
	Redirect string `json:"redirect"`
}

// logout ends the session here and returns where the browser goes next:
// the provider's logout endpoint, which ends the login there too.
func (o *oidcClient) logout(w http.ResponseWriter, r *http.Request) {
	o.sessions.Clear(w, o.sessions.SessionCookie())
	next := "/"
	if _, err := o.discover(r.Context()); err == nil && o.endSession != "" {
		q := url.Values{
			"client_id":                {o.cfg.OIDC.ClientID},
			"post_logout_redirect_uri": {o.cfg.ExternalURL + "/"},
		}
		next = o.endSession + "?" + q.Encode()
	}
	writeJSON(w, http.StatusOK, LoginRedirect{Redirect: next})
}

// Login errors are sent to the app as codes, which it turns into messages,
// so a link cannot make the sign-in page show text of its choosing. The
// details are in the log.
const (
	errUnreachable = "unreachable"
	errExpired     = "expired"
	errState       = "state"
	errRefused     = "refused"
	errToken       = "token"
	errClaims      = "claims"
	errSession     = "session"
)

// fail sends the browser back to the app's sign-in page with an error code.
func (o *oidcClient) fail(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/?login_error="+code, http.StatusFound)
}

// safeReturn only allows paths on this host as the target after login.
// Browsers drop tabs and newlines from URLs, so "/\t/evil.com" would turn
// into "//evil.com": control characters are refused before parsing.
func safeReturn(s string) string {
	if strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '\\' }) {
		return "/"
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil ||
		!strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") ||
		strings.HasPrefix(u.Path, "/oauth2/") || strings.HasPrefix(u.Path, "/api/") {
		return "/"
	}
	out := u.EscapedPath()
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	return out
}
