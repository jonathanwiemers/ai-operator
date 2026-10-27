# GitLab home, webhooks, hardening and the pilot's next steps

## Problem

ai-operator's home is moving to GitLab (`jo.wiemers/ai-operator`), but CI,
docs and the operator still assume GitHub as the primary place of work:

1. **GitHub-only CI.** Checks run only in `.github/workflows/ci.yml`. An MR on
   GitLab has no pipeline. Releases stay on GitHub / ghcr (that is fine); the
   day-to-day home does not.
2. **Bloop M16 / M17 gaps.** A planning run fails when one GitLab call times
   out (`applyPlan` posting decisions); a failed planning run pings nobody
   because the tracking issue is created only after planning; a branch that
   changes pin files can leave toolchains in the shared `$HOME/.toolchains`;
   owners are resolved by username at `yes` time (a renamed user or a
   look-alike can pass); `/agent` from anyone who can comment starts a run;
   `PausedReason` can quote job names into GitLab without `notify.Defuse`;
   `JobTrace` reads whole job logs into memory before keeping the last 4 MiB.
3. **Polling budget.** The project controller syncs every AgentProject every
   `pollInterval` (2m in the netcup01 defaults), listing issues, notes and
   pipelines. On GitLab Free the bot shares 100 requests per minute; webhooks
   (available per project on Free; group webhooks are not) are unused.
4. **Canceled main never released.** A default-branch pipeline canceled by a
   newer commit (Bloop launch commit `074ff650`) is skipped when picking the
   newest finished pipeline, so the commit never gets a green run and never
   releases.
5. **Coordinator C3–C5 still open.** Spec edits with a diff and an owner's
   `yes`, the retrospective when autopilot is done, and the milestone page
   with chat in the web UI are specified in
   [milestone-coordinator.spec.md](milestone-coordinator.spec.md) but not
   built.

## Goals

- **GitLab home:** every MR and `main` get a GitLab pipeline with the same
  checks as today's GitHub Actions; the Go (and Node) toolchains are pinned
  for the agent image; the docs say GitLab is the primary repository and
  GitHub a mirror that still builds the releases.
- **Hardening:** close the M16/M17 gaps above (resumable plan apply, tracking
  before planning and one transient retry with an owner ping, per-run
  toolchain dirs for branch-changed pins, pinned owner IDs on proposals,
  opt-in owners-only `/agent`, defused pause reasons, bounded `JobTrace`).
- **Webhooks:** project webhooks wake the operator within seconds of a
  comment, label move, pipeline or merge; polling becomes a slow fallback and
  saves API budget.
- **Main health:** a canceled default-branch pipeline whose commit has no
  successful pipeline of its own is retried once and reported if canceled
  again.
- **Pilot's next steps:** deliver C3 (`editSpec`), C4 (retrospective) and C5
  (UI milestone chat) as
  [milestone-coordinator.spec.md](milestone-coordinator.spec.md) describes
  them.

## Non-goals

- Moving releases, images or the chart off GitHub / ghcr, or changing Flux.
- Setting up the GitLab push mirror, the AgentProject in `infra/cluster`, the
  bot's membership, runners, or the public HTTPRoute for `/hooks/gitlab`:
  done by hand before arming / after the receiver ships.
- Group webhooks (GitLab Premium).
- Removing the GitHub Actions workflows.
- New pilot tools beyond C3–C5; changing the default of
  `workflow.commandsFrom` (stays `members`).

## Constraints

- `CLAUDE.md`: after API or kubebuilder marker changes run
  `make generate manifests`; before pushing `gofmt`, `go vet ./...`,
  `make test`, `make helm-lint`; after web changes `make ui`; never put
  kubebuilder RBAC markers into `internal/ui`.
- Every MR from slice `gitlab-ci` on keeps the GitLab pipeline green and the
  drift check clean.
- Target < 50 changed production lines per MR (tests, generated files, docs
  excluded); split further where a slice is bigger. Each MR leaves `main`
  releasable: every merge to `main` that touches code, the chart or the
  Dockerfiles is released. Docs-only changes are not released.
- Tests at the lowest layer that owns the behaviour; controller tests with
  the fake GitLab (`internal/gitlab/gitlabfake`); mutation-check the tests of
  each fix.
- Each MR of this milestone appends exactly one line to this spec's
  [Steering log](#steering-log).
- Subtle slices (webhook receiver and token check, self-registration, plan
  apply idempotence, spec retry and tracking order, per-run toolchain dirs,
  owner-ID pinning, commands-from, retry of canceled pipelines, `editSpec`,
  the UI's milestone API and authorization) use the strong profile and high
  effort.
- **Hard invariants (decision 13, locked):** existing CRD fields stay
  backward compatible (no field removed, renamed or retyped; new fields
  optional with defaults that keep today's behaviour); the autopilot merge
  gate and the pilot's authority do not widen (the pilot gains no merge,
  close or push; nothing that needs a `yes` today runs without one). Any MR
  that breaks one of these is a blocking review finding.

## Approach

Locked decisions 1–12 below; design notes that are not locked but that later
slices must follow unless the steering log records a deviation.

### 1. GitLab CI (locked)

`.gitlab-ci.yml` with the jobs of `ci.yml` (drift, gofmt, vet,
`go test -race`, `make ui`, `make build`, `make helm-lint`,
`hack/next-version.sh`), runner tag `shared`, images through the GitLab
Dependency Proxy (`${CI_DEPENDENCY_PROXY_GROUP_IMAGE_PREFIX}`), on MRs and
`main`. No release job on GitLab: releases stay on GitHub (`release.yml`,
ghcr, Flux unchanged), fed by a GitLab push mirror to GitHub that the owner
sets up by hand.

### 2. Go pin (locked)

`.mise.toml` pins `go` to the version in `go.mod` and `node` to the version
the web UI builds with, so T1 installs them in the agent container.

### 3. Plan apply is resumable (locked)

Every GitLab write in `applyPlan` retries transient errors (timeouts, 5xx,
429) with the client's backoff; posting decisions is idempotent (a decisions
note with the plan key's marker is not posted twice). A run that still fails
keeps the issues it created; the next run matches them by plan key.

### 4. Planning failures are seen and retried (locked)

On autopilot milestones the tracking issue is created before the first
planning run. A spec run that fails for a transient cause (GitLab or network
errors, agent lost) is retried once after `lifecycle.infraRetryAfter`; a
failure posts on the tracking issue and pings the owners (event `failed`).

### 5. Branch pins are enforced by the runner (locked)

When the agent's branch changes a pin file (`Mirror.PinFiles`), the runner
starts the agent with per-run toolchain directories (`MISE_DATA_DIR`,
`RUSTUP_HOME`, `CARGO_HOME` under a temporary path) for that run;
default-branch pins keep the shared `$HOME/.toolchains`.

### 6. Owner IDs are pinned (locked)

A proposal records the owners' user IDs when it is posted; a `yes` counts
only from those IDs.

### 7. Owners-only commands (opt-in, locked)

`workflow.commandsFrom: members|owners`, default `members` (today's
behaviour). With `owners`, `/agent …` on issues and MRs counts only from the
milestone's owners; others get one reply per user per 24 h, like the pilot's
rejection.

### 8. Defused pause reasons (locked)

`PausedReason` goes through `notify.Defuse` wherever it reaches GitLab
(comments, tracking description).

### 9. Streaming job traces (locked)

`JobTrace` keeps only the last 4 MiB while reading (bounded memory);
behaviour otherwise unchanged.

### 10. Canceled main pipelines (locked)

`mainHealth.retryCanceled` (default true): a canceled default-branch
pipeline whose commit has no successful pipeline of its own is retried once;
if it is canceled again, the watcher comments on (or creates) the main-health
issue with the commit and pipeline. A commit covered by a later green
pipeline of the same commit is fine.

### 11. C3 / C4 / C5 (locked)

Built as [milestone-coordinator.spec.md](milestone-coordinator.spec.md)
describes them (sections "Tools and authority" `editSpec`, "Retrospective",
"Web UI"), with the review steers of that spec; `editSpec` always needs a
`yes` (also in `managed`), C5 adds the milestone-scoped UI API that spec
names.

### 12. Webhooks (locked)

- **Receiver:** the operator manager serves `POST /hooks/gitlab` on its own
  port (`webhook.port`, default 8082) with a chart Service
  `<release>-webhook`; exposing it publicly is the cluster's job (non-goal).
  Body capped at 1 MiB, 10 s budget, answers 202 at once.
- **Trust:** a delivery counts only with a matching `X-Gitlab-Token`
  (constant-time compare) against the project's secret
  (`AgentProjectSpec.Webhook.SecretRef`); the payload is never trusted as
  data: it only names the project (by `project.path_with_namespace`) to sync
  now. Unknown projects and bad tokens get 401 and are logged without the
  body.
- **Self-registration:** with `spec.webhook.enabled` and `spec.webhook.url`,
  the operator creates or updates one project hook (issues, notes, merge
  requests, pipelines, pushes; `enable_ssl_verification`, the secret token)
  through the API, which needs Maintainer; a missing permission is a
  condition on the AgentProject, not a crash.
- **Adaptive polling:** while deliveries arrive, a project is polled every
  `webhook.fallbackInterval` (default 10m); after
  `webhook.silenceThreshold` (default 30m) without a delivery, or with
  webhooks off, it goes back to `pollInterval`. Milestones keep being polled
  at `pollInterval` (milestone events are not covered).
- A burst of deliveries for one project coalesces into one sync (the existing
  requeue).

### Design notes (plan; not locked)

Later slices follow these unless a steering-log line records a deviation.

- **Webhook wake path.** Webhook deliveries wake the AgentProject controller
  in-process (controller-runtime channel source). The receiver runs on the
  leader only and needs no new RBAC. A per-project trailing debounce of 5 s
  coalesces bursts before the sync.
- **Adaptive polling detail.** Keep a light, milestone-only poll at
  `pollInterval`. A full sync runs on a delivery, on a milestone change, or
  after `fallbackInterval`.
- **Twice-canceled main.** A twice-canceled main pipeline is reported on the
  open main-red issue, else on its own non-freezing issue
  (`ci::main-canceled`). On first use the watcher records a baseline, so old
  canceled pipelines are not retried.
- **`editSpec` line preservation.** `editSpec` replaces only the spec part of
  the milestone description; the marker, owners and rule lines are kept.
- **Retrospective role.** The retrospective runs with the `agents.implement`
  role (the coordinator spec's default for its strong model). Only milestones
  whose pilot still exists when autopilot is done get one.

## Affected files

- CI / pins: `.gitlab-ci.yml`, `.mise.toml`.
- Docs (early): `docs/releasing.md`, `README.md`, `CLAUDE.md`, `docs/usage.md`
  where it links GitHub.
- Operator: `internal/runner/spec.go`, autopilot and spec task controllers,
  `internal/runner` agent start, `pilot_actions.go`,
  `agentproject_controller.go` (`IsCommand`), `WorkflowSpec.CommandsFrom`,
  `autopilot.go`, `internal/gitlab/client.go` (`JobTrace`), `mainhealth.go`,
  `MainHealthSpec.RetryCanceled`, C3–C5 surfaces, webhook receiver,
  `AgentProjectSpec.Webhook`, chart Service and values; generated files via
  `make generate manifests`; chart `files/` in sync.
- Web UI: `web/`, `internal/ui` (milestone view and chat).
- Docs (last): `docs/usage.md`, `docs/configuration.md`, `docs/commands.md`,
  `docs/design.md`, `docs/ui.md`, chart README, PR tables of
  [pipeline-health-and-blocked-state.spec.md](pipeline-health-and-blocked-state.spec.md)
  and [milestone-coordinator.spec.md](milestone-coordinator.spec.md), and
  this spec reconciled.

## Risks / trade-offs

- **Webhook exposure** is a cluster concern; a wrong token or an open route
  without one would wake syncs. Mitigation: constant-time token check, body
  cap, payload used only as a project name, 401 without logging the body.
- **Self-registration needs Maintainer**; without it the condition surfaces
  the gap instead of crashing, and polling keeps working.
- **Canceled-main retry** costs one runner cycle per canceled commit; the
  baseline avoids replaying history. A second cancel opens a non-freezing
  issue so merges are not held for a cancel storm.
- **Owners-only `/agent`** is opt-in so existing projects keep today's
  behaviour; the default does not change.
- **Per-run toolchain dirs** cost install time on every branch that touches
  pins; shared dirs stay for default-branch pins.
- **API budget** improves while webhooks work; after `silenceThreshold` the
  cost returns to today's poll rate by design.

## Validation

- Render this markdown and check relative links to other specs and docs.
- Later slices (acceptance of the milestone): GitLab pipeline green on MRs and
  `main`; `make generate manifests` leaves no diff; controller and runner
  tests with the fake GitLab cover plan-apply retry without duplicate notes,
  tracking-before-planning and one transient spec retry with owner ping,
  per-run toolchain dirs, pinned owner IDs, `commandsFrom: owners`, defused
  `PausedReason`, streaming `JobTrace`, canceled-main retry and second-cancel
  report; C3–C5 and webhook receiver / registration / adaptive polling as in
  the milestone acceptance; `gofmt`, `go vet ./...`, `make test`,
  `make helm-lint`, `make ui` pass; existing CRD fields unchanged.

## Delivery

GitLab `jo.wiemers/ai-operator`, one MR per row, each based on `main`. Slice
keys match the steering log. Target < 50 production lines per MR; split
further if over budget.

| # | Key | Slice | Depends on | Notes |
|--:|---|---|---|---|
| 1 | `spec-doc` | This spec and its steering log | – | docs only |
| 2 | `gitlab-ci` | `.gitlab-ci.yml` and `.mise.toml` | `spec-doc` | |
| 3 | `docs-gitlab-home` | Docs: GitLab primary, GitHub mirror and releases | `gitlab-ci` | |
| 4 | `plan-apply-retries` | Plan apply: transient retries and idempotent decisions notes | `docs-gitlab-home` | strong |
| 5 | `tracking-before-planning` | Tracking issue before planning on autopilot | `plan-apply-retries` | strong; may pair with next |
| 6 | `spec-run-retry` | Spec runs retried once; failure ping | `tracking-before-planning` | strong |
| 7 | `per-run-toolchains` | Per-run toolchain dirs for branch-changed pins | `docs-gitlab-home` | strong |
| 8 | `pin-owner-ids` | Owner IDs pinned in proposals | `docs-gitlab-home` | strong |
| 9 | `commands-from` | `workflow.commandsFrom` | `pin-owner-ids` | strong |
| 10 | `defuse-pause-reasons` | Defused pause reasons | `docs-gitlab-home` | |
| 11 | `stream-job-trace` | Streaming `JobTrace` | `docs-gitlab-home` | |
| 12 | `retry-canceled-main` | Main health: retry canceled default-branch pipelines | `docs-gitlab-home` | strong |
| 13 | `edit-spec-propose` | C3: `editSpec` tool and proposal with the diff | `pin-owner-ids` | strong |
| 14 | `edit-spec-apply` | C3: applying the edit and the re-plan | `edit-spec-propose` | |
| 15 | `retrospective-run` | C4: retrospective run when autopilot is done | `docs-gitlab-home` | |
| 16 | `retrospective-create` | C4: `create 1 2` follow-up issues | `retrospective-run` | |
| 17 | `ui-milestone-api` | C5: milestone-scoped UI API and authorization | `docs-gitlab-home` | strong |
| 18 | `ui-milestone-chat` | C5: milestone page and chat in `web/` | `ui-milestone-api` | |
| 19 | `webhook-receiver` | Webhooks: receiver, token check, project sync, chart Service | `docs-gitlab-home` | strong |
| 20 | `webhook-wiring` | Webhooks: controller channel source and debounce | `webhook-receiver` | |
| 21 | `webhook-registration` | Webhooks: self-registration of the project hook | `webhook-receiver` | strong |
| 22 | `webhook-adaptive-polling` | Webhooks: adaptive polling and AgentProject condition | `webhook-wiring` | |
| 23 | `docs-reconcile` | Docs reconciled (earlier specs' PR tables, usage, configuration, commands, design, ui, chart README) | all above | |

Hardening slices 4–12 are independent of each other once `docs-gitlab-home`
lands (except `commands-from` after `pin-owner-ids`, and `spec-run-retry`
after `tracking-before-planning`). C3 needs owner-ID pinning. Webhook slices
stack on the receiver. C4, C5 and webhooks can proceed in parallel with
hardening after the docs slice.

## Open questions

None open for this milestone: the 13 decisions are locked. Design notes above
may still change through the steering log.

## Steering log

Every MR of this milestone appends exactly one line: date, slice key, what
landed, production line count, deviations from the plan.

- 2026-10-26 `spec-doc`: added this spec (Problem through MRs) and linked C3–C5 delivery from `milestone-coordinator.spec.md`; 0 production lines; no deviations.
- 2026-10-26 `gitlab-ci`: `.gitlab-ci.yml` (drift, gofmt, vet, test, build, version on `golang:1.26`; ui on `node:22-bookworm`; helm-lint on `alpine/helm:3.17.3`; all through the Dependency Proxy, tag `shared`, MRs and the default branch) and `.mise.toml` (go 1.26.0, node 22); 0 production lines (CI config); no deviations.
- 2026-10-26 `docs-gitlab-home`: docs say GitLab is primary (MRs, pipeline) and GitHub a push mirror that still builds releases (`docs/releasing.md` "Where the code lives", README CI badge → GitLab, CLAUDE.md MRs on GitLab); `docs/usage.md` had no GitHub source/issues/contributing links to retarget; 0 production lines; no deviations.
- 2026-10-26 `plan-apply-retries`: `gitlab.RetryTransient` (client backoff, deadline-aware); `applyPlan` retries its updates, finds a timed-out `CreateIssue` by plan key before creating again, and lists notes on every decisions retry (no duplicate note); fake GitLab `CreateErrorsAfterWrite` / `NoteErrorsAfterWrite`; ~88 production lines (over the 50 target: helper and its call sites are one slice); no other deviations.
- 2026-10-26 `tracking-before-planning`: autopilot creates the tracking issue on the first poll; without a plan it only runs the tracking issue and its commands (no pick, never done), and the description says "planning" (run pending) or "planning has not succeeded yet", derived from the empty plan (no new phase); the pilot starts with the tracking issue; ~19 production lines; no deviations.
- 2026-10-26 `spec-run-retry`: the runner reports `infra` for a spec run failing on a transient GitLab error (or agent lost) and posts every planning failure on the tracking issue with the owners' mention (`failed`) and whether a retry follows (`reportSpecFailure`); `syncSpecTask` requests the one retry after `infraRetryAfter` via `source.infraRetry`, in the same update as a re-plan; ~55 production lines (runner and controller halves are one slice); no deviations.
- 2026-10-26 `per-run-toolchains`: `Mirror.ChangedPins` (three-dot diff of the pin files) when the checkout of a branch is prepared; `Runner.invoke` sends `Invocation.PerRunToolchains` (the run ID as `[a-z0-9-]`) with every agent call of such a run; `LocalAgent.Run` validates it, removes earlier runs' `/tmp/ai-toolchains/*` (also on calls without a name) and passes `MISE_DATA_DIR`, `RUSTUP_HOME`, `CARGO_HOME` as `Invocation.Env` (`json:"-"`), which the engines append to their env; `toolchainsNote` has a per-run variant and no longer asks for `mktemp`; ~88 production lines (over the 50 target: agent and runner halves plus the rewritten note are one slice); no other deviations.
- 2026-10-26 `pin-owner-ids`: `PilotProposal.ownerIDs` (optional); `postProposals` resolves the owners once per call and pins their sorted IDs on every proposal it posts (on a lookup error nothing is recorded or posted; an ID nobody has, 0, when none resolves, so a name taken later cannot answer); `answerProposal` takes the author's ID and does nothing, without a reply, for a `yes` or `no` from an unpinned ID; legacy proposals without IDs keep the check by name; callers still require a current owner (pinned and current must both hold); fake GitLab `FindUserError`; ~22 production lines; deviation: the 0 sentinel for an empty resolution.
- 2026-10-26 `commands-from`: `workflow.commandsFrom: members|owners` (default `members`, no GitLab call); `commandGate` per poll looks the owners' IDs up lazily (cached per username) and filters the commands of others out of `commandNotes` (run requests, `reviewOnly`, routing), the tracking issue's commands and revisits; `rejectPilot` became `rejectOnce(text)`, the reply goes where the command was written, recorded in the milestone spec task's `status.pilot.rejected` (outside a planned milestone the implement task's); ~160 production lines (over the 50 target: gate, five call sites and the reply are one slice); deviations: a denied command on the tracking issue or a closed issue moves that place's own watermark (`autopilot.lastCommandNoteID`, `revisits.lastNoteIDs`) so it is not answered again a day later; on issues and MRs `source.lastCommandNoteID` stays and the notes-read watermark (`source.lastPilotNoteID`) picks the new ones to answer.
- 2026-10-26 `defuse-pause-reasons`: `pause` and `renderTracking` run `PausedReason` through `notify.Defuse` before the tracking comment and description; status keeps the raw reason; the owners' mention is appended after defusing; ~3 production lines; no deviations.
- 2026-10-26 `stream-job-trace`: `JobTrace` reads via `readTail` (ring of `maxTrace`) so peak memory stays O(4 MiB); returned bytes unchanged (last 4 MiB or whole log); ~45 production lines; no deviations.
- 2026-10-27 `retry-canceled-main`: `mainHealth.retryCanceled` (default true); `syncMainHealth` lists the default branch's pipelines once for `checkMainHealth` and the new `checkCanceled`, which records the newest finished pipeline as `status.mainHealth.canceledSince` on its first check, retries a newer canceled pipeline once when its commit has no `success` pipeline on the default branch, and reports it once when canceled again (`status.mainHealth.canceled`, the newest 10): a comment on the open main-red issue, else on the `ci::main-canceled` issue (marker `main-canceled`, owners pinged on creation with `mainRed`), which sets no `issueIID`; ~117 production lines (over the 50 target: check, report and the shared list are one slice); deviations: a refused retry is reported at once (like a refused retry of a red pipeline), `mainHealth.issue: false` only logs the report, switching `retryCanceled` off clears the baseline so switching it on again starts a new one.
- 2026-10-27 `edit-spec-apply` (C3a, controller side; D13-P1 merged slices 13 and 14): `PilotAction.spec` (optional); tool `editSpec` in `pilotTools`, not in `countedTools`, and `runAccepted` refuses one accepted by nobody; `specEdit` reads the milestone, drops every line starting with the command prefix from the pilot's text and appends the description's own, refuses when the description changed since the last sync, a spec or autopilot marker would appear or vanish, nothing changes or the diff is over 20 KiB; the proposal shows the go-difflib unified diff (now a direct dependency) in a defused `diff` block; `runAction` checks again and writes the description, the next poll re-plans; ~95 production lines (over the 50 target: check, proposal and apply are one slice); deviations: the diff compares the spec without any command lines (also the markers), and the marker check covers markers anywhere in the text.
- 2026-10-27 `edit-spec-propose` (C3b, model side): `editSpec` in the proposal schema's tool enum with a `spec` property (also in the assessment's schema, which shares it); the pilot's prompt explains it (full text without command lines, only when an owner asks, always behind a yes, say which planned issues are re-planned: drafts updated, started or merged ones kept, maybe follow-ups) and drops "edits" from what does not exist; `rejectRules` became `rejectActions`, which also drops, with a note in the reply, a spec over `aiv1.MaxPilotSpec` (64 KiB, now shared with the controller), a spec edit of an assessment and a second one in a proposal; ~35 production lines; deviations: the assessment and one-per-proposal refusals are additions (events are not an owner's request; two full texts would replace each other and bloat the task's status), `agents.pilot.strongModel` for `editSpec` is not built (the coordinator spec names it, this milestone does not).
- 2026-10-27 `retrospective-run` (C4a; two commits, controller then runner): `source.retrospective` and `status.pilot.retrospective` (`PilotRetrospective`: note, postedAt, follow-ups, failed, pinned owner IDs; optional); `syncPilots` asks a done autopilot's pilot once (`runRequest+1`), keeps it until that run is over, copies the result (or "the run ended without one") to the spec task and only then lets the pilot be deleted; a spec task with a retrospective ends later pilots of the milestone without a second one; the runner's `runRetrospective` gathers the queue (states, MRs, decisions, revisits), the autopilot's 🤖 comments on the tracking issue, the cost over the milestone's tasks and the tracking issue's creation-to-close time, makes one read-only call with the implement role's engine, model and effort (not its tools or instructions; `{summary, followUps ≤ 9}`, capped by the rest of `pilot.maxBudgetUSD` and the implement role's cap), posts "🧭 **Retrospective**" defused with numbered follow-ups, the `create 1 2` line and `marker.Retrospective`, and records it; without budget it posts the facts only; a failure is recorded, not retried; `gitlab.Issue.createdAt`; ~290 production lines (over the 50 target, as the issue expected; split into two commits rather than two MRs); deviations: the marker is `<!-- ai-operator:retrospective -->` (no space, like the other markers); a retrospective run answers open messages with a pointer instead of the pilot and drops pending events; a run restarted after posting finds its marker and records the note without follow-ups instead of posting twice; the retrospective mentions nobody (the done comment pinged the owners).
- 2026-10-27 `retrospective-create` (C4b): `command.RetroCreate` (`create 1 2`, `create 1, 3`, `create all`; the whole comment, case-insensitive, numbers deduplicated); `status.pilot.retrospective.created` (number → issue IID) and `lastNoteID` (optional); autopilot's `commands` calls `followUps`, which takes `create` comments newer than the cursor from an ID in the pinned `ownerIDs` only, lists the threads only then, and acts only on replies in the retrospective's thread; each named follow-up is opened once (inert title and body, "_Follow-up from the retrospective of <milestone> (#<tracking>)._", `<!-- ai-operator:follow-up=<milestone IID>-<n> -->`, the generated label, no milestone, not handed to the agent) and recorded right after; the answer goes into the thread with the links, the ones opened before linked again and unknown numbers named; ~110 production lines without comments (over the 50 target; parser, thread reply and follow-up handling only work together); deviations: a new `gitlab.API` call `ReplyIssueDiscussion` (the client could not reply in a thread; threadAnswers answers top-level), a follow-up GitLab refuses is reported in the reply and not retried until an owner asks again (no retry loop on every poll), the marker has no space after `ai-operator` like the other markers.
- 2026-10-27 `ui-milestone-api` (C5a): `GET …/milestones/{iid}` (`MilestoneView`: spec task, autopilot phase, raw `pausedReason`, `waitingFor` and queue items with plan title, state, MR and implement task, the tracking issue, the pilot's task, `AutonomyOf`, open proposals and cost, the implement tasks, the milestone's cost) and `GET …/milestones/{iid}/thread` (`{"parts": [tracking]}` like the task thread, read with the user's token after `checkTarget`'s tracking check against the spec task's milestone; 409 on a mismatch or an issue the user cannot read, the same 401/409/501/503 answers as the task thread through the new `userGitLab` and `targetRefused`, split out of `steeringTask` and `postNote`); both go through `loadProject` (`CanView`, 404) and 404 without a spec task; the tracking issue is the autopilot's unless it is off, else the Coordinate task's issue; `trackingIssueIID` on the project's milestones; no RBAC markers, chart unchanged; ~210 production lines with the view types (over the 50 target: view, thread and the shared helpers are one slice); deviations: a milestone without a tracking issue answers 404 on the thread, a failed notes read answers 502 (the task thread leaves the part empty), `waitingFor` and the milestone cost are additions.
- 2026-10-27 `ui-milestone-chat` (C5b): `POST …/milestones/{iid}/notes` with `{body}`; the same grant, limits, rate limit, token and error answers as the task notes (`postNote` split into `mayPost`, `readNote`, `createNote`; the thread's tracking-issue lookup and verification became `trackingIssue`, shared by both milestone endpoints); `toPilot` prefixes the trimmed body with `workflow.pilotPrefix` (after `WithDefaults`) unless `command.Pilot` already reads it as addressed to the pilot (the operator's own rule: `/pilotx` and `/pilot/x` get the prefix), `/agent …` included (D18-P1); no owner logic in the UI; logs who posted where, never the text; ~45 net production lines (112 added, 39 removed by the split); deviations: an `@bot` mention is not treated as an address (the message still gets the prefix), a milestone without a tracking issue answers 404 like its thread.

## MRs

| # | Title | Link | Status |
|--:|---|---|---|
| 1 | Spec: GitLab home, webhooks, hardening and the pilot's next steps | [!1](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/1) | draft |
