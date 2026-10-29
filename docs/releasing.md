# Releasing

Releases are fully automated by [`.github/workflows/release.yml`](../.github/workflows/release.yml).

## Where the code lives

**GitLab is the primary repository:** merge requests, review and the day-to-day
pipeline live at
[gitlab.com/jo.wiemers/ai-operator](https://gitlab.com/jo.wiemers/ai-operator)
(`.gitlab-ci.yml`). Open issues and MRs there.

**GitHub is a push mirror** of that GitLab project
([jonathanwiemers/ai-operator](https://github.com/jonathanwiemers/ai-operator)).
It does not take MRs. On every push to `main` that changes the operator, the
agent image, the CRDs or the chart, GitHub Actions
([`release.yml`](../.github/workflows/release.yml)) still builds and publishes
the images and the OCI chart to `ghcr.io`, creates the git tag and the GitHub
release. Flux keeps pulling from ghcr; that path is unchanged.

Release tags and GitHub releases exist **only on GitHub**. The mirror must not
overwrite them.

The owner sets the mirror up by hand: in GitLab, *Settings → Repository →
Mirroring repositories*, push direction, with a fine-grained GitHub token for
this repository only with **Contents** and **Workflows** read and write
(without Workflows, GitHub refuses every push that changes
`.github/workflows/`). Tick both:

* **Mirror only protected branches**, so only `main` is pushed;
* **Keep divergent refs**, so the mirror never deletes or rewrites refs that
  exist only on GitHub.

Without them the mirror prunes the release tags on its first sync: GitHub
turns those releases into drafts, and the next release reuses their version
and overwrites the images and the chart on ghcr. To repair, push each tag
again to the commit its Release run built
(`git push github <sha>:refs/tags/<version>`) and publish the release again
(`gh release edit <version> --draft=false`, `--latest` for the newest).

Docs-only merges to `main` are still not released.

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
  image pull secret (`imagePullSecrets`, a classic PAT with `read:packages`;
  the Secret must exist in the operator's namespace and in every agent
  namespace) and `helm registry login ghcr.io`.
* The workflow uses the built-in `GITHUB_TOKEN` (permissions `contents: write`,
  `packages: write`); no extra secrets are needed.

## Local builds

```sh
make docker-build IMG=… AGENT_IMG=…        # images
make helm-package VERSION=2026.10.1        # dist/ai-operator-2026.10.1.tgz
```
