# Desk accessibility

How the Desk stays usable by keyboard and screen reader: the target, the
automated gate, and the rules for landmarks, headings, focus, motion,
colour, charts and announcements. The milestone behind it is
[Desk: works by keyboard and screen reader](../specs/desk-accessible.spec.md).
Read this before you add a page or a control; the Nuxt UI rule is in
[Web UI development](web-ui.md#ui-kit).

## Target

WCAG 2.2 level AA. The automated gate is axe-core with the tags `wcag2a`,
`wcag2aa`, `wcag21aa` and `wcag22aa`. Rules outside those tags (the
landmark and `region` best practices among them) are not gated, and no
gated rule is turned off. AAA, conformance claims and screen readers other
than VoiceOver are out of scope.

## The axe gate

`web/e2e/desk.spec.ts` runs axe (`@axe-core/playwright`) inside the route
loop, so every route in `routes` is checked without a second list:

- at 375 px and at the route's widest size (1280 px, or 1180 px for the
  edge routes), in light;
- once more at the widest size in dark (`<route>@1280-dark`), for routes
  without a `colorScheme` of their own.

The same case also checks the outline: one `h1`, and no heading level
skipped, read from the accessibility tree. A violation fails as
`rule id: target selectors`.

**Known failures.** `web/e2e/known-a11y.ts` maps a check that fails today to
the issue that fixes it and the rules it lists:

```ts
'inbox@375': `${R6} R6: color-contrast`,
```

- The key is `"<route>@<width>"` or `"<route>@<width>-dark"`, the check's
  name. `web/e2e/knownKeys.ts` checks the key format, shared with
  `known-overflow.ts` (`web/test/knownKeys.test.mjs`, and again when the
  maps load).
- The value starts with the issue that fixes it (a link to
  `…/issues/<n>` or `#<n>`), then per requirement `R<n>: <rule id>, …`. An
  entry without an issue link or without rule ids stops the checks.
  Every entry also needs a line in the spec's steering log that says why it
  is not fixed.
- A listed check must find exactly the listed rules: a new rule fails it,
  and so does a listed rule that axe no longer finds. Then it runs as
  `test.fail()`, so Playwright fails the run once the check passes: the
  change that fixes a rule removes it, and the entry once it is empty.

Do not add an entry to get a change through: fix the violation. An entry is
for a finding that needs its own issue.

## Landmarks and the skip link

The shell (`App.vue`) has one `header` (the top bar on phones), a `nav`
labelled "Main" (the sidebar), the phone's bottom bar `nav` labelled
"Sections", one `main` with `id="main"`, and the context panel as an
`aside` labelled "Context" (`ContextPanel.vue`, in a sheet below 1180 px).

"Skip to content" is the first focusable element. It is visually hidden
until it has focus, and Enter moves focus to `main` without changing the
URL. It uses classes only, no inline style or script (the CSP).

A page adds no landmark of its own, except a labelled `nav` for a set of
links (RouteTabs is `nav` "View", the problem list `nav` "Problems").

## Headings

- One `h1` per page, in its header (`UPageHeader` or the stream header).
- Page sections are `h2`, cards inside a section `h3`. No level is
  skipped.
- A card title that is not a section is plain text, not a heading.
- Markdown from agents and people (`Markdown.vue`, `markdown.ts`) renders a
  `#` heading as a bold paragraph by default, so user text never adds an
  `h1` or skips a level. Pass `headingLevel` where the text is a document
  of its own (a research report): `#` becomes that level and deeper ones
  follow it, clamped at `h6`.

## Focus

- **Ring.** `:focus-visible` draws a 2 px outline in `--focus`
  (`style.css`), also on Nuxt UI controls: an unlayered rule maps their own
  tinted ring to `--focus`. A forced-colours outline keeps it visible.
- **Return.** Closing a sheet, dialog, menu or the ⌘K palette returns focus
  to the control that opened it. Nuxt UI (Reka UI) does this for its
  overlays; a sheet that also closes on Back (`useBackClose.ts`) keeps it.
  Test it with a keyboard flow when you add an overlay.
- **Route focus.** After an in-app navigation to a new path, focus moves to
  the page's `h1` (`tabindex="-1"`, no ring) once it is rendered, and the
  tab title is "<h1> · Desk" (`useRouteFocus.ts`). A new query or hash on
  the same path (a tab, a filter) keeps focus on its control.
- **Escape** closes the topmost open layer.
- **Order.** The tab order follows the visual order. No positive
  `tabindex`: the conventions check (`positiveTabindex`, `npm test`) fails
  on one.
- **Don't drop focus.** When a control that has focus disappears (an
  answered prompt's options, a composer disabled while it sends), move focus
  to what replaces it. `PromptCard.vue` focuses its "✓ … sending…" line;
  `Composer.vue` gets focus back after a send.

## Reduced motion

One rule in `style.css` under `prefers-reduced-motion: reduce` sets
animations and transitions to `0s` across the Desk. Components add no
motion rule of their own. Chart.js animation is always off. The e2e check
opens a sheet with `reducedMotion: 'reduce'` and expects no transition.

## Not by colour alone

Every status badge and dot has text, visible or visually hidden. Use
`Badge.vue`: with `dot` and no slot text, pass `label`, which is read but
not shown. The conventions rule `silentDots` (`npm test`) fails on a
`<Badge dot>` without text or `label`. Legends and the activity heatmap
carry their values as text too.

## Charts

- Every canvas has `role="img"` and an `aria-label` from `chartData.ts`.
  `summaryText` is the one rule set: the title, the range, the total, the
  largest series, *Other* when it exists, then the unknown or not-metered
  note. An empty chart starts with "No data". `chartSummary`,
  `monthSummary`, `breakdownSummary` and `tokensSummary` build it per chart
  (`web/test/chartData.test.mjs`).
- `ChartTable.vue` shows the same numbers as a table. Its "Show as table"
  button (`aria-pressed`, "Hide table" when open) is the next Tab stop after
  the chart. Hidden, the table is `sr-only`, so a screen reader can always
  reach it.
- Heatmap cells have a label with the date and the count, and the heatmap a
  text summary.

## The announcer

`useAnnounce()` in `web/src/ui/useAnnounce.ts` owns the Desk's two live
regions, polite and assertive, rendered once by `App.vue`. Components call
`announce(text)` or `announce(text, { assertive: true })`; `announceChange`
says an open item's status when it changes, not on the first load.

- **Polite:** a chat reply once it is complete ("Pilot replied: <first
  sentence>", `replyAnnouncement` in `chatView.ts`), never per streamed
  part; run and milestone status changes on the open page ("Task now
  Waiting for you"); Inbox count changes; the page title after a navigation
  to a new page, only when focus could not move to its `h1`.
- **Assertive:** errors only, such as a failed send or a failed answer, and
  the event stream going Offline.
- **Throttle and dedupe** (`announce.ts`, tested with fake timers): at most
  one polite message per two seconds, faster ones wait in order (the newest
  three only); the same text within ten seconds is dropped.
- **Not for toasts.** Nuxt UI toasts have their own live region. Do not
  announce what a toast says.
- **Why not `aria-live` per component:** regions that overlap talk over
  each other, a streamed reply is read word by word, and nobody can test
  what is said. The conventions rule `liveRegions` (`npm test`) fails on
  `aria-live` other than `off` outside the announcer. A `role="status"`
  line next to the control it reports on (a prompt card's "sending…") is
  fine.

## VoiceOver checklist

An agent cannot run VoiceOver, so each flow is an e2e test driven by
keyboard only (`test.describe('screen reader flows')` in `desk.spec.ts`,
at 1280 and 375 px, Chromium). At each step the test checks the focused
element's role and name, the ARIA snapshot of the region and what the
announcer says. Each flow starts with Tab to "Skip to content" and Enter.

The manual run in macOS Safari and iOS Safari with VoiceOver is pending: a
follow-up issue for it is still to be opened (the run that wrote the tests
cannot open issues, see the spec's steering log). Run the same steps there
and record what VoiceOver says.

1. **Answer a problem card in Needs you.** Open a milestone's *Needs you*
   tab, Tab to the recommended option, Enter.
   Automated: passes. The h1, the "View" tabs with *Needs you* current and
   the "Problems" list are read; the option is a button with its label;
   after Enter, focus is on the status line "✓ <option> · sending…" and the
   thread shows the answer as sent; a refused answer is said assertively
   and focus goes back to the options.
2. **Read a milestone page and switch tabs.** Open a milestone, Tab to
   *Decisions*, Enter, Tab on.
   Automated: passes. The tabs are links in `nav` "View" (Tab moves between
   them, not the arrow keys); after Enter, focus stays on the tab, which is
   current; the panel's search, filter tabs and headings follow; nothing
   is announced.
3. **Send a message to the pilot and hear the reply.** Tab to *Message* on
   the *Pilot* tab, type, Ctrl/Cmd+Enter.
   Automated: passes. The box is not marked invalid; focus stays in it
   while and after it sends; nothing is said while the reply streams, then
   "Pilot replied: <first sentence>" once.
4. **Read Metrics, including a chart as a table.** Open Metrics, Tab to
   "Show as table" under *Spend over time*, Enter, Tab.
   Automated: passes. Each chart reads as one sentence; Enter turns the
   button into "Hide table" (pressed) and shows the table with its column
   headers; the next Tab enters the table's scroll box.

What the flows found and fixed: an empty composer was marked invalid;
the first message in an empty chat dropped focus; answering a prompt card
by keyboard dropped focus, and a failed answer was silent; a tab switch
announced the page title again.

## Next

- [Web UI development](web-ui.md): the checks, the UI kit and its
  components.
- [Desk: works by keyboard and screen reader](../specs/desk-accessible.spec.md):
  the decisions and the steering log.
