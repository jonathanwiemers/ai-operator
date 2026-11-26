# Desk: a notification on your phone and desktop when a plan agent has answered

Status: archived. The milestone's spec, run by autopilot after "Desk: push
notifications on your phone and desktop while the Desk is closed"
([desk-push](desk-push.spec.md)). This file keeps the goal, the decisions,
the requirements, the acceptance and the [Steering log](#steering-log).
Where this spec and the docs disagree, the docs win: the flow is in the
[Notifications guide](../guides/notifications.md#when-a-plan-agent-answers-you),
the category in the
[Notifications reference](../reference/notifications.md#ready-plans), the
field in [AgentPlan status](../reference/status-and-history.md#agentplan-status)
and the design in [Web UI](../contributing/web-ui.md#inbox).

## Goal

1. When a plan agent finishes a turn, the sender of the message it answered
   gets **Plan ready · <plan title>**: as a push while the Desk is closed,
   otherwise in an open Desk tab. A click opens the plan. A turn can be the
   first draft, an answer to a follow-up, or a turn that failed or was
   interrupted.
2. The Inbox lists the plan as **Plan ready** until you send your next
   message, create its milestones or delete the plan.
3. Only the sender gets the notification and the Inbox item.
4. The Notifications dialog has a **Plans ready** category, on by default.
   Upgrading sends nothing for turns that finished before it.

## Decisions (locked)

1. **A plan is ready when its agent finishes a turn** (D1), every turn,
   also a failed or interrupted one and the "interrupted by a restart"
   reply.
2. **Only the sender of the answered message** (D2). The runner records the
   message's `Author` as `status.answeredFor`; only a principal whose
   `Username` equals it, and who can view the project, gets the item.
3. **A ready plan is an Inbox item** (D3) of kind and category `plan`, keyed
   `<namespace>/<project>/plan/<plan>/<message ID>`, so every turn notifies
   once. It stays while the phase is not `Working`, `Created` or `Failed`,
   `spec.chatMessages` is empty, and the plan exists.
4. **A new category, on by default** (D4): `plan`, **Plans ready**, "A plan
   agent answered your message.", in the web's and Go's category lists and
   defaults.
5. **No backfill** (D5): an empty `status.answeredFor` gives no item.
6. **Text and click** (D6): title "Plan ready · <plan title>" (else
   "Untitled plan"); body "The turn did not finish: <error>" or the first
   non-empty line of the reply, at most 140 runes, shown as text; a click
   opens the plan page.

## Requirements

- **R1.** `AgentPlanStatus.AnsweredFor`, set by the runner in the status
  patch that appends the turn's reply, on every path.
- **R2.** `InboxItem.Plan`; a pure function builds one principal's plan
  items, which the `inbox` handler and `pushCandidates` append when the chat
  login is configured. `inboxOf`, Needs you and the milestone rows do not
  change.
- **R3.** With the chat login, the UI server watches AgentPlans and
  publishes `plan` events; the push sender wakes on them; `plan` is a push
  category and a default.
- **R4.** The Desk: the category in the dialog, the notification text, the
  route to the plan page, a *Plan ready* Inbox row, and an Inbox reload on
  `plan` events. The service worker is unchanged.
- **R5.** Docs: the guide and reference for notifications, the plan guide,
  `status.answeredFor`, the Inbox design, and this spec.

## Non-goals

- Notifying on a failed plan pod or a plan that waits to start.
- Keeping quiet while the plan is open or on the device that sent the
  message.
- Notifying other owners or the plan's creator.
- Counting ready plans in Needs you or on milestone rows.
- Read state, pilot or task chat notifications, and changes to push
  delivery.

## Acceptance

- You send a message on a plan and close the Desk. When the agent answers,
  your phone shows "Plan ready · <title>" with the start of the answer;
  tapping it opens the plan.
- On a desktop without push, an open tab raises one in-tab notification. A
  turn that hits the usage limit says "The turn did not finish: usage limit
  until …".
- The Inbox shows **Plan ready** until your next message, or until all
  drafts have milestones.
- A second owner who did not send the message gets nothing.
- The dialog lists **Plans ready**, on. With it off, the answer shows in the
  Inbox but notifies nothing on that device.
- Right after the upgrade, no plan that answered before it notifies.

## Steering log

- **R1–R3 shipped in one merge request** (#871): the runner's
  `answeredFor`, the plan Inbox items, the `plan` events and the push
  category. R4 and R5 shipped together in a second one (#872), since the docs
  describe the finished feature (D872-P1).
- **No release between the two merge requests** (D872-P4): without the web
  part, a plan push would show the generic fallback text.
- **The chart's UI role is unchanged.** It already lists and watches
  `agentplans` when the database is set, which every install with the chat
  login has.
- **Plans ready sits after Budget warnings** (D872-P2), before the
  history-based categories: it comes from live Inbox items like the ones
  before it. The first merge request had put it after *Releases*; the
  second moved it.
- **`status.answeredFor` is documented in Status and history** (D872-P3),
  in a new AgentPlan status section: no reference page described AgentPlan
  status before.
- **The plan row shows the reply as plain text**, not inline markdown like
  the other rows, because the reply is untrusted.
- **The new browser check is not in the journey set**: the journey set is
  capped, and a plan row is not part of the daily loop. The existing journey
  check of the Notifications dialog also checks that *Plans ready* is on.
- No requirement was narrowed, and no follow-up issue was opened for this
  milestone.
