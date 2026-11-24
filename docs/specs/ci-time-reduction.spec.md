# CI time reduction — fewer jobs, fewer pipelines, cheaper web checks

Status: draft (2026-11-24) · companion: Bloop `docs/specs/ci-time-reduction.spec.md`

## 1. Problem statement

MR pipelines take 10–44 min (last 16 green pipelines, 2026-11-24). Job runtimes are modest;
the time goes to **queueing**: every job waits on average 1–11 min for a runner, and each of
the 12 jobs pays pod start, clone and toolchain pull on its own.

| Job (MR, n=13) | avg run | max run | avg queue |
|---|---|---|---|
| ui-e2e | 13m49 | 20m52 | 1m48 |
| test | 7m13 | 12m12 | 9m44 |
| deadcode | 3m31 | 7m15 | 8m34 |
| helm-lint | 2m48 | 11m29 | 2m51 |
| vulncheck | 2m46 | 3m49 | 7m45 |
| build | 2m35 | 4m36 | 10m44 |
| knip | 2m29 | 7m31 | 0m50 |
| vet | 2m27 | 4m53 | 7m04 |
| toolchain:image | 2m01 | 4m37 | 1m02 |
| drift | 1m24 | 2m54 | 5m46 |
| ui | 1m24 | 2m24 | 11m42 |
| gofmt | 0m28 | 0m50 | 6m34 |

Bloop shares the same `shared` / `multiarch` runners, so its load adds to this queue.

## 2. Goals

- MR pipeline wall time ≤ 15 min typical (from ~20–25 min).
- Jobs per MR pipeline: 12 → 4 (toolchain:image, check, test, web).
- No path runs a check it cannot affect (docs-only MR runs only `check`).
- Same failure coverage on `main` over a day as today.

## 3. Non-goals

- More runner capacity (infra/cluster) — separate decision.
- Changing the release flow (tag pipeline `release` job) beyond rules.
- Deleting Go tests or e2e specs (separate, judgement-based follow-up, §5.6).

## 4. Constraints

- **Operator main health** (`internal/controller/mainhealth.go`, `release_sweep.go`): release
  sweep needs the newest finished `main` pipeline green **at head SHA**; `baseFailing`
  compares failing MR job names against `main`'s failed jobs; flaky/auto-fix read main jobs.
  So `main` push must keep a pipeline, and job names that can fail on MRs should also run on
  `main` (at least nightly).
- `.check` rule (no checks on tags) stays.
- GitLab Free: no merge trains; `rules:changes` unreliable on tags (only release runs there).
- Dependency Proxy for images where possible; Playwright's `mcr.microsoft.com` is not proxied
  (existing note in `.gitlab-ci.yml`).

## 5. Proposed approach

### 5.1 Merge small Go jobs into one `check` (B)

`gofmt`, `vet`, `drift`, `deadcode`, `vulncheck`, `build`, `helm-lint` → one job `check`
running a new mise task `check` that runs each step, keeps going after a failure, and prints
a summary naming the failed steps (same pattern as Bloop `node:lint`). One Go cache.
Expected: ~7 queue waits → 1; run ≈ sum of steps with warm cache (~6–8 min, steps share one
module download and build cache).

### 5.2 One `web` job (B)

`ui` (build + ts-types), `knip` and `ui-e2e` → one `web` job: `npm ci` once, `make ui`
(ts-types + build), knip, Playwright. Install Chromium from a cache-stable source:
- preferred: bake Playwright Chromium + deps into the toolchain image via mise (one rebuild
  when the Playwright pin changes), or
- fallback: keep `node:22-bookworm` but cache `ms-playwright` and skip `--with-deps` apt
  install by baking the libs into a small image in `devops/ci`.
Measure the install share in the first MR and pick.

### 5.3 Path gates (C)

| Job | MR runs when changed |
|---|---|
| check | always (cheap, fmt/drift/helm) |
| test | `**/*.go`, `go.mod`, `go.sum`, `mise.toml`, `mise.lock`, `mise-tasks/**`, `hack/coverage-baseline.txt`, `.gitlab-ci.yml` |
| web | `web/**`, `internal/ui/**` (ts-types source), `go.mod`, `mise.*`, `.gitlab-ci.yml` |

`FORCE_CI_ALL=true` or `[ci-all]` in the commit message runs everything (Bloop convention).

### 5.4 Thin `main`, nightly full (A)

- `main` push: `toolchain:image` + `check` only. Same tree as the MR head that passed (ff
  merge), so `test`/`web` would re-run identical code. Pipeline stays → main health green at
  head SHA → release sweep unaffected.
- New pipeline schedule `nightly-full` on `main` (03:00 Europe/Berlin): every job, no path
  gates. A red nightly feeds main health (red issue, auto-fix, flaky hand-over) as today.
- `web`/`api` triggered pipelines on main: full (manual escape hatch).

### 5.5 Cheaper heavy checks on MRs (D)

- `test`: `-race` only on `main` nightly; MRs run `go test` without `-race` (2–5× faster
  on internal/controller). Coverage gate stays on MRs (no race needed for the profile).
  Implement via env `GO_TEST_RACE` read by `mise-tasks/test-coverage` (not mise.toml, to
  avoid toolchain rebuilds).
- `web` e2e: **two sets**, both kept.
  - **Journey set** (MRs): the critical user journeys, tagged `@journey` with Playwright's
    `test(…, { tag: '@journey' }, …)`; the MR job runs `playwright test --grep @journey`.
    Budget: ≤ 25 tests, ≤ 3 min run. Candidates (final pick in MR 2): Desk loads and signs
    in again after expiry (`sign in again`), overview cards, inbox → answer a prompt card
    (`needs-you`, `prompt-card`), composer sends a chat message, milestone page tabs and
    decisions, pilot drawer, search palette, axe check of the top-level routes. A test joins
    the set when its failure would block the owner's daily loop, not for coverage.
  - **Full set** (nightly, `web`/`api` runs, and MRs that change `web/e2e/**` or
    `web/playwright.config.ts` so a new or changed spec is proven before merge): every spec,
    every width.
  - Widths: the journey set runs at 1280 only (`E2E_WIDTHS` env read by the specs that loop
    widths: account-menu, cockpit, inbox-handled, and the a11y/overflow sweeps); the full set
    runs 375/768/1280. `known-a11y`/`known-overflow` keys stay unchanged.
  - `npm run e2e:journey` / `make ui-e2e-journey` for the same set locally.

### 5.6 Test pruning (D, follow-up MR)

From the junit/`go test -json` timings: list the 20 slowest Go tests and slowest e2e specs,
then apply `docs/testing.md` + GLOBAL Test Selection (drop duplicated layers, restated
constants, parity tests without exit). Output a table in this spec; delete only with owner OK.

## 6. Affected files / components

- `.gitlab-ci.yml` (jobs, rules, workflow)
- `mise.toml` / `mise-tasks/check` (new), `mise-tasks/test-coverage` (race toggle)
- `web/e2e/*.spec.ts` (width list from env), `web/playwright.config.*`
- GitLab pipeline schedule `nightly-full` (project setting, created via API after approval)
- `docs/contributing/*` CI section; `docs/specs/README.md` index entry
- `CLAUDE.md` ("CI runs it [-race] on every push" → nightly; job names in the pre-push list)

## 7. Risks / trade-offs

- **Race bugs surface a day late** (nightly, not MR). Mitigation: nightly red → main-health
  auto-fix issue, as today.
- **Non-journey and mobile-only e2e regressions surface nightly.** Same mitigation; an MR
  that touches the specs themselves runs the full set. The journey list needs care: if it
  grows past budget, MR time creeps back.
- **One `check` job hides which step failed** in the MR widget → summary line + per-step
  section headers in log.
- **Main health between nightlies only reflects `check`**: `baseFailing` cannot match a `test`
  failure on an MR against main until nightly ran. Effect: such MRs pause `ciRed` instead of
  self-healing. Low frequency (needs main broken without an MR change, i.e. flake/time).
- A thin green push pipeline after a red nightly sets main health green again: red episode
  ends early. Auto-fix issue is already filed at red time; acceptable.

## 8. Validation

- Before/after: same script as 2026-11-24 (last ~15 green pipelines, per-job avg run/queue,
  pipeline duration). Target in §2.
- Deliberate-failure checks on the MR branch: fmt error → `check` red naming gofmt; failing Go
  test → `test` red; docs-only change → only toolchain + check.
- One manual `nightly-full` run green before merging 5.4.

## 9. Delivery plan

CI config + task scripts (outside the 50-line production budget, still small MRs):

1. **MR 1 — jobs merged + path gates** (5.1, 5.3): `check` task, `.gitlab-ci.yml`.
2. **MR 2 — web job** (5.2, e2e journey/full sets and widths of 5.5).
3. **MR 3 — thin main + nightly** (5.4, race toggle of 5.5) + schedule created after merge.
4. **MR 4 — test pruning** (5.6), separate approval of the deletion table.

Order 1 → 2 → 3; 4 independent after 3.

## 10. MRs

| # | Title | MR | Status |
|---|---|---|---|
| 1 | | | |
| 2 | | | |
| 3 | | | |
| 4 | | | |

## 11. Steering log

- 2026-11-24: created from CI timing brainstorm; owner chose bundles A–D.
- 2026-11-24: owner steer: keep e2e, split into a journey set (MRs) and the full set
  (nightly + MRs touching the specs) instead of only cutting widths (§5.5).
