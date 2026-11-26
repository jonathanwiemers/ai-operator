# Loose issues

The loose issues queue runs and merges issues that have no milestone, beside
your milestones. This page shows how to turn it on, how you take issues in
the [Desk](../concepts/glossary.md#desk) and on GitLab, what holds an issue
and what merges it, who may act, how flaky issues join, and how to switch it
off.

An issue without a milestone normally stops in `agent::review`, and a human
merges it. With the [loose issues queue](../concepts/glossary.md#loose-issues-queue),
an [owner](../concepts/glossary.md#owner) takes the issues that should go
the whole way, and the operator merges each one when its pipeline is green
and its review is clean, as on an [autopilot](autopilot.md) milestone.

## Turn it on

Set `spec.looseIssues` on the [AgentProject](../reference/agentproject.md#speclooseissues):

```yaml
apiVersion: ai.wiemers.me/v1alpha1
kind: AgentProject
metadata:
  name: shop
  namespace: agents
spec:
  gitlab:
    project: acme/shop
  looseIssues:
    enabled: true
    owners: [alice, bob]
    ownerSubjects: [alice@example.com, bob]
    maxParallel: 2
```

The operator creates one [tracking issue](../concepts/glossary.md#tracking-issue)
titled `🤖 Loose issues`, without a milestone. It lists the queue by state:
pending with the position, active with the stage, held with the reason,
merged with the merge request, and left with the reason. It keeps the last
50 finished issues.

Nothing joins the queue by label. Anyone can write an issue, so your take is
the only way in.

## Take issues in the Desk

1. Open the project and its **Loose issues** tab, after Milestones.
2. Tick one or more issues in the table of open issues without a milestone.
   Search narrows the list.
3. Click **Hand to autopilot (n)**.

A toast shows the answer. A refused issue is named with its reason. Each
taken issue gets the comment "Taken into the loose issues queue by … (position
3)." An issue that is already in work becomes active at once.

The queue table above shows each issue's state. Its ⋯ menu has **Resume** for
a held issue, **Drop**, and **Merge head**, which merges the head the agent
pushed. If you are not one of the queue's `ownerSubjects`, the actions are
hidden and a note names who may act.

## Take issues on GitLab

Comment on the `🤖 Loose issues` issue. Only the queue's `owners` may command
it:

```text
/agent take #350 #421
/agent drop #421
/agent pause
/agent resume #421
```

A take names up to 20 issues. The queue refuses an issue that is closed, is
in a milestone, is a tracking issue of the operator, is the main-red issue,
or is in the queue already. One reply names every refusal. The full list of
commands is in [Commands](../reference/commands.md#on-the-loose-issues-tracking-issue).

## What runs, what holds and what merges

Issues start first taken, first started, up to `maxParallel` at a time. The
queue does not wait for a milestone, and a milestone does not wait for it.

Each issue merges through the same [merge gate](../concepts/glossary.md#merge-gate)
as a milestone's: CI green, review clean, no run pending and no foreign push.
A budget applies too: `budget.perIssueUSD` and `/agent budget` on the issue.

A problem [holds](../concepts/glossary.md#hold) only its own issue, also with
`maxParallel: 1`, because the issues are unrelated. The other issues go on.
The tracking issue and the tab say why it is held. These also stop an issue:

- **A spent budget** holds that issue until you raise the budget.
- **A companion repository** holds the issue with "companion repositories
  need a milestone". The queue does not land companions.
- **A red default branch** with `mainHealth.freezeAutopilot` freezes the
  whole queue: nothing starts or merges until the branch is green. The
  tracking issue says so.

An issue leaves the queue when it is merged, closed, moved into a milestone
or dropped. The tracking issue logs each leave with its reason. Dropping a
pending issue takes it off. Dropping an active issue lets its run end and
leaves it in `agent::review` for a human to merge.

## Owners and questions

The queue has its own owners, like a milestone:

- `owners` are GitLab users. The operator mentions them and they may command
  the queue. Left empty, they are `spec.notify.users`.
- `ownerSubjects` are Desk logins. Only they may take issues in the Desk and
  answer the agent's questions on a queue issue. There is no fallback. A queue
  without them runs, but nobody can act on it in the Desk, and the tab says so.

The agent's questions on a queue issue reach your Inbox like any other. See
[Owners and pings](owners-and-pings.md).

## Flaky issues

With the queue on, a `ci::flaky` issue handed to the agent goes into the
queue and not into a milestone. This covers `mainHealth.autoFixFlaky`, the
[pilot](pilot.md)'s hand-over and an owner's accepted proposal. The issue keeps
its fix task, and its note says "in the loose issues queue". With the queue off
nothing changes. See [Main health](while-the-agent-works.md#main-health).

## Switch it off

Set `looseIssues.enabled: false`. The operator drops the pending issues,
leaves the active ones in the manual flow (their runs end and a human merges
them) and closes the tracking issue with a note that names them. Switch the
queue on again and the same tracking issue reopens.

## Next

- [Run a milestone on autopilot](autopilot.md): the gate the queue reuses.
- [When autopilot pauses](pauses.md): holds and how they end.
- [Answer and hand over](answer-and-hand-over.md): hand one issue over by
  label.
- [AgentProject](../reference/agentproject.md#speclooseissues): the fields.
