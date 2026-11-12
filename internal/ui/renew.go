package ui

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// renewTimeout bounds a renewal: discovery, refresh grant and userinfo.
var renewTimeout = 10 * time.Second

// renewCache is how long a successful renewal is handed to requests that
// still carry the refresh token it redeemed.
const renewCache = 2 * time.Minute

// renewals keeps renewals single-flight per refresh token, keyed by its
// SHA-256, and their successes for renewCache. It works within one process:
// with more than one UI replica, a parallel renewal on another pod can lose.
type renewals struct {
	mu  sync.Mutex
	m   map[[32]byte]*renewFlight
	now func() time.Time
}

// renewFlight is one renewal; its results are set before done is closed.
type renewFlight struct {
	done chan struct{}
	p    Principal
	rs   RefreshState
	err  error
	at   time.Time // when it finished, under renewals.mu; zero in flight
}

// renew is redeem shared by all requests with the same refresh token: one
// leader redeems it, detached from its own cancellation; the others wait
// for its result, or reuse it within renewCache. Failures are not kept.
func (o *oidcClient) renew(ctx context.Context, rs RefreshState, prev Principal) (Principal, RefreshState, error) {
	r, key := &o.renewals, sha256.Sum256([]byte(rs.Token))
	r.mu.Lock()
	for k, f := range r.m {
		if !f.at.IsZero() && r.now().Sub(f.at) > renewCache {
			delete(r.m, k)
		}
	}
	f := r.m[key]
	if f != nil && (f.at.IsZero() || f.err == nil) {
		r.mu.Unlock()
		select {
		case <-f.done:
			return f.p, f.rs, f.err
		case <-ctx.Done():
			return Principal{}, rs, ctx.Err()
		}
	}
	f = &renewFlight{done: make(chan struct{})}
	r.m[key] = f
	r.mu.Unlock()
	f.p, f.rs, f.err = o.redeem(context.WithoutCancel(ctx), rs, prev)
	r.mu.Lock()
	if f.at = r.now(); f.err != nil {
		delete(r.m, key)
	}
	r.mu.Unlock()
	close(f.done)
	if f.err == nil {
		o.log.Info("session renewed", "user", f.p.Username, "roles", f.p.Roles)
	}
	return f.p, f.rs, f.err
}

// Renewal errors, tested with errors.Is: a refusal ends the login, an
// unavailable provider keeps the session. Causes never carry the token.
var (
	errRenewRefused     = errors.New("the provider refused the renewal")
	errRenewUnavailable = errors.New("the provider is unavailable for the renewal")
	errUserinfoSubject  = errors.New("userinfo is for another subject")
)

// mergeUserinfo adds the userinfo claims that claims lacks: providers put
// some (Zitadel: the roles, unless configured otherwise) only there. It
// changes nothing and returns an error when userinfo fails or is for
// another subject.
func (o *oidcClient) mergeUserinfo(ctx context.Context, p *oidc.Provider, tok *oauth2.Token, subject string, claims map[string]any) error {
	ui, err := p.UserInfo(ctx, oauth2.StaticTokenSource(tok))
	extra := map[string]any{}
	switch {
	case err != nil:
		if !errors.Is(err, context.Canceled) {
			o.log.V(1).Info("userinfo not used", "error", err.Error())
		}
		return err
	case ui.Subject != subject:
		return errUserinfoSubject
	case ui.Claims(&extra) != nil:
		return errors.New("userinfo claims do not decode")
	}
	for k, v := range extra {
		if _, ok := claims[k]; !ok {
			claims[k] = v
		}
	}
	return nil
}

// redeem renews the login of rs with its refresh token: refresh grant, a
// new ID token checked against the login's subject and nonce, userinfo, and
// the claims mapped again. It returns the principal and the slid state with
// the rotated token. prev is kept when the grant succeeded without claims,
// since the old token is dead by then.
func (o *oidcClient) redeem(ctx context.Context, rs RefreshState, prev Principal) (Principal, RefreshState, error) {
	now := o.sessions.now()
	if !rs.Live(now) {
		return Principal{}, rs, fmt.Errorf("%w: the login is past its deadline", errRenewRefused)
	}
	ctx, cancel := context.WithTimeout(ctx, renewTimeout)
	defer cancel()
	p, err := o.discover(ctx)
	if err != nil {
		return Principal{}, rs, fmt.Errorf("%w: discovery: %w", errRenewUnavailable, err)
	}
	tok, err := o.oauth2Config(p).TokenSource(ctx, &oauth2.Token{RefreshToken: rs.Token, Expiry: time.Unix(1, 0)}).Token()
	if re := (*oauth2.RetrieveError)(nil); errors.As(err, &re) && (re.ErrorCode == "invalid_grant" || re.ErrorCode == "unauthorized_client") {
		return Principal{}, rs, fmt.Errorf("%w: %w", errRenewRefused, err)
	} else if err != nil {
		return Principal{}, rs, fmt.Errorf("%w: %w", errRenewUnavailable, err)
	}
	// oauth2 keeps the old refresh token when the provider did not rotate.
	next := rs
	next.Token = tok.RefreshToken
	next.Slide(now, o.cfg.Auth.IdleTimeout.Duration)
	claims := map[string]any{}
	if raw, _ := tok.Extra("id_token").(string); raw != "" {
		id, err := p.Verifier(&oidc.Config{ClientID: o.cfg.OIDC.ClientID}).Verify(ctx, raw)
		if err == nil && (id.Subject != rs.Subject || id.Nonce != "" && id.Nonce != rs.Nonce) {
			err = errors.New("subject or nonce changed")
		}
		if err == nil {
			err = id.Claims(&claims)
		}
		if err != nil {
			return Principal{}, rs, fmt.Errorf("%w: ID token: %w", errRenewRefused, err)
		}
		_ = o.mergeUserinfo(ctx, p, tok, rs.Subject, claims)
	} else if err := o.mergeUserinfo(ctx, p, tok, rs.Subject, claims); errors.Is(err, errUserinfoSubject) {
		return Principal{}, rs, fmt.Errorf("%w: %w", errRenewRefused, err)
	} else if err != nil {
		o.log.Info("renewed without claims; roles were not refreshed", "user", prev.Username, "error", err.Error())
		return prev, next, nil
	}
	username, roles, err := o.claims.Map(claims)
	if err != nil {
		return Principal{}, rs, fmt.Errorf("%w: claim mapping: %w", errRenewRefused, err)
	}
	return Principal{Username: username, Roles: roles}, next, nil
}

// startRenewing writes the session cookie for p of a login with rs: valid
// for auth.renewAfter but never past the login's deadline, until which the
// browser keeps it. It returns the session's expiry.
func (o *oidcClient) startRenewing(w http.ResponseWriter, p Principal, rs RefreshState) (time.Time, error) {
	end := rs.Deadline()
	exp := o.sessions.now().Add(o.cfg.Auth.RenewAfter.Duration)
	if end.Before(exp) {
		exp = end
	}
	return exp, o.sessions.StartKept(w, p, exp, end)
}

// renewSession renews the expired session of r with rs and writes the
// cookies. An unavailable provider keeps the old principal for another
// auth.renewAfter; any other failure reports false and the login ends.
func (o *oidcClient) renewSession(w http.ResponseWriter, r *http.Request, rs *RefreshState) (*Session, *RefreshState, bool) {
	prev, _ := o.sessions.Stale(r)
	p, next, err := o.renew(r.Context(), *rs, prev.Principal)
	renewed := err == nil
	if errors.Is(err, errRenewUnavailable) {
		o.log.Info("renewal failed, session kept", "user", prev.Username, "error", err.Error())
		p, next, err = prev.Principal, *rs, nil
	}
	if err == nil && p.Username == "" {
		err = errors.New("no principal to keep")
	}
	var exp time.Time
	if err == nil {
		if exp, err = o.startRenewing(w, p, next); err == nil && renewed {
			err = o.sessions.StartRefresh(w, next)
		}
	}
	if err != nil {
		o.log.Info("renewal ended the login", "user", prev.Username, "error", err.Error())
		return nil, nil, false
	}
	return &Session{Principal: p, Expires: exp.Unix()}, &next, true
}
