# Configuration

Installation with Helm or Flux, the Desk, research, plans, pricing and the
Secrets. The `AgentProject` fields are in the
[AgentProject reference](reference/agentproject.md), with a complete
example. `kubectl explain agentproject.spec --recursive` shows the full
schema.

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
nothing until an AgentProject sets [`spec.webhook`](reference/agentproject.md#specwebhook).

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
on `agentresearches` and `agentplans`, and a Role in each `agent.namespaces`
entry with `create`, `delete`, `get`, `list`, `watch` and `patch` on
`agentplans` and `patch` on `agentplans/status`, for the Desk's milestone
plans. The UI only reads the database (the operator migrates) and starts
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
| `research.historyRetention` | `8760h` | how long the factory's history (the Desk's streams, see [ui.md](ui.md#desk-and-history)), the agent calls (`agent_calls`, see [design.md](contributing/design.md#history)), Desk chat messages and the Desk's milestone snapshot ([design.md](contributing/design.md#desk-snapshot)) are kept; `--history-retention` |
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
[design.md](contributing/design.md#desk-snapshot)): one milestone per poll, at most 4
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

### Plan

`AgentPlan` resources are milestone-planning conversations in the Desk. The
chart value `plan` feeds the operator and the UI. Invalid values stop the
operator at start, refuse the UI config, or fail `helm template` when the
raise limit is below the default.

| Value | Default | Description |
|-------|---------|-------------|
| `plan.idleTimeout` | `30m` | after the last message or turn, how long a plan pod stays before it ends (phase Idle; the workspace stays for the next pod). A running turn or an unanswered message keeps the pod; `--plan-idle-timeout` |
| `plan.retention` | `720h` | how long plans are kept after their last message, turn or creation; the pod and workspace go with the plan; `--plan-retention` |
| `plan.maxBudgetUSD` | `"5"` | default spend cap per plan (USD decimal string); `--plan-max-budget-usd` and the UI default for new plans |
| `plan.maxBudgetUSDLimit` | `"20"` | highest cap an owner may raise a plan to in the Desk (UI config); must be at least `plan.maxBudgetUSD` |

### Pricing

Every agent call is a row of `agent_calls` (see
[design.md](contributing/design.md#history), needs the research database) with a cost
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
