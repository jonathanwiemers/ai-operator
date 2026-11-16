# Autopilot works on several issues of a milestone at once

Status: the milestone's spec, run by autopilot. Narrowed requirements and
in-flight changes go into the [Steering log](#steering-log). Behaviour docs
landed in #475; this file is the steering log and the locked plan.

## Goal

1. Autopilot implements independent issues of a milestone at the same time,
   up to a limit the owner sets (default 3), and starts each issue as soon as
   the issues it depends on are merged.
2. Merges stay safe on GitLab Free with fast-forward merges: the issues land
   one after another, each with CI on the head it lands with.
3. A problem with one issue holds that issue, not the milestone; the other
   issues go on.
4. The Desk shows which issues run, land, wait or are held, in waves, and lets
   the owner change the limit.

## Background

File refs are at origin/main 959e032, 2026-11-13. This milestone runs after
"Runs that hang get noticed and restarted" (%23) by strict milestone order.

What exists:

- **The plan is a graph.** The planner gives every sub-issue `dependsOn`
  (`internal/runner/prompts.go:35`, `PlannedIssue.DependsOn`
  `api/v1alpha1/agenttask_types.go:1705`); `queueOrder`
  (`internal/controller/autopilot.go:284`) sorts it topologically. Recent
  plans are wide: %20 has four issues without dependencies (#391, #395,
  #396, #397), %13 six of eight, %17 many layers of 5–15 issues.
- **One issue at a time.** `step` (`autopilot.go:376`) drives the first
  `Active` item and only calls `pick` (:394) when there is none; `pick`
  activates one pending item whose dependencies are merged (`startable`
  :473).
- **One run per project.** The cluster's project defaults set
  `maxConcurrentRuns: 1` ("one agent pod per project",
  infra `clusters/netcup01/applications/ai-operator/projects/defaults.yaml:17`);
  further runs queue (`agenttask_controller.go:205`). The pilot is outside
  this limit. Agent pods request 1 GiB (limit 4 GiB).
- **Merging.** ai-operator and Bloop merge fast-forward only, with "pipeline
  must succeed"; merge trains and merged-results pipelines are Premium. The
  merge gate (`drive` :869) handles a default branch that moved:
  `need_rebase` → `rebase` (:1369, GitLab rebase, change hashed by
  `diffHash` :1508) → `rebased` (:1393): same change → the review holds and
  CI runs on the new head; changed → interdiff check and re-review; conflict
  → `mergeBase` (:1350), an agent run merges the default branch in. MR CI
  takes 8–10 min on ai-operator, 3–15 min on Bloop.
- **One pause per milestone.** `AutopilotStatus` (`agenttask_types.go:1891`)
  has one `pausedReason`, `pauseKind`, `pausedFor` (:1900–1912);
  `pause` (`autopilot.go:1615`) stops the whole queue. Pause cards
  (`web/src/components/PauseCard.vue`), the tracking issue, `/agent resume`
  and the pilot's events and tools assume a single paused issue.
- **Holds that stay as they are.** Companions wait per target project
  (`companionHold`, `companions_queue.go:138`); a red default branch stops
  starts and merges (`mainRedLane`, `freeze.go:132`); the budget pauses the
  milestone (`budget.go:33`); older milestones hold younger ones
  (`queuedAhead`, `agentproject_controller.go:604`).
- **Desk.** `MilestoneView` (`internal/ui/milestone.go:28`, built at :428)
  and `MilestoneIssues.vue` list the issues in queue order with one state
  each.

## Decisions (locked)

1. **Graph, not barriers.** An issue starts when every issue it depends on
   is merged and a slot is free; there is no wave barrier. Waves are the
   layers of the graph, for display only. When the owner wants a barrier,
   the plan says so with `dependsOn`.
2. **Limit.** New `spec.autopilot.maxParallel` on the AgentProject
   (default 3, minimum 1), overridden per milestone by a description line
   `/agent parallel N` and from the Desk (`parallel N` chat command on the
   milestone's autopilot, owners only). `1` is today's behaviour. Running
   counts `Active` items that are not landing (Decision 3) and not held
   (Decision 5); new starts pick pending items in queue order.
3. **Land one at a time.** Fast-forward merges stay. An item whose head is
   reviewed, green and mergeable is *ready to land*. At most one item per
   milestone is *landing* (rebase → CI → merge, the existing gate); ready
   items wait in the order they became ready. The landing item does not
   count against `maxParallel`. Merge commits instead were considered and
   rejected: CI would never see the merged result on GitLab Free.
4. **Siblings apart.** The planner prompt says: issues without a dependency
   between them may run at the same time, so they must not change the same
   files; if they would, one depends on the other. Conflicts that still
   happen go through `mergeBase` as today.
5. **Holds per issue.** Issue problems hold only that item: `ciRed`,
   `ciCanceled`, `findings`, `reviewErrors`, `notReviewed`, `runFailed`,
   `needsInput`, `mergeRequest`, `companion`, `stalled` (%23). The item gets
   `hold {kind, reason, since}`; its slot is free while held. Milestone
   pauses stay for `owner`, `budget`, `mainRed`, `config`, `cycle`,
   `emptyPlan`, `release`, and `stuck` when nothing can move (every running
   item held, nothing startable). `/agent resume #iid` (and the item's card)
   lifts one hold; `/agent resume` without a number lifts the milestone pause
   and every hold. `pausedFor` and friends stay for milestone pauses; the
   tracking issue lists holds under the pause line.
6. **Pilot.** One event per held item (as per paused issue today); every
   pilot tool already takes an issue. The pilot prompt says issues may run in
   parallel and a hold is per issue. `maxInterventionsPerIssue` unchanged.
7. **Cluster.** ai-operator and bloop get `maxConcurrentRuns: 4` in infra
   (three implementers plus a chat or fix run); other projects stay at 1.
   The operator warns in the tracking issue when `maxParallel` exceeds
   `maxConcurrentRuns` (runs then queue).
8. **Desk.** The milestone page groups issues as *Running (n of N)*,
   *Landing*, *Ready to land*, *Waiting* (with what for), *Held* (reason,
   Resume) and *Done*, and shows the waves; a stepper sets the milestone's
   limit (owners). Pause cards show one card per hold.
9. **No change** to review rules, budgets, CSP, or the order of milestones.

## Requirements

### Queue (Go)

- **R1** `maxParallel` on the AgentProject, `/agent parallel N` in the
  milestone description, effective limit in status
  (`autopilot.maxParallel`). `make generate manifests`. Tests: default,
  override, `1`.
- **R2** `step` drives every `Active` item and `pick` starts pending items
  while slots are free (Decisions 1, 2). Tests: two roots start together,
  a dependant waits for both, limit respected, `1` keeps the old order.
- **R3** Landing lane (Decision 3): ready items wait, one landing at a time,
  FIFO by ready time; rebases only for the landing item. Tests: three ready
  items land one after another, a held landing item frees the lane.
- **R4** Per-item holds (Decision 5): `AutopilotItem.Hold`, pause kinds
  split into item and milestone kinds, `stuck` when nothing can move,
  tracking issue lists holds. Tests: CI red on one item holds it and the
  other merges; owner pause stops all.
- **R5** `/agent resume #iid` and `parallel N` (chatcmd + tracking-issue
  words, owners only).
- **R6** Pilot events and prompt for holds (Decision 6).
- **R7** Planner prompt rule for siblings (Decision 4); maxParallel vs
  maxConcurrentRuns warning (Decision 7).

### Views (Go)

- **R8** `MilestoneView` gains per-item lane (running, landing, ready,
  waiting with reason, held with hold, done), wave number, running count
  and limit; pause view gains holds. `make ts-types`.

### Web

- **R9** Lanes and waves on the milestone page, limit stepper, Resume per
  hold, one pause card per hold (Decision 8); light and dark, 375px.

### Infra (companion)

- **R10** `maxConcurrentRuns: 4` for ai-operator and bloop in
  `jo.wiemers/infra/cluster`, after the release with R2–R4.

### Docs

- **R11** `docs/design.md` (parallel queue, landing lane, holds),
  `docs/commands.md` (`parallel`, `resume #iid`), `docs/configuration.md`
  (`maxParallel`), `docs/ui.md`, chart README, and
  `docs/specs/parallel-issues.spec.md` with a steering log.

## Constraints

- Under 50 production lines per MR; Go and web in separate MRs. R1–R4 in
  order; R4 may be split by pause kind.
- With `maxParallel: 1` the queue behaves as before; existing autopilot
  tests pass unchanged.
- When a requirement has to be narrowed, open a follow-up issue and say so
  in the steering log; every R has an issue or a steering log line.
- No new dependencies.
- Only the planner and pilot system prompts change.
- `make test`, `go vet ./...`, `gofmt`, `make helm-lint`, `make ui`,
  `make ts-types`; CI and the drift job green.

## Non-goals

- Two milestones of a project at the same time.
- Merge trains, merged-results pipelines, or switching projects to merge
  commits.
- Stacked merge requests (an issue still starts after its dependencies are
  merged).
- Predicting file overlap in the operator.

## Suggested order

R1 → R2 → R3 → R4 → R5, R6, R7 → R8 → R9 → R10 → R11.

## Acceptance

- On a milestone with two independent issues and a third depending on both,
  the first two run at the same time, land one after another, and the third
  starts after both are merged.
- With `/agent parallel 1` the milestone runs one issue at a time.
- A red pipeline on one running issue holds it; the other issue merges; the
  milestone is not paused.
- The milestone page shows the lanes and waves; the stepper changes the
  limit; Resume on a held issue starts it again.

## Steering log

- R1 #457 (`spec.autopilot.maxParallel`, effective limit in status),
  #458 (`/agent parallel N` line).
- R2 #459 (step drives every active issue, pick fills free slots).
- R3 #460 (landing lane, `status.autopilot.landing`, `items[].readyAt`).
- R4 #461 (hold type, item and milestone kinds), #462 (holds on the gate,
  `stuck`), #463 (holds end by themselves), #464 (`resume` lifts every hold,
  tracking issue lists holds and lanes).
- R5 #465 (`resume #N`), #466 (`parallel N`, owners only).
- R6 #468 (pilot events per hold, pilot prompt). The mid-milestone release
  followed it.
- R7 #467 (planner sibling rule, maxParallel vs maxConcurrentRuns warning).
- R8 #469 (lanes, waves, running count, limit), #470 (holds with ways out,
  an Inbox item per hold).
- R9 #471 (lane groups, Resume per hold), #472 (waves, limit stepper),
  #473 (one pause card per hold).
- R10 #474: companion change in `jo.wiemers/infra/cluster`
  (`maxConcurrentRuns: 4` for ai-operator and bloop). Landed as a companion
  merge (this issue depends on #474; the milestone's `/agent repo
  jo.wiemers/infra/cluster` line made R10 a companion change rather than a
  manual step for the owner).
- R11 #475 (behaviour docs: `docs/design.md`, `commands.md`,
  `configuration.md`, `usage.md`, `ui.md`, chart README) and #476 (this
  spec and index entry). Documentation only; no release after it.

### In-flight changes while the milestone ran

- The `/agent repo jo.wiemers/infra/cluster` line was added to the
  milestone, so R10 runs as a companion change instead of a manual step.
- The constraint "under 50 production lines per MR" was raised to the
  project's per-MR size budget of 300 production lines (commit 80f4cc6,
  per-project MR size budget). The issues already planned kept their small
  slices.
- `stalled` from %23 is an item kind (`api/v1alpha1/pause.go`,
  `ItemPauseKinds`).

### Narrowings and planning decisions

- At an effective limit of 1, item problems still pause the milestone, so 1
  is exactly today's behaviour.
- `mergeBase` frees the landing lane; the item lands again once its new head
  is reviewed and green.
- The `/agent parallel` range is 1–10, and a value outside it is ignored.
- Holds carry `resumeWhenReady`, `runBy` and `runHead` besides kind, reason
  and since.
- Hold events use the paused-issue key, and there is no stalled event per
  hold.
- The main-red fix item is never held.
- Holds are not written to the history database's pause events, so the
  Desk's pause history and the issues' paused time do not include holds.
  Follow-up #502 (History: record per-issue holds as pause events)
  (D476-P1).
