# When runs go wrong

Runs get cut off, models hit usage limits, GitLab is slow, and issues wait
for days. None of that loses work. This page shows what the operator does in
each case and what, if anything, is left for you. Most of it needs nothing
from you: an issue is blocked for a while and goes on by itself, without a
[pause](pauses.md).

## When a run is cut off

When a [run](../concepts/glossary.md#run) is cut off, work that is not
pushed yet is not thrown away. The [runner](../concepts/glossary.md#runner)
saves the implementer's work tree as a **checkpoint** commit on the work
branch and pushes it, and the next run continues from it.

A checkpoint is saved when an implementer call ends with:

- a [usage limit](#usage-limits) (`usage-limit`);
- an engine error or a timeout (`engine-error`, `timeout`);
- a budget stop (`budget-stop`);

and when the [agent container](../concepts/glossary.md#agent-container)
restarts (`container-restart`), the pod is lost or killed for lack of memory
(`pod-lost`, `oom-kill`). After a lost pod, the new pod saves what the
volume still holds, if the workspace is on the issue's branch and holds the
last pushed commit. Otherwise nothing is saved.

The commit looks like this:

```text
chore(wip): checkpoint #42 step 3 (usage-limit)
```

Its body has the changed files and the agent's last todo list or message.
It goes only to the issue's `agent/*` branch, without CI, and never with a
force push. A clean tree saves nothing.

- **Files left out.** Large files and new files named like secrets stay in
  the workspace but not in the commit, so no key or build output reaches
  GitLab. The limit is
  [`checkpoint.maxFileBytes`](../reference/lifecycle-and-ci.md#speccheckpoint).
  The progress comment and the run report name the files.
- **Conflicts are not saved.** If the run was cut off while it resolved
  merge conflicts, the merge is aborted and nothing is saved. The next run
  merges again.
- **No review, no merge.** While the merge request's head is a checkpoint,
  it stays a draft, nobody is asked to review it, and autopilot does not
  merge it ([merge gate](autopilot.md#the-merge-gate)).
- **The next run.** It starts from the checkpoint and is told so: "The
  previous attempt was cut off (usage-limit) after step 3; its uncommitted
  work was saved as a checkpoint commit."
- **No trace in the history.** The next commit of the implementer replaces
  the checkpoints at the tip of the branch with a commit under the agent's
  own message. If someone pushed in between, the checkpoint stays, the
  commit goes on top, and the report says "The checkpoint `<sha>` was
  kept". Commits of other people are never rewritten.

The issue, the progress comment and the failure report say what was saved:
`💾 Saved a checkpoint (<sha>, N files, +A/−D) because …`. The Desk counts
checkpoints by reason on the task page (*Agent calls*) and on the *Metrics*
tab of the issue, milestone and project.

To turn checkpoints off, set `checkpoint.disabled: true`. A cut-off run then
loses its uncommitted work. See
[Lifecycle and CI](../reference/lifecycle-and-ci.md#speccheckpoint).

## When a run fails

A failed implementer run saves its work too, whatever the reason, for example
an answer the operator could not read. If the workspace holds uncommitted
lines, the runner commits them as a checkpoint with the reason `run-failed`
before it posts the failure note. The
[problem thread](pilot.md#the-problem-thread) shows "348 lines saved as WIP
commit `80cd646`", and a retry starts from that commit, not from a clean
checkout.

One-off errors on push and on opening the merge request are retried up to
three times with a short wait: timeouts, connection resets, DNS errors, HTTP
5xx and 429. Other errors, such as a 403, fail at once.

Every failed run records its cause, also when the issue is blocked. The
cause is the line on the card and in the hold: "#558 needs input: the run
failed: decoding implementer answer: no JSON object in agent result".

## Usage limits

Subscriptions have usage windows, and APIs have rate limits. When an agent
reports such a limit, the run is **put off, not failed**:

- The issue moves to `agent::blocked` and `agent-blocked::quota`, and gets
  one comment that says when and on which model the run goes on:

  ```text
  ⏳ `claude` reached its usage limit (until 2026-10-28 19:20 UTC). The run
  is paused and resumes at 2026-10-28 19:20 UTC on `claude/claude-opus-5-5`;
  until then the issue is blocked
  ```

  The Desk shows these times in your time zone; see
  [Pauses](pauses.md#in-the-desk).

  If the fallback is limited too, the comment names both, and the run goes
  on with whichever is free first.
- The work of the call that hit the limit is saved as a
  [checkpoint](#when-a-run-is-cut-off).
- The run starts again at the reset time the agent reports. Without one, the
  wait starts at
  [`lifecycle.usageLimitBackoff`](../reference/lifecycle-and-ci.md#speclifecycle)
  and doubles with each further limit, up to `maxUsageLimitBackoff`.
- When the run starts again, the issue gets its label back. A comment
  `/agent …` or a move to `agent::ready` lifts the block too, but the run
  still waits for the reset.
- A put-off run does not count towards `maxRunsPerTask` and takes no run
  slot.
- Limits belong to your credentials, so they hold for the whole project:
  other issues on the same engine wait too, blocked the same way. Issues on
  another engine keep working.
- A limit during a review fix round pushes the work done so far, with the
  open findings.
- After 20 put-offs of the same run, it is reported as failed, in case an
  error was taken for a limit. The issue stays blocked, and the comment
  mentions the owners. `/agent retry` or `agent::ready` starts it again.

With a [fallback engine](../reference/agents-and-models.md#fallback-engine)
for the role, a limit does not block the issue: the work goes on on the
fallback, the run report says so, and other issues skip the limited engine
until its reset.

On autopilot, a blocked issue is no pause: autopilot waits, and the pilot
looks at it. Nothing is left for you to do. To see the limits from
Kubernetes:

```sh
kubectl -n agents get agenttasks -o wide
kubectl -n agents get agentproject <name> -o jsonpath='{.status.usageLimits}'
kubectl -n agents get agentproject <name> -o jsonpath='{.status.engineUsage}'
```

The first shows when each run is tried again, the second the active limits.
The third shows how much is left: claude reports its 5-hour and 7-day
windows with every call, each with its percentage and reset time. Cursor
reports none.

## When GitLab is slow

GitLab has rate limits too, and gitlab.com sometimes answers slowly or with
an error. The operator and the runners retry on their own:

- A request GitLab rate-limits is sent again after the wait GitLab asks for,
  up to a minute. Longer waits are left to the next poll.
- A read that hits a server error, a timeout or a broken connection is tried
  twice more. Writes are not, since GitLab may have applied them.
- A poll that still fails logs one line, "GitLab sync failed, trying again
  on the next poll", and the project's `Synced` condition says why. The next
  poll picks up where it left off.
- An error on one issue does not stop the poll. The other issues, main
  health and autopilot still run, and `Synced` names the issue
  (`issue #12: …`).

## Issues that stay open

Nothing is lost while an issue waits:

- **After each run** the pod is stopped. The workspace, with the checkout,
  the build caches and the agent's session, is kept, so the next run starts
  a new pod on it and goes on where the last one stopped. While the merge
  request's pipeline runs, no pod is needed: the operator follows the
  pipeline itself.
- **After a failed run** the pod stays for an hour, so you can read its logs
  (see [Run logs](watch-from-kubernetes.md#run-logs)).
- **After a while without activity** the workspace is deleted too.
- Any new `/agent` comment or move to `agent::ready` brings the issue back.
  After the workspace was deleted, the next run clones the repository again
  and reads the whole issue thread and the agent notes.

The times are
[`lifecycle.idleTimeout` and `lifecycle.retentionPeriod`](../reference/lifecycle-and-ci.md#speclifecycle).

### Agent notes

After every run the implementer writes short handoff notes: the state of
the work, the decisions and why, what is left, and the pitfalls. They are
one comment of the bot on the issue, **🧠 Agent notes**, which later runs
edit instead of adding new ones. A run that cannot resume the agent's
session, because the workspace was deleted or the session is gone, starts
from the issue, the thread and these notes instead of from zero.

If the notes are wrong, say so in a comment: comments written after the
notes win. Editing the note itself does not help, because the next run
writes it again. Only the bot's own comment counts. The model writes the
notes from the thread, so what someone told it in a comment can end up in
them.

## Next

- [When autopilot pauses](pauses.md): the problems that need you.
- [The problem thread](pilot.md#the-problem-thread): facts, history,
  options and the pilot chat.
- [Restart a stuck run](../reference/commands.md#restart-a-stuck-run): when
  a run hangs.
- [Lifecycle and CI](../reference/lifecycle-and-ci.md): the times and limits
  on this page.
