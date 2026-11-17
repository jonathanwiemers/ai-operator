# Web UI development

For people who work on the Desk itself: how to run the web UI locally, the
checks `make ui` and CI run, the bundle budget, the UI kit and the
conventions its components follow, and what is planned next. The design
behind it (the API, access rules, styles) is in [design.md](design.md). For
using the Desk, see [The Desk](../guides/desk.md) and
[Steer from the Desk](../guides/steer-from-the-desk.md); what each view
does under the hood is in [Desk behaviour in detail](#desk-behaviour-in-detail).

## Development

```sh
make ts-types  # web/src/generated/*.ts from the Go views (tygo.yaml)
make ui        # ts-types (with Go) + npm ci + tests + type-check + build into web/dist/app (embedded by go build) + bundle budget
make ui-e2e    # make ui, then the browser checks (see Checks)
make run-ui    # UI without login on http://127.0.0.1:9080, against your kubeconfig
cd web && npm run dev   # live reload on http://localhost:5173, API proxied to :9080
```

The Go server is in `internal/ui` (`cmd/ui` wires it), the app in `web/src`.
Without `make ui` the binary serves only the API.
`make ui` also runs `npm test` (`node --test` in `web/test`, e.g. the markdown
renderer). `web/src` uses no `v-html` and no `innerHTML`: keep it that way.

**View types.** The TypeScript types of the API's views are generated from
the Go structs with [tygo](https://github.com/gzuidhof/tygo) (`go tool
tygo`, pinned in `go.mod`): `internal/ui` into `web/src/generated/views.ts`,
the CRD types the views embed (`api/v1alpha1`) into
`web/src/generated/api.ts`, and the store types the views carry or embed
(`internal/research/store`: `SpendTotals`, `PauseHistory`, `ChatMessage`,
and from `milestone_rows.go` `MilestoneRow`, `IssueFigures` and
`FollowUpRow`) into `web/src/generated/store.ts`. A view struct exists only
when it differs from the store type by composition or by what it hides,
and then it embeds the store type (`tstype:",extends"`) instead of copying
its fields: `ChatMessageView` is the store's message plus `redacted`,
`FollowUpView` the follow-up plus its target's link, `IssueFiguresView`
the figures plus the agent time in seconds and the cost chip, and the
milestone list's `MilestoneRow` the snapshot's row plus the live state,
progress and cost (a live row fills only the snapshot fields it has). A
store type the web gets has JSON tags, `json:"-"` on what the web never
gets or gets in the view's shape (a chat message's client key and expiry,
a milestone's spend). tygo has no per-type exclusion, so such types live
in files of their own that `tygo.yaml` lists. The store keeps enums as plain strings,
without importing `api/v1alpha1`: a `tstype` tag names the union
(`aiv1.PauseKind | 'unknown'`). A cost that may be unknown is a `store.Money`: the
amount with 2 decimals in the JSON, `""` when unknown, never `"0.00"`
(`Money` in TypeScript is a string). All three files are committed. After
changing a view struct, run `make ts-types`; the CI `drift` job runs it and fails when the
files change. `tygo.yaml` lists the files with views (server-only types such
as the GitLab cookie and the log source live in other files) and the type
mappings; a Go type without a mapping becomes `Unmapped`, which fails the
type-check. String fields with a closed set of values and the open shapes
(`map[string]any`, `[]any`) get a `tstype` tag that names a union or type in
`web/src/types.ts`, which holds only such web-only types and re-exports the
generated ones; it declares no response. Every success
response is a named struct (`ChatPage`, `ResearchResponse`, `InboxResponse`,
…) in the file of its handler, so it is generated too; a nullable field is a
pointer with a `tstype:"T | null,required"` tag, so it stays `null` in the
JSON and in TypeScript. Every error is an `ErrorResponse`
(`error`, plus `field`, `login`, `unknown` or `taken` where set), written
with `writeError` or `writeErrorResponse`, never as a map. Enum types whose constants are not
named after the type (e.g. `PauseKind`, `PauseOwner`) come out as `string`.

**Client.** The app calls the API only through `web/src/client.ts`: one
function per route, named verb + resource (`getMe`, `getFeatures`,
`getInbox`, `logout`, …), with path parameters as arguments that the
function encodes, and a return type from `generated/views.ts`. A function
unwraps an envelope only where every caller wants the inner list, and its
comment says so (`getFeatures` returns `Feature[]`; `getInbox` keeps
`InboxResponse`). The fetch helpers (`get`, `post`, `del`) and their errors
(`NotFoundError`, `LoginRequiredError` on 401, which signs in again by
itself through `signInAgain.ts` or else sets `session.loginRequired`, and `ApiError` with the server's message) live
there too: the helpers are private to the file, the errors are imported
from it. A request body that is not a view gets its type next to the
function (`ResearchCreate`). The two server-sent event
streams are not fetches: `eventsURL()` and `transcriptURL(namespace, name,
task)` give the URL for the `EventSource`, so their paths live in the
client as well. `getStream` takes the caller's `URLSearchParams` (project,
milestone, issue, kinds, cursors), since the stream's filters are the
views'. `useResource` (`live.ts`) takes a client function,
`useResource(() => getInbox, kinds)`, or a closure over its arguments,
`useResource(() => { const { namespace, name, task } = props; return () =>
getTask(namespace, name, task) })`: the source reads the arguments itself,
outside the closure, so a change of them loads again. `api.ts` keeps the
session state and helpers around it (`loginURL`, `chatPostError`), no
fetches.

**Research pages with seeded data.** The research pages need a research
database and `AgentResearch` objects. For development, a local Postgres
with seeded results stands in for real runs:

```sh
make test-db          # Postgres 17 in docker on 127.0.0.1:55432
make research-seed    # database ai_operator_dev: migrated as research_owner
                      # (granting research_reader), seeded from hack/research-seed.sql
kubectl apply -f hack/research-dev.yaml   # the matching AgentResearches (CRDs: make install)
export AI_OPERATOR_UI_RESEARCH_DB_USER=research_reader AI_OPERATOR_UI_RESEARCH_DB_PASSWORD=reader
# uncomment the research section of hack/ui-dev.yaml, then
make run-ui
```

The seed (namespace `research-dev`) has:

* `morning-news`: two news flash runs that share two URLs, so the latest
  marks only its other items *new*;
* `heat-pumps`: a deep report with every block type;
* `hostile`: a stored result with raw HTML and `javascript:` links, as if
  written behind the validator's back. Its page must show *Result withheld*.
* agent calls of project `dev` for the [Spend](web-ui.md#spend-tab-in-detail) tab: milestone 4
  with its spec, pilot and retro calls, #77 over two runs (an opus step with
  a haiku subagent and a context series, a resumed step, a cursor review
  with an estimated cost, a cursor fix without a cost), #78 with a run from
  before token tracking, and a chat turn without a milestone. The tab is on
  the stream of an AgentProject `dev` in `research-dev`
  (`…/projects/research-dev/dev?tab=spend`).
* Desk milestones for the dashboard and the milestone pages (snapshot rows
  in `desk_milestones`): in project `dev` two done ones (`%3`, old and
  without agent calls, so it shows no spend, and `%4` with a retrospective
  and three follow-ups: open, opened as an issue, dismissed) and `%5`
  (running, no active issue); in project `web` `%2` running (5 issues, 2
  merged) and `%3` paused for #41, with run, merge and pause events and
  agent calls, so wall, agent and paused time and cost show. The lists
  need AgentProjects `dev` and `web` in `research-dev` (the operator need
  not run). A milestone's state in work comes from its live tasks, and the
  seed has none: *Recently done* lists `%3` and `%4`, while the open ones
  stay out of *In progress* (a snapshot row without tasks is in no work)
  and open by URL from the snapshot as `planned`
  (`…/projects/research-dev/web/milestones/2`). Running, paused and
  waiting rows, the strip's counts and *Working now* need spec tasks with
  autopilot status, e.g. an operator on a kind cluster.

The data is dated to the latest 08:00 UTC, so the pages look fresh, and
`make research-seed` replaces it when run again. Apply
`hack/research-dev.yaml` only where the operator does not run, since it
would start runs for these researches. The UI logs in as the reader, so the
pages go through the same SELECT-only grants as in the cluster. The
passwords are fixed (`owner`, `reader`, `chat`) and meant for this loopback
database only.

**Desk chat.** `make research-seed` also creates the chat writer
`research_chat_writer`. With the research section uncommented,
`export AI_OPERATOR_UI_CHAT_DB_USER=research_chat_writer
AI_OPERATOR_UI_CHAT_DB_PASSWORD=chat` turns on the chat section of task
pages; posts go through the same row-level security as in the cluster. No
operator reads this database, so posted messages stay *waiting*: insert
agent rows (`role = 'agent'`, `reply_to` the user row) as `postgres` to
see replies.

**A one-off's live states without an operator.** Start a one-off from the
form (uncomment `namespace` and `allowCreate` in `hack/ui-dev.yaml`; the
form needs an AgentProject with `spec.research.writerSecret` in
`research-dev`), then
patch its status by hand to walk through the phases:

```sh
kubectl -n research-dev patch agentresearch q-… --subresource=status --type=merge \
  -p '{"status":{"phase":"Waiting","waiting":{"engine":"claude","until":"2026-10-29T14:00:00Z"}}}'
kubectl -n research-dev patch agentresearch q-… --subresource=status --type=merge \
  -p '{"status":{"phase":"Running","waiting":null,"run":{"startedAt":"2026-10-29T14:00:00Z","engine":"claude","model":"claude-opus-5-5","reason":"chosen: claude/claude-opus-5-5"}}}'
```

## Checks

Playwright checks the built Desk in Chromium (`web/e2e`). It runs every
route at 375×812, 768×1024 and 1280×800 px.

```sh
make ui-e2e                  # make ui, install Chromium, npm run e2e
cd web && npm run e2e        # against the current web/dist/app
cd web && npx playwright test -g inbox   # one route
```

Chromium needs system libraries and fonts. Install them once with
`npx playwright install-deps chromium` (as root). Without fonts, text has no
width and every check fails as "hidden". `npm run e2e` type-checks
`web/e2e` with `web/e2e/tsconfig.json` first.

How it works:

- **Server.** `web/e2e/serve.mjs` is a static server with no dependencies.
  It serves `web/dist/app` like the Go server does: files as they are, any
  other path as `index.html`. It sends the same security headers. The CSP
  comes from `web/e2e/csp.txt`, and the Go test
  `TestCSPMatchesBrowserChecks` fails when that file and
  `contentSecurityPolicy` differ. Change both together.
- **Fixtures.** The browser never reaches an API. `page.route` answers
  `/api/v1/*` from `web/e2e/fixtures`, TypeScript values typed with
  `web/src/types.ts` and the generated views, so a changed view breaks the
  type-check. There is one module per API area (`overview.ts`, `rail.ts`,
  `session.ts`, `stream.ts` for the project, milestone and issue pages,
  `task.ts`, `spend.ts`, `research.ts`); `fixtures/index.ts` maps each path
  to its answer, or to a function of the query. They describe one project,
  `team-a/shop`: milestone %14 is paused on #281's question (release rows,
  prompts, companions, pauses, a retrospective), %12 is running, and
  `shop-281-implement` is the task page's case. Text written by agents and
  people carries markdown (bold, lists, links). A path without a fixture answers 404 and fails the
  check, naming the path. `/api/v1/events` answers an empty event stream.
  The browser's clock is fixed at 2026-11-10 12:00 UTC.
- **Checks.** For each route and width (`web/e2e/desk.spec.ts`):
  - the page and the content pane (`main` in `App.vue`) do not scroll
    sideways (`scrollWidth` equals `clientWidth`)
  - no CSP violation (`securitypolicyviolation` events and CSP console
    errors)
  - no request to another origin (such requests are also aborted)
  - no uncaught error

  Every named route has a case; routes with tabs have one
  per tab (`stream-milestone-pilot`), and the research page one per view
  (`research`, `research-report`), and the task and issue pages one per
  task tab (`task-details`, `issue-chat`). To check a new route, add it to `routes` with a `ready` condition and add
  the fixtures its requests need. The checks run with the system in light
  mode; `overview-dark` sets `colorScheme: 'dark'` for dark screenshots, and
  one more check switches the scheme on an open page and expects the dark
  tokens and back ([design.md](design.md#desk-styles)).

  The phone shell ([design.md](design.md#desk-styles), *Phones*) has
  checks of its own at 375 px: the mobile bar stays under 70 px high on Not
  found and on an empty research list, and the inputs of the task's Chat
  tab (the chat composer's among them) and the stream search compute to at
  least 16 px, while the composer and the search stay under 16 px at
  1280 px. On the overview, the Inbox and the task page's Overview, Chat and
  Details tabs no visible
  `button`, `[role=button]` or `input` is under 44 px high
  (exceptions in the check's `hitAllowlist`, with a reason), and at
  1280 px a plain `button` stays under 44 px. The `bottom bar …` cases check the
  [bottom bar](#navigation): the count, each item's 44 px, that each link
  navigates and gets `aria-current` and *Projects* opens the sheet; no
  *Research* with a features answer without it; that the issue page's end
  is above the bar once scrolled down; and that the bar hides while the
  task composer has the focus (`bottom-bar@375.png`,
  `bottom-bar-end@375.png`, `bottom-bar-typing@375.png`). `rail sheet stays
  the aside` checks there is no bar at 768 and 1280 px.

  The `task tabs …` cases open the task and the issue page at 375 and
  1280 px: the status line comes before the tabs, shows phase, step,
  result and activity and is no wider than its box (at 375 px the long
  step is clamped and opens on tap), *Overview* is current with the open
  questions' count and the
  run summary as its first section, each other tab is one tap away and
  sets `?tab=`, and `#live-transcript` without `?tab=` opens *Live*
  (`task-tabs-task@375.png`, `task-tabs-issue@1280.png`).
- **Known overflow.** `web/e2e/known-overflow.ts` lists the
  `route@width` cases that scroll sideways today, with the measured px.
  They run as expected failures (`test.fail()`). A slice that fixes one
  removes its entry: Playwright fails a listed case that passes, and an
  unlisted case that overflows.
- **Screenshots.** Each check takes a full-page screenshot to
  `web/e2e-results/screenshots/<route>@<width>.png`, even when it fails.
  The HTML report is in `web/e2e-results/report`, and traces of failed
  checks are in `web/e2e-results/output`.

  In CI the `ui-e2e` job keeps `web/e2e-results/` for two weeks, also when
  it fails ("Desk screenshots" on the merge request). UI merge requests
  link the 375 px and 1280 px screenshots.

### Docs screenshots

The screenshots in these docs and the README
(`docs/assets/screenshots/*.webp`) come from the same fixtures:

```sh
make docs-screenshots        # make ui, then render and convert (needs cwebp)
```

`web/e2e/docs.shots.ts` (config `web/playwright.docs.config.ts`, not part of
`npm run e2e`) opens each page at 1440×900 px with device scale 2 and the
clock fixed at the fixtures' `now`, saves PNGs to `web/e2e-results/docs`,
and `cwebp` turns them into 1920 px wide WebP. It answers the API like the
checks do and renames the fixtures' user and long test project to `sam`
and `storefront`, so the docs show plausible names. Everything is invented:
no real projects, users or tokens. Re-run it after a visible Desk change and
commit the WebP files that changed.

## Bundle budget

The initial JS plus CSS of the Desk is at most 250 kB gzipped (zlib level 9).
Initial is what a cold load fetches before the first route renders: the entry
`<script type=module>`, its `modulepreload`s and the stylesheets linked from
`web/dist/app/index.html`. Every routed view is lazy-loaded
(`() => import('./views/X.vue')` in `web/src/main.ts`) and is its own chunk
in `web/dist/app/assets`. Only `App.vue` (the shell) and `SignIn.vue` are in
the initial bundle, with what they import.

`make ui` runs `npm run budget` (`web/scripts/bundle-budget.mjs`) after the
build, so the CI `ui` job enforces it. It prints
`initial: X kB (JS a, CSS b), cap: C kB` and fails above `capKB` in
`web/bundle-budget.json`.

- 2026-11-11, before Nuxt UI: 51.9 kB (JS 48.7, CSS 3.2).
- 2026-11-11, with Nuxt UI and Tailwind (`measuredKB`): 101.8 kB (JS 89.6,
  CSS 12.2). The biggest parts are the Vue runtime, `<UApp>`'s providers
  (Reka UI, tailwind-variants) and the bundled icons; the cap is
  min(250, 101.8 × 1.3) = 132.3 kB.
- 2026-11-11, with the rail sheet (#312): 120.5 kB (JS 106.0, CSS 14.5).
  `USlideover` is loaded on phones only (`defineAsyncComponent` in
  `App.vue`); imported statically it made 136.8 kB. Even lazy, the parts
  of the Vue runtime and `@vueuse/core` it uses (Teleport, Transition)
  land in the shared initial chunks, since a module is not split across
  chunks. Later sheets and `UModal` reuse them.
- 2026-11-13, with the version line, the Costs tab and the ⌘K shell wiring
  (Desk polish R3 to R7): 132.3 kB (JS 113.0, CSS 19.2), up from 129.3 kB.
  The palette itself (`SearchPalette.vue`, `UCommandPalette`) is lazy and not
  in it; the cap stays at 132.3 kB.
- The cap only drops. A change that breaks it is reported, not answered by
  raising it.

**Route chunks.** A route listed under `routes` in `bundle-budget.json` is
checked on its own: the JS and CSS its view loads beyond the initial set.
`vite.config.ts` writes Vite's build manifest
(`web/dist/app/.vite/manifest.json`), and the script follows the view's
entry (`entry`, e.g. `src/views/Costs.vue`) through its static and dynamic
imports, transitively, stopping at chunks of the initial set, and adds their
CSS. It prints `route <name>: X kB (JS a, CSS b), cap: C kB` and fails above
the route's `capKB`. The manifest is the build's own record of the chunk
graph, so hashed names and chunks Vite splits off for sharing count without
a glob; a chunk shared with another lazy view counts for both. The manifest
is embedded and served like any built file; it names only chunks the app
loads anyway.

- `costs` (`/costs`): cap 120 kB (decision 15 of
  [the Costs page spec](../specs/costs-page.spec.md)). 2026-11-12, before
  Chart.js (#363): 43.7 kB (JS 43.7, CSS 0.0), mostly `USelect` (22.8) and
  the kit's helpers. 2026-11-12, with Chart.js 4 and *Spend over time*
  (#364, `measuredKB`): 104.7 kB (JS 104.7, CSS 0.0); the 61 kB added are
  mostly Chart.js (bar controller, category and linear scales, tooltip),
  then `vue-chartjs`, the wrappers and `DataList`. The cap stays at min(120, 104.7 × 1.3) = 120 kB. The initial
  bundle did not grow (129.4 kB before, 129.3 kB after): Chart.js and the
  wrappers are imported by `Costs.vue` only. 2026-11-12, with *This month*
  (#365): 110.7 kB (JS 110.7, CSS 0.0), 6.0 kB more for the line
  controller, line and point elements, `LineCompare.vue` and the month
  shaping; the initial bundle stays at 129.3 kB. `measuredKB` keeps the
  first chart slice's figure, the one the cap was set from. With *Where it goes*
  (#366): 112.0 kB, 1.3 kB more for `HorizontalBars.vue` and the breakdown
  shaping (it uses the bar controller already registered). With *Tokens*
  (#367): 113.0 kB, 1.0 kB more for the mixed bar and line chart and the
  token shaping. Final (#372, R17): 117.1 kB (JS 117.1, CSS 0.0), the initial
  bundle 129.1 kB; the cap stays 120.

## UI kit

The Desk's controls are [Nuxt UI](https://ui.nuxt.com) 4 components with
[Tailwind](https://tailwindcss.com) 4 utilities, in the plain Vite app (not
the Nuxt framework). The packages are pinned in `web/package.json`; the
Vite plugin is `ui()` from `@nuxt/ui/vite` in `web/vite.config.ts`, and
`App.vue` wraps the app in `<UApp>`. Views moved to the kit use components
and utilities and drop their scoped CSS, except what utilities cannot
express. `web/src/ui/` holds only what Nuxt UI lacks ([DataList](#datalist),
[RouteTabs](#routetabs), [Truncated](#truncated), `useBackClose` for
[sheets](#sheets) and `useKeyboardInset` for the [composer](#composer)).
**Imports are explicit.** Auto-imports and component resolution are off
(`autoImport: false`, `components: false`): `npm run build` runs `vue-tsc`
before Vite, so the declaration files they generate would not exist yet.
Each component is imported from its file, named with the `U` prefix:

```ts
import UButton from '@nuxt/ui/components/Button.vue'
import UApp from '@nuxt/ui/components/App.vue'
```

Composables come from `@nuxt/ui/composables` (e.g. `useToast`). The
variant props (`color`, `variant`, `size`) are typed from the theme Vite
generates, so `vue-tsc` does not check their values: copy them from the
Nuxt UI docs.

**Icons** are [lucide](https://lucide.dev) from the installed
`@iconify-json/lucide`, written `i-lucide-<name>` (e.g. `icon="i-lucide-house"`).
They are bundled at build time (`icon.clientBundle`): the plugin scans
`web/src` for literal names, and names built at runtime must be listed in
`clientBundle.icons` in `vite.config.ts`. An icon missing from the bundle
would be fetched from the Iconify API, which the CSP blocks; the browser
checks fail on that as a CSP violation and a request to another origin.

**CSP.** The kit runs under the unchanged policy (see [Security](#security)):
no inline style, script or other origin. Nuxt UI's colour plugin sets its
colours through an inline `<style>` (`useHead`), so it is left out:
`main.ts` installs only Nuxt UI's icons plugin, not the whole
`@nuxt/ui/vue-plugin` (which would also add the colour plugin and an unhead
instance), and `style.css` defines the colour variables
([design.md](design.md#desk-styles)). Colour mode is off (`colorMode: false`,
`colorScheme.ts` sets `.dark`), fonts are the system's (the plugin's font
handling is off), and components that use `useHead` (`UBanner`, `UKbd`, and
the ones that show shortcuts with `UKbd`) are not used. Tailwind's
preflight is not imported, so a view's own elements
keep the browser's defaults; elements Nuxt UI renders (they carry
`data-slot`) get a small reset in `style.css`.

**Class names.** Tailwind generates a utility for every word in `web/src`
(and in Nuxt UI's sources) that names one. A view's own class with a
utility's name gets the utility too: `class="table"` becomes
`display: table`, `class="hidden"` `display: none`, and scoped CSS overrides
only the properties it sets. Do not give own classes utility names (`table`,
`hidden`, `block`, `flex`, `grid`, `static`, `fixed`, `border`, `shadow`,
`truncate`, …). The markdown table wrapper is
`table-scroll`, the milestone's issue list `issue-table`, the chat's
withheld message `withheld`. `block` (milestone, retrospective), `static`
(prompt options) and `grid` (an SVG group in the context chart) remain, as
their utilities change nothing there.

**Chart colours.** Chart series take `--chart-1` to `--chart-6`, and *Other*
`--chart-other` (a grey), from `style.css`, light and dark; no chart names a
colour of its own. `colorSlots` in `web/src/chartData.ts` gives each key its
slot: the FNV-1a hash of the key mod 6, probing to the next free slot in
sorted key order. A key keeps its colour across reloads, ranges and key
orders without storage, and adding a key rarely moves another. Since any
two slots can meet, the palette was checked for every pair, not only
neighbours (OKLab ΔE, the dataviz validator): on the light card
(`--surface`, white) every pair is at least 9.0 apart for red-green colour
blindness and 16.3 for normal vision, and every colour has 3:1 contrast. On
the dark card (`#1c1a25`) the worst pairs are 7.4 (purple and blue, red-green
blindness) and 13.5 (brown and orange), and every colour has 3:1 contrast;
colour is never alone (legend with values, table view, decision 8 of the
Costs page spec), which those pairs rely on. `chartData.ts` also shapes the
data, without Vue or Chart.js: `topKeys` (six keys by known cost),
`seriesFromPoints` (a series per key over every bucket, the rest and the
server's folded points as *Other*, unknown and unmetered calls per bucket),
`bucketLabel` (`3 Nov`, `3–9 Nov`, `Nov 2026`, formatted in UTC, as the
bucket is a local date already) and `formatUSD`/`formatTokens`, and for the
wrappers `stackedChart` (labels, series with token and range total, unknown
calls per bucket), `chartSummary` (the canvas's text) and `readChartColors`.

### Charts

The Costs page draws its charts with [Chart.js](https://www.chartjs.org) 4
(MIT) and `vue-chartjs`, pinned in `web/package.json`. Chart.js draws on a
canvas and injects no `<style>` element; it sizes the canvas through the
CSSOM (`element.style`), which `style-src 'self'` allows, so the CSP stays as
it is. Libraries that inject styles (ApexCharts, unovis) are refused by that
CSP, and ECharts is too large for the route budget (decision 7 of the Costs
page spec). The chart SVGs elsewhere (context chart, sparkline, `ModelBar`)
stay hand-drawn.

The wrappers are in `web/src/ui/charts/`, one per chart type, so the
Chart.js options live in one place; a view passes them data shaped by
`chartData.ts`:

- `register.ts` registers only what is drawn (`BarController`,
  `BarElement`, `LineController`, `LineElement`, `PointElement`,
  `CategoryScale`, `LinearScale`, `Tooltip`); the rest is
  tree-shaken. No Chart.js legend. Only the wrappers import it, so Chart.js
  is in the chunks of the views with charts, never the initial bundle.
- `useChartColors.ts` reads `--chart-*`, `--text`, `--text-2`, `--border`
  and `--surface` from `<html>` with `getComputedStyle`, and again when
  `<html>`'s class changes (a MutationObserver; `colorScheme.ts` toggles
  `dark` with the system scheme), so a drawn chart follows light and dark.
- `StackedBars.vue`: stacked x and y, no animation, the height fixed by its
  container (`h-60`, `h-72` from the tablet width) and the width following
  it, fewer ticks below 760 px, a tooltip per bucket on hover and tap
  (`mode: 'index'`, `intersect: false`), USD with two decimals or tokens in
  k/M. The canvas is `role="img"` with `chartSummary` as `aria-label`.
- `LineCompare.vue`: a solid line (series 0) against a dashed comparison
  (series 1, `borderDash`, the muted `--chart-other`), no points but on
  hover, the same options policy as `StackedBars.vue` (no animation, the
  tooltip per x value on hover and tap, fewer ticks below 760 px). A value
  `null` is no point. Used by *This month* with `cumulativeMonth` and
  `monthSummary`.
- `HorizontalBars.vue`: a `Bar` with `indexAxis: 'y'`, one value per row in
  its token colour, USD labels, the same options policy; a click on a bar
  with a link emits `select`. Used by *Where it goes* with `breakdownRows`.
- `StackedBars.vue` takes an optional `line` (a percentage line on a second
  y axis, 0 to 100 %, tooltip as a percentage), used by *Tokens* with
  `tokensChart`.
- `ChartLegend.vue`: below the chart, a swatch (a utility class per token,
  no inline style; a dashed line for a dashed series), the label, the total
  per series (*n/a* when only calls of unknown cost) and an optional note;
  it wraps on phones. It and the table read `chartData.ts`'s `ChartView`.
- `ChartTable.vue`: the table view (a scrolling `DataList`): a row per
  bucket, a column per series, the total (left out with `sum` false, for
  cumulative lines) and the unknown calls (or the chart's own per-row
  `notes`). A *Table*
  button (`aria-pressed`) shows it; hidden, it is `sr-only`, so screen
  readers always reach the values a canvas cannot give them.

Rules for every chart (decision 8): at most six series plus *Other*, colours
from the tokens only, never colour alone (legend with values and the table
view), readable at 375 px, no animation, costs in USD with two decimals and
tokens in k/M. Unknown cost is never drawn as $0: it is marked per bucket.

### DataList

`web/src/ui/DataList.vue` is the one table component Nuxt UI lacks: below
760 px (the `tablet:` variant, `--breakpoint-tablet` in `style.css`; the
other is `desktop:`, 1180 px) each row is a card and each cell a two-column grid of
a label and the value; from 760 px up it is a plain table with a header row.
It renders one `<table>`, restyled by utilities, so there is one DOM for
screen readers and find-in-page. The label is a real `<span data-label
aria-hidden>` hidden from the tablet width up (never `content: attr()`), so it
stays selectable.

```vue
<DataList :columns="[{ key: 'status', label: 'Status' }]" :rows="runs" :row-key="(r) => r.id">
  <template #cell-status="{ row }"><Badge>{{ row.status }}</Badge></template>
</DataList>
```

- `columns`: `{ key, label, class?, numeric? }`; `class` goes on the column's
  cells, `numeric` right-aligns header and cells from the tablet width up,
  with tabular figures.
- `rows`, `rowKey`: the data and a unique key per row.
- Slot `cell-<key>` with `{ row }`; without it the cell shows `row[key]`.
- `expanded` and slot `detail`: rows for which `expanded(row)` is true get a
  full-width row below them with the `detail` slot (`{ row }`), e.g. a run's
  calls. Its content never widens the table: a wide table inside scrolls in
  its own container. The toggle is the caller's, usually a button in a cell
  with `after:absolute after:inset-0` and `aria-expanded`.

- `scroll`: the table keeps its columns at every width and scrolls inside its
  own `overflow-x-auto` container, for comparison tables (Spend's tables are
  the first). The page never scrolls sideways.

A row is `relative`, so a link in a cell can cover the row with
`after:absolute after:inset-0`. The research runs table is the first user;
the task page's plan and conditions and its agent calls use it too.

### RouteTabs

`web/src/ui/RouteTabs.vue` is a tab strip of links. Nuxt UI's `UTabs`
switches local state with buttons; the Desk's tabs live in `?tab=`
(`web/src/streamTabs.ts`), so they can be shared, reloaded and opened in a
new tab. Each tab is a `RouterLink`, the active one with
`aria-current="page"`, in a `<nav>` styled as the segmented strip the
stream page always had.

```vue
<RouteTabs :tabs="milestoneTabs" :active="tab" :to="(id) => ({ query: tabQuery(route.query, id, milestoneTabs) })" />
```

- `tabs`: `Tab<T>[]` from `streamTabs.ts`; `active`: the shown tab's id.
- `to`: the link of a tab, usually `tabQuery` on the current query.
- `badges`: optional counts per tab id, shown after the label when above 0.
- `label`: the `<nav>`'s `aria-label`, `View` by default.

The strip never widens the page: it is `min-w-0 max-w-full` and scrolls
sideways inside itself (`overflow-x-auto`, snapping to tab starts). On mount,
when `active` or `tabs` change and when the strip is resized, it scrolls
itself, not the page, so the active tab is fully in view. Below 760 px each
tab is at least 44 px high. A parent that is a flex row lets the strip wrap
to a line of its own (`flex-wrap`), as the stream page's title row does, so
it gets the full width before it scrolls. The stream page is the first user;
the task page (`TaskBody.vue`) shows its tabs with `label="Task"` and the
open questions' count as Overview's badge.

### Truncated

`web/src/ui/Truncated.vue` clamps text and opens it on tap, for what
`title` cannot serve on touch.

```vue
<Truncated :text="run.branch" />
<Truncated :lines="3"><Markdown :text="summary" /></Truncated>
```

- `text`: the text; the default slot takes rich content instead.
- `lines`: lines to show, 1 by default (`truncate`; 2 to 6 use `line-clamp-N`).

The clamp is measured (`scrollWidth`/`scrollHeight` against the client size,
re-measured by a `ResizeObserver`). Only text that is really clipped becomes
`role="button"` with `tabindex="0"` and `aria-expanded`; a tap, Enter or
Space toggles the clamp in place, with no overlay to dismiss. Text that fits
stays plain. It sets no `title`: use `title` only for what is readable
elsewhere. It sets no colour or weight, the parent's classes do; do not give
it `.fact .value`, whose global rules keep `nowrap`. The task page's facts
on its *Details* tab and the current step in its status line use it.

### Search palette

`components/SearchPalette.vue` is a `UCommandPalette` in a `UModal`
(fullscreen on phones, `max-w-xl` from 760 px). `App.vue` loads it with
`defineAsyncComponent` on the first open, so it is outside the initial
bundle; `src/searchPalette.ts` (the open state, the shortcut) stays in it.
Groups, built by `src/searchItems.ts`:

* **Projects** from the rail's list and **Milestones** (`%iid title`, with
  the project) from one `GET /api/v1/milestones?state=all` on first open,
  both filtered by the palette as you type.
* **Issues & MRs** from `GET /api/v1/search?q=`, 200 ms after the last
  keystroke and only from 2 characters; older answers are dropped, an error
  leaves the group empty. `#12` finds issue 12, `!34` merge request 34. A hit
  opens the task page.
* **Pages**: Inbox, Overview, Costs (feature `history`), Research (feature
  `research`), *Plans* (feature `plans`) to `/plans`, and *Plan milestone*
  (feature `plans`, when a project is open) to that project's Plans tab
  (`?tab=plans`); both reuse the flag icon for the bundle budget. See
  [Planner](#planner).

Keyboard: ⌘K / Ctrl+K opens, arrows move, Enter opens the item and closes
the palette, Escape closes it. The palette joins the browser history like a
sheet, so *Back* closes it.

### Sheets

A sheet is a `USlideover` (dialogs are `UModal`; `UDrawer` is not used,
its `vaul-vue` is unmaintained). Every sheet closes by its scrim, its close
button, Escape and the browser's back button. There are no swipe gestures.
The rail on phones is the first sheet: the bottom bar's *Projects* opens it from the
left with the rail's content (`components/RailNav.vue`, which the aside
renders on tablet and desktop). Below 1180 px the stream header's
*Context* button opens the context panel (`components/ContextPanel.vue`)
from the right, the side it has on desktop; widening the window to 1180 px
closes the sheet. Dialogs close the same ways; the first is the
Retrospective tab's dismiss confirmation.

`web/src/ui/useBackClose.ts` gives a sheet the back button:

```ts
const open = ref(false)
useBackClose(open) // <USlideover v-model:open="open">
```

- Opening pushes a history entry for the same URL (`history.state.__sheet`).
  Back pops it and closes the sheet: the URL and the route stay as they
  were, and the view does not load its data again.
- Closing by scrim, button or Escape pops the entry (`history.back()`, only
  while the entry is on top), so no entry is left behind.
- A navigation while the sheet is open, a link inside it or any other, is
  held until the entry is popped, then goes on: back from the new page
  returns to the page the sheet was opened on. Forward to the popped entry
  opens the sheet again.
- vue-router never sees the entry. The pops within the page are stopped
  before vue-router's `popstate` listener, which is why `main.ts` calls
  `listenForSheets()` before `createWebHistory()`: listeners on `window`
  run in the order they were added, whatever their capture flag.
  `useBackClose` throws without it.

A sheet sets `z-40` on its `overlay` and `content` (`ui` prop): Nuxt UI
sets no z-index, and the sticky mobile bar and the fixed bottom bar have 10. The e2e cases
`rail sheet …` cover each way to close, that the router did not navigate
and the API was not asked again, and a link inside the sheet followed by
back; `rail-sheet@375.png` shows the open sheet. The cases `context sheet …`
open the Context sheet on the project and the milestone page at 375 and
768 px, check its four parts (Needs you, the queue, companions, the last
30 days), that it fits the width, and that back, scrim, close button and
Escape close it; `context-sheet-<page>@<width>.png` show it open. At
1280 px the project page keeps the panel at its side and neither page has
the button.

### Composer

`web/src/components/Composer.vue` is the one text box of the Desk: the
task's *Chat with the agent* (`mode="chat"`, `TaskChat.vue`) and the
stream's notes (`mode="note"`, the project stream in `Stream.vue` and a
milestone's *Pilot* tab in `MilestonePilot.vue`).

```vue
<Composer v-model="body" mode="chat" placeholder="Message the agent…" hint="Enter+Ctrl/Cmd to send"
  :busy="sending" :disabled="sending" :error="sendError" :can-interrupt="canInterrupt" @send="onSend">
  <template #controls><!-- the chat's command buttons --></template>
</Composer>
```

- It holds a `UTextarea` that grows with its text (`autoresize`, up to 12
  rows; 16 px on phones, 14 px from tablet up), Send (a `UButton`, 44 px on
  phones like every button), Interrupt (`canInterrupt`), the error line and
  Ctrl/Cmd+Enter. Send and Ctrl/Cmd+Enter do nothing while `busy` or
  `disabled` or with blank text; Interrupt sends blank text as well.
- It emits `send` with `{ kind: 'message' | 'interrupt', body, target }`.
  The caller owns the text (`v-model`), `busy`, `error`, the drafts and
  the API call, and clears the text once the post succeeded.
- `mode="note"` adds the target pill and its *Send to* menu
  (`targets`, `v-model:target`), the commands menu that opens on `/` or
  `yes P` (`suggestions`), and `blocked`: the reason the box gives way to a
  card, with *Connect GitLab* when `connectUrl` is set. The state of a note
  composer is `useStreamNote` (`src/streamNote.ts`): the GitLab status
  (loaded the first time the composer is shown), the targets
  (`noteTargets` in `src/stream.ts`) and the post.
- The chat's draft helpers (`chatDraftKey`, `readChatDraft`,
  `writeChatDraft`) and command builders (`modelCommand`, `skipCommand`)
  are in `src/chatState.ts`; `TaskChat.vue` keeps the draft and its
  `clientKey` in session storage until a 2xx and puts the command buttons
  into the `controls` slot.

Where it sits: a note composer stays at the top of its pane (sticky) as the
stream scrolls. On a phone the chat composer is sticky at the bottom of the
screen, inside the chat card: above the bottom bar, and while typing above
the keyboard. Chrome on Android resizes the page for the keyboard
(`interactive-widget=resizes-content`); iOS Safari does not, so
`web/src/ui/useKeyboardInset.ts` sets `--keyboard-inset` on `<html>` from
`visualViewport` (the height the keyboard covers, 0 without one) while a
composer is mounted, and the composer's `bottom` is the larger of it and
the bottom bar with its inset. For sticky to follow the screen, the
content pane (`main`) is `overflow: visible` on phones: it grows with the
page and never scrolled. Send and Interrupt keep the focus in the text on a
press (`mousedown.prevent`), so the keyboard stays up and the bottom bar
does not come back and move the composer under the finger.

The e2e cases `composer …` type and send on the task's *Chat* tab and on
the project stream at 375 and 1280 px and check the POST body through the
route mock (Send and Ctrl+Enter, a new `clientKey` per message, the
target menu and the `/` commands), the 16 px text and 44 px Send on a
phone, that a failed send keeps the text and shows the error and a reload
retries with the same key, and at 375 px that the chat composer ends above
the bottom bar and above a 300 px `--keyboard-inset`
(`composer-chat@<width>.png`, `composer-note@<width>.png`,
`composer-keyboard@375.png`).

### Prompt card

`web/src/components/PromptCard.vue` is the one prompt card (see *Prompt
card* above for what it shows and sends): full on the Task page, the
milestone's *Pilot* tab and the full pause card, `compact` in the compact
pause card. Stream rows never show it (below).

```vue
<PromptCard :namespace="ns" :name="project" :prompt="p" />
<PromptCard compact :namespace="ns" :name="project" :prompt="p" :to="cardRoute" />
```

- Full: block markdown for the question, the context, the options' detail,
  the answer and its outcome. Options are `UButton`s (the recommended one
  solid and first), the free text a `UTextarea` (16 px on phones) with Send.
  It carries the anchor `#prompt-<id>` and scrolls to it when the URL has
  that hash.
- Compact: the id and the question in one line (inline markdown, clamped by
  `Truncated`, so a tap opens it on a phone), the recommended option as a
  button, or the status or read-only line, and *more…* to `to`.
- Both answer through `usePromptAnswer` (`src/promptAnswer.ts`); the order,
  status and command logic is `src/promptCard.ts`. Buttons are 44 px on
  phones (the global rule). Both carry `data-prompt="<id>"`.
- From the click on an option, or *Send*, every option button and the text
  box give way to "✓ <label> · sending…" ("✓ your answer" for text). A
  failed post or a failed command row brings them back with the error. A
  sent answer keeps them away ("· sent") until the status update closes the
  prompt.
- A closed full card shows only the chosen option with its "→" actions,
  the free text, the outcome and the status line. The other options fold
  under *Other options (n)*; a prompt closed without an answer folds them
  all under *Options (n)*.
- The status line and badge come from `promptStatus` and `shortStatus`.
  With the recorded `closedReason` and `closedBy`: "Answered by …"
  (success), "Handled by the pilot: <cause>" (progress), "Resolved on its
  own: <cause>", "Replaced by Q14" (a hash link to its card, without
  `?tab=`) and "Expired"
  (neutral). Prompts closed before the operator recorded a reason keep the
  texts by state.

A `prompt.posted` stream row (`EventCard.vue`) is one line without
buttons: "Q12 asked:" linked to the card (`#prompt-Q12` without `?tab=`,
so `milestoneTabFromRoute` picks the tab that holds it), the question
(from the prompt in status, else the event summary) and, while the view
has the prompt, its state badge. `IssueStream` takes the view's `prompts`
for that badge (the *Pilot* tab and the issue page pass them).

The e2e cases `prompt card …` answer at 375 and 1280 px through the route
mock and check the POST body to the target task's chat: an option on the
Task page and free text on the *Pilot* tab, check the milestone stream's
one-line `prompt.posted` row, with the touch sizes and no sideways scroll
(`prompt-card-task@<width>.png`, `prompt-card-pilot@<width>.png`,
`prompt-row-stream@<width>.png`). `e2e/prompt-card.spec.ts` checks the
card after a click (no button left, "sending…"), after a refused post (the
buttons and the error), closed cards with their folds and labels, and that
the *Pilot* tab draws an open prompt once.

### Note thread

`web/src/components/NoteThread.vue` is the one renderer of a GitLab comment
thread: full in the task page's **Conversation**, `compact` in the stream's
grouped notes.

```vue
<NoteThread :notes="fromGitLabNotes(part.notes, gitlabUser)" />
<NoteThread compact :notes="fromStreamEvents(events, me)" />
```

- Both take `ThreadNote`s (`src/noteThread.ts`), mapped from a stream note
  event or a GitLab note, and show each note's author (*you* for the user's,
  *Pilot* for the bot's), its time and the body as block markdown, in a
  bubble tinted for the user's and the pilot's notes. System notes are faded.
- Full: every note with its author line. Compact: an avatar and the newest
  note's author and issue or MR, the last three notes and *N earlier* for
  the rest. A redacted note says whose it is with a link to open it.
- Replying stays with the caller: the conversation's form (thread tabs,
  command chips, *Post comment*), the stream's composer. Its controls are
  44 px on phones (the global rule).

The e2e cases `note thread …` at 375 and 1280 px open the task's *Chat*
tab, switch to the MR's thread and post a command through the route mock,
and fold and unfold the milestone stream's thread of #281
(`note-thread-task@<width>.png`, `note-thread-stream@<width>.png`).

### Milestone rows

`web/src/components/MilestoneList.vue` owns the one column grid of the
milestone lists on the overview (*In progress*, *Recently done*, with a
*Project* column) and on a project page (without it).

```vue
<MilestoneList :rows="inProgress" :show-project="!perProject" label="In progress" />
```

- From tablet width (760 px) the list is a grid with the column template.
  The column heads and every `MilestoneRow.vue` are
  `grid-template-columns: subgrid` of it, so the columns are defined once.
  The edge tracks are 14 px wider than their content, because a subgrid's
  padding comes out of its edge tracks.
- On phones the heads are hidden and a row stacks: the project on top, title
  and state on one line, the figures (progress, open, cost, tokens) as a
  wrapping line below, *open* and *tok* labelled. Nothing scrolls sideways.
- Both are Tailwind utilities, no scoped `<style>`. The rows' data comes
  from `src/milestoneRows.ts` as before.

The e2e cases `milestone rows …` at 375, 768 and 1280 px check on the
overview and the project page that each row's cells start where the heads
do, and that on a phone a row stacks inside its width
(`milestone-rows-<page>@<width>.png`).

### Stream header

The header of the project and milestone pages (`web/src/views/Stream.vue`)
is Tailwind utilities and Nuxt UI, without scoped CSS.

- On phones the title has a row of its own and wraps. The cost chip and the
  *Context* button follow it, then the [RouteTabs](#routetabs) strip and the
  stream search (a full-width `UInput`, 16 px; 14 px from tablet width as
  before) take a full row each. The
  scope links (the project's *Everything* and its milestones) wrap onto more
  lines, a long milestone title inside its link.
- From tablet width (760 px) the title, the cost, the tabs, the search and
  the *Context* button share one row as before, and the scope links stay
  on one line that scrolls inside itself.
- Stream rows (`stream/EventCard.vue`) break long URLs, branches and SHAs
  anywhere, so a row never widens the stream.

The e2e case `stream-project header rows` at 375 and 1280 px checks the
rows' order and widths; the route checks cover every project and milestone
tab at 375, 768 and 1280 px.

### Inbox

The Inbox (`web/src/views/Inbox.vue`) is Tailwind utilities and Nuxt UI,
without scoped CSS.

- Each item is a list row whose title link covers the row (`after:absolute`),
  so a tap anywhere opens it where `inboxRoute.ts` says, as before. On
  phones a row stacks project, title, reason and time and is at least 44 px
  high; from 760 px they are four columns.
- The reason is inline markdown, clamped to two lines by
  [Truncated](#truncated): a clipped reason sits above the row's link and
  opens in place on a tap; one that fits is plain text and part of the row.
  A ready companion's merge request link also sits above the row's link.
  Titles and project names wrap instead of being cut off. No `title`
  attribute carries text of the list (`RelTime` keeps the absolute time as
  its `title`; the relative time is shown).
- *Notify me* and *Notifications on* are `UButton`s with `aria-pressed`.
  The links of the paused strip are at least 44 px high on phones.
- Paused autopilots show the compact pause card
  (`components/PauseCardCompact.vue`, `flat` in the list), also on
  utilities: on phones the title and *Open* (a `UButton` link, 44 px high)
  share the first line and where it paused wraps below; the reason and the
  *Way out* line open on a tap like the Inbox's reasons.

The e2e cases `inbox rows …` at 375 and 1280 px check the rows' and
links' heights, that no `title` holds the list's text, that a clipped
reason opens in place, and that a row and *Open* navigate
(`inbox-rows@<width>.png`).

### Dashboard

The dashboard (`web/src/views/Dashboard.vue`) and its Overview tab
(`components/DashboardOverview.vue`, with `MilestoneSections.vue` and
`EngineUsage.vue`) are Tailwind utilities and Nuxt UI, without scoped CSS.

- *Overview* and *Stream* are a [RouteTabs](#routetabs) strip beside the
  heading; on a phone the strip's links are 44 px high.
- The summary strip's chips wrap. The needs-you chip links to the Inbox,
  the *today* and *last 7 days* chips to the [Costs page](#costs-page)'s
  last 7 days; these links are at least 44 px high on phones.
- *Working now* has one column of cards on phones, two on tablets, and on
  desktop as many columns of at least 300 px as fit, as before. Long titles
  and project names break instead of widening a card. When `finishing` is
  above 0 the card shows *N finishing* (`overview.ts`) linking to the
  project page.
- The milestone lists use the shared milestone row grid
  (`MilestoneList.vue`); *Show all* is a `UButton`.

The e2e cases `overview cards › working now columns@<width>` at 375, 768
and 1280 px check the columns and the Inbox chip's height
(`overview-cards@<width>.png`); the route checks cover `overview` and
`overview-stream` at the three widths.

### Project Queue and Finishing

On a project's *Stream* tab (`web/src/views/Stream.vue`), above
`MilestoneSections`, `ProjectQueue.vue` and `ProjectFinishing.vue` read
`project.queue` / `queueCommands` and `project.finishing` (see
[Queue and Finishing](#queue-and-finishing)). Labels and button commands
live in `projectQueue.ts` and `finishing.ts`; *Close issue* calls
`closeHoldingIssue`. `needsEstimate` in `projectQueue.ts` marks
`waiting`/`holding` rows with no spend for
`GET …/estimate?milestone=` (see [Planner](#planner)). Tailwind utilities
and Nuxt UI, without scoped CSS; light and dark, readable at 375 px.

### Plans

The plan lists and the plan page (`web/src/views/Plan.vue`, route `plans`
at `/projects/:namespace/:name/plans/:plan`) are Tailwind utilities and
Nuxt UI, without scoped CSS. There is one list, `PlanList.vue`, with two
thin hosts: `AllPlans.vue` (route `all-plans` at `/plans`, its own chunk,
heading *Plans*) and the project's Plans tab (`Stream.vue`, `?tab=plans`
on `stream-project`; `projectTabsFor(hasFeature('plans'))` in
`streamTabs.ts` appends it, `milestoneTabs` spreads `streamBaseTabs`
without it). The old list URL `/projects/:namespace/:name/plans` is a
redirect record (`plans-list`) to that tab. `PlanList.vue` loads `GET
/api/v1/plans` (`?project=` when given a project) on mount and on a
project change, not live, and shows *Drafting* (every phase but Created)
and *Milestones created*, each last active first and hidden when empty; a
404 shows an empty list (`AllPlans.vue` shows *Not found*). Each plan is
a `PlanRow.vue` (project only across projects, title, phase, spend, last
activity, and the milestone's `%N` with its state and `ProgressBar.vue`,
or *Not planned* with the GitLab link). `ProgressBar.vue` is shared with
`MilestoneRow.vue`. *Plan milestone* in `PlanList.vue` shows when
`planStartProjects` (`planStart.ts`: the `steer` projects, sorted) is not
empty, also on an empty list: one project starts an untitled plan
(`createPlan`) and opens it, two or more open an inline picker of buttons
that wraps on phones. It reads *…* while busy, shows errors in a banner,
and a 403 hides it until the list reloads. Entry points: *Plans* in
`RailNav.vue` (below *Costs*), the project's Plans tab, and *Plans* /
*Plan milestone* in the ⌘K Pages group (`searchItems.ts`). *Delete* on
the plan page returns to the project's Plans tab. Chat is `PlanChat.vue` (Desk chat parts);
the draft is `PlanDraft.vue` with Edit / Preview, Save, the newer-draft
diff (`planDraft.ts`) and `PlanCreateDialog.vue` (Spec / Autopilot, estimate
from `estimate.ts`). List helpers live in `planRows.ts` (`plansPath` in
its own `plansPath.ts`, so `client.ts` keeps the initial chunk small). See
[Planner](#planner).

### Milestone Issues

The milestone's Issues tab (`web/src/components/MilestoneIssues.vue`) is
Tailwind utilities and Nuxt UI tokens, without scoped CSS.

- The table is one grid. From desktop width the column heads and every
  issue row are subgrids of it, so the cells line up with the heads as
  before; a release row spans all columns.
- On phones and tablets the heads are hidden (beside the rail a tablet has
  about 460 px, too little for eight columns). An issue row shows its number and title
  (and the version it shipped in) on the first line, then its state, merge
  request and labelled figures as a wrapping line. Issue numbers never
  wrap.
- Release rows (`releaseRows.ts`) sit between the issues in the same order
  and with the same content at every width.
- Links in rows (issue title, merge request, release, pipeline) are at
  least 44 px high on phones.

The e2e cases `milestone issues › issues and releases@<width>` at 375, 768
and 1280 px check the order of issues and releases, that numbers stay on
one line, the column alignment from desktop width, the stacking below it and
the link heights on a phone (`milestone-issues@<width>.png`); the route checks cover
`stream-milestone-issues` at the three widths.

### Milestone Pilot

The milestone's Pilot tab (`web/src/components/MilestonePilot.vue`) and
the autopilot queue (`web/src/components/QueueList.vue`, on the Issues tab
and in the context sheet) are Tailwind utilities and Nuxt UI tokens,
without scoped CSS.

- The Pilot tab keeps its order: the pilot line (autonomy, cost, *Pilot
  task*), the message box (*Ask the pilot* is `TaskChat` with the merged
  `Composer`, so its input is 16 px on phones) and pilot events (a
  question is one line there). The questions and the pause history are on
  the Needs you tab. On phones
  the *Pilot task* link is 44 px high.

- A queue item shows its position, its number (never wrapped), its title
  (two lines, the rest opens on tap) and its state, which wrap onto lines of
  their own where the column is narrow; below come what a waiting item
  waits for (`after #1, #2`, or `for a free slot`), the merge request,
  companion, task and decisions, and for a held item the hold's reason (as
  text) with *Resume*. Links and *Resume* are 44 px high on phones,
  *Revisit* and *Resume* are `UButton`s. The order is `queueRows`
  (`milestoneQueue.ts`).
- On the Issues tab the queue is grouped by lane (`laneGroups`):
  *Running (n of N)* (the view's `running` of `maxParallel`, shown also when
  empty), *Landing*, *Ready to land* (by `readyAt`), *Waiting*, *Held* and
  *Done*; empty lanes are left out, items keep their queue position and
  order. The context sheet keeps the plain list.
- Waves (display only, the layers of the plan graph): every planned item
  shows a chip `W1`, `W2`, … after its number (the view's `wave`; none for
  wave 0, outside the plan), and above the lanes a line says `N waves` when
  the plan has more than one (`waveChip`, `wavesLabel`).
- Owners (the `parallel` grant) get a stepper in the Running heading:
  *Running n of* − N +, the buttons labelled *Fewer at a time* and *More at
  a time* (44 px on phones). A click sends `{"cmd":"parallel","parallel":N}`
  to the spec task, the same as `/agent parallel N`; − is disabled at 1, +
  at 10 (`parallelCommand`). The new limit shows muted as pending until the
  view's `maxParallel` reports it; a failed post or answer drops it. The
  list's status line says *Setting the limit to N…*, then the operator's
  answer. Everyone else sees *Running (n of N)*.
- *Resume* (viewers with the `chat` grant, as on the pause card) sends
  `{"cmd":"resume","issue":N}` to the spec task, the same as
  `/agent resume #N`: it lifts that issue's hold only. The list shows
  *Resuming #N…*, then the operator's answer.

The e2e cases `milestone pilot › pilot tab and prompt link@<width>` and
`milestone pilot › queue lanes, order and numbers@<width>` at 375, 768 and
1280 px check the sideways scroll, the touch heights on a phone, the
prompt link to a closed card, the queue's lanes, order, positions,
one-line numbers, waiting and hold lines, wave chips and count, the Resume
payload and the stepper's payload and pending limit
(`milestone-pilot@<width>.png`,
`milestone-queue@<width>.png`, `milestone-queue-dark@<width>.png`); the route checks cover
`stream-milestone-pilot` at the three widths.

### Needs you tab

The milestone's Needs you tab (`web/src/components/MilestoneNeeds.vue`,
utilities only) sits between Issues and Pilot. `needsOpen`
(`web/src/needsOpen.ts`) splits the milestone view for it.

- *Open* (live milestones only): the full pause card, one card per hold,
  the open prompts (the merged `PromptCard`) without the pause card's own,
  and the pilot's open proposals (P-n) with *Accept* and *Reject*, which
  post the same chat command to the pilot task as the chat's buttons did.
  The tab's label is *Needs you · n*, n the items under *Open*.
- *Handled*: one block per problem (`web/src/components/NeedsProblem.vue`)
  from `needsCases` (`web/src/needsCases.ts`): an issue, or the milestone,
  with its prompts, the pilot's actions (`pilot.acted`) and its pauses,
  newest first. The heading names the issue (a link to its page) or
  *Milestone*, says *Open* or *Handled*, and "Pilot attempts: u of m" while
  the milestone has a pilot. Open problems come first and start unfolded;
  a chevron button (`aria-expanded`) folds the entries. The heading is no
  `<summary>`, whose children are presentational: the issue link stays a
  link.
  A closed prompt is a row (ID, question, label) that unfolds the full
  closed `PromptCard`; an open prompt's row links up to its card under
  *Open* and has no buttons. An action shows "Pilot" or "@owner", the
  summary and the why as text and the result lines as markdown without
  HTML. A pause shows its kind (`pauseTitle`), its time span and who ended
  it how (`pauseEnd`). The pause totals end the section on one line. A
  stored milestone shows only *Handled*.
- A prompt or problem link (`#prompt-Q<n>`, `#problem-<iid>`) without
  `?tab=` opens this tab; it unfolds the problem (and a closed prompt's
  row) and scrolls to it. On phones each fold is 44 px high.

The e2e cases in `web/e2e/needs-you.spec.ts` at 375 and 1280 px check that
a prompt link opens this tab at the card, that an open prompt shows once
and is counted in the label, that a pause card's own prompt is not listed
again, that the Pilot tab has no questions and no pause history, and that
*Handled* lists one problem per issue, open ones first, with its entries
newest first, the attempts, unfolding rows and problem links; the
route checks cover `stream-milestone-needs` at the three widths.

### Pause cards

The full pause card (`web/src/components/PauseCard.vue`, on the Needs you
tab), the pause rows of the Needs you tab's problems
(`web/src/components/NeedsProblem.vue`) and the compact card (`web/src/components/PauseCardCompact.vue`, Inbox, board,
stream) are Tailwind utilities and Nuxt UI tokens, without scoped CSS.

- The full card's reason is block markdown. Its actions, the Way out's
  buttons, *Confirm* and *Cancel* are `UButton`s, at least 44 px high on
  phones, where the action row and each remedy stack into a column and the
  skip select is 16 px. Commands post to the task chat as before
  (`pauseCard.ts`).
- The pause rows' reasons and the compact card's reason are inline
  markdown clamped in `Truncated` (one and two lines): a tap opens the
  rest in place. No `title` holds their text.

The e2e case `milestone pilot › pause cards: reason opens on tap, Skip
posts@<width>` at 375 and 1280 px opens a clipped history reason on a
phone, checks the button height and the sideways scroll, and asserts the
`skip` command posted by *Skip* (`pause-cards@<width>.png`).

### Board

The Board tab of a project (`web/src/components/ProjectBoards.vue`) and of
a milestone (`web/src/components/Board.vue`), and its cards
(`web/src/components/TaskCard.vue`), are Tailwind utilities and Nuxt UI
tokens, without scoped CSS.

- The four columns (*In progress*, *Needs you*, *CI*, *Review*) sit side
  by side on desktop, two per row on a tablet and stacked on a phone. Every
  column keeps its heading and count; on a phone an empty column is only
  its heading (count 0), without the placeholder.
- A card's title wraps, its badges (`Badge` on `UBadge`) wrap onto further
  lines and the whole card is the link, at least 44 px high.
- On the project's board a milestone's heading wraps; on phones its links
  (the milestone, *Stream*) and the *Plan* card are 44 px high.

The e2e cases `board › stream-project-board columns and cards@<width>` and
`board › stream-milestone-board columns and cards@<width>` at 375, 768 and
1280 px check the columns per row, the headings and counts, the cards
inside their column with wrapping titles and their heights, the link
heights on a phone and the sideways scroll (`board-project@<width>.png`,
`board-milestone@<width>.png`); the route checks cover
`stream-project-board` and `stream-milestone-board` at the three widths.

### Milestone Retrospective

The milestone's Retrospective tab (`web/src/components/MilestoneRetro.vue`)
is Tailwind utilities and Nuxt UI tokens, without scoped CSS.

- When it was posted and the link to its comment wrap as one line of
  facts; the link is 44 px high on phones. The text, and each leftover's
  body under *Details*, stay block markdown (`Markdown`).
- The cost drivers are a `DataList`: a table from the tablet width, a card
  per driver with labelled cost, tokens and share on phones.
- A leftover's number, title and state wrap onto further lines where the
  column is narrow. On phones its checkbox's label, the state link and the
  *Details* fold are 44 px high (the checkbox keeps its size, the label
  around it takes the tap).
- The follow-up actions (*Draft follow-up milestone*, *Edit the pilot's
  draft*, *Stop waiting*, *Dismiss remaining*, *Create milestone and start
  autopilot*, *Discard*) are `UButton`s, 44 px high on phones, where each
  action row and its hint stack. The editor's title (`UInput`) and
  description (`UTextarea`) are 16 px on phones, and its preview moves
  below the fields. *Create…* and *Discard* keep the focus in the field on
  a press, as the composer's *Send* does: the bottom bar, hidden while a
  field has the focus, would otherwise come back and move them between
  press and release.
- *Dismiss remaining* asks in a `UModal`, the Desk's first dialog: it closes
  by its scrim, its button, Escape and the browser's back button
  (`useBackClose`, as the sheets), except while the reply is being sent.
  *Dismiss them* posts as before.

The e2e case `milestone retrospective › follow-ups: create and dismiss@<width>`
at 375, 768 and 1280 px checks the sideways scroll, the stacked cost
drivers and the hit areas on a phone and the editor's font sizes; it ticks
a leftover, drafts, creates (asserting the `POST …/followup` body), closes
the dialog by back without a post, then dismisses (asserting `POST
…/followups/dismiss`) (`milestone-retro@<width>.png`,
`milestone-retro-editor@<width>.png`, `milestone-retro-dismiss@<width>.png`).
The tab is also among the hit-area routes, and the route checks cover
`stream-milestone-retrospective` at the three widths.

### Spend tab

The project's and the milestone's Spend tab (`web/src/components/SpendPanel.vue`,
with `ModelBar.vue`) is Tailwind utilities and tokens, without scoped CSS.

- The totals are key and value pairs: two columns on phones, one wrapping
  line from the tablet width.
- Every table is a comparison (the drill-down's milestones, issues, runs or
  calls, by kind, role and model, by kind and model, conflict calls, and
  the tuning tables) and a `DataList` with `scroll`: it keeps its columns
  at every width and scrolls sideways inside its own card on a phone, never
  the page. A long milestone or issue title wraps within 16rem, so it does
  not make the table as wide as the title.
- The drill-down links (crumbs, the bar unit *tokens*/*USD* and a row's
  milestone, issue or run) are 44 px high on phones.
- What the column heads *$ per 1M tokens*, *Subagent share* and *Gate
  blocks per call* and the step-commit line mean, and what a *repeat*
  conflict call is, stand below their table instead of in a hover title.
- A milestone's *old basis* or *mixed basis* mark is in the warning tone.

The e2e cases `spend › stream-project-spend tables scroll in their
container@<width>`, `spend › stream-milestone-spend tables scroll in their
container@<width>` and `spend › stream-milestone-spend calls of a
run@<width>` at 375, 768 and 1280 px check the figures against the
fixture, that neither the page nor the content pane scrolls sideways, and
at 375 px that the comparison tables' containers do (`scrollWidth >
clientWidth`) and the drill-down links are 44 px high
(`stream-project-spend@<width>.png`, `stream-milestone-spend@<width>.png`,
`stream-milestone-spend-calls@<width>.png`). The milestone's Spend tab is
also among the hit-area routes.

### Spend tab in detail

What the Spend tab shows, field by field; the user side is
[The Spend tab](../guides/budgets-and-spend.md#the-spend-tab). It reads
`GET /api/v1/spend` (without the research database the tab says it needs
the database).

* **Totals:** the cost in USD (*n/a* when no call reports or estimates
  one, never $0; *+N calls without cost* when some calls have none, they
  are not in the sum; *N calls not metered* for the calls that report
  neither a cost nor tokens, such as cursor with model `auto`, whose ACP
  session reports no usage; *N of M calls on the old token basis*, or
  *old token basis*, for calls recorded before the token basis was stored,
  see below), input, output, cache read and cache write tokens,
  calls (and subagent calls), checkpoints, duration and the cache-read
  share (cache reads of all input tokens). *Checkpoints* counts the
  checkpoint commits of the scope: those of calls that were cut off and
  those saved without a call (budget stop, lost pod, OOM kill), which add
  no call; the tooltip lists them by reason. Runs recorded before agent calls add their
  cost without tokens: *includes $X from runs before token tracking*.
  A call's tokens include its subagents (the sum of its models), so the
  totals, the drill-down and the breakdowns add up to the same tokens.
  Calls recorded before that count the main thread only. A Desk chat turn
  counts only that turn's tokens, models and cost; turns recorded before
  that counted all earlier turns of their chat session again. A resumed
  call (implementer step, fix round) counts only itself too; a call
  recorded before whose models do not add up to its tokens counts whole
  on its main model, unsplit
  ([What the spend counts](design.md#what-the-spend-counts)).
* **Token basis:** calls recorded before the token basis was stored count
  tokens differently (subagents and Desk chats). A scope that mixes them
  says *N of M calls on the old token basis* next to its totals (*old
  token basis* when all are old). At project scope each milestone row
  carries a *mixed basis* or *old basis* mark. Only milestones without a
  mark compare on tokens; old rows are not backfilled.
* **Drill-down:** a table one level down, with calls, tokens, cost and a
  share bar per row: a project's milestones, a milestone's issues (and
  its own spec, pilot and retro calls), an issue's runs, a run's calls.
  Clicking a row goes one level down; a project's milestone opens the
  milestone stream's Spend tab. The position is kept in the query
  (`?tab=spend&spendIssue=77&spendTask=…&spendRun=2`), so it can be
  linked to and back goes up again; breadcrumbs lead back to the project.
  Calls of no milestone and a milestone's own calls are not split further.
  An issue with a companion repository has a *companion* marker on its row
  (the tooltip names the project): its calls edit both trees and count to
  the home issue.
* **Calls** (a run's): seq, time, kind, role, engine and model (with the
  effort), whether the session was resumed, the engine it fell back from,
  tokens, cost and its source: *reported* by the engine, *estimated*
  from tokens and the `pricing:` table (shown as `~$0.42`), or *n/a*
  (cursor without pricing). A cursor call is *reported* when its ACP
  `usage_update` carries a cost in USD, *estimated* when it has tokens and
  a price, *n/a* otherwise, never $0; the cursor-agent in the agent image
  sends no usage yet. Calls recorded earlier are not re-priced. A call
  with neither a cost nor tokens shows *not metered* as its cost and *—*
  as its tokens.
* **Model bars:** every row of the drill-down (milestones, issues, runs
  and calls) has a stacked bar split by model: the main model first (in
  the progress color), the subagent models after it, each in its own color
  (named in the legend above the table, a model keeps its color in every
  row). Hovering a segment shows the model, its tokens and its cost. The
  bars of a table share one scale, the largest row full width, so rows can
  be compared (e.g. rising step costs). *tokens | USD* above the table
  switches the unit, kept in the query (`?spendUnit=usd`, tokens by
  default) while drilling down. In USD, a row without a known cost draws
  a faint token bar labelled *n/a* (never a $0 bar, on the scale of the
  other token bars), a model without a known cost is left out of the bar
  (named in its tooltip), and an estimated cost is marked `~`. Rows from
  before token tracking have no bar.
* **Breakdowns** by kind, role and model (subagent models as their own),
  highest cost first, each row with its share bar. A row whose calls all
  report neither a cost nor tokens stays a row and shows *not metered*
  instead of *n/a* and 0 tokens; in the drill-down's model list such a
  model is marked *(not metered)*. Cursor's model `auto` reads *cursor
  auto (unmetered)* in both: cursor reports neither the model it picked
  nor the usage.
* **By kind and model:** the calls crossed by kind and model (subagent
  models as their own rows), highest cost first: kind, model, calls,
  output tokens, cache write tokens, cost and cost per 1M tokens, so
  "Opus with large outputs" can be read off. At milestone and issue scope
  *Conflict calls per run* lists the conflict calls of each run (the 20
  most expensive, `conflictRuns` of the API, key `<issue>/<task>/<run>`);
  more than one call marks a repeat. The same questions per issue can be
  asked of the database with [`hack/spend-report.sql`](design.md#spend-report-script).
* **Tuning tables**, each with the average cost per call (of the calls
  with a known cost; *n/a* when none has one), to set the levers in
  [Agents and models](../reference/agents-and-models.md#roles) by:
  * *Review rounds:* the review calls by round. Within a run, round 1 is
    the reviews before the first fix call, round 2 those before the next,
    and so on; the rounds are summed across the scope's runs. A high
    round-1 cost on big changes points at the reviewers'
    `maxInlineDiff`; many rounds at the fixes, not the cap.
  * *Implementer steps: resumed vs fresh:* implement calls by whether
    their session was resumed, with tokens and cost per call
    (`implement.stepSession`).
    * *Step commits* (below the table): the scope's implement calls that
      left lines (their count) and their lines, added plus deleted:
      median, p90 and max, so you can see whether `implement.maxStepLines`
      keeps the steps small, and the step-size gate's nudges in the
      scope (`stepLines` of the API). Hidden without step data (no
      implement call left lines and the gate never nudged).
  * *With vs without subagents:* per role where a subagent ran, the calls
    that ran one (`subagentCalls` above 0) against those that did not,
    with tokens and cost per call and the subagent models' share of the
    tokens (`subagents`). Roles where no subagent ran are left out.
    * *Gate blocks per call* (same table): the read gate's denials
      (`readGateBlocks`) divided by the role's calls, one decimal; empty
      without a block, so projects without a `readGate` see an empty
      column. Many blocks mean the role keeps trying to read whole
      files: tighten the scout's prompt or raise `minLines` if the gate
      blocks reads that are fine.

Shares are of the rows' known cost; a row without a known cost shows
*n/a* there. When no row has a known cost they are shares of the tokens.
The Spend tab loads when it opens or the drill-down moves; it does not
follow live changes. *Costs across projects* above its drill-down opens
the [Costs page](#costs-page) filtered to the project.
### Costs page

The user side is [The Costs page](../guides/budgets-and-spend.md#the-costs-page).

*Costs* (`/costs`, `web/src/views/Costs.vue`, its own chunk) shows what
agents cost across every project you may see, and research when you may
read it, from `GET /api/v1/costs`. It is in the rail after *Overview*, and
so in the phone's Projects sheet and the bottom bar. The
entry shows with the research database (the `history` feature). The
dashboard's *today* and *last 7 days* chips open it for the last 7 days.

The filters live in the URL, so a shared link shows the same page:
`?range=` (`30d`, the default, `7d`, `90d`, `month`, `lastMonth`, `12m`) or
`?from=&to=` (a custom range, `YYYY-MM-DD`, both), `?group=` (`project`,
the default, `kind`, `model`, `engine`), `?project=<namespace>/<name>`,
`?kind=` and `?engine=`. An unknown value counts as its default or no
filter (`web/src/costs.ts`). Changing a filter replaces the query and
reloads the figures, not the page. Days are the browser's time zone, which
is not in the URL: a shared link shows the same range in your own days.

The KPI row (two columns on phones, three on tablets, six on desktop):

- *Known cost* of the range, with the change against the period of equal
  length before it; no change when that period has no known cost or this
  one only calls of unknown cost. Unknown is never $0: such a total shows
  *n/a* (or *not metered*).
- *Month to date* against the same days of last month (the 1st to today's
  day), independent of the range.
- *Calls*, with the subagent calls.
- *Metered tokens* (input, output and cache), in k/M.
- *Cache-read share* and the previous period's.
- *Unknown cost*: the calls without a reported or estimated cost, and the
  calls not metered at all (cursor's model auto).

Below it, once, the token basis note when the range mixes old and new
token bases, and *Research counted since <date>* when research calls are
counted (research cost before that date is only on the research page).
The page says when the history database is not configured (404) or not
connected yet (503), and when a range has no calls. The charts below follow
the [chart rules](#charts).
([Costs page spec](../specs/costs-page.spec.md)).

*Spend over time*: stacked bars of the known cost per bucket (day up to 90
days, week up to 180, month above), a segment per key of the *Group by*
filter: the six keys with the highest known cost in the range, the rest as
*Other*. Calls of unknown cost are not in the bars; a bucket with some says
*+ N unknown calls* in its tooltip and its table row, and a bucket with only
those says *No known cost*. The legend sums each series over the range;
the table view lists every bucket with each series, the total and the
unknown calls. Switching the group refetches and redraws, the page does not
reload.

*This month*: the cumulative known cost of this calendar month up to today
(a solid line, no point after today) against last month's (a dashed, muted
line over all its days), aligned by day of month, so the x axis runs to the
longer month's length and February's line ends at day 28. It does not
follow the range, only the project, kind and engine filters. The legend
gives month to date and the same days of last month, days 1 to today (to
last month's length when today is later), with the change, and names the
dashed line as last month; so does the table view, a row per day with both
cumulative sums (*–* where a line has no point) and the day's calls of
unknown cost in either month. Calls of unknown cost are not in the lines;
the legend counts them per month, up to today and over the same days.

*Where it goes*: horizontal bars of the known cost by project, kind, model
or engine, chosen with a segmented switch (it starts on project and is not
the `group` filter). The top six rows by known cost, then *Other*, which sums
the rest, so the bars equal the breakdown's totals. A row whose calls all
have an unknown cost shows *n/a*, never $0, and every row names its calls of
unknown cost. Each project, kind and engine row, in the legend and the table
view, is a link to the page with that filter set (`breakdownLink` in
`costs.ts`: the other filters stay), and a click on its bar does the same.
Model rows have no filter, and neither do *Other* and the legacy "before
calls" row: they are not links.

*Tokens*: metered tokens per bucket stacked by type (input, output, cache
read, cache write) in k/M, with the cache-read share as a line on a second
axis (0 to 100 %, right; the tooltip shows it as a percentage). The share is
cache read of input, cache read and cache write, the Spend tab's formula, and
has no point for a bucket without tokens; with one bucket it equals the KPI's
share. The legend has the range's totals and the share, the table view a row
per bucket with the calls not metered. Calls that report neither cost nor
tokens are named (*N calls not metered*), never drawn as 0-token bars. A range
that mixes token bases shows the basis note once, in this section.

*Most expensive*: three lists (milestones, issues, runs), up to ten rows each,
ranked by known cost in the range with the page's filters (`getCostsTop`,
fetched in parallel with `getCosts`; if it fails the page stays). A row shows
its label, project, the kind for runs, and the known cost with *+N calls
without cost* when some calls are unknown. A row with only unknown calls shows
*n/a* or *not metered*, never $0.00. The label links into the Spend drill-down
(`topRowLocation` in `costs.ts`, through `spendLocation`): a milestone row to
the milestone tab, an issue row to its issue, a run row to its task and run;
a run without a milestone (research `research/<name>`, chat) opens the project
tab with the scope query.

*Cost per merged line* (`MetricsBreakdown.vue`) and *Context*
(`MetricsContext.vue`): `getCostsMetrics`, fetched in parallel with
`getCosts` (if it fails, both sections are left out). The tables
(`metricsTables` in `metrics.ts`): one arm table per project with arms,
titled with the project's experiment when it still has the arm, then by
model, effort and step session. A `low` row is greyed out with the hint
*fewer than 5 merged issues: too few to compare*; the cost per 100 lines
carries the merged-issue count. The Context panel shows, per project, the
median base context per role and the top five tools by result tokens, as
`DataList` tables, so both stack into cards on phones. The kind and engine
filters do not apply, which the section's subtitle says when one is set.

*Plan usage*: one card per engine with `EngineUsage.vue` (5 h and 7 day
windows), the project the reading came from and its age (`RelTime`). It is
live state (`status.engineUsage`, the freshest reading per engine among the
viewer's projects), not bound to the range or the filters. No chart; an empty
state when no engine has reported.

*Budgets*: the budgets of milestones whose autopilot is not done, the
milestone's own and its issues' (`CostsView.budgets`), sorted by share used.
A row has the title (linking to the milestone or issue page), the project,
spent / limit and a share bar; at 80 % or more the bar uses the warning tone.
Unknown spend reads "unknown" with no bar, never $0. The first 10 show, *Show
all* the rest; an empty state without budgets. Shown only: the page sets and
enforces none. Live state, not bound to the range or the filters.

*Download CSV* (a menu next to the title, shown when the range has calls):
*Series* saves `costs-series-<from>-<to>.csv`, one row per bucket and key
of the current series (columns bucket, group, key, other, known cost USD,
unknown calls, unmetered calls, input, output, cache read and cache write
tokens, calls, duration ms; the *Other* point has an empty key and `other`
true), *Breakdowns* saves `costs-breakdowns-<from>-<to>.csv` (dimension
project, kind, model or engine, key and the same totals). The files are
built in the browser from the figures on the page, for the current filters
(`web/src/costs.ts`): a `Blob` and an `<a download>` through an object
URL that is revoked after the click, so there is no request and the CSP is
unchanged. RFC 4180 quoting; a cell starting with `=`, `+`, `-`, `@`, a tab or
a carriage return gets a leading `'` (project and model names are
user-controlled, a spreadsheet would run them as formulas). An unknown cost
is an empty cell, never 0; the runs from before agent calls are the key
*before calls*. A byte order mark makes spreadsheets read UTF-8.

**Limits of the figures.** They are what the recorded agent calls say, and
nothing else (`agent_calls`; only *Plan usage* and *Budgets* read live state):

- *API-equivalent cost.* Claude's cost is what the CLI reports, cursor's is
  estimated from tokens and the `pricing:` table. It is not an invoice and
  not the subscription's real spend.
- *Unknown is never $0.* Calls without a cost are not in any cost figure or
  bar; they are counted next to it (*+N unknown calls*), and a figure made
  only of them reads *n/a*. Calls that report neither cost nor tokens
  (cursor's model auto) are *unmetered*, counted separately and never drawn
  as 0-token bars.
- *Mixed token bases.* Calls recorded before the token basis was stored do
  not compare with newer ones; a range that mixes them says so once, under
  the KPI row (and in *Tokens*).
- *Research* is counted since its first stored call (*Research counted since
  <date>*; no backfill, older research cost is only on the research page),
  and only for viewers who may read research: for others the calls of kind
  `research` are not in any figure, and the date is not shown.
- *Buckets* follow the span: day up to 90 days, week (from Monday) up to
  180, month above, in the viewer's time zone (`tz`, sent by the browser, so
  a day edge is local midnight, also across a DST change).
- *Retention.* Calls expire with `research.historyRetention` (a year by
  default); a range starts no earlier than today minus the retention (a named
  range is cut there, a custom `from` before it is refused).
- *Chunk budget.* The `/costs` route chunk, Chart.js included, stays within
  120 kB gzipped, checked by `npm run budget` ([Bundle budget](#bundle-budget)).
  Final figure 117.1 kB; the initial bundle is 129.1 kB.

The e2e route cases `costs`, `costs-dark` and `costs-filtered` (`?range=90d&group=model&kind=implement`) at 375, 768 and 1280 px
(`costs@<width>.png`, `costs-dark@<width>.png`) wait for both charts'
canvases and check overflow and the CSP, `costs` is among the hit-area routes,
`costs filters live in the URL, no reload@1280` checks that a filter change
updates the URL and refetches without a reload, and `costs Spend over time
legend, table and colours@375` and `@1280` check the canvas's summary, the
legend's totals, the table view's figures and that the bars are drawn in the
token colours of light and then dark. `costs This month legend and
table@375` and `@1280` check the summary, the legend's month to date and
same days with the change, the dashed swatch, the table's named columns
and its last-month-only days, and that both lines are drawn.

### Research page

A research's page and a run's page (`web/src/views/Research.vue`,
`ResearchRun.vue`), the result they share (`components/ResearchResult.vue`)
and its blocks (`components/blocks/*`) are Tailwind utilities and tokens,
without scoped CSS.

- The header's rows stack on phones: the name, the view and depth badges,
  *Delete* (a `UButton`), the prompt (block markdown, three lines until
  *Show the whole prompt*), then the schedule, agent, last and next run and
  *Run history* one per line, and the feeds. *Delete*, *Show the whole
  prompt*, *Run history*, the feed links and the result's run line are
  44 px high on phones.
- A run's facts (agent, start, duration, spend, retention) and a stats
  block's figures sit two to a row on phones and wrap instead of being cut
  short, so no hover title is needed.
- A table block keeps its columns at every width, each at least 8rem wide;
  on a phone it scrolls sideways inside its own card, never the page. Long
  cells wrap.
- Items and links stack: an item's title wraps and its badges follow it;
  a link's host goes below it on phones. Item titles and links are 44 px
  high there. Markdown blocks and item summaries stay block markdown.

The e2e cases `research pages › research-report blocks@<width>`,
`research pages › research-run blocks@<width>` and `research pages ›
research news flash@<width>` at 375, 768 and 1280 px render a report with
every block type and the news flash, check that neither the page nor the
content pane scrolls sideways, at 375 px that the table's card does, the
figures stack and the header's and the blocks' links and buttons are 44 px
high, and at 1280 px that the table fits and the figures are one row
(`research-report@<width>.png`, `research-run@<width>.png`,
`research-news@<width>.png`). The three pages are also among the hit-area
routes.

### Research list and form

The research list and the one-off form (`web/src/views/Researches.vue`,
`ResearchNew.vue`) are Tailwind utilities and Nuxt UI, without scoped CSS.

- The list is one card per research at every width. The name's link covers
  the card; its lines wrap on phones (the badges go below the name). The
  last run's reason, a hover title before, is a line of its own.
- The form's fields are `UFormField` with `UTextarea`, `UInput` and
  `USelect`; *Depth* stays two native radios whose labels are 44 px high on
  phones. Inputs are 16 px and controls 44 px high on phones, 14 px from a
  tablet on; the selects are as wide as the form on phones. The question's
  counter sits beside its label. *Effort* (Claude models only) is a field
  of its own next to *Agent*. A 400 with a `field` the form knows
  (`prompt`, `depth`, `view`, `topics`, `project`, `agent`) shows on that
  field and marks its control invalid; any other error shows beside
  *Start research*.

The e2e cases `research list and form › research list@<width>`, `… ›
research form submits@<width>` and `… › research form field error@<width>`
at 375, 768 and 1280 px check that neither page scrolls sideways, that a
tap on a card opens its research, that the form posts every choice (depth,
view, topics, agent and effort) and opens the new research, and that a 400
`{error, field: "topics"}` shows on the Topics field only; at 375 px that
*New research* and the form's controls are 44 px high and its inputs 16 px
(`research-list@<width>.png`, `research-new@<width>.png`,
`research-new-error@<width>.png`). Both pages are also among the hit-area
routes (the Depth radios are allowlisted: their labels are the hit area),
and the form's known overflow at 375 px is gone.

## Desk behaviour in detail

What the Desk does under the hood, for each view the user guides
[The Desk](../guides/desk.md) and [Steer from the Desk](../guides/steer-from-the-desk.md)
describe in plain words. The endpoints are in [design.md](design.md#desk-api).

### Routes and tabs

- **Dashboard** (`/`): *Overview* (default) and *Stream* (`/?tab=stream`).
  The summary strip's spend is the UTC day and the last 7 days. *Recently
  done* shows the newest 10, *Show all* loads up to 200. A milestone row's
  cost is a dash when unknown, never $0. A user with research alone starts
  at `/research`.
- **Project** (`/projects/<namespace>/<name>`) and **milestone**
  (`…/milestones/<iid>`): the view tab is kept in `?tab=`; the default
  (*Stream* on a project, *Issues* on a milestone) is left out. A project
  has *Stream | Board | Spend*, plus *Plans* with the feature `plans`
  (`?tab=plans` without it opens *Stream*); a milestone has no *Plans*. Old
  `…/milestones/3?tab=stream` links still open the stream; a milestone link
  with a `#prompt-Q<n>` hash and no `?tab=` opens *Pilot*. The view tabs are
  a [RouteTabs](#routetabs) strip. Scope tabs keep a chosen view tab where
  the target has it and open its default otherwise.
- **Issue page** (`…/milestones/<iid>/issues/<issue>`): the *Task* section
  holds the task page's tabs by `?tab=`; its stream is `GET /api/v1/stream`
  with `issue=`. It reads the snapshot and history, so a closed milestone's
  issue opens after its tasks are gone.
- **Task page**: an old `#live-transcript` link without `?tab=` opens
  *Live*. Only the open tab is loaded.
- The Desk has no projects list or separate board and milestone pages; the
  old paths show Not found. Task and research pages keep their URLs.
- **Stored milestones.** With the research database a closed milestone whose
  tasks are gone answers from the snapshot (`stored`): no *Board* tab, no
  message box, the *Pilot* tab shows only events and pauses. Milestones
  closed before the snapshot existed are filled in once by the operator's
  backfill (see [Research internals](research.md#database)): issues with states
  and merge requests and the retrospective's text, but no leftovers and only
  the spend `agent_calls` still holds.

### Which events show where

Every event kind belongs to one of a milestone's two event tabs, never both
(`pilotKinds` and `runtimeKinds` in `internal/ui/stream.go`, mirrored in
`web/src/stream.ts`):

- *Pilot*: `prompt.posted`, `prompt.answered`, `prompt.closed`, `needs`,
  `autopilot`, `autopilot.paused`, `autopilot.resumed`, `proposal`,
  `pilot.writeFailed`, `note`, `chat.user`, `chat.agent`,
  `release.decided`, `release.started`, `release.ended` and
  `retrospective`.
- *Stream*: `run.started`, `run.finished`, `push`, `checkpoint` (a budget
  stop, a lost pod or an OOM kill; under the *Runs* chip), `ci`, `review`,
  `merged` and `closed`.

A project's *Stream* tab and the dashboard's show all kinds, and every kind
can be asked for with `kinds=`. A `prompt.posted` event is one line,
"Q12 asked: …", with the prompt's state badge while the milestone view
still has it, and a link to its card; no stream row has answer buttons. The bot's mirror comments
(`ai-operator:prompt` and its answer reply) are hidden from the note
threads, so a prompt shows once.

### Context panel and pause history

The context panel (from 1180 px, project pages only) shows the project's
needs-you items, the active milestone's queue (a milestone waiting for its
turn says *⏳ Waiting for its turn: %7, %9 go first*, from
`autopilot.queuedBehind`; it is running, not paused, so it has no pause card
and no Inbox item) and the last 30 days against the 30 before. Below
1180 px the header's *Context* button opens it as a [sheet](#sheets); its
count is the panel's own, so neither loads anything more.

The milestone API carries the **pause history** (`pauses`, from the
`autopilot.paused`/`autopilot.resumed` events within the history
retention): the newest 20 pauses with kind, reason, start, end, who resolved
each and how (`via`), an open one counting up to now, plus totals (time
paused, per kind, mean time to resume, per resolver and per way it ended).
It is absent without the history database.

### Cost chips

`$12.34 · 1.2M tok` on the stream header, each milestone scope tab and the
Task page header. The tooltip splits input, output and cache tokens, calls,
calls without a known cost and the part from runs recorded before agent
calls. With the research database they sum the recorded agent calls and
older runs, so deleted tasks still count; a cost no call reports (cursor
without pricing) shows *n/a*. Without the database they sum the cost of the
current tasks, without tokens.

### Navigation

Breadcrumbs are built from the URL: *Overview* › project › milestone ›
issue, *Overview* › project › *Plans* › plan, *Research* › research ›
*Runs* › run. A task page sits under its issue page; a task without an
issue under the project. The Inbox, Costs, Plans (`/plans`), the research
list and *Not found* show one crumb. Every crumb but the last links to that
level's default tab, but a plan's *Plans*, which opens the project's Plans
tab. Crumbs show `%<iid>`, `#<iid>` or the task's name until the
view has loaded titles; they cost no extra request. On narrow screens the
middle crumbs fold behind `…`. The Spend tab keeps its own drill-down
crumbs.

The phone's bottom bar (`App.vue`, under 760 px) is 56 px high; the item
of the section shown has `aria-current="page"`. It keeps clear of the
bottom safe-area inset, and the content pane has as much bottom padding.
While a text field has the focus (`src/textEntry.ts`) the bar and the
padding go. ⌘K or Ctrl+K opens the [palette](#search-palette) from
anywhere, also inside a text field.

The **about line** comes from `GET /api/v1/about`, loaded with the features
and refreshed every 5 minutes while the tab is visible; an error keeps the
last answer. It links the running version's GitLab release (the releases
page for a `dev` build, no link without `--self-project`). A newer self
release that is *Waiting* or *Running* adds *Update to X rolling out*; a
*Released* one *X is out — reload*. A `dev` build shows no note.

### Queue and Finishing

The project detail (`GET /api/v1/projects/{namespace}/{name}`) carries
`project.queue` and `project.finishing`, from the same ranking and close-out
fields the operator uses (`internal/milestoneorder`, autopilot status), so
the Desk and `pick` cannot disagree. Queue states: `running`, `holding`
(*Holding for %N*), `waiting`, `paused`, `budget`. With Desk chat and the
`chat` grant (`project.queueCommands`) the rows post `kind: "command"` to
the milestone's Spec task: *Run next* `{"cmd":"start"}`, *Clear mark*
`{"cmd":"unstart"}` (both owner subjects only, like `/agent start` and
`/agent start off`), *Pause* / *Resume* `{"cmd":"pause"}` /
`{"cmd":"resume"}`.

Finishing lists done autopilot milestones whose Spec task still exists,
oldest first, with what holds the close (`closeMilestone`): *Re-planning*,
*Retrospective being written*, *Follow-ups to settle*, each open issue of
`CloseWaitsFor` and *Companions not landed*. *Close issue* (`steer` grant,
an owner subject, GitLab connected) posts `POST
…/milestones/{iid}/issues/{issue}/close` with the user's token: a note
"Closed from the Desk by @\<you\> to finish %\<iid\>." then the close.
`needsEstimate` selects unstarted queue milestones (`waiting` / `holding`
with no spend) for `GET …/estimate?milestone=`.

### Planner

Plans are `AgentPlan` resources (feature `plans`, needs
`ui.chat.writerSecret`). Writes (create, chat, edit, raise, create the
milestone, delete) need the `steer` grant and the caller among the owner
subjects of the project's live Spec tasks (the grant alone when none has
any). Anyone who may see the project may list and read plans; bodies need
`conversation`. The plan page polls every 5 s while visible. Chat is
`GET`/`POST …/plans/{plan}/chat`, messages live in Postgres
(`chat_messages.plan`). An owner edit is `PUT …/draft` (a new revision,
`draftBy: owner`); the next turn tells the agent "the owner edited the
draft; it is now: …". Calls are kind `plan`. Raising the cap is `POST
…/budget` up to `plan.maxBudgetUSDLimit`. *Create milestone* uses the
estimate of `GET …/estimate?plan=` (*not enough history* below three
finished milestones) and `POST …/milestone` with the owner's GitLab token;
the plan records the IID, its pod stops, and creating again is refused;
audit line `plan.milestone`. The plan's title is `status.title`, else
`spec.title`. The plan pod may only patch the status, so the agent's title
goes there: while both are empty, each turn's prompt has a `## Title`
section and the turn schema's `title` (first line, trimmed, at most 255
runes) is written once; an interrupted turn writes none. *Create milestone*
then sets `status.title` to the milestone's title in the patch that records
the IID; a GitLab failure leaves it. So a `spec.title` given on create
wins until the milestone settles the title, and *Plan milestone* sends
none. The plan lists (`GET /api/v1/plans`, see [Plans](#plans)) link a
created milestone to its Desk page with the dashboard's state and
progress, or, while the Desk has no row for it, to GitLab with *Not
planned*. After `plan.idleTimeout` the pod ends (phase
*Idle*); the workspace stays and the next message resumes the engine
session. Values: [chart README](../../charts/ai-operator/README.md#plan).

### Agent calls

The *Calls* tab reads the spend endpoint with `task=`, a run's calls with
`run=`. *Lines* (`+A/−D`, implementer kinds implement, fix, ci and
conflict) is the uncommitted diff the call left (`git diff --numstat HEAD`
plus untracked text files). `· N gate blocks` counts read-gate blocks,
`· N step-gate nudges` the nudges of `implement.maxStepLines`. A 💾 badge
marks a call whose end saved a checkpoint commit; the totals also count the
checkpoints saved without a call (`budget-stop`, `pod-lost`, `oom-kill`).
Without the research database (404 or 503) the section is left out.

The **context-growth chart** above an expanded run's calls plots the
context tokens (input + cache) of each main-thread turn, one line per
implementer step on one shared scale, fainter for older steps; an orange
dot marks a turn after which a subagent ran. A series is at most 500
points, spread over the call's real turn count. Cursor calls plot their ACP
`usage_update` context sizes; calls without them and runs recorded before
the series was tracked show no chart.

### Notifications

Only some Inbox items notify (`notifiable` in `inboxNotify.ts`): pauses,
proposals, prompts, ready companions, budget warnings and tasks that need
input. Failed and blocked tasks and rejected pilot writes (`pilotError`)
only count. Each such item notifies once, also across reloads and live updates; items
already there when the tab opens or when notifications are turned on don't
notify. An autopilot that pauses again for another cause or issue is a new
item, as is an issue whose raised budget crosses 80 % again. A click leads
where the Inbox row does (one mapping, `inboxRoute.ts`). There is no
service worker, no Web Push and no e-mail. The choice is kept per browser.

### History

The operator appends the factory's events to the research database: task
status changes (a run started or finished with result, spend and duration,
a push, a final CI result or fix round, a review with its blocking
findings, a question, failure or block, a merge, autopilot phase changes,
the pilot's proposals, a task closed) and every comment it reads on issues
and merge requests, its own included. Each event is written once and kept
for `research.historyRetention` (a year by default). What happened while
the operator was down is not recorded (comments are caught up when read
again), and an edited comment keeps its first version.

### Chat commands

Desk chat posts `kind: "command"` with a JSON body; the thread renders a
short label and nests the operator's answer under it. Each command goes to
the task that owns it:

| Task | Commands |
|------|----------|
| Implement | `retry`, `review`, `use`, `effort`, `answer` (to `needsInput` prompts, owners), `restart` (owners) |
| autopilot Spec | `pause`, `resume` (`"issue":N` lifts one hold) and `skip` (chat grant); `merge`, `parallel`, `start`, `unstart`, `endmilestone` and `restart` (also the milestone's `owner-subjects`) |
| Coordinate (pilot) | `accept`, `reject`, `answer`, `revisit`, `draft` and `restart`, all only for the milestone's `owner-subjects`; plain messages need the chat grant only |

| Command | Effect |
|---------|--------|
| `{"cmd":"answer","prompt":"Q5","option":"B"}` | Choose option B of prompt Q5: its actions run (checked again by the operator). |
| `{"cmd":"answer","prompt":"Q5","text":"…"}` | Your own words (at most 4000 bytes). On an action prompt the pilot adds the option "From your answer: …" to confirm; on an agent's question the text is the answer. |
| `{"cmd":"revisit","decision":"D42-3","text":"…"}` | Revisit a decision with that instruction. Without `text`: opens the `revisit` prompt. |
| `{"cmd":"draft","followUps":[1,3]}` | Ask the pilot of a done milestone for a draft of a follow-up milestone from those leftovers (1 to 9); it lands in `retrospective.draft`, untrusted until an owner confirms it. |

An explicit prompt id is always required, and a prompt takes one answer.
Sends are retry-safe (`clientKey` in session storage until a 2xx). Chat
events appear in the stream when the `chat` feature is on (keyed apart from
history events). Delivery needs `agents.chat: true` on the AgentProject;
without it the operator marks the row failed.

### Pause card actions

The *Way out* section lists the `exits` of the milestone API (the
operator's `PauseExits` table). Automatic exits come first with a status
chip: *lifts on the next poll*, *in progress*, *waiting* or *not known*.
A remedy the API marks `allowed` for the viewer (`resume`, `skip`, `retry`,
`review` with the chat grant, `merge` with `permissions.merge`) is a button
sending the same chat command; other commands (`rebase`, `mergeBase`,
`unskip`, `startAnyway`, `endMilestone`, `restart`, or any without the
grant) show as the command text. With the section the card drops its
*Continues by itself* line and its action row keeps only links.

Against an operator that sends no exits, the actions depend on the kind:

| Kind | Actions |
|------|---------|
| `reviewErrors`, `notReviewed` | Review again, Merge anyway, Skip |
| `findings`, `runFailed`, `needsInput` | Retry, Open #N chat, Skip |
| `ciRed`, `ciCanceled` | Retry, Open pipeline, Skip |
| `mergeRequest` | Open MR, Resume, Skip |
| `companion` | Open companion MR, Resume, Skip |
| `cycle`, `stuck` | Open plan, Skip |
| `owner`, `mainRed`, `config`, no kind | Resume |
| `budget`, `emptyPlan` | none |
| `release` | Open pipeline, Resume |
| `stalled` | Skip |

Retry and Review again go to the paused issue's Implement task; Resume,
Skip and Merge anyway to the Spec task. *Merge anyway* names the head the
card shows, so the operator refuses it after a newer push. A hold card is
the same component with a `hold` and always offers *Resume*
(`{"cmd":"resume","issue":N}`), even for kinds whose pause card has none.

### Live transcript

The UI reads the pod's logs through the Kubernetes API and passes on only
known kinds of lines of the current run, with a fixed set of fields, never
a raw log line. A restarted container's log is followed again. Each new run
opens a new stream. The lines are what the containers logged, not signed,
and are shown as text only. The chart lets the UI's service account read
`pods` and `pods/log` in each namespace of `agent.namespaces` (Roles,
nothing cluster-wide); a project elsewhere gets "pod not available".
Prompts (`spec.agents.logPrompts`) are cut at 64 KiB each.

### GitLab steering

- **Thread:** read with the user's token, so GitLab decides what they see;
  read again every 30 seconds and after every change of the task.
- **Comments:** at most 10 000 characters and 10 per minute per UI replica.
  The UI logs user, GitLab user, project, target and note id, never the
  text. A comment goes to an MR only if its source branch is the task
  issue's work branch, and to a tracking issue only if it is the
  operator's tracking issue of the task's milestone.
- **Pilot prefix:** the UI puts `workflow.pilotPrefix` (`/pilot`) in front
  unless the message starts with it, so `/agent pause` arrives as `/pilot
  /agent pause`.
- **Follow-up milestone:** title (one line, at most 255 characters) and
  description (at most 64 KiB), then the project's markers and the
  source's `owners`, `owner-subjects` and `followup-of` lines (dropped from
  the user's text). The operator marks the leftovers taken; the UI writes
  nothing to the database. The fixed token needs the project's Planner or
  Reporter role to create milestones.
- **Tokens:** access and refresh token in an encrypted, `HttpOnly` cookie
  bound to the UI user, refreshed when the access token runs out, never
  readable by JavaScript; the connection ends 30 days after it was made.
- The `gitlab-client-secret` key must exist in the Secret, or the UI pod
  does not start. Projects on another host than `ui.gitlab.url` get no
  thread and no box.

### One-off researches

- **Namespace.** Created in `ui.research.namespace` only; the request has
  no namespace field. The chart grants the UI `create`, `delete` and `get`
  on AgentResearches there only. Without `allowCreate` the chart renders no
  write Role and the endpoints answer 404.
- **Names:** `q-<yyyymmdd>-<6 random characters>`.
- **Creator:** stamped as `ai.wiemers.me/created-by: <user name>`; only
  that user may delete it.
- **Projects:** runs with an AgentProject of that namespace that has
  `spec.research.writerSecret`.
- **Limits:** at most 32 KiB per request, a question of 1 to 16384
  characters, at most 20 topics of up to 100 characters, 10 researches per
  user and hour (then 429 with `Retry-After`).
- **Audit:** `"msg"="audit" "action"="research.create"` (or
  `research.delete`) with user, namespace, name, depth and agent, never the
  question.

### Research results and markdown

Each block type has its own component; unknown types are skipped. Agent
text is rendered by the Desk's own subset renderer (`web/src/markdown.ts`,
component `Markdown.vue`) straight into page elements, never through HTML:
paragraphs, headings, lists and task lists, blockquotes, tables, rules,
`<details>`/`<summary>`, emphasis, strikethrough, code, entities, the
attribute-free inline tags `code`, `kbd`, `sub`, `sup`, `b`, `strong`, `i`,
`em`, `s`, `del` and `br`, and links (`http(s)` only, new tab,
`rel="noopener noreferrer nofollow"`). Other HTML shows as typed, HTML
comments are dropped, images are never loaded. `mode="block"` renders run
summaries, notes, research summaries, prompt text, pause and release
reasons, transcript lines and research prompts; `mode="inline"` renders one
line for rows that are links already. Machine strings (condition messages,
exit reasons, API errors, thinking lines) stay plain text. Full-text search
over results is in the API only (`GET /api/v1/research/search`).

### Security

- **Identity.** The UI's service account may `get`, `list` and `watch`
  AgentProjects, AgentTasks and AgentResearches and read pods and their
  logs in the agent namespaces. No Secrets, no GitLab token; the research
  database only with the read-only reader login. The API returns views
  without the operator's bookkeeping and secret references. Its only write
  access, with `ui.research.allowCreate`, is `create`/`delete` on
  AgentResearches in `ui.research.namespace`.
- **Login.** Authorization code flow with PKCE, `state` and `nonce`; the ID
  token's signature, issuer, audience and expiry are verified. The session
  cookie holds only the user name, the roles and the expiry; the refresh
  token is only in the sealed refresh cookie, never logged or shown to the
  web app, and revoked at logout. See [Desk session model](design.md#desk-session-model).
- **Sessions** are AES-GCM encrypted cookies (`__Host-` prefix, `HttpOnly`,
  `Secure`, `SameSite=Lax`). A copied cookie stays valid until it expires;
  changing the session key ends all sessions. Logout ends the session at
  the provider if it supports RP-initiated logout.
- **Redirects.** After the login the browser only goes back to a path on
  the UI's own host; login errors reach the sign-in page as codes.
- **Signing in again.** On a 401 the Desk goes to
  `/oauth2/login?return=<current route>` once (`signInAgain.ts`): a
  `sessionStorage` marker stops another attempt for 60 seconds, and a
  `login_error` or a browser without `sessionStorage` stops it too. Unsent
  composer text is kept in `sessionStorage`
  (`desk-sign-in-draft:<route>:<composer>`) and put back when the login
  returns.
- **Browser protections.** A strict Content Security Policy (only the UI's
  own scripts, styles and API, no framing), HSTS, `nosniff`, and Go's
  cross-origin protection for requests that change state. Links to GitLab
  must be `http(s)` URLs.
- **Research results** are validated against the block schema again before
  they are sent.
- **https only.** `externalURL` must use https (http only for `localhost`);
  TLS ends at the Gateway.
- **Writing only as the user**, with their OAuth token (scope `api`) or
  fixed fine-grained token, never a shared or the bot's token. A stolen
  pair of cookies could post as that user until the GitLab connection
  expires or is revoked.
- **Without login** (`auth.mode: none`) the UI listens on localhost inside
  its pod only and answers only requests for a localhost host name (421
  otherwise), so a web page that points a DNS name at 127.0.0.1 (DNS
  rebinding) cannot use it.

## Roadmap

1. **Into a running step.** Messages that reach a running claude step at once
   (`--input-format stream-json`), not only at its next step.
2. **Actions** for an `operator` access level, limited to what GitLab
   commands and the Desk's buttons cannot do today (e.g. clear a usage
   limit, hibernate a task). *Restart run* has shipped.
3. **MCP** at `/mcp` on the same host, with tools on top of the same API and
   access rules. MCP clients authenticate with bearer tokens issued by the
   same provider (the UI then acts as an OAuth resource server and publishes
   `/.well-known/oauth-protected-resource`); the roles in those tokens go
   through the same claim mapping and rules.

### Conventions check

`web/test/conventions.test.mjs` (part of `npm test`, so `make ui`) walks
`web/src` (not `generated/`) and checks four rules, listed in the `RULES`
table at its top; the matchers are in `web/test/conventions.mjs`:

- `html-injection`: `v-html`, `innerHTML`, `outerHTML`, `insertAdjacentHTML`.
  **Enforced**: any hit fails the test.
- `raw-colour`: `#rgb`, `#rrggbb`, `#rrggbbaa`, `rgb(`, `rgba(`, `hsl(`,
  `hsla(`, `oklch(` outside `src/style.css`. **Enforced.** Use a token
  (`--on-accent` for text on the accent colour).
- `api-call-outside-client`: `get<`/`post<` and calls of the `get`/`post`
  helpers imported from `api.ts`, outside `src/client.ts`. **Enforced.**
- `breakpoint`: `@media` widths other than the three widths of
  [design.md](design.md#desk-styles): `min-width` 760 or 1180 px,
  `max-width` 759.98 or 1179.98 px, nothing else in px. **Enforced.**

All four rules are enforced: any hit fails `npm test`. A rule with
`enforce: false` in its `RULES` entry would only print each hit as
`file:line rule` (as a test diagnostic) with a count, and pass. Comments are
ignored.
