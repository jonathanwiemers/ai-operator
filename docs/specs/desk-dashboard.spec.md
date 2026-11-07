# Desk: dashboard, milestone pages, pilot and retrospective tabs

Status: approved by the maintainer (2026-11-06) as the milestone spec of
"Desk: dashboard, milestone pages, pilot and retrospective tabs", with the
decisions below locked.

## Goal

1. `/` is a dashboard across all projects: what runs now, every milestone in
   work with its progress and cost, and the milestones done recently.
2. A click on a milestone shows its issues with progress, time and cost. A
   click on an issue shows its stream. This works for done milestones too,
   also after their tasks are deleted.
3. Each milestone has a **Pilot** tab: the chat and what matters to the owner,
   apart from the runtime stream.
4. Each milestone has a **Retrospective** tab. An owner can turn the leftovers
   into a follow-up milestone, open them as issues or dismiss them. GitLab
   keeps its comment.
5. Every view has breadcrumbs, so going back a few levels is one click.

## Background

File refs are at origin/main, 2026-11-06.

- `/` is the *Everything* stream, one mixed list of events
  (`web/src/views/Stream.vue`). There is no view of milestones across
  projects. The queue per milestone lives in the context panel.
- A closed milestone is no longer planned, so the operator deletes its tasks
  (`agentproject_controller.go:541`). `GET …/milestones/{iid}` needs them
  (`internal/ui/milestone.go`, `loadMilestone`) and then returns 404.
- `history_events` and `agent_calls` outlive the tasks (`research.historyRetention`,
  default 8760h), with `milestone_iid` and `issue_iid`. So events and spend
  per issue survive. The milestone's title, its issue list, their states and
  MRs do not.
- `status.pilot.retrospective` holds `noteID`, `postedAt`, `followUps` (title,
  body) and `created`. The retrospective's text exists only as the GitLab
  note, and the follow-ups go with the task.
- `history.Recorder` (`cmd/operator/main.go:147`) is the operator's writer and
  watches task status. The UI reads the database and cannot write to it.
- The pieces of the Pilot tab exist in `ContextPanel.vue`: `AskPilot`, the
  prompt cards, `PausesCard`. Event kinds are in `internal/history/differ.go`.
- The tabs are in `web/src/streamTabs.ts` (`?tab=`, Stream, Board, Spend).
- Only the Spend tab has breadcrumbs (`spendCrumbs`, `web/src/spend.ts:108`).
  Routes are in `web/src/main.ts`.
- `gitlab.API` has `CloseMilestone` but no way to create one. The UI acts with
  the user's own GitLab token (scope `api`) for steering.
- `create 1 2` opens follow-ups as issues (`pilot_followups.go`). Autopilot
  keeps a milestone open until every follow-up is opened or 7 days passed
  (`followUpsSettled`), so unwanted leftovers hold it open.
- Depends on the milestone "Pauses say why, spend adds up, planning waits for
  the milestone's start": its `waiting` state, its pause reasons and its spend
  totals. Read that milestone before planning. Where its fields are not on
  main yet, the view treats them as absent.

## Decisions (locked)

1. **Slices, no big bang.** Every slice ships on its own and leaves the Desk
   working. The old views stay until their replacement is merged.
2. **Snapshot in Postgres.** New tables hold each milestone and its issues,
   written by the recorder as status changes, not only at close. Without the
   research database the dashboard shows what the tasks give, without done
   milestones.
3. **`/` is the dashboard, no second page.** Tabs *Overview* and *Stream*; the
   old mixed stream moves to *Stream*. A project page uses the same milestone
   list for that project.
4. **Overview, top to bottom:** summary strip (milestones running, paused,
   waiting, the needs-you count, spend today and last 7 days); *Working now*
   (per project the issue that holds the slot, for how long, what is next,
   engine limits); *In progress* (one flat list across projects: needs you
   first, then running, then waiting); *Recently done* (newest 10, then
   *Show all*).
5. **A milestone row:** project, title, state, progress bar, merged of total,
   open issues, cost, tokens. A row with leftovers says how many.
6. **Issues without a milestone stay out** of the overview. The Inbox covers
   what needs someone.
7. **Milestone tabs:** *Issues* (default), *Pilot*, *Retrospective*, *Stream*,
   *Board*, *Spend*. `?tab=stream` links keep working. The context panel stays
   on project pages only.
8. **An issue row:** title, state, MR, wall time, agent time, paused time, cost,
   tokens. Wall time runs from the first `run.started` to the merge (or now).
   Agent time sums `duration_ms`. Paused time comes from the pause history.
9. **Issue page** `…/milestones/<iid>/issues/<issue>`: the same figures, then
   the issue's stream. With a live task it holds what the Task page holds. Without
   one it shows history; a live transcript is gone with its pod. Task URLs stay.
10. **The Pilot tab is fixed by event kind, no filter:** questions, pauses, what
    the pilot did, the owners' messages, decisions, the retrospective. *Stream*
    keeps runs, pushes, CI, reviews and merges. An event is in one of the two.
11. **Retrospective text is stored** when posted, as markdown without HTML: it
    is untrusted. The GitLab comment stays.
12. **Follow-up milestone.** An owner ticks leftovers and asks for a draft. While
    the milestone has a pilot, the pilot writes it; a closed milestone starts
    from the follow-ups' titles and bodies. The owner edits it in the Desk and
    confirms. Creating it starts autopilot, so the button says so. It is created
    with the owner's GitLab token and carries the owners and autopilot lines.
13. **Settled by a line.** A new line `/agent followup-of <iid> <numbers>` names
    what the new milestone took over. The operator reads it and marks those
    settled, for open and closed milestones alike, so the UI writes nothing to
    the database. Like the owners line, it is no part of the spec hash.
14. **Dismiss.** On an open milestone, owners can dismiss the remaining leftovers
    from the tab; `create none` in the retrospective's thread does the same.
    Both settle them, so the milestone can close.
15. **Backfill.** The operator fills the snapshot once from GitLab: titles,
    issues, states, MRs and the retrospective notes of closed and done
    milestones, one milestone per poll. Spend shows only what `agent_calls`
    holds, so the oldest milestones show none, never $0.
16. **Breadcrumbs everywhere, in one place.** One component in the app's
    header, built from the route, on every view:
    Overview › project › %milestone › #issue, with the task page under its
    issue when it has one and under the project otherwise, and
    Research › research › runs › run. Top-level views show one crumb. Every
    crumb but the last is a link, and the last names the current view. The
    tab stays out of the trail; a crumb opens that level's default tab.
    Spend's own drill-down keeps its crumbs below the header.
17. **Crumbs need no extra request.** Names come from the view already loaded.
    Until then a crumb shows the iid or the route's name. On narrow screens
    the middle crumbs fold behind `…`, and the first and last stay.

## Requirements

Store

- R1 Migration: tables for milestones (project, iid, title, state, phase,
  started and done times, tracking issue, retrospective text and posted time)
  and their issues (iid, title, state, MR, order) and follow-ups (number, title,
  body, state: open, issue, milestone or dismissed, and the target's iid).
  Retention follows `research.historyRetention`.
- R2 The recorder upserts them from task status. A repeated write changes
  nothing. Deleting a task deletes no row.
- R3 Store queries: a milestone list with progress and spend for many projects,
  one milestone with per-issue figures (decision 8), in a fixed number of
  queries, not one per milestone.
- R4 Backfill (decision 15), restartable, recorded per milestone. It keeps
  well below GitLab's limit of 100 requests a minute per user.

API

- R5 `GET /api/v1/milestones`: the rows of decision 5 for the projects the user
  may see, with `state` and `limit` filters. `GET /api/v1/overview`: the strip
  and *Working now*.
- R6 `GET …/milestones/{iid}` answers from the snapshot when the tasks are gone,
  and says so in the view. The issue view likewise.
- R7 Totals count only projects the user may see, as every other view does.

Views

- R8 Dashboard at `/` (decisions 3 and 4), updating live, working on narrow
  screens. Without the database, a note says done milestones need it.
- R9 Milestone *Issues* tab and the project page's list (decisions 5, 7, 8).
- R10 Issue page (decision 9).
- R11 *Pilot* tab (decision 10): the composer goes to the pilot. The pause card
  and the prompt cards are here. Links with `#prompt-Q<n>` and the Inbox's links
  to a paused milestone open this tab.
- R12 *Retrospective* tab: text, cost drivers, the leftovers with their state and
  links to what came of them. A note while no retrospective exists.
- R13 Context panel: moved parts removed from milestone pages (decision 7).

Follow-ups

- R14 The draft (decision 12), through the existing command path to the pilot.
  The draft's text is untrusted until the owner confirms it.
- R15 `gitlab.API` creates a milestone. `POST …/milestones/{iid}/followup` creates
  it with the user's token, for owners with the `steer` grant only, and refuses
  a follow-up already taken.
- R16 The `followup-of` line (decision 13), in `rules`, ignored when it names an
  unknown milestone or number, with one notice on the tracking issue.
- R17 Dismiss and `create none` (decision 14), owners only.

Docs and data

- R18 `docs/ui.md`, `docs/usage.md`, `docs/commands.md`, `docs/design.md`,
  `docs/configuration.md` and the spec copy `docs/specs/desk-dashboard.spec.md`
  with each slice. `hack/research-seed.sql` holds a running, a paused, a waiting
  and a done milestone with a retrospective, so every view shows in dev.

Navigation

- R19 Breadcrumbs (decisions 16 and 17), with done milestones and deleted tasks
  too. A pure function maps a route to its crumbs, so every route is tested,
  and a route without crumbs fails that test. `spendCrumbs` reuses it where
  the two overlap.

## Constraints

- Every UI MR has screenshots from `make run-ui` on seeded data, wide and narrow.
- View types are generated (`make ts-types`).
- The UI's service account stays read-only; the UI's database role stays a
  reader.
- Retrospective text, follow-ups and drafts are untrusted: markdown without HTML.
- Nothing in the views calls GitLab per row; steering calls use the user's token.
- System prompts stay unchanged (prompt cache), except the draft's own prompt.
- %7, %9 and the milestone named in Background are in flight. Rebase, do not
  rework their changes.
- The default budget is under 50 production lines per MR; split by requirement.

## Non-goals

- Issues without a milestone in the overview.
- Editing or deleting a retrospective in the Desk.
- Live transcripts of finished runs.
- A Bloop-specific view; everything is per AgentProject.
- netcup01 config. Nothing here needs an infra change.

## Suggested order

1. Spec copy, then R1, R2 and R3.
2. R19 on today's routes, then R5, R6, R7, R8 and R9.
3. R10.
4. R11, R13.
5. R12, then R14, R15, R16, R17.
6. R4 last, once the tables have settled.
7. R18 with each slice, one last pass at the end.

## Acceptance

- `/` shows all milestones in work across projects, sorted as in decision 4, and
  a user sees only their projects' numbers.
- A closed milestone opens with its issues and figures after its tasks are gone,
  and its issue page shows the stream.
- A stored row survives the task's deletion; a repeated write changes nothing.
- The Pilot tab and the Stream tab share no event, and a `#prompt-Q<n>` link
  lands on the prompt.
- The retrospective reads the same in the Desk and on GitLab.
- Every route shows breadcrumbs, each ancestor is one click away, and they fold
  on a phone.
- From a done milestone, ticked leftovers become a milestone with the owners and
  autopilot lines, and they show as taken. A second attempt is refused.
- Dismissing on an open milestone lets it close without waiting 7 days.
- A non-owner sees all of this read-only.
- The backfill can stop and restart, and shows old milestones without inventing
  spend.
- The Desk works without the database, without done milestones.

## Slices

The slices follow the suggested order. Each changelog line below names the
slice by its key (the first word of this list).

1. `spec-copy`: this file (#214).
2. `store`: migration, recorder upserts and store queries (R1, R2, R3).
3. `crumbs`: the breadcrumb function and header component on today's routes (R19).
4. `milestone-api`: `GET /api/v1/milestones`, `/api/v1/overview`, snapshot
   fallback and per-user totals (R5, R6, R7).
5. `dashboard`: the Overview at `/` and the milestone Issues tab with the
   project list (R8, R9).
6. `issue-page`: the issue page with figures and stream (R10).
7. `pilot-tab`: the Pilot tab and the context panel cleanup (R11, R13).
8. `retrospective`: the Retrospective tab (R12).
9. `followups`: draft, milestone creation, the `followup-of` line, dismiss and
   `create none` (R14, R15, R16, R17).
10. `backfill`: the one-time fill from GitLab (R4).
11. `docs-seed`: the final docs pass and `hack/research-seed.sql` (R18).

## Changelog

One line per slice, appended when it lands: date, slice key, what landed,
about how many production lines, and deviations from this spec.

- 2026-11-06, `store` (R1, #215): migration `0006_desk_snapshot` with
  `desk_milestones`, `desk_milestone_issues` and `desk_follow_ups`, the store's
  `SaveMilestone`, `CloseMilestone` and `DeleteExpiredSnapshot`, writers
  revoked, retention wired; about 210 production lines (SQL included), over
  the budget because the issue asked for the whole store side of R1 at once.
  Deviations: issues and follow-ups reference their milestone (`ON DELETE
  CASCADE`) and retention deletes a milestone only once all its rows expired;
  `CloseMilestone` takes the new expiry too; R2's recorder and R3's queries
  follow.
- 2026-11-06, `store` (R2, #216): the recorder's snapshot queue
  (`internal/history/snapshot.go`): `Snapshot` of spec tasks, coalescing
  `RecordSnapshot` and `RecordClosed`, adds, updates and the recorder's start
  snapshot, a delete closes; about 150 production lines. Deviations: the
  tracking issue is the autopilot's (the recorder sees no Coordinate task);
  `web_url` stays empty; issue states and the phase are stored in lower case.
- 2026-11-06, `store` (R3 list, #217): `Store.Milestones`, the milestone list
  with progress, open follow-ups and spend for many projects in one statement;
  about 70 production lines (SQL included). Deviations: spend includes the
  legacy runs' cost as the spend views do; open milestones sort oldest start
  first, ahead of closed ones; a limit of 0 means the cap of 200.
- 2026-11-06, `store` (R3 detail, #218): `Store.Milestone` and
  `Store.IssueFigures`, one milestone with per-issue figures (decision 8) in
  five statements; about 150 production lines (SQL included), the list's
  statement shared with the detail. Deviations: `IssueFigures` carries
  `WallSec` too; spend and events count for an issue only within its
  milestone; an issue missing from the snapshot is not found.
- 2026-11-07, `crumbs` (R19, #220): `routeNames` (the routes are typed
  against it), the pure `crumbsFor`, the `crumbNames` store and
  `Breadcrumbs.vue` in the app's header on every view; the per-view crumb
  lines of the task and research pages are gone, `spendCrumbs` reuses the
  milestone and issue labels; about 200 production lines. Deviations: with no
  issue page yet, a task with an issue ends in the issue's crumb (the issue
  names the view); a plan task stays under the project; a research run is
  labelled by its start once loaded.
- 2026-11-07, `milestone-api` (R5 list, R7, #221): `GET /api/v1/milestones`
  (`internal/ui/milestones.go`): live rows from the visible projects' spec
  tasks merged with `Store.Milestones` in one call, `needsYou` from the
  Inbox, `state` and `limit` filters; about 280 production lines, over the
  budget because the issue asked for the whole list endpoint. Deviations:
  `waiting` is a running autopilot queued behind older milestones
  (`queuedBehind`, the dependency's turn); a snapshot row still open whose
  tasks are gone is in no list; follow-ups count from the snapshot when there
  is one; a live milestone without a snapshot row keeps the tasks' cost.
- 2026-11-07, `overview-api` (R5 overview, R7, #222): `GET /api/v1/overview`
  (`internal/ui/overview.go`) and `Store.SpendSince` (one query for many
  projects); about 190 production lines, over the budget because the issue
  asked for the whole endpoint. Deviations: the Spend API takes no `tz`, so
  today is the UTC day; the 7 days are today and the 6 before; needs-you is
  the Inbox's count, items without a milestone included; *next* is the first
  queued implement task in queue order, else the first pending issue of the
  oldest running autopilot.
- 2026-11-07, `milestone-api` (R6 milestone, #223): `GET …/milestones/{iid}`
  carries `state` and `issues` (live: the plan's and the autopilot's items as
  the snapshot orders them, figures from `Store.Milestone` in one call) and,
  without a spec task, answers from the snapshot with `stored: true`; about
  180 production lines, over the budget because the issue asked for both
  parts. Deviations: the figures are a nested `figures` object, absent
  without the database; `spec` stays a zero task, so `web/` is unchanged; a
  stored milestone still open is `planned`; a failing snapshot query answers
  500, not 404. The issue view's snapshot fallback follows with R10.
- 2026-11-07, `dashboard` (R8 first part, #224): `Dashboard.vue` on `/`
  (route `overview`; `everything` redirects to `/?tab=stream` from
  `/everything`), tabs from `dashboardTabs`, the Stream tab is
  `Stream.vue` unchanged; `DashboardOverview.vue` with the strip
  (`overviewStrip`) and *Working now*, placeholders for the lists; the
  rail's *Everything* is now *Overview*; about 230 production lines (styles
  included). Deviations: no spend chips without the database (unknown, not
  $0); active usage limits show as *limited until*; screenshots were not
  possible in the agent's container (no cluster, no browser).
- 2026-11-07, `milestone-list-views` (R8 lists, R9 project page, #225):
  `milestoneRows.ts` (`progressPct`, `rowCost`, `leftoversLabel`,
  `sortInProgress`), `MilestoneList.vue` and `MilestoneRow.vue`, both
  sections in `MilestoneSections.vue`, used by the Overview and, with the
  new `?project=ns/name` filter of `GET /api/v1/milestones` (404 for a
  project the user may not see), on the project's *Stream* tab above the
  composer, the context panel kept; about 250 production lines (styles
  included). Deviations: without the database *Recently done* shows a
  note instead of the live done rows, so done milestones show only with
  it; *Show all* reloads the section with `limit=200`; screenshots were
  not possible in the agent's container (no cluster, no browser).
- 2026-11-07, `milestone-issues-tab` (R9 milestone, #226): `projectTabs`,
  `milestoneTabs` (Issues default) and `storedMilestoneTabs` (no Board) in
  `streamTabs.ts`, `carryTab` for the scope links; `MilestoneIssues.vue`
  (state, progress, cost, the stored note, one row per issue, cards on
  narrow screens), `durationShort` in `format.ts`, `issueProgress` and
  `issueStateTone` in `milestoneRows.ts`. A stored milestone hides Board and
  the message box. Deviations: the title stays in the page header, not
  repeated in the tab; a row without a task is not linked (D226-P2); the
  context panel stays on milestone pages until R13; screenshots were not
  possible in the agent's container (no cluster, no browser).
- 2026-11-07, `issue-api` (R10 API, R6 issue half, #227): `IssueView` and
  `GET …/milestones/{iid}/issues/{issue}` in `internal/ui/issue.go` (the
  issue's row of the milestone view: live from the spec task's snapshot
  with its implement task and `IssueFigures`, else `stored: true` from the
  snapshot; 404 for an issue not in the milestone, D227-P2), and the
  stream's `issue=` filter (with project and milestone, D227-P1). Deviation:
  the filter is a new `StreamQuery.Issue` (`issue_iid` and a milestone that
  is this one or none), not `Issues = [iid]`, which would keep all of the
  milestone's events; its chat rows are those of the issue's tasks.
