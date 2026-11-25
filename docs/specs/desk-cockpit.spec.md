# Desk: the Cockpit, one main view, with the pilot as the only chat that reaches running agents

Status: the milestone's spec, run by autopilot. The milestone description in
GitLab holds the full text, with its background and file references; this
file keeps the goal, the decisions, the requirements, the constraints and
the acceptance, and the [Steering log](#steering-log). It supersedes the
milestone layout of [One Needs you tab per milestone](desk-needs-you.spec.md),
[the chat-first Desk](desk-chat-first-ui.spec.md) and
[one header and one row of tabs](desk-header-and-tabs.spec.md). The behaviour
is in [Steer from the Desk](../guides/steer-from-the-desk.md),
[The Desk](../guides/desk.md),
[Status and history](../reference/status-and-history.md#owner-messages),
[Web UI development](../contributing/web-ui.md#cockpit) and
[Design](../contributing/design.md#pod-topology). Its layout was later reordered by [Desk: the Cockpit newest first by issue](desk-cockpit-now-and-next.spec.md): threads by issue, Now & next instead of the live lines and the panel.

## Goal

1. A milestone page opens on the **Cockpit**: one stream where everything
   about the milestone comes together.
   - On top, pinned: open problems, open proposals and open questions, each
     answered in place.
   - In time order below: what the pilot did and why, merges, the owners'
     messages and milestone state changes.
   - Runs, CI rounds, reviews and checkpoints fold into one-line groups,
     closed by default.
   - A running agent shows as one live line that opens its transcript.
2. At the bottom of a milestone's Cockpit is one composer, to the pilot. The
   pilot is the only chat: task pages have no Chat tab.
3. An owner tells the pilot to pass words on to an issue's implementer, in
   the Cockpit composer or with `/pilot tell #712: …` on GitLab.
   - If #712's implementer is working on claude, claude takes the words in
     while it works, without stopping.
   - On cursor, or when that fails, the call stops at its current step and
     resumes the same session with the words.
   - Either way it happens within a minute, and the agent goes on from where
     it was. It neither waits for the run to end nor starts over.
   - If the issue is not running, the words go into its next run.
   - Each message is delivered exactly once. The Cockpit, the pilot's answer
     and the tracking issue say where it went.
   - A run that took a message is not counted as interrupted or failed, and
     it keeps its work.
4. A panel on the right shows Issues · Queue · Decisions. Below 1180 px it is
   a slide-over that opens from the header, on phones too.
5. The project page and the Overview open on their own Cockpit, with the
   same pinned cards, folded groups and panel, and no composer. The
   project's Cockpit spans its milestones; the Overview's spans all
   projects.
6. Fewer places: the milestone tabs are Cockpit · Issues · Metrics ·
   Retrospective. Activity, Needs you, Decisions, Board, the pilot drawer
   and the task Chat tab are gone, and old links open where their content
   moved. The Inbox stays as the list across projects; each entry opens its
   card in the Cockpit.

## Decisions

- **D1** The name is Cockpit. Rejected: Live, Flight deck, Stream, Mission.
- **D2** The milestone page, the project page and the Overview change
  together.
- **D3** The pilot is the only chat on milestones. The task Chat tab is
  removed, not kept read-only or behind ⋯. Plan chat stays.
- **D4** An owner's words reach a running implementer mid-run, not only the
  next run.
- **D5** Pinned (problems, proposals, open questions), shown (pilot messages
  and actions, merges, owner messages, milestone state) and folded (runs,
  CI, reviews, checkpoints, the rest). Rejected: everything expanded with
  filters.
- **D6** The Inbox stays as the cross-project list; each entry opens the
  milestone's Cockpit at its card. Acting happens only in the Cockpit.
- **D7** Mid-run delivery and the Cockpit are one milestone, mid-run
  requirements first, task chat removed only after them.
- **D8** Stream input for claude, stop and resume for the rest. A probe
  against the pinned claude CLI comes first.
- **D9** No composer on the project page or the Overview.
- **D10** The owner's own words are sent at once; the pilot says where they
  went. Actions the pilot proposes on its own keep the autonomy rules.
- **D11** Milestone tabs Cockpit · Issues · Metrics · Retrospective; panel
  Issues · Queue · Decisions; the Board a view of Issues.

## Requirements

API and operator:

- **R1** `spec.source.ownerMessages` (`{id, owner, text, at}`, the last 20)
  and `status.run.deliveredMessages` (ID, call, `stream`, `resume` or
  `nextRun`) on AgentTask.
- **R2** An owner's `instruct` on a running task appends an owner message
  and requests no run; an idle task and the pilot's own kinds keep
  `pilotInstruction` and `requestRun`. `describeAction` and `pilot.acted`
  say where the message went.
- **R3** An owner's `/pilot tell #N: …`, and a Cockpit message whose action
  is `instruct` with the owner's verbatim text, need no Accept. A
  non-owner's tell is refused; a pilot-proposed instruct stays a proposal
  under managed autonomy.

Runner:

- **R4** An opt-in probe (build tag, real claude login) checks that the
  pinned claude CLI replays a second user message written during a slow
  tool call and acts on it before the single result. If it fails, claude
  takes R6 and a follow-up issue is opened. A fake `claude` script for unit
  tests behaves as the probe showed.
- **R5** Claude implementer calls run with `--input-format stream-json
  --replay-user-messages`. The agent container takes a message for the
  running call; the runner watches the task and writes undelivered owner
  messages, framed as "## Message from @owner via the pilot". A message
  counts as delivered when its replay shows. At most 5 per run.
- **R6** Cursor calls, and claude calls whose write fails or whose replay
  does not come within 60 s, stop gracefully (SIGINT) and resume the same
  session with the message. A message during a reviewer call or a CI wait
  goes into the run's next implementer call, else the next run.
- **R7** Each delivery is recorded once; a message delivered mid-run is not
  repeated; an undelivered one goes into the next run and is recorded as
  `nextRun`. A stop for a message is no interruption, no failure and no
  usage-limit checkpoint. The stopped call is marked `stoppedForMessage`,
  and both calls are in the run's calls.
- **R8** The pilot's state shows each running issue's step, engine and
  waiting owner messages.

Web:

- **R9** A pure function sorts a milestone's events into shown events and
  folded groups per issue with a one-line summary; every `StreamKind` has
  one class.
- **R10** The Cockpit tab: pinned cards, the items newest at the bottom with
  paging, groups as closed collapsibles, search and *Expand all*, no filter
  chips. Problem and prompt links open their card.
- **R11** Live lines, "#712 implementing · 4 min", that open the transcript
  with the grant, else link to the task.
- **R12** The pilot composer at the bottom of the milestone Cockpit (chat,
  else a `/pilot` note, else read-only), the pilot line in its header. The
  pilot drawer, *Ask pilot* and `pilotQuery` go; on phones the composer
  stays above the keyboard.
- **R13** The milestone panel (Issues, Queue, Decisions), a column from
  1180 px, a slide-over below, kept in `?panel=`.
- **R14** Milestone tabs Cockpit · Issues · Metrics (+ Retrospective); the
  Board a view of Issues; the old tab ids aliased.
- **R15** The project Cockpit over all its milestones, no composer, panel
  Queue and Decisions, the Board a view of Milestones; old tabs aliased.
- **R16** The Overview Cockpit: the Inbox items of all projects pinned,
  Working now's live lines, merges and state changes shown, the rest folded
  per project; Working now in the panel. Tabs Cockpit · Timeline.
- **R17** Inbox entries open the milestone's Cockpit at their card.

UI server and cleanup:

- **R18** No task Chat tab (`?tab=chat` opens Overview). `POST
  …/tasks/{task}/chat` takes `message` and `interrupt` only on the pilot
  task, else 403 "talk to the pilot in the milestone's Cockpit"; `command`
  stays on every task.
- **R19** Remove what no page uses any more; knip stays green.
- **R20** Docs, screenshots and this spec with its steering log.

## Constraints

- Under 300 production lines per merge request.
- A run without owner messages keeps its prompts, calls, results and cost;
  only claude's input format changes.
- The message text is the owner's, framed as an instruction from `@owner`.
  It never widens the run's tools or read-only mode.
- Built from Nuxt UI components; keyboard rules and the axe check on every
  route, with no new known entries.
- Every old URL keeps working, and everything a removed tab or drawer showed
  is reachable from the Cockpit, its groups, the panel or the Issues tab.
- No new dependencies; the claude CLI stays as `Dockerfile.agent` installs
  it; the probe never runs in CI.

## Non-goals

Stream input for cursor; moving the Desk chat or the planner onto the new
input path; messages to reviewers, the planner or the describer; undoing a
delivered message; other engines; a composer on the project page or the
Overview; changes to plans, the task's Live, Logs and Calls tabs, Metrics or
Research; new event kinds besides the `pilot.acted` texts or a new stream
API; notifications for new pinned cards; a redesign of the problem card.

## Acceptance

1. **Cockpit.** A running milestone with one stuck issue and two merged ones
   opens on the Cockpit: the problem card on top, the merges and the
   pilot's actions in the stream, runs and CI rounds as closed groups that
   open by keyboard.
2. **Mid-run on claude.** While #N implements, its live line shows. An
   owner's "tell #N to use the existing retry helper" brings no Accept card;
   within a minute the transcript shows it as a user turn and `pilot.acted`
   says "delivered to the running implementer". The call goes on without a
   stop, a new run or a restart.
3. **Mid-run on cursor, and the fallback.** The event says the step
   restarted with it; the calls show a stopped and a resumed call with
   summed costs, and `status.run.interruptions` is 0. A claude call that
   never replays gets the same.
4. **Edge cases.** A message sent during a CI wait goes into the CI fix call
   if there is one, else the next run, and the tracking issue says which.
   After an operator restart right after a delivery, it is not delivered
   again.
5. **Panel.** At 1280 px Issues · Queue · Decisions beside the stream; at
   390 px header buttons open each view as a slide-over, Escape closes it,
   focus returns.
6. **Project and Overview.** A project with two running milestones opens on
   a Cockpit with both milestones' cards and groups, no composer. The
   Overview pins the Inbox items of all projects. An Inbox entry opens its
   card in the milestone's Cockpit.
7. **Old links and task chat.** `?tab=activity`, `needs`, `pilot`,
   `decisions` and `board` on a milestone and `?tab=chat` on a task open
   where their content moved. A task has no Chat tab, and a chat message to
   an implement task gets 403.

## Steering log

Collected from the milestone's merged merge requests and the repository at
the end of the milestone (issue #793, D793-P1: earlier issues recorded their
narrowings in their merge requests).

- **The claude probe (R4).** The stream-input path (R5) was built, which R4
  allowed only after the probe passed against the pinned CLI, so claude
  takes owner messages on stdin while it works. No follow-up issue for
  stream input was opened. The probe is
  `go test -tags claudeprobe -run TestClaudeProbe -v ./internal/runner/`
  ([Design](../contributing/design.md#pod-topology)); its output went into
  that issue's merge request, as R4 asked, and is not kept in the repository.
- **No session ID yet (R6).** The spec said the step starts again from the
  checkpoint. A graceful stop carries no error, so it takes no checkpoint and
  no `Reset`: the step's call starts again on the same work tree, with its
  full prompt and the messages after it. Nothing is lost either way.
- **Leftover messages (R7).** A message still waiting after its run gets one
  run request only when it came after that run started and no run is in
  progress or requested (`requestLeftovers`), so it is never requested
  twice.
- **Pinned is the page's, not the function's (R9).** `cockpitItems` tells
  only folded events from the rest: a `pinned` kind is also an event card in
  the stream. The pinned cards come from `MilestoneNeeds`, `ProjectPinned`
  and the Inbox. Under the Overview's scope (`all`) note threads fold too.
- **The composer has no Interrupt (R12).** The pilot chat sends messages
  only; the route still accepts `interrupt` on the pilot task (R18).
- **The Needs you count (R14)** became the badge of the *Cockpit* tab
  instead of a separate header count.
- **The Issues tab's switch (R14)** has Timeline (with the history
  database), Lanes and Board (not on a stored milestone); the table stays
  above it rather than being a fourth view. The panel buttons show on
  phones, and on the Issues tab at every width (D786-P2).
- **Old links (R14, R15, R16)** follow decisions D789-P1/P2, D790-P1 and
  D791-P2. A global `stream` → `activity` alias in `streamTabs.ts` is a
  leftover nothing reaches, since every page maps `stream` itself; it was
  left in place.
- **Inbox entries (R17).** An agent's question with a task opens the task
  page at its prompt, where it is answered; problems, prompts and proposals
  of a milestone open its Cockpit.
- **Docs (R20), narrowed: the screenshots were not retaken.** The agent
  container cannot start Playwright's Chromium (about 20 system libraries
  are missing, with no root to install them) and has no `cwebp`, so
  `make docs-screenshots` could not run. The task chat screenshot was
  removed with the Chat tab. The Overview, project and milestone Cockpit
  images (`dashboard`, `project-stream`, `milestone-pilot`) still show the
  Desk from before the Cockpit, so the docs pages stopped embedding them;
  [the screenshot list](../assets/screenshots/README.md) names the pages
  each goes back to. **Follow-up:** an issue "Docs: retake the Cockpit
  screenshots" runs `make docs-screenshots` on a machine with Chromium and
  `cwebp`, adds a shot of the panel as a phone slide-over, commits the
  images and puts them back on those pages. The operator opens it from
  this merge request; it is not open yet.
- No other requirement was cut.
