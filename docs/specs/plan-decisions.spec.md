# Desk: answer a plan's decisions step by step, with the agent's pick preselected

Status: the milestone's spec, run by autopilot. Narrowed requirements and
in-flight changes go into the [Steering log](#steering-log). *Where it
landed* lists the actual paths.

## Goal

1. On a plan page, an owner clicks **Review decisions** (or **Answer N
   questions** while the agent still has open questions). A wizard shows one
   decision per step. The agent's recommended option is preselected; the
   owner can keep it, pick another option or write their own answer under
   *Other*.
2. The wizard's last step lists every decision as *kept* or *changed*, has an
   optional box for more text to the agent, and one **Send** button. The
   answers, with or without the extra text, go to the agent as one chat
   message. The agent's next turn updates the draft and the decision list.
3. A plan has a **Decisions** tab next to Chat and Draft. It shows every
   current decision with its state, who decided it and who signed it off, and
   each decision's history: every version, who decided it and every sign-off
   with its time.
4. With several owners on one plan, each sign-off and each change names the
   owner who made it. A decision the agent changed on its own shows *Not
   signed off* until an owner reviews it.
5. *Create milestone* warns when decisions are still open or not signed off,
   and still lets the owner create the milestone.

## Background

- A plan turn answered with `reply`, `draft` and `title` only. The decisions
  existed only as prose under `## Decisions (locked)` in the draft.
- Plan chat messages are rows of `chat_messages` with `plan` set. The
  controller hands a message to the plan's spec and copies the replies back.
- The milestone Decisions tab (see
  [One Decisions tab](desk-decisions.spec.md)) already had a card. This
  milestone reuses its look, not its data type.

## Decisions (locked)

1. **The agent returns decisions as data.** The plan turn's structured output
   gets a `decisions` list next to `draft`. Rejected: parsing the markdown
   section back, because the prose has no fixed option format and owner edits
   break it.
2. **Shape and limits.** Each entry has an ID (`D1`, `D2`, …), a title, a
   question, 2 to 5 options, the recommended option's index, why and a state:
   `open` for a question in the reply, `locked` for a decision in the draft.
   IDs are stable and never reused; a dropped decision leaves the list but
   stays in the history. An invalid list keeps the previous one and adds a
   fixed line to the reply. An empty list means unchanged. The limits are in
   [Desk API](../reference/desk-api.md#limits).
3. **Open questions and locked decisions share one wizard.** The button says
   **Answer N questions** while any entry is `open`, and **Review decisions**
   otherwise, in the Decisions tab and the draft's toolbar.
4. **One send, one chat message.** The wizard posts to the plan chat endpoint
   with an optional `decisions` list of `{id, version, keep|option|other}`.
   The body may be empty. The message goes through the same budget stop,
   Interrupt and queue as any chat message. The runner renders the answers in
   fixed sentences under `## The owner's answers to the decisions`.
5. **Versions, decided by, signed off by.** A version rises when the title,
   question, options, recommendation or state changes. A new version made in a
   turn that answered a change from owner X is decided and signed off by X.
   Any other new version is decided by the agent and not signed off. A `keep`
   from owner X on the current version signs it off. A change the agent did
   not apply signs off nothing. Answers in an interrupted or failed turn are
   not applied.
6. **History in the research store.** The controller copies each version and
   sign-off into two tables when it copies the plan's replies. The rows
   expire with the chat history retention. The current list stays in
   `AgentPlan.status.decisions`. Rejected: the history in the status, because
   it grows without bound next to a 64 KiB draft.
7. **The wizard sends against a version.** A stale version answers 409 "the
   decisions changed"; the wizard reloads and keeps picks of unchanged
   decisions. Only owners open it; it is off while a turn runs and on a plan
   in phase `Created`. An owner-edited draft shows a warning and still sends.
8. **Layout.** Phones: Chat, Decisions, Draft. Wider: the chat left, and
   Draft and Decisions on the right. The wizard is a dialog, full screen on a
   phone. Picks are kept in sessionStorage per plan.
9. **Preselection.** The signed-off choice when the current version is signed
   off, otherwise the recommended option. The wizard starts at the first step
   that is open or not signed off.
10. **Create milestone warns, it does not block.** The description stays the
    draft as it is.
11. **Plans only.** The milestone Decisions tab, `DecisionView`,
    `desk_decisions` and Revisit do not change.

## Requirements

- **R1** The plan turn returns decisions: schema, prompt, `status.decisions`,
  validation.
- **R2** Answers reach the agent: store column, `ChatMessage.Decisions`,
  `postPlanChat` checks and 409, the prompt's fixed sentences.
- **R3** Versions, decided by, signed off by, in the runner.
- **R4** The history tables, the reconcile step and the store reads.
- **R5** `PlanView.decisions` and `decisionsPending`, and the history
  endpoint.
- **R6** The plan's Decisions tab and its layout.
- **R7** The review wizard.
- **R8** The answers block in the chat and the Create milestone warning.
- **R9** The docs and this spec.

## Constraints

- Under 300 production lines per merge request.
- A plan without decisions and a chat message without answers behave as
  before.
- No new dependencies; the UI kit and `Markdown.vue` only.
- Every new screen works on a phone.

## Non-goals

- Sign-off for milestone decisions.
- Blocking *Create milestone* until every decision is signed off.
- Writing who signed off into the milestone's description or GitLab.
- More than one owner's sign-off per decision, or approval rules per owner.
- Editing a decision's question or options in the wizard.
- Notifications to other owners.

## Where it landed

| Requirement | Paths |
|-------------|-------|
| R1 | `internal/runner/plan_prompt.go`, `internal/runner/plan_decisions.go`, `api/v1alpha1/agentplan_types.go` (`PlanDecision`) |
| R2 | `internal/ui/plan_answers.go`, `internal/controller/chat_plan.go`, `api/v1alpha1/agenttask_types.go` (`ChatMessage.Decisions`), `internal/runner/plan_answers.go` |
| R3 | `internal/runner/plan_answers.go`, `internal/runner/plan_decisions.go` |
| R4 | `internal/research/store/migrations/20261119201113_plan_decisions.sql`, `internal/research/store/plan_decisions.go`, `internal/controller/chat_plan.go` |
| R5 | `internal/ui/plan_decisions.go`, `internal/ui/plans.go` |
| R6 | `web/src/streamTabs.ts`, `web/src/planDecisions.ts`, `PlanDecisions.vue`, `PlanDecisionCard.vue` |
| R7 | `web/src/planWizard.ts`, `PlanDecisionWizard.vue`, `PlanWizardButton.vue` |
| R8 | `ChatAnswers.vue`, `PlanCreateDialog.vue` |
| R9 | [Write a milestone](../guides/write-a-milestone.md#answer-the-agents-decisions), [Desk API](../reference/desk-api.md#plan-decisions), [Glossary](../concepts/glossary.md#sign-off), [Plan decisions](../contributing/plan-decisions.md) |

## Steering log

- D620-P1 (R9): the internals went into a new page,
  `docs/contributing/plan-decisions.md`, because they span the runner, the
  controller and the store, and `web-ui.md` is long.
- D620 (R9): `docs/guides/desk.md` is at its 400-line cap, so it has one link
  to the plan wizard and the description lives in
  `docs/guides/write-a-milestone.md`. Every limit is stated once, in
  `docs/reference/desk-api.md`.
- D612-P2 (R1): a list whose draft is refused for its size is not applied
  either.
- D614-P1, D614-P2 (R3): on an `open` decision any answer is a change, since
  there is nothing to keep; on a locked decision a pick of the recommended
  option counts as a keep.
- D614-P3, D614-P4 (R3): answers to another version sign off nothing, and a
  keep signs off the current version also when the agent leaves the list
  unchanged.
- D617-P1 (R6): one `?tab=` serves both layouts: the phone's three tabs and
  the wide layout's right pane.
- D617-P2, D617-P3 (R6): the plan card reuses `DecisionCard.vue`'s look; a
  decider or signer shows by name and falls back to the subject, and the
  agent reads "the agent".
- D618-P1 to D618-P4 (R7): the wizard answers every decision, with the
  preselection as a keep; the signed-off choice is the version's recommended
  option; the button is off while a turn runs; picks carry the version they
  were made on.
- No requirement was narrowed in the merged slices (#612 to #619) beyond the
  entries above, and no follow-up issue was opened. A milestone's sign-off
  remains a non-goal and is the next step for a later milestone.
