# Run a milestone on autopilot

With [autopilot](../concepts/glossary.md#autopilot), a milestone runs end to
end without you: planned, implemented, reviewed, checked by CI and merged,
issue by issue. The [pilot](pilot.md) handles what goes wrong and asks you
when it needs a decision. This page shows how to turn autopilot on, what
happens from plan to last merge, how several issues and several milestones
share the work, the rules for a merge, and the tracking issue.

## Turn it on

Put `/agent autopilot` in the milestone description, next to `/agent spec`
or instead of it, with the milestone's owners:

```text
/agent spec
/agent autopilot
/agent owners @alice
/agent owner-subjects alice
/agent parallel 2
```

Before the first milestone, check three things in GitLab and your
[`AgentProject`](../reference/agentproject.md):

- **The bot may merge.** In *Settings > Repository > Protected branches*,
  set "Allowed to merge" for the default branch to *Developers +
  Maintainers*, or give the bot user the *Maintainer* role. The project's
  merge method and squash settings apply.
- **Reviewers are on.** Autopilot merges reviewed changes only, so keep at
  least one reviewer in
  [`agents.review.reviewers`](../reference/agents-and-models.md#roles).
- **Merged-results pipelines.** If your project uses them, also turn on
  *Pipelines must succeed*. Autopilot follows the pipeline of the pushed
  commit, and a commit without any pipeline counts as green after
  [`ci.noPipelineTimeout`](../reference/lifecycle-and-ci.md#specci).

In the Desk, *Plan milestone* adds these lines when you switch on
*Autopilot*. See [Write a milestone](write-a-milestone.md#marker-lines).

## A milestone end to end

1. **The tracking issue.** The operator opens
   **🤖 Autopilot: <milestone>** at once. It says whether the milestone
   waits for its turn ("⏳ waiting for its turn: %7, %9 go first"), is being
   planned ("📝 planning") or failed to plan. Nothing starts before there is
   a plan, but `/agent pause` and `/agent resume` already work.
2. **The plan.** The [planner](../concepts/glossary.md#planner) plans the
   milestone as usual but asks no questions. It takes the default it would
   propose and records it as a
   [decision](iterate-and-finish.md#decisions-and-revisit) on each issue.
   A planning failure is a comment on the tracking issue that mentions the
   owners.
3. **Issues start.** An issue starts once every issue it depends on is
   merged and a slot is free. "Blocked by" links count too. The operator
   moves it to `agent::ready`.
4. **The work.** Each issue is implemented, reviewed and checked by CI as
   usual. The implementer never stops with questions: it decides and records
   the decision. CI fix runs are reviewed too.
5. **The merge.** Once the pipeline is green and the review is clean, the
   operator marks the draft merge request ready and merges it. The merge
   closes the issue, and the free slot starts the next issue on the new
   default branch.
6. **The end.** When every issue is merged or skipped, the tracking issue
   gets a summary, the pilot writes the
   [retrospective](pilot.md#the-retrospective), and autopilot closes the
   milestone.

You follow all of it in the [Desk](../concepts/glossary.md#desk): the
milestone's page shows the plan, the running issues, pauses with their
buttons, and the pilot's prompts. Autopilot never merges a merge request of
an issue it did not plan. Other issues in the milestone follow the normal
flow of [handing over by label](answer-and-hand-over.md).

## Several issues at once

A milestone runs up to its parallel limit of issues at the same time: its
`/agent parallel N` line (1 to 10), else the project's
[`autopilot.maxParallel`](../reference/agentproject.md#specautopilot). An
owner changes it with the − and + stepper on the milestone's queue in the
Desk, or `/agent parallel 2` on the tracking issue. With `1`, one issue runs at a time.

Issues start in queue order: an issue's "Depends on" first, else plan order.
The Desk draws the plan in [waves](../concepts/glossary.md#wave), but there
are no waves to wait for: an issue starts as soon as its own dependencies
are merged.

**One at a time to the default branch.** Issues that run at the same time
land one by one in the [landing lane](../concepts/glossary.md#landing-lane).
An issue whose run, review and CI passed is *ready to land*. Only one issue
per milestone, the landing one, is marked ready, rebased, checked by CI on
the rebased head and merged. The others wait in the order they became ready.
A new push takes an issue out of that line. Above a limit of 1, the landing
issue does not count against the limit, so the next issue can start
meanwhile.

**Holds instead of pauses.** Above a limit of 1, a problem of one issue,
such as red CI or a failed run, [holds](../concepts/glossary.md#hold) only
that issue. The tracking issue says `⏸️ #12 is held: <reason>. The other
issues go on.` The held issue gives up its slot and the landing lane. At a
limit of 1, the same problem pauses the milestone. See
[When autopilot pauses](pauses.md).

## Several milestones at once

Autopilot milestones of one project go **one after another**. While an
older autopilot milestone is open (not done, not off, not closed), a younger
one starts no new issue and is not even planned, so its issues describe the
code as it is when work begins. That holds also while the older one is
paused. The tracking issue says "⏳ waiting for its turn". When its turn
comes, the milestone is planned from its description of that moment.

A milestone with `/agent spec` but no `/agent autopilot` is planned at once,
as before. A milestone that was planned already keeps going.

**Let a younger milestone go first.** An owner comments `/agent start` on
its tracking issue, or clicks *Run next* on the project's queue in the
Desk. A waiting milestone is
planned now; a planned one starts its issues before the older ones. The
older ones finish their running issues and then wait. Each older milestone
it passes notes "⏭️ %9 goes first (by @alice)." once. `/agent start off`,
or *Clear mark*, clears it. A running issue is never stopped for it.

**A paused waiting milestone** stays paused when its turn comes, and is not
planned until `/agent resume`. `/agent start` plans it but keeps the pause:
the issues are written and nothing starts until you resume.

**Run slots.** All runs of a project share
[`maxConcurrentRuns`](../reference/agentproject.md#other-spec-fields). When
a slot frees up, the waiting run that ranks first takes it:

1. the fix of a red default branch
   ([main health](while-the-agent-works.md#main-health));
2. a run that only reviews a head again, since it holds back a merge;
3. an issue without a milestone, labelled by hand;
4. an issue of the oldest milestone;
5. within one rank, the older run.

A running run is never stopped for a higher rank. A chat turn needs a free
slot but does not wait for higher ranks. The pilot does not take a slot. A
waiting run says who is ahead of it, for example *waiting for capacity (1/1
runs in flight), 2nd in line behind #140 (milestone %6)*.

## The merge gate

A model never decides to merge. The operator merges a merge request only
when all of these hold, the [merge gate](../concepts/glossary.md#merge-gate):

- the CI loop is done: the issue is in `agent::review`, the pipeline is
  green, or no pipeline came within `ci.noPipelineTimeout`;
- the last run succeeded, the review saw the pushed commit, no reviewer
  failed, and no blocker or major finding is left;
- CI checked that commit, and nobody pushed since;
- the merge request is open, targets the default branch, and GitLab reports
  it mergeable;
- the head is no [checkpoint](when-runs-go-wrong.md#when-a-run-is-cut-off)
  of a run that was cut off. Autopilot then waits, without a pause, for the
  run that continues the work;
- the default branch is not red
  ([merge freeze](while-the-agent-works.md#main-health));
- with a [companion repository](../concepts/glossary.md#companion-repository),
  its merge request is green, reviewed at its head, open and mergeable.

The merge passes the checked commit to GitLab, so GitLab refuses to merge
anything else. A draft merge request is marked ready once. If no pipeline
ran because the project skips pipelines for drafts, the operator starts a
merge request pipeline and waits for it.

**Rebases.** When the merge request needs a rebase, the operator asks GitLab
for one and waits for CI on the new head, again each time the default
branch moves. The review carries over if the change is the same: the same
files and the same added and removed lines. If not, the operator posts the
difference between the reviewed and the rebased version as a 🔁 note on the
merge request. A small difference is mechanical: the same commits, and at
most 40 changed lines or only files the default branch changed too. Then
the reviewers check the rebased head again, and the gate goes on. That
happens at most twice per issue. A bigger difference pauses with
`mergeRequest`.

**Conflicts.** If the merge request has
[merge conflicts](while-the-agent-works.md#merge-conflicts) or GitLab cannot
rebase it, the agent merges the default branch and resolves them. That run
is reviewed and checked by CI like any other, and the gate runs again.

What is missing decides what happens next: a later run, a pause or a hold.
See [When autopilot pauses](pauses.md).

## The tracking issue

The tracking issue is where autopilot reports in GitLab. The operator keeps
its description up to date:

- the state: running, paused with the reason, or done;
- one line per planned issue: merged with its merge request and commit, in
  progress with its label, pending with what it waits for, or skipped;
- the holds under the status line;
- an index of the decisions of every merged issue, with links.

It carries the `ai-generated` label only, so the agent never works on it. If
you delete it, the operator opens a new one.

**Commands.** In the Desk, the milestone page has the same actions: pause
and resume, skip, merge, the parallel stepper, and the pause card's buttons.
On the tracking issue you comment:

| Comment | What it does |
|---------|--------------|
| `/agent pause` / `/agent resume` | Stop and go on. `resume` lifts every hold too; `/agent resume #12` lifts one. |
| `/agent skip #12` / `/agent unskip #12` | Skip a planned issue, or bring a skipped one back. |
| `/agent startAnyway #12` | Start an issue although a dependency was skipped. |
| `/agent rebase #12` / `/agent mergeBase #12` | Rebase an issue's merge request, or have the agent merge the default branch into it. |
| `/agent merge #12 <sha>` | Owners only: merge this head without a full review. |
| `/agent parallel 2`, `/agent start` | Owners only: the parallel limit, and run this milestone next. |
| `/agent release`, `/agent no-release #12` | Owners only: release now, or drop a planned release. |
| `/agent endMilestone` | Owners only: skip the pending issues and finish after the active ones. |
| `/agent revisit D42-3 …` | Change a decision of a merged issue with a follow-up issue. |

Every command and its limits is in
[Commands](../reference/commands.md#on-the-tracking-issue). Questions go to
the pilot (`/pilot …`), and instructions for the agent go on the issue
itself. Any other `/agent` comment on the tracking issue gets one answer with
this list.

**Steering an issue.** Everything else works as on any issue: comment
`/agent …` on an active issue or its merge request, or revisit a decision.
If you pushed to the agent's branch yourself, to resolve a conflict say,
comment `/agent …` on the issue: the run takes your commits over, is
reviewed, and CI checks the new head before the merge.

**Finishing issues by hand.** An issue closed by a merged merge request, or
closed with `agent::done`, counts as merged. That works for the active issue
(also while paused: the pause ends by itself), for a skipped one (the issues
that wait for it can start), and for one that was done before autopilot got
to it.

## Change the plan

If you edit the milestone spec, issues that did not start yet follow the new
plan. Started and merged issues are kept. While the milestone is planned
again, no new issue starts. You can also ask the pilot for the change: see
[Spec changes through the pilot](pilot.md#spec-changes-through-the-pilot).

## Switch it off

Remove `/agent autopilot` from the milestone description. The tracking issue
gets a note and is closed, and nothing is merged any more. With `/agent spec`
still there, the milestone goes on in the normal flow, and the active issue
stays where it is. Add the line again to continue the queue.

## Next

- [When autopilot pauses](pauses.md): each kind of pause and its ways out.
- [The pilot](pilot.md): ask it, let it act, and read the retrospective.
- [When runs go wrong](when-runs-go-wrong.md): cut-off runs, usage limits
  and issues that wait.
- [Owners and pings](owners-and-pings.md): who gets asked.
