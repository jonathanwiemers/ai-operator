# Install

This page takes you from an empty cluster to an operator that watches your
GitLab project. You prepare GitLab, install the operator with Helm (or
Flux), create two Secrets, connect your project with an
[AgentProject](../concepts/glossary.md#agentproject) and add the database.
Then the next page turns on the [Desk](../concepts/glossary.md#desk), the
web UI where you steer the agents.

## What you need

- **A Kubernetes cluster** with a default StorageClass. Every
  [task](../concepts/glossary.md#task) gets a volume for its checkout. You
  need `kubectl` and `helm`, or Flux if you manage the cluster from Git.
- **A GitLab project**, on gitlab.com or your own GitLab. You need the
  Maintainer role on it for the setup.
- **A bot user with a token.** The operator works in GitLab as this user:
  it comments, sets labels, pushes branches and opens merge requests. A
  project access token gives you both, see below.
- **A Claude or Cursor subscription** for the agents: a Claude Pro or Max
  plan, a Claude API key, or a Cursor plan. The agents run the Claude Code or
  Cursor CLI with it.

## Set up GitLab

### The bot user and its token

In your project, open *Settings > Access tokens* and add a **project access
token**:

- Role: **Developer**.
- Scopes: **`api`** and **`write_repository`**.

GitLab creates a bot user for the token. Copy the token (it starts with
`glpat-`); you need it for the Secrets below.

Protect your default branch. The agents only push `agent/*` branches and
open draft merge requests. On [autopilot](../concepts/glossary.md#autopilot)
the operator merges them itself, so allow the bot's role to merge: under
*Settings > Repository > Protected branches*, set *Allowed to merge* of the
default branch to *Developers + Maintainers*, or give the token the
Maintainer role. The Maintainer role also lets the operator register a
webhook for you (see [Webhook](#webhook-optional)).

### Labels

The operator shows the state of each issue with scoped labels such as
`agent::ready` and `agent::review`. Create them once. This loop creates the
state labels and the labels the operator sets, with your own token (`api`
scope, Maintainer):

```sh
export GITLAB_TOKEN=glpat-…          # your own token, not the bot's
PROJECT=my-group%2Fshop              # the project path, with / as %2F
for label in agent::draft agent::ready agent::working agent::needs-input \
  agent::review agent::blocked agent::done \
  agent-blocked::infra agent-blocked::main-red agent-blocked::quota \
  agent-blocked::gitlab \
  agent-profile::fast agent-profile::strong \
  ai-generated ci::main-red ci::flaky ci::main-canceled; do
  curl -fsS -o /dev/null -X POST -H "PRIVATE-TOKEN: $GITLAB_TOKEN" \
    "https://gitlab.com/api/v4/projects/$PROJECT/labels" \
    --data-urlencode "name=$label" --data-urlencode "color=#6699cc"
done
```

What each label means, and which ones you set yourself, is in
[Labels](../reference/labels.md). The repository also has a
[label script](https://github.com/jonathanwiemers/ai-operator/blob/main/hack/sandbox/gitlab-setup.sh)
that sets colours and descriptions and can create the labels for a whole
group.

### Board

A board shows every issue's state at a glance. It is optional, since the
Desk shows the same states, but it helps when you work in GitLab. In your
project, open *Plan > Issue boards*, create a board named **Agents**, and
add one list per label in this order: `agent::draft`, `agent::ready`, `agent::working`,
`agent::needs-input`, `agent::review`, `agent::blocked`, `agent::done`.
Moving a card between lists changes its label. The
[board script](https://github.com/jonathanwiemers/ai-operator/blob/main/hack/gitlab-board.sh)
in the repository does the same, for a project or a group. A group board
shows the issues of every project in the group, but only for group labels:
if your projects already have the labels as project labels, promote them
first (*Manage > Labels > Promote*), or each name exists twice and the board
misses those projects' issues.

A [webhook](#webhook-optional), which wakes the operator within seconds, is
optional too. You add it at the end of this page.

## Install with Helm

The chart installs the operator, its custom resources and the service
account the agent pods use. Agent pods run in the namespace of their
AgentProject; this example uses `agents`. Keep your chart values in a file,
`values.yaml`, since the next steps add to it:

```yaml
agent:
  namespaces: [agents]
  createNamespaces: true
```

```sh
helm upgrade --install ai-operator oci://ghcr.io/jonathanwiemers/charts/ai-operator \
  --namespace ai-operator-system --create-namespace -f values.yaml
```

Run the same command again whenever you change `values.yaml`.
Without `--version`, Helm takes the newest release. To pin one, add
`--version` with a version from the
[releases page](https://github.com/jonathanwiemers/ai-operator/releases).
Versions are calendar versions such as `2026.11.2`, and the chart, the
operator and the agent image always share one.

Check that the operator runs:

```sh
kubectl -n ai-operator-system get pods
```

Every namespace that hosts an AgentProject must be listed in
`agent.namespaces`, so the chart creates the agents' service account there.
All chart values, with their defaults, are in the
[chart README](../../charts/ai-operator/README.md#values).

To upgrade, run the same command with a new `--version`; the custom
resource definitions come with the chart. [Upgrade](../guides/upgrade.md)
lists what each release removed and what to delete from your files.

## Install with Flux

If Flux manages your cluster, install the same chart with an
`OCIRepository` and a `HelmRelease`:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: ai-operator-system
---
apiVersion: source.toolkit.fluxcd.io/v1        # v1beta2 on Flux before 2.6
kind: OCIRepository
metadata:
  name: ai-operator
  namespace: ai-operator-system
spec:
  interval: 1h
  url: oci://ghcr.io/jonathanwiemers/charts/ai-operator
  ref:
    semver: ">=2026.10.1"      # follows new releases; or tag: <version> to pin
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

A few things to know:

- The custom resource definitions are part of the chart, so Flux installs
  and upgrades them with the release. They stay when you remove the release.
- Put your AgentProject and its Secrets (for example encrypted with SOPS)
  into a second Flux `Kustomization`. Give it `dependsOn` on the one above,
  and give the operator's `wait: true`, so the definitions exist before your
  project is applied.
- Keep tasks out of Git. The operator creates and deletes them.
- Change AgentProject settings in Git. Flux reverts edits you make with
  `kubectl`.

## Create the Secrets

The operator needs two Secrets in the namespace of your AgentProject: the
bot's GitLab token, and the credentials of the agents.

```sh
kubectl -n agents create secret generic gitlab --from-literal=token=glpat-…

kubectl -n agents create secret generic agent-credentials \
  --from-literal=CLAUDE_CODE_OAUTH_TOKEN="…"
```

- **`gitlab`** holds the project access token. The operator and the
  [runner](../concepts/glossary.md#runner)
  next to each agent use it. The model never sees it.
- **`agent-credentials`** holds whatever your [engines](../concepts/glossary.md#engine)
  need. Each key becomes an environment variable of the agent pods:

| Key | Engine and billing | How to get it |
|-----|--------------------|---------------|
| `CLAUDE_CODE_OAUTH_TOKEN` | `claude`, billed to your Claude Pro or Max plan | run `claude setup-token` |
| `ANTHROPIC_API_KEY` | `claude`, billed to the Claude API | the Claude Console |
| `CURSOR_API_KEY` | `cursor`, billed to your Cursor plan | the Cursor dashboard |

Add one `--from-literal` per key you use. The default agents run on
`claude`; the [Agents and models](../reference/agents-and-models.md) page
shows how to use Cursor instead.

## Connect your project

An AgentProject connects one GitLab project to the operator. Save this as
`shop.yaml`, with your project path and your GitLab username:

```yaml
apiVersion: ai.wiemers.me/v1alpha1
kind: AgentProject
metadata:
  name: shop
  namespace: agents
spec:
  gitlab:
    project: my-group/shop
    tokenSecretRef: {name: gitlab, key: token}
  agents:
    credentialsSecretName: agent-credentials
  notify:
    users: [alice]              # the owners: GitLab usernames, without @
```

`notify.users` names the project's [owners](../concepts/glossary.md#owner):
the people the operator mentions when it needs a human. A milestone can
name its own owners; you will do that on the
[first milestone](first-milestone.md). Every field you leave out takes its
default, listed in the [AgentProject reference](../reference/agentproject.md).

Apply it and watch the first sync:

```sh
kubectl apply -f shop.yaml
kubectl -n agents get agentprojects
```

`LAST SYNC` gets a time after the first poll. If it stays empty, the conditions
say why, for example a wrong token or project path:

```sh
kubectl -n agents describe agentproject shop
```

If your repository needs more than Node to build and test, the agents
install the toolchains it pins, or you use your
[own agent image](../reference/agent-image.md).

## Add the database

The Desk keeps some data in a Postgres database: your answers to
[prompts](../concepts/glossary.md#prompt) and your chat with the agents, the
history of each milestone, and what every agent call cost. Budgets and
research runs (questions that agents answer once or on a schedule) need it
too. The operator runs without it, but the Desk then cannot take your
answers, so add it before you turn on the Desk.

You provide the database; the operator creates its tables. It needs three
logins, each in a Secret with the keys `username` and `password` in the
release's namespace:

- the **owner**, for the operator: it owns the database;
- the **reader**, for the Desk: it only reads;
- the **chat writer**, for the Desk: it only adds your messages and answers.

The Zalando postgres-operator creates such a database and the Secrets from
one resource. If you use it, this works as is:

```yaml
apiVersion: acid.zalan.do/v1
kind: postgresql
metadata:
  name: ai-operator-postgres
  namespace: ai-operator-system
spec:
  teamId: ai-operator
  numberOfInstances: 1
  volume: {size: 5Gi}
  postgresql: {version: "17"}
  users:
    research_owner: []
    research_reader: []
    research_chat_writer: []
  databases:
    ai_operator: research_owner
```

Then add these values to your `values.yaml` (or under `values` in the
`HelmRelease`) and run the Helm command again:

```yaml
research:
  database:
    host: ai-operator-postgres.ai-operator-system.svc.cluster.local
    name: ai_operator
    ownerSecret: {name: research-owner.ai-operator-postgres.credentials.postgresql.acid.zalan.do}
    readerSecret: {name: research-reader.ai-operator-postgres.credentials.postgresql.acid.zalan.do}
    readerRole: research_reader
    chatWriterRole: research_chat_writer
```

With another Postgres, create the database, the three logins and their
Secrets yourself, and set the role names to match. The connection must use
TLS. The operator grants each role its rights when it starts.

The next page connects the Desk to the reader and the chat writer. All
database values, with their defaults, are in the
[chart README](../../charts/ai-operator/README.md#research). Research runs
need a fourth login, the writer; [Research](../guides/research.md#turn-it-on)
explains it.

## Webhook (optional)

Without a webhook, the operator notices a comment, a label or a pipeline on
its next poll. With a project webhook it reacts within seconds and polls
less, which saves the bot's API budget. Polling stays the fallback, so a
lost delivery is picked up later. Skip this for a first try.

1. **Create the secret token** in the namespace of your AgentProject:

   ```sh
   kubectl -n agents create secret generic gitlab-webhook \
     --from-literal=token="$(openssl rand -hex 32)"
   ```

2. **Make the receiver reachable.** The operator serves `POST /hooks/gitlab`
   on port 8082 behind the Service `ai-operator-webhook` (for a release named
   `ai-operator`). The chart does not expose it, so add a route. With the
   Gateway API:

   ```yaml
   apiVersion: gateway.networking.k8s.io/v1
   kind: HTTPRoute
   metadata:
     name: ai-operator-webhook
     namespace: ai-operator-system
   spec:
     parentRefs:
       - {name: public, namespace: gateway-system, sectionName: https}
     hostnames: [ai-operator.example.com]
     rules:
       - matches:
           - path: {type: PathPrefix, value: /hooks/gitlab}
         backendRefs:
           - {name: ai-operator-webhook, port: 8082}
   ```

3. **Turn it on** in your `shop.yaml` and apply it again:

   ```yaml
   spec:
     webhook:
       secretRef: {name: gitlab-webhook, key: token}
       enabled: true
       url: https://ai-operator.example.com/hooks/gitlab
   ```

   With `enabled` and `url`, the operator creates the project hook itself
   and keeps it up to date. That needs the bot's Maintainer role. Without
   it, leave `enabled` and `url` out and add the hook by hand under
   *Settings > Webhooks*: the same URL and secret token, with the triggers
   push, issues, comments, merge requests and pipelines.

4. **Check it** with `kubectl -n agents describe agentproject shop`. The
   condition `WebhookRegistered` says `Registered`, or why not: `Forbidden`
   (the bot is no Maintainer), `Invalid` (no `url`, or the Secret is
   missing) or `Failed` (tried again on the next poll). `WebhookDeliveries`
   turns `Receiving` after the first delivery. In GitLab, the hook's *Recent
   events* show `202` for a delivery the operator took, `401` for a wrong
   token or a project the operator does not know, and `413` for a body over
   1 MiB.

The fields are in the [AgentProject reference](../reference/agentproject.md#specwebhook),
the receiver's values in the [chart README](../../charts/ai-operator/README.md#gitlab-webhooks).

## Next

- [Turn on the Desk](turn-on-the-desk.md): log in and see your project.
- [Labels](../reference/labels.md): what each label means.
- [AgentProject](../reference/agentproject.md): every field with its
  default.
