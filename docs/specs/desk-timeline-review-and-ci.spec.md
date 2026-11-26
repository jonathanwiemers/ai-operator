# Desk timeline: review and CI time on every issue bar

Status: the milestone's spec, run by autopilot. The milestone description in
GitLab holds the full text, with its background and file references; this
file keeps the goal, the decisions, the requirements, the constraints and
the acceptance, and the [Steering log](#steering-log). It builds on
[Desk: a timeline of which issue runs when](desk-timeline-and-milestone-order.spec.md).
The behaviour is in
[Order milestones and read the timeline](../guides/milestone-order-and-timeline.md#the-issue-timeline),
[Status and history](../reference/status-and-history.md),
[Desk API](../reference/desk-api.md#timeline) and
[Web UI development](../contributing/web-ui.md#timeline).

## Goal

1. On a milestone's Issues tab **Timeline**, an issue's bar shows five kinds
   of time: **running** (the implementer or a fix run), **review** (the
   reviewers), **CI** (a pipeline of the issue's merge request), **waiting**
   (everything else up to the merge) and **held**. The gaps between runs are
   no longer blank. A pipeline shows as CI, also one started by a rebase
   without a new push.
2. Every segment kind can be seen and told apart in light and dark mode.
   Waiting is drawn with a visible hatched fill. Each kind's stripe has at
   least 3:1 contrast against the surface. A key under the timeline names
   all five kinds.
3. The Overview **Timeline** keeps colouring bars by milestone. A 3 px
   stripe in the segment kind's colour shows the five kinds there too.
4. Hovering a bar, or reaching it with a screen reader, gives the split:
   "#42 Add parser, running 12 min, review 4 min, CI 18 min, waiting 6 min,
   merged 14:05". An open issue ends with what it is doing now: "in CI since
   14:05".
5. Issues that ran before this milestone shipped also get review segments,
   and CI segments from their push to their CI result.

## Decisions

1. **Five segment kinds.** `running`, `review`, `ci`, `waiting` and `held`.
   Waiting is every part of an issue's life, from its first run to its
   merge, skip or now, that is none of the others. Rejected: a separate
   "waiting to merge" kind; it needs state that is not recorded.
2. **The CI start comes from a new history event, `ci.started`.** The
   differ writes it once per pipeline when `status.ci` gets a new pipeline
   in state `Pending` or `Running`, with `sha`, `pipelineID` and `round`.
   For issues without it, the CI start falls back to the issue's `push`.
   Rejected: pairing pushes with CI results only; that misses rebase
   pipelines in review.
3. **Review time comes from the reviewer calls.** A review segment is the
   union of `[At, At + DurationMS)` of the issue's `agent_calls` with
   `kind = 'review'`. A review still running shows as running until its
   call is recorded. Rejected: new review events; they would only work for
   new issues.
4. **The Overview keeps milestone colours and adds a kind stripe.** The
   fill stays the milestone colour mixed into the surface; the stripe takes
   the kind's colour; the segment key sits next to the milestone legend.
5. **Precedence where kinds overlap:** review, then running, then CI, then
   held, then waiting.
6. **Colours.** Five `--segment-*` tokens mapped to existing tones: running
   progress fg, review info fg, CI neutral fg, waiting muted fg, held
   warning fg, each at 3:1 on `--surface`. The stripe takes the token, the
   fill is the tone bg (Issues tab) or the milestone mix (Overview). Waiting
   gets a diagonal hatch. No new colour families.
7. **`ci.started` in the stream.** On the Activity tab and folded in the
   Cockpit, with the feed line "CI started on #42". The Cockpit's CI round
   count stays on `ci` events. No notification.
8. **The CI segment rule.** A CI segment starts at the first `ci.started`
   or `push` after the previous CI end, and ends at the next `ci` event, the
   merge or skip, or now.
9. **The API answer keeps its shape.** `segments[].kind` gains `review` and
   `ci`; nothing else changes.

## Requirements

- **R1** `ci.started`: the history event, its stream kind and its feed line.
- **R2** Review and CI segments in the store: `TimelineEvents` reads `push`,
  `ci` and `ci.started`, a store read of the review calls, and
  `BuildTimeline` cuts each life into the five kinds.
- **R3** The `timeline` endpoint passes the review calls on; the view's
  `kind` type lists five kinds.
- **R4** Layout and names in the web: the split per kind in a bar's name,
  the open endings, the exported segment key.
- **R5** Drawing: the five tokens, the stripes, the hatch, the key on both
  views, and the UI kit entry.
- **R6** Docs: the guide, the two reference pages and this spec.

## Constraints

- Under 300 production lines per merge request.
- No new dependencies and no database migration.
- The timeline's parameters, the 7-day cap, visibility, `history: false`,
  the wave grouping, packing, ticks, the now line and the bar links work as
  before. So do the Cockpit's CI round count and the `ci` event.
- `Timeline.vue` stays the only custom timeline component; the
  `raw-control` check, the axe check and the bundle budget stay green.

## Non-goals

- Showing a review while it runs, before its call is recorded.
- CI segments for rebase pipelines from before this milestone, or for a
  companion repository's pipelines.
- Splitting waiting into its reasons, or running into implement, fix and
  conflict runs.
- Time before an issue's first run, and any forecast.
- Notifications for `ci.started`; changes to the Metrics page.

## Acceptance

- An issue that ran 20 minutes with a 5-minute review at its end, then CI
  for 15 minutes, then waited 10 minutes and merged, shows running, review,
  CI and waiting in that order up to the merge marker, with no blank part.
- A merge request rebased in review after a green pipeline shows a second
  CI segment from the rebase pipeline's start to its result.
- In light and dark mode the five kinds are told apart by their stripes,
  waiting shows its hatch, and the contrast check passes for the five
  tokens. The key lists all five.
- On the Overview, bars keep their milestone colours and show the kind
  stripes, and the key shows the five kinds next to the milestone legend.
- Hovering #42 shows "#42 …, running 15 min, review 5 min, CI 15 min,
  waiting 10 min, merged HH:MM"; an issue in CI now ends with "in CI since
  HH:MM".
- An issue merged before this milestone shows its review segments and a CI
  segment from its push to its CI result.
- A new pipeline adds "CI started on #42" to the Activity feed; the Cockpit
  folds it and its group still says "1 CI round".

## Steering log

Collected from the milestone's merge requests, as their commits record them
(the merge request numbers are in GitLab).

- **R1** History: a `ci.started` event when a pipeline starts, shown in the
  Activity feed (`bc8d9d8f`), with its row in Status and history.
- **R2** and **R3** The timeline store and API: review and CI segments on
  every issue bar (`eaae1eeb`), with `segments[].kind` in the Desk API page.
  The web drew review and CI in interim tones until R5.
- **R4** The split by kind in a bar's name and the exported segment key
  (`078257ed`). An open issue's name now also gives each kind's time so
  far, e.g. "running 3 min, held 57 min, held since 14:05".
- **R5** The tokens, the stripes, the hatch and the key (`0a5ceb0c`).
  - The `--segment-*` tokens alias the tone fgs and are set once in `:root`;
    the tone fgs change in `.dark` on the same element, so they follow
    (D830-P1). All five reach 3:1 on `--surface` as they are: no tone had to
    be adjusted.
  - The hatch is drawn at 30 %, a little above the planned ~25 %, so it
    stays visible on the light muted fill (D830-P2). It is an inline style:
    the initial bundle sat at 143.0 kB of its 143.1 kB cap, and the tokens
    took it to 143.07 kB.
  - On the Overview the key is the milestone legend, then the five kinds,
    then *Merged* and *Skipped*, in one row (D830-P3).
- **R6** The guide, the UI kit entry and this spec shipped with the web
  merge request (D830-P5); the reference pages shipped with R1 and R3.
- **Not narrowed.** Every requirement shipped as planned; no follow-up
  issue was needed. The agent container could not start Chromium, so the
  web merge request's e2e checks ran in CI only.
