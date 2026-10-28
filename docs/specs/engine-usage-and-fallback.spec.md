# Engine usage readings and engine fallback

## Problem

The netcup01 `AgentProject` (`infra/cluster`,
`clusters/netcup01/applications/ai-operator/projects/defaults.yaml`) runs the
`fast` profile and both reviewers (bugbot, security) on cursor `auto`; spec,
`strong` and `implement` run on claude opus. When the Cursor Pro budget runs
out mid-month, every cursor role stalls until the monthly reset:

1. **No reset date for cursor.** `classifyLimit`
   (`internal/runner/limits.go`) reads a reset time only from Claude's
   `|<unix>` suffix. cursor-agent says

   > `ActionRequiredError: You've hit your usage limit You've saved $496 on API model usage this month with Pro. Switch to a different model or set a Spend Limit to continue with Auto. Your usage limits will reset when your monthly cycle ends on 10/13/2026.`

   so the run backs off 5 m → 1 h and calls cursor again, every hour, for
   up to two weeks, and gives up after `MaxDeferrals` (20) deferrals.
2. **No way around a limit.** A limited engine defers the run
   (`deferRun`, `internal/runner/runner.go`) even when another engine with
   budget left is configured on the same project. Autopilot on Bloop
   milestone 18 waits with it.
3. **No idea how much is left.** `claude -p --output-format stream-json
   --verbose` (already the runner's invocation) emits a line per call like

   ```json
   {"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1793211600,"rateLimitType":"five_hour","overageStatus":"rejected","overageDisabledReason":"org_level_disabled","isUsingOverage":false,"unifiedWindows":{"five_hour":{"utilization":0.15,"resetsAt":1793211600},"seven_day":{"utilization":0.17,"resetsAt":1793685600}}},"uuid":"…","session_id":"…"}
   ```

   (Claude Code 2.1.292, checked 2026-10-28). `runCLI` / `logEvent`
   (`internal/runner/engine.go`, `transcript.go`) ignore it. The UI shows
   only active limits (`status.usageLimits`) and model spend. cursor-agent
   exposes no usage (`cursor-agent about` / `status` show only the tier).

## Goals

- Read Claude's `rate_limit_event`s, record the newest per engine on the
  task's run status, aggregate the freshest per engine into the project
  status, and show it on the Projects page, e.g. `claude: 5 h 2 % (resets
  14:00), 7 days 16 % (resets Mon 09:00), as of 12:31`, marked stale once a
  window's reset time has passed. For cursor show only the active limit and
  its reset.
- Parse cursor's reset date, so a cursor limit waits until the monthly reset
  instead of polling hourly.
- An optional `fallback: {engine, model, effort}` per role and profile. On a
  limit, when the fallback's engine is not limited itself, the same request
  runs on the fallback right away instead of being deferred; the issue's run
  report says so. Without a usable fallback, today's deferral stays.
- Optionally switch to the fallback before the hard limit, when a fresh
  reading of the role's engine is at or above a configured percentage.

## Non-goals

- Usage numbers for cursor (no CLI surface; no scraping of the dashboard or
  its private API).
- Fallback chains (a fallback of a fallback) or falling back to another
  model of the same engine: limits belong to the engine's credentials.
- Resuming a session across engines: a fallback call starts a fresh session
  with the full prompt, like a resume that failed today.
- Spending limits or budgets per engine; changing `MaxDeferrals` or the
  backoff.
- Changing the netcup01 config: adding `fallback:` to `defaults.yaml` in
  `infra/cluster` is a follow-up after the release.

## Constraints

- `CLAUDE.md`: `make generate manifests` after API changes (CRDs and the
  chart's `files/` are generated); `gofmt`, `go vet ./...`, `make test`,
  `make helm-lint`; `make ui` after web changes; no kubebuilder RBAC markers
  in `internal/ui`. Docs in `docs/` and the chart README follow behaviour.
- New CRD fields are optional; without them behaviour is today's. No field is
  removed, renamed or retyped.
- No floats in CRDs: utilization is stored as whole percent (`int32`).
- A soft threshold (goal 4) never defers a run: without a usable fallback the
  role runs on its own engine as today.
- Target < 50 changed production lines per MR (tests, generated files, docs
  excluded). Each MR leaves `main` releasable (every code merge is released).
- Tests at the lowest layer that owns the behaviour: transcript parsing and
  limit classification as unit tests; fallback in runner tests with the fake
  agent and `gitlabfake`; aggregation in the controller tests.

## Approach

### 1. Usage readings from the transcript

`runCLI` handles `type: rate_limit_event` lines: it decodes
`rate_limit_info` (`status`, `unifiedWindows.<name>.{utilization,
resetsAt}`) and keeps the last one of the call on `Result.Usage`
(`*UsageReading`: status, windows with name, utilization and reset as Unix
seconds). `Result` already crosses the runner ↔ agent socket as JSON, so the
reading reaches the runner without a protocol change. An event without
`unifiedWindows` keeps only its status. The event is not logged (no
content).

### 2. Run and project status

New API types (`api/v1alpha1`):

```go
// EngineUsage is what an engine last said about its usage windows.
type EngineUsage struct {
	Engine     Engine        `json:"engine"`
	// Status as the engine reports it: allowed, allowed_warning, rejected.
	Status     string        `json:"status,omitempty"`
	Windows    []UsageWindow `json:"windows,omitempty"`
	ObservedAt metav1.Time   `json:"observedAt"`
}

// UsageWindow is one usage window, e.g. five_hour or seven_day.
type UsageWindow struct {
	Name     string       `json:"name"`
	Percent  int32        `json:"percent"`
	ResetsAt *metav1.Time `json:"resetsAt,omitempty"`
}
```

- `status.run.engineUsage []EngineUsage` on the AgentTask: after every agent
  call that returned a reading, the runner replaces its engine's entry
  (`observedAt` = now) and merge-patches the list. Reviewers run in
  parallel, so the runner keeps the list in memory under a mutex and always
  patches the whole list.
- `status.engineUsage []EngineUsage` on the AgentProject: next to
  `usageLimits`, the project controller keeps the freshest reading per
  engine over its tasks and its own previous status (a finished task's
  reading survives the task's deletion), sorted by engine.
- The runner reads `status.engineUsage` from the project at run start (like
  `usageLimits`) and keeps it current with its own readings during the run
  (approach 6).

### 3. UI

`ProjectView` (`internal/ui/view.go`) gains `engineUsage`. The Projects page
shows one line per engine under the project's name:
`claude: 5 h 2 % (resets 14:00), 7 days 16 % (resets Mon 09:00), as of
12:31`. Window names map to short labels (`five_hour` → `5 h`, `seven_day` →
`7 days`, `seven_day_opus` → `7 days opus`, others verbatim); times are
local, the weekday is shown when the reset is not today. A window whose reset
time has passed is greyed out and marked `stale` (its percentage was reset
since). An engine with an active limit keeps the existing badge, which now
says when it resets (`cursor limited until 13 Nov`). The project page's limit
banner shows the same line. `docs/ui.md` describes it; the MR has a
screenshot from real `rate_limit_event`s.

### 4. Cursor reset date

`classifyLimit` also reads `reset[s] … <M>/<D>/<YYYY>` (cursor's US date,
`monthly cycle ends on 10/13/2026`) as 00:00 UTC of that day. A date that is
not in the future is ignored (the backoff applies). The reset epoch of
Claude keeps precedence. This alone turns the hourly polling into one wait
until the reset, with or without a fallback.

### 5. Fallback configuration

```go
// RoleFallback is the engine a role runs on while its own engine is limited.
type RoleFallback struct {
	Engine Engine `json:"engine"`
	// +kubebuilder:validation:MinLength=1
	Model  string `json:"model"`
	// Effort (claude engine only).
	// +kubebuilder:validation:Enum=low;medium;high;xhigh;max
	// +optional
	Effort string `json:"effort,omitempty"`
}
```

`RoleSpec.Fallback *RoleFallback` (optional). `RoleSpec` is inlined in
`spec`, `pilot`, `implement`, `profiles[]`, `review` and `describe`, so every
role and profile can have one. Model and effort labels on an issue change the
implementer's engine and model, not its fallback.

`AgentProjectSpec.Fallback(role) (RoleSpec, error)` returns the role to run
instead: the role with the fallback's engine, model and effort. It refuses a
fallback whose engine is the role's own engine, or whose engine/model is not
in `AllowedModels()` (`agents.models` plus the profiles' and implement's
models; the defaults allow claude opus, sonnet and haiku). The project
controller checks every role's fallback on each sync and sets the condition
`FallbacksValid` (`False`, reason `InvalidFallback`, a message naming the
roles) so a broken fallback shows in the UI before a limit needs it.

Example for netcup01 (follow-up in `infra/cluster`):

```yaml
profiles:
  - name: fast
    engine: cursor
    model: auto
    fallback: {engine: claude, model: claude-sonnet-5-5}
review:
  engine: cursor
  model: auto
  fallback: {engine: claude, model: claude-sonnet-5-5}
```

### 6. Fallback at run time

`Runner.invoke` wraps today's body (now `invokeOn`):

1. Call the role's engine (or get the project-wide `UsageLimitError` without
   calling it, as today).
2. On a `UsageLimitError`, ask `spec.Fallback(role)`. With a fallback whose
   engine has no active limit (`r.projectStatus.ActiveUsageLimit`), record
   the limit and call the fallback with the same invocation: its model and
   effort, no `Resume` and the full prompt. Otherwise return the limit error
   (deferral as today).
3. `Result` gains `Engine`, `Model` and `FallbackFrom` (the limited engine,
   empty without a fallback). Callers record sessions under `res.Engine`
   (`setSession`, `st.ImplementEngine`), so the next run on cursor starts a
   fresh cursor session (the existing "another engine continued meanwhile"
   rule), and `agentResult` names the engine and model that actually ran.

Recording the limit:

- in memory: `r.projectStatus.UsageLimits` gets the limit, so later calls of
  the same run (the other reviewer, the fix round, describe) go straight to
  the fallback without calling the limited engine again;
- on the task: `status.run.usageLimits []UsageLimit` (limits the run routed
  around; replaced per engine, cleared when a run ends without one). The
  project controller's `usageLimits()` takes them into account next to
  deferred runs, so other tasks skip the limited engine as well. A limit
  without a reset time uses `now + lifecycle.usageLimitBackoff` (5 m), after
  which the engine is tried again.

Reporting: the run log keeps one line per fallback (role, from, to, reset).
The implement run's report lists them with the other notices, e.g. "↪️
`cursor` reached its usage limit (resets 2026-11-13); bugbot and security
reviewed on `claude/claude-sonnet-5-5` instead." Spec and pilot runs log the
line and add it to their failure/answer report where they post one. No extra
issue note: the report is the run's issue note.

### 7. Switching before the hard limit (optional)

`agents.fallbackAtPercent` (optional, 1–100, unset = off). Before calling a
role's engine, when the project's freshest reading of that engine has a
window whose reset is still ahead and whose percentage is at or above the
threshold, and the role has a usable fallback, `invoke` treats it like a
limit with that window's reset time (step 2 above) without calling the
engine. The report says "`claude` 5 h window at 93 % (threshold 90 %)". The
threshold only routes; it never defers.

## Affected files

- `internal/runner/engine.go`, `transcript.go`: `rate_limit_event`,
  `Result.Usage`, `Result.Engine/Model/FallbackFrom`.
- `internal/runner/limits.go`: cursor reset date.
- `internal/runner/runner.go`: `invoke` / `invokeOn`, recording readings and
  routed limits, `Step` clears them.
- `internal/runner/implement.go`, `spec.go`, `pilot.go`, `steps.go`: sessions
  and `agentResult` under the engine that ran; fallback notices in reports.
- `api/v1alpha1/agentproject_types.go`, `agenttask_types.go`, `defaults.go`,
  `routing.go`: `EngineUsage`, `UsageWindow`, `RoleFallback`,
  `status.engineUsage`, `status.run.engineUsage`, `status.run.usageLimits`,
  `agents.fallbackAtPercent`, `Fallback()`; generated deepcopy, CRDs and the
  chart's `files/`.
- `internal/controller/agentproject_controller.go`: `engineUsage()`,
  `usageLimits()` with routed limits, `FallbacksValid`.
- `internal/ui/view.go`, `web/src/types.ts`, `web/src/views/Projects.vue`,
  `Project.vue` (and a small formatter in `web/src/`).
- Docs: `docs/design.md` (Engines, profiles and subscriptions),
  `docs/configuration.md` (`fallback`, `fallbackAtPercent`, status fields),
  `docs/ui.md`, chart README.

## Risks / trade-offs

- **`rate_limit_event` is undocumented.** Its shape may change with a
  Claude Code release. Parsing is tolerant (unknown or missing fields give no
  reading, never a failed run), and the agent image pins the CLI version.
- **Fallback costs more context.** A fallback starts a fresh session with the
  full prompt (no cache, no resume). Acceptable: the alternative is waiting
  for days.
- **Quality differs per engine.** A reviewer on Sonnet instead of cursor
  `auto` may find other things; the report says which engine reviewed.
- **Fallback drains the other budget.** With cursor out for two weeks, all
  fast implementations and reviews land on the claude subscription and can
  hit its 5-hour window, after which runs defer as today. The usage line
  (and goal 4) make that visible and, if wanted, a soft threshold keeps
  headroom only for the roles that have no fallback.
- **Cursor's date has no time or zone.** 00:00 UTC may be early; a premature
  try costs one failed call and falls back or backs off as today.
- **Status churn.** One task status patch per agent call that carried a
  reading; a run has a handful of calls.

## Validation

- Unit (`transcript_test.go` / `engine_test`): the verified line above gives
  status `allowed`, `five_hour` 15 % and `seven_day` 17 % with their resets;
  a stream with two events keeps the last; malformed or window-less events
  give no windows and never an error.
- Unit (`limits_test.go`): cursor's message above gives `2026-10-13
  00:00 UTC` (with a clock before it), a past date gives zero, Claude's
  `|<unix>` still wins.
- Runner (`runner_test.go`, fake agent and `gitlabfake`):
  - a cursor-limited review continues on claude: both reviewers report
    findings, cursor is called once, the run is not deferred, the report
    names the fallback, `status.run.usageLimits` has cursor;
  - a `fast`-profile implement run whose cursor call hits the limit
    implements on claude: the session is stored under claude, the push and
    report happen, no deferral;
  - no fallback, or a fallback whose engine is limited as well: deferred as
    today;
  - project-wide cursor limit: cursor is not called, the fallback runs;
  - threshold: a 93 % reading with a 90 % threshold routes to the fallback; a
    stale reading (reset passed) or no fallback does not, and never defers;
  - readings land in `status.run.engineUsage`.
- API (`defaults_test.go` / `routing_test.go`): `Fallback()` refuses the same
  engine and an unknown model, applies the fallback's model and effort.
- Controller (`agentproject_controller_test.go`): freshest reading per engine
  across tasks and the previous status; routed limits in `usageLimits`;
  `FallbacksValid` false for an unknown model, true otherwise.
- UI: `make ui`; screenshot of the Projects page from a real reading (a local
  run of the UI against a project status written from a real
  `rate_limit_event`, or the cluster after release).
- `make generate manifests`, `make test`, `make helm-lint`, `go vet`.

## Delivery

GitLab `jo.wiemers/ai-operator`, one MR per row, stacked in order (each on
`main` after the previous merges). Target < 50 production lines per MR.

| # | Key | Slice | Depends on | Status |
|--:|---|---|---|---|
| 1 | `spec-doc` | This spec | – | open, [!25](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/25) |
| 2 | `usage-parse` | Runner: `rate_limit_event` → `Result.Usage`; cursor reset date | `spec-doc` | planned |
| 3 | `usage-status` | API `EngineUsage`; runner records `status.run.engineUsage`; controller aggregates `status.engineUsage`; CRDs, chart | `usage-parse` | planned |
| 4 | `usage-ui` | UI: usage line and limit reset on the Projects and project pages; `docs/ui.md`; screenshot | `usage-status` | planned |
| 5 | `fallback-config` | API `fallback`, `Fallback()`, `FallbacksValid`; CRDs, chart, `docs/configuration.md` | `spec-doc` | planned |
| 6 | `fallback-run` | Runner: fallback in `invoke`, engine-aware sessions, routed limits on the task and in `usageLimits()`, report notices; `docs/design.md` | `fallback-config`, `usage-parse` | planned |
| 7 | `fallback-threshold` | `agents.fallbackAtPercent` | `fallback-run`, `usage-status` | planned |

Slices 2–4 (usage) and 5 (fallback config) are independent; 6 needs the
cursor reset date from 2 only for its tests' realism, not for its code. Each
MR carries its own docs.

## Steering log

Every MR of this spec appends exactly one line: date, slice key, what landed,
production line count, deviations.

- 2026-10-28 `spec-doc`: added this spec; 0 production lines.
- 2026-10-28 `usage-parse`: `runCLI` keeps the last `rate_limit_event` of a call on `Result.Usage` (`usageReading`: status, windows sorted by name, utilization rounded to whole percent); `classifyLimit` reads cursor's `M/D/YYYY` reset date as 00:00 UTC when there is no Claude epoch; the API types `EngineUsage` and `UsageWindow` land here already (no status field yet, so no CRD change) so the reading crosses the agent socket in its final shape; ~53 production lines with the types; deviation: the types moved forward from `usage-status`.
- 2026-10-28 `usage-status`: `status.run.engineUsage` (runner's `recordUsage` after every agent call that returned a reading, whole list under a mutex, also into the run's project status; a failed write only logs) and `status.engineUsage` (controller `engineUsage`: freshest per engine over the tasks and the previous status) via `v1alpha1.MergeEngineUsage`; docs in `design.md` and `usage.md`; ~40 production lines; no deviations.
- 2026-10-28 `usage-ui`: `ProjectSummary.engineUsage`; `components/EngineUsage.vue` (one line per engine, windows ≥ 80 % highlighted, stale after the reset, a non-`allowed` status shown) on the Projects card and under the project title; the limit badge says `limited until <reset>`; `format.ts` `windowLabel` and `clockTime`; `docs/ui.md`; screenshots from a real reading in a local cluster; ~35 production lines (TS/Vue, without styles); no deviations.
- 2026-10-28 `fallback-config`: `RoleSpec.fallback` (`RoleFallback`: engine, model or alias, claude-only effort), `AgentProjectSpec.FallbackRole` (named `Fallback()` in the plan; refuses the role's own engine and models outside `AllowedModels()`, resolves aliases, keeps the role's other fields) and `FallbackErrors`; condition `FallbacksValid` set on every reconcile (True `Valid` / False `InvalidFallback`); CRDs, chart, `docs/configuration.md` (field and validation; the run-time behaviour is documented with `fallback-run`); ~45 production lines; no deviations.

## MRs

| # | Title | Link | Status |
|--:|---|---|---|
| 1 | Spec: engine usage readings and engine fallback | [!25](https://gitlab.com/jo.wiemers/ai-operator/-/merge_requests/25) | open |
