# Using the ai-operator from GitLab

Every `/agent` command is listed in [commands.md](reference/commands.md).

Writing a milestone, answering the agent, following its work and iterating
are in the [guides](guides/README.md), and so are autopilot, its pauses and
the pilot, the Desk, [budgets and spend](guides/budgets-and-spend.md),
[releases](guides/releases.md) and
[cross-repo changes](guides/cross-repo-changes.md). This page keeps watching
from Kubernetes until it moves too.

Setting up GitLab (the bot user, labels, board and webhooks) is part of
[Install](getting-started/install.md#set-up-gitlab).

## Watching from Kubernetes

```sh
kubectl -n agents get agentprojects
kubectl -n agents get agenttasks -w       # type, issue, phase, current step, last result, runs, cost
kubectl -n agents get agenttasks -o wide  # + merge request URL
kubectl -n agents get agenttask shop-42-add-paypal-button -o jsonpath='{.status.run}'
kubectl -n agents logs -f shop-42-add-paypal-button -c runner   # every step of a run
kubectl -n agents logs -f shop-42-add-paypal-button -c agent    # agent server: CLI runs, git calls, the agent's thinking and tool calls
```

`status.run` of an AgentTask holds the result of the last run: `lastResult`,
`summary`, `costUSD` (accumulated), `branch`, `mergeRequestURL`, `pushedSHA`,
`currentStep` while a run is in progress, and the result of the last review:
`blockingFindings` (blocker and major findings left), `reviewErrors`
(reviewers that failed), `reviewErrorsTransient` (all of them failed
transiently, in an outage), `reviewErrorKinds` (per failed reviewer:
`usageLimit` with the `engine` and `resetAt`, `transient` or `other`) and
`reviewedSHA` (the commit that review saw; a
CI fix run can push commits after it). `reviewedFor` is the head the last
re-review run was asked for (`spec.source.reviewFor`).

The spec task of a milestone holds the plan in `status.plan` (issues in plan
order with their dependencies, the dependencies the operator linked, and the
planner's release decision `release` with its reason `releaseWhy`) and, on autopilot, the queue in
`status.autopilot`: `phase`, `pausedReason`, `pauseKind` (the cause of a
pause, e.g. `findings`, `reviewErrors`, `ciRed`, `mergeRequest`, `owner`),
`trackingIssueIID` and one item
per issue with its state, MR, merged commit and decisions (`revisitOf` for a
follow-up). `status.revisits` tracks the [revisits after the
merge](guides/iterate-and-finish.md#change-a-decision-after-the-merge): when watching started, the last check
and the last handled comment per issue.

```sh
kubectl -n agents get agenttask shop-spec-m3-checkout-v2 -o jsonpath='{.status.autopilot}'
```

### Run logs

Every run is a numbered list of steps, e.g. for an issue:

1. read issue
2. read milestone
3. read project
4. clone repository (`fetch repository` once the workspace exists)
5. prepare checkout
6. find merge request
7. read comments
8. read failed CI jobs (CI fix runs only)
9. merge `<default branch>` (conflict runs only)
10. run implementer (skipped when a conflict run merged without conflicts)
11. conclude merge (conflict runs with conflicts only)
12. commit
13. review round 1, fix round 1, commit, review round 2, … (up to
    `review.maxIterations`)
14. push
15. update agent notes, post decisions (when there are any)
16. open merge request (first push only)
17. comment
18. update issue labels

A spec run reads the milestone and the project, clones or fetches, lists the
milestone's issues, runs the spec agent, applies the plan, records it
(`status.plan`, used by autopilot) and links the dependencies.

The steps show up in three places:

- **The runner log** has a `step started` and a `step done` (or `step
  failed`) line per step, with the duration and the result. Each line carries
  `run=<task>#<run request>`, and so do the agent container's lines, so you
  can filter both logs by run. The `run finished` line lists all steps with
  their durations.
- **`kubectl get agenttasks`** shows the step in progress in the `Step`
  column (`status.run.currentStep`).
- **GitLab:** while a run goes on, the MR's progress comment shows the run log
  so far, open, with the step in progress. The agent's report on the MR then
  ends with a collapsed **Run log** table that lists the steps up to that
  comment, with their durations, results and the cost so far. A failure
  comment on the issue includes it too, with the failed step marked.

Comment bodies and diffs are never logged, and prompts only with
`spec.agents.logPrompts: true` (for the web UI's live transcript). `spec.agents.logLevel:
debug` (or the operator's `--agent-log-level`) adds the runner's git commands.

**The agent's session** is in the agent container's log as it happens,
tagged with the run like the other lines:

```
agent thinks       text="The endpoint needs a test first."
agent says         text="Adding the test."
agent uses a tool  tool=Bash input="go test ./..."
agent uses a tool  tool=Edit input="/workspace/repo/api.go"
```

Thinking shows up when the model thinks (depending on model and
`effort`; the CLI may summarize it). Tool output, the contents of edits and
the agent's final answer (posted on GitLab anyway) are not logged; commands
are logged by their first line, without heredoc contents, and unknown tools
only by the names of their arguments. What the agent writes in its own
thinking and messages can still quote code. `logLevel: error` turns these
lines off.

cursor-agent runs show the same messages and tool calls, with Cursor's tool
names (`read`, `write`, `run_terminal_cmd`, …), but no thinking: the CLI keeps
it to itself in print mode.

### When the agent container dies

The agent container runs the agent CLI and everything it starts: builds,
test runners, dev servers. If it uses more memory than its limit, the kubelet
kills it (`OOMKilled`) and starts it again in the same pod. The operator
records how the agent container last stopped in `status.agentTermination`
(reason, exit code, time, restarts, memory limit), and the runner, which lost
its call to the agent, waits for the container to be back and reports it:

- **Out of memory:** the operator raises the memory limit of that task's
  agent container by half (a memory request grows in proportion), at most to
  `spec.agents.maxMemory` (default: twice `resources.limits.memory`), and the
  run starts again in a new pod with it. The issue stays in
  `agent::working` and gets a comment: "The agent container ran out of
  memory after 1h28m and was killed (OOMKilled, memory limit 4Gi). The run
  starts again with 6Gi (restart 1 of at most 3)". The raised limit stays
  with the task for its later runs (`status.agentMemoryLimit`); lowering
  `maxMemory` or the limit lowers it too. If the killed call was the
  implementer's and the volume still holds its files, the new pod first
  saves its uncommitted edits as a checkpoint (`chore(wip): checkpoint
  #<issue> step <n> (oom-kill)`, see
  [When the whole pod is lost](#when-the-whole-pod-is-lost)), and the run
  starts again from it. If nothing can be saved (see [When a run is cut
  off](guides/when-runs-go-wrong.md#when-a-run-is-cut-off)), the run starts again from the last pushed
  commit. If no node has room for the raised memory, the task's status says the pod cannot be
  scheduled (`kubectl get agenttasks -o wide`).
- **Out of memory at the cap** (or after 3 restarts, or without a memory
  limit to raise): the run fails with that reason, and the issue is
  blocked (`agent-blocked::infra`, see
  [When the whole pod is lost](#when-the-whole-pod-is-lost) for the
  automatic retry). Raise `spec.agents.resources` or
  `spec.agents.maxMemory`, then comment `/agent retry`. A pod started with
  other agent resources is replaced between runs, so the retry gets the new
  limit.
- **Another reason** (a crash): an implementer call's uncommitted edits are
  saved as a checkpoint (`chore(wip): checkpoint #<issue> step <n>
  (container-restart)`, pushed without CI, the MR set to draft), then the
  checkout is reset to the last commit, which is that checkpoint, so the
  call is tried once more with its files; if it loses the container again, the run
  fails with the reason and exit code, and the issue is blocked
  (`agent-blocked::infra`).
- A call made while the container was down (it died between calls, e.g.
  from a background process) is made again once it is back.

On nodes with cgroup v1, or with the kubelet's `singleProcessOOMKill`, the
kernel kills only the biggest process (a test runner, the CLI) instead of the
whole container. The container keeps running and the agent CLI simply
reports an error, so nothing is recorded.

```sh
kubectl -n agents get agenttask shop-42-add-paypal-button -o jsonpath='{.status.agentTermination}'
```

### When the whole pod is lost

If the pod stops mid-run (evicted, the node shut down or went away, or the
runner itself crashed), the run request stays open. The operator replaces a
pod that has terminated for good, and the new pod's runner starts the run
again from the last pushed commit. If the lost pod was in an implementer
call (the runner notes that in its state on the volume) and the volume still
holds the workspace, the new pod first saves what that call left, before it
resets the workspace: uncommitted edits become a checkpoint commit
(`chore(wip): checkpoint #<issue> step <n> (pod-lost)`, its body taken from
the agent's last todo list or message), pushed without CI with the MR set to
draft, together with step commits the lost pod made but had not pushed. The
run then starts again from that commit. Nothing is saved, and the run starts
from the last pushed commit, when checkpoints are off, the workspace is on
another branch, or someone pushed to the branch after the lost pod's last
push (see [When a run is cut off](guides/when-runs-go-wrong.md#when-a-run-is-cut-off)). A progress comment the lost attempt left on the merge request is
removed. The new attempt's progress comment and report say so above the run
log: "↻ Started again: the previous attempt was interrupted (its pod stopped
before the run finished, e.g. evicted or the node went down). Restart 1 of
at most 3." Agent pods use a local volume, so on a node that is down the new
pod waits until the node is back.

After 3 restarts of the same run request (`status.run.interruptions`) the
runner gives up instead of trying again: something in the run may keep
taking the pod down. The run fails, and an issue moves to `agent::blocked` +
`agent-blocked::infra` (as for a lost agent container), not
`agent::needs-input`: the cause is outside it.

- After `spec.lifecycle.infraRetryAfter` (default 30m) the operator starts
  one new run by itself; the issue shows `agent::working` again. The
  failure comment says when.
- If that run fails the same way, the issue stays blocked and waits for
  you; that comment says so and mentions the owners (event `blocked`, also
  on autopilot). There is one automatic retry per run request, so
  `/agent retry` (or a move to `agent::ready`) starts a run that gets its
  own retry.
- `/agent …` or a move to `agent::ready` lifts the block at once and runs.
  On autopilot, the gate waits while the issue is blocked, and
  `/agent resume` does not lift such a block: only a new run does.

`/agent retry` starts the run again with a new count. A spec run that gives
up this way is planned again once too, like any [planning failure for an
outside reason](guides/write-a-milestone.md). A
run that finishes resets the count. Suspending a task (`spec.suspend`)
mid-run stops its pod too, but does not count: the run starts again when the
task is resumed.

Task phases: `Pending`, `Queued` (waiting for `maxConcurrentRuns` or for
a task that ranks ahead, see [several milestones](guides/autopilot.md#several-milestones-at-once);
pilots never queue), `Running`, `Waiting` (paused by a usage limit), `Idle`,
`Hibernated`, `Archived`, `Blocked` (hit `maxRunsPerTask`).

To force a task to stop its pod, set `spec.suspend: true` on the AgentTask. To
stop all activity for a project, set `spec.suspend: true` on the AgentProject.
