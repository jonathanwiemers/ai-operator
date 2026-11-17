# Desk: one Needs you tab per milestone that shows what was asked, who handled it and what was tried

Status: the milestone's spec, run by autopilot. Narrowed requirements and
in-flight changes go into the [Steering log](#steering-log).

## Goal

1. On a milestone page, each question to the owners appears with its message
   and options in one place only: the new *Needs you* tab. The Pilot tab and
   every stream show a question as one line with its state and a link.
2. When an owner picks an option or sends an answer, the buttons and the
   text box disappear at once. A closed question shows only what was chosen,
   what that did and the outcome.
3. Every closed question says who closed it and why: answered by an owner,
   handled by the pilot, resolved on its own (with the cause), replaced by a
   newer question, or expired.
4. The *Needs you* tab lists, per issue (and for the milestone), the problems
   the autopilot met and every attempt to fix them: the pilot's own actions,
   the answered questions with their outcomes and the pauses. It also shows
   how many pilot attempts are left on the issue's current head.
5. The Inbox has an *Open | Handled* switch. *Handled* shows the last 7 days
   of closed questions and pilot actions across the owner's projects, with
   the same labels.

## Background

File refs are at origin/main c3485dc, 2026-11-15.

**Where a question shows today.**

- A milestone's Pilot tab (`web/src/components/MilestonePilot.vue`) draws the
  open questions as full `PromptCard`s under *Questions* (lines 64–93).
- It then shows the *Pilot events* stream (lines 118–121). There, each
  `prompt.posted` event draws the same prompt again as a compact `PromptCard`
  with the recommended option as a button
  (`web/src/components/stream/EventCard.vue:282-290`).
- While paused, the full `PauseCard` shows its own prompt too. Only the
  *Questions* list leaves that one out (`MilestonePilot.vue:27-33`). The
  stream does not.
- The pause history (`PausesCard`) sits at the bottom of the Pilot tab
  (line 123).
- A prompt link `#prompt-Q<n>` without `?tab=` opens the Pilot tab
  (`web/src/streamTabs.ts:74-81`). The milestone tabs are Issues, Pilot,
  Retrospective, Stream, Board and Spend (`streamTabs.ts:32-37`), rendered
  in `web/src/views/Stream.vue:639-650`.

**What a card shows after an answer.**

- `PromptCard.vue` keeps every option of a closed prompt, with each option's
  actions ("→ …") and the chosen one ticked (lines 141–170).
- After a click, the buttons stay until the status update arrives, with
  "sent…" below them (lines 116–121 and 202–208).
- The status line comes from `promptStatus` (`web/src/promptCard.ts:61-81`).
  Every superseded prompt gets one text: "Superseded: its cause resolved or
  a newer question replaced it".

**Why a prompt closed is lost.**

- `api/v1alpha1/prompt.go:34-46` has three closed states: `answered`,
  `superseded` and `expired`.
- `promptResolved` (`internal/controller/prompt_lifetime.go:99-136`) closes a
  prompt as superseded for six different causes: the proposal's milestone
  state changed (line 109), main is green again (line 113), the agent ran
  again without the answer (line 119), the budget was resolved (line 123),
  the pause is over (line 131), the issue merged or closed (line 136).
- `postPrompt` supersedes an open prompt with the same kind and key
  (`internal/controller/prompts.go:46-50`). `boundPrompts` deletes the oldest
  open prompts when more than 20 pile up (lines 82–94). Those leave the
  status, so they need no reason.
- The GitLab mirror guesses the cause again
  (`internal/controller/prompt_mirror.go:204-217`, `stateLine` at lines
  240–259).
- Pauses already record who ended them: `PauseResolver` (`operator`,
  `pilot`, `owner`, `selfHealed`, `laterRun`) at
  `api/v1alpha1/agenttask_types.go:1843-1861`. The pause history carries
  `Item`, `ResolvedBy` and `Via` per pause
  (`internal/research/store/pauses.go:25-36`), but only with the history
  database.

**What the pilot did on its own.**

- With `pilot.autonomy: managed`, the pilot runs the recommended option
  without a prompt. The only record is the audit comment "🧭 The pilot acted
  on its own" on the tracking issue
  (`internal/controller/pilot_autonomy.go:130-160`).
- Its sources are `runAccepted` (`internal/controller/pilot_actions.go:680`)
  and, at lines 737–750: proposals that came from a prompt write their
  outcome into that prompt's answer; all others get a comment, the audit
  comment when `AcceptedBy` is empty, otherwise the owner's outcome comment.
- The only count kept is `status.pilot.interventions`: issue IID to
  `{head, count}`, where "0" is the milestone
  (`agenttask_types.go:730-733`, `874-879`). The Desk shows neither the
  actions nor the count.

**UI server.**

- `PromptView` (`internal/ui/prompt.go:19-39`) and `PromptAnswerView`
  (lines 61–69) are built by `promptViews` (line 75), which returns the open
  prompts and the newest `maxClosedPrompts` (10, line 14) closed ones.
- `MilestoneView` (`internal/ui/milestone.go:28-65`) carries `prompts`,
  `pilot` (`MilestonePilot`, lines 316–327) and `pauses`.
- The Inbox (`internal/ui/stream.go:514-533`, `inboxOf` at line 580;
  `web/src/views/Inbox.vue`) lists only what is open.

**Milestones that run before this one.**

- %23 *Runs that hang get noticed and restarted* adds the `stalled` pause
  kind and the pilot tool `restartRun` (#488–#491).
- %24 *Autopilot works on several issues of a milestone at once* turns issue
  problems into per-issue holds, with one pause card per hold on the
  milestone page, the Inbox and the boards (#461–#473).

This milestone builds on both. The *Open* section shows the pause cards as
%24 leaves them. A `restartRun` from %23 is one more pilot action in the
log.

## Decisions (locked)

1. **Scope.** This milestone covers prompts of the kinds `assessment`,
   `mainHealth`, `needsInput`, `budget`, `revisit` and `proposal`; autopilot
   pauses and holds (pause cards); and the older proposals (🧭 P-n,
   `MilestonePilot.proposals`). The older proposals show only under *Open*:
   nothing is kept once they close. Rejected: also covering the GitLab
   issues the operator opens itself (flaky CI, main canceled). That is
   another milestone.
2. **A new milestone tab, *Needs you*,** between Issues and Pilot. Its label
   is "Needs you" and, while something is open, "Needs you · n". It has two
   sections: *Open* (pause cards, open prompts, open older proposals) and
   *Handled* (problems and attempts, Decision 7). The Pilot tab keeps the
   pilot line (autonomy, cost, Pilot task), the composer and *Pilot events*.
   Its *Questions* section and the pause history move to *Needs you*. A link
   `#prompt-Q<n>` without `?tab=` opens *Needs you*. Rejected: keeping
   everything on the Pilot tab with one-line events (it mixes chat with
   decisions); making the Inbox the only place (milestone context gets
   lost).
3. **One place with buttons.** Answer buttons show only on the *Needs you*
   tab, the Inbox, the task page and pause cards (full and compact). A
   stream event row never shows them, on any page: `prompt.posted` becomes
   one line, "Q12 asked: <question>", with the prompt's state badge and a
   link to the card on the milestone's *Needs you* tab (on the project page
   when it has no milestone). A pause card's own prompt is not listed again
   under *Open*.
4. **The operator records why a prompt closed.** The Desk does not guess.
   New optional fields on `Prompt`:
   - `closedReason`, an enum:

     | Value | Cause |
     |---|---|
     | `answered` | an owner's answer |
     | `expired` | lifetime ran out |
     | `stateChanged` | the proposal's milestone state changed |
     | `mainHealthy` | main is green again |
     | `agentRanAgain` | the agent ran again without the answer |
     | `budgetResolved` | the budget was resolved |
     | `pauseOver` | the pause ended |
     | `issueDone` | the issue merged or closed |
     | `replaced` | a newer prompt with the same kind and key |

   - `closedBy`, of type `PauseResolver`: `owner` for answers; for
     `pauseOver` the pause's `status.autopilot.resumedBy`; `operator` for
     every other cause.
   - `replacedBy`, the newer prompt's ID, for `replaced`.

   Prompts closed before this milestone have none of them and keep today's
   texts. Rejected: one enum mixing who and why; deriving the cause in the
   UI server.
5. **Labels.**
   - `owner`: "Answered by @login" plus the choice (today's text).
   - `pilot`: "Handled by the pilot", plus the cause.
   - `operator`, `selfHealed`, `laterRun`: "Resolved on its own", plus the
     cause in words ("main is green again", "the issue was merged or
     closed", "the agent ran again without the answer", "the budget is no
     longer short", "the pause ended", "the milestone moved on").
   - `replaced`: "Replaced by Q14", linking to it.
   - `expired`: "Expired: nobody answered in time".

   The same words go into the GitLab comment's state line. Badge tones:
   owner success, pilot progress, everything else neutral.
6. **A log of the pilot's actions in status.** New `status.pilot.acted` on
   the Coordinate task keeps the newest 30 (`MaxPilotActed`) entries. Each
   entry is one proposal that `runAccepted` ran without a prompt: `at`;
   `by` (empty for the pilot on its own, otherwise the owner who accepted);
   `summary` (≤ 300 bytes, untrusted); `why` (≤ 1000 bytes, untrusted);
   `lines`, the per-action result lines the comment shows (at most 8, each
   ≤ 300 bytes); `issues`, the intervention keys it counted against (0 for
   the milestone); `ran`, false when none of its actions ran. Proposals from
   a prompt are not logged twice: their outcome is in the prompt's answer.
   The audit comment stays as it is. Rejected: parsing the audit comments
   from GitLab; a history-database event (the tab must work without the
   database).
7. ***Handled* groups problems per issue,** in the web, from the milestone
   view. A problem is one issue IID, or the milestone for 0. Its entries
   are, newest first: prompts of the issue (`issueIID`, 0 when none);
   `pilot.acted` entries listing the issue; pauses whose `item` is the issue
   (`item` 0 or absent goes to the milestone). A problem with an open prompt
   or an active pause or hold comes first and is marked *Open*. The others
   are marked *Handled* and are sorted by their newest entry. Each problem
   shows "Pilot attempts: used of max on the current head" from
   `status.pilot.interventions`. The count counts as 0 when its head differs
   from the task's `status.run.pushedSHA`, the same rule as the operator's.
   Closed prompts show as compact rows with their label. A click unfolds the
   full closed card.
8. **The card after an answer.** Right after a click, or after *Send* on the
   text box, the full card and the compact card hide every option button
   and the text box, and show "✓ <label> · sending…" ("✓ your answer ·
   sending…" for text). When the answer fails, the buttons and the text come
   back with the error. A closed card shows only the chosen option (or the
   free text) with its "→" action lines, the outcome and the label
   (Decision 5). The other options fold under "Other options (n)". A
   superseded or expired prompt folds all its options under "Options (n)".
9. **Inbox *Handled*** is built from live state only, with no database. It
   shows the closed prompts and `pilot.acted` entries of the last 7 days
   from the projects the user may see, newest first, at most 50. Each row
   has its label and links to the problem on the milestone's *Needs you*
   tab. The *Open* list stays as it is.
10. **No change to when prompts open or close, or to what the pilot does.**
    This milestone only records and shows.

## Requirements

### API (Go)

- **R1** Add `closedReason` (enum of Decision 4), `closedBy`
  (`PauseResolver`) and `replacedBy` (string, `Q<n>`) to `Prompt`, all
  optional, with kubebuilder enum markers. Run `make generate manifests`.
  Tests: each enum value validates and an unknown one fails. The CI drift
  job is green.
- **R2** Add `PilotActed` and `PilotStatus.Acted`
  (`+kubebuilder:validation:MaxItems=30`, `MaxPilotActed = 30`) with the
  fields of Decision 6. Run `make generate manifests`. Tests: deepcopy is
  generated and the CRD has the field.

### Operator (Go)

- **R3** Record the close reason. `promptResolved` returns the reason with
  the state, and `syncPrompts` writes `closedReason` and `closedBy`. For
  `pauseOver`, `closedBy` is `status.autopilot.resumedBy` (empty:
  `operator`). `postPrompt` sets `replaced`, `operator` and `replacedBy`.
  Where the operator sets `PromptAnswered`, it sets `answered` and `owner`.
  Tests: one controller test per cause checks the three fields. A prompt
  closed without the fields keeps them empty.
- **R4** Log the pilot's actions. In `runAccepted`, for each proposal
  without `Prompt`, append a `PilotActed` entry to the Coordinate task's
  `status.pilot.acted` through `patchProposals`, keeping the newest 30.
  Untrusted text is cut to the bounds of Decision 6. Tests: a managed action
  logs `by` empty, `ran` true, its `issues` and the lines. An accepted
  proposal logs `by`. A prompt-based proposal logs nothing. The 31st entry
  drops the oldest.
- **R5** Make the GitLab comment's state line (`stateLine`) use
  `closedReason`, `closedBy` and `replacedBy` with the words of Decision 5
  when they are set, and today's text when they are not. Tests: a
  `mainHealthy` prompt says "Resolved on its own: main is green again". A
  `replaced` prompt names its successor. An old prompt is unchanged.

### UI server (Go)

- **R6** Add `closedReason`, `closedBy` and `replacedBy` to `PromptView`,
  filled in `promptViews`. Run `make ts-types`. Tests: a closed prompt with
  and without the fields.
- **R7** Add `acted` (the `PilotActed` entries, newest first) and
  `interventions` (one per key: `issueIID`, `used`, `max`, with `used` 0
  when the head differs from the issue's task `status.run.pushedSHA`) to
  `MilestonePilot`. Run `make ts-types`. Tests: a milestone view with two
  entries and a stale head shows `used` 0.
- **R8** Add `handled` to `InboxResponse`, with the rows of Decision 9:
  `namespace`, `project`, `milestoneIID`, `issueIID`; `kind` (`prompt` or
  `acted`), `promptID`, `title` (question or summary); `closedReason`,
  `closedBy`, `by`, `at`. It is built from the same live tasks as `inboxOf`,
  for the projects the user may see. Run `make ts-types`. Tests: an entry
  older than 7 days is left out, at most 50 rows, newest first, and a
  project the user may not see is left out.

### Web

- **R9** Make `prompt.posted` stream rows one line: "Q12 asked:
  <question>" plus the state badge and a link to the card on the *Needs
  you* tab (Decision 3). No `PromptCard` in stream rows. Tests: a
  milestone's Pilot tab with one open prompt has exactly one element with
  `data-prompt` for it, and no button in *Pilot events*.
- **R10** Make `promptStatus` return the labels and tones of Decision 5 from
  `closedReason`, `closedBy` and `replacedBy`, falling back to today's
  texts. Tests: a case per reason, plus one without fields.
- **R11** Change the prompt card after an answer (Decision 8): full and
  compact cards hide the options and the text box from the click until the
  answer succeeds or fails; failure brings them back with the error; closed
  cards show only the chosen option and its actions, the outcome and the
  label, and the rest folds under "Other options (n)" or "Options (n)".
  Tests: e2e for the click, the failure and a closed answered card.
- **R12** Add the *Needs you* milestone tab (Decision 2): new `needs` in
  `MilestoneTab` and `milestoneTabs`, between Issues and Pilot, with the
  open count in the label; `milestoneTabFromRoute` opens it for `#prompt-`
  links; a new component holds *Open*; `MilestonePilot.vue` loses
  *Questions* and `PausesCard`. A stored milestone's tab shows only
  *Handled*. Tests: the tab from the route; e2e: a link `#prompt-Q3` opens
  *Needs you* at the card.
- **R13** Add a pure helper `web/src/needsCases.ts` that groups prompts,
  `pilot.acted`, `pauses` and `interventions` into problems as Decision 7
  says. Tests: an issue with a closed prompt, two pilot actions and a pause;
  a milestone-level entry; an open problem sorting first; a stale head
  counting 0.
- **R14** Render *Handled* on the *Needs you* tab from `needsCases`. Each
  problem shows a heading with "#N title" or "Milestone", *Open* or
  *Handled*, and "Pilot attempts: u of m". Its entries show as rows: a
  prompt with its label, unfolding to the full closed card; a pilot action
  with "Pilot" or "@owner", the summary, the why and the lines; a pause
  with its kind, its time span and who ended it how. Tests: e2e on the
  milestone fixture.
- **R15** Add the Inbox *Open | Handled* switch, kept in `?view=handled`.
  *Handled* lists `handled` rows with the labels of R10 and links to the
  milestone's *Needs you* tab. Tests: e2e switches, shows the rows and
  follows a link.

### Docs

- **R16** Update the Desk and usage docs (the milestone tabs, the prompt
  card, the Inbox, a new "Needs you tab" section, how a prompt closes and
  the recorded reason, where the pilot's own actions show) and the design
  docs if they describe the prompt states. Add this spec with a steering
  log. Tests: `make ui` and the docs check pass.

## Constraints

- Under 300 production lines per merge request; tests, generated files and
  docs do not count. Split a requirement only when it is bigger, and say so
  in the issue.
- When a requirement has to be narrowed, open a follow-up issue for the rest
  and say so in the [Steering log](#steering-log).
- Generated files are checked: run `make generate manifests` after changing
  API types or kubebuilder markers, and `make ts-types` after changing view
  structs. Do not mirror Go views in `web/src/types.ts`.
- Do not change when prompts open, close, expire or are superseded. Do not
  change what the pilot may do, its autonomy, its intervention limits or the
  audit comment text.
- The GitLab prompt comment changes only its state line (R5). Its marker,
  options and Desk link stay.
- Untrusted text (questions, context, options, summaries, why, lines) is
  shown as text or as markdown without HTML, as today.
- No kubebuilder RBAC markers in `internal/ui`. No new dependencies.

## Non-goals

- The GitLab issues the operator opens itself (🔁 flaky CI, ⏹️ main
  canceled) in *Needs you* or the Inbox.
- Changing the project page's context panel or the boards, beyond the
  one-line stream rows.
- A history of older proposals (🧭 P-n) after they close, or of prompts
  beyond the 20 the status keeps.
- A history-database table or stream event kind for the pilot's actions.
- Undoing or changing an answer after it was sent.
- Notifications or mentions for handled items.

## Suggested order

R1, R2 → R3, R4, R6, R7 → R5, R8, R10, R13 → R9, R11, R12 → R14, R15 → R16

## Acceptance

- On a running milestone with one open assessment prompt, the *Needs you*
  tab shows its message and options once. The Pilot tab's *Pilot events*
  shows "Q… asked: …" as one line without buttons, and the line leads to the
  card. (Goal 1)
- On a paused milestone, the pause card with its prompt is on *Needs you*,
  and the prompt is not listed a second time there or anywhere on the page.
  (Goal 1)
- A GitLab link to `#prompt-Q3` of a milestone opens *Needs you* at Q3.
  (Goal 1)
- An owner clicks an option: the buttons go at once and "✓ <label> ·
  sending…" shows. Once answered, the card shows only that option, what it
  did and the outcome, and *Other options (n)* unfolds the rest. When the
  answer is refused, the buttons come back with the error. (Goal 2)
- A mainHealth prompt that closes because main turned green shows "Resolved
  on its own: main is green again", and its GitLab comment says the same. A
  prompt replaced by Q14 says "Replaced by Q14" with a link. A prompt whose
  pause the pilot ended says "Handled by the pilot". (Goal 3)
- On a managed milestone where the pilot retried #12 twice on its own,
  *Handled* shows the problem "#12 …" with both actions (summary, why,
  result lines), the closed prompts of #12 and its pauses, and "Pilot
  attempts: 2 of 2". After a new push to #12 it shows 0 of 2. (Goal 4)
- A problem with an open prompt sorts above the handled ones and is marked
  *Open*. (Goal 4)
- The Inbox's *Handled* lists the prompts closed and pilot actions run in
  the last 7 days across the owner's projects with their labels, and each
  row opens the problem on the milestone's *Needs you* tab. *Open* is
  unchanged. (Goal 5)

## Steering log

- 2026-11-17 #526 (R3, R5): a `pauseOver` prompt takes
  `status.autopilot.resumedBy` only for `pause-` and stalled events once the
  milestone is no longer paused; `stuck/` events (holds record no resolver)
  and newer pauses get `operator` (D526-P1). Decision 5 has no label for a
  pause an owner ended (`closedBy: owner` without an answer): the GitLab
  state line says "Handled by an owner: the pause ended". R10 should use the
  same words.
