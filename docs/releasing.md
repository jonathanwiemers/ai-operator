# Releasing

Releases are fully automated by [`.github/workflows/release.yml`](../.github/workflows/release.yml).

## Versioning

Calendar versions `YEAR.MONTH.N`:

* `N` starts at `1` every month and increases with each release of that
  month: `2026.10.1`, `2026.10.2`, `2026.10.3`, then `2026.11.1`.
* Months have no leading zero (`2026.1.1`, not `2026.01.1`), so every version
  is valid SemVer, which Helm requires.
* The next version is computed from the existing git tags by
  [`hack/next-version.sh`](../hack/next-version.sh) (`make version` prints it).

## What a release does

A release runs on every push to `main` that changes the operator, the agent
image, the CRDs or the chart (or manually via *Run workflow*). Releases never
run in parallel, so two merges cannot get the same version.

1. run the tests and `helm lint`
2. build and push multi-arch (amd64, arm64) images
   * `ghcr.io/jonathanwiemers/ai-operator:<version>` and `:latest`
   * `ghcr.io/jonathanwiemers/ai-operator-agent:<version>` and `:latest`
3. package the chart with `version` = `appVersion` = `<version>` and push it to
   `oci://ghcr.io/jonathanwiemers/charts/ai-operator`
4. create the git tag `<version>` and a GitHub release with the chart attached

The chart's default image tags are its `appVersion`, so a chart version always
deploys the operator and agent images built from the same commit.

## First release checklist

* GitHub Packages created by Actions are **private** by default. Either make
  the `ai-operator`, `ai-operator-agent` and `charts/ai-operator` packages
  public (*Package settings → Change visibility*), or give the cluster an
  image pull secret (`imagePullSecrets`) and `helm registry login ghcr.io`.
* The workflow uses the built-in `GITHUB_TOKEN` (permissions `contents: write`,
  `packages: write`); no extra secrets are needed.

## Local builds

```sh
make docker-build IMG=… AGENT_IMG=…        # images
make helm-package VERSION=2026.10.1        # dist/ai-operator-2026.10.1.tgz
```
