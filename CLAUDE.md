# CLAUDE.md

## Development

- Merge requests are opened on GitLab
  (`https://gitlab.com/jo.wiemers/ai-operator`). The GitLab pipeline
  (`.gitlab-ci.yml`) must be green before merge. GitHub mirrors `main`.
  Both GitLab (`release` job, registry.gitlab.com, what the cluster runs) and
  GitHub (`release.yml`, ghcr.io) build releases; see `docs/contributing/releasing.md`.
- Go 1.26. After changing types or kubebuilder markers in `api/` or
  `internal/controller/`, run `make generate manifests`. CI fails if the
  generated files (deepcopy, CRDs, RBAC, the chart's `files/`) are out of date.
- Before pushing: `gofmt`, `go vet ./...`, `make helm-lint`, and the tests of
  the packages you changed only: `go test ./internal/<pkg>/...`, narrowed with
  `-run` in big packages such as `internal/runner`, without `-race`. Do not
  run `make test` (the whole suite with `-race`) locally: on the shared agent
  node it takes up to an hour. CI runs the suite on merge requests that
  change Go (without `-race`) and with `-race` in the nightly pipeline. After changing
  Markdown or a `docs/….md` ref, also run `go test ./internal/docscheck/`
  (links, anchors, page caps); a change to docs only needs no other tests.
  After changing Go code, also run `make deadcode` and `make vulncheck`;
  both must print no finding, as the CI `check` job blocks on them
  (`mise run check` runs all its steps locally). After changing the web
  UI (`web/`): `make ui` (type-check and build), `cd web && npm run knip`
  (unused files, exports and dependencies; blocking in CI) and `make
  ui-e2e-journey` (the browser checks merge requests run; a new check of
  a daily journey gets `journeyAt` from `web/e2e/journey.ts`).
- CI measures Go statement coverage per package against
  `hack/coverage-baseline.txt` and fails when a package drops more than 0.2
  points. A test may be deleted only if its package's coverage stays the
  same. A baseline line may only rise (`make coverage-baseline` from the
  `test` job's `coverage.out`), except for a package that is deleted, or
  code that moves to a new package: then re-measure the old and the new one.
- `make gosec` is a one-off audit target, not a CI job: it flags the
  operator's intended git and engine subprocess calls. Fix what it reports,
  or justify it with `// #nosec G<nnn> -- <reason>`: every `#nosec` names a
  rule and a reason.
- New research-store migrations (`internal/research/store/migrations/`) are
  named by the UTC time they were created,
  `$(date -u +%Y%m%d%H%M%S)_name.sql`, never by the next number: parallel
  branches would pick the same number. `0001`–`0009` keep their names.
- The web UI's view types (`web/src/generated/`) are generated from Go by
  tygo (`tygo.yaml`). After changing view structs in `internal/ui` or the CRD
  types the views embed, run `make ts-types` (`make ui` does it when Go is
  installed); the drift step of the CI `check` job fails on stale files.
  Do not mirror Go views in `web/src/types.ts`, it keeps only web-only types.
- The web UI's service account is defined by hand in the chart
  (`templates/ui-rbac.yaml`): never put kubebuilder RBAC markers into
  `internal/ui`, `make manifests` would grant them to the operator.
- A merge to `main` does not release. A release is a decision: the operator
  creates a tag `YEAR.MONTH.N` and GitLab release through the API (planner,
  pilot, owner's `/agent release`, a milestone's final release, the sweep), or
  a human creates one in the UI or with `glab release create`. The tag's
  pipeline builds it (`release` job). The push mirror carries the tag to
  GitHub, where `release.yml` builds the same version for ghcr.io. See
  `docs/contributing/releasing.md`.
- Docs layout (map in `docs/README.md`): `getting-started/` install and first
  milestone; `guides/` one task per page; `reference/` commands, fields,
  labels, Desk API; `concepts/` how it works, roles, security, glossary;
  `contributing/` design and internals; `specs/` archived specs with an
  index. Chart values stay in `charts/ai-operator/README.md`.
- User pages (`getting-started/`, `guides/`, `reference/`, `concepts/`):
  "you", active voice, short sentences; open with what the page is for and
  end with `Next` links; link terms to `docs/concepts/glossary.md`;
  examples before tables; a complete example on every configuration page;
  Desk first. Stay under 400 lines and name no source files (CRD fields,
  commands, labels, Helm values and URLs are fine). Internals go in
  `contributing/`. Defaults are stated once, in `reference/`. Enforced by
  `go test ./internal/docscheck/`.
- When you add a page, update that folder's README and `docs/README.md`.
  List a new spec in `docs/specs/README.md`. Keep the chart README in sync
  when Helm values change.
- Desk UI is built from Nuxt UI components: use one whenever it fits, even
  with small compromises in look. A custom component is allowed only when
  nothing in Nuxt UI fits; it is listed with its reason in the
  [UI kit](docs/contributing/web-ui.md#ui-kit) section, and `npm test`
  (`raw-control`) fails on raw controls elsewhere; and keep the axe check
  green (`docs/contributing/accessibility.md`).
- Write and review milestone specs with the `milestone-spec` skill
  (`skills/milestone-spec/`).
