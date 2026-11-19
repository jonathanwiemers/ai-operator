# While the agent works

Once an issue is handed over, a [run](../concepts/glossary.md#run) starts:
the [implementer](../concepts/glossary.md#implementer) changes the code on a
branch, the [reviewers](../concepts/glossary.md#reviewer) check it, and CI
runs. This page shows what you see on the merge request and in the
[Desk](../concepts/glossary.md#desk) while that happens, how commit messages
and toolchains work, and what the operator does when CI fails, the default
branch is red, or the merge request has conflicts.

An issue's merge request goes: draft → code in steps → review → ready → CI,
with fixes if needed → merge, by you or by
[autopilot](../concepts/glossary.md#autopilot).

## Follow it in the Desk

Click the issue in the milestone's *Issues* tab to open its task page. It
shows the merge request, the run as it happens, the CI pipeline and what
the run cost. The *Live* tab follows the agent: its steps, what it says and
the tools it calls. See [Live transcript](desk.md#live-transcript) for
more on the views.

## The draft merge request

On the first run, the operator opens a **draft merge request** right away,
before the agent changes anything:

- Its branch is named like `agent/42-add-paypal-button` and starts with an
  empty commit "Start work on #42". That commit starts no pipeline.
- Its description says `Closes #42` and holds a placeholder until the first
  push is described.
- It is in the issue's milestone, and the issue gets a comment that links
  it. From then on every run reports on the merge request.

While a run goes on, the merge request has a **progress comment** with the
run log so far: each step, its time and result. It is updated as the run
goes, and replaced by the run's report when the run ends. A run that fails
or waits for a usage limit leaves its run log there with a short line why.

## Steps, and steering while it works

While the merge request is a draft, the agent works in **steps**: after
each piece that is worth a commit of its own, the step is committed and
pushed, and the agent goes on. Step pushes start no branch pipeline. A run
has at most 25 steps; one that runs out leaves the merge request a draft
and says so. `workflow.stepCommits: false` makes one commit per run (see
[`spec.workflow`](../reference/agentproject.md#specworkflow)).

You can steer while it works. A comment you write on the issue or the
merge request reaches the agent before its next step; it needs no `/agent`
prefix. An `/agent …` comment is different: it starts its own run after
this one, and the comments after it wait for that run too.

With [`maxStepLines`](../reference/agents-and-models.md#roles) set, the
**step-size gate** keeps steps small on claude: once the uncommitted change
passes that many lines, the agent is told to finish the step. Review fix
rounds and conflict runs have no gate.

## Review

When the implementer is done, two reviewers check the change: **bugbot**
for bugs and **security**. Blocker and major findings go back to the
implementer, for a few rounds; the rest are listed for you in the report.
A reviewer that fails because its engine is briefly unavailable is called
again after a short wait. The number of rounds and retries is in
[Roles](../reference/agents-and-models.md#roles).

Then the merge request is set:

- **Ready** when the review is clean. Its `Draft:` prefix is removed before
  the push, so the push gets its pipeline even where pipelines skip drafts.
  Without reviewers, every run with changes marks it ready.
- **Draft** when findings are left, a reviewer failed, or the agent asks
  questions. A merge request with only the start commit stays a draft.
- **Your hold stays.** If you mark a ready merge request as draft yourself,
  it stays a draft. Mark it ready to release it.

If the reviewers keep failing, ask for a **re-review**: the *review* action
in the Desk chat, or `/agent review` (see
[Commands](../reference/commands.md#on-an-issue-or-its-merge-request)).
The reviewers check the pushed head again, and the implementer runs only
if they find blocker or major issues.

## The description and the report

After the push, the **describer** rewrites the merge request description
when the change moved on: *Why*, *What*, *How*, *Verification* and, when it
helps, a diagram at the bottom. It describes the whole branch, not the run,
and only rewrites its own block: text you add above or below it, and
`Closes #42`, stay.

Then the run's **report** goes on the merge request. The first report
points to the description; later ones say what that run changed. If the
agent has questions, the report lists them with a link to the Desk (see
[Answer and hand over](answer-and-hand-over.md)).

The agent also posts its [decisions](../concepts/glossary.md#decision) on
the issue (see
[Decisions and Revisit](iterate-and-finish.md#decisions-and-revisit)).

Without questions, the issue stays in `agent::working` until CI has run,
and moves to **`agent::review`** once it is green (see
[CI feedback](#ci-feedback)). After a clean run, "review" means: reviewed,
tests and CI pass, the merge request is ready. A draft in review still has
findings the report lists. On autopilot the operator merges it.

## Commit messages

The agent writes the message of every commit it leaves on the branch:
each step, each fix, CI or conflict round. The operator adds `Refs #42`,
and uses the issue title when the agent wrote none. If a run is cut off,
its work is saved as a checkpoint commit `chore(wip): checkpoint …` until
the next step folds it in (see
[`spec.checkpoint`](../reference/lifecycle-and-ci.md#speccheckpoint)).

With `workflow.conventionalCommits: true` the messages follow
[Conventional Commits](https://www.conventionalcommits.org/):

```
feat(checkout): add the paypal button

The payment step offers PayPal next to the card form.

Refs #42
```

- The agent picks the type (`feat` for new behaviour, `fix` for a bug fix,
  …) from `workflow.commitTypes`, and the scope from the part of the code
  the step touches. The subject is imperative and lowercase, at most 72
  characters.
- A message that still does not match, such as `Add thing`, becomes
  `chore(agent): add thing`, and the run report says how many were
  rewritten.
- The branch starts with `chore: start work on #42`, and a merge of the
  default branch is `chore: merge main into agent/42-…`.
- The **merge request title** follows the format too, because a squash
  merge puts it on the default branch. The describer writes it each time it
  rewrites the description, with `Draft: ` while the merge request is a
  draft. A title you renamed by hand stays. After you turn the option on,
  titles follow with the next push that changes the diff.
- **Autopilot's merge** sets the squash commit message to the title and
  `Refs #42`, so the commit on the default branch is conventional even when
  you renamed the title.

With the option off, which is the default, the agent writes its messages
freely. The fields are in
[`spec.workflow`](../reference/agentproject.md#specworkflow).

## Toolchains the agent installs

The agent runs your tests, linters and formatters itself instead of
guessing their output, so it needs your toolchains. Besides the tools of
the [agent image](../reference/agent-image.md), it installs the ones your
repository pins at the root of the **default branch**. For example, with
this `.mise.toml` the agent installs Node and Go before it runs your tests:

```toml
[tools]
node = "22"
go = "1.26"
```

| Pin file | Installed with | For example |
|----------|----------------|-------------|
| `.mise.toml`, `mise.toml`, `.tool-versions` | `mise install` | Node, Go, Python, jq |
| `rust-toolchain.toml`, `rust-toolchain` | `rustup toolchain install` | Rust with `rustfmt`, `clippy`, targets |

- The tools come from official sources and land on the issue's workspace
  volume, so later runs of the same issue reuse them. No root, no system
  packages, no piped install scripts. Nothing installs on its own: the
  agent runs mise tools with `mise exec`.
- If the agent's branch changes a pin, say it bumps Rust, that version goes
  to a temporary place for the run, so it never mixes with the versions of
  the default branch. A pin the agent changes during a run counts from the
  next run on.
- The agent says in its report when an install failed. To avoid a download
  per issue, add the toolchain to your own agent image.

## CI feedback

After every push, the operator follows the pipeline of the pushed commit:

| Pipeline | What happens |
|----------|--------------|
| running | the issue stays in `agent::working` |
| green, or waiting for manual jobs | `agent::review` |
| red, first time | the failed jobs are retried once, to rule out flaky jobs |
| still red, and the same jobs fail on the default branch | `agent::review`, with a comment that the change did not cause it |
| still red | the agent gets the failed jobs and the end of their logs, and fixes the cause: a "CI fix" run |
| still red after the fix rounds | `agent::needs-input`, with the failed jobs |
| canceled | `agent::review`, with a comment |
| no pipeline in time | `agent::review`: the project has no CI for this commit. A skipped pipeline counts as none. |

The number of fix rounds, the retry and the wait for a pipeline are in
[`spec.ci`](../reference/lifecycle-and-ci.md#specci); `ci.disabled: true`
moves an issue to review right after the push.

- **Fresh rounds for each instruction.** The fix rounds start again with
  every run that is no CI fix, such as an `/agent` instruction, so each new
  instruction gets the full number. Runs the pilot starts keep the count.
- **Reviews.** CI fix runs are reviewed on autopilot only.
- **Logs are untrusted.** Job logs reach the agent as data, never as
  instructions.
- **Which pipeline.** The operator follows the merge request's head
  pipeline when the head is the pushed commit, which covers merge request
  and merged-results pipelines, and otherwise the newest pipeline of the
  commit.
- **Drafts.** If your pipelines skip draft merge requests, a run that left
  problems gets no pipeline and moves to review once the wait for a
  pipeline is over. Before it merges, autopilot marks such a merge request
  ready and starts a pipeline. If GitLab cannot run a merge request
  pipeline, for example because the project has none or no CI at all,
  autopilot merges without CI.
- **Your own commits.** If you push to the agent's branch yourself and then
  comment `/agent …`, a run that has nothing to add takes your commits over,
  and CI is followed for your head.

While the issue is in `agent::review`, the operator keeps watching the
merge request's head pipeline. A pipeline started later, by you, by
autopilot or by a run that marked the merge request ready, or a canceled
pipeline you retry, is followed too: while it runs or once it is red, the
issue goes back to `agent::working`, and the loop above takes over. So a
job such as "add a changelog entry" that only runs for ready merge requests
gets fixed by the agent too, as far as code can fix it.

## Main health

The operator also watches the **default branch**. At a fixed interval it
reads the newest finished pipeline, and acts on it:

| Default branch pipeline | What happens |
|-------------------------|--------------|
| green | nothing; an open `ci::main-red` issue gets "✅ green again" and is closed |
| red, first time | its failed jobs are retried once |
| green after the retry | flaky: one issue per job, "🔁 Flaky CI job: `<job>`" (label `ci::flaky`), with a comment for each time it happens: the failing tests and the end of the failed log |
| still red after the retry | an issue "🚑 `main` is red: `<jobs>`" (label `ci::main-red`) with the pipeline, the commit and the end of the failed logs; it mentions the project's owners |
| red again, other jobs | a comment on the `ci::main-red` issue, and its title follows |
| canceled, and no newer pipeline replaced it | retried once, unless the commit has a green pipeline of its own |
| canceled again | a comment on the open `ci::main-red` issue, else an issue "⏹️ `main`: pipeline canceled twice" (label `ci::main-canceled`). It freezes nothing; close it by hand |

The operator finds its own issues again by a hidden marker. Issues a human
opens with these labels are left alone. Close a flaky issue once the job is
fixed; the next failure opens a new one. Job logs are untrusted: mentions
and commands in them are made harmless. Every switch, with its default, is
in [`spec.mainHealth`](../reference/lifecycle-and-ci.md#specmainhealth).

**Merge freeze.** While the default branch is red, autopilot merges
nothing. An issue that passed review and CI is blocked
(`agent-blocked::main-red`), and autopilot pauses: "`main` is red (#N):
merging waits until it is green". Issues that already started keep working
until they reach the merge gate. Once the branch is green, the block is
lifted and autopilot goes on by itself: "▶️ Autopilot continues: `main` is
green again". A flaky branch does not freeze. `/agent resume` does not end
a freeze; to ship during one, merge by hand.

**Let the agent fix a red branch.** With `mainHealth.autoFix: true`, the
`ci::main-red` issue goes to the agent, once per red episode. You can also
hand it over yourself with `agent::ready`. Its task is to make the branch
green: fix the cause, and stabilise or quarantine a test that fails only
sometimes, never delete it. On autopilot, the oldest autopilot milestone
that is not done runs this fix through the same merge gate, and it is the
only merge during the freeze. If it cannot be merged, autopilot pauses and
mentions the owners. Without autopilot, you merge it. Switch `autoFix` on
only where that is wanted: whoever can make the default branch fail starts
an agent run.

**Let the agent fix a flaky test.** A `ci::flaky` issue goes to the agent
once:

- when the [pilot](../concepts/glossary.md#pilot) hands it over, on its own
  or as a proposal, after the job was flaky at least twice; after once,
  when an owner accepts the proposal;
- or with `mainHealth.autoFixFlaky: true`, the second time the job flakes,
  also without a pilot.

The issue joins a milestone and runs like any of its issues. Its task is
to make the test deterministic; a quarantine needs an owner's `yes`. Only a
flaky issue the operator opened and did not get edited is handed over.

## Merge conflicts

When the default branch moves on and the merge request of an issue in
`agent::review` gets conflicts, the agent resolves them:

1. The operator sees the conflict and moves the issue back to
   `agent::working`.
2. The run merges the default branch into the agent's branch: a merge
   commit, no force push, so your checkout of the branch stays valid.
3. The implementer gets the conflicted files with their markers. It keeps
   the intent of both sides, checks that the change still fits the new
   code, and records decisions where a resolution is not obvious. The merge
   is only finished when no conflict marker is left.
4. The result is reviewed and pushed, CI runs, and the issue returns to
   `agent::review`. The report names the files that had conflicts.

If the merge has no conflicts after all, the implementer does not run. If
the agent cannot resolve a conflict, the merge is undone, nothing is
pushed, and the issue goes to `agent::needs-input` with the files. The
operator tries once per merge request head: when the default branch moves
again, it tries again.

By default the implementer resolves conflicts in its own long session, on
its own model, which can be expensive. The
[conflict role](../reference/agents-and-models.md#conflict-role) runs them
on a cheaper model in a fresh session instead. The Metrics tab shows what
conflict calls cost.

With `workflow.resolveConflicts: false` you resolve conflicts yourself, and
autopilot pauses on them.

## Next

- [Owners and pings](owners-and-pings.md): who gets mentioned when the
  agent needs a human, and who may give commands.
- [Iterate and finish](iterate-and-finish.md): change requests, decisions
  and merging.
- [Lifecycle and CI](../reference/lifecycle-and-ci.md): every CI and main
  health setting.
