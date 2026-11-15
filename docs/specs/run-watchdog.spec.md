# Runs that hang get noticed and restarted

Status: the milestone's spec, run by autopilot. Narrowed requirements and
requirements without an issue go into the [Steering log](#steering-log).

## Goal

1. A run that stops making progress is noticed within about half an hour,
   by the operator, without an LLM call per task.
2. The pilot is told, gets the evidence (step, logs, pod state), judges it
   and restarts the run, within its autonomy, and escalates when restarts do
   not help.
3. An owner sees a task's raw container logs and pod health in the Desk and
   restarts a stuck run with one button, without cluster access.
4. The hang we had does not happen again: remote git commands cannot block
   forever.

## Background

File refs are at origin/main 4505ba4, 2026-11-12.

**Incident 2026-11-12.** #391 (%20, !417) hung for 13 h in step `18 push`.
The runner logged `step started … push` at 18:51:54, the agent wrote the
bundle at 18:51:57, then nothing. The branch on GitLab stayed at the start
commit. A chat run request (21:46, `runRequest: 2`) was never taken because
the runner loop was still in run 1, and %21, %22 waited behind %20 by strict
order. Deleting the pod by hand fixed it: the interruption path started the
run again, it pushed `1c46853` at 07:51 and succeeded.

What existed before the milestone:

- **Git without a deadline.** `Mirror.runInput`
  (`internal/runner/git.go`) ran `exec.CommandContext` with the run's
  context, which had no deadline; no `http.lowSpeedLimit` /
  `lowSpeedTime`. `push`, `Sync`'s `ls-remote` and fetches are remote.
  Other runner calls already had timeouts.
- **Interruptions.** A run whose pod or runner is lost mid-run is started
  again, counted in `status.run.interruptions`; after `MaxInterruptions`
  (3) it fails with `blockReason: infra`. Hibernation resets the active
  request instead.
- **Progress fields.** `status.run.currentStep` was a string without a
  time. `lastActivityTime` only moved when a run started or finished.
- **Pilot.** Woken by events from status (pauses, blocked, needs input,
  budget, main red, `/pilot`). It never saw logs. Its tools had no
  restart. `pilot.stuckAfter` (30 m) only covered autopilot pauses, not a
  task stuck in `Running`.
- **Desk.** The live transcript already read the `runner` and `agent`
  container logs; the UI role had `get` on `pods`, `pods/log`. There was
  no raw log view and no restart.
- The operator may delete pods but had no `pods/log` grant.

## Decisions (locked)

1. **Cheap tick, pilot on suspicion.** No LLM call on a timer. The operator
   checks every Running task every 5 minutes
   (`lifecycle.stallCheckInterval`, default 5m). The pilot is woken only
   when a check finds a stall. Tasks shorter than the threshold are never
   looked at.
2. **Stall = silence.** A task is stalled when its pod runs and the newest
   line of both the `runner` and the `agent` container log (Kubernetes log
   timestamps, `tailLines: 1`) is older than `lifecycle.stallAfter`
   (default 30m). Also stalled: the pod is `Pending` or in
   `CrashLoopBackOff` / `ImagePullBackOff` (and `ErrImagePull`) for longer
   than `stallAfter`. One threshold for all steps; R1 makes mechanical
   steps fail fast on their own.
3. **Condition, not a phase.** A stall sets condition `Stalled=True` with
   reason (`Silent`, `PodPending`, `CrashLoop`) and message; it clears on
   the next log line or when the run ends. The task stays `Running`.
4. **Step time.** The runner writes `status.run.currentStepSince` with
   `currentStep`, so the evidence and the Desk say how long the step runs.
5. **Restart = the interruption path.** Restart deletes the agent pod; the
   active request is started again as an interruption (counted, so
   `MaxInterruptions` still ends a loop with `blockReason: infra`). The
   workspace PVC keeps the agent's commits. A note on the issue says who
   restarted it, in which step, after how long.
6. **`/agent restart`.** New Desk chat command, owners only, on an
   Implement, Spec or Coordinate task. It answers "Nothing to restart"
   when no run is in progress. The Desk's Restart button sends this
   command, so the operator does the delete and the UI role stays
   `get`-only. Narrowed: Desk chat only; a GitLab-comment form is a
   follow-up (see [Steering log](#steering-log)).
7. **Pilot event `run-stalled/`.** With the evidence: step, step age, stall
   reason, pod phase / restarts / last termination reason, the last 40
   lines of each container log (capped 4 KB each), `pushedSHA` vs the
   branch head on the remote. Logs are untrusted data in the pilot prompt,
   framed as such. Narrowed: events only for Implement tasks.
8. **Pilot tool `restartRun`.** Counted managed tool (runs without owner
   in `managed`, proposed in `propose`), bounded by
   `maxInterventionsPerIssue`. The operator checks `Stalled=True` again
   when it executes; otherwise it refuses with "the run is moving again".
   The pilot prompt gets a short runbook: silent mechanical step →
   restart; long silent agent step → read the last tool use first; pod
   pending or crash looping → restart once, then escalate.
9. **Escalation.** When the pilot's restarts for the issue are used up and
   the task is stalled again, or `MaxInterruptions` is reached, the
   autopilot pauses with kind `stalled` and the evidence in the pause
   card. Resume after Desk `restart` or when the run ends. Narrowed: the
   logs stay out of the pause reason (they are in the Desk).
10. **Desk.** The task page gets a **Logs** tab (raw `runner` / `agent`
    log, tail 500, follow, download) and a health line (pod phase,
    restarts, step and step age, `Stalled` badge with reason), plus a
    **Restart run** button with a confirm dialog, owners only, enabled
    while a run is in progress.

## Requirements

### Runner (Go)

- **R1** Remote git commands (`push`, `fetch`, `ls-remote`) get
  `-c http.lowSpeedLimit=1000 -c http.lowSpeedTime=60` and a per-command
  timeout (5m push/fetch, 1m ls-remote); a timeout fails the step with a
  clear error (`git push timed out after 5m`). Tests with a remote that
  accepts and never answers.
- **R2** `status.run.currentStepSince` written with `currentStep` and
  cleared with it (Decision 4). `make generate manifests`.

### Operator (Go)

- **R3** Stall check (Decisions 1–3): `lifecycle.stallAfter`,
  `lifecycle.stallCheckInterval`, operator RBAC `pods/log` `get`,
  condition `Stalled`, metric `ai_operator_task_stalled`. Tests with a
  fake log source: silent both, silent one (not stalled), pending pod,
  crash loop, clears on new line.
- **R4** Desk `restart` (Decisions 5, 6): `chatcmd` parse + owner check,
  pod delete as interruption, issue note, "Nothing to restart" answer.
  Tests: restart mid-run starts the request again and counts; restart
  with no run does nothing; non-owner refused.
- **R5** Pilot event `run-stalled/` with evidence (Decision 7), once per
  Implement task and stall (event key = issue + stall start).
- **R6** Pilot tool `restartRun`, autonomy and recheck (Decision 8),
  runbook in the pilot system prompt, escalation pause kind `stalled`
  (Decision 9). Tests: managed runs it, propose proposes it, refused when
  no longer stalled, pause after the cap.

### UI (Go)

- **R7** Task view gains health (pod phase, restarts, termination reason,
  step, `currentStepSince`, `Stalled`);
  `GET …/tasks/{task}/logs?container=&tail=&follow=` raw log endpoint
  (reuses `PodLogs`, owners and steer grant only). `make ts-types`.

### Web

- **R8** Logs tab, health line, Restart run button with confirm
  (Decision 10), light and dark, 375px.

### Docs

- **R9** `docs/commands.md` (`restart`), `docs/configuration.md`
  (`stallAfter`, `stallCheckInterval`, git timeouts), `docs/ui.md`,
  `docs/design.md` (stall detection), chart README, and this file with a
  steering log.

## Constraints

- Under 50 production lines per MR; Go and web in separate MRs; R1 first.
  When a requirement has to be narrowed, open a follow-up issue and say so
  in the steering log; every R has an issue or a steering log line.
- No new dependencies.
- The UI role stays `get` on pods and `pods/log`; only the operator deletes
  pods.
- Only the pilot system prompt changes (runbook, `restartRun`); other
  prompts, CSP, budgets and review rules unchanged.
- `make test`, `go vet ./...`, `gofmt`, `make helm-lint`, `make ui`,
  `make ts-types`; CI and the drift job green.

## Non-goals

- Running the pilot on a timer for healthy tasks.
- Per-step thresholds.
- Restarting single containers, exec into pods, or a shell in the Desk.
- Stall detection for research runs (follow-up if needed).
- Log search or log retention beyond what the pod keeps.
- `/agent restart` as a GitLab issue or tracking-issue comment (follow-up).

## Suggested order

R1 → R2 → R3 → R4 → R5 → R6 → R7 → R8 → R9.

## Acceptance

- A runner whose `git push` never gets an answer fails the push step within
  5 minutes with a timeout error.
- A task whose containers are silent for `stallAfter` shows `Stalled=True`
  within one check interval; in `managed` the pilot restarts it, the issue
  gets the note, and the run finishes.
- After the restart cap, the autopilot pauses with kind `stalled` and the
  evidence in the pause card (without the container logs in the reason).
- An owner opens the task in the Desk on a phone, reads the runner log, and
  restarts the run with the button; a non-owner sees no button.

## Requirements and where they were implemented

| Requirement | Implemented in |
| --- | --- |
| R1 remote git timeout and low-speed abort | `internal/runner/git.go`, `git_test.go` |
| R2 `currentStepSince` | `api/v1alpha1`, runner status patches |
| R3 stall check, condition, metric, `pods/log` RBAC | `api/v1alpha1` lifecycle fields, `internal/controller/stall.go`, `metrics.go`, chart `files/rbac/role.yaml` |
| R4 Desk `restart` | `internal/chatcmd`, `internal/controller/restart.go`, chat/autopilot handlers |
| R5 `run-stalled/` evidence | `internal/controller/pilot_events.go` |
| R6 `restartRun`, runbook, pause `stalled` | `internal/controller/pilot_*.go`, `api/v1alpha1/pause.go`, `internal/runner/prompts.go` |
| R7 task health and logs API | `internal/ui` views, `…/logs` endpoint, `make ts-types` |
| R8 Logs tab, health line, Restart run | `web/src` TaskBody, Logs tab, RestartRun |
| R9 docs and this spec | `docs/commands.md`, `configuration.md`, `ui.md`, `design.md`, chart README, this file |

Issues: R1 #477, R2 #478, R3 #482, R4 #485, R5 #487, R6 #489, R7 #491,
R8 #496 and #497, R9 #498 (!507).

## Steering log

- R1 #477: remote git `http.lowSpeedLimit`/`Time` and timeouts (5m
  push/fetch, 1m ls-remote).
- R2 #478: `status.run.currentStepSince` with `currentStep`.
- R3 #482: `lifecycle.stallAfter` / `stallCheckInterval`, condition
  `Stalled`, metric `ai_operator_task_stalled`, operator `pods/log` `get`.
- R4 #485: Desk chat `restart` only (owners; Implement, Spec, Coordinate).
  **Narrowing:** no GitLab-comment `/agent restart` in this milestone; open
  a follow-up for that form.
- R5 #487: pilot event `run-stalled/<iid>/<stall-start>` with evidence.
  **Narrowing:** events only for Implement tasks (Spec/Coordinate stalls
  do not wake the pilot).
- R6 #489: counted `restartRun` with recheck, pilot runbook, pause kind
  `stalled`. **Narrowing:** container logs stay out of the pause reason
  (Desk *Logs* tab holds them).
- R7 #491: task health in the API and raw `…/logs` endpoint (owners +
  steer).
- R8 #496, #497: Desk health line, Logs tab (tail 500, follow, download),
  Restart run button with confirm.
- R9 #498 (!507): docs and this spec; documentation only, no release after
  it. Docs correction: the pause card does not send `restart` (not in the
  web's `remedyActions`); owners use the task *Restart run* button or Desk
  chat. Wiring `restart` as a pause-card button is a follow-up. Restart's
  GitLab note is only when the task has an issue (Spec without autopilot:
  Desk answer only).
