# Research internals

How research runs work inside: how the operator starts and ends a run, what
the runner does in the pod, how results and new items are stored, the block
schema the agent answers in, and the database with its logins and grants.
For using research, see the [Research guide](../guides/research.md); the
fields are in the [AgentResearch reference](../reference/agentresearch.md).

Code: the types in `api/v1alpha1/agentresearch_types.go`, the controller in
`internal/controller/agentresearch_controller.go` (pod in
`research_resources.go`), the runner in `internal/runner/research_run.go`,
`research.go` and `research_feeds.go`, and the packages under
`internal/research/` (`choose.go`, `schedule.go`, `feeds/`, `blocks/`,
`store/` with its migrations).

## Runs in detail

The operator starts runs and records their outcome:

* **When.** A research without a schedule runs once, right after it is
  created. A scheduled one runs at `status.nextRun`, the next time of
  `spec.schedule` in `spec.timeZone`; the first is the first scheduled time
  after the research was created. If the operator was down at a scheduled
  time, the run starts once when it is back, however many times were
  missed. After a change of the schedule the next time is counted again
  from the last run. Schedule and time zone are validated by the controller
  (`InvalidSchedule`), not by the CRD.
* **Start.** The operator chooses the agent (below), writes `status.run` (a
  new UUID, the start time, the engine, model and effort, and the reason)
  and then creates the pod `<research>-<first 8 characters of the run ID>`
  (the research name cut to fit). The runner reads its agent from
  `status.run`. The pod is owned by the `AgentResearch`, so deleting the
  research garbage-collects the pod of its current run; there is no
  finalizer.
* **End.** The runner records the outcome in the database and then in
  `status.run.outcome`. When the outcome is there, or the pod has ended or
  is gone, the operator finishes the run: without the runner's report the
  run failed, with a message such as "no report from the runner: pod
  DeadlineExceeded: …", in the database and the status. It then sets
  `finishedAt`, `lastRun` (the run's start), `lastEngine`, `lastModel`, `lastResultID` (for
  `succeeded` and `degraded` runs) and the `phase` (`Scheduled` for a
  scheduled research, else `Succeeded` or `Failed`), and deletes the pod. A
  run that ended `limited` is due again at once. A pod that never starts is
  ended 5 minutes after its deadline, counted from the run's start.
* **Requeues.** While `Valid` is `False` the research is checked again every
  minute (for any reason; the writer Secret is not watched), while the
  database is not ready every 30 seconds.

A run's pod has a deadline of the depth's timeout plus 2 minutes and
`restartPolicy: Never`. Like a task's pod it has a `runner` and an `agent`
container, but no git, no GitLab token and no workspace volume (see
[Pod topology](design.md#pod-topology)).

## Choosing agents in detail

`internal/research/choose.go` chooses the agent when the run is due, before
the pod exists, from the usage the referenced AgentProject knows of
(`status.engineUsage` and `status.usageLimits`, fed by tasks and research
runs alike) plus what the research's last run reported.

A candidate is blocked when its engine has an active limit (the project's,
or one the research's last run hit), else when the fullest window of its
engine's freshest reading that has not reset since is at or above
`--research-max-usage-percent`. A window whose reset time has passed no
longer counts. The reason names the chosen `engine/model` and each passed
candidate by its alias where the project has one. With no candidate left,
`status.waiting` holds the earliest reset or limit end among the blocked
candidates: a one-off stays `Waiting` until then, a scheduled run is
skipped with every blocked candidate in `skippedReason` and `nextRun` moved
to `until` if that comes first. Both are cleared when a run starts.

## Feeds in detail

The runner (`internal/runner/research_feeds.go`, fetching and parsing in
`internal/research/feeds`) fetches `spec.sources` before it asks the agent
and puts the recent entries into the prompt as data.

* **Window.** Entries published since the previous result's run start are
  kept, at most 48 h back; without a previous run the window is 24 h.
  Entries without a date are kept. URLs already in the previous result are
  dropped (`blocks.URLHash`).
* **Caps.** 30 entries per feed and 200 in all (newest first). Titles are
  cut to 200 runes, summaries to 400.
* **Fetch.** https only, redirects too. The dialer refuses loopback,
  private, link-local, CGNAT, `0.0.0.0/8`, unspecified and multicast
  addresses, checked on the connected IP, so DNS rebinding and redirects are
  covered. Per feed: 15 s timeout, body capped at 4 MiB. At most 4 feeds in
  parallel, all within the first minute of the run. User agent:
  `ai-operator-research/1 (+https://gitlab.com/jo.wiemers/ai-operator)`.
* A failing feed is a warning on the run and a "not available this run"
  line in the prompt, never a failed run.

## The agent's answer

The runner (`internal/runner/research.go`) asks the agent with the request,
any feed entries it just fetched, the topics, the view, the current date,
and, for a news flash, the previous result's item URLs (at most 200). Its
system prompt holds the block schema, tells the agent to use the feed
entries first, then web search and fetch, and to treat web pages as data
and ignore any instructions found on them. On claude the agent may use only
WebSearch and WebFetch. Bash, Edit, Write and NotebookEdit are disallowed,
and the schema is also passed as the structured output schema. Cursor gets
the schema in the prompt and runs read-only.

The answer is validated against the block schema. If it fails, the agent
gets one repair round with the validation errors. It continues its session
if it has one, otherwise the runner sends the whole prompt again. If the
repaired answer is still invalid, or the repair call fails, the raw answer
is kept as a single `markdown` block inside a code fence ("Unstructured
answer"), and the run is `degraded`, with the validation errors among its
warnings. A usage limit ends the run without a result.

## In the pod

`ai-runner research` (`internal/runner/research_run.go`) carries out one
run. It has the project's writer login but no GitLab token, and it does not
migrate (the writer cannot):

1. It reads the `AgentResearch`. If `status.run` is no longer its run (a
   newer run started, or the outcome is already recorded), the pod is stale
   and exits without writing anything.
2. It connects to the database and records the run as `running`, with the
   agent from `status.run`, the reason for that choice, and an expiry of the
   start time plus `retention`.
3. It waits for the agent container, reads the previous result (item URLs
   and start time), fetches `spec.sources` when set, and asks the agent
   (see above). Previous URLs go into the prompt for a news flash only. The
   run's deadline is the depth's timeout; recording the outcome gets
   another minute.
4. It records the outcome in the database, and then in `status.run`:
   `outcome`, `message`, `costUSD`, the engine's newest usage reading
   (`engineUsage`), any usage limit (`usageLimits`) and its engine calls
   (`calls`, seq 1..n, kind and role `research`). The operator then writes
   the calls to `agent_calls` (project = `spec.projectRef`, task =
   `research/<name>`, run = `status.run.number`, keyed
   `research/<UID>/<run ID>/<seq>`; the pod has no rights on that table),
   sets `finishedAt` and deletes the pod. A failed write is retried with the
   next reconcile. The project's next sync merges the reading and an active
   limit into the project's `status.engineUsage` and `status.usageLimits`,
   where task runs and later research runs see them.

| Outcome | When | Stored |
|---------|------|--------|
| `succeeded` | the answer passed validation (dropped unknown blocks are warnings) | the result and its items |
| `degraded` | still invalid after the repair round | the raw answer as one markdown block; `message` and a warning hold the validation errors |
| `limited` | the engine hit a usage limit | no result; `usageLimits` holds the engine until its reset, or for an hour when the engine names none |
| `failed` | anything else, e.g. the database or the agent container was unreachable, or the agent failed | no result; `message` holds the error |

The runner exits 0 whenever it recorded an outcome, failures included. It
exits non-zero only when it could not write the status; the operator then
fails the run when the pod ends.

## Runs, results and new items

Each run is a row in the database with its agent, the reason for that
choice, its outcome, cost and warnings. A run has one outcome: whichever of
the runner and the operator records it first wins. If the pod ends before
the runner gets to record the outcome, the operator records it instead. A
later finish, such as the operator noticing that the pod's deadline passed,
does not change it.

A run's result is stored with one row per news item, identified by its URL
(scheme and host lowercased, without the fragment). An item remembers when
the research first reported its URL. That date is what marks an item as
**new** in a news flash: the UI flags it in the run that first reported the
URL (`"new": true`), not in later runs. The prompt of the next run lists
the item URLs of the latest result (at most 200), so a news flash does not
repeat itself.

Every run gets an expiry when it starts: its start time plus
`spec.retention`. The operator, as the database's owner, deletes expired
runs with their results and items (in batches of 1000) at its start and
then every hour. A run's `agent_calls` rows expire with
`--history-retention`, not with `spec.retention`.

## Block schema v1

The agent answers with one JSON object; the runner validates it
(`internal/research/blocks`) before anything is stored, and the UI
validates every stored result again before it sends it to the browser:

```json
{"schemaVersion": 1, "title": "Morning news", "summary": "Three stories today.",
 "blocks": [
   {"type": "heading", "text": "Kubernetes", "level": 2},
   {"type": "items", "items": [
     {"topic": "Kubernetes", "title": "Kubernetes 1.36 released",
      "summary": "…", "url": "https://kubernetes.io/blog/…", "source": "kubernetes.io",
      "publishedAt": "2026-10-29T06:00:00Z", "importance": 3}]},
   {"type": "markdown", "text": "Background: [the release notes](https://…)."},
   {"type": "table", "columns": ["Model", "Price"], "rows": [["A", "9 €"]]},
   {"type": "stats", "stats": [{"label": "CVEs", "value": "4", "delta": "+2"}]},
   {"type": "links", "links": [{"title": "Changelog", "url": "https://…"}]}
 ]}
```

| Block | Fields |
|-------|--------|
| `heading` | `text`, `level` 2 or 3 |
| `markdown` | `text`: CommonMark without raw HTML (no `<` followed by a letter, `/`, `!` or `?` outside code), without images, links `http(s)` only |
| `items` | `items`: news or findings with `title` and `url` (required), `topic`, `summary`, `source`, `publishedAt` (RFC 3339), `importance` 1 (low), 2 (normal, the default) or 3 (high) |
| `table` | `columns` and `rows` of strings, one cell per column |
| `stats` | `stats`: `label`, `value`, optional `delta` |
| `links` | `links`: `title`, `url` |

Limits (lengths in characters unless bytes):

| What | Limit |
|------|-------|
| whole result | 1 MiB |
| blocks | 100 |
| items, over all items blocks | 200 |
| entries of a stats or links block | 200 |
| markdown block | 64 KiB |
| title, heading, item title, source, link title, stats label, value and delta | 300 |
| summary, item summary | 2000 |
| topic | 100 |
| URL | 2048, absolute `http`/`https` with a host, no user info, no spaces |
| table | 20 columns, 500 rows, 1000 per cell |

Lists must not be empty, control characters other than newlines and tabs
(in markdown and summaries) are refused, and an answer with a problem is
refused as a whole with every problem listed (at most 50). Blocks of an
unknown type are dropped with a warning on the run, the rest is kept.

## Database

Research stores its runs in a Postgres database you provide; the operator
owns the schema (migrations in `internal/research/store/migrations/`). The
same database holds the factory's tables, which only the operator writes
and the UI reads: `history_events` and `agent_calls` (see
[History](design.md#history)), `chat_messages`, and the Desk's milestone
snapshot `desk_milestones`, `desk_milestone_issues`, `desk_follow_ups` and
`desk_milestone_releases` with its backfill records `desk_backfill` and
`desk_backfill_projects` (see [Desk snapshot](design.md#desk-snapshot)).
They expire with `research.historyRetention`; a backfill record goes with
its milestone. The chart is set up for the way the Zalando
postgres-operator provides a database: a host, and one Secret per login
with the keys `username` and `password` (other key names with `usernameKey`
and `passwordKey`). There are no connection URIs.

**Logins.**

| Login | Secret | Where | Rights |
|-------|--------|-------|--------|
| owner | `research.database.ownerSecret` | the release's namespace | owns the database: migrations, grants, retention. Only the operator has it |
| reader | `research.database.readerSecret` | the release's namespace | read only; the UI's login. Its role is `readerRole` |
| writer | `AgentProject.spec.research.writerSecret` | the project's namespace | read, insert, update; the runner container of research runs. Its role must be one of `writerRoles` |
| chat writer | `ui.chat.writerSecret` | the release's namespace | select and insert user rows on `chat_messages`; its role is `chatWriterRole` |

The agent container never gets a login, and the writer cannot delete or
change the schema.

**Connection.** `research.database.host` (empty: research is off), `port`
(default `5432`), `name` (the database, required with a host) and `sslMode`
(default `require`: encrypted, not verified; also `disable`, `verify-ca`,
`verify-full`). The operator, the UI and the runners of research runs all
use these; the runners get them from the operator.

**Grants.** At every start the operator applies the embedded migrations
(table `schema_migrations`, under an advisory lock) and then grants, in the
database's schema:

* `readerRole`: `USAGE` on the schema and `SELECT` on all tables;
* each of `writerRoles`: `USAGE` on the schema and `SELECT`, `INSERT`,
  `UPDATE` on `research_runs`, `research_results` and `research_items`
  (and `REVOKE` on `history_events`, `chat_messages`, `agent_calls` and the
  Desk snapshot tables `desk_milestones`, `desk_milestone_issues`,
  `desk_follow_ups`, `desk_milestone_releases`, `desk_backfill` and
  `desk_backfill_projects`);
* `chatWriterRole`: `USAGE` on the schema and `SELECT`, `INSERT` on
  `chat_messages` (row-level policy: user rows in state `queued` only);
* `ALTER DEFAULT PRIVILEGES` for the reader, so tables of later migrations
  follow without a new grant.

Role names are quoted identifiers, so a name with a dot such as
`agents.research_writer` (Zalando prefixes cross-namespace roles with their
namespace) is one role. Grants are additive: a role removed from the values
keeps its rights until you revoke them by hand. The roles must exist; the
operator does not create them.

**Network.** With `agent.networkPolicy.enabled`, the agent NetworkPolicy
blocks cluster CIDRs, so set `research.database.networkPolicyPeer`
(`namespace` and `podLabels` of the database pods): the chart then adds a
NetworkPolicy `<agent role>-research-db` per agent namespace, which lets
only pods with the label `ai.wiemers.me/research` reach those pods on TCP
`port`. Task pods stay blocked. NetworkPolicies are additive: task pods are
not selected by the second policy and keep the agent policy as it is. The
peer's pods are all pods of `networkPolicyPeer.namespace` when `podLabels`
is empty.

**Operator.** Research is on exactly when `research.database.host` is set;
without it the operator opens no connection. The operator gets the
connection as flags (`--research-db-host`, `--research-db-port`,
`--research-db-name`, `--research-db-sslmode`), the roles as
`--research-reader-role`, `--research-writer-roles` and
`--research-chat-writer-role`, the candidates as
`--research-candidates-quick` and `--research-candidates-deep`
(`engine/model[@effort]` lists) and `--research-max-usage-percent`;
invalid values stop it at start. The owner login comes from `ownerSecret`
as the environment variables `AI_RESEARCH_DB_USER` and
`AI_RESEARCH_DB_PASSWORD`, never as arguments. A host without a name, user
or password stops the operator at start; the chart refuses to render a host
without `name` or `ownerSecret.name`. If the database is unreachable or a
migration fails, the operator keeps running the factory and retries with
backoff (5 s up to 5 min). Once ready, it deletes expired runs with their
results and items at once and then every hour, and with
`--history-retention` also expired history events, agent calls, chat
messages and milestone snapshots.

**Backfill.** With the database ready, the operator also fills the Desk's
milestone snapshot once from GitLab with the closed milestones it planned
before the snapshot existed (see [Desk snapshot](design.md#desk-snapshot)):
one milestone per poll, at most 4 GitLab requests a step and 16 a minute.
It has no setting: it stops by itself once every closed milestone is in,
and lists them again once a day.

**UI.** The chart puts `host`, `port`, `name` and `sslMode` (plus
`ui.research.namespace` and `allowCreate`) into the UI's config file and
the login from `readerSecret` into `AI_OPERATOR_UI_RESEARCH_DB_USER` and
`AI_OPERATOR_UI_RESEARCH_DB_PASSWORD`; it refuses to render without
`readerSecret.name`. The chat writer login from `ui.chat.writerSecret`
becomes `AI_OPERATOR_UI_CHAT_DB_USER` and `AI_OPERATOR_UI_CHAT_DB_PASSWORD`.
The UI only reads with the reader (the operator migrates) and starts
without the database: it connects in the background and retries with
backoff (5 s up to 5 min), the research endpoints answer 503 until then,
and the factory pages work as before. `sslMode: disable` is refused unless
the host is a loopback address (local development). The UI's RBAC for
`agentresearches` and `agentplans` is listed in the
[chart README](../../charts/ai-operator/README.md#research).

### Example: my-cluster

The database `ai_operator` of the Zalando `postgresql`
`ai-operator-postgres` in `ai-operator-system`, with these logins:

| Login (role) | Secret | Namespace | Used by |
|---|---|---|---|
| `research_owner` (owns `ai_operator`) | `research-owner.ai-operator-postgres.credentials.postgresql.acid.zalan.do` | `ai-operator-system` | operator |
| `research_reader` | `research-reader.ai-operator-postgres.credentials.postgresql.acid.zalan.do` | `ai-operator-system` | UI |
| `agents.research_writer` | `agents.research-writer.ai-operator-postgres.credentials.postgresql.acid.zalan.do` | `agents` | runner of research runs |

The chart values (the release in `ai-operator-system`, agents in `agents`):

```yaml
research:
  database:
    host: ai-operator-postgres.ai-operator-system.svc.cluster.local
    port: 5432
    name: ai_operator
    sslMode: require
    ownerSecret:
      name: research-owner.ai-operator-postgres.credentials.postgresql.acid.zalan.do
    readerSecret:
      name: research-reader.ai-operator-postgres.credentials.postgresql.acid.zalan.do
    readerRole: research_reader
    writerRoles: [agents.research_writer]
    networkPolicyPeer:
      namespace: ai-operator-system
      podLabels: {cluster-name: ai-operator-postgres}
agent:
  networkPolicy:
    enabled: true
ui:
  research:
    allowCreate: true      # optional: one-off researches from the UI
    namespace: agents
```

and on each AgentProject in `agents` that runs researches:

```yaml
spec:
  research:
    writerSecret:
      name: agents.research-writer.ai-operator-postgres.credentials.postgresql.acid.zalan.do
```

Add [research rules](../guides/desk.md#research-rules) to
`ui.authorization.rules` for the users who should see the Research section.
The Desk chat also needs `research.database.chatWriterRole` and
`ui.chat.writerSecret`, see
[Add the database](../getting-started/install.md#add-the-database).

`hack/helm-research-values.yaml` renders these values in `make helm-lint`.
