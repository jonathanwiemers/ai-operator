# Notifications

Look things up here: the notification categories of the
[Desk](../concepts/glossary.md#desk), their defaults, the Inbox items and
history events behind each, and which browsers show them. How you turn
notifications on, pick categories and install the Desk is in the
[Notifications guide](../guides/notifications.md).

## Example

Two issues of an [autopilot](../concepts/glossary.md#autopilot) milestone
get stuck, and later its release is published. With the default categories,
a Desk tab shows three notifications:

```text
#712 stuck · Docs: notifications
The merge request's CI failed three times on the same job.

#714 held · Desk: the Notifications dialog
Waits for the companion merge request.

Released 2026.11.4 · ai-operator
Desk: notifications you choose
```

The first two are *Problems*, from the Inbox; the third is *Releases*, from
a history event.

## Categories

The order is the order of the Notifications dialog.

| Category | Default | What notifies | Title |
|----------|---------|---------------|-------|
| *Problems* | on | A [problem](../concepts/glossary.md#problem-thread) Inbox item: a stuck issue, a [hold](../concepts/glossary.md#hold), or a paused milestone. One per milestone, issue and pause kind. | `#712 stuck · <issue>`, `#712 held · <issue>`, `Milestone paused · <milestone>` |
| *Questions* | on | A [prompt](../concepts/glossary.md#prompt) of an agent or the pilot, and a task that needs your input. | `Question Q3 · <project>`, `#712 needs input` |
| *Proposals* | on | A pilot proposal that waits for your answer. | `Proposal P4 · <project>` |
| *Companions ready* | on | A [companion](../concepts/glossary.md#companion-repository) merge request ready for you to merge. | `Companion ready to merge · <project>` |
| *Budget warnings* | on | An issue that spent 80 % of its [budget](../concepts/glossary.md#budget). | `Budget at 80 % · #712 · <project>` |
| *Milestone started* | on | A `milestone.started` event: the planner split the milestone into issues. Needs the history database. | `Milestone started · <project>` |
| *Milestone done* | on | An `autopilot` event with phase `Done`. Needs the history database. | `Milestone done · <project>` |
| *Releases* | on | A `release.ended` event with state `Released` or `Failed`. Needs the history database. | `Released <version> · <project>`, `Release failed · <project>` |
| *Failed or blocked tasks* | off | A task that failed or is blocked. | `#712 failed`, `#712 blocked` |

- The body of an Inbox notification is the item's reason or title; times in
  it are in your browser's time zone.
- A milestone or release notification has the milestone's title in its body.
  A failed release has its reason first.
- A rejected status write of the pilot is an Inbox item that never
  notifies.
- An Inbox item notifies once while it stays in the Inbox. When it leaves
  and comes back, it notifies again. A problem keeps its identity when its
  reason text changes.

## The history database

*Milestone started*, *Milestone done* and *Releases* come from the
[history events](status-and-history.md#history-events) in the research
database. Without it, the Notifications dialog shows them disabled with
"Needs the history database", and only the Inbox categories notify.

While one of these categories is on, the Desk asks
[`GET /api/v1/stream`](desk-api.md) for new `autopilot`,
`milestone.started` and `release.ended` events on every task change and
every 60 seconds. Events from before the tab opened never notify.

## Browsers

Notifications show only while a Desk tab or the installed Desk is open.

| Browser | Notifications | Install |
|---------|---------------|---------|
| Chrome (desktop) | Yes | **Install app** |
| Firefox (desktop) | Yes | No |
| Safari (macOS) | Yes | *File* → *Add to Dock* (Safari 17 or later) |
| Chrome (Android) | Yes, as system notifications | **Install app** |
| Safari (iPhone, iPad) | None yet: iOS needs Web Push, which a later release brings | **Add to Home Screen** (iOS 16.4 or later), full screen |

A click opens the item in an open Desk window and brings it forward; only
without one does the browser open a new window.

## Settings

The switch and the categories are kept per browser, not per account. A
browser that had notifications on before the categories existed keeps them
on, with the default categories.

## Next

- [Notifications guide](../guides/notifications.md): turn them on, pick
  categories and install the Desk.
- [Status and history events](status-and-history.md#history-events): the
  events behind the milestone and release notifications.
- [Desk API](desk-api.md): the Inbox and stream endpoints.
