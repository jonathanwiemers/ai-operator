# Project sets

An [AgentProjectSet](../concepts/glossary.md#project-set) makes the operator
work in every project of a GitLab group, so you do not write one
[AgentProject](../concepts/glossary.md#agentproject) per repository. This
page shows a complete set, how its globs match, when a project gets its own
AgentProject and when it loses it, why webhooks are required, what a set
costs in GitLab requests, how the Desk shows it, and how you move an
explicit AgentProject into a set. Every field is in the
[AgentProjectSet reference](../reference/agentprojectset.md).

## An example

Every project of `my-group` and its subgroups, except the archive folder and
one project you keep out:

```yaml
apiVersion: ai.wiemers.me/v1alpha1
kind: AgentProjectSet
metadata:
  name: my-group
  namespace: agents
spec:
  gitlab:
    url: https://gitlab.com
    group: my-group
    tokenSecretRef: {name: gitlab, key: token}
  include:
    - my-group/**
  exclude:
    - my-group/archived/**
    - my-group/secrets
  template:
    agents:
      credentialsSecretName: agent-credentials
    milestones:
      specMarker: /agent spec
    maxConcurrentRuns: 1
    webhook:
      enabled: true
      url: https://ai-operator.example.com/hooks/gitlab
      secretRef: {name: gitlab-webhook, key: token}
```

The set lists the group's projects every two minutes. For each project that
has work it writes a child AgentProject from `template`, filling in
`gitlab` itself. The children are normal AgentProjects: they plan
milestones, run issues and show up in the Desk. The token needs the `api` and
`write_repository` scopes in every project of the group.

## Globs

`include` and `exclude` hold full project paths with two wildcards:

- `*` matches within one path segment: `my-group/shop-*` matches
  `my-group/shop-api`, not `my-group/shop/api`.
- `**` matches across segments: `my-group/**` is every project below
  `my-group`, however deep.

Matching ignores case. `exclude` wins over `include`. Archived projects are
always skipped. A glob that does not parse makes the set `Ready=False` with
the reason `InvalidSpec`.

The same globs work in the cluster's cross-repo allow-list, see
[Cross-repo changes](cross-repo-changes.md#the-three-gates). A template
`crossRepo` target that the set's `exclude` matches is dropped from every
child, and the set's status names it.

## Active and dormant

A project is **active** while it has work for the agents: an active milestone
whose description holds the template's spec marker, an issue with the ready
label, or an AgentTask of its child.
Only active projects get a child. Any other matched project is
[dormant](../concepts/glossary.md#dormant-project).

A child that has had no work for `dormantAfter` and has no AgentTask is
removed. The set first switches the child's webhook off, waits until GitLab's
hook is gone, and only then deletes the child. A child with a task stays, and
a project that turns active again gets its child back on the next poll. A
child whose project leaves the set's scope (an edited `include` or
`exclude`, an archived project) is removed without the wait.

An `/agent` comment on an issue in a project without a child is not seen.
Label the issue with the ready label, or add a milestone with the spec
marker, and the project turns active within one poll.

## Explicit AgentProjects win

If an AgentProject that the set does not own already watches a project (the
same `spec.gitlab.project`, any namespace, without case), the set skips it
and shows it as `explicit`. That AgentProject keeps its settings.

If the child's name belongs to an object the set does not own, the set skips
the project and shows it as `nameTaken` with the name in the reason. Rename
the other object or write an explicit AgentProject for that project. Child
names are the slug of the path below the group: `devops/functions/pnpm` in
`my-group` is `devops-functions-pnpm`.

## Why webhooks are required

A set child does not poll GitLab on its own. The set watches every project's
milestones in its own request; when they change it wakes the child, and a
webhook delivery wakes it for everything else. So the template needs
`webhook.enabled`, `webhook.secretRef` and `webhook.url`; without one, the
set is `Ready=False` with the reason `WebhookRequired` and writes no child.
See [Webhook](../getting-started/install.md#webhook-optional) for the
receiver.

While the set's discovery is current, a child full-syncs on a delivery, on a
wake from the set and every `webhook.fallbackInterval`. If discovery goes
stale, the children poll like explicit AgentProjects.

## The request budget

The bot has about 100 GitLab requests a minute. A set discovers with GraphQL:
one request per 100 projects per poll, and it carries the milestones and
ready issues too. An idle group of 112 projects costs two requests every two
minutes, not one sync per project.

Watch it with the metric `ai_operator_gitlab_requests_total`, labelled by
`kind` (`rest` or `graphql`) and `owner` (the set or project name). GitLab
refuses queries that are too complex. When the query's score passes 200, the
set is `Ready=False` with the reason `TooComplex`: split the group into two
sets.

## In the Desk

The project list groups the children under their set and shows the set's
counts and its last discovery. A set whose `Ready` or `Discovered` condition
is false shows the reason in the warning tone. In Kubernetes,
`kubectl -n agents get agentprojectset` (short name `aps`) shows the same
counts.

## Move an explicit AgentProject into a set

1. Wait until the project's tasks are done: deleting an AgentProject deletes
   its tasks.
2. Delete the explicit AgentProject. Its GitLab hook stays behind; the set's
   child registers its own, so delete the old one in the project's settings.
3. The set picks the project up on its next poll, if it has work.

## Next

- [AgentProjectSet reference](../reference/agentprojectset.md): every field,
  the status and the conditions.
- [Cross-repo changes](cross-repo-changes.md): globs in the allow-list.
- [Run a milestone on autopilot](autopilot.md): what a child does.
