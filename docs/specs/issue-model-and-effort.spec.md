# Choosing engine, model and effort per issue

## Problem

The implementer of an issue is fixed by its profile label (`agent-profile::fast|strong`):
engine, model and effort come from the AgentProject. A human cannot say "run this one on
Opus" or "think harder here" without editing the cluster resource, and the planner can only
pick between whole profiles, not how hard the agent should think.

## Goals

- `/agent use <engine> <model> [effort]` on an issue or its MR picks the implementer's
  engine and model (and optionally effort) for that issue, from a list the AgentProject
  allows; `/agent use default` goes back to the profile.
- `/agent effort <level>` / `/agent effort default` sets only the effort.
- The choice is a scoped label on the issue (`agent-model::claude/opus`,
  `agent-effort::high`), visible on the board, and a human may set the labels directly.
- The planner sets an effort per sub-issue along with the profile.
- The AgentProject caps the effort an issue may ask for (`agents.maxEffort`, default
  `high`).

## Non-goals

- Reviewers, the planner and the describer stay project-wide.
- Automatic escalation, usage-limit fallback (incl. Cursor's reset date) and a guidance
  file in the target repo: follow-ups.

## Constraints

- Only allowed models: a comment must not be able to pull in an arbitrary (expensive)
  model, and a typo must not fail a run.
- Commands are taken from humans only and applied once (state of the runner, like
  revisits).
- The cursor CLI has no effort flag: effort is ignored there, with a notice.

## Approach

- API:
  - `workflow.modelLabelPrefix` (`agent-model::`), `workflow.effortLabelPrefix`
    (`agent-effort::`).
  - `agents.models`: `{engine, model, alias}` entries. Allowed are these (default: Opus,
    Sonnet and Haiku with the aliases `opus`, `sonnet`, `haiku`) plus every engine/model
    the profiles and `agents.implement` use, so `/agent use cursor auto` works where a
    profile runs on cursor.
  - `agents.maxEffort` (default `high`).
  - `AgentProjectSpec.IssueRole(labels)` = `ImplementRole` plus the label overrides, with
    notices for unknown labels, a capped effort and effort on cursor.
- Operator (`internal/command`, project controller): on the poll that sees a `/agent use`
  or `/agent effort` comment of a human (newer than `status.source.lastRoutingNoteID`), it
  validates it, swaps the labels on the issue and answers with a short comment. A comment
  that is only such a command starts no run (it is left out of "latest command") and is
  dropped from the agent's prompt; with instructions on further lines it also starts a
  run. Commands written while the issue was a draft are applied on handover.
- Runner: `AgentProjectSpec.IssueRole(labels)` picks the implementer; invalid labels are
  ignored with a notice in the run report.
- Engine change: the session is kept per engine, so switching to another engine starts a
  fresh session with the full prompt; switching the model or effort within an engine
  resumes the session.
- Planner: optional `effort` per planned issue (schema enum), a prompt rule when to pick
  which, and an `agent-effort::` label on created issues (capped by `maxEffort`); a label
  that exists already (human) is kept on re-plans, like the profile label.

## Affected files

`api/v1alpha1` (types, defaults, `routing.go`), `internal/command` (new),
`internal/controller` (project sync, revisit follow-ups copy the labels), `internal/runner`
(implement.go, spec.go, prompts.go), docs (`commands.md` new, `usage.md`,
`configuration.md`, `design.md`, README), sample, chart CRDs.

## Risks / trade-offs

- Any project member who may comment can switch models within the allowed list; the list
  and `maxEffort` are the cost guard.
- A label set to a model that was later removed from the list is ignored with a notice.
- The planner may over-pick high effort; the cap and the prompt rule bound it.

## Validation

- Unit: command parsing (use/effort/default, malformed, unknown model, bot comments),
  IssueRole precedence and cap, planner effort labels.
- Controller: commands become labels with an answer, also from before the task existed;
  applied once; a pure command starts no run, one with instructions does; bot comments and
  unknown models change nothing.
- Runner: the implementer runs with the labels' model and effort; a pure command is not in
  its prompt.

## Steers

- Applied by the operator on the poll instead of by the runner at run start: a command
  shows its effect (label, answer) at once and costs no run.
- Docs: one page for all `/agent` commands (`docs/commands.md`) and a reworked README.

## Delivery

Single PR on `jonathanwiemers/ai-operator`, reviewed by a subagent.
