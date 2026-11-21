# Status and history events

This page is for scripts and dashboards that read what the operator
records about merges, pauses and the run order: the `mergedLines` status
fields of an AgentTask and an AgentProject, the project's
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
| `item.skipped` | Autopilot skipped an issue. Written once per issue. | None. |

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
