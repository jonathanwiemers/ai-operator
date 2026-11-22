# Upgrade

This page is for when you move ai-operator to a new release. It shows the
upgrade command, why a manifest that still sets a removed field stops
applying, why a removed Helm value goes unnoticed, and what each release
removed, so you know what to delete from your manifests and values file.

## Upgrade the chart

Run the command you installed with, with the new version:

```sh
helm upgrade --install ai-operator oci://ghcr.io/jonathanwiemers/charts/ai-operator \
  --namespace ai-operator-system -f values.yaml --version 2026.11.5
```

The versions are on the
[releases page](https://github.com/jonathanwiemers/ai-operator/releases).
The chart, the operator and the agent image share one version, so this one
command upgrades all three. If Flux manages the chart, its `OCIRepository`
picks the version: a `semver` range follows new releases by itself, and a
pinned `tag` you change to the new version.

The custom resource definitions come with the chart, and Helm upgrades them
in the same step. If you set `crds.install: false`, nothing upgrades them
for you: render them from the new chart with `helm template` and apply them
before you upgrade the operator.

Before you upgrade, read [Removed in this release](#removed-in-this-release)
for every release between yours and the new one, and delete what it lists.

## A removed field fails `kubectl apply`

Each [AgentProject](../concepts/glossary.md#agentproject), project set and
research is a custom resource. The new release's definitions no longer
list a removed field. `kubectl apply` validates your manifest strictly
against them, so a manifest that still sets the field is rejected as a
whole, and nothing of it is applied:

```text
Error from server (BadRequest): error when applying patch: ...
AgentProject in version "v1alpha1" cannot be handled as a AgentProject:
strict decoding error: unknown field "spec.workflow.legacyLabels"
```

The message names the field. Delete it from the manifest, then apply
again. Do not turn validation off with `--validate=ignore` to get past it:
the API server then drops the field anyway, and the manifest keeps
something that does nothing.

A removed `status` field needs nothing from you. The operator writes
status, not your manifests. The API server drops a value it has stored for
the field the next time it reads the resource, and a `kubectl get -o
jsonpath` that asks for it prints nothing.

The same applies to tools that apply your manifests for you, such as Flux
or Argo CD: their sync fails with the same error until you delete the
field from the manifest in Git.

## A removed Helm value is ignored

The chart has no schema for its values. Helm does not complain about a
value the chart no longer reads: it ignores it silently, and the setting
you meant to make has no effect. After an upgrade, check your values file
against the list below and against the
[chart README](../../charts/ai-operator/README.md#values) of the new
version, and delete what is gone.

## Removed in this release

Each row is one item a release removed because nothing in the operator
read it. *Leaves in* is the first release without it.

| Item | Kind | Leaves in | What to delete |
|------|------|-----------|----------------|
| `status.pilot.rejected` | AgentTask status field | the first release after 22 November 2026 | Nothing. The operator stopped writing it when each denied command got its own reply; a stored value is dropped. |
| `status.autopilot.passedBy` | AgentTask status field | the first release after 22 November 2026 | Nothing. The operator stopped reading and writing it when milestones got a run order; a stored value is dropped. |

Both fields belong to the [task](../concepts/glossary.md#task) of a
[milestone](../concepts/glossary.md#milestone): `pilot` is the
[pilot](../concepts/glossary.md#pilot)'s bookkeeping, `autopilot` the
[autopilot](../concepts/glossary.md#autopilot) queue.

## Example: delete a removed field

Say a release removes `spec.workflow.legacyLabels` from the AgentProject.
(The name is made up for this example; the table above lists the real
removals.) Your manifest before the upgrade:

```yaml
apiVersion: ai.wiemers.me/v1alpha1
kind: AgentProject
metadata:
  name: shop
  namespace: agents
spec:
  gitlab:
    project: acme/shop
    tokenSecretRef: {name: gitlab, key: token}
  agents:
    credentialsSecretName: agent-credentials
  workflow:
    commandsFrom: owners
    legacyLabels: true          # removed in the new release
  notify:
    users: [alice]
```

After the upgrade, `kubectl apply -f shop.yaml` fails with
`strict decoding error: unknown field "spec.workflow.legacyLabels"`.
Delete the line:

```yaml
apiVersion: ai.wiemers.me/v1alpha1
kind: AgentProject
metadata:
  name: shop
  namespace: agents
spec:
  gitlab:
    project: acme/shop
    tokenSecretRef: {name: gitlab, key: token}
  agents:
    credentialsSecretName: agent-credentials
  workflow:
    commandsFrom: owners
  notify:
    users: [alice]
```

Apply it again and check that the project syncs:

```sh
kubectl apply -f shop.yaml
kubectl -n agents get agentprojects
```

## Next

- [Install](../getting-started/install.md): the install command and the
  chart values you started with.
- [AgentProject reference](../reference/agentproject.md): every field the
  current release has.
- [Watch from Kubernetes](watch-from-kubernetes.md): check projects and
  tasks with `kubectl` after the upgrade.
