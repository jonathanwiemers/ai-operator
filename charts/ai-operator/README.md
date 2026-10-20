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
| `crds.install` | `true` | install and upgrade the CRDs |
| `crds.keep` | `true` | keep the CRDs, and with them all AgentProjects and AgentTasks, on `helm uninstall` |
| `serviceAccount.create` | `true` | |
| `serviceAccount.name` | release full name | |
| `serviceAccount.annotations` | `{}` | |
| `agent.image.repository` | `ghcr.io/jonathanwiemers/ai-operator-agent` | default agent image for AgentProjects without `spec.agents.image`; empty disables the default |
| `agent.image.tag` | chart `appVersion` | |
| `agent.logLevel` | `logLevel` | `debug`, `info`, `error` for the agent pods; `spec.agents.logLevel` overrides it per project |
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

## Without Helm

`make deploy` applies the same resources with kustomize from `config/`.
