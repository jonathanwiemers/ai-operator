# Desk: ask the pilot

Status: approved by the maintainer (2026-11-03). Revised the same day: the
maintainer chose to reuse the milestone's pilot instead of a new responder
pod (see Decisions).

## Problem

The task chat ([desk-live-agent-chat](desk-live-agent-chat.spec.md)) talks
to the agent of one task, inside that task's pod. A pod needs a run slot,
and most projects allow one (`maxConcurrentRuns: 1`). The bloop end-to-end
test shows what that means: a question waits behind an implement run for an
hour or more. The pilot (`Coordinate` task) waits the same way:
`bloop-pilot-m21` stays `Pending` while #391 runs. The maintainer decided
that chat turns keep waiting for a slot, which is right for steering an
implementer.

Most of what the maintainer wants to type in the Desk is not steering but a
question about the milestone:

- "Why did #391 fail?"
- "What does the spec say about side pots?"
- "Where is the milestone at?"
- "What is P3 about?"

That is the pilot's job. It already has a session that knows the
milestone, a light model, and a state snapshot it gets for its GitLab
messages (`pilotState`).

## Goals

- **Ask the pilot** on the Desk milestone page: a question gets the
  pilot's answer within about a minute, whatever the implementers are
  doing.
- **Fresh answers:** every chat turn of the pilot carries the milestone's
  current state, not only what its session saw last.
- **Read-only:** the pilot chat answers. Acting stays with the command
  buttons (retry, pause, accept P3, …) and the proposals the pilot already
  makes.
- The same durability and grants as the task chat.

## Non-goals

- A pod or engine call outside the pilot (the first draft's `desk-pilot`
  responder). It is the fallback if this is not enough (see Risks).
- Project-level questions, and milestones without a pilot.
- Changing how the pilot acts (proposals, autonomy, retrospective).

## Constraints

- Pilot pods have a workspace on mbp's local disk: they run on mbp only,
  so no answers while mbp is off.
- The task chat's pieces stay as they are: `chat_messages`, the
  dispatcher, runner chat turns, §3a durability and the `chat` and
  `conversation` grants.

## Proposed approach

### 1. Pilots outside the run capacity

`Coordinate` tasks neither count toward `maxConcurrentRuns` nor wait for
it. Today `runningTasks` counts every pod of the project, and the capacity
check at pod creation applies to every task.

- **Why this is safe:** a project has at most one pilot per planned
  milestone with a tracking issue. Pilot runs use the light `agents.pilot`
  model, build nothing and push nothing, and their spend is capped by
  `pilot.maxBudgetUSD`.
- The existing gates still apply: suspend, blocked, usage-limit deferral,
  the agent image.

### 2. Fresh state in pilot chat turns

When the runner answers a chat message on a `Coordinate` task, the prompt
appends the milestone state from `pilotState`. This is the same JSON the
pilot's GitLab messages get, bounded and marked as untrusted data, like in
`buildPilotPrompt`.

- The pilot's chat turns stay read-only (`chatRole` for the pilot role is
  not writable today).
- The pilot system prompt for chat gains one rule: answer from the state
  and the session; to act, name the command or the proposal, and never
  claim to have acted.

### 3. Warm pilots

A pilot pod that hibernated answers after a cold start (pod start plus
session resume, about 30–60 s). The pilot's own `chatIdleTimeout` already
keeps the pod for 15 min after a chat turn. No change in v1. If cold
starts annoy, raise `lifecycle.chatIdleTimeout` for the project.

### 4. Desk

- The milestone page gets **Ask the pilot**: the chat thread and composer
  of the milestone's `Coordinate` task, the same component as on the task
  page.
  - It includes the pilot's command buttons (accept or reject per open
    proposal).
  - It shows the waiting line from slice 9c ("starting the agent…",
    "the agent is answering…").
- Without a pilot it says why: "No pilot for this milestone. Autopilot, or
  `/pilot coordinate` on an issue, starts one."
- The task page keeps its chat for steering one implementer.

## Affected files / components

- `internal/controller/agenttask_controller.go`: the capacity check skips
  `Coordinate` tasks, and `runningTasks` does not count them.
- `internal/runner/chat.go`: `buildChatPrompt` gets the state for the pilot
  role; one system prompt rule.
- `web/src/views/Milestone.vue`: Ask the pilot (`TaskChat` for the
  milestone's Coordinate task).
- Docs: `design.md` (Pilot, Desk live chat), `ui.md`, `configuration.md`
  (`maxConcurrentRuns` no longer counts pilots).

## Risks / trade-offs

- **More pods at once:** a pilot per planned milestone can run next to the
  implementer on mbp. Pilots are light and few; bloop has two planned
  milestones today.
- **Cold starts:** up to about a minute after the pilot hibernated.
- **No answers while mbp is off,** and none for milestones without a
  pilot.
- **Fallback:** if these hurt, the first draft's separate responder (one
  always-on pod, no workspace, may run on netcup01, project-level) is the
  next step. It reuses the same table and UI.

## Validation / test plan

- **Controller:**
  - a `Coordinate` task gets its pod while an implementer holds the only
    slot;
  - an implementer still waits while another implementer runs;
  - pilots do not count toward the limit.
- **Runner:**
  - a pilot chat turn's prompt carries the state and stays read-only;
  - an implementer's chat turn prompt is unchanged.
- **Web:** Milestone page with and without a pilot (`node --test` for the
  logic, a screenshot on `make run-ui`).
- **End to end on netcup01:** ask `bloop-pilot-m21` "where is the
  milestone at" while #391 runs, and get an answer within a minute or two.

## Delivery plan

| # | MR | Notes |
|---|---|---|
| 1 | Operator and runner: pilots outside the run capacity, state in pilot chat turns | small; tests above |
| 2 | Web: Ask the pilot on the milestone page | |

The desk-live-agent-chat slice 10 (`workflow.inputs`) stays on hold until
this has run for a while.

## Decisions (maintainer, 2026-11-03)

1. **Reuse the milestone's pilot** for the Desk's questions instead of a
   new responder pod. This supersedes the first draft's placement, context
   and model choices: the pilot runs on mbp with its own model and
   session.
2. No GitLab threads as extra context. The pilot has what its runs have.
3. Model: the project's `agents.pilot` role. That is what the pilot uses
   anyway.

## PRs / MRs

| # | Title | MR | Status |
|---|---|---|---|
| — | docs(spec): Desk ask the pilot | [!85](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/85) | open |
| 1 | feat: pilots outside the run capacity, milestone state in pilot chat | [!86](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/86) | open |
| 2 | Web: Ask the pilot on the milestone page | — | pending |
