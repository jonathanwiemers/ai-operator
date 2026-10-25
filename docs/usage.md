# Using the ai-operator from GitLab

Every `/agent` command is listed in [commands.md](commands.md).

Everything happens in GitLab. You write specs, answer questions, move cards
and review merge requests. The operator turns those actions into agent work.

## One-time setup in GitLab

Create these labels. Scoped labels (`::`) make sure an issue only ever has one
of each kind.

| Label | Set by | Meaning |
|-------|--------|---------|
| `agent::draft` | spec agent | sub-issue planned, waiting for you |
| `agent::ready` | you | hand the issue to the implementing agent |
| `agent::working` | operator | an agent run is requested or running |
| `agent::needs-input` | agent | the agent has questions, or the run failed |
| `agent::review` | agent | merge request is ready for your review |
| `agent::blocked` | operator | the issue cannot go on for an outside reason; nothing to answer |
| `agent-blocked::<reason>` | operator | why it is blocked: `infra`, `main-red`, `quota` or `dependency` |
| `agent::done` | you | finished; pod and workspace are deleted |
| `agent-profile::<name>` | spec agent / you | which agent profile implements the issue (e.g. `agent-profile::fast`, `agent-profile::strong`) |
| `ai-generated` | spec agent / operator | issue was created by the spec agent or the operator |
| `ci::main-red` | operator | the issue that reports a red default branch, see [main health](#main-health) |
| `ci::flaky` | operator | the issue that collects the failures of one flaky CI job |

Put `agent::draft`, `agent::ready`, `agent::working`, `agent::needs-input`,
`agent::review`, `agent::blocked` and `agent::done` on an issue board as
columns. An issue moves
through them like this:

```mermaid
stateDiagram-v2
    state "agent::draft" as draft
    state "agent::ready" as ready
    state "agent::working" as working
    state "agent::needs-input" as needs
    state "agent::review" as review
    state "agent::blocked" as blocked
    state "agent::done" as done

    [*] --> draft: spec agent plans the issue
    [*] --> ready: you hand over your own issue
    draft --> ready: you, after answering the questions
    ready --> working: operator starts a run
    working --> review: MR pushed, CI green
    working --> needs: questions or CI red
    needs --> working: /agent or ready
    review --> working: /agent comment
    review --> done: you merge and close
    working --> blocked: outside reason
    review --> blocked: outside reason
    blocked --> review: reason gone
    blocked --> working: /agent or ready
    done --> [*]: task, pod and workspace deleted
```

You act on `draft`, `needs-input` and `review`. Everything in between,
including fixing a red pipeline, happens without you.

**`needs-input` or `blocked`?** `agent::needs-input` means a human has to
answer or decide something. `agent::blocked` means the issue waits for
something outside it, such as an engine outage or a red default branch.
The `agent-blocked::<reason>` label says what it waits for. The operator sets
both labels and remembers the label they replaced. When the reason is gone,
that label comes back. You can also lift a block yourself: move the card to
`agent::ready` or comment `/agent …`, and the issue gets a run as usual.

`hack/gitlab-board.sh` creates that board, named **Agents**, with one list per
label in this order. Moving a card between columns changes its label, so
dragging a card from draft to ready hands the issue to the agent. A group
board shows the issues of every project in the group on one page:

```sh
# group labels (one set for every project in the group), then the group board
GITLAB_GROUP=my-group GITLAB_TOKEN=glpat-… hack/sandbox/gitlab-setup.sh
GITLAB_GROUP=my-group GITLAB_TOKEN=glpat-… hack/gitlab-board.sh

# or a board for a single project
GITLAB_PROJECT=my-group/shop GITLAB_TOKEN=glpat-… hack/gitlab-board.sh
```

Run both with your own token (`api` scope, *Maintainer* on the group or
project): creating group labels and boards needs more than the bot's
*Developer* role. `GITLAB_GROUP` must be a group, not your personal
namespace. The board script stops if a label is missing. It never removes,
renames or reorders boards and lists: missing lists are added at the end, and
where a second board is not allowed (group boards on GitLab Free) it adds
them to the existing board. Running it again changes nothing.

If projects in the group already have the `agent::*` labels as project
labels, promote them to group labels first (project > Manage > Labels >
Promote, which merges the same-named labels of all projects in the group).
Otherwise each name exists twice and the group board does not show those
projects' issues. `gitlab-setup.sh` with `GITLAB_GROUP` stops when it finds
such labels, and `gitlab-board.sh` warns about them.

Create a **project access token** with role *Developer* and the scopes `api` and
`write_repository`. Protect your default branch. The agent only pushes
`agent/*` branches and opens draft merge requests.

## 1. Write the milestone spec

Create a milestone. Its description is the overarching spec for the whole
feature. Add the marker line `/agent spec` anywhere in the description.

```markdown
/agent spec

## Goal
Customers can pay with PayPal in the new checkout.

## Requirements
- ...
```

Within a poll interval (default 1 minute), a spec pod named like
`shop-spec-m3-checkout-v2` starts. It reads the spec and the repository, then
creates one **sub-issue per merge request** in the milestone. Each sub-issue
holds a self-contained sub-spec:

- the part of the milestone spec it implements, context and implementation
  plan (files, functions)
- acceptance criteria and a test plan
- `Depends on #…` for ordering, also as [issue links](#dependencies-as-issue-links)
- **Open questions**, each with the default the agent would pick
- labels `ai-generated`, `agent::draft` and the chosen `agent-profile::…`

The milestone description is never modified. If you edit the spec later, the
spec agent runs again. It updates sub-issues that are still drafts and leaves
issues that are in progress, done, or created by hand alone.

### Dependencies as issue links

The planner's dependencies are also GitLab issue links, so the issue page and
the boards show them:

- On **GitLab Premium/Ultimate** an issue is linked as **is blocked by** the
  issues it depends on, and GitLab shows the blocked badge.
- On **GitLab Free** blocking links do not exist; the operator falls back to
  **relates to** links, which have no direction. The `Depends on` list in the
  description stays the record of the order.

A re-plan adds links for new dependencies and removes the links it made for
dependencies it dropped, on drafts only. Links you made are never removed,
and a link of the plan you remove stays removed. On autopilot, an **is
blocked by** link you add (Premium) holds an issue back until the linked
issue is closed, even if the plan does not list it or it is in another
project, so you can order or block work right on the issue. The run log's
`link dependencies` step says which link type was used.

## 2. Answer questions and hand the issue over

Answer the questions as comments on the issue. Check the profile label: change
`agent-profile::fast` to `agent-profile::strong` if the issue is harder than
the planner thought, or the other way round. Then move the issue to
**`agent::ready`**.

You can also hand over issues you wrote yourself. Just add `agent::ready`
(and optionally a profile label; without one, `agents.implement` is used).

## 3. The agent works

The operator moves the issue to `agent::working`, assigns the bot user if
nobody is assigned (see [Watching progress](#watching-progress)) and starts a
pod named like `shop-42-add-paypal-button`. In that pod:

1. On the first run, the **draft MR** `Closes #42` is opened right away, in
   the issue's milestone, before the agent changes anything. Its description
   holds a placeholder until the first push is described (step 7). The branch
   `agent/42-add-paypal-button` starts with an empty "Start work on #42"
   commit pushed with `ci.skip`, so no pipeline runs for it (the message has
   no `[skip ci]`, which a merge commit template could copy to the default
   branch).
   The issue gets a comment linking the MR; from then on every run reports
   there.
2. While a run goes on, the MR has a **progress comment** with the run log so
   far: each step, its time and result, and the step in progress. It is
   edited as steps start and end (at most every 3 seconds) and replaced by the
   run's report when the run ends. A run that fails or waits for a usage limit
   leaves it with its run log and the reason.
3. The implementing agent (engine and model from the issue's profile) works on
   the branch. It follows your comments, runs the tests, and stops with
   questions if a design decision is unresolved. While the MR is a draft it
   works in **steps** (`workflow.stepCommits`): after each piece that is worth
   a commit of its own, the step is committed and pushed with `ci.skip` (no
   branch pipeline; merge request pipelines still run where your rules allow
   them for drafts), and the agent continues. Comments you write on the issue
   or the MR in the meantime are handed to it before its next step, so you
   can steer while it works; they need no `/agent` prefix. A `/agent …`
   command is not handed over: it starts its own run after this one, and the
   comments after it wait for that run too. At most 25 steps per run; a run
   that runs out of steps leaves the MR a draft and says so.
4. The **bugbot** and **security** reviewers check the diff in parallel. A
   reviewer that fails because its engine's service is briefly unavailable
   (for example cursor-agent's `RetriableError: [unavailable]`, a reset
   connection, a 502/503/504) is called again after 30 seconds, then after 2
   minutes (`review.retries`, default 2). Other failures, and usage limits,
   are not retried; the report says how many attempts a failed reviewer had.
5. Blocker and major findings go back to the implementer (at most
   `review.maxIterations` times). Remaining findings are listed for you.
6. **If the review is clean**, the MR is marked **ready** (its `Draft:` prefix
   is removed) before the push, so the push gets its pipeline even where
   pipelines skip draft MRs. If the run leaves blocker or major findings, a
   reviewer failed, or the agent asks questions, the MR goes back to draft.
   An MR with only the start commit stays a draft, and so does an MR you
   marked draft yourself after a run left it ready: that is your hold, mark
   it ready to release it. Without reviewers configured, every
   run with changes marks it ready; CI fix runs off autopilot are not
   reviewed and leave the draft state as it is. A clean run that has nothing
   new to push starts a merge request pipeline for the head it marked ready.
7. The branch is pushed. If the diff changed since it was last described,
   the **describe** agent (`agents.describe`) rewrites the **MR
   description**: Why, What, How, Verification and, when it helps, a mermaid
   diagram of the change at the bottom. It describes the whole branch, not
   the run, and only rewrites its own part (between hidden markers): text
   you add above or below it, and `Closes #42`, stay. Then the report is
   posted on the MR; the first run's report points to the description
   instead of repeating it, later reports say what that run changed. If the
   agent has questions, they are also posted on the issue.
8. The agent's decisions are posted on the issue (see
   [Decisions](#decisions)).

If the agent has questions, the issue moves to **`agent::needs-input`**.
Otherwise it stays in `agent::working` until the MR pipeline has run; failing
jobs (lint, tests, …) go back to the agent (see [CI feedback](#ci-feedback)),
and the issue moves to **`agent::review`** once the pipeline is green.
After a clean run "review" means: reviewed, tests and CI pass, the MR is
ready, a human can look and merge. A draft in review still has findings the
run report lists (and, where pipelines skip drafts, no CI). On
[autopilot](#autopilot-a-milestone-end-to-end) the operator merges it.

So an issue's MR goes: draft MR → code → review → ready → pipeline → CI fixes
if needed → merge (by you, or by autopilot).

### Owners and pings

The operator mentions the **owners** (`cc @alice @bob`) in the comments that
need a human, so GitLab sends them a mail and a To-Do:

| When | Where |
|------|-------|
| a run failed | issue |
| the agent asks questions | issue (with the questions) |
| the agent could not finish alone (e.g. unresolved conflicts) | MR report |
| CI is still red after the fix rounds (not on autopilot) | issue |
| the issue reached review (not on autopilot) | MR report, or "Ready for review" on the MR after CI; the owners also become the MR's reviewers if it has none |
| autopilot paused or is done | tracking issue |
| the default branch stayed red after a retry | the new `ci::main-red` issue (project owners: `spec.notify.users` or the default) |

Only new comments carry the mention, never the progress comment that is
edited while a run goes on, and each commit pings for review once. On
autopilot, failed runs, questions and red CI ping through the pause on the
tracking issue instead; while paused, a new reason for the active issue (say
the review is fixed, but CI is red now) is announced, and pinged, again. The owners are, most specific first:

1. a line in the milestone description, for the milestone and its issues:

   ```
   /agent owners @alice @bob
   ```

   An `/agent owners` line without names turns the pings off for the
   milestone. Adding or changing the line does not re-plan the milestone.
2. `spec.notify.users` of the AgentProject;
3. the operator's default (Helm value `defaultOwners`).

`spec.notify.events` picks the situations, `spec.notify.requestReview: false`
keeps the reviewers to you (see
[configuration.md](configuration.md#specnotify)).

## CI feedback

After every push, the operator follows the pipeline of the pushed commit:

| Pipeline | What happens |
|----------|--------------|
| running | the issue stays in `agent::working` |
| green (also: waiting for manual jobs) | `agent::review` |
| red, first time | the failed jobs are retried once, to rule out flaky jobs |
| still red, the same jobs also fail on the default branch | `agent::review` with a comment: not caused by this change |
| still red | the agent gets the failing jobs and the end of their logs and fixes the cause (a "CI fix" comment on the MR) |
| still red after 2 fix rounds | `agent::needs-input` with the failing jobs |
| canceled | `agent::review` with a comment |
| no pipeline within 10 minutes | `agent::review` (the project has no CI for this commit); a skipped pipeline (a push with `ci.skip`) counts as none |

The fix rounds count from the last push you triggered (`/agent …` or moving
the issue to ready), so every new instruction gets two fresh rounds. CI fix
runs skip the reviewer agents (except on [autopilot](#autopilot-a-milestone-end-to-end)).
Job logs reach the agent as untrusted data.

If you push to the agent's branch yourself and then comment `/agent …`, a run
that has nothing to add takes your commits over: your head counts as the
pushed commit, and CI is followed for it like for the agent's push.
Configure the loop in `spec.ci` (see [configuration.md](configuration.md));
`ci.disabled: true` restores "review right after the push".

The loop follows the MR's head pipeline if the MR's head is the pushed
commit, which covers merged-results and merge request pipelines, and
otherwise the newest pipeline of the commit. The MR is marked ready before a
clean run pushes, so pipelines that skip draft MRs run too. A push of a run
that left problems is a draft; if your pipelines skip drafts, it gets no
pipeline and moves to review after `ci.noPipelineTimeout`. Autopilot never
merges such an MR without CI: its gate pauses on the findings, and an MR whose
head had no pipeline is marked ready if needed and gets a pipeline first (see
[the merge gate](#the-merge-gate)).

While the issue is in `agent::review`, the operator keeps watching the MR's
head pipeline. GitLab starts no pipeline when a draft is marked ready, but one
started afterwards (by you, by autopilot, or by a run that marked the MR
ready without a push) or a canceled pipeline you retry is followed: while it runs or once it is red the issue goes back to
`agent::working`, and the loop above takes over: retry, CI fix run, review.
One that is already green when the operator first sees it counts as green
right away and the issue stays in review. So a job such as "add a changelog
entry" that only runs for ready MRs gets fixed by the agent too, as far as
code can fix it: the agent can add the file the job asks for, but it cannot
set labels on the MR.

## Main health

The operator also watches the default branch: every `mainHealth.interval`
(5 minutes) it reads the newest finished pipeline of the default branch
(skipped and canceled ones are passed over), and the jobs only when that
pipeline is new. Jobs that are allowed to fail don't count. A long failure
list shows the first 3 jobs and their logs.

| Default branch pipeline | What happens |
|-------------------------|--------------|
| green | nothing; an open `ci::main-red` issue gets "✅ green again at `<sha>`" and is closed |
| red, first time | its failed jobs are retried once |
| green after the retry | flaky: one issue per job, "🔁 Flaky CI job: `<job>`" (labels `ai-generated`, `ci::flaky`), opened on the first failure and commented with the pipeline on every later one (the first 3 jobs of a pipeline) |
| still red after the retry | an issue "🚑 `main` is red: `<jobs>`" (labels `ai-generated`, `ci::main-red`) with the pipeline, the commit, the failing jobs and the last 40 lines of each job's log; it mentions the project's owners |
| red again, the same jobs | nothing new |
| red again, other jobs | a comment on the `ci::main-red` issue, and its title follows |

The operator finds its issues again by a hidden marker, also after the
AgentProject was recreated; issues a human opens with these labels are left
alone. Job logs are untrusted: mentions and quick actions in them are made
inert, and job names in titles keep only letters, digits and ` :._/-`, at
most 60 characters. Close a flaky issue once the job is fixed; the next
failure opens a new one.

The state is in the AgentProject status:

```bash
kubectl get agentproject shop -o jsonpath='{.status.mainHealth}'
```

`state` is `green`, `retrying`, `red` or `flaky`, with the pipeline, commit,
failing jobs and the open `ci::main-red` issue. Configure it in
`spec.mainHealth` (see [configuration.md](configuration.md#specmainhealth));
`mainHealth.retryFailedJobs: false` reports a red pipeline right away. The
watcher does not hold merges yet.

## Merge conflicts

When the default branch moves on and the MR of an issue in `agent::review`
gets conflicts, the agent resolves them:

1. The operator sees the conflict on its normal poll (GitLab's
   `has_conflicts` / `detailed_merge_status`) and moves the issue back to
   `agent::working`.
2. The run merges the default branch into the agent's branch (a merge commit,
   no force push, so your checkout of the branch stays valid).
3. The conflicted files go to the implementer with their conflict markers. It
   resolves them, keeping the intent of both sides, checks that the change
   still fits the new code, and records decisions for resolutions that are not
   obvious. The runner concludes the merge only if no conflict marker is left.
4. The result is reviewed and pushed, CI runs, and the issue returns to
   `agent::review`. The MR comment says which files had conflicts.

If the merge has no conflicts after all, the implementer is not run; the
merge commit is reviewed and pushed. If the agent cannot resolve a conflict
(markers left, or it asks a question), the merge is undone, nothing is
pushed, and the issue goes to `agent::needs-input` with the files. The
operator asks once per MR head: if the new head conflicts again, because the
default branch moved again, it asks again.

Set `workflow.resolveConflicts: false` to leave conflicts to humans.

## 4. Iterate

| You do | Effect |
|--------|--------|
| comment `/agent <instructions>` on the issue or the MR | another run; the agent resumes its session and gets your new comments |
| move the issue back to `agent::ready` | another run (e.g. after answering questions) |
| discuss normally (no `/agent` prefix) | nothing; no tokens are spent (during a step run on a draft, the agent reads it before its next step) |
| push to the agent's branch yourself | the next run continues from your commits |
| change the profile label | the next run uses that profile |
| comment `/agent use claude opus` (or set `agent-model::claude/opus`) | the next runs use that engine and model; the comment alone starts no run |
| comment `/agent effort high` (or set `agent-effort::high`) | the next runs think that hard (claude, at most `agents.maxEffort`) |

`/agent` must be the first word of the comment. `/agents are great` does not
trigger a run. All commands are listed in [commands.md](commands.md).

### Decisions

Every run records the decisions the agent made that you might want
differently: data shapes, names users see, libraries, error handling, scope
cuts. Small mechanical choices are not decisions. They are posted in one
comment per run on the issue:

> 📌 **Decisions** in run `shop-42#3`. Change one with `/agent revisit <id> <what you want instead>`.
>
> #### D42-3: How tags are stored
> **Question:** How are tags normalised?
> **Chosen:** lowercase
> **Why:** `store.js` already lowercases slugs, so search stays case-insensitive.
> **Alternatives:** keep the case as typed; store display and normalised forms

The planning agent posts its decisions for each issue as a comment on that
issue too, with the question, the choice, why, and the alternatives, and IDs
like `D42-P1`:

> 📌 **Decisions** of the plan in run `shop-spec-m3#1`. Change one with `/agent revisit <id> <what you want instead>`.
>
> #### D42-P1: Payment provider API
> **Question:** Which PayPal API?
> **Chosen:** Orders v2
> **Why:** the project already uses the REST client; v1 is deprecated.
> **Alternatives:** Checkout v1

The issue description keeps only the spec (with a pointer to the comment), so
all decisions are discussed, answered and revisited in the comments. While an
issue is a draft, a re-plan updates this comment in place and its IDs are
renumbered; implementer IDs (`D42-3`) never change. On autopilot the planner
asks no questions, so this comment is where its defaults show up. Issues
planned before this change keep their decisions in the description, and
those still work; a re-plan of such an issue while it is still a draft moves
them into the comment (and renumbers them). Reviewers get the planner's
decisions with the issue.

To change a decision, comment on the issue or its MR:

```
/agent revisit D42-3 keep the case as typed and add a normalised column
```

This is a normal run request. The agent gets the decision it is asked to
change together with your instruction, changes the code, and records a new
decision that supersedes the old one. The old decision is then marked
*Superseded by D42-5*.

- The comment must start with `/agent revisit <id>`; one revisit per comment.
  Several revisit comments since the last run are handled in the same run;
  for the same ID the latest comment wins. A comment like `/agent revisit the
  currency` (no ID) is a normal comment, and the report shows the right form.
- An ID that does not exist on the issue is passed on as a plain comment, and
  the run's report says so. Revisiting a decision that is already superseded
  is not applied: revisit the decision that replaced it.
- A revisit counts as handled once the run's decisions are posted. If the run
  fails before that, the next run applies it again.
- If the agent does not record a decision that replaces the old one, the old
  one is not marked, and the report says so.

Decisions are only read from the bot's decision comments (planner decisions
of older issues also from the description), so a comment by someone else
cannot add or fake decisions.

#### Revisiting after the merge

Once an issue of a planned milestone is merged and closed, its decisions can
still be changed. Comment `/agent revisit <id> <instruction>` on the closed
issue, or on the milestone's [tracking issue](#tracking-issue) on autopilot.
The operator then creates a **follow-up issue** in the milestone:

- titled `Revisit D42-3: <decision title>`, with the `ai-generated` label and
  the profile label of the original issue;
- its description holds the old decision, your instruction, and who asked;
- its implementer works on top of the merged change and records a decision
  that supersedes the old one. When that decision is posted, the old decision
  on the original issue is marked *Superseded by D57-1*, already before the
  follow-up is merged. If you drop the follow-up, comment on it or open an
  issue by hand to restore the old behavior.

Without autopilot the follow-up goes straight to `agent::ready`, since you
asked for it. On autopilot it is created as `agent::draft` and joins the
queue after the planned issues; it is merged like any other.

The operator answers every revisit comment it handles:

| Reply | Why |
|-------|-----|
| `Created #57 to revisit D42-3 …` | the follow-up was created |
| `#42 is still open …` | comment on the open issue; its next run changes the decision |
| `D42-9 was not found on #42` | the ID is wrong |
| `… already superseded …` | revisit the decision that replaced it |
| `#57 already revisits D42-3 …` | one follow-up per decision: comment on it |
| `… not in this milestone` / `… does not exist` | the ID names another issue |

Only comments written after the operator started watching the milestone,
and after the issue was closed, are handled, each once: a revisit you wrote
while the issue was open belongs to its runner (if the issue was merged
before a run picked it up, comment again on the closed issue). On the tracking issue,
revisits are handled while autopilot is on, also after it is done. A re-plan
of the milestone leaves follow-ups alone. Milestones without `/agent spec` or
`/agent autopilot` are not watched: there, open a new issue by hand.

## 5. Finish

Merge the MR, then close the issue (the MR's `Closes #42` does that) or move
it to `agent::done`. The task, its pod and its workspace are deleted.

## Autopilot: a milestone end to end

With **`/agent autopilot`** in the milestone description (instead of, or next
to, `/agent spec`), the milestone runs without you:

1. The spec agent plans it as usual, but asks no questions. It takes the
   default it would propose and records it as a [decision](#decisions), with
   why and the alternatives, in a comment on each issue.
2. The operator creates a tracking issue, **🤖 Autopilot: <milestone>**, and
   moves the first issue to `agent::ready`. Issues go **one at a time**, in
   dependency order (an issue's "Depends on" first, otherwise plan order). An
   issue linked as "blocked by" an open issue waits for it too.
3. The issue is implemented, reviewed and checked by CI as usual. The
   implementer never stops with questions: it decides and records the
   decision. On autopilot, CI fix runs are reviewed as well.
4. Once the pipeline is green and the review is clean, the operator marks
   the draft MR ready and **merges it**, then starts the next issue on the
   new default branch. The merge closes the issue.
5. When every issue is merged (or skipped), the tracking issue gets a summary
   and is closed.

**Before you use it,** allow the bot to merge into the default branch:
*Settings > Repository > Protected branches*, "Allowed to merge":
*Developers + Maintainers*, or give the bot user the *Maintainer* role. The
merge method and squash settings of the project apply. Keep at least one
reviewer in `agents.review.reviewers` (the default has two): autopilot only
merges reviewed changes. Autopilot never merges an MR of an issue it did not
plan; other issues in the milestone follow the normal flow.

If the project uses merged-results pipelines, also turn on *Pipelines must
succeed*: autopilot follows the pipeline of the pushed commit, and a commit
without any pipeline counts as green after `ci.noPipelineTimeout`.

### The merge gate

An MR is merged only when all of these hold:

- the issue is in `agent::review`, i.e. the CI loop finished (green, or no
  pipeline within `ci.noPipelineTimeout`; with `ci.disabled` the run moves it
  there directly);
- the last run succeeded, no blocker or major review finding is left, no
  reviewer failed, and the review saw the commit that was pushed;
- CI checked that commit. An issue in review whose commit the CI loop never
  saw goes back to `agent::working` first;
- the MR is open, targets the default branch, and its head is exactly that
  commit (nobody pushed since);
- if no pipeline ran for the commit and the MR is still a draft (projects
  that skip pipelines for draft MRs; normally the run marked it ready before
  pushing), the operator marks it ready, starts a merge request pipeline and
  waits for it like for any pipeline. Only if there is still none, the gate
  goes on without CI;
- GitLab reports the MR as mergeable. A draft MR is marked ready once. If it
  needs a rebase (fast-forward or semi-linear projects), the operator asks
  GitLab to rebase once and waits for CI on the new head. The review carries
  over to the rebased head only if the change is the same: same files, same
  added and removed lines. Otherwise it pauses.
- If the MR has [merge conflicts](#merge-conflicts), or GitLab cannot rebase
  it (conflicts during the rebase, a failed rebase, or a change too large to
  check), the agent merges the default branch and resolves the conflicts. That
  run is reviewed and checked by CI like any other, then the gate runs again.

The merge call passes the checked commit (`sha`), so GitLab refuses to merge
anything else.

### When it pauses

Autopilot pauses instead of guessing. It comments on the tracking issue with
the reason and what to do, mentioning the milestone's
[owners](#owners-and-pings):

- the issue landed in `agent::needs-input` (CI still red after the fix
  rounds, a failed run, used-up usage-limit deferrals);
- blocking review findings or failed reviewers;
- a canceled pipeline, or CI red with jobs that also fail on the default
  branch;
- someone pushed to the agent's branch, the MR was closed or retargeted, it
  needs an approval, has unresolved discussions, or was marked draft again
  after autopilot marked it ready;
- the MR still has conflicts after the agent merged the default branch for
  this head (or `workflow.resolveConflicts` is off);
- the merge was refused (for example: the bot may not merge), or the rebased
  change differs from the reviewed one;
- the active issue lost its workflow label while open;
- no reviewers are configured;
- the plan has a dependency cycle, or nothing can start because the
  remaining issues wait for skipped issues, for issues outside the queue
  ("blocked by" links), or for each other (a cycle of links).

While paused, runs that were already requested continue (your `/agent`
comments still reach the agent), but nothing new is started or merged.
A run of the issue the pause is about gets the pause reason in its prompt
("Autopilot paused because: …"), marked as the operator's context, before
your comments. So `/agent investigate the root and fix it` on that issue
knows what "it" is.

While a pause over the active issue's run, review or CI holds, autopilot
checks the issue on every poll. If the reason it paused for is gone but the
issue fails the merge gate for another one, it says so with a new pause
comment (mentioning the owners) instead of keeping the old reason.

A pause over the active issue ends **by itself** when it no longer holds:

- the issue's MR was merged by hand, or the issue was closed: autopilot
  counts it (merged, or skipped if closed without a merge) and goes on with
  the next issue;
- the pause was over its run, review or CI (needs input, a failed run,
  blocking findings, failed reviewers, an unreviewed head, red or canceled
  CI) and a later run fixed it: for example you commented `/agent fix the
  findings`, the run succeeded, was reviewed and CI is green. Autopilot then
  continues with the merge.

The tracking issue gets "▶️ Autopilot continues: …". Every other pause (an
approval, unresolved discussions, someone else's push, a dependency cycle,
`/agent pause`) waits for `/agent resume`.

### Commands on the tracking issue

(All commands: [commands.md](commands.md).)

| Comment | Effect |
|---------|--------|
| `/agent pause` | stop starting and merging |
| `/agent resume` | continue; the active issue is checked again |
| `/agent skip #12` | skip a planned issue; issues that depend on it stay blocked until it is merged by hand or they are skipped too |
| `/agent revisit D42-3 …` | change a decision of a merged issue: a [follow-up](#revisiting-after-the-merge) joins the queue |

Any other `/agent …` comment on the tracking issue (also a bare `/agent` or a
`revisit` without a decision ID) gets one answer: the commands above, and
that instructions for the agent go on the active issue (`#N`). The tracking
issue does not pass them on to the agent.

Everything else works as on any issue: comment `/agent …` on the active issue
or its MR, revisit decisions with `/agent revisit <id> …`, or fix things by
hand and `resume`. If you pushed to the agent's branch (to resolve a
conflict the agent could not, say), comment `/agent …` on the issue: the run
takes your commits over, is reviewed, and CI checks the new head before the
merge.

You can also finish issues by hand. An issue closed by a merged MR, or closed
with `agent::done`, counts as merged: the active one (also while paused: the
pause ends by itself), a skipped one (its dependants continue on `resume`),
and one that was already done before autopilot got to it.

### Tracking issue

The operator keeps its description up to date: the state (running, paused
with the reason, done), one line per planned issue (merged with the MR and
commit, in progress with its label, pending with what it waits for, skipped),
and an index of the decisions of every merged issue with links. It carries
the `ai-generated` label only, so it is never worked on itself. If it is
deleted, a new one is created.

### Switching it off

Remove `/agent autopilot` from the milestone description. The tracking issue
gets a note and is closed, and nothing is merged any more. With `/agent spec`
still there, the milestone continues in the normal flow; the issue that was
active stays where it is. Adding the marker again continues the queue.

If the plan changes (you edit the milestone spec), issues that were not
started yet follow the new plan; started and merged ones are kept. While the
milestone is being planned again, no new issue is started.

## Usage and rate limits

Subscriptions have usage windows, and APIs have rate limits. When an agent
CLI reports such a limit, the run is **paused, not failed**:

- The issue stays in `agent::working` and gets one comment: "⏳ The `claude`
  engine reached a usage limit. The run is paused and retried automatically
  at …".
- The run is retried at the reset time the CLI reports. If it doesn't
  report one, the wait is 5 minutes, doubling with every further limit hit
  up to 1 hour (`spec.lifecycle.usageLimitBackoff` /
  `maxUsageLimitBackoff`).
- Paused runs don't count towards `maxRunsPerTask` and don't take a run slot.
- Limits belong to your credentials, so they apply to the whole project:
  other issues that need the same engine wait too instead of hitting the
  limit again. Issues on another engine (e.g. `fast` on cursor while claude
  is limited) keep working.
- If the limit hits during a review fix round, the work done so far is pushed
  with the open findings instead of being held back.
- After 20 deferrals of the same run it is reported as failed, in case an
  error was mistaken for a limit.

Nothing to do on your side; `kubectl get agenttasks -o wide` shows the retry
time, and the project status lists the active limits:

```sh
kubectl -n agents get agentproject <name> -o jsonpath='{.status.usageLimits}'
```

GitLab has limits too, and gitlab.com is sometimes slow or answers 502. The
operator and the runners retry on their own:

- A request GitLab rate-limits (429) is sent again after the wait GitLab
  asks for (`Retry-After` / `RateLimit-Reset`), up to a minute. Longer waits
  are left to the next poll.
- Reads that hit a server error (5xx), a timeout or a broken connection are
  retried twice, after about 1 and 2 seconds. Writes are not: GitLab may have
  applied them.
- A poll may take at most `spec.pollInterval`, but no less than 2 minutes;
  retries stop there, so a slow GitLab cannot hold up the other projects.
- A poll that still fails is logged as one line, "GitLab sync failed, trying
  again on the next poll", and the project's `Synced` condition says why;
  the next poll (`spec.pollInterval`) picks up where it left off. Other
  errors are logged with a stack trace as before.
- With the operator at log level `debug` (Helm `logLevel: debug`) every poll
  logs how many requests it sent ("GitLab poll done"); a poll that needed
  retries logs it at the default level too.

## Issues that stay open

Nothing is lost while an issue waits:

- **Right after each run** (1 minute later) the pod is stopped. The workspace
  (checkout, build caches, agent session) is kept, so the next run starts a
  new pod on it and picks up where the last one stopped. While the MR
  pipeline runs, no pod is needed: the operator follows the pipeline itself.
  After a **failed** run the pod stays for an hour, so you can read its logs
  (`kubectl logs`, see below).
- **7 days** after the last run the workspace is deleted as well.
- Any new `/agent` comment or move to ready brings the task back. After the
  workspace was deleted, the next run clones fresh and reads the full issue
  thread plus the agent notes (below).

### Agent notes

After every run the implementer writes short handoff notes: the state of
the work, decisions and why, what is left, and pitfalls. They are kept in
one comment of the bot on the issue, **🧠 Agent notes**, which is created on
the first run and edited by later ones (right after the push) instead of
adding new comments. A run that cannot resume the agent's session (the
workspace was deleted, or the session is gone) starts from the issue, the
thread and these notes instead of from zero.

If the notes are wrong, say so in a comment: comments written after the
notes take precedence. Editing the note itself does not help, the next run
overwrites it. Only the bot's own comment counts as notes. Keep in mind
that the notes are written by the model from the thread, so whatever it
was told in a comment can end up in them.

Both durations are configurable (`spec.lifecycle`).

## Watching progress

The workflow labels are the state the operator works with. GitLab's milestone
page does not show labels as columns, it groups issues by its own rules:

| Column | GitLab's rule | Agent issues in it |
|--------|---------------|--------------------|
| Unstarted | open, nobody assigned | `agent::draft`, and `agent::ready` until the next poll |
| Ongoing | open, someone assigned | `agent::working`, `agent::needs-input`, `agent::review` |
| Completed | closed | merged and closed |

So that an issue the agent works on shows as **Ongoing**, the operator assigns
the bot user (the user of the GitLab token) when it picks the issue up and
nobody is assigned yet. The assignee is only a mirror for GitLab's views, the
operator never reads it. It never replaces an assignee: if you assign yourself
to take an issue over, the bot is not added back. Turn this off with
`spec.workflow.assignBot: false`.

The draft merge requests the agent opens carry the issue's milestone, so they
show up in the milestone's **Merge requests** tab. Issues without a milestone
get merge requests without one. Issues the operator picked up before this
behaviour existed get the assignee on their next run request; merge requests
that already exist keep no milestone.

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
(reviewers that failed) and `reviewedSHA` (the commit that review saw; a CI
fix run can push commits after it).

The spec task of a milestone holds the plan in `status.plan` (issues in plan
order with their dependencies, and the dependencies the operator linked) and, on autopilot, the queue in
`status.autopilot`: `phase`, `pausedReason`, `trackingIssueIID` and one item
per issue with its state, MR, merged commit and decisions (`revisitOf` for a
follow-up). `status.revisits` tracks the [revisits after the
merge](#revisiting-after-the-merge): when watching started, the last check
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
  `maxMemory` or the limit lowers it too. The killed call's uncommitted edits
  are lost; the run starts again from the last pushed commit. If no node has
  room for the raised memory, the task's status says the pod cannot be
  scheduled (`kubectl get agenttasks -o wide`).
- **Out of memory at the cap** (or after 3 restarts, or without a memory
  limit to raise): the run fails with that reason. Raise
  `spec.agents.resources` or `spec.agents.maxMemory`, then comment `/agent
  retry`. A pod started with other agent resources is replaced between runs,
  so the retry gets the new limit.
- **Another reason** (a crash): the checkout is reset to the last commit and
  the call is tried once more; if it loses the container again, the run
  fails with the reason and exit code.
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
again from the last pushed commit; uncommitted edits of the lost attempt are
gone. A progress comment the lost attempt left on the merge request is
removed. The new attempt's progress comment and report say so above the run
log: "↻ Started again: the previous attempt was interrupted (its pod stopped
before the run finished, e.g. evicted or the node went down). Restart 1 of
at most 3." Agent pods use a local volume, so on a node that is down the new
pod waits until the node is back.

After 3 restarts of the same run request (`status.run.interruptions`) the
runner gives up instead of trying again: something in the run may keep
taking the pod down. The run fails; for an issue, it moves to
`agent::needs-input` with a comment that mentions the owners (on autopilot,
the pause on the tracking issue mentions them), and `/agent retry` starts it
again with a new count. A spec run fails like any other failed spec run. A
run that finishes resets the count. Suspending a task (`spec.suspend`)
mid-run stops its pod too, but does not count: the run starts again when the
task is resumed.

Task phases: `Pending`, `Queued` (waiting for `maxConcurrentRuns`), `Running`,
`Waiting` (paused by a usage limit), `Idle`, `Hibernated`, `Archived`,
`Blocked` (hit `maxRunsPerTask`).

To force a task to stop its pod, set `spec.suspend: true` on the AgentTask. To
stop all activity for a project, set `spec.suspend: true` on the AgentProject.
