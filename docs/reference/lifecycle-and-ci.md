# Lifecycle and CI

These `spec` fields of an [AgentProject](agentproject.md) decide how long
agent pods and workspaces live, how often a run is retried, how the agent
reacts to CI, what it saves when a call is cut off, and how the operator
watches the default branch. Every field is optional.

## Example

```yaml
spec:
  lifecycle:
    idleTimeout: 1m
    retentionPeriod: 168h
    maxRunsPerTask: 20
    stallAfter: 30m
  ci:
    maxFixRounds: 2
    retryFailedJobs: true
  checkpoint:
    maxFileBytes: 5242880
  mainHealth:
    interval: 5m
    autoFix: true
    autoFixFlaky: true
```

## `spec.lifecycle`

Each issue has a [task](../concepts/glossary.md#task) with its own pod and
workspace volume. The workspace keeps the checkout and the agent's sessions
between [runs](../concepts/glossary.md#run).

| Field | Meaning | Default |
|-------|---------|---------|
| `idleTimeout` | After the last run, the pod is stopped; the workspace stays. A longer timeout only saves the pod start. After a failed run the pod stays at least 1h, so you can read its logs. | `1m` |
| `chatIdleTimeout` | After the last Desk chat turn, how long an idle pod stays. Unanswered chat messages keep the pod up. | `15m` |
| `problemWarmMax` | While a problem waits for you, the longest time the milestone's pilot pod is kept up, so it answers without a cold start. An idle pod costs no tokens. | `2h` |
| `retentionPeriod` | After the last run, the workspace is deleted. | `168h` (7 days) |
| `maxRunsPerTask` | The task is `Blocked` when it reaches this many runs. Runs paused by usage limits do not count. | `20` |
| `usageLimitBackoff` | First wait after a usage or rate limit that names no reset time. It doubles with each further hit. | `5m` |
| `maxUsageLimitBackoff` | The longest such wait. | `1h` |
| `infraRetryAfter` | After a run failed for an outside reason (pod or container lost, out of memory with no raise left, interrupted 3 times), the issue is blocked (`infra`). This long after the failure the operator starts one new run. Planning runs are retried the same way, also after a passing GitLab or network error. | `30m` |
| `stallAfter` | A running task whose pod logged nothing for this long, or stayed pending or crash-looping this long, is stalled: condition `Stalled=True` (reasons `Silent`, `PodPending`, `CrashLoop`). The task stays running; the condition clears on the next log line or when the run ends. | `30m` |
| `stallCheckInterval` | How often a running task is checked for a stall. The check makes no model call; the pilot is woken only when it finds one. | `5m` |

The Desk's [watchdog](../concepts/glossary.md#watchdog) shows stalled runs,
and an owner can restart one (see
[Restart a stuck run](commands.md#restart-a-stuck-run)).

> **Upgrading from a release with a longer `idleTimeout` default (`2h`,
> `1h`):** the API server stored the old default in every AgentProject that
> was created with a `lifecycle` block. Remove `idleTimeout`, or set it to
> `1m`, to stop pods right after each run.

### Remote git timeouts

These are fixed, not fields. Every `git push`, `fetch` and `ls-remote` the
runner makes aborts a transfer slower than 1000 bytes a second for 60
seconds, and has a time limit: 5 minutes for push and fetch, 1 minute for
ls-remote. A timeout fails the step with a clear error, such as
`git push timed out after 5m`.

## `spec.ci`

After each push, the operator waits for the pipeline. When it fails, the
agent gets the failed jobs' logs and fixes them. See
[CI feedback](../guides/while-the-agent-works.md#ci-feedback).

| Field | Meaning | Default |
|-------|---------|---------|
| `disabled` | Move issues to review right after the push, without waiting for CI. | `false` |
| `maxFixRounds` | CI fix runs per push before the issue goes to needs-input. | `2` |
| `retryFailedJobs` | Retry the failed jobs once before the agent is involved. | `true` |
| `noPipelineTimeout` | No pipeline for the pushed commit within this time means "no CI". | `10m` |

The GitLab token needs the `api` scope (Developer role) to read pipelines
and job logs and to retry jobs.

The operator follows the merge request's head pipeline when the merge
request's head is the pushed commit, which covers merge request and
*merged results* pipelines (GitLab Premium). Otherwise it takes the newest
pipeline of the pushed commit, such as a branch pipeline.

A milestone can change `maxFixRounds` and `retryFailedJobs` for its issues
with a rule, see [`spec.pilot`](agentproject.md#specpilot).

## `spec.checkpoint`

When an implementer call is cut off (a usage limit, an engine error or
timeout, a budget stop), the runner saves the uncommitted work as a
checkpoint commit on the work branch, `chore(wip): checkpoint #<issue> step
<n> (<reason>)`, and pushes it without starting CI and never forced. A
merge request that was ready is set to draft first. The next run starts
from the checkpoint. See
[When a run is cut off](../guides/when-runs-go-wrong.md#when-a-run-is-cut-off).

The same happens when the agent container restarts, and in the new pod
after a lost pod or an out-of-memory kill. A clean work tree saves nothing,
and a merge in progress is aborted and saves nothing. A checkpoint never
counts as the pushed or reviewed commit. The Task page and the Metrics tab
count the checkpoints.

| Field | Meaning | Default |
|-------|---------|---------|
| `disabled` | Save no checkpoints: the work of a cut-off call is reset by the next run. | `false` |
| `maxFileBytes` | Files larger than this are left out of a checkpoint; they stay in the workspace and the run log names them. New files named like secrets (`.env*`, `*.pem`, `*.key`, `id_rsa*`, `*.p12`) are always left out. | `5242880` (5 MiB) |

## `spec.mainHealth`

The operator watches the default branch's pipelines. While the branch is
red, autopilot merges nothing. See [Main health](../guides/while-the-agent-works.md#main-health).

| Field | Meaning | Default |
|-------|---------|---------|
| `disabled` | Do not watch the default branch. | `false` |
| `interval` | How often the newest finished pipeline of the default branch is read. | `5m` |
| `retryFailedJobs` | Retry the failed jobs of a red pipeline once. Green after the retry counts as flaky. | `true` |
| `retryCanceled` | Retry a canceled pipeline once when its commit has no successful pipeline and no newer pipeline replaced it. Canceled again, the operator comments on the `ci::main-red` issue if one is open, else on a `ci::main-canceled` issue, which freezes nothing. | `true` |
| `issue` | Open the `ci::main-red` issue, one `ci::flaky` issue per flaky job, and the `ci::main-canceled` issue. Off, a pipeline canceled twice is only logged. | `true` |
| `freezeAutopilot` | Autopilot merges nothing while the branch is red: the issue at the merge gate is blocked (`main-red`), and autopilot goes on once the branch is green. A status not checked for 3 intervals does not freeze. | `true` |
| `autoFixFlaky` | Hand a `ci::flaky` issue to the agent the second time the job flakes, once: into the active autopilot milestone with the lowest number, else without a milestone. Off, only the pilot hands one over. | `false` |
| `autoFix` | Hand the `ci::main-red` issue to the agent, once per red episode. On autopilot the frozen milestone merges the fix; otherwise a human merges it. | `false` |

The token needs the same rights as for `spec.ci`, and must be able to
create issues.

## Next

- [AgentProject](agentproject.md): the other field groups.
- [Agents and models](agents-and-models.md): memory limits, roles and
  models.
- [Labels](labels.md): the `agent-blocked::` and `ci::` labels.
