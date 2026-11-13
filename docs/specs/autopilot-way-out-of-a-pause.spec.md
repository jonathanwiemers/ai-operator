# Autopilot finds its own way out of a pause

Status: approved by the maintainer (2026-11-05) as the milestone spec of
"ai-operator: autopilot finds its own way out of a pause", with the
decisions below locked.

## Goal

Every autopilot pause has a known way out. The operator or the pilot takes
it where that is safe; otherwise the Desk shows the owner the way out as
one-click options. A pause that nothing can lift is detected and assessed,
never left waiting silently. The Desk shows where every paused autopilot
stands and how long pauses took.

## Background

Three dead ends on 2026-11-05:

1. **Orbit milestone 24, `ciRed` that never lifts.** `main` went red after
   #414 (`seatRing.test.ts` timeouts under coverage). The active MR !735 was
   red on the same job: CI state `BaseFailing`, pause `ciRed` with
   `resumeWhenReady`. The fix (!736) made `main` green at 10:12. But
   `BaseFailing` only clears when the MR's head pipeline changes
   (`pipelineChanged`, `internal/controller/ci.go`), and nothing starts one.
   The resume check (`autopilot.go`, `ResumeWhenReady` → `gateCheck`) needs a
   green MR pipeline, so the pause can never lift. A retry would fail anyway:
   the branch sits on the old base `2dc61015`, without the fix. It took an
   owner's `/agent merge the default branch` to move on.
2. **ai-operator milestone 4, `stuck` on a skipped dependency.** #83 was
   skipped because milestone 5's #125 replaces it. #88 depends on #83 in the
   plan, so `pick` paused: "#88 waits for the skipped #83". The only exit was
   closing #83 *with* `agent::done` (`doneByHand`). Closing alone or removing
   the GitLab link did nothing, and a `resume` before the label re-paused at
   once. The pilot did not know about the replacement in milestone 5 and
   proposed un-skipping #83; it has no tool for that either. Its prompt was
   never recorded because of `actions: null` (fixed in !164).
3. **Red `main` fixed by hand.** Orbit had `mainHealth.autoFix` off, so #425
   (`ci::main-red`) was handed to the agent by an owner. Its MR !736 then
   stayed outside the frozen milestone and waited for a human merge.
   example/infra !294 turned `autoFix` on for both projects. An owner's
   manual handoff still does not join the main-red lane.

Relevant code: `internal/controller/autopilot.go` (`pick`, `gateCheck`,
`rebase`/`rebased`, `mergeBase`, resume checks, `pause`),
`internal/controller/ci.go` (`baseFailing`, `pipelineChanged`),
`internal/controller/pilot_actions.go` (proposals → prompts, `checkAction`),
`internal/runner/pilot*.go` (pilot state and tools), `api/v1alpha1`
(`PauseKind` has 15 kinds), `internal/history` (`autopilot` events), and the
Desk `PauseCard.vue`, `PauseCardCompact.vue`, `PromptCard.vue` and Inbox.

## Decisions (locked)

1. **`BaseFailing` heals itself.** The active MR may be `BaseFailing`,
   paused or not. Once main health is `green` at a commit that is not the
   MR's base, the autopilot rebases it with the existing `rebase()`. Its diff
   hash check keeps the review valid. The existing `mergeBase` fallback
   covers conflicts and repeated failures. It runs once per main-green
   commit and is logged with a tracking-issue line.
2. **One exit table.** `api/v1alpha1` gets `PauseExits(kind)`: the automatic
   exits (conditions the operator lifts the pause on by itself) and the
   owner's remedies (commands or labels, with what each does). The operator's
   pause text, the pilot's state and the Desk pause card all read from it,
   so they cannot drift apart. A table test fails for a `PauseKind` without
   an exit.
3. **Stuck detector.** A pause whose autopilot state hash (items, CI states,
   main health, MR heads) has not changed for `pilot.stuckAfter` (default
   30m, optional field) triggers one pilot assessment per pause. The result
   is a Desk prompt whose options come from the exit table and the pilot's
   tools. It never resumes on its own.
4. **New pilot tools:**
   - `rebase` and `mergeBase` on the active MR;
   - `unskip` (a skipped item goes back to pending);
   - `markDone` (close an issue with the done label and a reason);
   - `startAnyway` (dependents of a skipped item may start, and the tracking
     issue gets a line saying so).
   They count as interventions. Under `managed` autonomy the pilot runs
   `rebase` and `mergeBase` itself. `unskip`, `markDone` and `startAnyway`
   are only ever prompt options and run after an owner's answer.
5. **Context from other milestones.** The pilot state lists issues in other
   open milestones of the project that reference this milestone's issues:
   GitLab "mentioned in" system notes, plus `%N #X` / "replaces" in their
   descriptions. Capped at 20 lines.
6. **Every main-red fix joins the main-red lane.** An agent-labelled
   `ci::main-red` issue is driven and merged by the frozen milestone, whoever
   handed it over (operator `autoFix` or an owner).
7. **No silent pilot write failures.** A pilot or operator status patch the
   API server rejects creates a `pilot.writeFailed` history event, an Inbox
   item (kind `pilotError`, with the API error) and a tracking-issue line.
   It is posted once per error text and source.
8. **Pause history.** The operator writes `autopilot.paused` and
   `autopilot.resumed` history events with kind, reason, item, and who
   resolved it: operator, pilot, owner or self-healed. The Desk derives the
   time paused, the pauses by kind, the time to resume and who resolved
   each from them.
9. **Desk:**
   - the pause card gets a "Way out" section: every exit with its live
     status, plus buttons for the remedies the viewer's grants allow;
   - the milestone context panel gets a "Pauses" card (history and totals);
   - the Inbox gets a top strip "N autopilots paused, longest <duration>"
     across projects, linking to each milestone.

## Requirements

Self-healing
- R1 Decision 1: rebase a `BaseFailing` MR once main is green on a newer
  commit. Regression test: Orbit's case (MR red on base X, main red, then
  main green on Y; rebase requested once; green pipeline resumes and merges;
  a second green commit while the rebase is in flight does not rebase
  again). Conflict test: falls back to `mergeBase`.
- R2 Decision 2: `PauseExits`, used by the pause text, the pilot state and
  the UI API. Table test over every `PauseKind`.
- R3 Decision 3: the stuck detector with `pilot.stuckAfter`. Tests: one
  assessment per pause, none while anything changes, none after resume.
- R4 Decision 4: the pilot tools in runner and operator (`checkAction`,
  autonomy counting, prompt options). Tests per tool, incl. `startAnyway`
  letting `pick` start #88's case and `markDone` making `doneByHand` true.
- R5 Decision 5: references from other milestones in the pilot state, with a
  test for "replaces %4 #83".
- R6 Decision 6: a hand-labelled `ci::main-red` issue in the main-red lane.
  Test: owner `/agent` on the main-red issue, then the frozen milestone merges
  its MR.
- R7 Decision 7: write-failure reporting. Test: a fake status patch error →
  history event, Inbox item, one note.

Observability and Desk
- R8 Decision 8: pause/resume events with the resolver. Store query for the
  milestone's pauses and totals; UI API on the milestone view.
- R9 "Way out" on the full pause card and a short version on the compact
  card. Remedies send the existing commands; buttons show only for grants
  that allow them.
- R10 "Pauses" card in the milestone context panel; Inbox strip of paused
  autopilots across projects.
- R11 Docs: `docs/usage.md` gets a section per pause kind with its exits
  (generated from or checked against `PauseExits`); `docs/commands.md` and
  `docs/configuration.md` cover the new tools and `pilot.stuckAfter`.

## Constraints

- `CLAUDE.md` rules: `make generate manifests`, `gofmt`, `go vet`,
  `make test`, `make helm-lint`, `make ui`. No kubebuilder RBAC markers in
  `internal/ui`. Docs and the chart README follow behaviour.
- New CRD fields are optional; nothing is removed or renamed. A new UI
  config key ships in a release before example/infra uses it
  (`UnmarshalStrict`).
- CRD schema changes get a `crdSchema.admit` test in `api/v1alpha1` (the
  fake client skips CRD validation, which hid the `actions: null` bug).
- Milestone 5 ("Desk prompts") is still changing `PromptCard.vue`, the pause
  cards and the prompt API. Desk slices (R9, R10) come last in the order and
  build on whatever milestone 5 has merged by then. Rebase, don't fork.
- The default stays: a skipped dependency pauses its dependents. R4 adds ways
  out, it does not change that.
- Target < 50 changed production lines per MR. Exceptions are allowed for R4
  (tools across runner and operator) and R9, stated in the MR. Every MR
  leaves `main` releasable.
- Tests at the lowest layer that owns the behaviour: controller tests with
  `gitlabfake` for R1, R3, R4, R6 and R7; store tests for R8; web logic in
  `web/test/*.mjs`; no render unit tests.

## Non-goals

- Changing skip semantics by default, or resuming a pause without an owner
  for anything beyond `rebase`/`mergeBase`.
- Retiring the GitHub workflows (CI/Release fail since the GitHub Actions
  minutes ran out; that is a separate change).
- A new pilot model or prompt rewrite beyond the new state and tools.
- Splitting the AgentProject reconciler.

## Suggested order

1. Spec copy + R1 `BaseFailing` self-heal
2. R2 `PauseExits` (pause text uses it)
3. R7 write-failure reporting
4. R6 main-red lane for manual handoffs
5. R5 references from other milestones in the pilot state
6. R4 pilot tools (runner, operator, prompt options)
7. R3 stuck detector
8. R8 pause/resume events and the store query
9. R9 "Way out" on the pause cards
10. R10 "Pauses" card and the Inbox strip
11. R11 docs pass

## Acceptance

- Replaying Orbit milestone 24 in a controller test: once main is green, the
  autopilot rebases !735 itself, and the milestone continues without an
  owner.
- Replaying ai-operator milestone 4: the stuck pause yields a Desk prompt
  whose options include `markDone #83` and `startAnyway #88`. Answering one
  starts #88.
- A rejected status patch shows up in the Inbox within one poll.
- Every `PauseKind` has at least one exit in `PauseExits`, and the pause
  card shows it.
- The Desk shows the paused autopilots across projects and the pause
  history of a milestone.
