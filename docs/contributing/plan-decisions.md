# Plan decisions: versions, sign-off and history

How a plan's decisions travel from the agent's turn to the Desk. This page
names the source files. For what owners see, read
[Write a milestone](../guides/write-a-milestone.md#answer-the-agents-decisions);
for the endpoints and limits, [Desk API](../reference/desk-api.md#plan-decisions).
The design is in the [spec](../specs/plan-decisions.spec.md).

## The path of a decision

1. **Turn output.** `planTurnSchema` in `internal/runner/plan_prompt.go` has
   a `decisions` list next to `reply`, `draft` and `title`. The prompt tells
   the agent to keep it in sync with the draft's `## Decisions (locked)`
   section. An empty list means unchanged.
2. **Validation.** `validPlanDecisions` (`internal/runner/plan_decisions.go`)
   checks the limits, the ID form `D<n>`, that an ID at or below
   `status.decisionSeq` already exists (IDs are never reused), and that the
   recommended index is in range. A broken list is not applied; the reply
   gets the fixed line `planDecisionsInvalid`, as for a draft that is too
   large.
3. **Versions.** `mergePlanDecisions` builds the new list. A decision gets
   version 1 when it is new. Its version rises when the title, question,
   options, recommended index or state changes. A reworded `why` does not
   raise it. `status.decisionSeq` keeps the highest number seen.
4. **Who decided, who signed off.** The chat message the turn answers
   carries the owner's `Author`, `AuthorName` and the `decisions` answers
   (`internal/runner/plan_answers.go`).
   - A new version made while an owner's change for that ID was answered:
     decided and signed off by that owner.
   - Any other new version: decided by `agent`, not signed off.
   - A `keep` on the current version, or a pick of the recommended option on
     a locked decision: the owner signs off that version, and replaces an
     earlier sign-off in the status.
   - On an `open` decision any answer is a change, since there is nothing to
     keep.
   - A change the agent did not apply signs off nothing.
   - An interrupted or failed turn applies none of this.
5. **The prompt.** `buildPlanPrompt` renders the answers under
   `## The owner's answers to the decisions` in fixed sentences: the kept
   IDs on one line, each change on its own, then the line that asks the
   agent to apply them or say why not. A message without answers builds the
   prompt it always did.

## Where the data lives

- **Current list:** `AgentPlan.status.decisions` (at most 30, atomic list)
  and `status.decisionSeq`. The page's poll reads only this.
- **Answers in flight:** the `decisions` column of `chat_messages`, and
  `ChatMessage.Decisions` in the plan's spec, copied by `Chat.dispatchPlan`.
- **History:** the research store tables `plan_decision_versions` (one row
  per plan, decision ID and version) and `plan_decision_signoffs` (one row
  per owner's sign-off), created by the migration
  `20261119201113_plan_decisions.sql`. `chatPlanReply.Reconcile` upserts
  them from `status.decisions` whenever it copies the plan's replies, so a
  second reconcile of the same status stores nothing new. The status holds
  only the latest sign-off of a version; the table keeps each owner's.
- **Retention:** both tables carry `expires_at` from the chat history
  retention, and `internal/research/store/retention.go` deletes them with
  the plan's chat messages.

## The UI server

`postPlanChat` calls `checkPlanAnswers` (`internal/ui/plan_answers.go`):
exactly one of keep, option and other per answer, no ID twice, a free answer
within the limit, and every ID with the version the wizard saw. A mismatch
answers 409 "the decisions changed". `planView` adds `decisions` and
`decisionsPending`; `GET …/plans/{plan}/decisions` is in
`internal/ui/plan_decisions.go` and reads the store through
`PlanDecisionHistory`. The wizard and the tab are in
`web/src/planWizard.ts`, `web/src/planDecisions.ts` and the components
`PlanDecisionWizard.vue`, `PlanDecisions.vue` and `PlanDecisionCard.vue`.
Picks are kept in sessionStorage per plan until a send succeeds or the
versions change; on a 409 the wizard merges the reloaded list and keeps the
picks of unchanged versions.

## Next

- [Spec: plan decisions](../specs/plan-decisions.spec.md): goals, locked
  decisions and the steering log.
- [Research internals](research.md): the database and its retention.
- [Web UI development](web-ui.md#plans): the plan page.
