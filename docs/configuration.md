# Configuration reference

An `AgentProject` connects one GitLab project. A complete example is in
[`config/samples/agentproject.yaml`](../config/samples/agentproject.yaml).
`kubectl explain agentproject.spec --recursive` shows the full schema.

## Installation

With Helm (recommended, see the [chart values](../charts/ai-operator/README.md)):

```sh
helm install ai-operator oci://ghcr.io/jonathanwiemers/charts/ai-operator \
  --version <YEAR.MONTH.N> \
  --namespace ai-operator-system --create-namespace \
  --set 'agent.namespaces={agents}' --set agent.createNamespaces=true
```

With Flux, see [Install with Flux](#install-with-flux) below.

The chart also serves a receiver for GitLab project webhooks
(`webhook.enabled`, port `8082`, Service `<release>-webhook`); it does
nothing until an AgentProject sets [`spec.webhook`](#specwebhook).

Without Helm:

```sh
make deploy                          # CRDs, RBAC, operator in ai-operator-system
kubectl create namespace agents
kubectl apply -f config/samples/agent_serviceaccount.yaml   # service account for agent pods
```

Agent pods run in the namespace of their `AgentProject`. Every such namespace
needs the `ai-operator-agent` service account, bound to the agent ClusterRole
(read tasks and projects, patch `agenttasks/status`). The chart creates both
for every namespace in `agent.namespaces`.

### Install with Flux

The chart is an OCI artifact, so Flux can install it with an `OCIRepository`
and a `HelmRelease`:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: ai-operator-system
---
apiVersion: source.toolkit.fluxcd.io/v1        # v1beta2 on Flux < 2.6
kind: OCIRepository
metadata:
  name: ai-operator
  namespace: ai-operator-system
spec:
  interval: 1h
  url: oci://ghcr.io/jonathanwiemers/charts/ai-operator
  ref:
    tag: <YEAR.MONTH.N>        # or semver: ">=2026.10.3" to follow new releases
  layerSelector:
    mediaType: application/vnd.cncf.helm.chart.content.v1.tar+gzip
    operation: copy
---
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: ai-operator
  namespace: ai-operator-system
spec:
  interval: 30m
  chartRef:
    kind: OCIRepository
    name: ai-operator
  values:
    agent:
      namespaces: [agents]
      createNamespaces: true
```

* The CRDs are regular chart templates, so they are installed and upgraded
  with the release. They are kept when the release is removed.
* Put your `AgentProject` and its secrets (e.g. SOPS-encrypted) into a second
  Flux `Kustomization` with `dependsOn` on the one above and `wait: true` on
  the operator's, so the CRDs exist before the project is applied.
* Keep `AgentTask`s out of Git. The operator creates and deletes them.
* Change `AgentProject` settings such as `spec.suspend` in Git; Flux reverts
  edits made with `kubectl`.

### Web UI

The chart can also install a read-only web UI with OIDC login and role-based
access per project (`ui.enabled`, off by default), exposed through a Gateway
API HTTPRoute. See [ui.md](ui.md).

With a [research database](#research-database) the UI connects to it as the
reader: the chart puts `research.database.host`, `port`, `name` and
`sslMode` (plus `ui.research.namespace` and `allowCreate`) into the UI's
config file and the login from `research.database.readerSecret` into the
environment variables `AI_OPERATOR_UI_RESEARCH_DB_USER` and
`AI_OPERATOR_UI_RESEARCH_DB_PASSWORD`; it refuses to render without
`readerSecret.name`. The UI's ClusterRole then also gets `get`/`list`/`watch`
on `agentresearches`. The UI only reads (the operator migrates) and starts
without the database: it connects in the background and retries with backoff
(5 s up to 5 min), the research endpoints answer 503 until then, and the
factory pages work as before. `sslMode: disable` is refused unless the host
is a loopback address (local development). Who sees the Research section is
set by [research rules](ui.md#research-rules). With `ui.research.allowCreate`
(which needs `ui.research.namespace`; the chart refuses to render without
it) the UI also gets a Role in that namespace with `create`, `delete` and
`get` on `agentresearches`, for [one-off researches](ui.md#one-off-researches).

### Research

`AgentResearch` resources describe research questions that agents answer
once or on a schedule, with the results kept in Postgres. An AgentProject
gives their runs a database login with `spec.research`. Concepts, the CRD,
the agent choice, the block schema, examples and troubleshooting are in
[research.md](research.md); the AgentProject fields in
[`spec.research`](research.md#specresearch-on-agentproject).

#### Research database

Research is on exactly when `research.database.host` is set; without it the
operator opens no connection and starts as before. The database is configured
the way the Zalando postgres-operator provides it: a host and one Secret per
login with the keys `username` and `password`.

| Value | Default | Description |
|-------|---------|-------------|
| `research.database.host` | `""` | Postgres host; empty turns research off |
| `research.database.port` | `5432` | |
| `research.database.name` | `""` | database, required with a host |
| `research.database.sslMode` | `require` | `disable`, `require` (encrypted, not verified), `verify-ca` or `verify-full` |
| `research.database.ownerSecret` | `{name: "", usernameKey: username, passwordKey: password}` | owner login of the operator, a Secret in the release's namespace; `name` is required with a host |
| `research.database.readerSecret` | `{name: "", …}` | read-only login of the UI, a Secret in the release's namespace |
| `research.database.readerRole` | `""` | role granted read access (the reader login) |
| `research.database.writerRoles` | `[]` | roles granted write access (the writer logins of research runs) |
| `research.database.chatWriterRole` | `""` | role granted chat insert access (the UI's chat login from `ui.chat.writerSecret`) |
| `ui.chat.writerSecret` | `{name: "", usernameKey: username, passwordKey: password}` | Desk chat writer login of the UI, a Secret in the release's namespace; empty `name` turns chat off. Passed as `AI_OPERATOR_UI_CHAT_DB_USER`/`AI_OPERATOR_UI_CHAT_DB_PASSWORD`; needs `research.database.host` |
| `research.database.networkPolicyPeer` | `{namespace: "", podLabels: {}}` | the database pods research-run pods may reach, see [below](#network-policy-for-research-runs) |
| `research.candidates.quick` | `claude/claude-sonnet-5-5`, `cursor/auto` | default agents of quick research runs, in order, see [research.md](research.md#choosing-agents) |
| `research.candidates.deep` | `claude/claude-opus-5-5` (effort `high`), `claude/claude-sonnet-5-5`, `cursor/auto` | the same for deep runs |
| `research.maxUsagePercent` | `70` | research uses an engine only while every usage window not reset since is below this (1 to 100) |
| `research.historyRetention` | `8760h` | how long the factory's history (the Desk's streams, see [ui.md](ui.md#desk-and-history)), the agent calls (`agent_calls`, see [design.md](design.md#history)), Desk chat messages and the Desk's milestone snapshot ([design.md](design.md#desk-snapshot)) are kept; `--history-retention` |
| `ui.research.allowCreate` | `false` | one-off researches from the UI, see [Web UI](#web-ui) |
| `ui.research.namespace` | `""` | where the UI creates one-off researches; required with `allowCreate` |

The logins on my-cluster (the complete values, with the AgentProject's writer
login, are in [research.md](research.md#example-my-cluster)):

| Login (role) | Secret | Namespace | Used by |
|---|---|---|---|
| `research_owner` (owns `ai_operator`) | `research-owner.ai-operator-postgres.credentials.postgresql.acid.zalan.do` | `ai-operator-system` | operator |
| `research_reader` | `research-reader.ai-operator-postgres.credentials.postgresql.acid.zalan.do` | `ai-operator-system` | UI |
| `agents.research_writer` (Zalando prefixes cross-namespace roles with the namespace) | `agents.research-writer.ai-operator-postgres.credentials.postgresql.acid.zalan.do` | `agents` | runner of research runs |

```yaml
research:
  database:
    host: ai-operator-postgres.ai-operator-system.svc.cluster.local
    name: ai_operator
    ownerSecret: {name: research-owner.ai-operator-postgres.credentials.postgresql.acid.zalan.do}
    readerSecret: {name: research-reader.ai-operator-postgres.credentials.postgresql.acid.zalan.do}
    readerRole: research_reader
    writerRoles: [agents.research_writer]
    networkPolicyPeer:
      namespace: ai-operator-system
      podLabels: {cluster-name: ai-operator-postgres}
```

The operator gets the host, port, name and `sslMode` as flags
(`--research-db-host`, `--research-db-port`, `--research-db-name`,
`--research-db-sslmode`, `--research-reader-role`, `--research-writer-roles`,
`--research-chat-writer-role`, `--research-candidates-quick` and
`--research-candidates-deep` as `engine/model[@effort]` lists,
`--research-max-usage-percent`; invalid values stop the operator at start)
and the owner login as the environment variables `AI_RESEARCH_DB_USER` and
`AI_RESEARCH_DB_PASSWORD` from `ownerSecret`, never as arguments. A host
without a name, user or password stops the operator at start; the chart
refuses to render a host without `name` or `ownerSecret.name`.

At every start the leader connects as the owner, applies the embedded
migrations and grants the roles: `readerRole` may read every table,
each of `writerRoles` may read, insert and update runs, results and items,
`chatWriterRole` may select and insert user rows on `chat_messages`, and
default privileges extend the reader (and not the writers) to later tables.
Role names are quoted, so `agents.research_writer` is one role. Grants are
additive: a role removed from the values keeps its rights until revoked by
hand (see [research.md](research.md#database)). If the database is
unreachable or a migration fails, the operator keeps running the factory and
retries with backoff (5 s up to 5 min). Once ready, it deletes expired runs
with their results and items at once and then every hour, and with
`--history-retention` also expired history events, agent calls, chat
messages and milestone snapshots.

With the database ready, the operator also fills the Desk's milestone
snapshot once from GitLab with the closed milestones it planned before the
snapshot existed (the backfill, see
[design.md](design.md#desk-snapshot)): one milestone per poll, at most 4
GitLab requests a step and 16 a minute. It has no setting: it stops by
itself once every closed milestone is in, and lists them again once a day.

The runner of a research run logs in as a writer: name its Secret per
project in `spec.research.writerSecret` (see
[research.md](research.md#specresearch-on-agentproject)).

#### Network policy for research runs

The agent NetworkPolicy (`agent.networkPolicy`) blocks the cluster CIDRs, so
research-run pods could not reach an in-cluster database. With
`agent.networkPolicy.enabled`, `research.database.host` and
`research.database.networkPolicyPeer.namespace` set, the chart adds a second
NetworkPolicy `<agent role>-research-db` in every agent namespace. It selects
only pods with the label `ai.wiemers.me/research` and allows egress on TCP
`research.database.port` to the pods matching `networkPolicyPeer.podLabels`
in `networkPolicyPeer.namespace` (all its pods without labels).
NetworkPolicies are additive: task pods are not selected and keep the agent
policy as it is, without access to the database.

### Pricing

Every agent call is a row of `agent_calls` (see
[design.md](design.md#history), needs the research database) with a cost
and its source:

- **claude** reports `total_cost_usd` (the notional API price on a
  subscription): `cost_source = reported`. A `pricing` entry for the model
  does not change it.
- **cursor** reports a USD cost only when an ACP `usage_update` carries
  one (`cost_source = reported`, the increase of the session's running
  total during the call; other currencies are ignored). The cursor-agent
  in the agent image sends none yet. Without one, when it reports tokens
  (ACP's prompt response) and `pricing` has a price for the model, the
  operator estimates the cost from them when it writes the row:
  `cost_source = estimated`.
- Otherwise (no tokens, or no price) the cost is NULL and
  `cost_source = unknown`: the call counts with its duration, and the Desk
  shows "n/a", never $0. A call with neither a cost nor tokens (cursor
  with model `auto`: ACP reports no usage) is counted as *not metered*
  besides that (`unmeteredCalls`); it does not change whether the cost is
  known. Calls recorded before the token basis was stored
  (`token_basis` 0) are counted as `oldBasisCalls` next to `calls`: their
  tokens are not comparable with newer calls'.

The chart value `pricing` holds the prices in USD per million tokens:

```yaml
pricing:
  models:
    "cursor/auto": {input: 1.25, output: 10, cacheRead: 0.125, cacheWrite: 1.25}
    "gpt-5": {input: 2, output: 20}
    "cursor/*": {input: 3, output: 15, cacheRead: 0.3, cacheWrite: 3.75}
```

A key is `<engine>/<model>`, a bare model, or `<engine>/*` (the fallback
for every model of the engine, such as cursor's `auto`); the operator looks
them up in that order. Each price has `input`, `output`, `cacheRead` and
`cacheWrite` (missing ones are 0). Estimates are rounded to 4 decimals like
reported costs. The default `{}` estimates nothing. A non-empty value is
rendered into the ConfigMap `<release>-pricing`, mounted into the operator
and passed as `--pricing-file`; the pod restarts when the prices change.
The file is parsed strictly: unknown keys, negative prices or an unreadable
file stop the operator at start. Prices apply to calls written from then on;
stored rows keep their cost.

## Secrets

```sh
kubectl -n agents create secret generic gitlab --from-literal=token=glpat-…

kubectl -n agents create secret generic agent-credentials \
  --from-literal=CLAUDE_CODE_OAUTH_TOKEN="…" \
  --from-literal=CURSOR_API_KEY="…"
```

* `spec.gitlab.tokenSecretRef` is the GitLab project access token (Developer,
  `api` + `write_repository`). The operator and the runner use it. The model
  never sees it.
* `spec.agents.credentialsSecretName` names a secret whose keys become
  environment variables of the agent pods. Add whatever your engines need:

| Key | Used by | How to get it |
|-----|---------|---------------|
| `CLAUDE_CODE_OAUTH_TOKEN` | `claude` engine, billed to your Claude Pro/Max subscription | run `claude setup-token` |
| `ANTHROPIC_API_KEY` | `claude` engine, billed to the Claude API | Claude Console |
| `CURSOR_API_KEY` | `cursor` engine, billed to your Cursor subscription | Cursor dashboard |

## `spec.gitlab`

| Field | Default | Description |
|-------|---------|-------------|
| `url` | `https://gitlab.com` | GitLab instance |
| `project` | – | full path, e.g. `my-group/shop` |
| `tokenSecretRef` | – | `{name, key}` of the access token |

## `spec.milestones`

| Field | Default | Description |
|-------|---------|-------------|
| `specMarker` | `/agent spec` | text that must appear in a milestone description for it to be planned; `""` plans every active milestone with a description |
| `autopilotMarker` | `/agent autopilot` | text that plans a milestone and runs it end to end: agents take their defaults, issues are worked one at a time and each MR is merged when CI is green and the review is clean (see [Autopilot](usage.md#autopilot-a-milestone-end-to-end)); `""` disables autopilot. The bot must be allowed to merge into the default branch |
| `titlePattern` | – | optional regex for milestone titles; if set, issues are also only picked up from matching milestones |

Neither marker has a setting for *when* planning happens. A milestone with the
`specMarker` only (no autopilot) is planned when it is created, and an owner
reviews the issues. An autopilot milestone is planned when its turn comes,
that is when no older autopilot milestone of the project is still open (or
when an owner comments `/agent start`); until then it only has a tracking
issue. See [Autopilot](usage.md#autopilot-a-milestone-end-to-end).

## `spec.workflow`

All labels can be renamed. Defaults:

| Field | Default |
|-------|---------|
| `draftLabel` | `agent::draft` |
| `readyLabel` | `agent::ready` |
| `workingLabel` | `agent::working` |
| `needsInputLabel` | `agent::needs-input` |
| `reviewLabel` | `agent::review` |
| `doneLabel` | `agent::done` |
| `blockedLabel` | `agent::blocked` |
| `blockedReasonPrefix` | `agent-blocked::` (reasons: `infra`, `main-red`, `quota`, `dependency`) |
| `generatedLabel` | `ai-generated` |
| `profileLabelPrefix` | `agent-profile::` |
| `modelLabelPrefix` | `agent-model::` |
| `effortLabelPrefix` | `agent-effort::` |
| `commandPrefix` | `/agent` |
| `pilotPrefix` | `/pilot` |
| `commandsFrom` | `members` (or `owners`) |
| `inputs` | unset = `[gitlab, desk]` |
| `assignBot` | `true` |
| `resolveConflicts` | `true` |
| `stepCommits` | `true` |
| `conventionalCommits` | `false` (see [usage](usage.md#commit-messages)) |
| `commitTypes` | `feat, fix, refactor, perf, test, docs, build, ci, chore, style, revert` |
| `release.mode` | `Auto` (or `Off`; empty means `Auto`), see [below](#specworkflowrelease) |
| `release.minInterval` | `15m` |
| `release.maxUnreleasedAge` | `24h` |

### `spec.workflow.release`

Releasing by decision (see [usage](usage.md#releases), which later changes of
this milestone fill in).

- `mode`: `Auto` (default, also when empty) or `Off`. Any other value is
  rejected. `Off` means the operator never starts a release pipeline.
- `minInterval`: the minimum time between two releases the operator starts
  (default `15m`); the final release of a milestone is exempt.
- `maxUnreleasedAge`: outside milestones, with no autopilot running or
  paused, the operator releases the default branch once its oldest
  release-relevant commit since the newest calendar-version tag landed more
  than this ago and main is green (default `24h`), see
  [usage](usage.md#outside-milestones).

The checkpoint fields (`checkpoint.disabled`, default `false`, and
`checkpoint.maxFileBytes`, default 5 MiB) are in
[`spec.checkpoint`](#speccheckpoint); usage is in [When a run is cut
off](usage.md#when-a-run-is-cut-off).

`commandsFrom` is who may give `/agent` commands: `members` (anyone who can
comment) or `owners`. With `owners`, an `/agent …` comment on an issue, its
MR, the tracking issue or a closed issue (revisits) counts only from the
milestone's owners (`/agent owners @…`, else `notify.users`, else the
operator's default owners; for an issue without a milestone `notify.users`,
else the default owners), checked by GitLab user ID. Anyone else's command
starts and changes nothing; each gets one reply in its thread, up to 10 a
day per author.

`inputs` lists where instructions may be given: `gitlab` (comment commands)
and `desk`. At least one entry; unset means both. With `gitlab`, owners may
also answer a prompt with its option's letter as a reply in the thread of its
comment, on an issue, the tracking issue or a merge request
([commands](commands.md)). With only `desk`:

- `/agent` and `/pilot` comments on issues, merge requests, the tracking issue
  and closed issues (revisits) start and change nothing. Each gets one reply
  (once per comment, up to 10 a day per author): "This project takes
  instructions in the Desk", with a link to the issue's open prompt, else the
  task's Desk page, else the milestone's.
- Prompt threads (option letters, `yes`/`no` to proposals) are not read.
- The implementer does not take `/agent` and `/pilot` comments as
  instructions, and run reports say what to do in the Desk instead of which
  comment to write.
- The implementer does not read the issue's comment thread: a resumed session
  already has the conversation and gets only new human comments on the merge
  request (code review). A fresh session (archived workspace, engine switch)
  gets the issue description, the agent's own comments and notes, the merge
  request's comments, and the task's last 20 answered Desk chat messages
  (yours and the agent's, at most 30 KiB, oldest dropped first). The operator
  copies them into the run request (`spec.source.chatContext`). Without the
  research database there is no chat to pass, so the list stays empty.

Labels, milestone planning and human MR review threads stay on GitLab either
way.

The links in these replies, and in the prompts' comments, are the Desk's
address: the operator's `--desk-url` flag (the chart's `ui.externalURL`; agent
pods get `AI_DESK_URL`). Without it prompts say "Answer in the Desk." and
replies carry no link. What `inputs: [desk]` does not change: labels
(`agent::ready`), milestone planning and human review threads on merge
requests stay on GitLab, and the operator still mirrors every prompt and
answer there.

`assignBot` assigns the bot user to an issue when the operator picks it up and
the issue has no assignee, so GitLab's milestone page lists it under
**Ongoing**. It is sent in the same update that adds the working label. An
issue that already has an assignee is left alone, so a human assignee is never
replaced. Set it to `false` to leave assignees untouched.

`resolveConflicts` lets the agent resolve merge conflicts: when the MR of an
issue in review conflicts with the default branch, the next run merges the
default branch into the work branch and the agent resolves the conflicts (see
[usage.md](usage.md#merge-conflicts)). On autopilot it is also used when GitLab
cannot rebase an MR. Set it to `false` to leave conflicts to humans; autopilot
then pauses on them.

`stepCommits`: while the MR is a draft, the implementer works in steps. Each
step is committed and pushed as it is done (without CI), and comments written
in the meantime reach the agent before its next step, so you can follow the
work on the MR and steer it (see [usage.md](usage.md#3-the-agent-works)). Set
it to `false` for one commit per run.

`conventionalCommits`: with `true`, commit messages and MR titles follow
Conventional Commits (`type(scope): subject`, subject up to 72 characters).
`false` keeps today's messages and titles. `commitTypes` lists the allowed
types (1 to 20 entries, each matching `^[a-z][a-z0-9]*$`). `chore` is always
allowed because the runner's own messages use it: a list without `chore` gets
it added. With `true`:

- the implementer's answer schema gets a `pattern` on `commitMessage`
  (`^(<types>)(\([a-z0-9._/-]+\))?!?: .{1,72}(\n\n[\s\S]*)?$`), and its
  prompts say how to pick the type and the scope;
- the runner checks every message it commits for the agent. One that does
  not match becomes `chore(agent): <first line, lowercased>` (the body and
  `Refs #N` stay); it is logged and counted in the run report;
- a new branch starts with `chore: start work on #N`, and a conflict run's
  merge commit is `chore: merge <base> into <branch>`;
- the describe agent also returns the MR `title` (same `pattern`, one line,
  no `Draft:` prefix), and the runner sets it, with `Draft: ` while the MR is
  a draft, so a squash merge lands a conventional commit. A title renamed by
  hand, or a describer title that does not match, leaves the title as it is;
- the autopilot's merge sends a `squash_commit_message` (the MR title, or its
  `chore(agent): …` fallback, then `Refs #N`), so the default branch gets a
  conventional commit even if the title is not. Ignored by GitLab when the
  project does not squash.

See [usage.md](usage.md#commit-messages).

## `spec.agents`

| Field | Default | Description |
|-------|---------|-------------|
| `image` | operator's `--default-agent-image` (with Helm: the agent image of the same release) | agent image (see below) |
| `credentialsSecretName` | – | see Secrets |
| `serviceAccountName` | `ai-operator-agent` | |
| `resources` | – | resources of the agent container (agent CLIs, builds, tests). Size the memory for the project's build and tests plus the CLI (about 1Gi); a container over its limit is killed (`OOMKilled`); its limit is then raised up to `maxMemory` and the run starts again. Requests as high as the limits avoid kills when the node runs short |
| `maxMemory` | twice `resources.limits.memory` | cap for raising the agent container's memory limit after an OOM kill: each kill raises that task's limit by half and the run starts again (at most 3 times per run). Set it to the limit to turn raising off; without a memory limit nothing is raised |
| `runnerResources` | 50m / 128Mi requests, 1Gi limit | resources of the runner container (GitLab, git mirror) |
| `workspaceSize` | `10Gi` | per-task PVC |
| `storageClassName` | cluster default | |
| `cacheClaimName` | – | existing (ideally RWX) PVC mounted at `/cache` for shared dependency caches (`GOMODCACHE`, `GOCACHE`, `npm_config_cache`, `PIP_CACHE_DIR`, `YARN_CACHE_FOLDER`) |
| `env`, `nodeSelector`, `tolerations` | – | passed to the pod |
| `logLevel` | operator's `--agent-log-level` (with Helm: `agent.logLevel`, falling back to `logLevel`) | `debug`, `info` or `error` for the runner and agent containers; `info` logs every step, `debug` adds git commands. A running pod picks up a change when it is next started |
| `spec` | claude, `claude-opus-5-5` | spec (planning) agent |
| `profiles` | `fast`, `strong` | implementing agents selected per issue |
| `implement` | claude, `claude-opus-5-5` | implementing agent for issues without a profile label |
| `review` | claude, `claude-sonnet-5-5`, reviewers `bugbot` + `security`, `maxIterations: 2`, `retries: 2`, `retryAfter: 15m` | review agents |
| `describe` | claude, `claude-sonnet-5-5` | writes the MR description after each push that changed the diff; `disabled: true` keeps the placeholder |
| `pilot` | claude, `claude-sonnet-5-5` | the milestone's [pilot](usage.md#talking-to-the-pilot), who answers its owners; read-only, one resumed session per milestone |
| `models` | `claude-opus-5-5`, `claude-sonnet-5-5`, `claude-haiku-5-5` (aliases `opus`, `sonnet`, `haiku`) | engines and models an issue may switch its implementer to with `/agent use` or an `agent-model::` label; the models of the profiles and of `implement` are always allowed. Entries: `engine`, `model`, optional `alias` |
| `logPrompts` | `false` | log every prompt (role, system prompt, prompt, each cut at 64 KiB) in the agent container, for the web UI's [live transcript](ui.md#live-transcript). Prompts hold issue text, comments, diffs and CI logs |
| `fallbackAtPercent` | – | switch a role with a [fallback](#fallback-engine) to it before its engine's hard limit, when a usage window of the engine (claude reports them) is at least this full, e.g. `90` |
| `maxEffort` | `high` | the highest effort an issue may ask for (`agent-effort::` label, `/agent effort`, the planner's choice); higher values are lowered. The roles' own `effort` is not capped |
| `chat` | `false` | turn Desk [live chat](design.md#desk-live-chat) on for this project's tasks; the operator hands `chat_messages` rows to the tasks. Needs the research database; without it (or with `false`) queued messages are marked failed |

If `resources` sets a memory limit or CPUs (limits, else requests), the
implementing agent is told them in its system prompt, with the advice to keep
builds and tests within them (few test workers, one heavy command at a time,
no watch modes or dev servers). Build tools and test runners size themselves
by the node, not the container, and a container over its memory limit is
killed together with the run.

### Roles

`spec`, `implement`, `review`, `describe`, `pilot` and every profile share these fields:

| Field | Engines | Description |
|-------|---------|-------------|
| `engine` | – | `claude` (default) or `cursor` |
| `model` | both | e.g. `claude-opus-5-5`, `claude-sonnet-5-5`; `auto` for cursor. Claude defaults: `claude-opus-5-5`, reviews `claude-sonnet-5-5`. Cursor uses its own default |
| `effort` | claude | `low` … `max` |
| `maxBudgetUSD` | claude | spend cap per invocation, e.g. `"5"` |
| `allowedTools` | claude | override the tool allow-list |
| `instructions` | both | appended to the role prompt (house rules, test commands) |
| `fallback` | both | `{engine, model, effort}` the role runs on while its own engine is at a usage limit, see [Fallback engine](#fallback-engine) |
| `subagents` | claude | up to 8 custom subagents `{name, description, model, tools, prompt}`; default none (claude keeps its built-in ones). `name` is lowercase `[a-z][a-z0-9-]`, at most 40 characters; `tools` from `Read`, `Grep`, `Glob`, `WebFetch`, `WebSearch`, `Bash`, `Edit`, `Write`, `NotebookEdit`, `TodoWrite` (empty: claude's default); `model` empty inherits the role's. See [Subagents](#subagents) |
| `readGate` | claude | `{minLines}`: blocks whole reads of files over `minLines` lines (default 350, minimum 100) and tells the role to ask a subagent or read a section; default no gate. See [Read gate](#read-gate) |

`review` additionally has `reviewers` (`bugbot`, `security`),
`maxIterations`, `retries` (calls again a reviewer that failed
transiently, e.g. an engine outage; `0` turns it off) and `retryAfter`
(how long [autopilot](usage.md#when-it-pauses) waits before it asks for
one re-review of a head whose reviewers all failed transiently) and
`reviewerSettings`, a list of per-reviewer settings keyed by `name`
(`bugbot`, `security`). Its `maxInlineDiff` is the largest diff in
characters that is pasted into that reviewer's prompt each round (default
`150000`); a larger diff is only referenced and the reviewer reads it
through git. A lower cap saves input tokens on big changes, but the
reviewer then spends turns reading files itself and may look less
thoroughly; `0` never inlines. Example:
`reviewerSettings: [{name: bugbot, maxInlineDiff: 50000}]`. `describe`
has `disabled`.

To tune the cap, look at *Review rounds* in the [Spend tab](ui.md#spend) of
a milestone with big changes: round 1 reviews the whole diff, later rounds
review it again after each fix. If the cost per call stays high in every
round, a lower `maxInlineDiff` for the costly reviewer saves input tokens on
each round; if a lower cap raises the cost per call instead (the reviewer
reads the files itself) or brings more rounds, go back up.

`implement` additionally has `stepSession` (`resume` or `fresh`, default
`resume`), which also applies to the profiles. It decides how a run that
works in steps (`workflow.stepCommits`, draft MR) goes on after each step:

* `resume` continues the same session. The agent remembers everything it
  read and did, but every step carries the whole session as context, so the
  cost per step grows with the run.
* `fresh` starts every step after the first in a new session. It gets the
  run's prompt (issue, thread, notes from earlier runs), the summaries of
  the steps before, the agent notes the last of them left and the comments
  handed over since. A step costs about the same as the first one, but what
  the session knew beyond the notes and the code is lost.

The first step, the review-fix rounds and the next run work as with
`resume`: fix rounds and the next run resume the last step's session. Compare
the two in the Spend tab's *Implementer steps: resumed vs fresh* table, which
splits the implement calls of the scope by `resumed`, with tokens and cost per
call. With `resume`, the *fresh* row is mostly first steps and the *resumed*
row the later ones, so a resumed cost per call well above the fresh one is the
growing context. With `fresh`, nearly every step lands in the *fresh* row:
compare its cost per call (and the runs' totals, which include the extra
review-fix rounds a forgetful step may cause) with a milestone on `resume`.

`implement` and every profile also have `maxStepLines` (minimum 1, unset
is off), the **step-size gate** of runs that work in steps. A profile's
value wins; a profile without one inherits `implement`'s, so
`implement: {maxStepLines: 400}` covers every profile. With it, a stepwise
implement call (draft MR, `workflow.stepCommits`; not the review-fix, CI or
conflict rounds, nor a run that does not work in steps) gets:

* a claude `PostToolUse` hook on `Edit`, `Write` and `Bash`
  (`ai-runner step-gate --max-lines N`, in the same `--settings` object as
  the [read gate](#read-gate)'s hooks). Once the uncommitted diff
  (`git diff --numstat HEAD` plus untracked text files, added plus deleted
  lines) passes `N`, it tells the agent: *Your uncommitted diff is L lines
  (step limit N). Finish this step: get it to build, then return
  in_progress with its commitMessage.* It repeats at most once every 5 tool
  calls. Like the read gate it fails open: an error means no nudge;
* one more sentence in the steps note: *Keep a step under N changed lines
  where you can.*

The gate is claude only: a cursor role (or a cursor fallback) runs without
the hook, with a warning in the run log once per run; it still gets the
sentence. Smaller steps mean more commits and pushes, and a cut-off call
loses less (see [checkpoints](#speccheckpoint)).

```yaml
agents:
  implement:
    maxStepLines: 400
  profiles:
    - name: fast
      maxStepLines: 200   # overrides implement's 400
```

#### Conflict role

`implement.conflict` is the role of the call that resolves the conflicts of
a [conflict run](usage.md#merge-conflicts), for the issues of every profile.
Unset, the implementer resolves them in its own session, as before. Set, the
conflict call of a run that asks nothing else of the implementer (no new
comments, CI fix or revisits) runs on it:

| Field | Description |
|-------|-------------|
| `engine`, `model`, `effort`, `maxBudgetUSD`, `allowedTools`, `instructions`, `fallback`, `subagents`, `readGate` | as for every [role](#roles). Each field set overrides the issue's implementer (profile or `implement`, with the [model and effort labels](#per-issue-model-and-effort)); an unset field is the implementer's. `engine` defaults to `claude`. A role on another engine than the implementer's does not take its model and effort, nor a fallback on its own engine |
| `freshSession` | default `true`: the call starts a new session with a self-contained prompt (the issue's title and description, the agent notes, the MR's diff stat and the conflicting files) and the implementer's session stays for its later calls (review fixes, the next run). `false` resumes the implementer's session on the conflict role's model, if the engine is the same |

A resumed conflict call reads the implementer's whole session again; after
a queue wait its prompt cache is cold, so the call pays a full cache write
on the implementer's model for a mostly mechanical merge. A fresh call on a
lighter model avoids both. The call is still of kind `conflict` in the
[Spend tab](ui.md#spend), cut off with [checkpoints](#speccheckpoint) like
the implementer's (a merge in progress is aborted, nothing is committed),
and goes to its `fallback` while its engine is at a usage limit. With a
[companion repository](#speccrossrepo) only a claude conflict role is used.

```yaml
agents:
  implement:
    conflict:
      model: claude-sonnet-5-5
      effort: medium
```

### Subagents

A role's `subagents` are passed to claude as `--agents` JSON, and the role's
system prompt gets one more line: *You have subagents (scout). Delegate
searches, lookups and log reading to them, and read only the files you edit
yourself.* The default is none: no `--agents`, no line, and claude uses its
built-in subagents at will, as before.

The line depends on the kind of subagent. A **writer** is a subagent whose
`tools` explicitly list `Write` or `Edit`; every other one is a **reader**,
including one with empty `tools` (claude's default) and a Bash-only one.
Readers get the sentence above; with writers present it names them instead
of "them" (*Delegate searches, lookups and log reading to scout, and …*).
Writers get another: *Give boilerplate (tests, fixtures, configs, stubs that
follow an existing file) to writer with a short spec and one reference file.
It writes the files and answers with their paths only. Review its changes
with git diff and fix what needs judgment.* Several writers are joined with
"or". A role whose subagents are all readers gets exactly the line above.
The [read gate](#read-gate) sentence, if any, comes last.

Subagents are claude only. A call that runs on cursor, because the role's
engine is cursor or because a claude role runs on its cursor
[fallback](#fallback-engine), goes without them; the runner logs *subagents
are claude only; ignored for role implement on cursor* once per run and
role. A cursor role whose claude fallback runs gets them.

A read-only scout on haiku and a writer on sonnet for the implementer:

```yaml
spec:
  agents:
    implement:
      subagents:
        - name: scout
          description: Searches the code, looks things up on the web and reads logs. Use it for every search and lookup.
          model: claude-haiku-5-5
          tools: [Read, Grep, Glob, WebFetch, WebSearch]
          prompt: You search and read for the implementing agent and never edit. Answer in bullets with exact names, paths and line numbers, no preamble. The caller checks line numbers before editing.
        - name: writer
          description: Writes boilerplate (tests, fixtures, configs, stubs that follow an existing file) from a short spec and one reference file.
          model: claude-sonnet-5-5
          tools: [Read, Grep, Glob, Write, Edit]
          prompt: You write boilerplate for the implementing agent. Read the reference file it names and follow its patterns (naming, structure, imports, style). Write the files with Write or Edit. Never answer with code; answer with the list of paths you wrote, one per line.
```

The scout answers in bullets with exact names, paths and line numbers and no
preamble; the implementer checks the line numbers before it edits.

The writer trades money for context: its calls cost something, but what it
writes never enters the implementer's context (it answers with paths only),
so the expensive model neither generates the boilerplate nor carries it
through the later steps. The implementer reviews the result with `git diff`
and fixes what needs judgment. Give a writer only work that follows an
existing file.

To see whether it pays off, compare runs with and without it in the
[Spend tab](ui.md): the scout's model shows up in the by-model breakdown
and the model bars (subagent models as their own), the calls count their
subagent calls, and on the context-growth chart an orange dot marks each
turn after which a subagent ran. Fewer main-model input and cache tokens
per step, and a flatter context line, mean the delegation works. The *With
vs without subagents* table puts it in numbers per role: the calls in which
a subagent ran against those without one, with tokens and cost per call and
the subagent models' share of the tokens. A call counts as *with* when a
subagent actually ran, not when one was configured, so claude's built-in
subagents count too. A lower cost per call *with* means delegation pays; a
high subagent share with no saving means the scout reads more than the role
would have.

### Read gate

The [subagents](#subagents) line is a hint, and models often ignore it: they read big files
whole into their own context anyway. A role's `readGate` turns it into a
rule. It is a `PreToolUse` hook (`ai-runner read-gate`, on the agent image)
that claude runs before every `Read` and `Bash` call of the role:

- **Read** of a file with more than `minLines` lines is denied, unless it
  sets `offset` or `limit` (a section) or `pages`. Images, PDFs and
  notebooks, missing paths and anything that is not a regular file go
  through.
- **Bash** commands that dump such a file whole are denied: `cat`, `head`,
  `tail`, `less` or `more` as the first word (after an optional `cd … &&`),
  unless `head`/`tail` read at most `minLines` lines (`-n K`). Pipes,
  redirects, `sed -n 'A,Bp'` and anything the gate does not understand go
  through; it catches the common dump, it is not a shell parser.
- **Subagents are exempt**: every call from a subagent goes through, reading
  big files is the scout's job.

A denied call gets *File … is over 350 lines (…; read gate: 350). Ask a
subagent (scout) for what you need from it, or read only the section you
edit with offset/limit.* as its tool result, and the agent goes on. Without
`subagents` the message names claude's built-in ones (*a subagent such as
Explore*). The gate **fails open**: bad hook input, an unreadable file or
any other error lets the call through and logs one line, and the hook has a
5 s timeout. A gate bug never fails a run.

`minLines` defaults to 350 when `readGate: {}` is set; the minimum is 100.
Unset, there is no gate, no `--settings` and no prompt sentence, as before.
The runner passes the hooks to claude as `--settings` JSON, and the role's
system prompt gets one more sentence, on the subagents line when the role
has subagents: *Reads of files over 350 lines are blocked; ask a subagent or
read a section with offset/limit.* Without subagents it is its own paragraph
and says *ask a subagent such as Explore*.

The read gate is claude only (cursor-agent has no hooks). A call that runs
on cursor, because the role's engine is cursor or because a claude role
runs on its cursor [fallback](#fallback-engine), goes without it; the runner
logs *read gate is claude only; ignored for role implement on cursor* once
per run and role.

The scout from above, with the gate:

```yaml
spec:
  agents:
    implement:
      readGate: {}            # or {minLines: 500}
      subagents:
        - name: scout
          # … as above
```

### Per-issue model and effort

On top of its profile, an issue can pick the implementer's engine, model and
effort with labels, set by `/agent use` / `/agent effort` comments or by hand
(see [commands.md](commands.md#model-and-effort)). Only the models in
`agents.models` (plus those the profiles and `implement` use) are allowed:

```yaml
agents:
  models:
    - {engine: claude, model: claude-opus-5-5, alias: opus}
    - {engine: claude, model: claude-sonnet-5-5, alias: sonnet}
    - {engine: cursor, model: auto}
    - {engine: cursor, model: gpt-5.6-sol-high, alias: gpt}
  maxEffort: high
```

The planner sets an `agent-effort::` label on the sub-issues it creates when
it thinks an issue needs more or less thinking than its profile; a label you
set is kept on re-plans.

### Profiles

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

The spec agent sees the names and descriptions and labels each sub-issue it
creates with `agent-profile::<name>`. Write descriptions that let it decide.
When planning, an unknown choice gets no label. When implementing, a missing
or unknown label falls back to `implement`. A profile label set by hand is
never overwritten by a re-plan.

If no profiles are configured, `fast` (claude, `claude-sonnet-5-5`) and
`strong` (claude, `claude-opus-5-5`) are used.

### Fallback engine

A role or profile can name a fallback: another engine and model to run on
while its own engine is at a usage limit (e.g. cursor's monthly budget is
used up), instead of waiting for the reset:

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

When the role's engine reports a usage limit (or the project already knows
of one) and the fallback's engine is not limited, the same request runs on
the fallback right away instead of waiting; the run report says which role
ran where. A fallback starts a fresh session with the full context (it
cannot resume another engine's session). While the fallback's engine is
limited too, the run waits for the reset as without a fallback.

With `agents.fallbackAtPercent` (e.g. `90`), a role with a fallback
switches to it already when the freshest usage reading of its engine has a
window, not reset since, at least that full; it keeps headroom for the roles
without a fallback. Only claude reports its usage windows. The threshold
never makes a run wait: without a usable fallback the role runs on its own
engine, and nothing is recorded as a limit.

The fallback must run on another engine (limits belong to an engine's
credentials) and name an allowed model, by its name or alias (see
[Per-issue model and effort](#per-issue-model-and-effort)); `effort` is
claude only. The role's other fields (instructions, tools, budget) stay.
Model and effort labels on an issue change the implementer's engine and
model, not its fallback. The condition `FallbacksValid` on the AgentProject
is `False` (reason `InvalidFallback`) and names the roles whose fallback is
broken, so a mistake shows before a limit needs it.

An issue with a [companion repository](usage.md#companion-repository)
needs a claude implementer: only claude works on the second checkout
(`--add-dir`). When the implementer's engine (after the profile, the model
label and `/agent use`) is cursor, it runs on the role's fallback if that
is claude, else on `agents.implement` if that is claude, and the run report
says so (`↪️ The implementer runs on claude/<model> … instead of
cursor/<model>: the companion repository … needs claude.`). Without either,
the run fails before any agent call. On a companion run, a fallback to
cursor is not used: at a claude usage limit the run waits for the reset.
Reviewers and the describer keep their engines.

### Engines

| | `claude` | `cursor` |
|-|----------|----------|
| CLI | `claude -p` | `cursor-agent -p` |
| structured output | `--json-schema` | schema in the prompt, JSON parsed from the answer |
| role instructions | `--append-system-prompt` | folded into the first prompt of a session |
| tool restrictions | `--allowedTools` / `--disallowedTools` | none via flags; read-only roles run without `--force` and the work tree is reset after them |
| session resume | `--resume` | `--resume` |
| cost reporting | yes (notional with a subscription) | no |
| live log of the session | thinking, messages, tool calls | messages, tool calls (no thinking in print mode) |

Sessions are stored per role and engine. Switching an issue to a profile with
another engine starts a new session with the full context.

## `spec.lifecycle`

> **Upgrading from a release with a longer `idleTimeout` default (`2h`,
> `1h`):** the API server stored the old default in every AgentProject that
> was created with a `lifecycle` block, and the sample sets it explicitly.
> Remove `idleTimeout` (or set it to `1m`) to stop pods right after each run.

| Field | Default | Description |
|-------|---------|-------------|
| `idleTimeout` | `1m` | after the last run, the pod is stopped; the workspace stays. A longer timeout keeps the pod (and its resource requests) for quick follow-ups, which saves only the pod start. After a failed run the pod stays at least 1h, so its logs can be read |
| `chatIdleTimeout` | `15m` | after the last Desk chat turn, how long an idle pod stays before hibernation (instead of only `idleTimeout`); unanswered chat messages keep the pod up regardless |
| `retentionPeriod` | `168h` (7 days) | after the last run, the workspace PVC is deleted |
| `maxRunsPerTask` | `20` | the task is `Blocked` when reached (runs paused by usage limits don't count) |
| `usageLimitBackoff` | `5m` | first wait after a usage or rate limit without a reported reset time; doubles per further hit |
| `maxUsageLimitBackoff` | `1h` | cap for that wait |
| `infraRetryAfter` | `30m` | after a run failed for an outside reason (agent pod or container lost, out of memory with no restart left, interrupted 3 times) the issue is blocked (`infra`); this long after the failure the operator starts one new run, once per run request. A planning run is retried the same way, also after a transient GitLab or network error |

## `spec.ci`

The CI feedback loop, see [usage.md](usage.md#ci-feedback).

| Field | Default | Description |
|-------|---------|-------------|
| `disabled` | `false` | move issues to review right after the push, without waiting for CI |
| `maxFixRounds` | `2` | CI fix runs per push you triggered, before the issue goes to needs-input |
| `retryFailedJobs` | `true` | retry the failed jobs once before the agent is involved |
| `noPipelineTimeout` | `10m` | no pipeline for the pushed commit within this time means "no CI" |

The GitLab token needs the `api` scope to read pipelines and job logs and to
retry jobs (Developer role).

The operator finds the pipeline by the pushed commit, which covers branch
pipelines and merge request pipelines. *Merged results* pipelines (GitLab
Premium) run on a merge commit instead and are not found; with them the loop
ends after `noPipelineTimeout` as "no CI". Set `ci.disabled: true` in that
case until this is supported.

## `spec.checkpoint`

When an implementer call (the first call of a run, a step, a fix round, a
CI or conflict round) is cut off by a usage limit, an engine error or
timeout, or a budget stop, the runner saves the uncommitted work tree as a
checkpoint commit on the work branch,
`chore(wip): checkpoint #<issue> step <n> (<reason>)` with the trailer
`Agent-Checkpoint: <run>/<step> <reason>`, and pushes it with `ci.skip`,
never forced. A ready merge request is set to draft first. The next run
starts from the checkpoint. A clean tree saves nothing; a merge in progress
(conflict round) is aborted and saves nothing. A usage limit in a fix round
keeps its own path: the run publishes what is committed with the open
findings. The checkpoint is recorded in the task's `status.run.checkpoint`
(cleared by the next regular push) and the run log's `save checkpoint`
step. A checkpoint no agent call ended in (a budget stop, a lost pod, an
OOM kill) is also listed in `status.run.runCheckpoints` (the newest 20), for
the checkpoint counts of the Task page and the Spend tab. It starts no pipeline and never counts as the pushed or reviewed
commit.

The same happens before the work tree is reset after an agent container
restart (`container-restart`; the call is then tried again from the
checkpoint), and in the new pod after a lost pod or an OOM kill
(`pod-lost`, `oom-kill`), before it resets the workspace. For that the
runner notes the implementer call in flight in its state on the volume, and
the new pod saves only when the workspace is on the task's branch and its
`HEAD` is the last pushed commit or descends from it. Step commits the lost
pod made but had not pushed are pushed with it. Where the runner has no
result of the call, the commit body takes the agent's last todo list or
message, which the agent container keeps in `.git/ai-last-status` while a
call runs.

| Field | Default | Description |
|-------|---------|-------------|
| `disabled` | `false` | save no checkpoints: the work of a call that is cut off is reset with the workspace by the next run |
| `maxFileBytes` | `5242880` (5 MiB) | files larger than this are left out of a checkpoint (they stay in the workspace and the run log names them); new files named like secrets (`.env*`, `*.pem`, `*.key`, `id_rsa*`, `*.p12`) are always left out |

## `spec.mainHealth`

The watcher of the default branch, see [usage.md](usage.md#main-health).

| Field | Default | Description |
|-------|---------|-------------|
| `disabled` | `false` | do not watch the default branch |
| `interval` | `5m` | how often the newest finished pipeline of the default branch is read (one `GET pipelines` per check; jobs and logs only for a new red pipeline) |
| `retryFailedJobs` | `true` | retry the failed jobs of a red pipeline once; green after the retry counts as flaky |
| `retryCanceled` | `true` | retry a canceled pipeline once when its commit has no successful pipeline of its own (one more `GET pipelines` per such pipeline), unless a newer pipeline that is not canceled or skipped supersedes it (GitLab's auto-cancel of redundant pipelines; one more `GET pipelines` when no finished one does); canceled again, comment on the `ci::main-red` issue if open, else on a `ci::main-canceled` issue, which freezes nothing. Canceled pipelines up to the newest one when it first ran are left alone |
| `issue` | `true` | open the `ci::main-red` issue, one `ci::flaky` issue per flaky job and the `ci::main-canceled` issue (off: a twice-canceled pipeline is only logged) |
| `freezeAutopilot` | `true` | autopilot merges nothing while the default branch is `red`: the issue that passed the gate is blocked (`main-red`) and autopilot continues by itself once the branch is green. A status not checked for 3 intervals does not freeze (see [merge freeze](usage.md#main-health)) |
| `autoFixFlaky` | `false` | hand a `ci::flaky` issue to the agent at its second occurrence, once: into the active autopilot milestone with the lowest IID (its queue), else without a milestone (`agent::ready`, also under `milestones.titlePattern`), with the operator's fixed task. Without it only the pilot's `fixFlaky` hands one over (see [fixing a flaky test](usage.md#main-health)) |
| `autoFix` | `false` | hand the `ci::main-red` issue to the agent (`agent::ready`), once per red episode, also under `milestones.titlePattern`. On autopilot, the frozen milestone merges the fix (the main-red lane, also for an owner's hand-over while `autoFix` is off); otherwise a human merges it (see [fixing a red branch](usage.md#main-health)) |

The token needs the same rights as for `spec.ci` (read pipelines and job logs,
retry jobs) and must be able to create issues.

## `spec.notify`

Whom the operator mentions (`cc @alice @bob`, appended to the comment that
needs a human) and asks for reviews, see
[usage.md](usage.md#owners-and-pings).

| Field | Default | Description |
|-------|---------|-------------|
| `users` | operator's `--default-owners` (with Helm: `defaultOwners`) | GitLab usernames, without `@`. A milestone overrides them with a line `/agent owners @alice @bob` in its description, for itself and its issues. Desk accept/reject of pilot proposals uses a separate line `/agent owner-subjects alice@example.com bob` (the UI's username claim); there is no project-level or default fallback |
| `events` | all | which situations mention them: `failed` (a run failed), `needsInput` (questions, or the agent could not finish alone), `ciFailed` (CI still red after the fix rounds), `review` (an issue reached review, not on autopilot), `autopilotPaused`, `autopilotDone`, `mainRed` (the default branch stayed red after a retry), `blocked` (a run gave up for an outside reason and its issue stays blocked, also on autopilot) |
| `requestReview` | `true` | when an issue reaches review (not on autopilot), make the users the reviewers of its MR, unless it has reviewers |

The users must be members of the project to be notified; mentioning the bot
user itself notifies nobody.

## `spec.pilot`

The milestone coordinator, see [usage.md](usage.md#talking-to-the-pilot); its
model is `agents.pilot`.

| Field | Default | Description |
|-------|---------|-------------|
| `mention` | `true` | a comment that starts with a mention of the bot user (`@<bot> …`) talks to the pilot, like `workflow.pilotPrefix`. The bot's username is read on every poll |
| `maxBudgetUSD` | `"20"` | the pilot's spend per milestone (its task's `status.run.costUSD`). It covers answers and assessments of events. Once it is used up, the pilot answers that it is, and an assessment only says what happened, without a model call. On claude a single answer or assessment may spend what is left at most (or `agents.pilot.maxBudgetUSD`, if lower). The spend is what the engines report: cursor reports none, so on cursor the budget never runs out |
| `autonomy` | unset | `managed`: the pilot runs `retry`, `reReview`, `instruct` (a fixed kind and a note), `openIssue`, `fixFlaky`, `retryPipeline`, `rebase` and `mergeBase` on its own, counted and audited on the tracking issue; `propose`: every action waits for an owner's `yes`. Unset: `managed` on autopilot milestones, `propose` on the others; a set value applies to every milestone. Neither the milestone description nor a rule changes it; an owner's `/pilot stop` lowers a milestone to `propose`, `/pilot go` returns it to this value (never above it). See [usage.md](usage.md#talking-to-the-pilot) |
| `maxInterventionsPerIssue` | `2` | the pilot's own actions per issue and head when it manages (an `openIssue` counts against the issue whose event or message triggered it, on the tracking issue against the milestone, a `fixFlaky` or `retryPipeline` against the milestone, for the milestone's life); past it, it proposes and pings the owners. An action counts when it is started, also if it then fails |
| `stuckAfter` | `30m` | an autopilot pause whose state (the queue's items, their heads and CI states, main health, the pause reason) has not changed for this long is **stuck**: the pilot assesses it once per pause, with the pause's ways out, and the owners get its options (from the ways out and the pilot's tools, e.g. `markDone`, `startAnyway`, `unskip`, `rebase`) as an assessment prompt. It never runs on its own, also when the pilot manages, and never resumes the autopilot; a resume ends the pause, a new pause gets its own assessment. An owner's `/agent pause` is never assessed. A Go duration (`45m`, `2h`) |

**Per milestone.** A milestone's standing rules (lines `/agent rule R<n>:
switch <setting>=<value>` in its description, see
[usage.md](usage.md#talking-to-the-pilot)) override four settings for its
issues: `ci.maxFixRounds` (0 to 5), `ci.retryFailedJobs`,
`agents.review.retries` and `agents.review.maxIterations` (written
`review.retries`, `review.maxIterations`; 0 to 5). Nothing else can be
switched per milestone: budgets and limits, the merge gate, the freeze,
`pilot.*` and `mainHealth.*` stay as configured here. Apart from them, a
line `/agent budget <usd>` (no rule) caps what an autopilot milestone spends in
all: at 100 % of it autopilot pauses (needs the research database, see
[usage.md](usage.md#budget)).

## `spec.budget`

Budgets of autopilot work, from what its agent calls cost (`agent_calls`,
needs the research database). The operator checks them once per poll, so
between calls; a running call is never killed, but a running run makes no
further call once its budget is used (`spec.budgetStop`, see
[usage.md](usage.md#budget)).

| Field | Default | Description |
|-------|---------|-------------|
| `perIssueUSD` | – | budget of every open issue of an autopilot milestone, as a decimal string, e.g. `"20"`. Once an issue's calls cost that much, autopilot pauses (`pauseKind: budget`, `pausedFor` the issue). An `/agent budget <usd>` comment on the issue overrides it for that issue ([commands.md](commands.md#on-an-issue-or-its-merge-request)). Unset or `0`: no per-issue budget |
| `perIssueAlertUSD` | – | spend of an open issue of an autopilot milestone, as a decimal string, e.g. `"15"`, at which autopilot posts one heads-up on the tracking issue (and a pilot event) with the issue's spend and its top kinds and models, and does **not** pause. Once per issue and value (`status.autopilot.items[].budgetAlertedAt`); another value re-arms it. The same heads-up comes at 80 % of an issue's own budget (`perIssueUSD` or its `/agent budget`). Unset or `0`: no alert ([usage.md](usage.md#spend-heads-up-per-issue)) |

```yaml
spec:
  budget:
    perIssueUSD: "20"
    perIssueAlertUSD: "15"
```

A milestone's own budget is its description's `/agent budget <usd>` line.
See [usage.md](usage.md#budget).

## `spec.webhook`

GitLab project webhooks that wake the operator within seconds, see
[usage.md](usage.md#webhooks-optional) for the setup. All fields are
optional; without `secretRef` every delivery for the project is refused and
the project is polled as before.

| Field | Default | Description |
|-------|---------|-------------|
| `secretRef` | – | `{name, key}` of a Secret in the AgentProject's namespace with the hook's secret token. A delivery counts only when its `X-Gitlab-Token` matches (constant-time compare, surrounding whitespace of the secret ignored); else `401` |
| `enabled` | `false` | register the project hook in GitLab: one hook for `url` with issues, notes, merge requests, pipelines and pushes, SSL verification and the secret token. Updated when the URL or the secret changes, checked hourly (a hook deleted or changed by hand is restored), deleted when switched off; other hooks are left alone. Needs the bot's Maintainer role; the `WebhookRegistered` condition (`Registered`, `Forbidden`, `Invalid`, `Failed`) tells how it went |
| `url` | – | public URL of the receiver, e.g. `https://ai-operator.example.com/hooks/gitlab`; needed with `enabled` |
| `fallbackInterval` | `10m` | while deliveries arrive, how often the project is fully synced anyway; in between, each `pollInterval` lists only the milestones (milestone changes send no delivery) and syncs fully when a planned one changed, appeared or vanished, or the AgentProject's spec changed |
| `silenceThreshold` | `30m` | how long after the last valid delivery the project counts as receiving. After it, after an operator restart, or without `secretRef`, every poll is a full sync again. The `WebhookDeliveries` condition (`Receiving`, `Silent`) and `status.webhook.lastDeliveryTime` show it |

While deliveries arrive, steps that only time moves on (infra retries,
autopilot's auto-resume, the `mainHealth.interval`, the hourly hook check)
run on full syncs, so they can be up to
`fallbackInterval` late. The receiver itself is a chart setting
(`webhook.enabled`, `webhook.port`, `webhook.service.*`, see the
[chart values](../charts/ai-operator/README.md#values)); it serves on the
leader only.

## `spec.crossRepo`

The companion repositories the project's issues may change in the same run
as their home change, see [usage.md](usage.md#companion-repository) and
the design in [design.md](design.md#cross-repo). Three gates must all
agree: the cluster allows the project, the AgentProject names it here, and
a milestone line `/agent repo <project> [merge]` (or an owner's
`/agent repo <project>` on the issue) gives it to an issue.

```yaml
spec:
  crossRepo:
    targets:
      - project: my-group/infra/cluster
        paths:
          - clusters/my-cluster/applications/ai-operator/
        autoMerge: true
        # tokenSecretRef: {name: gitlab-infra, key: token}
```

`targets[]` (at least one):

| Field | Default | Description |
|-------|---------|-------------|
| `project` | – | the companion's full path, e.g. `my-group/infra/cluster` (compared case-insensitively, without leading or trailing `/`) |
| `paths` | – | gitignore-style allow-list of the paths a companion change may touch (at least one pattern): `*` and `?` within a segment, `**` for any number of segments, a trailing `/` for a directory (everything under it), a leading or inner `/` anchors to the root, a pattern without one matches a name at any depth. Refused: `!` negation, absolute paths, `.` and `..`, character classes (`[`, `]`), backslashes, a leading `#`, surrounding whitespace |
| `autoMerge` | `false` | the ceiling for autopilot: only with it may a milestone line `/agent repo <project> merge` let autopilot merge the companion's merge request. Otherwise the owners are asked to merge it |
| `tokenSecretRef` | `spec.gitlab.tokenSecretRef` | `{name, key}` of a Secret in the AgentProject's namespace with the token for the companion project. It goes into the runner container only (`GITLAB_COMPANION_TOKEN`), never into the agent container |

A target is **dropped** when:

- cross-repo changes are off in the cluster (`--cross-repo-allowed-projects`
  is empty), or the project is not in that list;
- the project is the home project (`spec.gitlab.project`), or
  `jo.wiemers/personal/kubernetes`, which is never a valid target;
- it repeats an earlier target's project;
- `paths` is empty, a pattern does not compile, or a pattern matches
  `.gitlab-ci.yml`, anything under `.gitlab/`, or the whole tree (a
  top-level file or a file in any top-level directory, e.g. `*` or `**`).

The condition `CrossRepoReady` tells how it went: `True` with reason
`TargetsValid`, or `False` with reason `InvalidTargets` and the message
`dropped: <project>: <why>; …`, one entry per reason. It is absent without
`spec.crossRepo`. The valid targets still work; a dropped target is used
nowhere (no milestone line, label, token or guard permission counts for
it). The runner itself refuses a companion change outside `paths`, in
`.gitlab-ci.yml`, `.gitlab/` or `.gitmodules`, with a symlink or submodule,
or over 1 MiB of new files, see
[usage.md](usage.md#companion-repository).

**Cluster settings.** Two operator flags, set by the chart (see the
[chart values](../charts/ai-operator/README.md#values)):

| Flag | Helm value | Default | Description |
|------|------------|---------|-------------|
| `--cross-repo-allowed-projects` | `crossRepo.allowedProjects` (a list, joined with `,`) | empty | comma separated full paths of the projects AgentProjects may name in `spec.crossRepo.targets`. Empty turns cross-repo changes off: every target is dropped |
| `--self-project` | `selfProject` | empty | full path of the GitLab project this operator is built from, e.g. `my-group/ai-operator`. A companion of one of its merge requests [lands](usage.md#landing) once the running operator's version is at least the first release tag that contains the merge. Empty: that project's companions follow the tag rule like any other project's |

**The build version.** The operator knows its release from the build:
the Dockerfile's `ARG VERSION` (default `dev`) is linked in as
`main.version` (`-ldflags "-X main.version=…"`), and the release jobs set it
to the calendar version (`YEAR.MONTH.N`, see
[releasing.md](releasing.md)). The operator logs it at start
(`ai-operator starting`, `version`, `selfProject`). A `dev` build never
counts as a release, so the companions of `--self-project` wait until a
released operator runs.

## Other `spec` fields

| Field | Default | Description |
|-------|---------|-------------|
| `pollInterval` | `1m` | GitLab polling interval (with webhooks receiving, see [`spec.webhook`](#specwebhook)) |
| `maxConcurrentRuns` | `3` | runs in flight per project; more runs queue, the main-red fix first, then review-only runs, then issues without a milestone, then the oldest milestone (see [several milestones at once](usage.md#several-milestones-at-once)). Pilots (`Coordinate` tasks) are not counted and never queue for it. Keep this low on subscriptions, which have usage windows |
| `suspend` | `false` | stop polling and starting runs |
| `research` | | research runs of the AgentResearches that reference this project: `writerSecret` (`{name, usernameKey: username, passwordKey: password}`, the database login of their runner, a Secret in this namespace) and `candidates` (`quick`, `deep`: up to 5 `{engine, model, effort}` each, replacing the chart's `research.candidates`), see [research.md](research.md#specresearch-on-agentproject) |

## Agent image

`Dockerfile.agent` builds a base image with `ai-runner`, git, `claude` and
`cursor-agent`. The build args `INSTALL_CLAUDE` / `INSTALL_CURSOR` toggle the
CLIs. It also has `mise` and `rustup` (pinned, checksum-verified; build args
`MISE_VERSION` / `RUSTUP_VERSION` with the checksums next to them), with which
the agent installs the toolchains your default branch pins
(`.mise.toml`, `.tool-versions`, `rust-toolchain.toml`) into
`/workspace/home/.toolchains` (`MISE_DATA_DIR`, `RUSTUP_HOME`, `CARGO_HOME`; no auto-install),
see [usage.md](usage.md#toolchains-the-agent-installs). For tools without a
pin, or to save the download per issue, extend the image:

```dockerfile
FROM ghcr.io/jonathanwiemers/ai-operator-agent:2026.10.1
USER root
RUN apt-get update && apt-get install -y golang-go && rm -rf /var/lib/apt/lists/*
USER 1000
```

The same image runs both containers of an agent pod: `ai-runner` in the
`runner` container and `ai-runner agent-server` in the `agent` container (see
[design.md](design.md#security)). Pods run as uid 1000, non-root, with all
capabilities dropped. In the agent container `HOME` points to the workspace
(`/workspace/home`), so CLI sessions survive pod restarts. Install tools
outside `$HOME`.

## Network policy

The Helm chart can add a NetworkPolicy to every namespace in
`agent.namespaces` (`agent.networkPolicy.enabled=true`): no ingress, DNS
allowed, and egress everywhere except cloud metadata endpoints and private
networks. The runner needs the Kubernetes API, which usually has a private
address, so allow-list it (and any self-hosted GitLab or package mirror):

```sh
kubectl get endpoints kubernetes -o jsonpath='{.subsets[*].addresses[*].ip}'
helm upgrade ai-operator … \
  --set agent.networkPolicy.enabled=true \
  --set 'agent.networkPolicy.allowedCIDRs={10.0.0.10/32,10.20.0.0/24}'
```

NetworkPolicies need a CNI that enforces them (Calico, Cilium, …).
