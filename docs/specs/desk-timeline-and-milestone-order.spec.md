# Desk: a timeline of which issue runs when, and milestones you drag into order

Status: the milestone's spec, run by autopilot. Narrowed requirements and
requirements without an issue go into the [Steering log](#steering-log).

## Goal

1. On a milestone's **Issues** tab, a **Timeline** shows one row per issue, grouped by wave, on a time axis. Each bar shows when the agent ran, when the issue waited for CI or review, when it was held, and when it merged. Issues that ran side by side are visibly side by side. A line marks now, and running bars grow with it.
2. The dashboard's **Overview** has a **Timeline** across all projects you can see: one row per project, bars per issue labelled `#iid` and coloured by milestone, for the last 6 h, 24 h (default) or 7 days. You see at a glance which issue of which milestone and project ran when.
3. A project page opens on the **Milestones** tab that %41 adds. It holds the run-order queue, Finishing, In progress and Recently done. The event feed and the composer are on the **Activity** tab (%41 renames Stream), and old `?tab=stream` links still open it.
4. On the Milestones tab an owner drags an open autopilot milestone to a new place in the queue, or uses the row menu (**Move to top / Move up / Move down**). The operator runs milestones in exactly that order. The same order can be set from GitLab with `/agent move top|bottom|before %N|after %N`.
5. Dragging a milestone above the running one asks for confirmation first. Then it works like *Run next* does today: the running milestone's active issues finish, it starts no new ones, and the new first milestone plans.

## Background

File refs are at origin/main 2de46eb (2de46eb68240aa656c6c238761060dde52862a71), 2026-11-20.

**Runs after %41.** "Desk: one header and one row of tabs on every page, and an account menu" (%41) runs before this milestone. It adds the project **Milestones** tab (first and default, with `ProjectQueue`, `ProjectFinishing`, `MilestoneSections`; this milestone's former R5), renames the Stream tab to **Activity** (`?tab=stream` still opens it), puts every pill switch and filter into a `UDashboardToolbar` at the top of its tab, turns the level-1 tabs into underline tabs, moves the milestone Pilot tab into a drawer and lists Needs you only while something needs you. `Stream.vue` line refs below are from before %41; the planner checks the code again.

**Milestone order today.** The order is not stored. It is computed:
- `internal/milestoneorder/order.go:1-5` ranks milestones with a run-next mark first (earliest mark first), then the unmarked ones by iid. `Rank` and `Ahead` (`order.go:60-85`) give the run order and what is ahead of each milestone.
- The mark is `AutopilotStatus.StartRequestedBy` / `StartRequestedAt` (`api/v1alpha1/agenttask_types.go:2150-2160`). It is set by the `start` command in `startNow` (`internal/controller/autopilot.go:2536-2568`) and cleared by `start off` / the Desk's `unstart` (`autopilot.go:2414-2415`). Only owner subjects may send it (`autopilot.go:2384-2385`).
- The controller ranks with `queuedAhead` (`internal/controller/agentproject_controller.go:745-753`). `specTurn` (`agentproject_controller.go:732-738`) lets a milestone plan when nothing is ahead, or when it has `StartRequestedBy`. The Desk ranks the same way in `queue` (`internal/ui/queue.go:28-62`), which fills `project.queue`.
- `AgentProjectStatus` is at `api/v1alpha1/agentproject_types.go:1438`. There is one `AgentProject` per GitLab project.

**The project page today.** `web/src/views/Stream.vue:554-567` puts `ProjectQueue`, `ProjectFinishing` and `MilestoneSections` at the top of the Stream tab. Below them come the composer, the filter chips and the event feed (`Stream.vue:568-640`). The tabs come from `projectTabsFor` (`web/src/streamTabs.ts:54`): Stream, Decisions, Board, Metrics and, with plans, Plans. `ProjectQueue.vue` shows one row per milestone with *Run next / Clear mark* and *Pause / Resume* (`web/src/projectQueue.ts:41`). These send `kind: "command"` chat messages to the milestone's spec task.

**The issue queue today.** `QueueList.vue` (`grouped` on the Issues tab, `web/src/components/MilestoneIssues.vue:247`) shows issues in lanes (`laneGroups`, `web/src/milestoneQueue.ts:104`) with wave chips (`waveChip`, `milestoneQueue.ts:140`). It shows no time. The Overview's *Working now* (`web/src/components/DashboardOverview.vue:54`) has one card per running task, with "running for".

**The data.** `history_events` (`internal/research/store/migrations/0002_history.sql`) records, per project, milestone and issue:
- `run.started` and `run.finished` (with `durationSec`);
- `push`, `ci`, `review`, `merged`, `closed`;
- `autopilot.paused` and `autopilot.resumed`.

The kinds are listed in `internal/history/differ.go:24-66`. Per-issue holds are **not** recorded yet: that is open issue #502. Without the research database (the `history` feature) none of these events exist.

**Web.** Chart.js is a dependency, but the timeline does not use it (Decision 5). Custom components live in `web/src/ui/` and are listed in the UI kit (`docs/contributing/web-ui.md`, "UI kit"). `web/bundle-budget.json` caps the bundle size.

**Milestones that run before this one:**
- %31 (settling milestones);
- %32 (one Metrics page);
- %34 (decision wizard);
- %36: one look on every page. It sets the page header and tabs, the UTabs pill switches (#630), the `raw-control` rule (#631) and the chart colour tokens (#633). This milestone's switches, menus and colours follow what %36 lands.
- %37: keyboard and screen reader, with an accessibility check on every page. The drag list and the timeline must pass that check.
- %38 (agent language);
- %39: plans that build on the queue. Reordering can put a milestone before work it builds on; this milestone does not warn about that (Non-goals).

## Decisions (locked)

1. **One milestone, two parts.** The issue timeline and the drag-and-drop order ship together, because the owner wants them together. The requirements are split so the parts can be planned in parallel (Suggested order).
2. **Timeline rows: one per issue, grouped by wave.** Rows are not per run slot. Wave headings use the W1, W2… of `waveChip`.
3. **A bar is the issue's whole life.** Segments:
   - *running*: from `run.started` to `run.finished` of any run on the issue;
   - *waiting*: the time between its first run and its merge that is neither running nor held;
   - *held*: from hold to resume (R3).

   A marker shows the merge, or the skip. An issue still open ends at now.
4. **Overview timeline: one row per project.** Bars are per issue, labelled `#iid` and coloured by milestone, with a legend of the milestones in view. When bars overlap, the project row grows into as many lines as needed, so no bar hides another. Windows are 6 h, 24 h and 7 d; 24 h is the default.
5. **Drawn with a custom HTML/CSS component**, `web/src/ui/Timeline.vue`, listed in the UI kit with its reason: nothing in Nuxt UI draws a time axis. Every bar is a link to its issue page and has an accessible name ("#42 Add parser, running 12 min, merged 14:05"). Chart.js is rejected because a canvas can't be reached by keyboard.
6. **Past and now only.** No forecast of waiting issues. They stay in Lanes.
7. **The project page split is done by %41.** **Milestones** is the first and default tab: the queue, Finishing, In progress and Recently done. **Activity** (formerly Stream) keeps the composer, the filters and the feed. `?tab=stream` opens Activity. A project URL without `?tab=` opens Milestones. This milestone adds dragging and the row menu to the queue on that tab (R6). Milestone pages keep %41's tabs.
8. **One stored order replaces the run-next marks.**
   - The order is the only thing that ranks milestones. *Run next* becomes *Move to top*.
   - On upgrade the order is seeded once from today's rank (marks, then iid), so nothing moves by itself.
   - After that, `StartRequestedAt` is no longer written or read for ranking.
   - A milestone at the top plans when its turn rule allows. A milestone paused by its owner waits until it is resumed, as an unmarked first milestone does today.
9. **Moving above the running milestone works like Run next does today.** The running milestone becomes *holding*: its active issues finish, it starts no new ones. The new first milestone plans. The Desk asks to confirm such a drop. A GitLab command applies without asking.
10. **New milestones join at the bottom.** A done or closed milestone leaves the order.
11. **Dragging uses `vue-draggable-plus` (SortableJS)**, the one new dependency, loaded only in the project page's chunk. Each row also has a Nuxt UI dropdown menu with *Move to top / Move up / Move down* for keyboard, screen reader and phones.
12. **The move is a command.** The Desk sends the same command as `/agent move …` to the moved milestone's spec task, so the owner check and the tracking-issue note are the same in both places. Who may move: the owner subjects, as for `start` today.
13. **The order is stored on the project:** `AgentProject` `status.milestoneOrder` is a list of milestone iids. `internal/milestoneorder` ranks by it for both the controller and the Desk. Rejected: a rank on each spec task, because many separate values can disagree; one list cannot.
14. **The command words are `move top`, `move bottom`, `move before %N` and `move after %N`.**
    - `%N` must be another open autopilot milestone of the same project; anything else is refused with a reason.
    - `start` stays as another way to write `move top`.
    - `start off` and the Desk's *Clear mark* go away. `start off` answers "There are no run-next marks any more; use `move`."
15. **The Issues tab gets a Timeline / Lanes switch**, a UTabs pill switch. Timeline is the default when the `history` feature is on. Without it the tab shows Lanes as today, with no switch.
16. **Per-issue holds are recorded in history** (open issue #502, taken into this milestone as R3), so the *held* segment can be drawn.

## Requirements

### Operator (Go)

**R1 Stored milestone order.**
- Add `MilestoneOrder []int64` to `AgentProjectStatus`.
- `internal/milestoneorder` ranks by it: listed iids in list order, then open autopilot milestones that are not in the list by iid, at the bottom (Decision 10).
- When the list is empty, the operator seeds it once from today's rank (marks, then iid) and writes it. Done and closed milestones are dropped from the list on sync.
- `queuedAhead`, `specTurn` and `internal/ui/queue.go` use the new rank. `specTurn` no longer lets `StartRequestedBy` jump the queue (Decision 8).
- Run `make generate manifests`.

Tests: `internal/milestoneorder` table tests (seed from marks, list order, unlisted at the bottom, done dropped); `queuedAhead` with a stored order; `queue` in `internal/ui` returns the stored order.

**R2 The `move` command.**
- Parse `move top|bottom|before %N|after %N` in `internal/chatcmd`, from both the tracking-issue words and the Desk JSON (`{cmd:'move', to:'top'|'bottom'}` or `{cmd:'move', before:N}` / `{cmd:'move', after:N}`).
- Apply it in the autopilot: owner subjects only, with the same refusal as `start`.
- Rewrite `status.milestoneOrder`.
- Post one note on the moved milestone's tracking issue: "⏫ Moved before %N (by X)" or "⏫ Moved to the top (by X)".
- When the move puts the milestone above the running one, the note says that the running milestone's active issues finish first (Decision 9).
- `start` maps to `move top`; `start off` / `unstart` answers as in Decision 14.
- The tracking issue's command hint offers `move top` where it offered `start`.

Tests: parse cases and refusals (not an owner, unknown `%N`, done `%N`, same milestone); apply cases on a three-milestone project, including moving above the running one; a replayed command changes nothing twice.

**R3 Per-issue holds in history (closes #502).** When an autopilot item gets a hold and when the hold is lifted, the history differ writes one event each, with `issue_iid`, `milestone_iid`, the hold's reason and its start time. Keys are idempotent like the pause events.

Tests: differ tests for hold set, hold lifted, replayed update.

### UI server (Go)

**R4 Timeline endpoint.**
- Add `GET /api/v1/timeline` with either `?project=ns/name&milestone=iid`, or `?from=&to=` across the viewer's projects (optionally `?project=`).
- It answers rows: project, milestone iid and title, issue iid and title, wave, and segments (`running`, `waiting`, `held`, each with start and end, end empty while it goes on), plus `mergedAt` or `skippedAt`.
- The rows are built from `history_events` (Decision 3).
- Only projects the viewer may see appear. Without the research database the answer is `history: false` with no rows.
- The window may be at most 7 days. Segments are cut at its edges.
- Add view structs in `internal/ui` and run `make ts-types`.

Tests: segment building from a fixed event list (overlapping runs, a hold inside waiting, merge, skip, open issue up to now, cut at the window edges); visibility; no database.

### Web

**R5 Milestones tab.** Done by %41 (Decision 7). No issue for it; its tests stay green.

**R6 Draggable queue.**
- Add `vue-draggable-plus` (Decision 11).
- `ProjectQueue.vue` rows get a drag handle and a `UDropdownMenu` with *Move to top / Move up / Move down* next to *Pause / Resume*. *Run next* and *Clear mark* go away.
- A drop or a menu pick sends the `move` command (Decision 12). The row shows the new place as pending until `project.queue` reports it, and goes back with the error if the post or the operator's answer fails.
- The handle and the menu show only with `project.queueCommands`.

Tests: node tests for the command a move produces (drop index to `before`/`after`/`top`/`bottom`); e2e drag and menu on a three-milestone fixture.

**R7 Confirm above the running milestone.** A drop or menu move that puts a milestone above a `running` entry opens a `UModal`: "Move %5 above %3? %3's running issues finish, then %5 runs. %3 starts no new issues until %5 is done." Cancel puts the row back.

Tests: node test for when the confirmation is needed; e2e cancel and confirm.

**R8 Timeline layout.** A pure module `web/src/timeline.ts`:
- maps rows and a window to bar positions in percent;
- packs overlapping bars of one Overview row into lines;
- places the now line;
- writes each bar's accessible name and the axis ticks (hours for 6 h and 24 h, days for 7 d).

Tests: node tests for positions, packing, cut bars, ticks and names.

**R9 Timeline component.**
- `web/src/ui/Timeline.vue` draws the rows from R8 with Tailwind utilities.
- Bars are `RouterLink`s to the issue page, coloured from the tone tokens (running: progress, waiting: muted, held: warning) or, on the Overview, from a categorical milestone palette in the colour tokens of %36, in light and dark.
- Row labels stay on the left while the time axis scrolls sideways at 375 px.
- It reloads on live updates and moves the now line each minute.
- List it in the UI kit with its reason.

Tests: the conventions check (`npm test`); the accessibility check from %37 on a page with the timeline.

**R10 Milestone Issues tab.** Add the Timeline / Lanes switch (Decision 15) as a pill switch in the Issues tab's toolbar (the `UDashboardToolbar` from %41), above the queue in `MilestoneIssues.vue`. Timeline loads `GET /api/v1/timeline?project=&milestone=`. Lanes is today's `QueueList grouped`. The choice is kept in `?view=`.

Tests: e2e switch with and without the `history` feature.

**R11 Overview timeline.** Add a **Timeline** section after *Working now* in `DashboardOverview.vue`, with a 6 h / 24 h / 7 d pill switch in a `UDashboardToolbar` at the top of the section, as %41 does for tab filters (24 h default, kept in `?window=`) and the milestone legend. Without the research database it shows the same info alert as the milestone lists.

Tests: e2e with two projects and overlapping issues.

### Docs

**R12 User and reference pages.**
- `docs/guides/desk.md`: the Milestones tab, dragging, the timeline.
- `docs/guides/autopilot.md`: run order and moving milestones; drop run-next marks.
- `docs/reference/commands.md`: `move`, and `start` as `move top`.
- `docs/reference/desk-api.md`: `GET /api/v1/timeline`.
- `docs/reference/status-and-history.md`: `status.milestoneOrder` and the hold events.
- `docs/concepts/glossary.md`: "run order".

**R13 Internals and spec.**
- `docs/contributing/web-ui.md`: the Milestones tab, the queue's drag and menu, the Timeline in the UI kit, the new dependency and why.
- The spec `docs/specs/desk-timeline-and-milestone-order.spec.md` with a steering log, and its line in `docs/specs/README.md`.

## Constraints

- Under 300 production lines per merge request; tests, generated files and docs do not count.
- When a requirement has to be narrowed, open a follow-up issue for the rest and say so in the spec's steering log.
- Run `make generate manifests` after changing API types (R1) and `make ts-types` after changing view structs (R4); the CI drift job fails on stale files.
- One new dependency: `vue-draggable-plus` (Decision 11). No other new dependencies, Go or web. The bundle stays within `web/bundle-budget.json`; the library loads only with the project page.
- Nuxt UI first. The only new custom component is `web/src/ui/Timeline.vue`, listed in the UI kit. The `raw-control` check stays green.
- Keep working as today:
  - the milestone pages' tabs, the Activity feed and its filters (as %41 left them);
  - `/agent pause`, `/agent resume`, `/agent parallel`;
  - the planning turn rule for milestones that nobody moved: the seeded order equals today's rank.
- No kubebuilder RBAC markers in `internal/ui`. The timeline endpoint only reads.
- Before pushing: `gofmt`, `go vet ./...`, `make helm-lint`, the tests of the changed Go packages (`go test ./internal/<pkg>/...`), and `make ui` after web changes. After docs changes: `go test ./internal/docscheck/`. Do not run `make test` locally.
- User pages follow the docs rules: "you", short sentences, no source file names, under 400 lines, `Next` links.

## Non-goals

- A forecast of when waiting issues or queued milestones will run (Decision 6).
- Warning when a move puts a milestone before a milestone it "builds on" (%39).
- Ordering issues inside a milestone by drag; the plan's waves decide that.
- Ordering milestones across projects, or one queue for a project set.
- A timeline of research runs, plan chats or releases.
- Reconstructing holds from before R3 ships: older issues show no held segments.

## Suggested order

R1 → R2 → R6 → R7, R3 → R4 → R8 → R9 → R10, R11 → R12, R13

## Acceptance

- On a milestone with three issues where #11 and #12 ran in parallel and #13 waited for them, the Issues tab's Timeline shows #11 and #12 overlapping in W1 and #13 starting after both in W2. Each bar has running and waiting segments and a merge marker. Clicking a bar opens the issue. (Goal 1)
- On a milestone where #12 was held for an hour after this milestone shipped, its bar shows a held segment of that hour. (Goal 1)
- Without the research database, the Issues tab shows Lanes with no switch. (Goal 1)
- On the Overview with two projects running issues in the last 24 hours, the Timeline shows one row per project. Bars labelled `#iid` are coloured by milestone, with a legend. Overlapping bars of one project sit on separate lines. Switching to 7 d widens the window. (Goal 2)
- Opening a project page without `?tab=` shows the Milestones tab with the queue, Finishing, In progress and Recently done. An old link with `?tab=stream` shows the Activity tab with the feed and the composer. (Goal 3)
- On a project with queued %5, %6 and %7, an owner drags %7 above %6. After the next sync the queue shows %5, %7, %6. %7's tracking issue says "⏫ Moved before %6 (by …)". `/agent move after %6` on %7's tracking issue puts it back. A non-owner's `move` is refused. (Goal 4)
- Using only the keyboard, an owner opens a row's menu and picks *Move up*; the order changes the same way. (Goal 4)
- On a project where %5 is running, an owner drags %7 to the top. A confirmation appears. After confirming, %5 shows *Holding for %7*, its running issues finish, and %7 plans. Cancelling leaves the order unchanged. (Goal 5)
- Right after upgrading, a project whose %6 had a run-next mark keeps the same queue order as before. (Goal 4)

## Steering log

What this milestone narrowed or left to others, checked against main and
the milestone's commits after its last merge request:

- **Milestones tab (R5, Decision 7).** %41 had not landed when this
  milestone shipped, so the project page still has no *Milestones* tab. The
  draggable queue (R6, R7, #662) sits where `ProjectQueue` was, at the top
  of the project's *Stream* tab. The user docs describe it there. Moving it
  is left to %41.
- **Holds in history (R3)** shipped with `hold.set`, `hold.cleared` and an
  extra `item.skipped` event, so the timeline can draw the skip marker;
  #502 is closed by it. Holds from before that version are not rebuilt
  (Non-goals).
- **Timeline endpoint (R4)** caps an answer at 500 issues, newest first, and
  says so with `truncated`; the Overview shows a note then.
- **Now line (R9).** It moves with the Desk's shared 15-second clock, not
  once a minute; a fetch happens only on live updates.
- **Moves (R2).** A paused milestone can be moved; it stays paused at its
  new place. A replayed command posts no second note but writes the order
  again.
- **Accessibility (R9, %37).** The axe checks of the queue and the
  timelines pass except dark-mode colour contrast of the active pill and
  the project pages, which stays with the Desk-wide #641.
- **Bundle budget.** No commit of this milestone changed
  `web/bundle-budget.json`: `vue-draggable-plus` loads with the lazily
  loaded queue and the timelines in their own chunks, inside the existing
  caps.
- **Docs (R12, R13, #668).** The queue, dragging and the timelines
  got their own guide, `docs/guides/milestone-order-and-timeline.md`,
  because `docs/guides/desk.md` is at the 400-line cap (D668-P1).
- No follow-up issue is referenced by this milestone's code or commits.
