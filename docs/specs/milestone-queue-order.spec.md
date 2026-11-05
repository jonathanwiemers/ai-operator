
# Oldest milestone first: an order for the run queue

## Goal

When a project's run slot frees up, the task that should go next gets it:
first the fix for a red default branch, then work somebody asked for outside
a milestone, then the oldest milestone. Finishing a milestone beats starting
the next one. Today the task that happens to check first wins. The Desk says
why a task waits and who is ahead of it.

## Background

What happens today (file refs at origin/main 592ab63, 2026-11-05):

- Every milestone on autopilot has its own queue: one issue at a time, in
  dependency order, then plan order. All open autopilot milestones run side
  by side; nothing orders them against each other.
- `maxConcurrentRuns` bounds the tasks in flight per project (netcup01: 1,
  `defaults.yaml`). The check is in `AgentTaskReconciler.Reconcile`
  (`agenttask_controller.go`, `if pod == nil`): it counts the other tasks in
  phase `Running` (`runningTasks`, pilots excluded). If the project is full,
  the task goes to `Queued` with "waiting for capacity (1/1 runs in flight)"
  and tries again after `queueRetry` (30 s).
- There is no order in that check: no priority, no age. Whichever task
  reconciles first after a slot frees up takes it.
- A run deferred by a usage limit is `Waiting` and takes no slot.
- The controller sets no `MaxConcurrentReconciles`, so tasks reconcile one at
  a time. The count and a new check in the same place do not race with each
  other.
- `AgentTask.spec.milestone` holds the milestone (`MilestoneRef`: id, iid,
  title). Spec and implement tasks both have it. The autopilot's main-red
  fix is `AutopilotState.MainRedFix`; the task itself does not say so.
- Real case, ai-operator on 2026-11-05: %6, %7, %8 and %9 were all open with
  one issue each in `agent::working`. %9 (newest) ran while %7 was queued and
  %6 had 7 issues left. More milestones in flight in one repository means
  more rebases, and %9's step-size gate needs %8 on main first.

## Decisions (locked)

1. **Order.** Tasks of a project are ranked by, in this order:
   1. the main-red fix (lane `urgent`);
   2. tasks without a milestone: a person or the operator asked for that one
      issue (a hand-labelled issue, a flaky-job issue);
   3. tasks of a milestone, lowest milestone iid first. GitLab hands out iids
      in creation order, so this is oldest first, with no date to read;
   4. within the same rank, the older task (`creationTimestamp`, then name,
      so the order is total).
2. **One function.** `api/v1alpha1` gets `QueueLess(a, b *AgentTask) bool`
   with this order. Nothing else compares tasks.
3. **Admission.** Before a task without a pod takes a slot, the reconciler
   lists the project's tasks. If another task in phase `Queued` ranks ahead
   of it, it goes to `Queued` too and tries again after `queueRetry`, even
   when a slot is free. Only tasks in phase `Queued`, not suspended, count as
   ahead. A `Waiting` task (usage limit, budget stop), a blocked or a
   hibernated task holds nobody back.
4. **Work-conserving.** A slot is never left idle while a task could use it.
   When the oldest milestone has nothing to run (it waits for CI, or it is
   paused), a younger milestone's task gets the slot. So younger milestones
   still move during an older one's CI waits. That is on purpose.
5. **No preemption.** A running run is never stopped or paused for a task
   that ranks higher. It waits for the next free slot. A run that lost its
   pod keeps its place: it only needs a free slot. So does a chat turn on a
   task without a pending run, because somebody is waiting for the answer.
6. **Pilots stay outside** the queue and the capacity, as today. A pod that
   exists (a run continuing, a chat turn) needs no admission.
7. **Spec tasks rank like their milestone.** A new milestone's planning
   waits for older milestones' queued tasks, and runs in their gaps.
8. **Urgent lane:** a new optional `spec.queueLane` (`urgent` or empty) on
   `AgentTask`. The operator sets it when it hands a task the main-red fix
   and clears it when the fix is done.
9. **Why a task waits:** the `Queued` message says who is ahead: *waiting
   for capacity (1/1 runs in flight), 2nd in line behind #140 (milestone
   %6)*. The first in line keeps today's text with *next in line*.
10. **Per project.** Each project has its own queue, as `maxConcurrentRuns`
    is per project.

## Requirements

- R1 `QueueLess` in `api/v1alpha1` with table tests: each rank against the
  others, same milestone, same timestamp (name decides), a nil milestone.
- R2 `spec.queueLane` (API, `make generate manifests`, chart CRD) and the
  operator setting and clearing it for `MainRedFix`. Test with the main-red
  lane.
- R3 Admission per decisions 3–6 in `agenttask_controller.go`. Regression
  tests with the fake client, capacity 1:
  - two queued tasks: only the older milestone's starts;
  - a new task of a younger milestone does not overtake a queued task of an
    older one, also with a free slot;
  - a new task of an older milestone goes ahead of a younger queued one;
  - a suspended or `Waiting` older task holds nobody back (work-conserving);
  - a task with a pod is not held back;
  - a pilot is not held back and holds nobody back;
  - capacity 2: the top two start.
- R4 The `Queued` message per decision 9; position and the task ahead come
  from `QueueLess`.
- R5 Docs: `docs/usage.md` (several milestones at once, what goes first and
  how `/agent pause` moves a milestone out of the way),
  `docs/configuration.md` (`maxConcurrentRuns`), `docs/design.md`.

## Constraints

- Behaviour with a single task in the queue stays as it is.
- `runningTasks` and the capacity limit stay as they are; the order is a
  check next to them.
- One list call per check, on the cached client. No GitLab calls in the
  admission path.
- CRD fields are only added.
- Slices stay small and reviewable (< 50 production lines).

## Non-goals

- A priority override per milestone. `/agent pause` on the older milestone
  already lets a younger one go first.
- Running milestones strictly one after the other (no younger issue before
  the older milestone is done). It would leave the slot idle during CI.
- Ordering across projects, or a queue shared between projects.
- Changing `maxConcurrentRuns` on netcup01.
- Dependencies between milestones. An issue linked as "blocked by" an issue
  of another milestone already waits.

## Suggested order

1. Spec copy to `docs/specs/milestone-queue-order.spec.md` + R1.
2. R2.
3. R3.
4. R4, R5.

## Acceptance

- With capacity 1 and queued tasks of %6 and %9, the %6 task starts when the
  slot frees up, every time.
- The main-red fix goes ahead of every milestone task, and a hand-labelled
  issue goes ahead of milestone tasks.
- While %6 waits for CI and has nothing queued, a %9 task runs.
- A run in progress is never interrupted by a higher-ranked task.
- A queued task on the Task page says who is ahead of it.

## Delivery

Delivered by hand in one MR instead of the suggested slices.
