# `/agent` commands

Everything you tell the agents, you tell them in GitLab: in comments on
issues and merge requests, and in the milestone description. This page lists
all of it. For the workflow around it see [usage.md](usage.md).

You can also write these from the [web UI](ui.md#steering): its comments are
GitLab comments in your name and work exactly the same.

## How commands work

- A command is a comment whose **first word** is `/agent` (the prefix is
  `workflow.commandPrefix`, see [configuration](configuration.md#specworkflow)).
  `/agent: …` works too; `/agents are great` is no command.
- Comments **without** the prefix are plain discussion: they start nothing and
  cost nothing. The agent reads them with the next run (during a step run on a
  draft MR, before its next step).
- Commands are read from **humans** only. The bot's own comments never count,
  so an agent cannot command itself.
- On an issue's merge request a command works the same as on the issue.
- The operator polls GitLab (`pollInterval`, default `1m`), so a command takes
  effect within a poll.

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

Moving the issue back to `agent::ready` also starts a run, e.g. after you
answered the agent's questions.

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
  [effort]` and `effort <level|default>` are commands; anything else, like
  `/agent use the existing logger`, is a normal instruction that starts a run.
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
| `/agent pause` | Stop starting and merging. Runs already requested still finish. |
| `/agent resume` | Continue; the active issue is checked again. |
| `/agent skip #12` | Skip a planned issue. Issues that depend on it stay blocked until it is merged by hand or they are skipped too. |
| `/agent revisit D42-3 …` | Change a decision of a merged issue: a follow-up issue joins the queue. |
| anything else | Answered once with this list, a pointer to `/pilot …` and the active issue. Instructions for the agent go on the active issue or its MR, not here. |

See [Autopilot](usage.md#autopilot-a-milestone-end-to-end).

## To the pilot

A comment that **starts** with `/pilot` (`workflow.pilotPrefix`) or a mention
of the bot (`@ai-bot …`; `pilot.mention`) talks to the milestone's
[pilot](usage.md#talking-to-the-pilot), on the tracking issue or on an issue
(or MR) of the milestone that has a task. Only the milestone's owners; the
pilot runs its counted tools on its own when it
[manages](usage.md#talking-to-the-pilot), and everything else only on
proposals they accept.

| Comment | Effect |
|---------|--------|
| `/pilot <question>` | The pilot answers where you asked, e.g. `/pilot why are we paused?`. It may add a proposal, posted on the tracking issue. |
| `yes` / `no` as a reply in a proposal's thread | Accepts (runs all its actions, once) or drops the proposal. Only owners, and only those who were owners when it was posted (by user ID: a username that changed hands does not count); the whole comment must be the answer. |
| `/pilot yes P3` / `/pilot no P3` | The same, anywhere in the milestone. A bare `/pilot yes` is refused with the list of open proposals; an expired one "no longer applies". |
| `/pilot fix the flaky test #340` | Like any question: the pilot may propose `fixFlaky #340`, which hands the flaky issue to the agent in this milestone once it was flaky twice (see [Main health](usage.md#main-health)). |
| `/pilot tell #12: <words>` | Proposes to pass your words on to #12's implementer verbatim, as your instruction via the pilot. |
| `/pilot stop` | The pilot of this milestone proposes every action from now on (kill switch), until `/pilot go`; kept over restarts. No model call. |
| `/pilot go` | Back to the project's `pilot.autonomy` (never above it: on a `propose` project it still proposes). No model call. |
| `/pilot coordinate` | On an issue of a planned milestone without autopilot: opens the tracking issue "🧭 Pilot: <milestone>" and starts its pilot. |
| `/pilot rules` | Lists the milestone's [standing rules](usage.md#talking-to-the-pilot), with the reason for any that is not applied. No model call. |
| `/pilot forget R2` | Proposes removing rule R2; an owner's `yes` removes its line from the milestone description. |

## In the milestone description

These are lines in the description, not comments. They are read on every
poll.

| Line | Effect |
|------|--------|
| `/agent spec` | Plan the milestone: the planning agent creates one sub-issue per merge request. Changing the description re-plans the drafts. (`milestones.specMarker`) |
| `/agent autopilot` | Plan and run the milestone end to end: no questions, one issue at a time, merged when CI is green and the review is clean. Removing the line stops it. (`milestones.autopilotMarker`) |
| `/agent owners @alice @bob` | Mention these users when an issue of the milestone needs a human, instead of the project's owners. `/agent owners` alone mentions nobody. Changing this line does not re-plan. See [Owners and pings](usage.md#owners-and-pings). |
| `/agent rule R1: switch ci.maxFixRounds=3` | A standing rule of the milestone: `switch <setting>=<value>` (`ci.maxFixRounds`, `ci.retryFailedJobs`, `review.retries`, `review.maxIterations`), `pilot <text>` for the pilot, or `prompt <text>` for the implementers and reviewers. The pilot writes them after an owner's `yes`; you may edit them by hand. Changing them does not re-plan. See [Talking to the pilot](usage.md#talking-to-the-pilot). |

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

The planner never changes a profile or effort label that is already on an
issue: change them as you like. It adds its own only where none is (also
again on a re-plan, if you removed one from a draft).
