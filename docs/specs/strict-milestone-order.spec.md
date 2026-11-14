# Autopilot milestones one after another

## Problem

`maxConcurrentRuns: 1` limits runs, not milestones. The queue is
work-conserving, so a younger milestone takes the slot whenever the older one
waits for CI or a pause. On 2026-11-06 four milestones of ai-operator (%7, %9,
%10, %11) each held an open merge request. Every merge moved main, the next MR
needed a rebase, a rebase over the same files changed the diff, and the
reviewed change no longer counted: !224 paused at 13:08 with a green pipeline,
waited behind implement runs for a redundant re-review, and #179 cost $32.52.

R12 of %10 already stops a new milestone from planning while an older one is
open, but its decision 11 exempts milestones that have a plan.

## Goals

- At most one milestone of a project starts new issues.
- A rebase pause ends by itself once the new head is reviewed and green.
- The pilot cannot order a re-review that cannot change anything.
- A review-only run does not wait behind implement runs.

## Non-goals

- Stopping or preempting active issues: they finish.
- Milestones without autopilot: no queue, unchanged.
- Other `mergeRequest` pauses (approvals, draft, a hand push): a human acts on
  those, they keep waiting for `resume`.
- Desk display of the waiting state: R13 of %10.

## Constraints

- No API type changes while %10 and %11 are in flight.
- Under 50 production lines.

## Approach

1. `pick` does not start a pending issue while `queuedAhead` lists open
   autopilot milestones ahead of it. Same rule as R12 (done, off and closed
   no longer count; paused does), with ranking from
   `internal/milestoneorder` (run-next marks first, then age; see the
   [desk-queue-closeout](desk-queue-closeout.spec.md) steer of Decision 10).
   Skipping closed issues and recording hand merges go on. The tracking
   issue says "⏳ waiting for its turn: %7, %9 go first".
2. `pauseHead` is `pauseMR` with `ResumeWhenReady`, used by the `unrebased`
   paths of `rebased` and by "someone pushed to the branch". `autoResume` holds
   such a pause while the MR's head is not the run's `PushedSHA`: the status
   still describes the older head, and `drive` would pause again at once. A
   rebase that changed the diff also asks for a review-only run of the new head
   (`ReviewFor`), which takes the head over, so no human has to start it. A
   hand push gets no such run: it may not be finished.
3. `validAction` refuses the pilot's `reReview` of a head that is fully
   reviewed (`RunStatus.FullyReviewed`). It stays allowed after a failed run,
   with reviewer errors, blocking findings or an unreviewed companion.
4. `QueueLess` ranks a pending review-only run right after the urgent lane.

## Steer on %10

Decision 11 of %10 now covers planning only. Decision 10 (`/agent start`) of
%10 now lifts the hold of step 1: a run-next mark
(`status.autopilot.startRequestedBy` plus `startRequestedAt`) ranks the
milestone first in `queuedAhead` and the planning hold (marked before
unmarked, earliest mark first, then age). `/agent start` on a planned
milestone sets the mark; `/agent start off` clears it. Passed milestones'
tracking issues say "⏭️ %N goes first (by …)". See
[desk-queue-closeout.spec.md](desk-queue-closeout.spec.md).

## Affected

`internal/controller/autopilot.go`, `agentproject_controller.go`,
`pilot_actions.go`, `api/v1alpha1/queue.go`, `crossrepo.go`, `docs/usage.md`,
`docs/design.md`, `docs/configuration.md`.

## Risks

- A slot stays idle during CI waits: slower, on purpose.
- One open pause stops the whole project's milestones. Step 2 removes the most
  common pause that never lifted; Orbit has its own queue.
- A review-only run jumps ahead of a task without a milestone. It is short. A
  review of a moved head does not rank (its key is not the status head yet),
  which only matters while older milestones still drain.
- `validAction` has no MR: in the rare window of a failed rebase whose branch
  moved, the status looks fully reviewed and the pilot's `reReview` is refused.
  `retry` and `rebase` paths stay.
- A "blocked by" link from an older issue to a younger milestone's issue would
  deadlock under the hold. It needs Premium links; a human removes the link or
  uses `startAnyway`.

## Validation

Unit tests: the hold (active issue finishes, paused older holds, done releases),
the rebase pause (old head does not end it, new reviewed green head merges),
`validAction`, `QueueLess`. Live: %7 finishes, %9 goes on, %10 and %11 hold.

## Delivery

One MR. After its release, `/agent resume` on #195 and #213 lifts the manual
pauses from 2026-11-06; the hold then keeps the order.
