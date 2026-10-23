# Owner pings and agent-written MR descriptions

## Problem

1. When a run fails, the agent asks questions, CI stays red, an MR is ready
   for review or autopilot pauses, the operator comments on GitLab, but nobody
   is told: whoever wants to react has to watch the board.
2. The MR description is a placeholder ("The agent works on this; …"). What
   the change does and how is spread over the per-run report comments.

## Goals

- Mention one or more GitLab users ("owners") in the comments that need a
  human, configurable for all projects (operator flag), per project
  (`AgentProject.spec.notify`) and per milestone (`/agent owners @a @b` in
  its description).
- Ask the owners for a review natively: set them as MR reviewers when an
  issue reaches review, if the MR has none.
- Keep a description of the whole change in the MR, written by an agent:
  Why, What, How, Verification and, when it helps, a mermaid diagram at the
  bottom. It replaces the first report comment's summary.

## Non-goals

- Other channels (Slack, mail): GitLab mentions already create To-Dos and
  mails.
- Pinging when planning is done (draft sub-issues): milestones have no
  comments; a follow-up can pick a place.
- Validating mermaid by rendering it.

## Constraints

- One PR (the user asked for one; it exceeds the usual 50-line budget).
- Mentions only in new comments: edits of existing comments (live progress)
  must not ping again.
- The description must not clobber what humans write into it.
- Agent-written text must not ping people (prompt injection, noise).

## Approach

### Owners

- `AgentProjectSpec.Notify` (`NotifySpec`): `users []string` (GitLab
  usernames), `events []NotifyEvent` (default: all of `failed`, `needsInput`,
  `ciFailed`, `review`, `autopilotPaused`, `autopilotDone`), `requestReview
  *bool` (default true).
- Operator flag `--default-owners` (chart `defaultOwners`) for projects
  without `notify.users`.
- A milestone description line `<commandPrefix> owners @a @b` replaces the
  project's owners for the milestone and its issues (most specific wins, no
  union, so the global default does not ping on every milestone).
- The project controller resolves the owners each poll and writes them to
  `AgentTask.spec.owners`; runner and controllers only read the task. Issues
  without (an active) milestone take the project's owners.
- `internal/notify`: parsing, resolving, rendering `cc @a @b`, and requesting
  the owners as MR reviewers (username → ID via `GET /users?username=`).

| Event | Where | Comment |
|---|---|---|
| `failed` | runner `reportFailure` | issue |
| `needsInput` | implement run with questions / needs_input | issue (questions) or MR report |
| `ciFailed` | `finishCI` red after the fix rounds | issue |
| `review` | issue moved to review (runner without CI wait, `finishCI` green); not on autopilot | MR report / new MR comment, plus reviewers |
| `autopilotPaused` | `autopilot.pause` | tracking issue |
| `autopilotDone` | autopilot summary | tracking issue |

### MR description

- New role `agents.describe` (`DescribeSpec`: RoleSpec + `disabled`), light
  model by default, read-only, no session.
- After the push of a run, if the branch changes something against the
  default branch and the diff differs from the one last described (hash in
  the runner state), the describer gets the issue, this run's summary, the
  previous description, the diff stat and the diff (inline up to the review
  limit) and answers `why`, `what`, `how`, `verification`, `diagram`.
- The runner renders them into a managed block
  `<!-- ai-operator:description -->…<!-- /ai-operator:description -->`; text
  outside it (e.g. `Closes #N`, human notes) stays. New MRs start with the
  block holding a placeholder; older MRs get the block replacing the legacy
  placeholder, or put before a human-written description.
- The diagram is kept only if it starts with a known mermaid type and stays
  under 5000 bytes; otherwise it is dropped, not the description.
- Mentions in the prose are defused (zero-width space after `@`).
- The first run that writes the description posts its report without the
  summary ("Described the change in the MR description."); later reports keep
  their run summary.
- A failing describer does not fail the run: its error is a notice in the
  report.

## Affected files

`api/v1alpha1` (types, defaults), `cmd/operator`, `internal/notify` (new),
`internal/gitlab` (+fake), `internal/controller` (project sync, CI, autopilot),
`internal/runner` (implement, prompts, runner state, failure), chart values /
deployment / README, `docs/usage.md`, `docs/configuration.md`,
`docs/design.md`.

## Risks / trade-offs

- An extra (cheap) agent call per run that changed the diff.
- Owners on the task spec: an owners change updates the task (no run).
- Owners must be project members to be notified; the bot's own mention does
  nothing.
- GitLab may notify for mentions a human adds to the description by hand;
  the managed block itself never contains live mentions.

## Validation

- Unit: owners parsing/resolution, mention rendering, description block
  replace/insert, diagram sanitizing, defusing mentions.
- Runner e2e (fake GitLab, scripted engine): description written after the
  push, first report without summary, unchanged diff skips the describer,
  questions note mentions owners, failure note mentions owners.
- Controller: owners resolved from milestone/project/default onto tasks; CI
  red mentions; green requests reviewers; autopilot pause mentions.

## Steers from review

- Agent text going to GitLab (description prose, run reports, questions) is
  made inert: mentions after any non-word character and lines starting with
  `/` (quick actions, which GitLab runs in API-written notes and
  descriptions) get a zero-width space.
- The describer re-reads the MR right before writing, so edits made during
  its call survive; a failing checkout reset no longer drops the description.
- The owners line is stripped (with one surrounding blank line) before
  hashing, before milestone selection and from the agents' prompts.
- On autopilot, failures and questions do not ping: the pause on the
  tracking issue does.
- The CI loop records `status.ci.pinged` per commit, so a pipeline re-run in
  review does not ping again; the first report keeps its summary when the
  run stops for input.
- Not done: owners lines inside code fences still count.

## Delivery

Single PR on GitHub `jonathanwiemers/ai-operator`
([#35](https://github.com/jonathanwiemers/ai-operator/pull/35)), reviewed by
a subagent, merged when the review is clean and CI is green.
