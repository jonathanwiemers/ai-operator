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
| `GET /api/v1/push` and `PUT`, `DELETE /api/v1/push/subscription` | Whether push notifications are on, and a device's push subscription. See [Push API](push-api.md). | login |

## Projects and milestones

| Method and path | Purpose | Needs |
|-----------------|---------|-------|
| `GET /api/v1/projects` | Your projects, with task counts and cost. A project an AgentProjectSet created has `set`, the set's name. | see |
| `GET /api/v1/projectsets` | Your AgentProjectSets: group, `include` and `exclude`, the last discovery, the counts (`active`, `dormant`, `excluded`, `explicit`, `nameTaken`) and each matched project you may see, with its state and reason. An empty list on a cluster without the AgentProjectSet resource. | see, on the set's name |
| `GET /api/v1/overview` | The dashboard: what is running, paused and waiting, what needs you, spend today and this week. `?tz=` is an IANA zone such as `Europe/Berlin`: "Spend today" and "Spend 7 days" count from that zone's midnight. A missing or unknown zone, or `Local`, means UTC. | see |
| `GET /api/v1/milestones` | The milestones of your projects. `?state=open` (default), `done` or `all`; `?project=`, `?limit=`. An open autopilot milestone has `queuePosition`, its place in the project's run order, from 1. | see |
| `GET /api/v1/search?q=` | Find tasks by `#issue`, `!merge request` or a part of the title. | see |
| `GET /api/v1/projects/{namespace}/{name}` | One project: its tasks by milestone, the autopilot queue, finishing milestones and cost. | see |
| `GET /api/v1/projects/{namespace}/{name}/milestones/{iid}` | One milestone: autopilot state, pause and its ways out, prompts, issues, releases, the pilot and the retrospective. | see |
| `GET /api/v1/projects/{namespace}/{name}/milestones/{iid}/issues/{issue}` | One issue of the milestone and its figures. | see |
| `GET /api/v1/projects/{namespace}/{name}/milestones/{iid}/problems` | The milestone's problems, open first: issue, title, status, cause and times. Without the research database only the open ones. | see |
| `GET /api/v1/projects/{namespace}/{name}/milestones/{iid}/problems/{id}` | One problem's thread: facts, events oldest page first with `older` and `?before=`, its open question, the pilot task and what you may do. `{id}` is the problem's id or `issue-<iid>`. | see |
| `GET /api/v1/projects/{namespace}/{name}/milestones/{iid}/thread` | The comments of the milestone's tracking issue. | see, GitLab connected |
| `POST /api/v1/projects/{namespace}/{name}/milestones/{iid}/notes` | Post `{"body": "…"}` to the milestone's pilot on the tracking issue. | `steer`, GitLab connected |
| `POST /api/v1/projects/{namespace}/{name}/milestones/{iid}/followup` | Create the follow-up milestone of the retrospective's follow-ups. | `steer`, owner subject, GitLab connected |
| `POST /api/v1/projects/{namespace}/{name}/milestones/{iid}/followups/dismiss` | Dismiss the retrospective's remaining follow-ups. | `steer`, owner subject, GitLab connected |
| `POST /api/v1/projects/{namespace}/{name}/milestones/{iid}/issues/{issue}/close` | Close an issue that keeps a done autopilot milestone open. | `steer`, owner subject, GitLab connected |
| `GET /api/v1/projects/{namespace}/{name}/estimate` | Estimated cost of `?plan=` (with `&draft=`, a draft key) or `?milestone=`, from the project's last finished milestones. | see, research database; Desk chat with `?plan=` |
| `GET /api/v1/projects/{namespace}/{name}/decisions` | The agents' decisions with their revisit chain and what *Revisit* does. `?milestone=` (an IID, empty for all), `?q=` (search), `?state=all`, `active` or `superseded`. `available: false` without the history database. | see |

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
| `POST /api/v1/projects/{namespace}/{name}/tasks/{task}/chat` | Send a command (retry, pause, answer, revisit, restart, …) to any task. A message or an interrupt goes to a milestone's pilot task only; any other task answers 403 "talk to the pilot in the milestone's Cockpit". | Desk chat, `chat`, `agents.chat` on the project; owner subject for `merge`, `start`, `unstart`, `endmilestone`, `parallel`, `restart`, `answer`, `accept`, `reject`, `draft`, `revisit` |
| `POST /api/v1/projects/{namespace}/{name}/tasks/{task}/warm` | Wake the milestone's pilot without a message, so it answers fast. The Desk calls it when you open a problem thread. | owner subject |
| `GET /api/v1/projects/{namespace}/{name}/tasks/{task}/thread` | The comments of the task's issue, merge request and tracking issue. | see, GitLab connected |
| `POST /api/v1/projects/{namespace}/{name}/tasks/{task}/notes` | Post a comment to the issue, the merge request or the tracking issue. | `steer`, GitLab connected |

## Plans

A plan is a milestone you draft with the planner in the Desk before it
exists in GitLab.

| Method and path | Purpose | Needs |
|-----------------|---------|-------|
| `GET /api/v1/plans` | The plans of every project you may see, last active first, and those projects, plus the dormant projects of your sets, with whether you may start plans in them. `?project=namespace/name` narrows both to one project. | see, Desk chat |
| `GET /api/v1/projects/{namespace}/{name}/plans/{plan}` | One plan with its drafts. | see, Desk chat |
| `POST /api/v1/projects/{namespace}/{name}/plans` | Start a plan with `{"message": "…", "title": "…"}`. `message` is required and becomes the plan's first chat message; `title` is optional. | `steer`, Desk chat |
| `DELETE /api/v1/projects/{namespace}/{name}/plans/{plan}` | Delete a plan. | `steer`, Desk chat |
| `PUT /api/v1/projects/{namespace}/{name}/plans/{plan}/draft` | Save your edit of the draft `key`. | `steer`, Desk chat |
| `DELETE /api/v1/projects/{namespace}/{name}/plans/{plan}/drafts/{key}` | Remove a draft that has no milestone. | `steer`, Desk chat |
| `PUT /api/v1/projects/{namespace}/{name}/plans/{plan}/drafts/order` | Reorder the drafts. | `steer`, Desk chat |
| `POST /api/v1/projects/{namespace}/{name}/plans/{plan}/budget` | Raise the plan's budget, up to its limit. | `steer`, Desk chat |
| `POST /api/v1/projects/{namespace}/{name}/plans/{plan}/milestone` | Create the milestone of the draft `key` in GitLab. | `steer`, Desk chat, GitLab connected |
| `GET /api/v1/projects/{namespace}/{name}/plans/{plan}/chat` | The plan's chat. | see, Desk chat |
| `GET /api/v1/projects/{namespace}/{name}/plans/{plan}/decisions` | The history of the plan's decisions: each decision's versions and sign-offs, newest first. | see, Desk chat |
| `POST /api/v1/projects/{namespace}/{name}/plans/{plan}/chat` | Send the planner a message. | `steer`, Desk chat |

On plans, `steer` also needs you to be an owner subject
of the project's milestones, when any of them names owner subjects.

```sh
curl -sS -X POST -b "$COOKIE" -H 'Content-Type: application/json' \
  "$DESK/api/v1/projects/agents/tools-foo/plans" \
  -d '{"message":"Add a CSV export to the Metrics page"}'
```

Without `message` the answer is `400` with `field: message`. The projects
of `GET /api/v1/plans` have `namespace`, `name`, `path` (the GitLab path),
`set` (the [project set](../concepts/glossary.md#project-set), empty for
an AgentProject of its own), `dormant` and `steer`. A dormant entry
(`dormant: true`) has no AgentProject yet: address it by the name its child
will get, and the plan endpoints work as for any project. A plan has
`message`, its status message, such as "Waiting for the project set to
create the project". Until the set creates the project, only `GET` plan,
`GET` and `POST` chat, `DELETE` plan and `POST …/plans` work; every other
plan endpoint answers `409` with "waiting for the project set to create
the project".

### Plan drafts

A plan holds up to 5 [plan drafts](../concepts/glossary.md#plan-draft).
This example saves an edit of draft 2, puts draft 2 first and creates its
milestone:

```sh
P="$DESK/api/v1/projects/$NS/$PROJECT/plans/$PLAN"
curl -sS -X PUT -b "$COOKIE" -H 'Content-Type: application/json' \
  "$P/draft" -d '{"key":2,"baseRevision":3,"draft":"# Page\n…"}'
curl -sS -X PUT -b "$COOKIE" -H 'Content-Type: application/json' \
  "$P/drafts/order" -d '{"keys":[2,1,3]}'
curl -sS -X POST -b "$COOKIE" -H 'Content-Type: application/json' \
  "$P/milestone" -d '{"key":2,"title":"Page","description":"…",
                      "spec":true,"autopilot":true}'
```

`GET …/plans/{plan}` has `drafts`, in the order their milestones are
created, never `null`. Each draft has `key`, `title`, `draft`, `revision`,
`by` (`agent` or `owner`), `sha`, `createdMilestoneIID` and
`createdMilestoneURL` once created, and `creating: true` while its
milestone is being created. A key is given once per plan and never
reused. A plan from before drafts shows its draft as key 1.

The plan and the plan list have `draftsTotal`, `draftsCreated` and
`createdMilestones`, a list of `iid` and `url` in draft order.

The rules:

- `PUT …/draft` takes `key`, `baseRevision` (the `revision` your edit
  started from), `draft` and an optional `title`. Without `key` (or with
  0), it edits the first draft without a milestone. When the draft's
  revision is no longer `baseRevision`, the answer is `409` with the
  current plan: show the newer draft.
- `PUT …/drafts/order` takes `keys`, every draft's key once (`422`
  otherwise).
- `POST …/milestone` takes `key`, `title`, `description`, `spec` and
  `autopilot`, with the same default for `key` as `PUT …/draft`. It answers
  `201` with the milestone's `iid` and `webURL`.
- Only the first draft without a milestone can be created: any other
  answers `409` with "create draft 1 first", naming the draft to create.
  Before it creates the
  milestone, each `%draft-N` of a created draft becomes `%IID`. A
  `%draft-N` of a draft not created yet answers `422` with
  `field: "description"`, naming the reference.
- A created draft is read-only: editing, removing or moving it answers
  `409`.
- When GitLab refuses the milestone, the draft stays uncreated and the
  plan stays open.
- `…/estimate?plan=` counts the draft `&draft=` names. Without it, it
  counts the first draft without a milestone, else the last draft.

Only the agent adds drafts. The plan's phase becomes `Created` once every
draft has its milestone; until then the plan takes chat messages.

### Plan decisions

A plan's current decisions are in `GET …/plans/{plan}`: `decisions` is the
list (never `null`) and `decisionsPending` counts the ones that are open or
whose current version is not signed off. Each entry has `id`, `title`,
`question`, `options`, `recommended` (an index), `why`, `state` (`open` or
`locked`), `version`, `decidedBy`, `decidedByName`, `decidedAt`,
`signedOffBy`, `signedOffByName` and `signedOffAt`. `decidedBy` is `agent`
or the owner.

To answer them, post to the plan's chat with a `decisions` list. Each answer
names the decision and the `version` you saw, and one of `keep`, `option`
(an index) or `other` (your own text). The `body` is the extra text and may
be empty when answers are present:

```sh
curl -sS -X POST -H "Cookie: $COOKIE" -H 'Content-Type: application/json' \
  "$DESK/api/v1/projects/$NS/$PROJECT/plans/$PLAN/chat" \
  -d '{"kind":"message","body":"","decisions":[
        {"id":"D1","version":2,"keep":true},
        {"id":"D2","version":1,"option":0},
        {"id":"D6","version":3,"other":"Use the existing queue."}]}'
```

Each answer sets exactly one of the three, and each ID appears once; if not,
the answer is `400`. Answers go with `"kind": "message"` only.

If an ID is not in the plan's current list, or its version is not the
current one, the answer is `409` with "the decisions changed": read the plan
again and send against the new versions.

`GET …/plans/{plan}/decisions` returns `{"available": true, "decisions":
[…]}`. Each decision has its `id` and `versions`, newest first. A version
has the fields above up to `decidedAt`, and `signOffs`: a list of `by`,
`byName` and `at`, one per owner's sign-off. The history includes decisions
the agent dropped. `available` is `false` without the history database.

## Activity and spend

| Method and path | Purpose | Needs |
|-----------------|---------|-------|
| `GET /api/v1/inbox` | What needs you now: questions, prompts, proposals, pauses, failed runs, budget warnings. `handled` lists what closed in the last 7 days, at most 50 rows, newest first: closed prompts and the pilot's actions, with `kind` (`prompt` or `acted`), `closedReason`, `closedBy`, `by` and `at`. | see |
| `GET /api/v1/events` | Server-sent events that say *what* changed (a project, a task, a research), without content. | login |
| `GET /api/v1/stream` | The activity stream: comments, runs, pushes, CI, reviews, prompts, chat, decisions (`decision.recorded`). `?project=`, `?milestone=`, `?issue=`, `?kinds=`, `?q=`. Comment bodies only with `conversation`. | see, research database |
| `GET /api/v1/projects/{namespace}/{name}/history` | Activity per day: runs, merges, cost. `?days=` (1 to 90), `?tz=`. | see, research database |
| `GET /api/v1/spend` | What a project, milestone, issue, task or run cost, with breakdowns by kind, role and model. A project, milestone or issue also gets its merged issues' merged lines and the cost per 100 merged production lines (`mergedLines`). `?namespace=&project=` are required; narrow it with `&milestone=`, `&issue=`, `&task=` and `&run=` (`run` needs `task`). | see, research database |
| `GET /api/v1/costs` | Costs over a range for the Metrics page. `?range=` or `?from=&to=`, `?group=`, filters. | see, research database |
| `GET /api/v1/costs/top` | The ten costliest milestones, issues and runs of the range. | see, research database |
| `GET /api/v1/costs/metrics` | The cost per 100 merged lines by experiment arm, model, effort and step session, and the median base context and top tools per project, for the range. | see, research database |
| `GET /api/v1/metrics/code` | The lines of the range. Per bucket, it gives the written lines (the calls' added and deleted lines, by call time) and the merged lines by category (each merge and companion merge at its own time, `partial` when some diff had no count). It also gives the totals, and the issues merged in the range, each whole with its companions and its calls before the range: merged and measured counts, their lines and the cost per 100 merged production lines. Without `?project=`, it gives the same per project; companion lines count under the home project. It takes the parameters of `/api/v1/costs`; only the range and `?project=` apply, and research calls never count. | see, research database |
| `GET /api/v1/metrics/flow` | The issues merged in the range, each once at its latest merge: merged issues per bucket, the median wall time (first run start to merge) and median agent time (the sum of the issue's call durations), and the review and fix calls per merged issue. An issue counts whole, with its runs and calls before the range. It takes the parameters of `/api/v1/costs`; only the range and `?project=` apply. | see, research database |
| `GET /api/v1/metrics/pauses` | The milestone pauses that started in the range: per bucket and kind, totals, open now, by kind, resolver and via, per project, and the ten longest with a link to the milestone (`?tab=pilot`, which opens its *Cockpit*). It takes the parameters of `/api/v1/costs`; only the range and `?project=` apply. `truncated` is set when more than 20000 pause events were found, and only the newest were read. | see, research database |
| `GET /api/v1/timeline` | When each issue ran, waited and was held, for one milestone or a time window. See [Timeline](#timeline). | see |

The stream's `decision.recorded` event is a decision saved for an issue. It
has the milestone, the issue and the decision's posting time, and its `data`
is `{id, title, choice, kind, supersedes}` (`supersedes` only when it
replaces an earlier decision). The history recorder writes one per decision
ID, once. The Cockpit shows it in the issue's thread; the Overview does not.

### Timeline

`GET /api/v1/timeline` draws the [timeline](../guides/milestone-order-and-timeline.md#the-issue-timeline)
in the Desk. You ask for the last day of all your projects like this:

```sh
curl -s -b "$COOKIE" \
  "$DESK/api/v1/timeline?from=2026-11-20T00:00:00Z&to=2026-11-21T00:00:00Z"
```

```json
{
  "history": true,
  "from": "2026-11-20T00:00:00Z",
  "to": "2026-11-21T00:00:00Z",
  "now": "2026-11-21T09:30:00Z",
  "rows": [
    {
      "namespace": "agents", "project": "shop",
      "milestoneIID": 12, "milestoneTitle": "Checkout",
      "issueIID": 340, "issueTitle": "Add coupon field", "wave": 1,
      "segments": [
        {"kind": "running", "start": "2026-11-20T08:00:00Z", "end": "2026-11-20T08:30:00Z"},
        {"kind": "review", "start": "2026-11-20T08:30:00Z", "end": "2026-11-20T08:40:00Z"},
        {"kind": "held", "start": "2026-11-20T08:40:00Z", "end": "2026-11-20T11:00:00Z"},
        {"kind": "running", "start": "2026-11-20T11:00:00Z", "end": "2026-11-20T11:25:00Z"},
        {"kind": "ci", "start": "2026-11-20T11:25:00Z", "end": "2026-11-20T11:40:00Z"}
      ],
      "mergedAt": "2026-11-20T11:40:00Z",
      "deskURL": "/projects/agents/shop/milestones/12/issues/340"
    }
  ],
  "truncated": false
}
```

It has two modes:

- **One milestone**: `?project=namespace/name&milestone=<iid>`. Each issue
  from its first run to now. `from` and `to` are not allowed here.
- **A window**: `?from=&to=`, both RFC 3339 times, across every project
  you may see. Add `?project=namespace/name` for one project. `to` must be
  after `from`, and the window may be at most 7 days.

| Field | Meaning |
|-------|---------|
| `history` | `false` without the research database. Then `rows` is empty. |
| `from`, `to`, `now` | The window and the server's time. For one milestone, `from` is the first segment's start and `to` is now. |
| `rows` | One per issue: project, milestone, issue, `wave` (0 when unknown) and `deskURL`, the issue's page in the Desk. |
| `segments` | `kind` is `running`, `review`, `ci`, `waiting` or `held`, with `start` and `end`. `review` is a reviewer at work, `ci` a pipeline of the issue's merge request. Where they overlap, `review` wins over `running`, `running` over `ci`, and `ci` over `held`. `end` is absent while the segment goes on. Segments are cut at the window's edges. |
| `mergedAt`, `skippedAt` | When the issue merged or was skipped. Absent otherwise. Its life ends there. |
| `truncated` | `true` when more than 500 issues matched. Only the newest are in `rows`. |

Only projects you may see appear. A `project` you may not see answers 404.
A wrong parameter answers 400 with the reason. Holds appear only from the
operator version that records them; older issues show no `held` segments.

## Research

These endpoints exist only with a research database, and only for research
rules. See [Research](../guides/research.md).

| Method and path | Purpose | Needs |
|-----------------|---------|-------|
| `GET /api/v1/researches` | The researches you may read. | `research: read` |
| `GET /api/v1/researches/{namespace}/{name}` | One research and its latest result. | `research: read` |
| `GET /api/v1/researches/{namespace}/{name}/runs` | Its runs, newest first. | `research: read` |
| `GET /api/v1/researches/{namespace}/{name}/runs/{id}` | One run and its result. | `research: read` |
| `GET /api/v1/research/form` | What the one-off form offers: projects, models, depths. | `research: write` |
| `POST /api/v1/researches` | Start a one-off research. | `research: write` |
| `DELETE /api/v1/researches/{namespace}/{name}` | Delete your own one-off research. | `research: write` |

The one-off endpoints also need the chart value `ui.research.allowCreate`;
without it they answer 404.

## Limits

- A chat message is at most 16 KiB, a one-off research request 32 KiB.
- A plan holds at most 5 drafts. A draft is at most 64 KiB, its title at
  most 255 characters on one line.
- A plan chat message may answer at most 30 decisions, and an `other`
  answer is at most 2,000 characters. The agent returns at most 30
  decisions per plan, each with 2 to 5 options. A title is at most 120
  characters, a question and a `why` at most 1,000, an option at most 500.
  The history expires with the chat history retention.
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
