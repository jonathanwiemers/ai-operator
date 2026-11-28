# Desk: a notification click opens the event it is about

Status: the milestone's spec, run by autopilot. Narrowed requirements and
requirements without an issue go into the [Steering log](#steering-log).

## Goal

1. A click or tap on any Desk notification opens the place it is about, on
   every device (iPhone with the Desk on the home screen, Android, desktop)
   and in every window state: open, frozen or discarded in the background,
   or no window.
2. A milestone started, milestone done or release notification opens that
   event's own card in the milestone's Cockpit, scrolled into view, focused
   and highlighted for about 2 s. A release card still links the GitLab
   release.
3. With an expired session, the click goes through the sign-in and lands on
   the same card, anchor included.
4. Inbox notifications keep opening where they open today.

## Decisions (locked)

- **D1: every device and window state is in scope.** The main case is the
  iPhone home-screen Desk in the background. Rejected: fixing one device.
- **D2: a click confirms or navigates.** The worker posts `{ open }` with a
  reply port. The page answers once it has routed. Without an answer within
  1000 ms the worker navigates the window to `openURL(open)`; if navigating
  is refused, it opens a new window. A notification without `open` only
  focuses. Rejected: always navigating (reloads the Desk on every click),
  and the message-only path.
- **D3: an event opens its own card.** `#event-<id>` on the milestone's
  Cockpit, or the project's when the event has no milestone. The Cockpit
  opens Show N more and the group, scrolls, focuses and highlights the card
  (a static ring with reduced motion). The release card keeps its GitLab
  link. A card not on the loaded page leaves the Cockpit at its top.
- **D4: the sign-in keeps the anchor.** `loginURL()` returns path, query and
  hash; `safeReturn` keeps an escaped fragment on an allowed path. Rejected:
  `sessionStorage` across the login (a second mechanism next to `?open=`).

## Requirements

- **R1** `clickOpen` in `notifyOpen.ts` (pure) with `clickReplyMs` = 1000;
  `sw.ts` posts with a `MessageChannel` port inside `waitUntil`;
  `deskWorker.ts` answers `{ opened: true }` after `router.push`.
- **R2** `returnPath` and `loginURL` keep the hash; `safeReturn` appends the
  escaped fragment after all its checks.
- **R3** `eventRoute(e)` and `id="event-<id>"` with `tabindex="-1"` on
  `EventCard.vue`.
- **R4** The Cockpit thread follows `#event-<id>` (`eventPlace`), once per
  hash, and highlights the card.
- **R5** The notifications guide, the Service worker and Security parts of
  the web UI page, this spec.

## Non-goals

- Paging the Cockpit back to find an older event, or a "no longer here"
  message.
- Opening GitLab straight from a notification.
- New notification kinds, texts or categories; changes to the push sender.
- Deep links in GitLab notes, emails or chat.
- Notification actions, and a "copy link" on event cards.

## Steering log

- `eventRoute` names `?tab=cockpit` on the project route, because the
  project page opens on Milestones. `cockpitQuery` (`streamTabs.ts`) and
  `scrollBehavior` (`main.ts`) learned `#event-`, so the link keeps the
  Cockpit tab and a hash change on the same page does not scroll to the top.
- A `duplicated` navigation counts as opened: the window already is on the
  place, so the worker neither navigates nor opens a window.
- `clickOpen` also covers a post that throws or a reply other than
  `{ opened: true }`: both count as silence.
- No narrowing and no follow-up issue came out of the three merge requests.
