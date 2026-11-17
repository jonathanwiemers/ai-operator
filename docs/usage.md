# Using the ai-operator from GitLab

Every `/agent` command is listed in [commands.md](reference/commands.md).

Writing a milestone, answering the agent, following its work and iterating
are in the [guides](guides/README.md), and so are autopilot, its pauses and
the pilot, and the Desk in [The Desk](guides/desk.md) and
[Steer from the Desk](guides/steer-from-the-desk.md). This page keeps
budgets, companions, releases and watching from Kubernetes until those
sections move too.

Setting up GitLab (the bot user, labels, board and webhooks) is part of
[Install](getting-started/install.md#set-up-gitlab).

## Budgets, companions and releases

Running a milestone on autopilot, its pauses and the pilot are in the
guides: [Autopilot](guides/autopilot.md), [When autopilot pauses](guides/pauses.md)
and [The pilot](guides/pilot.md). The parts below move next.

### Budget

A line `/agent budget <usd>` in the milestone description (for example
`/agent budget 5`, decimals allowed) gives an autopilot milestone a budget. On every
poll the operator reads what the milestone's agent calls cost so far
(`agent_calls`, all calls with its milestone: planning, implementers,
reviewers, CI and conflict rounds, the pilot) and shows it with the limit
in the spec task's `status.autopilot.budget` (`limitUSD`, `spentUSD`).
Once the spend reaches the budget, autopilot pauses with
`pauseKind: budget` ("the budget of $5.00 is used ($5.12 spent) …"). A
running agent call is never killed: it finishes, and nothing new starts or
merges, as with every pause. A run in progress stops too, between calls:
on the next poll the operator sets `spec.budgetStop` on the milestone's
Implement tasks (for an issue's budget, on that issue's task), and the
runner reads it before every agent call. After the call in progress the
run ends `Deferred` ("the budget is used"), like a run that hit a usage
limit: the steps it pushed stay on the branch, its workspace and session
are kept, and the run request stays pending without a pod. Once the pause
ends the flag is cleared and the run continues from where it stopped. Desk
chat turns still run. Calls without a known cost (cursor without
`pricing`, see the [chart README](../charts/ai-operator/README.md#pricing)) count $0
towards it.

To go on, raise the line, e.g. to `/agent budget 50`: once the budget is
above the spend, autopilot continues on the next poll by itself. Removing
the line ends the pause too. `/agent resume` alone pauses again at once
while the budget is spent. Like the owners and the rules, the line is no
part of the spec: changing it re-plans nothing.

A value that is not a number above 0 is ignored (the operator logs it):
the milestone has no budget then. Without the research database, or while
the spend cannot be read, the budget is not enforced (logged on every
poll): autopilot never pauses for lack of data.

#### What the spend counts

Budgets, the [Spend tab](ui.md#spend) and the retrospective read the same
numbers: one row per agent call in `agent_calls`.

- **A call counts only itself.** Its tokens are input, output, cache read
  and cache write of everything the call ran: the main thread, subagents
  and compaction (claude's `modelUsage`). A call that resumes a session
  (implementer steps, fix rounds, re-runs) counts only what it spent
  itself, not the session's earlier calls: claude's `--resume` restores
  the session's totals into `modelUsage` and `total_cost_usd`, so the
  operator takes `model_usage_since_last_result` instead. Desk chat turns
  count their own turn the same way.
- **Per-model tokens are the same tokens, split.** A call's models (its
  main model and the subagents' models) add up to the call's tokens, so
  *by model*, *by kind*, *by role* and the drill-down all add up to the
  total. Calls recorded by older releases could hold a
  resumed session's carried-over totals in their models: such a call
  counts whole on its main model, with its own tokens and cost, and is
  not split.
- **Cursor's `auto` is not metered.** Cursor calls with model `auto` (the
  `fast` profile, the bugbot and security reviewers) let cursor pick the
  model, and its ACP session reports neither which model it picked
  (`currentModelId: "default[]"`) nor any usage. So these calls have no
  tokens and no cost: they appear as *cursor auto (unmetered)*, count as
  *N calls not metered* next to the total, add nothing to the token sums,
  and count $0 towards a budget. No model or price is guessed for them.
- **Old token basis.** Calls recorded before a call stored how its tokens
  were computed (`token_basis` 0, older releases: before the sum of
  `modelUsage` and per-turn deltas) are on the old basis, and their tokens
  are not comparable with newer calls'. Every total, child and breakdown
  counts them (runs without calls and checkpoint events are no calls and
  never count). When a milestone holds such calls, the retrospective's
  total says so: "N of M calls on the old token basis" when it mixes both,
  "old token basis" when all are old. They are not recomputed.
- **Cursor's cost: reported, estimated or n/a.** Cursor sends usage over
  ACP only optionally, and the cursor-agent in the agent image sends none
  yet. When an ACP `usage_update` carries a cost in USD, that is the
  call's reported cost (ACP's cost is the session's running total, so a
  call counts the increase since the previous call of its session; other
  currencies and credits are ignored). Otherwise a call with tokens (from
  the ACP prompt response) is estimated from the
  [`pricing:` table](../charts/ai-operator/README.md#pricing), and without tokens or a
  price its cost is n/a, never $0. The context size a `usage_update`
  reports becomes the call's context series, never its tokens. Calls
  recorded before a price was configured, or before this release, are not
  re-priced.

#### Spend report script

`hack/spend-report.sql` answers "where did this issue's cost come from" and
"what do conflict rounds cost" from `agent_calls` alone, with psql on the
research database (it only reads). Variables: `project` (required),
`milestone` and `issue` (0 or unset: all). It prints four tables:

1. per issue: kinds, models, runs, review rounds (as in the Spend tab's
   *Review rounds*), calls, input, output, cache read and cache write
   tokens, cost and cost per 1M tokens;
2. conflict calls per issue and model, with the most calls one run made
   (above 1: the run resolved conflicts repeatedly) and the output and
   cache tokens, to see conflict rounds on Opus with large outputs;
3. the calls of one issue (`issue` ≠ 0): task, run, seq, kind, role, model,
   resumed, tokens, cost and duration;
4. every cursor call and every call on model `auto`, oldest first: time,
   kind, role, issue, task, run, seq, engine, model, `fallback_from`, cost
   source, tokens, cost, duration and the engine and `fallback_from` of the
   next call of its run. A cursor call that hit its usage limit is a row
   without cost or tokens; when the role has a fallback, the next call is
   the fallback's (`next_fallback_from` = `cursor`).

```sh
# Why did #179 cost $53.88: every call of the issue, in order.
psql "$DATABASE_URL" -v project=ai-operator -v issue=179 -f hack/spend-report.sql
# The milestone's issues, its conflict spend by model and its cursor/auto calls.
psql "$DATABASE_URL" -v project=ai-operator -v milestone=12 -f hack/spend-report.sql
```

Models are the calls' own models; the Spend tab's *By kind and model*
(and *Conflict calls per run* at milestone and issue scope) also splits out
the subagent models. Expired calls are left out, as in the tab.

#### Budget prompts

The owners get the milestone's budget as a `budget` prompt in the Desk,
mirrored on the tracking issue. The two raises are computed by the
operator from the spend so far: 1.25 and 1.5 times it, rounded up to whole
dollars, the first at least the budget + 1 (e.g. $41 and $49 for $32.40 of
$40).

- **At 80 %** of the budget spent, once per budget value
  (`status.autopilot.budget.warnedAt`): raise the budget to either amount,
  **keep going** (recommended, nothing changes) or **pause now** (only
  while autopilot runs).
- **At 100 %**, with the budget pause: raise the budget to either amount
  (the first recommended), **end the milestone here**, or **stay paused**.

A raise rewrites the description's `/agent budget` line, read again first:
a budget raised above the amount or removed by hand meanwhile is kept, and
the outcome says so. The next poll reads the new budget and a budget pause
ends by itself. Ending the milestone skips its pending issues, so none is
picked any more, and autopilot is done after the active issues; during the
budget pause the active issues cannot go on without spend, so they are skipped
too and autopilot is done at once. A milestone with nothing in its queue
(not planned yet, or a re-plan that left no issue) is never done: ending it
only answers that there is nothing to end; close the milestone in GitLab or
remove `/agent autopilot` to stop it. While a done milestone's budget is used
its issues' runs stay stopped (`spec.budgetStop`); raising the line lets
them go on. A prompt closes (superseded) once its budget is another or the
spend is under its threshold again; the 80 % prompt also closes when the
100 % one follows. Falling under 80 % and crossing it again with the same
budget posts nothing new; only another budget value is warned about again.
Each open prompt is one Desk Inbox item and one browser notification.
Issue budgets get no prompt, see below. Raising the budget and
ending the milestone are the operator's options only: the pilot cannot
propose them, and options of the model that use them are dropped.

#### Per-issue budget

Each open issue of the queue (pending or active) can have a budget of its
own: the project's default `spec.budget.perIssueUSD` (see
[AgentProject reference](reference/agentproject.md#specbudget)), or for one issue an
`/agent budget <usd>` comment on it (or its MR), e.g. `/agent budget 40`;
`/agent budget default` goes back to the project's. The comment starts no
run and is answered on the issue; it is stored on the issue's Implement
task (`spec.budgetUSD`). The same poll reads the issues' spend with the
milestone's, in one query, and shows it per issue in
`status.autopilot.items[].budget`.

Once an issue's calls cost its budget, autopilot pauses with
`pauseKind: budget` and `pausedFor` the issue ("#12 used its budget of
$20.00 ($20.31 spent). Raise it with `/agent budget <usd>` on the issue").
A pending issue that spent its budget earlier pauses it too, before it is
started. The milestone's budget goes first; then the issues in queue order.
Raising the issue's budget above its spend, `/agent budget default` (when
the default is above it), or skipping the issue ends the pause on the next
poll. Merged and skipped issues have no budget any more.

#### Spend heads-up per issue

`spec.budget.perIssueAlertUSD` (e.g. `"15"`) is an early signal that pauses
nothing: once an issue's calls cost that much, autopilot posts one heads-up
on the tracking issue and wakes the pilot (event `budget-alert/<iid>/<usd>`),
and keeps going. The heads-up names the issue, its spend, and its top three
kinds (implement, review, fix, conflict …) and models:

```
💸 Heads-up: #12 spent $15.50, past the spend alert of $15.00 per issue (`budget.perIssueAlertUSD`). Autopilot keeps going.

- Top kinds: implement $7.75 (4 calls), conflict $4.65 (2 calls), review $2.32 (3 calls)
- Top models: claude-opus-5-5 $13.95, claude-sonnet-5-5 $1.55
```

An issue with a budget gets the same heads-up at 80 % of it (with
`perIssueUSD: "20"`: the heads-up at $16, the pause at $20). Each heads-up
comes once per issue and threshold (`status.autopilot.items[].budgetAlertedAt`
and `budgetAlertedUSD`); another alert value or budget re-arms it. With both
set, each threshold the issue passes gets its own heads-up. An issue that
jumps past its budget in one poll gets only the pause. Like the budgets, the
heads-up comes from the poll's one spend query and needs the database.

The 80 % heads-up of an issue's budget is also a Desk Inbox item
(`budgetWarning`, "80 % of the issue's budget of $20.00 spent") and one
browser notification; both open the issue's Implement task, where the
task chat's `budget <usd>` raises it. There is no second comment and no
prompt: the item is derived from `budgetAlertedUSD`, so there is one per
crossing of a budget. It leaves the Inbox when the issue merges or is
skipped, its budget is raised (which re-arms the heads-up) or the budget
pause for it takes over. The `perIssueAlertUSD` heads-up is no Inbox item.

### Companion repository

An issue can change one **companion repository** besides its own project
(the design: [specs/cross-repo-changes.spec.md](specs/cross-repo-changes.spec.md)).
The project must be one of the AgentProject's valid `spec.crossRepo.targets`
(see [AgentProject reference](reference/agentproject.md#speccrossrepo), allowed by the cluster's
`crossRepo.allowedProjects`). Which issue gets one is decided per issue:

- **The milestone line** `/agent repo <project> [merge]` (a full path such as
  `my-group/infra/cluster`, or a GitLab URL of it) lets the milestone's
  issues change that project. It is part of the spec, so changing it
  re-plans. A malformed line, or one whose project is no valid target, is
  ignored; on an autopilot milestone the tracking issue names it once
  (`status.autopilot.repoNotices`), otherwise only the operator log and the
  spec task's `spec.repos[].problem` do.
- **The planner** sees each companion of the milestone's lines that count:
  the project, its allowed `paths`, and the files under them on its default
  branch (at most 200 in total, read through the GitLab API). It sets
  `repo` on the plan items that must change the companion, and the plan
  gives those issues the label `agent-repo::<project>` (recorded in the
  issue description). A `repo` that is none of the milestone's companions
  gives no label; the issue's plan comment says so. On a re-plan, a label
  the plan set follows the plan (replaced, or removed when the item drops
  `repo`); a label an owner set stays. Without such lines the planner's
  prompt is unchanged.
- **The label** `agent-repo::<project>` on the issue gives it the companion.
  It counts under a milestone line for that project, or when an owner or
  the bot added it (the issue's label events). An owner sets it with
  `/agent repo <project>` and removes it with `/agent repo none` (with
  `workflow.commandsFrom: owners` nobody else may), or by hand. A label that
  does not count is ignored, and the issue is told once.

When the label counts, the operator writes the issue's task
`spec.companion`: the project, the target's `paths`, the companion branch
`agent/<home project>-<iid>-<title>`, and `autoMerge`. Autopilot may merge
the companion's merge request only when all three agree: the milestone line
ends in `merge`, the milestone is on autopilot, and the target has
`autoMerge: true`. A companion an owner gave an issue outside such a line
is never merged by autopilot. Changing the companion starts no run.

In a run, the runner fetches the companion branch and the companion's
default branch into its own mirror of the companion, and the agent
container gets a checkout of it at `/workspace/repos/<slug>` (the
project's path lowercased, `/` as `-`, e.g. `my-group-infra-cluster`), on
the companion branch: on its remote head if it exists, else on the
companion's default branch. After the implementer and the fix rounds, the
companion's changes are committed and brought back into the runner's
mirror (in a run that pushes steps, with each step too). The companion
token goes to the runner container only. If the companion's target is no
longer valid, the run goes on without it and its report says so.

Before every push of the home branch (each step, the final push, CI-fix,
conflict and checkpoint pushes), the runner checks the companion branch in
its own mirror, from where it branched off the companion's default branch:
every commit and the net change. The run fails, and neither repository is
pushed, when a commit is a merge commit, when a change (also a deletion, or
either side of a rename) is outside the allowed `paths`, touches
`.gitlab-ci.yml`, a `.gitlab/` directory or `.gitmodules`, or adds a symlink
or a submodule, or when the new files of the range add up to more than
1 MiB. A file added and removed again in a later commit still fails it: a
push publishes every commit. The failure report on the issue names the path
and the reason.

After the home branch is pushed, a run whose companion branch changes
anything against the companion's default branch pushes the companion branch
too (a normal push, so the companion's CI runs). If that push fails, the
run fails with a report; the next run pushes both again. The first such run
opens the companion's merge request in the companion project, **as a
draft**: titled `<home title> (companion of <home project>#<iid>)`, labelled
`agent::companion`, with the describer's text for the companion diff and the
line `Lands after: <home project>!<home MR> is merged and live.` Later runs
find it again and rewrite its description when the companion diff changed.
It stays a draft (a run puts one marked ready back to draft): the operator
marks it ready when it can land. The home merge request's description gets
a line `Companion: <project>!<iid>` inside the runner's part of it. The task
records it all in `status.run.companion` (`project`, `mrIID`, `mrURL`,
`branch`, `pushedSHA`, `reviewedSHA`).

The companion change is committed before the reviewers run (and after
each fix round), so **the reviewers see both trees** in one input: the
section `## Tree: home (<project>)` with its changed files and diff, then
`## Tree: companion (<project>, allowed paths: …; lands after the home change
is released)` with the companion's. The reviewer's inline-diff cap
(`maxInlineDiff`) counts both diffs together: when they exceed it, both
trees are listed by their changed files only, so the companion is never cut
off. The security reviewer's prompt also says that the companion is applied
to production by Flux on merge, and asks it to flag secrets, plaintext
credentials, RBAC widening, and changes that break when applied before the
home release. A review of both trees sets `status.run.companion.reviewedSHA`
to the companion head it saw, with `status.run.reviewedSHA`; a run without a
review (a CI fix) that pushes a new companion head leaves the companion
unreviewed, and autopilot pauses with `notReviewed` before the merge. `/agent review` then reviews both heads again: the re-review is
asked for `<home head>+<companion head>`, so a new companion head is a new
review even when the home head did not change. Without a companion change
the reviewers' input is the same as without a companion.

A later run that ends without a companion change (the agent reverted it, or
the issue lost its `agent-repo::` label) closes the companion's merge
request, deletes its branch, removes the `Companion:` line and clears
`status.run.companion`, and its report says so. A merge request merged by
hand meanwhile is left as it is. Without a token for the companion (its
target is no longer valid) the report asks to close it by hand.

The implementer's prompt gets a "Companion repository" section: the
project, its checkout `/workspace/repos/<slug>` and branch, the allowed
paths (a change outside them, to CI configuration, or adding a symlink or
a submodule fails the run), that the companion lands only after the home
change is merged and released, and that the runner commits both trees.
claude is started with `--add-dir /workspace/repos/<slug>`. A cursor
implementer is moved to claude for such a run, see
[Fallback engine](reference/agents-and-models.md#fallback-engine). The system prompts
are the same with and without a companion.

**CI.** The [CI loop](guides/while-the-agent-works.md#ci-feedback) follows the companion's pipeline of
`status.run.companion.pushedSHA` too (the companion merge request's head
pipeline if its head is that commit, else the newest pipeline of the
commit), with the same rules: the failed jobs are retried once, a canceled
pipeline goes to review with a comment, none within `ci.noPipelineTimeout`
counts as no CI. The default-branch check is the home project's only. The
issue stays in `agent::working` until both trees are done, and the state is
in `status.ci.companion` (`sha`, `state`, `pipelineID`, `failedJobs`,
`since`). A red companion pipeline gets a CI fix run like a red home one:
`spec.source.ciFixProject` names the companion, and the fix prompt names its
pipeline and checkout and has its jobs and logs. Both trees share the fix
rounds (`ci.maxFixRounds`, counted in `status.run.ciRound`); if both are
red, home is fixed first. A companion still red after the rounds moves the
issue to `agent::needs-input`, with a comment naming the companion.

**Before the home merge.** On autopilot the [merge gate](guides/autopilot.md#the-merge-gate)
also needs the companion green on its pushed head, reviewed at that head,
open, and mergeable by GitLab's `detailed_merge_status`. It stays a draft
until it lands, so `draft_status` counts as mergeable (a draft with
conflicts does not). On `need_rebase` or a conflict the operator asks
GitLab to rebase the companion's merge request once per companion head
(`spec.source.companionRebase`); its rebased head becomes the pushed one,
keeps its review if the change hashes the same (like a rebase of the home
MR), and is checked by CI before the home merge. A red companion pauses
with [`ciRed`](guides/pauses.md#cired), an unreviewed one with
[`notReviewed`](guides/pauses.md#notreviewed), both naming the companion; anything else that
blocks it (closed, a conflict the rebase did not fix, an approval, open
discussions, someone pushed to its branch) pauses with
[`companion`](guides/pauses.md#companion). `/agent merge #N <sha>` skips the review checks
of both trees; CI and the companion's open and mergeable checks still apply.

#### Landing

Once the home merge request is merged (by autopilot or by hand), the
AgentProject's `status.companions` tracks its companion merge request until
it can land. The entry outlives the home issue's AgentTask, which is deleted
when the issue closes. It records the home issue, milestone, merge request
and merge commit (`homeMergeSHA`), the companion merge request and the head
the agent pushed (`headSHA`, with the hash of its change), the task's
`autoMerge` and owners, and the `state` with a `reason`. The operator checks
each entry once per poll. No model decides any of it:

1. **`waitingRelease`**: the home change must be **live**:
   - a home merge that touches only `docs/**` and `*.md` files is live at
     merge;
   - the operator's own project (`--self-project`, chart value
     `selfProject`): the running operator's version is at least the first
     release that contains the merge commit. That release is the smallest
     calendar tag (`YEAR.MONTH.N`, compared number by number) among the tags
     that contain it (`GET /projects/:id/repository/commits/:sha/refs?type=tag`).
     A build without a release version (`dev`) is never live, and the reason
     says so;
   - a project that tags its default branch (one of its 20 newest tags
     points to a commit created in the last 30 days, a merge commit or,
     for fast-forward and squash merges, a single-parent one): a tag contains
     the merge commit, other than the tag of an operator release that still
     builds (its tag pipeline has not succeeded, or failed);
   - any other project: merged is enough.

   `release` records the tag that made it live. A companion of a milestone
   on autopilot that waits here triggers a release of its home change by
   itself, see [releases](#releases).
2. **`rebasing`**: on `need_rebase` or a conflict, the operator asks GitLab
   to rebase the companion's merge request, **once per entry**
   (`rebasedSHA`). A head that changes after the home merge is taken over
   only if its change hashes as it did at the home merge (a rebase, also one
   by hand). Any other head is `blocked`: someone pushed to the branch.
3. **`waitingCI`**: the head needs a green pipeline (none within
   `ci.noPipelineTimeout` counts as no CI), and GitLab must report it
   `mergeable` (a draft, `draft_status`, counts).
4. **`ready`**, with `readyAt`. Then it is decided who merges it.

**Autopilot merges** a ready companion only when all of these hold at that
moment, not only at the home merge (an owner who removes `merge` or turns
`autoMerge` off in the meantime stops the merge):

- the issue's companion was set to be merged by autopilot (`autoMerge` in
  the entry, from `spec.companion.autoMerge`);
- the milestone still has the line `/agent repo <project> merge` (the
  operator reads the milestone again);
- the milestone still has the autopilot marker;
- the project is still a valid target with `autoMerge: true`.

Then the entry is **`merging`**: autopilot marks the merge request ready,
starts a merge request pipeline if its head has none (once, `pipelineStartedAt`;
none within `ci.noPipelineTimeout` counts as no CI) and waits for it to be
green. If GitLab starts no merge request pipeline for it (HTTP 400 or 422),
autopilot still waits `ci.noPipelineTimeout` for any pipeline of the head
before it merges without CI; if the bot may not start one (401, 403), the
owners merge it instead; a rate limit or a GitLab error is tried again on
the next poll. Then it merges with `sha` set to the checked head. It then follows the
companion's default-branch pipeline of the merge commit (`mergeSHA`, from
`mergeStartedAt` on): green, or none within `ci.noPipelineTimeout`, is
**`merged`** with `endedAt`. A red or canceled one makes the entry `blocked`,
which pauses the milestone with [`companion`](guides/pauses.md#companion); the pause comment
on the tracking issue mentions the owners. A merge GitLab refuses (the head
moved, not mergeable) goes back to the checks of step 3 once
(`mergeRefusals`); a second refusal blocks it until `/agent resume`. A head
that moved, a red head pipeline or a new merge status goes back through the
checks too. Marking the merge request draft again hands it to the owners.

**Otherwise the owners merge it.** The operator marks the merge request
ready and comments on it once (`notifiedAt`), mentioning the milestone's
owners (else the project's `notify.users` or the default owners). The
comment names the home merge request, the release that made the home change
live, and why autopilot does not merge it. The entry's `autoMerge` is
cleared, so a later change of the switches does not merge it either; such
an entry (`ready`, no `autoMerge`, `notifiedAt`) is what the Desk's
`companionReady` Inbox item is raised from. After 7 days still open, it
gets one reminder (`remindedAt`), no further one. Nothing is merged.

A failed rebase, a conflict or a `need_rebase` after the one rebase, a red
pipeline, an approval, unresolved discussions, or someone's push makes the
entry **`blocked`**, with the reason. On an autopilot milestone, the
milestone [pauses](guides/pauses.md#companion) with kind `companion`. The pause ends by
itself when no companion of the milestone is blocked any more (resolved or
rebased by hand, merged or closed, or its default-branch pipeline green on a
retry). `/agent resume` checks the blocked companions again and allows one
more rebase and merge each; a companion autopilot merged onto a red default
branch is recorded as `merged`. Outside autopilot, the companion's merge
request gets one comment per block, mentioning the owners.

A merge or close by hand at any point, also while `ready` or `merging`, is
recorded as `merged` (with `mergeSHA`) or `closed`, with `endedAt`, and ends
a pause about it. Ended entries are removed from the status 7 days later.

**The autopilot queue and companions.** The next issue does not wait for a
companion to land. The one exception: an issue with a companion on project
P (its `agent-repo::P` label under the milestone's line for P) does not
start while another companion on P from the same milestone is open, so two
changes to the same companion project land one after the other. The
tracking issue names the companion it waits for on the issue's line
(`waits for the companion my-group/infra/cluster!12 of #4 to land`). This is
a wait, not a pause: the issue starts by itself once that companion is
merged or closed. Only the open companion of a skipped issue, which never
lands without its home merge, can make the milestone pause as `stuck`;
merge or close it. Issues without a companion, or with one on another
project, are not held.

The milestone is **done only once all its companions have landed**. Until
then autopilot keeps running after the last issue merged and says once on
the tracking issue which companions it waits for
(`status.autopilot.closeWaitsForCompanions`). A done milestone is not
closed while one of its companions is open either. The retrospective lists
the milestone's companions with their merge request, state, release and
whether autopilot or a human merged them.

### Releases

A release is a decision with a reason, recorded in
`status.autopilot.releases` (newest 50, ended ones are dropped first). The
operator carries the decisions out on every poll, also while the milestone is
paused; the queue does not wait for them. Each release that runs is
commented on the tracking issue and carries release notes, see
[what is posted](#what-a-release-posts). (The other deciders come in later
changes of this milestone.)

Each decision is recorded in the history: `release.decided` when it is
made (who, why, after which issue), `release.started` when its pipeline
starts (pipeline, the head it runs on, issues and merge requests) and
`release.ended` when it ends (state, version, reason, release link). A
retried or resumed pipeline starts again, and a decision that ends again (a
failed final release ended without a release) ends again. The milestone's
snapshot in the research database keeps the decisions, so a closed
milestone still has them, and the Desk's milestone view (`releases`, and the
`version` of each issue) reads them from there once its tasks are gone; the
Pilot tab shows the three events. Decisions outside milestones (AgentProject
`status.releases`) get no history events.

**The planner decides first.** The planning run decides per issue whether to
release after it (`release` and `releaseWhy` in `status.plan`), and writes
the decision into the issue's description as a `**Release:**` line (see
[what an issue holds](guides/write-a-milestone.md)). On every poll the
operator syncs the plan into the records:

- An issue the plan releases after gets one record `by: planner`,
  `afterIssue`, `why` and `state: Planned`, unless a planner record after it
  already exists, in any state. Polling again or planning again never adds a
  second one.
- A re-plan that no longer releases after the issue, or that dropped the
  issue, makes a record that is still `Planned` `Dropped` ("the re-plan
  dropped it"). A record past `Planned` is never changed by a re-plan: once
  its issue is merged, only the pilot or an owner can drop it.
- When the issue merges, its `Planned` records become `Waiting` with the
  merge commit as `sha`, and the steps below carry them out. An issue merged
  without a known merge commit (closed as done by hand) leaves its record
  `Planned`.
- A record after a skipped issue stays `Planned`: if the issue is merged
  after all, its release follows; otherwise the milestone's final release
  covers it.

**The pilot decides next.** An answer or an assessment of the
[pilot](guides/pilot.md) may carry a release decision: after issue N,
add a release (`release: true`) or drop a planned one (`false`), with a
reason. The runner records it in the pilot task's
`status.pilot.releaseDecisions`; on every poll, with the commands of the
tracking issue, the operator handles those newer than
`status.autopilot.lastPilotRelease`, each once, in order. It validates each:

- N is an issue of this milestone's queue;
- the reason is not empty (it is folded to one line of 200 characters);
- it never touches the final release.

A valid decision is carried out:

- **Add** creates a record `by: pilot`, `afterIssue: N` and the reason as
  `why`: `Planned` while N is not merged, `Waiting` with N's merge commit as
  `sha` once it is. Nothing is added while a record after N has not ended or
  has released it ("already decided"); after a `Dropped`, `Failed` or
  `NotNeeded` one a new record is added.
- **Drop** makes the `Planned` and `Waiting` records after N that are not
  the final release `Dropped` ("dropped by the pilot: <reason>"), whoever
  decided them. A `Running` release is never stopped.

A refused decision changes nothing, and one comment on the tracking issue
says what was refused and why ("🛑 Refused the pilot's release after #42:
#42 is not an issue of this milestone"); a decision with nothing to do
(already decided, nothing to drop, the release already runs) is answered the
same way with "ℹ️". Either way it is handled: the watermark moves past it,
and the pilot sees the records in its next state.

**Owners decide too**, with two commands on the tracking issue (owners only,
also with `commandsFrom: members`, see [commands](reference/commands.md#on-the-tracking-issue)):
`/agent release [reason]` adds a record `by: owner`, `actor: @you`, `Waiting`
for the newest commit of the default branch (the SHA of its newest pipeline);
`/agent no-release #N [reason]` drops the `Planned` and `Waiting` records
after N that are not the final release ("dropped by @you: <reason>"). The
reason is optional and folded to one line of 200 characters.

**A waiting companion decides by itself.** A [companion](#landing) of the
milestone in `waitingRelease` whose home merge is not docs-only adds a
record `by: companion`, `afterIssue` its home issue, `Waiting` for the home
merge commit, with the why "<project>!<mr> waits for #N to be live". No
record is added while another one covers the home merge: a companion record
after that issue in any state (a `Failed` or `Dropped` one is not tried
again; the final release covers it), any record after that issue that has
not ended or has released, or a record that has not ended or has released
whose commit (`releasedSHA` once it runs) contains the home merge. Once a
tag contains the home merge, the companion lands on by itself. A companion
outside a milestone on autopilot adds its record to the project's list, see
[outside milestones](#outside-milestones).

**No contract with the project's CI:** the operator creates the release
itself through the API (`POST /projects/:id/releases`): a tag with the next
calendar version (`YEAR.MONTH.N`, counted on from the project's tags) on the
default branch and a GitLab release with the notes. Whatever the project's CI
runs for a tag (images, charts, packages, a deploy) is what the release
builds; a project without tag pipelines is released by the tag alone. A human
releases the same way, in the GitLab UI (*Deploy → Releases → New release*)
or with `glab release create <version> --ref main --notes "<why>"`; the
operator sees such a tag like its own. ai-operator itself builds in the
pipeline of its release tags (see [releasing.md](contributing/releasing.md)).

A decision is `Planned` until its commit is merged, then `Waiting`. Each
poll checks the oldest decisions first, in this order:

1. **Mode Off:** with `spec.workflow.release.mode: Off` the decision is
   `NotApplicable` ("release.mode is Off").
2. **Already released:** before anything is created, the operator asks
   whether a calendar-version tag contains the commit. If one does, the
   decision is `AlreadyReleased` with that version: a project that still
   tags on push, or a release a human created, is reported. If the tag is
   the version of a release the operator ran for this milestone, the
   decision is `Released` instead ("contained in 2026.11.3"); while that
   release still runs, the decision waits for it. The tag of a `Failed`
   release does not count.
3. **One at a time:** while one release of the milestone is `Running`, the
   others wait ("after the running release").
4. **Minimum interval:** a release starts at most once per
   `spec.workflow.release.minInterval` (default 15 m) after the last start,
   failed attempts included; the reason says until when. The milestone's
   final release is exempt.
5. **Green head:** the release tags the head of the default branch. Its
   newest pipeline must be `success`, and its commit must contain the
   decision's commit (the same commit, or a descendant). Until then the
   decision waits, with the reason. A default branch without any pipeline (a
   project without CI there) releases the decision's commit itself.
6. **Created:** the operator creates the tag and the GitLab release at that
   head. The decision is `Running`, with the version, the release's link,
   the head (`releasedSHA`), and the milestone's merged issues and merge
   requests that no earlier release lists. If the version's tag exists
   already (someone tagged meanwhile), the decision waits and the next poll
   counts on from that tag.

A `Running` decision follows the pipeline of its tag. Once it succeeded (or
was skipped), the decision is `Released`. Without a tag pipeline 10 minutes
after the release was created, the tag is the release: `Released`, with the
reason "no pipeline for the tag …". A tag pipeline that failed or was
canceled is a failed attempt.

**Retry once:** after a failed attempt the operator retries the tag pipeline
(never a new tag); the reason keeps the failure. After the second failed
attempt the decision is `Failed` with the reason, and the queue goes on.

A GitLab error ends the poll; the next poll tries again.

#### The final release

The milestone's last release is the operator's, with no model involved. Once
every issue of the queue is through, autopilot is not done before a release
contains the milestone's last merge. The order is: final release, then the
[companions](#companion-repository), then done, then the
[retrospective](guides/pilot.md#the-retrospective).

- A milestone that merged nothing (or only issues closed as done by hand,
  without a merge commit) has no final release.
- Otherwise there is one record `by: final`, `final: true`, `why: "the
  milestone's last merge"`, `afterIssue` and `sha` of the issue merged last
  (`mergedAt` of its queue item). It is created once; planning again does not
  add a second one. If another issue merges after it was created (a re-plan
  added one, a revisit follow-up), it moves to that newer merge, as a fresh
  decision, unless its pipeline is running.
- **Not needed:** when the record is created and no tag contains the commit
  yet, the operator compares the newest calendar-version tag (the highest
  version) with the commit. An empty change gives `NotNeeded` ("no change
  since 2026.11.3"), a change of only `docs/**` and `*.md` files
  ([landing's rule](#landing)) gives `NotNeeded` ("nothing release-relevant
  since 2026.11.3"): no pipeline. A project without calendar-version tags is
  left to the steps above, which then record `NotApplicable` or release.
- While the record has not ended, the milestone keeps running, and the
  tracking issue says "every issue is through; done once a release contains
  the last merge". The final release does not wait for the minimum interval.
- Once it is `Released`, `AlreadyReleased`, `NotNeeded`, `NotApplicable` or
  `Dropped`, the records still `Planned` (their issue was never merged) are
  `Dropped` ("its issue was not merged"), and autopilot goes on to the
  companions and done.
- A final release that failed twice pauses the milestone with
  [`pauseKind: release`](guides/pauses.md#release), with the reason. Its ways out:
  - By itself, once a tag contains the commit (released by hand): the record
    is `AlreadyReleased` with that version and autopilot is done.
  - `/agent resume`: the final release starts over: its tag pipeline is
    retried as a first attempt (one retry again); without a tag yet it is
    `Waiting` with attempts back to 0 and the same checks.
  - `/agent endMilestone` (owners only): the record is `Dropped` ("ended
    without a release by @you") and autopilot is done.

The green-head check looks at the default branch's own pipelines only;
pipelines recorded for a release are skipped, so they never hold a later
release back.

#### Outside milestones

Merges outside milestones (Renovate, a human's merge request) are released
by the operator too. Their decisions are kept in the AgentProject's
`status.releases` (newest 20, ended ones are dropped first) and carried out
with the same steps as above, after the companions and main's health on every
full poll. Two deciders add records there, and only while **no autopilot of
the project is `Running` or `Paused`**: the milestone's releases, its final
one last, cover everything merged meanwhile. While one runs, a record that is
already `Running` is followed to its end and every other one waits.

- **A waiting companion:** a [companion](#landing) in `waitingRelease` whose
  home merge is not docs-only adds a record `by: companion` at once,
  `afterIssue` its home issue, `Waiting` for the home merge commit ("<project>!<mr>
  waits for #N to be live"). Not while another record of the list has not
  ended (the next poll adds it, if that release did not contain it), and only
  once per home merge: a `Failed` one is not tried again, the sweep covers it.
- **The sweep:** when the project tags its default branch
  ([landing's rule](#landing)) and no record of the list has not ended, the
  operator compares the newest calendar-version tag with the default branch.
  If the change is release-relevant (not empty, not only `docs/**` and
  `*.md`), its oldest commit landed more than
  `spec.workflow.release.maxUnreleasedAge` ago (default 24 h), and
  [main's health](guides/while-the-agent-works.md#main-health) is green (or flaky) on the head, it adds a
  record `by: sweep`, `Waiting` for the head: "3 commits since 2026.11.4 are
  unreleased, the oldest since 2026-11-09 06:00 UTC (more than 24h)". A
  commit lands when it reaches the default branch: the first parents from the
  head count (merge commits, squashed or rebased commits), not the older
  commits of a merged branch. A docs-only commit among code commits counts
  too, so such a release may come earlier, never later. There is one sweep
  decision per head: one that `Failed` is not tried again until another
  merge moves the head. With
  `mainHealth.disabled` the green-head step alone decides.

So a Renovate merge outside a milestone is released within a day. These
records are not commented anywhere (there is no tracking issue) and get no
history events; their release notes say why, without a list of issues.

#### What a release posts

**On the tracking issue**, one comment when a release starts to run and one
when that run ends. The retry after a failed attempt is the same run; a
final release started over with `/agent resume` is a new one.

- Started: "🚀 Release started (planner): <why>. Contains #1, #2. Release
  2026.11.3: <link>". An owner's release names the owner.
- Released: "📦 Released 2026.11.3: <link to the GitLab release>".
- Failed after the retry: "❌ The release failed twice: <reason>. The queue
  goes on." The final release says "the milestone is not done without it"
  instead, and the [release pause](guides/pauses.md#release) follows.
- The final release's end is commented also when nothing ran, since it
  decides when the milestone is done: "📦 Already released in 2026.11.3
  (…)", "📦 No release needed: nothing release-relevant since 2026.11.2",
  "📦 No release: release.mode is Off". A final
  release released by hand while paused is said by the resume comment
  only. A dropped decision and one that waits are not commented; a
  decision that a release of another one contains says so in its record.

The record's `announced` is the state last commented on: a comment that
fails is posted on the next poll, and none is posted twice.

**The release notes** (the description of the GitLab release the operator
creates; the project's tag pipeline may add to it) are markdown:

```markdown
## Why

The planner released after #2. Reason given by the planner: users see the new API.

## Contains

- #1 API for checkout (!11)
- #2 Checkout page (!12)

Milestone: [Checkout v2](https://gitlab.example/group/app/-/milestones/3)
```

The final release says "The operator released after #2: the end of
milestone Checkout v2.", an owner's "@alice released: <why>". The list
holds the issues and merge requests the release contains (`issues` and
`mergeRequests` of the record).

The planner's and the pilot's reasons and the issue titles are agent text
and stay untrusted: each is folded to one line and cut to 200 characters,
mentions and quick actions are defused (a zero-width space after `@` and
before `/`), the characters of links, images, HTML and code
(`` \ [ ] ( ) < > ! ` ``) are escaped with a backslash, and URLs are broken
(a zero-width space after `://` and in `www.`), so `@all`,
`[x](http://evil)` or `<img>` render as plain text. The reason is labelled
as the planner's or the pilot's. The notes are at most 4000 bytes, valid
UTF-8: the list is cut at whole lines and ends with "… and N more issues".

## Watching from Kubernetes

```sh
kubectl -n agents get agentprojects
kubectl -n agents get agenttasks -w       # type, issue, phase, current step, last result, runs, cost
kubectl -n agents get agenttasks -o wide  # + merge request URL
kubectl -n agents get agenttask shop-42-add-paypal-button -o jsonpath='{.status.run}'
kubectl -n agents logs -f shop-42-add-paypal-button -c runner   # every step of a run
kubectl -n agents logs -f shop-42-add-paypal-button -c agent    # agent server: CLI runs, git calls, the agent's thinking and tool calls
```

`status.run` of an AgentTask holds the result of the last run: `lastResult`,
`summary`, `costUSD` (accumulated), `branch`, `mergeRequestURL`, `pushedSHA`,
`currentStep` while a run is in progress, and the result of the last review:
`blockingFindings` (blocker and major findings left), `reviewErrors`
(reviewers that failed), `reviewErrorsTransient` (all of them failed
transiently, in an outage), `reviewErrorKinds` (per failed reviewer:
`usageLimit` with the `engine` and `resetAt`, `transient` or `other`) and
`reviewedSHA` (the commit that review saw; a
CI fix run can push commits after it). `reviewedFor` is the head the last
re-review run was asked for (`spec.source.reviewFor`).

The spec task of a milestone holds the plan in `status.plan` (issues in plan
order with their dependencies, the dependencies the operator linked, and the
planner's release decision `release` with its reason `releaseWhy`) and, on autopilot, the queue in
`status.autopilot`: `phase`, `pausedReason`, `pauseKind` (the cause of a
pause, e.g. `findings`, `reviewErrors`, `ciRed`, `mergeRequest`, `owner`),
`trackingIssueIID` and one item
per issue with its state, MR, merged commit and decisions (`revisitOf` for a
follow-up). `status.revisits` tracks the [revisits after the
merge](guides/iterate-and-finish.md#change-a-decision-after-the-merge): when watching started, the last check
and the last handled comment per issue.

```sh
kubectl -n agents get agenttask shop-spec-m3-checkout-v2 -o jsonpath='{.status.autopilot}'
```

### Run logs

Every run is a numbered list of steps, e.g. for an issue:

1. read issue
2. read milestone
3. read project
4. clone repository (`fetch repository` once the workspace exists)
5. prepare checkout
6. find merge request
7. read comments
8. read failed CI jobs (CI fix runs only)
9. merge `<default branch>` (conflict runs only)
10. run implementer (skipped when a conflict run merged without conflicts)
11. conclude merge (conflict runs with conflicts only)
12. commit
13. review round 1, fix round 1, commit, review round 2, … (up to
    `review.maxIterations`)
14. push
15. update agent notes, post decisions (when there are any)
16. open merge request (first push only)
17. comment
18. update issue labels

A spec run reads the milestone and the project, clones or fetches, lists the
milestone's issues, runs the spec agent, applies the plan, records it
(`status.plan`, used by autopilot) and links the dependencies.

The steps show up in three places:

- **The runner log** has a `step started` and a `step done` (or `step
  failed`) line per step, with the duration and the result. Each line carries
  `run=<task>#<run request>`, and so do the agent container's lines, so you
  can filter both logs by run. The `run finished` line lists all steps with
  their durations.
- **`kubectl get agenttasks`** shows the step in progress in the `Step`
  column (`status.run.currentStep`).
- **GitLab:** while a run goes on, the MR's progress comment shows the run log
  so far, open, with the step in progress. The agent's report on the MR then
  ends with a collapsed **Run log** table that lists the steps up to that
  comment, with their durations, results and the cost so far. A failure
  comment on the issue includes it too, with the failed step marked.

Comment bodies and diffs are never logged, and prompts only with
`spec.agents.logPrompts: true` (for the web UI's live transcript). `spec.agents.logLevel:
debug` (or the operator's `--agent-log-level`) adds the runner's git commands.

**The agent's session** is in the agent container's log as it happens,
tagged with the run like the other lines:

```
agent thinks       text="The endpoint needs a test first."
agent says         text="Adding the test."
agent uses a tool  tool=Bash input="go test ./..."
agent uses a tool  tool=Edit input="/workspace/repo/api.go"
```

Thinking shows up when the model thinks (depending on model and
`effort`; the CLI may summarize it). Tool output, the contents of edits and
the agent's final answer (posted on GitLab anyway) are not logged; commands
are logged by their first line, without heredoc contents, and unknown tools
only by the names of their arguments. What the agent writes in its own
thinking and messages can still quote code. `logLevel: error` turns these
lines off.

cursor-agent runs show the same messages and tool calls, with Cursor's tool
names (`read`, `write`, `run_terminal_cmd`, …), but no thinking: the CLI keeps
it to itself in print mode.

### When the agent container dies

The agent container runs the agent CLI and everything it starts: builds,
test runners, dev servers. If it uses more memory than its limit, the kubelet
kills it (`OOMKilled`) and starts it again in the same pod. The operator
records how the agent container last stopped in `status.agentTermination`
(reason, exit code, time, restarts, memory limit), and the runner, which lost
its call to the agent, waits for the container to be back and reports it:

- **Out of memory:** the operator raises the memory limit of that task's
  agent container by half (a memory request grows in proportion), at most to
  `spec.agents.maxMemory` (default: twice `resources.limits.memory`), and the
  run starts again in a new pod with it. The issue stays in
  `agent::working` and gets a comment: "The agent container ran out of
  memory after 1h28m and was killed (OOMKilled, memory limit 4Gi). The run
  starts again with 6Gi (restart 1 of at most 3)". The raised limit stays
  with the task for its later runs (`status.agentMemoryLimit`); lowering
  `maxMemory` or the limit lowers it too. If the killed call was the
  implementer's and the volume still holds its files, the new pod first
  saves its uncommitted edits as a checkpoint (`chore(wip): checkpoint
  #<issue> step <n> (oom-kill)`, see
  [When the whole pod is lost](#when-the-whole-pod-is-lost)), and the run
  starts again from it. If nothing can be saved (see [When a run is cut
  off](guides/when-runs-go-wrong.md#when-a-run-is-cut-off)), the run starts again from the last pushed
  commit. If no node has room for the raised memory, the task's status says the pod cannot be
  scheduled (`kubectl get agenttasks -o wide`).
- **Out of memory at the cap** (or after 3 restarts, or without a memory
  limit to raise): the run fails with that reason, and the issue is
  blocked (`agent-blocked::infra`, see
  [When the whole pod is lost](#when-the-whole-pod-is-lost) for the
  automatic retry). Raise `spec.agents.resources` or
  `spec.agents.maxMemory`, then comment `/agent retry`. A pod started with
  other agent resources is replaced between runs, so the retry gets the new
  limit.
- **Another reason** (a crash): an implementer call's uncommitted edits are
  saved as a checkpoint (`chore(wip): checkpoint #<issue> step <n>
  (container-restart)`, pushed without CI, the MR set to draft), then the
  checkout is reset to the last commit, which is that checkpoint, so the
  call is tried once more with its files; if it loses the container again, the run
  fails with the reason and exit code, and the issue is blocked
  (`agent-blocked::infra`).
- A call made while the container was down (it died between calls, e.g.
  from a background process) is made again once it is back.

On nodes with cgroup v1, or with the kubelet's `singleProcessOOMKill`, the
kernel kills only the biggest process (a test runner, the CLI) instead of the
whole container. The container keeps running and the agent CLI simply
reports an error, so nothing is recorded.

```sh
kubectl -n agents get agenttask shop-42-add-paypal-button -o jsonpath='{.status.agentTermination}'
```

### When the whole pod is lost

If the pod stops mid-run (evicted, the node shut down or went away, or the
runner itself crashed), the run request stays open. The operator replaces a
pod that has terminated for good, and the new pod's runner starts the run
again from the last pushed commit. If the lost pod was in an implementer
call (the runner notes that in its state on the volume) and the volume still
holds the workspace, the new pod first saves what that call left, before it
resets the workspace: uncommitted edits become a checkpoint commit
(`chore(wip): checkpoint #<issue> step <n> (pod-lost)`, its body taken from
the agent's last todo list or message), pushed without CI with the MR set to
draft, together with step commits the lost pod made but had not pushed. The
run then starts again from that commit. Nothing is saved, and the run starts
from the last pushed commit, when checkpoints are off, the workspace is on
another branch, or someone pushed to the branch after the lost pod's last
push (see [When a run is cut off](guides/when-runs-go-wrong.md#when-a-run-is-cut-off)). A progress comment the lost attempt left on the merge request is
removed. The new attempt's progress comment and report say so above the run
log: "↻ Started again: the previous attempt was interrupted (its pod stopped
before the run finished, e.g. evicted or the node went down). Restart 1 of
at most 3." Agent pods use a local volume, so on a node that is down the new
pod waits until the node is back.

After 3 restarts of the same run request (`status.run.interruptions`) the
runner gives up instead of trying again: something in the run may keep
taking the pod down. The run fails, and an issue moves to `agent::blocked` +
`agent-blocked::infra` (as for a lost agent container), not
`agent::needs-input`: the cause is outside it.

- After `spec.lifecycle.infraRetryAfter` (default 30m) the operator starts
  one new run by itself; the issue shows `agent::working` again. The
  failure comment says when.
- If that run fails the same way, the issue stays blocked and waits for
  you; that comment says so and mentions the owners (event `blocked`, also
  on autopilot). There is one automatic retry per run request, so
  `/agent retry` (or a move to `agent::ready`) starts a run that gets its
  own retry.
- `/agent …` or a move to `agent::ready` lifts the block at once and runs.
  On autopilot, the gate waits while the issue is blocked, and
  `/agent resume` does not lift such a block: only a new run does.

`/agent retry` starts the run again with a new count. A spec run that gives
up this way is planned again once too, like any [planning failure for an
outside reason](guides/write-a-milestone.md). A
run that finishes resets the count. Suspending a task (`spec.suspend`)
mid-run stops its pod too, but does not count: the run starts again when the
task is resumed.

Task phases: `Pending`, `Queued` (waiting for `maxConcurrentRuns` or for
a task that ranks ahead, see [several milestones](guides/autopilot.md#several-milestones-at-once);
pilots never queue), `Running`, `Waiting` (paused by a usage limit), `Idle`,
`Hibernated`, `Archived`, `Blocked` (hit `maxRunsPerTask`).

To force a task to stop its pod, set `spec.suspend: true` on the AgentTask. To
stop all activity for a project, set `spec.suspend: true` on the AgentProject.
