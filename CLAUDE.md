# CLAUDE.md

## Development

- Merge requests are opened on GitLab
  (`https://gitlab.com/jo.wiemers/ai-operator`). The GitLab pipeline
  (`.gitlab-ci.yml`) must be green before merge. GitHub mirrors `main`.
  Both GitLab (`release` job, registry.gitlab.com, what the cluster runs) and
  GitHub (`release.yml`, ghcr.io) build releases; see `docs/releasing.md`.
- Go 1.26. After changing types or kubebuilder markers in `api/` or
  `internal/controller/`, run `make generate manifests`. CI fails if the
  generated files (deepcopy, CRDs, RBAC, the chart's `files/`) are out of date.
- Before pushing: `gofmt`, `go vet ./...`, `make test`, `make helm-lint`.
  After changing the web UI (`web/`): `make ui` (type-check and build).
- New research-store migrations (`internal/research/store/migrations/`) are
  named by the UTC time they were created,
  `$(date -u +%Y%m%d%H%M%S)_name.sql`, never by the next number: parallel
  branches would pick the same number. `0001`–`0009` keep their names.
- The web UI's view types (`web/src/generated/`) are generated from Go by
  tygo (`tygo.yaml`). After changing view structs in `internal/ui` or the CRD
  types the views embed, run `make ts-types` (`make ui` does it when Go is
  installed); the CI `drift` job fails on stale files. Do not mirror Go views
  in `web/src/types.ts`, it keeps only web-only types.
- The web UI's service account is defined by hand in the chart
  (`templates/ui-rbac.yaml`): never put kubebuilder RBAC markers into
  `internal/ui`, `make manifests` would grant them to the operator.
- Every merge to `main` that touches code, the chart or the Dockerfiles is
  released automatically with a calendar version (`YEAR.MONTH.N`), see
  `docs/releasing.md`. Docs-only changes are not released.
- Keep the docs in `docs/` and the chart README in sync with behavior changes.
