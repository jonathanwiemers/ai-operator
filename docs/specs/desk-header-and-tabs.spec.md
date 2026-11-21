# Desk: one header and one row of tabs on every page, and an account menu

Status: archived. The milestone's spec, run by autopilot after "Desk: works
by keyboard and screen reader" ([desk-accessible](desk-accessible.spec.md))
and before "Desk: a timeline of which issue runs when"
([desk-timeline-and-milestone-order](desk-timeline-and-milestone-order.spec.md)).
The docs requirement (R8, issue #675) wrote this file and its steering log.
Where this spec and the docs disagree, the docs win: the tab lists, defaults
and aliases are in [web-ui.md](../contributing/web-ui.md#routes-and-tabs),
what you see in [The Desk](../guides/desk.md).

## Goal

1. Every Desk page has the same header: status, title, one primary action and a ⋯ menu, one meta line. Directly below is **one** row of underline tabs that switch pages. Filters and view switches are never a second row of tabs: they sit in a toolbar inside the tab's content, with search on the right.
2. A project page opens on a **Milestones** tab (queue, Finishing, In progress, Recently done). The event feed, its filters, search and the heatmap move to an **Activity** tab. Old `?tab=stream` links open Activity.
3. A milestone page has the tabs Issues · Activity · Decisions · Board · Metrics, plus Needs you only while something needs you and Retrospective once one exists. The pilot chat opens in a drawer from an **Ask pilot** button, from any tab.
4. The sidebar footer ends in an **account menu**: one button with your name and the live dot opens a menu with your login, roles, how long the login lasts, the appearance switch, the Desk version and **Sign out**. Later account settings go into this menu.
5. Each Claude usage meter in the sidebar says when that window resets ("resets 14:05", "resets Tue 09:00").

## Background

File refs were at origin/main `46f4b2cd`, 2026-11-20.

- **Two rows of tabs.** Level 1 was `web/src/ui/RouteTabs.vue`, RouterLinks in a grey segmented box. Level 2 were `UTabs` `variant="pill"` switches: the event filter (`views/Stream.vue`), Decisions All/Active/Superseded (`?state=`), tokens/USD (`SpendPanel.vue`), runner/agent (`TaskLogs.vue`), Open/Handled (`Inbox.vue`, `?view=`) and `WhereItGoes.vue`. A pill row under a segmented row read as nested tabs.
- **Tab lists** in `web/src/streamTabs.ts`: project Stream, Decisions, Board, Metrics (+ Plans); milestone Issues, Needs you, Decisions, Pilot, Retrospective, Stream, Board, Metrics; dashboard Overview, Stream; task Overview, Chat, Live, Logs, Calls, Details; `tabAlias` mapped `spend` → `metrics`.
- **Project and milestone header** (`Stream.vue`): `UPageHeader` with the status, `CostChip`, a **Metrics** button that duplicated the Metrics tab, and Context. The Stream tab began with `ProjectQueue`, `ProjectFinishing` and `MilestoneSections`, which pushed the feed below the first screen.
- **Milestone tabs.** Needs you was listed even when nothing waited; Pilot was a tab with `MilestonePilot`.
- **Task tabs.** Details held only the stat tiles (runs, spend, branch, last push).
- **Header cost differed between tabs** on a project page (Stream vs. Metrics, seen 2026-11-20). Cause unknown.
- **Sidebar footer** (`RailNav.vue`): `ThemeSwitch`, one `UProgress` row per usage window without reset time, the live dot, the username, *Sign out*, the "login lasts" line and the version. Hidden when collapsed.
- **Reset times** existed in `EngineUsage.vue` (`clockTime(resetsAt, now)`, "stale, reset" once past).

## Decisions (locked)

1. **Two levels, two looks.** Level 1 switches pages and is an underline tab row. Level 2 changes what one tab shows and lives in a toolbar inside that tab. A page never shows two tab rows. Rejected: keep the segmented box and restyle the pills.
2. **Level 1 is `UNavigationMenu` horizontal with `highlight`**, rendered by `RouteTabs.vue`, which keeps its props (`tabs`, `active`, `to`, `badges`, `label`), its `nav` landmark name and its sideways scroll. Call sites do not change. Rejected: `UTabs` (not links).
3. **Level 2 is `UDashboardToolbar`** at the top of the tab content: switches and filters on the left, search on the right; on a phone it wraps, search last.
4. **Header pattern.** `UPageHeader` with `pageHeaderUi`: headline = status, title = the `<h1>`, links = one primary action plus `HeaderActions` (⋯), description = one meta line. Alerts sit below the tabs. The project header's Metrics button is removed.
5. **The header cost is one number per page**, the same on every tab; the cause of the difference is found and fixed, not hidden.
6. **Project tabs: Milestones · Activity · Decisions · Board · Metrics · Plans.** Milestones is first and the default. Activity is the old Stream. `?tab=stream` opens Activity.
7. **Milestone tabs: Issues · Activity · Decisions · Board · Metrics**, then Needs you and Retrospective when they apply. Needs you is listed only while the milestone is paused or has an open problem, with its count as the badge; `?tab=needs` otherwise opens Issues.
8. **Pilot is a drawer.** *Ask pilot* in the milestone header opens `USlideover` (right; full width under 768 px) with `MilestonePilot`. `?pilot=1` keeps it open; old `?tab=pilot` links open Issues with the drawer. Closing returns focus to the button. Rejected: a section on Issues.
9. **Dashboard: Overview · Activity.** `?tab=stream` opens Activity.
10. **Task tabs: Overview · Chat · Live · Logs · Calls.** Details is gone; its stat tiles move to the top of Overview. `?tab=details` opens Overview.
11. **The account menu** is a `UDropdownMenu` on a button at the very bottom of the sidebar, also when collapsed (avatar initials and live dot). Top to bottom: username, roles, "Login lasts …" (OIDC); **Appearance** Light / Dark / Auto as checkbox items (replaces `ThemeSwitch`); the version line; a separator; **Sign out** (OIDC only). With `auth.mode: none` it shows "No login (auth off)". Rejected: a menu in a top bar.
12. **Reset times.** Each usage row gets "resets 14:05" (`clockTime`, `absTime` as title) and "reset" in `--text-3` once past. The meters stay hidden when collapsed.
13. **Query tabs stay.** Tabs keep `?tab=`; no path-based tabs.

## Requirements

### Web

- **R1 Underline route tabs** (Decisions 1, 2). `RouteTabs.vue` renders `UNavigationMenu` horizontal, `highlight`, with badges; same props, landmark, sideways scroll and reveal of the active tab. UI kit row updated. Tests: RouteTabs and route e2e, axe per route, `aria-current="page"` on the active tab only, no tab row inside another.
- **R2 Tab toolbar** (Decision 3). Every pill switch moves into a `UDashboardToolbar` at the top of its tab; search on the right on Activity; the heatmap below the toolbar. Tests: e2e at 375 and 1280 px that each toolbar sits in the tab and does not overflow; `?state=`, `?view=` and the event filter keep working.
- **R3 Header pattern and cost** (Decisions 4, 5). Headers follow Decision 4; `metricsLink` and its button go; alerts below the tabs; the milestone `<h1>` uses the project list's title while loading. Find and fix the cost difference. Tests: unit test for the header cost source; e2e for the same cost on Activity and Metrics; `stream-project header rows` updated.
- **R4 Project and dashboard tabs** (Decisions 6, 9, 13). Tab id `activity` replaces `stream`; project gets `milestones` first and default; `tabAlias` maps `stream` → `activity`; `Stream.vue` splits Milestones and Activity. Tests: `streamTabs` node tests; e2e for the default and `?tab=stream` on project and dashboard.
- **R5 Milestone tabs and pilot drawer** (Decisions 7, 8). New milestone tab list; Needs you only when needed, with badge; Pilot opens in `USlideover` from *Ask pilot*; `?pilot=1`, `?tab=pilot` → Issues + drawer. Tests: `milestoneTabsFor` node tests; `needs-you.spec.ts` for the hidden and shown tab; e2e that the drawer opens, traps focus, closes on Escape and returns focus.
- **R6 Task tabs** (Decision 10). Drop Details; stat tiles at the top of Overview; `?tab=details` → Overview. Tests: `taskTabs` alias test; task e2e updated.
- **R7 Account menu and reset times** (Decisions 11, 12). Rework the `RailNav.vue` footer; delete `ThemeSwitch.vue`; a pure reset-label helper in `format.ts`. Tests: node test for the label (today, other day, past); e2e for the menu by keyboard, with OIDC and auth off, collapsed, switching the colour scheme; axe with the menu open.

### Docs

- **R8** `guides/desk.md`, `getting-started/turn-on-the-desk.md`, `guides/pilot.md`, `guides/steer-from-the-desk.md`; `contributing/web-ui.md` sections Page skeleton, Filters, RouteTabs, Stream header (renamed Activity header), Needs you tab, Routes and tabs, Navigation and the UI kit; docs screenshots retaken; this spec with a steering log and a line in the specs index.

## Constraints

- Under 300 production lines per MR; tests, generated files and docs do not count.
- Every old URL keeps working: `?tab=stream`, `?tab=pilot`, `?tab=details`, `?tab=needs`, `?tab=spend`.
- Everything the accessible milestone added stays: one `<h1>` per page inside `UPageHeader`, focus to the `<h1>` after navigation, focus kept on tab switches, landmarks, the axe check per route with no new entries in `known-a11y.ts`.
- Nuxt UI components only; `npm test` green; no new dependencies.
- No Go changes; the API and the generated types stay as they are.
- A narrowed requirement gets a follow-up issue and a steering log line; every R has an issue or a steering log line.

## Non-goals

- The timeline milestone's Timeline, drag-and-drop queue, `/agent move` and timeline API (this milestone gives them their place only).
- A properties column on milestone and task pages.
- Real account settings (notifications, display preferences, tokens).
- Path-based tabs (`/projects/x/activity`).
- Changing the Inbox, Costs or Plans pages beyond moving their switches into the toolbar.
- Merging Live into Chat on the task page.

## Suggested order

R1, R7 → R2 → R3 → R4 → R5, R6 → R8.

## Acceptance

- On any project, milestone, task or the dashboard there is one row of underline tabs under the header and no second row; filters sit in a toolbar inside the tab.
- A project page without `?tab=` shows Milestones with the queue; Activity shows the feed with filter pills and search in one toolbar and the heatmap; `?tab=stream` opens Activity.
- The project header cost is the same on Activity and Metrics, and there is no Metrics button in the header.
- On a running milestone the tabs are Issues · Activity · Decisions · Board · Metrics; when it pauses, Needs you appears with a badge; Ask pilot opens the drawer on any tab, and `?tab=pilot` opens it too.
- A task has no Details tab; its stat tiles are at the top of Overview.
- The account menu at the bottom of the sidebar has login, roles, login duration, appearance, version and Sign out; with the sidebar collapsed the avatar opens it.
- Each usage meter shows "resets 14:05" (or a day and time), and "reset" once past.
- The axe check passes on every route, at 375, 768 and 1280 px, light and dark.

## Steering log

One line per change of plan or narrowed requirement: `- YYYY-MM-DD, Rn (#issue): what changed and why; follow-up #n`.

- 2026-11-21, R1 (#669): `RouteTabs.vue` renders `UNavigationMenu` and stays in the UI kit's custom list with a new reason: it wraps the menu for `?tab=` links, sets the active item from its prop (vue-router's active match ignores the query and would mark every tab, D669-P1), and keeps the in-strip scroll and reveal of the active tab.
- 2026-11-21, R7 (#670): the account menu's items are built in a new pure `web/src/accountMenu.ts`; `ThemeSwitch.vue` is deleted and its logic stays in `colorScheme.ts`. `UDropdownMenu` in `RailNav.vue` regroups shared Reka UI chunks, so the Metrics route's bundle cap rose from 176.7 to 177.0 kB (measured 176.9), logged in [web-ui.md](../contributing/web-ui.md#bundle-budget).
- 2026-11-21, order: R4 (#671, project and dashboard tabs) merged before R2 (#672, tab toolbar), against the suggested order, so the toolbar was built once on the split Activity tab instead of on Stream and then moved. R4 renamed the route keys in `known-a11y.ts` with the tabs (`…-stream` → `…-activity`); the entries are the accessible milestone's open `color-contrast` and `target-size` ones, now listed under the route where the heatmap and feed are, and no new rule was added.
- 2026-11-21, R2 (#672): `UDashboardToolbar` on *Where it goes* raised the Metrics route's cap to 177.6 kB (measured 177.5), logged in [web-ui.md](../contributing/web-ui.md#bundle-budget). The toolbar's layout follows D672-P1 (switches left, search right, `searchToolbarUi` puts search on its own row below 760 px).
- 2026-11-21, R3 and R6 (#673): R6 merged into the header MR, as both change `TaskBody.vue`'s header and tabs. The header cost differed because two requests carry it, the project (or milestone) view reloaded on live events and the Metrics tab's totals, both summed from `agent_calls`; the server records calls up to a second after the task event that reloads the project, and plan calls send no event, so the view's copy lagged behind the Metrics tile. Fix: `web/src/headerCost.ts` keeps the larger of two sums from calls (`keepCost`) and never lets a tasks-based sum replace one from calls; unit-tested, and an e2e checks the same cost on Activity and Metrics. No Go change and no follow-up issue. Besides the stat tiles, the pod and its conditions (once on Details) moved to the end of Overview (D673-P1). The ⋯ holds the project or milestone in GitLab (D673-P2). Four `task-details` and `issue-details` entries left `known-a11y.ts`.
- 2026-11-21, R5 (#674): Needs you is listed while `needsOpen` counts open items, and also while a problem thread or a `#prompt-`/`#problem-` link is open, so a thread does not vanish once handled (D674-P1). The count is the RouteTabs badge, the label stays *Needs you* (D674-P2). The drawer's state is `?pilot=1` (D674-P3). On a stored milestone the button says *Pilot*, as its drawer holds the pilot's events only (D674-P4). The server's longest-pause links keep `?tab=pilot` (no Go change), which the Desk opens as Issues plus the drawer.
- 2026-11-21, R8 (#675): narrowed. The docs and this spec are written, but the docs screenshots were not retaken: in the run's container Playwright's Chromium does not start (missing system libraries such as `libglib-2.0`) and `cwebp` is missing, and system packages may not be installed. The shot list already matched the new Desk (`project-stream` opens `?tab=activity`; `account-menu` and `metrics-filters` shots exist since #670 and the one-look milestone), but `account-menu.webp` was never rendered, so `guides/desk.md` shows no image of the account menu yet. The run has no GitLab access, so it opened no issue: the milestone's owners opened the follow-up issue (the pilot's instruction on !701, 2026-11-21; its number did not reach this run, so it is to be filled in here) to run `make docs-screenshots`, commit the WebP files, and add the account-menu image to [The account menu](../guides/desk.md#the-account-menu). Until then the alt texts of the old images describe only what they show.
