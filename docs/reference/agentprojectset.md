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
| `projects` | [Project entries](../concepts/glossary.md#project-entry): projects the set names, each with its own settings. See [`spec.projects`](#specprojects). | none |
| `pollInterval` | How often the set discovers the group. | `2m` |
| `dormantAfter` | How long a child stays without work before the set removes it. | `168h` |

The API server defaults the children's fields as for any AgentProject. A
change to `template` or `projects` reaches the children on the next poll.

## `spec.projects`

Each entry names one project of the group and gives its child its own
settings:

```yaml
spec:
  template:
    maxConcurrentRuns: 1
    milestones:
      specMarker: /agent spec
    release:
      mode: "Off"
    webhook:
      enabled: true
      url: https://ai-operator.example.com/hooks/gitlab
      secretRef: {name: gitlab-webhook, key: token}
  projects:
    - path: my-group/shop
      name: shop
      spec:
        maxConcurrentRuns: 2
        release:
          mode: null
    - path: my-group/tools/cli
```

The child of `my-group/shop` is called `shop`, runs two issues at once and
drops the template's `release.mode`, so it releases with the AgentProject
default. The child of `my-group/tools/cli`
gets the template as it is; the entry only keeps the project active.

| Field | Meaning | Default |
|-------|---------|---------|
| `path` | The project's full path below `gitlab.group`. No globs. Each path appears once, without case. | – |
| `name` | The child's name, a DNS label of at most 63 characters, unique in the list. It applies when the child is created; an existing child keeps its name. | the slug of the path, as for any child |
| `spec` | Any AgentProject `spec` field except `gitlab`, merged onto `template`. | empty: the template as it is |

The set merges `spec` onto `template` as a JSON Merge Patch (RFC 7386):

- Objects merge key by key, at every depth.
- `null` deletes the template's key.
- Lists and plain values replace the template's: an entry's
  `crossRepo.targets` is the whole list.

The set then decodes the result as strictly as the template. The merged
spec still needs `webhook.enabled`, `webhook.secretRef` and `webhook.url`.

A named project is never dormant: it has a child whether or not it has
work, and that child is not removed after `dormantAfter`. `include`,
`exclude` and archiving still apply. When two projects would get the same
child name, the named one gets it.

A broken entry stops the whole set, like a broken template:

| Reason | Message, for example |
|--------|----------------------|
| `InvalidSpec` | `projects[0]: path is empty`, `projects[1]: path "my-group/*" is a glob, name one project`, `path "other/x" is not below gitlab.group "my-group"`, `path "my-group/shop" is already projects[0]`, `name "Shop": …` (no DNS label), `name "shop" is already projects[0]` |
| `InvalidTemplate` | `projects[1].spec: json: unknown field "maxRuns"` (refused even with a `null` value), `projects[1].spec.gitlab is set by the set`, or the merged spec does not decode |
| `WebhookRequired` | `projects[1]: set children need webhooks: webhook.url is not set` |

Remove an entry and its child gets the template on the next poll. From
then on the dormant rules apply: without work it goes after `dormantAfter`.

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
| `active` | The project has work and a child. An AgentPlan whose `spec.projectRef` names the child counts as work, also when the child does not exist yet. |
| `dormant` | No spec-marker milestone, ready issue, task or plan. The child, if any, goes after `dormantAfter`. A project whose only open milestones are settling (`activeMilestones` 0, `settlingMilestones` above 0 on the child) counts as idle: only a ready issue or a plan makes it active. |
| `excluded` | `exclude` matches, the project is archived, or `include` no longer matches a project that has a child. |
| `explicit` | An AgentProject the set does not own watches it, named in the reason. |
| `nameTaken` | The child's name belongs to an object the set does not own. |

A reason such as `removing: waiting for the project hook to be deleted` or
`kept: 1 open task` says what the set does with a child it would remove.
The set never removes a child that has an open task or a plan; a plan
counts until it is deleted by hand or by the operator's plan retention. The
`kept:` reason names both, followed by why the set would remove the child:

- `kept: 1 plan`: an active entry whose only work is its plan.
- `kept: 1 open task, 2 plans`
- `kept: 2 plans; not matched by include`
- `kept: 2 open tasks, 1 plan; no work for 168h`

Reasons of a project that `spec.projects` names:

| Reason | Meaning |
|--------|---------|
| `named in spec.projects` | `active`: the child is written from the entry. |
| `named in spec.projects; the child keeps its name shop-api, name shop applies only to a new child` | The entry's `name` differs from the existing child's. |
| `adopted from AgentProject agents/shop` | The set adopted that AgentProject in this poll. From the next poll on the reason is `named in spec.projects`. |
| `named in spec.projects, but archived` | No child. The tail is any reason an unnamed project would have: `matches exclude …`, `not matched by include`, `not found in the group` (state `dormant`), `watched by AgentProject …`. |

A named project that an AgentProject outside the set watches stays
`explicit` until the set can adopt that AgentProject. The reason then ends
with what is missing:

- `; annotate it with ai.wiemers.me/project-set=my-group to hand it to the set`
- `; not adopted: annotated for set other-set`
- `; not adopted: in namespace team-a, the set is in agents`
- `; not adopted: controlled by AgentProjectSet/other-set`
- `; not adopted: also watched by team-a/shop-copy`
- `; not adopted: name shop-old, the entry's child name is shop`

Adoption keeps the object: the set becomes its controller, adds the label
and writes the entry's spec onto it. Its tasks, plans and GitLab hook stay.

An AgentPlan created by the Desk for a dormant entry carries the label
`ai.wiemers.me/project-set` with the set's name. Its creation wakes the set,
which discovers at once instead of waiting for `pollInterval`.

## Conditions

| Type | Status | Reason | Meaning |
|------|--------|--------|---------|
| `Ready` | True | `Ready` | The set discovers its group. |
| `Ready` | False | `InvalidSpec` | An empty group or include, a glob that does not parse, or a broken `projects` entry. |
| `Ready` | False | `InvalidTemplate` | The template or an entry's `spec` has an unknown field or sets `gitlab`. |
| `Ready` | False | `WebhookRequired` | The template, or an entry's merged spec, lacks `webhook.enabled`, `secretRef` or `url`. |
| `Ready` | False | `TooComplex` | The query's score passes 200. Split the set. |
| `Discovered` | False | `DiscoveryFailed` | The last discovery failed; the set keeps the last status and retries. |

## Children

A child is an AgentProject in the set's namespace, named by the slug of the
path below the group, cut to 50 characters plus a 6-character hash when
longer, unless its entry in `spec.projects` gives a `name`. It carries:

| Key | Meaning |
|-----|---------|
| label `ai.wiemers.me/project-set` | The set's name. |
| annotation `ai.wiemers.me/template-hash` | The spec the child was last written from: the template, merged with its entry if it has one. |
| annotation `ai.wiemers.me/removing` | The set is removing the child: its webhook is off and it goes once the hook is gone. |
| annotation `ai.wiemers.me/project-set` | On an AgentProject the set does not own: the name of the set it is handed to. The set adopts it as the child of its entry in `spec.projects`. |

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
