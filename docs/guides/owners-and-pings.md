# Owners and pings

The [owners](../concepts/glossary.md#owner) of a milestone are the people
the operator turns to when the agent needs a human. This page shows how you
name them, when GitLab notifies them, and how you keep `/agent` commands to
them.

A milestone has two lists of owners, one for each place you steer from:

```
/agent owners @alice @bob
/agent owner-subjects alice@example.com bob
```

- **`/agent owners`** names GitLab users. The operator mentions them, and
  they may give the owners-only commands.
- **`/agent owner-subjects`** names Desk users, by the user name of their
  Desk login. Only they may answer the milestone's
  [prompts](../concepts/glossary.md#prompt), accept the pilot's proposals
  and restart runs in the [Desk](../concepts/glossary.md#desk).

Neither list stands in for the other. Both are
[marker lines](../concepts/glossary.md#marker-line) in the milestone
description, and changing them does not plan the milestone again.

## Who the owners are

The GitLab owners are, most specific first:

1. the milestone's `/agent owners` line, for the milestone and its issues.
   The line without names turns the mentions off for the milestone;
2. the project's [`spec.notify.users`](../reference/agentproject.md#specnotify);
3. the operator's default owners, the chart value `defaultOwners`.

An issue without a milestone takes the project's users, else the default.
The Desk owners have no fallback: without an `/agent owner-subjects` line,
nobody can answer the milestone's prompts in the Desk.

## When the owners get pinged

The operator mentions the owners (`cc @alice @bob`) in the comments that
need a human, so GitLab sends them a mail and a to-do:

| When | Where |
|------|-------|
| a run failed (not on autopilot) | the issue |
| a run gave up for an outside reason and the issue stays blocked: after 20 usage-limit waits, or when the automatic retry after an outside failure failed too | the issue, also on autopilot |
| the agent asks questions (not on autopilot) | the issue, in the first question's comment |
| the agent could not finish alone, for example conflicts it could not resolve | the merge request's report |
| CI is still red after the fix rounds (not on autopilot) | the issue |
| the issue reached review (not on autopilot) | the merge request's report, or "Ready for review" on the merge request once CI passed. The owners also become its reviewers if it has none. |
| autopilot paused or is done | the [tracking issue](../concepts/glossary.md#tracking-issue) |
| planning failed (autopilot) | the tracking issue, saying whether a retry follows |
| the default branch stayed red after a retry | the new `ci::main-red` issue, with the project's owners |
| a default-branch pipeline was canceled twice | the new `ci::main-canceled` issue, with the project's owners |

Only new comments carry the mention, never the progress comment that is
updated while a run goes on. On autopilot, failed runs, questions and red
CI ping through the pause on the tracking issue instead, and the pilot
asks the owners in the Desk (see
[When it pauses](pauses.md#when-it-pauses)).

To pick the situations, set `spec.notify.events`. To keep the reviewers of
merge requests to yourself, set `spec.notify.requestReview: false`. Both
are in [`spec.notify`](../reference/agentproject.md#specnotify). Owners
must be members of the project to be notified.

## Commands from owners only

By default anyone who can comment gives `/agent` commands. With
`workflow.commandsFrom: owners` only the GitLab owners count, checked by
their GitLab user, on issues, their merge requests, the tracking issue and
closed issues:

- Anyone else's command starts and changes nothing. It gets one reply in
  its thread: "🤖 Only the owners of this milestone can give `/agent`
  commands here". The replies are limited per author, see
  [Replies to comments not acted on](../reference/commands.md#replies-to-comments-not-acted-on).
- On a milestone without owners nobody's command counts, and the reply
  says to add an `/agent owners` line.
- Comments without `/agent` still reach the agent with its next run.

In the Desk, the owner-subjects decide the same things, whatever
`commandsFrom` says. See also
[Who may command](../concepts/security.md#who-may-command).

## Next

- [While the agent works](while-the-agent-works.md): the merge request, CI
  and conflicts.
- [Iterate and finish](iterate-and-finish.md): change requests, decisions
  and merging.
- [Commands](../reference/commands.md): every command, and which are owners
  only.
