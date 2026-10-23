# `/agent` commands

Everything you tell the agents, you tell them in GitLab: in comments on
issues and merge requests, and in the milestone description. This page lists
all of it. For the workflow around it see [usage.md](usage.md).

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
  engine and model a profile or `agents.implement` uses. A model may be named
  by its alias (`opus`) or in full (`claude-opus-5-5`).
- **Effort cap:** an effort above `agents.maxEffort` (default `high`) is
  lowered to it. The cursor engine has no effort setting; there the effort is
  ignored, and the run report says so.
- **The operator answers** each command with a short comment: what it set,
  what it lowered, or why it did nothing (a model that is not allowed: the
  answer lists the allowed ones).
- **No run of its own:** a comment that is only a `use` or `effort` command
  sets the labels and starts nothing. Put instructions on the next lines to
  start a run with the new setting right away:

  ```
  /agent use claude opus high
  The parser change is subtle, please add property tests.
  ```

- **Sessions:** switching to another engine starts a fresh agent session
  with the whole thread; switching the model or effort within an engine
  resumes the session.
- **Draft issues:** commands on an issue that is not handed over yet are
  applied when it is (moved to `agent::ready`). You can also set the labels
  directly.

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

See [Autopilot](usage.md#autopilot-a-milestone-end-to-end).

## In the milestone description

These are lines in the description, not comments. They are read on every
poll.

| Line | Effect |
|------|--------|
| `/agent spec` | Plan the milestone: the planning agent creates one sub-issue per merge request. Changing the description re-plans the drafts. (`milestones.specMarker`) |
| `/agent autopilot` | Plan and run the milestone end to end: no questions, one issue at a time, merged when CI is green and the review is clean. Removing the line stops it. (`milestones.autopilotMarker`) |
| `/agent owners @alice @bob` | Mention these users when an issue of the milestone needs a human, instead of the project's owners. `/agent owners` alone mentions nobody. Changing this line does not re-plan. See [Owners and pings](usage.md#owners-and-pings). |

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

A label the planner set is never overwritten by a re-plan once you changed
it.
