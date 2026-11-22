# Lean codebase: no dead code, audited security, tests that check behaviour

Status: in progress. The milestone's spec, run by autopilot. It was created by
the first merge request so later merge requests have a steering log to write
to.

## Goal

1. CI catches dead Go code, unused web exports, files and dependencies, and Go dependencies with known vulnerabilities. Each of these checks is a blocking job on GitLab and GitHub, and all of them are green on `main`.
2. The codebase has no code, dependency, CRD field, Helm value, Desk API route, command or label that nothing reads. Duplicated helpers are merged into one. An operator upgrading reads on one page what was removed and what to delete from their manifests and values.
3. No production Go file has more than 1000 lines. Groups of code that have grown inside `internal/controller`, `internal/runner` and `internal/ui` sit in their own packages where they stand alone, and behaviour is unchanged.
4. Every `gosec` finding is either fixed or carries a `#nosec` with a reason. The four trust boundaries (webhook tokens, Desk auth and authorization, runner subprocess arguments, secrets in logs and prompts) have been reviewed by hand, and every fixed finding has a regression test.
5. Go test coverage is measured per package in CI, and no package falls below its baseline. Tests that only check their own values are gone, and the three least-covered packages have tests for their untested behaviour.

## Background

File refs are at origin/main 46f4b2c, 2026-11-20.

- **Size.** There are about 72k production lines of Go (generated code excluded) and about 90k lines of Go tests. The biggest packages are `internal/controller` (21.6k production lines), `internal/runner` (18.8k) and `internal/ui` (12.1k). The web UI has about 80 modules in `web/src` and 67 `node --test` files in `web/test/` (`web/package.json:10`).
- **Files over 1000 production lines.**

  | File | Lines |
  |---|---|
  | `internal/controller/autopilot.go` | 2938 |
  | `api/v1alpha1/agenttask_types.go` | 2520 |
  | `internal/runner/implement.go` | 1767 |
  | `api/v1alpha1/agentproject_types.go` | 1607 |
  | `internal/gitlab/gitlabfake/fake.go` | 1482 |
  | `internal/gitlab/client.go` | 1428 |
  | `internal/controller/agentproject_controller.go` | 1265 |
  | `internal/controller/pilot_actions.go` | 1222 |
  | `internal/runner/runner.go` | 1144 |
  | `internal/runner/agent.go` | 1092 |
  | `internal/runner/prompts.go` | 1035 |

- **CI today.** The GitLab jobs are `drift`, `gofmt`, `vet`, `test`, `build`, `ui`, `ui-e2e`, `helm-lint` and `release` (`.gitlab-ci.yml:77-187`). Their scripts are mise tasks (`mise.toml`, from `[tasks.drift]` on). `.github/workflows/ci.yml:40-54` runs the same checks (`.gitlab-ci.yml:1`). Nothing checks for dead code, vulnerable dependencies, insecure patterns or unused web exports, and nothing measures coverage. `go test -race ./...` runs in the `test` job with a Postgres service (`.gitlab-ci.yml:96-117`).
- **Tool pinning.** `controller-gen` is installed into `bin/` at a pinned version (`Makefile:3-15`). `tygo` is a `tool` line in `go.mod` (`go.mod:98`) and runs as `go tool tygo` (`Makefile:62-63`). `tygo.yaml` generates the web types from `api/v1alpha1` (`tygo.yaml:14`), `internal/ui` (`:79`), `internal/problem` (`:185`) and `internal/research/store` (`:194`). Web devDependencies are pinned exactly (`web/package.json:23-30`).
- **Webhook.** `internal/webhook/receiver.go:125` reads `X-Gitlab-Token`. `receiver.go:246-248` compares hashes with `subtle.ConstantTimeCompare`.
- **Desk auth.** The routes are registered in `internal/ui/server.go:139-202`. Each `/api/v1` route is wrapped in `authed` (`server.go:233`), and research routes are wrapped in `researchHandler`. Without OIDC (`server.go:112`), `authed` lets every request in as `anonymous` (`server.go:235-238`). Per-resource checks live in `internal/ui/authz.go:28-131` (`CanView`, `CanSteer`, `CanChat`, …), and each handler has to call them itself.
- **Subprocesses.** git runs from `internal/runner/git.go:169` and `internal/runner/workspace.go:36`, engines from `internal/runner/engine.go:716`, `session_claude.go:64` and `session_acp.go:91`. The git token is passed in `GIT_CONFIG_*` variables, not argv (`git.go:100-121`).
- **Upgrades.**
  - Upgrading is two lines in `docs/getting-started/install.md:126-127`: "run the same command with a new `--version`", and the CRDs are upgraded with the chart. That page has 387 lines, and user pages may have at most 400 (`go test ./internal/docscheck/`).
  - `kubectl apply` uses strict field validation by default, so a manifest that sets a removed CRD field fails to apply.
  - The chart has no `values.schema.json`, so a removed Helm value is ignored silently.
- **MR size.** `internal/difflines` counts added and deleted lines, and only a pure rename counts as zero (`difflines.go:61-85`). A deletion-only MR and a file split therefore both count toward the 300-line budget.
- **Runs before this one.** The open milestones %31, %32, %34, %36, %37, %38, %39 and %40 run first. Most of them change the Desk (`web/`, `internal/ui`), %36 and %37 heavily, and %36 enforces the raw-control check (#631). This milestone cleans up the code as it is after those merges. The planner reads `main` again when this milestone's turn comes, and the audits, the file list and the line counts are taken from that code, not from the refs above.

## Decisions (locked)

1. **One milestone, six phases** (D1). Tooling (R1–R3), then dead and unused code (R4–R6), then simplification and restructuring (R7–R8), then security (R9–R14), then tests (R15–R16), then docs (R17). Each phase builds on a tool from the phase before. Splitting into two milestones was rejected by the owner.
2. **Gates** (D2). `govulncheck`, Go dead code (`staticcheck -checks U1000` plus `deadcode`) and `knip` become blocking CI jobs. `govulncheck` blocks from its first MR. The dead-code and `knip` jobs start with `allow_failure: true` and become blocking in the MR that brings their backlog to zero. `gosec` runs once, as an audit: each finding is fixed or gets `// #nosec G<nnn> -- <reason>`. It stays a `make gosec` target and does not run in CI, because it flags the operator's intended git and engine subprocess calls. Report-only jobs and a blocking gosec gate were rejected.
3. **Manual security review of four boundaries** (D3). These are webhook token checks, Desk authentication and per-resource authorization, runner subprocess arguments, and secrets in logs, errors, transcripts and agent prompts. There is one audit requirement per boundary. Tools alone and a review of every package were rejected.
4. **Remove anything no code reads** (D4), including CRD fields, Helm values, Desk API routes, commands and labels. "No code reads it" is proven, not guessed. The item has no reference in Go code outside generated deepcopy, CRD and RBAC files, in chart templates, in `web/src`, or in prompt text. The MR description shows the search that proves it. Documentation alone does not count as a reader. Keeping user-facing items and only listing them was rejected by the owner.
5. **Value-only tests** (D5). A test is value-only if it does one of these:
   - asserts on a constant or fixture it set itself without production code in between
   - only checks that a fake or mock returns what it was told to return
   - repeats another table case through the same code path
   - tests generated code (deepcopy, tygo output)

   Such a test is deleted or merged. A Go test may be deleted only if per-package statement coverage stays the same, which the coverage gate (R3) checks. A web test may be deleted only if `node --test --experimental-test-coverage` shows no drop for the module it tests, with before and after quoted in the MR. There is no fixed coverage floor. A number would push agents to write filler tests.
6. **The runner test timeouts are out** (D6). #419, #420 and #421 stay open for their own milestone.
7. **Removed user-facing items are listed for upgraders on one page** (D7).
   - A new guide, `docs/guides/upgrade.md`, says how to upgrade. It has a "Removed in this release" list that names each removed CRD field, Helm value, command, label and Desk API route, with the release it leaves in and what to delete from manifests or values.
   - The upgrade paragraph in `docs/getting-started/install.md` links to it. The list lives in its own page and not in `install.md` because that page is 13 lines below the docscheck cap.
   - The spec's steering log keeps the same list.
   - Keeping removed CRD fields in the schema as ignored for one release was rejected: it is exactly the dead code this milestone removes.
8. **Tools are pinned like `tygo`** (D8). `govulncheck`, `staticcheck`, `deadcode` and `gosec` become `tool` lines in `go.mod` and run as `go tool <name>`. `knip` becomes an exact-pinned devDependency in `web/package.json`. Renovate updates them like other dependencies. Installs into `bin/` and tool lines in `mise.toml` (a change there rebuilds the CI toolchain image) were rejected.
9. **Tests count as callers** (D9). `deadcode` runs as `go tool deadcode -test ./...`, so code that tests reach is live. This includes test fakes such as `internal/gitlab/gitlabfake`. `staticcheck -checks U1000` covers unused fields, types and constants. The owner rejected treating code that only tests reach as dead.
10. **Simplify, unify and restructure** (D10). R7 merges duplicated helpers, removes wrappers that only forward a call and inlines single-use indirection. It keeps interfaces that exist so tests can use fakes. R8 splits big files and moves cohesive groups of code into their own packages (Decision 11). Both keep behaviour and existing test assertions unchanged. Only D4 removals and D5 test deletions may change what is tested. The owner rejected limiting this milestone to in-place refactors.
11. **Restructuring as small verbatim moves** (D11).
    - Every production Go file over 1000 lines (the table in Background, re-measured at plan time) is split into files under 1000 lines, along its existing sections, in the same package.
    - Code moves into a new package under `internal/` only out of `internal/controller`, `internal/runner` or `internal/ui`, and only for a group of files that needs nothing from the old package after the move. No import cycles, and no type aliases or forwarding functions left behind.
    - `api/v1alpha1` keeps its package path, and the view structs stay in `internal/ui`, so `tygo.yaml`, the CRDs and `web/src/generated/` do not change.
    - Each MR moves code verbatim (only package names, imports and identifier exports change, as `git diff --color-moved` shows). It stays under 300 moved production lines and leaves `make generate manifests ts-types` with an empty diff.
    - MRs on the same package run one after another.

    Letting pure-move MRs exceed the budget was rejected: it breaks the house rule.

## Requirements

### Tooling (CI)

**R1 – Go static checks.**
- Add `govulncheck`, `staticcheck`, `deadcode` and `gosec` as `go.mod` tools (Decision 8).
- Add Make targets `vulncheck` (`go tool govulncheck ./...`), `deadcode` (`go tool staticcheck -checks U1000 ./...` and `go tool deadcode -test ./...`, which fails when it prints anything, see Decision 9) and `gosec`.
- Add mise tasks `vulncheck` and `deadcode` and the matching `.go` jobs in `.gitlab-ci.yml`. `deadcode` gets `allow_failure: true` (Decision 2). Add the same steps to `.github/workflows/ci.yml`.
- `govulncheck` blocks from this MR. Every reachable vulnerability it reports is fixed here by a dependency bump. If one cannot be fixed, say so in the steering log and open a follow-up.

Tests: both jobs run on the MR pipeline, `vulncheck` green and `deadcode` with its findings in the job log.

**R2 – Web unused-code check.**
- Add `knip` (Decision 8) with `web/knip.json`. Its entry points are the Vite entry, `web/test/*.test.mjs` and `web/e2e/`. `web/src/generated/` is ignored for unused exports.
- Add `npm run knip` and run it in the `ui` CI job with `allow_failure: true`, and in the GitHub `ui` step.

Tests: the job log lists the findings, and `make ui` still passes.

**R3 – Coverage per package.**
- The `test` mise task writes `-coverprofile=coverage.out -covermode=atomic`.
- A small Go program under `hack/coverage/` reads the profile, computes statement coverage per package and compares it with the committed `hack/coverage-baseline.txt` (one `package percent` line each). It fails when a package is more than 0.2 points below its baseline. It prints a table of all packages and adds new packages to its output with their value.
- `make coverage-baseline` rewrites the baseline from a profile.
- The first baseline is measured on this MR's pipeline. A later MR may raise a package's baseline but never lower it. Exceptions: D4 deletes the package, or R8 moves code to a new package, in which case the MR re-measures the old and new packages.

Tests: a unit test of the comparison with small fixture profiles (drop, equal, rise, new package, removed package), and the `test` job prints the table.

### Dead and unused code

**R4 – Go dead code to zero.**
- Delete what `make deadcode` reports, split by package group: `internal/controller`, `internal/runner`, `internal/ui`, and all other packages plus `api/` and `cmd/`.
- The last MR of the group switches the `deadcode` job to blocking.

Tests: `make deadcode` is empty, the touched packages' tests pass, and the coverage gate is green.

**R5 – Web unused code and dependencies to zero.**
- Delete the unused files, exports and dependencies that `knip` reports, and run `go mod tidy` with an empty diff.
- The last MR switches the `knip` step to blocking.

Tests: `npm run knip` is empty, and `make ui` and `npm test` pass.

**R6 – Unused user-facing items (Decision 4).**
- Find the CRD fields in `api/v1alpha1` that no code reads, the Helm values in `charts/ai-operator/values.yaml` that no template reads, the Desk API routes in `internal/ui/server.go` that `web/src` does not call, and the commands and labels that no code handles.
- Remove them in one MR per kind. Each MR description has an evidence table (item, search, result).
- Run `make generate manifests` and `make ts-types`. Update `docs/reference/` and `charts/ai-operator/README.md`, and add each item to the "Removed in this release" list in `docs/guides/upgrade.md` (Decision 7).
- The first R6 MR creates `docs/guides/upgrade.md` and links it from `docs/getting-started/install.md`, `docs/guides/README.md` and `docs/README.md`.

Tests: drift is green, `make helm-lint` passes, `go test ./internal/docscheck/` passes, and the tests of the touched packages pass. Deleted tests follow Decision 5.

### Simplify and restructure

**R7 – Simplify and unify (Decision 10).**
- Review one package group per MR, using R4's groups plus `web/src`.
- Merge duplicated helpers into one place (for example `internal/textutil` for string helpers), remove forwarding wrappers and shorten functions that repeat a pattern available elsewhere.
- Each MR description lists what it merged and why. Larger findings become follow-up issues.

Tests: the existing tests pass unchanged and the coverage gate is green.

**R8 – Split big files and restructure packages (Decision 11).**
- Split every production Go file over 1000 lines into files under 1000 lines, in the same package, along its sections. Tests move with their code into the matching `_test.go` files.
- In `internal/controller`, `internal/runner` and `internal/ui`, move each self-contained group of files into its own package under `internal/`, when the group needs nothing from the old package after the move. Each new package gets a package doc comment saying what it owns.
- One MR moves under 300 production lines verbatim. MRs on the same package run one after another.
- When a move turns out to need a behaviour change or a cycle, the group stays where it is and a follow-up issue records why.

Tests: the existing tests pass unchanged, `make generate manifests ts-types` leaves no diff, the coverage gate is green (Decision 11 for re-measured packages), and `find api internal cmd -name '*.go' ! -name '*_test.go' ! -name 'zz_*' | xargs wc -l` shows no file over 1000 lines after the last MR.

### Security

**R9 – gosec audit (Decision 2).**
- Run `make gosec`. Fix each finding or add `#nosec` with a reason.
- Record the counts by rule (fixed, justified) in the steering log.

Tests: `make gosec` exits 0, and every fix has a test.

**R10 – Webhook tokens.**
- Review `internal/webhook/receiver.go`: the token lookup per project, the constant-time compare, what an empty or missing secret does, the request body size limit, and replays of a delivery ID.

Tests: one regression test per fixed finding that fails before the fix.

**R11 – Desk authentication and authorization.**
- Check every route in `internal/ui/server.go:139-202`:
  - it is wrapped in `authed` or `researchHandler`
  - its handler calls the `Authorizer` check (`internal/ui/authz.go`) that fits the resource named in its path
  - write routes reject cross-site requests
- Check that anonymous mode (no OIDC) only runs when the config says so.
- Add a table-driven test that walks all registered routes. It fails for any `/api/v1` route that answers 2xx to a principal without a grant for the project.

Tests: that route test, plus a regression test per fix.

**R12 – Runner subprocess arguments.**
- Review the git and engine calls in `internal/runner` (`git.go`, `workspace.go`, `engine.go`, `session_claude.go`, `session_acp.go`, or wherever R8 moved them). Look for branch names, refs, paths and model or engine flags from issues, labels or CRDs that could start with `-` or carry shell or option syntax. Add `--` separators or validation where a value comes from outside.

Tests: one test per fix with a malicious value (for example a branch named `--upload-pack=x`).

**R13 – Secrets in logs, errors, transcripts and prompts.**
- Trace the GitLab tokens, webhook secrets, the session key and the engine API keys. Check that none of them reaches a log line, an error string returned to the Desk, a stored transcript, the raw logs route (`/api/v1/projects/{namespace}/{name}/tasks/{task}/logs`) or an agent prompt.
- Redact at the source where one does.

Tests: a test per fix that sets a known secret and asserts it is absent from the output.

**R14 – Audit notes.** Each of R10–R13 ends with a short "Audit" entry in the spec's steering log: what was checked, what was fixed, and which follow-ups were opened. `docs/concepts/security.md` is updated where the behaviour a user relies on changed.

Tests: `go test ./internal/docscheck/` passes.

### Tests

**R15 – Remove value-only tests (Decision 5).**
- Work one package group per MR (R4's groups plus `web/test`). Delete or merge the value-only tests.
- Each MR description lists the deleted tests with the rule of Decision 5 they fall under.
- Weakening an assertion to keep a test is not allowed.

Tests: the coverage gate is green, and for web tests the before and after coverage is quoted.

**R16 – Close the biggest gaps.**
- Take the three packages with the lowest coverage in the baseline from R3, excluding `cmd/` and `internal/e2e`.
- Add tests for their untested exported behaviour (branches and error paths a caller can hit). Do not add tests of getters or constants.
- Raise their baselines.

Tests: the new tests pass, and the baseline rises for those three packages.

### Docs

**R17 – Docs and spec.**
- `CLAUDE.md` "Development": the checks to run before pushing gain `make deadcode` and `make vulncheck` (for Go changes) and `npm run knip` (for `web/` changes). Add the rule that a deleted test needs unchanged coverage.
- `CONTRIBUTING.md` gets the same checks.
- `docs/contributing/design.md` gets the new package layout from R8: one line per new package saying what it owns.
- `docs/guides/upgrade.md` (created in R6) is final. It follows the user-page rules: "you", it opens with what the page is for, has a complete example (a values file and an AgentProject manifest before and after removing a field), and ends with `Next` links. Defaults are not restated.
- Archive the spec as `docs/specs/lean-codebase.spec.md` with a steering log. It contains the removed-items list, the gosec counts, the audit entries and the moves R8 skipped. Add one line for it in `docs/specs/README.md`.

Tests: `go test ./internal/docscheck/` passes.

## Constraints

- Under 300 production lines per merge request. Tests, generated files and docs do not count. Deleted and moved production lines count, so the planner splits deletions and moves by package group and by chunk (Decision 11).
- When a requirement has to be narrowed, open a follow-up issue for the rest and say so in the steering log.
- Run `make generate manifests` after changing API types and `make ts-types` after changing view structs. The CI drift job fails on stale files.
- Behaviour stays the same, except for D4 removals and security fixes. Existing test assertions are not weakened, and only Decision 5 deletes tests.
- R8 MRs only move code. They do not rename exported identifiers beyond what a package move needs, and they do not change `api/v1alpha1`'s package path, the CRD group or version, or `tygo.yaml`.
- No new dependencies except the five tools in Decision 8.
- No kubebuilder RBAC markers in `internal/ui`, including in any package split out of it. The Desk's service account stays hand-written in `templates/ui-rbac.yaml`.
- Desk changes keep to Nuxt UI components and the `raw-control` check.
- User doc pages stay under 400 lines and name no source files (docscheck). The upgrade list goes into `docs/guides/upgrade.md`, not into `docs/getting-started/install.md`.
- Before pushing, run `gofmt`, `go vet ./...` and `make helm-lint`, and the tests of the changed packages with `go test ./internal/<pkg>/...`, narrowed with `-run` in `internal/runner`, without `-race`. Do not run `make test` locally. After Markdown changes, run `go test ./internal/docscheck/`. After `web/` changes, run `make ui` and `npm test`. From R1 and R2 on, also run `make deadcode`, `make vulncheck` and `npm run knip`.
- Do not change `mise.toml` `[tools]`, because a change there rebuilds the CI toolchain image. Adding tasks is fine.
- Research store migrations are not renamed, moved or deleted, and new ones follow the UTC timestamp naming.

## Non-goals

- Fixing the `internal/runner` CI timeouts (#419, #420, #421) or the flaky `helm-lint` and `release` jobs (#350, #58, #128).
- Changing behaviour while restructuring, renaming the CRD API group or version, or moving `api/v1alpha1`.
- A CI gate on file length. R8 brings the files under 1000 lines once.
- A blocking `gosec` job, `golangci-lint` or other linters beyond Decision 2.
- A fixed coverage target, or tests written only to raise the number.
- Pen-testing, cluster and infra hardening (NetworkPolicies, the agent image), and changes in companion repositories.
- Deprecation periods for removed user-facing items (Decision 7).

## Suggested order

R1, R2 → R3 → R4, R5 → R6 → R7 → R8 → R9 → R10, R11, R12, R13 → R14 → R15 → R16 → R17

## Acceptance

- On `main` after the milestone, a pipeline shows the `vulncheck`, `deadcode` and `knip` checks as blocking and green on GitLab and GitHub. An MR that adds a Go function no code or test calls, or an unused web export, fails its pipeline.
- An MR that deletes a test which was the only one covering a branch fails the coverage gate, and the gate's table names the package and its drop.
- `make deadcode` and `npm run knip` print nothing. Searching any removed CRD field, Helm value or Desk API route from the "Removed in this release" list finds no reader in the code.
- An operator upgrading follows the link from the install page to `docs/guides/upgrade.md` and reads which fields and values to delete. A manifest that still sets a removed field fails `kubectl apply`, and that failure is the one the page describes.
- No production Go file under `api/`, `internal/` or `cmd/` (generated files excluded) has more than 1000 lines. Every new package has a doc comment and a line in `docs/contributing/design.md`. `make generate manifests ts-types` on `main` gives no diff. The steering log lists any group R8 left in place, with its follow-up.
- `make gosec` exits 0, and every `#nosec` has a reason.
- A webhook delivery with a wrong or missing token is rejected. A Desk user without a grant for a project gets 403 or 404 on every `/api/v1` route of that project, which the route test proves. A branch named `--upload-pack=x` does not reach git as an option.
- With a known token set, no log line, Desk error, raw log or prompt contains it.
- The spec's steering log lists the gosec counts, the four audit entries, the removed items and the follow-ups. The three packages of R16 have higher baselines than at the start.

## Steering log

- R1 (#680): the tools are `tool` lines in `go.mod`: govulncheck `golang.org/x/vuln` v1.10.0, staticcheck `honnef.co/go/tools` v0.8.2, deadcode `golang.org/x/tools` v0.53.0 and gosec `github.com/securego/gosec/v2` v2.30.0, the latest releases on 2026-11-22. Adding them raised some shared modules by minimum version selection (among them `golang.org/x/text` v0.44.0, `golang.org/x/net` v0.63.0 and `github.com/go-logr/logr` v1.4.4).
- R1 (#680): govulncheck found no reachable vulnerability in a required module. It found 32 reachable vulnerabilities in the standard library of Go 1.26.0 (`crypto/tls`, `crypto/x509`, `encoding/asn1`, `encoding/xml`, `mime`, `net`, `net/http`, `net/textproto`, `net/url`, `os`), fixed in Go 1.26.1 to 1.26.11. `go.mod` gains `toolchain go1.26.11`, and the `.go` CI jobs set `GOTOOLCHAIN: auto`, so go downloads that release. The `go 1.26.0` directive and `mise.toml` stay as they are. After the change govulncheck reports no vulnerabilities. Nothing was left unfixed, and no follow-up issue was opened.
- R1 (#680): the mise tasks `vulncheck` and `deadcode` are file tasks in `mise-tasks/`, not `[tasks]` entries in `mise.toml`. The CI toolchain image bakes `mise.toml`, so adding tasks there also rebuilds it, and on the first pipeline that rebuild failed downloading helm from get.helm.sh. With `mise.toml` unchanged, the jobs use the cached image. Later MRs that add or change mise tasks (R2, R3) hit the same rebuild unless they also use file tasks.
- R1 (#680): the starting backlogs. `make deadcode` reports 2 findings: an unused field `mu` in `internal/runner/agent_session_test.go` (U1000) and the unreachable `ValidID` in `internal/rules/rules.go` (R4). `make gosec` reports 90 findings (R9): G115 23, G104 16, G301 11, G304 8, G101 6, G204 5, G703 4, G702 3, G306 3, G124 3, G602 2, G402 2, G404 1, G123 1, G118 1, G109 1.
- R3 (#682): the coverage gate is the mise file task `test-coverage`, which the GitLab `test` job runs. It runs `go test -race -coverprofile=coverage.out -covermode=atomic ./...`, then `go run ./hack/coverage` against `hack/coverage-baseline.txt`. `[tasks.test]` in `mise.toml` stays as it is, so the toolchain image is not rebuilt (see R1). The job keeps `coverage.out` as an artifact for a week, and `make coverage-baseline` turns it into the baseline file. Values are compared at one decimal place, and a package fails when it is more than 0.2 points below its baseline. New and removed packages are listed but never fail.
- R3 (#682), the baseline rule: a later MR may raise a package's baseline in `hack/coverage-baseline.txt`, but never lower it. There are two exceptions. When a D4 removal deletes a package, the MR drops its line. When R8 moves code to a new package, the MR re-measures the old and the new package from its own pipeline's `coverage.out`.
- R3 (#682), open: the first `hack/coverage-baseline.txt` is not committed in #682. The implementing agent has no access to the pipeline's `coverage.out` artifact, and the whole suite must not run locally. Until the file exists, the gate prints the table with every package as `new` and does not fail. A follow-up commits the file from a `test` job artifact (`make coverage-baseline`). It must land before R4 and R15, which rely on the gate.
