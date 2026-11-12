# Desk: one Costs page across all projects, with charts

Status: the milestone's spec, run by autopilot. This copy is kept in sync
with each slice (R18); narrowed requirements and requirements without an
issue go into the [Steering log](#steering-log).

## Goal

1. One Desk page, `/costs`, shows what agents cost across every project the
   viewer may see, and research, over time.
2. Charts answer the owner's questions at a glance: how much this month and
   compared to last, which project, kind of work and model drive it, and
   whether caching and step size are paying off.
3. Every figure leads to the existing Spend drill-down (milestone, issue,
   run, call) instead of repeating it.
4. Research runs cost is kept as long as agent work cost, not 30 days.

## Background

File refs are at origin/main 8cf828c, 2026-11-11. This milestone runs after
"Desk: works on a phone, markdown everywhere, one UI kit, one definition per
type" (%17) and builds on its kit (Nuxt UI), `web/src/client.ts` (#338 to
#340), the `Money` JSON type (#341) and R1's Playwright checks.

What exists:

- Cost is per scope only. `GET /api/spend` (`internal/ui/spend.go:156`)
  requires `namespace` and `project` and answers a project, milestone, issue,
  task or run, with children and breakdowns by kind, role, model, review
  round, session (fresh/resumed), checkpoint and subagents. The Spend tab
  (`SpendPanel.vue`, 698 lines) renders it per project.
- Across projects there are only two chips: `SpendToday` and `Spend7d` on
  the dashboard (`internal/ui/overview.go:93`, `store.SpendSince`), and the
  Projects list's per-project totals (`store.SpendByProject`).
- Over time there is only the context panel's "Last 30 days" sparkline per
  project (`ContextPanel.vue:118`, `GET .../history`, `store.Days`), built
  from `run.finished` events, not `agent_calls`; it has runs and cost, no
  tokens, kinds or models.
- `agent_calls` (`migrations/0004_agent_calls.sql`) has one row per call with
  `at`, project, milestone, issue, task, run, kind, role, engine, model,
  tokens, `cost_usd` (NULL when unknown), `cost_source`, `models` (per-model
  split, subagents included), `duration_ms` and `token_basis`. Rows expire
  with the history retention (`historyRetention: 8760h`, one year). Indexes
  are by milestone, issue and task; none by time, so a range over all
  projects scans every project's rows.
- Call kinds (`api/v1alpha1`, `CallKind*`): spec, implement, review, fix, ci,
  conflict, describe, pilot, retro, followUpDraft, chat, research.
- Research is the exception. `RunResearch` (`internal/runner/research.go:170`)
  writes no `agent_calls` row: its cost lands only in `research_runs.cost_usd`
  (`store.FinishRun`), without tokens, and research runs expire with the
  research's own retention (sample: 720h). After 30 days their cost is gone.
- Engine usage windows (`EngineUsage.vue`, 5 h and 7 days per engine) are
  shown per run, nowhere as a whole.
- Charts are hand-written SVG (`contextChart.ts`, `ContextPanel.vue`,
  `ModelBar.vue`). No chart library is installed. The CSP is `style-src
  'self'` with no `unsafe-inline` (`internal/ui/server.go:263`).
- Claude's cost is what the CLI reports, API-equivalent; cursor's is
  estimated from tokens and the `pricing:` table, else unknown (never $0).
  Calls with neither cost nor tokens are "unmetered" (cursor `auto`).

From the retrospectives of %13 and %15:

- Mixed token bases and unmetered calls must be visible where figures are
  compared (`basisNote`, `unmeteredNote` in `web/src/spend.ts`).
- Cross-layer slices were the expensive ones. Store, API and web stay apart.

## Decisions (locked)

1. **One page, `/costs`**, in the rail and the phone bar's Projects sheet
   (the bottom bar keeps its four entries). The dashboard's today and 7-day
   chips link to it. The project Spend tab links to it filtered to the
   project, and the Costs page links back into the Spend drill-down.
2. **Scope is the viewer's projects.** Exactly the projects the overview
   counts (`CanView`), plus research runs when the research feature is on
   and the viewer may see research. Never another tenant's figures, not even
   as an "other" total.
3. **One source for money: `agent_calls`.** Cost and token figures read
   nothing from CRDs or history events, except the legacy runs `spendWith`
   already folds in (shown as the kind "before calls", no model, no tokens).
   Only *Plan usage* (R14) and *Budgets* (R15) read live state, as the
   overview does.
4. **Research writes calls.** A finished research run writes one
   `agent_calls` row: kind `research`, `project` empty, `task`
   `research/<name>`, `run` the run's number, tokens and model when the engine
   reports them, the history retention. `research_runs.cost_usd` stays for
   the research page. No backfill: older research cost is only on the
   research page, and the Costs page says from which date research is
   counted.
5. **Server sums, the browser draws.** Postgres buckets and groups;
   responses carry at most a few hundred points. Buckets: day for ranges up
   to 90 days, week up to 180, month above, in the viewer's time zone
   (`tz`, as `history` does). Ranges: 7, 30, 90 days, this month, last
   month, 12 months; at most the history retention.
6. **Unknown is never $0.** A bucket or row with unknown calls shows its
   known cost and marks the rest, as the Spend tab does. Unmetered calls are
   counted separately. Cost charts show known cost; tokens charts show
   metered tokens. A range that mixes token bases says so once (%15).
7. **Charts use Chart.js 4** (MIT, canvas), pinned to an exact version as
   `web/package.json` does, with `vue-chartjs`. Only the controllers,
   elements, scales and plugins used are registered (tree-shaken). It is
   loaded with the `/costs` route only, never in the initial bundle. Why
   this library: it draws on a canvas and injects no `<style>` element, so
   the CSP stays as it is; libraries that inject styles (ApexCharts, unovis
   through its CSS-in-JS) are refused by `style-src 'self'`, and ECharts is
   too large for the budget. One wrapper per chart type in
   `web/src/ui/charts/` (stacked bars, line with a dashed comparison line,
   horizontal bars) keeps the Chart.js options in one place. Shaping data
   (buckets to datasets, top N plus *Other*, bucket labels per time zone,
   colours from tokens) is a pure `.ts` module with tests; the wrappers only
   pass it on. If the library needs a CSP change or breaks the chunk budget
   (decision 15), stop and report instead of loosening either.
8. **Chart rules.** At most six series plus *Other*. Colours come from the
   tokens in `style.css`, read at runtime and re-read when the colour scheme
   changes (light and dark). Never colour alone: a legend with values and a
   table view under each chart, which also serves screen readers, since a
   canvas has no text. Tooltips on hover and tap. Readable at 375 px (fewer
   ticks, legend below, the canvas resizes with its container). No
   animations. Costs are USD with two decimals; tokens in k/M.
9. **Filters live in the URL:** `?range=&from=&to=&group=&project=&kind=&engine=`.
   A shared link shows the same page. `group` is one of project, kind,
   model, engine.
10. **Group by model** splits each call by its `models` entries (subagents
    count to their own model), as `ByModel` does today.
11. **Top lists rank by known cost** in the range: milestones, issues and
    runs (a chat or research run counts as a run). Each row names its
    project and links to the Spend drill-down. Ten rows each.
12. **One API route per page part is not needed:** `GET /api/costs` returns
    the KPIs, the series for the chosen grouping and the breakdowns;
    `GET /api/costs/top` the top lists. Named structs, generated types, one
    function each in `client.ts`.
13. **A time index.** A migration adds `agent_calls (namespace, project, at)`.
    The page's queries must use it (checked with `EXPLAIN` in a store test on
    the test database).
14. **No new budgets.** The page shows the budgets that exist (milestone and
    per-issue) as they are used; it sets none and enforces none.
15. **Chunk budget.** The `/costs` route chunk, Chart.js included, is at most
    120 kB gzipped, checked in CI next to %17's initial-bundle check. The
    first chart slice records the real figure; the cap drops to that plus
    30 % if that is lower. The initial bundle does not grow by more than the
    route entry and the client functions.

## Requirements

Store

- R1 Migration: the time index (decision 13).
- R2 `CostSeries(projects, from, to, bucket, tz, group, filters)`: known
  cost, unknown and unmetered calls, tokens by type, calls and duration per
  bucket and group key, legacy runs included (decision 3). Tests for bucket
  edges in a non-UTC zone across a DST change, unknown and unmetered calls,
  the group by model split, an empty range and a project outside the list.
- R3 `CostBreakdowns(projects, from, to, filters)`: totals by project, kind,
  model and engine, cache-read share, step lines (as `StepLines`), with the
  totals of the previous period of equal length.
- R4 `CostTop(projects, from, to, filters, n)`: top milestones, issues and
  runs with project, label and known cost.

Research

- R5 The runner reports a research run's usage and model; the operator writes
  its `agent_calls` row on finish, idempotent by key (decision 4). Test that
  a run seen twice is one row and that a run without usage has an unknown
  cost, not $0.

API

- R6 `GET /api/costs` and `GET /api/costs/top` (decision 12) over the
  viewer's projects (decision 2), with parameter validation as `/api/spend`,
  503 when the history database is not connected, 404 without the research
  feature. Tests: a viewer sees only their projects; research appears only
  when allowed; bad ranges are refused.

Web

- R7 Route `/costs`, rail entry, Projects sheet entry, links from the
  dashboard chips and the Spend tab (decision 1), filters in the URL
  (decision 9). KPI row: this range's known cost with the change against the
  previous period, month to date, calls, tokens, cache-read share, unknown
  and unmetered calls. No charts yet.
- R8 Chart.js set up (decision 7): the three wrappers, token colours with
  dark mode, the table view, the chunk check (decision 15) and a Playwright
  run of `/costs` with the real CSP header showing no violation. Tests of the
  pure module: datasets from buckets, top N plus Other, bucket labels per time
  zone, colours assigned stably per key across reloads.
- R9 *Spend over time*: stacked bars per bucket by the chosen group, with a
  table view.
- R10 *This month*: cumulative month-to-date line with last month dashed
  over the same days.
- R11 *Where it goes*: horizontal bars by project, kind, model and engine,
  each row a filter link.
- R12 *Tokens*: stacked input, output, cache read and cache write per bucket,
  with the cache-read share as a line.
- R13 *Most expensive*: the three top lists (decision 11) linking into the
  Spend drill-down.
- R14 *Plan usage*: per engine, the freshest usage windows among the visible
  projects' `status.engineUsage` (`agentproject_types.go:1337`), with their
  age, drawn with `EngineUsage.vue`.
- R15 *Budgets*: open milestones with a budget, used and limit, sorted by
  share used, linking to the milestone.
- R16 Download the current series and breakdowns as CSV, built in the
  browser from the response.

Checks and docs

- R17 Playwright (from %17 R1) adds `/costs` at 375, 768 and 1280 px on
  fixtures with several projects, research and unknown calls: no sideways
  scroll, no CSP violation, screenshots as artifacts.
- R18 `docs/ui.md` (the page, its figures and their limits),
  `docs/design.md` (research calls, the index) and the spec copy
  `docs/specs/costs-page.spec.md` with each slice.

## Constraints

- Starts after %17; strict order holds it until then. Use the kit, the client
  and `Money`; add no second way of doing what %17 unified.
- Store, runner/operator, API and web changes are separate slices. Groundwork
  first: R1 to R6 before the charts.
- The default budget is under 50 production lines per MR; one chart per MR.
- The bundle budget of %17 (decision 5) holds; `/costs` is lazy-loaded and its
  chunk is reported in each web MR (decision 15).
- The CSP does not change (`internal/ui/server.go:263`).
- A decision that narrows a requirement opens a follow-up issue at that moment.
  Every R has an issue, or a line in the steering log saying why not.
- UI MRs link R17's screenshots at phone and desktop width.
- `make ui`, `make ts-types`, the `drift` job and the conventions check stay
  green. No `v-html`, no raw colours.
- New migrations are named by UTC time (`CLAUDE.md`).
- System prompts stay unchanged (prompt cache).

## Non-goals

- New budget kinds (per project, per month), alerts or enforcement.
- Provider invoices or real subscription spend: figures stay API-equivalent
  as reported or estimated.
- Re-pricing old rows, backfilling research cost or token bases.
- Replacing the per-project Spend tab or its drill-down.
- Forecasts, anomaly detection, hour-of-week heatmaps.
- A second chart library, hand-drawn SVG charts on this page, export to
  images or PDF. The context panel's sparkline and `ModelBar` stay SVG.
- Changing the context panel's 30-day sparkline source.

## Suggested order

R1 → R2, R3, R4 → R5 → R6 → R7 → R8 → R9 → R10, R11 → R12, R13 → R14, R15
→ R16 → R17 (grows with each web slice) → R18.

## Acceptance

- `/costs` shows, for the last 30 days, known cost per day stacked by
  project, and switching to kind, model or engine needs no reload of the page.
- The sum of the page's project bars for a range equals the sum of the
  projects' Spend tabs for the same calls, unknown calls marked in both.
- A viewer who can see one project sees only that project's figures.
- A research run finished after release appears under research with its
  cost, and still does after its research's retention has deleted the run.
- Month to date is compared with the same days of last month.
- Every top-list row opens the right Spend drill-down.
- At 375, 768 and 1280 px no part of the page scrolls sideways, in CI; the
  live Desk logs no CSP violation and calls no other origin.
- The page's queries use the time index (store test).

## Steering log

Each slice appends its lines here: requirements it narrowed (with the
follow-up issue), and requirements without an issue and why.

- 2026-11-12 planning: background correction. Research runs have written
  `agent_calls` rows since addbd42 (`AgentResearchReconciler.insertCalls`),
  under the research's project (`spec.projectRef`), task = research name,
  run 0, history retention. R5 therefore only adds run numbers and the task
  prefix `research/<name>`. Decision 4's empty `project` is not followed:
  keeping the project keeps `CanView` scoping and the Spend drill-down
  working. Research is shown as the kind `research`, and is excluded for
  viewers without research read access.
- 2026-11-12 planning: the API routes are `/api/v1/costs` and
  `/api/v1/costs/top` (the server's prefix).
- 2026-11-12 planning: R8 is split. The pure module, the colour tokens and the
  chunk check come first; Chart.js and each wrapper arrive with their first
  chart (stacked bars with R9, line with R10, horizontal bars with R11).
- 2026-11-12 planning: R14 and R15 read live state through
  `GET /api/v1/costs` (fields `planUsage`, `budgets`) rather than a third
  route.
- 2026-11-12 #354 (R1): migration `20261112024252_agent_calls_time_idx.sql`
  adds `agent_calls (namespace, project, at)`; `assertUsesIndex` in the store
  tests, used for `SpendSince`.
- 2026-11-12 #355 (R5): `status.runCount` and `status.run.number` on
  AgentResearch; research calls are written with task `research/<name>` and
  run = the run's number (key unchanged, so a retried finish stays one row);
  `history.StoreCall` stores a cost source without a cost as unknown. Older
  rows keep task = name and run 0 (no backfill).
- 2026-11-12 #356 (R2): `store.CostSeries(CostQuery, bucket, group)` sums
  `spendWith` rows per local date bucket (`YYYY-MM-DD`) and group key; the
  `spend` CTE gains `engine` and `model`, `split` gains `namespace`,
  `project` and `engine`. Checkpoint events (no cost) are left out of the
  series. Group `model` counts a call once per model it used, as
  `Spend.ByModel` does: cost, tokens and duration add up to the ungrouped
  totals, calls do not.
- 2026-11-12 #357 (R3): `store.CostBreakdowns(CostQuery)` returns the totals,
  the previous period's totals (`[From-(To-From), From)`), by project, kind,
  model and engine (known cost descending, then key; legacy runs under key
  `""`), both cache-read shares, the range's step lines and `ResearchSince`,
  in one batch. Filters apply to every field, step lines included; the
  engine filter does not move `ResearchSince`, which only `Kind` and
  `ExcludeKinds` can drop. `SpendTotals.cacheReadShare` and
  `stepLinesSQL(filter)` are now shared with `Spend`.
- 2026-11-12 #358 (R4): `store.CostTop(CostQuery, n)` returns the n (1..50)
  most expensive milestones, issues and runs over the same rows and filters
  as `CostSeries`, in one batch: known cost descending, then latest call;
  unknown-only rows last with their `UnknownCalls` (D358-P1). Issues carry
  `max(milestone_iid)`, runs their milestone, issue and the kind of their
  first call (`""` for a legacy run); labels as `childLabel`
  (`%14`, `#281`, `<task> run 3`). Calls without a milestone or issue are in
  no milestone or issue row, only in runs.
- 2026-11-12 #359 (R6, part 1): `GET /api/v1/costs` (`internal/ui/costs.go`).
  `parseCostParams` is shared with `/costs/top`. A named range older than
  the history retention starts at the oldest allowed date; only a custom
  `from` before it is refused. Folded series keys are one point per bucket
  with `other: true` and key `""`. `kind=research` stays valid for a viewer
  without research access and gives no calls. The month's days are one
  `CostSeries` query (first of last month to today) split at the first of
  this month.
- 2026-11-12 #360 (R6, part 2): `GET /api/v1/costs/top` (`costsTop`) takes
  the parameters, validation, scope, research exclusion and 404/503 rules of
  `/costs` through `parseCostParams` (a bad `group` is still a 400, otherwise
  ignored) and returns `CostsTopView` with ten rows per list.
  `CostTopRowView` leaves out zero IIDs, task, run and kind.
- 2026-11-12 #361 (R14, R15 server part): `GET /api/v1/costs` gains
  `planUsage` (per engine, the visible project's reading with the newest
  `observedAt`; ties go to the first project by name) and `budgets` (every
  milestone and issue budget of milestones whose autopilot is not `Done`;
  `Off` and `Paused` stay). Both come from the cached CRDs after the store
  reads and honour only `?project=`; a failing store query still fails the
  whole response (500), they are not served alone. A budget without a
  positive `limitUSD` is unlimited and left out. Issue budgets of merged or
  skipped items stay listed while the milestone runs. An issue's title
  comes from its implement task, empty before the task exists.
- 2026-11-12 #362 (R7): route `/costs` (`views/Costs.vue`, lazy), filters
  parsed and written by `web/src/costs.ts` (defaults `30d` and `project` are
  left out of the URL, a custom range is `from` and `to` without `range`).
  The rail's *Costs* entry, also in the phone's Projects sheet, shows with
  the `history` feature, the one under which `/costs` answers. The
  dashboard's today and 7-day chips link to `/costs?range=7d` (D362-P1), the
  Spend tab to `/costs?project=<namespace>/<name>`. The client's `get` now
  throws `ApiError` with the status (for the 503 state). No charts yet.
- 2026-11-12 #363 (R8, part 1): `web/src/chartData.ts` (`topKeys`,
  `seriesFromPoints`, `bucketLabel`, `colorSlots`, `formatUSD`,
  `formatTokens`); Other has the key `OTHER`, since the server's key `""`
  without `other` is the legacy runs. Tokens `--chart-1` … `--chart-6` and
  `--chart-other`, checked for every pair (any two slots can meet); on the
  dark card two pairs stay under the validator's targets (docs/ui.md,
  *Chart colours*), relying on the legend and table view. `npm run budget`
  checks `routes.costs` through Vite's build manifest, following static and
  dynamic imports and stopping at initial chunks: 43.7 kB before Chart.js.
  Chart.js, the wrappers, runtime token reading and the CSP e2e check follow
  with R9 to R11 as planned.
