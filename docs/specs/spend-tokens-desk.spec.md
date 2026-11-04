# Spend you can see, fewer tokens, cleaner Desk

## Goal

1. Know what every issue, MR and milestone cost: tokens and USD, summed per
   project → milestone → issue → run → agent call, with a drill-down in the Desk.
2. Use that data to spend fewer tokens: budgets that stop runaway work, and
   the first two levers where the data already points.
3. Clean up: one Desk navigation instead of four overlapping milestone/board
   pages, and the structural fixes the 2026-11-04 architecture review found.

## Background

What exists today:

- `Result.CostUSD` comes only from claude's `total_cost_usd` (notional API
  price on a subscription). `ParseResult` (`internal/runner/engine.go`)
  ignores the result line's `usage` (input / output / cache_creation /
  cache_read tokens), `modelUsage` (per model), `num_turns` and
  `duration_ms`. Cursor reports no cost: every cursor call counts $0
  (Bloop !702 review round: "$0.00").
- Cost lives as a string on `status.run.costUSD` per AgentTask. The UI sums
  project and milestone cost from **live** CRDs (`internal/ui/view.go`,
  `milestone.go`); deleted tasks drop out of the sums. The pilot retro sums
  CRDs too (`internal/runner/pilot_retro.go`).
- `history_events` `run.finished` carries a per-run `costUSD` delta with
  milestone/issue/MR ids (365 d retention). Only per-day sums are queried.
- Desk chat turns record no cost at all (`internal/runner/chat.go`).
- The runner configures no subagents (no `--agents`, implementer denies
  only `Bash(git push:*)`), so claude uses its built-in ones at will. The
  result line already reports `modelUsage` (tokens and cost per model,
  subagent models included) and `subagent_stats`, and every stream-json
  assistant message carries its own `usage` (the context size per turn).
  The runner ignores all three, so nobody can see whether subagents run,
  what they cost, or how fast a session's context grows.
- Real numbers (history, 2026-10-24 … 11-04, notional USD): Bloop m22
  $257 / 6 issues, m20 $248 / 3 issues, m23 $135, m21 $79; ai-operator m3
  $9 (mostly cursor, so undercounted). One Bloop run (#384, !702) cost $158:
  implementer steps 3→7 on one resumed opus session cost $17, $23, $31, $32,
  $37, rising with the session's context. Issue #396: $214 over 7 runs.
  Today nobody can see *why* without reading run reports by hand.

Architecture review findings this milestone takes up (file refs at main
66a450e):

- A single failing issue aborts the whole project sync:
  `agentproject_controller.go:424-442` returns on the first error from
  `syncImplementTask` / `syncRunBlock` / `syncBlocked` / `syncCI`. Later
  issues, main health, autopilot, pilots and cleanup are then skipped.
- Cost handled as ad-hoc strings: parse-add-format copied at
  `runner.go:509`, `:593`, `agentlost.go:153`. Precision is 4, 2 or -1
  decimals depending on the site. Budget-left math is repeated in
  `pilot.go:141`, `pilot_retro.go:231` and `pilot_assess.go:42`.
- Runner state-file errors are swallowed (`loadState` `runner.go:191`;
  `_ = r.saveState` in implement.go, describe.go, chat.go). A corrupt
  state.json can re-post notes or decisions.
- Usage-limit state lives in three shapes (`Run.LimitedEngine`/`DeferredUntil`,
  `Run.UsageLimits[]`, project aggregation), with merge helpers in runner,
  api and controller.
- Seven truncate helpers; `truncateMsg` cuts bytes and can split UTF-8.
- `AutopilotItem.Decisions` is uncapped in status (`autopilot.go:1168`).
- Inbox click and notification click route to different pages
  (`Inbox.vue:12-31` vs `inboxNotify.ts:79-86`).
- `web/src/types.ts` (703 lines) mirrors Go view structs by hand.
- `runImplement` is one 602-line function with no `implement_test.go`.
- Desk pages overlap: `Projects.vue` is unrouted (dead).
  `/milestones/:iid/detail` (Milestone.vue) remains only for the full pause
  card, queue, proposals, ask-the-pilot and a Board. `/board` (Project.vue)
  repeats the same Board, and Task.vue's autopilot section repeats the queue.
  `docs/ui.md` still describes the Projects page.

## Decisions (locked)

1. **One row per agent call** in a new Postgres table `agent_calls`
   (migration 0004, research DB). Columns: at, namespace, project, milestone_iid,
   issue_iid, mr_iid, task, run, seq, kind (spec, implement, review, fix, ci,
   conflict, describe, pilot, retro, chat, research), role, engine, model,
   effort, session resumed (bool), fallback_from, input_tokens,
   output_tokens, cache_read_tokens, cache_write_tokens, cost_usd (numeric),
   cost_source (`reported` | `estimated` | `unknown`), turns, duration_ms.
   Retention follows history (365 d).
2. **The operator stays the only DB writer.** The runner appends each call
   to a bounded `status.run.calls` list (last 50, each with a task-wide
   monotonically increasing `seq`). The history differ writes unseen seqs
   with an idempotent key (task UID + seq). This needs no new grants and no
   DB login in runner pods. Research runs already run in the operator and
   write directly.
3. **Sums come from Postgres, not from CRDs**, in the UI and in the pilot
   retro. Runs from before the cutover show their `run.finished` cost
   without token detail.
4. **Cost source per engine:** claude = reported `total_cost_usd`. Cursor =
   estimated from tokens × an optional `pricing:` table in the operator
   config (USD per million input/output/cache tokens per model), when ACP
   reports tokens. Otherwise `unknown`: the call counts with duration but no
   USD, and the UI shows "n/a", never $0.
5. **Spend is a tab** on the project and milestone stream (Stream | Board |
   Spend), plus a cost chip on stream headers, milestone list rows and Task
   pages. Drill-down: milestone → issue → run → call. Breakdown by kind,
   role and model, with cache-read share.
6. **Budgets:** an optional `/agent budget <usd>` milestone header line and a
   per-issue default (`budget.perIssueUSD` on the project, unset = none).
   At 80 % an Inbox item; at 100 % the autopilot pauses with
   `pauseKind: budget` on the existing pause card. The owner resumes with
   `/agent budget <new>`. The running agent call is never killed; the check
   runs between calls.
7. **Desk end state:** Inbox · Stream (everything / project / milestone,
   tabs Stream | Board | Spend, milestone context panel holding queue,
   proposals, full pause card and ask-the-pilot) · Task · Research.
   `/milestones/:iid/detail` and `/board` redirect to the new places, then
   Milestone.vue, Project.vue, Projects.vue and StageBar are deleted.
8. **Step-session lever:** new `implement.stepSession: resume | fresh`
   (default `resume`, today's behaviour). `fresh` starts each implementer
   step in a new session with the agent notes and a handoff summary as
   context. The milestone ships the option and a Spend comparison;
   flipping the default on netcup01 is the owner's call after the data.
9. **Subagents: measure, then opt in.** `agent_calls` also stores
   `models` (jsonb: per model input/output/cache tokens and cost, from
   `modelUsage`), `subagent_calls` (from `subagent_stats`) and
   `context_series` (jsonb int array: input + cache tokens of each
   assistant turn, capped at 500 points, evenly thinned beyond).
   A new optional `subagents:` list per role and profile (claude only) is
   passed as `--agents` JSON: name, description, model, tools, prompt. A
   role with subagents gets one prompt line telling it to delegate
   searches, lookups and log reading to them and to read only the files it
   edits itself. Default: none, so behaviour stays as it is today. The
   chart example documents a read-only `scout` on haiku
   (Read, Grep, Glob, WebFetch, WebSearch). Turning it on for netcup01 is
   the owner's call after a comparison in the Spend tab, like decision 8.

## Requirements

Observability
- R1 Parse claude's `usage`, `modelUsage`, `num_turns`, `duration_ms` into a
  `CallUsage` on `Result`; parse ACP token usage if cursor sends it (first
  slice checks with the image's cursor-agent and records the finding in the
  spec). Fixture tests for both.
- R2 Every engine call records a `CallUsage`: implement steps, reviewers,
  fix/CI/conflict rounds, describe, spec, pilot turns, retro, assess,
  research, and Desk chat turns (today uncounted). Fallback calls record both
  attempts.
- R3 `status.run.calls` (bounded, seq) and the differ writing `agent_calls`;
  `history` grants: owner writes, UI reader selects.
- R4 One cost helper in `api/v1alpha1` (`AddCost`, `CostOf`, `BudgetLeft`,
  one precision: 4 decimals stored, 2 shown) replacing every copy.
- R5 UI API: `GET /api/v1/spend?namespace&project[&milestone][&issue][&task]`
  returns totals plus children of the next level, and breakdowns by kind,
  role and model. It is gated by the existing project read grant.
- R6 Spend tab, cost chips, Task-page call table. The retro reads the DB and
  names the three biggest cost drivers (issue, kind, model), with tokens.

Token reduction
- R7 Budgets as in decision 6, incl. Inbox item and notification.
- R8 `implement.stepSession` as in decision 8. A test shows a `fresh` step
  gets the notes and handoff but no resume id.
- R9 Reviewer inline-diff cap (`maxInlineDiff`, 150 000 chars per reviewer,
  per round) becomes configurable per reviewer. The default stays. The Spend
  breakdown shows review cost per round, so the owner can tune it.

Subagents
- R19 Parse `modelUsage`, `subagent_stats` and per-turn assistant `usage`
  into `CallUsage` and the decision 9 columns. Fixture test with a recorded
  stream that includes a subagent (`parent_tool_use_id` set).
- R20 `subagents:` per role/profile → `--agents` JSON + the delegation
  prompt line, as in decision 9. Validation rejects unknown tools and
  empty names; cursor roles ignore the field with a logged warning. Runner
  test: the invocation carries the JSON and the line only when configured.
- R21 Visualise:
  - in the Spend drill-down, a stacked bar per call and per run, split by
    model (main model vs subagent models), in tokens and USD;
  - on the Task page, per run, a context-growth chart (turn on x, context
    tokens on y, one series per implementer step, markers where a
    subagent ran);
  - in the live transcript, a subagent's events fold into one collapsible
    row with its model and token count.
  Follow the repo's chart conventions. No chart library heavier than the
  one already in `web/` (add none if inline SVG does).

Cleanup
- R10 The per-issue sync loop collects errors and keeps going. Orphan
  deletion is skipped only for tasks whose issue failed (no task deleted
  because a sibling failed). Regression test: one issue's GitLab 404 does
  not stop autopilot or another issue's CI handling.
- R11 State-file load/save errors are logged. Steps that could re-post
  notes or decisions fail instead of continuing.
- R12 One usage-limit representation per run with all merge/active logic in
  `api/v1alpha1`. Old fields stay readable for one release (CRD fields are
  never removed in the same release).
- R13 One `textutil.Truncate` (rune-safe), replacing the seven copies.
- R14 Cap `AutopilotItem.Decisions` (newest 20). The full history is in
  `history_events`.
- R15 Inbox and notifications share one route mapping.
- R16 Desk consolidation per decision 7, with redirects. Update `docs/ui.md`.
- R17 Generate `web/src/types.ts` view types from Go (tygo or equivalent)
  in `make ui`. CI fails on drift.
- R18 Split `runImplement` into phase functions on the existing `state`
  struct, with `implement_test.go` covering each phase's happy path and one
  failure. No behaviour change.

## Constraints

- `CLAUDE.md` rules: `make generate manifests`, `gofmt`, `go vet`,
  `make test`, `make helm-lint`, `make ui`. No kubebuilder RBAC markers in
  `internal/ui`. Docs and the chart README follow behaviour.
- New CRD fields are optional and nothing is removed in the same release.
  The UI config is `UnmarshalStrict`: new UI config keys ship in a release
  before infra/cluster values use them.
- Target < 50 changed production lines per MR. Exceptions allowed for
  migration + differ (R3), Desk consolidation (R16) and the `runImplement`
  split (R18), each stated in its MR.
- Every MR leaves `main` releasable.
- Desk consolidation (R16) merges before the Spend tab (R6): both touch
  Stream.vue and ContextPanel.vue.
- Tests at the lowest layer that owns the behaviour: parsing in runner unit
  tests, the differ against `storetest` Postgres, the spend query in store
  tests, web logic in `web/test/*.mjs`. No template/render unit tests.

## Non-goals

- Real billing: claude cost stays the notional API price; no subscription
  quota accounting beyond the existing usage windows.
- Splitting the AgentProject god reconciler into sub-structs (beyond R10).
  This is noted for a later milestone.
- A research search page (`/api/v1/research/search` stays as is).
- Changing netcup01 config (`pricing:`, budgets, `stepSession`) in this
  milestone. Those are infra/cluster follow-ups after the release.
- Backfilling tokens for runs before the cutover.
- Killing a running agent call when a budget is hit.

## Suggested order

1. Spec copy + R4 cost helper
2. R10 sync error isolation
3. R11 state-file errors · R13 truncate · R14 decisions cap (small, any order)
4. R1 + R19 parse usage, per-model split, subagent stats, context series
   (+ cursor ACP finding)
5. R2 + R3 record every call, `agent_calls` migration + differ
6. Pricing config + estimated cursor cost (decision 4)
7. R5 spend API
8. R15 Inbox routing · R16 Desk consolidation (redirects first, then deletion)
9. R6 Spend tab, chips, Task call table, retro from DB · then R21 charts
10. R7 budgets
11. R8 step sessions · R9 reviewer diff cap · R20 subagents
12. R12 usage-limit unification
13. R17 generated TS types
14. R18 `runImplement` split

## Acceptance

- On the Desk, a closed milestone shows total USD and tokens, and drills down
  to every issue, run and call. The numbers match `agent_calls` and survive
  task deletion.
- Desk chat, pilot and cursor calls appear: cursor with an estimated or
  "n/a" cost, never $0.
- A milestone with `/agent budget 5` pauses its autopilot with a budget
  pause card once it has spent $5. `/agent budget 50` resumes it.
- One issue failing in GitLab no longer stops other issues or the autopilot.
- `/milestones/:iid/detail` and `/board` redirect. Projects.vue,
  Project.vue and Milestone.vue are gone. `docs/ui.md` matches.
- A claude run shows its per-model split and a context-growth chart; with
  a `scout` configured, subagent turns show up as their own model and as
  markers on the chart.
- `make test`, `make ui` and the CI drift check for generated TS types pass.
