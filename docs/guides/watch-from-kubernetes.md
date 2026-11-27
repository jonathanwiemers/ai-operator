# Watch from Kubernetes

The Desk shows what the agents do, but everything it shows is also in the
cluster. This page is for when you want `kubectl`: the state of your
projects and tasks, the logs of a run, and what happens when an agent's
container or its whole pod is lost. The examples use the namespace
`agents`, a project `shop` and an issue task `shop-42-add-paypal-button`.

## Projects and tasks

Each [`AgentProject`](../concepts/glossary.md#agentproject) gets one
[task](../concepts/glossary.md#task) (an `AgentTask`) per issue and per
milestone spec. List them:

```sh
kubectl -n agents get agentprojects        # GitLab project, active tasks, last sync
kubectl -n agents get agenttasks -w        # type, issue, phase, step, result, runs, cost, CI
kubectl -n agents get agenttasks -o wide   # also when a deferred run resumes, and the merge request
```

The `Step` column shows the step of the run in progress, `CI` the state of
the pipeline the operator follows. The phase of a task is one of these:

| Phase | Meaning |
|-------|---------|
| `Pending` | Created, no run yet. |
| `Queued` | Waiting for a free slot (`maxConcurrentRuns`) or for a task that ranks ahead, see [several milestones](autopilot.md#several-milestones-at-once). Pilots never queue. |
| `Running` | A run is in progress. |
| `Waiting` | A run waits to start again: for a [usage limit](when-runs-go-wrong.md#usage-limits) to reset, for a retry with more memory after an [out-of-memory kill](#when-the-agent-container-dies), or for a raised [budget](budgets-and-spend.md#when-a-budget-is-reached). The message says which, and until when. |
| `Idle` | The run is done; the pod waits a short while for the next request. |
| `Hibernated` | The pod is stopped, the workspace is kept. A suspended task is here too. |
| `Archived` | The workspace is deleted after a long time without runs; the next run clones again. |
| `Blocked` | It hit `maxRunsPerTask` and needs you. |

The times are `lifecycle.idleTimeout` and `lifecycle.retentionPeriod`, see
[Lifecycle and CI](../reference/lifecycle-and-ci.md#speclifecycle).

### The last run

`status.run` of a task holds the result of the last
[run](../concepts/glossary.md#run):

```sh
kubectl -n agents get agenttask shop-42-add-paypal-button -o jsonpath='{.status.run}'
```

- `lastResult` and `summary`: how the run ended, in one word and in a few
  lines.
- `costUSD`: what the task's runs cost so far.
- `branch`, `mergeRequestURL` and `pushedSHA`: the branch, its merge
  request and the last commit pushed.
- `currentStep`: the step in progress, while a run goes on.
- `interruptions`: how often the run was started again after its pod was
  lost, see [When the whole pod is lost](#when-the-whole-pod-is-lost).
- `deferredUntil`, `deferrals` and `blockReason`: when a put-off run starts
  again and why, see
  [GitLab rate limit](../reference/status-and-history.md#gitlab-rate-limit).

The result of the last review is there too:

- `blockingFindings`: blocker and major findings left.
- `reviewErrors`: reviewers that failed. `reviewErrorsTransient` is true
  when all of them failed for a passing reason, such as an outage.
  `reviewErrorKinds` says per reviewer why: `usageLimit` (with the `engine`
  and `resetAt`), `transient` or `other`.
- `reviewedSHA`: the commit the review saw. A CI fix run can push commits
  after it.
- `reviewedFor`: the head the last re-review run was asked for.

### A milestone's plan and queue

The spec task of a milestone holds the plan in `status.plan`: the issues in
plan order with their dependencies, the dependencies the operator linked,
and the planner's release decision (`release`, with its reason
`releaseWhy`).

On [autopilot](../concepts/glossary.md#autopilot), `status.autopilot` holds
the queue: its `phase`, the `pausedReason` and `pauseKind` of a
[pause](pauses.md), the `trackingIssueIID`, and one item per issue with its
state, merge request, merged commit and decisions (`revisitOf` for a
follow-up). `status.revisits` tracks the
[revisits after the merge](iterate-and-finish.md#change-a-decision-after-the-merge):
when watching started, the last check and the last handled comment per
issue.

```sh
kubectl -n agents get agenttask shop-spec-m3-checkout-v2 -o jsonpath='{.status.autopilot}'
```

## Run logs

Each task's pod has two containers: the
[runner](../concepts/glossary.md#runner), which talks to GitLab and pushes,
and the [agent container](../concepts/glossary.md#agent-container), which
runs the agent. Follow both:

```sh
kubectl -n agents logs -f shop-42-add-paypal-button -c runner   # every step of a run
kubectl -n agents logs -f shop-42-add-paypal-button -c agent    # the agent's calls, git, thinking and tools
```

The pod stops shortly after each run. After a failed run it stays for at
least an hour, so you can still read its logs.

### The steps of a run

Every run is a numbered list of steps. For an issue:

1. read issue
2. read milestone
3. read project
4. clone repository (`fetch repository` once the workspace exists)
5. prepare checkout
6. find merge request
7. open merge request (only when there is none yet: a draft, before the
   agent starts)
8. read comments
9. read failed CI jobs (CI fix runs only)
10. merge `<default branch>` (conflict runs only)
11. run implementer (skipped when a conflict run merged without conflicts)
12. conclude merge (conflict runs with conflicts only)
13. commit
14. review round 1, fix round 1, commit, review round 2, … (up to
    `review.maxIterations`)
15. push
16. update agent notes, post decisions (when there are any)
17. comment
18. update issue labels, request review (when
    [`notify.requestReview`](../reference/agentproject.md#specnotify) is on)

An issue worked in steps has `run implementer, step 1`, `push step 1` and
so on, and a run that saves a
[checkpoint](when-runs-go-wrong.md#when-a-run-is-cut-off) has steps for
that too.

A spec run reads the milestone and the project, clones or fetches, lists
the milestone's issues, runs the planner, applies the plan, records it in
`status.plan` and links the dependencies.

You see the steps in three places:

- **The runner log** has a `step started` and a `step done` (or
  `step failed`) line per step, with the duration and the result. Each line
  carries `run=<task>#<run request>`, and so do the agent container's
  lines, so you can filter both logs by run. The `run finished` line lists
  all steps with their durations.
- **`kubectl get agenttasks`** shows the step in progress in the `Step`
  column.
- **GitLab and the Desk:** while a run goes on, the merge request's
  progress comment shows the steps so far, with the one in progress. The
  agent's report then ends with a folded **Run log** table: the steps, their
  durations and results, and the cost so far. A failure comment on the
  issue has it too, with the failed step marked. The Desk's
  [Live tab](desk.md#live-transcript) shows the same lines as they happen.

### What gets logged

Comment bodies and diffs are never logged. Prompts are logged only with
`spec.agents.logPrompts: true`, for the Desk's live transcript.
`spec.agents.logLevel: debug` adds the runner's git commands.

The agent's session shows up in the agent container's log as it happens,
tagged with the run like the other lines:

```
agent thinks       text="The endpoint needs a test first."
agent says         text="Adding the test."
agent uses a tool  tool=Bash input="go test ./..."
agent uses a tool  tool=Grep input="func handleCheckout"
user says          text="Use the existing retry helper."
```

A `user says` line is a message passed on to a running claude implementer.
It shows up once claude has taken the message in.

Thinking shows up when the model thinks, depending on the model and its
effort; the CLI may summarize it. Tool output, the contents of edits and
the agent's final answer (it is posted anyway) are not logged. Commands are
logged by their first line, without heredoc contents, and unknown tools only
by the names of their arguments. The agent's own thinking and messages can
still quote code. `logLevel: error` turns these lines off, and the step
lines with them: only errors are left.

cursor-agent runs show the same messages and tool calls, with Cursor's tool
names (`read`, `write`, `run_terminal_cmd`, …), but no thinking: the CLI
keeps it to itself.

The log fields and their defaults are in
[Agents and models](../reference/agents-and-models.md).

## When the agent container dies

The agent container runs the agent CLI and everything it starts: builds,
test runners, dev servers. If it uses more memory than its limit, Kubernetes
kills it (`OOMKilled`) and starts it again in the same pod. The operator
records how it last stopped in `status.agentTermination` of the task: the
reason, exit code, time, restarts and memory limit. The runner waits until
the container is back and handles it by the cause:

- **Out of memory:** the operator raises the memory limit of that task's
  agent container by half (a memory request grows with it), up to
  `spec.agents.maxMemory`, and the run starts again in a new pod. The issue
  stays in `agent::working` and gets a comment like "The agent container
  ran out of memory after 1h28m and was killed (OOMKilled, memory limit
  4Gi). The run starts again with 6Gi (restart 1 of at most 3)". The raised
  limit stays with the task for its later runs (`status.agentMemoryLimit`);
  lowering `maxMemory` or the limit lowers it too. If the agent was
  implementing and its files are still there, the new pod first saves its
  uncommitted edits as a checkpoint commit,
  `chore(wip): checkpoint #<issue> step <n> (oom-kill)`, and the run starts
  again from it (see
  [When a run is cut off](when-runs-go-wrong.md#when-a-run-is-cut-off)).
  If no node has room for the raised memory, the task's message says "agent
  pod cannot be scheduled" with the reason; see it with
  `kubectl -n agents describe agenttask <task>` or in `status.message`.
- **Out of memory at the cap**, after 3 restarts, or without a memory limit
  to raise: the run fails with that reason, and the issue is blocked with
  `agent-blocked::infra` (see the automatic retry below). Raise
  `spec.agents.resources` or `spec.agents.maxMemory`, then comment
  `/agent retry`. A pod started with other agent resources is replaced
  between runs, so the retry gets the new limit.
- **Another reason**, a crash: the implementer's uncommitted edits are saved
  as a checkpoint (`… (container-restart)`, pushed without CI, the merge
  request set to draft), and the call is tried once more on that commit. If
  it loses the container again, the run fails with the reason and exit code,
  and the issue is blocked with `agent-blocked::infra`.
- **Between calls:** a call made while the container was down (it died from
  a background process, say) is made again once it is back.

On nodes with cgroup v1, or with the kubelet's `singleProcessOOMKill`, the
kernel kills only the biggest process (a test runner, the CLI) instead of
the whole container. The container keeps running and the agent CLI simply
reports an error, so nothing is recorded.

```sh
kubectl -n agents get agenttask shop-42-add-paypal-button -o jsonpath='{.status.agentTermination}'
```

The memory fields and their defaults are in
[Agents and models](../reference/agents-and-models.md).

## When the whole pod is lost

A pod can stop in the middle of a run: it is evicted, its node shuts down or
goes away, or the runner itself crashes. The run is not lost. The operator
replaces the pod, and the new pod starts the run again from the last pushed
commit.

If the lost pod was implementing and its workspace is still there, the new
pod first saves what the agent left: uncommitted edits become a checkpoint
commit (`chore(wip): checkpoint #<issue> step <n> (pod-lost)`, its message
taken from the agent's last todo list or message), pushed without CI with
the merge request set to draft, together with commits the lost pod made but
had not pushed. The run then starts again from there.
[When a run is cut off](when-runs-go-wrong.md#when-a-run-is-cut-off) says
when nothing can be saved.

A progress comment the lost attempt left on the merge request is removed.
The new attempt's progress comment and report say so above the run log:
"↻ Started again: the previous attempt was interrupted (its pod stopped
before the run finished, e.g. evicted or the node went down). Restart 1 of
at most 3." Agent pods use a local volume, so on a node that is down the new
pod waits until the node is back.

### After 3 restarts

After 3 restarts of the same run (`status.run.interruptions`) the runner
gives up: something in the run may keep taking the pod down. The run fails,
and the issue moves to `agent::blocked` + `agent-blocked::infra`, not to
`agent::needs-input`, since the cause is outside it. A lost agent container
blocks an issue the same way.

- After [`lifecycle.infraRetryAfter`](../reference/lifecycle-and-ci.md#speclifecycle)
  the operator starts one new run by itself, and the issue shows
  `agent::working` again. The failure comment says when.
- If that run fails the same way, the issue stays blocked and waits for you.
  The comment says so and mentions the [owners](owners-and-pings.md), also
  on autopilot. There is one automatic retry per run request, so
  `/agent retry` (or a move to `agent::ready`) starts a run that gets its
  own retry.
- Any `/agent …` comment or a move to `agent::ready` lifts the block at once
  and runs. On autopilot the queue waits while the issue is blocked, and
  `/agent resume` does not lift such a block: only a new run does.

`/agent retry` starts the run again with a new count. A spec run that gives
up this way is planned again once too, like any
[planning failure](write-a-milestone.md) for an outside reason. A run that
finishes resets the count. Suspending a task mid-run stops its pod too, but
does not count: the run starts again when the task is resumed.

## Stop a task or a project

To make a task stop its pod, set `spec.suspend: true` on the `AgentTask`.
To stop all activity for a project, set `spec.suspend: true` on the
`AgentProject`:

```sh
kubectl -n agents patch agenttask shop-42-add-paypal-button --type merge -p '{"spec":{"suspend":true}}'
kubectl -n agents patch agentproject shop --type merge -p '{"spec":{"suspend":true}}'
```

Set it back to `false` to go on.

## Next

- [When runs go wrong](when-runs-go-wrong.md): cut-off runs, usage limits
  and issues that wait.
- [Lifecycle and CI](../reference/lifecycle-and-ci.md): pods, workspaces,
  retries and their defaults.
- [The Desk](desk.md): the same state, with the live transcript.
