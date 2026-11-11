# Operator-created releases

## Problem

A release decision used to start a pipeline on `main` with
`AGENT_RELEASE=true`. The project's CI had to know that contract: compute the
version, tag, and create the GitLab release (ai-operator `release`, Bloop
`release:create`). A project without such a job never released, and
`tagsDefaultBranch` kept every project without a recent tag on its default
branch at `NotApplicable` (it blocked all ai-operator releases until the
fast-forward fix in this MR).

## Goals

- A release decision becomes a GitLab release (tag and notes) that the
  operator creates through the API. No project needs an operator-specific
  job, variable or pipeline source.
- Whatever the project's CI runs for a tag is what a release builds. The
  operator follows that tag pipeline.
- Humans release the same way, in the UI (*Deploy → Releases → New release*)
  or with `glab release create`. The operator records those as
  `AlreadyReleased`.
- Projects without tag pipelines (or without CI at all) still release: the tag
  is the release.

## Non-goals

- SemVer versions and bumps (follow-up). The operator computes CalVer
  `YEAR.MONTH.N` only.
- GitHub releases (`release.yml`, ghcr.io): unchanged.
- Deploy tracking beyond the tag pipeline.

## Constraints

- No new CRD fields: `PipelineID` holds the tag pipeline, `Attempts` counts
  its retries.
- The bot token needs Developer to create tags (Maintainer for protected
  tags); the bot is a group Maintainer.
- The guarded client (`gitlab.Guard`) refuses release and pipeline-retry
  writes outside the home project and its targets.

## Approach

1. **Waiting.** Gates in order: mode `Off` → `NotApplicable`; a release tag
   already contains the SHA → `AlreadyReleased`; another record is `Running`
   → wait; `minInterval`; the green head of `main` that contains the SHA (or
   the SHA itself when `main` has no pipelines). Then `CreateRelease(tag =
   nextRelease(tags, now), ref = head, description = release notes)`. A
   400/409/422 (the tag appeared meanwhile) waits: the next poll takes the
   next version. `tagsDefaultBranch` is no release gate any more; companion
   landing and the sweep keep it.
2. **Running.** The operator follows `ListPipelines{Ref: tag}`:
   success/skipped → `Released`; failed/canceled → `RetryPipeline` up to
   `maxReleaseAttempts` (2), then `Failed`; no tag pipeline after
   `tagPipelineWait` (10 min) → `Released` ("the tag is the release").
3. **Resume.** `/agent release` on a failed final release resumes it as
   `Running` with fresh attempts and retries its failed pipeline, never a new
   tag.
4. **Tags of unfinished releases.** A `Failed` record's tag does not count as
   containing. A companion's home is not live through a tag whose operator
   release still builds or failed (`buildingReleases`).
5. **ai-operator CI.** Tag pipelines run (`workflow: if: $CI_COMMIT_TAG`); the
   `release` job, the only job of a tag pipeline (the check jobs ran on
   `main` already), runs on `^20\d\d\.\d+\.\d+$` tags with
   `VERSION=$CI_COMMIT_TAG`, builds images and chart, and appends an
   "Install" section to the GitLab release (or creates the release when a
   human pushed only a tag).

## Affected files

- `internal/gitlab/client.go`, `guard.go`, `gitlabfake/`: `CreateRelease`,
  `RetryPipeline`, guard methods, fake tag pipelines.
- `internal/controller/release.go`, `release_sweep.go`, `release_notes.go`,
  `companions.go`: the state machine above.
- `.gitlab-ci.yml`, `docs/releasing.md`, `docs/usage.md`, `docs/design.md`,
  `CLAUDE.md`.

## Risks / trade-offs

- **Bootstrap.** The operator running before this MR still starts
  `AGENT_RELEASE` pipelines, which the new CI no longer honors. After the
  merge a human creates the first release (UI or `glab release create`); its
  tag pipeline builds the operator that creates tags itself.
- **Bloop** keeps its `release:create` job until its follow-up; it is no
  longer triggered (no `AGENT_RELEASE` pipeline), and its tag pipelines
  already build.
- A tag created by the operator whose pipeline fails twice stays as a tag.
  The record is `Failed`, the tag never counts as containing, and a resume
  retries its pipeline.
- GitLab and GitHub version counters stay independent (as before).

## Validation

- Unit tests with the GitLab fake: release created at the green head, no-CI
  path tags the SHA, 409 waits, tag pipeline success/failure/retry/timeout,
  failed tag not containing, resume retries the pipeline, companion waits for
  a building release, sweep releases via the API, guard refuses outside
  home/targets.
- `go vet ./...`, `go test ./internal/controller/... ./internal/gitlab/...`.
- After merge: the bootstrap release's tag pipeline runs `release` green and
  the GitLab release gets its Install section.

## Delivery

Single MR !346 (`fix/release-tags-fast-forward`), together with the
fast-forward tag fix it started with. It exceeds the 50-line budget; the
owner chose one MR to unblock releases now. Follow-ups: SemVer support, Bloop
drops `release:create`, retire `hack/next-version.sh` once GitHub no longer
needs it.
