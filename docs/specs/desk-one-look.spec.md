# Desk: one look on every page, built from Nuxt UI, with readable text and a light/dark/auto switch

Status: the milestone's spec, run by autopilot. It was created by the first
merge request (R1, issue #621) so later merge requests have a steering log to
write to; the docs issue (R11) completes it. Where this spec and the docs
disagree, the docs win.

## Goal

1. Every Desk page uses the same layout: the same sidebar, the same page header (eyebrow, title, one meta line, actions on the right, tabs below), the same context panel where a page has one, and a breadcrumb only below the top level.
2. Controls and building blocks look and behave the same everywhere because they are Nuxt UI components. A custom component exists only where Nuxt UI has nothing that fits, and the docs say why for each one. The agent guide (`CLAUDE.md`) and a test enforce the rule.
3. All body, meta and header text meets WCAG AA (4.5:1) in light and dark mode. The main buttons have their own colour, apart from errors and counts.
4. The owner can choose Light, Dark or Auto (follow the system). The choice is remembered in that browser.
5. No page scrolls sideways or clips text at 375px, tablet or desktop width, including long branch names, URLs, code and milestone titles. Pages show less noise: readable schedules, dollar amounts with separators, milestone titles instead of bare `%n`, and no empty tabs.

## Decisions (locked)

1. **Nuxt UI first.** Use a Nuxt UI component whenever one fits, even with small compromises in look. Write a custom component only when nothing in Nuxt UI fits. Each custom component is listed in `docs/contributing/web-ui.md` ("UI kit") with one line on why Nuxt UI does not cover it. Rejected: a custom design system on Tailwind (more code to keep), and a second kit (two looks).
2. **The rule lives in the agent guide.** A bullet under "Development" in the root `CLAUDE.md` says Decision 1 in two sentences and links the UI kit section, so every agent that touches `web/` reads it. A new enforced conventions rule, `raw-control`, fails on raw `<button>`, `<select>`, `<input type="checkbox|radio|range">`, `<progress>` and `<textarea>` in `web/src`. Its `skip` list is the files of the documented custom components. Rejected: docs only, because agents drift back to raw controls.
3. **Shell = Nuxt UI dashboard layout.**
   - `UDashboardGroup`, `UDashboardSidebar` (collapsible, with a slideover on phones) and `UDashboardPanel` replace the shell in `App.vue`.
   - `UNavigationMenu` (vertical) replaces the lists in `RailNav.vue`.
   - The phone bottom tab bar stays custom, because Nuxt UI has none. It is listed per Decision 1.
   - The sidebar stores its collapsed state in `localStorage` (no cookie).
   - If one of these components breaks the CSP browser check, keep today's code for that part only, and record it in the steering log and in the custom list.
4. **One page header.**
   - Every view's header is `UPageHeader`: headline = eyebrow (status), title, description = one meta line (cost chip, dates), and the actions in its `links` slot.
   - At most one primary action and one secondary action. Further actions go in a `UDropdownMenu` (⋯).
   - `RouteTabs` sits directly under the header.
   - Overview's own Overview/Stream tabs move into this tabs row.
   - A page states its status once, in the eyebrow. Badge rows and repeated "Running" lines go.
5. **Breadcrumbs.** `UBreadcrumb` replaces `Breadcrumbs.vue` and is shown only on pages below the top level (project, milestone, issue, task, plan, research run). Top-level pages (Overview, Inbox, Metrics, Plans, Research) show none.
6. **One context panel layout.** Pages with a context panel (today the project page) use one shared layout. Below 1180px the panel is closed by default and opens as a `USlideover` from the existing Context button, so the board gets the full width. At 1180px and wider it is a side column, as today.
7. **Shared building blocks.**
   - **Stat tiles:** one component on `UCard` (label, value, optional delta or sub-line) replaces all four tile styles.
   - **Empty states:** `UEmpty`.
   - **Banners** (paused, problem, info): `UAlert`, one colour per tone.
   - **Progress:** `UProgress` replaces `ProgressBar.vue`.
   - **Hints:** `UTooltip` replaces `title=""` on interactive elements.
   - **Selects and buttons:** `USelect`/`USelectMenu` and `UButton` replace the raw ones.
8. **Two filter kinds.**
   - `RouteTabs` when the choice belongs in the URL (page sections).
   - `UTabs` with `variant="pill"`, size `sm`, for local switches (All/Active/Superseded, Open/Handled, tokens/USD).
   - Multi-value filters (project, kind, model, engine) are `USelectMenu`. On phones they sit in a `USlideover` behind a "Filters (n)" button.
   - Rejected: chips (a third style) and `UButtonGroup` toggles (no selected state).
9. **Colour tokens.**
   - Light `--text-3` becomes `#67627f` (5.36:1 on `--bg`, 5.78 on `--surface`, 5.10 on `--surface-2`).
   - Dark `--text-3` becomes `#958fac` (5.10:1 on `--surface-2`).
   - New `--primary: #c4361f` in both modes (white text 5.39:1), with `--ui-primary: var(--primary)`.
   - `--accent` stays for the logo and the brand.
   - Error and count badges use `--ui-error` / the danger tone, never the primary colour.
   - Row names in tables use the text colour, with the link colour only on hover or focus.
   - Rejected: an indigo primary (it would change the brand), and keeping `#e24329` for buttons (fails AA with white text).
10. **Theme switch.**
    - Three choices, Light, Dark and Auto, as `UTabs` with icons (sun, moon, monitor) in the sidebar footer and in the phone menu sheet.
    - Stored in `localStorage` under `desk.theme` (`light`, `dark`, `auto`; missing or unreadable = `auto`), with every read and write in try/catch.
    - `colorScheme.ts` keeps setting only the `dark` class, so the CSP stays the same. Nuxt UI `colorMode` stays off.
    - Auto follows `prefers-color-scheme` live. Light and Dark ignore system changes.
    - The class is set in `main.ts` before mount, as today, so there is no extra script.
11. **Formats.**
    - Money uses `Intl.NumberFormat` with the browser's locale and two decimals (`$2,614.69`). `<$0.01` and `$0` stay.
    - Cron schedules show in words with the time zone ("daily at 07:15 Europe/Berlin", "weekdays at 09:00 UTC", "every 15 minutes"). The raw cron moves into a `UTooltip`. Expressions outside the common forms (fixed minute and hour, day-of-week lists or ranges, `*/n` minutes or hours) show raw.
    - A milestone is shown by title with `%n` as a secondary label, wherever a list shows `%n` alone.
12. **Empty tabs are hidden.** A milestone hides the Retrospective tab until a retrospective exists. Any tab whose content is empty by definition is hidden too. A direct `?tab=` link to a hidden tab shows the default tab.

## Requirements

- **R1** Add the Nuxt-UI-first bullet to the root `CLAUDE.md` and rewrite the "UI kit" section of `docs/contributing/web-ui.md` around Decision 1. Add the `raw-control` rule, report-only until R9.
- **R2** Fix the layout bugs (MilestoneList columns, ContextPanel overflow, `<code>` wrapping in banners, the project queue at 375px). Add fixtures with long titles, branch names, URLs and code so the overflow check catches them. `known-overflow.ts` stays empty.
- **R3** Colour tokens per Decision 9 in `style.css`; error and count badges and table row names move to their tokens. A node test checks 4.5:1 for `--text`, `--text-2` and `--text-3` on `--bg`, `--surface` and `--surface-2`, and for white on `--primary`, in light and dark.
- **R4** Theme switch per Decision 10, with tests for light, dark, auto, throwing storage and ignored system changes when fixed, and an e2e check that the choice survives a reload.
- **R5** Shell per Decision 3: `App.vue` on the dashboard components, `RailNav.vue` on `UNavigationMenu`. The sidebar collapse persists; on phones the menu sheet opens and closes, and Back closes it.
- **R6** Page header and breadcrumbs per Decisions 4 and 5 on every view. Remove the repeated status lines. A node test checks that every routed view renders its header through `UPageHeader`.
- **R7** Context panel per Decision 6, with e2e at 760 and 1180.
- **R8** Shared building blocks per Decision 7. Delete `ProgressBar.vue` and the tile CSS. A node test checks that `ProgressBar` and `title=` on buttons and links are gone.
- **R9** Filters and raw controls per Decision 8. `raw-control` becomes `enforce: true`. The Metrics filters go into a phone sheet.
- **R10** Simplifications per Decisions 11 and 12: `cronText()`, milestone titles, the hidden Retrospective tab, no stray "Follow-ups to settle" link, grouped decision chips in the context panel. `cost()` and the CSV keep their machine format.
- **R11** Docs: `web-ui.md` skeleton, building blocks, filters and theme sections and refreshed screenshots; `design.md` colour tokens and contrast targets; `guides/desk.md` theme switch; this spec's steering log.
- **R12** Charts follow the new tokens: re-picked chart colours (3:1 on `--surface`, none near the primary or danger colour), tooltips from the surface, border and text tokens, and the money format on ticks, tooltips and legends.
- **R13** Untitled plans show their draft's heading (issue #564).

## Constraints

- Production lines per MR stay within the project's `maxMRLines`. Tests, fixtures, generated files and docs do not count.
- The API, the Go view structs and the routes do not change; R13 changes only how a plan view fills its title. `?tab=` links keep working, except that a link to a hidden empty tab falls back per Decision 12.
- The CSP stays as it is. Do not use `useHead` components, Nuxt UI `colorMode`, or inline `<style>`/`<script>`.
- No new npm dependencies.
- The bundle budget (`npm run budget`) passes. Do not raise the budget without a steering log line.
- Existing behaviour stays: ⌘K, `useBackClose` on sheets, `useKeyboardInset` in the composer, `DataList` cards below 760px.
- When a requirement has to be narrowed, open a follow-up issue and log it in the steering log. Every R has an issue or a steering log line.

## Acceptance

- On a fresh browser, the Desk follows the system theme. Picking Dark turns it dark, a reload keeps it dark, and switching the system to light changes nothing. Picking Auto follows the system again.
- On every top-level page, the header has the same shape and no breadcrumb. On a milestone page the breadcrumb leads back to the project, and the status appears once.
- On the bloop project page at desktop width, the *In progress* list shows full milestone titles in one column. At 375px no page scrolls sideways, and long branch names in the context panel wrap.
- With the light theme, meta text and table headers measure at least 4.5:1. The Send and Plan milestone buttons are `#c4361f`, while the Inbox count badge uses the danger colour.
- `npm test` fails when someone adds a raw `<button>` to a view. `CLAUDE.md` names the Nuxt-UI-first rule and links the custom component list.
- The sidebar shows "daily at 07:15 Europe/Berlin" for a research scheduled `15 7 * * *`, and Metrics shows `$2,614.69`. A milestone without a retrospective has no Retrospective tab.
- On Metrics, in both themes, no chart series has the button colour, chart tooltips look like the Desk's other tooltips, and axis amounts show `$2,614.69`.

## Steering log

One line per narrowed requirement: `- YYYY-MM-DD, Rn: what was narrowed and why; follow-up #n`.

- 2026-11-20, R4: the bundle budget's `costs` route cap rises from 126 to 127 kB (measured 126.6, was 125.9). `UTabs` in the rail's theme switch shares `UAvatar`, `useId` and `useDirection` with `USelect`, so Vite splits them out of the Metrics chunks, and the extra chunks cost 0.7 kB gzipped. The initial bundle shrinks (132.0 to 131.9 kB), and nothing the Metrics page loads is new; no follow-up.
- 2026-11-20, R5: the bundle budget's initial cap rises from 132.3 to 134.8 kB (measured 134.7, was 131.9) and the `costs` route cap from 127 to 129 kB (measured 128.9, was 126.6). The shell is on `UDashboardGroup`, `UDashboardSidebar` and `UDashboardPanel`; everything but the group loads lazily, but the sidebar's `localStorage` state (`useStorage`) and the dashboard context land in the shared initial chunks, and the sidebar's lazy overlays (`USlideover`, `UModal`) split shared Reka UI parts out of the Metrics chunks. No dashboard component broke the CSP check, so none fell back to the old shell; no follow-up.
- 2026-11-20, R5: the bundle budget's initial cap rises from 134.8 to 136.0 kB (measured 135.9, was 134.7). `RailNav.vue` on `UNavigationMenu` stays a lazy chunk, but Tailwind's one stylesheet now carries the theme classes of `UNavigationMenu`, `UChip` and `UProgress` (+1.0 kB CSS), and the rail's new Lucide icons join the bundled icon set (+0.2 kB JS). Its scoped CSS, which it replaces, sat in the lazy chunk and saved nothing in the initial set. The `costs` route drops from 128.9 to 128.4 kB. No component broke the CSP check; no follow-up.
- 2026-11-20, R6 (#626): the bundle budget's initial cap rises from 136.0 to 136.4 kB (measured 136.3, was 135.9) and the `costs` route cap from 129 to 130.5 kB (measured 130.3, was 128.4). Tailwind's one stylesheet now carries the theme classes of `UPageHeader` and `UBreadcrumb` (+0.5 kB CSS; `UBreadcrumb` itself loads lazily), and the Metrics chunk carries `UPageHeader`. Download CSV stays a `<details>` menu in the header's links, because `UDropdownMenu` would add 16 kB to the Metrics route; R9 decides on it. Phones show the first and the last crumb on one line, without the `…` that unfolded the middle ones. No follow-up.
- 2026-11-20, R6 (#627): the bundle budget's `costs` route cap rises from 130.5 to 130.7 kB (measured 130.6, was 130.3). The nested page headers put their further actions in a ⋯ `UDropdownMenu` (`HeaderActions.vue`, on the plan, issue and task pages); it shares Reka UI and `UInput` parts with the Metrics page, so Vite regroups the shared chunks the Metrics route loads (+0.3 kB; loading the menu lazily measured worse). The Metrics page loads nothing new, and the initial bundle shrinks from 136.3 to 136.0 kB. No follow-up.
- 2026-11-20, R8 (#628): the bundle budget's `costs` route cap rises from 130.7 to 131.8 kB (measured 131.7, was 130.6). The Metrics tiles, its Pauses, Code and Flow tiles and the Spend tab's figures are one `StatTile` on `UCard`; the tile and the card's theme are a chunk the Overview, issue, task and research pages share, so the Metrics route loads it too. The hand-written tile classes it replaces were Tailwind utilities that stay in the one stylesheet; no follow-up.
- 2026-11-20, R8 (#628): the bundle budget's `costs` route cap rises from 131.8 to 133.1 kB (measured 133.0, was 131.7). The empty states are `UEmpty`, which draws its icon with `UAvatar`; the Metrics page shows one in six places (no calls, Context, Flow, Pauses, budgets and plan usage), so the route loads both. The initial bundle grows from 136.0 to 136.3 kB, within its cap, for the five new Lucide icons of the empty states. No follow-up.
- 2026-11-20, R8 (#629): the bundle budget's `costs` route cap rises from 133.1 to 135.3 kB (measured 135.2, was 133.0). Banners are `UAlert`; the Metrics page shows two (the load error and the Pauses truncation note), so the route loads `UAlert` (+1.8 kB). Loading it lazily measured worse (135.5 kB): the budget counts dynamic imports, and the split adds a chunk. The initial bundle shrinks from 136.3 to 136.2 kB, because the sign-in page loads its one alert lazily. No follow-up.
- 2026-11-20, R9 (#630): the bundle budget's `costs` route cap rises from 135.3 to 176.7 kB (measured 176.6, was 135.2) and the initial cap from 136.4 to 136.8 kB (measured 136.7, was 136.2). Decision 8 makes the value filters `USelectMenu`, which imports Reka UI's combobox and its virtualizer statically (+28 kB; Nuxt UI has no searchable select without them, and the budget counts lazy imports too), the phone's filter sheet is a `USlideover` (+5 kB) and the pills are `UTabs` (+5 kB, *Where it goes*). The initial set grows by `useBackClose`'s query option and a 0.3 kB Reka UI chunk Vite now splits out. No follow-up.
- 2026-11-20, R9 (#630): the Metrics *Group by* select goes into the phone's filter sheet with the value filters, and into their row from 760 px; it is not counted in *Filters (n)*, as it has no *All*. There is no model filter on the page, so the sheet has project, kind and engine. No follow-up.
