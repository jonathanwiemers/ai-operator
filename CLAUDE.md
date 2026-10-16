# CLAUDE.md

## Development

- Go 1.26. After changing types or kubebuilder markers in `api/` or
  `internal/controller/`, run `make generate manifests`. CI fails if the
  generated files (deepcopy, CRDs, RBAC, the chart's `files/`) are out of date.
- Before pushing: `gofmt`, `go vet ./...`, `make test`, `make helm-lint`.
- Every merge to `main` that touches code, the chart or the Dockerfiles is
  released automatically with a calendar version (`YEAR.MONTH.N`), see
  `docs/releasing.md`. Docs-only changes are not released.
- Keep the docs in `docs/` and the chart README in sync with behavior changes.
