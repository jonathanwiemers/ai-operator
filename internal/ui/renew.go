package ui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// renewTimeout bounds a renewal: discovery, refresh grant and userinfo.
var renewTimeout = 10 * time.Second

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
	now, maxLife := o.sessions.now(), o.cfg.Auth.MaxLifetime.Duration
	if !rs.Live(now, maxLife) {
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
	next.Slide(now, o.cfg.Auth.IdleTimeout.Duration, maxLife)
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
