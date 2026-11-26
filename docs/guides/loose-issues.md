# Loose issues

The loose issues queue runs and merges issues that have no milestone, beside
your milestones. This page shows how to turn it on, how you take issues in
the [Desk](../concepts/glossary.md#desk) and on GitLab, what holds an issue
and what merges it, who may act, how flaky issues join, how the queue's
pilot works in rounds, and how to switch it off.

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

## The queue's pilot

The queue has its own [pilot](pilot.md), like a milestone. It looks into
stuck queue issues, red CI after the fix rounds, stalled runs and holds. It
posts its assessment on the `🤖 Loose issues` issue and answers the queue's
owners.

**In the Desk.** The **Loose issues** tab shows a card per problem under
*Needs you*: a held issue, the queue's pause, or an open assessment. Below
the cards, type into *Message the queue's pilot…* and send with Ctrl+Enter
(Cmd+Enter on a Mac). The pilot answers on its task page. Accept or reject
its proposals on the tab. Only the queue's `ownerSubjects` may write; others
read "Read-only: only the queue's owners can message the pilot."

A held issue and an open assessment also reach your
[Inbox](desk.md#inbox) as a problem, so the browser and push notifications
for stuck issues reach them. The entry opens the tab with the card in view.

**On GitLab.** Comment on the tracking issue or on a queue issue:

```text
/pilot why is #421 held?
/pilot tell #421: use the existing retry helper
/pilot stop
```

Only the queue's `owners` may instruct it. The commands are in
[Commands](../reference/commands.md#the-queues-pilot).

**What it may do.** It follows [`pilot.autonomy`](../reference/agentproject.md#specpilot),
managing as on an autopilot milestone, and audits each action on the
tracking issue. It may retry a run, ask for another review, instruct the
implementer, retry the default branch's pipeline, rebase, merge the default
branch in, hand a flaky test into the queue, open an issue, mark a pending
issue done, and pause or resume. A skip drops the issue from the queue. An
issue it opens is not taken: an owner takes it. Milestone tools such as
revisiting a decision, standing rules or spec changes are refused with the
reason.

### Rounds

The queue's pilot works in [rounds](../concepts/glossary.md#round). A round
ends at the first of two limits: a number of finished queue issues (merged,
closed or dropped), or hours since the round started. You set both in
`looseIssues.pilot`:

```yaml
spec:
  looseIssues:
    enabled: true
    owners: [alice]
    ownerSubjects: [alice@example.com]
    pilot:
      roundIssues: 5
      roundHours: 24
```

`0` switches a limit off. With both `0` the pilot keeps one session. The
hours count from the round's start, not from a time of day.

At the end of a round the pilot writes a round note of at most 1500
characters: what merged, what is held and why, and the open threads. The
tracking issue gets it as "🧭 **Round 2 note**" with "Round 3 started." If
the pilot cannot write it, the operator posts the facts: what merged, left
and is held, and what the pilot spent.

**What carries over.** The next round starts a fresh session. It knows the
last round only from the note, the queue's current state, and the open
prompts and proposals, which stay open. Nothing else carries over.

A round never ends in the middle of a pilot call: the running call finishes
first. While the pilot writes its note, new messages wait for the next
round. Its budget, [`pilot.maxBudgetUSD`](../reference/agentproject.md#specpilot),
counts per round. When it is used up, the pilot says so until the next round
starts.

The tab and the tracking issue show the current round, for example
"Round 3 · started 09:12 · 2 of 5 issues · $1.40". The tracking issue gives
the time in UTC.

## Switch it off

Set `looseIssues.enabled: false`. The operator drops the pending issues,
leaves the active ones in the manual flow (their runs end and a human merges
them) and closes the tracking issue with a note that names them. Switch the
queue on again and the same tracking issue reopens.

## Next

- [Run a milestone on autopilot](autopilot.md): the gate the queue reuses.
- [When autopilot pauses](pauses.md): holds and how they end.
- [The pilot](pilot.md): what a pilot does and how you talk to it.
- [Answer and hand over](answer-and-hand-over.md): hand one issue over by
  label.
- [AgentProject](../reference/agentproject.md#speclooseissues): the fields.
