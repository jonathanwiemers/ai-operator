# Pipeline health, infra retries and the blocked state

## Problem

Two real cases on Bloop (milestone M16, autopilot, tracking issue #338):

1. **Main red for ~11 hours** (2026-10-24 13:25 → 00:39 UTC): 5 of 13 main
   pipelines failed, every one on the same flaky vitest test
   (`challengeDriver.test.ts > replays logged shots without live input`, job
   `test:coverage`); the pipelines in between were green. Autopilot paused once
   (!617, "red with jobs that also fail on the default branch… Fix it"), but
   nobody owned the fix: no issue, no ping beyond the pause. MR pipelines that
   happened to pass were merged, so main stayed red underneath. The operator
   only looks at the default branch inside `baseFailing`, on demand, for one MR.
2. **Autopilot paused ~7 hours overnight on #317 / !633**: both reviewers
   failed with `cursor-agent failed: exit status 1: RetriableError:
   [unavailable] Error`, a transient outage. The gate treats
   `run.ReviewErrors > 0` as a pause that only a new run can lift. The board
   showed #317 as `agent::working`, so nothing looked stuck. `/agent investigate
   the root and fix it` on the tracking issue was silently ignored (only
   `pause|resume|skip|revisit` are handled there). The same comment on #317
   started run #2, whose agent did not know the pause reason and investigated
   missing screenshots instead (36 min, $7.93).

Root causes:

- Transient infra errors (reviewer outages) are treated like problems a human
  must solve.
- Nobody watches the default branch; a red or flaky main has no owner and does
  not stop autopilot from merging.
- `agent::needs-input` mixes "a human must answer" with "the agent cannot get
  on for an outside reason"; the latter is invisible on the board.
- Commands the tracking issue does not know are dropped without an answer, and
  runs started during a pause do not get the pause reason.

## Goals

- Retry transient reviewer failures inside the run; if they keep failing,
  re-review the head automatically later instead of pausing for a human.
- Watch the default branch's pipeline per project; retry red jobs once to tell
  flaky from broken; keep one issue per red main and one per flaky job.
- Freeze autopilot merges while main is red; resume by itself once it is
  green again.
- A new state label `agent::blocked` plus a reason label
  `agent-blocked::<reason>`, set and cleared by the operator, so the board
  shows what waits on something outside the issue.
- Answer unknown commands on the tracking issue; give runs started during a
  pause the pause reason.
- Later: a **milestone coordinator**, the user's conversation with ai-operator on the tracking issue (and a UI chat over it), with read-only triage
  as its automatic mode (see [Later](#later-the-milestone-coordinator)).

## Non-goals

- An LLM that merges, closes or pushes, or bypasses the merge gate.
- Fixing the flaky test or the agent image's missing Rust toolchain / browser
  libraries: those are Bloop issues.
- Parsing test names out of job logs (left to the coordinator).
- Labels on merge requests (open question 3).
- An `agent::queued` state for pending autopilot items (open question 2).

## Constraints

- Deterministic first: everything in PRs 1–7 is plain operator code with unit
  and controller tests; the coordinator acts only through it.
- GitLab budget: the bot user's API rate limit is shared by all projects. Main
  health adds at most one `ListPipelines` per project per
  `mainHealth.interval` (default 5m), plus job calls only when the pipeline
  changed.
- The blocked labels stay idempotent and recoverable: the operator derives
  them from `status.blocked` every poll, so a failed label call is repeated.
  (The other workflow labels keep today's behaviour: they are set on
  transitions.)
- Scoped labels: `agent::blocked` replaces the other `agent::*` label (GitLab
  keeps one per scope). The label it replaced is stored and restored.
- Job logs and job names are untrusted text: defused (`notify.Defuse`) in
  comments, marked untrusted in prompts. Job names in issue titles are
  reduced to `[A-Za-z0-9 :._/-]`, capped at 60 characters, and set in code
  spans.
- PR budget `< 50` production lines each (tests, docs, generated files
  excluded); PRs land in the order below.

## Approach

### 1. Reviewer retries (runner)

- `classifyTransient(err)`: a failed invocation whose message matches
  `RetriableError|\[unavailable\]|ECONNRESET|connection reset|502 Bad
  Gateway|503 Service Unavailable|504 Gateway` and is not a usage limit
  (`AsUsageLimit` wins). No bare status codes, like `limitPatterns`.
- In `review()`, each reviewer retries a transient failure up to
  `agents.review.retries` times (default 2) with 30s, then 2m backoff (bounded
  by the run's context). A non-transient error fails at once, as today.
- The run status records `ReviewErrorsTransient` (all of the last review's
  errors were transient) next to `ReviewErrors`; the report line says
  "failed after 3 attempts".

### 2. Re-review run (runner + autopilot)

- New `spec.source.reviewFor: <sha>` (like `mergeBaseFor`): the run skips the
  implementer, checks out the head, reviews `base..head` with the issue and
  plan decisions, and records `ReviewedSHA`, findings, `ReviewErrors`. It
  pushes nothing; it posts a short report. Blocking findings go to the
  implementer as usual (`review.maxIterations`), i.e. the run then continues
  like a normal one.
- Autopilot gate (a new branch in `gateCheck`; today any `ReviewErrors > 0`
  is a `pauseRun`): `ReviewErrors > 0 && ReviewErrorsTransient` and no
  re-review was requested for this head yet → request one after
  `agents.review.retryAfter` (default 15m), state "waiting", no pause. Only a
  second transient failure on the same head pauses, now as a **blocked**
  pause (see 4) with reason `infra`.
- `/agent review` on an issue or MR requests the same run by hand (also off
  autopilot).

### 3. Tracking issue answers, pause context

- A `/agent …` comment on the tracking issue that is not
  `pause|resume|skip|revisit` gets a reply: what the tracking issue
  understands, and "to instruct the agent, comment on #N (the active issue)".
  Until the coordinator exists it does nothing else.
- While autopilot is paused for an issue, a run of that issue gets the pause
  reason in its prompt ("Autopilot paused because: …"), as untrusted operator
  context, before the human comments.

### 4. `agent::blocked` and reason labels

- `WorkflowSpec.BlockedLabel` (default `agent::blocked`) and
  `BlockedReasonPrefix` (default `agent-blocked::`). Reasons (enum):
  `infra` (reviewer or engine outage, agent pod lost, OOM / interruption
  give-up), `main-red` (merge freeze), `quota` (usage-limit deferral),
  `dependency` (autopilot: waits for a skipped or "blocked by" issue).
- `AgentTask.status.blocked`: `reason`, `message`, `since`, `restoreLabel`
  (the `agent::*` label it replaced). Set and cleared only through two helpers
  (`block`, `unblock`) in the controller; the poll re-applies the labels from
  the status, so a failed call heals.
- `ActiveLabels()` includes the blocked label: the gate no longer reports
  "no workflow label", and the issue keeps its task and pod.
- While blocked, the CI loop does not follow the issue (it only blocks when
  the loop is final or no run is pending). `unblock` restores
  `restoreLabel`; a human moving the issue to `agent::ready` or commenting
  `/agent …` clears the block (`unblock` before the run request).
- First users in this PR: autopilot pauses whose reason is not a human's to
  fix (transient reviewers after the re-review, `main-red` from 6) block the
  active issue; their auto-resume unblocks it. `needs-input` keeps meaning "a
  human must answer or decide".

### 5. Failed runs: blocked for infra causes

- `reportFailure` moves the issue to `blocked` + `agent-blocked::infra`
  instead of `needs-input` when the failure is: agent lost, interruptions
  give-up, OOM give-up, all reviewers transient on a non-autopilot task.
  Usage-limit deferral sets `agent-blocked::quota` while deferred and
  restores `agent::working` when the run resumes; the give-up after
  `MaxDeferrals` stays `blocked::quota`.
- A blocked issue with reason `infra` gets one automatic new run request after
  `lifecycle.infraRetryAfter` (default 30m), once per run request; then it
  waits for a human. The comment says so and pings the owners (event
  `blocked`, part of `notify.events`).

### 6. Main health watcher and merge freeze

- `AgentProjectSpec.MainHealth`: `disabled`, `interval` (5m), `retryFailedJobs`
  (true), `issue` (true), `freezeAutopilot` (true).
- Project controller, every `interval`: newest finished pipeline of the
  default branch (`ListPipelines{Ref, Scope: finished, Limit: 1}`). New
  `AgentProject.status.mainHealth`: `state` (green, retrying, red, flaky),
  `pipelineID`, `sha`, `failedJobs`, `since`, `issueIID`, `retriedPipelineID`.
- Red, first time for this pipeline → retry its failed jobs once
  (`RetryPipeline`, like the MR loop), state `retrying`.
  - Green after the retry → state `flaky` for those jobs: comment on (or
    create) one issue per job, title "🔁 Flaky CI job: `<job>`", labels
    `ai-generated`, `ci::flaky`; occurrences listed with pipeline links. No
    freeze. Dedupe by a hidden marker with the job name.
  - Still red → state `red`: create or update one issue "🚑 `<default
    branch>` is red: `<jobs>`" (labels `ai-generated`, `ci::main-red`), with
    the pipeline, commit, failing jobs and the defused tail (40 lines) of each
    job log, pinging the owners (event `mainRed`). Later red pipelines add a
    comment only when the failing jobs change.
- Green again → comment "✅ green again at `<sha>`" and close the main-red
  issue; `state` green.
- `baseFailing` reads `status.mainHealth` when it is fresh (≤ interval)
  instead of listing pipelines; its comment links the main-red issue.
- Freeze: with `freezeAutopilot`, the merge gate fails with reason
  `main-red` ("`main` is red (#N): merging waits until it is green") while
  `state` is `red`. The freeze is checked last, after run, review and CI
  passed the gate, so the CI loop of the active issue is already final when
  it gets blocked (`agent-blocked::main-red`); a blocked issue's CI is not
  followed (PR 3). When main is green, the block is lifted, the gate runs
  again (CI the loop never saw sends the issue back to working first) and
  autopilot continues by itself. Nothing is merged and no new issue is
  started while frozen. `flaky` does not freeze.

### 7. Main-red issue on the agent's queue (opt-in)

- `mainHealth.autoFix: true`: the main-red issue is created `agent::ready`
  and runs like any issue (implemented, reviewed, CI-checked).
- Not through `queueFollowUp`: that appends to the queue, and a frozen
  autopilot does not pick. Instead autopilot gets a **main-red lane**: while
  frozen, it drives the issue named by `status.mainHealth.issueIID` (and only
  that one) through the same merge gate and merges it. That is the only merge
  allowed during a freeze. Off autopilot, a human merges it.
- A flaky-job issue stays for humans (quarantining a test is a judgment call)
  until the coordinator exists.

### Later: the milestone coordinator

Superseded by [milestone-coordinator.spec.md](milestone-coordinator.spec.md);
the sketch below is kept for history.

Separate spec once 1–7 run for a while. The coordinator is the main way a
user talks to ai-operator about a milestone: "change the spec so the Rink
gets a second flipper", "why is it paused?", "skip #320 and tell me what that
breaks". Triage (classifying a pause or a red main) is not a separate agent but
the coordinator's automatic, read-only mode.

**Shape.**

- One coordinator per milestone: a new task type `Coordinate` with its own
  pod and resumed session, like an issue task; role `agents.coordinator`.
- **The conversation is the tracking issue.** A free-text `/agent …` from an
  owner on the tracking issue goes to the coordinator, which answers there.
  The web UI gets a milestone page whose chat is that thread (the
  Conversation panel and the `steer` permission exist already, comments go
  out in the user's name). GitLab stays the record; the chat works without
  the UI.
- **The coordinator acts only through what a human could do**: comments,
  `/agent` commands on issues and MRs, edits of the milestone description
  (which re-plans: unstarted issues follow the new plan, started and merged
  ones are kept), and the tracking-issue commands. It has no merge, close or
  push tool; the merge gate, the freeze and the CI loop stay deterministic
  and are the safety net.
- **Reads** come from the operator's own state (task statuses,
  `status.mainHealth`, autopilot queue, plan, decisions) plus GitLab reads;
  it does not poll main itself.

**Authority.**

| Action | Needs the user's confirmation in the thread |
|---|---|
| answer, explain, summarise | no |
| retry a run, re-review, `/agent …` instruction on the active issue | no |
| open an issue (e.g. flaky test, missing tool in the agent image) | no |
| edit the milestone spec (shows the diff first), revisit a decision | yes |
| `skip`, `pause`, `resume` | yes |
| anything that merges, closes or pushes | not available |

Only comments of the milestone's owners count as instructions; other
comments and all job logs are untrusted data.

**Triggers.** A free-text message on the tracking issue; automatically
(read-only triage) on an autopilot pause, a block with reason `infra`, and a
new main-red or flaky issue: it classifies (`infra|flaky|regression|env|
decision|unknown`) with evidence and **proposes** an action in a comment. An
automatic trigger never acts: its context holds job logs and other people's
comments, so the actions in the table above run only on an owner's message
(or the owner's yes to a proposal).

**The end.** When autopilot is done, the coordinator runs a read-only
retrospective subagent and posts the last update on the tracking issue before
it is closed: what merged, the decisions and their revisits, every pause with
how it ended, cost and time, and proposed follow-ups (as a list the user can
turn into issues with one reply). Then its task and pod are deleted.

**Risks.** Cost (one more resumed session per milestone; triage on a light
model); an LLM that edits specs (diff + confirmation, re-plan keeps started
work); prompt injection through comments and logs (owners only, untrusted
data, no merge tool).

## Label set

| Label | Set by | Meaning |
|---|---|---|
| `agent::draft` / `ready` / `working` / `review` / `done` | as today | unchanged |
| `agent::needs-input` | agent / operator | a human must answer or decide (questions, CI red after the fix rounds, a real run failure) |
| `agent::blocked` | operator | the issue cannot go on for an outside reason; no question for you |
| `agent-blocked::infra` | operator | reviewer / engine outage, agent pod lost, OOM or interruption give-up |
| `agent-blocked::main-red` | operator | autopilot merge freeze while the default branch is red |
| `agent-blocked::quota` | operator | usage-limit deferral |
| `agent-blocked::dependency` | operator | autopilot: waits for a skipped or "blocked by" issue |
| `ci::main-red` | operator | the issue tracking a red default branch |
| `ci::flaky` | operator | the issue tracking one flaky CI job |

Bloop's existing plain `blocked` label is unrelated and left alone.

## Affected files

- `api/v1alpha1`: `ReviewSpec.Retries/RetryAfter`, `RunStatus.ReviewErrorsTransient`,
  `SourceSpec.ReviewFor`, `WorkflowSpec.BlockedLabel/BlockedReasonPrefix`,
  `AgentTaskStatus.Blocked`, `MainHealthSpec`, `AgentProjectStatus.MainHealth`,
  `LifecycleSpec.InfraRetryAfter`, notify events `blocked`, `mainRed`;
  defaults; generated deepcopy / CRDs / chart `files/`.
- `internal/runner`: `limits.go` (transient classifier), `implement.go`
  (review retries, re-review run), `prompts.go` (pause context),
  `runner.go` (`reportFailure`, deferral labels).
- `internal/controller`: `autopilot.go` (gate, commands reply, freeze,
  blocked pauses), `ci.go` (`baseFailing`), new `blocked.go`, new
  `mainhealth.go`, `agentproject_controller.go` (poll wiring, run requests
  for `/agent review`), `revisit.go` (queue the main-red issue).
- `internal/command`: `/agent review`.
- `internal/gitlab` (+ fake): job trace tail if not there yet.
- Docs: `docs/usage.md` (labels, board, CI feedback, autopilot pauses, main
  health), `docs/configuration.md`, `docs/commands.md`, `docs/design.md`,
  chart README.

## Risks / trade-offs

- **Retrying main's failed jobs** costs runner minutes and hides a flake for
  one cycle; recording it as `flaky` makes it visible instead. Projects that
  must not retry set `mainHealth.retryFailedJobs: false` (red right away).
- **Freeze** can stall a milestone on a red main nobody fixes; the main-red
  issue pings the owners, and `autoFix` lets the agent fix it.
- **Blocked label** adds a state every label-driven code path must know.
  Mitigation: blocked only when no run is pending and the CI loop is final;
  `ActiveLabels` includes it; tests for each transition back.
- **Transient classifier** may mistake a real failure for an outage: bounded
  by 2 in-run retries plus one re-review per head, then a human.
- **Re-review on a head** costs reviewer tokens again; only after transient
  failures.
- **API budget**: main health is one call per project per interval; job and
  trace calls only on a new red pipeline.

## Validation

- Unit: `classifyTransient` (cursor `RetriableError`, usage limit wins, real
  errors stay), review retry with a scripted engine (fail, fail, pass; fail
  hard), `block`/`unblock` label options, main-health state machine
  (green → retrying → flaky / red → green), issue dedupe markers, defused log
  tails.
- Controller (fake GitLab): autopilot with transient reviewer errors requests
  one re-review, then a blocked pause; freeze holds the merge and auto-resumes
  on green; blocked issue goes back to its old label; a human `agent::ready`
  on a blocked issue runs; unknown tracking-issue command gets a reply.
- Runner e2e: re-review run pushes nothing, records `ReviewedSHA`; pause
  reason in the prompt.
- Replay of the two Bloop cases in tests: the `[unavailable]` reviewer
  message; main red / green on retry / red with the same job.
- Rollout on Bloop first (`mainHealth` on, `autoFix` off), watch one milestone.

## Delivery

GitHub `jonathanwiemers/ai-operator`, one PR per row, in this order. Each PR
updates the docs for its own behaviour.

| # | PR | Depends on | Size (prod lines) |
|--:|---|---|--:|
| 0 | This spec | – | 0 |
| 1 | Review: retry transient reviewer failures in the run | – | ~40 |
| 2 | Tracking issue: answer unknown commands; pause reason in the run prompt | – | ~35 |
| 3 | `agent::blocked` + `agent-blocked::*`: types, `block`/`unblock`, `ActiveLabels`, label sync | – | ~50 |
| 4 | Re-review run (`reviewFor`, `/agent review`); autopilot re-reviews transient failures, then a blocked pause | 1, 3 | ~50 |
| 5 | Failed runs: infra causes and usage limits block instead of needs-input; one automatic retry | 3 | ~45 |
| 6a | Main health watcher: status, retry once, flaky / main-red issues | – | ~50 |
| 6b | Merge freeze on red main (blocked `main-red`, auto-resume); `baseFailing` uses main health | 3, 6a | ~35 |
| 7 | `mainHealth.autoFix`: main-red issue on the agent's queue | 6b | ~40 |
| 8 | Milestone coordinator (own spec, own split): C1 task type + read-only answers on the tracking issue; C2 safe actions + automatic triage; C3 spec edits with confirmation; C4 retrospective on done; C5 UI milestone page with the chat | 2, 6a | – |

1, 2, 3 and 6a are independent and can be built in parallel; 6a + 6b alone
would have prevented case 1, 1 + 3 + 4 case 2.

## Open questions

1. Retry main's failed jobs by default (`retryFailedJobs: true`) or only
   report? Recommendation: retry; that is what turns case 1 into a flaky
   issue instead of an 11-hour red.
2. Should pending autopilot items show `agent::queued` instead of
   `agent::draft`? Out of scope here; `agent-blocked::dependency` covers the
   stuck ones.
3. Mirror `agent::*` and `agent-blocked::*` onto the MR? Cheap follow-up; the
   board is issue-based, so not needed for the goals.
4. Default `infraRetryAfter` (30m) and `review.retryAfter` (15m) are guesses;
   tune after the Bloop rollout.

## Steers

- Spec review (bugbot, security, 2026-10-25): the freeze runs after the CI
  loop is final; `autoFix` uses a main-red lane bound to
  `status.mainHealth.issueIID` instead of `queueFollowUp`; automatic triage
  only proposes; job names in titles are sanitised; the PR 4 gate branch is
  new code; only the blocked labels are re-derived every poll.

- PR 1: `status.run.reviewErrorsTransient` moves to PR 4, where the autopilot
  gate first reads it; PR 1 only retries and reports the attempts.

- PR 6a came to about 270 code lines, not ~50: the watcher is a state
  machine (green, retrying, red, flaky) with its issue handling. It was split
  into 6a-1 (#50) and 6a-2 (#53), the smallest useful slices. Kept on review:
  the check waits for the next interval after a GitLab error, and a main-red
  issue a human closed is not reopened while the same jobs stay red.
- PR 2: the pause reason reaches the issue task one poll late (copied with
  the run request). Follow-ups from its security review: `PausedReason` (job
  names) in tracking-issue comments is not defused yet, and anyone who can
  comment can command the tracking issue (as on issues).

- PR 4 was split into 4a (#55) and 4b (#56), about 105 lines of Go each;
  PR 6b into 6b-1 (#57, 106) and 6b-2 (#58, 25). Kept on review: one
  automatic re-review per head (a later transient failure on the same head
  pauses at once); `unblockActive` lifts any infra block of the active
  issue on resume (revisit in PR 5); a failing GitLab keeps a red main
  health fresh, so merges stay frozen during an outage (safe side).
- The gate waits for any blocked task (#57), so an infra-blocked issue ends
  its pause by a new run, `/agent resume`, or a merge or close by hand. If a
  frozen issue was already infra-blocked, it gets the infra block again on
  the next gate check after main is green.
- Follow-up for all commands: anyone who can comment can repeat `/agent
  review` (cost), like every `/agent …`; a per-head cap or owners-only
  commands would cover it.

- PR 5 came to about 92 + 61 and PR 7 to about 77 + 103 lines of Go.
  Rules settled in PR 5: a new run (running, or pending without a
  deferral) lifts any block; a human `/agent` or ready move during a
  deferral shows `working` while the run still waits for the reset; a block
  a failed run asked for is not lifted by `unblockActive`, only by a new
  run. PR 7: autoFix hands over only issues whose logs carry the untrusted
  marker; CI fix prompts carry job logs as untrusted data.
- With PRs 1-7 merged, this spec is delivered; the coordinator continues in
  `milestone-coordinator.spec.md`.

## PRs

| # | Title | Link | Status |
|--:|---|---|---|
| 0 | Spec: pipeline health, infra retries and the blocked state | [#47](https://github.com/jonathanwiemers/ai-operator/pull/47) | merged 2026-10-25 |
| 1 | Review: retry reviewers that fail transiently | [#48](https://github.com/jonathanwiemers/ai-operator/pull/48) | merged 2026-10-25 |
| 3 | Workflow: agent::blocked and agent-blocked::<reason> labels | [#49](https://github.com/jonathanwiemers/ai-operator/pull/49) | merged 2026-10-25 |
| 2 | Autopilot: answer unknown tracking-issue commands, pause reason in the run prompt | [#52](https://github.com/jonathanwiemers/ai-operator/pull/52) | merged 2026-10-25 |
| 6a-1 | Main health: watch the default branch, retry once, main-red issue | [#50](https://github.com/jonathanwiemers/ai-operator/pull/50) | merged 2026-10-25 |
| 6a-2 | Main health: one issue per flaky job | [#53](https://github.com/jonathanwiemers/ai-operator/pull/53) (replaces #51, closed with its base branch) | merged 2026-10-25 |
| 4a | Review: re-review run (reviewFor, /agent review), reviewErrorsTransient | [#55](https://github.com/jonathanwiemers/ai-operator/pull/55) | merged 2026-10-25 |
| 4b | Autopilot: re-review transient reviewer failures, then a blocked pause | [#56](https://github.com/jonathanwiemers/ai-operator/pull/56) | merged 2026-10-25 |
| 6b-1 | Autopilot: merge freeze while the default branch is red | [#57](https://github.com/jonathanwiemers/ai-operator/pull/57) | merged 2026-10-25 |
| 6b-2 | CI: base-failing check reads main health, links the main-red issue | [#58](https://github.com/jonathanwiemers/ai-operator/pull/58) | merged 2026-10-25 |
| 5a | Runs: usage-limit deferrals block the issue (quota) | [#62](https://github.com/jonathanwiemers/ai-operator/pull/62) | merged 2026-10-25 |
| 5b | Runs: infra failures block the issue, with one automatic retry | [#63](https://github.com/jonathanwiemers/ai-operator/pull/63) | merged 2026-10-25 |
| 7a | Main health: autoFix hands the main-red issue to the agent | [#64](https://github.com/jonathanwiemers/ai-operator/pull/64) | merged 2026-10-25 |
| 7b | Autopilot: main-red lane merges the fix of a red default branch | [#65](https://github.com/jonathanwiemers/ai-operator/pull/65) | merged 2026-10-25 |
