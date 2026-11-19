# AgentProjectSet

An [AgentProjectSet](../concepts/glossary.md#project-set) makes the operator
work in every project of a GitLab group that its globs match. This page
lists every field with its default, the status, the conditions, and the
labels and metric the set uses. For when to use one, see the
[Project sets guide](../guides/project-sets.md).

## Example

```yaml
apiVersion: ai.wiemers.me/v1alpha1
kind: AgentProjectSet
metadata:
  name: my-group
  namespace: agents
spec:
  gitlab:
    group: my-group
    tokenSecretRef: {name: gitlab, key: token}
  include: ["my-group/**"]
  exclude: ["my-group/archived/**"]
  pollInterval: 2m
  dormantAfter: 168h
  template:
    agents:
      credentialsSecretName: agent-credentials
    webhook:
      enabled: true
      url: https://ai-operator.example.com/hooks/gitlab
      secretRef: {name: gitlab-webhook, key: token}
```

## `spec`

| Field | Meaning | Default |
|-------|---------|---------|
| `gitlab.url` | The GitLab instance. | `https://gitlab.com` |
| `gitlab.tokenSecretRef` | `{name, key}` of a Secret with a token that has `api` and `write_repository` in every project. The children use it too. | – |
| `gitlab.group` | Full path of the root group. The set discovers it and its subgroups. | – |
| `include` | Project path globs, at least one. `*` within a path segment, `**` across segments, without case. | – |
| `exclude` | Path globs the set never works in. They win over `include`. Archived projects are always skipped. | none |
| `template` | Any [`AgentProject`](agentproject.md) `spec` field except `gitlab`. Unknown fields are refused. It needs `webhook.enabled`, `webhook.secretRef` and `webhook.url`. | empty |
| `pollInterval` | How often the set discovers the group. | `2m` |
| `dormantAfter` | How long a child stays without work before the set removes it. | `168h` |

The API server defaults the children's fields as for any AgentProject. A
change to `template` reaches the children on the next poll.

## `status`

| Field | Meaning |
|-------|---------|
| `lastDiscoveryTime` | When the set last listed the group. |
| `counts` | Projects per state: `active`, `dormant`, `excluded`, `explicit`, `nameTaken`. |
| `projects` | One entry per matched project: `path`, `state`, `child`, `reason`, `lastActiveTime`, the hash of its active `milestones`, `milestonesTruncated` (20 or more active milestones: the hash may miss a change, so its AgentProject lists them itself), and `hookID` and `hookCheckedAt` of the set's own hook on a dormant project. |
| `droppedTargets` | Template `crossRepo` targets that `exclude` matches. The set leaves them out of its children. |
| `complexity` | `score` and `limit` of the last GraphQL query. |

Entry states:

| State | Meaning |
|-------|---------|
| `active` | The project has work and a child. |
| `dormant` | No spec-marker milestone, ready issue or task. The child, if any, goes after `dormantAfter`. |
| `excluded` | `exclude` matches, the project is archived, or `include` no longer matches a project that has a child. |
| `explicit` | An AgentProject the set does not own watches it, named in the reason. |
| `nameTaken` | The child's name belongs to an object the set does not own. |

A reason such as `removing: waiting for the project hook to be deleted` or
`kept: 1 open task` says what the set does with a child it would remove.

## Conditions

| Type | Status | Reason | Meaning |
|------|--------|--------|---------|
| `Ready` | True | `Ready` | The set discovers its group. |
| `Ready` | False | `InvalidSpec` | An empty group or include, or a glob that does not parse. |
| `Ready` | False | `InvalidTemplate` | The template has an unknown field or sets `gitlab`. |
| `Ready` | False | `WebhookRequired` | The template lacks `webhook.enabled`, `secretRef` or `url`. |
| `Ready` | False | `TooComplex` | The query's score passes 200. Split the set. |
| `Discovered` | False | `DiscoveryFailed` | The last discovery failed; the set keeps the last status and retries. |

## Children

A child is an AgentProject in the set's namespace, named by the slug of the
path below the group, cut to 50 characters plus a 6-character hash when
longer. It carries:

| Key | Meaning |
|-----|---------|
| label `ai.wiemers.me/project-set` | The set's name. |
| annotation `ai.wiemers.me/template-hash` | The template the child was last written from. |
| annotation `ai.wiemers.me/removing` | The set is removing the child: its webhook is off and it goes once the hook is gone. |

The set owns its children: deleting the set deletes them. On a child, and
on an AgentProject the set lists as `explicit`, the `WebhookDeliveries`
condition shows the reason `ViaProjectSet` while the set's discovery is
current: the set watches the project's milestones, edits included, and wakes
the AgentProject when they change. See
[`spec.webhook`](agentproject.md#specwebhook).

## Hooks on dormant projects

A dormant project has no child and so no hook of its own. The set registers
one there, for issue and milestone events only, with the template's
`webhook.url` and `webhook.secretRef`. A new milestone or ready issue then
wakes the set within seconds, and it creates the child at once. The set adds
at most three such hooks per poll and checks each about hourly.

The set deletes its hook when the child has registered its own, when the
project is excluded or archived, or when it leaves the group. Deleting the
set deletes the remaining hooks first: the finalizer
`ai.wiemers.me/project-hooks` holds the set until they are gone, or until a
hook failed three times. A new token in `webhook.secretRef` reaches these
hooks only when they are created anew.

## Metrics

`ai_operator_gitlab_requests_total{kind, owner, op}` counts GitLab requests.
`kind` is `rest` or `graphql`, `owner` the AgentProject or AgentProjectSet
that made them, `op` the client call. An idle set adds at most one `graphql`
request per 100 projects per poll.

`ai_operator_project_syncs_total{namespace, project, kind}` counts each
AgentProject's polls: `full`, `light` (milestones only) or `skipped` (the
set watches the milestones). `ai_operator_webhook_deliveries_total{event,
result}` counts deliveries: `accepted`, `heartbeat`, `set` (a set was woken)
or `refused`.

## Next

- [Project sets guide](../guides/project-sets.md): an example and the rules.
- [AgentProject](agentproject.md): the fields a template may hold.
