# Status and history events

This page is for scripts and dashboards that read what the operator
records about merges, pauses, owner messages and the run order: the
`mergedLines` status fields of an AgentTask and an AgentProject, an issue
task's owner messages and their deliveries, the project's
`milestoneOrder`, and the history events in the research
database that the Desk's figures come from. How the Desk shows them is in
[Lines of code](../guides/budgets-and-spend.md#lines-of-code).

## Example

An autopilot milestone's tracking task after its issue #12 merged:

```yaml
status:
  autopilot:
    items:
      - iid: 12
        state: Merged
        mergeRequestIID: 34
        mergedSHA: 3f2a9c1
        mergedLines:
          production: {added: 40, deleted: 5}
          tests: {added: 60, deleted: 0}
          docs: {added: 10, deleted: 0}
          generated: {added: 0, deleted: 0}
```

The `merged` history event of the same merge:

```json
{
  "kind": "merged",
  "issue_iid": 12,
  "mr_iid": 34,
  "data": {
    "sha": "3f2a9c1",
    "lines": {
      "production": {"added": 40, "deleted": 5},
      "tests": {"added": 60, "deleted": 0},
      "docs": {"added": 10, "deleted": 0},
      "generated": {"added": 0, "deleted": 0}
    }
  }
}
```

## `mergedLines`

The lines one merge request merged, split into the four categories of
[Lines of code](../guides/budgets-and-spend.md#lines-of-code). The operator
counts them once, from the merge request's diff in GitLab, when the merge
request becomes merged.

| Field | Meaning |
|-------|---------|
| `production`, `tests`, `docs`, `generated` | Each `{added, deleted}`: the diff lines that start with `+` or `-`, without the file headers. A binary file counts 0. |
| `partial` | Set when GitLab sent some file's diff without its content (collapsed or too large). Those lines are not counted, so the sums are a lower bound. Absent otherwise. |

`mergedLines` is absent when nothing was measured: the count failed, or the
merge request merged before the operator counted lines. A failed count
only logs; it never holds up or fails a merge or a landing, and it is not
tried again. There is no backfill for older merges.

It appears in two places, both read only:

| Field | Where | What |
|-------|-------|------|
| `status.autopilot.items[].mergedLines` | AgentTask (the milestone's tracking task) | The issue's own merge request, counted when autopilot records the merge. |
| `status.companions[].mergedLines` | [AgentProject](agentproject.md#other-spec-fields) | A [companion](../concepts/glossary.md#companion-repository) merge request, counted when its entry becomes merged: a merge by an owner, autopilot's own merge, or a resumed landing. It counts to the home issue. |

## Owner messages

An owner's words for an issue's running
[implementer](../concepts/glossary.md#implementer), passed on by the
[pilot](../concepts/glossary.md#pilot) (see
[Tell a running implementer something](../guides/steer-from-the-desk.md#tell-a-running-implementer-something)).
The pilot appends them to the issue's AgentTask; the runner reports where
each one went:

```yaml
spec:
  source:
    ownerMessages:
      - id: tell/8812
        owner: alice
        text: use the existing retry helper
        at: "2026-11-22T14:04:51Z"
status:
  run:
    deliveredMessages:
      - id: tell/8812
        call: 3
        how: stream
        at: "2026-11-22T14:05:02Z"
    calls:
      - seq: 3
        role: implementer
```

| Field | Meaning |
|-------|---------|
| `spec.source.ownerMessages[]` | `id` (a stable key from its source, `tell/<note>` or `chat/<message>`, so a message is never appended twice), `owner`, `text` and `at`. At most 20 are kept, the newest; `text` is at most 4000 characters. |
| `status.run.deliveredMessages[]` | One entry per delivered message: `id`, `call` (the `seq` of the call that took it), `how` and `at`. At most 20. |
| `how: stream` | Written into the running claude call while it worked. Recorded once claude replayed it. |
| `how: resume` | The call stopped at its current step and resumed its session with the message. Recorded before the resumed call starts. |
| `how: nextRun` | The issue was not running, or the run ended first: the message went into the next run's prompt. |
| `status.run.calls[].stoppedForMessage` | `true` on a call that was stopped so a message could be resumed into it. Its resumed call follows it. |

- Each message is delivered once. A message with an entry in
  `deliveredMessages` is never sent again, also after a pod restart.
- At most 5 messages per run are delivered while it runs, by `stream` and
  `resume` together; later ones wait for the next run.
- A stop for a message is not an interruption: it does not count in
  `status.run.interruptions`, is no failure and takes no checkpoint. The
  stopped call and the resumed call are both in `status.run.calls`, and
  the run's cost sums them.

## The loose issues queue

The [loose issues queue](../concepts/glossary.md#loose-issues-queue) is one AgentTask of `type: Queue` per project, named `<project>-loose-issues`. It runs no pod. Its `status.autopilot` holds the items, with `phase`, `maxParallel` and `frozenAt` while the queue is frozen.

```yaml
status:
  autopilot:
    phase: Running
    items:
      - {iid: 350, state: Merged, mergeRequestIID: 34}
      - {iid: 421, state: Active, takenBy: "@alice"}
      - {iid: 46, state: Left, leftReason: "dropped by @alice; continues in the manual flow"}
```

`state` is `Pending`, `Active` (with `hold` while held), `Merged` or `Left`. A `Left` item has `leftReason` and `leftAt`: The queue keeps the newest 50 finished items. Switched off, its phase is `Off`.

### The pilot's round

The queue's [pilot](../concepts/glossary.md#pilot) works in
[rounds](../concepts/glossary.md#round). The current one is
`status.autopilot.round` on the Queue task:

```yaml
status:
  autopilot:
    round:
      number: 3
      startedAt: "2026-11-26T09:12:00Z"
      finished: 2
      costUSD: "1.40"
      startCostUSD: "6.85"
      note: "Merged #350 and #352. #421 is held for red CI: …"
```

| Field | Meaning |
|-------|---------|
| `number` | The round, from 1. |
| `startedAt` | When the round started. |
| `finished` | Queue issues merged or left since `startedAt`. |
| `costUSD` | What the pilot spent in this round. |
| `startCostUSD` | The pilot task's `status.run.costUSD` when the round started: the base of the round's budget. |
| `note` | The previous round's note, at most 1500 characters. |
| `noteRequest` | While the round ends: the pilot's run request for the note. Otherwise absent. |

The pilot is one AgentTask of `type: Coordinate` per queue, named
`<project>-loose-issues-pilot`. It has no milestone. These fields tie it to
the queue and its rounds:

| Field | Meaning |
|-------|---------|
| `spec.queueTask` | The name of the Queue task it pilots. Empty on a milestone's pilot. |
| `spec.source.pilotRound` | The current round, copied from the Queue task: `number`, `startedAt`, `note` and `startCostUSD`. A new `number` starts a fresh session. |
| `spec.source.roundNote` | The run request that asks for the note of the round that ends. |
| `status.pilot.roundNote` | The answer, the newest only: `request`, `note`, or `failed` with why there is no note. Then the operator posts a note of the facts. |

## `milestoneOrder`

The project's [run order](../concepts/glossary.md#run-order): the iids of
its open autopilot milestones, first to run first. It is on the
AgentProject, read only:

```yaml
status:
  milestoneOrder: [5, 7, 6]
```

| Rule | What happens |
|------|--------------|
| Empty list | The operator seeds it from the old run-next marks (earliest mark first), then by iid, so nothing moves on upgrade. |
| New milestone | It is ranked after the listed ones, by iid, and appended on the next sync. |
| Done, closed or off autopilot | It is dropped on the next sync. |
| `/agent move` or a drag in the Desk | The operator rewrites the list. |

Only the first milestone starts new issues. Change the order with
[`/agent move`](commands.md#on-the-tracking-issue), not by editing the
status.

## History events

The operator writes history events to the research database, one row per
event with a unique key, so writing one twice keeps one row. They expire
after the history retention, `research.historyRetention` (the default is in
the [chart values](../../charts/ai-operator/README.md#research)). Every
event has its namespace and project, the milestone, issue and merge request
numbers where they apply (`milestone_iid`, `issue_iid`, `mr_iid`), a
`kind`, a time and `data`.

| `kind` | When | `data` |
|--------|------|--------|
| `merged` | Autopilot merged an issue's merge request. The issue's merge time is its latest `merged` event. | `sha`, `mergeRequestURL`, and `lines` (a `mergedLines` object) when the merge was measured. |
| `companion.merged` | A companion merge request of an issue merged. Recorded under the home project, milestone and issue, with the companion's merge request in `mr_iid`, and keyed `companion/<companion path>/<merge request>/merged`. It is a kind of its own, so it does not move the issue's merge time. The Desk's streams do not show it. | `companion` (the companion's path), `sha`, `mergeRequestURL`, and `lines` when measured. |
| `autopilot.paused` | An autopilot milestone [paused](../concepts/glossary.md#pause), or the pause changed its kind. Recorded on the milestone's tracking issue. | `kind`, `reason`, and `item`, the issue it paused for. |
| `autopilot.resumed` | The pause ended. | The same, plus `resolvedBy`, `via` (how it ended) and `pausedSec`. |
| `hold.set` | An issue of an autopilot milestone got a [hold](../concepts/glossary.md#hold), or its hold restarted. Its time is the hold's start. | `holdKind`, `reason`, and `since`, the hold's start (RFC 3339). |
| `hold.cleared` | The hold ended: it was lifted, or the issue merged, was skipped or left the milestone. | The same as its `hold.set`. |
| `ci.started` | A new pipeline of an issue's merge request is `Pending` or `Running`: a new commit, also one a rebase pushed in review, or a new pipeline after a final state. Written once per pipeline and keyed `ci/<sha>/<pipelineID>/started`, with the pipeline ID 0 while GitLab has reported none. An ID reported later writes no second event. Summary "CI started on #42". It starts the issue's CI segment on the Timeline. No notification. | `sha`, `pipelineID` and `round`, the CI fix round. |
| `item.skipped` | Autopilot skipped an issue. Written once per issue. | None. |
| `milestone.started` | A milestone's issues are planned: its spec task's plan went from empty to non-empty. Written once; a later re-plan writes no second event. Summary "Milestone planned: N issues". | `issues` (the planned count) and `title`. |
| `autopilot` | A milestone's autopilot changed its phase (`Running`, `Paused`, `Done`, `Off`). Recorded on the tracking issue. | `phase`, `reason` while paused, and `title`. |
| `release.decided`, `release.started`, `release.ended` | A release of the milestone was decided, its pipeline started, or it ended. | `releaseID`, `by`, `afterIssue`, `final` and `title`; `release.ended` also `state` (`Released`, `Failed`, …), `version` and `reason`. |

`title` is the milestone's title. The Desk's
[milestone and release notifications](notifications.md#categories) come
from `milestone.started`, `autopilot` with phase `Done`, and `release.ended`
with state `Released` or `Failed`.

An issue's merged lines are the sum of the `lines` of its `merged` and
`companion.merged` events; per project, companion lines count under the
home project. An issue whose `merged` event has no `lines` counts as not
measured, never as 0. A companion merged without `lines` makes the sum
partial. An issue's [hold](../concepts/glossary.md#hold) is not a pause
event yet, so the pause figures count milestone pauses only.

## Next

- [Lines of code](../guides/budgets-and-spend.md#lines-of-code): written
  and merged lines in the Desk.
- [Desk API](desk-api.md#activity-and-spend): the endpoints that sum these
  events, such as `GET /api/v1/metrics/code`.
- [When autopilot pauses](../guides/pauses.md): the pause kinds and their
  ways out.
- [Desk API](desk-api.md#timeline): the timeline built from the run, hold,
  merge and skip events.
