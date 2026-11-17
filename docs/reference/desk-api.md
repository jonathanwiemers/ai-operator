# Desk API

The [Desk](../concepts/glossary.md#desk) is a web app on top of a small
JSON API. This page lists every endpoint with what it is for and the grant
it needs, so you can script against it: read a milestone's state, post a
chat message, or pull the spend into a report. The Desk itself uses nothing
else.

## Example

Log in to the Desk in your browser and copy the value of the
`__Host-ai-operator-session` cookie (without HTTPS it is called
`ai-operator-session`). Then send it with each request:

```sh
DESK=https://ai.example.com
COOKIE='__Host-ai-operator-session=…'

# Who am I, and which roles does the Desk see?
curl -s -b "$COOKIE" "$DESK/api/v1/me"

# What needs me now?
curl -s -b "$COOKIE" "$DESK/api/v1/inbox"

# Ask the agent of a task to retry. Needs Desk chat, the chat grant and
# agents.chat: true on the project (see "How it works" below).
# TASK is the task's name, as in the address of its page in the Desk.
TASK=…
curl -s -b "$COOKIE" -H 'Content-Type: application/json' \
  -d '{"kind":"command","body":"{\"cmd\":\"retry\"}","clientKey":"retry-1"}' \
  "$DESK/api/v1/projects/agents/shop/tasks/$TASK/chat"
```

Without login (the chart's `ui.auth.mode: none`), port-forward to the Desk
and leave out the cookie:
`curl -s http://127.0.0.1:9080/api/v1/me`.

## How it works

- **Who may do what.** The Desk's access rules decide it, see
  [Access rules](../guides/desk.md#who-sees-what). A rule that matches you lets you
  *see* its projects. The grants `steer`, `chat`, `transcript` and
  `conversation` add more, and research rules (`research: read` or `write`)
  open the research endpoints. The table below names what each endpoint
  needs on top of seeing the project.
- **Projects you may not see do not exist for you.** The API answers 404
  for them, and change events leave them out.
- **Owner subjects.** Some actions also need you to be one of the
  milestone's owner subjects, the users its `/agent owner-subjects` line
  names (see [Commands](commands.md#in-the-milestone-description)). The
  operator checks this again when it acts.
- **GitLab in your name.** Endpoints that write comments, close issues or
  create milestones use *your* GitLab token: connect your account once with
  **Connect GitLab** in the Desk. Without it they answer 409.
- **Errors** are JSON with an `error` text. Invalid input answers 400, often
  with the `field` it is about. Too many requests answer 429.
- **Without the research database**, history, chat, spend and costs answer
  404 or leave their fields out. A cost the Desk cannot know is `""` with
  `costKnown: false`, never `0.00`.
- **Desk chat** means the chart sets `ui.chat.writerSecret`, which needs the
  research database. Without it, sending chat messages and every plan
  endpoint answer 404. A task's messages also need `agents.chat: true` on
  its AgentProject: without it the Desk accepts the message, and the
  operator then marks it failed ("live chat is not enabled") and nothing
  runs.
- **Retries are safe** for chat messages: send the same `clientKey` again
  and you get the same message `id` back.

## Account

| Method and path | Purpose | Needs |
|-----------------|---------|-------|
| `GET /api/v1/me` | Your user name, your roles and the login mode. | login |
| `GET /api/v1/about` | The Desk's version and the newest release of the operator's own project. | login |
| `GET /api/v1/features` | The sections you may see: factory, history, chat, plans, research. | login |
| `GET /api/v1/gitlab` | Whether GitLab is set up and your account is connected. | login |

## Projects and milestones

| Method and path | Purpose | Needs |
|-----------------|---------|-------|
| `GET /api/v1/projects` | Your projects, with task counts and cost. | see |
| `GET /api/v1/overview` | The dashboard: what is running, paused and waiting, what needs you, spend today and this week. | see |
| `GET /api/v1/milestones` | The milestones of your projects. `?state=open` (default), `done` or `all`; `?project=`, `?limit=`. | see |
| `GET /api/v1/search?q=` | Find tasks by `#issue`, `!merge request` or a part of the title. | see |
| `GET /api/v1/projects/{namespace}/{name}` | One project: its tasks by milestone, the autopilot queue, finishing milestones and cost. | see |
| `GET /api/v1/projects/{namespace}/{name}/milestones/{iid}` | One milestone: autopilot state, pause and its ways out, prompts, issues, releases, the pilot and the retrospective. | see |
| `GET /api/v1/projects/{namespace}/{name}/milestones/{iid}/issues/{issue}` | One issue of the milestone and its figures. | see |
| `GET /api/v1/projects/{namespace}/{name}/milestones/{iid}/thread` | The comments of the milestone's tracking issue. | see, GitLab connected |
| `POST /api/v1/projects/{namespace}/{name}/milestones/{iid}/notes` | Post `{"body": "…"}` to the milestone's pilot on the tracking issue. | `steer`, GitLab connected |
| `POST /api/v1/projects/{namespace}/{name}/milestones/{iid}/followup` | Create the follow-up milestone of the retrospective's follow-ups. | `steer`, owner subject, GitLab connected |
| `POST /api/v1/projects/{namespace}/{name}/milestones/{iid}/followups/dismiss` | Dismiss the retrospective's remaining follow-ups. | `steer`, owner subject, GitLab connected |
| `POST /api/v1/projects/{namespace}/{name}/milestones/{iid}/issues/{issue}/close` | Close an issue that keeps a done autopilot milestone open. | `steer`, owner subject, GitLab connected |
| `GET /api/v1/projects/{namespace}/{name}/estimate` | Estimated cost of `?plan=` or `?milestone=`, from the project's last finished milestones. | see, research database; Desk chat with `?plan=` |

## Prompts and the pilot's actions

The milestone and task views carry the questions (prompts) and what the
pilot did. A closed prompt has `closedReason` (`answered`, `expired`,
`stateChanged`, `mainHealthy`, `agentRanAgain`, `budgetResolved`,
`pauseOver`, `issueDone` or `replaced`), `closedBy` (`owner`, `pilot`,
`operator`, `selfHealed` or `laterRun`) and, for `replaced`, `replacedBy`,
the newer prompt's ID. Prompts closed before these fields existed have none
of them. A milestone's `pilot.acted` lists the newest 30 actions the pilot
ran without a prompt, newest first. `pilot.interventions` gives per issue
(`issueIID`, 0 for the milestone) the actions `used` of `max` on the
current head.

## Tasks

A task is one issue's work (or a milestone's planning, or its pilot), see
the [glossary](../concepts/glossary.md#task).

| Method and path | Purpose | Needs |
|-----------------|---------|-------|
| `GET /api/v1/projects/{namespace}/{name}/tasks/{task}` | One task, its cost, its open questions and what you may do on it. | see |
| `GET /api/v1/projects/{namespace}/{name}/tasks/{task}/transcript` | Live transcript of the task's run, as server-sent events. | `transcript` |
| `GET /api/v1/projects/{namespace}/{name}/tasks/{task}/logs` | Raw log of the task's pod. `?container=runner` or `agent`, `?tail=`, `?follow=1`. | `steer`, owner subject |
| `GET /api/v1/projects/{namespace}/{name}/tasks/{task}/chat` | The task's chat, newest first. Bodies only with `conversation`. | see, research database |
| `POST /api/v1/projects/{namespace}/{name}/tasks/{task}/chat` | Send a message, an interrupt or a command (retry, pause, answer, revisit, restart, …). | Desk chat, `chat`, `agents.chat` on the project; owner subject for `merge`, `start`, `unstart`, `endmilestone`, `parallel`, `restart`, `answer`, `accept`, `reject`, `draft`, `revisit` |
| `GET /api/v1/projects/{namespace}/{name}/tasks/{task}/thread` | The comments of the task's issue, merge request and tracking issue. | see, GitLab connected |
| `POST /api/v1/projects/{namespace}/{name}/tasks/{task}/notes` | Post a comment to the issue, the merge request or the tracking issue. | `steer`, GitLab connected |

## Plans

A plan is a milestone you draft with the planner in the Desk before it
exists in GitLab.

| Method and path | Purpose | Needs |
|-----------------|---------|-------|
| `GET /api/v1/projects/{namespace}/{name}/plans` | The project's plans, newest first. | see, Desk chat |
| `GET /api/v1/projects/{namespace}/{name}/plans/{plan}` | One plan with its draft. | see, Desk chat |
| `POST /api/v1/projects/{namespace}/{name}/plans` | Start a plan, with an optional `{"title": "…"}`. | `steer`, Desk chat |
| `DELETE /api/v1/projects/{namespace}/{name}/plans/{plan}` | Delete a plan. | `steer`, Desk chat |
| `PUT /api/v1/projects/{namespace}/{name}/plans/{plan}/draft` | Save your edit of the draft. | `steer`, Desk chat |
| `POST /api/v1/projects/{namespace}/{name}/plans/{plan}/budget` | Raise the plan's budget, up to its limit. | `steer`, Desk chat |
| `POST /api/v1/projects/{namespace}/{name}/plans/{plan}/milestone` | Create the milestone in GitLab from the draft. | `steer`, Desk chat, GitLab connected |
| `GET /api/v1/projects/{namespace}/{name}/plans/{plan}/chat` | The plan's chat. | see, Desk chat |
| `POST /api/v1/projects/{namespace}/{name}/plans/{plan}/chat` | Send the planner a message. | `steer`, Desk chat |

On plans, `steer` also needs you to be an owner subject
of the project's milestones, when any of them names owner subjects.

## Activity and spend

| Method and path | Purpose | Needs |
|-----------------|---------|-------|
| `GET /api/v1/inbox` | What needs you now: questions, prompts, proposals, pauses, failed runs, budget warnings. `handled` lists what closed in the last 7 days, at most 50 rows, newest first: closed prompts and the pilot's actions, with `kind` (`prompt` or `acted`), `closedReason`, `closedBy`, `by` and `at`. | see |
| `GET /api/v1/events` | Server-sent events that say *what* changed (a project, a task, a research), without content. | login |
| `GET /api/v1/stream` | The activity stream: comments, runs, pushes, CI, reviews, prompts, chat. `?project=`, `?milestone=`, `?issue=`, `?kinds=`, `?q=`. Comment bodies only with `conversation`. | see, research database |
| `GET /api/v1/projects/{namespace}/{name}/history` | Activity per day: runs, merges, cost. `?days=` (1 to 90), `?tz=`. | see, research database |
| `GET /api/v1/spend` | What a project, milestone, issue, task or run cost, with breakdowns by kind, role and model. `?namespace=&project=` are required; narrow it with `&milestone=`, `&issue=`, `&task=` and `&run=` (`run` needs `task`). | see, research database |
| `GET /api/v1/costs` | Costs over a range for the Costs page. `?range=` or `?from=&to=`, `?group=`, filters. | see, research database |
| `GET /api/v1/costs/top` | The ten costliest milestones, issues and runs of the range. | see, research database |
| `GET /api/v1/costs/metrics` | The cost per 100 merged lines by experiment arm, model, effort and step session, and the median base context and top tools per project, for the range. | see, research database |

## Research

These endpoints exist only with a research database, and only for research
rules. See [Research](../guides/research.md).

| Method and path | Purpose | Needs |
|-----------------|---------|-------|
| `GET /api/v1/researches` | The researches you may read. | `research: read` |
| `GET /api/v1/researches/{namespace}/{name}` | One research and its latest result. | `research: read` |
| `GET /api/v1/researches/{namespace}/{name}/runs` | Its runs, newest first. | `research: read` |
| `GET /api/v1/researches/{namespace}/{name}/runs/{id}` | One run and its result. | `research: read` |
| `GET /api/v1/research/search?q=` | Search the results, web search syntax. | `research: read` |
| `GET /api/v1/research/form` | What the one-off form offers: projects, models, depths. | `research: write` |
| `POST /api/v1/researches` | Start a one-off research. | `research: write` |
| `DELETE /api/v1/researches/{namespace}/{name}` | Delete your own one-off research. | `research: write` |

The one-off endpoints also need the chart value `ui.research.allowCreate`;
without it they answer 404.

## Limits

- A chat message is at most 16 KiB, a one-off research request 32 KiB.
- Chat, comments and plans have rate limits per user; over them the API
  answers 429.
- Event streams (`/api/v1/events`, `…/transcript` and
  `…/logs?follow=1`) end when your session expires: open them again.
  `/api/v1/events` sends a heartbeat every 25 seconds. You may have 20
  streams open at a time; more answer 429. `/api/v1/stream` is a plain
  JSON list, not an event stream: page it with `?before=` and `?after=`.
- If your gateway cuts long requests, set
  `ui.httpRoute.timeouts: {request: 0s}` in the chart to keep the streams
  open.

## Next

- [Commands](commands.md): the `/agent` commands the chat commands mirror.
- [AgentProject](agentproject.md): the project settings, such as
  `agents.chat`, which lets a project's tasks take chat messages.
- [Chart values](../../charts/ai-operator/README.md#web-ui): the Desk's
  `ui.*` values.
