# ai-operator Helm chart

Installs the ai-operator, its CRDs and RBAC, and (optionally) the service
accounts that agent pods use in the namespaces hosting your `AgentProject`s.

```sh
helm install ai-operator oci://ghcr.io/jonathanwiemers/charts/ai-operator \
  --version 2026.10.1 \
  --namespace ai-operator-system --create-namespace \
  --set 'agent.namespaces={agents}' --set agent.createNamespaces=true
```

Releases use calendar versions `YEAR.MONTH.N`: `2026.10.1`, `2026.10.2`, …
Chart version, app version and image tags are always the same, so
`--version 2026.10.3` installs operator and agent images `2026.10.3`.
Available versions are listed on the [releases page](https://github.com/jonathanwiemers/ai-operator/releases).

Upgrade with `helm upgrade ai-operator oci://ghcr.io/jonathanwiemers/charts/ai-operator --version <new>`.
The CRDs are part of the templates, so they are upgraded too.

## Values

| Key | Default | Description |
|-----|---------|-------------|
| `image.repository` | `ghcr.io/jonathanwiemers/ai-operator` | operator image |
| `image.tag` | chart `appVersion` | |
| `image.pullPolicy` | `IfNotPresent` | |
| `imagePullSecrets` | `[]` | for private registries; set on the operator pod and on the agent service account in each of `agent.namespaces` (the Secret must exist there) |
| `nameOverride`, `fullnameOverride` | `""` | |
| `replicaCount` | `1` | with more than 1, only the leader serves the webhook receiver |
| `leaderElection` | `true` | |
| `logLevel` | `info` | `debug`, `info`, `error` |
| `defaultOwners` | `[]` | GitLab usernames mentioned when a task needs a human, for AgentProjects without `spec.notify.users`; a milestone line `/agent owners @a @b` overrides them |
| `crossRepo.allowedProjects` | `[]` | full paths of the GitLab projects AgentProjects may name as companion repositories in `spec.crossRepo.targets`; empty turns cross-repo changes off (condition `CrossRepoReady=False`) |
| `selfProject` | `""` | full path of the GitLab project the operator is built from (`--self-project`, e.g. `my-group/ai-operator`): a [companion](../../docs/usage.md#landing) of one of its merge requests lands once the running operator is a release (`YEAR.MONTH.N`, the image's build version) that contains the merge. Empty: that project's companions follow the tag rule like any other project's |
| `crds.install` | `true` | install and upgrade the CRDs |
| `crds.keep` | `true` | keep the CRDs, and with them all AgentProjects and AgentTasks, on `helm uninstall` |
| `serviceAccount.create` | `true` | |
| `serviceAccount.name` | release full name | |
| `serviceAccount.annotations` | `{}` | |
| `agent.image.repository` | `ghcr.io/jonathanwiemers/ai-operator-agent` | default agent image for AgentProjects without `spec.agents.image` (CLIs, `mise` and `rustup` for the toolchains a repository pins); empty disables the default |
| `agent.image.tag` | chart `appVersion` | |
| `agent.logLevel` | `logLevel` if it is `debug`, `info` or `error`, else `info` | `debug`, `info`, `error` for the agent pods; `spec.agents.logLevel` overrides it per project |
| `agent.serviceAccountName` | `ai-operator-agent` | must match `spec.agents.serviceAccountName` of your AgentProjects |
| `agent.namespaces` | `[]` | namespaces that host AgentProjects; the chart creates the agent service account and RoleBinding there |
| `agent.createNamespaces` | `false` | also create those namespaces |
| `agent.networkPolicy.enabled` | `false` | NetworkPolicy for agent pods in `agent.namespaces`: no ingress, no egress to blocked CIDRs |
| `agent.networkPolicy.allowedCIDRs` | `[]` | private destinations still allowed (Kubernetes API endpoints, self-hosted GitLab, mirrors) |
| `agent.networkPolicy.blockedCIDRs` | link-local, RFC 1918, CGNAT, IPv6 ULA / link-local | |
| `agent.networkPolicy.dnsSelector` | pods labelled `k8s-app: kube-dns` | where DNS is allowed |
| `metrics.port` | `8080` | |
| `metrics.service.enabled` | `false` | expose metrics through a Service |
| `metrics.service.type`, `metrics.service.annotations` | `ClusterIP`, `{}` | |
| `healthProbePort` | `8081` | |
| `webhook.enabled` | `true` | serve the GitLab webhook receiver (`POST /hooks/gitlab`) and its Service `<release>-webhook`; see below |
| `webhook.port` | `8082` | port of the receiver and of the Service |
| `webhook.service.type`, `webhook.service.annotations` | `ClusterIP`, `{}` | |
| `resources` | 50m / 64Mi requests, 256Mi limit | |
| `podSecurityContext`, `securityContext` | non-root, read-only root FS, no capabilities | |
| `nodeSelector`, `tolerations`, `affinity`, `podAnnotations`, `podLabels` | | |
| `extraArgs` | `[]` | extra operator flags |

### GitLab webhooks

With `webhook.enabled`, the operator listens on `webhook.port` for GitLab
project hook deliveries on `POST /hooks/gitlab`. A delivery only wakes the
sync of the AgentProjects on that GitLab project whose
`spec.webhook.secretRef` matches the hook's secret token; projects without a
`secretRef` are never woken. A burst of deliveries for one project leads to
at most one sync per 5 seconds (the last delivery is never lost).

The chart renders no route: exposing `<release>-webhook` to GitLab (an
HTTPRoute or Ingress for `/hooks/gitlab`) is up to the cluster.

With `spec.webhook.enabled: true` and `spec.webhook.url` (the public URL of
`/hooks/gitlab`), the operator registers the project hook itself: one hook
for issues, notes, merge requests, pipelines and pushes, with SSL
verification and the `secretRef`'s token. It updates that hook when the URL
or the secret changes, looks it up hourly to recreate one deleted by hand,
and deletes it when `enabled` is switched off; other hooks are left alone.
This needs the bot's Maintainer role. The AgentProject's `WebhookRegistered`
condition tells how it went (`Registered`, `Forbidden`, `Invalid`, `Failed`);
a failed registration never fails the sync, polling goes on.

While deliveries arrive, polling slows down to save the bot's API budget: a
project is fully synced on every delivery and every
`spec.webhook.fallbackInterval` (default `10m`); in between, each
`pollInterval` lists only the milestones (milestone changes cause no
deliveries) and syncs fully when a planned one changed, appeared or vanished,
or the AgentProject's spec changed. After `spec.webhook.silenceThreshold`
(default `30m`) without a delivery, after a restart (deliveries are kept in
memory) or without `secretRef`, every poll is a full sync again. Steps that
only time advances (infra retries, auto-resume, the main health interval) run
on full syncs, so up to `fallbackInterval` late while deliveries arrive. The
`WebhookDeliveries` condition (`Receiving` or `Silent`) and
`status.webhook.lastDeliveryTime` show the state.

Only the leader serves the receiver. With `replicaCount` above 1, a delivery
that reaches a non-leader pod fails and GitLab's own retry covers it.

### Research

Research runs (`AgentResearch`, see [docs/research.md](../../docs/research.md))
store their results in Postgres. The feature is off while
`research.database.host` is empty. The chart installs the `AgentResearch` CRD
either way. With a host the operator connects as the owner, applies the
migrations and grants at start (retrying while the database is unreachable)
and deletes expired results hourly; see
[docs/configuration.md](../../docs/configuration.md#research-database). It
also runs the research controller: one pod per run, whose runner logs in
with the project's `spec.research.writerSecret`. The agent Role gets
`get` on `agentresearches` and `get`/`patch` on their status. The operator
chooses each run's agent from `research.candidates` and leaves an engine to
the factory once it is limited or at `research.maxUsagePercent`; see
[Choosing agents](../../docs/research.md#choosing-agents). With `ui.enabled`
the UI logs in with `readerSecret` and its ClusterRole gets
`get`/`list`/`watch` on `agentresearches` for its Research section (see
[docs/ui.md](../../docs/ui.md#platform)); `ui.research.*` is under
[Web UI](#web-ui).

| Key | Default | Description |
|-----|---------|-------------|
| `research.database.host` | `""` | Postgres host; empty turns research off |
| `research.database.port` | `5432` | |
| `research.database.name` | `""` | database, required with a host |
| `research.database.sslMode` | `require` | libpq `sslmode`: `disable`, `require`, `verify-ca` or `verify-full` |
| `research.database.ownerSecret` | `{name: "", usernameKey: username, passwordKey: password}` | login of the operator (owns the schema), a Secret in the release's namespace; `name` is required with a host. Passed as the env `AI_RESEARCH_DB_USER`/`AI_RESEARCH_DB_PASSWORD` |
| `research.database.readerSecret` | `{name: "", usernameKey: username, passwordKey: password}` | login of the UI (read only), a Secret in the release's namespace; `name` is required with a host and `ui.enabled`. Passed as the env `AI_OPERATOR_UI_RESEARCH_DB_USER`/`AI_OPERATOR_UI_RESEARCH_DB_PASSWORD` |
| `research.database.readerRole` | `""` | role the migrations grant read access to |
| `research.database.writerRoles` | `[]` | roles the migrations grant write access to: the logins of `spec.research.writerSecret` of the AgentProjects |
| `research.database.chatWriterRole` | `""` | role the migrations grant chat insert access to (the UI's chat login from `ui.chat.writerSecret`) |
| `research.database.networkPolicyPeer` | `{namespace: "", podLabels: {}}` | the database pods. With `agent.networkPolicy.enabled` and a `namespace`, the chart adds a NetworkPolicy `<agent role>-research-db` per agent namespace: pods with the label `ai.wiemers.me/research` (research runs, not tasks) may reach these pods on TCP `research.database.port` |
| `research.candidates.quick` | `claude/claude-sonnet-5-5`, `cursor/auto` | agents tried in order for quick runs without `spec.agent`; `spec.research.candidates` of an AgentProject overrides them. Each entry is `{engine, model, effort?}`; passed as `--research-candidates-quick=engine/model[@effort],…` |
| `research.candidates.deep` | `claude/claude-opus-5-5` (high), `claude/claude-sonnet-5-5`, `cursor/auto` | the same for deep runs, passed as `--research-candidates-deep` |
| `research.maxUsagePercent` | `70` | research uses an engine only while all its usage windows (not reset since) are below this, 1 to 100, a fixed `spec.agent` included; passed as `--research-max-usage-percent` |
| `research.historyRetention` | `8760h` | how long the factory's history (comments, runs, CI, reviews, merges: the web UI's streams; every agent call in `agent_calls`) and Desk chat messages are kept in the database; passed as `--history-retention` |

With the database of the Zalando postgres-operator on netcup01 (the
AgentProject's writer login and the UI part are in
[docs/research.md](../../docs/research.md#example-netcup01)):

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

### Pricing

Cost of agent calls in `agent_calls` (needs `research.database.host`):

| Engine | Cost | `cost_source` |
|---|---|---|
| claude | the `total_cost_usd` claude reports (the notional API price), kept even when `pricing` has the model | `reported` |
| cursor, with tokens and a price in `pricing` | tokens × price, estimated by the operator when it writes the row | `estimated` |
| cursor without tokens or without a price | NULL, shown as "n/a", never $0 | `unknown` |

| Key | Default | Description |
|-----|---------|-------------|
| `pricing` | `{}` | prices in USD per million tokens under `models`, keyed `<engine>/<model>`, a bare model or `<engine>/*` (engine-wide fallback), looked up in that order; each with `input`, `output`, `cacheRead`, `cacheWrite` (missing = 0). Non-empty: rendered into the ConfigMap `<release>-pricing` (`pricing.yaml`), mounted into the operator and passed as `--pricing-file`; a change restarts the operator. Unknown keys or negative prices stop the operator at start |

```yaml
pricing:
  models:
    "cursor/auto": {input: 1.25, output: 10, cacheRead: 0.125, cacheWrite: 1.25}
    "cursor/*": {input: 3, output: 15, cacheRead: 0.3, cacheWrite: 3.75}
```

Prices apply to calls written after the operator starts with them; rows
already written keep their cost.

### Subagents

Not a chart value: a role of an AgentProject can have `subagents`, passed to
claude as `--agents` (claude only; the default is none). A read-only scout on
haiku and a writer on sonnet for the implementer:

```yaml
# AgentProject spec
agents:
  implement:
    readGate: {}        # optional: block whole reads of files over 350 lines
    maxStepLines: 400   # optional: tell it to close a step past 400 changed lines
    conflict:           # optional: resolve merge conflicts on sonnet, in a fresh session
      model: claude-sonnet-5-5
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

A subagent whose `tools` list `Write` or `Edit` is a writer: the role is told
to hand it boilerplate with a short spec and one reference file, and what it
writes never enters the main context (it answers with paths only), at the
cost of its own calls. Every other subagent is a reader.

See [docs/configuration.md](../../docs/configuration.md#subagents) for how
the role is told to use it and how to compare the cost in the Spend tab, and
[Read gate](../../docs/configuration.md#read-gate) for what `readGate`
blocks (claude only, like `subagents`), and [Roles](../../docs/configuration.md#roles)
for `maxStepLines`, the step-size gate of draft runs (claude only too), and
[Conflict role](../../docs/configuration.md#conflict-role) for `conflict`.

### Web UI

The optional read-only web UI, see [docs/ui.md](../../docs/ui.md). It runs as
its own Deployment (`<release>-ui`) with a service account that can only read
AgentProjects and AgentTasks (and AgentResearches with
`research.database.host`). The one exception is `ui.research.allowCreate`:
`create`/`delete` on AgentResearches in `ui.research.namespace` only.

| Key | Default | Description |
|-----|---------|-------------|
| `ui.enabled` | `false` | |
| `ui.externalURL` | | URL users open, without a path, e.g. `https://ai.example.com`; required with `oidc`. The redirect URI is `<externalURL>/oauth2/callback` . With the UI enabled, the operator gets it as `--desk-url` (agent pods as `AI_DESK_URL`) for the "Answer in the Desk" links in the prompts it mirrors to GitLab and in its replies to comments; without it those carry no link. Each project's `spec.workflow.inputs` (default `[gitlab, desk]`; `[desk]` stops `/agent` and `/pilot` comments from being acted on) is described in [configuration.md](../../docs/configuration.md#specworkflow) |
| `ui.auth.mode` | `oidc` | `oidc`, or `none`: no login, listens on `127.0.0.1` only (reach it with `kubectl port-forward`), no Service, no HTTPRoute |
| `ui.auth.sessionTTL` | `12h` | how long a login lasts; roles are read at login |
| `ui.oidc.issuerURL` | | required with `oidc` |
| `ui.oidc.clientID` | | required with `oidc` |
| `ui.oidc.existingSecret` | | required with `oidc`: Secret with `session-key` (at least 32 characters) and, for confidential clients, `client-secret` |
| `ui.oidc.scopes` | `[openid, profile, email]` | |
| `ui.oidc.claims.username` | `has(claims.email) ? claims.email : claims.sub` | CEL expression giving the user name |
| `ui.oidc.claims.roles` | `has(claims.groups) ? claims.groups : []` | CEL expression giving the roles: a list, a string or a map whose keys are the roles (Zitadel: `claims["urn:zitadel:iam:org:project:roles"]`) |
| `ui.authorization.rules` | `[]` | `{roles: [...], projects: ["namespace/name", ...], transcript, steer, conversation, chat}`; a user with any of the roles sees the matching projects (all if `projects` is empty, globs allowed); `transcript: true` adds live transcripts and prompts, `steer: true` writing comments, the milestone chat included, `conversation: true` the bodies of the comments and Desk chat in the streams, `chat: true` posting Desk chat (needs `ui.chat.writerSecret`). `"*"` matches every user. Nobody sees anything without a rule. A research rule `{roles, research: read\|write, researches: ["namespace/name", ...]}` grants researches instead (no projects, no `transcript`, `steer`, `conversation` or `chat`), see [Access rules](../../docs/ui.md#access-rules) |
| `ui.research.allowCreate` | `false` | let users with the research write grant start one-off researches and delete their own (needs `research.database.host`); renders a Role and RoleBinding with `create`/`delete`/`get` on `agentresearches` in `ui.research.namespace` only, see [One-off researches](../../docs/ui.md#one-off-researches) |
| `ui.research.namespace` | `""` | namespace the UI creates one-off researches in; required with `allowCreate` (the chart fails without it). One-offs run with an AgentProject there that has `spec.research.writerSecret` |
| `ui.chat.writerSecret` | `{name: "", usernameKey: username, passwordKey: password}` | Desk chat writer login, a Secret in the release's namespace; empty `name` turns chat off. Passed as `AI_OPERATOR_UI_CHAT_DB_USER`/`AI_OPERATOR_UI_CHAT_DB_PASSWORD`; needs `research.database.host` and `research.database.chatWriterRole` |
| `ui.gitlab.url`, `ui.gitlab.clientID` | | GitLab and its OAuth application (confidential, scope `api`, redirect URI `<externalURL>/oauth2/gitlab/callback`) for [steering](../../docs/ui.md#steering): users connect their account, see the comments and post in their own name |
| `ui.gitlab.tokenUser` | | instead of OAuth, for one user: the UI user name whose fixed GitLab token (key `gitlab-token`) the UI uses for them |
| `ui.gitlab.existingSecret` | `ui.oidc.existingSecret` | Secret with `gitlab-client-secret` (OAuth) and/or `gitlab-token` (`tokenUser`) |
| `ui.httpRoute.enabled` | `false` | Gateway API HTTPRoute for the UI's host |
| `ui.httpRoute.parentRefs` | `[]` | the Gateway (listener); required when enabled |
| `ui.httpRoute.hostnames` | host of `externalURL` | |
| `ui.httpRoute.timeouts` | `{}` | timeouts of the route's rule, e.g. `{request: 0s}` if the Gateway cuts the event stream |
| `ui.httpRoute.annotations` | `{}` | |
| `ui.networkPolicy.enabled` | `false` | allow ingress to the UI only from `ui.networkPolicy.from` (and to the probe port) |
| `ui.networkPolicy.from` | `[]` | NetworkPolicy peers, e.g. the Gateway's namespace |
| `ui.service.type`, `ui.service.annotations` | `ClusterIP`, `{}` | |
| `ui.port`, `ui.healthProbePort` | `9080`, `8081` | |
| `ui.replicaCount` | `1` | replicas share logins through the session key |
| `ui.resources` | 20m / 48Mi requests, 192Mi limit | |
| `ui.nodeSelector`, `ui.tolerations`, `ui.affinity`, `ui.podAnnotations`, `ui.podLabels`, `ui.extraArgs` | | |

## Without Helm

`make deploy` applies the same resources with kustomize from `config/`.
