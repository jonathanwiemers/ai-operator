# Desk: the Cockpit newest first by issue, with Now & next and decisions

Status: the milestone's spec, run by autopilot. The milestone description in
GitLab holds the full text, with its background and file references; this
file keeps the goal, the decisions, the requirements, the constraints and
the acceptance, and the [Steering log](#steering-log). It builds on
[Desk: the Cockpit](desk-cockpit.spec.md) and [Desk: decisions](desk-decisions.spec.md). The behaviour is in
[Steer from the Desk](../guides/steer-from-the-desk.md),
[Read and revisit decisions](../guides/decisions.md),
[The Desk](../guides/desk.md),
[Desk API](../reference/desk-api.md) and
[Web UI development](../contributing/web-ui.md#cockpit).

## Goal

1. A milestone's Cockpit reads from the top, like a pull request: the
   composer to the pilot, the latest exchange with the pilot, the Needs you
   cards, one thread per issue with the latest activity first (and a
   Milestone thread for milestone-wide events), and *Load older* at the
   bottom.
2. Each issue thread shows the issue's state and current step in its
   header, its newest 3 items, runs, CI and reviews folded into one-line
   groups, and the rest behind *Show N more*. A decision appears in its
   issue's thread with its choice, and the owner can revisit it there.
3. Decisions have their own *Decisions* tab again on milestone and project
   pages, with search, superseded chains and Revisit.
4. A **Now & next** sidebar answers "what is going on and what comes next":
   Running, Waiting, Up next and Milestones. It collapses to a rail with
   counts, remembered per browser; below 1180 px a one-line strip under the
   header opens it.
5. The project Cockpit and the Overview follow the same pattern.

## Decisions

1. **Order (D1).** Composer, latest pilot exchange, Needs you cards, issue
   threads with the latest activity first, *Load older*. On narrow screens a
   one-line strip sits under the header. Rejected: pinned cards above the
   composer; Needs you cards inside their issue's thread.
2. **By issue means threads (D2).** Threads sort by latest activity; a
   Milestone thread holds events without an issue. Rejected: a Time / By
   issue switch; a fixed issue or wave order.
3. **An issue thread (D3).** Header with state and step, the newest 3 items,
   *Show N more*. Rejected: everything expanded; a header-only thread.
4. **The Decisions tab again (D4).** The side panel loses its Decisions
   view. Rejected: Revisit inside the narrow panel.
5. **Decisions in the stream (D5).** A `decision.recorded` event in its
   issue's thread, with Revisit in place; its ID links to the tab. Rejected:
   a link-only line.
6. **Now & next (D6).** The only place for "what is going on now"; it
   replaces the panel's Issues and Queue views and the live lines. Rejected:
   a section in the left app sidebar; a status block mixed into the stream.
7. **Which pages change (D7).** Milestone, project and Overview change
   together; the Loose issues tab only takes the order. Rejected: the
   milestone Cockpit only.
8. **One milestone (D8).** Rejected: a split into stream and decisions
   first, then the sidebar.
9. **The pilot conversation (D9).** The latest exchange sits under the
   composer, older ones behind *Show earlier*; a `pilot.acted` with an issue
   also shows in that issue's thread. Rejected: the whole conversation in the
   Milestone thread.

## Requirements

- **R1** A `decision.recorded` event, once per decision ID, with data
  `{id, title, choice, kind, supersedes}`, written by the history recorder.
- **R2** `cockpitThreads` replaces `cockpitItems`: the conversation and the
  threads, sorted by latest activity.
- **R3** The Cockpit top to bottom; the composer is not sticky.
- **R4** The issue thread card: header, newest 3, *Show N more*,
  `#issue-<iid>` anchors and the live transcript.
- **R5** Decisions in threads, with Revisit in place.
- **R6** The Decisions tab on milestone and project pages; old
  `?panel=decisions` links open it at the card.
- **R7** `nowAndNext` and the Now & next sidebar of a milestone.
- **R8** The rail, remembered in `localStorage`, and the strip below 1180 px.
- **R9** The project Cockpit with a History section in the sidebar.
- **R10** The Overview: one thread per project and Now & next per project.
- **R11** The Loose issues tab in the same order.
- **R12** Clean-up of what no page uses.
- **R13** Docs.

## Constraints

- No new dependencies and no new routes; Nuxt UI building blocks.
- One header and one row of tabs; the strip is not a second tab row.
- Every old URL keeps working: `?panel=issues`, `?panel=queue`,
  `?panel=decisions` and `#<decision-id>`.
- The pinned cards, the composer's chat, each kind's class (apart from the
  new kind) and `GET …/stream` stay as they are.

## Non-goals

- Issue threads or Now & next on the Loose issues tab.
- A Time / By issue switch, or filter chips.
- `decision.recorded` events for decisions whose rows have expired.
- Other new stream kinds; live push of the sidebar beyond today's updates.

## Acceptance

1. The Cockpit shows, from the top: the composer, the latest exchange, the
   stuck issue's problem card, then the threads, the newest on top, with
   *Load older* last.
2. A thread's header says "Implementing · m min"; its CI rounds are a closed
   group; *Show N more* reveals older items.
3. A decision shows in its issue's thread; Revisit there sends the revisit;
   an old `?panel=decisions#D712-1` link opens the tab at that card.
4. At 1280 px the sidebar shows Running, Waiting, Up next and Milestones,
   collapses to a rail that survives a reload; at 390 px a strip opens it and
   Escape returns focus.
5. A project shows threads of all its milestones and a sidebar with Running
   grouped by milestone and a History section; the Overview shows one thread
   per project; the Loose issues tab shows its composer first.

## Steering log

Collected from the milestone's merge requests, as their commits record them
(the merge request numbers are in GitLab).

- **R1** History: a `decision.recorded` event per stored decision
  (`1d3bdcf4`).
- **R2** Web: `cockpitThreads`, the conversation and issue threads, pure
  (`4b8fdf7f`). **R7** `nowAndNext`, the pure model (`d0226243`).
- **R3** The Cockpit top to bottom (`6795c4b3`). **R6** The Decisions tab
  again (`e5105028`).
- **R4** The issue thread card (`a8e925b2`). **R5** Decisions in threads with
  Revisit in place (`287a50a7`).
- **R7** The Now & next sidebar on a milestone, replacing Issues, Queue and
  the live lines (`be7eb972`). **R8** The rail and the strip (`abdeb25b`).
- **R9** The project Cockpit with Now & next and a History section
  (`9f42fc57`). **R10** The Overview Cockpit with Now & next per project
  (`6dc4959c`). **R11** and **R12** Loose issues in the Cockpit order and the
  final clean-up (`f8b90be3`).
- **Rail state key.** The rail's state is kept under `desk.sidebar`
  (D821-P1), the same on every page.
- **Docs (R13), narrowed: the screenshots were not retaken.** The agent
  container has no Chromium for Playwright and no `cwebp`, so
  `make docs-screenshots` could not run. The scenes of `web/e2e/docs.shots.ts`
  already point at the new layout (pinned cards, the Now & next sidebar), but
  the images in `docs/assets/screenshots/` still show the Desk before this
  milestone, and no page embeds the three "to retake" ones. **Follow-up:** the
  issue "Docs: retake the Cockpit screenshots" from the
  [Cockpit spec](desk-cockpit.spec.md#steering-log) covers it; run
  `make docs-screenshots` on a machine with Chromium and `cwebp`, add a shot
  of Now & next as a phone slide-over, and put the images back on their
  pages.
- No other requirement was cut.
