# Desk: pause card and actions

Status: approved by the maintainer (2026-11-03), with the proposed answers
to both open questions (see Decisions). The direction was picked the same
day: a structured pause cause, a "Needs you" card with the fixes that cause
allows, and a count in the top bar; no Web Push.

## Problem

Bloop m21 paused on #391 because the `bugbot` reviewer hit Cursor's usage
limit ("1 reviewers failed on #391, so the change is not fully reviewed").
The maintainer saw it by chance:

- **Not notified.** The Inbox lists tasks that need input, failed or are
  blocked, and pilot proposals. A paused autopilot is none of these (#391's
  run succeeded), so it is not in the Inbox. Only the milestone page shows
  a banner, and the GitLab tracking issue gets a comment.
- **No way out on the Desk.** The banner is free text. What helps depends
  on the cause: a re-review, a retry, fixing the MR in GitLab, `resume`,
  `skip`, or waiting for a reset. Slice 9c's command buttons sit in the
  chats of two different tasks, and nothing connects them to the pause.
- **No override.** When the reviewers cannot run (a usage limit without a
  fallback, an outage), the only way to merge is by hand in GitLab.

## Goals

- Every autopilot pause is in the Inbox and pinned on the milestone page
  and its stream, with a count in the top bar and the tab title.
- The card says what happened, what ends the pause by itself (and when, if
  known), and offers the actions that fit the cause, one click each.
- An owner can merge a head the reviewers could not review, as an explicit,
  recorded decision.

## Non-goals

- Web Push or e-mail. GitLab already mentions the owners on a pause
  (`notify`).
- Pauses of tasks without autopilot. They are already in the Inbox as
  needs-input, failed or blocked.
- Fixing MR state from the Desk (approvals, discussions, conflicts). The
  card links the MR.

## Constraints

- Actions use the slice 9 command path (`chat_messages` kind `command`,
  `internal/chatcmd`, the same authorization as `/agent` comments). No new
  write path in the UI server.
- Free-text reasons stay: the GitLab comments and older clients use them.
- `ownerSubjects` decides who may merge anyway (spec §4a of
  desk-live-agent-chat).

## Proposed approach

### 1. Pause cause (operator)

`AutopilotStatus` gets `pauseKind` beside `pausedReason`. Every pause call
sets it:

| Kind | From | Ends by itself |
|---|---|---|
| `owner` | `pause` command | no |
| `cycle`, `stuck` | plan with a cycle, nothing can start | no |
| `needsInput`, `runFailed` | gate: needs-input label, last run not succeeded | when a later run passes the gate (`resumeWhenReady`) |
| `findings` | blocking review findings | the same |
| `reviewErrors` | reviewers failed | the same; when transient, after one re-review |
| `notReviewed` | head pushed after the last review | the same |
| `ciRed`, `ciCanceled` | CI of the MR | the same |
| `mergeRequest` | MR closed, draft, approvals, discussions, conflicts, head moved, refused | when merged by hand |
| `mainRed` | default branch red (freeze) | when main is green |
| `config` | no workflow label, no reviewers configured | no |

**The pilot sees it too (steer, 2026-11-03).** On bloop #391 the pilot
told the maintainer that a re-review "would probably fail again" and that
the reviewer model cannot be changed, minutes after the Claude fallback for
the Cursor roles went live; the re-review then passed on the fallback. Its
state (`pilotState`) has no engine information. MR 1 adds `engines` to it:
the project's usage limits (`status.usageLimits`, engine and reset) and,
per role, its engine, model and fallback. The pilot rule for pauses says to
check them before it calls a re-review hopeless.

For `reviewErrors`, the runner also keeps why the reviewers failed
(`status.run.reviewErrorKinds`: `usageLimit` with engine and reset,
`transient`, `other`). The card can then say "Cursor is limited until
13 Nov; set a fallback or merge anyway".

### 2. Merge anyway (operator)

A new owner-only command, `merge` (Desk button and `/agent merge` on the
tracking issue). It accepts the active issue's current head
(`status.run.pushedSHA`, sent with the command) without the review part of
the merge gate.

- **What it skips:** failed or missing reviews, and blocking findings.
- **What still applies:** green CI, MR state and approvals.
- **Recorded:** `status.autopilot.mergeOverride {iid, sha, by, at}` and a
  tracking-issue comment ("merged without a full review, by …").
- **Expires:** a different head (a new push) voids it.

Only senders in `ownerSubjects` (Desk) or owners (GitLab) may send it.

### 3. Inbox, card, count (UI server and web)

- `inboxOf` adds a `pause` item per paused autopilot: milestone, kind,
  reason, `pausedFor`, when.
- **Milestone page:** a "Needs you" card replaces the banner. It shows the
  reason, what ends it by itself, and the actions for its kind:

  | Kind | Actions |
  |---|---|
  | `reviewErrors`, `notReviewed` | Review again, Merge anyway, Skip |
  | `findings`, `runFailed`, `needsInput` | Retry, Open #N chat, Skip |
  | `ciRed`, `ciCanceled` | Retry, Open pipeline, Skip |
  | `mergeRequest` | Open MR, Resume, Skip |
  | `cycle`, `stuck` | Open plan, Skip … |
  | `owner`, `mainRed`, `config` | Resume |

  Retry and Review go to the active issue's Implement task. Resume, Skip and
  Merge anyway go to the autopilot's Spec task. Each answer shows in the
  card and in the chat.
- **Stream and project page:** the same card, compact, pinned on top.
- **Top bar:** an Inbox count. The tab title becomes `(n) Desk` while
  anything needs you. The browser Notification API while a Desk tab is
  open, only when the user turns it on (no service worker; decision 2).

## Affected files / components

- `api/v1alpha1/agenttask_types.go`: `pauseKind`, `mergeOverride`,
  `run.reviewErrorKinds` (CRD regenerated).
- `internal/controller/autopilot.go`, `rereview.go`, `freeze.go`: kinds on
  every pause, the override in the gate.
- `internal/runner/implement.go`: reviewer failure kinds.
- `internal/runner/pilot.go`: `engines` in the pilot state.
- `internal/chatcmd`, `internal/command`: `merge`.
- `internal/ui/stream.go`, `milestone.go`: inbox item, pause kind and
  actions in the milestone view.
- `web/`: `PauseCard.vue`, Milestone, stream, project and Inbox views, top
  bar count, title.
- Docs: `design.md`, `usage.md`, `commands.md`, `ui.md`.

## Risks / trade-offs

- **Merge anyway weakens the gate.** It is limited to owners, to one head
  and to the review part, and it is recorded on GitLab.
- **More kinds to keep current.** A pause without a kind shows the free
  text and Resume only, so a new pause path degrades safely.
- **Count noise:** pauses that end by themselves within minutes (a re-review
  pending) also count. The card says so; the count only drops when the pause
  ends.

## Validation / test plan

- **Controller:** every pause path sets its kind (table test over the
  gate); `merge` by a non-owner is refused; `merge` for an old head is
  refused; the override merges with green CI and still waits on red CI.
- **Runner:** a reviewer usage limit is recorded with engine and reset.
- **UI server:** `inboxOf` lists a paused autopilot once, and drops it when
  it runs again.
- **Web:** action mapping per kind (`node --test`); a screenshot of the
  card on `make run-ui`.
- **End to end:** pause a bloop milestone with `pause`, see it in the Inbox
  and the count, resume from the card.

## Delivery plan

| # | MR | Notes |
|---|---|---|
| 1a | Runner: engines in the pilot state | split out first, it misled the maintainer on #391 |
| 1b | Operator: `pauseKind`, reviewer failure kinds | CRD, all pause paths, tests |
| 2 | Operator: `merge` override | command, gate, tracking comment, tests |
| 3a | UI server: inbox pause item, pause fields in the milestone view | after 2 |
| 3b | Web: pause card, inbox, top-bar count, tab title, notifications | autopilot milestone (steer below) |

## Steering log

- 2026-11-03: the pilot's state gets `engines` (MR 1a), after the pilot
  misjudged a re-review on #391.
- 2026-11-03: MR 2 review findings: a run that finishes on the head after
  the override (a re-review) voids it; GitLab `merge` needs the sha too;
  the command only ends review pauses.
- 2026-11-03: MR 3 split into 3a (server, hand-made) and 3b (web), which
  the maintainer handed to an autopilot milestone with the other
  remaining Desk work.

## Decisions (maintainer, 2026-11-03)

1. **Merge anyway skips only the review part** of the gate. CI, MR state
   and approvals still apply.
2. **Browser notifications are in MR 3:** the Notification API while a
   Desk tab is open, off until the user turns them on. No service worker,
   no Web Push.

## PRs / MRs

| # | Title | MR | Status |
|---|---|---|---|
| — | docs(spec): Desk pause card and actions | [!88](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/88) | approved |
| 1a | feat(runner): engines in the pilot state | [!91](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/91) | merged |
| 1b | feat: pause kind and reviewer failure kinds | [!92](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/92) | merged |
| 2 | feat(autopilot): owners may merge without a full review | [!94](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/94) | merged |
