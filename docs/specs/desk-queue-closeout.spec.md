# Desk: run milestones in the order I choose, and close finished ones

Status: the milestone's spec, run by autopilot. Narrowed requirements and
requirements without an issue go into the [Steering log](#steering-log).

## Goal

1. An owner picks which autopilot milestone of a project runs next, from the
   Desk or with `/agent start`, without turning the others off.
2. The Desk shows each project's autopilot queue: what runs, what waits and
   why, in the order they will run.
3. Done milestones that GitLab still lists as open show what holds them, and
   an owner settles that from the Desk in one place.

## Decisions (locked)

1. **Queue order = marks first, then age.** A milestone gets a *run next*
   mark (`StartRequestedBy` plus `StartRequestedAt`) from `/agent start` or
   the Desk. `queuedAhead` and the planning hold rank marked milestones
   before unmarked ones (earliest mark first), then by age. The rule "at
   most one milestone starts new issues" stays: the marked milestone starts
   new issues, older ones hold. Active issues of the others finish (no
   preemption). Shared ranking in `internal/milestoneorder`.
2. **`start` lifts the hold.** `/agent start` on a milestone that already
   has a plan sets the mark too. The tracking issues of the milestones it
   passes say "⏭️ %N goes first (by …)". *Clear mark* (Desk) and `/agent
   start off` remove the mark.
3. **Desk commands.** `chatcmd` gains `start`, `unstart` and `endmilestone`
   for a milestone's autopilot, owners only like the tracking-issue words,
   answered like the others.
4. **Queue view.** The project page gets a *Queue* section above
   `MilestoneSections`: the open autopilot milestones in run order, each
   with state (running, holding for %N, waiting to plan, paused: reason,
   budget), issues done/total, spend, and the buttons Run next / Clear
   mark, Pause / Resume. A view struct from the operator's own ranking
   function, so the Desk and `pick` cannot disagree.
5. **Close-out view.** The project page gets a *Finishing* section for done
   milestones still open on GitLab, each with what holds it: retrospective
   being written, follow-ups to settle (link to the Retrospective tab),
   open issues (`CloseWaitsFor`, each with *Close issue*), companions not
   landed (link). *Close issue* closes it with the user's GitLab token and
   a note "closed from the Desk by …". Autopilot then closes the milestone
   as today. No "close anyway" button: the owner closes the issue or the
   milestone in GitLab.
6. **Overview count.** The Overview card of a project shows "N finishing"
   when the Finishing section is not empty.
7. **No change** to system prompts, CSP, budgets or the review rules.

## Requirements and where they were implemented

| Requirement | Implemented in |
| --- | --- |
| R1 `StartRequestedAt` and ranking (marks, then age) for `queuedAhead` and the planning hold | `api/v1alpha1` status fields, `internal/milestoneorder`, `internal/controller` (`queuedAhead`, pick hold); [design](../design.md#autopilot), [usage](../usage.md#several-milestones-at-once) |
| R2 `/agent start` marks a planned milestone; `start off` clears; passed milestones get the note | `internal/controller/autopilot.go` (`startNow`, `passedBy`); [commands](../commands.md#on-the-tracking-issue-autopilot) |
| R3 Desk `start`, `unstart`, `endmilestone` with owner check | `internal/chatcmd`, Desk dispatch; [ui](../ui.md#queue-and-finishing) |
| R4 Queue view from R1's ranking | `internal/ui/queue.go`, project detail; [ui](../ui.md#queue-and-finishing), [API](../ui.md#api) |
| R5 Finishing view and `POST …/issues/{issue}/close` | `internal/ui/finishing.go`, `close_issue.go`; [ui](../ui.md#queue-and-finishing), [API](../ui.md#api) |
| R6 Queue section with buttons | `web/src/components/ProjectQueue.vue`, `projectQueue.ts`; [ui](../ui.md#project-queue-and-finishing) |
| R7 Finishing section, *Close issue*, Overview *N finishing* | `ProjectFinishing.vue`, `finishing.ts`, `overview.ts`, `DashboardOverview.vue`; [ui](../ui.md#queue-and-finishing) |
| R8 docs | `docs/design.md`, `docs/ui.md`, steered [strict-milestone-order](strict-milestone-order.spec.md), this file |

Merged issues of this milestone (R1–R7 landed before R8): #406, #413, #415,
#416 and their related MRs on main. R8 is #417.

## Steering log

- Decision 10 of %10 ([strict-milestone-order](strict-milestone-order.spec.md)):
  `/agent start` now lifts the hold of step 1 via the run-next mark. The
  old note "does not lift the hold of step 1 yet" is replaced there.
- No requirement of this milestone was narrowed; every R1–R8 has a merged
  issue (see the table and Depends on of #417).
- R8 is documentation only; no release after it.
- Chart README: unchanged (it does not list Desk commands or the close
  endpoint).
