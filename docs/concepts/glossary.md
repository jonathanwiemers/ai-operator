# Glossary

The terms the docs use, in alphabetical order, one or two sentences each.
Other pages link a term here the first time they use it. Each entry links the
page that explains it.

### Agent container

The container in each pod that runs the agent CLI and the model's commands.
It holds the model credentials and the checkout, but no GitLab token and no
cluster credentials. See [Security](security.md#two-containers-per-pod).

### AgentProject

The Kubernetes resource you write once per GitLab project: which project,
which tokens, which models, how many runs at a time. Its fields are in the
[AgentProject reference](../reference/agentproject.md).

### Arm

One variant of an [experiment](#experiment): a name and the implementer
settings it changes, such as a fresh session per step, a lower effort or a
[profile](#profile). An issue keeps its arm for every run. See
[Cost levers](../guides/cost-levers.md#how-issues-get-their-arm).

### Autopilot

A milestone with `/agent autopilot` in its description runs end to end:
planned, implemented, reviewed and merged issue by issue, with the
[pilot](#pilot) handling what goes wrong. See
[How it works](how-it-works.md#from-spec-to-merged-work) and
[Run a milestone on autopilot](../guides/autopilot.md).

### Budget

A spending cap in US dollars, for an issue or for a whole milestone (the
`/agent budget <usd>` line). When it is spent, autopilot pauses and asks an
owner whether to raise it; see
[Budgets and spend](../guides/budgets-and-spend.md).

### Cockpit

The main view of a milestone, a project and the Overview in the
[Desk](#desk). A milestone's Cockpit reads from the top: the message box to
its [pilot](#pilot), the latest exchange with it, what [needs you](#needs-you),
then one thread per issue with the latest activity first, with merges and
milestone state shown, [decisions](#decision) in their issue's thread, and
runs, CI rounds and reviews folded into closed lines. [Now & next](#now--next)
sits beside it. See [The Cockpit](../guides/steer-from-the-desk.md#the-cockpit).

### Companion repository

A second GitLab project an issue may change in the same run, for example an
infrastructure repository. Your `AgentProject` allows it in
`spec.crossRepo.targets`, the milestone names it with `/agent repo <project>`,
and it gets its own merge request; see
[Cross-repo changes](../guides/cross-repo-changes.md).

### Decision

A choice an agent made and recorded on the issue, with why and the
alternatives it rejected, under an ID such as `D42-3` (or `D42-P1` for the
planner's). You can [revisit](#revisit) any of them. The Desk's
*Decisions* panel shows them; see
[Read and revisit decisions](../guides/decisions.md) and
[How it works](how-it-works.md#from-spec-to-merged-work).

### Describer

The agent that writes and updates a merge request's description after each
push that changed the diff. See [Roles](roles.md#describer).

### Desk

The web UI where you steer: ask the [pilot](#pilot), chat with an agent,
answer [prompts](#prompt), revisit decisions, and watch runs, CI and costs.
See [How it works](how-it-works.md#steering-it) and the guides
[The Desk](../guides/desk.md) and [Steer from the Desk](../guides/steer-from-the-desk.md).

### Dormant project

A project that a [project set](#project-set) matches but that has no work: no
milestone with the spec marker, no ready issue and no task. It has no
[set child](#set-child), or loses it after `dormantAfter`. See
[Project sets](../guides/project-sets.md#active-and-dormant).

### Engine

The coding agent CLI a role runs on: Claude Code (`claude`) or Cursor
(`cursor`). Each role and [profile](#profile) names an engine and a model;
see [Roles](roles.md).

### Experiment

Two to four [arms](#arm) of implementer settings that a project tries side
by side, set in `agents.implement.experiment`. Each issue runs on one arm,
and the Desk's Metrics page compares the arms' cost per written line. See
[Cost levers](../guides/cost-levers.md).

### Hold

When an autopilot milestone's parallel limit is above 1, a problem of one
issue (red CI, a failed run, blocking findings) holds only that issue, even
if it is the only one running, and the issues that can still start go on.
If every running issue is held and nothing else can start, the milestone
[pauses](#pause) as `stuck`. A hold ends by itself once the cause is gone,
or with `/agent resume #N`; see
[When autopilot pauses](../guides/pauses.md#pause-or-hold).

### Implementer

The agent that writes the code for one issue, in steps on a draft merge
request. See [Roles](roles.md#implementer).

### Landing lane

On an autopilot milestone, merge requests that passed the
[merge gate](#merge-gate) land one at a time: rebased when needed, checked
by CI on that head and merged, in the order they became ready. See
[Several issues at once](../guides/autopilot.md#several-issues-at-once).

### Marker line

A line in a milestone description that starts with `/agent` and tells the
operator what to do with it: `/agent spec`, `/agent autopilot`,
`/agent owners @you`, `/agent owner-subjects`, `/agent parallel <n>`,
`/agent budget <usd>` and a few more. See
[Write a milestone](../guides/write-a-milestone.md#marker-lines) and
[Commands](../reference/commands.md#in-the-milestone-description).

### Merge gate

The rule autopilot merges by: CI green, the review clean, the last run
succeeded, and the merge request's head is the one that was checked. A
model never decides to merge. See
[The merge gate](../guides/autopilot.md#the-merge-gate).

### Merged lines

The lines an issue's merge requests merged, its
[companions'](#companion-repository) included, counted once from GitLab at
each merge and split into production, tests, docs and generated files. They
measure the outcome; [written lines](#written-lines) measure the effort. See
[Lines of code](../guides/budgets-and-spend.md#lines-of-code).

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

### Needs you

What waits for a milestone's owners, pinned on top of its Cockpit in the [Desk](#desk) (once a tab of its own): the
open and resolved [problems](#problem-thread), each with its thread, and
the pilot's proposals. See [The Desk](../guides/desk.md#the-pinned-cards).

### Now & next

The sidebar beside a [Cockpit](#cockpit) in the [Desk](#desk): what is going
on and what comes next. *Running* lists the issues an agent works on, with
step and time; *Waiting* the issues that wait for CI or a review and the held
ones; *Up next* the next issues; *Milestones* the milestone and the ones
after it. It collapses to a rail with counts; below 1180 px a one-line strip
under the header opens it. See
[The Cockpit](../guides/steer-from-the-desk.md#the-cockpit).

### Pause

Autopilot stops the whole milestone: nothing new starts until the pause
ends. Each pause has a kind with its ways out, which the Desk shows on a
pause card. Some kinds always pause the milestone, for example `budget`,
`mainRed` or `stuck`; problems of one issue, such as `ciRed` or `stalled`,
only [hold](#hold) that issue when the milestone's parallel limit is above
1, and pause the milestone when the limit is 1. See
[When autopilot pauses](../guides/pauses.md), with every kind and its ways
out.

### Pilot

The agent that watches a milestone for you: it answers your questions,
fixes what it may on its own, asks the [owners](#owner) when it needs a
decision, and writes the [retrospective](#retrospective). See
[Roles](roles.md#pilot) and [The pilot](../guides/pilot.md).

### Plan draft

One milestone [spec](#spec) in a Desk plan, before it exists in GitLab. A
plan holds up to 5 drafts in order, and their milestones are created in
that order, so a later draft can build on an earlier one as `%draft-N`.
See
[Write a milestone](../guides/write-a-milestone.md#draft-a-series-of-milestones).

### Planner

The agent that turns a milestone [spec](#spec) into
[sub-issues](#sub-issue). See [Roles](roles.md#planner).

### Problem thread

One stuck issue, or the milestone's own pause, pinned in a milestone's *Cockpit*
as a chat: its status, the cause and the
[problem facts](#problem-facts) on top, everything that happened oldest
first, and one place to act at the bottom, with the pilot chat. See
[The problem thread](../guides/pilot.md#the-problem-thread).

### Problem facts

What the operator collected about a stuck issue from task status and GitLab:
the merge request, the pipeline, the failed run, saved work and what waits on
the issue. The pilot gets the same facts. See
[The problem thread](../guides/pilot.md#the-problem-thread).

### Profile

A named implementer setup, with its own engine, model and effort, that an
issue selects with an `agent-profile::<name>` label. The planner picks one
per issue; see [Roles](roles.md#implementer).

### Project entry

One project a [project set](#project-set) names in `spec.projects`, with
its own settings on top of the set's template. A named project always has
a [set child](#set-child), and the set can adopt an existing AgentProject
for it. See
[Settings for one project](../guides/project-sets.md#settings-for-one-project)
and the [reference](../reference/agentprojectset.md#specprojects).

### Project set

An `AgentProjectSet`: one resource that makes the operator work in every
project of a GitLab group that its globs match, writing a [set child](#set-child)
for each project with work. See [Project sets](../guides/project-sets.md) and
the [reference](../reference/agentprojectset.md).

### Prompt

A question to the owners with two to four options, one of them
recommended, from the operator, the pilot or an agent, under an ID such as
`Q5`. You answer it in the Desk with one click or one sentence; see
[Steer from the Desk](../guides/steer-from-the-desk.md#answer-prompts).

### Push subscription

A device's permission to get [Desk](#desk) notifications while no Desk tab
is open. It is made in the Notifications dialog, stored with your user name
and roles, and deleted when you turn notifications off, sign out or don't
open the Desk for 30 days. See [Notifications](../reference/notifications.md#push).

### Read gate

A rule on a role that stops it from reading big files whole, so it reads a
section or asks a subagent. See [Roles](roles.md#read-gate).

### Release point

A place in the plan after which the operator cuts a release, written into
the issue as a `**Release:**` line by the planner. The pilot can add or drop
one, and an owner can cut a release in between with `/agent release`; see
[How it works](how-it-works.md#planned-releases) and
[Releases](../guides/releases.md).

### Retrospective

The pilot's last comment on a finished autopilot milestone: what was
merged, the decisions and pauses, cost and time, and numbered follow-ups you
can open as new issues. See
[The retrospective](../guides/pilot.md#the-retrospective).

### Reviewer

An agent that reads each change of the implementer: bugbot for bugs,
security for security problems. See [Roles](roles.md#reviewers).

### Research

A question an agent answers on the web, once or on a schedule, as an
`AgentResearch` next to your projects. The Desk shows the result as a news
flash or a report. See [Research](../guides/research.md).

### Revisit

Asking to change a recorded [decision](#decision), in the Desk or with
`/agent revisit D42-3 <what you want instead>`. Before the merge a new run
changes the code; after the merge the operator opens a follow-up issue. See
[Decisions and Revisit](../guides/iterate-and-finish.md#decisions-and-revisit).

### Run

One piece of agent work in a [task](#task): a planning run, an
implementing run with its reviews, a CI fix or a chat turn. Each run has a
report and its cost; see [How it works](how-it-works.md#pods-per-issue).

### Run order

The order in which a project's open [autopilot](#autopilot) milestones run.
Only the first starts new issues. New milestones join at the bottom; done
or closed ones leave. An [owner](#owner) changes it by dragging in the
[Desk](#desk) or with `/agent move`. See
[Order milestones and read the timeline](../guides/milestone-order-and-timeline.md#the-queue).

### Runner

The trusted container in each pod. It holds the GitLab token, talks to
GitLab and pushes; it never runs the model's code. See
[Security](security.md#two-containers-per-pod).

### Scout

An optional subagent that searches and reads for a role on a cheap model.
See [Roles](roles.md#scout-and-writer).

### Set child

An [AgentProject](#agentproject) that a [project set](#project-set) wrote for
an active project and owns. It polls through its set and goes when the
project is [dormant](#dormant-project). See
[Project sets](../guides/project-sets.md#active-and-dormant).

### Settling

The state of a milestone whose autopilot is done and whose retrospective is
posted: it waits up to 7 days for follow-ups. The operator then reads only
its tracking issue, at most every 15 minutes. See
[Retrospective](#retrospective).

### Sign-off

An owner's confirmation of one version of a plan's decision in the Desk,
made by keeping it or changing it in the wizard. A decision the agent
changed on its own shows *Not signed off* until an owner keeps it. See
[Write a milestone](../guides/write-a-milestone.md#the-decisions-tab-and-sign-off).

### Spec

The description of a milestone: the goal, the requirements and the
decisions, with the [marker lines](#marker-line). The planner turns it into
issues, and each issue gets a short spec of its own. See
[Write a milestone](../guides/write-a-milestone.md).

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
[The tracking issue](../guides/autopilot.md#the-tracking-issue).

### Watchdog

The operator's check for runs that hang: no output from the pod for a long
time, or a pod that cannot start. The pilot can then restart the run, and an
owner can with *Restart run* in the Desk. There is no `/agent` command for
it, but the restart leaves a note on the issue; see
[How it works](how-it-works.md#when-something-goes-wrong) and the
[`stalled` pause](../guides/pauses.md#stalled).

### Wave

A layer of the plan in the Desk: issues without dependencies are the first
wave, and each later wave depends on an earlier one. Waves are for reading
the plan only; an issue starts as soon as its own dependencies are merged.

### Writer

An optional subagent that writes boilerplate, such as tests that follow an
existing file, for a role. See [Roles](roles.md#scout-and-writer).

### Written lines

The lines the [implementer](#implementer) calls wrote: every step commit,
rewrites and fix rounds included, every file counted. They measure the
effort; [merged lines](#merged-lines) measure the outcome. See
[Lines of code](../guides/budgets-and-spend.md#lines-of-code).

## Next

- [How it works](how-it-works.md): the terms in context, from spec to
  release.
- [Roles](roles.md): the agents behind the terms.
- [Concepts](README.md): all concept pages.
