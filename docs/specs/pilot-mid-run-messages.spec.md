# Owners reach a running implementer through the pilot, mid-run

Status: the milestone's spec, run by autopilot. The milestone description in
GitLab holds the full text, with its background and file references; this
file keeps the goal, the decisions, the requirements and the
[Steering log](#steering-log). Nearly all of it shipped with
[Desk: the Cockpit](desk-cockpit.spec.md) (its R1–R8 and R20); this
milestone's one merge request added the missing tests and this file. The
behaviour is in [The pilot](../guides/pilot.md#your-words-to-the-agent),
[While the agent works](../guides/while-the-agent-works.md),
[Status and history](../reference/status-and-history.md#owner-messages) and
[Design](../contributing/design.md#pod-topology).

## Goal

An owner tells the pilot to pass words on to an issue's implementer, in the
Desk chat or with `/pilot tell #712: …`. A running claude implementer takes
them in while it works; on cursor, or when that fails, the call stops at its
current step and resumes the same session with them. An idle issue gets them
in its next run. The owner's own words need no Accept, each message is
delivered exactly once, and the pilot says where it went.

## Decisions

1. **An owner's words reach a running implementer mid-run** (D4), instead of
   waiting for the run to end.
2. **Stream input for claude, stop and resume for the rest** (D8), after a
   probe of the pinned claude CLI.
3. **The owner's own words are sent at once** (D10), without a proposal to
   accept. Actions the pilot proposes on its own keep the autonomy rules.

## Requirements

- **R1.** `spec.source.ownerMessages` and `status.run.deliveredMessages`
  (`stream`, `resume`, `nextRun`), at most the last 20 messages.
- **R2.** An owner's instruct on a running task appends a message and
  requests no run; an idle task and the pilot's own kinds keep the next-run
  path.
- **R3.** An owner's `/pilot tell` and Desk chat words are acted on without
  Accept.
- **R4.** An opt-in probe of the pinned claude CLI decides whether claude
  gets stream input.
- **R5.** Claude implementer calls take messages on stdin, recorded as
  `stream` once replayed, at most 5 mid-run per run.
- **R6.** Cursor, and claude without a replay, stop gracefully and resume
  the session with the message, recorded as `resume`.
- **R7.** Each message is delivered exactly once, also after a pod restart;
  a stop for a message is no interruption and both calls count.
- **R8.** The pilot's state shows each running issue's step, engine and
  waiting messages.
- **R9.** The Desk stream shows the delivery texts; the Live transcript
  shows a delivered message as a user turn.
- **R10.** Docs for the guides, the reference, the runner internals and this
  spec.

## Steering log

Collected at the end of the milestone (issue #795, D795-P1: plan only the
gaps). The details of R1–R9 are in
[desk-cockpit's steering log](desk-cockpit.spec.md#steering-log).

- **R1–R3, R5, R7, R8** shipped with desk-cockpit's R1–R8: the fields, the
  owner's instruct onto the running task, no Accept for the owner's own
  words, stream input, delivery into the next call and run, and the running
  implementer in the pilot state.
- **R4, the probe, passed** against the pinned CLI, so claude keeps stream
  input and no follow-up issue was opened (D795-P5). Run it with
  `go test -tags claudeprobe -run TestClaudeProbe -v ./internal/runner/`.
- **R6, no session ID yet.** A stop before claude reported a session ID
  starts the step's call again on the same work tree, with the message after
  its prompt, not from a checkpoint, as desk-cockpit's log records.
- **R2 wording** (D795-P4). A `nextRun` delivery reads "delivered with #N's
  next run at …". "queued for #N's next run" is the pilot's immediate answer
  for an idle task.
- **R7 and R9, tests added by #795.** A fresh runner after a restart writes
  only the undelivered message into a running claude call, counts only this
  run's deliveries in the cap, and puts a waiting message into the next
  run's prompt once, recorded once as `nextRun`. A fresh reconciler writes no
  second note or `pilot.acted` entry for a message already reported. A web
  test checks the three delivery texts as shown stream items. They passed
  as written: no fix was needed.
- **R9, the transcript user turn,** was covered already by the web
  transcript tests.
- **R10.** The docs came with desk-cockpit's docs change (`5853d804`); #795
  changed no user page.
- No other requirement was cut.
