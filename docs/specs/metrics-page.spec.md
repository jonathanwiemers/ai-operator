# Desk: one Metrics page, with lines of code next to cost, for all projects and each project

Status: the milestone's spec, run by autopilot. Every implementing merge
request built one or more requirements; the docs issue (R18) added this
copy at the end of the milestone (planner decision D610-P1). Narrowed
requirements and the planner's departures from the text below are in the
[Steering log](#steering-log). Where this spec and the docs disagree, the
docs win: [Lines of code](../guides/budgets-and-spend.md#lines-of-code),
[The Metrics page](../guides/budgets-and-spend.md#the-metrics-page) and
[Merged lines](../contributing/design.md#merged-lines).

## Goal

1. Every issue of a milestone shows the lines its agents wrote and the lines its merge requests merged. Merged lines include the issue's companion MRs. Production lines are shown apart from tests, docs and generated files. The milestone Issues table, the milestone rows and a task's run rows show these lines next to cost and tokens.
2. One Desk page, `/metrics`, replaces `/costs`. It shows cost, tokens, code, flow and pauses across every project the viewer may see. The same page filtered to one project is that project's metrics.
3. The Spend tab of a project or milestone becomes a **Metrics** tab. It shows the lines next to the cost, and the cost per 100 merged production lines.
4. Nothing of today's Costs page is lost: old `/costs` links, the filters, the CSV export and the Spend drill-down keep working.

## Background

File refs are at origin/main 86a8f9b, 2026-11-16.

This milestone runs after every open milestone of the project. In particular it builds on these:
- %18 (Desk: one Costs page across all projects, with charts) builds `/costs`. This milestone renames and extends it.
- %20 (Desk polish) adds the phone Costs tab, which this milestone renames.
- %25 (Docs) moves the docs into Getting started, Guides, Concepts and Reference. R18 writes into that layout.
- %31 cuts GitLab calls while done milestones settle. This milestone adds no call per poll, only one per merge (Decision 2).

What exists:

- **Lines written per call.** After every implementer call, the runner counts the diff the call left uncommitted (`internal/runner/agent.go:143`, `Workspace.UncommittedLines` at `internal/runner/workspace.go:370`, `git diff --numstat HEAD` plus untracked text files). That diff is the step commit that follows the call.
  - It is stored in `CallUsage.LinesAdded/LinesDeleted` (`api/v1alpha1/agenttask_types.go:1622`) and in `agent_calls.lines_added/lines_deleted` (`internal/research/store/migrations/0007_agent_calls_checkpoints.sql`).
  - Only the implementer kinds implement, fix, ci and conflict carry lines (`implementerKinds`, `web/src/spend.ts:510`).
  - All files count: tests, docs and generated files included.
- **Where written lines are shown today.**
  - Per call, in the *Lines* column of the task's call table (`web/src/components/TaskCalls.vue:56`, `callLines` in `web/src/spend.ts:514`).
  - The step-size statistics (`StepLines`, `internal/research/store/spend.go:585`) appear on `/costs` and on the Spend tab.
  - `SpendTotals` (`internal/research/store/spend_totals.go:20`) has no lines. So the milestone Issues table (`web/src/components/MilestoneIssues.vue:100-107`: Issue, State, MR, Wall, Agent, Paused, Cost, Tokens), the milestone rows (`MilestoneRow.vue`) and the run rows of `TaskCalls.vue:43-47` show none.
- **Merged lines are not recorded.**
  - Autopilot reads MR diffs only to hash a change (`diffHash`, `internal/controller/autopilot.go:1781`) and for companions (`internal/controller/companions.go:144`).
  - Every home merge ends in `autopilot.merged` (`internal/controller/autopilot.go:1808`). It sets `MergedAt`, `MergedSHA` and the MR on the `AutopilotItem` (`api/v1alpha1/agenttask_types.go:2089`).
  - The history differ turns the new Merged state into a `merged` event with `data.sha` (`internal/history/differ.go:309`).
  - `store.issueFigures` takes the issue's merge time from the latest `merged` event (`internal/research/store/milestones.go:198`).
  - The 300-line budget per MR exists only as a sentence in the planner prompt (`buildSpecPrompt`, `internal/runner/prompts.go:548`).
- **Companion MRs.**
  - They are tracked on the AgentProject, in `status.companions` (`api/v1alpha1/agentproject_types.go:1409`, `CompanionStatus` in `api/v1alpha1/crossrepo.go:259`). Each entry has `HomeIssueIID`, `MilestoneIID`, `Project` and `MRIID`.
  - Entries are removed `CompanionRetention` after they end.
  - An entry becomes Merged in three places: a human merge seen at `internal/controller/companions.go:190`, a resume at `companions.go:539`, and autopilot's own merge at `internal/controller/companions_merge.go:322`.
  - The history differ watches only AgentTasks (`internal/history/differ.go:162`). The AgentProject reconciler holds the Recorder (`internal/controller/agentproject_controller.go:85`, `Recorder.Record` at `internal/history/recorder.go:81`).
- **Pauses.**
  - The differ writes `autopilot.paused` and `autopilot.resumed` events per milestone (`internal/history/differ.go:328`).
  - `store.Pauses` reads one milestone's events, at most 2000 (`internal/research/store/pauses.go:68`). `pairPauses` (`pauses.go:89`) turns them into `Pause` (kind, reason, item, since, until, resolver, via, seconds) and `PauseTotals` (count, paused time, by kind, mean time to resume, by resolver, by via).
  - Nothing reads pauses across milestones or over a time range.
  - Per-issue holds are not pause events yet (open issue #502).
- **GitLab diffs.** `gitlab.Diff` (`internal/gitlab/client.go:302`) has the patch text, the paths, and the new, deleted, renamed, collapsed and too_large flags. Collapsed and too-large diffs come without content.
- **Cost UIs.**
  - `/costs` (`web/src/views/Costs.vue`) reads `GET /api/v1/costs` and `GET /api/v1/costs/top` (`internal/ui/server.go:181-182`, `internal/ui/costs.go`).
  - Its filters are range, group by, project (`namespace/name`), kind and engine (`parseCostParams`, `internal/ui/costs.go:182`; URL state in `web/src/costs.ts`). Its sections are Spend over time, This month, Where it goes and Tokens.
  - The Spend tab of a project or milestone stream (`SpendPanel.vue`, `web/src/views/Stream.vue:669`, `?tab=spend`) reads `GET /api/v1/spend` (`internal/ui/spend.go:129`).
  - The rail entry is at `RailNav.vue:78` and the phone tab at `App.vue:251`.
- **Charts** are hand-written SVG components (StackedBars, ChartLegend, ChartTable, LineCompare). No chart library is installed.
- **Prometheus.** The operator exports only `ai_operator_task_stalled` (`internal/controller/metrics.go`). The chart opens the metrics port (`charts/ai-operator/templates/deployment.yaml:42`) and has no ServiceMonitor.

## Decisions (locked)

1. **Two kinds of lines.**
   - *Written* is the sum of `lines_added` and `lines_deleted` over the calls. It includes every step commit, rewrites and fix rounds, so it measures effort.
   - *Merged* is the final diff of the issue's MRs, home and companions. It measures the outcome.
   - Both are shown as `+added/−deleted`. Neither replaces the other.
2. **Merged lines are measured once, at merge, from GitLab.**
   - Each MR gets exactly one `ListMergeRequestDiffs` call, made when it becomes Merged and has no `mergedLines` yet.
   - A failed call logs and leaves `mergedLines` empty. It never blocks or delays a merge or a landing: it is a measurement, like `countLines`.
   - A file whose diff comes without content (collapsed or too large) sets `partial: true`. Its lines are not guessed. The Desk shows a partial count as `≥ N`.
   - Rejected: counting in the runner, which misses human pushes and rebases. Rejected: reading diffs on every poll.
3. **Four categories, from a fixed path list.** The category is decided on `new_path` (`old_path` for deleted files). The first match wins:
   - **generated**: `zz_generated*`, `web/src/generated/**`, `config/crd/**`, `config/rbac/**`, `charts/*/files/**`, `*.pb.go`, `go.sum`, `package-lock.json`, `pnpm-lock.yaml`, `yarn.lock`.
   - **tests**: `*_test.go`, `**/test/**`, `**/tests/**`, `**/testdata/**`, `*.test.*`, `*.spec.ts`.
   - **docs**: `docs/**`, `*.md`.
   - **production**: everything else.

   The same list applies to companion repositories. It lives in code, in one place, not in configuration.
4. **Storage of home merged lines.**
   - `AutopilotItem` gets `mergedLines`: `production`, `tests`, `docs` and `generated`, each `{added, deleted}`, plus `partial`.
   - The `merged` history event carries the same object as `data.lines`.
   - There is no new table and no migration. The lines expire with the history retention (`historyRetention`), like calls.
5. **Companion merged lines count to the home issue.**
   - `CompanionStatus` gets the same `mergedLines`.
   - When an entry becomes Merged, the AgentProject reconciler records a `companion.merged` event:
     - namespace and project: the home AgentProject;
     - `milestone_iid` and `issue_iid`: the home milestone and issue;
     - `mr_iid`: the companion MR;
     - `data.companion`: the companion's path;
     - `data.lines`: the counts;
     - key: `companion/<companion path>/<mrIID>/merged`.
   - It is a kind of its own, so the issue's merge time (from `merged` events) does not move.
   - An issue's merged lines are the sum of its `merged` and `companion.merged` events. A per-project table counts companion lines under the home project.
6. **Written lines are not split by category.** That would need the runner to classify paths per call.
7. **Cost per 100 merged production lines.**
   - It is the cost of all calls of the issues merged in the scope or range (calls made before the range included), divided by those issues' merged production lines (added plus deleted, companions included), times 100.
   - It is `—` when the cost is unknown or there are no production lines.
   - It carries the same unmetered and token-basis notes as cost (`basisNote`, `unmeteredNote` in `web/src/spend.ts`).
   - A partial count shows the figure with `≤`.
8. **One page, `/metrics`, replaces `/costs`.**
   - `/costs?…` redirects to `/metrics?…` with the same query.
   - The rail entry and the phone tab are renamed **Metrics**. The rail subtitle is "cost, code, flow and pauses, across projects".
   - "General" is the page without a project filter. "Per project" is `/metrics?project=namespace/name`, and the project stream links to it.
   - The sections, in order:
     1. Cost: today's Spend over time, This month and Where it goes.
     2. Tokens.
     3. Code.
     4. Flow.
     5. Pauses.
   - Code, Flow and Pauses use the range and the project filter only. When Kind or Engine is set, these sections say "Kind and engine filter cost and tokens only".
   - `GET /api/v1/costs`, `/costs/top` and `/spend` keep their answers. They only gain fields.
   - The new sections get their own endpoints: `GET /api/v1/metrics/code`, `/metrics/flow` and `/metrics/pauses`. They take the same query parameters and use the same visibility rules as `/api/v1/costs`.
9. **Flow** covers the issues with a `merged` event in the range:
   - merged issues per bucket;
   - median wall time (first `run.started` to `merged`, as `issueFigures` computes it);
   - median agent time (the sum of the issue's call durations);
   - review calls and fix calls per merged issue (mean).
10. **Pauses** are the milestone pauses, the `autopilot.paused` and `autopilot.resumed` events, paired by `pairPauses`.
    - A pause counts in the range and in the bucket where it started. Its seconds are its full length; an open pause counts up to now.
    - The section shows:
      - pauses per bucket, stacked by kind;
      - paused hours per bucket;
      - tiles: number of pauses, paused time, mean time to resume, open now;
      - tables by kind (count, time), by resolver and by via;
      - per project, when there is no project filter;
      - the 10 longest pauses of the range (project, milestone, kind, reason, length), each linking to the milestone's Pilot tab.
    - One query reads at most 20000 events. Above that the answer says `truncated` and the page shows "only the newest 20000 pause events".
    - The Issues table keeps its Paused column.
11. **The Spend tab becomes the Metrics tab.** It keeps the same drill-down (milestone, issue, run, call). `?tab=spend` still opens it; new links use `?tab=metrics`.
12. **No backfill.** Merged lines start with the first merge after the release, and older issues show `—`. Written lines already exist for every call since migration 0007. Pauses exist as far back as the history retention keeps them.
13. **The Desk stays the source of truth.** A Prometheus or VictoriaMetrics export and a Grafana dashboard in `infra/cluster` are the next milestone.

## Requirements

### Operator (Go)

**R1 Count and classify an MR's lines.** Add a small package, for example `internal/difflines`:
- one function returns a path's category (Decision 3);
- one turns `[]gitlab.Diff` into the four `{added, deleted}` counts plus `partial`.

Count the patch lines that start with `+` or `-`, but not the `+++` and `---` headers. Binary files count 0.

Tests: table tests for every pattern of Decision 3 and for the order (`docs/x_test.go` is tests, `web/src/generated/a.md` is generated); a deleted file uses `old_path`; collapsed and too-large diffs set `partial`; binary patches count 0.

**R2 Record home merged lines at merge.**
- Add `AutopilotItem.mergedLines` (Decision 4).
- `autopilot.merged` fills it once, with R1's count (Decision 2).
- Run `make generate manifests`.

Tests (gitlabfake): a merge records the counts; `DiffErrors` on the MR still merges and closes the issue, and leaves `mergedLines` empty; a second `merged` call on the same item does not call the diffs endpoint again.

**R3 The merged event carries the lines.** The history differ (`internal/history/differ.go:309`) adds `data.lines` when the item has `mergedLines`.

Tests: an event with lines; an event without lines when `mergedLines` is empty; the event key is unchanged.

**R4 Companion merged lines.**
- Add `CompanionStatus.mergedLines`.
- When an entry becomes Merged (all three places in Background), count its diff once with R1. Record the `companion.merged` event of Decision 5 through the reconciler's Recorder.
- A failed count changes no landing state.
- Run `make generate manifests`.

Tests (gitlabfake): a human merge, a resume and autopilot's merge each record the lines and one event; a failing diffs call still lands; a second poll records no second event, because the key makes the write idempotent.

### Store (Go)

**R5 Written lines in SpendTotals.**
- `SpendTotals` gains `linesAdded` and `linesDeleted`, the sums over the scope's calls. Legacy rows (runs before `agent_calls`) add 0.
- Every view that embeds `SpendTotals` carries them.
- Run `make ts-types`.

Tests: in `spend_test.go` and `costs_test.go`, the sums over implement and fix calls; review calls add 0.

**R6 Merged lines per issue, milestone and scope.**
- `IssueFigures` gets the issue's merged lines (Decision 5), with each companion's share listed.
- `MilestoneRow` gets the sum over the milestone, `partial` if any part is partial.
- `Spend` for a project, milestone or issue scope gets the sum and the cost per 100 merged production lines (Decision 7). Task and run scopes have none.

Tests: sums with and without companions; `partial` carries up to the sums; issues without lines count as unmeasured, not as 0 (a milestone of three merged issues with one unmeasured says "2 of 3 measured").

**R7 Code and Flow over a range.**
- Store queries take a `CostQuery`, limited to the range and the project.
- Code returns per bucket the written lines and the merged lines by category, the range totals, the cost per 100 merged production lines, and, without a project filter, the same per project.
- Flow returns the figures of Decision 9.
- The queries use the time indexes (`history_events_at_idx`, and the `agent_calls` index of `20261112024252_agent_calls_time_idx.sql`).

Tests: series and totals on seeded data; bucket edges in a non-UTC time zone; "uses the time index" checks like `TestCostSeriesUsesTimeIndex`.

**R8 Pauses over a range.**
- A store query reads the `autopilot.paused` and `autopilot.resumed` events of the visible projects from the range start to now, at most 20000 of them, newest first.
- It pairs them with `pairPauses` and keeps the pauses that started in the range.
- It returns the figures of Decision 10, with `truncated`.
- The existing per-milestone `Pauses` does not change.

Tests: a pause that started before the range and ended in it is left out; an open pause counts up to now; buckets in a non-UTC time zone; truncation; the time index is used.

### UI server (Go)

**R9 `GET /api/v1/metrics/code`.**
- It returns R7's Code figures.
- It parses the parameters with `parseCostParams` and filters projects as `/api/v1/costs` does.
- Research is never part of it.
- Run `make ts-types`.

Tests: handler tests for the ranges, the project filter, a project the viewer may not see (treated as `/costs` treats it) and a bad parameter (400).

**R10 `GET /api/v1/metrics/flow`.** It returns R7's Flow figures, with the same parameters and rules as R9.

Tests: as R9.

**R11 `GET /api/v1/metrics/pauses`.** It returns R8's figures, with the same parameters and rules as R9. Each of the longest pauses carries the milestone's Desk link.

Tests: as R9, plus `truncated`.

**R12 Spend view and issue figures carry merged lines.** `SpendView` and `IssueFiguresView` include R6's merged lines, the companion shares and the cost per 100 merged production lines.

Tests: the golden tests (`internal/ui/golden_test.go`) are updated.

### Web

**R13 `/metrics` replaces `/costs`.**
- Add the route and the redirect from `/costs`, keeping the query.
- Rename the rail entry, the phone tab (and %20's phone Costs tab if it exists) and the page title to Metrics.
- Add a *Metrics* link on the project stream to `/metrics?project=namespace/name`.
- Today's sections, filters and CSV menu stay as they are.

Tests: `web/test` unit tests for the redirect and the query; a Playwright check that `/costs?range=7d&project=a/b` lands on `/metrics` with the same filters.

**R14 Code and Flow sections.**
- **Code** has:
  - bars per bucket: written lines and merged production lines;
  - tiles: written, merged by category, cost per 100 merged production lines;
  - a per-project table when there is no project filter.
- **Flow** has:
  - bars of merged issues per bucket;
  - tiles: median wall time, median agent time, review and fix calls per merged issue.
- Both use the existing chart components with a table view (ChartTable), and show the note of Decision 8 when Kind or Engine is set.

Tests: unit tests for the formatting (`+A/−D`, `≥ N`, `—`); a Playwright check of both sections at phone width with no sideways scrolling.

**R15 Pauses section.**
- It shows the figures of Decision 10 with the existing chart components and a table view.
- Kinds, resolvers and vias use the labels the Pilot tab's pauses card already uses (`PausesCard.vue`, `web/src/pauses.ts`).
- When the answer says `truncated`, the section shows the note.

Tests: unit tests for the bucket and table helpers; a Playwright check at phone width; a longest-pause row opens the milestone's Pilot tab.

**R16 Lines in the tables.**
- Milestone Issues table (`MilestoneIssues.vue`): add *Written* and *Merged* columns. *Merged* shows production `+A/−D`; its tooltip lists the four categories and each companion's share.
- Milestone rows (`MilestoneRow.vue`): add merged production lines.
- `TaskCalls.vue` run rows: add a *Lines* column with the run's written lines.
- On a phone, the new figures appear in the row's card, like Cost and Tokens.

Tests: unit tests in `web/test` for the row helpers; the Playwright Issues table check sees both columns.

**R17 The Spend tab becomes the Metrics tab.**
- Rename the tab, keep `?tab=spend` as an alias and use `?tab=metrics` in new links.
- The tab shows the written and merged lines and the cost per 100 merged production lines next to the totals.
- The *By kind* breakdown shows the written lines per implementer kind.

Tests: a unit test for the tab alias; a Playwright check on a milestone stream.

### Docs

**R18 Docs and the spec file.**
- Update the Desk guide and the budgets-and-spend guide that %25 writes (in `docs/guides/`). If they don't exist when this is planned, update the *Spend* and *Costs page* sections of `docs/ui.md`.
- Describe `/metrics`; written vs merged lines; the categories of Decision 3; how companions count; the cost per 100 lines; and the Pauses section.
- Document `mergedLines` in the AutopilotItem and CompanionStatus reference. Document `data.lines` and the `companion.merged` event wherever the history events are described.
- Add `docs/specs/metrics-page.spec.md` with this spec and a steering log, and list it in `docs/specs/README.md`.

Tests: the docs pages build and their links resolve.

## Constraints

- Under 300 production lines per merge request; tests, generated files and docs do not count.
- When a requirement has to be narrowed, open a follow-up issue for the rest and say so in the steering log of `docs/specs/metrics-page.spec.md`.
- Run `make generate manifests` after changing API types (R2, R4) and `make ts-types` after changing view structs (R5, R6, R9 to R12); the CI drift job fails on stale files.
- Before pushing, run `gofmt`, `go vet ./...`, `make test` and `make helm-lint`. After changing `web/`, run `make ui`.
- No new Go or npm dependencies. Charts use the existing SVG components. The CSP stays `style-src 'self'` without `unsafe-inline`.
- Merging and landing never wait for or fail on a line count (Decision 2). There is no new GitLab call per poll: exactly one diffs call per merged MR, once.
- `GET /api/v1/costs`, `/api/v1/costs/top` and `/api/v1/spend` keep their parameters and fields; they only gain fields. `store.Pauses` and the Pilot tab's pauses keep their behavior.
- No new research-store migration is needed. If one turns out to be needed, name it `$(date -u +%Y%m%d%H%M%S)_name.sql`.
- Never put kubebuilder RBAC markers into `internal/ui`; the Desk's service account stays in `templates/ui-rbac.yaml`.
- Do not mirror Go views in `web/src/types.ts`; use the generated types.
- The prompts of every role stay unchanged.
- Keep `docs/` and the chart README in sync with behavior changes.

## Non-goals

- Exporting metrics to Prometheus or VictoriaMetrics, a ServiceMonitor, or a Grafana dashboard in `infra/cluster`. That is the next milestone (Decision 13).
- Recording per-issue holds as pause events. That is #502. Until it lands, the Pauses section counts milestone pauses only.
- Backfilling merged lines of MRs merged before the release (Decision 12).
- Splitting written lines by category, or a per-project override of the path list (Decisions 3 and 6).
- Merged lines of issues merged without autopilot (projects without autopilot).
- Health figures on the Metrics page: checkpoints, step-gate nudges, read-gate blocks, CI failures.
- Enforcing the 300-line budget from the measured lines. This milestone only measures.

## Suggested order

R1 → R2 → R3, R4 → R5, R6, R7, R8 → R9, R10, R11, R12 → R13 → R14, R15, R16, R17 → R18

## Acceptance

- **Home MR lines (Goal 1).** On a milestone where autopilot merged an MR that changed `internal/foo.go` (+40/−5), `internal/foo_test.go` (+60) and `docs/foo.md` (+10):
  - the Issues table shows *Merged* `+40/−5`;
  - its tooltip shows tests +60/−0, docs +10/−0, generated +0/−0;
  - *Written* shows the sum of the issue's implementer calls.
- **Companion lines (Goal 1).** On an issue with a companion MR in `group/infra` (+12/−3 production) that a human merged after the home merge, *Merged* shows `+52/−8`. The tooltip lists `group/infra +12/−3`. The issue's merge time does not change when the companion merges.
- **Other tables (Goal 1).** The milestone's row in the project's milestone list shows the merged production lines. The task page's run rows show each run's written lines.
- **Failed or partial counts (Goal 1).** When GitLab's diffs call fails at merge, the MR is still merged and the issue closed, and its *Merged* cell shows `—`. When one file's diff is too large, the cell shows `≥ N`.
- **General view (Goal 2).** `/metrics` with no project filter shows Cost, Tokens, Code, Flow and Pauses across all visible projects. The Code table lists each project's merged production lines and cost per 100 lines.
- **Per-project view (Goal 2).** The *Metrics* link on a project stream opens `/metrics?project=namespace/name`, and every section shows that project only. Setting Kind to `review` changes Cost and Tokens and shows the note on Code, Flow and Pauses.
- **Pauses (Goal 2).** In a 30-day range where a milestone paused twice for CI (2 h and 30 min, both resumed by the owner) and is paused now for a budget stop:
  - the section shows 3 pauses;
  - the CI kind is 2 pauses and 2.5 h;
  - the resolver owner is 2;
  - "open now" is 1;
  - the budget pause is in the longest list with a link to the milestone's Pilot tab.
- **Metrics tab (Goal 3).** On a milestone stream, the *Metrics* tab shows written and merged lines and the cost per 100 merged production lines next to the cost. The drill-down to issue, run and call works as before.
- **Nothing lost (Goal 4).** An old link `/costs?range=7d&project=a/b` opens `/metrics` with range 7 days and project a/b. The CSV menu downloads the same files as before. `?tab=spend` on a milestone opens the Metrics tab.

## Steering log

What changed on the way: the planner's departures from the text above,
and what the implementing merge requests narrowed or built differently.
The requirements were built in this order (all merged 2026-11-19): R1–R3
(ca2feb2), R5 (18d12a5), R4 (232e2e3), R13 (769ed2e), R8 and R11
(8f330d7), R10 (8fa00b2), R15 (#606, b7f76cc), R6 and R12 (f10f0a0), R7
and R9 (4a8ac58), R16 (13dd1c1), R14 (#608, 29ca767), R17 (52a1b96), R18
(#610).

- Planning (D610-P1): each implementing merge request opens a follow-up
  issue for what it narrows and names it in its description; this docs
  issue creates this file and writes the log, so parallel merge requests
  never edit the same file.
- Planning: **only measured issues count in the cost per 100 merged
  production lines** (Decision 7 counts all merged issues). An issue whose
  `merged` event has no `lines` adds neither its cost nor its lines, and
  the figure says how many were measured ("2 of 3 measured"). Counting the
  cost of unmeasured issues against lines that leave them out would
  overstate the figure.
- Planning: the existing per-arm breakdown of `/costs/metrics` (*Cost per
  merged line*) measures the implement calls' lines of merged issues,
  which are written lines, not Decision 1's merged lines. It was relabeled
  *Cost per 100 written lines* (*Written lines*, *Per 100 written lines*);
  its API field keeps the name `mergedLines`, so the answer of
  `/api/v1/costs/metrics` does not change.
- #606 (R15) and #608 (R14): the `/metrics` route's JS and CSS no longer fit
  the bundle cap of 120 kB. The Pauses section raised it to 124 kB
  (measured 123.3), Code and Flow to 126 kB (measured 125.0), each reported
  on its merge request (`web/bundle-budget.json`).
- R14, R15 (as built): Code, Flow and Pauses come after the page's existing
  lists (Most expensive, Cost per 100 written lines, Context, Plan usage,
  Budgets), not right after Tokens as Decision 8 orders them. The Cost and
  Tokens sections keep Decision 8's order.
- R15 (as built): the truncation note reads "Counts only the newest 20000
  pause events."
- R4 (as built): a companion whose count failed still records its
  `companion.merged` event, without `lines`; the issue's sum is then
  `partial` (`≥`) rather than unmeasured, since the home merge was
  measured.
- R1 (as built): a diff that comes empty and is not a pure rename also
  sets `partial`, like a collapsed or too-large one.
- R18 (#610): the user pages may not name source paths, so the guide
  describes the generated and tests patterns that are repository paths in
  words; the exact list is in [Merged lines](../contributing/design.md#merged-lines).
  The status fields and history events got a reference page of their own,
  [Status and history events](../reference/status-and-history.md), since no
  reference page described AgentTask status and the AgentProject page is
  at its line cap.
- R18 (#610): this run could not read the merge request descriptions, and
  neither the commits nor the code name a follow-up issue of this
  milestone. A narrowing reported only in a merge request description is
  missing here; add it with its follow-up issue.
