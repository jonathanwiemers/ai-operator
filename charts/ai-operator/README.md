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
either way. These values are not used yet: the controller, the runner mode
and the UI pages follow in later releases.

| Key | Default | Description |
|-----|---------|-------------|
| `research.database.host` | `""` | Postgres host; empty turns research off |
| `research.database.port` | `5432` | |
| `research.database.name` | `""` | database |
| `research.database.sslMode` | `require` | libpq `sslmode` |
| `research.database.ownerSecret` | `{name: "", usernameKey: username, passwordKey: password}` | login of the operator (owns the schema), a Secret in the release's namespace |
| `research.database.readerSecret` | `{name: "", usernameKey: username, passwordKey: password}` | login of the UI (read only), a Secret in the release's namespace |
| `research.database.readerRole` | `""` | role the migrations grant read access to |
| `research.database.writerRoles` | `[]` | roles the migrations grant write access to: the logins of `spec.research.writerSecret` of the AgentProjects |
| `research.database.networkPolicyPeer` | `{namespace: "", podLabels: {}}` | the database pods, which research-run pods may reach on TCP 5432 |
| `research.candidates.quick` | `claude/claude-sonnet-5-5`, `cursor/auto` | agents tried in order for quick runs without `spec.agent`; `spec.research.candidates` of an AgentProject overrides them |
| `research.candidates.deep` | `claude/claude-opus-5-5` (high), `claude/claude-sonnet-5-5`, `cursor/auto` | the same for deep runs |
| `research.maxUsagePercent` | `70` | research uses an engine only while all its usage windows are below this |

With the database of the Zalando postgres-operator on netcup01:

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

### Web UI

The optional read-only web UI, see [docs/ui.md](../../docs/ui.md). It runs as
its own Deployment (`<release>-ui`) with a service account that can only read
AgentProjects and AgentTasks.

| Key | Default | Description |
|-----|---------|-------------|
| `ui.enabled` | `false` | |
| `ui.externalURL` | | URL users open, without a path, e.g. `https://ai.example.com`; required with `oidc`. The redirect URI is `<externalURL>/oauth2/callback` |
| `ui.auth.mode` | `oidc` | `oidc`, or `none`: no login, listens on `127.0.0.1` only (reach it with `kubectl port-forward`), no Service, no HTTPRoute |
| `ui.auth.sessionTTL` | `12h` | how long a login lasts; roles are read at login |
| `ui.oidc.issuerURL` | | required with `oidc` |
| `ui.oidc.clientID` | | required with `oidc` |
| `ui.oidc.existingSecret` | | required with `oidc`: Secret with `session-key` (at least 32 characters) and, for confidential clients, `client-secret` |
| `ui.oidc.scopes` | `[openid, profile, email]` | |
| `ui.oidc.claims.username` | `has(claims.email) ? claims.email : claims.sub` | CEL expression giving the user name |
| `ui.oidc.claims.roles` | `has(claims.groups) ? claims.groups : []` | CEL expression giving the roles: a list, a string or a map whose keys are the roles (Zitadel: `claims["urn:zitadel:iam:org:project:roles"]`) |
| `ui.authorization.rules` | `[]` | `{roles: [...], projects: ["namespace/name", ...], transcript, steer}`; a user with any of the roles sees the matching projects (all if `projects` is empty, globs allowed); `transcript: true` adds live transcripts and prompts, `steer: true` writing comments, the milestone chat included. `"*"` matches every user. Nobody sees anything without a rule |
| `ui.research.allowCreate` | `false` | not used yet: let users with the research write grant start and delete one-off researches |
| `ui.research.namespace` | `""` | not used yet: namespace the UI creates researches in |
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
