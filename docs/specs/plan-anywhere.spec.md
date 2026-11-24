# Desk: start a plan from anywhere, in any project of your sets

Status: archived. The milestone's spec, run by autopilot after "Desk: one
header and one row of tabs on every page"
([desk-header-and-tabs](desk-header-and-tabs.spec.md)). It builds on
[plans-overview](plans-overview.spec.md) and
[group-projects](group-projects.spec.md). The docs requirement (R8, issue
#759) wrote this file and its steering log. Where this spec and the docs
disagree, the docs win: the flow is in
[Write a milestone](../guides/write-a-milestone.md#draft-it-in-the-desk),
the set rules in [Project sets](../guides/project-sets.md#active-and-dormant)
and the [AgentProjectSet reference](../reference/agentprojectset.md), the
API in the [Desk API](../reference/desk-api.md#plans), the internals in
[web-ui.md](../contributing/web-ui.md#plans).

## Goal

1. From every Desk page, one click on **New plan** at the top of the sidebar opens a New plan page. The button is there when the sidebar is collapsed too. On the page you pick the project, write what you want to plan and click **Start plan**. The plan opens with your message sent and the agent working on it.
2. The project picker offers every project you may steer: projects with their own AgentProject, and every project a project set discovered, dormant ones included. It is searchable and grouped like the sidebar, so a set with 100+ projects stays usable.
3. A plan in a dormant set project works. The set brings the project up within seconds of the plan's creation. Until then the plan says it is waiting for the project.
4. A plan keeps its set project. The set never removes a project, and so never garbage-collects its plans, while the project has a plan. Once the last plan is deleted or reaches its retention, the project goes dormant as before.

## Background

File refs were at main `3247236`, 2026-11-21.

- **Starting a plan.** `/plans` and the project's Plans tab rendered `PlanList.vue`. Its *Plan milestone* button started an untitled, empty plan at once with one project, or opened an inline row of project buttons (`planStartProjects` in `planStart.ts`). The rail had a *Plans* link but no way to start a plan.
- **Plan API.** `GET /api/v1/plans` listed the plans of visible AgentProjects and the projects with `steer`. Every per-project plan endpoint went through `loadPlans` and `loadProject`, which answered 404 without an AgentProject. `createPlan` took `{"title"}` only; a first message needed a second call to the chat endpoint.
- **Plans needed an AgentProject.** The plan controller marked a plan without its project `Failed` with "project not found" and did not retry. A plan is deleted after `plan-retention` (720h by default) since its last activity.
- **Project sets.** A set created a child only while the project had work: a ready issue, open AgentTasks or a spec-marker milestone. Otherwise the entry was `dormant` with no child. The removal kept a child only for open tasks ("kept: 1 open task"), so a child with plans was deleted with them. The set watched nothing but itself.
- **Access.** Authz rules match namespace and AgentProject name. A dormant entry has no AgentProject, but its future child name is known, so the same rules apply to it.

## Decisions (locked)

1. **Where you start a plan.** A **New plan** button at the top of the sidebar, above the navigation, whenever the `plans` feature is on; collapsed, an icon button with a tooltip. It opens `/plans/new`. The **New plan** action on `/plans` and on a project's Plans tab links there, the tab with `?project=<namespace>/<name>`. The inline picker in `PlanList` is removed. Rejected: only replacing the picker on `/plans`.
2. **Plans in dormant set projects.** The Desk creates the AgentPlan with `spec.projectRef` set to the child name the set would use, with the label `ai.wiemers.me/project-set: <set>` and no owner reference. The set counts plans as work, so it creates the child; creating the labelled plan wakes the set. The plan controller sets the owner once the child exists. Rejected: the Desk creating the AgentProject (new RBAC, goes around the set); offering only projects with a child.
3. **Plans keep their set project.** Every AgentPlan whose `spec.projectRef` names a child counts as work, like an open AgentTask, in planning (the entry stays `active`, also when the child's milestones only settle) and in removal (the live re-check lists plans too), until the plan is deleted by hand or by retention. The kept reason names both, e.g. "kept: 1 open task, 2 plans". Rejected: counting only plans still drafting.
4. **The New plan form.** Project (required), first message (required, "What do you want to plan?", the plan chat message limit) and title (optional, `planTitleOK`). **Start plan** creates the plan and its first message in one request and opens the plan. Rejected: project only.
5. **What the picker offers.** A searchable `USelectMenu`: **Projects** lists the visible AgentProjects you may steer, set children included; one group per visible set lists its `dormant` entries without a child that you may steer (`CanSteer` on the set's namespace and the computed child name), by GitLab path, marked "dormant, starts with the plan". Entries in state `excluded`, `explicit` or `nameTaken`, and entries whose child name an AgentProject already holds, are not offered. A project shows once. With nothing to offer, the page says so and shows no form.
6. **API shape: the existing endpoints, with a stand-in project.** `loadPlans` falls back to an offered set entry when the AgentProject is missing and hands the handlers a stand-in project (namespace, child name, no tasks, steer from the grant alone). With a stand-in project only `GET` plan, `GET` and `POST` chat, `DELETE` plan and `POST …/plans` work; every other plan endpoint answers 409 "waiting for the project set to create the project". `POST …/plans` takes `{"title"?, "message"}`, writes the message with `insertChat` and deletes the plan if that fails; without `message` it answers 400 on `message`. Rejected: a new `POST /api/v1/plans`.
7. **A waiting plan's status.** A labelled plan without its project gets phase `Starting` and "Waiting for the project set to create the project", retried every 10 s. Ten minutes after creation it gets `Failed` with "The project set did not create <name>: <reason>". An unlabelled plan keeps "project not found". No new phase. `PlanListItem` gets `message`, which the plan page shows below the header while the phase is `Starting` or `Failed`.
8. **One child name function.** `projectSetChildName` moves to `api/v1alpha1.ProjectSetChildName`, unchanged, called by the set controller and the Desk server.

## Requirements

### Operator (Go)

- **R1** Move `projectSetChildName` to `api/v1alpha1.ProjectSetChildName` with its tests (Decision 8).
- **R2** Plans count as set work (Decisions 2 and 3): the set lists the AgentPlans of its namespace per `spec.projectRef`; `projectSetInput.PlanCounts`; an entry with plans is active; `removeChildren` skips a child with plans and `removeChild` re-lists them live; `keptReason` names tasks and plans; the set watches AgentPlan creates with the set label and wakes the named set; RBAC markers to list and watch AgentPlans.
- **R3** The plan controller waits for a set project (Decision 7): `Starting` and a 10 s requeue, `Failed` after 10 minutes with the set's reason, the owner reference once the project exists, unlabelled plans as before.

### UI server (Go)

- **R4** `GET /api/v1/plans` lists set projects (Decision 5): `PlanProject` gets `path`, `set` and `dormant`; `allPlans` adds the offered entries under the child name and lists their plans; `?project=` works for them; `make ts-types`.
- **R5** Create and use a plan in a set project (Decisions 2, 4, 6 and 7): the stand-in project in `loadPlans`; `createPlan` with `message`, the set label and no owner for a stand-in project, and the delete on a failed insert; the 409s; `PlanListItem.message`; the rate limit and the audit line, which gains `set` and never holds the message.

### Web

- **R6** The New plan page `/plans/new` (route `plan-new`, Decisions 4 and 5): the header, the grouped `USelectMenu` preselected from `?project=`, the message `UTextarea` and title `UInput`, **Start plan**, field errors; the grouping in `planStart.ts` with node tests.
- **R7** Entry points and the waiting plan (Decisions 1 and 7): **New plan** at the top of `RailNav` (icon and tooltip collapsed, also in the phone sheet), on `/plans` and on the Plans tab; `PlanList` loses its picker; the plan page shows `message` while `Starting` or `Failed`.

### Docs

- **R8** `guides/write-a-milestone.md`, `guides/desk.md`, `guides/project-sets.md`, `reference/agentprojectset.md`, `reference/desk-api.md` and `contributing/web-ui.md`; this spec with a steering log, its line in the specs index and a steer line in `plans-overview.spec.md`.

## Constraints

- Under 300 production lines per merge request; tests, generated files and docs do not count.
- A narrowed requirement gets a follow-up issue and a steering log line.
- `make generate manifests` after API types or markers, `make ts-types` after view structs.
- No kubebuilder RBAC markers in `internal/ui`; the Desk service account gets no new rights.
- Child names, the dormancy rules for projects without plans, `dormantAfter`, `plan-retention` and the plan rate limit do not change. Plans in AgentProjects not owned by a set behave as before.
- No new dependencies; Nuxt UI components only; the axe check stays green.

## Non-goals

- Creating AgentProjects from the Desk, or planning in a GitLab project that no set or AgentProject covers.
- Planning in excluded, explicit or `nameTaken` entries, or changing a set's include or exclude from the Desk.
- A command palette entry or keyboard shortcut for New plan.
- Dormant set projects in the sidebar's project list, or a project page for them.
- Untitled plans showing their draft's heading.
- Changes to the plan page beyond the waiting message, or to the plan agent, its prompt or the decisions wizard.
- Creating the child straight from the known entry without a discovery.

## Suggested order

R1 → R2, R3 → R4 → R5 → R6 → R7 → R8

## Acceptance

- On any Desk page, open or collapsed sidebar, **New plan** opens `/plans/new`; on a project's Plans tab it opens with that project selected.
- On `/plans/new` you pick an AgentProject, write "Add a CSV export to the Metrics page" and click **Start plan**: the plan opens, your message is first in the chat and the agent answers without a further click.
- With the set `jo-wiemers` and a dormant project `jo.wiemers/tools/foo`, the picker shows it under `jo-wiemers`, marked dormant, and "foo" finds it. Excluded projects and projects you may only view are not offered; an AgentProject's project shows once.
- A plan in `jo.wiemers/tools/foo` says "Waiting for the project set to create the project"; within a minute the set shows the project active with a child `tools-foo`, the plan has the child as owner and the agent answers.
- With a set whose discovery fails, a plan in a dormant entry waits and after 10 minutes shows `Failed` with the set's reason.
- A set child with a plan and no other work past `dormantAfter` stays, with "kept: 1 plan"; after the plan is deleted, the set marks the project dormant and removes the child.

## Steering log

One line per change of plan or narrowed requirement: `- YYYY-MM-DD, Rn (#issue): what changed and why; follow-up #n`.

- 2026-11-23, R1 and R2: one merge request, as R1 is a move that R2 needs. The tests use the pure `planProjectSet` and `keptReason` plus controller-runtime's fake client for the live re-check and the plan watch, instead of envtest: `internal/controller` has no envtest suite, and the fake client covers the same paths faster. No follow-up issue.
- 2026-11-23, R2: an active entry kept only by plans, with no other work, reads "kept: N plans" (`keptReason(0, plans, "")`) in place of the dormant reason, so the status says why the child stays. With a removal reason it is appended: "kept: 2 plans; not matched by include".
- 2026-11-23, R3: fake-client tests, as for R2. The `Failed` reason prefers a failing set condition (`Discovered`, then `Ready`, with status False) over the entry's reason, since a set whose discovery fails has a stale entry; then the entry's reason, else "the entry is <state>"; "set <name> not found" and "the set has no entry for it" cover the rest. Plan retention is checked first, so an idle waiting plan is still deleted on time.
- 2026-11-23, R5: `message` stayed optional on `POST …/plans` while the old *Plan milestone* picker still created untitled, empty plans. Besides the endpoints in Decision 6, the plan's decisions history and its cost estimate also answer 409 on a stand-in project.
- 2026-11-24, R6 (#757): `USelectMenu` on the New plan page split shared chunks, so the initial bundle cap rose to 141.9 kB and the Metrics route's to 178.5 kB, logged in [web-ui.md](../contributing/web-ui.md#bundle-budget).
- 2026-11-24, R7 (#758): with the picker removed, `message` became required (400 "write what you want to plan" on `message`), and the audit line dropped its `message` flag.
- 2026-11-24, R8 (#759): the docs and this spec. `guides/desk.md` sat at its 400-line cap, so it names *New plan* in the rail and on the Plans tab and links to the guide for the flow (D759-P1).
