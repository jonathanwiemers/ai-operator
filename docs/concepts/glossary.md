# Glossary

The terms the docs use, in alphabetical order, one or two sentences each.
Other pages link a term here the first time they use it. Each entry links the
page that explains it; pages that are still being written are named in plain
text.

### Agent container

The container in each pod that runs the agent CLI and the model's commands.
It holds the model credentials and the checkout, but no GitLab token and no
cluster credentials. See [Security](security.md#two-containers-per-pod).

### AgentProject

The Kubernetes resource you write once per GitLab project: which project,
which tokens, which models, how many runs at a time. Its fields are in the
planned reference page reference/agentproject.md.

### Autopilot

A milestone with `/agent autopilot` in its description runs end to end:
planned, implemented, reviewed and merged issue by issue, with the
[pilot](#pilot) handling what goes wrong. See
[How it works](how-it-works.md#from-spec-to-merged-work) and the planned
guide guides/autopilot.md.

### Budget

A spending cap in US dollars, for an issue or for a whole milestone (the
`/agent budget <usd>` line). When it is spent, autopilot pauses and asks an
owner whether to raise it; see the planned guide guides/budgets-and-spend.md.

### Companion repository

A second GitLab project an issue may change in the same run, for example an
infrastructure repository. Your `AgentProject` allows it in
`spec.crossRepo.targets`, the milestone names it with `/agent repo <project>`,
and it gets its own merge request; see the planned guide
guides/cross-repo-changes.md.

### Decision

A choice an agent made and recorded on the issue, with why and the
alternatives it rejected, under an ID such as `D42-3` (or `D42-P1` for the
planner's). You can [revisit](#revisit) any of them. See
[How it works](how-it-works.md#from-spec-to-merged-work).

### Describer

The agent that writes and updates a merge request's description after each
push that changed the diff. See [Roles](roles.md#describer).

### Desk

The web UI where you steer: ask the [pilot](#pilot), chat with an agent,
answer [prompts](#prompt), revisit decisions, and watch runs, CI and costs.
See [How it works](how-it-works.md#steering-it) and the planned guides
guides/desk.md and guides/steer-from-the-desk.md.

### Engine

The coding agent CLI a role runs on: Claude Code (`claude`) or Cursor
(`cursor`). Each role and [profile](#profile) names an engine and a model;
see [Roles](roles.md).

### Hold

When an autopilot milestone's parallel limit is above 1, a problem of one
issue (red CI, a failed run, blocking findings) holds only that issue, even
if it is the only one running, and the issues that can still start go on.
If every running issue is held and nothing else can start, the milestone
[pauses](#pause) as `stuck`. A hold ends by itself once the cause is gone,
or with `/agent resume #N`; see [How it works](how-it-works.md#when-something-goes-wrong)
and the planned guide guides/pauses.md.

### Implementer

The agent that writes the code for one issue, in steps on a draft merge
request. See [Roles](roles.md#implementer).

### Landing lane

On an autopilot milestone, merge requests that passed the
[merge gate](#merge-gate) land one at a time: rebased when needed, checked
by CI on that head and merged, in the order they became ready. See
[How it works](how-it-works.md#from-spec-to-merged-work).

### Marker line

A line in a milestone description that starts with `/agent` and tells the
operator what to do with it: `/agent spec`, `/agent autopilot`,
`/agent owners @you`, `/agent owner-subjects`, `/agent parallel <n>`,
`/agent budget <usd>` and a few more. See the planned guide
guides/write-a-milestone.md and
[Commands](../reference/commands.md#in-the-milestone-description).

### Merge gate

The rule autopilot merges by: CI green, the review clean, the last run
succeeded, and the merge request's head is the one that was checked. A
model never decides to merge. See
[How it works](how-it-works.md#from-spec-to-merged-work).

### Milestone

A GitLab milestone whose description holds a [spec](#spec). It is the unit
the operator plans, runs on [autopilot](#autopilot) and releases. See
[How it works](how-it-works.md).

### Owner

A person the operator mentions and asks about a milestone, named with
`/agent owners @you` (GitLab users) and `/agent owner-subjects` (Desk logins).
Only owners answer [prompts](#prompt): in the Desk the owner-subjects, in
GitLab the owners. With `workflow.commandsFrom: owners` only the owners give
commands. See [Security](security.md#who-may-command).

### Pause

Autopilot stops the whole milestone: nothing new starts until the pause
ends. Each pause has a kind with its ways out, which the Desk shows on a
pause card. Some kinds always pause the milestone, for example `budget`,
`mainRed` or `stuck`; problems of one issue, such as `ciRed` or `stalled`,
only [hold](#hold) that issue when the milestone's parallel limit is above
1, and pause the milestone when the limit is 1. See
[How it works](how-it-works.md#when-something-goes-wrong) and the planned
guide guides/pauses.md.

### Pilot

The agent that watches a milestone for you: it answers your questions,
fixes what it may on its own, asks the [owners](#owner) when it needs a
decision, and writes the [retrospective](#retrospective). See
[Roles](roles.md#pilot).

### Planner

The agent that turns a milestone [spec](#spec) into
[sub-issues](#sub-issue). See [Roles](roles.md#planner).

### Profile

A named implementer setup, with its own engine, model and effort, that an
issue selects with an `agent-profile::<name>` label. The planner picks one
per issue; see [Roles](roles.md#implementer).

### Prompt

A question to the owners with two to four options, one of them
recommended, from the operator, the pilot or an agent, under an ID such as
`Q5`. You answer it in the Desk with one click or one sentence; see the
planned guide guides/steer-from-the-desk.md.

### Read gate

A rule on a role that stops it from reading big files whole, so it reads a
section or asks a subagent. See [Roles](roles.md#read-gate).

### Release point

A place in the plan after which the operator cuts a release, written into
the issue as a `**Release:**` line by the planner. The pilot can add or drop
one, and an owner can cut a release in between with `/agent release`; see
[How it works](how-it-works.md#planned-releases) and the planned guide
guides/releases.md.

### Retrospective

The pilot's last comment on a finished autopilot milestone: what was
merged, the decisions and pauses, cost and time, and numbered follow-ups you
can open as new issues. See [Roles](roles.md#pilot).

### Reviewer

An agent that reads each change of the implementer: bugbot for bugs,
security for security problems. See [Roles](roles.md#reviewers).

### Revisit

Asking to change a recorded [decision](#decision), in the Desk or with
`/agent revisit D42-3 <what you want instead>`. Before the merge a new run
changes the code; after the merge the operator opens a follow-up issue.

### Run

One piece of agent work in a [task](#task): a planning run, an
implementing run with its reviews, a CI fix or a chat turn. Each run has a
report and its cost; see [How it works](how-it-works.md#pods-per-issue).

### Runner

The trusted container in each pod. It holds the GitLab token, talks to
GitLab and pushes; it never runs the model's code. See
[Security](security.md#two-containers-per-pod).

### Scout

An optional subagent that searches and reads for a role on a cheap model.
See [Roles](roles.md#scout-and-writer).

### Spec

The description of a milestone: the goal, the requirements and the
decisions, with the [marker lines](#marker-line). The planner turns it into
issues, and each issue gets a short spec of its own. See the planned guide
guides/write-a-milestone.md.

### Sub-issue

One of the issues the planner writes for a milestone, one per merge
request, with its own short spec, acceptance criteria and dependencies. See
[How it works](how-it-works.md#from-spec-to-merged-work).

### Task

What the operator keeps per piece of work: one per planned milestone for
the planner, one per issue, and one per milestone with a pilot. Each task
has its own pod and volume; see [How it works](how-it-works.md#pods-per-issue).

### Tracking issue

The issue the operator opens for an autopilot milestone (or for a pilot
started with `/pilot coordinate`). It shows the queue and its state, takes
commands such as `/agent pause`, and is where the pilot posts. See
[How it works](how-it-works.md#from-spec-to-merged-work).

### Watchdog

The operator's check for runs that hang: no output from the pod for a long
time, or a pod that cannot start. The pilot can then restart the run, and an
owner can with *Restart run* in the Desk. There is no `/agent` command for
it, but the restart leaves a note on the issue; see
[How it works](how-it-works.md#when-something-goes-wrong).

### Wave

A layer of the plan in the Desk: issues without dependencies are the first
wave, and each later wave depends on an earlier one. Waves are for reading
the plan only; an issue starts as soon as its own dependencies are merged.

### Writer

An optional subagent that writes boilerplate, such as tests that follow an
existing file, for a role. See [Roles](roles.md#scout-and-writer).

## Next

- [How it works](how-it-works.md): the terms in context, from spec to
  release.
- [Roles](roles.md): the agents behind the terms.
- [Concepts](README.md): all concept pages.
