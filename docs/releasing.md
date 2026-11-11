# Releasing

Releases are built twice: by the `release` job in
[`.gitlab-ci.yml`](../.gitlab-ci.yml) to `registry.gitlab.com` (what the
cluster deploys) and by
[`.github/workflows/release.yml`](../.github/workflows/release.yml) to
`ghcr.io`.

**A release is a decision, not a side effect of a merge.** A merge to `main`
runs the checks and releases nothing. The cluster follows every GitLab
release, so a release is a deploy: the operator releases ai-operator like
any other project it manages, when the planner, the pilot, an owner or the
end of a milestone decides so, and says why
([usage.md](usage.md#releases)). GitHub releases once a day if code changed
(see [GitHub releases](#github-releases)).

## Where the code lives

**GitLab is the primary repository:** merge requests, review and the day-to-day
pipeline live at
[gitlab.com/jo.wiemers/ai-operator](https://gitlab.com/jo.wiemers/ai-operator)
(`.gitlab-ci.yml`). Open issues and MRs there.

**GitHub is a push mirror** of that GitLab project
([jonathanwiemers/ai-operator](https://github.com/jonathanwiemers/ai-operator)).
It does not take MRs. Once a day, GitHub Actions
([`release.yml`](../.github/workflows/release.yml)) builds and publishes the
images and the OCI chart to `ghcr.io`, creates the git tag and the GitHub
release, if `main` changed since its last release (see
[GitHub releases](#github-releases)).

GitHub's release tags and releases are its own (GitLab releases are tagged
independently, see [GitLab releases](#gitlab-releases)). The mirror must not
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

## Versioning

Calendar versions `YEAR.MONTH.N`:

* `N` starts at `1` every month and increases with each release of that
  month: `2026.10.1`, `2026.10.2`, `2026.10.3`, then `2026.11.1`.
* Months have no leading zero (`2026.1.1`, not `2026.01.1`), so every version
  is valid SemVer, which Helm requires.
* On GitLab the operator computes the next version from the project's tags
  when it creates a release. GitHub computes its own with
  [`hack/next-version.sh`](../hack/next-version.sh) (`make version` prints it).

## GitHub releases

`release.yml` no longer runs on push. It runs once a day (`schedule`,
03:17 UTC) and by hand (*Actions → Release → Run workflow*). Its first job
looks up GitHub's newest calendar-version tag (the tags
`hack/next-version.sh` counts) and diffs it against `main` over the paths
that count as code: `api`, `cmd`, `internal`, `charts`, `web`, `config/crd`,
`Dockerfile`, `Dockerfile.agent`, `go.mod` and `go.sum`. Without a change
there the run ends without a release, so docs-only days and days without
merges release nothing; a manual run follows the same rule. Without any
tag it releases. It needs no secret beyond the built-in `GITHUB_TOKEN`.

Runs never overlap (`concurrency`), so two cannot get the same version. A
release:

1. run the tests and `helm lint`
2. build and push multi-arch (amd64, arm64) images
   * `ghcr.io/jonathanwiemers/ai-operator:<version>` and `:latest`
   * `ghcr.io/jonathanwiemers/ai-operator-agent:<version>` and `:latest`
3. package the chart with `version` = `appVersion` = `<version>` and push it to
   `oci://ghcr.io/jonathanwiemers/charts/ai-operator`
4. create the git tag `<version>` and a GitHub release with the chart attached

The chart's default image tags are its `appVersion`, so a chart version always
deploys the operator and agent images built from the same commit.

The operator binary knows its release: the Dockerfile's `ARG VERSION`
(default `dev`) is linked in as `main.version`, which the operator logs at
start and compares with the tags of `--self-project` (a companion of the
operator's own project lands once the running release contains the merge,
see [usage.md](usage.md#landing)). GitHub passes it as a build argument; the
GitLab job writes it into its copy of the Dockerfile before the build. A
local `make docker-build` is `dev`, which never counts as a release.

## GitLab releases

A GitLab release is a tag `YEAR.MONTH.N` on `main` with a GitLab release.
Nothing in CI decides or tags: the release exists first, and the tag's
pipeline builds it.

* **The operator** creates it through the API (`POST /projects/:id/releases`,
  like for every project it manages, see [usage.md](usage.md#releases)) when
  a release decision is due: the planner's after an issue, the pilot's, an
  owner's `/agent release [reason]` on the tracking issue, a companion
  waiting for its home release, the sweep outside milestones, and the final
  release of every milestone. It tags `main`'s green head with the next
  version and writes the notes (why, the milestone, the issues and merge
  requests it contains) into the release. Then it follows the tag pipeline:
  success is released, a failed one is retried once.
* **A human** creates one in the GitLab UI (*Deploy → Releases → New
  release*, a new tag on `main`) or with the API:

  ```sh
  glab release create 2026.11.3 -R jo.wiemers/ai-operator --ref main --notes "Why this release"
  ```

  The operator sees the tag like its own (a pending decision whose commit it
  contains is *already released*). To release with a reason the operator
  records, comment `/agent release [reason]` on a milestone's tracking issue
  instead.

The `release` job in [`.gitlab-ci.yml`](../.gitlab-ci.yml) is the only job
in the pipeline of a tag `YEAR.MONTH.N`: the tagged commit is on `main`, and
its merge request and push pipelines ran the checks already, so the tag
pipeline does not run them again. Tag only commits of `main` whose pipeline
passed (the operator tags `main`'s green head). A push to `main` (a merge)
runs the checks only. The job runs in the resource group
`release`, so releases never build in parallel, and is not interruptible: a
newer pipeline cancels only interruptible jobs (`workflow:auto_cancel`), so
it never cancels a release, running or queued. It:

1. takes the version from the tag (`CI_COMMIT_TAG`)
2. builds and pushes the images (amd64 only; the cluster has no arm64 agents)
   * `registry.gitlab.com/jo.wiemers/ai-operator:<version>` and `:latest`
   * `registry.gitlab.com/jo.wiemers/ai-operator/agent:<version>` and `:latest`
3. packages the chart and pushes it to
   `oci://registry.gitlab.com/jo.wiemers/ai-operator/charts/ai-operator`
4. appends the install section to the tag's GitLab release, keeping its
   notes, or creates the release for a bare tag. The notes are untrusted
   text: the job only passes them on as JSON (`jq`), never evaluates them.

Everything uses `CI_JOB_TOKEN`; no project CI/CD variables are needed. The
operator's token needs the Developer role or more, enough to create tags on
`main` (Maintainer if tags are protected). The chart's
default image repositories stay on ghcr.io, so a GitLab install sets
`image.repository` and `agent.image.repository` (the release notes show how).
The cluster pulls with the GitLab deploy token in `image-pull-secret`.

GitLab and GitHub each count their own tags and release at different
times (GitLab when a release is decided, GitHub once a day), so the same
version can name different commits on the two registries. If the push mirror
carries a GitLab tag to GitHub that differs there, the mirror keeps GitHub's
(divergent ref) and marks that update as failed.

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
