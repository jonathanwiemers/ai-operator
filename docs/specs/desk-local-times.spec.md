# Desk: every time in your browser's time zone

Status: the milestone's spec, run by autopilot. The milestone description in
GitLab holds the full text; this file keeps the goal, the decisions and the
requirements, and the [Steering log](#steering-log). The behaviour is in
[Pauses](../guides/pauses.md#in-the-desk), [Desk API](../reference/desk-api.md)
and [Web UI development](../contributing/web-ui.md#times).

## Goal

1. Every time the Desk shows is in the browser's time zone, including times
   inside server-written reasons, GitLab notes and agent messages. A tooltip
   shows the full local time with its zone and the UTC time as written.
2. GitLab comments and issue text keep UTC, in one form:
   "2026-11-21 14:05 UTC". A Go test fails when new code writes a clock time
   another way.
3. Overview's "Spend today" and "Spend 7 days" count from the browser's local
   midnight.
4. A scheduled research shows its schedule in the schedule's own zone and its
   next run in local time.
5. Every absolute-time tooltip names its zone.

## Decisions

- **D1** The server keeps UTC and the Desk converts it. Rejected: a timestamp
  field in every view; formatting on the server in a zone sent with every
  request.
- **D2** All Desk text is converted except code. Only the full forms
  "YYYY-MM-DD HH:MM UTC" and "YYYY-MM-DD HH:MM:SS UTC" count. Times without a
  date, other zones and ISO forms stay as written.
- **D3** A schedule stays in its own zone; its next run is local.
- **D4** `GET /overview` takes `tz`. A missing or unknown zone, or `Local`,
  means UTC, so the dashboard never fails over it.
- **D5** Absolute-time tooltips name their zone (`absTime`).
- **D6** One Go helper, `timefmt.UTCMinute`, writes every UTC time, and a scan
  test keeps other clock layouts out of server text.

## Requirements

- **R1** One UTC form on the server: `internal/timefmt`, the scan test,
  `problem.ResetTime` always with the date.
- **R2** Overview in the browser's day: `tz` on `GET /overview`.
- **R3** Local times in Markdown: `web/src/localTime.ts`.
- **R4** Local times in plain server text: `LocalText.vue`.
- **R5** Local next run for researches and zoned `absTime` tooltips.
- **R6** Docs and this spec.

## Steering log

- A pause reason with a same-day reset changed once, from "16:00 UTC" to
  "2026-11-21 16:00 UTC". A milestone paused over such a reason at the
  rollout comments its reason once more. This was accepted.
- The bundle cap rose to 142.0 kB for `LocalText.vue` and the zoned
  `absTime`; the raise is recorded in
  [Web UI development](../contributing/web-ui.md#bundle-budget).
- No requirement was narrowed and no follow-up issue was opened.
