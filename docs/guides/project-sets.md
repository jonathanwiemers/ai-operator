# Project sets

An [AgentProjectSet](../concepts/glossary.md#project-set) makes the operator
work in every project of a GitLab group, so you do not write one
[AgentProject](../concepts/glossary.md#agentproject) per repository. This
page shows a complete set, how its globs match, when a project gets its own
AgentProject and when it loses it, why webhooks are required, what a set
costs in GitLab requests, how the Desk shows it, how you give one project its
own settings, and how you hand an explicit AgentProject to the set. Every
field is in the
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
label, an AgentTask of its child, or a
[plan](write-a-milestone.md#draft-it-in-the-desk) in it.
Only active projects get a child. Any other matched project is
[dormant](../concepts/glossary.md#dormant-project).

A project whose only open milestones are settling counts as idle. Settling
means the autopilot is done and only waits for the retrospective's
follow-ups. Its tasks and spec markers do not keep it active, a ready issue
or a plan does. Its child keeps its tasks, so it is not removed while the milestone
settles. During that time the child syncs on a delivery, on a wake from the
set and every 15 minutes, or every `webhook.fallbackInterval` if that is
longer.

A child that has had no work for `dormantAfter` and has no AgentTask is
removed. The set first switches the child's webhook off, waits until GitLab's
hook is gone, and only then deletes the child. A child with a task or a plan
stays, so its plans are never deleted with it. Once the last plan is deleted,
by you or by its retention, `dormantAfter` counts as before. A project that
turns active again gets its child back on the next poll. A
child whose project leaves the set's scope (an edited `include` or
`exclude`, an archived project) is removed without the wait.

An `/agent` comment on an issue in a project without a child is not seen.
Label the issue with the ready label, or add a milestone with the spec
marker, and the project turns active within one poll.

To plan in a dormant project, start a plan in the Desk and pick the project
under its set. The new plan wakes the set, which creates the child within
seconds; the plan waits for it, see
[Pick the project](write-a-milestone.md#pick-the-project).

## Settings for one project

Most projects of a group want the same settings. When one needs its own,
name it in `spec.projects` as a
[project entry](../concepts/glossary.md#project-entry). You write only what
differs from the template:

```yaml
spec:
  template:
    maxConcurrentRuns: 1
    # … the rest of the template above
  projects:
    - path: my-group/shop
      name: shop
      spec:
        maxConcurrentRuns: 2
        agents:
          credentialsSecretName: shop-credentials
```

The child of `my-group/shop` is called `shop`. It gets the whole template,
with `maxConcurrentRuns` and the credentials Secret replaced. Objects merge
key by key, a `null` removes a template key, and a list replaces the
template's list.

A named project always has a child, with or without work: it is never
dormant. `exclude` and archiving still win, and the status says
`named in spec.projects, but …` with the cause. A typo in an entry, such
as an unknown field or a path outside the group, stops the whole set with
`Ready=False`, so a broken entry never writes a child with wrong settings.
The [reference](../reference/agentprojectset.md#specprojects) lists the
rules and messages.

## Explicit AgentProjects win

If an AgentProject that the set does not own already watches a project (the
same `spec.gitlab.project`, any namespace, without case), the set skips it
and shows it as `explicit`. That AgentProject keeps its settings. This
stays true for a project that `spec.projects` does not name. For a named
project the set can take the AgentProject over, see
[Hand an explicit AgentProject to the set](#hand-an-explicit-agentproject-to-the-set).

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
`kind` (`rest` or `graphql`), `owner` (the set or project name) and `op`
(the client call). GitLab
refuses queries that are too complex. When the query's score passes 200, the
set is `Ready=False` with the reason `TooComplex`: split the group into two
sets.

## In the Desk

The project list groups the children under their set and shows the set's
counts and its last discovery. A set whose `Ready` or `Discovered` condition
is false shows the reason in the warning tone. In Kubernetes,
`kubectl -n agents get agentprojectset` (short name `aps`) shows the same
counts.

## Hand an explicit AgentProject to the set

You do not delete the AgentProject: deleting it deletes its tasks. The set
adopts it instead and keeps its tasks, plans and GitLab hook. Say the
AgentProject `shop` in the set's namespace watches `my-group/shop`:

1. Add an entry with the settings `shop` has today. The set writes this
   spec onto it, so leave nothing out:

   ```yaml
   projects:
     - path: my-group/shop
       name: shop
       spec:
         maxConcurrentRuns: 2
   ```

   The `name` must be the AgentProject's name.
2. Annotate the AgentProject with the set's name, in its manifest:

   ```yaml
   metadata:
     name: shop
     annotations:
       ai.wiemers.me/project-set: my-group
   ```

3. Stop your GitOps tool from pruning it, or it deletes the object when you
   remove the manifest. With Flux, add the annotation
   `kustomize.toolkit.fluxcd.io/prune: disabled` as well.
4. Check the set's status. After the next poll the project's entry reads
   `adopted from AgentProject agents/shop`, and `named in spec.projects`
   from then on. If it stays `explicit`, the reason says what is missing,
   for example another AgentProject on the same path.
5. Remove the AgentProject's manifest from your repository.

## Remove an entry

Delete the entry from `spec.projects`. On the next poll the child gets the
template without the entry's settings. From then on it follows the
[dormant rules](#active-and-dormant): without work it goes after
`dormantAfter`, with an open task or a plan it stays.

## Next

- [AgentProjectSet reference](../reference/agentprojectset.md): every field,
  the status and the conditions.
- [Cross-repo changes](cross-repo-changes.md): globs in the allow-list.
- [Run a milestone on autopilot](autopilot.md): what a child does.
