# AgentProject

An [AgentProject](../concepts/glossary.md#agentproject) connects one GitLab
project to the operator: which milestones it plans, how it labels issues,
which agents work on them and when it asks you. This page shows a complete
example and then every `spec` field group with its default. Default values
are stated here and on the pages it links, and nowhere else.

The reference is split into four pages:

- **AgentProject** (this page): GitLab, milestones, autopilot, house rules,
  workflow and releases, notifications, the pilot, budgets, webhooks,
  cross-repo changes and the other fields.
- **[Agents and models](agents-and-models.md)**: `spec.agents`, the roles,
  subagents, the read gate, models and effort per issue, profiles,
  fallbacks and engines.
- **[Lifecycle and CI](lifecycle-and-ci.md)**: `spec.lifecycle`, `spec.ci`,
  `spec.checkpoint` and `spec.mainHealth`.
- **[Agent image](agent-image.md)**: the image the agents run in, and the
  network policy.

The Helm chart's values are in the
[chart README](../../charts/ai-operator/README.md#values).

## Example

A project that runs milestones on [autopilot](../concepts/glossary.md#autopilot),
two issues at a time, with Desk chat, budgets and an
[owner](../concepts/glossary.md#owner) list. Only `gitlab.project`,
`gitlab.tokenSecretRef` and `agents.credentialsSecretName` are required;
every field you leave out takes its default.

```yaml
apiVersion: ai.wiemers.me/v1alpha1
kind: AgentProject
metadata:
  name: shop
  namespace: agents
spec:
  gitlab:
    url: https://gitlab.com
    project: my-group/shop
    tokenSecretRef: {name: gitlab, key: token}
  milestones:
    specMarker: /agent spec
    autopilotMarker: /agent autopilot
  autopilot:
    maxParallel: 2
  houseRules:
    maxMRLines: 300
    constraints:
      - "Run `make test` before you push; CI runs it too."
  workflow:
    commandsFrom: owners
    inputs: [gitlab, desk]
    conventionalCommits: true
    release:
      mode: Auto
  agents:
    credentialsSecretName: agent-credentials
    resources:
      requests: {cpu: "1", memory: 2Gi}
      limits: {memory: 6Gi}
    chat: true                 # Desk chat, needs the research database
    implement:
      model: claude-opus-5-5
      readGate: {}
  ci:
    maxFixRounds: 2
  mainHealth:
    autoFix: true
  notify:
    users: [alice, bob]        # the owners when a milestone names none
  pilot:
    maxBudgetUSD: "20"
  budget:                      # needs the research database
    perIssueUSD: "20"
    perIssueAlertUSD: "15"
  maxConcurrentRuns: 3
```

The GitLab token and the agent credentials are Secrets in the same
namespace. How to create them is part of the installation, see
[Secrets](../getting-started/install.md#create-the-secrets).

## `spec.gitlab`

| Field | Meaning | Default |
|-------|---------|---------|
| `url` | The GitLab instance. | `https://gitlab.com` |
| `project` | The project's full path, e.g. `my-group/shop`. Required. | – |
| `tokenSecretRef` | `{name, key}` of the Secret with the project access token. Required. | – |

## `spec.milestones`

A [marker line](../concepts/glossary.md#marker-line) in a milestone's
description tells the operator to plan it.

| Field | Meaning | Default |
|-------|---------|---------|
| `specMarker` | Text a milestone description must contain to be planned. `""` plans every active milestone that has a description. | `/agent spec` |
| `autopilotMarker` | Text that plans a milestone and runs it end to end on autopilot. `""` turns autopilot off. The bot must be allowed to merge into the default branch. | `/agent autopilot` |
| `titlePattern` | A regular expression for milestone titles. When set, only matching milestones are planned, and issues are only picked up from them. | – |

A milestone with only the `specMarker` is planned when you create it, and
an owner reviews the issues. An autopilot milestone is planned when its
turn comes: when no older autopilot milestone of the project is still open,
or when an owner comments `/agent start`. Until then it only has a
[tracking issue](../concepts/glossary.md#tracking-issue). See
[Autopilot](../usage.md#autopilot-a-milestone-end-to-end).

## `spec.autopilot`

| Field | Meaning | Default |
|-------|---------|---------|
| `maxParallel` | How many issues of an autopilot milestone are worked on at the same time, at least 1. | `3` |

An issue starts once every issue it depends on is merged and fewer than
`maxParallel` issues are running. Held issues do not count, and above 1 the
issue that is landing does not count either. With `1`, one issue runs at a
time, and every problem of an issue pauses the milestone instead of holding
only that issue (see [When it pauses](../usage.md#when-it-pauses)).

A milestone sets its own limit with the line `/agent parallel N` (1 to 10).
An owner can also change it with `parallel N` on the tracking issue or with
the stepper in the Desk (see [Commands](commands.md#in-the-milestone-description)).
Any other value is ignored, and the project's value applies.

Merges stay one at a time, whatever the limit: the issues that are ready
wait in the order they became ready, and one of them is landing (rebase,
CI, merge). See [The merge gate](../usage.md#the-merge-gate).

Every running issue needs an agent run, and runs count against the
project's [`maxConcurrentRuns`](#other-spec-fields), as do chat, review and
fix runs. When a milestone's limit is above `maxConcurrentRuns`, the
tracking issue warns you, and the extra runs wait in the project's queue.
Set `maxConcurrentRuns` to at least `maxParallel` plus one.

## `spec.houseRules`

The rules the Desk [planner](../concepts/glossary.md#planner) writes into a
milestone draft's Constraints, and the planning agent sizes issues by. Each
merge request pays a fixed cycle of CI, review and rebase, so a larger
budget means fewer, larger merge requests and faster milestones. A
milestone's own Constraints win over these.

```yaml
spec:
  houseRules:
    maxMRLines: 300
    constraints:
      - "Run `make generate manifests` after changing API types; CI fails on stale files."
```

| Field | Meaning | Default |
|-------|---------|---------|
| `maxMRLines` | Size budget of one merge request in production lines. Tests, generated files and docs do not count. `0` states no budget. | `300` |
| `constraints` | More rules, one line each, e.g. a check CI runs. | – |

## `spec.workflow`

How the operator talks to GitLab: its labels, its commands, where it takes
instructions and how it commits.

```yaml
spec:
  workflow:
    readyLabel: "ai::ready"     # rename a label
    commandsFrom: owners
    inputs: [desk]              # take instructions in the Desk only
    stepCommits: true
    conventionalCommits: true
```

Labels and prefixes (see [Labels](labels.md) for what each label means):

| Field | Default |
|-------|---------|
| `draftLabel` | `agent::draft` |
| `readyLabel` | `agent::ready` |
| `workingLabel` | `agent::working` |
| `needsInputLabel` | `agent::needs-input` |
| `reviewLabel` | `agent::review` |
| `doneLabel` | `agent::done` |
| `blockedLabel` | `agent::blocked` |
| `blockedReasonPrefix` | `agent-blocked::` (reasons `infra`, `main-red`, `quota`, `dependency`) |
| `generatedLabel` | `ai-generated` |
| `profileLabelPrefix` | `agent-profile::` |
| `modelLabelPrefix` | `agent-model::` |
| `effortLabelPrefix` | `agent-effort::` |
| `commandPrefix` | `/agent` |
| `pilotPrefix` | `/pilot` |

Behaviour:

| Field | Meaning | Default |
|-------|---------|---------|
| `commandsFrom` | Who may give `/agent` commands: `members` (anyone who can comment) or `owners`. | `members` |
| `inputs` | Where you may give instructions: `gitlab` (comments), `desk`, or both. At least one. | both |
| `assignBot` | Assign the bot user to an issue it picks up that has no assignee, so GitLab lists it under **Ongoing**. A human assignee is never replaced. | `true` |
| `resolveConflicts` | Let the agent resolve merge conflicts with the default branch (see [Merge conflicts](../usage.md#merge-conflicts)). With `false` you resolve them; autopilot pauses on them. | `true` |
| `stepCommits` | While the merge request is a draft, the agent works in steps and pushes each one, so you can follow and steer it (see [The agent works](../usage.md#3-the-agent-works)). `false` makes one commit per run. | `true` |
| `conventionalCommits` | Commit messages and merge request titles follow Conventional Commits. | `false` |
| `commitTypes` | The allowed commit types, 1 to 20, lowercase. `chore` is always added. | `feat`, `fix`, `refactor`, `perf`, `test`, `docs`, `build`, `ci`, `chore`, `style`, `revert` |

**`commandsFrom: owners`.** An `/agent` comment on an issue, its merge
request, the tracking issue or a closed issue counts only when one of the
milestone's owners wrote it. The owners are the milestone's
`/agent owners` line, else `notify.users`, else the operator's default
owners. Anyone else's command starts and changes nothing, and gets one
reply in its thread (at most 10 a day per author).

**`inputs: [desk]`.** `/agent` and `/pilot` comments start and change
nothing. Each gets one reply, "This project takes instructions in the
Desk", with a link to the open prompt, the task or the milestone in the
Desk. Replies in a prompt's thread (option letters, `yes` or `no`) are not
read. The agent does not take comments as instructions, and its reports
tell you what to do in the Desk. A fresh session reads the issue, the
agent's own comments and notes, the merge request's comments and the
task's last 20 Desk chat messages instead of the issue's comment thread.
Labels, milestone planning and review threads on merge requests stay on
GitLab either way, and the operator still mirrors every prompt and answer
there. With `gitlab` in the list, owners may also answer a prompt with its
option's letter in the prompt's thread (see [Commands](commands.md)).

The links in these replies and prompts point to the Desk's address, the
chart's `ui.externalURL`. Without it, prompts say "Answer in the Desk." and
replies carry no link.

**`conventionalCommits: true`.** Messages look like
`type(scope): subject`, with a subject of at most 72 characters. A message
that does not match becomes `chore(agent): <first line, lowercased>`, and
the run report counts it. A new branch starts with
`chore: start work on #N`. The merge request title follows the same format
(with `Draft: ` while it is a draft), unless you renamed it by hand, and an
autopilot merge sends a matching squash commit message. See
[Commit messages](../usage.md#commit-messages).

### `spec.workflow.release`

When the operator starts a release on its own, see
[Releases](../usage.md#releases).

| Field | Meaning | Default |
|-------|---------|---------|
| `mode` | `Auto` or `Off`. With `Off` the operator never starts a release pipeline. Empty means `Auto`. | `Auto` |
| `minInterval` | The shortest time between two releases the operator starts. A milestone's final release does not wait for it. | `15m` |
| `maxUnreleasedAge` | Outside milestones, with no autopilot running or paused: release the default branch once its oldest unreleased change is this old and the branch is green (see [Outside milestones](../usage.md#outside-milestones)). | `24h` |

## `spec.notify`

Whom the operator mentions (`cc @alice @bob`) when something needs a human,
and asks for reviews. See [Owners and pings](../usage.md#owners-and-pings).

| Field | Meaning | Default |
|-------|---------|---------|
| `users` | GitLab usernames, without `@`. A milestone replaces them for itself and its issues with the line `/agent owners @alice @bob`. | the chart's `defaultOwners` |
| `events` | Which situations mention them: `failed`, `needsInput`, `ciFailed`, `review` (not on autopilot), `autopilotPaused`, `autopilotDone`, `mainRed`, `blocked`. | all |
| `requestReview` | When an issue reaches review (not on autopilot), make the users the reviewers of its merge request, unless it has some. | `true` |

The users must be members of the project to be notified. Accepting or
rejecting the pilot's proposals in the Desk needs the milestone line
`/agent owner-subjects alice@example.com bob` (the Desk's user names);
there is no project-wide fallback for it.

## `spec.pilot`

The [pilot](../concepts/glossary.md#pilot) watches each milestone and
answers its owners, see [Talking to the pilot](../usage.md#talking-to-the-pilot).
Its model is `agents.pilot` (see [Roles](agents-and-models.md#roles)).

| Field | Meaning | Default |
|-------|---------|---------|
| `mention` | A comment that starts with `@<bot>` talks to the pilot, like `/pilot`. | `true` |
| `maxBudgetUSD` | What the pilot may spend per milestone, as a decimal string. When it is used up, the pilot says so and assessments only report what happened. Cursor reports no cost, so on cursor it never runs out. | `"20"` |
| `autonomy` | `managed`: the pilot retries, re-reviews, instructs, opens issues, fixes flaky tests, retries pipelines and rebases on its own, within limits. `propose`: every action waits for an owner's `yes`. A set value applies to every milestone. | `managed` on autopilot, `propose` otherwise |
| `maxInterventionsPerIssue` | The pilot's own actions per issue and head when it manages, at least 1. Past it, it proposes and pings the owners. | `2` |
| `stuckAfter` | An autopilot pause that has not changed for this long is stuck: the pilot assesses it once and gives the owners options as a prompt. It never resumes the milestone on its own. | `30m` |

An owner's `/pilot stop` lowers a milestone to `propose`, and `/pilot go`
returns it to the configured value, never above it.

**Per milestone.** A milestone's rules (`/agent rule R<n>: switch
<setting>=<value>`, see [Commands](commands.md#in-the-milestone-description))
override four settings for its issues: `ci.maxFixRounds` (0 to 5),
`ci.retryFailedJobs`, `review.retries` and `review.maxIterations` (0 to 5).
Nothing else can be switched per milestone.

## `spec.budget`

Budgets of autopilot work, from what its agent calls cost. They need the
research database. The operator checks them between calls: a running call
is never killed, but the run makes no further call once its budget is used
up. See [Budget](../usage.md#budget).

```yaml
spec:
  budget:
    perIssueUSD: "20"
    perIssueAlertUSD: "15"
```

| Field | Meaning | Default |
|-------|---------|---------|
| `perIssueUSD` | Budget of every open issue of an autopilot milestone, e.g. `"20"`. When an issue's calls cost that much, autopilot pauses. `/agent budget <usd>` on the issue overrides it. `0` means none. | none |
| `perIssueAlertUSD` | Spend at which autopilot posts one heads-up on the tracking issue, without pausing. A new value arms it again. `0` means none. | none |

The same heads-up also comes at 80 % of an issue's own budget. A
milestone's budget in all is its description line `/agent budget <usd>`.

## `spec.webhook`

GitLab webhooks wake the operator within seconds instead of at the next
poll. See [Webhook](../getting-started/install.md#webhook-optional) for the setup. Without
`secretRef` every delivery is refused and the project is polled as before.

| Field | Meaning | Default |
|-------|---------|---------|
| `secretRef` | `{name, key}` of a Secret with the hook's secret token. A delivery counts only when its token matches. | – |
| `enabled` | Register the project hook in GitLab for `url`, and keep it in place. Needs the bot's Maintainer role. | `false` |
| `url` | The receiver's public URL, e.g. `https://ai-operator.example.com/hooks/gitlab`. Needed with `enabled`. | – |
| `fallbackInterval` | While deliveries arrive, how often the project is fully synced anyway. | `10m` |
| `silenceThreshold` | How long after the last delivery the project still counts as receiving. After it, every poll is a full sync again. | `30m` |

While deliveries arrive, steps that only time moves on (infra retries,
autopilot's auto-resume, the main health check) can be up to
`fallbackInterval` late. The conditions `WebhookRegistered` and
`WebhookDeliveries` on the AgentProject say how it goes. The receiver
itself is a chart setting (`webhook.*`, see the
[chart values](../../charts/ai-operator/README.md#gitlab-webhooks)).

## `spec.crossRepo`

The [companion repositories](../concepts/glossary.md#companion-repository)
an issue may change in the same run as its own project. See
[Companion repository](../usage.md#companion-repository). Three gates must
agree: the cluster allows the project, the AgentProject names it here, and
a milestone line `/agent repo <project> [merge]` (or an owner's
`/agent repo <project>` on the issue) gives it to an issue.

```yaml
spec:
  crossRepo:
    targets:
      - project: my-group/infra/cluster
        paths:
          - clusters/my-cluster/applications/shop/
        autoMerge: true
        # tokenSecretRef: {name: gitlab-infra, key: token}
```

`targets` (at least one):

| Field | Meaning | Default |
|-------|---------|---------|
| `project` | The companion's full path, compared without case. | – |
| `paths` | The paths a companion change may touch, at least one, written like `.gitignore` lines: `*` and `?` within a name, `**` across folders, a trailing `/` for a folder. No `!`, no `[ ]`, no `..`, no absolute paths. | – |
| `autoMerge` | Only with it may a milestone's `/agent repo <project> merge` let autopilot merge the companion's merge request. Otherwise the owners are asked to merge it. | `false` |
| `tokenSecretRef` | `{name, key}` of a Secret with a token for the companion project. The agent never sees it. | `spec.gitlab.tokenSecretRef` |

A target is dropped, and used nowhere, when the cluster does not allow its
project, when it is the home project or on the cluster's never-list, when
it repeats an earlier target, or when `paths` is empty, does not compile,
or covers `.gitlab-ci.yml`, `.gitlab/` or the whole tree. The condition
`CrossRepoReady` names each dropped target and why. The runner also refuses
a companion change outside `paths`, in `.gitlab-ci.yml`, `.gitlab/` or
`.gitmodules`, with a symlink or submodule, or with more than 1 MiB of new
files.

The cluster's side is set in the chart: `crossRepo.allowedProjects` (empty
turns cross-repo changes off), `crossRepo.neverProjects` and `selfProject`,
see the [chart values](../../charts/ai-operator/README.md#values).

## Other `spec` fields

| Field | Meaning | Default |
|-------|---------|---------|
| `pollInterval` | How often GitLab is polled. While webhooks arrive, most polls only list the milestones, see [`spec.webhook`](#specwebhook). | `1m` |
| `maxConcurrentRuns` | Runs in flight per project, at least 1. More runs queue: the main-red fix first, then review-only runs, then issues without a milestone, then the oldest milestone. Pilots do not count. Keep it low on subscriptions, and above `autopilot.maxParallel`. | `3` |
| `suspend` | Stop polling and starting runs. | `false` |
| `research` | Settings for research runs of this project: `writerSecret` (the database login) and `candidates`, see [Research](../research.md#specresearch-on-agentproject). | – |

## Next

- [Agents and models](agents-and-models.md): who does the work, on which
  model.
- [Lifecycle and CI](lifecycle-and-ci.md): pods, retries, CI and the
  default branch.
- [Commands](commands.md): the milestone lines that override these fields.
