# Specs

Every non-trivial change starts as a spec: the problem, the goals and
non-goals, the decisions the maintainer approved, and the plan of merge
requests. A spec that ran as an autopilot milestone keeps a steering log of
what changed on the way. Specs record the problem and the decisions of their
time; when a spec and the [docs](../README.md) disagree, the docs win. Read a
spec to learn why it works that way.

New specs go here as `<title>.spec.md` and are added to this index. Each group
below is in the order the specs were written, so later ones build on earlier
ones. Each entry names the milestone the spec came from when the file or its
git history does; otherwise "no milestone".

## Workflow in GitLab

1. [Choosing engine, model and effort per issue](issue-model-and-effort.spec.md) — Per-issue engine, model and effort via labels and commands. Milestone: no milestone.
2. [Owner pings and agent-written MR descriptions](owner-pings-and-mr-descriptions.spec.md) — Ping owners when a human is needed; agents write MR descriptions. Milestone: no milestone.
3. [GitLab home, webhooks, hardening and the pilot's next steps](gitlab-home-and-hardening.spec.md) — Webhooks, hardening and the pilot's next steps on GitLab. Milestone: no milestone.
4. [Engine usage readings and engine fallback](engine-usage-and-fallback.spec.md) — Read engine usage and fall back when a limit hits. Milestone: no milestone.
5. [No lost work: checkpoints, smaller steps and conventional commits](checkpoints-and-conventional-commits.spec.md) — Checkpoint unpushed work, keep steps small, conventional commits. Milestone: ai-operator: no lost work, smaller steps, conventional commits.
6. [Read gate and writer subagent](read-gate-and-writer.spec.md) — Enforce read-only tools and a writer subagent for edits. Milestone: ai-operator: read gate and writer subagent.
7. [Cross-repo changes: companion repositories](cross-repo-changes.spec.md) — Change a companion repository in the same run and land it by rule. Milestone: Cross-repo changes: companion repositories.
8. [Merge request size budget per project](mr-size-budget.spec.md) — Per-project MR size budget and house rules for the planner. Milestone: no milestone.
9. [Projects: the operator works in every project of a GitLab group](group-projects.spec.md) — One AgentProjectSet watches a group; projects appear only while they have work. Milestone: Projects: the operator works in every project of a GitLab group, except the ones you exclude.
10. [Cursor's slow pool routes cursor roles to their fallback](cursor-slow-pool-fallback.spec.md) — Detect Cursor's slow pool and run cursor roles on their fallback. Milestone: no milestone.
11. [Cost levers: fresh step sessions, effort and Sonnet arms, context by tool](cost-levers.spec.md) — Experiment arms for the implementer, cost per merged line per arm, and context by tool. Milestone: Cost levers: fresh step sessions, effort and Sonnet arms, context by tool.
12. [Webhooks tell of milestones too, so idle projects stop polling GitLab](milestone-webhooks.spec.md) — Milestone events, heartbeats, explicit projects covered by the set, set hooks on dormant projects. Milestone: no milestone.
13. [Agents write in one language](agent-language.spec.md) — One chart value, `agent.language`, sets the language every agent writes in. Milestone: Agents write in English, set by one chart value.

## Autopilot and the pilot

1. [Pipeline health, infra retries and the blocked state](pipeline-health-and-blocked-state.spec.md) — Detect bad pipelines, retry infra and surface a blocked state. Milestone: no milestone.
2. [Milestone coordinator (`/pilot`)](milestone-coordinator.spec.md) — A `/pilot` coordinator for the milestone. Milestone: no milestone.
3. [Autopilot closes its milestone](autopilot-close-milestone.spec.md) — Close a done autopilot milestone after the retrospective. Milestone: no milestone.
4. [Autopilot finds its own way out of a pause](autopilot-way-out-of-a-pause.spec.md) — Every pause has a way out for the operator, pilot or owner. Milestone: ai-operator: autopilot finds its own way out of a pause.
5. [Pauses say why, spend adds up, planning waits for the milestone's start](honest-reporting.spec.md) — Honest pause reasons, spend totals and delayed planning. Milestone: ai-operator: pauses say why, spend adds up, planning waits for the milestone's start.
6. [Oldest milestone first: an order for the run queue](milestone-queue-order.spec.md) — Run queue prefers the oldest open milestone. Milestone: no milestone.
7. [Autopilot milestones one after another](strict-milestone-order.spec.md) — At most one milestone starts new issues at a time. Milestone: no milestone.
8. [Desk: run milestones in the order I choose, and close finished ones](desk-queue-closeout.spec.md) — Choose queue order from the Desk and close finished milestones. Milestone: Desk: run milestones in the order I choose, and close finished ones.
9. [Runs that hang get noticed and restarted](run-watchdog.spec.md) — Notice hung runs, restart them and escalate when that fails. Milestone: %23 Runs that hang get noticed and restarted.
10. [Autopilot works on several issues of a milestone at once](parallel-issues.spec.md) — Independent issues of one milestone run in parallel up to a limit. Milestone: Autopilot works on several issues of a milestone at once.

## Web UI (the Desk)

1. [Live agent output, prompts and steering](ui-live-transcript-and-steering.spec.md) — Live transcript of a task's pod, with prompts on request. Milestone: no milestone.
2. [The Desk: chat-first streams with history](desk-chat-first-ui.spec.md) — Chat-first project streams with history in Postgres. Milestone: no milestone.
3. [Render GitLab-flavoured notes in the Desk](desk-markdown-notes.spec.md) — Render GitLab-flavoured Markdown notes in the Desk. Milestone: no milestone.
4. [Live chat with the agents](desk-live-agent-chat.spec.md) — Chat with the agent of a running task. Milestone: no milestone.
5. [Ask the pilot](desk-ask-the-pilot.spec.md) — Ask the milestone's pilot without waiting for a run slot. Milestone: no milestone.
6. [Pause card and actions](desk-pause-card.spec.md) — Structured pause card with the fixes that pause allows. Milestone: no milestone.
7. [Spend you can see, fewer tokens, cleaner Desk](spend-tokens-desk.spec.md) — Spend drill-down, budgets and a cleaner Desk navigation. Milestone: no milestone.
8. [Dashboard, milestone pages, pilot and retrospective tabs](desk-dashboard.spec.md) — Dashboard, milestone pages, pilot and retrospective tabs. Milestone: Desk: dashboard, milestone pages, pilot and retrospective tabs.
9. [Works on a phone, markdown everywhere, one UI kit](desk-mobile-cleanup.spec.md) — Phone layout, shared Markdown and one UI kit. Milestone: %17 Desk: works on a phone, markdown everywhere, one UI kit, one definition per type.
10. [One Costs page across all projects, with charts](costs-page.spec.md) — One `/costs` page across projects, with charts and drill-down. Milestone: Desk: one Costs page across all projects, with charts.
11. [Stay logged in while you use it (OIDC refresh tokens)](desk-session-refresh.spec.md) — OIDC refresh tokens keep the Desk session alive. Milestone: Desk: stay logged in while you use it (OIDC refresh tokens).
12. [Desk polish: version, phone Costs tab, ⌘K search](desk-polish.spec.md) — Version footer, phone Costs tab, drop the milestone strip, ⌘K search. Milestone: Desk polish: version, phone Costs tab, no milestone strip, ⌘K search.
13. [One Needs you tab per milestone](desk-needs-you.spec.md) — What was asked, who handled it and what was tried, per milestone and in the Inbox. Milestone: Desk: one Needs you tab per milestone that shows what was asked, who handled it and what was tried.
14. [Plans name themselves, show their milestone's progress, one plan list](plans-overview.spec.md) — Agent-given plan titles, a `/plans` page and a Plans tab per project with milestone state. Milestone: Desk: plans name themselves, show their milestone's progress, and one plan list for all projects and each project.
15. [One Decisions tab to read, search and revisit decisions](desk-decisions.spec.md) — Stored copies of every decision, a Decisions tab per milestone and project, the revisit chain and what Revisit does. Milestone: Desk: one Decisions tab to read, search and revisit what the agents decided.
16. [One Metrics page, with lines of code next to cost](metrics-page.spec.md) — `/metrics` replaces `/costs`: written and merged lines, the cost per 100 merged production lines, Code, Flow and Pauses sections, and the Metrics tab. Milestone: Desk: one Metrics page, with lines of code next to cost, for all projects and each project.
17. [Answer a plan's decisions step by step](plan-decisions.spec.md) — The plan turn returns decisions as data, a wizard with the agent's pick preselected, versions with decided-by and sign-off, and a history. Milestone: Desk: answer a plan's decisions step by step, with the agent's pick preselected.
18. [Desk: one look on every page](desk-one-look.spec.md) — One shell, header and set of building blocks from Nuxt UI, readable text, a light/dark/auto switch, and the rule that enforces it. Milestone: Desk: one look on every page, built from Nuxt UI, with readable text and a light/dark/auto switch.
19. [One thread per problem, every conversation like a chat](desk-problem-thread.spec.md) — A `problems` table, one thread per stuck issue with one bar to act and an answer that shows at once, and `UChatMessages`/`UChatPrompt` for the Pilot tab, chats and streams. Milestone: no milestone (single MR).
20. [Desk: works by keyboard and screen reader](desk-accessible.spec.md) — WCAG 2.2 AA: an axe-core check on every route with a known list, landmarks and a skip link, focus return, one announcer, charts and status not by colour alone, reduced motion and a VoiceOver pass. Milestone: Desk: works by keyboard and screen reader, with an accessibility check on every page.
21. [Desk plans: draft a series of milestones that build on the queue](plan-milestone-series.spec.md) — Milestone files for the plan agent with queue position and `%N` references, a "Builds on" part, up to 5 drafts per plan created in strict order with `%draft-N` replaced, and a `%` menu in the plan chat. Milestone: Desk plans: draft a series of milestones that build on the queue.
22. [Desk: a timeline of which issue runs when, and milestones you drag into order](desk-timeline-and-milestone-order.spec.md) — A Timeline of issue runs per milestone and across projects, a run order stored on the AgentProject (`status.milestoneOrder`) that replaces run-next marks, and a draggable queue with `/agent move`. Milestone: Desk: a timeline of which issues run when, per milestone and across projects.

## Research

1. [Research platform: news flash, scheduled and long research](research-platform.spec.md) — News flash, scheduled and long research with an agent-shaped UI. Milestone: no milestone.
2. [Research feed sources](research-feed-sources.spec.md) — Feed sources the research runner reads. Milestone: no milestone.

## Releases

1. [GitLab release pipeline](gitlab-release-pipeline.spec.md) — GitLab job that builds images, the chart and the release. Milestone: no milestone.
2. [Operator-created releases](operator-created-releases.spec.md) — The operator creates releases through the GitLab API. Milestone: no milestone.
3. [Open-source release](open-source-release.spec.md) — Prepare the repository for the public open-source release. Milestone: no milestone.

## Docs

1. [Docs: a clear path from install to autopilot, in plain language](docs-revamp.spec.md) — Four user sections, contributing, specs index and a docs check. Milestone: %25 Docs: a clear path from install to autopilot, in plain language.
