# Labels

This page lists the GitLab labels the operator reads and sets. It covers
the `agent::` state labels, what moves an issue between them, the labels
you may set yourself, and the labels only the operator sets. Your board
shows them, so you can see each issue's state at a glance.

The label names below are the defaults. Your `AgentProject` can rename the
state labels under `spec.workflow` (`readyLabel`, `doneLabel` and so on).

## Examples

Hand an issue to the agent: add `agent::ready`. The operator starts a
[run](../concepts/glossary.md#run) on its next poll and moves the issue to
`agent::working`.

Pick a stronger model for one issue: add `agent-model::claude/opus`, or
write `/agent use claude opus` (see [Commands](commands.md#model-and-effort)).

Finish an issue by hand: merge its merge request and close the issue, or
set `agent::done`. The operator removes the issue's task, pod and
workspace.

## State labels

An issue has at most one `agent::` state label. It tells you who has the
issue: you, or the agent.

| Label | Meaning | Who sets it |
|-------|---------|-------------|
| `agent::draft` | Planned, not handed over yet. The planner may still change it when you edit the spec. | The [planner](../concepts/glossary.md#planner), or you |
| `agent::ready` | Handed over. The next poll starts a run. | You, or autopilot when it starts the issue |
| `agent::working` | A run is in progress, or CI is checking the agent's push. | The operator only |
| `agent::needs-input` | The agent asked questions, a run failed for a reason in the issue, or CI stayed red after the fix rounds. You answer. | The operator only |
| `agent::review` | The merge request is pushed and CI is green, or there is no pipeline. Your review. | The operator only |
| `agent::blocked` | Something outside the issue stops it, for example a red `main` or a quota. | The operator only |
| `agent::done` | Finished. The task, pod and workspace are removed. | You, autopilot after it merges, or the pilot when it closes an issue |

`agent::draft` and `agent::done` mean the agent does nothing on the issue.
Any of the other five means the issue has a task.

### What moves an issue

- **draft → ready**: you hand the issue over, in the Desk or by setting the
  label. On an [autopilot](../concepts/glossary.md#autopilot) milestone
  autopilot does it once the issue's dependencies are merged.
- **ready → working**: the operator starts a run.
- **working → review**: the run ends without questions and CI is green,
  was canceled, or there is no pipeline.
- **working → needs-input**: the agent asked questions, the run failed for
  a reason in the issue, or CI is still red after the fix rounds. A run that
  fails for a reason outside the issue, such as the cluster, a quota or a
  red `main`, moves the issue to `agent::blocked` instead (see
  [Why it is blocked](#why-it-is-blocked)).
- **needs-input → working**: you answer the agent's
  [prompts](../concepts/glossary.md#prompt) in the Desk, write an `/agent`
  comment, or set `agent::ready`.
- **review → working**: you write an `/agent` comment, CI turns red, or the
  merge request has a merge conflict.
- **working or review → blocked**: a reason outside the issue stops it.
- **blocked → back**: the operator puts the issue back where it was once
  the reason is gone. A new run, from `agent::ready` or an `/agent`
  comment, also lifts the block.
- **review → done**: autopilot merges, or you merge and close the issue.

Setting `agent::working`, `agent::review` or `agent::needs-input` by hand
starts nothing. If you remove `agent::blocked`, the issue is left without a
state label, and the agent does nothing until you set `agent::ready`.

### Why it is blocked

With `agent::blocked` the operator also sets a reason label,
`agent-blocked::<reason>`:

| Reason | Meaning |
|--------|---------|
| `agent-blocked::infra` | The run failed for a reason outside the agent, for example the cluster. It is retried after a while. |
| `agent-blocked::main-red` | The default branch is red, so the issue waits. |
| `agent-blocked::quota` | The model provider's rate limit or quota was hit. |

If you remove the reason label alone, the operator sets it again.

## Labels you may set yourself

Each of these has a command too, so you can also set it with a comment.

| Label | Effect | Command |
|-------|--------|---------|
| `agent::ready` | Hand the issue to the agent. Starts a run. | — |
| `agent::done` | The issue is finished. Its task, pod and workspace are removed. | — |
| `agent-profile::<name>` | Implement with that [tier](../concepts/glossary.md#tier), for example `agent-profile::strong`. An unknown name falls back to the project's implementer. | — |
| `agent-model::<engine>/<model>` | Implement with that engine and model, for example `agent-model::claude/opus`. It must be allowed in the `AgentProject`. | `/agent use` |
| `agent-effort::<level>` | Implement with that effort: `low`, `medium`, `high`, `xhigh` or `max`. It is lowered to `agents.maxEffort` (default `high`). | `/agent effort` |
| `agent-repo::<project>` | Change that [companion repository](../concepts/glossary.md#companion-repository) too. | `/agent repo` |

- **Precedence:** the model and effort labels override the profile. The
  profile overrides the project's implementer. See
  [Model and effort](commands.md#model-and-effort).
- **The planner sets one profile label, no effort label.** It keeps a
  profile, model or effort label that a human set, also on a re-plan. On a
  draft it replaces its own profile label with its new pick. See
  [tier](../concepts/glossary.md#tier).
- **Companion labels need a reason:** an `agent-repo::` label counts only
  when the milestone has a line `/agent repo <project>`, or when an
  [owner](../concepts/glossary.md#owner) or the operator set it. Otherwise it
  is ignored with one notice. A companion label the planner set is replaced
  on a re-plan; one an owner set stays.

## Labels the operator sets

You do not set these. You may read them, filter by them, and close the
issues they mark.

| Label | Where | Meaning |
|-------|-------|---------|
| `ai-generated` | issues | The operator or one of its agents created the issue: the planner's [sub-issues](../concepts/glossary.md#sub-issue), the pilot's issues, revisit follow-ups and the `ci::` issues. |
| `ci::main-red` | issues | The default branch is red. The operator opened this issue to track it. |
| `ci::flaky` | issues | A job on the default branch fails now and then. The operator opened this issue to track it. |
| `ci::main-canceled` | issues | The default branch's pipeline was canceled. |
| `agent::companion` | merge requests | A merge request in a companion repository, opened for an issue of this project. |
| `agent-blocked::<reason>` | issues | Why the issue is blocked, see [above](#why-it-is-blocked). |

The `ci::` issues have no state label: the agent works on them only when
autopilot or the pilot hands one over, or when you set `agent::ready`.

## Next

- [Commands](commands.md): every `/agent` and `/pilot` command.
- [Glossary](../concepts/glossary.md): the terms on this page.
- [How it works](../concepts/how-it-works.md): the path from a spec to merged
  work.
