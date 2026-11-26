# Security

A coding agent runs code and reads text that strangers may have written:
issues, comments, the repository itself. This page explains what the model
can and cannot reach, what the Desk login protects, and where your secrets
live. Read it before you give the operator a GitLab token.

## The starting point

Assume the model can do anything a shell in its container can do, including
following instructions someone planted in an issue, a comment or the code.
The operator is built so that even then the model cannot get your GitLab
token or access to your cluster, and cannot make something that has them
run its code.

## Two containers per pod

Every [task](glossary.md#task) runs in its own pod with two containers (see
[How it works](how-it-works.md#the-trusted-runner-and-the-agent-container)):

| | The runner | The agent container |
|--|------------|---------------------|
| Trusted | yes | no |
| Runs | the operator's own code | the agent CLI and the model's commands |
| Holds | the GitLab token, the pod's access to the cluster | the model credentials, the checkout |
| Talks to | GitLab, the cluster | the model provider, whatever the network allows |

What the agent container does **not** have:

- **No GitLab token.** The model cannot comment, push, merge or read other
  projects in your name. The runner does all of that.
- **No cluster credentials.** Only the runner gets the pod's service account
  token. That account may read its project and the tasks, and write only
  their status. It cannot read Secrets or touch pods.
- **No look into the runner.** The containers do not share processes, so
  the model cannot read the runner's environment.

The model credentials (an Anthropic API key, a Claude subscription login or
a Cursor key) are the one secret in the agent container. Treat them as
exposed to the model: use credentials that only work for the model
provider, and set a spending limit there.

## Commits move as git bundles

Git can run commands from a repository's own settings: hooks, filters and a
few config options. The model can change those settings in its checkout.
So the runner never runs git in the agent's checkout:

- The runner keeps its own copy of the repository, which only it touches.
- Before a run, it hands new commits to the agent container as a git
  bundle, a file that carries commits and nothing else.
- After a run, the agent container hands its commits back the same way.
  Importing a bundle cannot run anything.
- Everything the agent returns, its report, its diff and its answers, is
  treated as untrusted data.

## What the runner may push and merge

The runner and the operator limit themselves, also when the bot account in
GitLab has wider rights:

- They push only commits to branches that start with `agent/`, never to the
  default branch, never with force, and no tags. (One exception: the runner
  may fold the agent's own checkpoint commits on its branch.) The only tags
  are the releases the operator creates through the GitLab API.
- They merge only in your project, or in a
  [companion repository](glossary.md#companion-repository) you allowed to
  merge, and on autopilot only after the merge gate.
- They delete only `agent/` branches.
- A model never decides to merge. The operator merges by rule: CI green,
  review clean, the head unchanged.

The [pilot](glossary.md#pilot) runs in a pod with the same two containers,
and it cannot merge, push or close issues either. Its actions go through the
operator, which checks each one before it runs it.

Still use a GitLab account with as few rights as possible, and protect the
default branch. Autopilot needs the right to merge into it.

## Names that could become options

Branch names, models and session IDs end up on the command lines of git and
the engines. The runner refuses a value that git or the engine could read as
an option, before anything runs:

- A branch name, including your default branch, must be one git accepts
  (`git check-ref-format --branch`) and must not start with `-`. A run on
  such a branch fails, and the error names the branch.
- A model, an effort or a session ID must start with a letter or a digit and
  hold no spaces. A role whose `model` is set to something like
  `--dangerously-skip-permissions` fails its runs
  ([agents and models](../reference/agents-and-models.md)). Allowed and
  denied tools must not start with `-`.

## The network

The pods run without root and without extra privileges. The chart can add a
network policy (`agent.networkPolicy.enabled`, with your exceptions in
`agent.networkPolicy.allowedCIDRs`):

- nothing can connect to the pods;
- the pods cannot reach cloud metadata endpoints or private networks, except
  the address ranges you allow (for example your Kubernetes API or a
  self-hosted GitLab).

The agent container can still reach the internet: the model providers,
package registries and GitLab. It needs them to work, and it has no GitLab
token to use there. If you want to restrict that further, for example to a
package mirror, use your cluster's own egress controls.

The implementer installs the toolchains your repository pins, from their
official sources. A change to a pin on the agent's branch only lasts for
that run. This is an instruction to the agent, not a sandbox: the model can
download and run what the network allows, as it can run your build.

## What the Desk login protects

The [Desk](glossary.md#desk) is optional and runs apart from the operator.

- **Login.** You log in with OpenID Connect at your own identity provider.
  The Desk serves without login only when you set `auth.mode: none`. Then
  it only listens on localhost inside its pod, and you reach it with
  `kubectl port-forward`. An unknown or broken auth setting stops the Desk
  from starting; it never falls back to no login.
- **Rules.** Access rules map the roles your identity provider gives you to
  projects. You see only the projects a rule grants you; without a rule you
  see nothing. A project set you may see lists only the projects you may see.
  What you may do beyond looking, such as reading transcripts,
  chatting or steering, is granted per rule too. Answering a milestone's
  prompts also needs your Desk login among its
  [owners](glossary.md#owner) (`/agent owner-subjects`).
- **No operator token.** The Desk never holds the operator's GitLab token
  or the agent credentials. When you connect your GitLab account, it reads
  threads and posts comments with your own token, in your name.
- **Little in the cluster.** The Desk's own account can read projects and
  tasks, and create milestone plans and, if you allow it, researches. It
  cannot change a task: it steers by posting comments and chat messages
  that the operator reads.
- **Sessions** live in encrypted cookies, not in a database.

## Who may command

By default, anyone who can comment on an issue can give `/agent` commands,
as in GitLab itself. With `workflow.commandsFrom: owners` only the
milestone's owners can. Owners named with `/agent owners` are checked by
their GitLab user ID, and the owners of a prompt or proposal are pinned when
it is posted, so a renamed account or a later owner cannot answer it in
GitLab.

In the Desk, only the Desk logins named with `/agent owner-subjects` may
answer a milestone's prompts. A milestone with `/agent owners` but no
`/agent owner-subjects` cannot be answered in the Desk.

## Where secrets live

All secrets are Kubernetes Secrets in your cluster. You name them in the
`AgentProject` or in the chart values:

| Secret | Named in | Who gets it |
|--------|----------|-------------|
| GitLab token | `spec.gitlab.tokenSecretRef` | the operator and the runner |
| Model credentials | `spec.agents.credentialsSecretName` | the agent container |
| Webhook secret | `spec.webhook.secretRef` | the operator |
| Token of a companion repository | `tokenSecretRef` of the target in `spec.crossRepo.targets` | the runner, only while the target is allowed |
| Desk login (OIDC client, session key) | `ui.oidc.existingSecret` | the Desk |
| Desk's GitLab app, or a fixed token for one user | `ui.gitlab.existingSecret` | the Desk |
| Research database logins | `spec.research.writerSecret`, chart values under `research.database` and `ui.chat` | the runner of a research pod, the operator, the Desk |

Nothing is stored in GitLab, and the model never sees a secret except its
own model credentials.

The model can read those credentials in its container, for example with
`env`, and repeat them. So the runner redacts them before text leaves the
pod. It replaces the credentials it holds, GitLab and Anthropic tokens by
their prefixes (`glpat-`, `sk-ant-`, …) and passwords in URLs with
`[redacted]`. This covers the logs you see in the Desk, failure comments,
the agent's answers and the CI logs it puts into prompts. The model can
still push a key in the code it writes, so keep your model credentials
scoped to this use.

## Webhook deliveries

A delivery only wakes the operator to sync a project now. It counts only
when its `X-Gitlab-Token` matches the webhook secret of an `AgentProject`, or
of an `AgentProjectSet` with a hook on that project, and when that project
is the one the delivery names. The token is compared in constant time.

- A delivery with a wrong, missing or empty token gets `401` before its body
  is read. So does every delivery to a project without a webhook secret, or
  with a missing or empty one.
- A body over 1 MiB gets `413`.
- A repeated delivery only wakes another sync. Commands are read from GitLab
  and run once per comment, however often a delivery arrives.

The projects of one `AgentProjectSet` share the set's webhook secret.

## Push notifications

With [push](../reference/notifications.md#push) on, the Desk server sends
notifications to your devices while no Desk tab is open.

- **What a push holds.** The Inbox item or the history event, as JSON, with
  its reason cut so the push stays small. It holds no token, no comment
  thread and no log.
- **Encrypted to the device.** Each push is encrypted with the device's own
  keys (`aes128gcm`, RFC 8291) and signed with the Desk's VAPID key
  (RFC 8292). The push service that carries it cannot read it.
- **Only known push services.** An endpoint must be `https` on a host of
  `ui.webPush.allowedHosts`, and the server follows no redirect. A user
  cannot make the Desk call another address (SSRF).
- **Who gets what.** A subscription stores your user name and roles from
  your last Desk start. The server checks the access rules for them on
  every send, so a device never gets a push for a project you cannot view.
  A device you don't open for 30 days loses its subscription, and turning
  notifications off or signing out deletes it.
- **The database login.** The Desk's database login reads every table and
  writes only the three push tables. The research and chat writers cannot
  touch them. The VAPID private key lives in the database, not in a
  Kubernetes Secret, so the Desk's account needs no write access to Secrets.
- **Logs.** The server logs counts, never endpoints or keys.

## Attachments

The planner and the implementer see files linked in the milestone, the
issue and comments (see
[Screenshots and files](../guides/write-a-milestone.md#screenshots-and-files)).
The runner fetches a link only when it is an upload of the agent's own
project: a relative `/uploads/…` in a Markdown link, or an absolute link of
the project's own URL or of `/-/project/<id>/uploads/`. It uses the bot
token. It fetches nothing else: no other project, no other host, no
repository file. Limits: [Plan attachments](../reference/plan-attachments.md).

The files land only in the agent's checkout, in a folder git ignores, so
they are never committed. The prompt marks them as untrusted data.

## Research runs

Researches read web pages, which may carry instructions too. A research pod
holds even less: no GitLab token at all, and its database login only in the
runner, which checks the agent's answer before it stores it. The Desk shows
the result as checked blocks, never as raw HTML.

## What is left

- The model can push anything to its own `agent/` branch and write anything
  into the comments it posts. Humans or the merge gate review the merge
  request before it merges.
- CI pipelines of agent branches run code the model wrote. Do not expose
  protected CI variables to unprotected branches.
- The model can read and send out whatever is in its checkout. Do not let
  it work on repositories that hold plaintext secrets.

## Next

- [Roles](roles.md): which agent does what.
- [How it works](how-it-works.md): the flow from spec to release.
- [Glossary](glossary.md): the terms on this page.
- [The full threat model](../contributing/design.md#security), for
  contributors and reviewers.
- [Reporting a vulnerability](../../SECURITY.md).
