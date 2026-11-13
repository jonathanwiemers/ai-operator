# GitLab release pipeline

## Problem

Releases are built only by GitHub Actions (`.github/workflows/release.yml`) and
published to ghcr.io. The cluster (`example/infra`) therefore pulls
the chart and both images from a private GitHub registry, through a ghcr.io PAT
in `image-pull-secret`. GitLab is the primary repository, and every other
homelab app already pulls from `registry.gitlab.com` with the GitLab deploy
token that the same Secret carries.

## Goals

- Every merge to `main` that `release.yml` releases is also released by GitLab
  CI, with the same steps: test, version, images, chart, tag, release.
- Images `registry.gitlab.com/jo.wiemers/ai-operator:<version>` and
  `registry.gitlab.com/jo.wiemers/ai-operator/agent:<version>`, chart
  `oci://registry.gitlab.com/jo.wiemers/ai-operator/charts/ai-operator`.
- example/infra can switch the `OCIRepository` and the image repositories to
  GitLab without any new credential.

## Non-goals

- Retiring `release.yml` or ghcr.io (later, once the GitLab path has run for a
  while).
- Keeping the GitLab and GitHub version counters in lockstep (accepted by the
  owner for now, see Risks).
- arm64 images on GitLab: the cluster runs the operator and agents on amd64
  (my-cluster).

## Constraints

- Versions stay `YEAR.MONTH.N` from `hack/next-version.sh` (months without
  leading zero). The shared `jo-release-create` (devops/ci) only accepts
  two-digit months, so it is not used.
- Shared runners (`tags: [shared]`), DinD through the Dependency Proxy, image
  builds via the shared `.docker-build` template (`jo.wiemers/devops/ci`).
- Two releases must never get the same version.

## Approach

One `release` job on `main`, the GitLab counterpart of the single job in
`release.yml`:

- **Trigger**: push to `main` with `rules:changes` on the same paths as
  `release.yml`, or a manual pipeline from the web UI (counterpart of
  `workflow_dispatch`). Runs after the whole `check` stage, so tests,
  `helm lint` and the drift check gate the release.
- **Serialisation**: `resource_group: release` and `interruptible: false`, so
  releases run one at a time. `workflow:auto_cancel:on_new_commit:
  interruptible` makes a newer pipeline cancel only interruptible jobs, so it
  never cancels a release, running or still waiting for the resource group.
- **Steps**: compute the version from the git tags (full clone, tags fetched),
  build and push both images with `jo-docker-build` (`linux/amd64`, tags
  `<version>` and `latest`, registry build cache), package the chart with
  `make helm-package` and `helm push` it, then create the GitLab release, which
  also creates the tag at `CI_COMMIT_SHA`. All with `CI_JOB_TOKEN`; no new
  CI/CD variables.
- **Tag history**: GitLab has the release tags only up to `2026.10.51`; the
  later ones exist only on GitHub. They are pushed to GitLab once, before the
  first release, so the next GitLab release continues the sequence.
- Tag pipelines stay excluded by `workflow:rules`, so creating the tag starts
  no pipeline.

## Affected files

- `.gitlab-ci.yml`: `release` stage and job, web pipelines, header comment.
- `docs/releasing.md`, `CLAUDE.md`: two release paths.
- example/infra (separate MR): `applications/ai-operator/helm/ocirepository.yaml`
  and `values.yaml` switch to `registry.gitlab.com`.

## Risks / trade-offs

- **Counter drift**: GitHub and GitLab each compute the next version from their
  own tags. If one side fails or skips a release, the same version can point to
  different commits on the two registries. If the push mirror also carries
  GitLab's tags to GitHub, a differing tag is kept as a divergent ref (mirror
  update marked failed). Accepted for now; the cluster only reads GitLab.
- The release (and tag) is created last, as on GitHub: a failed build leaves
  pushed images without a release, and the retry gets the same version and
  overwrites them, which is intended.
- Docker Hub base images (`golang`, `node`) are pulled by BuildKit inside DinD,
  not through the Dependency Proxy; a rate limit fails the job (retry).

## Validation

- `glab ci lint` on the new `.gitlab-ci.yml`.
- MR pipeline: no `release` job on the MR.
- After merge: the `release` job publishes both images and the chart,
  creates tag + GitLab release `2026.10.N`; `helm show chart
  oci://registry.gitlab.com/jo.wiemers/ai-operator/charts/ai-operator --version
  <v>` works with the deploy token.
- example/infra: `OCIRepository` Ready on the GitLab chart, HelmRelease
  upgraded, operator and agent pods pull from `registry.gitlab.com`.

## Delivery

1. ai-operator MR: GitLab release job + docs (CI config and docs only).
2. One-off: push tags `2026.10.52`–`.62` to GitLab (before merging 1). Done 2026-10-30.
3. example/infra MR: chart + images from GitLab, after the first GitLab
   release exists.

| MR | Status |
|----|--------|
| ai-operator!54: GitLab release pipeline | merged |
| ai-operator!55: allow API-triggered pipelines | merged |
| ai-operator!56: remove the docker context between the two builds | merged |
| example/infra!285: ai-operator from registry.gitlab.com | merged |
| ai-operator!63: skip a release whose commit is already released | merged |
| ai-operator!65: never auto-cancel a queued release job | open |

### Steers

- 2026-10-30: release jobs ran out of order and released older code under a
  higher version; the job now exits when its commit is already in a release
  tag (!63).
- 2026-10-30: with the default `conservative` auto-cancel, a newer pipeline
  cancelled a release still `waiting_for_resource` despite
  `interruptible: false`; the next merge was CI-only, so the change stayed
  unreleased until the job was retried. Auto-cancel is now `interruptible` (!65).
