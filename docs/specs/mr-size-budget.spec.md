# Merge request size budget per project

## Problem

Milestones land slowly because every merge request pays a fixed, serial cost:
2–3 MR pipelines (5–7 min each), the main pipeline after the ff merge
(5–6 min), the review, and a rebase plus re-review whenever main moved. That
is 30–55 minutes per MR before any implementation time.

The MR count is set by a house rule, not by the work. The Desk planner
(`internal/runner/plan_prompt.go:37-38`) writes "Under 50 production lines per
merge request" and "Go and web changes in separate merge requests" into the
Constraints of every milestone draft, for every project. The milestone-spec
skill (`skills/milestone-spec/SKILL.md:98`, `template.md:47`) does the same.
The planning agent splits milestones to match, so a 250-line feature becomes
6–8 MRs and 3–7 hours of CI waiting.

The rules are also wrong outside ai-operator: the planner tells Bloop
milestones to run `make generate manifests` and `make ts-types`.

## Goals

- Each project sets its own MR size budget in its `AgentProject`. The default
  is 300 production lines.
- Each project sets its own house rules. The planner and the planning agent
  take them from the project, not from a constant.
- Go and web changes may share an MR, unless a project's rules split them.

## Non-goals

- Enforcing the budget in CI or in the reviewer. It stays guidance for the
  planner, the planning agent and the implementer.
- The step-size gate (`maxStepLines`). It stays as it is; it bounds steps,
  not MRs.
- `autoReReviewMaxLines` (40). It bounds the interdiff of a rebase, not the
  size of the MR.
- Faster CI (skipping the redundant main pipeline after an ff merge, caching).
  That is a separate change.
- Rewriting milestones that are already planned.

## Constraints

- System prompts stay constant: they are part of the cached prompt prefix.
  The budget and the rules go into the user prompts.
- Under 300 production lines (this spec's own new budget); tests, generated
  files and docs do not count.
- `make generate manifests` after the API change; the CI drift job checks it.

## Approach

1. API: `spec.houseRules` on `AgentProject`:

   ```yaml
   spec:
     houseRules:
       maxMRLines: 300        # production lines per MR; 0 = no budget line
       constraints:           # extra rules, one line each
         - "Run `make generate manifests` after changing API types; the CI drift job fails on stale files."
   ```

   `maxMRLines` is `*int32`, minimum 0. Unset means 300 (`DefaultMaxMRLines`
   in `defaults.go`, read through `spec.MaxMRLines()`). `constraints` is
   `[]string`, empty by default.
2. Desk planner: `plan_prompt.go` loses the hard-coded house rules. The system
   prompt says "Put the project's house rules from the turn into
   Constraints". `buildPlanPrompt` adds a `## House rules` section on every
   turn. The section has the size line ("Under N production lines per merge
   request; tests, generated files and docs do not count."), the follow-up
   rule (narrowed requirement → follow-up issue and steering log line), and
   each `constraints` entry. With `maxMRLines: 0` the size line is left out.
3. Planning agent: `specSystemPrompt` replaces "roughly a few hundred changed
   lines at most" and "exceed a few hundred lines" with "the project's size
   budget in the prompt". `buildSpecPrompt` adds a `## Size budget` section
   with N and the counting rule. When the milestone's Constraints name a
   budget, the milestone wins: it is the spec. The two-layers rule stays.
4. Implementer: unchanged. The issue's sub-spec carries the milestone's
   Constraints already.
5. Skill and docs: `skills/milestone-spec/SKILL.md` and `template.md` say
   "the project's size budget (`spec.houseRules.maxMRLines`, default 300)"
   and drop the Go/web split. `docs/configuration.md` documents
   `houseRules`.

## Affected

`api/v1alpha1/agentproject_types.go`, `api/v1alpha1/defaults.go`, generated
CRD and deepcopy, `internal/runner/plan_prompt.go`, `internal/runner/plan.go`,
`internal/runner/prompts.go`, `internal/runner/spec.go`,
`skills/milestone-spec/SKILL.md`, `skills/milestone-spec/template.md`,
`docs/configuration.md`, Helm chart CRD.

Companion (after the release): `jo.wiemers/infra/cluster`, the ai-operator
`AgentProject`: `houseRules.constraints` with the `make generate manifests` /
`make ts-types` drift rule.

## Risks

- Bigger MRs take longer to review and have more review findings per round.
  Fewer rounds of CI and rebase in total should still win. Watch the first
  milestone after the change: MR count, review rounds, hours to done.
- A bigger diff is more likely to conflict on a rebase, and a conflicting
  rebase changes the reviewed diff. Strict milestone order and the landing
  lane keep the number of open MRs small.
- Between the release and the infra MR, ai-operator milestone drafts lose the
  drift rule. The CI drift job still catches stale files; the window is one
  infra MR.
- The default changes for every project, Bloop included: their drafts get
  300 instead of 50. A project that wants smaller MRs sets `maxMRLines`.

## Validation

Unit tests:
- `buildPlanPrompt`: the size line with the default, with a set value, none
  with 0; `constraints` entries appear in order.
- `buildSpecPrompt`: the budget section with the default and a set value.
- `plan_prompt_test.go`: drop the assertions on the two removed lines.

Live: plan one milestone in the Desk and check the Constraints in the draft;
then let its planning cut issues and compare the issue count with a similar
earlier milestone.

## Delivery

One MR (about 80 production lines). After its release, one infra/cluster MR
adds `houseRules.constraints` to the ai-operator `AgentProject`.

Right away, without code: the Constraints of milestones that are not planned
yet (%26, %27, %28) can change from "Under 50" plus the Go/web split to
"Under 300 production lines per MR". %25 is docs only and keeps its rule.

## Steering log

- 2026-11-15: spec written. The global agent rule (`agents/skills` GLOBAL.md)
  now lets a repository set its own budget, and ai-operator's is `< 300`
  (agents/skills!30).
- 2026-11-15: approved. %26, %27 and %28 changed by hand to "Under 300
  production lines per merge request", without the Go/web split. Implemented
  in one MR: 81 production lines added and 21 removed. With `maxMRLines: 0` the
  planning agent is told the project sets no budget.
