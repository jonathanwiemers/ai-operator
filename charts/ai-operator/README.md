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
| `replicaCount` | `1` | |
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
| `healthProbePort` | `8081` | |
| `resources` | 50m / 64Mi requests, 256Mi limit | |
| `podSecurityContext`, `securityContext` | non-root, read-only root FS, no capabilities | |
| `nodeSelector`, `tolerations`, `affinity`, `podAnnotations`, `podLabels` | | |
| `extraArgs` | `[]` | extra operator flags |

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
| `ui.authorization.rules` | `[]` | `{roles: [...], projects: ["namespace/name", ...]}`; a user with any of the roles sees the matching projects (all if `projects` is empty). `"*"` matches every user. Nobody sees anything without a rule |
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
