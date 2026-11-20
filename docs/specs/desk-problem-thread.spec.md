# Desk: one thread per problem, and every conversation reads like a chat

Status: built by hand in one merge request (owner's request, see
[Delivery](#delivery)). Narrowed requirements and in-flight changes go into
the [Steering log](#steering-log).

## Goal

1. One problem is one thing on the Desk. A milestone's *Needs you* tab lists
   its problems (one row per stuck issue, or the milestone itself). Opening a
   row shows one thread: what happened, why it is stuck, what the operator and
   the pilot tried, what owners answered, and the outcome, oldest at the top.
2. A thread has exactly one place to act: a bar at the bottom with the open
   question's options (consequence and recommended pick), or the ways out of
   a pause or hold when no question is open, plus a text box to talk to the
   pilot about this problem.
3. A click on an option shows at once as the owner's message in the thread
   (*sending* → *sent*), the buttons leave, and the operator's reaction
   (retry started, hold cleared, pilot acted, problem resolved) follows as new
   messages without a reload. With no reaction within 60 s the thread says so.
4. The Pilot tab, task chat, plan chat, the Stream page and an issue's stream
   read like a chat: oldest at the top, the text box at the bottom, *Load
   older* at the top, and the view follows new messages while the reader is
   at the bottom (a jump button otherwise).
5. A problem keeps its thread after it is resolved. If the same issue gets
   stuck again within 24 h, the thread reopens instead of starting over.

## Background

File refs are at origin/main ae376fdc, 2026-11-20.

**Problems are a client-side grouping.** The UI server lists them in
`milestoneProblems` (`internal/ui/milestone.go:142`) from the CRD:
`problem.IIDs` (`internal/problem/facts.go:137`) picks stuck issues,
`MilestoneView.Problems` (`milestone.go:53`) carries them. The web groups
prompts, pauses, holds and pilot actions per problem in `needsCases.ts` and
renders `MilestoneNeeds.vue` → `NeedsProblem.vue` → `NeedsEntries.vue` →
`PromptCard.vue` / `PauseCard.vue` / `ProblemCard.vue`. A problem with two
prompts and a pause shows three cards, each with its own text and buttons.

**Answers have no visible effect.** `PromptCard.vue` posts
`{"cmd":"answer","prompt":"Q3","option":"A"}` to
`/api/v1/…/tasks/{task}/chat` (`internal/ui/chat.go:43`). The controller runs
it through `applyPilotChatCommands` → `answerPrompt`
(`internal/controller/prompt_answer.go:32`) → `runAccepted`
(`pilot_actions.go:733`). The card shows "✓ Retry · sent" and stays until the
prompt closes; the retry, the cleared hold and the outcome land in other tabs.

**History is incomplete.** `internal/history` (Differ `differ.go:89`,
Recorder `recorder.go:86`, leader-only, non-blocking) writes
`history_events` (migration `0002_history.sql`). Holds
(`autopilot.go:1990`, `:2015`), the pilot's own actions (`pilotActed`,
`pilot_actions.go:822`) and problems opening or closing are not recorded.
`/api/v1/stream` merges events and chat in `mergeStream`
(`internal/ui/stream.go:280`).

**Problem chat exists.** A chat message whose first line is `problem: #N`
goes to `chatProblem` (`internal/runner/chat.go:571`), which gets the facts
and may propose `pilot-actions` that the controller runs as the owner
(`chatAccepted`, `pilot_actions.go:347`).

**Conversation views disagree on direction.** `Stream.vue` and
`IssueStream.vue` put the newest at the top with the composer above;
`MilestonePilot.vue` puts the composer above its events; `TaskChat.vue`,
`ChatThread.vue` and `PlanChat.vue` put the oldest at the top with their own
scroll boxes. None follows new messages except `LiveTranscript.vue`.

**Nuxt UI.** The Desk uses `@nuxt/ui` 4.12.0. `UChatMessages` renders a list
of `{id, role, parts:[{type:'text', text}]}` with sides per role, keeps the
scroll at the bottom and shows a jump button; `UChatPrompt` is the text box
with submit on Enter. Neither needs the `ai` package at runtime.

No milestone runs before this one: the owner asked for it out of order.
`desk-one-look` (%36) may restyle the same views later and builds on this.

## Decisions (locked)

1. **A `problems` table in the research store.** Columns: `id` (bigserial),
   `namespace`, `project`, `milestone_iid`, `issue_iid` (0 = the milestone),
   `state` (`open`|`resolved`), `cause`, `opened_at`, `resolved_at`,
   `resolved_by`. The operator writes it through the history Recorder
   (leader-only, non-blocking, same batches). Rejected: a CRD status list
   (size cap, lost on delete) and keeping the grouping client-side (no
   stable id, no history after resolve).
2. **Open and resolve follow `problem.IIDs`.** The Differ compares the set
   before and after each MilestoneTask update. An issue entering the set opens
   a problem; leaving it resolves it with `resolved_by` = who closed the last
   prompt, `pilot` or `self`. An issue that re-enters within 24 h of
   resolving reopens the same row (Goal 5).
3. **The thread is a query, not a copy.** `GET …/problems/{id}` returns the
   problem, its live status and a timeline: `history_events` and
   `chat_messages` for that issue (or milestone) from `opened_at − 1 min` to
   `resolved_at` (or now), merged by the existing `mergeStream`. Rejected:
   storing thread messages separately (two truths).
4. **New history kinds**: `problem.opened`, `problem.resolved`,
   `hold.set`, `hold.cleared`, `pilot.acted` (see the steering log for
   reopen). Each has a
   one-line summary a person can read without the data.
5. **The pilot acts on its own and logs it.** Pilot actions show as pilot
   messages in the thread. Only a question the pilot cannot answer asks the
   owner (owner's choice in brainstorming).
6. **Live status is computed, not stored.** The UI server derives one of
   *waiting for you*, *operator acting*, *pilot looking*, *resolved* from the
   CRD (open prompts, pending accepted actions, pilot in flight).
7. **One bar to act.** The bottom bar shows the newest open prompt's options
   (consequence under each, recommended first and marked). With no open
   prompt it shows the pause or hold ways out. Older prompts in the thread are
   one message each: question, chosen answer, who chose. Never two sets of
   buttons.
8. **Optimistic answer.** The click adds the owner's message at once with
   *sending*, then *sent* on the 202, then the operator's events append via
   the SSE refetch. 60 s without a new event for the problem adds a local note
   "No reaction from the operator yet" with a link to the task logs.
9. **`UChatMessages` + `UChatPrompt` everywhere.** One wrapper,
   `web/src/ui/DeskChat.vue`, maps Desk messages to Nuxt UI's message shape
   (roles: `user` = an owner, `assistant` = pilot/agent, `system` = operator
   events, shown compact) and adds *Load older* that keeps the scroll anchor.
   Used by the problem thread, Pilot tab, task chat, plan chat, Stream and
   IssueStream. A local type replaces `UIMessage`; no `ai` dependency.
10. **Problem chat keeps the `problem: #N` prefix.** The thread's text box
    adds it; the thread hides it when showing the message.
11. **Route and anchors.** `/projects/:ns/:name/milestones/:iid/problems/:id`.
    *Needs you* becomes a list plus the thread (side by side from 1024 px,
    list then thread below). Old `#problem-<iid>` and `#prompt-Qn` anchors
    open the matching problem.
12. **The Inbox stays a list** of open problems across projects (one row per
    problem, not per prompt), each row linking to its thread.
13. **Without the research database** the thread shows the CRD facts, the
    open prompt and its options, and says history is off.
14. **Removed**: `ProblemCard.vue`, `NeedsProblem.vue`, `NeedsEntries.vue`,
    `needsCases.ts`, `ChatThread.vue`. `PromptCard.vue` and `PauseCard.vue`
    shrink to the bar's content.

## Requirements

### Operator (Go)

- **R1** Migration `<utc>_problems.sql`: the table of Decision 1, index on
  `(namespace, project, milestone_iid, issue_iid, opened_at desc)`, grants as
  in `migrate.go` (reader SELECT, writer through the Recorder's role).
  Store functions `OpenProblem`, `ResolveProblem`, `ListProblems`,
  `GetProblem`. Tests: store tests for open, resolve, reopen within 24 h, new
  row after 24 h.
- **R2** Differ: problem open/resolve/reopen from `problem.IIDs` (Decision 2)
  and kinds `hold.set`, `hold.cleared`, `pilot.acted` from MilestoneTask
  status changes (Decision 4). Tests: Differ table tests for each kind and for
  `resolved_by`.

### UI server (Go)

- **R3** `GET /api/v1/projects/{ns}/{name}/milestones/{iid}/problems`
  (list with live status) and `…/problems/{id}` (problem, status, open
  prompt with options, ways out, timeline). Inbox lists problems.
  `make ts-types`. Tests: handler tests for list, thread window, status
  derivation, no-database fallback.

### Web

- **R4** `web/src/ui/DeskChat.vue` (Decision 9) and pure helpers in
  `web/src/chatView.ts` (message mapping, prefix hiding, Load-older anchor).
  Listed in the UI kit. Tests: node tests for mapping and prefix hiding.
- **R5** Problem thread view and the new *Needs you* tab (Decisions 7, 8, 11),
  optimistic answer and the 60 s note, Inbox rows per problem. Remove the
  files of Decision 14. Tests: `needs-you.spec.ts` and `prompt-card.spec.ts`
  rewritten for the thread (answer shows at once, buttons leave, operator
  event appends, anchors redirect); node test for the 60 s rule.
- **R6** Pilot tab, task chat, plan chat, Stream and IssueStream on
  `DeskChat` (Goal 4). Tests: `desk.spec.ts` checks composer at the bottom and
  newest last at 375 and 1280 px.

### Docs

- **R7** `docs/guides/answer-and-hand-over.md`, `docs/guides/desk.md`,
  `docs/guides/pilot.md`, `docs/guides/pauses.md` (thread instead of cards),
  `docs/reference/desk-api.md` (problem endpoints),
  `docs/contributing/design.md` (History: new kinds, problems table),
  `docs/contributing/web-ui.md` (DeskChat in the UI kit, Composer and Needs
  you sections), this spec, and its line in `docs/specs/README.md`.

## Constraints

- One merge request, by the owner's explicit request; it exceeds the usual
  per-MR budget. Commits stay split by requirement so the diff reads in
  order.
- Prompt answers keep the same chat command and controller path; no CRD
  change. The `problem: #N` contract with the runner stays.
- Recorder stays non-blocking; a full channel drops, never stalls the
  controller.
- No new dependency.
- Checks: `gofmt`, `go vet ./...`, `make helm-lint`, package tests of
  `internal/history`, `internal/research/store`, `internal/ui`,
  `go test ./internal/docscheck/`, `make ui`, `make ts-types`, Playwright e2e
  for the changed specs. `make test` runs in CI.

## Non-goals

- Backfilling problems from before the migration: old pauses and prompts
  still show in the stream, not as threads.
- Token streaming of pilot replies into the thread.
- Merging Decisions and plan wizard into threads.
- Notifications (push, mail) when a problem opens.
- The tempting next step: let the pilot propose the whole fix plan in the
  thread with one *Approve* button. Separate milestone.

## Suggested order

R1 → R2 → R3 → R4 → R5, R6 → R7

## Acceptance

1. On a milestone with a stuck issue that has a pause and two prompts,
   *Needs you* shows one row; its thread shows the pause, both prompts (the
   older as a closed message) and one bar with the newest prompt's options.
2. Clicking *Retry* shows "Retry" as the owner's message with *sending*, then
   *sent*; the buttons are gone; within a minute "retry started" and later
   "problem resolved" appear without a reload.
3. With the operator stopped, the same click shows the 60 s note.
4. On the Pilot tab and in task chat the text box is at the bottom; a new
   pilot message scrolls into view while at the bottom and shows the jump
   button when scrolled up.
5. An issue resolved 2 h ago that gets stuck again reopens its thread with
   the old messages above a "reopened" line.
6. With the research database off, a problem shows its facts and options and
   says history is off.

## Delivery

Single merge request on `feat/problem-thread`, requested by the owner ("we
need this quickly"). This deviates from the < 50 production-line budget per
MR; review is eased by one commit per requirement in the order above.

## Steering log

- 2026-11-20: spec written; owner chose Postgres table, pilot acts and logs,
  single MR, `UChatMessages` + `UChatPrompt`.
- 2026-11-20 (R2): no separate `problem.reopened` kind. The store reopens a
  problem resolved less than 24 h ago when a new `problem.opened` arrives
  (it sets `reopened_at`), and the thread labels that event "stuck again".
  A row left open by a missed update shows as resolved from the live CRD
  status. A pause over the whole milestone is problem `#0`.
- 2026-11-20 (R6): `ChatThread` (nested replies) is replaced by `ChatTurn`,
  one message inside `DeskChat`'s bubble; replies follow their parent in
  time order instead of nesting. Task chat and plan chat keep all their
  command controls in `DeskChat`'s `prompt` slot. `AskPilot` and `TaskChat`
  lose the `focus` prop: a problem's talk with the pilot is its thread.
- 2026-11-20 (R6): the Stream tab and the issue stream are `DeskChat` too,
  read as full-width event cards (`card` messages), oldest first, with the
  day label on a day's first card; the pause and hold cards sit in the bar
  above the composer instead of a sticky header.
- 2026-11-20 (R6): the initial bundle grew from 136.7 to 138.4 kB (cap
  136.8): the theme CSS of `UChatMessages`/`UChatPrompt` lands in the global
  stylesheet and their icons in the up-front icon bundle. The owner raised
  the cap to 138.5 kB. After merging main (raw controls on Nuxt UI, cap
  137.9) it measures 139.5 kB; the cap is 139.6 kB. Later trims to look at: the icon bundle and
  `@iconify/vue` (about 38 kB raw in the initial chunk), and `pauseCard.ts`
  in the initial chunk.
- 2026-11-20 (R6): the phone's sticky text box moved from `Composer` to
  `DeskChat`, whose whole chat is the sticky range (a sticky element in a
  tight wrapper never sticks). Only the text box sticks, and only while the
  bar is empty: a sticky bar moved under the finger when the keyboard
  closed, and a tall question or pause card would hide its own buttons.
