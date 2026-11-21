# Desk: works by keyboard and screen reader, with an accessibility check on every page

Status: archived. The milestone's spec, run by autopilot. It was created by
the first merge request (R1, issue #636), so later merge requests have a
steering log to write to; the docs requirement (R10) completes it. Where this
spec and the docs disagree, the docs win: the axe check and the known list are
in [web-ui.md](../contributing/web-ui.md#checks).

## Goal

1. Every Desk page can be used by keyboard alone: a skip link, a visible focus ring, focus that goes back where it came from when a sheet or dialog closes, and Escape that closes what is open.
2. A screen reader user knows where they are and what changed: labelled landmarks, one `h1` per page with headings in order, the new page announced after navigation, and chat replies, run status and the Inbox count announced without reading streamed text word by word.
3. Nothing depends on colour alone. Every chart has a text summary and a table.
4. An automated accessibility check (axe-core, WCAG 2.2 AA) runs on every page in the browser tests and fails the pipeline on new violations, the same way the overflow check does.
5. Agents and tests find Desk controls by role and name, so `getByRole` and accessibility-tree tools work on every page.

## Background

File refs are at origin/main `61aa49d`, 2026-11-18. This milestone runs after "Desk: one look on every page, built from Nuxt UI, with readable text and a light/dark/auto switch" (%36) by milestone order. %36 replaces the shell with Nuxt UI's dashboard layout, puts every page header on `UPageHeader`, adds `--primary`, fixes text contrast and adds the theme switch. Refs below to `App.vue`, `RailNav.vue` and the view headers are from before %36. Check them against main when this milestone is planned.

What exists:

- **Attributes.** 138 `aria-*` attributes and 62 `role=` across the `.vue` files. Icon-only `UButton`s all carry a label.
- **Landmarks.**
  - `App.vue:319` has a `<main>` with no id and no skip link before it.
  - The rail's `<nav>` (`components/RailNav.vue:79`) has no label.
  - The phone bottom bar is `<nav aria-label="Sections">` (`App.vue:240`).
- **Headings.** 14 `h1`, 27 `h2`, 39 `h3` and 8 `h4` across views and components, with no check on order. %36 brings one `h1` per page through `UPageHeader`.
- **Focus.**
  - `style.css:284` sets a `:focus-visible` ring from `--focus`.
  - Sheets close on Back (`web/src/ui/useBackClose.ts`). Whether focus goes back to the opener is not tested.
  - After a route change, focus stays on the clicked link and nothing is announced.
- **Live regions.**
  - Only one: `views/Research.vue:162` (`aria-live="polite"` on the pending card).
  - Chat replies (`components/Conversation.vue`, `ChatThread.vue`, `TaskChat.vue`, `PlanChat.vue`), run status on task and milestone pages, the Inbox count and the event stream's Offline state are silent to a screen reader.
  - Toasts come from Nuxt UI (`UApp` in `App.vue`).
- **Charts.**
  - `chartData.ts:233` `chartSummary` gives the stacked cost chart's canvas an `aria-label` (`ui/charts/StackedBars.vue:17`, `views/Costs.vue:155`).
  - `HorizontalBars.vue` and `LineCompare.vue` have no summary.
  - `ChartTable.vue` shows the numbers as a table.
- **Motion.** Chart.js animation is off. `prefers-reduced-motion` is honoured only in `components/DecisionCard.vue:98`.
- **Browser checks.**
  - `web/e2e/desk.spec.ts` runs every route (`routes`, line 86) at 375, 768 and 1280 (`widths`, line 17) for overflow, hit areas and the CSP.
  - `web/e2e/known-overflow.ts` lists known failures as `test.fail()`.
  - No accessibility rules run. `package.json` dev dependencies are Playwright, Vite, vue-tsc and types only.

## Decisions (locked)

1. **Target WCAG 2.2 level AA.** The automated gate is axe-core with the tags `wcag2a`, `wcag2aa`, `wcag21aa` and `wcag22aa`. Rules outside those tags are not gated. Rejected: AAA (contrast 7:1 would redo %36's tokens), and Lighthouse (whole-page scores, no per-rule failures).
2. **axe runs in the existing route loop.** `desk.spec.ts` runs axe on each route at 375 and 1280 in light, and at 1280 in dark. Known violations go in a new `web/e2e/known-a11y.ts` map shaped like `known-overflow.ts` (`"<route>@<width>[-dark]"` → issue link) and run as `test.fail()`. The map is empty at the end of the milestone, apart from entries with an open issue and a steering log line. Rejected: a separate a11y spec file (a second route list to keep in step).
3. **Landmarks and skip link.**
   - One `header`, a `nav` labelled "Main" (the sidebar), the bottom bar `nav` labelled "Sections", one `main` with `id="main"`, and the context panel as an `aside` labelled "Context".
   - A "Skip to content" link is the first focusable element. It is visible on focus and moves focus to `main`.
4. **Route change.** After an in-app navigation, focus moves to the page's `h1` (`tabindex="-1"`, no visible ring on programmatic focus) and the document title is set to "<page title> · Desk". Navigation inside a page (tabs that change `?tab=`) keeps focus on the tab.
5. **Headings.** One `h1` per page, in the header. Page sections are `h2`, cards inside a section `h3`. No level is skipped. Card titles that are not sections use plain text, not a heading.
6. **Focus.**
   - Every focusable element shows the `--focus` ring on `:focus-visible`, including Nuxt UI components (their ring maps to `--focus`).
   - Closing a sheet, dialog, menu or the ⌘K palette returns focus to the control that opened it. Escape closes the topmost open layer.
   - The tab order follows the visual order. No positive `tabindex`.
7. **One announcer.** A composable `useAnnounce()` in `web/src/ui/` owns two visually hidden live regions: polite and assertive. Rules:
   - Polite: a chat reply when it is complete ("Pilot replied: <first sentence>"), never per streamed token; run and milestone status changes on the open page ("Task now Waiting for you"); Inbox count changes; the page title after navigation.
   - Assertive: errors only (send failed, event stream Offline).
   - At most one polite message per two seconds. The same text within ten seconds is dropped.
   - Nuxt UI toasts keep their own live region and are not announced twice.
   Rejected: `aria-live` on each component (overlapping, chatty, hard to test).
8. **Not by colour alone.** Every status badge and dot carries visible text or a visually hidden text. Tones never stand alone, including chart legends and the activity heatmap.
9. **Charts.**
   - Every chart canvas has `role="img"` and an `aria-label` summary from `chartData.ts` (extend `chartSummary` to bars and lines).
   - The `ChartTable` with the same numbers is one keyboard step away ("Show as table").
   - The heatmap cells have a label with date and count.
10. **Reduced motion.** One rule in `style.css` under `prefers-reduced-motion: reduce` turns off transitions and animations across the Desk. The per-component rule in `DecisionCard.vue` goes.
11. **Manual pass with VoiceOver** in macOS Safari and iOS Safari, on four flows:
    1. answer a problem card in Needs you;
    2. read a milestone page and switch tabs;
    3. send a message to the pilot and hear the reply;
    4. read Metrics, including a chart as a table.
    Findings are fixed in this milestone or filed as issues with a steering log line. Other screen readers (NVDA, JAWS, TalkBack) are not tested.

## Requirements

### Checks

- **R1** Add `@axe-core/playwright` (exact version, released at least two weeks before), and run axe per Decision 2 in `desk.spec.ts`. Add `web/e2e/known-a11y.ts` and fill it with today's violations, each with an issue, so the pipeline stays green. Each later requirement removes its entries. Tests: the axe check itself. A unit test for the known-list key format, shared with `known-overflow.ts`.

### Web

- **R2** Landmarks, skip link, route focus and document title per Decisions 3 and 4. Tests: e2e: Tab from page load focuses the skip link first, and Enter moves focus to `main`. After clicking a sidebar link, focus is on the new page's `h1` and `document.title` matches. Landmark roles and names via `getByRole`.
- **R3** Headings per Decision 5 on every view and shared component. Tests: e2e per route, exactly one `h1` and no level jump (read from the accessibility tree).
- **R4** Focus per Decision 6: ring tokens for Nuxt UI, focus return for every sheet, dialog, menu and the palette, Escape order. Tests: e2e keyboard flows at 1280 and 375:
  - open ⌘K, pick a milestone, land on its `h1`;
  - open the context sheet, close it with Escape, focus is back on the Context button;
  - open the phone menu sheet, close it with Back, focus is back on the menu button.
- **R5** `useAnnounce()` per Decision 7, and wire it into the chat threads, task and milestone status, the Inbox count and the event stream's Offline state. Remove the ad-hoc `aria-live` in `Research.vue`. Tests: node tests for the throttle and dedupe with fake timers. e2e: a streamed fixture reply produces one polite message after it ends, and Offline produces one assertive message.
- **R6** Not-by-colour and labels per Decision 8: badges, dots, heatmap, legends, form fields (every input with a visible label or `aria-label`, errors tied with `aria-describedby` through `UFormField`). Tests: the axe check, plus a node conventions rule (report-only, then enforced) that flags `Badge` with `dot` and no text.
- **R7** Charts per Decision 9. Tests: `chartData.test.mjs` cases for the bar and line summaries (empty data, one series, many series with Other). e2e: every chart canvas on Metrics has `role="img"` and a non-empty label, and "Show as table" is reachable by Tab.
- **R8** Reduced motion per Decision 10. Tests: e2e with `reducedMotion: 'reduce'`: a sheet opens with no transition (computed `transition-duration` is 0s).
- **R9** VoiceOver pass per Decision 11. Fix what fits in this milestone, and file the rest. Record the four flows and their results in the docs (R10).

### Docs

- **R10** Docs:
  - New `docs/contributing/accessibility.md`: target, the axe gate and the known list, landmarks, headings, focus, the announcer and when to use it, the VoiceOver checklist. Listed in `docs/contributing/README.md` and `docs/README.md`.
  - `docs/contributing/web-ui.md`: a link from the UI kit and charts sections.
  - Root `CLAUDE.md`: the Nuxt-UI-first bullet from %36 gains "and keep the axe check green (`docs/contributing/accessibility.md`)".
  - `docs/specs/desk-accessible.spec.md` with a steering log, listed in `docs/specs/README.md`.

## Constraints

- Production lines per MR stay within the project's `maxMRLines`. Tests, fixtures and docs do not count.
- The only new dependency is `@axe-core/playwright`, a dev dependency pinned exactly. No runtime dependencies. The bundle budget passes.
- The CSP stays as it is. The announcer and skip link use no inline styles or scripts.
- Follow %36's Nuxt-UI-first rule: use Nuxt UI's accessible parts (Reka UI underneath) before writing ARIA by hand.
- The e2e job's run time grows by at most 50%. If axe makes it slower, drop dark mode to one route per page kind and log it in the steering log.
- No API, Go view or route changes.
- Run `make ui` and the e2e browser checks. CI and the drift job must be green.
- When a requirement has to be narrowed, open a follow-up issue and log it in the spec's steering log.

## Non-goals

- WCAG AAA, and conformance claims or an accessibility statement.
- Screen readers other than VoiceOver.
- Translations and other languages.
- A keyboard shortcut help sheet or new shortcuts beyond what Escape and focus return need. That is the tempting next step.
- Windows forced-colours mode, beyond not hiding focus rings or text.
- Visual redesign. Looks stay as %36 left them.

## Suggested order

R1 → R2 → R3 → R4 → R5 → R6, R7, R8 → R9 → R10.

## Acceptance

- The pipeline runs axe on every Desk route. Adding an icon-only `UButton` without a label to a view fails it, and `known-a11y.ts` has no entries without an open issue.
- On any page, the first Tab shows "Skip to content", and Enter moves to the main content. After picking a project in the sidebar, VoiceOver reads the project's title.
- Opening the context sheet on a project page and pressing Escape closes it, with focus back on the Context button.
- In the pilot chat, VoiceOver says "Pilot replied" once when the reply has finished streaming, not while it streams. When the event stream drops, VoiceOver says "Offline" once.
- On Metrics, each chart reads as one sentence, and the same numbers are one Tab away as a table.
- With "Reduce motion" on in macOS, sheets and menus open without animation.
- `docs/contributing/accessibility.md` lists the four VoiceOver flows with their results.

## Steering log

One line per narrowed requirement: `- YYYY-MM-DD, Rn: what was narrowed and why; follow-up #n`.

- 2026-11-20, R1 (#636): axe finds two rules that no requirement covers: `color-contrast` (tab labels, muted badges, the phone bar's current item, faded chat notes, in light and dark, 79 checks) and `target-size` (the activity heatmap's day cells on the project stream, a link in the milestone's issue list at 375 px, 4 checks). The run that filled `known-a11y.ts` could not open issues, so their entries name R6 (Decision 8 changes the same tones, badges and heatmap). The issue numbers in `known-a11y.ts` (R4 #639, R6 #641) were inferred from the planner's order, not read from GitLab. Follow-up: check those numbers, and open an issue for `color-contrast` and `target-size` if R6 does not take them, then point their entries to it.
- 2026-11-20, R2 (#637): the skip link and the route focus and tab title (`useRouteFocus`, in the shell because it must run from the first navigation) add 1.1 kB to the initial bundle (JS +0.8, CSS +0.3 for the skip link's focus classes), measured 140.6 kB against a cap of 139.6. The cap rises by the measured amount to 140.7 (constraint "the bundle budget passes", step 5 of #637). R2 adds no entries to remove from `known-a11y.ts`: the landmark rules (`region`, `landmark-*`, `bypass`) are axe best practices outside the gated WCAG tags, and `duplicate-id` was not found.
- 2026-11-20, R4 and R8 (#639): the focus-ring colour rules for Nuxt UI, the forced-colours outline and the reduced-motion rule (CSS only) take the initial bundle to 140.8 kB, over its cap of 140.7. The cap rises by the measured amount to 140.9 (constraint "the bundle budget passes").
- 2026-11-20, R5 (#640): Decision 7 announces the page title after navigation, but Decision 4 already moves focus to the `h1`, which a screen reader reads. The title is announced only when route focus found no `h1` to focus (D640-P1), so it is not spoken twice. The announcer and its live regions (in the shell, like the route focus), and the route focus kept on the h1 when the closing rail sheet gives focus back to its opener, take the initial bundle to 141.6 kB, over its cap of 140.9. The cap rises by the measured amount to 141.7 (constraint "the bundle budget passes").
- 2026-11-20, R6 (#641): R6 fixes `link-in-text-block` (links in running text are underlined) and names the composer, chat prompt, pilot conversation and ⌘K search fields. It does not take `color-contrast` or `target-size`: neither is status by colour alone, and fixing them changes tokens and hit areas, while R6 keeps screenshots unchanged apart from labels. Their entries in `known-a11y.ts` still name #641, because the run could not open issues. No `Badge` with a dot lacked text, so the `silent-dot` rule was enforced from the start. Follow-up: open an issue for `color-contrast` and `target-size` and point their entries to it.
- 2026-11-20, R9 (#643): the VoiceOver pass is automated as accessibility-tree flow tests (`screen reader flows` in `web/e2e/desk.spec.ts`, the four flows at 1280 and 375 px, keyboard only, with focus, ARIA snapshots and the announcer checked per step); the manual run in macOS and iOS Safari stays open. The run could not open issues, so its follow-up issue is still to be opened, with the checklist in `docs/contributing/accessibility.md`. The flows found four small findings, all fixed in #643: an empty composer was `aria-invalid` (Vue casts `error=""` to `true` for `UFormField`), the first message in an empty chat unmounted the composer and dropped focus, answering a prompt card by keyboard dropped focus and a failed answer was silent, and a tab switch announced the page title again. The milestone tabs are links in a `nav` (RouteTabs), so the flow moves between them with Tab, not the arrow keys. Follow-up: open the manual VoiceOver issue and point the checklist to it.
- 2026-11-20, R10 (#643): final pass on `web/e2e/known-a11y.ts`. It still holds 82 entries, all `color-contrast` or `target-size` (R1's line above), naming #641 (R6), which did not take them (R6's line above). No other rule is listed, so every requirement's own entries are gone. The entries break the milestone's rule of an open issue per entry: the runs could not open issues. Follow-up: open one issue for `color-contrast` (token contrast in light and dark) and `target-size` (heatmap cells, a link at 375 px), point the entries' `R6` constant to it, and fix them there; they change tokens and hit areas, which R9's "small findings" do not cover.
