# ai-operator manages GitLab's rate limit

Status: archived; the milestone's spec, run by autopilot and implemented.
Narrowed requirements and requirements without an issue are in the
[Steering log](#steering-log). The docs describe the result:
[GitLab's rate limit](../concepts/how-it-works.md#gitlabs-rate-limit),
[When GitLab limits a run](../guides/when-runs-go-wrong.md#when-gitlab-limits-a-run),
[GitLab rate limit fields](../reference/status-and-history.md#gitlab-rate-limit)
and the [design notes](../contributing/design.md#gitlab-rate-limit).

## Goal

1. The operator, the runners and the Desk server pace their GitLab requests.
   When the bot's per-minute budget runs low, background calls wait and
   run writes go first, so a burst no longer turns into 429s.
2. A run that still meets a 429 or a GitLab 5xx is never a failure for a
   human. It waits out the limit inside the run when the wait is short.
   Otherwise it is deferred and starts again at the reset, by itself.
3. On autopilot nobody needs to click Retry for a GitLab limit. The pilot
   hears about rate-limit deferrals, and when the automatic retries are used
   up it retries the issue itself once GitLab is healthy again.
4. A failed run never leaves a milestone silently stuck. When the failure
   report itself cannot reach GitLab, autopilot still pauses with
   `runFailed`, and the Desk shows the pause card.
5. The Desk shows the limit: the bot's GitLab usage, deferred runs on the
   task, the milestone and the queue, and a refusal for `/agent` commands
   typed into the comment box.

## Background

The incident (2026-11-26, 23:05–23:40 UTC): the bot user hit GitLab Free's
100 requests per minute per user. Every REST call, GraphQL POST and git
HTTPS fetch or push with the token counts against that one bucket. The
operator's polls logged 429s and recovered on the next poll. One implement
run failed on a 429 while listing issue notes. Its failure report could not
reach GitLab either, so the issue stayed on `agent::working`, and the
milestone and the two queued behind it sat still for eight hours with no
pause card. Two `/agent retry` comments from the Desk comment box were
ignored, because they were posted with the bot's own token.

What existed: one Go client for all API traffic (`internal/gitlab`), short
429 retries (3 attempts, about 3 s without a header), infra blocks only for
Spec tasks, and the usage-limit deferral (`deferRun`) as the model to copy.

## Decisions (locked)

1. **Pacing comes from GitLab's own headers.** Each process reads
   `RateLimit-Remaining` and `RateLimit-Reset` from every response, keeps
   the latest value per token, and paces from that. Rejected: a central
   request broker (a network hop per call), a fixed local token bucket (it
   cannot see the other pods' calls).
2. **Two priorities.** `background` (polls, heartbeats, group discovery,
   webhook checks, Desk reads) or `work` (everything from a runner pod,
   pushes, merges, labels, notes, Desk writes). At or below
   `gitlab.rateLimit.reserve` (default 20) a background call waits until
   the reset, or fails as transient past its deadline. At 0 every call
   waits, at most 65 s. The default priority is `work`.
3. **Waiting out a 429 inside the run.** Until `Retry-After` or
   `RateLimit-Reset`, else the next full minute plus jitter; up to 5
   attempts and 2 min in total, reads and writes alike. 5xx keeps the
   current rules.
4. **A run that still fails on GitLab defers.** On a transient GitLab error
   without an engine-side failure, for every task type: the request stays
   pending, `LastResult: Deferred`, the checkpoint is kept, `deferredUntil`
   is the reset plus 10–40 s of jitter (else the lifecycle backoff), and
   the block reason is `gitlab`. The cap is `MaxDeferrals`, shared with
   usage-limit deferrals; at the cap the run fails with `BlockInfra`.
5. **The project shares the limit.** A deferral sets
   `status.gitLab.rateLimit.limitedUntil` on the AgentProject. A run that
   would start before then defers to that time without calling GitLab.
6. **Notes come late and ping no one.** While the limit holds, the runner
   posts nothing. When the run starts again it posts the ⏳ note once.
7. **The pilot takes the human's part.** A `gitlab` block is a pilot event
   `gitlab/<iid>`; the pilot does nothing while a deferral is pending.
   After the cap its counted `retry` is allowed once `limitedUntil` has
   passed. Three or more `gitlab` events in an hour put one line into the
   pilot's next status note.
8. **Backstop in the gate.** An issue on `agent::working` whose task is
   idle with `LastResult: Failed` and `NeedsInputCause: runFailed` pauses
   `runFailed` and gets the needs-input label. A recorded GitLab failure
   raises the run request once instead. `reportFailure`'s writes use
   `gitlab.RetryTransient`.
9. **The usage is visible.** `status.gitLab.rateLimit` on each AgentProject
   (at most once per poll), and the metrics
   `ai_operator_gitlab_ratelimit_remaining` and
   `ai_operator_gitlab_paced_seconds_total{priority}`. Runner pods report
   their reading in the run status patch they already make.
10. **Desk comment box and commands.** With the bot's fixed token, `/agent
    <word>` lines are not posted: a word in the Desk commands goes to the
    chat's handler with the same grants, any other word answers 400. A
    user's own token posts as today.

## Requirements

- **R1** Header tracking and pacing (Decisions 1–2), and the
  `gitlab.rateLimit.reserve` Helm value.
- **R2** In-run 429 waits (Decision 3); `gitlab.Error` carries the reset.
- **R3** Background priority at the operator's call sites.
- **R4** Deferral on transient GitLab errors (Decision 4), the `gitlab`
  block reason and label.
- **R5** Project-wide limit and late notes (Decisions 5–6).
- **R6** Gate backstop and failure-report retries (Decision 8).
- **R7** Pilot event, `retry` after the cap and the pattern line
  (Decision 7).
- **R8** Usage status and metrics (Decision 9).
- **R9** Desk views carry the block reason, the deferral and the project's
  rate limit.
- **R10** Comment-box commands (Decision 10).
- **R11** Task banner per cause.
- **R12** Milestone, queue and project badges and the usage bar.
- **R13** The comment box shows the 400 message inline.
- **R14** Docs and this spec.

## Constraints

- At most 300 production lines per MR, about 150 per commit.
- Usage-limit deferral keeps its behaviour, notes and Desk text.
- Pacing never makes a call fail that would have passed before.
- No new dependencies; Nuxt UI components only; the axe check stays green.

## Non-goals

- Cutting the operator's request volume (webhooks only, caching notes).
- GitLab's hourly or per-group limits.
- Posting Desk comments as the logged-in user.
- New pilot tools, retrying engine failures other than usage limits.

## Acceptance

- In a burst of 4 runs and 4 projects' polls on one token,
  `ai_operator_gitlab_paced_seconds_total{priority="background"}` rises and
  the runs meet no 429.
- A 429 with a reset in 40 s is waited out inside the run.
- With the limit held for 6 minutes the task defers with the `gitlab`
  label; a second task of the project defers to the same time; both start
  by themselves and each issue gets the ⏳ note once.
- After the deferral cap the pilot retries once the limit has passed.
- A stranded `agent::working` issue after a GitLab failure gets a new run
  within one poll; any other cause pauses `runFailed`.
- The project header shows the bot's GitLab usage this minute.
- With a fixed bot token, `/agent retry` in the Desk comment box starts a
  run and posts nothing; `/agent pause` there shows the refusal.

## Steering log

- R5, R8 #886 (!900): the pre-start deferral on the project's limit does
  not count towards `MaxDeferrals`. `gitLabLimitedAt` marks a deferral
  chain; it is cleared before the late note is posted, so a failed status
  write loses the note rather than posting it twice. The project status
  takes the freshest reading by `readAt` and keeps the peak of the last
  hour; the remaining gauge is labelled `user` and shows the full limit
  once the last reading's window has reset. This spec was added in #886
  because the Desk issues already linked to it; R14 completes the docs.
- R1: only reads honour `gitlab.Background`; a write is always `work`,
  whatever its context says.
- R2: the no-header wait is the next full minute plus 1–5 s. 5xx keeps 3
  attempts, for reads only.
- R5: the late ⏳ note is posted on implement tasks of an issue only.
- R6 (Decision 8): a run that fails on GitLab never reaches the gate's
  backstop. At the deferral cap the runner records `failureKind: gitlab`
  and asks for an `infra` block, and the operator's existing infra retry
  starts the one new run after `lifecycle.infraRetryAfter`, once per run
  request (`source.infraRetry`). So the acceptance's "a new run starts
  within one poll" became "after `infraRetryAfter`". The gate's backstop
  (`strandedRun`) handles the other causes and pauses `runFailed`.
- R7: the pattern line is added at most once an hour (`gitlab-pattern/<hour>`).
- R10: only a comment made of bare `/agent <word>` lines is routed. A
  command with arguments (`/agent skip #12`), commands mixed with text, or
  any command while Desk chat is off get the 400 refusal.
- R12 #890 (D890-P1): the usage bar hides once its reading's window reset
  more than a minute ago, since "this minute" would be wrong.
- R14 #892: no reference page lists Helm values and metrics, so
  `gitlab.rateLimit.reserve` and the two metrics are in the status
  reference's *GitLab rate limit* section, with `status.gitLab.rateLimit`.
  The comment box is described in *When runs go wrong*. No follow-up
  issue was opened in this milestone.
