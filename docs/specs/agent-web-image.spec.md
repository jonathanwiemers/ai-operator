# Agents run the Desk's Playwright checks in their own pods

Status: archived. The milestone's spec, run by autopilot. The docs requirement
(R3, issue #895) wrote this file. Where this spec and the docs disagree, the
docs win: see [web-ui.md](../contributing/web-ui.md#checks),
[releasing.md](../contributing/releasing.md) and
[agent-image.md](../reference/agent-image.md).

## Goal

1. An implementer working on ai-operator runs `make ui-e2e-journey` (and `make ui-e2e`) in its agent pod, and Chromium starts. The checks pass or fail on their merits, not with missing shared libraries or missing fonts.
2. An implementer runs `make docs-screenshots` in its pod: Chromium renders the shots and `cwebp` turns them into WebP files.
3. The browser in the image always matches the Playwright version the web UI pins. A merge request that makes them differ fails CI, and Renovate bumps both in one merge request.
4. A broken browser image never blocks a release.
5. ai-operator's own agents run on the new image without a hand edit in the cluster: the switch is a companion merge request in `jo.wiemers/infra/cluster`.

## Background

Agent pods run as uid 1000 with all capabilities dropped, on `node:22-bookworm-slim` with only a few packages. Chromium's system libraries and fonts were missing, and `npx playwright install-deps chromium` needs root. Earlier milestones narrowed their work for this (see the steering logs of [desk-cockpit](desk-cockpit.spec.md) and [desk-header-and-tabs](desk-header-and-tabs.spec.md)). The CI `web` job on `node:22-bookworm` already proves `install --with-deps chromium`.

## Decisions

1. A project-specific image kept in this repo: `Dockerfile.agent-web` builds `agent-web:<version>` and `:latest` `FROM` the agent image of the same release. The base `Dockerfile.agent` stays unchanged.
2. Chromium is baked in at the pinned Playwright version, with `PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright` owned by uid 1000, so `npx playwright install chromium` downloads nothing.
3. `webp` (`cwebp`) is in the image.
4. No merge-request job builds or tests the image; it is checked by hand after its first release.
5. No change to memory, cache or `workers: 2`.
6. The image is built in its own release-stage job, `release-agent-web`: after `release`, GitLab only, `allow_failure: true`, `interruptible: false`.
7. The switch is a companion change in `jo.wiemers/infra/cluster`, landing once a release contains the job and has built the image.
8. The companion names `agent-web:latest` with `imagePullPolicy: Always`. The runner in new pods can be a release ahead of the operator, or one behind after a failed `release-agent-web`, until it is retried.

## Requirements

- **R1** `Dockerfile.agent-web`, a pin test in `web/test` that fails unless `PLAYWRIGHT_VERSION` equals `@playwright/test`, a path-gate entry, and a Renovate custom manager.
- **R2** The `release-agent-web` job, and the companion change in `jo.wiemers/infra/cluster`.
- **R3** Docs: Checks in web-ui.md, releasing.md, the agent image reference, this spec and its index line.

## Non-goals

- Chromium in the base agent image for every project.
- Building `agent-web` on GitHub or ghcr.io.
- Retaking the docs screenshots (#677, #678).
- Switching other projects to `agent-web`.
- Tying the agent image's tag to the operator's release.
- A merge-request job that builds the image.

## Acceptance

1. A merge request that changes `@playwright/test` without `Dockerfile.agent-web` fails `npm test` and names both versions.
2. On the next release tag, `release` publishes as before and `release-agent-web` pushes `agent-web:<version>` and `:latest`; if it fails, the release keeps its install section and other images.
3. The companion merge request stays open until the cluster's operator runs that release; then ai-operator's `AgentProject` names `agent-web:latest` with `imagePullPolicy: Always`.
4. An implementer runs `make ui-e2e-journey` in its pod and Chromium starts, with nothing downloaded.
5. `make docs-screenshots` in the same kind of pod writes WebP files to `docs/assets/screenshots`.

## Steering log

One line per change of plan or narrowed requirement: `- YYYY-MM-DD, Rn (#issue): what changed and why; follow-up #n`.

- 2026-11-27, R1, R2: merged as one merge request, with the companion in `jo.wiemers/infra/cluster` as planned.
- 2026-11-27, R1, R2: checked were the pin test, the pipeline lint and the Renovate config validation. Agents could not build the image or start Chromium in a pod. That is verified by hand after the first release (Acceptance 2-5).
- 2026-11-27, R3 (#895): #677 and #678 (retaking the docs screenshots) stay loose issues; this image makes them possible.
