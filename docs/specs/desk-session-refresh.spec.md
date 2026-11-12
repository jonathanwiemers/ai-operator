# Desk: stay logged in while you use it (OIDC refresh tokens)

Status: the milestone's spec, run by autopilot. Narrowed requirements and
requirements without an issue go into the [Steering log](#steering-log).

## Goal

1. A Desk login lasts as long as it is used: it slides with activity and ends
   after the provider's idle time (Zitadel here: 7 days), not after a fixed
   12 hours.
2. The session is renewed in the background with the provider's refresh
   token, so no request, live stream or unsent message is lost to a renewal.
3. Role changes and revoked logins at the provider take effect within
   minutes, not at the next login.
4. When a login does end, the Desk signs in again by itself if the provider
   still knows the user, and comes back to the same page.

## Background

Before the milestone, sessions were AES-GCM encrypted stateless cookies
(`internal/ui/session.go`) with the principal and a fixed expiry
(`auth.sessionTTL`, 12h). The login dropped the refresh token; the default
scopes had no `offline_access`. `authed` answered 401 without a valid
session, the web showed the sign-in page and `live.ts` stopped the event
stream for good. Logout did not revoke anything. The GitLab connection
cookie already refreshed its token on use (`internal/ui/gitlab.go`); the
renewer follows that pattern. Zitadel rotates refresh tokens on every use.

## Decisions (locked)

1. **Refresh token in its own encrypted cookie**, `__Host-ai-operator-refresh`
   (`ai-operator-refresh` when not secure): token, subject, nonce, start,
   last renewal and idle deadline, sealed with the session key. The UI stays
   stateless; the session cookie keeps its shape and size.
2. **Short session, renewed on use.** The session cookie is valid for
   `auth.renewAfter` (default 15m). `authed` renews an expired session with a
   valid refresh cookie in place (refresh grant, ID token verified against
   the original subject, userinfo merged, claims mapped again, both cookies
   written) and then serves the request.
3. **Idle and absolute limits.** The refresh cookie expires after
   `auth.idleTimeout` (168h) without a renewal, sliding on each one.
   `auth.maxLifetime` (0 = the provider decides) caps a login from its start.
4. **Without a refresh token, today's behaviour**: the session lasts
   `auth.sessionTTL` and is not renewed; the UI logs once per login why to
   look at `offline_access`.
5. **`offline_access` is a default scope**; startup warns when `oidc.scopes`
   lacks it. Providers needing extra parameters (Google's
   `access_type=offline`) are not covered.
6. **One renewal per refresh token**: single-flight per hash of the token in
   the process, the result kept for 2 minutes for requests still carrying the
   old token. An `invalid_grant` outside that window ends the login. With
   more than one replica a parallel renewal can still lose; the chart README
   says so.
7. **Provider down is not a logout.** Passing failures (network, 5xx,
   10s timeout) keep the session for another `renewAfter`, at most until the
   idle deadline, and are logged. Refusals (`invalid_grant`,
   `unauthorized_client`, subject or nonce mismatch, claim mapping failure)
   end the login.
8. **Roles follow the provider**: each renewal maps claims again; claims that
   map to no allowed login log the user out.
9. **Streams renew too.** Event and transcript streams end at the session's
   expiry; the browser reconnects and the reconnect renews. `live.ts` does
   not show *disconnected* for a reconnect that succeeds within 5 seconds.
10. **Logout revokes** the refresh token at the provider's
    `revocation_endpoint` (RFC 7009) when there is one, best effort with a 5s
    timeout, then clears both cookies.
11. **Automatic sign-in again.** On a 401 the web goes to
    `/oauth2/login?return=<current route>` by itself, once; a
    `sessionStorage` marker (60s) stops a loop. Top-level navigation, no
    iframe, no `prompt=none`.
12. **Nothing typed is lost**: unsent composer text is kept in
    `sessionStorage` per route before the automatic sign-in, restored after
    login, then dropped.
13. **`/api/v1/me` says how the login lasts**: `renewing` and `idleUntil`;
    the account menu shows *Signed in until <date> unless you are away
    longer* or *Signed in until <time>*.
14. **No change** to CSP, session key handling, the GitLab cookie,
    authorization rules, `auth.mode: none` or system prompts.

Decisions made while splitting and implementing:

15. **Session switch in R6.** The change to short sessions (`renewAfter`
    when a refresh cookie exists) arrives together with the renewal in
    `authed`, not with the callback (R4), so no release has sessions that
    expire without renewing.
16. **No failure caching.** Only successful renewals are kept for 2 minutes;
    a failed renewal is retried by the next request, so a passing outage
    ends as soon as the provider is back.
17. **`idleUntil` for fixed logins.** Logins without a refresh token report
    `renewing: false` and `idleUntil` as the session's own expiry, so the
    account menu can show *Signed in until <time>*.
18. **Quiet reconnect in the client.** `live.ts` reconnects at once after a
    stream ends at expiry and shows *Offline* only when no stream has been
    open for 5 seconds.

## Requirements and where they were implemented

| Requirement | Implemented in |
| --- | --- |
| R1 fake OIDC provider, login callback test | `internal/ui/oidcfake_test.go`, `oidc_test.go` |
| R2 `auth.renewAfter`, `idleTimeout`, `maxLifetime`, `offline_access` default, startup warning, chart values and README | `internal/ui/config.go`, `server.go`, `charts/ai-operator/values.yaml` |
| R3 refresh cookie type, deadlines | `internal/ui/session.go`, `session_test.go` |
| R4 callback keeps the refresh token | `internal/ui/oidc.go` |
| R5 renewer: grant, ID token check, userinfo, claim mapping, single-flight, 2-minute cache, passing vs refusing | `internal/ui/renew.go`, `renew_test.go` |
| R6 `authed` renews, streams end at expiry | `internal/ui/server.go`, `authed_renew_test.go` |
| R7 logout revokes | `internal/ui/oidc.go` |
| R8 `/api/v1/me` `renewing`, `idleUntil` | `internal/ui/api.go`, `web/src/generated/` |
| R9 automatic sign-in on 401 | `web/src/api.ts`, `web/src/live.ts` |
| R10 unsent composer text survives sign-in | `web/src/chatState.ts` and the composers |
| R11 quiet stream reconnect | `web/src/live.ts` |
| R12 account menu line | `web/src/format.ts` (`loginLasts`), account menu |
| R13 docs | `docs/ui.md` (Enable it, Security), `docs/design.md` (Desk session model), chart README, this file |

Issues: R1 to R12 were commits of the milestone's sub-issues ("Desk session
refresh R1" to "R12"); R13 is #386.

## Steering log

- R5 was split in two (renewer, then single-flight and cache); R13 is
  documentation only, no release after it.
