# The Desk

The [Desk](../concepts/glossary.md#desk) is the web UI where you steer ai-operator. You see
what needs you, follow every [milestone](../concepts/glossary.md#milestone) and
[task](../concepts/glossary.md#task), and act on them: answer prompts, talk to the
[pilot](../concepts/glossary.md#pilot), chat with an agent, resume a pause. This page shows
how you get in, who sees what, and what each view shows. What you do in them is in
[Steer from the Desk](steer-from-the-desk.md). To turn the Desk on, follow
[Turn on the Desk](../getting-started/turn-on-the-desk.md).

## Log in

Open the Desk's address and sign in at your identity provider. The Desk
opens on *Overview*. Your user name is on the [account menu](#the-account-menu)'s
button at the bottom of the rail; open it to see the roles the Desk got from your provider.

Your login renews itself while you use it, and your roles are read again
each time. A role you lose at the provider is gone in the Desk within
minutes. If the login ends while a page is open, the Desk signs you in again
by itself and puts back text you had not sent yet.

## Who sees what

Nobody sees anything unless an access rule grants it. You write the rules
in the chart values, under `ui.authorization.rules`. A rule matches a user
who has **any** of its `roles`; `"*"` matches every logged-in user. A user
sees what all matching rules grant together.

```yaml
ui:
  authorization:
    rules:
      - roles: [ai-operator-admin]      # every project, every grant
        transcript: true
        steer: true
        conversation: true
        chat: true
      - roles: [shop-devs]
        projects: ["agents/shop"]       # streams, boards and tasks only
```

- **`projects`** are `namespace/name` patterns with `*` and `?`; `*` does not
  cross the `/`. Without `projects` the rule grants every project. Seeing a
  project means seeing all its tasks. A project you may not see does not
  exist for you in the Desk.
- **`chat: true`** lets you send Desk chat messages to agents, answer
  [prompts](../concepts/glossary.md#prompt), and use the buttons on pause
  cards and in the queue. It needs the chat writer login (`ui.chat.writerSecret`)
  and `agents.chat: true` in the project. Who may chat is decided by this
  grant, not by GitLab membership.
- **`steer: true`** lets you write GitLab comments from the Desk, in your
  own name (see [GitLab comments](steer-from-the-desk.md#gitlab-comments-from-the-desk)).
  GitLab's own permissions apply on top. It also lets
  [owners](../concepts/glossary.md#owner) plan milestones, read a task's
  logs and turn leftovers into a follow-up milestone.
- **`conversation: true`** shows the text of GitLab comments in the streams
  and of Desk chat messages. These are readable without access to the
  GitLab project, so they are a grant of their own. Without it a comment
  shows as who commented where, with a link.
- **`transcript: true`** shows the live transcripts of runs. They can quote
  code, issue text and CI logs.

Answering a milestone's prompts and the owner-only buttons (such as
*Move to top*, *Merge anyway*, *Revisit* and *Restart run*) also need your Desk user name on the milestone's
`/agent owner-subjects` line (see [Owners and pings](owners-and-pings.md)).

A user whose roles match no rule sees *No projects* in the rail. The roles
under your user name help you fix the role mapping.

### Map your provider's claims

Two chart values read your user name and roles from the login token:
`ui.oidc.claims.username` and `ui.oidc.claims.roles`. Each is a
[CEL](https://cel.dev) expression over `claims`, the token's claims plus
those only the provider's userinfo endpoint returns. `username` must give a
string. `roles` may give a list of strings, one string, or a map whose keys
are the roles. If the roles claim is missing, the user has no roles. If the
user name cannot be read, the login fails, and the Desk's log says why. The
expressions for common providers are in
[Turn on the Desk](../getting-started/turn-on-the-desk.md#other-providers).

### Research rules

With the research database the Desk also has a *Research* section. A rule
with `research` set is a research rule: `research: read` shows researches
and their results, `research: write` also lets you start one-off researches
and delete your own (with `ui.research.allowCreate`). `researches` are
`namespace/name` patterns like `projects`.

Project rules and research rules are kept apart: a research rule grants no
project and must not set `projects`, `transcript`, `steer`, `conversation`
or `chat`, and a project rule grants no research. So add a rule for each:

```yaml
ui:
  research:
    allowCreate: true
    namespace: agents                   # where one-off researches are created
  authorization:
    rules:
      - roles: [ai-operator-admin]      # every project
        transcript: true
      - roles: [ai-operator-admin]      # every research, and one-offs
        research: write
      - roles: [news-readers]           # only the news flashes
        research: read
        researches: ["agents/news-*"]
```

One-off researches are created in `ui.research.namespace` only, so the write rule must
match names there (for example `agents/*`). A user may start 10 per hour and delete only
their own. Scheduled researches are managed with `kubectl`.

## Find your way

![Desk overview with running, paused and done milestones, their progress, cost and tokens](../assets/screenshots/dashboard.webp)

- **The rail** on the left starts with [New plan](write-a-milestone.md#draft-it-in-the-desk)
  (with Desk chat; an icon when collapsed), then *Inbox*, *Overview*, *Metrics* (cost, code, flow and pauses across
  projects, see [The Metrics page](budgets-and-spend.md#the-metrics-page); old `/costs` links open it), *Plans*, your projects with a status dot
  (running, needs you, idle), a count of what needs you, the counts of active and
  [settling](../concepts/glossary.md#settling) milestones, the researches, and how much
  of each [engine's](../concepts/glossary.md#engine) usage window is used, with
  when it resets (*resets 14:05*, *resets Tue 09:00*, or *reset* once past). It ends in the
  [account menu](#the-account-menu).
- **Breadcrumbs** lead back up from pages below the top level: *Overview* ›
  project › milestone › issue, or *Overview* › project › *Plans* › plan.
- **Search**: ⌘K (macOS) or Ctrl+K opens the search from anywhere. It finds
  projects, milestones, issues and merge requests, and pages such as the
  Inbox, *Plans* or *Plan milestone*, which opens the project's *Plans* tab.
- **Live**: every page updates by itself.
- **Pages** share one header: status, title, one main button and a ⋯ menu,
  and a line with cost and counts. One row of tabs sits below. A tab's
  switches and filters, with search on the right, sit in a bar on top of it.

### The account menu

The button with your name and the live dot at the bottom of the rail opens
it, also with the rail collapsed (then it shows your initials). On a phone,
tap *Projects* in the bottom bar first. It has your login, roles and how long
the login lasts; **Appearance** (*Light*, *Dark* or *Auto*, which follows your
system; this browser remembers it); the operator's version, with a note when
an update is rolling out or out; and **Sign out**. On a test cluster you can
run the Desk [without login](../getting-started/turn-on-the-desk.md#without-login):
everyone who can port-forward to it sees every project, and the menu says
*No login (auth off)*, without *Sign out*.

## Inbox

The *Inbox* lists what needs you across your projects, newest first. The
count also shows in the browser tab's title, as `(3) Desk`:

- prompts and questions from an agent or the pilot;
- tasks that failed or are blocked;
- paused autopilots, as a short pause card. A strip on top says how many
  autopilots are paused and for how long, longest first;
- [companion](../concepts/glossary.md#companion-repository) merge requests
  ready for the owners to merge;
- issues that spent 80 % of their own [budget](../concepts/glossary.md#budget);
- a status write of the pilot that the cluster refused, for a day.

A row opens where you act: the milestone's *Cockpit* with its card open and in view, or the task page.

The *Open | Handled* switch on top changes the list. *Handled* shows up to
50 rows that closed in the last 7 days, newest first: the prompts, with who
closed them and why, and the actions the pilot ran on its own or after an
owner accepted them. A row opens its card in the milestone's *Cockpit*;
`/inbox?view=handled` opens *Handled* directly.

### Browser notifications

Click *Notify me* in the Inbox header and allow notifications in your
browser. Each new pause, prompt, pilot proposal, agent question, ready
companion and budget warning then raises one notification (failed and blocked
tasks only count in the Inbox); a click opens the row's place. They only come
while a Desk tab is open. *Notifications on* turns them off.

## Overview

*Overview* (`/`) shows, from top to bottom:

- **the summary**: milestones running, paused and waiting, what needs you,
  and the spend of today and the last 7 days;
- **Working now**: per project the issue that runs now and for how long, what
  is next, usage limits, and *N finishing* for done milestones not yet closed;
- **In progress**: every milestone in work, those that need you first;
- **Recently done**: the newest done milestones, *Show all* for more;
- **Timeline**: which issue of which project ran when, in the last 6 h,
  24 h or 7 d ([the Overview timeline](milestone-order-and-timeline.md#the-overview-timeline)).

A milestone row shows its progress (merged of all issues), open issues, cost,
tokens and open leftovers; an unknown cost is a dash, never $0. The *Activity*
tab (`/?tab=activity`, old `?tab=stream` links too) shows every event of all your projects.

## A project

A project page has the tabs *Cockpit | Milestones | Metrics*, and *Plans* with Desk chat. It opens on *Cockpit*:

- **Cockpit**: the stream of all the project's milestones. On top are the pinned cards of every milestone that waits for you, each under the milestone's name: its problems, proposals and questions, answered there.
  *Talk to the pilot* opens that milestone's Cockpit, which has the message box; the project page has none.
  Below come the events, newest at the bottom, with runs, CI and reviews folded into one group per milestone and issue (open one, or *Expand all*), a search, and a line per running issue that links to its milestone.
- **Milestones**: the project's [run-order queue](milestone-order-and-timeline.md#the-queue),
  which owners drag into order, the [milestones that are finishing](steer-from-the-desk.md#the-queue-and-finishing),
  and its milestones in progress and recently done. The switch on top shows the *Board* instead: one board per milestone, plus the issues without one (`?view=board`).
- **Metrics**: what the project's agent calls cost, with its lines of code.
- **Plans**: the project's [plans](write-a-milestone.md#draft-it-in-the-desk)
  and *New plan* for this project; a [plan's decisions](write-a-milestone.md#answer-the-agents-decisions) have a wizard.

![A project page with its Cockpit: a milestone's pinned cards and the stream, beside the panel](../assets/screenshots/project-stream.webp)

A panel on the right switches between *Queue* (the active milestone's queue, its companions and the last 30 days; pick a day to page the Cockpit back to it, *Back to now* returns) and *Decisions* (the decisions of all milestones, or of the one you pick).
Below 1180 px the *Queue* and *Decisions* buttons in the header open it from the right; Escape closes it. The open view is in the address (`?panel=decisions`).

Old links to the former tabs open where their content is now: `?tab=activity` and `stream` open *Cockpit*, `?tab=decisions` the panel's *Decisions* (`?panel=decisions`, the milestone you picked stays), and `?tab=board` the *Board* of *Milestones* (`?tab=milestones&view=board`).

## A milestone

A milestone page opens on *Cockpit*, which counts what waits for you: problems, proposals and questions pinned on top, answered there,
then the events, newest at the bottom, with runs, CI and reviews folded into groups (open one, or *Expand all*) and a search.
Then come *Issues* and *Metrics*, and *Retrospective* once written. The tab is in the address, so you can link to it. The Cockpit ends with the [message box to the pilot](#message-the-pilot).
A panel on the right switches between *Issues* (the issues by state: running, waiting, next, merged, skipped; *Open table* opens the Issues tab), *Queue* (the project's milestones, this one marked) and *Decisions* (superseded ones folded). Below 1180 px, and on the Issues tab, the *Issues*, *Queue* and *Decisions* buttons in the header open it from the right; Escape closes it. The open view is in the address (`?panel=queue`).

Old links to the former tabs open where their content is now, and the address changes to match: `?tab=activity`, `stream`, `needs` and `pilot` open *Cockpit*, `?tab=decisions` the panel's *Decisions* (`?panel=decisions`), and `?tab=board` the *Board* view of *Issues* (`?tab=issues&view=board`).

### The pinned cards

![The Cockpit's pinned cards with the pause card, its ways out and an open question](../assets/screenshots/milestone-pilot.webp)

What waits for the milestone's owners, on top of the Cockpit; the *Cockpit* tab's count says how many. On the left are the
[problems](../concepts/glossary.md#problem-thread), one per stuck issue and
one for the milestone (a budget pause), open first, each with its status
(*Waiting for you*, *Operator acting*, *Pilot looking*, *Resolved*), time and
cause. Under them are the pilot's proposals with *Accept* and *Reject*, and
the pause totals. Click a problem to open its **thread** beside the list
(below on a phone): what happened, oldest first, and one place to act at the
bottom ([The problem thread](pilot.md#the-problem-thread)). Each problem has
its own address; `#prompt-Q3` or `#problem-280` links open it.

### Issues

![Paused milestone with its issue table, release rows and the pause banner](../assets/screenshots/milestone-issues.webp)

The milestone's state, progress and cost, then one row per issue: its
title, state, merge request, wall time (first run to merge), agent time,
time paused, cost, tokens, and written and merged lines (see [Lines of
code](budgets-and-spend.md#lines-of-code)). An issue that shipped shows its version.
Between the issues sit the releases, at the point they were decided:
*Release after #N*, *Releasing…* with its pipeline, or the version with a
link to the GitLab release.

Below, a switch picks the view, kept in the address (`?view=`):

- *Timeline* shows when each issue ran, waited and was held (see
  [The issue timeline](milestone-order-and-timeline.md#the-issue-timeline)).
- *Lanes* groups the issues: *Running*, *Landing*, *Ready to land*, *Waiting*, *Held* and *Done*, with the
  [waves](../concepts/glossary.md#wave) of the plan. Each item shows its merge
  request, task and [decisions](../concepts/glossary.md#decision). Owners set
  how many issues run at a time here.
- *Board* shows the milestone's tasks in columns: *In progress*, *Needs you*, *CI* and *Review*.

A milestone with companion merge requests lists them under *Companions*.

Click a row to open the **issue page**: the same figures, the task while one
works on the issue, and the issue's own stream.

### Decisions

The panel's *Decisions* lists every [decision](../concepts/glossary.md#decision) of the milestone's
issues, with the option chosen; replaced ones are folded. It needs the history database. A link ending in `#D42-3` opens that line.
The project panel's *Decisions*, with the milestone picked, shows the full cards: the
question, the option chosen (*Chosen*), the alternatives, why, the run and a link to the comment on
GitLab, a search, and *Active* or *Superseded*.

A revisited decision shows its chain: who asked, what for, the comment or
follow-up issue, and the decision that replaced it. Owners get *Revisit* (opens the prompt in the milestone's *Cockpit*),
*Comment* (on an open issue, in your GitLab name) or a command to copy.
See [Read and revisit decisions](decisions.md).

### Message the pilot

The Cockpit ends with *Message the pilot*: the pilot's autonomy, cost and
task link, then the box. Your messages and the pilot's answers show in the
stream; on a phone the box stays above the keyboard. A done milestone has
none. See [Steer from the Desk](steer-from-the-desk.md#ask-the-pilot).

### Retrospective

![Retrospective with cost drivers by issue and model, and numbered leftovers](../assets/screenshots/milestone-retrospective.webp)

Once the milestone is done: the pilot's [retrospective](../concepts/glossary.md#retrospective),
the cost drivers (the issues and roles that cost most), and the leftovers
with their state. Owners turn leftovers into a follow-up milestone here, see
[The retrospective](pilot.md#the-retrospective).

### Metrics

*Metrics* is the milestone's cost and
[lines](budgets-and-spend.md#lines-of-code); `?tab=spend` still opens it.

### Done milestones

With the research database, a done milestone stays after its tasks are
deleted, read from history: *Issues* says *From history*, with no board and no
message box. Without the database the Desk shows only milestones with live
tasks, and no spend or per-issue figures.

## A task

![Task chat between a user and the implementing agent](../assets/screenshots/task-chat.webp)

A task page's header has its status (such as *Running*), the title, and a
line with the step and how long it runs, the last result and activity,
restarts, *Stalled* when the [watchdog](../concepts/glossary.md#watchdog)
sees no progress, and the cost. On the right are the merge request and
*Restart run* (owners with `chat`), the rest in ⋯. Banners below the tabs
show a block, a usage limit or an out-of-memory restart.

Its tabs, each shown when there is something in it:

- **Overview**: runs, spend, branch and last push on top, then the last
  run's summary, the agent's questions, review findings, and the pipeline
  with its fix rounds and failed jobs. A Spec task adds the plan and the
  autopilot's state. The pod, how to follow its logs, and the conditions
  close the tab. Old `?tab=details` links open it.
- **Chat**: *Chat with the agent* and the GitLab conversation, see
  [Steer from the Desk](steer-from-the-desk.md#chat-with-an-agent).
- **Live**: the [live transcript](#live-transcript), with the `transcript`
  grant.
- **Logs**: for owners with the `steer` grant, the last 500 lines of the
  runner's or agent's container, *Download*, and *Follow* for new lines.
- **Calls**: every agent call, with the database: per run its written lines
  and its calls with model, tokens, duration and cost; for the implementer
  the lines each call changed; a 💾 badge where a call saved a checkpoint;
  and a chart of how the session's context grew.

A task's stage follows its status the same way the operator sets the GitLab
labels; hover a badge to see why. After a push the task stays in *CI* until
the operator has seen that commit's pipeline.

### Live transcript

The *Live* tab follows the task's pod while it runs:

- the runner's steps, with their results;
- which agent runs: role, engine, model, read-only or not;
- what the agent says and thinks (thinking: claude only), and the tools it
  calls with their main argument;
- a message passed on to a running claude implementer, as a *user* line,
  once claude has taken it in;
- a subagent's work as one folded row with its name, model and tokens, which
  opens to its own lines;
- with `spec.agents.logPrompts: true` in the project, every prompt the
  agent gets (see [Agents and models](../reference/agents-and-models.md)).

The lines are what the containers logged, shown as text. A pod stops about
a minute after its run; then the tab is empty, and the run log is on the
merge request. A project in a namespace the chart does not list in
`agent.namespaces` has no transcripts.

## Research

With the research database and a research rule, the rail (and on a phone the
bottom bar) has *Research*: one card per research with its view (news flash
or report), schedule or *one-off*, last and next run, and agent. A research's
page shows its prompt, feeds and latest result: a news flash groups items by
topic, important and new ones first; a report shows its blocks in order. *Run
history* lists the stored runs with their cost. With `research: write`, *New
research* starts a one-off research. More in [Research](research.md).

## Watch progress in GitLab

The operator assigns its bot user to an issue it picks up, so GitLab lists it
as **Ongoing** (see [`workflow.assignBot`](../reference/agentproject.md#specworkflow)),
and the agent's merge requests carry the issue's milestone.

## What the login protects

The Desk runs apart from the operator, with its own account: it reads
projects, tasks and agent pod logs, and never holds the operator's GitLab
token, the agent credentials or any Secret. It changes no task itself; it
steers by comments and chat messages the operator checks, and writes to
GitLab only in your name, with your token and the `steer` grant. Agent text
is shown as text with safe markdown (no HTML, no images, `http(s)` links
only). The Desk runs over https only; your session lives in encrypted
cookies. More in [Security](../concepts/security.md#what-the-desk-login-protects).

## Next

- [Steer from the Desk](steer-from-the-desk.md): chat, prompts, Revisit,
  pause cards, the queue and the planner.
- [Order milestones and read the timeline](milestone-order-and-timeline.md).
- [Turn on the Desk](../getting-started/turn-on-the-desk.md): set it up.
- [Desk API](../reference/desk-api.md): the same data for scripts.
