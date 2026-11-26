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
| *Plans ready* | on | A plan agent finished a turn that answered your message, also a turn that failed or was interrupted. Only you, the sender, get it. See [Ready plans](#ready-plans). | `Plan ready · <plan title>` |
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

## Ready plans

A ready plan is an Inbox item of the `plan` category. A
[plan](../concepts/glossary.md#plan-draft) agent raises it each time it
finishes a turn, for the user whose message the turn answered
([`status.answeredFor`](status-and-history.md#agentplan-status)). Other
users who can view the project see neither the item nor a notification.

- The title is `Plan ready · <plan title>`, or `Plan ready · Untitled plan`
  without a title.
- The body is the first line of the agent's reply, at most 140 characters.
  A turn that failed or was interrupted has `The turn did not finish:
  <error>` instead.
- The item stays until you send your next message, all drafts have their
  milestones, or the plan is deleted. Each turn notifies once.
- Ready plans need the plan chat, which needs the research database.
- Turns that finished before the upgrade that added ready plans never
  notify.

## The history database

*Milestone started*, *Milestone done* and *Releases* come from the
[history events](status-and-history.md#history-events) in the research
database. Without it, the Notifications dialog shows them disabled with
"Needs the history database", and only the Inbox categories notify.

While one of these categories is on, the Desk asks
[`GET /api/v1/stream`](desk-api.md) for new `autopilot`,
`milestone.started` and `release.ended` events on every task change and
every 60 seconds. Events from before the tab opened never notify.

## Push

Push sends a notification to a device while no Desk tab is open. The
[Desk](../concepts/glossary.md#desk) server sends it, through the device's
browser vendor's push service. A device must have a
[push subscription](../concepts/glossary.md#push-subscription): turn on
*Notifications on this device* in the dialog.

The dialog asks `GET /api/v1/push` ([Push API](push-api.md)). Push is off
for one of four reasons, which the dialog shows as written here:

| Reason in the dialog | What to do |
|----------------------|------------|
| Push notifications are turned off on this Desk (ui.webPush.enabled). | Set `ui.webPush.enabled: true`. |
| Push notifications need the Desk's database (database.host). | Set up the research database. |
| Push notifications need the Desk's external URL (ui.externalURL). | Set `ui.externalURL` to the Desk's https address. |
| The Desk's database is not connected yet. | Wait; the Desk connects on its own. |

With push off, in-tab notifications work as before. A device with push on
shows no in-tab notifications; the push shows each one.

| Setting | Value |
|---------|-------|
| Allowed push services (`ui.webPush.allowedHosts`) | `fcm.googleapis.com`, `*.push.apple.com`, `updates.push.services.mozilla.com`, `*.notify.windows.com` |
| Time to live | 24 hours: a push service drops a push a device did not fetch in that time |
| Urgency | `high` for *Problems* and *Questions*, `normal` for the rest |
| A subscription without a refresh | Deleted after 30 days. The Desk refreshes it on every start while you are signed in |
| Failed sends | Deleted after 10 in a row, or at once when the push service answers 404 or 410 |
| Ledger of what was sent | Kept for 30 days |

- An endpoint must be `https` on an allowed host. `*.` matches any
  subdomain, not the bare name. An empty list allows no host.
- Each device gets each notification once, also after a restart and with
  more than one replica. Turning a category on later pushes only what is
  new, not what is already there.
- Subscribing sends nothing for what is in the Inbox or happened in the last
  15 minutes.
- An item that leaves the Inbox and comes back pushes again.
- A device gets pushes only for projects its user may view, with the
  user's roles from their last Desk start.
- Turning notifications off, or signing out, deletes the device's
  subscription. Deleting the Desk's key row rotates the key; devices
  subscribe again on their next start.

## Browsers

In-tab notifications show while a Desk tab or the installed Desk is open; [push](#push) also shows them while it is closed.

| Browser | Notifications | Install |
|---------|---------------|---------|
| Chrome (desktop) | Yes, also with push | **Install app** |
| Firefox (desktop) | Yes | No |
| Safari (macOS) | Yes | *File* → *Add to Dock* (Safari 17 or later) |
| Chrome (Android) | Yes, as system notifications, also with push | **Install app** |
| Safari (iPhone, iPad) | Push only, from the Desk on the Home Screen (iOS 16.4 or later) | **Add to Home Screen**, full screen |

A click opens the item in an open Desk window and brings it forward; only
without one does the browser open a new window.

## Settings

The switch and the categories are kept per browser, not per account, and so is a push subscription. A
browser that had notifications on before the categories existed keeps them
on, with the default categories.

## Next

- [Notifications guide](../guides/notifications.md): turn them on, pick
  categories and install the Desk.
- [Status and history events](status-and-history.md#history-events): the
  events behind the milestone and release notifications.
- [Desk API](desk-api.md): the Inbox and stream endpoints.
- [Push API](push-api.md): the routes a device subscribes with.
