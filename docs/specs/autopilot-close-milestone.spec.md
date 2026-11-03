# Autopilot closes its milestone

Status: approved by the maintainer (2026-11-03): close after the
retrospective's follow-ups, or a week after it was posted.

## Problem

When autopilot is done it closes its tracking issue and comments, but the
milestone stays open. The maintainer closes it by hand, or it lingers on the
Desk and in GitLab as active.

## Goals

- A done autopilot closes its milestone by itself.
- Nothing the done milestone still offers is cut short: the pilot's
  retrospective, and the owners' `create 1 2` for its follow-ups.
- A milestone with open issues stays open, and the tracking issue says which.

## Non-goals

- Closing milestones without autopilot, or after `phase: Off`.
- Keeping closed milestones on the Desk. Their history stays in Postgres and
  GitLab.

## Constraints

- The operator lists active milestones only. A closed milestone is no longer
  planned: its spec task (with the autopilot status and the retrospective)
  is deleted on the next poll, and so is its pilot. Closing therefore ends
  everything the milestone's tasks do.
- One GitLab write per close; no extra reads per poll (autopilot already
  lists the milestone's issues).

## Proposed approach

On every poll of a `Done` autopilot (`internal/controller/autopilot.go`,
`closeMilestone`) with no re-plan pending (an edited milestone description
plans again and may continue the queue), close the milestone when all of
these hold:

1. **Retrospective settled:** it is on the spec task
   (`status.pilot.retrospective`, also when it failed), or the milestone has
   no pilot to write one.
2. **Follow-ups settled:** the retrospective has none, failed, every one was
   created, or it was posted at least 7 days ago (`followUpWindow`).
3. **No open issues** in the milestone besides the tracking issue. Otherwise
   it comments once on the tracking issue which issues keep the milestone
   open (`status.autopilot.closeWaitsFor`, so it comments again only when
   that list changes), and closes once they are closed.

Closing is `PUT /projects/:id/milestones/:milestone_id` with
`state_event=close` (`gitlab.API.CloseMilestone`), then one comment on the
tracking issue. Close first, comment after: the next poll does not see the
milestone again, so the comment cannot repeat. A milestone that is gone
(404) counts as closed.

## Affected files / components

- `internal/gitlab/client.go`, `gitlabfake`: `CloseMilestone`.
- `internal/controller/autopilot.go`: `closeMilestone`; the autopilot gets
  the milestone's pilot task (`syncAutopilot`).
- `api/v1alpha1/agenttask_types.go`: `AutopilotStatus.CloseWaitsFor`
  (CRD regenerated).
- Docs: `design.md` (Autopilot, Pilot lifecycle), `usage.md`.

## Risks / trade-offs

- **Follow-ups after a week:** `create` no longer works once the milestone
  is closed; the follow-ups are still in the retrospective comment and can
  be opened by hand. Reopening the milestone brings the autopilot back
  (`Running`, then `Done` again), which is today's behaviour for a
  re-planned milestone.
- **Desk:** a closed milestone leaves the Desk's milestone list.

## Validation / test plan

- Done, retrospective with no follow-ups, no open issues → milestone closed,
  one comment.
- Done, pilot still writing the retrospective → not closed.
- Done, follow-ups open and posted less than 7 days ago → not closed; all
  created → closed; 7 days later → closed.
- Done, a skipped issue still open → not closed, one comment naming it, no
  repeat on the next poll; closed once the issue is closed.
- Done, no pilot at all → closed.
- Done, retrospective failed → closed.
- Done, milestone description edited (re-plan pending) → not closed
  (review finding).

## Delivery plan

One MR: GitLab client, autopilot, status field, docs and tests (about 60
production lines with the generated CRD excluded; slightly over the budget,
kept together because the pieces do nothing apart).

## PRs / MRs

| # | Title | MR | Status |
|---|---|---|---|
| 1 | feat(controller): autopilot closes its milestone | see MR | review done |
