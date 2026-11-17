# Web UI: spend and costs

This page keeps what the Desk shows about spend, until it moves to the
budgets guide. Using the Desk is in [The Desk](guides/desk.md) and
[Steer from the Desk](guides/steer-from-the-desk.md); developing it in
[Web UI development](contributing/web-ui.md).

## Spend

The *Spend* tab of a project or milestone stream shows what its agent
calls cost, from the research database (`GET /api/v1/spend`; without it
the tab says it needs the database):

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
  ([What the spend counts](usage.md#what-the-spend-counts)).
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
  asked of the database with [`hack/spend-report.sql`](usage.md#spend-report-script).
* **Tuning tables**, each with the average cost per call (of the calls
  with a known cost; *n/a* when none has one), to set the levers in
  [Agents and models](reference/agents-and-models.md#roles) by:
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

## Costs page

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
the [chart rules](contributing/web-ui.md#charts).
(`docs/specs/costs-page.spec.md`).

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
  120 kB gzipped, checked by `npm run budget` ([Bundle budget](contributing/web-ui.md#bundle-budget)).
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

