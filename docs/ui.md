# Web UI

A web UI shows what the agents are doing as a **Desk**: a dashboard of
the milestones in work and done across your projects, and one stream per
project, newest at the top, that mixes the conversation (comments, the
pilot's replies and proposals) with what the agents did (runs, pushes, CI,
reviews, merges, autopilot changes), with the message box pinned above it.
With a research database the streams reach back through the
[history](#desk-and-history); the boards, task pages and autopilot queues stay
one click away. With a [research database](research.md) it also
shows researches: news flashes, reports and their run history, and starts
one-off researches. Pages update live. Users log in with OpenID Connect
(Zitadel, Keycloak, Dex, Entra ID, …), and their roles decide which projects
and researches they see.

The UI is off by default. It is a separate Deployment (`<release>-ui`, binary
`/ui` in the operator image) with its own service account that can only read
`AgentProjects` and `AgentTasks` (and `AgentResearches`, see
[Security](#security)). It never sees the GitLab tokens or the agent
credentials. Everything it shows comes from the resources' status, and links
lead to GitLab; only with your GitLab account connected (see
[Steering](#steering)) does it read comments and post them, the milestone
chat included, with your own GitLab token.

## Desk and history

The app is a **Desk**: a left rail, the stream, and, on project pages, a context panel.

* **Rail:** *Inbox* (what needs you now across your projects: tasks with
  questions, failed or blocked, the open prompts (and proposals from before prompts), rejected pilot status writes (*Pilot write rejected*, with the API error, for 24 hours), companion merge requests ready for the owners to merge (*Companion ready · #N* with the companion `project!iid` linking the merge request in GitLab and the release in the reason; the row opens the home issue's task page), and paused
  autopilots as the compact pause card, see below, which opens the
  milestone's *Pilot* tab, as the strip's links, a pause, a proposal and a
  milestone's prompt do; the count also shows in the tab title as `(n) Desk`; see
  [Notifications](#notifications)). While any autopilot you may see is
  paused, the Inbox opens with a strip *N autopilots paused, longest
  &lt;duration&gt;* across projects, with a link to each milestone and how long
  it has been paused (longest first; from `status.autopilot.pausedAt`, a
  pause without a start counts but shows no time). *Overview*
  (the dashboard, see below), your projects with a status dot
  (running, needs you, idle), a one-line state and a needs-you count, the
  researches (with the research section) and the engine usage. On narrow
  screens the rail is a drawer behind the menu button.
* **Dashboard** (`/`): the tabs *Overview* (the default) and *Stream*
  (`/?tab=stream`, the *Everything* stream of all your projects, as
  before; the old name `everything` redirects there, at `/everything`).
  *Overview* shows, top to bottom, the **summary strip**: the milestones
  running, paused and waiting, the needs-you count (linking to the Inbox)
  and, with the research database, the spend of today (the UTC day) and of
  the last 7 days; **Working now**: one card per project with work, the
  issue that holds its run slot (linking to its task page) and for how long,
  what is next, active usage limits and the engine usage; a project with an
  autopilot or queued work and no run says so; **In progress**: one flat
  list of the milestones in work across projects, those that need you
  first, then running, then waiting, then paused and planned; **Recently
  done**: the newest 10 done milestones, *Show all* loads up to 200. A
  milestone row shows the project, the title (linking to the milestone),
  the state, a progress bar with merged (or skipped) of total, the open
  issues, the cost (a dash when unknown, never $0), the tokens and, while
  the retrospective's leftovers are open, *N leftovers*; on narrow screens
  a row stacks. Without the research database a note says that done
  milestones and spend need it, and *Recently done* stays empty.
  It updates live; on narrow screens the strip wraps and the cards stack.
  Counts cover only the projects you may see. A user with research alone
  starts at `/research`.
* **Stream** (the dashboard's *Stream* tab for everything, `/projects/<namespace>/<name>` for a
  project, `…/milestones/<iid>` for a milestone: its issues, its tracking
  issue and what was recorded for it): a header with the view tabs: *Stream*,
  *Board* and *Spend* on a project, *Issues*, *Pilot*, *Retrospective*, *Stream*,
  *Board* and *Spend* on a milestone (the tab is kept in `?tab=`, e.g. `…/milestones/3?tab=board`,
  so it can be linked to and reloaded; the default, *Stream* on a project and
  *Issues* on a milestone, is left out, and old `…/milestones/3?tab=stream`
  links still open the stream; a milestone link with a `#prompt-Q<n>` hash
  and no `?tab=` opens *Pilot*). A milestone's **Issues** tab shows its state,
  progress (merged of total) and cost, then one row per issue: title (linking
  to its issue page), state, merge request, wall time (first run
  to merge, or now), agent time (its calls' durations), paused time (the
  autopilot's pauses over it), cost and tokens; a dash where a figure is
  unknown (no history database, no calls recorded), never $0. On narrow
  screens the rows become cards. Below the view tabs come the scope tabs (the
  project, its milestones; they keep a chosen view tab where the target has
  it and open its default otherwise), a search box, and a strip of the last 30 days' activity, where
  a day jumps the stream to that day. The *Board* tab shows the project's
  boards (one per milestone and the issues without a milestone)
  or the milestone's board; the *Spend* tab what the project or milestone
  cost (see [Spend](#spend)). A project's *Stream* tab starts with the
  project's milestones in the dashboard's lists (*In progress* and
  *Recently done*, without the project column), above the stream. Below
  them, pinned, the **message box** (on a milestone it is on the *Pilot*
  tab instead): it sends to a milestone's pilot (on its
  tracking issue, as `/pilot …`) or comments on an issue, in your GitLab
  name, with `/` suggesting commands; it needs your GitLab account
  connected and the `steer` grant (see [Steering](#steering)), and the
  operator handles what you write like any GitLab comment. While the
  stream's milestone (the scoped one, or on a project stream the one the
  context panel shows; a milestone page pins it on its Stream tab and shows a compact line on Issues) is paused, the compact pause card is pinned above
  the message box, and *Open* leads to the milestone's *Pilot* tab; it goes
  when the autopilot runs again. The *Everything* stream pins nothing.
  Filter chips (*Chat*, *Needs you*, *Runs*, *CI & review*, *Merged*; on a
  milestone only *Runs*, *CI & review* and *Merged*), day dividers,
  and one card per event. Comments on the same issue or merge request
  among the loaded events are one card at the newest comment, read top
  down inside. *Load older* pages back; new events come in live.
* **Issue page** (`…/milestones/<iid>/issues/<issue>`, from a row of the
  *Issues* tab): the issue's number, title, state, links to the GitLab
  issue and its merge request, and its figures as on its row (wall, agent
  and paused time, cost, tokens; a dash where unknown). While a task works
  on the issue, a *Task* section holds what its task page holds (stage,
  banners, facts, questions, chat, conversation, the **Live** panel, agent
  calls, review, CI, autopilot, pod) and *Task page* links to the task's
  URL, which keeps working. Without one a note says why there is no live
  transcript: no task yet, or the task is gone with its pod. Below comes
  the issue's **Stream**, the events recorded for it (`GET /api/v1/stream`
  with `issue=`), as cards with day dividers, *Load older* paging back and
  new events live. It reads the snapshot and history, so a closed
  milestone's issue still opens with its figures and stream after its
  tasks are gone.
* **Which events show where:** every event kind belongs to one of a
  milestone's two event tabs, never both (`pilotKinds` and `runtimeKinds`
  in `internal/ui/stream.go`, mirrored in `web/src/stream.ts`). *Pilot*:
  questions and their answers (`prompt.posted`, `prompt.answered`,
  `prompt.closed`, `needs`), the autopilot and its pauses (`autopilot`,
  `autopilot.paused`, `autopilot.resumed`), what the pilot did
  (`proposal`, `pilot.writeFailed`), comments (`note`), the Desk chat
  (`chat.user`, `chat.agent`) and the `retrospective` (*Retrospective
  posted*, once per milestone, with its number of follow-ups and a link to
  the milestone's *Retrospective* tab). *Stream*:
  runs (`run.started`, `run.finished`), pushes (`push`), CI (`ci`),
  reviews (`review`) and merges (`merged`, `closed`). A project's *Stream*
  tab and the dashboard's still show all of them, and every kind can be
  asked for with `kinds=`.
* **Pilot tab** (`…/milestones/<iid>?tab=pilot`): what matters to the
  milestone's owners, apart from the runtime stream. Top to bottom: the
  full pause card while the autopilot is paused; the milestone's prompts
  (the pilot's autonomy, cost and task, the open prompt cards, the closed
  ones folded under *Earlier questions*); the message box to the pilot (the
  Desk chat, *Ask the pilot*, with the `chat` grant; else, with `steer`,
  the message box posting `/pilot …` on the tracking issue; else read-only:
  *Only the milestone's owners can message the pilot*, with the chat thread
  shown when the `chat` feature is on); the milestone's events of the
  *Pilot* kinds above, newest first, without filter chips; and the pause
  history (*Pauses*). The milestone's *Stream* tab asks only for the
  *Stream* kinds, so the two tabs share no event. A milestone page has no
  context panel (project pages only). A stored milestone's *Pilot* tab shows its history
  only: the events and the pauses, no cards and no message box.
* **Retrospective tab** (`…/milestones/<iid>?tab=retrospective`):
  when the retrospective was posted and a link to its comment on the
  tracking issue; its text, rendered like GitLab renders it but without
  HTML (HTML in it shows as text); the cost drivers (the top issues and
  roles with cost, tokens and their share of the listed ones, by cost, else
  by tokens; issues and roles overlap; only with the research database);
  and the leftovers: number, title, the body folded under *Details*, and a
  state badge (*open*, *opened as #N*, *taken by %N*, *dismissed*) linking
  to the issue or milestone it became. When the pilot could not write one,
  it says why. Without a retrospective: *The retrospective is written when
  the autopilot is done.* A stored milestone shows its stored text and
  leftovers; one posted before the text was stored shows *The text of this
  retrospective is not stored.*
  An owner (`permissions.followUp`) turns leftovers into a **follow-up
  milestone** here: a checkbox on each open leftover, then *Draft follow-up
  milestone*. While the milestone has a pilot (`permissions.draft`) it
  sends the `draft` command to it and shows *The pilot is drafting…* until
  `retrospective.draft` arrives with the live reload (a refusal in the
  pilot's answer or the draft's `error` shows instead); without one (a
  closed milestone) the draft starts from the ticked leftovers' titles and
  bodies (*Follow-ups of <title>*, a section per leftover). A draft the
  pilot wrote earlier is offered as *Edit the pilot's draft*. The editor
  has the title (one line, at most 255 characters) and the description as
  plain text, with a preview by the same markdown renderer: the draft is
  untrusted and never rendered as HTML. *Create milestone and start
  autopilot* (`POST …/followup`) says that autopilot starts at once; on
  success the tab links to the new milestone in the Desk and on GitLab,
  and the leftovers show as *taken by %N* after the operator's next poll
  (until then they cannot be ticked again). A 409 names the leftovers
  already opened, taken or dismissed. On an open milestone under autopilot
  an owner (`permissions.dismiss`) can also **dismiss the remaining
  leftovers**: *Dismiss remaining*, then *Dismiss them* in the confirm
  step, replies `create none` in the retrospective's thread in the owner's
  GitLab name (`POST …/followups/dismiss`); the tab shows *Dismissing… the
  operator settles them on its next poll* until the live reload shows them
  *dismissed*, and the milestone can close without waiting 7 days.
  Everyone else sees the tab read-only.
* **Cost chips** on the stream header (the project or milestone), on each
  milestone scope tab and on the Task page header: `$12.34 · 1.2M tok`, the
  tooltip breaking it down into input, output and cache tokens, calls,
  calls without a known cost and the part from runs recorded before agent
  calls. With the research database they sum the recorded agent calls
  and older runs, so deleted tasks still count; a cost no call reports
  (cursor without pricing) shows *n/a*, never $0. Without the database,
  or until the UI is connected to it, they sum the cost of the current
  tasks, without tokens, as before.
* **Context panel** (wide screens): what needs you in this project, the
  active milestone's queue (per issue its state, merge request, task page
  and decisions; a milestone that waits for its turn to be planned says
  *⏳ Waiting for its turn: %7, %9 go first* with the hint `/agent start`,
  from `autopilot.queuedBehind`. It is running, not paused, so it has no
  pause card and no Inbox item: nothing is asked of anyone), and the last
  30 days: runs per day with the spend,
  merges, spend and events against the 30 days before. The panel is for
  project pages only: a milestone page has none and uses the full width for
  its tabs. What the panel once showed there lives in the tabs: the queue
  (with the *⏳ Waiting for its turn* hint) and the **Companions** in
  *Issues* (with a compact pause line linking to *Pilot* while paused), the
  full **pause card**, the pilot's autonomy and cost, the **questions**
  (prompt cards), **Ask the pilot** and the **pause history** in *Pilot*.
  The milestone API also carries the autopilot's **pause
  history** (`pauses`, from the `autopilot.paused`/`autopilot.resumed`
  events within the history retention: the newest 20 pauses with kind,
  reason, start, end and who resolved each, an open one counting up to
  now, plus totals of all: time paused, per kind, mean time to resume, per
  resolver); it is absent without the history database. Below the
  questions, the **Pauses** card shows it: the totals line (*paused 3h 10m
  in 4 pauses · mean 47m to resume*; the mean once a pause has ended), a
  chip per kind with its count and time, longest first, who resolved them
  (operator, later run, pilot, owner, self-healed), and the newest pauses (five, the
  rest of the 20 behind *Earlier pauses*) with kind, issue, duration, who
  resolved it or *open*, and the reason as text. An open pause keeps
  counting on the Desk between reloads. Without pause history (no history
  database, no pauses yet) there is no card. A milestone whose issues have
  companion merge requests (cross-repo changes) lists them under
  **Companions**: per companion its `project!iid` linking the merge
  request, its chip, the home issue (its task page, else the GitLab issue),
  the release that made the home change live and the operator's reason.
* **Companion chip**: an issue with a companion merge request shows
  *companion: \<chip\>* on its board card, in the milestone queue, on the
  Task page header and on the pause card of its issue: `draft` (the home MR
  is not merged), `waiting for release` (waiting for the release, a rebase
  or CI), `ready`, `merging` (autopilot merges it) or `merged`; a blocked or
  closed companion keeps its chip with *· blocked* (red) or *· closed*. The
  tooltip names the merge request and the reason. The chip links the
  companion merge request everywhere but on the board card, which is a link
  to the task page itself.
* **Prompt card**: one question to the milestone's owners (`Q<n>`): the
  question, its context (markdown without HTML: untrusted), the options
  with the recommended one first, a detail line and what each one does, a
  text box when the prompt takes free text, and once closed who answered
  with which option, when and where (Desk or GitLab), or that it was
  superseded or expired. One click on an option sends
  `{"cmd":"answer","prompt":"Q<n>","option":"A"}` to the prompt's target
  task; the text box sends `{"cmd":"answer","prompt":"Q<n>","text":"…"}`
  (at most 4000 bytes). On an action prompt the pilot turns the text into a
  new option marked *from your answer*, which you confirm with a click;
  nothing runs from the text itself. The operator's reply shows under the
  card. Others see the card read-only: "Only the milestone's owners can
  answer." The card's anchor is `#prompt-Q<n>`, so the Desk link in the
  prompt's GitLab comment scrolls to it (a milestone's prompt on its
  *Pilot* tab, which such a link opens). The compact card shows the
  question and the recommended option.
* **Where prompts appear:** the Inbox (kind `prompt`, counted like the rest,
  with the tab title and the browser notification), the milestone's *Pilot*
  tab (full cards, replacing the old proposals list), the
  stream (compact cards), the Task page (an agent's questions above the chat) and the
  pause cards of `mainRed` and `budget` pauses.
* **Revisit:** each decision row in the queue has a **Revisit** button. It
  opens a `revisit` prompt on the decision's issue: the agent's choice and
  the alternatives it recorded, as options, and a text box. See
  [Revisiting after the merge](usage.md#revisiting-after-the-merge).
* **Prompts in the stream and on the Task page**: a `prompt.posted` event
  is the prompt's compact card (question, the recommended option as a
  button, "more…") while the milestone view still has the prompt; if status
  dropped it, only the summary line shows. `prompt.answered` and
  `prompt.closed` are one-line events. The bot's mirror comments
  (`ai-operator:prompt`, and its answer reply) are hidden from the note
  threads, so a prompt shows once. On a task page the agent's open
  `needsInput` prompts (and the newest closed one) are full cards above the
  chat.

The Desk has no projects list or separate board and milestone pages. The former board page (`/projects/<namespace>/<name>/board`) and milestone page
(`…/milestones/<iid>/detail`) redirect to the project stream's Board tab and
the milestone stream's *Stream* tab; task and research pages keep their URLs.
With the research database, a closed milestone whose tasks are gone still
opens from history: the milestone API answers from the snapshot (`stored`),
with its issues and their figures, and the *Issues* tab says *From history:
the tasks of this milestone are gone*. Such a milestone has no *Board* tab and
no message box. Milestones closed before the snapshot existed are filled in
once by the operator's backfill (see
[configuration](configuration.md#research-database)): their issues with
states and merge requests and the retrospective's text, but no leftovers
and only the spend `agent_calls` still holds, so the oldest show a dash,
never $0. Without the research database the dashboard, the lists and the
milestone pages show only what the live tasks give: no done milestones once
their tasks are gone, no spend in the strip and no per-issue figures.

### Navigation

Every view has breadcrumbs at the top, built from its URL: *Overview* ›
project › milestone › issue, and *Research* › research › *Runs* › run. A
task page sits under its issue page (milestone and issue from the task) and
ends in the task's name, a task without an issue under the project. The Inbox, the research list and
*Not found* show one crumb. Every crumb but the last is a link to that
level's default tab (the `?tab=` stays out); the last one names the
current view. Crumbs show titles once the view has loaded them, before
that the milestone's `%<iid>`, the issue's `#<iid>` or the task's name;
they cost no extra request. On narrow screens the middle crumbs fold
behind `…`, the first and last stay. The Spend tab keeps its own
drill-down crumbs below the header.

### Spend

The *Spend* tab of a project or milestone stream shows what its agent
calls cost, from the research database (`GET /api/v1/spend`; without it
the tab says it needs the database):

* **Totals:** the cost in USD (*n/a* when no call reports or estimates
  one, never $0; *+N calls without cost* when some calls have none, they
  are not in the sum; *N calls not metered* for the calls that report
  neither a cost nor tokens, such as cursor with model `auto`, whose ACP
  session reports no usage), input, output, cache read and cache write tokens,
  calls (and subagent calls), duration and the cache-read share (cache
  reads of all input tokens). Runs recorded before agent calls add their
  cost without tokens: *includes $X from runs before token tracking*.
  A call's tokens include its subagents (the sum of its models), so the
  totals, the drill-down and the breakdowns add up to the same tokens.
  Calls recorded before that count the main thread only. A Desk chat turn
  counts only that turn's tokens, models and cost; turns recorded before
  that counted all earlier turns of their chat session again.
* **Drill-down:** a table one level down, with calls, tokens, cost and a
  share bar per row: a project's milestones, a milestone's issues (and
  its own spec, pilot and retro calls), an issue's runs, a run's calls.
  Clicking a row goes one level down; a project's milestone opens the
  milestone stream's Spend tab. The position is kept in the query
  (`?tab=spend&spendIssue=77&spendTask=…&spendRun=2`), so it can be
  linked to and back goes up again; breadcrumbs lead back to the project.
  Calls of no milestone and a milestone's own calls are not split further.
  An issue with a companion repository has a *companion* marker on its row
  (the tooltip names the project): its calls edit both trees and count to
  the home issue.
* **Calls** (a run's): seq, time, kind, role, engine and model (with the
  effort), whether the session was resumed, the engine it fell back from,
  tokens, cost and its source: *reported* by the engine, *estimated*
  from tokens and the `pricing:` table (shown as `~$0.42`), or *n/a*
  (cursor without pricing). A call with neither a cost nor tokens shows
  *not metered* as its cost and *—* as its tokens.
* **Model bars:** every row of the drill-down (milestones, issues, runs
  and calls) has a stacked bar split by model: the main model first (in
  the progress color), the subagent models after it, each in its own color
  (named in the legend above the table, a model keeps its color in every
  row). Hovering a segment shows the model, its tokens and its cost. The
  bars of a table share one scale, the largest row full width, so rows can
  be compared (e.g. rising step costs). *tokens | USD* above the table
  switches the unit, kept in the query (`?spendUnit=usd`, tokens by
  default) while drilling down. In USD, a row without a known cost draws
  a faint token bar labelled *n/a* (never a $0 bar, on the scale of the
  other token bars), a model without a known cost is left out of the bar
  (named in its tooltip), and an estimated cost is marked `~`. Rows from
  before token tracking have no bar.
* **Breakdowns** by kind, role and model (subagent models as their own),
  highest cost first, each row with its share bar. A row whose calls all
  report neither a cost nor tokens (cursor's model `auto`) stays a row and
  shows *not metered* instead of *n/a* and 0 tokens; in the drill-down's
  model list such a model is marked *(not metered)*.
* **Tuning tables**, each with the average cost per call (of the calls
  with a known cost; *n/a* when none has one), to set the levers in
  [configuration.md](configuration.md#roles) by:
  * *Review rounds:* the review calls by round. Within a run, round 1 is
    the reviews before the first fix call, round 2 those before the next,
    and so on; the rounds are summed across the scope's runs. A high
    round-1 cost on big changes points at the reviewers'
    `maxInlineDiff`; many rounds at the fixes, not the cap.
  * *Implementer steps: resumed vs fresh:* implement calls by whether
    their session was resumed, with tokens and cost per call
    (`implement.stepSession`).
    * *Step commits* (below the table): the scope's implement calls that
      left lines (their count) and their lines, added plus deleted:
      median, p90 and max, so you can see whether `implement.maxStepLines`
      keeps the steps small, and the step-size gate's nudges in the
      scope (`stepLines` of the API). Hidden without step data (no
      implement call left lines and the gate never nudged).
  * *With vs without subagents:* per role where a subagent ran, the calls
    that ran one (`subagentCalls` above 0) against those that did not,
    with tokens and cost per call and the subagent models' share of the
    tokens (`subagents`). Roles where no subagent ran are left out.
    * *Gate blocks per call* (same table): the read gate's denials
      (`readGateBlocks`) divided by the role's calls, one decimal; empty
      without a block, so projects without a `readGate` see an empty
      column. Many blocks mean the role keeps trying to read whole
      files: tighten the scout's prompt or raise `minLines` if the gate
      blocks reads that are fine.

Shares are of the rows' known cost; a row without a known cost shows
*n/a* there. When no row has a known cost they are shares of the tokens.
The Spend tab loads when it opens or the drill-down moves; it does not
follow live changes.

### Agent calls on the Task page

A Task page lists every recorded call of its task in an *Agent calls*
section, from the same endpoint (`task=`): the task's totals on top, then
its runs newest first (calls, tokens, cost). Expanding a run loads its
calls (`run=`): time, kind, role, engine and model, resumed, input,
output, cache read and cache write tokens, duration and cost (`~` for an
estimate, *n/a* when unknown). A call the read gate blocked shows
`· N gate blocks` after its model. *Lines* shows `+A/−D` for implementer
calls (kinds implement, fix, ci and conflict): the uncommitted diff the
call left in the work tree when it ended (`git diff --numstat HEAD` plus
untracked text files, binary files not counted), which is what the step
commit after it holds. Other calls show `—`. A call the step-size gate
nudged (`implement.maxStepLines`) shows `· N step-gate nudges` after its
lines. A call whose end saved a
checkpoint commit (it was cut off by a usage limit, an engine error or
timeout, or an agent container restart) gets a 💾 badge with the reason,
e.g. *💾 usage-limit*; hovering it shows the commit. The totals on top
count the task's checkpoints with their reasons (*💾 3 checkpoints:
usage-limit ×2, engine-error*). Checkpoints that no call ended in, after a
budget stop or saved by the next pod after a lost one, are not counted
there; the run report names them. The calls come from the database, so
runs whose CRD state was trimmed are listed too. Without the research
database (404 or 503) the section is left out.

Above an expanded run's calls a **context-growth chart** shows how the
session's context grew: the turn on x, the context tokens (input + cache)
of each main-thread turn on y, one line per implementer step (calls of
kind implement, ci, conflict and fix; reviewers, describe and pilot calls
are left out). All steps share one scale, so a resumed session's later
steps start and end higher. The lines share one color, fainter for the
first step and full for the latest, and the legend names each by seq and
kind (*#3 implement*). An orange dot marks each turn after which a
subagent ran. Hovering a dot or a line's end shows the turn, the tokens
and the step. A series is at most 500 points; a thinned one is spread
over the call's real turn count, so long sessions keep their length on
the axis. Runs recorded before context series were tracked (and cursor
calls) show no chart.

### Notifications

The Inbox header has a **Notify me** button. It asks the browser for
permission and, once you allow it, shows *Notifications on* (click it to turn
them off). The choice is remembered per browser; they are off until you turn
them on. While they are on, a new Inbox item raises one browser notification:
a paused autopilot (*Autopilot paused · milestone*, with the cause and the
reason), a prompt (*Q5 · project*; a proposal "P3" from before prompts shows as before), or a task with a
question (*#12 needs input*), or a companion merge request ready for the
owners to merge (*Companion ready to merge · project*, with the merge
request, the home issue and the release). Failed and blocked tasks only count in the
Inbox. Items already there when the tab opens or when you turn notifications
on don't notify; each item notifies once, also across reloads and live
updates, and an autopilot that pauses again for another cause or issue is a
new item. Clicking a notification opens the milestone's *Pilot* tab (pause,
proposal, a milestone's prompt) or the task page (a ready companion: its home issue's task). Notifications work only while a Desk tab is open: there is
no service worker, no Web Push and no e-mail. When the browser blocks them,
the header says *Notifications are blocked in this browser* (allow them in
the site settings); a browser without the Notification API shows no button.

**History.** The operator appends the factory's events to the research
database (configured with `research.database`, see
[configuration](configuration.md#research-database)): changes of the tasks'
status (a run started or finished with its result, spend and duration, a
push, a final CI result or a fix round, a review with its blocking findings,
a question, failure or block, a merge, autopilot phase changes, the pilot's
proposals posted, accepted or closed, a task closed) and every comment the
operator reads on issues and merge requests, its own included. Each event
is written once; events are kept for `research.historyRetention` (a year by
default). Without a research database the Desk shows the live state only
and says that the history needs the database. What happened while the
operator was down is not recorded (comments are caught up when it reads
them again), and an edited comment keeps its first version.

The bodies of comments are mirrored from GitLab, so they are a grant of
their own: a rule with `conversation: true` shows them (and searches them);
without it a comment shows as who commented on which issue, with its link.

## Platform

The UI is a platform with sections, which the app takes from
`GET /api/v1/features`: **Factory** (projects, milestones and tasks, at `/`
and `/projects/…`) and, with a [research database](configuration.md#research-database)
and a [research rule](#research-rules), **Research** (at `/research`). With
more than one section the top bar shows them as tabs, the active one
following the page; with the factory alone the bar has no tabs and the UI
looks as it always did. `/research` and the pages below it are *Not found*
for users without the research section. A user with research alone starts
on `/research`.

The **Research** page lists the researches you may read, one card each:
name and namespace, view (news flash or report), depth and phase; the
schedule with its time zone (or *one-off* and who started it); the last and
next run; the run line of the current or last run (`claude/claude-sonnet-5-5
· 3 min · 07:00`, the outcome when it was not a success, the choice reason on
hover); and, when no agent was free, *waiting for `claude` until 14:00* with
the skip reason. The page updates live on research events.

A research's page (`/research/<namespace>/<name>`) has a header with the
prompt (the first lines, *Show the whole prompt* for the rest), the schedule
or *one-off*, the agent (`auto` or the fixed `engine/model`), the last and
next run, a *Feeds* line of the configured source links when `spec.sources`
is set, the waiting line and the error of a failed last run. Below it is
the latest result, with its title, run line (linked to the run's page) and
summary:

* **News flash** (`view: newsflash`): the items of all items blocks grouped
  by topic, the research's `topics` in their order first, then the other
  topics alphabetically, then *Other* for items without one. Within a topic
  the important items come first, then the newest. An item first seen in
  this run (its URL in no earlier run that is still kept) gets a *new*
  badge. The other blocks follow in their order.
* **Report** (`view: report`): all blocks in order.

A run's page (`/research/<namespace>/<name>/runs/<id>`) shows its status
(*degraded* when the answer failed validation and is kept as plain text),
error, warnings, agent, start, duration, spend, retention and choice reason,
and its result in the research's view. Both pages update live.

The **run history** (`/research/<namespace>/<name>/runs`, *Run history* in a
research's header) lists the stored runs, newest first, 20 at a time with
*Load older*: start (linked to the run's page), duration, status, agent,
choice reason, cost and error. Runs that expired are gone from it.

Full-text search over the stored results is in the API
(`GET /api/v1/research/search`, see [API](#api)); the app has no search page
yet.

**One-off research.** With the research write grant (`write` in
`/api/v1/features`) the Research page has a *New research* button, which
opens the form at `/research/new`; without it there is no button and
`/research/new` is *Not found*. The form takes the question (with a
character counter up to 16384), the depth (*quick*, up to 20 minutes, or
*deep*, up to 2 hours), the view (*Report* by default), topics separated by
commas, the project when there is more than one (projects without a writer
login are disabled, with a hint) and the agent: *auto* or one of the
project's allowed models, with an effort for claude. The server checks
everything again; its errors show next to their field. A started research
opens its page, which follows the run live on research events: *Starting*
until the operator takes it, *Waiting* (*waiting for `claude` until 14:00*
and the reason) while no agent is free, *Running* with the agent, the choice
reason and the elapsed time, and then the report, without a reload. A
one-off you started has a *Delete* button (with a confirmation), which
removes the research and goes back to the list; its stored results stay
until they expire. See [One-off researches](#one-off-researches) for the
rules.

**Blocks.** Each block type has its own component: headings; markdown;
items (title linked to the source, source, publication time, *important*
for importance 3 and *minor* for 1, *new*); tables; stats (a delta starting
with `+` is green, with `-` red); and links. Types the UI does not know are
skipped. Agent output is text: markdown is rendered by the UI's own subset
renderer (`web/src/markdown.ts`) straight into page elements, with no HTML
in between. It knows paragraphs, headings, lists and task lists,
blockquotes, tables, rules, `<details>`/`<summary>`, emphasis,
strikethrough, code, entities, the attribute-free inline tags `code`, `kbd`,
`sub`, `sup`, `b`, `strong`, `i`, `em`, `s`, `del` and `br`, and links; HTML
comments are dropped. Everything else, other raw HTML and any tag with
attributes included, shows as typed. Links
only go to `http(s)` URLs, open in a new tab with `rel="noopener noreferrer
nofollow"`, and other links show as their text. Images are never loaded.

## What it shows

| Page | Content |
|------|---------|
| **Task** | stage, phase, current step, runs, last result and its summary, spend, branch and last push; review findings; pipeline, fix rounds and failed jobs; out-of-memory restarts; usage limit deferrals; the pod and how to follow its logs. Spec tasks add the plan with dependencies and the autopilot's phase, pause reason and a link to the milestone stream, whose *Issues* tab shows the queue |

**Engine usage.** claude reports how much of its usage windows is used with
every agent call; the project keeps the freshest reading per engine
(`status.engineUsage`). The Board tab shows it as one line
per engine, e.g. `claude: 5 h 29 % (resets 19:20), 7 days 18 % (resets Tue
07:00) as of 15:27`, with times in your time zone (the weekday when not
today, the date beyond a week). A window at 80 % or more is highlighted. A
window whose reset time has passed was reset since the reading: it is
greyed out as `stale, reset 14:00` until the next agent call brings a new
one. cursor reports no usage, so it shows only an active limit, as a badge
with its reset (`cursor limited until 13 Nov`).

**Chat with the agent.** When Desk chat is configured (`ui.chat.writerSecret`)
and your roles have the `chat` grant (`permissions.chat` on the task, or the
`chat` feature), a task page has a **Chat with the agent** section: the
task's Desk thread (oldest at top), a composer that posts without a GitLab
token, and an Interrupt while a delivered turn has no reply and the agent
pod is answering. Sends are retry-safe (`clientKey` in session storage until
a 2xx). Bodies need the `conversation` grant; without it they show as
*message hidden*. While a message waits, the status line is honest about
the task: the task's own message when it is Queued, Pending or Blocked
(e.g. waiting for capacity), *starting the agent…* while the pod comes up,
and *the agent is answering…* only once the task is Running or Idle with a
pod (with a link to the Live transcript when you may see it). With the
`chat` grant the composer also shows command buttons by task type:
Implement has Retry, Review again and a Model… form (`use` / `effort`);
a Spec task with autopilot has Pause or Resume and Skip…; a Coordinate
task has Accept/Reject per open proposal. Commands post as
`kind: "command"` with a JSON body and render as a short label in the
thread (operator answers nest under them as system replies). Each
command goes to the task that owns it: `retry` / `review` / `use` /
`effort` on an Implement task, `pause` / `resume` / `skip` on an autopilot
Spec task, and `accept` / `reject`, `answer`, `revisit` and `draft` on the
milestone's Coordinate (pilot) task (author must be in the milestone's
`owner-subjects`); `answer` to the agent's questions (`needsInput`
prompts) goes on its Implement task, same owners. The prompt commands:

| Command | Effect |
|---------|--------|
| `{"cmd":"answer","prompt":"Q5","option":"B"}` | Choose option B of prompt Q5: its actions run (checked again by the operator). |
| `{"cmd":"answer","prompt":"Q5","text":"…"}` | Your own words (at most 4000 bytes). On an action prompt the pilot adds the option "From your answer: …" to confirm; on an agent's question the text is the answer. |
| `{"cmd":"revisit","decision":"D42-3","text":"…"}` | Revisit a decision with that instruction. Without `text`: opens the `revisit` prompt. |
| `{"cmd":"draft","followUps":[1,3]}` | Ask the pilot of a done milestone for a draft of a follow-up milestone from those leftovers (1 to 9); it lands in `retrospective.draft`, untrusted until an owner confirms it. Refused while a draft is being written, for numbers that are unknown, opened or taken, and once the leftovers are settled (the pilot is gone then). See [commands](commands.md#on-the-tracking-issue-autopilot). |

An explicit prompt id is always required, and a prompt takes one answer.
Chat events
appear in the Desk stream when the `chat` feature is on (keyed apart from
history events).

With GitLab connected (see [Steering](#steering)), a task page has a
**Conversation** panel: the comments of its issue, merge request and
tracking issue, and, with the `steer` permission, a box to write comments in
your own GitLab name.

A milestone's *Pilot* tab has the same panel as its **Chat**: the comments of the
milestone's tracking issue and, with `steer`, a box whose messages go to the
pilot as `/pilot …` (the UI adds the prefix). Only the milestone owners'
instructions count, as in GitLab; answer a proposal "P<n>" from before
prompts with `yes P<n>`.

With the `transcript` permission, a task page also has a **Live** panel: the
running pod's steps and the agent's session as they happen (see
[Live transcript](#live-transcript)).

On the Board tab, an autopilot that paused over an
issue's run, review or CI and is **waiting** for a run that is working on
that issue now shows as *Autopilot waiting for #N*: it continues by itself
once the issue is reviewed and green. A pause that needs a human shows as
*Autopilot paused*. On the Board tab, each paused milestone's section starts
with the **compact pause card**: the heading for the `pauseKind`, the pause
reason on one line (the free text for a pause without a kind), the paused
issue, *A run is working on #N now* while one is, what ends the pause by
itself, and *Open* to the milestone's *Pilot* tab. It has no buttons; the
actions are on the full card there. The Inbox, the stream and a milestone's
*Issues* tab show the same compact card. In the stream and on the *Issues*
tab, where the card has the pause's exits, the line on
what ends the pause becomes the short **Way out**: the first automatic exit
with its status and the number of remedies, e.g. *Way out: A later run makes
the issue pass the merge gate (reviewed and green) — waiting · 5 remedies*.
While a run works on a task, its card marks the CI result as the last
push's.

On the milestone's *Pilot* tab (and as a compact line on its *Issues* tab,
which links to Pilot), a paused autopilot (waiting or not) shows as the **pause card**: a heading for
the `pauseKind`, the pause reason as text, the
paused issue with its title, MR and pushed head, what ends the pause by
itself, for failed reviewers which engine is usage-limited until when
(*Cursor is limited until 13 Nov*), *A run is working on #N now* while one
is, and a merge anyway an owner already gave for this head.

Its **Way out** section lists every exit of the pause (`exits` of the
milestone API, from the operator's `PauseExits` table). The automatic exits
come first, each with its condition, what follows, a status chip and the
operator's detail (*main is red at abc1234*): *lifts on the next poll* (the
condition holds), *in progress* (a run pending, a rebase in flight),
*waiting* or *not known*. Then the owner's remedies with what each does: a
remedy the API marks `allowed` for you (`resume`, `skip`, `retry`, `review`
with the chat grant, `merge` with `permissions.merge`) is a button that sends
the same Desk chat command as the buttons below; other commands (`rebase`,
`mergeBase`, `unskip`, `startAnyway`, or any without the grant) show as the
word to send on the tracking issue or the issue; labels, GitLab actions and
answers to the pilot's prompt show as text, with a link to the merge
request, the companion merge request, the issue or the milestone where it
applies. With the section the
card drops its *Continues by itself* line, and its action row keeps only the
links (Open chat, Open pipeline, Open MR, Open companion MR, Open plan): the commands come from
the remedies, so none shows twice.

Against an operator that sends no exits, the actions depend on the kind:

| Kind | Actions |
|------|---------|
| `reviewErrors`, `notReviewed` | Review again, Merge anyway, Skip |
| `findings`, `runFailed`, `needsInput` | Retry, Open #N chat, Skip |
| `ciRed`, `ciCanceled` | Retry, Open pipeline (when there is one), Skip |
| `mergeRequest` | Open MR, Resume, Skip |
| `companion` | Open companion MR, Resume, Skip; it continues by itself once the companion merge request can be merged again or is merged by hand |
| `cycle`, `stuck` | Open plan, Skip (pick a pending or active issue) |
| `owner`, `mainRed`, `config`, and a pause without a kind | Resume |
| `budget` | none: raise the budget above the spend (the milestone's `/agent budget` line, or for a `pausedFor` issue an `/agent budget <usd>` comment on it), and it continues by itself |
| `emptyPlan` | none: change the milestone description, which plans it again; it continues by itself once the plan has issues |

Retry and Review again go to the paused issue's Implement task; Resume,
Skip and Merge anyway to the milestone's Spec task, as Desk chat commands
(see [Chat with the agent](#what-it-shows)). Merge anyway shows only with
`permissions.merge` (an owner subject of the milestone with the chat grant)
and asks first: *Merge \<sha\> without a full review? CI, MR state and
approvals still apply.* It names the head the card shows, so the operator
refuses it after a newer push. The card shows *sent…* after a click, then the
operator's answer (a refusal in its words; *answered* when you may not read
bodies) or why the command failed. Without the chat grant the card keeps the
links and says that commands need Desk chat, or `/agent …` on the tracking
issue.

A task's **stage** is derived from its status the same way the operator moves
the GitLab labels; the hint on each badge explains it. After a push the task
stays in *CI* until the operator has followed that commit's pipeline, for at
most `spec.ci.noPipelineTimeout` plus one poll interval (unless the project
has `spec.ci.disabled`).

## Enable it

You need a host name for the UI, a Gateway (Gateway API) that serves it with
TLS, and an OIDC client.

### 1. Create the OIDC client

Redirect URI: `https://<host>/oauth2/callback`. Post-logout redirect URI:
`https://<host>/`. The UI uses the authorization code flow with PKCE, so a
public client (no secret) works; a confidential client works too.

#### Zitadel

1. Create a project, e.g. `ai-operator`, and add its **roles**, e.g.
   `ai-operator-admin` and `shop-devs`.
2. In the project's settings, turn on **Assert Roles on Authentication**. The
   roles then come with the login (the UI also reads the userinfo endpoint,
   which is where Zitadel puts them unless the application has *User roles
   inside ID Token* set). Optionally turn on **Check authorization on
   Authentication**, so users without a role in the project cannot log in at
   all.
3. Add an application of type **Web**, authentication method **PKCE** (or
   **Code** with a client secret), with the redirect URIs above.
4. Grant the roles to users (*Authorizations*).

Zitadel puts the roles into the claim `urn:zitadel:iam:org:project:roles`, a
map from role to the organizations that granted it. The UI takes a map's keys
as the roles:

```yaml
ui:
  oidc:
    claims:
      username: claims.preferred_username
      roles: 'claims["urn:zitadel:iam:org:project:roles"]'
```

#### Other providers

| Provider | `roles` expression |
|----------|--------------------|
| Dex, Authentik, most others | `has(claims.groups) ? claims.groups : []` (the default) |
| Keycloak, realm roles | `has(claims.realm_access) ? claims.realm_access.roles : []` |
| Keycloak, client roles | `claims.resource_access["ai-operator"].roles` |
| Entra ID, app roles | `has(claims.roles) ? claims.roles : []` |

Add the scope that makes your provider send the claim (e.g. `groups` for Dex)
to `ui.oidc.scopes`.

### 2. Create the Secret

```sh
kubectl -n ai-operator-system create secret generic ai-operator-ui \
  --from-literal=session-key="$(openssl rand -hex 32)" \
  --from-literal=client-secret=…        # confidential clients only
```

The session key encrypts the login cookies. Changing it logs everyone out.

### 3. Set the chart values

```yaml
ui:
  enabled: true
  externalURL: https://ai.example.com
  oidc:
    issuerURL: https://acme.zitadel.cloud
    clientID: "291833245810002@ai-operator"
    existingSecret: ai-operator-ui
    claims:
      username: claims.preferred_username
      roles: 'claims["urn:zitadel:iam:org:project:roles"]'
  authorization:
    rules:
      - roles: [ai-operator-admin]          # every project
      - roles: [shop-devs]
        projects: ["agents/shop"]           # namespace/name of the AgentProject
      - roles: [team-b]
        projects: ["team-b/*"]
  httpRoute:
    enabled: true
    parentRefs:
      - name: public
        namespace: gateway-system
        sectionName: https
  networkPolicy:
    enabled: true
    from:
      - namespaceSelector:
          matchLabels:
            kubernetes.io/metadata.name: gateway-system
```

The HTTPRoute takes its host name from `externalURL` (or
`ui.httpRoute.hostnames`) and forwards the whole host to the UI: the UI is
served at the root of its own host name, not under a path. All values are
listed in the [chart README](../charts/ai-operator/README.md#web-ui).

The UI pod needs to reach the OIDC provider (discovery, keys, token and
userinfo endpoints) and the Kubernetes API.

## Access rules

Nobody sees anything unless a rule grants it. A rule matches a user who has
**any** of its `roles`; `"*"` matches every logged-in user. `projects` are
`namespace/name` patterns with `*` and `?` (path globs, `*` does not cross the
`/`); without `projects` the rule grants every project. A user sees the union
of all matching rules. Seeing a project means seeing all its tasks.

Projects a user may not see do not exist for them: the API answers 404, and
no change events are sent for them.

`steer: true` lets its roles write comments from the UI (see
[Steering](#steering)); GitLab's own permissions apply on top.

`conversation: true` shows its roles the bodies of the comments in the
projects' streams, mirrored from GitLab into the history (see
[Desk and history](#desk-and-history)), and the bodies of Desk chat
messages: readable without access to the GitLab project, so a grant of its
own.

`chat: true` lets its roles post Desk chat messages to the projects' agents
(no GitLab token). Default off; needs `ui.chat.writerSecret` and the
research database. Who may chat is decided by this grant, not by GitLab
membership. Delivery also needs `agents.chat: true` on the AgentProject;
without it the operator marks the row failed.

`transcript: true` on a rule also shows its roles the live transcripts of the
projects' runs. Transcripts can quote code, issue text and CI logs, so they
are a grant of their own:

```yaml
ui:
  authorization:
    rules:
      - roles: [ai-operator-admin]
        transcript: true
        steer: true
        conversation: true
        chat: true
      - roles: [shop-devs]
        projects: ["agents/shop"]       # streams, boards and tasks; no transcripts or comment bodies
```

### Research rules

With a [research database](configuration.md#research-database) the UI also
has a Research section. A rule with `research` set is a **research rule**:
`research: read` lets its roles see the researches and their results,
`research: write` also start one-off researches and delete them (only with
`ui.research.allowCreate`). `researches` are `namespace/name` patterns of
AgentResearches like `projects`; without it the rule grants every research.

Factory and research rules are kept apart: a research rule grants no
projects (it must not set `projects`, `transcript`, `steer`, `conversation`
or `chat`), and a project rule grants no research, even without `projects`.
So the admin rule above does not show the Research section; add a research
rule for it:

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
      - roles: [news-readers]           # only the news flashes, no factory
        research: read
        researches: ["agents/news-*"]
```

`GET /api/v1/features` tells the app which sections to show: `factory` when
a project rule matches the user, `research` when a research rule matches and
a database is configured. Without login (`auth.mode: none`) everyone sees
both and may write with `ui.research.allowCreate`.

#### One-off researches

With `ui.research.allowCreate`, users with `research: write` start one-off
researches (no schedule) from the UI and delete their own:

- **Namespace.** The UI creates them in `ui.research.namespace`, never
  elsewhere: the request has no namespace field. The write rule must match
  the new research there (e.g. `researches: ["agents/*"]` or
  `["agents/q-*"]`); the chart grants the UI `create`, `delete` and `get` on
  AgentResearches in that namespace only.
- **Names** are chosen by the UI: `q-<yyyymmdd>-<6 random characters>`.
- **Creator.** The UI stamps `ai.wiemers.me/created-by: <user name>`. Only
  that user may delete it, and only one-offs in `ui.research.namespace`;
  scheduled researches are managed with `kubectl`. Deleting removes the
  AgentResearch and its pod; the stored results stay until their retention
  ends.
- **Projects.** A one-off runs with an AgentProject of `ui.research.namespace`
  that has `spec.research.writerSecret` (the runs' database login). The form
  lists the others as unavailable, and starting one with them is refused.
- **Agent.** Automatic (the [candidates](research.md#choosing-agents)) or one
  of the project's allowed models, checked like a research's `spec.agent`.
- **Limits.** The request is at most 32 KiB: a question of 1 to 16384
  characters and at most 20 topics of up to 100 characters. A user may start
  10 researches per hour; more answer 429 with `Retry-After`.
- **Audit.** Each create and delete logs a line
  `"msg"="audit" "action"="research.create"` (or `research.delete`) with the
  user, namespace, name, depth and agent, never the question.

Without `allowCreate` the UI has no client that writes, the chart renders no
write Role, and the endpoints answer 404.

Roles are read at login and kept in the session, so a role change takes
effect at the next login, at the latest after `ui.auth.sessionTTL` (default
`12h`). A user without any matching role gets an empty project list that
names their roles, which helps to debug the claim mapping.

### Claim mapping

`ui.oidc.claims.username` and `ui.oidc.claims.roles` are
[CEL](https://cel.dev) expressions over the variable `claims`: the ID
token's claims, plus the claims only the userinfo endpoint returns (the ID
token wins where both have one). `username` must give a string. `roles` may
give a list of strings, a single string or a map whose keys are the roles. If
the roles expression fails because the claim is missing, the user has no
roles; a username that cannot be evaluated fails the login (the UI's log
says why).

## Live transcript

The **Live** panel on a task page follows the task's pod while it runs:

- the runner's steps (started, done, failed, with their results);
- which agent runs (role, engine, model, read-only);
- what the agent says and thinks (thinking: claude only), and the tools it
  calls with their main argument (command, file, pattern);
- a subagent's work (claude's Task/Agent tool) as one collapsed row
  *subagent · name · model · tokens* under the tool call that started it.
  It opens to the subagent's own thinks, says and tool lines. The tokens
  sum input, output and cache tokens over the subagent's messages seen so
  far and grow while it runs. Lines of a subagent started by a subagent
  show unfolded;
- with `spec.agents.logPrompts: true` on the AgentProject, every prompt:
  the role, the system prompt (with the project's instructions) and the
  prompt, each cut at 64 KiB. That includes what autopilot, CI fix runs,
  revisits and merge conflicts add to it.

The UI reads the pod's logs through the Kubernetes API, and passes on only
known kinds of lines of the current run, with a fixed set of fields, never a
raw log line. If a container restarts (out of memory), its log is followed
again. A pod stops a minute after its run (`lifecycle.idleTimeout`); after
that the panel is empty, and the run log of the last run is on the merge
request. Each new run opens a new stream.

The lines are what the containers logged, not signed: anything running in
the agent container could print lines that look like entries. They are shown
as text only, never as HTML.

The chart lets the UI's service account read `pods` and `pods/log` in each
namespace of `agent.namespaces` (Roles, nothing cluster-wide). An
AgentProject in a namespace that is not listed there has no transcripts:
its streams end with "pod not available".

## Steering

GitLab stays the place of record: comments written in the UI are GitLab
comments in **your** name, and the operator handles them like any other. A
plain comment reaches a running agent at its next step; `/agent …` starts a
run; `/agent use …` and `/agent effort …` change model and effort; on an
autopilot milestone's tracking issue, `/agent pause|resume|skip`. All
commands: [commands.md](commands.md).

### Set it up

1. Create an OAuth application on the GitLab your projects live on (for
   gitlab.com: *Group → Settings → Applications*, or *User settings →
   Applications*): **confidential**, scope **`api`** (GitLab has no narrower
   scope that may post comments), redirect URI
   `https://<host>/oauth2/gitlab/callback`.
2. Put its secret into the UI's Secret under `gitlab-client-secret` (or into
   another Secret named by `ui.gitlab.existingSecret`).
3. Set the chart values:

   ```yaml
   ui:
     gitlab:
       url: https://gitlab.com
       clientID: "<application id>"
     authorization:
       rules:
         - roles: [ai-operator-admin]
           steer: true
   ```

Each user then connects their account once with **Connect GitLab** on a
task page.

### One user, a fixed token

For a single user, the UI can use a fixed token of that user instead of an
OAuth application: a **fine-grained personal access token** (GitLab 19.2 and
later) limited to the group with your projects:

| Boundary | Permissions |
|----------|-------------|
| group (selected membership) | `read_project`, `read_work_item`, `create_work_item` (comments on issues and MRs are work item actions; it also allows creating issues), `read_merge_request` |
| user | `read_user` |

Put it into the Secret under `gitlab-token` and name its owner's UI user name
(what `oidc.claims.username` gives):

```yaml
ui:
  gitlab:
    url: https://gitlab.com
    tokenUser: ada@example.com
```

That user is connected without OAuth; nobody else may use the token (other
users can still connect their own accounts if `clientID` is set too). The
token expires (365 days at most) and must be renewed; a rejected token shows
as an error, not as a reconnect.

### How it works

- **Thread:** the task's issue, merge request and (on autopilot) tracking
  issue comments, read with your token, so GitLab decides what you see. The
  page looks again every 30 seconds and after every change of the task.
- **Comments:** posted with your token to the issue, MR or tracking issue
  you pick, at most 10 000 characters and 10 per minute. The buttons above
  the box start a comment with the common commands. The UI logs who posted
  where (user, GitLab user, project, target, note id), never the text.
- **Milestone chat:** a message to the milestone's pilot goes to its
  tracking issue with your token, under the same grant and limits. The UI
  puts the project's pilot prefix (`workflow.pilotPrefix`, `/pilot`) in
  front unless the message starts with it, so `yes P3` arrives as
  `/pilot yes P3` (and `/agent pause` as `/pilot /agent pause`: post
  commands in GitLab). Whether it counts as an owner's instruction is the
  operator's decision, as for any comment in GitLab.
- **Follow-up milestone:** an owner (one of the source milestone's
  `owner-subjects`) with the `steer` grant creates it with your token on the
  *Retrospective* tab (*Create milestone and start autopilot*) from
  the leftovers still open, an open or a closed milestone alike: your title
  (one line, at most 255 characters) and description (at most 64 KiB), a
  blank line, then the project's spec and autopilot markers and the
  source's `owners`, `owner-subjects` and `followup-of` lines (those three
  are dropped from your text). Creating it starts autopilot. The operator
  marks the leftovers taken on its next poll; the UI writes nothing to the
  database. Within the comment rate limit; the UI logs who created which
  milestone from which, never the text. The fixed token needs to be allowed
  to create milestones (the project's Planner or Reporter role).
- **Tokens:** the access and refresh token live in an encrypted, `HttpOnly`
  cookie (like the login), bound to your UI user, refreshed when the access
  token runs out, never readable by the page's JavaScript. The connection
  ends 30 days after you made it, however often it was refreshed.
  **Disconnect** forgets them; revoke the application in GitLab to end it
  there too.
- **Targets are checked:** a comment goes to an MR only if its source branch
  is the task issue's work branch, and to a tracking issue only if it is the
  operator's tracking issue of the task's milestone (the task status, which
  holds their numbers, is written by the agent pods too).
- The rate limit counts per UI replica. The `gitlab-client-secret` key must
  exist in the Secret, or the UI pod does not start.
- Projects on another GitLab host than `ui.gitlab.url` get no thread and no
  box.

## Without login

For a quick look, or a cluster without an OIDC provider:

```yaml
ui:
  enabled: true
  auth:
    mode: none
```

```sh
kubectl -n ai-operator-system port-forward deploy/ai-operator-ui 9080
# open http://127.0.0.1:9080
```

The UI then listens on `127.0.0.1` inside its pod only, so only
`kubectl port-forward` reaches it, and everyone who can port-forward to it sees
every project. The chart creates no Service in this mode and refuses to
combine it with `ui.httpRoute`; the binary itself refuses `none` on any
address but a loopback one, and answers only requests for `localhost`,
`127.0.0.1` or `[::1]`, so a web page cannot reach it through a DNS name that
points to 127.0.0.1 (DNS rebinding).

## Security

- **Separate, read-only identity.** The UI's service account may only `get`,
  `list` and `watch` AgentProjects and AgentTasks (with a research database
  also AgentResearches), and read pods and their logs in the agent
  namespaces (for transcripts). No Secrets, no GitLab token; the research
  database only with the read-only reader login, which the chart passes
  from its Secret as environment variables. The API returns views of the
  resources without the
  operator's bookkeeping and secret references. Its only write access, with
  `ui.research.allowCreate`, is `create`/`delete` on AgentResearches in
  `ui.research.namespace` (see [One-off researches](#one-off-researches)).
- **Login.** Authorization code flow with PKCE, `state` and `nonce`; the ID
  token's signature, issuer, audience and expiry are verified. The provider's
  tokens are not kept: the session holds only the user name, the roles and
  the expiry.
- **Sessions** are AES-GCM encrypted cookies (`__Host-` prefix, `HttpOnly`,
  `Secure`, `SameSite=Lax`), so the UI keeps no state and replicas share
  logins through the session key. Logout clears the cookie and ends the
  session at the provider (RP-initiated logout) if it supports it. Since
  nothing is stored on the server, a copied cookie stays valid until it
  expires (`ui.auth.sessionTTL`); changing the session key ends all sessions.
- **Redirects.** After the login the browser only goes back to a path on the
  UI's own host; login errors reach the sign-in page as codes, never as
  text from the URL.
- **Browser protections.** A strict Content Security Policy (only the UI's
  own scripts, styles and API, no framing), HSTS, `nosniff`, and Go's
  cross-origin protection for requests that change state (logout, comments,
  one-off researches). Links to
  GitLab must be `http(s)` URLs.
- **Research results are untrusted.** They come from agents that read web
  pages. The UI validates every stored result against the block schema again
  before it sends it (see [API](#api)), renders blocks with its own
  components and its markdown subset renderer, never as HTML, loads no
  images and links only to `http(s)` URLs. The Content Security Policy is the
  same with research as without.
- **https only.** `externalURL` must use https (http only for `localhost`);
  TLS ends at the Gateway.
- **Deny by default** (see [Access rules](#access-rules)).
- **Writing only as the user.** Comments are posted with the user's own
  GitLab OAuth token (scope `api`) or their fixed fine-grained token, never
  with a shared or the bot's token,
  and only with the `steer` grant; they go through Go's cross-origin
  protection, a size and a rate limit, and leave an audit line. The tokens
  stay server-side in an encrypted cookie bound to the UI user. A stolen pair
  of cookies could post as that user until the GitLab connection expires or
  is revoked.

## API

The UI is a Vue app on top of a small JSON API, which other clients can use
with the same session cookie:

| Endpoint | Returns |
|----------|---------|
| `GET /api/v1/me` | user name, roles, auth mode |
| `GET /api/v1/features` | the sections you may see, e.g. `{"features":[{"name":"factory"},{"name":"history"},{"name":"chat"},{"name":"research","write":true,"namespace":"agents"}]}` (`history` with the factory and a research database; `chat` with `ui.chat.writerSecret` and a `chat` grant); `write` and `namespace` only with the research write grant and `ui.research.allowCreate`, see [Research rules](#research-rules) |
| `GET /api/v1/projects` | the projects you may see, with task counts per stage |
| `GET /api/v1/milestones` | `{"milestones": […], "history"}`: the milestones of the projects you may see, from their spec tasks and, with the research database (`history: true`), the snapshot of done ones whose tasks are gone. Each row: `namespace`, `project`, `iid`, `title`, `webURL`, `state` (`running`, `paused`, `waiting` for its turn to be planned, `done` (autopilot done or the milestone closed), `planned` without autopilot), `phase` (the autopilot's, lower case), `progress` (`merged` (or skipped, or closed) of `total`, `open`), `followUpsOpen` (leftovers still open), the cost as in a project (`costUSD`, `costKnown`, `tokens`, `costFrom`; without `spend`), `startedAt`, `doneAt`, `needsYou` (the milestone has an Inbox item) and `stored` (its tasks are gone, the row is the snapshot's). State and progress come from the tasks, spend, start and done times and follow-ups from the snapshot. `?state=` `open` (default: needs you first, then running, then waiting, paused and planned, the oldest start first within each), `done` (the newest done first) or `all` (open, then done); `?limit=` 1–200 (default 50, more is capped); 400 for other values; `?project=<namespace>/<name>` narrows the rows to one project (404 when you may not see it). Without the database, or when it fails, done milestones whose tasks are gone are missing and costs are the tasks' |
| `GET /api/v1/overview` | The dashboard's summary strip and *Working now* for the projects you may see, from the live tasks: `running`, `paused` and `waiting` (milestones by the states of `/api/v1/milestones`), `needsYou` (the Inbox's count), `spendToday` (since 00:00 UTC) and `spend7d` (today and the 6 days before) as costs (`costUSD`, `costKnown`, `tokens`, `costFrom`), and `history`. Without the research database, or when it fails, the spend fields are absent (unknown, not $0) and `history` is false. `working`: one entry per running implement task (`namespace`, `project`, `issueIID`, `title`, `milestoneIID`, `task`, `since`, when its run started), and one without an issue for a project with an active autopilot or queued work and nothing running; each carries the project's `next` (`issueIID`, `title`, `milestoneIID`: the first queued implement task in queue order, oldest milestone first, else the next pending issue of the oldest running autopilot), `usageLimits` and `engineUsage`. Sorted by project, the longest running first. No GitLab calls |
| `GET /api/v1/projects/{namespace}/{name}` | a project with its tasks by milestone (`trackingIssueIID` where a milestone has a tracking issue); the project and each milestone carry their cost: `costUSD` (`""` with `costKnown: false` when no call reports a cost), `tokens` (input, output and cache), `costFrom` (`calls`: summed from the recorded agent calls and older runs, with `spend` the totals as in `GET /api/v1/spend`; `tasks`: summed over the current tasks, without a connected research database). The milestone and task endpoints carry the same fields. A task whose issue has a companion merge request (cross-repo changes) carries `companion`: `homeIssueIID`, `project`, `mrIID`, `mrURL`, `state` (the landing state of `status.companions`, absent before the home merge), `chip` (`draft` before the home merge, `waiting for release` while waiting for the release, a rebase or CI, `ready`, `merging`, `merged`; `blocked` and `closed` keep the chip of the step they stopped at), `reason` (why it waits, is blocked or was closed), `release`, `readyAt`, `autoMerge` |
| `GET /api/v1/projects/{namespace}/{name}/milestones/{iid}` | a planned milestone: its spec task, the autopilot (`phase`, `pausedReason` as raw text; while paused `pauseKind`, `pausedFor`, that issue's pushed `head` and `reviewErrors`, and its `exits` (the ways out of `PauseExits`, each automatic one with a live `status`: `met` (lifts on the next poll), `inProgress` (a run pending, a rebase in flight), `waiting` or `unknown`, and a plain-text `detail` such as "main is red at abc1234"; a remedy has `allowed` when the page may offer it as a button: `merge` with `permissions.merge`, `resume`, `skip`, `retry` and `review` with `permissions.chat`, never labels, GitLab actions, prompt answers or commands the Desk cannot send); `mergeOverride`; queue `items` with title, state, MR number and URL, and task; `queuedBehind`, the older autopilot milestones ahead, ascending, while it waits for its turn to be planned), the `tracking` issue, the `pilot` (task, autonomy, open proposals made before prompts, cost), the milestone's `prompts` (the open ones and the newest 10 closed, newest first; each with its `kind`, `question`, untrusted `context`, `options` with the recommended one first and their `actions` as plain text, `state`, `answer`, `canAnswer` (open, and you are an owner subject of the milestone with the chat grant; the operator checks again) and the `target` task whose chat takes the `answer` command: the Coordinate task), its implement tasks, its `companions` (the companion merge requests of its issues as in a task's `companion`, also those whose task is gone, by home issue), its cost and `permissions` (`gitlab`, `steer`, `merge`, `draft`: what the page may offer; `merge` means you may send merge anyway: an owner subject of the milestone with the chat grant, which the operator checks again; `draft` that you may ask for a draft of a follow-up milestone: as `merge`, while the pilot exists and a follow-up is open; `followUp` that you may create the follow-up milestone: `steer`, an owner subject of the milestone, and a follow-up is open; `dismiss` that you may dismiss the remaining follow-ups: `steer`, an owner subject of the milestone, autopilot on it, and its posted retrospective has open follow-ups none dismissed yet), its `state` (as in `GET /api/v1/milestones`) and its `issues` (plan order, then the autopilot's items the plan lacks: `iid`, `title`, `state` (`planned`, `pending`, `active`, `merged`, `skipped`, or `closed` for an issue the backfill found closed without a merge on record), MR number and URL, the live implement `task`, and with the research database `figures`: `wallSec` from the first run to the merge (or now), `agentSec` (the calls' durations), `pausedSec` (the milestone's pauses over the issue), `costUSD`, `costKnown` (false without calls, never $0) and `tokens`; no `figures` without the database or while the snapshot lacks the issue), and its `retrospective` once the pilot wrote one (live from task status, else the snapshot's; absent without one): `text` (untrusted markdown without HTML), `postedAt`, `noteURL` (the comment on the tracking issue), `failed` (why there is none), `followUps` (`number`, `title`, `body`, `state` `open`, `issue`, `milestone` or `dismissed`, `targetIID` and the `targetURL` of the issue or milestone it became) and with the research database `costDrivers`, the top 5 of the milestone's issues and roles by cost, those of unknown cost (`costUSD` "") after by tokens (`label`, `kind` `issue` or `role`, `costUSD`, `costKnown`, `tokens`; roles span issues, so the two overlap), and while the pilot exists its `draft` of a follow-up milestone (the one asked for with `pending: true` and its `numbers` while it is written, else the last one written: `numbers`, `title`, `description`, untrusted markdown without HTML until an owner confirms it, `at`, or `error`). A milestone whose tasks are gone (a closed one, after the operator deleted them) answers from the snapshot with `stored: true`: title, `state` (`done` once closed or done, else `planned`), issues with figures, the `tracking` issue, cost from the agent calls, `pauses` and the stored `retrospective` (no `failed`); `spec` is empty and there is no `autopilot`, `pilot`, prompt, task or companion; `permissions` keep `gitlab`, `steer` and `followUp` (from the snapshot's owner subjects), while `chat`, `transcript`, `merge`, `draft` and `dismiss` are false. 404 without a spec task and without the research database or a snapshot; 500 when the database fails |
| `GET /api/v1/projects/{namespace}/{name}/milestones/{iid}/issues/{issue}` | an issue of the milestone, its row of the milestone's `issues` (`iid`, `title`, `state`, MR number and URL, the live implement `task`, `figures` with the research database) with `namespace`, `project`, `milestoneIID`, `milestoneTitle` and the issue's GitLab `webURL`. With a live `task` the page shows what `GET …/tasks/{task}` holds; its stream is `GET /api/v1/stream?project=…&milestone=<iid>&issue=<issue>`. A milestone whose tasks are gone answers from the snapshot with `stored: true` and no `task`. No GitLab calls. 404 for an issue not in the milestone, a milestone without a spec task and without the research database or a snapshot, or a project you may not see; 500 when the database fails for a stored issue (a live one then has no `figures`) |
| `GET /api/v1/projects/{namespace}/{name}/milestones/{iid}/thread` | the comments of the milestone's tracking issue (`parts`), read with your GitLab token once the issue is confirmed as the operator's tracking issue of this milestone (409 otherwise); 409 when not connected, 404 without a tracking issue, 502 when GitLab fails to list the comments |
| `POST /api/v1/projects/{namespace}/{name}/milestones/{iid}/notes` | posts `{"body": "…"}` to the milestone's pilot on its confirmed tracking issue in your GitLab name, with the pilot prefix in front unless the body starts with it; needs the `steer` grant (403, 404 without the project), 409 for an issue that is not the milestone's tracking issue, 429 over the rate limit |
| `POST /api/v1/projects/{namespace}/{name}/milestones/{iid}/followup` | creates the follow-up milestone of `{"followUps": [1, 3], "title": "…", "description": "…"}` in GitLab with your token: the title (trimmed, 1–255 characters, one line) and the description (trimmed, 1 byte to 64 KiB, without its `owners`, `owner-subjects` and `followup-of` lines), a blank line, then the project's spec and autopilot markers and the source milestone's `owners`, `owner-subjects` and `followup-of <iid> <numbers>` lines; from the live tasks, else the snapshot (closed milestones, research database). 201 `{"iid", "webURL"}`; 400 for an invalid body or follow-ups the retrospective lacks (`unknown`), 403 without the `steer` grant or unless you are one of the milestone's owner subjects, 404 without the project or the milestone, 409 `{"taken": […]}` for follow-ups already opened, taken or dismissed, when GitLab is not connected or rejected the token (`login`) or refused the milestone (a title in use), 429 over the comment rate limit. Writes nothing to the database: the operator settles the follow-ups when it reads the line; audit line without the text |
| `POST /api/v1/projects/{namespace}/{name}/milestones/{iid}/followups/dismiss` | dismisses the remaining follow-ups of an open milestone's retrospective (no body): confirms the tracking issue as for the chat, finds the retrospective's thread (one `discussions` call) and replies `create none` there with your token; the operator settles them on its next poll, so the milestone can close. 202 with the reply (`id`, `author`, `body`, `createdAt`); 403 without the `steer` grant, unless you are one of the milestone's owner subjects, or when your GitLab account is not among the owners the operator pinned when it was posted (it would ignore the reply) or GitLab refuses the reply; 404 without the project; 409 when the milestone has no live tasks (closed), autopilot is off, there is no posted retrospective, nothing is open or it was dismissed already, the tracking issue cannot be confirmed, the retrospective's comment is gone, or GitLab is not connected or rejected the token (`login`); 429 over the comment rate limit; 502 when GitLab fails. Writes nothing to the database; audit line `milestone.dismiss` |
| `GET /api/v1/projects/{namespace}/{name}/tasks/{task}` | one task and its project (`permissions.chat` when Desk chat is configured and granted), the task's `cost` and `prompts`: an Implement task's needsInput prompts as in the milestone view, answered on the task itself (empty for other tasks) |
| `GET /api/v1/projects/{namespace}/{name}/tasks/{task}/transcript` | server-sent events: `entry` per transcript line of the task's pod (what it logged so far, then live), `end` with the reason (`no pod`, `pod stopped`, `pod not available`, `session expired`); needs the `transcript` grant (403 without), counts as an event stream |
| `GET /api/v1/projects/{namespace}/{name}/tasks/{task}/chat` | `{"messages": […]}` of the task's Desk chat, newest first: `id`, `at`, `author`, `authorName`, `role`, `kind`, `body` (omitted and `redacted` without `conversation`), `replyTo`, `state`, `reason`; `?before=` / `?after=` message ids, `?limit=` 1–100 (default 50); 404 without a research database |
| `POST /api/v1/projects/{namespace}/{name}/tasks/{task}/chat` | posts `{"body": "…", "kind"?: "message"\|"interrupt"\|"command", "clientKey": "…"}` (kind defaults to `message`; `clientKey`, 1–128 characters, is required: the composer's retry key); for `kind: "command"` the body must be JSON with a known `cmd` (`retry`, `review`, `use` with `engine`+`model` or `default: true`, `effort` with an effort or `"default"`, `budget` with `usd` (a budget above 0 or `"default"`, Implement tasks), `pause`/`resume`, `skip` with `issue`, `merge` with `issue` and the head's `sha` (owner subjects only, checked by the operator), `accept`/`reject` with `proposal`, `answer` with `prompt` (`Q<n>`) and either `option` (`A`–`E`) or non-blank `text` (at most 4000 bytes; the milestone's prompts on its Coordinate task, the agent's questions on its Implement task), `revisit` with `decision` (`D<issue>-<n>` or `D<issue>-P<n>`) and optional `text` (at most 4000 bytes), `draft` with `followUps` (1 to 9, at least one; the Coordinate task); unknown fields rejected); 400 with the parse error otherwise; 201 `{"id"}` on create, 200 `{"id"}` when `clientKey` already existed for this user (retry-safe); needs the `chat` grant and `ui.chat.writerSecret` (404 without the feature, 403 without the grant, 413 over 16 KiB, 429 over the rate limit); audit line without the text |
| `GET /api/v1/gitlab` | whether GitLab is configured and your account connected (`enabled`, `connected`, `username`) |
| `GET /api/v1/projects/{namespace}/{name}/tasks/{task}/thread` | the comments of the task's issue, MR and tracking issue (`parts`), read with your GitLab token; 409 when not connected |
| `POST /api/v1/projects/{namespace}/{name}/tasks/{task}/notes` | posts `{"target": "issue"\|"mr"\|"tracking", "body": "…"}` in your GitLab name; needs the `steer` grant |
| `GET /api/v1/stream` | `{"events": […], "older", "newest", "history"}`: events of the projects you may see, newest first: `id`, `at`, `namespace`, `project`, `milestoneIID`, `issueIID`, `mergeRequestIID`, `task`, `kind` (`note`, `run.started`, `run.finished`, `push`, `ci`, `review`, `needs`, `merged`, `closed`, `autopilot`, `autopilot.paused`, `autopilot.resumed`, `proposal`, `pilot.writeFailed`, `prompt.posted`, `prompt.answered`, `prompt.closed`, `retrospective`, `chat.user`, `chat.agent`; which tab shows which is under *Which events show where* above), `actor`, `summary`, `data` (per kind, e.g. a comment's `body`, `url`, `bot`; a run's `result`, `costUSD`, `durationSec`; chat's `body`, `role`, `kind`, `state`), `redacted` (a comment's or chat message's body withheld without `conversation`). Chat rows come only when `?kinds=` names `chat.user` or `chat.agent` (their ids are not unique against history events), merged with history by time; with `&milestone=` only the chat of that milestone's tasks, with `&issue=` of that issue's. `?project=namespace/name` (404 if you may not see it), `&milestone=<iid>`, `&issue=<iid>` with a milestone (the issue's events: recorded with it, in that milestone or without one; 400 without a project and milestone), `?kinds=` comma separated, `?q=` full text (1–200 characters; comments only where you have `conversation`; chat is left out of text search), `?before=` the `older` cursor or `t:<RFC 3339 time>`, `?after=` the `newest` cursor (what is newer, for live updates), `?limit=` 1–100 (default 50); 400 for other values. `history` is false without a research database (no events), 503 until the UI is connected to it |
| `GET /api/v1/projects/{namespace}/{name}/history` | `{"days": [{"day", "runs", "merged", "costUSD", "events"}], "totals": …, "previous": …}`: activity per day of the last `?days=` (1–90, default 30) in `?tz=` (an IANA time zone, default UTC), every day oldest first, with the totals and those of the days before; 404 without a research database |
| `GET /api/v1/spend` | what a scope cost, summed from the recorded agent calls: `?namespace=&project=` (required) narrowed by `&milestone=`, `&issue=`, `&task=` and `&run=` (needs `task`); `{"totals", "children", "byKind", "byRole", "byModel", "byReviewRound", "bySession", "byCheckpoint", "bySubagents", "cacheReadShare", "stepLines"}`. `totals` (also per child and breakdown): `costUSD` (2 decimals; `""` with `costKnown: false` when no call reports or estimates a cost, never `"0.00"`), `unknownCalls` (calls without a cost, not in `costUSD`), `unmeteredCalls` (the part of them that reports no tokens either: not metered), `inputTokens`, `outputTokens`, `cacheReadTokens`, `cacheWriteTokens`, `calls`, `subagentCalls`, `durationMS`, `legacyCostUSD` (the part of `costUSD` from runs recorded before agent calls, without tokens). `children` are the next level, oldest or lowest first: a project's milestones (`key` the IID, `""` for none), a milestone's issues (and the milestone's own calls with `level: "milestone"`), an issue's runs (`key` `task/run`), a task's or run's calls (`key` the seq), each with `level`, `key`, `label`, `at` (its first call), `companion` (issue rows of an issue with a companion repository: its project; the companion's calls count to the home issue), `totals` and `models` (`model`, `main` (false for subagent models), `tokens`, `costUSD`, `costKnown`, `unmetered` (none of its calls reports a cost or tokens)); calls also carry `kind`, `role`, `engine`, `model`, `effort`, `resumed`, `fallbackFrom`, `costSource` (`reported`, `estimated`, `unknown`), `turns`, `contextSeries` (context tokens per main-thread turn) and `subagentTurns` (indexes into it after which a subagent ran) and `readGateBlocks` (reads the read gate denied), `checkpoint` and `checkpointSHA` (the reason and commit when the call's end saved a checkpoint, left out otherwise) and `linesAdded`, `linesDeleted` (the uncommitted lines an implementer call left, 0 for other calls) and `stepGateNudges` (how often the step-size gate nudged the call, 0 without). The breakdowns sum the calls by kind, role and model (subagent models as their own); `byReviewRound` the review calls by round (`key` `"1"`, `"2"`, …: within a run, the reviews before the first, second, … fix call by seq, summed across runs); `bySession` the implement calls by session (`key` `fresh` or `resumed`); `byCheckpoint` the calls that saved a checkpoint by reason (`key`, e.g. `usage-limit`), the most frequent first; `bySubagents` the calls per `role` with `withSubagents` (a subagent ran) true or false, with `totals` and `subagentTokens` (the tokens of the subagent models) and `readGateBlocks` (the read gate's denials); `cacheReadShare` is cache reads over all input tokens; `stepLines` the size of the scope's step commits: `count` (implement calls with lines added plus deleted above 0), `median`, `p90` (interpolated, rounded) and `max` of their lines, and `nudges` (the step-size gate's nudges of all calls), all 0 without step data. 400 without namespace or project, for a number that is not positive or a run without task; 404 without a research database or when you may not see the project; 503 until the UI is connected to the database |
| `GET /api/v1/inbox` | `{"items": […]}`: what needs you now across your projects, newest first: `kind` (`task` with `reason` `needs-input`, `failed` or `blocked`; `proposal` with `proposalID`; `prompt` for each open prompt, with `promptID`, the question cut as `title` and the prompt kind as `reason`; a task with an open needsInput prompt has no `task` item; `pause` for a paused autopilot, with the milestone's `title`, `reason` (the pause reason), `pauseKind` and the paused issue as `issueIID`, listed after the dated items; `companionReady` for a companion merge request that is ready and waits for the owners to merge it (autopilot does not, and the owners were told), with the companion `project!iid` as `title`, the release that made the home change live in `reason`, the merge request as `url`, the home issue as `issueIID` and `at` when the owners were told; it leaves once merged or closed), `namespace`, `project`, `task`, `issueIID`, `milestoneIID`, `title`, `at`, `url` |
| `GET /api/v1/events` | server-sent events: `change` with the kind (`project`, `task`, `research`), namespace and name of what changed; research events only for researches you may read |
| `GET /api/v1/researches` | the researches you may read: `namespace`, `name`, `view`, `depth`, `schedule`, `timeZone`, `phase`, `lastRun`, `nextRun`, `lastEngine`, `lastModel`, the current or last `run` (`id`, `startedAt`, `finishedAt`, `engine`, `model`, `effort`, `reason`, `outcome`, `message`, `costUSD`), `skippedReason`, `waiting` (`engine`, `until`), `oneOff`, `createdBy`, `canDelete` (your own one-off you may delete) |
| `GET /api/v1/researches/{namespace}/{name}` | `{"research": …, "run": …, "result": …}`: the research as in the list plus its `prompt`, `topics` and `agent` (absent for auto), its latest result and that result's run (as in `runs`), both `null` without one |
| `GET /api/v1/researches/{namespace}/{name}/runs` | `{"runs": […]}`, newest first: `id`, `startedAt`, `finishedAt`, `status` (`running`, `succeeded`, `degraded`, `failed`, `limited`), `engine`, `model`, `effort`, `costUSD`, `choiceReason`, `error`, `warnings`, `expiresAt`; `?limit=` 1–100 (default 20), `?before=` an RFC 3339 time (the `startedAt` of the last run of the previous page); 400 for other values |
| `GET /api/v1/researches/{namespace}/{name}/runs/{id}` | `{"research": …, "run": …, "result": …}`: the research as above, one run of it and its result (`null` without one); 404 for a run of another research |
| `GET /api/v1/research/search?q=` | `{"hits": […]}`: at most 50 results of the researches you may read that match `q` (1–200 characters, web search syntax: words, `"a phrase"`, `or`, `-word`), best match first, then newest: `runID`, `namespace`, `research`, `title`, `summary`, `createdAt`; results of deleted researches are not searched |
| `GET /api/v1/research/form` | what the one-off form offers: `namespace`, `projects` (`name`, `available`, `reason` when not, `models` with `engine`, `model` and `label` like `claude/sonnet`), `depths`, `views`, `efforts` |
| `POST /api/v1/researches` | starts a one-off research from `{"project", "prompt", "depth"?, "view"?, "topics"?, "agent"?: {"engine", "model", "effort"?}}` (depth `quick` and view `report` by default, no agent: automatic); 201 with `{"namespace", "name"}`, 400 with an `error` for an invalid request (unknown fields included) and the `field` it is about (`prompt`, `topics`, `depth`, `view`, `project`, `agent`) where there is one, 413 over 32 KiB, 429 with `Retry-After` over the rate limit |
| `DELETE /api/v1/researches/{namespace}/{name}` | deletes your own one-off in `ui.research.namespace`: 204, 403 for someone else's, a scheduled one or another namespace |

The one-off endpoints answer 404 without `ui.research.allowCreate` or any
research rule (and a delete for a research you may not read), and 403 with
`research: read` only or a write rule that does not match the new research
in `ui.research.namespace`; see
[One-off researches](#one-off-researches).

The research endpoints exist only with a research database. They answer 404
without a matching research rule, for a research you may not read and for
one that does not exist (no matter whether it has stored runs), and 503 until
the UI is connected to the database. A result has `runID`, `title`,
`summary`, `blocks` (the [block schema](research.md#the-agents-answer), items with
`"new": true` when this run is the first of the research to report
their URL, see [new items](research.md#runs-results-and-new-items)), `createdAt`, `expiresAt` and `warnings`. The UI runs
every stored result through the block validator again before it sends it: one
that fails (changed in the database, or by an older validator) is replaced by
the title "Result withheld", no blocks and a warning, and logged; a search hit
with an invalid title or summary is left out.

Change events carry no content; the app fetches what it shows again. The
stream sends a heartbeat every 25 seconds and ends when the session expires.
A user may have 20 streams open at a time (one per browser tab); more answer
429.
If your Gateway cuts long requests, the app reconnects and catches up; set
`ui.httpRoute.timeouts: {request: 0s}` to keep the stream open.

## Development

```sh
make ts-types  # web/src/generated/*.ts from the Go views (tygo.yaml)
make ui        # ts-types (with Go) + npm ci + type-check + build into web/dist/app, embedded by go build
make run-ui    # UI without login on http://127.0.0.1:9080, against your kubeconfig
cd web && npm run dev   # live reload on http://localhost:5173, API proxied to :9080
```

The Go server is in `internal/ui` (`cmd/ui` wires it), the app in `web/src`.
Without `make ui` the binary serves only the API.
`make ui` also runs `npm test` (`node --test` in `web/test`, e.g. the markdown
renderer). `web/src` uses no `v-html` and no `innerHTML`: keep it that way.

**View types.** The TypeScript types of the API's views are generated from
the Go structs with [tygo](https://github.com/gzuidhof/tygo) (`go tool
tygo`, pinned in `go.mod`): `internal/ui` into `web/src/generated/views.ts`,
the CRD types the views embed (`api/v1alpha1`) into
`web/src/generated/api.ts`. Both are committed. After changing a view
struct, run `make ts-types`; the CI `drift` job runs it and fails when the
files change. `tygo.yaml` lists the files with views (server-only types such
as the GitLab cookie and the log source live in other files) and the type
mappings; a Go type without a mapping becomes `Unmapped`, which fails the
type-check. String fields with a closed set of values and the open shapes
(`map[string]any`, `[]any`) get a `tstype` tag that names a union or type in
`web/src/types.ts`, which holds only such web-only types and the response
envelopes the handlers build as maps. Enum types whose constants are not
named after the type (e.g. `PauseKind`, `PauseOwner`) come out as `string`.

**Research pages with seeded data.** The research pages need a research
database and `AgentResearch` objects. For development, a local Postgres
with seeded results stands in for real runs:

```sh
make test-db          # Postgres 17 in docker on 127.0.0.1:55432
make research-seed    # database ai_operator_dev: migrated as research_owner
                      # (granting research_reader), seeded from hack/research-seed.sql
kubectl apply -f hack/research-dev.yaml   # the matching AgentResearches (CRDs: make install)
export AI_OPERATOR_UI_RESEARCH_DB_USER=research_reader AI_OPERATOR_UI_RESEARCH_DB_PASSWORD=reader
# uncomment the research section of hack/ui-dev.yaml, then
make run-ui
```

The seed (namespace `research-dev`) has:

* `morning-news`: two news flash runs that share two URLs, so the latest
  marks only its other items *new*;
* `heat-pumps`: a deep report with every block type;
* `hostile`: a stored result with raw HTML and `javascript:` links, as if
  written behind the validator's back. Its page must show *Result withheld*.
* agent calls of project `dev` for the [Spend](#spend) tab: milestone 4
  with its spec, pilot and retro calls, #77 over two runs (an opus step with
  a haiku subagent and a context series, a resumed step, a cursor review
  with an estimated cost, a cursor fix without a cost), #78 with a run from
  before token tracking, and a chat turn without a milestone. The tab is on
  the stream of an AgentProject `dev` in `research-dev`
  (`…/projects/research-dev/dev?tab=spend`).
* Desk milestones for the dashboard and the milestone pages (snapshot rows
  in `desk_milestones`): in project `dev` two done ones (`%3`, old and
  without agent calls, so it shows no spend, and `%4` with a retrospective
  and three follow-ups: open, opened as an issue, dismissed) and `%5`
  (running, no active issue); in project `web` `%2` running (5 issues, 2
  merged) and `%3` paused for #41, with run, merge and pause events and
  agent calls, so wall, agent and paused time and cost show. The lists
  need AgentProjects `dev` and `web` in `research-dev` (the operator need
  not run). A milestone's state in work comes from its live tasks, and the
  seed has none: *Recently done* lists `%3` and `%4`, while the open ones
  stay out of *In progress* (a snapshot row without tasks is in no work)
  and open by URL from the snapshot as `planned`
  (`…/projects/research-dev/web/milestones/2`). Running, paused and
  waiting rows, the strip's counts and *Working now* need spec tasks with
  autopilot status, e.g. an operator on a kind cluster.

The data is dated to the latest 08:00 UTC, so the pages look fresh, and
`make research-seed` replaces it when run again. Apply
`hack/research-dev.yaml` only where the operator does not run, since it
would start runs for these researches. The UI logs in as the reader, so the
pages go through the same SELECT-only grants as in the cluster. The
passwords are fixed (`owner`, `reader`, `chat`) and meant for this loopback
database only.

**Desk chat.** `make research-seed` also creates the chat writer
`research_chat_writer`. With the research section uncommented,
`export AI_OPERATOR_UI_CHAT_DB_USER=research_chat_writer
AI_OPERATOR_UI_CHAT_DB_PASSWORD=chat` turns on the chat section of task
pages; posts go through the same row-level security as in the cluster. No
operator reads this database, so posted messages stay *waiting*: insert
agent rows (`role = 'agent'`, `reply_to` the user row) as `postgres` to
see replies.

**A one-off's live states without an operator.** Start a one-off from the
form (uncomment `namespace` and `allowCreate` in `hack/ui-dev.yaml`; the
form needs an AgentProject with `spec.research.writerSecret` in
`research-dev`), then
patch its status by hand to walk through the phases:

```sh
kubectl -n research-dev patch agentresearch q-… --subresource=status --type=merge \
  -p '{"status":{"phase":"Waiting","waiting":{"engine":"claude","until":"2026-10-29T14:00:00Z"}}}'
kubectl -n research-dev patch agentresearch q-… --subresource=status --type=merge \
  -p '{"status":{"phase":"Running","waiting":null,"run":{"startedAt":"2026-10-29T14:00:00Z","engine":"claude","model":"claude-opus-5-5","reason":"chosen: claude/claude-opus-5-5"}}}'
```

## Roadmap

1. **Into a running step.** Messages that reach a running claude step at once
   (`--input-format stream-json`), not only at its next step.
2. **Actions** for an `operator` access level, limited to what GitLab cannot
   do (e.g. restart a stuck pod, clear a usage limit, hibernate a task).
   GitLab stays the place where the work is steered.
3. **MCP** at `/mcp` on the same host, with tools on top of the same API and
   access rules. MCP clients authenticate with bearer tokens issued by the
   same provider (the UI then acts as an OAuth resource server and publishes
   `/.well-known/oauth-protected-resource`); the roles in those tokens go
   through the same claim mapping and rules.
