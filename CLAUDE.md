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
- The web UI's service account is defined by hand in the chart
  (`templates/ui-rbac.yaml`): never put kubebuilder RBAC markers into
  `internal/ui`, `make manifests` would grant them to the operator.
- Every merge to `main` that touches code, the chart or the Dockerfiles is
  released automatically with a calendar version (`YEAR.MONTH.N`), see
  `docs/releasing.md`. Docs-only changes are not released.
- Keep the docs in `docs/` and the chart README in sync with behavior changes.
