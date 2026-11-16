# Agents and models

`spec.agents` of an [AgentProject](agentproject.md) decides who does the
work and on which model: the pod the agents run in, the
[roles](../concepts/roles.md) (planner, implementer, reviewer, describer,
pilot), the [profiles](../concepts/glossary.md#profile) an issue picks
from, and what happens when an [engine](../concepts/glossary.md#engine) is
at its usage limit. `spec.agents` itself is required, and so is its
`credentialsSecretName`; every other field is optional.

## Example

```yaml
spec:
  agents:
    credentialsSecretName: agent-credentials
    resources:
      requests: {cpu: "1", memory: 2Gi}
      limits: {memory: 6Gi}
    spec:                       # planning, one run per milestone
      model: claude-opus-5-5
      effort: high
    implement:                  # issues without a profile label
      model: claude-opus-5-5
      maxStepLines: 400
      readGate: {}
      subagents:
        - name: scout
          description: Searches the code, looks things up on the web and reads logs. Use it for every search and lookup.
          model: claude-haiku-5-5
          tools: [Read, Grep, Glob, WebFetch, WebSearch]
          prompt: You search and read for the implementing agent and never edit. Answer in bullets with exact names, paths and line numbers, no preamble.
      conflict:
        model: claude-sonnet-5-5
        effort: medium
    profiles:
      - name: fast
        description: Small, well-defined changes that follow existing patterns.
        engine: cursor
        model: auto
        fallback: {engine: claude, model: sonnet}
      - name: strong
        description: Design decisions, cross-cutting or risky changes, unclear scope.
        model: claude-opus-5-5
    review:
      model: claude-sonnet-5-5
      reviewers: [bugbot, security]
    models:
      - {engine: claude, model: claude-opus-5-5, alias: opus}
      - {engine: claude, model: claude-sonnet-5-5, alias: sonnet}
      - {engine: cursor, model: auto}
    maxEffort: high
```

## `spec.agents`

The pod and the settings that apply to every role:

| Field | Meaning | Default |
|-------|---------|---------|
| `image` | The agent image, see [Agent image](agent-image.md). | the agent image of the installed release |
| `imagePullPolicy` | Pull policy of the agent image. | `IfNotPresent` |
| `credentialsSecretName` | A Secret whose keys become environment variables of the agents, e.g. `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_API_KEY`, `CURSOR_API_KEY` (see [Secrets](../getting-started/install.md#create-the-secrets)). Required. | – |
| `serviceAccountName` | Service account of the agent pods. | `ai-operator-agent` |
| `resources` | Resources of the agent container, where the CLIs, builds and tests run. Size the memory for your build and tests plus about 1Gi for the CLI. Requests as high as the limits avoid kills when the node runs short. | – |
| `maxMemory` | Cap for raising the memory limit after an out-of-memory kill: each kill raises the task's limit by half and starts the run again, at most 3 times per run. Set it to the limit to turn raising off. Without a memory limit nothing is raised. | twice `resources.limits.memory` |
| `runnerResources` | Resources of the runner container (GitLab, git). | requests 50m CPU and 128Mi, limit 1Gi |
| `workspaceSize` | Size of each task's workspace volume. | `10Gi` |
| `storageClassName` | Storage class of the workspace. | cluster default |
| `cacheClaimName` | An existing volume claim, ideally ReadWriteMany, mounted at `/cache` for shared Go, npm, pip and yarn caches. | – |
| `env`, `nodeSelector`, `tolerations` | Passed to the pod. | – |
| `logLevel` | `debug`, `info` or `error` for the pod's logs. `info` logs every step, `debug` adds git commands. A pod picks it up when it next starts. | the chart's `agent.logLevel`, else `info` |
| `logPrompts` | Log every prompt (cut at 64 KiB) for the Desk's [live transcript](../ui.md#live-transcript). Prompts hold issue text, comments, diffs and CI logs. | `false` |
| `chat` | Turn on Desk chat for this project's tasks. Needs the research database. | `false` |
| `models` | The models an issue may switch to, see [Per-issue model and effort](#per-issue-model-and-effort). | opus, sonnet and haiku |
| `maxEffort` | The highest effort an issue may ask for; higher values are lowered. A role's own `effort` is not capped. | `high` |
| `fallbackAtPercent` | 1 to 100: switch a role to its [fallback](#fallback-engine) once a usage window of its engine is this full, e.g. `90`. | – |

When `resources` sets a memory limit or CPUs, the implementer is told them
and asked to stay within them: few test workers, one heavy command at a
time, no watch modes. Build tools size themselves by the node, not the
container, and a container over its memory limit is killed with the run.

The roles, each a block under `spec.agents`:

| Field | Role | Default |
|-------|------|---------|
| `spec` | Plans a milestone into issues. | claude, `claude-opus-5-5` |
| `implement` | Works on issues without a profile label. | claude, `claude-opus-5-5` |
| `profiles` | Implementers an issue picks with a label, see [Profiles](#profiles). | `fast` and `strong` |
| `review` | Reviews each change before it is merged. | claude, `claude-sonnet-5-5` |
| `describe` | Writes the merge request description after each push that changed the diff. `disabled: true` keeps the placeholder. | claude, `claude-sonnet-5-5` |
| `pilot` | The milestone's [pilot](../concepts/glossary.md#pilot). Read-only, one session per milestone. | claude, `claude-sonnet-5-5` |

## Roles

Every role and profile has these fields:

| Field | Engines | Meaning | Default |
|-------|---------|---------|---------|
| `engine` | – | `claude` or `cursor`. | `claude` |
| `model` | both | e.g. `claude-opus-5-5`, or `auto` on cursor. | see above; cursor picks its own |
| `effort` | claude | `low`, `medium`, `high`, `xhigh` or `max`. | the engine's |
| `maxBudgetUSD` | claude | Spend cap per call, e.g. `"5"`. | – |
| `allowedTools` | claude | Replaces the role's tool list. | the role's |
| `instructions` | both | Added to the role's prompt: house rules, test commands. | – |
| `fallback` | both | `{engine, model, effort}` to run on while the engine is at a usage limit, see [Fallback engine](#fallback-engine). | – |
| `subagents` | claude | Up to 8 helpers the role delegates to, see [Subagents](#subagents). | none |
| `readGate` | claude | `{minLines}`: block whole reads of big files, see [Read gate](#read-gate). | no gate |

`review` also has:

| Field | Meaning | Default |
|-------|---------|---------|
| `reviewers` | Which reviewers run: `bugbot`, `security`. | both |
| `maxIterations` | Review and fix rounds per push. | `2` |
| `retries` | How often a reviewer that failed for a passing reason (an engine outage) is called again. `0` turns it off. | `2` |
| `retryAfter` | How long [autopilot](../usage.md#when-it-pauses) waits before it asks for one more review of a head whose reviewers all failed that way. | `15m` |
| `reviewerSettings` | Per reviewer, by `name`: `maxInlineDiff`, the largest diff in characters pasted into its prompt. A larger diff is only referenced and read through git. `0` never pastes it. | `150000` |

A lower `maxInlineDiff` saves input tokens on big changes, but the reviewer
then reads files itself and may look less closely. To tune it, compare the
*Review rounds* in the Desk's [Spend tab](../ui.md#spend): if a lower cap
raises the cost per call or brings more rounds, go back up.

`implement` and every profile also have:

| Field | Meaning | Default |
|-------|---------|---------|
| `stepSession` | How a run that works in steps goes on after each step: `resume` continues the same session, `fresh` starts a new one. Set on `implement`, it applies to the profiles too. | `resume` |
| `maxStepLines` | The step-size gate: once the uncommitted change passes this many lines, the agent is told to finish the step. At least 1. A profile without one takes `implement`'s. | off |

With `resume`, the agent remembers everything it read and did, but each
step carries the whole session, so steps get more expensive as the run
goes on. With `fresh`, each step after the first gets the issue, the
summaries of the steps before, the agent's notes and new comments. It costs
about as much as the first step, but forgets what was not in the notes or
the code. The *Implementer steps: resumed vs fresh* table in the Spend tab
compares the two.

The step-size gate is claude only; a cursor role just gets the sentence
"Keep a step under N changed lines where you can." Smaller steps mean more
commits, and a cut-off call loses less (see
[`spec.checkpoint`](lifecycle-and-ci.md#speccheckpoint)).

### Conflict role

`implement.conflict` is the role that resolves the conflicts of a
[conflict run](../usage.md#merge-conflicts), for every profile. Unset, the
implementer resolves them in its own session. Set, it handles a conflict
run that asks nothing else of the implementer (no new comments, CI fix or
revisit).

It has the [role fields](#roles). Each field you set replaces the issue's
implementer's; an unset field is the implementer's. The exception is
`engine`: it defaults to `claude`, so a conflict role on an implementer
that runs on cursor moves the conflict call to claude unless you set
`engine: cursor`. A conflict role on another engine takes neither the
implementer's model and effort nor a fallback on its own engine. It also
has:

| Field | Meaning | Default |
|-------|---------|---------|
| `freshSession` | Start a new session from the issue, the agent's notes and the conflicting files, and keep the implementer's session for later. `false` resumes the implementer's session on the conflict role's model, if the engine is the same. | `true` |

A fresh call on a lighter model is cheaper for a mostly mechanical merge.
With a companion repository only a claude conflict role is used.

## Subagents

A role's `subagents` are helpers the agent hands work to, so it keeps its
own context small. They are claude only, and the default is none: claude
then uses its built-in helpers as it likes. Each one has:

| Field | Meaning |
|-------|---------|
| `name` | Lowercase letters, digits and `-`, starting with a letter, at most 40 characters. |
| `description` | When to use it. Required. |
| `model` | Its model. Empty takes the role's. |
| `tools` | From `Read`, `Grep`, `Glob`, `WebFetch`, `WebSearch`, `Bash`, `Edit`, `Write`, `NotebookEdit`, `TodoWrite`. Empty is claude's default. |
| `prompt` | Its instructions. Required. |

A subagent whose `tools` list `Write` or `Edit` is a
[writer](../concepts/glossary.md#writer); every other one is a reader, like
the [scout](../concepts/glossary.md#scout) in the example. The role is told
to delegate searches, lookups and log reading to the readers. With a
writer, it is also told to give it boilerplate (tests, fixtures, configs)
with a short spec and one reference file, and to review the result. A
writer answers with the paths it wrote only, so its code never fills the
role's context. Give a writer only work that follows an existing file.

On cursor, a role runs without its subagents, and the run log says so.

To see whether they pay off, compare runs in the Spend tab: subagent models
show up as their own, and the *With vs without subagents* table gives the
cost per call of the calls in which a subagent ran against those without.
A lower cost *with* means delegation pays.

## Read gate

Models often read big files whole anyway, even when told to delegate. A
role's [read gate](../concepts/glossary.md#read-gate) makes it a rule:

- Reading a file of more than `minLines` lines whole is denied, unless the
  call reads a section. Images, PDFs and notebooks go through.
- Shell commands that dump such a file (`cat`, `head`, `tail`, `less`,
  `more`) are denied, unless they read at most `minLines` lines.
- Subagents are exempt: reading big files is the scout's job.

A denied call tells the agent to ask a subagent or read a section, and the
agent goes on. The gate fails open: an error lets the call through, so it
never fails a run.

| Field | Meaning | Default |
|-------|---------|---------|
| `minLines` | Files over this many lines are gated, at least 100. | `350` |

`readGate: {}` turns it on with the default; unset, there is no gate. Like
subagents it is claude only: on cursor the role runs without it.

## Per-issue model and effort

On top of its profile, an issue can pick the implementer's engine, model and
effort with labels, set by `/agent use` and `/agent effort` or by hand (see
[Model and effort](commands.md#model-and-effort)). Only the models in
`agents.models`, plus those of the profiles and `implement`, are allowed:

```yaml
agents:
  models:
    - {engine: claude, model: claude-opus-5-5, alias: opus}
    - {engine: claude, model: claude-sonnet-5-5, alias: sonnet}
    - {engine: cursor, model: auto}
  maxEffort: high
```

Each entry has `engine`, `model` and an optional `alias`. Unset, the list
is `claude-opus-5-5` (`opus`), `claude-sonnet-5-5` (`sonnet`) and
`claude-haiku-5-5` (`haiku`), all on claude. An empty list `[]` allows only
the models of the profiles and `implement`.

The planner sets an `agent-effort::` label on an issue when it thinks the
issue needs more or less thinking than its profile. A label you set is kept
when the milestone is planned again.

## Profiles

```yaml
profiles:
  - name: fast
    description: Small, well-defined changes that follow existing patterns.
    engine: cursor
    model: auto
  - name: strong
    description: Design decisions, cross-cutting or risky changes, unclear scope.
    engine: claude
    model: claude-opus-5-5
```

A profile has `name`, `description`, the [role fields](#roles) and the
implementer's `maxStepLines`. The planner sees the names and descriptions
and labels each issue it creates `agent-profile::<name>`, so write
descriptions that let it decide. An issue with a missing or unknown label
runs on `implement`. A label you set by hand is never replaced when the
milestone is planned again.

Without profiles, `fast` (claude, `claude-sonnet-5-5`) and `strong` (claude,
`claude-opus-5-5`) are used.

## Fallback engine

A role or profile can name a fallback: another engine and model to run on
while its own engine is at a usage limit, for example when cursor's monthly
budget is used up.

```yaml
profiles:
  - name: fast
    engine: cursor
    model: auto
    fallback: {engine: claude, model: sonnet}
review:
  engine: cursor
  model: auto
  fallback: {engine: claude, model: claude-sonnet-5-5, effort: medium}
```

When the role's engine reports a limit and the fallback's engine is not
limited, the same request runs on the fallback at once, and the run report
says so. The fallback starts a new session with the full context. When the
fallback's engine is limited too, the run waits for the reset.

With `agents.fallbackAtPercent`, a role switches already when a usage
window of its engine is that full, which keeps headroom for the roles
without a fallback. Only claude reports its windows. The threshold never
makes a run wait.

The fallback must be on another engine and name an allowed model, by name
or alias. `effort` is claude only. The role's other fields stay. Model and
effort labels on an issue change the implementer, not its fallback. The
condition `FallbacksValid` on the AgentProject names the roles whose
fallback is broken, so you see a mistake before a limit needs it.

An issue with a [companion repository](../concepts/glossary.md#companion-repository)
needs a claude implementer. When the implementer is on cursor, it runs on
its fallback if that is claude, else on `implement` if that is claude, and
the run report says so. Without either, the run fails before any agent
call. A companion run never falls back to cursor: at a claude limit it
waits.

## Engines

| | `claude` | `cursor` |
|-|----------|----------|
| Structured answers | built in | from the prompt |
| Tool restrictions | yes | none; read-only roles get their changes reset |
| Resumes sessions | yes | yes |
| Reports cost | yes (notional with a subscription) | no |
| Live transcript | thinking, messages, tool calls | messages, tool calls |
| Subagents, read gate, step-size gate | yes | no |
| Effort | yes | no |

Sessions are kept per role and engine. Switching an issue to a profile on
another engine starts a new session with the full context.

## Next

- [AgentProject](agentproject.md): the other field groups.
- [Agent image](agent-image.md): what the agents run in.
- [Roles](../concepts/roles.md): what each role does.
