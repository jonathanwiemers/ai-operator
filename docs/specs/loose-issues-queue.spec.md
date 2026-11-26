# Loose issues: a project queue that runs and merges the issues you pick, without a milestone

Status: the milestone's spec, run by autopilot. Narrowed requirements and
in-flight changes go into the [Steering log](#steering-log). The behaviour is
in the [guide](../guides/loose-issues.md); when this file and the docs
disagree, the docs win.

## Goal

1. An owner opens a project's **Loose issues** tab in the Desk, ticks open
   issues without a milestone and clicks **Hand to autopilot**. On GitLab,
   `/agent take #350 #421` on the queue's tracking issue does the same.
2. The taken issues run, up to `looseIssues.maxParallel` at a time, and are
   merged once CI is green and the review is clean, beside the milestones.
3. The queue has its own owners: GitLab users it mentions (`owners`) and Desk
   logins that may take issues and answer questions (`ownerSubjects`).
4. A problem holds only its own issue. The tracking issue and the tab say why.
5. With the queue on, a flaky job's `ci::flaky` issue goes into the queue.

## Decisions (locked)

1. **A separate project-level queue**, with its own owners and merge path.
   Rejected: handing issues into a running milestone's queue, and a Desk list
   with merging by hand.
2. **Any open issue without a milestone, picked by an owner.** Nothing joins by
   label: the owner's take is the trust gate.
3. **The Desk tab and the tracking issue** are the two ways to take.
4. **You select, and there is no queue pilot.** A stuck issue is held and
   waits for an owner.
5. **A `Queue` task, not a hidden milestone.** It reuses the autopilot's gate,
   merge, rebase, hold and tracking code, and skips planning, dependency
   order, releases, closing, the retrospective and settling.
6. **`spec.looseIssues`**: `enabled` (false), `owners` (falls back to
   `notify.users`), `ownerSubjects` (no fallback), `maxParallel` (1 to 10,
   default 1).
7. **Beside the milestone queue.** Not in the run order; a problem holds only
   its own issue; the freeze applies with `mainHealth.freezeAutopilot`; the
   queue plans no releases.
8. **First taken, first started.**
9. **Flaky issues go to the queue** with `looseIssues.enabled`, keeping the
   `FlakyFix` flag and fixed task.
10. **An issue leaves** when merged, closed, moved into a milestone or dropped.
    Dropping an active issue leaves it in `agent::review` for a human.

## Requirements

R1 `spec.looseIssues` and the `Queue` task type · R2 the Queue task and its
tracking issue · R3 take and drop · R4 run, gate and merge · R5 steering
commands · R6 flaky issues · R7 the loose issues view · R8 the Loose issues
tab · R9 docs.

## Steering log

- The docs landed in one merge request at the end (R9), as the plan decided.
- Held is not a state of its own: an item is `Active` with a `hold`. The
  tracking issue and the view show it as held.
- A take of an issue already in work says "(active)" in its comment instead of
  a position, and the item is active at once.
- `/agent merge`, `rebase`, `mergeBase`, `pause` and `resume` share the
  milestone's dispatch; `pause` and `resume` answer in queue words.
- Other tracking-issue words (`move`, `release`, `endMilestone`, `parallel`,
  `skip`) are answered "not on the loose issues queue".
- Left items carry `leftReason` and `leftAt`; the status keeps the newest 50
  finished items.
- Not done, as the milestone's non-goals say: a pilot for the queue, problem
  cards and Inbox items for holds, reordering and priorities, companion
  repositories, releases and a retrospective for the queue.
