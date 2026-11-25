# Desk: notifications you choose, that arrive, and a Desk you install on your phone

Status: done. This is the milestone's spec, run by autopilot. The GitLab
milestone description holds the full text. This file keeps the problem, the
goal, the decisions, the requirements and the [Steering log](#steering-log).
The behaviour is in [Notifications and the Desk on your phone](../guides/notifications.md),
[Notifications](../reference/notifications.md),
[Status and history events](../reference/status-and-history.md#history-events)
and [Web UI development](../contributing/web-ui.md#service-worker).

## Problem

- Stuck issues stopped notifying. Since the problem thread, a spec task's
  pauses, holds and assessment prompts reach the Inbox as `problem` items.
  The web did not notify for that kind, and it keyed every problem of a
  project the same way.
- There was one on/off switch per browser and no way to pick what notifies.
- Chrome on Android shows notifications only through a service worker, and
  the Desk had none. `new Notification` threw there, and the error was
  swallowed.
- Nothing told you that a milestone started, that its autopilot was done or
  that a release was published.
- The Desk had no manifest and no PNG icons, so it could not be installed.

## Goal

1. While a Desk tab is open, each new stuck issue, paused milestone, hold,
   question, proposal, ready companion and budget warning raises one
   notification. It comes once per item, also when a project has several
   problems at once.
2. The account menu's **Notifications** dialog turns notifications on for
   this browser and picks categories.
3. With the history database, you get a notification when a milestone
   starts, when its autopilot is done, and when a release is published or
   fails.
4. Notifications show on Chrome for Android. A click opens the item in the
   open Desk window.
5. The Desk is installable: **Install app** in Chrome, **Add to Home
   Screen** on an iPhone.

## Decisions

- **D1** Two milestones. This one fixes in-tab notifications, adds the
  settings and makes the Desk installable. A later milestone adds Web Push,
  and with it notifications on iPhone.
- **D6** Both phones: Chrome on Android, and Safari on iOS 16.4 or later
  from the home screen.
- **D7** The server owns keys and categories. `InboxItem.key` and
  `InboxItem.notify` are computed in Go, so the later push sender can use
  the same fields.
- **D8** Nine categories, all on except `failed`. `pilotError` never
  notifies.
- **D9** Settings are per device, in `localStorage` (`desk-notify`, JSON).
  The old value `on` migrates. Rejected: a per-account setting on the
  server.
- **D10** "Milestone started" means the spec task's `status.plan` went from
  empty to non-empty: a new `milestone.started` event. `autopilot`,
  `milestone.started` and `release.*` events carry `data.title`.
- **D11** The event categories need the history database. Without it they
  are disabled, with "Needs the history database".
- **D12** One service worker with no `fetch` handler and no cache. Every
  notification goes through `registration.showNotification`.
- **D13** A click goes through the app's router: `{ open }` to an open
  window, or `/?open=<JSON>`. The service worker holds no route logic.
- **D14** The PNG icons are committed and rendered from `logo.svg` by a
  Playwright script. No new dependency.
- A key that leaves the Inbox is forgotten. A problem is keyed by
  milestone, issue and pause kind, never by its reason text.

## Requirements

- **R1** `key` and `notify` on Inbox items.
- **R2** The `milestone.started` event and `data.title` on milestone and
  release events.
- **R3** Headers for `/sw.js` and `/manifest.webmanifest`, and `worker-src`
  and `manifest-src` in the CSP.
- **R4** Notification settings per browser, with `wants(category)`.
- **R5** The Notifications dialog, from the account menu and the Inbox
  button.
- **R6** The service worker, its registration, `{ open }` and `?open=`.
- **R7** In-tab Inbox notifications by server key and category, with
  problem texts.
- **R8** Event notifications from `GET /stream`.
- **R9** The manifest, icons and the safe-area insets in standalone mode.
- **R10** These docs and this archived spec.

## Merge requests

| Step | What merged |
|------|-------------|
| R1–R3 | Inbox item key and category, `milestone.started`, the service worker and manifest headers |
| R9 | Installable app, with manifest and home-screen icons |
| R4, R5 | Notification settings per browser and the Notifications dialog |
| R6 | A service worker for notifications and their clicks |
| R7 | In-tab Inbox notifications by server key and category; stuck issues notify again |
| R8 | Notifications for milestone started, milestone done and releases |
| R10 | Docs: the guide, the reference and this spec |

## Steering log

- R9 ran before R4 to R8, ahead of the suggested order, because it did not
  depend on them.
- The bundle cap rose from 142.2 kB to 142.9 kB with the Notifications
  dialog (#797). The dialog and its labels load lazily, but the new lazy
  entry made the bundler regroup shared Reka UI modules. The raise is
  recorded in [Web UI development](../contributing/web-ui.md#bundle-budget).
- Narrowed against D12: the service worker shares `notifyOpen.ts` (what a
  click opens) with the app, not `notificationText`. The page builds the
  text and calls `registration.showNotification`, so the worker never needs
  the text. Nothing moved to a follow-up, because nothing is lost until Web
  Push adds a sender in the worker.
- The page-side code (registration, showing notifications, click messages
  and the event poller) lives in a lazy `deskWorker.ts`, loaded after the
  shell, to keep the initial bundle within its budget. For the same reason,
  `localTimeNodes` moved out of `localTime.ts`, so that `localTimesText`
  pulls in no Vue.
- Added beyond the spec: the dialog says "Not supported in this browser"
  where the Notification API is missing. The category switches stay usable
  while the main switch is off.
- R10: no screenshots were added. The browser table states support facts
  once, in the reference (D802-P1), and the guide links to it.
- No requirement was dropped, and no follow-up issue was opened. Web Push
  and iPhone notifications are the planned next milestone (D1).
