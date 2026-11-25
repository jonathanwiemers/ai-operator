# Desk: push notifications on your phone and desktop while the Desk is closed

Status: done. This is the milestone's spec, run by autopilot. The GitLab
milestone description holds the full text. This file keeps the problem, the
goal, the decisions and the [Steering log](#steering-log).
The behaviour is in [Notifications and the Desk on your phone](../guides/notifications.md),
[Notifications](../reference/notifications.md#push),
[Push API](../reference/push-api.md),
[Security](../concepts/security.md#push-notifications) and
[Web UI development](../contributing/web-ui.md#service-worker).

## Problem

[Desk notifications](desk-notifications.spec.md) show only while a Desk tab
is open. An iPhone shows none, and nobody learns of a stuck issue or a
release while the Desk is closed.

## Goal

1. With notifications on for a device, it gets notifications for the picked
   categories while no Desk tab is open: iPhone (Home Screen app), Android
   and desktop.
2. Each device gets each notification once, also after a restart or with
   several replicas. A subscribed device shows no in-tab notifications.
3. A user gets pushes only for projects they can view. Pushes stop when they
   turn notifications off, sign out, or don't open the Desk for 30 days.
4. Without a database or `ui.externalURL`, push is off; the dialog says why.

## Decisions

- **D1** Postgres stores subscriptions and the VAPID key. Rejected: a
  Kubernetes Secret, which would give the Desk write access to Secrets.
- **D2** Web Push with the Go standard library (`aes128gcm`, VAPID ES256);
  no new dependency.
- **D3** The UI server sends; one replica at a time, through an advisory
  lock.
- **D4** A ledger (`push_sent`) is claimed before sending and unclaimed on
  failure. Subscribing seeds it.
- **D5** Subscriptions keep the username and roles of the last `PUT`; 30 days
  without one deletes them.
- **D6** Only the hosts of `ui.webPush.allowedHosts` (anti-SSRF).
- **D7** The payload is the item or event, not the text; TTL 24 h, urgency
  `high` for problems and questions.
- **D8** 404 or 410 deletes a subscription at once; 10 failures in a row too.

## Steering log

- **Reader grants.** The Desk's database login (the reader) may write
  `push_keys`, `push_subscriptions` and `push_sent`, and nothing else. The
  grants are explicit in `Migrate`, not by default privileges, and the
  research and chat writers get no access. Security documents it.
- **Lock per cycle.** The advisory lock is taken at the start of each cycle
  and released at its end, not held for the process's life. A replica that
  dies loses it with its connection, and the next cycle of another replica
  takes over.
- **An item that comes back pushes again.** Each cycle forgets ledger rows
  of Inbox keys that left the Inbox; event keys stay until the 30-day
  cleanup.
- **Resubscribing without categories.** `PUT` without `categories` keeps the
  stored ones (a new subscription gets the defaults); an empty list picks
  none. The service worker cannot read the device's settings, so it
  resubscribes after `pushsubscriptionchange` without them.
- **Seeding failure.** When seeding the ledger fails, the new subscription
  is deleted again, so the device never gets the backlog.
- **A subscription of another user.** `PUT` of an endpoint stored for
  another username answers 409 and leaves the row alone.
- **Redirects.** The sender follows no redirect: a 3xx counts as a failure,
  so a push service cannot lead it past the allowed hosts.
- **Page split.** The push routes moved to their own
  [Push API](../reference/push-api.md) page: `desk-api.md` was at 379 lines.
- No requirement was narrowed, so there is no follow-up issue.
