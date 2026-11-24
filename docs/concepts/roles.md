# Roles

Several agents work on a milestone, each with one job. This page says what
each one does, when it runs and what you see of it, so you know who wrote a
comment, who asks you something and where a cost comes from.

Every role runs on an [engine](glossary.md#engine) (Claude Code or Cursor)
with a model and an effort you choose per role under `spec.agents` in your
`AgentProject`. The operator itself is no agent: it follows fixed rules for
the queue, the merges and the releases, and no model ever decides to merge. Every role writes in the language the
operator sets; see [Language](../reference/agents-and-models.md#language).

## At a glance

| Role | Does | Runs | Setting |
|------|------|------|---------|
| [Planner](#planner) | turns the spec into issues | once per milestone, again when the spec changes | `agents.spec` |
| [Implementer](#implementer) | writes the code of one issue | when an issue is handed over, on `/agent …`, on red CI | `agents.implement`, `agents.profiles` |
| [Reviewers](#reviewers) | check each change for bugs and security problems | after each implementing run | `agents.review` |
| [Describer](#describer) | writes the merge request description | after a push that changed the diff | `agents.describe` |
| [Pilot](#pilot) | watches the milestone, fixes, asks, sums up | on events, when you ask | `agents.pilot`, `spec.pilot` |

The [helpers](#helpers) (scout, writer and the read gate) are optional and
work inside a role's run.

## Planner

The planner reads the milestone [spec](glossary.md#spec) and the code, and
writes one [sub-issue](glossary.md#sub-issue) per merge request. Each issue
gets a short spec of its own, acceptance criteria, its dependencies, a
[profile](glossary.md#profile) and whether a release follows it (a
[release point](glossary.md#release-point)).

**When it runs:** when a milestone with `/agent spec` or `/agent autopilot`
is planned, and again when you change the spec. A re-plan updates the issues
that are still drafts and leaves started ones alone. An
[autopilot](glossary.md#autopilot) milestone is planned when its turn
comes, so the issues describe the code as it is then.

**What you see:** the new issues in GitLab, and a comment on each with the
planner's [decisions](glossary.md#decision). Without autopilot it may leave
open questions in the issues for you to answer before you hand them over; on
autopilot it asks nothing, takes the default it would propose and records it
as a decision. In the Desk, *Plan milestone* lets you draft a new
milestone's spec together with an agent before it exists in GitLab.

## Implementer

The implementer writes the code for one issue, as one merge request. It
works in steps: each step is a commit on a draft merge request, so you can
follow it and step in. It also fixes red CI and resolves merge conflicts
with the default branch.

**When it runs:** when an issue is handed over (`agent::ready`, or by
autopilot), when you give an instruction with `/agent <what to change>` or
in the Desk chat, when CI fails for a real reason, and when the reviewers
find something that blocks.

**Which model:** the planner picks a profile for each issue, for example a
cheap one for small changes and a strong one for hard ones. Issues without a
profile use `agents.implement`. You can override the model and effort per
issue with `/agent use …`, within the models your project allows.

**What you see:** the branch and the draft merge request, a report comment
after each run with what changed, how it was tested and the decisions it
made, and the task's chat and live transcript in the Desk. When it needs a
decision outside autopilot, it asks in a prompt.

## Reviewers

The reviewers read every change of the implementer. There are two, and
`agents.review.reviewers` sets which of them run:

- **bugbot** looks for bugs: wrong logic, missed cases, broken tests.
- **security** looks for security problems.

They run read-only, in the same pod and on the same checkout, so they see
exactly what the implementer produced. Each finding has a severity. Blocking
findings go back to the implementer, which fixes them in another round; after
a limited number of rounds the change goes to a human anyway.

**When they run:** after the implementer's run, on its diff. On
autopilot, CI fix runs are reviewed too, because no human reviews before
the merge.

**What you see:** the findings in the run's report on the merge request.
On autopilot, a merge request only merges once the review is clean.

## Describer

The describer writes the merge request description: why, what, how it was
checked, and a diagram where it helps. It rewrites only its own part of the
description, which is marked as written by the agent: your notes above or
below it and the `Closes #N` line stay, but edits inside that part are
replaced on its next run.

**When it runs:** after a push that changed the diff.

**What you see:** the merge request description, kept up to date.

## Pilot

Every milestone with a [tracking issue](glossary.md#tracking-issue) has a
[pilot](glossary.md#pilot): an agent that watches the milestone for you. It
knows the spec, the queue, every issue's state, CI and last run, and the
health of the default branch.

It does four things:

- **Answers you.** Ask it where things stand or why the milestone is paused.
- **Fixes what it may.** On an autopilot milestone it may act on its own
  within limits: start a run again, ask for another review, give the
  implementer an instruction, restart a run that hangs, open a follow-up
  issue. Each action is counted and reported on the tracking issue. Past a
  few actions on the same issue it stops acting and asks instead.
  `/pilot stop` makes it ask before every action.
- **Asks you.** When it needs a decision, it puts options to the
  [owners](glossary.md#owner) as a prompt, with the one it recommends.
- **Sums up.** When the milestone is done, it writes the
  [retrospective](glossary.md#retrospective).

The pilot never merges, closes issues or pushes code. It proposes actions
and the operator carries them out, after checking each one again.

**When it runs:** on events of the milestone, such as a
[pause](glossary.md#pause) or a run that hangs, and whenever you talk to
it. It does not wait in the queue of runs, and its spend has its own cap per
milestone (`pilot.maxBudgetUSD`).

**What you see:** its answers and prompts in the Desk in the milestone's
*Cockpit* and in the inbox, and its comments on the tracking issue. You
talk to it in the Desk, at the bottom of the Cockpit, or with a comment that starts with `/pilot` or
mentions the bot user. Without autopilot, `/pilot coordinate` on an issue of
a planned milestone gives that milestone a pilot.

## Helpers

These work inside a role's run. You only meet them in the run logs and the
spend views, and only if you set them up.

### Scout and writer

A role can have **subagents**: smaller agents it hands work to, each with
its own model. They are optional and work on Claude Code only. Two kinds are
common:

- A **scout** searches the code, reads logs and looks things up, and answers
  in short bullets. It runs on a cheap model, so the role's own expensive
  model reads less.
- A **writer** writes boilerplate, such as tests and fixtures that follow an
  existing file. It answers with the paths it wrote, and the role reviews
  the result.

You see them in the Desk's spend views: their models show up next to the
role's, and the spend tells you whether handing work to them saves money.

### Read gate

The read gate is a rule, not an agent. With `readGate` set on a role, the
role may not read a big file whole: it has to read a section or ask a
subagent. A blocked read is counted in the spend views. The gate never fails
a run.

## Next

- [How it works](how-it-works.md): how these roles work together on a
  milestone.
- [Security](security.md): what each role can and cannot reach.
- [Glossary](glossary.md): the terms on this page.
- [Reference](../reference/README.md): the `spec.agents` fields, with their
  defaults.
