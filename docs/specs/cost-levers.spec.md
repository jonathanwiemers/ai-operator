# Cost levers: fresh step sessions, effort and Sonnet arms, context by tool

Status: the milestone's spec, run by autopilot. Narrowed requirements and
in-flight changes go into the [Steering log](#steering-log). The user guide
is [Cost levers](../guides/cost-levers.md).

## Goal

The owner can find out, from real issues and not from guesses, which
settings make a merged line of code cheaper. Three settings are tried side
by side, as "arms" of one experiment: fresh sessions per step, a lower
implement effort and a Sonnet 5.5 profile. Each run records its arm. The
Desk Metrics page (%32) shows the cost per 100 merged production lines per
arm. The operator also records what fills the context: how big the context
is before the agent starts (mostly `CLAUDE.md`), and how much each tool
(Read, Bash, Grep, …) puts into it. MRs stay big; none of these levers makes
them smaller.

## Background

At origin/main 86a8f9b (2026-11-16):

- **Cost grows with session length.** About 95% of an implementer's tokens
  are cache reads, and every turn re-reads the whole context. So a long
  session pays for its early turns again and again. In one game-project run
  the same step went from $17 to $37 as the context grew.
- **Fresh sessions per step already exist, but are not used.**
  `implement.stepSession: resume|fresh`
  (`api/v1alpha1/agentproject_types.go:766`): with `fresh`, every step after
  the first starts a new session from the agent notes and the step
  summaries. It applies to stepwise runs, and `workflow.stepCommits` is on
  by default, so most runs are stepwise. No AgentProject sets it, so every
  run resumes.
- **Effort.** `MaxEffort` (`agentproject_types.go:537`, default `high`)
  caps the per-issue `agent-effort::*` label. The infra `spec` role runs
  Opus at effort `high`. The `implement` role sets no effort.
- **Profiles.** The planner picks a profile per issue from the profiles'
  descriptions: today `fast` (Cursor auto) and `strong` (Claude Opus). There
  is no middle profile. Sonnet 5.5 costs half of Opus 5.5 per token
  ($2 / $10 / $0.10 cache read vs. $4 / $20 / $0.20 per million).
- **What is measured.** `agent_calls` (insert at
  `internal/research/store/calls.go:101`) records kind, role, engine, model,
  effort, resumed, tokens, cost, lines added and deleted, and read-gate
  blocks. `AgentTask` status has `ContextSeries`
  (`api/v1alpha1/agenttask_types.go:1647`, the context size per turn) and
  `ReadGateBlocks` (`:1613`).
- **Where tool results are parsed.** The runner already walks the claude
  stream's `tool_result` blocks: `readGateBlocks` and `toolResultText`
  (`internal/runner/engine.go:514`, `:541`). It does not record which tool a
  result came from or how big it is.
- **%32** (Desk Metrics) adds merged production lines per issue and the cost
  per 100 merged production lines to the Desk. This milestone builds on it
  and does not duplicate it.

## Decisions (locked)

1. **Arms, not global switches.** An AgentProject may declare
   `agents.implement.experiment` with a name and two to four arms. Each arm
   has a name and overrides of the implement role only: `stepSession`,
   `effort` and `profile`. One arm may be empty: the baseline.
2. **Assignment is deterministic.** An issue's arm is picked when its first
   implement run starts, from a stable hash of the project path, the
   experiment name and the issue IID, and then kept for every run of that
   issue (fix runs included). Labels and `/agent` commands on the issue win
   over the arm: an issue with an explicit `agent-effort::*` label or a
   profile the planner set by hand keeps it, and the run records "no arm".
   The pilot never changes an issue's arm.
3. **Record the arm everywhere.** The arm name goes on `AgentTask` status
   and into a new `agent_calls.arm` column (null = no experiment). Review,
   CI and conflict calls of the issue carry the issue's arm too, so their
   cost counts for it.
4. **Metrics per arm.** The Metrics page (%32) gets a breakdown by arm of
   the running experiment: issues, merged production lines, cost, tokens,
   and cost per 100 merged production lines. Also by model, by effort and
   by step-session mode across all runs. It shows the issue count next to
   each figure and greys out an arm with fewer than 5 merged issues: too few
   to compare.
5. **Context by tool.** The runner maps each `tool_result` to its
   `tool_use` name and sums the result size per tool name for the call, in
   bytes and in estimated tokens (bytes / 4). This goes into a new
   `agent_calls.tool_results` JSONB column
   (`{"Read": {"calls": 12, "bytes": 81234}, …}`). MCP tools keep their full
   name, and a subagent's tools count under the subagent (`Task`).
6. **Base context.** `agent_calls.base_context_tokens` is the context size
   of the first turn of a fresh session (`ContextSeries[0]`), i.e. the
   system prompt, the tool definitions and the `CLAUDE.md` files. Resumed
   calls leave it null. The Metrics page shows its median per project and
   role, so a `CLAUDE.md` trim shows up as a drop.
7. **Noisy Bash output.** A new optional `bashMaxOutputChars` on the role
   spec sets Claude Code's `BASH_MAX_OUTPUT_LENGTH` for that role's pods. If
   it is unset, the env var is not set at all. Cursor ignores it.
8. **Sonnet profile and arms are infra.** The profiles and the experiment
   live in the infra AgentProjects
   (`clusters/netcup01/applications/ai-operator/projects/`). This milestone
   ships the code. One infra companion MR (cross-repo, after the release
   that ships the code) does the following:
   - adds a `balanced` profile (Claude, `claude-sonnet-5-5`), with a
     description that tells the planner to use it for "medium" issues;
   - adds one experiment per Bloop and ai-operator AgentProject, with arms
     `baseline`, `fresh` (`stepSession: fresh`), `medium`
     (`effort: medium`) and `sonnet` (`profile: balanced`).

   The owner merges it; the autopilot does not.

## Requirements

- R1. CRD: `agents.implement.experiment`
  `{ name, arms: [{ name, stepSession?, effort?, profile? }] }`, with
  validation: 2 to 4 arms, unique names, `effort` within the effort enum and
  not above `MaxEffort`, `profile` an existing profile name. Also
  `bashMaxOutputChars` on the role spec (int, 1,000 to 200,000). The
  generated CRDs and docs are updated.
- R2. Arm assignment as in Decision 2, unit-tested: it is stable across
  runs, spread evenly over the IIDs, and an explicit label or command wins.
- R3. The run applies the arm's overrides to the implement run's
  step-session mode, effort and profile. Precedence: issue label / command >
  arm > project config > defaults.
- R4. Migration: `agent_calls.arm TEXT NULL`, `agent_calls.tool_results
  JSONB NULL`, `agent_calls.base_context_tokens BIGINT NULL`. The runner and
  the research writer fill them for claude calls. Cursor calls fill `arm`
  only.
- R5. The runner's tool-result accounting (Decision 5), with stream
  fixtures: one with Read, Bash and Grep results, one with a subagent, and
  one with an MCP tool.
- R6. Metrics page: the arm breakdown and the by-model / by-effort /
  by-step-session tables (Decision 4); a "Context" panel per project with
  the median base context per role and the top 5 tools by result tokens,
  over the selected range. Mobile layout as in %17.
- R7. `bashMaxOutputChars` reaches the pod env (Decision 7), with a test.
- R8. The infra companion MR (Decision 8), opened after the release, not
  merged by the autopilot.
- R9. Docs: a "Cost levers" section in the operator docs that says how to
  start an experiment, how to read the arm table, and when the numbers mean
  something (Decision 4's minimum).

## Constraints

- **Few, big MRs.** The owner wants big MRs: each MR costs a full CI round,
  and CI time is the bottleneck. Plan two or three MRs (for example: CRD +
  runner + migration; Metrics page; docs + infra companion), not a slice
  per requirement.
- Builds on %32 (Metrics page, merged production lines). Under the strict
  milestone order this milestone starts after %13 to %32.
- No change to how the planner picks profiles, beyond the new profile's
  description in infra.
- Do not change `stepSession`'s default. `fresh` is only switched on through
  an arm, and only after the infra MR.
- Read the `CLAUDE.md` and the agent notes before changing the runner's
  stream parsing. It is shared with the read gate and the spend accounting.

## Non-goals

- Shrinking MRs or lowering `maxStepLines`.
- Trimming the Bloop `CLAUDE.md`. That is the Bloop milestone "Agent guide:
  a short root CLAUDE.md, the details next to the code"; this milestone only
  measures it (Decision 6).
- Automatic winner selection. The owner reads the table and changes the
  infra.
- Arms for review, CI, conflict, spec or pilot roles.

## Suggested order

1. CRD fields, arm assignment and overrides, migration, runner accounting
   (`arm`, `tool_results`, `base_context_tokens`), `bashMaxOutputChars`.
2. Metrics page: arm, model, effort and step-session breakdowns, Context
   panel; docs.
3. After the release: the infra companion MR.

## Acceptance

- A project with an experiment runs its issues across its arms.
  `agent_calls` rows carry the arm, and the Metrics page shows the cost per
  100 merged production lines per arm, with the issue counts.
- Every claude call has `tool_results`, and every fresh one has
  `base_context_tokens`. The Context panel shows the median base context per
  project and role and the top tools.
- `bashMaxOutputChars` set on a role shows up as `BASH_MAX_OUTPUT_LENGTH` in
  its pods.
- The infra companion MR is open with the `balanced` profile and the two
  experiments, waiting for the owner.

## Steering log

- The plan split the milestone into three issues: #538 (CRD, arm
  assignment, migration, runner accounting, `bashMaxOutputChars`), #539
  (the Metrics breakdowns and the Context panel) and #540 (docs and the
  infra companion).
- #538: the planner records the profile and effort labels it set in a
  `plan-labels` marker of the issue description, so the arm assignment can
  tell them from a human's (Decision 2). A model label set by a human also
  keeps an issue out of the experiment. The arm is on
  `AgentTask.status.experiment`; issues of a renamed experiment or a dropped
  arm run without overrides and record no arm.
- #539: the "Metrics page" of the spec is the *Cost per merged line* and
  *Context* sections of the Desk's Costs page. "Merged production lines"
  are the lines added and deleted by an issue's implement calls once it is
  merged.
- #540 (R9): the guide is a page of its own, `guides/cost-levers.md`,
  linked from the Costs page section of the budgets-and-spend guide
  (D540-P2).
- #540 (R8): the milestone names no companion repository, and the plan set
  none on the issue (D540-P4). The infra change, a `balanced` profile and
  the `cost-levers` experiment for the Bloop and ai-operator AgentProjects,
  is given as exact YAML in the guide's example and in the merge request
  description, for the owner to apply to the infra repository by hand.
