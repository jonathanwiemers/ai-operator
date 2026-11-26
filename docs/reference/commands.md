# Commands

This page lists every command you can give the operator: `/agent` and
`/pilot` comments in GitLab, the marker lines in a milestone description,
and the replies it takes in a prompt's or the retrospective's thread. Look
a command up by where you write it.

The [Desk](../concepts/glossary.md#desk) does most of this with a click:
answer [prompts](../concepts/glossary.md#prompt), chat with an agent or the
[pilot](../concepts/glossary.md#pilot), revisit a decision, or use the
buttons on a pause card. Use the comments when you work in GitLab. The Desk
can also write these comments in your name.

## Examples

```text
/agent also cover the error case              on an issue: another run with your words
/agent use claude opus high                   on an issue: model and effort for this issue
/agent revisit D42-3 keep the case as typed   change a recorded decision
/agent pause                                  on the tracking issue: stop starting and merging
/pilot why are we paused?                     ask the milestone's pilot
```

In a milestone description:

```text
/agent spec
/agent autopilot
/agent owners @alice @bob
/agent owner-subjects alice@example.com
```

## How commands work

- A command is a comment whose **first word** is `/agent`. `/agent: …`
  works too; `/agents are great` is no command. The prefix is set in the
  `AgentProject` (`workflow.commandPrefix`, default `/agent`).
- For `use`, `effort`, `budget` and `repo`, only the first line is the
  command; the lines below go to the agent. `/agent review` must be the
  whole comment. Command words are not case
  sensitive (`startanyway`, `mergebase`), and `#` before an issue number is
  optional (`skip 12`).
- A comment **without** the prefix is plain discussion. It starts nothing
  and costs nothing. The agent reads it with its next
  [run](../concepts/glossary.md#run).
- Commands count from **humans** only. The bot's own comments never count,
  so an agent cannot command itself.
- **Who may command** is `workflow.commandsFrom`: `members` (default,
  anyone who can comment) or `owners`. With `owners`, only the milestone's
  [owners](../concepts/glossary.md#owner) can give `/agent` commands.
  Some commands are owners only either way; the tables say so.
- On an issue's merge request a command works the same as on the issue.
- The operator polls GitLab every `pollInterval` (default `1m`), so a
  command takes effect within a minute. With webhooks it takes seconds.
- With `workflow.inputs: [desk]` the operator acts on no `/agent` or
  `/pilot` comment. It replies with a link to the Desk instead. The default
  is `[gitlab, desk]`. The marker lines in the milestone description work
  either way.

## On an issue or its merge request

In the Desk, the issue's task page has a chat with the agent and buttons for
*Retry*, *Review* and the model. In GitLab:

| Command | Effect |
|---------|--------|
| `/agent <instructions>` | Starts a run. The agent resumes its session and reads every comment since its last run, with your words. Example: `/agent also cover the error case`, or `/agent retry`. |
| `/agent revisit <id> <what you want instead>` | Starts a run that changes a recorded [decision](../concepts/glossary.md#decision). Example: `/agent revisit D42-3 keep the case as typed`. The agent records a new decision that replaces the old one. |
| `/agent review` | The reviewers check the agent's last push again; the implementer does not run. Use it after the reviewers failed, for example in an outage. It must be the whole comment: any other word or line, like `/agent review the logger`, makes a normal run. If someone else pushed, or a human commented since the last run, it is a normal run. |
| `/agent use <engine> <model> [effort]` | Picks the implementer's engine and model for this issue. Example: `/agent use claude opus`, `/agent use cursor auto`. See [Model and effort](#model-and-effort). |
| `/agent use default` | Back to the model of the issue's [profile](../concepts/glossary.md#profile). |
| `/agent effort <level>` | How hard the implementer thinks: `low`, `medium`, `high`, `xhigh` or `max` (claude only). Example: `/agent effort low`. |
| `/agent effort default` | Back to the effort of the issue's profile. |
| `/agent budget <usd>` | The issue's [budget](../concepts/glossary.md#budget) in USD on an autopilot milestone, in place of the project's `budget.perIssueUSD`. Example: `/agent budget 40`. Once the issue's calls cost that much, autopilot pauses. Raising it above the spend resumes it on the next poll. |
| `/agent budget default` | Back to the project's `budget.perIssueUSD`. |
| `/agent repo <project>` | The issue changes a [companion repository](../concepts/glossary.md#companion-repository) too. Example: `/agent repo my-group/infra/cluster` (a path or a GitLab URL). It sets the label `agent-repo::<project>`. The project must be one of the `AgentProject`'s `spec.crossRepo.targets`; otherwise the reply lists them. Autopilot merges such a companion only with a milestone line `/agent repo <project> merge`. |
| `/agent repo none` | The issue has no companion repository. |

`use`, `effort`, `budget` and `repo` start no run of their own. The operator
sets the value and answers on the issue. An invalid value changes nothing,
and the answer says why. Moving the issue back to `agent::ready` also starts
a run, for example after you answered the agent's questions.

**An issue without an `agent::` label** is not the agent's yet. A command
there starts nothing. The operator replies once: add `agent::ready` to hand
the issue over. A `use` or `effort` command is applied once the issue has
the label. See [Labels](labels.md).

### Restart a stuck run

Restarting is a **Desk** action only. On the task page, use *Restart run* in
the page header, or the chat command `{"cmd":"restart"}`. Only the
milestone's `owner-subjects` may restart. It works on an implementer's,
planner's or pilot's task.

The restart deletes the agent's pod, and the run starts again as an
interruption. After 3 interruptions the run fails as blocked
(`agent-blocked::infra`). The workspace keeps the agent's commits. When the
task has an issue, a note there says who restarted it, in which step, and
after how long. Without a run in progress the answer is
`Nothing to restart: no run is in progress.`

`/agent restart` as a GitLab comment is no command: on an issue it is plain
instructions for the agent, on the tracking issue it gets the list of
commands. A pause card for a stalled run offers *Skip*; to restart, use the
task page in the Desk.

### Model and effort

`/agent use` and `/agent effort` set **labels** on the issue, and the
implementer of every following run reads them:

| Label | Set by | Example |
|-------|--------|---------|
| `agent-profile::<name>` | the planner, or you | `agent-profile::strong` |
| `agent-model::<engine>/<model>` | `/agent use`, or you | `agent-model::claude/opus` |
| `agent-effort::<level>` | the planner, `/agent effort`, `/agent use … <effort>`, or you | `agent-effort::high` |

To switch and start a run in one comment, put instructions on the next
lines:

```text
/agent use claude opus high
The parser change is subtle, please add property tests.
```

- **Precedence:** the model and effort labels override the profile. The
  profile overrides the project's implementer (`agents.implement`).
  Reviewers, the planner and the merge request describer are set per
  project and do not change.
- **Allowed models:** those in `agents.models` (by default `claude opus`,
  `claude sonnet` and `claude haiku`), plus every engine and model a profile
  or `agents.implement` names. You may name a model by its alias (`opus`)
  or in full (`claude-opus-5-5`), in any case.
- **Exact forms only:** `use default`, `use <claude|cursor> <model>
  [effort]`, `effort <level|default>` and `budget <one word>` are commands.
  Anything else, like `/agent use the existing logger`, is an instruction
  that starts a run.
- **The rest of the profile stays:** the run budget, tools and instructions
  come from the issue's profile, also on another model. A budget sized for
  a cheap model may cut a strong model's run short.
- **Effort cap:** an effort above `agents.maxEffort` (default `high`) is
  lowered to it. The cursor engine has no effort; the label is kept and
  counts once the issue runs on claude.
- **The answer:** one short comment says what was set, what was lowered, or
  why nothing changed (a model that is not allowed: it lists the allowed
  ones).
- **Sessions:** switching the engine starts a fresh agent session with the
  whole thread. Switching the model or effort within an engine resumes it.
- **Draft issues:** commands on an issue that is not handed over yet apply
  once it is. A command answered once is not applied again.

## On a closed issue

| Command | Effect |
|---------|--------|
| `/agent revisit <id> <instruction>` | On a merged and closed issue of a planned milestone: opens a follow-up issue that changes the decision. Example: `/agent revisit D42-3 keep the case as typed`. |

## On the tracking issue

An [autopilot](../concepts/glossary.md#autopilot) milestone has a
[tracking issue](../concepts/glossary.md#tracking-issue). In the Desk, the
milestone page does the same: pause and resume, skip, merge, the
parallel stepper, and the pause card's buttons. In GitLab:

| Command | Effect |
|---------|--------|
| `/agent pause` | Stop starting and merging. Runs already asked for still finish. On a milestone that waits for its turn, it also holds the planning until `resume`. |
| `/agent resume` | Go on. Lifts the milestone's pause and every held issue's [hold](../concepts/glossary.md#hold). An issue whose problem is still there is held again on the next poll. |
| `/agent resume #12` | Lift the hold of #12 only. Other holds and a milestone pause stay. |
| `/agent skip #12` | Skip a planned issue. Issues that depend on it stay blocked until it is merged by hand, they are skipped too, or they may start anyway. |
| `/agent unskip #12` | A skipped issue that is still open goes back to pending. Autopilot picks it once its dependencies are merged. |
| `/agent startAnyway #12` | A pending issue that waits for a skipped dependency may start without it. Its other dependencies still hold. |
| `/agent rebase #12` | An active issue with a pushed merge request: GitLab rebases it onto the default branch, and the new head goes through CI. The review carries over if the change is the same. If GitLab cannot rebase, the agent merges the default branch instead. Refused while a run of the issue is pending or someone else pushed. |
| `/agent mergeBase #12` | An active issue: the agent merges the default branch into the merge request and resolves conflicts. Its push is reviewed and checked like any other. Once per head, and not with `workflow.resolveConflicts: false`. |
| `/agent merge #12 <sha>` | **Owners only.** Merge the issue's head `sha` (at least 7 characters) without a full review. CI, the merge request state and approvals still apply. A new push voids it. Without a sha the answer names the current head. |
| `/agent move before %6` | **Owners only.** Put the milestone just before %6 in the project's [run order](../concepts/glossary.md#run-order). `move after %6`, `move top` and `move bottom` work the same way; the `%` is optional. `%6` must be another open autopilot milestone of the project. The tracking issue says `⏫ Moved before %6 (by @you).` When the milestone passes the running one, the note adds that the running one's active issues finish first and it starts no new ones. A move to where the milestone already is, of a done milestone, or before or after itself is refused with the reason. |
| `/agent start` | **Owners only.** The same as `/agent move top`. A waiting milestone plans on the next poll, unless an owner paused it. A planned one starts its issues before the others. |
| `/agent start off` | Run-next marks are gone. The answer is "There are no run-next marks any more; use `move`." |
| `/agent parallel 2` | **Owners only.** Up to 2 issues run at the same time (1 to 10). It writes the `/agent parallel` line of the milestone description. Running issues go on when you lower it. |
| `/agent release [reason]` | **Owners only.** Release after the current state of the default branch, once its pipeline is green. Example: `/agent release the new checkout is ready for users`. The reason is one line, at most 200 characters. |
| `/agent no-release #12 [reason]` | **Owners only.** Drop the planned release after #12. Example: `/agent no-release #12 nothing users notice yet`. A release that runs already, and the final release, stay. `norelease` works too. |
| `/agent endMilestone` | **Owners only.** The milestone ends here. Pending issues are skipped, and autopilot is done after the active ones. During a pause for the final release it drops that release. |
| `/agent revisit D42-3 …` | Change a decision of a merged issue: a follow-up issue joins the queue. |
| anything else | Answered once with this list, a pointer to `/pilot` and the active issue. Instructions for the agent go on the active issue or its merge request. |

"Owners only" holds also with `commandsFrom: members`.

### Replies in the retrospective's thread

When autopilot is done, the pilot posts a
[retrospective](../concepts/glossary.md#retrospective) with numbered
follow-ups. In the Desk, its *Retrospective* tab opens them, drafts a
follow-up milestone from them, or dismisses the rest. In GitLab, reply in
the retrospective's thread, without a prefix:

| Reply | Effect |
|-------|--------|
| `create 1 3`, `create 1, 3`, `create all` | Opens those follow-ups as plain issues: not handed to the agent, no milestone. Each opens once. |
| `create none` | Dismisses the remaining follow-ups, so the milestone can close without waiting 7 days. |

Only the owners of the milestone when the retrospective was posted count.
Other replies are ignored without an answer. A new milestone can also take
follow-ups over with the line `/agent followup-of` (see
[below](#in-the-milestone-description)).

## On the loose issues tracking issue

The [loose issues queue](../concepts/glossary.md#loose-issues-queue) has its
own tracking issue, `🤖 Loose issues`, without a milestone. Only the queue's
`owners` may command it. In the Desk, the **Loose issues** tab does the same.
See [Loose issues](../guides/loose-issues.md).

| Command | Effect |
|---------|--------|
| `/agent take #350 #421` | Take up to 20 open issues without a milestone into the queue, in this order. Each gets the comment "Taken into the loose issues queue by @you (position 3)."; an issue already in work becomes active at once. One reply names every refusal. |
| `/agent drop #350` | Take a pending, active or held issue off. A pending one is gone; an active one finishes its run and stays in `agent::review` for a human. |
| `/agent pause`, `/agent resume` | Stop starting and merging, or go on. |
| `/agent resume #350` | Lift the hold of #350 only. |
| `/agent merge #350 <sha>`, `/agent rebase #350`, `/agent mergeBase #350` | As on a milestone's [tracking issue](#on-the-tracking-issue). |
| anything else | Answered "`skip` is not on the loose issues queue." with the word you wrote, in the Desk chat too. `move`, `release`, `endMilestone`, `parallel` and `skip` are not queue commands: `maxParallel` is set in the AgentProject, and `drop` takes an issue off. |

A take refuses an issue that:

- is closed;
- is in a milestone (`is in milestone "…"`);
- is a tracking issue of the operator;
- is the main-red issue;
- is in the queue already.

A comment from anyone else is answered "Only the owners of the loose issues
queue can give it commands."

## To the pilot

The [pilot](../concepts/glossary.md#pilot) watches an autopilot milestone.
In the Desk, ask it in the milestone's chat. In GitLab, start a comment with
`/pilot` (`workflow.pilotPrefix`), or mention the bot (`@ai-bot …`, on by
default with `pilot.mention`). Write it on the tracking issue or on an issue
or merge request of the milestone. Only the milestone's owners may talk to
the pilot. On a milestone without owners, anyone may ask it questions.

| Comment | Effect |
|---------|--------|
| `/pilot <question>` | The pilot answers where you asked. Example: `/pilot why are we paused?`. It may add a proposal, which goes to the owners as a prompt ("Do it" or "Do nothing"). |
| `/pilot tell #12: <words>` | Passes your words on to #12's implementer at once, as your instruction, without a prompt. If #12 runs, they reach its running implementer; else they go into its next run. The reply says which. |
| `/pilot fix the flaky test #340` | Like any question. The pilot may propose handing the flaky-test issue to the agent in this milestone. |
| `/pilot <spec change>` | Example: `/pilot drop the CSV export from the spec`. The pilot may propose a new spec, shown as a diff. After an owner's yes, the operator writes the description, keeps its `/agent` lines, and the milestone is re-planned. An owner always has to say yes. |
| `/pilot stop` | The pilot only proposes from now on, until `/pilot go`. A kill switch, kept over restarts. |
| `/pilot go` | Back to the project's `pilot.autonomy`. |
| `/pilot rules` | Lists the milestone's standing rules, and why any of them is not applied. |
| `/pilot forget R2` | Proposes removing rule R2. An owner's yes removes its line from the description. |
| `/pilot coordinate` | On an issue of a planned milestone without autopilot: opens a tracking issue and starts its pilot. |
| `/pilot yes P3` / `/pilot no P3` | Accept or drop the older proposal P3, anywhere in the milestone. A bare `/pilot yes` is refused with the list of open proposals and prompts. |

`stop`, `go`, `rules` and `yes`/`no` cost no model call.

When the pilot manages the milestone, it runs some actions on its own
within limits, such as `rebase #12` and `mergeBase #12`. It never unskips,
starts anyway or closes an issue on its own: those are options of its
prompts and run only on an owner's answer.

### The queue's pilot

The [loose issues queue](../concepts/glossary.md#loose-issues-queue) has its
own pilot. Write `/pilot …` on the `🤖 Loose issues` issue or on an issue in
the queue; only the queue's `owners` may instruct it. `/pilot <question>`,
`tell #N: <words>`, `stop`, `go` and `yes`/`no` work as above. The rest
answers in queue words:

| Comment or proposal | Answer |
|---------------------|--------|
| `/pilot rules`, `/pilot forget R2` | "Standing rules are not on the loose issues queue: they belong to a milestone." |
| `/pilot coordinate` | "The pilot of the loose issues queue listens on #7.", with the tracking issue's number. It starts no other pilot. |
| revisit, setRule, forgetRule, editSpec, endMilestone, unskip, startAnyway, raiseBudget | Refused: "`revisit` is not on the loose issues queue: it has no decisions to revisit", each with its own reason. |
| an issue outside the queue | "#12 is not in the loose issues queue". |

While the pilot writes its [round](../concepts/glossary.md#round) note,
comments to it wait for the next round. See
[Loose issues](../guides/loose-issues.md#the-queues-pilot).

### Answers in a prompt's thread

A prompt is answered best in the Desk, with a click or a sentence. The
operator also mirrors each prompt as a GitLab comment. While
`workflow.inputs` has `gitlab`, you can answer there:

| Reply | Effect |
|-------|--------|
| `A` (an option's letter) | Answers the prompt with that option. The letter starts the reply, alone or before punctuation or a line break: `A`, `b.`, `C: cheaper`. Text after it is ignored. |
| `yes` / `no` | In the thread of an older proposal "P3": accepts it (runs all its actions, once) or drops it. |

Only owners count, by the GitLab user who was an owner when the prompt was
posted. Free text is answered in the Desk. A letter answers only a
milestone's prompts, not the questions an implementer asks on an issue:
answer those in the Desk or with `/agent <your answer>`. On a merge request only the
prompt's own thread counts, never review threads.

## Replies to comments not acted on

An `/agent` or `/pilot` comment the operator does not act on gets one reply
in its thread, never silence:

| Reason | Reply |
|--------|-------|
| The issue has no `agent::` label | "Nothing happens yet: #N has no `agent::ready` label. Add it to hand #N to the agent." A `use` or `effort` command "is applied once it has". |
| Not an owner, with `commandsFrom: owners` | "Only the owners of this milestone can give `/agent` commands here", with a hint to add `/agent owners @…` when the milestone has none. |
| Not an owner, for an owners-only command | The same, for `merge`, `start`, `endMilestone`, `release`, `no-release` and `parallel`. |
| Not an owner, to the pilot | "Only the owners of this milestone can instruct me." |
| The tracking issue does not take it | The list of [its commands](#on-the-tracking-issue), a pointer to `/pilot` and the active issue. |
| `inputs` is `[desk]` | "Nothing happens: this project takes instructions in the Desk", with a link to the open prompt, the task or the milestone. |

The operator answers each comment once, also when it polls again or a
webhook repeats it. Comments from before the operator watched the project
get no reply. To bound the bot's comments, one author gets at most 10 such
replies a day. Comments beyond that are only logged.

## In the milestone description

These are lines in the milestone description, not comments. Each starts a
line. The operator reads them on every poll. See
[Marker line](../concepts/glossary.md#marker-line).

| Line | Effect |
|------|--------|
| `/agent spec` | Plan the milestone: the [planner](../concepts/glossary.md#planner) creates one [sub-issue](../concepts/glossary.md#sub-issue) per merge request. Changing the description re-plans the drafts. The marker is `milestones.specMarker`. |
| `/agent autopilot` | Plan and run the milestone end to end: no questions, each issue started once its dependencies are merged, merged when CI is green and the review is clean. Removing the line stops it. The marker is `milestones.autopilotMarker`. |
| `/agent owners @alice @bob` | Mention these GitLab users when an issue needs a human, in place of the project's owners. They may give the owners-only commands. `/agent owners` alone mentions nobody. |
| `/agent owner-subjects alice@example.com bob` | The Desk identities (the login's username) that may answer the milestone's prompts, accept proposals and restart runs. There is no project-wide fallback. |
| `/agent rule R1: switch ci.maxFixRounds=3` | A standing rule. `switch <setting>=<value>` for `ci.maxFixRounds` (0 to 5), `ci.retryFailedJobs` (true or false), `review.retries` (0 to 5) or `review.maxIterations` (0 to 5); `pilot <text>` for the pilot; or `prompt <text>` for the implementers and reviewers. The pilot writes rules after an owner's yes; you may edit them. |
| `/agent budget 5` | The milestone's budget in USD (decimals allowed, above 0). Once its calls cost that much, autopilot pauses; a running call finishes. Raising it above the spend, or removing the line, resumes on the next poll. An invalid value means no budget. It needs the research database. At 80 % and at the pause the owners get a budget prompt in the Desk. |
| `/agent parallel 2` | Up to 2 issues run at the same time (1 to 10), in place of the project's `autopilot.maxParallel` (default 3). With `1`, a problem of an issue pauses the milestone instead of holding the issue. Only the first such line counts. |
| `/agent followup-of 12 1 3` | This milestone takes over follow-ups 1 and 3 of milestone %12's retrospective (`%12` works too; numbers 1 to 9). The first milestone to name a follow-up keeps it. A bad line or number gets one comment on the tracking issue. |
| `/agent repo my-group/infra/cluster [merge]` | The milestone's issues may change this companion repository, one line per project. With `merge`, autopilot may merge the companion's merge request, if its target has `autoMerge: true`. A bad line is named once on the tracking issue. |

Changing `owners`, `owner-subjects`, `rule`, `budget`, `parallel` or
`followup-of` does not re-plan. `spec` changes and `repo` lines are part of
the spec, so changing them re-plans.

## Next

- [Labels](labels.md): the state labels and the labels you may set.
- [Glossary](../concepts/glossary.md): the terms on this page.
- [How it works](../concepts/how-it-works.md#steering-it): where you steer
  from.
