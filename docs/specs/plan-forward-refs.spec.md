# Desk plans: create a series whose earlier milestones name later ones

Status: archived. The milestone's spec, run by autopilot after "Desk plans:
draft a series of milestones that build on the queue"
([plan-milestone-series](plan-milestone-series.spec.md)). This file keeps
the goal, the decisions, the requirements, the acceptance and the
[Steering log](#steering-log). Where this spec and the docs disagree, the
docs win: the flow is in
[Write a milestone](../guides/write-a-milestone.md#draft-a-series-of-milestones),
the response in [Desk API](../reference/desk-api.md#plan-drafts) and the
design in [Design](../contributing/design.md#desk-plans).

## Goal

1. A plan's drafts can always be created in list order, even when an earlier draft names a later one (`%draft-N`). Creating the earlier draft no longer fails with "%draft-N is not created yet".
2. In the earlier milestone, a reference to a later draft reads as that draft's title in quotes. Once the later draft is created, the Desk replaces the quoted title with the real `%IID`, as long as the earlier milestone still waits for its turn.
3. **Create milestone** and **Create all** say before creating which references point ahead. Afterwards they say which ones got their `%IID` and which kept the title, and why.
4. The plan agent prefers references to earlier drafts, and knows how references to later drafts are written.

## Decisions (locked)

1. **A reference to a later draft is accepted.** When a draft is created, each `%draft-N` naming a later, uncreated draft of the same plan is written as `"<title of draft N>"` (straight double quotes), in the description only. Unknown keys, the draft itself and any `%draft-N` in the title still answer 422. Rejected: always refusing, which makes a series with a Non-goal pointing ahead impossible to create.
2. **The Desk remembers what it wrote.** The created draft keeps `pendingRefs` of `{key, text}`: the later draft's key and the exact quoted text. At most 5 entries, one per referenced draft.
3. **Filling in the number.** After a draft's milestone is created, the Desk edits every created draft's milestone with a pending ref to that key: it replaces every occurrence of the recorded text with `%IID` with `UpdateMilestoneDescription` and the owner's token, and drops the entry. It skips the edit and drops the entry when the milestone's Spec AgentTask has `spec.runRequest > 0` ("already planning"), the text is gone ("text changed in GitLab"), or GitLab fails (its error). A skip never undoes or fails the create.
4. **Removing a draft** drops the pending refs to it. The quoted title stays in the earlier milestone.
5. **What the owner sees.** The create response gets `filledIn: [{key, iid, state: "filled" | "skipped", reason?}]`. The Create milestone dialog says "Refers to later drafts: N. Their titles are used until they are created." when a draft points ahead. After creating, it and Create all list each `filledIn` entry.
6. **The plan agent's rule.** Refer to earlier drafts with `%draft-N` where possible; a reference to a later draft is written as its title until that draft is created, and the Desk then fills in its `%IID` while the earlier milestone waits for its turn.

## Requirements

- **R1** `PendingRefs []PlanDraftRef` on `PlanDraft` (`MaxItems=5`; `Key` `Minimum=1`, `Text` `MaxLength=300`), with the generated CRD and view types.
- **R2** `createPlanMilestone` accepts forward references and stores them as `pendingRefs` in the status patch that records `createdMilestoneIID`.
- **R3** After a create, fill in the number, answer `filledIn` and write an audit line per entry. Removing a draft drops the pending refs to it.
- **R4** The new draft-reference sentence in `planSystemPrompt`.
- **R5** The note before creating and the `filledIn` list after it in Create milestone; Create all collects and lists `filledIn` of every create.
- **R6** These docs and this spec.

## Non-goals

- Creating drafts out of list order, or reordering created milestones in the autopilot queue.
- Filling in references in milestones that are already planning, or in milestones created outside the plan.
- References to drafts of other plans.
- Holding back the planning of a new milestone until the later drafts are created.

## Acceptance

- A plan with draft 1 naming draft 2 and draft 2 naming draft 1: Create all creates both. Draft 1's milestone first shows draft 2's title in quotes, then draft 2's `%IID`, and the result lists it as filled.
- Draft 1 created into an empty autopilot queue starts planning at once; after draft 2 is created, draft 1's milestone keeps the quoted title, the result says "already planning", and draft 1 is not re-planned.
- Draft 1's milestone edited in GitLab to remove the quoted title: creating draft 2 says "text changed in GitLab" and does not edit it.
- A reference to a draft that does not exist still answers 422 naming it.
- A new plan turn's system prompt holds the new draft-reference sentence.

## Steering log

- **A separate `PlanMilestoneView`** (R3). The create endpoint answered `FollowUpMilestoneView`, shared with `POST …/followup`, before; it now answers its own `PlanMilestoneView {iid, webURL, filledIn}`, so the follow-up endpoint does not gain an always-empty `filledIn`.
- **`filledIn` names the earlier draft** (Decision 5). `key` and `iid` are the earlier draft and its milestone, the one the Desk edited or skipped, not the draft just created. The dialogs pair each entry with the new milestone's IID to say "%41: filled in %42" or "%41: kept the title (reason)".
- **A later draft without a title answers 422** (Decision 1). There is no title to quote, so it is refused like an unknown key, naming the reference. So is a description naming more than 5 later drafts, the most `pendingRefs` holds.
- **Title references report `field: "title"`** (Decision 1). Before, any refused reference reported `field: "description"`; a `%draft-N` in the title now names the field it is in.
- **The stored draft text is kept in sync** (Decision 3). When a reference is filled in, the earlier draft's stored text (its final text, read-only) gets the same `%IID`, so the plan shows what the milestone says.
- **The note counts in the browser** (R5). Create milestone counts the edited description's `%draft-N` naming a later draft without a milestone, with the server's pattern, so it follows your edits; it does not check the later draft's title, which the server refuses.
- No browser check opens the create dialogs, so no e2e mock was extended; the dialogs' wording is covered by `node --test` on the shared formatter.
- No requirement was narrowed, and no follow-up issue was opened for this milestone.
