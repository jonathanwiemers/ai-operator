# Desk: one Decisions tab to read, search and revisit what the agents decided

Status: the milestone's spec, run by autopilot. Narrowed
requirements and in-flight changes go into the
[Steering log](#steering-log). The file refs of *Background* are those the
spec was written against; *Where it landed* lists the actual paths.

## Goal

1. A milestone page has a **Decisions** tab that lists every decision of its
   issues, from the planner and from the implementers. Each card shows the
   question, the option that was chosen, the other options, why it was
   chosen, who decided and when, without opening GitLab.
2. An owner finds a decision by searching its ID, title or text, and can
   filter to active or superseded ones. The project page has the same tab
   across its milestones.
3. A revisited decision shows its chain: who asked for the change, what they
   asked for, the follow-up issue and its state, and the decision that
   replaced it, step by step.
4. Before an owner revisits a decision, the card says in one line what will
   happen (a follow-up issue queued on autopilot, a comment on the open
   issue, or nothing because it was already replaced), and the Revisit
   action does exactly that.
5. Decision links in the Desk open this tab at the decision instead of the
   GitLab comment.

## Background

File refs are at origin/main b215c48, 2026-11-15.

How decisions are recorded:

- The planner and the implementer post decisions as markdown sections in
  bot comments on the issue, starting with `📌 **Decisions**`
  (`internal/marker/marker.go`). One section is rendered by
  `renderDecision` (`internal/runner/decisions.go`): a `#### <id>: <title>`
  heading, `**Question:**`, `**Chosen:**`, `**Why:**`, `**Alternatives:**`
  (joined with `"; "`), an optional `_Supersedes <id>._` line, and the
  marker `<!-- ai-operator:decision=<id> -->`. Planner IDs are
  `D<issue>-P<n>`, implementer IDs `D<issue>-<n>`. Planner decisions of
  draft issues are renumbered on a re-plan (`internal/runner/spec.go`).
  Older issues keep planner decisions in the description.
- A revisited decision gets `> ⚠️ Superseded by <id> in run …` and the
  marker `… superseded -->` (`markSuperseded`).
- Revisits of open issues are applied by the issue's next run
  (`findRevisits`). Revisits of closed issues become a follow-up issue
  `Revisit D42-3: …` (`revisitAfterMerge`,
  `internal/controller/revisit.go`), whose description names the requester
  and the requested change and carries the marker `ai-operator:revisit=<id>`.
  `syncRevisits` runs for every spec task with a milestone.
- From the Desk, an owner's Revisit goes to the milestone's pilot task
  (`internal/controller/prompt_revisit.go`). Without text it opens a
  `revisit` prompt with "Keep" and "Switch to <alternative>" options.

What the Desk saw:

- Only refs: `DecisionRef{ID, Title, URL, Superseded}` on
  `AutopilotItem.Decisions` (at most 20), collected when an autopilot issue
  is merged. Milestones without autopilot and open issues had none.
- The queue showed each decision as its ID linking to the GitLab comment,
  plus a Revisit button for the pilot (`web/src/components/QueueList.vue`,
  `web/src/milestoneQueue.ts`).
- The full text of a decision was nowhere outside GitLab. The history
  database had no decisions table.

## Decisions (locked)

1. **The history database keeps a copy of every decision; GitLab stays the
   source of truth.** Rejected: the UI server reading GitLab comments on
   every page view; storing full text in task status.
2. **All decisions of every planned milestone, autopilot or not, open
   issues included.** The stored set of an issue is replaced each time the
   issue is read, so renumbered planner decisions leave no stale rows.
3. **The operator reads incrementally.** `syncDecisions` runs next to
   `syncRevisits` for every spec task with a milestone. It lists the
   milestone's issues updated since its watermark minus the 5-minute clock
   skew (`revisitClockSkew`) and reads their notes. The watermark is
   `status.decisions.checkedAt`. Without it every issue is read once: the
   backfill. When a follow-up is read, the issue it revisits is read too.
4. **Only the bot's comments count as decisions**, as `isDecisionComment`
   says, plus planner decisions in the description of older issues. The
   comment format in GitLab does not change.
5. **Revisit requests are recorded from two sources:** human comments
   matching `marker.Revisit` with the project's command prefix, and
   follow-up issues by the bot with the `ai-operator:revisit=<id>` marker. A
   request counts as applied once a decision superseding its ID exists.
6. **The chain is derived when it is read, not stored.**
7. **The server decides the Revisit mode of each decision:** `prompt`
   (a closed issue on a milestone with a pilot task), `comment` (an open
   issue with a live implement task, posted through
   `POST …/tasks/{task}/notes` in the owner's GitLab name), `command`
   (anything else) and `none` (superseded, or a follow-up already open).
   Each comes with the line that says what will happen. Only the
   milestone's owners see the actions (`canRevisit`).
8. **Two tabs.** On a milestone page *Decisions* comes after *Needs you*,
   also on stored milestones. On a project page it comes after *Stream*,
   with a milestone filter. No cross-project page and no ⌘K search.
9. **Search is server-side.** `q` matches, case-insensitively, the ID,
   title, question, choice, why and alternatives. `state` is `all`,
   `active` or `superseded`. Grouped by issue in plan order, then by ID; at
   most 200 per response, newest milestones first.
10. **Decision text is untrusted agent text**, rendered as markdown without
    HTML.
11. **Rows expire with the milestone's Desk snapshot**, and the existing
    retention deletes them.
12. **Without the history database** the tab says "Decisions need the
    history database", and decision links keep pointing at GitLab.
13. **Desk links open the tab** (`?tab=decisions#<id>`). Each card keeps an
    *on GitLab* link. `AutopilotItem.Decisions` and `DecisionRef` stay.

## Requirements

### Operator (Go)

- **R1. Parse a decision section** into ID, title, question, choice, why,
  alternatives, supersedes, superseded and superseded-by.
- **R2. Store: decisions table and save.** A migration named by UTC time
  with `desk_decisions` and `desk_decision_revisits`;
  `SaveIssueDecisions` replaces one issue's rows in one transaction;
  retention deletes expired rows.
- **R3. Store: query and search.** `Decisions(project, milestone, q, state,
  limit)` with `superseded_by` derived and the revisit requests.
- **R4. Recorder.** `history.Recorder.RecordIssueDecisions` queues one
  issue's decisions without blocking.
- **R5. Status watermark** `status.decisions.checkedAt`
  (`DecisionSyncStatus`).
- **R6. syncDecisions: decision comments**, advancing the watermark only
  after every listed issue was read.
- **R7. syncDecisions: revisit requests**, re-reading the revisited issue.
- **R8. Tracking issue links the tab** under `### Decisions` when the Desk
  URL is configured.

### UI server (Go)

- **R9. Decision views and endpoint**
  `GET /api/v1/projects/{namespace}/{name}/decisions?milestone=&q=&state=`
  with `DecisionsResponse{available, decisions}`.
- **R10. Revisit mode and what it does**, with these texts:
  - `prompt` with autopilot: "Opens a revisit prompt. Your choice creates a
    follow-up issue in this milestone, queued after the planned issues.
    <id> is marked superseded when the follow-up posts its new decision."
  - `prompt` without autopilot: "… the follow-up starts right away
    (agent::ready) …"
  - `comment`: "Comments on #N. Its next run changes the code and posts a
    decision that supersedes <id>."
  - `command`: "Comment this command on #N or the tracking issue."
  - `none`: "Already replaced by <id>." or "#M already revisits <id>;
    comment there."

### Web

- **R11. Milestone Decisions tab** with search (`?q=`), *All / Active /
  Superseded* chips (`?state=`) and "No decisions recorded yet."
- **R12. Decision card** with anchor `#<id>`: title, ID, *Plan* or
  *Implementation*, question, options with *Chosen* first, why, run, when
  and *on GitLab*; muted *Superseded by <id>*; stacks on a phone.
- **R13. Chain and revisit on the card**, one action per mode.
- **R14. Project Decisions tab and relinked IDs**: a milestone select; queue
  IDs link to `?tab=decisions#<id>` with the database, to GitLab without.

### Docs

- **R15. Docs and spec**: the Decisions tabs, the card, the chain, the
  revisit modes and the links for users; the tables, `syncDecisions` and
  the watermark for contributors; this spec with a steering log.

## Constraints

- Under 300 production lines per merge request; tests, generated files and
  docs do not count.
- Run `make generate manifests` after API changes and `make ts-types` after
  view changes. New migrations are named by their UTC creation time.
- No new dependencies. No kubebuilder RBAC markers in `internal/ui`.
- Unchanged: the decision comment format, how runs post decisions and apply
  revisits, `DecisionRef` and `AutopilotItem.Decisions`, the revisit prompt,
  the replies of `revisitAfterMerge`, the agent prompts.
- The only new GitLab writes are the comments an owner sends from a card.
- `syncDecisions` adds GitLab reads only for issues updated since its
  watermark, plus the one-time backfill. Its errors are reported like other
  phase errors and do not stop the poll.

## Non-goals

- Discussing a decision with an agent that proposes new options.
- Decisions before planning, as option cards in the *Plan milestone* chat.
- Analysing what a revisit affects beyond its own issue.
- A cross-project decisions page, and decisions in ⌘K search.
- Backfill for closed milestones whose tasks are already deleted.
- Changing how decisions are posted, numbered or formatted in GitLab;
  editing or deleting a decision from the Desk.
- Showing the spec's own `## Decisions (locked)` as decisions.

## Where it landed

| Requirement | Paths |
|-------------|-------|
| R1 | `internal/marker/decision.go` (`ParseDecision`, `ParseDecisions`) |
| R2, R3 | `internal/research/store/migrations/20261117231921_desk_decisions.sql`, `internal/research/store/decisions.go` |
| R4 | `internal/history/decisions.go` |
| R5 | `api/v1alpha1/agenttask_types.go` (`DecisionSyncStatus`) |
| R6, R7 | `internal/controller/decisions_sync.go` |
| R8 | `internal/controller/autopilot.go` |
| R9, R10 | `internal/ui/decisions.go` |
| R11–R13 | `web/src/components/MilestoneDecisions.vue`, `DecisionCard.vue`, `DecisionChain.vue`, `DecisionRevisit.vue`, `web/src/decisions.ts` |
| R14 | `web/src/streamTabs.ts`, `web/src/views/Stream.vue`, `web/src/components/QueueList.vue`, `decisionHref` in `web/src/milestoneQueue.ts` |
| R15 | the pages in the steering log's #548 entry |

## Steering log

- #548 (R15): the spec names `docs/ui.md`, `docs/usage.md` and `docs/design.md`, which
  the docs cutover removed. R15 went to: `docs/guides/decisions.md` (new,
  for `ui.md`'s tabs, card, chain, modes and links), `docs/guides/desk.md`
  (one line and the project tab), `docs/guides/steer-from-the-desk.md` and
  `docs/guides/iterate-and-finish.md` (for `usage.md`),
  `docs/reference/desk-api.md`, `docs/contributing/design.md` (for
  `design.md`), `docs/contributing/web-ui.md` and
  `docs/concepts/glossary.md` (D548-P1). `docs/reference/commands.md` is
  unchanged: the revisit text did not change.
- Decision 11: the rows expire at the time of their save plus the history
  retention, the same rule as the snapshot's `expiresAt`, and
  `DeleteExpiredDecisions` deletes them on their own (no foreign key to
  `desk_milestones`: an issue's decisions can be read before its
  milestone's snapshot exists).
- Decision 9: both tabs order by milestone (newest first), then the issue's
  plan position, planner before implementer, and number.
- R10: `none` without a stored successor reads "Already replaced.". The
  `prompt` text without autopilot names the project's ready label. A user
  who is not an owner gets the text (and, for `command`, the command)
  without an action.
- R6: the watermark advances once every listed issue was read, also when
  the recorder's queue dropped an issue or a save failed; that issue is
  read again when it changes next.
- R13: `comment` posts through the implement task's notes endpoint, so the
  comment lands on the issue in the owner's GitLab name.
- R14 (#548): the link choice uses the existing `history` feature of
  `GET /api/v1/features`, not a new field (D548-P2). With *All milestones*
  each group header on the project tab names its milestone and links to
  that milestone's tab.
- No requirement was narrowed in the merged slices (up to #548) beyond the
  entries above; no follow-up issue was opened.
