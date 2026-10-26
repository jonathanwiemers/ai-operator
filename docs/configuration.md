# Configuration reference

An `AgentProject` connects one GitLab project. A complete example is in
[`config/samples/agentproject.yaml`](../config/samples/agentproject.yaml).
`kubectl explain agentproject.spec --recursive` shows the full schema.

## Installation

With Helm (recommended, see the [chart values](../charts/ai-operator/README.md)):

```sh
helm install ai-operator oci://ghcr.io/jonathanwiemers/charts/ai-operator \
  --version <YEAR.MONTH.N> \
  --namespace ai-operator-system --create-namespace \
  --set 'agent.namespaces={agents}' --set agent.createNamespaces=true
```

With Flux, see [Install with Flux](#install-with-flux) below.

Without Helm:

```sh
make deploy                          # CRDs, RBAC, operator in ai-operator-system
kubectl create namespace agents
kubectl apply -f config/samples/agent_serviceaccount.yaml   # service account for agent pods
```

Agent pods run in the namespace of their `AgentProject`. Every such namespace
needs the `ai-operator-agent` service account, bound to the agent ClusterRole
(read tasks and projects, patch `agenttasks/status`). The chart creates both
for every namespace in `agent.namespaces`.

### Install with Flux

The chart is an OCI artifact, so Flux can install it with an `OCIRepository`
and a `HelmRelease`:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: ai-operator-system
---
apiVersion: source.toolkit.fluxcd.io/v1        # v1beta2 on Flux < 2.6
kind: OCIRepository
metadata:
  name: ai-operator
  namespace: ai-operator-system
spec:
  interval: 1h
  url: oci://ghcr.io/jonathanwiemers/charts/ai-operator
  ref:
    tag: <YEAR.MONTH.N>        # or semver: ">=2026.10.3" to follow new releases
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

* The CRDs are regular chart templates, so they are installed and upgraded
  with the release. They are kept when the release is removed.
* Put your `AgentProject` and its secrets (e.g. SOPS-encrypted) into a second
  Flux `Kustomization` with `dependsOn` on the one above and `wait: true` on
  the operator's, so the CRDs exist before the project is applied.
* Keep `AgentTask`s out of Git. The operator creates and deletes them.
* Change `AgentProject` settings such as `spec.suspend` in Git; Flux reverts
  edits made with `kubectl`.

### Web UI

The chart can also install a read-only web UI with OIDC login and role-based
access per project (`ui.enabled`, off by default), exposed through a Gateway
API HTTPRoute. See [ui.md](ui.md).

## Secrets

```sh
kubectl -n agents create secret generic gitlab --from-literal=token=glpat-…

kubectl -n agents create secret generic agent-credentials \
  --from-literal=CLAUDE_CODE_OAUTH_TOKEN="…" \
  --from-literal=CURSOR_API_KEY="…"
```

* `spec.gitlab.tokenSecretRef` is the GitLab project access token (Developer,
  `api` + `write_repository`). The operator and the runner use it. The model
  never sees it.
* `spec.agents.credentialsSecretName` names a secret whose keys become
  environment variables of the agent pods. Add whatever your engines need:

| Key | Used by | How to get it |
|-----|---------|---------------|
| `CLAUDE_CODE_OAUTH_TOKEN` | `claude` engine, billed to your Claude Pro/Max subscription | run `claude setup-token` |
| `ANTHROPIC_API_KEY` | `claude` engine, billed to the Claude API | Claude Console |
| `CURSOR_API_KEY` | `cursor` engine, billed to your Cursor subscription | Cursor dashboard |

## `spec.gitlab`

| Field | Default | Description |
|-------|---------|-------------|
| `url` | `https://gitlab.com` | GitLab instance |
| `project` | – | full path, e.g. `my-group/shop` |
| `tokenSecretRef` | – | `{name, key}` of the access token |

## `spec.milestones`

| Field | Default | Description |
|-------|---------|-------------|
| `specMarker` | `/agent spec` | text that must appear in a milestone description for it to be planned; `""` plans every active milestone with a description |
| `autopilotMarker` | `/agent autopilot` | text that plans a milestone and runs it end to end: agents take their defaults, issues are worked one at a time and each MR is merged when CI is green and the review is clean (see [Autopilot](usage.md#autopilot-a-milestone-end-to-end)); `""` disables autopilot. The bot must be allowed to merge into the default branch |
| `titlePattern` | – | optional regex for milestone titles; if set, issues are also only picked up from matching milestones |

## `spec.workflow`

All labels can be renamed. Defaults:

| Field | Default |
|-------|---------|
| `draftLabel` | `agent::draft` |
| `readyLabel` | `agent::ready` |
| `workingLabel` | `agent::working` |
| `needsInputLabel` | `agent::needs-input` |
| `reviewLabel` | `agent::review` |
| `doneLabel` | `agent::done` |
| `blockedLabel` | `agent::blocked` |
| `blockedReasonPrefix` | `agent-blocked::` (reasons: `infra`, `main-red`, `quota`, `dependency`) |
| `generatedLabel` | `ai-generated` |
| `profileLabelPrefix` | `agent-profile::` |
| `modelLabelPrefix` | `agent-model::` |
| `effortLabelPrefix` | `agent-effort::` |
| `commandPrefix` | `/agent` |
| `pilotPrefix` | `/pilot` |
| `assignBot` | `true` |
| `resolveConflicts` | `true` |
| `stepCommits` | `true` |

`assignBot` assigns the bot user to an issue when the operator picks it up and
the issue has no assignee, so GitLab's milestone page lists it under
**Ongoing**. It is sent in the same update that adds the working label. An
issue that already has an assignee is left alone, so a human assignee is never
replaced. Set it to `false` to leave assignees untouched.

`resolveConflicts` lets the agent resolve merge conflicts: when the MR of an
issue in review conflicts with the default branch, the next run merges the
default branch into the work branch and the agent resolves the conflicts (see
[usage.md](usage.md#merge-conflicts)). On autopilot it is also used when GitLab
cannot rebase an MR. Set it to `false` to leave conflicts to humans; autopilot
then pauses on them.

`stepCommits`: while the MR is a draft, the implementer works in steps. Each
step is committed and pushed as it is done (without CI), and comments written
in the meantime reach the agent before its next step, so you can follow the
work on the MR and steer it (see [usage.md](usage.md#3-the-agent-works)). Set
it to `false` for one commit per run.

## `spec.agents`

| Field | Default | Description |
|-------|---------|-------------|
| `image` | operator's `--default-agent-image` (with Helm: the agent image of the same release) | agent image (see below) |
| `credentialsSecretName` | – | see Secrets |
| `serviceAccountName` | `ai-operator-agent` | |
| `resources` | – | resources of the agent container (agent CLIs, builds, tests). Size the memory for the project's build and tests plus the CLI (about 1Gi); a container over its limit is killed (`OOMKilled`); its limit is then raised up to `maxMemory` and the run starts again. Requests as high as the limits avoid kills when the node runs short |
| `maxMemory` | twice `resources.limits.memory` | cap for raising the agent container's memory limit after an OOM kill: each kill raises that task's limit by half and the run starts again (at most 3 times per run). Set it to the limit to turn raising off; without a memory limit nothing is raised |
| `runnerResources` | 50m / 128Mi requests, 1Gi limit | resources of the runner container (GitLab, git mirror) |
| `workspaceSize` | `10Gi` | per-task PVC |
| `storageClassName` | cluster default | |
| `cacheClaimName` | – | existing (ideally RWX) PVC mounted at `/cache` for shared dependency caches (`GOMODCACHE`, `GOCACHE`, `npm_config_cache`, `PIP_CACHE_DIR`, `YARN_CACHE_FOLDER`) |
| `env`, `nodeSelector`, `tolerations` | – | passed to the pod |
| `logLevel` | operator's `--agent-log-level` (with Helm: `agent.logLevel`, falling back to `logLevel`) | `debug`, `info` or `error` for the runner and agent containers; `info` logs every step, `debug` adds git commands. A running pod picks up a change when it is next started |
| `spec` | claude, `claude-opus-5-5` | spec (planning) agent |
| `profiles` | `fast`, `strong` | implementing agents selected per issue |
| `implement` | claude, `claude-opus-5-5` | implementing agent for issues without a profile label |
| `review` | claude, `claude-sonnet-5-5`, reviewers `bugbot` + `security`, `maxIterations: 2`, `retries: 2`, `retryAfter: 15m` | review agents |
| `describe` | claude, `claude-sonnet-5-5` | writes the MR description after each push that changed the diff; `disabled: true` keeps the placeholder |
| `pilot` | claude, `claude-sonnet-5-5` | the milestone's [pilot](usage.md#talking-to-the-pilot), who answers its owners; read-only, one resumed session per milestone |
| `models` | `claude-opus-5-5`, `claude-sonnet-5-5`, `claude-haiku-5-5` (aliases `opus`, `sonnet`, `haiku`) | engines and models an issue may switch its implementer to with `/agent use` or an `agent-model::` label; the models of the profiles and of `implement` are always allowed. Entries: `engine`, `model`, optional `alias` |
| `logPrompts` | `false` | log every prompt (role, system prompt, prompt, each cut at 64 KiB) in the agent container, for the web UI's [live transcript](ui.md#live-transcript). Prompts hold issue text, comments, diffs and CI logs |
| `maxEffort` | `high` | the highest effort an issue may ask for (`agent-effort::` label, `/agent effort`, the planner's choice); higher values are lowered. The roles' own `effort` is not capped |

If `resources` sets a memory limit or CPUs (limits, else requests), the
implementing agent is told them in its system prompt, with the advice to keep
builds and tests within them (few test workers, one heavy command at a time,
no watch modes or dev servers). Build tools and test runners size themselves
by the node, not the container, and a container over its memory limit is
killed together with the run.

### Roles

`spec`, `implement`, `review`, `describe`, `pilot` and every profile share these fields:

| Field | Engines | Description |
|-------|---------|-------------|
| `engine` | – | `claude` (default) or `cursor` |
| `model` | both | e.g. `claude-opus-5-5`, `claude-sonnet-5-5`; `auto` for cursor. Claude defaults: `claude-opus-5-5`, reviews `claude-sonnet-5-5`. Cursor uses its own default |
| `effort` | claude | `low` … `max` |
| `maxBudgetUSD` | claude | spend cap per invocation, e.g. `"5"` |
| `allowedTools` | claude | override the tool allow-list |
| `instructions` | both | appended to the role prompt (house rules, test commands) |

`review` additionally has `reviewers` (`bugbot`, `security`),
`maxIterations`, `retries` (calls again a reviewer that failed
transiently, e.g. an engine outage; `0` turns it off) and `retryAfter`
(how long [autopilot](usage.md#when-it-pauses) waits before it asks for
one re-review of a head whose reviewers all failed transiently); `describe`
has `disabled`.

### Per-issue model and effort

On top of its profile, an issue can pick the implementer's engine, model and
effort with labels, set by `/agent use` / `/agent effort` comments or by hand
(see [commands.md](commands.md#model-and-effort)). Only the models in
`agents.models` (plus those the profiles and `implement` use) are allowed:

```yaml
agents:
  models:
    - {engine: claude, model: claude-opus-5-5, alias: opus}
    - {engine: claude, model: claude-sonnet-5-5, alias: sonnet}
    - {engine: cursor, model: auto}
    - {engine: cursor, model: gpt-5.6-sol-high, alias: gpt}
  maxEffort: high
```

The planner sets an `agent-effort::` label on the sub-issues it creates when
it thinks an issue needs more or less thinking than its profile; a label you
set is kept on re-plans.

### Profiles

```yaml
profiles:
  - name: fast
    description: Small, well-defined changes that follow existing patterns.
    engine: cursor
    model: auto
  - name: strong
    description: Design decisions, cross-cutting or risky changes, unclear scope.
    engine: claude
    model: claude-opus-5-5
```

The spec agent sees the names and descriptions and labels each sub-issue it
creates with `agent-profile::<name>`. Write descriptions that let it decide.
When planning, an unknown choice gets no label. When implementing, a missing
or unknown label falls back to `implement`. A profile label set by hand is
never overwritten by a re-plan.

If no profiles are configured, `fast` (claude, `claude-sonnet-5-5`) and
`strong` (claude, `claude-opus-5-5`) are used.

### Engines

| | `claude` | `cursor` |
|-|----------|----------|
| CLI | `claude -p` | `cursor-agent -p` |
| structured output | `--json-schema` | schema in the prompt, JSON parsed from the answer |
| role instructions | `--append-system-prompt` | folded into the first prompt of a session |
| tool restrictions | `--allowedTools` / `--disallowedTools` | none via flags; read-only roles run without `--force` and the work tree is reset after them |
| session resume | `--resume` | `--resume` |
| cost reporting | yes (notional with a subscription) | no |
| live log of the session | thinking, messages, tool calls | messages, tool calls (no thinking in print mode) |

Sessions are stored per role and engine. Switching an issue to a profile with
another engine starts a new session with the full context.

## `spec.lifecycle`

> **Upgrading from a release with a longer `idleTimeout` default (`2h`,
> `1h`):** the API server stored the old default in every AgentProject that
> was created with a `lifecycle` block, and the sample sets it explicitly.
> Remove `idleTimeout` (or set it to `1m`) to stop pods right after each run.

| Field | Default | Description |
|-------|---------|-------------|
| `idleTimeout` | `1m` | after the last run, the pod is stopped; the workspace stays. A longer timeout keeps the pod (and its resource requests) for quick follow-ups, which saves only the pod start. After a failed run the pod stays at least 1h, so its logs can be read |
| `retentionPeriod` | `168h` (7 days) | after the last run, the workspace PVC is deleted |
| `maxRunsPerTask` | `20` | the task is `Blocked` when reached (runs paused by usage limits don't count) |
| `usageLimitBackoff` | `5m` | first wait after a usage or rate limit without a reported reset time; doubles per further hit |
| `maxUsageLimitBackoff` | `1h` | cap for that wait |
| `infraRetryAfter` | `30m` | after a run failed for an outside reason (agent pod or container lost, out of memory with no restart left, interrupted 3 times) the issue is blocked (`infra`); this long after the failure the operator starts one new run, once per run request. A planning run is retried the same way, also after a transient GitLab or network error |

## `spec.ci`

The CI feedback loop, see [usage.md](usage.md#ci-feedback).

| Field | Default | Description |
|-------|---------|-------------|
| `disabled` | `false` | move issues to review right after the push, without waiting for CI |
| `maxFixRounds` | `2` | CI fix runs per push you triggered, before the issue goes to needs-input |
| `retryFailedJobs` | `true` | retry the failed jobs once before the agent is involved |
| `noPipelineTimeout` | `10m` | no pipeline for the pushed commit within this time means "no CI" |

The GitLab token needs the `api` scope to read pipelines and job logs and to
retry jobs (Developer role).

The operator finds the pipeline by the pushed commit, which covers branch
pipelines and merge request pipelines. *Merged results* pipelines (GitLab
Premium) run on a merge commit instead and are not found; with them the loop
ends after `noPipelineTimeout` as "no CI". Set `ci.disabled: true` in that
case until this is supported.

## `spec.mainHealth`

The watcher of the default branch, see [usage.md](usage.md#main-health).

| Field | Default | Description |
|-------|---------|-------------|
| `disabled` | `false` | do not watch the default branch |
| `interval` | `5m` | how often the newest finished pipeline of the default branch is read (one `GET pipelines` per check; jobs and logs only for a new red pipeline) |
| `retryFailedJobs` | `true` | retry the failed jobs of a red pipeline once; green after the retry counts as flaky |
| `issue` | `true` | open the `ci::main-red` issue and one `ci::flaky` issue per flaky job |
| `freezeAutopilot` | `true` | autopilot merges nothing while the default branch is `red`: the issue that passed the gate is blocked (`main-red`) and autopilot continues by itself once the branch is green. A status not checked for 3 intervals does not freeze (see [merge freeze](usage.md#main-health)) |
| `autoFixFlaky` | `false` | hand a `ci::flaky` issue to the agent at its second occurrence, once: into the active autopilot milestone with the lowest IID (its queue), else without a milestone (`agent::ready`, also under `milestones.titlePattern`), with the operator's fixed task. Without it only the pilot's `fixFlaky` hands one over (see [fixing a flaky test](usage.md#main-health)) |
| `autoFix` | `false` | hand the `ci::main-red` issue to the agent (`agent::ready`), once per red episode, also under `milestones.titlePattern`. On autopilot, the frozen milestone merges the fix (the main-red lane); otherwise a human merges it (see [fixing a red branch](usage.md#main-health)) |

The token needs the same rights as for `spec.ci` (read pipelines and job logs,
retry jobs) and must be able to create issues.

## `spec.notify`

Whom the operator mentions (`cc @alice @bob`, appended to the comment that
needs a human) and asks for reviews, see
[usage.md](usage.md#owners-and-pings).

| Field | Default | Description |
|-------|---------|-------------|
| `users` | operator's `--default-owners` (with Helm: `defaultOwners`) | GitLab usernames, without `@`. A milestone overrides them with a line `/agent owners @alice @bob` in its description, for itself and its issues |
| `events` | all | which situations mention them: `failed` (a run failed), `needsInput` (questions, or the agent could not finish alone), `ciFailed` (CI still red after the fix rounds), `review` (an issue reached review, not on autopilot), `autopilotPaused`, `autopilotDone`, `mainRed` (the default branch stayed red after a retry), `blocked` (a run gave up for an outside reason and its issue stays blocked, also on autopilot) |
| `requestReview` | `true` | when an issue reaches review (not on autopilot), make the users the reviewers of its MR, unless it has reviewers |

The users must be members of the project to be notified; mentioning the bot
user itself notifies nobody.

## `spec.pilot`

The milestone coordinator, see [usage.md](usage.md#talking-to-the-pilot); its
model is `agents.pilot`.

| Field | Default | Description |
|-------|---------|-------------|
| `mention` | `true` | a comment that starts with a mention of the bot user (`@<bot> …`) talks to the pilot, like `workflow.pilotPrefix`. The bot's username is read on every poll |
| `maxBudgetUSD` | `"20"` | the pilot's spend per milestone (its task's `status.run.costUSD`). It covers answers and assessments of events. Once it is used up, the pilot answers that it is, and an assessment only says what happened, without a model call. On claude a single answer or assessment may spend what is left at most (or `agents.pilot.maxBudgetUSD`, if lower). The spend is what the engines report: cursor reports none, so on cursor the budget never runs out |
| `autonomy` | unset | `managed`: the pilot runs `retry`, `reReview`, `instruct` (a fixed kind and a note), `openIssue` and `fixFlaky` on its own, counted and audited on the tracking issue; `propose`: every action waits for an owner's `yes`. Unset: `managed` on autopilot milestones, `propose` on the others; a set value applies to every milestone. Neither the milestone description nor a rule changes it; an owner's `/pilot stop` lowers a milestone to `propose`, `/pilot go` returns it to this value (never above it). See [usage.md](usage.md#talking-to-the-pilot) |
| `maxInterventionsPerIssue` | `2` | the pilot's own actions per issue and head when it manages (an `openIssue` counts against the issue whose event or message triggered it, on the tracking issue against the milestone, a `fixFlaky` against the milestone, for the milestone's life); past it, it proposes and pings the owners. An action counts when it is started, also if it then fails |

**Per milestone.** A milestone's standing rules (lines `/agent rule R<n>:
switch <setting>=<value>` in its description, see
[usage.md](usage.md#talking-to-the-pilot)) override four settings for its
issues: `ci.maxFixRounds` (0 to 5), `ci.retryFailedJobs`,
`agents.review.retries` and `agents.review.maxIterations` (written
`review.retries`, `review.maxIterations`; 0 to 5). Nothing else can be
switched per milestone: budgets and limits, the merge gate, the freeze,
`pilot.*` and `mainHealth.*` stay as configured here.

## Other `spec` fields

| Field | Default | Description |
|-------|---------|-------------|
| `pollInterval` | `1m` | GitLab polling interval |
| `maxConcurrentRuns` | `3` | runs in flight per project; more runs queue. Keep this low on subscriptions, which have usage windows |
| `suspend` | `false` | stop polling and starting runs |

## Agent image

`Dockerfile.agent` builds a base image with `ai-runner`, git, `claude` and
`cursor-agent`. The build args `INSTALL_CLAUDE` / `INSTALL_CURSOR` toggle the
CLIs. It also has `mise` and `rustup` (pinned, checksum-verified; build args
`MISE_VERSION` / `RUSTUP_VERSION` with the checksums next to them), with which
the agent installs the toolchains your default branch pins
(`.mise.toml`, `.tool-versions`, `rust-toolchain.toml`) into
`/workspace/home/.toolchains` (`MISE_DATA_DIR`, `RUSTUP_HOME`, `CARGO_HOME`; no auto-install),
see [usage.md](usage.md#toolchains-the-agent-installs). For tools without a
pin, or to save the download per issue, extend the image:

```dockerfile
FROM ghcr.io/jonathanwiemers/ai-operator-agent:2026.10.1
USER root
RUN apt-get update && apt-get install -y golang-go && rm -rf /var/lib/apt/lists/*
USER 1000
```

The same image runs both containers of an agent pod: `ai-runner` in the
`runner` container and `ai-runner agent-server` in the `agent` container (see
[design.md](design.md#security)). Pods run as uid 1000, non-root, with all
capabilities dropped. In the agent container `HOME` points to the workspace
(`/workspace/home`), so CLI sessions survive pod restarts. Install tools
outside `$HOME`.

## Network policy

The Helm chart can add a NetworkPolicy to every namespace in
`agent.namespaces` (`agent.networkPolicy.enabled=true`): no ingress, DNS
allowed, and egress everywhere except cloud metadata endpoints and private
networks. The runner needs the Kubernetes API, which usually has a private
address, so allow-list it (and any self-hosted GitLab or package mirror):

```sh
kubectl get endpoints kubernetes -o jsonpath='{.subsets[*].addresses[*].ip}'
helm upgrade ai-operator … \
  --set agent.networkPolicy.enabled=true \
  --set 'agent.networkPolicy.allowedCIDRs={10.0.0.10/32,10.20.0.0/24}'
```

NetworkPolicies need a CNI that enforces them (Calico, Cilium, …).
