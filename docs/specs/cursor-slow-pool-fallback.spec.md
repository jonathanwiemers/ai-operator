# Cursor's slow pool routes cursor roles to their fallback

## Problem

Past the Cursor plan's usage, Cursor serves the account from its slow pool:
every request waits seconds (5 s on 2026-11-17) and only `auto` is allowed.
cursor-agent raises no error, so [the engine fallback](engine-usage-and-fallback.spec.md)
never fires. Reviews on cursor took 7.5 instead of 4 minutes until the
monthly reset, almost four weeks away.

## Goals / non-goals

- Goal: a cursor role with a fallback runs on the fallback while the
  account is in the slow pool, and the run report says so.
- Non-goal: usage numbers for cursor in the status or the Desk, a new spec
  field, or routing roles without a fallback.

## Constraints

- cursor-agent has no usage command. Cursor's dashboard API
  (`api2.cursor.sh`, Connect RPC with JSON) is undocumented and may change,
  so every failure counts as "not slow".
- Only the agent container gets the credentials secret today.

## Approach

1. `runner.CursorUsage` exchanges `CURSOR_API_KEY` for an access token
   (`/auth/exchange_user_api_key`), asks
   `DashboardService/GetUsageLimitPolicyStatus` for `isInSlowPool`, and when
   slow asks `GetCurrentPeriodUsage` for `billingCycleEnd`. Answers are kept
   10 minutes; parallel reviewers wait for one answer.
2. `nearLimit` returns a soft `UsageLimitError` for a cursor role with a
   fallback while the account is slow, independent of
   `agents.fallbackAtPercent`. The existing soft path runs the fallback,
   notes it, records nothing and never defers; if the fallback is limited
   too, the role runs on cursor.
3. The runner container gets `CURSOR_API_KEY` from the credentials secret
   (`optional`).

## Affected files

`internal/runner/cursorusage.go` (new), `internal/runner/usage.go`,
`internal/runner/runner.go`, `cmd/runner/main.go`,
`internal/controller/resources.go`, docs.

## Risks / trade-offs

- Cursor changes or blocks the API: no fallback, as before.
- The fallback spends claude usage. The deployment chooses this by giving
  cursor roles a fallback; none is configured without one.
- The runner container now holds the Cursor key next to the GitLab token.

## Validation

Unit tests against an `httptest` Cursor API: slow pool gives a soft limit
with the cycle end, the answer is cached and asked again after 10 minutes,
no requests for roles that cannot route around, no limit outside the slow
pool or on a rejected key. A runner test: both reviewers run on the
fallback and the report says why. Controller tests pass with the new env.

## Delivery

One MR (asked for by the maintainer), about 90 production lines. The
deployment then sets `fallback` on the cursor roles (`review`, profile
`fast`) in its AgentProjects.
