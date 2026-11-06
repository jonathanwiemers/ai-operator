# Pauses say why, spend adds up, planning waits for the milestone's start

Status: milestone spec of "ai-operator: pauses say why, spend adds up,
planning waits for the milestone's start", with the decisions below locked.
File references are at origin/main, 2026-11-06.

## Goal

1. A pause names its actual cause: the failed jobs, the run's error, the
   agent's question or the reviewer that failed, not a list of what it could
   be.
2. The Spend numbers add up. By model, by issue and by kind show the same
   total, and calls nobody metered are counted as such.
3. Autopilot never reports a milestone as done when it merged nothing because
   its queue was empty.
4. The spec agent plans an autopilot milestone when that milestone starts, not
   when it is created. Issues then describe the code as it is when work
   begins.

## Background

Most of this came up in the retrospectives of Bloop %20–%24 and ai-operator
%6; planning late is the owner's request. File refs are at origin/main,
2026-11-06.

### Pauses

- `gateCheck` (`internal/controller/autopilot.go:1096`) answers every
  `needsInput` label with one text: "CI is still red, the run failed or the
  agent asked a question". Bloop %22 and ai-operator %6 could not tell which
  it was afterwards. `pilot_events.go:58` repeats the text, and the
  `autopilot.paused` events and the Pauses card inherit it.
- The label is set in two places, and both know the cause: `finishCI`
  (`ci.go:267`, `CIStatus.FailedJobs` and `PipelineURL`) and the runner
  (`implement.go:843`–`879`, the agent's answer or the run's failure).
- `reviewErrors` (`rereview.go:19`) says "2 reviewers failed" although
  `status.run.reviewErrorKinds` holds reviewer, kind, engine and `resetAt`.
  The Desk already reads them (`internal/ui/milestone.go:270`).
- `pause()` skips the comment when `PausedReason` is unchanged. So a reason
  that changes between polls posts a new pause comment each time.
- Resume comments ("▶️ Autopilot continues: …") do not say whether a person
  or a later run ended the pause.

### Spend

- Bloop %24: per-model tokens sum to 671M (Opus alone 642.8M), the
  milestone's total is 408.6M. By issue and by kind match the total.
- ai-operator %8 ran without subagents, and there the two agree exactly:
  opus 7.64M + sonnet 5.07M = 12.71M.
- The row's tokens come from the result line's `usage`, the split from
  `modelUsage` (`internal/runner/engine.go:641`–`664`). The split includes
  subagent models; whether `usage` includes subagent threads is not
  established.
- `docs/specs/spend-tokens-desk.spec.md` says `modelUsage` and
  `total_cost_usd` are running totals of the CLI process, and that a
  long-lived Desk chat reports the session total on every turn. That note
  came from the CLI bundle, with no billed live run. `addCallUsage` subtracts
  nothing, so if chat turns are recorded as calls they may count twice.
- ai-operator %6 lists a model `auto` with no cost and no tokens while
  `costKnown` is true. Cursor runs with model `auto`, and ACP reports no usage
  (`session_acp.go:567`). `scanTotals` sets `CostKnown` when any call has a
  cost (`spend.go:185`), so unmetered calls disappear from the report.

### Empty queue

- Bloop %20 commented "✅ Autopilot is done: 0 of 0 issues merged", then
  posted a retrospective. Its issues #384 and #385 existed, and an owner had
  just edited the milestone description, which plans it again. It continued
  later and merged both.
- The guards in `run` (`len(Status.Plan) == 0`) and `step`
  (`a.st.RunPending()`) are older than that incident, so they did not stop
  it, and the path is not known yet. `pick` calls `done` when nothing is left
  (`autopilot.go:377`), and `done` does not look at `len(a.ap.Items)`.

### Planning

- `syncSpecTask` (`agentproject_controller.go:720`) creates the spec task with
  `RunRequest = 1` the first time it sees a milestone, so planning runs at
  once, however many milestones are ahead.
- The run queue is oldest milestone first (`api/v1alpha1/queue.go`), and one
  run at a time per project. A milestone created behind two others can wait
  days, and meanwhile their merges change the code its issues describe.
- The queue is work-conserving: a younger milestone's issue takes the slot
  while an older one waits for CI. So "its first issue gets a slot" is no
  usable start, because that would be almost at once.
- `run` returns early while `Status.Plan` is empty, and the tracking issue then
  says "not planned, planning has not succeeded yet"
  (`autopilot.go:1903`), which would be wrong for a waiting milestone.

## Decisions (locked)

1. **Typed cause.** New `status.run.needsInputCause`: `ciRed`, `runFailed` or
   `agentQuestion`, written where the label is set. Without a value (a run
   from before this release) the old text stays.
2. **Stable reasons.** A pause reason holds only what stays the same while
   the cause does: no times, no counters. `FailedJobs` are sorted.
3. **Long text stays out of the reason.** The question or error is cut to one
   line of 200 characters, and the reason links to the comment or pipeline.
4. **One token basis.** When `modelUsage` is present, the row's tokens are the
   sum of its models, so by model, by issue and by kind share one basis. If
   R5 shows that `usage` is right and `modelUsage` is wrong, stop and say so
   on the tracking issue before changing this.
5. **Running totals become deltas.** A call in a long-lived process records
   the difference to that process's previous result, for tokens, per-model
   figures and cost. A first turn and a one-shot call record the result as is.
6. **No backfill.** Old rows stay as they are. The Spend tab and retrospectives
   compare new milestones only.
7. **Unmetered is its own number.** `costKnown` keeps its meaning. The count
   of calls without cost or tokens is shown next to it. `auto` stays a row,
   marked as not metered.
8. **New pause kind `emptyPlan`.** Its way out is changing the milestone's
   spec, which plans it again. It goes through `PauseExits`, the Desk's pause
   card and the docs like every other kind.
9. **When a milestone starts.** An autopilot milestone starts when no older
   autopilot milestone of its project is still open: every lower iid is done,
   off or closed. A paused older milestone still counts, because most pauses
   lift by themselves within minutes.
10. **Owner override.** `/agent start` on the tracking issue starts planning
    now, ahead of its turn. It is handled like `/agent pause` (`autopilot.go`), owners only.
11. **Only milestones without a plan.** A milestone that has a plan or a
    planning run keeps going, including at the rollout of this change. A
    description change after planning re-plans as today.
12. **Milestones without autopilot are unchanged.** A `/agent spec` milestone
    without `/agent autopilot` has no queue and an owner reviews it, so it
    plans on creation as today.

## Requirements

### Pauses

- R1 `status.run.needsInputCause` (decision 1), set in `finishCI` and by the
  runner next to the label.
- R2 `gateCheck` builds the reason from it:
  - `ciRed`: "CI is red after the fix rounds: failing jobs a, b (pipeline URL)"
  - `runFailed`: "the run failed: <error>"; a usage limit names the engine and
    the reset time
  - `agentQuestion`: "the agent asks: <question>" with a link to the comment

  `pilot_events.go:58` uses the same text.
- R3 `reviewErrors` names each reviewer and kind from `reviewErrorKinds`, for
  example "bugbot failed (usageLimit, resets 16:00 UTC)". A `resetAt` is part
  of the reason only if it stays the same across polls.
- R4 The resume comment says who ended the pause: an owner, the operator or a
  later run (`ResumedBy`).

### Spend

- R5 Establish the basis on recorded lines: extend
  `internal/runner/testdata/` with a call with a subagent on the main model
  and a two-turn chat, built from the CLI in the agent image. Write down in the
  spec whether `usage` holds subagent threads, whether `--resume` carries
  totals over, and which of the two explains %24. Say plainly if a fixture is
  reconstructed and not recorded.
- R6 Apply decision 4 in `CallUsage` and `agentCall` (`calls.go`).
- R7 Apply decision 5 to chat turns, or record in the spec why they are not
  affected.
- R8 `SpendSummary`, the Spend tab and the retrospective's cost drivers show
  unmetered calls (decision 7).

### Empty queue

- R9 Trace how %20 reached `done` with no items and record it in the spec.
  Add a test that replays it: a planned milestone whose description changes.
- R10 `done` cannot run with zero items: autopilot does not close the tracking
  issue or the milestone and asks for no retrospective.
- R11 A planning run that succeeds with no issues pauses with `emptyPlan`
  (decision 8). While a planning run is pending, or the milestone waits for
  its turn, there is no pause.

### Planning

- R12 `syncSpecTask` creates an autopilot milestone's spec task with
  `RunRequest = 0` while an older milestone is open (decision 9), and asks for
  the first run when its turn comes. The description hash used for that run is
  the one at that moment: an edit while waiting neither plans nor re-plans.
- R13 The tracking issue exists from the start and says "⏳ waiting for its
  turn: %7, %9 go first", with `/agent start` as the way out. The Desk shows
  the same state. No Inbox item: nothing is asked of anyone.
- R14 `/agent start` (decision 10). On a milestone that is already planned or
  planning it answers so and does nothing.
- R15 `specRetryDue`, the pilot and `/agent pause` handle a spec task that has
  never run: no retry, no assessment, and a paused waiting milestone stays
  paused when its turn comes.

### Docs

- R16 `docs/usage.md`, `docs/commands.md`, `docs/ui.md`, `docs/configuration.md`, `docs/design.md`
  and the spec copy `docs/specs/honest-reporting.spec.md`, including the
  corrected running-totals note.

## Constraints

- Pause reasons are defused by `pause()`; agent and job text stays untrusted
  and is never put into a mention or a command.
- Budgets read the same spend. If R6 or R7 changes a project's totals, say so
  in the MR, with the direction of the change.
- System prompts stay unchanged (prompt cache).
- %7 and %9 are in flight and touch `autopilot.go`, `implement.go`, `calls.go`
  and the spend queries. Rebase, do not rework their changes.
- The default budget is under 50 production lines per MR; split by requirement.

## Non-goals

- Parsing ACP `usage_update` for cursor token counts. Cursor is out of quota
  until 2026-11-13, so it could not be checked live.
- Resolving which model `auto` picked.
- Rewriting stored `agent_calls` rows.
- Cost of subscription runs (Bloop %23): that report came from before
  `agent_calls` and is not reproduced here.
- Issues within a long milestone going stale as its earlier slices merge.
- Retrying planning after a usage limit, per-issue timing and acceptance
  evidence in the retrospective: other milestones.
- netcup01 config. Nothing here needs an infra change.

## Suggested order

1. Spec copy, then R9 and R10: the wrong "done" is the worst of the three.
2. R11, then R12, R13, R14 and R15.
3. R1, R2, then R3 and R4.
4. R5, then R6, R7 and R8.
5. R16 with each slice, one last pass at the end.

## Acceptance

- A test per cause shows its reason; a reason stays the same across polls, and
  one pause posts one comment.
- A test replays %20: no done comment, no closed tracking issue, no
  retrospective.
- A planning run with no issues ends in an `emptyPlan` pause with its way out.
- On a fixture with a subagent, by model, by issue and by kind show the same
  token total. On a two-turn chat fixture, the second call holds only that
  turn.
- A milestone with a cursor call shows it as not metered in the Spend tab and
  the retrospective.
- With an older milestone open, a new autopilot milestone gets a tracking
  issue, no planning run and no issues. When the older one is done, planning
  starts with the current description.
- `/agent start` plans a waiting milestone at once. An owner's edit while
  waiting starts nothing, and a paused older milestone keeps the younger one
  waiting.
- A milestone with a plan is not touched by the rollout, and one without
  autopilot still plans on creation.
- The spec states what was recorded and what was reconstructed for R5.

## Findings

Filled in by the MRs that establish them.

### R5: token basis (recorded vs reconstructed)

_Open: filled in by the MR for R5 / R9._

To record: whether `usage` holds subagent threads, whether `--resume` carries
totals over, and which of the two explains Bloop %24.

### R9: how Bloop %20 reached done

Traced from the code at origin/main (`internal/controller/autopilot.go`,
`internal/runner/spec.go`, `runner.go`) and replayed by
`TestEmptyQueueIsNeverDone` (`autopilot_close_test.go`). Without the R10
guard that test ends in `Phase: Done` with `Items: []`. The Bloop operator
logs were not available, so the path is the only one the code allows, not
one read from %20's events.

`done` has two callers, `pick` and `endMilestone`. "0 of 0" means
`len(a.ap.Items) == 0` when `done` ran.

The path:

1. The milestone is planned (`status.plan` = #384, #385). Nothing has
   started, so both items are `pending` (for example, the milestone was
   paused before its first pick).
2. An owner edits the description. `syncSpecTask` bumps `RunRequest`, which
   starts a re-plan.
3. The re-plan succeeds, but `planOrder` maps none of the planner's issues,
   so `out.Plan` is `[]`. The re-plan prompt says that issues "already in
   progress are context: do not re-plan them", so a planner that only lists
   what it changes returns no issues. `planOrder` returns a non-nil empty
   slice, and `runner.go` records it (`if out.Plan != nil`) before
   `ObservedRunRequest`.
4. On the next poll, `sync` computes `mergeQueue(items, queueOrder([]))`.
   That drops every pending item, so `Items` is now `[]`.
5. `sync` runs `commands()` before the plan guard
   (`len(Status.Plan) == 0`). An accepted `endMilestone` in
   `status.pilot.commands` (the "End the milestone here" option of a budget
   prompt, answered by an owner) then calls `endMilestone`. It finds nothing
   to skip and no active item, comments "The milestone ends here: nothing is
   left to skip", and calls `done`.
6. `done` sets `Done`, closes the tracking issue and comments "✅ Autopilot
   is done: 0 of 0 issues merged". `syncPilots` then asks the pilot for the
   retrospective (`autopilotDone`), and `closeMilestone` closes the milestone
   once that is in.
7. A later re-plan lists #384 and #385 again. `pick` sets `Running` ("a
   re-plan can add issues after done"), and both are merged.

Ruled out:

- `pick` → `done`. `mergeQueue` keeps every IID of the plan order, so
  `Items` is never shorter than the order. An empty order means an empty
  plan, and the guard returns before `step`. A plan that is all cycle gives
  an empty order too, but `sync` then pauses with `cycle` and does not step.
- `chatCommands`. The Desk verbs go through `command()`, which never calls
  `done`. `endMilestone` is no Desk verb.
- `budget()`. It pauses and posts prompts, and never sets `Done`
  (`budget.go:151` only reads `Done`, for `budgetStopped`).
- `pilotSelf`. `endMilestone` is an owner tool: `checkAction` refuses it in
  options the model made, so the pilot cannot end a milestone on its own,
  even under managed autonomy.
- A status write that drops the plan. Autopilot saves with a merge patch of
  `status.autopilot` only, so it cannot overwrite the `status.plan` that the
  runner patched.

R10 guards the sink. `done` with no items keeps the phase and closes and
comments nothing. `endMilestone` with no items answers "Nothing to end: …
close the milestone in GitLab or remove `/agent autopilot`" and changes
nothing. A pause stays a pause. R11 (`emptyPlan`) is what makes step 3
visible to the owners.
