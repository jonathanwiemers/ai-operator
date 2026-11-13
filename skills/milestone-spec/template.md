/agent spec  
/agent autopilot  
/agent owners @gitlab-user  
/agent owner-subjects desk-login  

# Area: the outcome a user notices

## Goal

1. …
2. …
3. …

## Background

File refs are at origin/main <short-sha>, <YYYY-MM-DD>. This milestone runs
after "<title>" (%N) by milestone order.

What exists:

- **<Thing>.** What it does today (`path/to/file.go:123`, `FuncName`).
- **<Limit>.** What is missing or wrong, with numbers if you have them.

## Decisions (locked)

1. **<Decision>.** What and why. Rejected: <alternative> (why not).
2. …

## Requirements

### <Area> (Go)

- **R1** What changes (Decision 1). Tests: …
- **R2** …

### Web

- **R3** … Light and dark, 375px. Tests: …

### Docs

- **R4** `docs/<page>.md` (…), and `docs/specs/<name>.spec.md` with a
  steering log.

## Constraints

- Under 50 production lines per MR; Go and web in separate MRs.
- With default settings the behaviour stays as before; existing tests pass
  unchanged.
- When a requirement has to be narrowed, open a follow-up issue and say so
  in the steering log; every R has an issue or a steering log line.
- No new dependencies.
- `make test`, `go vet ./...`, `gofmt`, `make helm-lint`, `make ui`,
  `make ts-types`; CI and the drift job green.

## Non-goals

- …

## Suggested order

R1 → R2 → R3 → R4.

## Acceptance

- On …, … happens.
- …
