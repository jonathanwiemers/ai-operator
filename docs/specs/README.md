# Specs

Every non-trivial change starts as a spec: the problem, the goals and
non-goals, the decisions the maintainer approved, and the plan of merge
requests. A spec that ran as an autopilot milestone keeps a steering log of
what changed on the way. The user-facing result is in the
[docs](../README.md); read a spec to learn why it works that way.

New specs go here as `<title>.spec.md`. Each group below is in the order the
specs were written, so later ones build on earlier ones.

## Workflow in GitLab

1. [Choosing engine, model and effort per issue](issue-model-and-effort.spec.md)
2. [Owner pings and agent-written MR descriptions](owner-pings-and-mr-descriptions.spec.md)
3. [GitLab home, webhooks, hardening and the pilot's next steps](gitlab-home-and-hardening.spec.md)
4. [Engine usage readings and engine fallback](engine-usage-and-fallback.spec.md)
5. [No lost work: checkpoints, smaller steps and conventional commits](checkpoints-and-conventional-commits.spec.md)
6. [Read gate and writer subagent](read-gate-and-writer.spec.md)
7. [Cross-repo changes: companion repositories](cross-repo-changes.spec.md)

## Autopilot and the pilot

1. [Pipeline health, infra retries and the blocked state](pipeline-health-and-blocked-state.spec.md)
2. [Milestone coordinator (`/pilot`)](milestone-coordinator.spec.md)
3. [Autopilot closes its milestone](autopilot-close-milestone.spec.md)
4. [Autopilot finds its own way out of a pause](autopilot-way-out-of-a-pause.spec.md)
5. [Pauses say why, spend adds up, planning waits for the milestone's start](honest-reporting.spec.md)
6. [Oldest milestone first: an order for the run queue](milestone-queue-order.spec.md)
7. [Autopilot milestones one after another](strict-milestone-order.spec.md)

## Web UI (the Desk)

1. [Live agent output, prompts and steering](ui-live-transcript-and-steering.spec.md)
2. [The Desk: chat-first streams with history](desk-chat-first-ui.spec.md)
3. [Render GitLab-flavoured notes in the Desk](desk-markdown-notes.spec.md)
4. [Live chat with the agents](desk-live-agent-chat.spec.md)
5. [Ask the pilot](desk-ask-the-pilot.spec.md)
6. [Pause card and actions](desk-pause-card.spec.md)
7. [Spend you can see, fewer tokens, cleaner Desk](spend-tokens-desk.spec.md)
8. [Dashboard, milestone pages, pilot and retrospective tabs](desk-dashboard.spec.md)
9. [Works on a phone, markdown everywhere, one UI kit](desk-mobile-cleanup.spec.md)
10. [One Costs page across all projects, with charts](costs-page.spec.md)
11. [Stay logged in while you use it (OIDC refresh tokens)](desk-session-refresh.spec.md)
12. [Desk polish: version, phone Costs tab, ⌘K search](desk-polish.spec.md)

## Research

1. [Research platform: news flash, scheduled and long research](research-platform.spec.md)
2. [Research feed sources](research-feed-sources.spec.md)

## Releases

1. [GitLab release pipeline](gitlab-release-pipeline.spec.md)
2. [Operator-created releases](operator-created-releases.spec.md)
3. [Open-source release](open-source-release.spec.md)
