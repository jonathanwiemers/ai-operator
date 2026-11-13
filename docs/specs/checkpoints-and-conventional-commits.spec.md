# No lost work: checkpoints, smaller steps and conventional commits

Status: approved by the maintainer (2026-11-05) as the milestone spec of
"ai-operator: no lost work, smaller steps, conventional commits", with the
decisions below locked.

## Goal

1. Work that is not pushed yet is never thrown away. When a run is cut off
   (usage limit, failed call, budget stop, a container that restarts, a lost
   pod), the runner saves the work tree as a **checkpoint** commit on the
   work branch and pushes it. The next run continues from there.
2. The implementer pushes more often. A **step-size gate** tells it to close
   its step once its uncommitted diff gets big, so a cut-off loses little
   and the MR's history can be read step by step.
3. Every commit the agent leaves on a branch, and every commit that lands on
   the default branch, follows **Conventional Commits**
   (`type(scope): subject`). The agent writes the messages, checkpoints
   included: a checkpoint's placeholder message is replaced by the agent's
   own one in the next run.

## Background

What happens today (file refs at origin/main, 2026-11-05):

- `workflow.stepCommits` (default `true`) makes the implementer work in
  steps while the MR is a draft (`stepsNote`, `internal/runner/prompts.go`).
  After each coherent piece it returns `in_progress` with a `commitMessage`,
  the runner commits (`Runner.commit`, `implement.go`) and pushes with
  `ci.skip` (`Mirror.push`, `git.go`). At most 25 steps per run.
- The implementer seldom stops. The last 16 merged Orbit agent MRs (!726 to
  !741) are mostly one commit of 1 500–4 000 changed lines, for example !741
  with 42 files (+1782/−422), !738 with 55 files (+2374/−1373) and !732 with
  100 files (+2224/−2668). Only !733, !734 and !740 have 2–4 step commits.
- Every run starts with `Workspace.Prepare`: `checkout --force -B <branch>
  origin/<branch>`, then `Reset` (`reset --hard`, `clean -fdq`). Uncommitted
  work from an earlier attempt is therefore gone:
  - a usage limit during an implement step defers the run, and the next run
    resets the tree. Only a usage limit in a *fix round* keeps its work
    (`implement.go`, "Keep what is done");
  - a restarted agent container is reset before the call is tried again
    (`runner.go`, `r.Agent.Reset`);
  - a lost pod or an OOM kill starts again "from the last pushed commit;
    uncommitted edits of the lost attempt are gone" (`docs/usage.md`).
- After a usage limit the implementer's **session** is remembered and
  resumed, but its files were reset. The agent believes edits exist that
  are not there and either redoes them or builds on nothing.
- Commit messages: the answer schema asks for an "imperative subject line
  (max 72 chars), blank line, body" (`prompts.go`); `commitMessage()` adds
  `Refs #N` and falls back to the issue title. A new branch starts with
  `Start work on #N`.
- Orbit and ai-operator merge with `ff` and squash on by default, with no
  squash commit template. The commit on the default branch is the **MR
  title**, which is `Draft: ` + the issue title (`implement.go`, MR create)
  until autopilot marks it ready. Main therefore reads "Knobloo: LED screen
  set into the Stammtisch, …", not conventional. The autopilot merges with
  `MergeOptions{SHA, ShouldRemoveSourceBranch}` (`autopilot.go`), without a
  squash message.
- No role has `fallbackAtPercent` or an implementer fallback on my-cluster, so
  a usage limit always cuts a step off hard.
- Milestone 8 (read gate) adds the `ai-runner` hook subcommand and the
  `--settings` plumbing. The step-size gate here reuses it.

## Decisions (locked)

1. **Checkpoint triggers.** The runner saves a checkpoint when the
   implementer's call (implement step, fix round, CI or conflict round) ends
   with a usage limit, an engine error or timeout, or a budget stop; before
   it resets the work tree after an agent container restart; and, in a new
   pod after a lost pod or an OOM kill, before `Prepare` resets the
   workspace. The PVC still holds the files then: the runner first checks
   that the workspace is on the task's branch and that its `HEAD` is the
   last pushed commit or a descendant of it. Otherwise it saves nothing.
2. **What a checkpoint is.** One commit on the task's work branch
   (`agent/*`), with everything the step commit would stage, pushed with
   `ci.skip`, never forced. Its subject is fixed and conventional:
   `chore(wip): checkpoint #<issue> step <n> (<reason>)`. The body holds the
   diff stat and the agent's last status text (its last todo list or last
   message, cut at 1 000 characters). The trailer
   `Agent-Checkpoint: <run>/<step> <reason>` marks it. There is no checkpoint
   when the tree is clean.
3. **Safety guards before a checkpoint:**
   - remove a stale `.git/index.lock` left by a killed process;
   - a merge or rebase in progress is aborted back to the commit before it
     (`MergeResult.From`), and no checkpoint is made: half-resolved
     conflicts are never committed;
   - files larger than `checkpoint.maxFileBytes` (default 5 MB) and new
     files matching the secret patterns (`.env*`, `*.pem`, `*.key`,
     `id_rsa*`, `*.p12`) are left out of the commit and named in the run
     report. They stay in the workspace;
   - a checkpoint never goes to the base branch or a branch other than the
     task's.
4. **A checkpoint is never merged or reviewed.** When the MR is ready, the
   runner sets it to draft before the push. The operator and the autopilot
   treat an MR whose head commit carries `Agent-Checkpoint` as work in
   progress: no reviewers, no merge, no "ready". The pilot state shows it.
5. **The next run continues from the checkpoint.** `Prepare` keeps doing
   what it does, from the pushed checkpoint, so the resumed session and the
   files agree again. The run's prompt (fresh or resumed) gets one line:
   *The previous attempt was cut off (<reason>) after step <n>; its
   uncommitted work was saved as a checkpoint commit. Check `git show
   --stat HEAD` before you continue.*
6. **The agent replaces the checkpoint message.** When the next step of
   that branch ends (`in_progress` or `done`), the runner folds every
   checkpoint at the tip into that step's commit: `reset --soft` to the
   last commit before the checkpoints, then commit with the agent's message.
   It pushes with `--force-with-lease=<branch>:<checkpoint sha>`, so a commit
   someone else pushed in the meantime stops the fold. The checkpoint is then
   kept, and the run report says so. Folding runs only on the agent branch
   and only when every commit it removes carries `Agent-Checkpoint`.
7. **Conventional Commits:** a new `workflow.conventionalCommits` (default
   `false`, so behaviour stays as it is), with an optional
   `workflow.commitTypes` (default `feat, fix, refactor, perf, test, docs,
   build, ci, chore, style, revert`). When it is on:
   - the implementer's answer schema gets a `pattern` on `commitMessage`
     (`^(<types>)(\([a-z0-9._/-]+\))?!?: .{1,72}(\n\n[\s\S]*)?$`), and
     `stepsNote` and the fix/CI prompts say how to pick the type and scope;
   - the runner checks every message it commits. One that still fails
     becomes `chore(agent): <first line>`, which is logged and counted;
   - the first branch commit becomes `chore: start work on #N`;
   - the describe role returns a `title` in the same format (without the
     draft prefix). The runner sets the MR title to it and keeps the draft
     prefix. Because the merge squashes into the MR title, human merges are
     conventional too;
   - the autopilot passes `squash_commit_message` (the conventional title,
     blank line, `Refs #N`) in `MergeOptions` when it merges.
8. **Step-size gate** (needs milestone 8's hook subcommand): a new
   `implement.maxStepLines` (also on profiles; unset = off). A
   `PostToolUse` hook on `Edit`, `Write` and `Bash`
   (`ai-runner step-gate --max-lines N`) counts the uncommitted diff
   (`git diff --numstat HEAD` plus untracked text files). Once it passes N,
   it answers with additional context: *Your uncommitted diff is L lines
   (step limit N). Finish this step: get it to build, then return
   in_progress with its commitMessage.* It repeats at most once every 5
   tool calls. It applies only while steps are on (draft MR,
   `stepCommits`); it fails open like the read gate. `stepsNote` says that
   a step should stay under N lines where it can.
9. **Rollout on my-cluster is outside this milestone** (infra MRs after the
   release, by the owner): `workflow.conventionalCommits: true` on both
   projects, `implement.maxStepLines: 400` on Orbit, and
   `fallbackAtPercent: 90` with a sonnet fallback on `implement` and
   `strong`. The infra change and the checks to run one week after it are in
   the owner's rollout checklist (not in this repository).

## Requirements

Checkpoints
- R1 `Workspace.Checkpoint(reason)` with the guards of decision 3 and the
  message of decision 2. Tests: clean tree (no commit), dirty tree, stale
  lock, merge in progress (aborted, no commit), big file and secret file
  left out, wrong branch refused.
- R2 Triggers of decision 1 in the runner: usage limit, engine error and
  timeout, budget stop, container restart before `Reset`, and the new-pod
  path before `Prepare` with the `HEAD` check. A test per trigger with a
  fake agent; a lost-pod test with a workspace left dirty.
- R3 Run report and progress comment: "Saved a checkpoint (<sha>, N files,
  +A/−D) because <reason>", and the files left out.
- R4 Decision 4: draft before a checkpoint push; the operator, autopilot and
  reviewers skip an MR whose head is a checkpoint; pilot state field.
  Regression test: autopilot does not merge a green MR whose head is a
  checkpoint.
- R5 Decision 5's prompt line and decision 6's fold with
  `--force-with-lease`; tests for a fold, a lease failure (checkpoint kept)
  and a non-checkpoint commit at the tip (no fold).

Conventional Commits
- R6 `workflow.conventionalCommits` and `workflow.commitTypes` (API,
  validation, CRD, docs). Schema pattern, prompt text, the runner's check
  and the `chore(agent):` fallback, `chore: start work on #N`. Table tests
  for the check.
- R7 Describe returns `title`; the runner sets the MR title, keeping the
  draft prefix; the autopilot's merge sends `squash_commit_message`. Tests
  with a fake GitLab.

Step size
- R8 `ai-runner step-gate` (decision 8) with table tests, `maxStepLines` on
  `implement` and profiles, the `--settings` entry next to the read gate
  (merged into one settings object), and the `stepsNote` sentence.

Measurement and docs
- R9 `agent_calls` and the Task page show checkpoints (count, reason) and
  step-gate nudges per call; the Spend tab's implementer table shows lines
  per step commit.
- R10 Docs: `docs/usage.md` (what happens when a run is cut off, replacing
  "uncommitted edits … are gone"), `docs/configuration.md`
  (`conventionalCommits`, `commitTypes`, `maxStepLines`,
  `checkpoint.maxFileBytes`), `docs/design.md`.

## Constraints

- Defaults keep today's behaviour except checkpoints, which are on for
  every project: they only save work that would otherwise be thrown away.
  `checkpoint: {disabled: true}` turns them off.
- Never a force push except the lease-guarded fold of decision 6 on the
  agent branch.
- The default branch, protected branches and other people's commits are
  never rewritten.
- CRD fields are only added.
- Slices stay small and reviewable (< 50 production lines where it fits;
  say so in the MR when a slice has to be bigger).
- R8 waits for milestone 8's hook subcommand to be on main.

## Non-goals

- Snapshots while a call is still running (a timer that commits during a
  long step). Checkpoints happen when a call ends or is lost; the step-size
  gate keeps steps short.
- Rewriting commits that are already on the default branch.
- Conventional Commits for commits people push by hand.
- Turning anything on in my-cluster (decision 9).

## Suggested order

1. Spec copy to `docs/specs/checkpoints-and-conventional-commits.spec.md` +
   R1.
2. R2 (triggers) and R3 (report).
3. R4 (draft, no merge or review of a checkpoint).
4. R5 (prompt line and fold).
5. R6 (conventional commits on branch commits).
6. R7 (conventional MR title and squash message).
7. R8 (step-size gate, after milestone 8).
8. R9, R10 (may go with the slices they describe).

## Acceptance

- A usage limit in the middle of an implement step leaves a pushed
  `chore(wip): checkpoint …` commit with the step's edits, and a draft MR.
  The next run starts from it, and its first step folds the checkpoint into
  one conventional commit written by the agent.
- A pod lost mid-step leaves the same checkpoint, made by the new pod
  before it resets the workspace.
- A merge in progress, a secret file and a 50 MB file never end up in a
  checkpoint.
- The autopilot never merges or reviews an MR whose head is a checkpoint.
- With `conventionalCommits: true` every commit on an agent branch and
  every squash commit on the default branch matches the pattern.
- With `maxStepLines: 400` a run that writes 1 500 lines ends in several
  step commits.
- With nothing configured, commits, prompts and the command line are what
  they are on main today, apart from checkpoints after a cut-off.
