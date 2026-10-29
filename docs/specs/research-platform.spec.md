# ai-operator: research platform (news flash, scheduled and long research, agent-shaped UI)

## Goal

The web UI becomes a platform with features. The **factory** (projects, milestones, tasks: today's UI) is one of them. **Research** is the second:

- **Scheduled research**, e.g. "latest news, ordered by topic" every morning, shown as a **news flash** in the UI.
- **Long research** on demand: a question in, a structured report out.
- Agents shape what the UI shows by returning **blocks** from a fixed schema, so new kinds of results need no new UI code.
- Results are kept for a while in **Postgres** and are searchable.
- The system **chooses the engine and model** for each run itself, from the run's depth and the usage left on each subscription. Research never eats the budget the factory needs.

## Background (how it is wired today)

- All state lives in the CRDs `AgentProject` and `AgentTask` (etcd). There is no database.
- Work is always a GitLab issue or a milestone. A task runs in its own pod, with two containers: `runner` (GitLab token, writes status) and `agent` (CLI credentials, does the work), talking over a unix socket (`internal/runner`, `cmd/runner`).
- Engines and their usage:
  - engines: claude, cursor;
  - roles and profiles map to `{engine, model, effort}` (`api/v1alpha1/routing.go`);
  - every claude call records its `rate_limit_event` reading (`EngineUsage` in task and project status);
  - limits are in `UsageLimits`;
  - roles have an optional `fallback`, and `agents.fallbackAtPercent` switches before the hard limit (`docs/design.md`, "Engines, profiles and subscriptions").
- The UI:
  - a separate deployment (`cmd/ui`, `internal/ui`, `web/`: Vue 3 + Vite, embedded);
  - OIDC login, deny-by-default access rules with grants (`transcript`, `steer`), a strict CSP;
  - a read-only service account (get/list/watch on the CRDs, pod logs);
  - no kubebuilder RBAC markers in `internal/ui`; its RBAC is hand-written in `templates/ui-rbac.yaml`.
- The database is already prepared in the cluster (`jo.wiemers/infra/cluster`, `clusters/netcup01/applications/ai-operator/postgresql.yaml`):
  - Zalando postgres-operator `postgresql` `ai-operator-postgres` in `ai-operator-system`;
  - host `ai-operator-postgres.ai-operator-system.svc.cluster.local:5432`, database `ai_operator`, TLS (`sslmode=require`);
  - one Zalando secret per login, with keys `username`/`password`:

    | Login (role) | Secret | Namespace | Used by |
    |---|---|---|---|
    | `research_owner` (owns `ai_operator`) | `research-owner.ai-operator-postgres.credentials.postgresql.acid.zalan.do` | `ai-operator-system` | operator |
    | `research_reader` | `research-reader.ai-operator-postgres.credentials.postgresql.acid.zalan.do` | `ai-operator-system` | UI |
    | `agents.research_writer` (Zalando prefixes cross-namespace roles with the namespace) | `agents.research-writer.ai-operator-postgres.credentials.postgresql.acid.zalan.do` | `agents` | runner of research runs |

  - The reader and writer have no table rights yet: the migrations grant them.

## Decisions (locked; do not reopen)

1. **Storage is Postgres, configured the way Zalando postgres-operator provides it** (no URI secrets):
   - Optional: the research feature is off and invisible without `research.database.host`.
   - Chart values:
     - `research.database.host`, `port` (5432), `name`, `sslMode` (`require`);
     - `ownerSecret` (operator) and `readerSecret` (UI): each `{name, usernameKey: username, passwordKey: password}`;
     - `readerRole` and `writerRoles` (a list): role names the migrations grant to.
   - The runner container of a research run gets the writer login from `AgentProject.spec.research.writerSecret` (`{name, usernameKey, passwordKey}`, a Secret in the project's namespace). The host, port, database and sslMode come from the operator's config. The agent container never gets database credentials.
   - The operator owns the schema: embedded SQL migrations with a `schema_migrations` table, applied at operator start as the owner.
   - The migrations grant:
     - `readerRole`: USAGE and SELECT;
     - each `writerRoles` entry: SELECT, INSERT and UPDATE on the run, result and item tables;
     - `ALTER DEFAULT PRIVILEGES` for the same, so later tables follow.
   - Roles are quoted identifiers (the writer's name contains a dot).
   - Retention: the operator, as the owner, deletes expired rows hourly.
   - Driver: `github.com/jackc/pgx/v5`.
2. **New CRD `AgentResearch`** (namespaced, `ai.wiemers.me/v1alpha1`):
   - `spec.projectRef`: an `AgentProject` in the same namespace. Its credentials, agent image, engines, profiles and usage readings are used, and research runs feed that project's `EngineUsage`/`UsageLimits`, so factory and research share one view of the budget.
   - `spec.prompt`: the instructions.
   - `spec.topics` (optional list).
   - `spec.view`: `newsflash` | `report`.
   - `spec.depth`: `quick` | `deep`. Sets the timeout (quick 20 m, deep 2 h) and the default candidates.
   - `spec.schedule`: optional cron, with `spec.timeZone`. Without a schedule the research runs once.
   - `spec.retention`: default `720h`.
   - `spec.agent`:
     - `auto` (the default), or
     - `{engine, model, effort}`, validated like `fallback` with `FindModel`.
   - `status`: `lastRun`, `nextRun`, `lastResultID`, `lastEngine`/`lastModel`, `skippedReason`, conditions.
3. **Runs are pods like tasks**, without git:
   - the runner gets a research mode, `ai-runner research`;
   - it builds the prompt (instructions, topics, the block schema, and the item URLs of the previous run, so a news flash does not repeat itself);
   - it calls the agent server;
   - the claude agent may use WebSearch/WebFetch;
   - it validates the answer and writes it to Postgres.
   - Invalid JSON gets one repair round with the validation errors. After that, the raw answer is stored as a single `markdown` block, and the run is marked `degraded`.
4. **The system chooses engine and model (`agent: auto`):**
   - `research.candidates` per depth, in the chart and overridable per `AgentProject` under `spec.research.candidates`. Defaults:
     - quick: `claude/claude-sonnet-5-5`, then `cursor/auto`;
     - deep: `claude/claude-opus-5-5` high, then `claude/claude-sonnet-5-5`, then `cursor/auto`.
   - The first candidate wins if its engine is not limited and the freshest reading of every window that has not reset since is below `research.maxUsagePercent` (default 70). The factory gets the rest.
   - If none qualifies:
     - a scheduled run is skipped, with `skippedReason` saying which agent was limited until when, and tried again at the earliest reset if that is before the next scheduled time;
     - a one-off run waits for the earliest reset.
   - In both cases the UI shows "waiting for `claude` until 14:00".
   - The run records which agent ran and why (`chosen: claude/claude-sonnet-5-5 (opus: 7-day window at 82 %)`).
5. **Agent-shaped UI = block schema v1.** Agents return `{"schemaVersion":1,"title":…,"summary":…,"blocks":[…]}` with these block types:
   - `heading`;
   - `markdown`, rendered sanitized: no raw HTML, links `http(s)` only;
   - `items`: news or findings, each with `topic, title, summary, url, source, publishedAt, importance 1–3`;
   - `table` (`columns`, `rows` of strings);
   - `stats` (`label, value, delta?`);
   - `links`.

   The server validates them (types, sizes, URL schemes). The Vue renderer has one component per type. No agent HTML, no external images: the CSP stays as it is. Unknown block types are dropped with a warning on the run.
6. **Data model:**
   - `research_runs`: id, namespace, research, started/finished, status, engine, model, effort, cost, choice reason, error;
   - `research_results`: run_id, created_at, expires_at, title, summary, blocks JSONB, a tsvector for full-text search;
   - `research_items`: run_id, url hash, topic, first_seen. These drive the "new since the last flash" badges and dedupe.
7. **UI as a platform:**
   - The shell gets top-level sections: **Factory** (today's pages, URLs unchanged) and **Research**.
   - `GET /api/features` lists what the user may see. A feature is hidden without its grant or, for research, without a database.
   - Research pages:
     - a list of researches (schedule, last and next run, agent used, waiting/skipped reason);
     - the **news flash** of a research: the latest run, grouped by topic, importance first, a "new" badge, and the run line "claude/claude-sonnet-5-5 · 3 min · 07:00";
     - a report view;
     - run history;
     - full-text search over the stored results.
8. **Access:**
   - New rule grants: `research: read` (see researches and their results) and `research: write` (start a one-off research and delete own one-offs from the UI). Rules can scope by `researches: ["namespace/name"]` globs, like `projects`.
   - The UI's service account additionally gets `get/list/watch` on agentresearches, and, only when a rule grants write, `create/delete` on them (chart value `ui.research.allowCreate`).
   - The UI stamps the creator (`ai.wiemers.me/created-by`) and enforces the namespace from a chart value.
9. **One-off research from the UI:** a form with the question, depth, topics and agent (auto or a choice from the allowed models). It creates an `AgentResearch` without a schedule, and the page follows the run live (the same event stream as tasks).

## Requirements

### Operator and runner (`api/`, `internal/`, `cmd/`)
- `AgentResearch` types, defaults and validation (`make generate manifests`).
- A controller: schedule (cron + timeZone), one pod per run, `nextRun`, skip/wait handling, conditions, cleanup of the pods of finished runs.
- `internal/research` (or similar):
  - store: migrations, insert run/result/items, retention delete, queries for the UI;
  - block schema: Go types, validation and a JSON schema text for the prompt.
- Runner research mode, candidate choice (shares the usage logic of `internal/runner/usage.go`), recording `EngineUsage` from research runs to the referenced project.

### Chart
- `research.database.{host, port, name, sslMode, ownerSecret, readerSecret, readerRole, writerRoles}`, `research.candidates`, `research.maxUsagePercent`, `ui.research.allowCreate`, `ui.research.namespace`.
- `agent.networkPolicy`: research-run pods (their own label, not task pods) may reach the database, e.g. `research.database.networkPolicyPeer` (namespace and pod selector) on TCP 5432. Today's policy blocks cluster CIDRs, so without this the runner cannot connect. Task pods stay unchanged.
- The netcup01 values to use once released:
  - `host: ai-operator-postgres.ai-operator-system.svc.cluster.local`, `name: ai_operator`;
  - the secrets and roles from the table in Background;
  - peer: namespace `ai-operator-system`, pods `cluster-name: ai-operator-postgres`.
- RBAC for the operator (agentresearches, their pods) and the UI (hand-written).
- The CRD in `files/`.

### Web UI (`web/`, `internal/ui`)
- Platform shell and `/api/features`.
- Research API endpoints: list, get, runs, result, search, create/delete for one-offs (the UI's existing CSRF/cross-origin protection, size and rate limits, audit line).
- Block renderer components, the news flash, report, history and search pages, the one-off form.
- Screenshots of the news flash and a report in the MR (`make run-ui` with a local Postgres and seeded results).

### Docs
- `docs/research.md` (new: concepts, CRD, choosing agents, retention, examples incl. a morning news flash).
- `docs/configuration.md`, `docs/ui.md` (platform, grants, research pages), `docs/design.md` (architecture, pod topology, security of web content), chart README.

## Constraints

- Repo rules apply:
  - `make generate manifests` after API changes;
  - gofmt, `go vet ./...`, `make test`, `make helm-lint`, `make ui`;
  - no RBAC markers in `internal/ui`;
  - the GitLab pipeline green before merge.
- Store tests run against a real Postgres:
  - `.gitlab-ci.yml` adds a `postgres:17` service through `${CI_DEPENDENCY_PROXY_GROUP_IMAGE_PREFIX}`;
  - locally a make target starts one in docker;
  - no mocks for SQL.
- **Web content is untrusted:**
  - the research agent has no GitLab token and no database credentials;
  - its answer is data;
  - the prompt tells it to ignore instructions found on pages;
  - validation caps sizes (e.g. 200 items, 64 KiB markdown per block, 1 MiB per result).
- Research must not starve the factory: the `maxUsagePercent` guard applies to every research run, including one-offs.
- Without `research.database` the operator, chart and UI behave exactly as today (tests prove it).
- One MR per suggested step, < 50 production lines where the step allows. A larger step says why in its description.

## Non-goals

- Agent-written HTML/JS, external images, iframes.
- Push notifications (email, ntfy) for a new news flash: later.
- MCP endpoint, research tools for factory agents, results posted to GitLab.
- Other features on the platform beyond Factory and Research.
- Editing or annotating results in the UI.
- Provisioning the database: done in `infra/cluster` (see Background). Turning research on in the netcup01 values is the owner's follow-up after the release.

## Suggested order (one MR each, split further if over budget)

1. Spec: copy this milestone to `docs/specs/research-platform.spec.md` with a steering log.
2. Store: pgx, migrations, data model, retention, CI Postgres service, make target.
3. Block schema v1: types, validation, prompt schema text, tests with good and hostile inputs.
4. `AgentResearch` CRD and chart values (no controller logic yet), docs skeleton `docs/research.md`.
5. Controller: schedule, pods, status, cleanup, retention loop.
6. Runner research mode: prompt, agent call, validation and repair round, store write, usage recording.
7. Agent choice (`auto`, candidates, `maxUsagePercent`, skip/wait), with tests for limited, over-threshold and all-busy cases.
8. UI backend: `/api/features`, access grants, read endpoints, search.
9. UI frontend: platform shell, block renderer, news flash, report, history, search (screenshots).
10. One-off research from the UI: create/delete endpoint, RBAC, form, live run (screenshots).
11. Docs reconcile: research.md, configuration.md, ui.md, design.md, chart README, spec delivery table.

## Acceptance

- With a database:
  - a scheduled `AgentResearch` (`view: newsflash`, `depth: quick`) runs on time and stores a validated result;
  - the UI's Research section shows it as a news flash grouped by topic, with "new" badges against the previous run;
  - the result expires after `retention`.
- A one-off deep research started from the UI runs, the page follows it live, and the report renders from blocks.
- `agent: auto`:
  - with claude's 5-hour window at 85 % and `maxUsagePercent: 70`, the run goes to the next candidate;
  - with every candidate limited, a scheduled run is skipped with the reason and retried at the earliest reset, and the UI shows it;
  - the choice reason is stored with the run.
- Hostile agent output fails validation without reaching the browser: raw HTML in markdown, `javascript:` links, oversized results, unknown block types.
- Without a database nothing changes: no Research section, no new RBAC used, existing tests unchanged.
- Docs, CRDs and the chart are updated. Every MR's pipeline is green.

## Delivery

GitLab `jo.wiemers/ai-operator`, one MR per row, each based on `main` after the previous merges. Slice keys match the steering log. Target < 50 production lines per MR; a larger step says why in its description.

| # | Key | Slice | Depends on | Status |
|--:|---|---|---|---|
| 1 | `spec-doc` | This spec (milestone text, delivery, plan decisions, steering log) | – | this MR |
| 2 | `store-schema` | pgx, embedded migrations, `research_runs`/`results`/`items`, retention delete, CI Postgres service, local make target | `spec-doc` | open |
| 3 | `block-schema` | Block schema v1 Go types, validation, prompt JSON schema text, hostile-input tests | `spec-doc` | open |
| 4 | `research-schedule` | Cron + timeZone helpers for next-run calculation (shared by CRD validation and the controller) | `spec-doc` | this MR |
| 5 | `research-crd` | `AgentResearch` types, defaults, validation, chart values, CRD in `files/`, docs skeleton `docs/research.md` (no controller yet) | `research-schedule` | open |
| 6 | `store-writes` | Insert run/result/items, UI query helpers (list, get, search, items for "new" badges) | `store-schema`, `block-schema` | open |
| 7 | `operator-database` | Operator takes `research.database` (host, port, name, sslMode, owner/reader secrets, reader/writer roles), applies migrations at start as the owner with the grants; chart wiring incl. the research-pod NetworkPolicy peer; feature off without a host | `store-schema` | open |
| 8 | `research-controller` | Schedule, one pod per run, `nextRun`, skip/wait, conditions, finished-pod cleanup, hourly retention | `research-crd`, `operator-database`, `store-writes` | open |
| 9 | `runner-research-answer` | Validate/repair agent JSON, write result and items, mark `degraded` after a failed repair | `block-schema`, `store-writes` | open |
| 10 | `runner-research-mode` | `ai-runner research`: prompt (instructions, topics, schema, previous URLs), agent call, usage recording to the project | `runner-research-answer`, `research-controller` | open |
| 11 | `agent-choice` | `agent: auto`, candidates per depth, `maxUsagePercent`, skip/wait with `skippedReason` | `runner-research-mode` | open |
| 12 | `ui-research-access` | `GET /api/v1/features`, `research: read`/`write` grants, researches globs | `operator-database` | open |
| 13 | `ui-research-api` | Read endpoints: list, get, runs, result, search (CSRF, size/rate limits, audit) | `ui-research-access`, `store-writes` | open |
| 14 | `ui-shell` | Platform shell with Factory and Research top-level sections | `ui-research-access` | open |
| 15 | `ui-blocks` | Block renderer (no `v-html`), news flash and report views | `ui-research-api`, `ui-shell`, `block-schema` | open |
| 16 | `ui-history-search` | Run history and full-text search pages | `ui-blocks` | open |
| 17 | `oneoff-api` | Create/delete one-off `AgentResearch`, UI RBAC (`ui.research.allowCreate`), creator stamp | `ui-research-api`, `research-crd` | open |
| 18 | `oneoff-form` | One-off form (question, depth, topics, agent) and live run follow | `oneoff-api`, `ui-blocks` | open |
| 19 | `docs-reconcile` | `docs/research.md`, configuration, ui, design, chart README, this delivery table | all above | open |

`store-schema` and `block-schema` (and `research-schedule`) are independent after `spec-doc`. `ui-shell` can start once features and grants exist. Each MR carries its own docs for the slice it lands.

## Plan decisions

Cross-cutting choices locked while planning this milestone (do not reopen without `/agent revisit`):

1. **API prefix.** Research (and features) live under `/api/v1/` (so `GET /api/v1/features`), matching the existing UI API. The milestone's `GET /api/features` is shorthand for that path.
2. **Who chooses the agent.** The operator, not the runner, chooses the agent before it creates a pod. The runner receives the chosen `{engine, model, effort}` and does not re-pick.
3. **Writer login in agent namespaces.** Research pods get the writer's username/password from `AgentProject.spec.research.writerSecret`, a Secret in the project's (agent) namespace; host, port, database and sslMode come from the operator's config. On netcup01 Zalando creates that Secret there itself (cross-namespace user `agents.research_writer`). (Steer 2026-10-29: replaces the `writerURI` Secret.)
4. **Research rules vs factory rules.** Research access rules in the UI are separate from factory rules: new grants `research: read` / `research: write` and optional `researches` globs, not reuse of `transcript` / `steer` / `projects`.
5. **Markdown rendering.** Markdown is rendered by an own subset renderer to Vue nodes, without `v-html`. No raw HTML from agent content reaches the DOM.

## Steering log

Every MR of this spec appends exactly one line: date, slice key, what landed,
production line count, deviations.

- 2026-10-29 `spec-doc`: added this spec (Goal through Acceptance, Delivery, Plan decisions, Steering log, MRs); 0 production lines; no deviations.
- 2026-10-29 steer `database-zalando` (owner): the database exists on netcup01 (`infra/cluster` !283, !284). Decision 1, Background, Chart and Non-goals now describe it: host plus Zalando username/password Secrets (owner, reader, cross-namespace writer) instead of URI Secrets, grants by the migrations, a NetworkPolicy peer for research pods; plan decision 3 and delivery row 7 follow. 0 production lines.
- 2026-10-29 `store-schema`: `internal/research/store` (pgx pool from Zalando-style parts, embedded migrations under an advisory lock, `research_runs`/`results`/`items` with expiry on runs and a `simple` tsvector, idempotent additive grants with default privileges for quoted reader/writer roles, batched retention delete), `internal/research/storetest` (schema and roles per test), `make test-db`, Postgres 17 service in GitLab and GitHub CI; about 290 production lines (SQL included) plus a 110-line test helper, over budget because schema, migrator, grants and retention only make sense together; deviations: `runs.expires_at` and `runs.warnings` added (plan D28-P1, D28-P2).
- 2026-10-29 `block-schema`: `internal/research/blocks` (block types with a flat JSON form, `Validate` with path-listed problems capped at 50, size and character limits, http(s)-only URLs, a strict markdown scan for raw HTML, images and link destinations outside code spans and fences, `Degraded` as one fenced markdown block, `PromptSchema` as JSON Schema with the rules in its description, `ItemURLs`, `URLHash`), table-driven tests with good and hostile fixtures and `FuzzValidate`; about 520 production lines plus 120 lines of schema text, over budget because types, validator and schema text must agree and the tests prove the security properties only as a whole; deviations: importance 3 is high, items need a title and URL, lists must not be empty, stats and links blocks hold at most 200 entries, URLs at most 2048 characters (decisions in !37).
- 2026-10-29 `research-schedule`: `internal/research` schedule helper (`ParseSchedule`, `Next`, `NextRun`) on `github.com/robfig/cron/v3` ParseStandard with IANA `timeZone` (empty = UTC), refused `CRON_TZ=`/`TZ=` prefixes, one catch-up when activations were missed; about 55 production lines; no deviations.
- 2026-10-29 `research-crd`: `AgentResearch` CRD (namespaced, short name `ar`, status subresource, print columns; spec with enum/size validation and defaults `view: report`, `depth: quick`, `retention: 720h`; status with `phase`, `waiting` and `run` as `ResearchRunStatus`), `AgentProject.spec.research` (`writerSecret` with defaulted keys, `candidates` per depth), helpers `Timeout`, `RetentionOrDefault`, `OneOff`, `ResolveResearchAgent`/`ValidateResearchAgent`, chart values `research.*` and `ui.research.*` (unused), samples, a CRD schema test against the samples, `docs/research.md` skeleton; about 160 production lines, over budget because one CRD with its project field cannot be split meaningfully; deviations: `ResolveResearchAgent` added next to `ValidateResearchAgent` (the controller needs the resolved model), the "Agent" print column shows `status.lastModel`, an Age column was added, the type registers itself in its own `init` like the other types.
- 2026-10-29 `store-writes`: `internal/research/store` write path (`Run` and status constants, idempotent `StartRun`, first-finish-wins `FinishRun` and `FinishRunOrInsert` that append warnings, `SaveResult` writing the result and one item row per distinct URL hash in one transaction with `first_seen` carried over within the namespace/research, `PreviousItemURLs` from the latest result's blocks); about 110 production lines (SQL included), over budget because the run, result and item writes share one transaction and their tests; deviations: the UI query helpers of delivery row 6 move to `ui-research-api` (the issue scoped this slice to the write side), `cost_usd` stays NULL until a run finishes.

## MRs

| # | Title | Link | Status |
|--:|---|---|---|
| 1 | Spec: research platform | !34 | merged |
| 2 | Spec: research database as prepared on netcup01 (Zalando secrets) | (merged as aa45a96) | merged |
| 3 | Research store: pgx, migrations, data model, role grants, retention, Postgres in CI | !36 | merged |
| 4 | Block schema v1: Go types, validation, prompt schema text, hostile-input tests | !37 | open |
| 5 | Research schedule: cron with timeZone, next run time | !38 | open |
| 6 | AgentResearch CRD, AgentProject spec.research, chart values, docs skeleton | !39 | open |
| 7 | Research store: insert run, result and items; previous run's URLs | !40 | open |
- 2026-10-29 `operator-database`: operator flags `--research-db-{host,port,name,sslmode}`, `--research-reader-role`, `--research-writer-roles`, owner login from env `AI_RESEARCH_DB_USER`/`AI_RESEARCH_DB_PASSWORD` (incomplete config exits at start), leader-only `research.Database` runnable (migrate and grant with backoff 5 s to 5 min, `Ready`/`Store` for the controller, retention at once and hourly), chart flags, secretKeyRef env and `fail` for a host without name or owner secret, `hack/helm-research-values.yaml` in `make helm-lint`, configuration docs; about 120 production lines plus 20 chart lines, over budget because config, runnable and wiring are only testable together; deviations: the research-pod NetworkPolicy peer of delivery row 7 is left to the research-controller slice (research pods do not exist yet), the retention loop of row 8 landed here.
- 2026-10-29 `research-controller`: `AgentResearchReconciler` (registered only with `--research-db-host`; `DatabaseReady` false and a 30 s retry until the database is migrated; `Valid` with `ProjectNotFound`, `InvalidSchedule`, `InvalidAgent`, `NoAgent`, `NoAgentImage`, `WriterSecretMissing`; `Running`; one-offs once, scheduled runs at `status.nextRun` with one catch-up, overlapping times skipped with `skippedReason`; `status.run` written before the pod; runs finished when the runner reported or the pod ended, is gone or is 5 min past its deadline, `FinishRunOrInsert(failed, …)` without a report, then `lastRun`/`lastResultID`/`lastEngine`/`lastModel`/`phase` and the pod deleted; status patches with optimistic locking), `buildResearchPod` (runner `ai-runner research` with the writer login as `secretKeyRef` and the connection as values, agent container like a task's without cache, emptyDirs, `restartPolicy: Never`, deadline timeout + 2 m, research label, owner reference) on helpers factored out of `buildPod` (output unchanged), `--research-candidates` from the chart, agent Role rules for `agentresearches` with a host, the research egress NetworkPolicy `<agent role>-research-db` (task policy byte-identical); about 470 production lines plus 40 chart lines, over the 270 estimate because of the run-end edge cases (stale cache, pods that never start, failed pod creation) and the validation reasons; deviations: the reconciler takes `aiv1.ResearchCandidates` instead of a map, `NoAgent`/`NoAgentImage` reasons added, the chart part stays in this MR because the acceptance criteria need it.
- 2026-10-29 `runner-research-answer`: `internal/runner/research.go` (`ResearchRequest`, `ResearchAnswer`, stable `researchSystemPrompt` with the injection warning and `blocks.PromptSchema`, `researchPrompt` with date, request, topics, view, depth and at most 200 previous item URLs for a news flash, `researchInvocation` read-only with role `research`, claude limited to WebSearch/WebFetch with Bash/Edit/Write/NotebookEdit disallowed and the schema as `JSONSchema`, `RunResearch`: structured output or the JSON object of the text, `blocks.Validate`, one repair round that resumes the session (else re-sends the prompt) with the errors capped at 4 KiB, then `blocks.Degraded` of the whole raw answer with status `degraded`; costs summed, latest usage kept, usage limits returned unchanged), no pod or store wiring; about 150 production lines, over budget because prompt, call and repair loop form one unit; deviations: `View`/`Depth` use the `aiv1` types, cursor gets the schema only in the system prompt (no `JSONSchema`, which cursor would fold into the prompt a second time), a failed repair call (not a limit) degrades the first answer instead of failing the run, the depth adds one line to the prompt.
- 2026-10-29 `runner-research-mode`: `ai-runner research` (k8s client, agent client, writer-login store config from `AI_RESEARCH_DB_*`, no `GITLAB_TOKEN`, no migration), `internal/runner/research_run.go` (`ResearchRun`: stale-pod check on `status.run`, `StartRun` with expiry start + retention, agent readiness, previous item URLs for a news flash, `RunResearch` within the depth's timeout, `SaveResult`, `FinishRun` as succeeded/degraded/limited/failed, merge patch of `status.run` with outcome, message, cost, `engineUsage` and `usageLimits` (reset, else 1 h); exit non-zero only when the status cannot be written), `AgentProjectReconciler.Research` merging the researches' readings and active limits into the project status, `storetest.Count`; about 280 production lines with comments (runner ~190, command ~50, project aggregation ~40), over budget because the store write, the status report and the outcome mapping are one unit with one test fixture; deviations: the runner does not set `finishedAt` (the controller finishes a run only while it is unset), `StartRun` comes before the agent readiness wait, the store is opened inside `Run` (a stale pod never connects, an unreachable database still reports `failed`).
- 2026-10-29 `agent-choice`: `AgentProjectStatus.FullestWindow` shared by `Runner.nearLimit` (unchanged behaviour) and `internal/research/choose.go` (`Choose`: candidates in order, skipped when the project cannot run them, blocked by an active limit or a fresh window at or over `maxUsagePercent`, effort capped, cursor without effort, reasons with aliases; otherwise the earliest reset or limit end; `ParseCandidates`), controller integration (fixed `spec.agent` as the only candidate under the same guard, choice in `status.run.reason` and the store, one-off `Waiting` until the reset, scheduled skip with `skippedReason`, `waiting` and `nextRun = min(reset, next)`, a run that ended `limited` due again at once with its limit, `NoAgent` when no candidate is usable), operator flags `--research-candidates-quick/-deep` (`engine/model[@effort]`) and `--research-max-usage-percent` replacing the JSON `--research-candidates`, chart rendering, docs "Choosing agents"; about 210 production lines with comments (choice ~150 incl. the flag parser, controller ~40, flags ~15), over the 120 estimate because the reason texts, the unusable-candidate handling and the flag parser came on top of the plan; deviations: a candidate with a model the project does not allow is skipped like one on an unused engine, a fixed agent's effort is capped too, a scheduled skip keeps phase `Scheduled` (one-offs use `Waiting`), times in reasons carry "UTC".
- 2026-10-29 `ui-research-access`: UI config `research.database.{host,port,name,sslMode}` (on exactly with a host; name, reader login from `AI_OPERATOR_UI_RESEARCH_DB_USER`/`_PASSWORD` required; `sslMode` default `require`, `disable` only for a loopback host), `research.namespace` and `allowCreate` (a valid namespace required with it), rule grants `research: read|write` and `researches` globs (research rules refuse `projects`/`transcript`/`steer`; `researches` without `research` refused), `Authorizer` keeping factory and research rules apart (`CanReadResearch`, `CanWriteResearch`, `SeesFactory`, `ResearchAccess`; auth mode none reads, writes with `allowCreate`), `GET /api/v1/features`, `ConnectResearch` (ping-only `store.Open` in the background with 5 s–5 min backoff) and `researchHandler` (404 without research, 503 until connected) for the coming endpoints, chart config, reader env from `readerSecret` with `fail` without its name, `agentresearches` get/list/watch in the UI ClusterRole (default renders unchanged; the UI NetworkPolicy is ingress-only, so no egress change), the UI in `hack/helm-research-values.yaml`, docs; about 195 production lines without comments plus 30 chart lines, over the 90 estimate because config, authorization and features are one access model and the 503 gate needs the background connect; deviations: the write grant needs `ui.research.allowCreate` in every auth mode (not only none), so `write` in `/features` means the UI may actually create.
