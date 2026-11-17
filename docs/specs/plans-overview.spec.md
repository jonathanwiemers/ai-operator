# Desk: plans name themselves, show their milestone's progress, and one plan list for all projects and each project

Status: the milestone's spec, run by autopilot. Narrowed requirements and
requirements without an issue go into the [Steering log](#steering-log).

## Goal

1. After the owner's first message, the plan gets a short title from the agent. The Desk shows that title instead of *Untitled plan · date*. Once the milestone is created, the plan's title becomes the milestone's title and stays that way.
2. A new Desk page, `/plans`, lists the plans of every project the user may see. Each row shows the plan's project, title, phase, spend and last activity. Plans still being drafted come first.
3. Every project page has a *Plans* tab next to *Stream*, *Board* and *Spend*. It shows the same list as `/plans`, filtered to the project. It has the *Plan milestone* button, which moves there from the Stream tab.
4. In both lists, a plan that created a milestone links to it and shows its state and progress. The state is running, waiting, paused, planned or done; the progress is merged issues out of total. While the Desk does not track the milestone, the plan links to it on GitLab and says *Not planned*.
5. *Plan milestone* on `/plans` asks for the project first. On a project's Plans tab it starts a plan in that project right away.

## Background

File refs are at origin/main 3b427fa (3b427faa4bbc635064bc06d4d4b066ea242193e4), 2026-11-15.

- **Plan title today.** `AgentPlanSpec.Title` (`api/v1alpha1/agentplan_types.go:12-14`) is set only when the plan is created (`internal/ui/plans.go:159-194`, `{"title"?}`). The Desk never sends one: *Plan milestone* calls `createPlan(ns, name)` without a body (`web/src/views/Stream.vue:196`). So every plan shows `planTitle`'s fallback *Untitled plan · date* (`web/src/planRows.ts:7-13`). `planItem` copies `pl.Spec.Title` into `PlanListItem.Title` (`internal/ui/plans.go:57-67`). The title can be at most 255 characters, on one line (`maxPlanTitle`, `:138`, `:173`).
- **What the agent can write.** The plan pod's role allows `get/list/watch` on `agentplans` and only `get/patch` on `agentplans/status` (`config/rbac/agent_role.yaml:23-29`, `charts/ai-operator/templates/rbac.yaml:81-87`). The runner patches only the status (`internal/runner/plan.go:276-286`).
- **The plan turn.** The engine answers with the schema `{reply, draft}` (`internal/runner/plan_prompt.go:63-71`, `PlanTurn` at `:74-77`). The system prompt describes those two fields at `:53-58`. On the first turn of a session the prompt includes "The plan's title: …" when the spec has one (`:94-96`). `PlanRun.turn` writes the draft at `internal/runner/plan.go:92-103`. An interrupted turn clears `out.Draft` (`:87-88`).
- **Milestone creation.** `createPlanMilestone` (`internal/ui/plan_milestone.go:20-78`) creates the GitLab milestone with the dialog's title, which `planMilestoneTitle` prefills (`web/src/planRows.ts:50-53`). It then records `createdMilestoneIID/URL` and the final draft in one status patch (`:69-74`).
- **Milestone state and progress.** `GET /api/v1/milestones` (`internal/ui/milestones.go:95-158`) builds the dashboard's rows:
  - It takes an optional `?project=ns/name` and answers 404 when the user may not see that project (`:120-127`).
  - `liveMilestoneRows` builds rows from the live spec tasks (`:192-219`): state from `liveMilestoneState` (`:268-283`), progress over the plan's and the autopilot's items (`:224-265`).
  - `mergeMilestoneRows` merges them with the history snapshot, which keeps done milestones whose tasks are gone (`:299-328`).
  - The handler does the merge inline (`:128-152`). `visibleProjects` (`:162-182`) lists the visible projects and all tasks.
  - The types are at `:17-39`.

  The web draws a row in `web/src/components/MilestoneRow.vue`: state badge at `:41-43`, progress bar with merged/total at `:47-59`, helpers `progressPct` and `stateTone` in `web/src/milestoneRows.ts:8-12`, `:35-46`. The Desk milestone page is route `stream-milestone` (`web/src/main.ts:37-42`).
- **Plans today.**
  - The routes are per project only (`internal/ui/server.go:162-170`). `projectPlans` lists the namespace's plans, 404 without chat (`internal/ui/plans.go:88-107`). `planOwner` decides `permissions.steer` (`:69-86`).
  - When the database is set, which chat needs, the UI's ClusterRole may already list `agentplans` in all namespaces (`charts/ai-operator/templates/ui-rbac.yaml:23-29`).
  - The list page is `web/src/views/Plans.vue`. Its route `plans` at `/projects/:namespace/:name/plans/:plan?` (`web/src/main.ts:51`) shows the list without `:plan` (`Plans.vue:39-52`: title and phase badge only) and `Plan.vue` with it. A deleted plan goes back to that list (`web/src/views/Plan.vue:50`).
  - The plan page links the milestone on GitLab (`Plan.vue:81`). The route guard hides `plans` without the feature (`web/src/main.ts:74-79`).
- **Project tabs today.**
  - A project page has *Stream | Board | Spend* (`projectTabs`, `web/src/streamTabs.ts:25-29`). Milestone pages spread the same list after *Issues | Pilot | Retrospective* (`milestoneTabs`, `:31-36`).
  - `Stream.vue` picks the list and the tab from `?tab=` (`web/src/views/Stream.vue:75-90`), renders the tabs with `RouteTabs` (`:468-474`) and has one pane per tab (`:527-672`).
  - The Stream tab starts with the plan entry: *Plan milestone* and a *Plans* link (`:530-541`). Its logic (`showPlans`, `canPlan`, `startPlan`) is at `:170-204`.
- **Navigation.**
  - Routes are lazy chunks, named from `web/src/routeNames.ts` (`plans` at `:11`) and covered by `crumbsFor` and its test (`web/src/crumbs.ts:71-110`, `plans` at `:104-110`).
  - The rail shows *Costs* across projects and a soft per-project *Plan* item (`web/src/components/RailNav.vue:77-84`, `:55-62`).
  - The ⌘K Pages group has *Plan milestone* for the open project, reusing the flag icon for the bundle budget (`web/src/searchItems.ts:38-46`).
- **Docs.** The Planner section is in `docs/ui.md:463-527`, the plans API rows at `:1694-1699`, navigation at `:358` and `:2323-2324`, and the Plans web notes at `:2606-2616`. The AgentPlan type is described in `docs/design.md#agentplan`.
- **Queue.** This milestone runs after the open milestones %13 to %26, oldest first. It builds on the planner from %22 and the dashboard milestone rows from %11, both on main at 3b427fa. %26 (Needs you tab) adds a tab to milestone pages, not to project pages. %24 (parallel issues) changes autopilot items but not how `liveMilestoneState` or progress are counted. This milestone reads whatever those produce.

## Decisions (locked)

1. **The agent's title goes in a new `status.title`.** The plan pod may only patch the status. Giving it `patch` on `agentplans` would also let it change `spec.maxBudgetUSD` and `spec.chatMessages`. Rejected: widening the agent role, or a controller copying the title into the spec.
2. **The agent names the plan once.** The runner asks for a title on every turn while the plan has none, so the first answer normally sets it. If the first message is too vague, a later turn sets it. Once `status.title` or `spec.title` is set, the runner no longer asks and ignores any title in the answer.
3. **The milestone title settles the plan's title.** When `createPlanMilestone` succeeds, it writes the created milestone's title (`m.Title`, falling back to the request's title) to `status.title`. This happens in the same status patch that records the IID. There is no second agent call.
4. **Which title is shown:** `status.title`, else `spec.title`, else *Untitled plan · date*. The rule lives in `planItem`. By Decision 2 the agent never writes `status.title` while `spec.title` is set, so a title the owner gave wins until the milestone settles it.
5. **One source for milestone state and progress.** Move the live-plus-snapshot merge out of the `milestones` handler (`internal/ui/milestones.go:128-152`) into a method that the dashboard and the plans endpoint both call. A plan's milestone is the row with the same namespace, project and `createdMilestoneIID`. Rejected: asking GitLab for the milestone's issues, which costs one call per plan and counts differently from the dashboard.
6. **`PlanListItem` gains `namespace`, `project` and `milestone`.** `milestone` is `{state, progress, stored}`, using the existing `MilestoneState` and `MilestoneProgress` types. It is nil when the plan created no milestone or the Desk has no row for it. The milestone rows are only read when at least one listed plan has a `createdMilestoneIID`.
7. **Where the milestone link goes.** It opens the Desk milestone page (`stream-milestone`) when `milestone` is set, else `createdMilestoneURL` on GitLab with the note *Not planned*. The label is always `%N`.
8. **One list endpoint: `GET /api/v1/plans`, optionally `?project=ns/name`.** It answers `{plans, projects}`:
   - `plans`: the `PlanListItem`s of the visible projects, or of that one project, ordered by `lastActivityAt`, newest first.
   - `projects`: the same projects as `{namespace, name, steer}`, sorted by namespace and name. `steer` is `planOwner`'s answer.

   It returns 404 without chat, and 404 for a `project` the user may not view, like `GET /api/v1/milestones`. Both Desk lists use it. `GET /api/v1/projects/{ns}/{name}/plans` stays for the API, but the web no longer calls it.
9. **Two sections.** *Drafting* holds every phase except Created, Failed included with its danger badge. *Milestones created* holds the plans in phase Created. Each section is ordered by last activity, newest first. A section with no plans is not shown, and a list with no plans says *No plans yet.* Rejected: one list with a filter.
10. **One plan row.** It is a component `web/src/components/PlanRow.vue`. The project column shows only when the list spans projects (`showProject`), like `MilestoneRow.vue`. The row shows:
    - the title linking to the plan
    - the phase badge
    - spend of budget (`planSpendLabel`)
    - last activity
    - for created plans, the milestone link, the milestone's state badge (`stateTone`) and the progress bar with merged/total

    On a phone it wraps without sideways scrolling.
11. **One progress bar.** Move the bar from `MilestoneRow.vue:47-59` into `web/src/components/ProgressBar.vue` (props: merged, total, label). Both rows use it, and `MilestoneRow` looks the same as before.
12. **One plan list.** It is a component `web/src/components/PlanList.vue` with an optional `project` prop (`{namespace, name}`). It fetches `GET /api/v1/plans` (with `?project=` when set) and renders:
    - the *Plan milestone* button
    - the two sections of `PlanRow`, with `showProject` only without `project`
    - loading, error and empty states

    The `/plans` page and the project's Plans tab are thin hosts of it. There is no second list.
13. **The project's Plans tab.**
    - `projectTabs` becomes *Stream | Board | Spend | Plans*. *Plans* shows only with the feature `plans`, as `?tab=plans` on `stream-project`.
    - `milestoneTabs` keeps its tabs: it spreads a base list of Stream, Board and Spend, without Plans.
    - The plan entry on the Stream tab (`Stream.vue:530-541`) and its logic (`:170-204`) are removed. The Plans tab replaces them.
    - `/projects/{ns}/{name}/plans` without a plan redirects to `?tab=plans`. `/projects/{ns}/{name}/plans/{plan}` keeps showing the plan page.
14. ***Plan milestone* asks for nothing but the project.**
    - The button shows when `projects` has a project with `steer: true`, so also on an empty list. A 403 on create hides it.
    - With `project` set, it starts an untitled plan in that project and opens it.
    - On `/plans` it opens a small picker of the steer projects, or skips the picker when there is only one, then does the same.
    - The agent names the plan later (Decision 2). The start logic lives in `web/src/planStart.ts`.
15. **Navigation.**
    - The rail gets a top-level *Plans* item, sub-line *milestone plans, across projects*, below *Costs*, with the feature `plans`. The soft per-project *Plan* item (`RailNav.vue:55-62`, `:82-84`) is removed: the project's tab replaces it.
    - ⌘K Pages gets *Plans*, reusing the flag icon. Its *Plan milestone* entry for the open project opens the project's Plans tab.
    - The route is `all-plans` at `/plans`, a lazy chunk, with the breadcrumb *Plans*.
    - On a plan, the breadcrumb is Overview › project › *Plans* (the project's Plans tab) › plan.
    - Deleting a plan returns to the project's Plans tab.
16. **The agent's title must be short:** the prompt asks for at most 80 characters, plain words, no ticket numbers, in the style of a milestone title. The runner keeps only the first line, trims it and cuts it to 255 characters (`maxPlanTitle`).
17. **`title` is a required string in the turn schema**, empty when there is nothing to set, like `draft`.

## Requirements

### API (Go)

**R1. `AgentPlanStatus.Title`.** Add `Title string` (`json:"title,omitempty"`, `+optional`, `+kubebuilder:validation:MaxLength=255`) to `AgentPlanStatus` in `api/v1alpha1/agentplan_types.go`. Its comment: the plan's title from the agent, then the created milestone's title. Run `make generate manifests` and `make ts-types`.
Tests: `api/v1alpha1/agentplan_test.go` admits a status with `title` against both CRD files and rejects one over 255 characters. The drift job is green.

### Runner (Go)

**R2. Ask for a title while the plan has none.** In `internal/runner/plan_prompt.go`:
- Add `"title"` to `planTurnSchema` and to its `required` list (Decision 17), and add `Title string` to `PlanTurn`.
- Add one line to the system prompt's "Answer every turn with" list (Decision 16).
- Add `Untitled bool` to `planTurnInput`. When it is true, `buildPlanPrompt` adds a `## Title` section: "The plan has no title yet: give one in title."

Tests: `internal/runner/plan_prompt_test.go` covers the section with and without `Untitled`, and the schema lists `title` as required.

**R3. Write the agent's title once.** In `PlanRun.ask`, pass `Untitled: plan.Spec.Title == "" && plan.Status.Title == ""`. In `PlanRun.turn`'s `answer` mutate, set `s.Title` only when `s.Title == ""`, `plan.Spec.Title == ""` and the cleaned title is not empty (Decision 16). An interrupted turn writes no title.
Tests: `internal/runner/plan_test.go` covers four cases:
- a first turn sets `status.title`
- a second turn with another title keeps the first
- a plan with `spec.title` gets none
- an interrupted turn and a multi-line title

### UI server (Go)

**R4. Show and settle the title.** `planItem` sets `Title` to `status.title`, else `spec.title` (Decision 4). `createPlanMilestone` sets `pl.Status.Title` to the created milestone's title in the patch that records the IID (`internal/ui/plan_milestone.go:69-74`). The failure path leaves the title unchanged.
Tests:
- `internal/ui/plans_test.go`: the title precedence.
- `internal/ui/plan_milestone_test.go`: the title after a create and after a GitLab failure.

**R5. One milestone-rows method.** Move the live-plus-snapshot merge of the `milestones` handler (`internal/ui/milestones.go:128-152`, without the state filter, the sort and the limit) into a method on `Server`. It takes the visible projects and the tasks and returns the merged rows and whether the history answered (Decision 5). The handler calls it. Its answer does not change.
Tests: the existing `internal/ui/milestones_test.go` and `golden_test.go` pass unchanged, plus one test of the method on a live and a stored row.

**R6. Milestone state on plan items.** Add `Namespace`, `Project` and `Milestone *PlanMilestone` (`json:"milestone,omitempty"`; fields `state`, `progress`, `stored`) to `PlanListItem` (Decision 6). `GET …/plans/{plan}` fills them from R5's rows. Run `make ts-types`.
Tests: `internal/ui/plans_test.go` covers three cases:
- a Created plan whose milestone has a running spec task with 2 of 4 issues merged shows `{state: running, progress: {merged: 2, total: 4, open: 2}}`
- a plan whose milestone is only in the snapshot and done shows `stored: true`
- a Created plan without a row has no `milestone`

**R7. `GET /api/v1/plans`.** Register the route in `internal/ui/server.go` and add the handler (Decision 8):
- the visible projects from `visibleProjects`, narrowed by `?project=` as in `milestones` (`internal/ui/milestones.go:120-127`)
- one `List` of all `AgentPlan`s, kept when their `projectRef` is one of those projects in the same namespace
- `milestone` from R5's rows, read only when a kept plan has an IID
- `steer` per project from `planOwner`

Tests: `internal/ui/plans_test.go` covers seven cases:
- plans of a project the user may not view are left out
- `?project=` narrows both `plans` and `projects`
- `?project=` of a hidden project is 404
- `steer` differs per project
- the order is by last activity
- milestone state is filled as in R6
- 404 without chat

### Web

**R8. Shared progress bar.** Add `web/src/components/ProgressBar.vue` (Decision 11) and use it in `MilestoneRow.vue`, with no visible change. It keeps today's `role="progressbar"` and aria attributes.
Tests: `make ui`. The dashboard and the project page look as before.

**R9. Plan row.** Add `web/src/components/PlanRow.vue` (Decision 10), with the helpers in `web/src/planRows.ts`:
- `planMilestoneLink(item)` returns `{label: '%N', to}` for the Desk page, `{label: '%N', href, note: 'Not planned'}` for GitLab, or null (Decision 7).
- `planSections(items)` returns `{drafting, created}`, each ordered (Decision 9).

Tests: `web/test/planRows.test.mjs` covers both helpers. `make ui` passes.

**R10. Plan list.** Add `getPlans(project?)` in `web/src/client.ts` and `web/src/components/PlanList.vue` with its sections, states and `project` prop, without the button (Decision 12).
Tests: `make ui`. A node test of the pure helper that builds the request path, with and without a project.

**R11. *Plan milestone* in the plan list.** Add `web/src/planStart.ts` and the button in `PlanList.vue` (Decision 14). `planStartProjects(projects)` returns the steer projects, sorted. It shows *…* while busy, shows the error in a banner, and a 403 hides the button.
Tests: a node test for `planStartProjects` (none, one, two, a steer-false project left out). `make ui`.

**R12. The `/plans` page.** Add these:
- `web/src/views/AllPlans.vue`, hosting `PlanList` without `project`
- route `all-plans` at `/plans` as a lazy chunk, with its `routeNames` entry and the *Plans* crumb
- the rail item *Plans* and the ⌘K entry *Plans* (Decision 15)

Tests: the `crumbsFor` test covers `all-plans`, and a `searchItems` test covers *Plans*. The bundle budget passes. `make ui`.

**R13. The project's Plans tab.** In `web/src/streamTabs.ts`, add *Plans* to the project tabs only when the feature `plans` is on, and keep `milestoneTabs` as it is (Decision 13). `Stream.vue` renders `PlanList` with the project on `?tab=plans`, and removes the plan entry on the Stream tab and its logic (`Stream.vue:170-204`, `:530-541`).
Tests: a `streamTabs` node test covers four cases:
- the project tabs with and without the feature
- `milestoneTabs` has no Plans
- `tabFromQuery('plans')` without the feature falls back to Stream

`make ui`.

**R14. Plan links point at the tab.** Make these changes:
- `/projects/:namespace/:name/plans` without `:plan` redirects to `stream-project` with `?tab=plans`, and the route shows only `Plan.vue`. `Plans.vue` is removed.
- `crumbsFor` for a plan links *Plans* to the tab.
- `Plan.vue:50` returns to the tab after a delete.
- The rail's per-project *Plan* item is removed.
- ⌘K *Plan milestone* opens the tab (Decision 15).

Tests: the `crumbsFor` test covers the plan trail, and a `searchItems` test covers the *Plan milestone* target. By hand: an old `/projects/{ns}/{name}/plans` link opens the Plans tab. `make ui`.

### Docs

**R15. Docs and spec.** Update these pages:
- `docs/ui.md` Planner (`:478-487`: entry points, the `/plans` page, the project's Plans tab, the title rule, the milestone link, state and progress, *Plan milestone*; `:513-517`: the plan takes the milestone's title)
- the project tabs wherever `docs/ui.md` lists *Stream | Board | Spend*
- the API table (`:1694-1695`: `title`, `namespace`, `project`, `milestone`; a new row for `GET /api/v1/plans` with `?project=`)
- navigation (`:358`, `:2323-2324`)
- the Plans web notes (`:2606-2616`: `PlanList.vue`, `PlanRow.vue`, `ProgressBar.vue`, `planStart.ts`, `AllPlans.vue`, the tab)
- `docs/design.md#agentplan` (`status.title`)

Add `docs/specs/plans-overview.spec.md` with this spec and a steering log.
Tests: the links in the changed docs resolve.

## Constraints

- Under 300 production lines per merge request; tests, generated files and docs do not count. Go and web changes may share a merge request.
- When a requirement has to be narrowed, open a follow-up issue for the rest and say so in the steering log of `docs/specs/plans-overview.spec.md`.
- Generated files are checked: run `make generate manifests` after changing API types or kubebuilder markers, and `make ts-types` after changing view structs or the CRD types the views embed. The CI drift job fails on stale files.
- Do not change the agent role, the UI role or any chart RBAC (Decision 1; the UI may already list plans, see Background). Add no kubebuilder RBAC markers to `internal/ui`.
- The answer of `GET /api/v1/milestones` does not change (R5 is a move). The answer of `GET /api/v1/projects/{ns}/{name}/plans` keeps its shape, apart from the fields R6 adds.
- Milestone pages keep their tabs. `MilestoneRow.vue` and the dashboard keep their look.
- `spec.title`, the `POST …/plans` request body and its 255-character check stay as they are.
- Prompts of the other roles do not change.
- New views are lazy route chunks, and the initial bundle stays within `bundle-budget.json`. New icons only when the bundle budget allows; otherwise reuse the flag icon.
- Every new page, tab and row works on a phone without sideways scrolling.
- No new dependencies.
- Before pushing: `gofmt`, `go vet ./...`, `make test`, `make helm-lint`. After web changes: `make ui`.

## Non-goals

- Renaming a plan by hand in the Desk. This is the tempting next step; it gets its own milestone.
- Live updates of the plan lists: they load when the list opens.
- Filters, search or paging beyond the two sections and the project filter.
- Removing `GET /api/v1/projects/{ns}/{name}/plans`.
- Moving the plan page itself into the tab: `/projects/{ns}/{name}/plans/{plan}` stays its own page.
- A Plans tab on milestone pages.
- Renaming the GitLab milestone when the plan's title changes, or the other way round after creation.
- Asking the agent for a new title after the milestone is created.
- A title field for *Plan milestone*.
- Showing a plan's milestone cost or leftovers in the plan row: the milestone page has them.
- Deleting plans from the lists. The plan page keeps *Delete*.

## Suggested order

R1 → R2 → R3 → R4, R5 → R6 → R7, R8 → R9 → R10 → R11 → R12, R13 → R14 → R15

## Acceptance

- On a new plan started with *Plan milestone*, after the first answer to "add a version line to the rail", the plan lists and the plan page show a short title from the agent, not *Untitled plan · date*. After three more turns it is still the same title.
- On a plan created through `POST …/plans` with `{"title":"My plan"}`, the title stays *My plan* after any number of turns.
- On a plan whose milestone is created with the title *Desk: version in the rail*, both lists and the plan page show *Desk: version in the rail*.
- On `/plans`, a user who may view two projects sees the plans of both, each with its project name. Plans being drafted appear under *Drafting*, and created ones under *Milestones created*. Plans of a third project the user may not view are not shown.
- On a project page with the plans feature, the tabs read *Stream | Board | Spend | Plans*. *Plans* shows the same sections and rows as `/plans`, only the project's plans, and no project column. A milestone page has no Plans tab. Without the feature there is no Plans tab.
- On a project's Plans tab, an owner sees *Plan milestone*, also with no plans yet, and it opens a new plan of that project. A user without steer sees no button. The Stream tab no longer has *Plan milestone* or a *Plans* link.
- On `/plans`, a user who owns two projects clicks *Plan milestone*, picks one and lands on the new plan of that project. With one owned project there is no picker. With none there is no button.
- In both lists, a plan whose milestone runs on autopilot with 2 of 4 issues merged shows *%N*, a *running* badge and a half-full bar with *2/4*. *%N* opens the Desk milestone page. After the milestone is done, the row shows *done* and a full bar, also once its tasks are gone and the history database has it.
- On a plan whose milestone was created without *Spec*, the row shows *%N* linking to GitLab, with *Not planned* and no bar.
- An old link `/projects/{ns}/{name}/plans` opens the project's Plans tab. On a plan page, the breadcrumb *Plans* and *Delete* lead back to that tab.
- The rail shows *Plans* below *Costs* and no per-project *Plan* item. ⌘K lists *Plans* under Pages, and *Plan milestone* opens the open project's Plans tab.
- The dashboard's milestone lists look and work as before.

## Steering log

Each slice appends its lines here: requirements it narrowed (with the
follow-up issue), and requirements without an issue and why.

- 2026-11-17 #534 (R1–R4, spec file): `docs/design.md` and `docs/ui.md`
  no longer exist; the internals are in `docs/contributing/design.md` and
  `docs/contributing/web-ui.md`, the user pages in `docs/guides/` and
  `docs/reference/`. `status.title` (who writes it and when) is described in
  the Planner section of `docs/contributing/web-ui.md` and the plans rows of
  the API table in `docs/contributing/design.md`. R15 should target those
  pages.
