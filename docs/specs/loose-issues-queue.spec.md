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
  repositories, releases and a retrospective for the queue. The pilot, the
  problem cards and the Inbox items came with the next milestone, below.

## The pilot: a pilot for the queue that starts fresh every day or every five issues

The follow-up milestone "Loose issues: a pilot for the queue that starts
fresh every day or every five issues" replaced Decision 4: the queue got a
pilot that works in rounds.

### Goal

1. The queue's pilot looks into stuck queue issues, red CI and stalled runs,
   answers the owners on the `🤖 Loose issues` issue (`/pilot …`) and in a
   composer on the Loose issues tab, and acts under the same autonomy rules.
2. It works in rounds. A round ends after 5 finished queue issues or 24
   hours, and the next one starts with a fresh session that knows the last
   one from a round note and the queue's state.
3. A held queue issue or an open assessment is a problem card on the tab and
   an Inbox item, so notifications reach it.
4. The tab shows the round: number, start, finished issues and spend.

### Decisions (locked)

1. **Rounds: 5 issues or 24 hours**, `looseIssues.pilot.roundIssues` (0–50)
   and `roundHours` (0–168), the first limit wins and 0 is off. Hours count
   from the round's start. Rejected: a fixed time of day, and a reset after
   every issue.
2. **What carries over**: a round note of at most 1500 characters, posted on
   the tracking issue, or a facts-only note when the call fails. The next
   round gets the note, the queue's state and the open prompts and
   proposals; the session is new.
3. **A reset waits for a running call.**
4. **The budget counts per round**: `pilot.maxBudgetUSD` caps each round.
5. **The milestone pilot's tools, minus the milestone ones.** Allowed:
   retry, reReview, instruct, retryPipeline, rebase, mergeBase, fixFlaky
   (into the queue), openIssue (not taken into the queue), markDone, pause
   and resume; skip becomes a drop. Refused: revisit, setRule, forgetRule,
   editSpec, endMilestone, unskip, startAnyway and raiseBudget. Autonomy
   follows `pilot.autonomy`, `managed` as on autopilot.
6. **One pilot task per queue**, a Coordinate task with `spec.queueTask`;
   rounds are a status (`status.autopilot.round`), not tasks.

### Requirements

R1 round settings and status · R2 the queue's pilot task, prompt and wake
events · R3 actions on queue issues · R4 rounds · R5 problems and the Inbox
· R6 the pilot on the Loose issues tab · R7 docs.

### Steering log

- No requirement was narrowed, so no follow-up issue was opened.
- The docs landed in one merge request at the end (R7), as the plan decided.
- The note is asked for with a run request of its own
  (`spec.source.roundNote`, answered in `status.pilot.roundNote`). The
  operator copies the round to the pilot as `spec.source.pilotRound`, and
  the runner drops the session when that number changes.
- Comments and messages that reach the pilot while it writes its note wait
  for the next round.
- markDone takes only a pending queue issue, never an active one, so the
  merge gate is not passed by.
- On the tracking issue, `skip` stays "not on the loose issues queue"; only
  the pilot's skip becomes a drop.
- The tracking issue's round line is in UTC and says "no time limit" when
  `roundHours` is 0. The tab's line is in local time and leaves that out.
- A hold and an assessment on the same issue are one card, with the prompts
  inside it. The cards carry no kind label.
