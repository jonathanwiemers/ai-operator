package ui

import (
	"context"
	"crypto/rand"
	"errors"
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
		o.fail(w, r, "The identity provider cannot be reached.")
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
		o.fail(w, r, "The login could not be started.")
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
		o.fail(w, r, "The login expired or was started in another browser. Please sign in again.")
		return
	case q.Get("state") != st.State:
		o.fail(w, r, "The login state does not match. Please sign in again.")
		return
	case q.Get("error") != "":
		o.log.Info("provider refused the login", "error", q.Get("error"), "description", q.Get("error_description"))
		o.fail(w, r, "The identity provider refused the login: "+q.Get("error"))
		return
	}
	p, err := o.discover(r.Context())
	if err != nil {
		o.fail(w, r, "The identity provider cannot be reached.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	conf := o.oauth2Config(p)
	tok, err := conf.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		o.log.Error(err, "code exchange failed")
		o.fail(w, r, "The login code could not be redeemed.")
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		o.fail(w, r, "The identity provider returned no ID token.")
		return
	}
	idToken, err := p.Verifier(&oidc.Config{ClientID: o.cfg.OIDC.ClientID}).Verify(ctx, raw)
	if err != nil {
		o.log.Error(err, "ID token verification failed")
		o.fail(w, r, "The ID token is invalid.")
		return
	}
	if idToken.Nonce != st.Nonce {
		o.fail(w, r, "The ID token does not belong to this login.")
		return
	}
	claims := map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		o.fail(w, r, "The ID token's claims cannot be read.")
		return
	}
	// Providers put some claims (Zitadel: the roles, unless configured
	// otherwise) only into the userinfo response. The ID token wins where
	// both have a claim.
	if ui, err := p.UserInfo(ctx, oauth2.StaticTokenSource(tok)); err == nil {
		extra := map[string]any{}
		if ui.Subject == idToken.Subject && ui.Claims(&extra) == nil {
			for k, v := range extra {
				if _, ok := claims[k]; !ok {
					claims[k] = v
				}
			}
		}
	} else if !errors.Is(err, context.Canceled) {
		o.log.V(1).Info("userinfo not used", "error", err.Error())
	}
	username, roles, err := o.claims.Map(claims)
	if err != nil {
		o.log.Error(err, "claim mapping failed", "subject", idToken.Subject)
		o.fail(w, r, "Your account's claims could not be mapped: "+err.Error())
		return
	}
	if err := o.sessions.Start(w, Principal{Username: username, Roles: roles}); err != nil {
		o.log.Error(err, "cannot start session", "user", username)
		o.fail(w, r, err.Error())
		return
	}
	o.log.Info("login", "user", username, "roles", roles)
	http.Redirect(w, r, st.Return, http.StatusFound)
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
	writeJSON(w, http.StatusOK, map[string]string{"redirect": next})
}

// fail sends the browser back to the app's sign-in page with a message.
func (o *oidcClient) fail(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/?login_error="+url.QueryEscape(msg), http.StatusFound)
}

// safeReturn only allows paths on this host as the target after login.
func safeReturn(s string) string {
	if !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") || strings.ContainsAny(s, "\\\r\n") {
		return "/"
	}
	if strings.HasPrefix(s, "/oauth2/") || strings.HasPrefix(s, "/api/") {
		return "/"
	}
	return s
}
