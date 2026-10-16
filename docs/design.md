# ai-operator design

The operator turns GitLab milestones and issues into work for coding agents
running in Kubernetes pods. Humans stay in GitLab: they write specs, answer
questions, move cards on the board and review merge requests.

## Flow

```
 Milestone description ──(contains "/agent spec")──▶ spec task ─▶ spec pod
        │                                                        │
        │                                     creates draft issues (agent::draft),
        │                                     one per merge request, with plan,
        │                                     acceptance criteria and questions
        ▼
 Human answers questions in comments, moves issue to agent::ready
        │
        ▼
 operator: issue -> agent::working, AgentTask.spec.runRequest++ ─▶ issue pod
        │
        │   implement (resumable session)
        │   └▶ bugbot + security reviewers in parallel (read-only, diff only)
        │       └▶ blocking findings? fix (same session), ≤ maxIterations
        │   commit, push agent/<iid>-<slug>, open Draft MR "Closes #iid"
        ▼
 issue -> agent::review (or agent::needs-input when the agent has questions)
        │
        ├── human comments "/agent …" on issue or MR ─▶ another run (resumed session)
        ├── human moves the issue back to agent::ready  ─▶ another run
        └── human closes the issue / moves it to agent::done ─▶ task, pod and PVC deleted
```

## Custom resources

| Kind | Who writes it | Purpose |
|------|---------------|---------|
| `AgentProject` | you | GitLab connection, label workflow, agent image, engines/models per role, lifecycle and limits |
| `AgentTask` | operator | one per planned milestone (`Spec`) and per handed-over issue (`Implement`); owns the pod and the workspace PVC |

`AgentTask.spec.runRequest` is a counter: each increment asks for exactly one
more run. The runner acknowledges in `status.run.observedRunRequest`. Requests
that arrive during a run are coalesced into one follow-up run. The operator
writes the top-level status, the runner only `status.run`; both use merge
patches so they never overwrite each other.

Names are declarative: `shop-spec-m3-checkout-v2` (milestone !3) and
`shop-42-add-paypal-button` (issue #42). Pod, task and PVC (`…-ws`) share the
name, so `kubectl get pods` reads like the board. Tasks are found by the
issue IID label, so renaming an issue does not create a second task.

## Pod topology

**One pod per issue, one pod per planned milestone.** All roles of an issue
(implement, bugbot, security) run as separate CLI invocations inside the same
pod on the same workspace.

Why not one pod per milestone running everything? Issues move independently:
a pod per issue gives parallelism, isolates failures, and maps "issue done"
directly onto "delete the pod and PVC". Why not separate pods per role? The
reviewers are short, read-only invocations that need exactly the work tree the
implementer just produced; separate pods would need RWX volumes and
coordination for no gain. The engine abstraction keeps the option open.

Shared state:

* `/workspace` – per-task RWO PVC: the checkout, build outputs, `$HOME`
  (agent CLI sessions, so follow-up runs resume) and runner state.
* `/cache` – optional RWX PVC shared by all agent pods (`GOMODCACHE`,
  `npm_config_cache`, `PIP_CACHE_DIR`, …).

## Lifecycle of long-lived issues

```
Running ──run done──▶ Idle ──idleTimeout (2h)──▶ Hibernated ──retention (14d)──▶ Archived
   ▲                   │                            │                              │
   └───────────────────┴─────── new run request ────┴──────────────────────────────┘
```

* **Idle**: pod kept warm, everything is hot.
* **Hibernated**: pod deleted, PVC kept. Waking up costs a pod start; the
  checkout, build cache and agent session are still there.
* **Archived**: PVC deleted. The branch, the MR and the GitLab thread hold
  everything that matters; the next run clones fresh and starts a new
  session with the full issue thread as context.
* **Blocked**: `maxRunsPerTask` reached – a guard against loops.
* `maxConcurrentRuns` caps token-consuming runs per project; extra runs queue.

The issue can stay open for months; resources are reclaimed in stages and
nothing is lost.

## Token economy

* **Explicit triggers only.** Runs start when an issue is moved to ready, or a
  comment starts with `/agent`. Discussion between humans costs nothing.
  Comments by the bot user never trigger.
* **Polling is cheap.** Comments are only fetched when an issue's or MR's
  `updated_at` changed.
* **Resumed sessions.** Follow-up runs resume the implementer's session and
  only send the new comments, instead of the whole issue, spec and thread.
* **Context prepared by the runner.** The runner fetches the issue, milestone
  and comments and puts them into the prompt; the model never spends turns on
  GitLab API exploration and never gets GitLab credentials.
* **Stable system prompts** (constants, no timestamps) so the prompt prefix
  stays cacheable.
* **Reviewers see the diff**, not the repository; they read more code only to
  confirm a finding. They are one-shot, without session persistence.
* **Bounded fix loop** (`review.maxIterations`), per-invocation
  `maxBudgetUSD` (claude), `maxRunsPerTask`.
* **Structured output** (JSON schema) instead of free text that would need a
  second model call to parse.
* Spend is visible per task: `kubectl get agenttasks` shows the accumulated
  cost the CLIs report.

## Engines and subscriptions

Each role (`spec`, `implement`, `review`) picks an engine:

| engine | CLI | auth (from `credentialsSecretName`) | notes |
|--------|-----|-------------------------------------|-------|
| `claude` | `claude -p` | `CLAUDE_CODE_OAUTH_TOKEN` (Pro/Max subscription, created with `claude setup-token`) or `ANTHROPIC_API_KEY` | JSON schema, effort, budget, tool allow-lists, cost reporting |
| `cursor` | `cursor-agent -p` | `CURSOR_API_KEY` (Cursor subscription) | instructions and schema are folded into the prompt; no tool allow-list (read-only roles get no `--force` and the work tree is reset after them); no cost reporting |

Agent CLIs are the right building block if you want to use subscriptions:
subscriptions authenticate the vendors' CLIs, while SDK/API integrations need
API keys. The CLIs also bring the agent harness (file tools, shell, context
management, sessions) that we would otherwise rebuild. Adding another CLI
(Codex, Gemini, opencode, …) means implementing the `Engine` interface in
`internal/runner/engine.go` – about 80 lines.

Things to keep in mind with subscriptions:

* Subscriptions have usage windows. Several agents in parallel exhaust them
  quickly, so keep `maxConcurrentRuns` low.
* The plans are meant for your own use. Check the terms of your plan before
  running agents for a team on a personal subscription; use API keys for that.
* With a subscription the reported cost is the notional API price. It is
  still useful to compare tasks and roles.

## Security

* The model gets neither the GitLab token nor Kubernetes access:
  `GITLAB_TOKEN`, `AI_*` and `KUBERNETES_*` are stripped from the agent CLI
  environment. Git authentication is passed per git command via
  `GIT_CONFIG_*` env vars, not stored in `.git/config`.
* Pods run as non-root with dropped capabilities and seccomp `RuntimeDefault`.
* The agent service account can only read tasks/projects and patch
  `agenttasks/status`.
* Use a project access token (role Developer, scopes `api`,
  `write_repository`) and protect the default branch – the agent only pushes
  `agent/*` branches and opens draft MRs.
* **Known gap (v1):** the runner and the agent CLI share a container, so a
  determined model could read the service account token or the runner's
  environment via `/proc`. Hardening path: split into two containers (runner
  with credentials, agent without, sharing the workspace volume) plus a
  NetworkPolicy that only allows the model API, GitLab and package registries.

## Roadmap / open questions

* Webhooks instead of polling (GitLab → operator endpoint) for instant reactions.
* Back off on subscription usage limits (detect the error, requeue later
  instead of failing the run).
* Planning summary: post the spec agent's summary somewhere visible (a
  "plan" issue, or the milestone description with a marker block).
* GitLab issue links (`blocks`) for dependencies on Premium instances.
* Multi-project milestones (group milestones) and group-level `AgentProject`.
* Container split for credentials (see Security) and a NetworkPolicy.
* Rebase/conflict handling when `main` moves under a long-lived MR.
* CI feedback: read failed pipeline jobs of the MR and feed them to the
  implementer on `/agent fix-ci`.
* Metrics: runs, cost, durations per project and role.
