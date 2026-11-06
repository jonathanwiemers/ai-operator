# `/agent` commands

Questions to the owners (what to do about a red `main`, a pilot proposal, an
agent's question, a budget) are **prompts**. Answer them in the Desk with one
click or one sentence: see [Prompts](usage.md#prompts) and the [Desk](ui.md).
The operator mirrors each prompt and its answer as a comment in GitLab.

The comments on this page are the **fallback for projects whose
`workflow.inputs` has `gitlab`** (the default, `[gitlab, desk]`): everything
the agents can be told can also be told in comments on issues and merge
requests and in the milestone description. With `inputs: [desk]` the operator
acts on none of the `/agent` and `/pilot` comments; it replies and points to
the Desk (see [Replies](#replies-to-comments-not-acted-on)). Labels, milestone
planning and the milestone description stay on GitLab either way. For the
workflow around it see [usage.md](usage.md). The [web UI](ui.md#steering) can
also write these as GitLab comments in your name.

## How commands work

- A command is a comment whose **first word** is `/agent` (the prefix is
  `workflow.commandPrefix`, see [configuration](configuration.md#specworkflow)).
  `/agent: …` works too; `/agents are great` is no command.
- Comments **without** the prefix are plain discussion: they start nothing and
  cost nothing. The agent reads them with the next run (during a step run on a
  draft MR, before its next step).
- Commands are read from **humans** only. The bot's own comments never count,
  so an agent cannot command itself.
- **Who may command** is `workflow.commandsFrom`: `members` (default, anyone
  who can comment) or `owners`. With `owners`, an `/agent …` comment on an
  issue, its MR, the tracking issue or a closed issue counts only from the
  milestone's [owners](usage.md#commands-from-owners-only), by GitLab user
  ID; anyone else's starts and changes nothing, and each such comment gets
  one reply in its thread ([up to a cap](#replies-to-comments-not-acted-on)). `/pilot` comments have their own owners check,
  whatever this setting says.
- On an issue's merge request a command works the same as on the issue.
- The operator polls GitLab (`pollInterval`, default `1m`), so a command takes
  effect within a poll; with [webhooks](usage.md#webhooks-optional) within
  seconds.

## On an issue or its merge request

| Command | Effect |
|---------|--------|
| `/agent <instructions>` | Starts a run. The agent resumes its session and gets every comment since its last run, with your instructions. Examples: `/agent also cover the error case`, `/agent retry`, `/agent continue`. |
| `/agent revisit <id> <what you want instead>` | Starts a run that changes a recorded decision, e.g. `/agent revisit D42-3 keep the case as typed`. The agent records a new decision that supersedes the old one. One revisit per comment; for the same ID the newest wins. See [Decisions](usage.md#decisions). |
| `/agent review` | Starts a re-review run: the reviewers check the agent's pushed head again, the implementer does not run and nothing is pushed unless they find blocker or major findings (those go to the implementer as in any run). For reviewers that failed in an outage; asking again reviews the same head again. If the MR's head is not the agent's last push (someone pushed), or a human commented since the last run, the run is a normal one: the agent takes over the head or reads the comments. The comment is the operator's, not the agent's; with an instruction next to it (`/agent review the logger`, or another `/agent …` comment since the last run) it is a normal run. |
| `/agent use <engine> <model> [effort]` | Picks the implementer's engine and model for this issue, e.g. `/agent use claude opus`, `/agent use cursor auto`, `/agent use claude sonnet low`. See [Model and effort](#model-and-effort). |
| `/agent use default` | Back to the model of the issue's profile. |
| `/agent effort <low\|medium\|high\|xhigh\|max>` | How hard the implementer thinks on this issue (claude only). |
| `/agent effort default` | Back to the effort of the issue's profile. |
| `/agent budget <usd>` | The issue's budget in USD on an autopilot milestone, e.g. `/agent budget 40`, in place of the project's `budget.perIssueUSD`. Once the issue's calls cost that much, the autopilot pauses; raising it above the spend resumes it on the next poll. Like `use`, it starts no run and is answered on the issue; an invalid value changes nothing. See [Budget](usage.md#budget). |
| `/agent budget default` | Back to the project's `budget.perIssueUSD`. |
| `/agent repo <project>` | The issue changes a companion repository too, e.g. `/agent repo my-group/infra/cluster` (a full path or a GitLab URL): sets the label `agent-repo::<project>`, replacing another one. The project must be one of the AgentProject's valid `spec.crossRepo.targets`; otherwise nothing changes and the answer lists them. Like `use`, it starts no run. A companion set this way is never merged by autopilot unless the milestone has a line `/agent repo <project> merge`. See [Companion repository](usage.md#companion-repository). |
| `/agent repo none` | The issue has no companion repository (removes the `agent-repo::` label). |

Moving the issue back to `agent::ready` also starts a run, e.g. after you
answered the agent's questions.

**Issues without an `agent::` label** are not the agent's yet: a command
there starts nothing. On the issues the operator watches (open issues with
the generated label, `ci::*` ones too, and the queue of an autopilot
milestone) an `/agent` or `/pilot` comment gets one reply saying so: add
`agent::ready` (`workflow.readyLabel`) to hand the issue over. A `use` or
`effort` command is applied once the issue has the label, and the reply
says that instead. The operator replies once per comment, also after a
restart; comments from before the operator watched are not answered.

#### Replies to comments not acted on

An `/agent` or `/pilot` comment the operator does not act on gets one
reply in its thread naming the reason: no `agent::` label (above), the
commenter is not an owner (`commandsFrom: owners`, `/pilot`, `merge`, `start`), or a
command the tracking issue does not take (the reply lists `start` while the
milestone waits for its turn, `pause`, `resume`,
`skip #N`, `merge #N` and `revisit <id> …`). Each comment is answered once,
also when a poll is repeated. To bound the bot's comments, one author gets
at most 10 such replies a day (`status.commandReplies.authors` of the
AgentProject); the operator logs the comments beyond that instead of
answering them. This is the only case a command is ignored without a reply.

### Model and effort

`/agent use` and `/agent effort` change **labels** on the issue, and the
implementer of every following run reads them:

| Label | Set by | Example |
|-------|--------|---------|
| `agent-profile::<name>` | the planner, or you | `agent-profile::strong` |
| `agent-model::<engine>/<model>` | `/agent use`, or you | `agent-model::claude/opus` |
| `agent-effort::<level>` | the planner, `/agent effort`, `/agent use … <effort>`, or you | `agent-effort::high` |

- **Precedence:** the model and effort labels override the profile, the
  profile overrides `agents.implement`. Reviewers, the planner and the MR
  description agent are not affected: they are set per project.
- **Allowed models:** only those the AgentProject allows (`agents.models`,
  by default `claude opus`, `claude sonnet`, `claude haiku`), plus every
  engine and model a profile or `agents.implement` names explicitly (a
  profile on `cursor` with `model: auto` allows `cursor auto`). A model may
  be named by its alias (`opus`) or in full (`claude-opus-5-5`), in any case.
- **Exact forms only:** `use default`, `use <claude|cursor> <model>
  [effort]`, `effort <level|default>` and `budget <one word>` are
  commands; anything else, like `/agent use the existing logger`, is a
  normal instruction that starts a run.
- **The rest of the profile stays:** budget (`maxBudgetUSD`), tools and
  instructions still come from the issue's profile, also on another model.
  A budget sized for a cheap model may cut a strong model's run short.
- **Effort cap:** an effort above `agents.maxEffort` (default `high`) is
  lowered to it. The cursor engine has no effort setting: an effort label is
  kept but only counts once the issue runs on claude, and the answer and the
  run report say so.
- **The operator answers** with one short comment per poll, where the last
  command was written: what it set, what it lowered, or why it did nothing
  (a model that is not allowed: the answer lists the allowed ones).
- **No run of its own:** a comment that is only a `use` or `effort` command
  sets the labels and starts nothing. Put instructions on the next lines to
  start a run with the new setting right away:

  ```
  /agent use claude opus high
  The parser change is subtle, please add property tests.
  ```

- **Sessions:** a run on another engine than the last one (also switching
  back) starts a fresh agent session with the whole thread and the agent
  notes; switching the model or effort within an engine resumes the
  session.
- **Draft issues:** commands on an issue that is not handed over yet are
  applied when it is (moved to `agent::ready`). You can also set the labels
  directly. Commands the operator answered once are not applied again, also
  when the issue is handed over a second time.

## On a closed issue

| Command | Effect |
|---------|--------|
| `/agent revisit <id> <instruction>` | For an issue of a planned milestone that is merged and closed: opens a follow-up issue that changes the decision. See [Revisiting after the merge](usage.md#revisiting-after-the-merge). |

## On the tracking issue (autopilot)

| Command | Effect |
|---------|--------|
| `/agent start` | Owners only (also with `commandsFrom: all`), tracking issue only: plan a milestone that waits for its turn now, ahead of the older ones (`status.autopilot.startRequestedBy`). Said on the tracking issue ("▶️ Planning starts now, ahead of its turn (by @you)"); the planning run is asked for on the next poll. On a milestone that is planned or being planned it answers so and does nothing. |
| `/agent pause` | Stop starting and merging. Runs already requested still finish. |
| `/agent resume` | Continue; the active issue is checked again. |
| `/agent skip #12` | Skip a planned issue. Issues that depend on it stay blocked until it is merged by hand, they are skipped too, or they may `startAnyway`. |
| `/agent unskip #12` | A skipped issue that is still open goes back to pending; autopilot picks it once its dependencies are merged. A pause because nothing could start ends by itself once it can. |
| `/agent startAnyway #12` | A pending issue that waits for a skipped dependency may start without it (that issue only; its other dependencies and "blocked by" links still hold). Said on the tracking issue ("▶️ #12 may start although #8, which it depends on, was skipped (@you)"); the stuck pause ends by itself once it can start. Any case (`startanyway`) works. |
| `/agent rebase #12` | The active issue only (not a pending one, not the main-red fix), once it has a pushed MR: GitLab rebases the MR onto the default branch. The review carries over if the change is the same (as for `need_rebase`, see [the merge gate](usage.md#the-merge-gate)); a pause about the issue whose way out it is (CI red, merge request) ends, and the rebased head goes through CI. Said on the tracking issue. If the MR was rebased before, or GitLab cannot rebase it, the agent merges the default branch instead. Refused with the reason while a run of the issue is pending, a rebase is in flight, or someone else pushed to the branch. |
| `/agent mergeBase #12` | The active issue only, like `rebase`: the agent merges the default branch into the MR and resolves conflicts (see [Merge conflicts](usage.md#merge-conflicts)); its push is reviewed and checked by CI like any other, and a pause about the issue whose way out it is ends. Once per head, and not with `workflow.resolveConflicts: false`. |
| `/agent merge #12 <sha>` | Owners only (also with `commandsFrom: all`): merge the active issue's head `sha` (at least 7 characters; without it the answer names the current head) without a full review. Skips failed or missing reviews and blocking findings; CI, the MR state and approvals still apply. Said on the tracking issue; a new push or a review that finishes later voids it. |
| `/agent revisit D42-3 …` | Change a decision of a merged issue: a follow-up issue joins the queue. |
| anything else | Answered once with this list, a pointer to `/pilot …` and the active issue. Instructions for the agent go on the active issue or its MR, not here. |
| `create 1 2`, `create 1, 3`, `create all` | As a reply in the thread of the [retrospective](usage.md#the-retrospective), once autopilot is done (the whole comment, no prefix): opens those follow-ups as plain issues (generated label, no milestone, not handed to the agent), each once, and answers in the thread with the links. Only from the owners pinned when the retrospective was posted; anything else is ignored silently. |

See [Autopilot](usage.md#autopilot-a-milestone-end-to-end).

## To the pilot

A comment that **starts** with `/pilot` (`workflow.pilotPrefix`) or a mention
of the bot (`@ai-bot …`; `pilot.mention`) talks to the milestone's
[pilot](usage.md#talking-to-the-pilot), on the tracking issue or on an issue
(or MR) of the milestone that has a task. Only the milestone's owners; the
pilot runs its counted tools on its own when it
[manages](usage.md#talking-to-the-pilot), and everything else only on
proposals they accept. Its tools `rebase #N` and `mergeBase #N` are the
tracking issue's commands above, counted (a managing pilot runs them
itself), applied by autopilot on its next poll and said there ("the pilot
asked to rebase !N", or "@you via the pilot" after your answer).
Its tools `unskip #N` and `startAnyway #N` are the commands above too, and
`markDone #N` closes the open issue of a skipped or pending queue item with
`agent::done` and the pilot's reason; these three are only ever options of
the pilot's prompts and run on an owner's answer, never on its own (see
[When it pauses](usage.md#when-it-pauses)).

| Comment | Effect |
|---------|--------|
| `/pilot <question>` | The pilot answers where you asked, e.g. `/pilot why are we paused?`. It may add a proposal, put to the owners as a proposal prompt ("Do it" or "Do nothing") mirrored on the tracking issue. |
| `yes` / `no` as a reply in a proposal's thread | For proposals "P3" posted before prompts, until they expire: accepts (runs all its actions, once) or drops the proposal. Only owners, and only those who were owners when it was posted (by user ID: a username that changed hands does not count); the whole comment must be the answer. |
| `A` (an option's letter) as a reply in a prompt's thread | Answers the prompt with that option, like the Desk (`via: gitlab`), while `workflow.inputs` has `gitlab`. The letter must start the comment, alone or before a line break or punctuation (`A`, `b.`, `C: cheaper`); text after it is ignored, free text is answered in the Desk. Only owners, by the user ID pinned when the prompt was posted; anyone else, or a second answer, gets one reply in the thread saying why. Prompts on issues and the tracking issue, not on merge requests. |
| `/pilot yes P3` / `/pilot no P3` | Like `yes` / `no` in a proposal's thread (proposals "P3" only), anywhere in the milestone. A bare `/pilot yes` is refused with the list of open proposals and the open prompts (answered in the Desk); an expired one "no longer applies". |
| `/pilot fix the flaky test #340` | Like any question: the pilot may propose `fixFlaky #340`, which hands the flaky issue to the agent in this milestone once it was flaky twice (see [Main health](usage.md#main-health)). |
| `/pilot tell #12: <words>` | Puts a proposal prompt to pass your words on to #12's implementer verbatim, as your instruction via the pilot; the reply names it ("Q3 on #N: an owner answers it in the Desk"). |
| `/pilot <spec change>` | E.g. `/pilot drop the CSV export from the spec`: the pilot may propose `editSpec`, a new milestone spec shown as a diff. After an owner's `yes` (always needed, also when it manages) the operator writes the description, keeps its `/agent` lines, and the milestone is re-planned. See [Spec changes through the pilot](usage.md#spec-changes-through-the-pilot). |
| `/pilot stop` | The pilot of this milestone proposes every action from now on (kill switch), until `/pilot go`; kept over restarts. No model call. |
| `/pilot go` | Back to the project's `pilot.autonomy` (never above it: on a `propose` project it still proposes). No model call. |
| `/pilot coordinate` | On an issue of a planned milestone without autopilot: opens the tracking issue "🧭 Pilot: <milestone>" and starts its pilot. |
| `/pilot rules` | Lists the milestone's [standing rules](usage.md#talking-to-the-pilot), with the reason for any that is not applied. No model call. |
| `/pilot forget R2` | Proposes removing rule R2; an owner's `yes` removes its line from the milestone description. |

## Replies to comments not acted on

An `/agent` or `/pilot` comment the operator will not act on is never ignored
silently: it gets one reply in its thread, once per comment (marked with the
comment's id, so a re-poll or a webhook never replies twice; up to 10 a day
per author, more are only logged). This covers the open issues with the
generated label (`ci::*` too), the queue of autopilot milestones, closed
issues (revisits), tracking issues and the notes the webhook delivers.

| Reason | Reply |
|--------|-------|
| The issue has no `agent::` label | "Nothing happens yet: #N has no `agent::ready` label. Add it to hand #N to the agent." A routing-only command (e.g. `/agent use …`) says it "is applied once it has". |
| Not an owner (`commandsFrom: owners`) | "Only the owners of this milestone can give `/agent` commands here (`workflow.commandsFrom: owners`)", with a hint to add `/agent owners @…` when the milestone has none. |
| Not an owner, to the pilot | "Only the owners of this milestone can instruct me." |
| The tracking issue only takes these commands | The "anything else" answer of [its list](#on-the-tracking-issue-autopilot): the commands, a pointer to `/pilot …` and the active issue. |
| `inputs` is `[desk]` | "Nothing happens: this project takes instructions in the Desk: <link>", to the issue's open prompt, else its task, else the milestone (without a Desk URL, see `--desk-url`, no link). |

A bare `/pilot yes` is refused for the same reason: it does not say which
prompt is meant. Answer in the Desk, or with the option's letter in the
prompt's thread.

## In the milestone description

These are lines in the description, not comments. They are read on every
poll.

| Line | Effect |
|------|--------|
| `/agent spec` | Plan the milestone: the planning agent creates one sub-issue per merge request. Changing the description re-plans the drafts. (`milestones.specMarker`) |
| `/agent autopilot` | Plan and run the milestone end to end: no questions, one issue at a time, merged when CI is green and the review is clean. Removing the line stops it. (`milestones.autopilotMarker`) |
| `/agent owners @alice @bob` | Mention these users when an issue of the milestone needs a human, instead of the project's owners. `/agent owners` alone mentions nobody. Changing this line does not re-plan. See [Owners and pings](usage.md#owners-and-pings). |
| `/agent owner-subjects alice@example.com bob` | Desk identities (the UI's username claim) that may accept or reject pilot proposals and answer the milestone's prompts on the Desk. No project-level fallback. Changing this line does not re-plan. |
| `/agent rule R1: switch ci.maxFixRounds=3` | A standing rule of the milestone: `switch <setting>=<value>` (`ci.maxFixRounds`, `ci.retryFailedJobs`, `review.retries`, `review.maxIterations`), `pilot <text>` for the pilot, or `prompt <text>` for the implementers and reviewers. The pilot writes them after an owner's `yes`; you may edit them by hand. Changing them does not re-plan. See [Talking to the pilot](usage.md#talking-to-the-pilot). |
| `/agent budget 5` | The milestone's budget in USD (decimals allowed, above 0). Once its calls (`agent_calls`, needs the research database) cost that much, the autopilot pauses (`pauseKind: budget`); a running call finishes. Raising the value above the spend, or removing the line, resumes it on the next poll; `/agent resume` alone pauses again at once. An invalid value is ignored (logged): no budget. Without the database the budget is not enforced. Changing this line does not re-plan. At 80 % and at the pause the owners get a budget prompt in the Desk whose raise options rewrite this line ([Budget prompts](usage.md#budget-prompts)). See [Budget](usage.md#budget). |
| `/agent repo my-group/infra/cluster [merge]` | The milestone's issues may change this companion repository (a full path or a GitLab URL, one line per companion). With `merge`, autopilot may merge the companion's merge request, if the milestone is on autopilot and the target has `autoMerge: true`. The line is part of the spec: changing it re-plans. A malformed line or a project that is no valid target is ignored and named once on the tracking issue. See [Companion repository](usage.md#companion-repository). |

## Labels you may set yourself

Every command that changes labels has a label equivalent, and the board is
the place to see them:

| Label | Effect |
|-------|--------|
| `agent::ready` | hand the issue to the agent (starts a run) |
| `agent::done` | the issue is finished; its task, pod and workspace are removed |
| `agent-profile::<name>` | implement with that profile |
| `agent-model::<engine>/<model>` | implement with that engine and model (must be allowed) |
| `agent-effort::<level>` | implement with that effort (at most `agents.maxEffort`) |
| `agent-repo::<project>` | change that companion repository too; counts only under a milestone line `/agent repo <project>` or when an owner (or the bot) set it, otherwise it is ignored with one notice |

The planner never changes a profile or effort label that is already on an
issue: change them as you like. It adds its own only where none is (also
again on a re-plan, if you removed one from a draft).
