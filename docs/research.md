# Research

Research is the second feature of the platform, next to the factory
(projects, milestones, tasks):

* **Scheduled research**, e.g. "the latest news, ordered by topic" every
  morning, shown as a **news flash** in the UI.
* **One-off research**: a question goes in and a structured **report** comes
  out.

## Concepts

* An `AgentResearch` is a research question: the instructions, optional
  topics, how the UI shows the result (`view`), how long a run may take
  (`depth`), and an optional schedule. Without a schedule it runs once.
* A **run** is a pod like an agent task's, without git. The agent answers
  with **blocks** (headings, markdown, news items, tables, stats, links) from a
  fixed schema. The runner validates them and stores them in Postgres. The UI
  renders these blocks and never renders HTML from the agent.
* A research references an `AgentProject` in its namespace (`projectRef`).
  Runs use that project's agent image, credentials, engines and models. Their
  usage readings count towards the project's, so the factory and research
  share one view of the subscriptions' budget.
* The operator chooses the engine and model of each run from the depth's
  candidates and the usage left, unless `spec.agent` fixes one.
* Results are kept for `retention` (default 30 days) and are searchable.
* The feature is off while the chart value `research.database.host` is empty.
  Without it the operator, the chart's RBAC and the UI behave as they did
  before research existed: no database connection, no research controller,
  no Research section.
* The UI shows researches in its **Research** section (news flash, report,
  run history) and starts one-off researches, see
  [ui.md](ui.md#platform).

To turn research on: set up the [database](#database) and its logins, set
the chart values `research.database.*` (see the [netcup01
example](#example-netcup01)), give each AgentProject that runs researches a
writer login (`spec.research.writerSecret`), and create an `AgentResearch`.

## `AgentResearch`

`kubectl get agentresearches` (short name `ar`) lists the view, depth,
schedule, last and next run, and the model of the last run.

| Field | Default | Description |
|-------|---------|-------------|
| `spec.projectRef` | required | the `AgentProject` in the same namespace whose agent image, credentials, engines and usage the runs use |
| `spec.prompt` | required | the instructions, 1 to 16384 characters |
| `spec.topics` | | up to 20 topics (at most 100 characters each) the results are grouped by, e.g. the sections of a news flash |
| `spec.sources` | | up to 20 feeds (RSS 2.0 or Atom 1.0) the runner reads before each run: `url` (https), optional `name` and `topic` (the topic the feed's entries belong to). See [Feed sources](#feed-sources) |
| `spec.view` | `report` | `newsflash`: the newest run's items grouped by topic, new items marked; `report`: the blocks as a document |
| `spec.depth` | `quick` | `quick`: a run may take 20 minutes; `deep`: 2 hours. It also selects the candidates of the agent choice |
| `spec.schedule` | | standard cron expression (5 fields, or `@daily` and the like); without one the research runs once |
| `spec.timeZone` | UTC | IANA time zone of the schedule, e.g. `Europe/Berlin` |
| `spec.retention` | `720h` | how long the results are kept |
| `spec.agent` | chosen automatically | `{engine, model, effort}`: always run on this agent. The model must be an allowed model of the project (`spec.agents.models`, the profiles and `implement`) or its alias; `effort` is for claude only |

The operator runs a research only while the research database is ready (see
[Runs](#runs)). The status, written by the operator (and `run` partly by the
runner):

| Field | Description |
|-------|-------------|
| `status.phase` | `Scheduled` (waiting for its next time), `Running`, `Waiting` (a one-off waiting for usage to reset), `Succeeded`, `Failed` (a one-off's last run) |
| `status.lastRun`, `status.nextRun` | when the last run finished and the next is due |
| `status.lastResultID` | the run ID of the newest stored result (`succeeded` and `degraded` runs) |
| `status.lastEngine`, `status.lastModel` | the agent of the last run |
| `status.skippedReason` | why the last due run did not start (no candidate qualifies, previous run still in progress) |
| `status.waiting` | `{engine, until}` while no candidate qualifies, see [Choosing agents](#choosing-agents) |
| `status.run` | the current or last run: `id`, `startedAt`, `finishedAt`, `engine`, `model`, `effort`, `reason` (the choice), `outcome` (`succeeded`, `degraded` when the answer failed validation and was stored as plain markdown, `failed`, `limited`), `message`, `costUSD`, `engineUsage`, `usageLimits`, `calls` (the run's engine calls, see below) |
| `status.conditions` | `Valid`, `DatabaseReady`, `Running`, see [Runs](#runs) and [Troubleshooting](#troubleshooting) |

The UI marks researches it created with the annotation
`ai.wiemers.me/created-by`. The pods of a research's runs carry the label
`ai.wiemers.me/research`.

## `spec.research` on `AgentProject`

| Field | Default | Description |
|-------|---------|-------------|
| `research.writerSecret.name` | required with `writerSecret` | Secret in the project's namespace holding the database login the runner of a research run writes its results with |
| `research.writerSecret.usernameKey` | `username` | |
| `research.writerSecret.passwordKey` | `password` | |
| `research.candidates.quick`, `research.candidates.deep` | the chart's `research.candidates` | up to 5 `{engine, model, effort}` per depth, tried in order when a research has no `spec.agent` |

The database's host, port, name and `sslMode` come from the operator's
configuration (chart values `research.database.*`). The login must be one of
the chart's `research.database.writerRoles`, the roles the migrations grant
write access to. Only the runner container gets the login. The agent
container gets no database credentials.

The operator checks the login before every run: without
`spec.research.writerSecret`, or when the Secret or one of its keys is
missing in the project's namespace, the research's condition `Valid` is
`False` with the reason `WriterSecretMissing` and a message naming the
Secret, and no pod starts. The check runs again every minute and whenever the
project changes.

## Examples

**A morning news flash**
([`config/samples/agentresearch.yaml`](../config/samples/agentresearch.yaml)):
every day at 07:00 Berlin time, the news of the last day, grouped by the
topics, with the stories new since the previous flash marked.

```yaml
apiVersion: ai.wiemers.me/v1alpha1
kind: AgentResearch
metadata:
  name: morning-news
  namespace: agents
spec:
  projectRef: shop
  prompt: |
    Collect the most important news of the last 24 hours on the topics below.
    Prefer primary sources; one item per story, with its link.
  topics: [Kubernetes, Go, AI agents]
  view: newsflash
  depth: quick
  schedule: "0 7 * * *"
  timeZone: Europe/Berlin
```

**A deep one-off report**: runs once, right after it is created, for up to
2 hours, on the deep candidates (Opus first). Kept for 90 days. The UI's
one-off form creates the same kind of object (with a generated name and the
`ai.wiemers.me/created-by` annotation).

```yaml
apiVersion: ai.wiemers.me/v1alpha1
kind: AgentResearch
metadata:
  name: heat-pumps
  namespace: agents
spec:
  projectRef: shop
  prompt: |
    Compare air-to-water heat pumps for a 150 m² house built in 1975 in
    Germany: efficiency in cold weather, noise, prices including
    installation, and the current subsidies. Cite your sources.
  view: report
  depth: deep
  retention: 2160h
```

**The writer login and a candidates override on the project**, with the
Secret the Zalando postgres-operator creates for the cross-namespace role
`agents.research_writer`
([`config/samples/agentproject.yaml`](../config/samples/agentproject.yaml)).
Quick runs of this project's researches try cursor first and Sonnet second;
deep runs keep the chart's candidates:

```yaml
apiVersion: ai.wiemers.me/v1alpha1
kind: AgentProject
metadata:
  name: shop
  namespace: agents
spec:
  # … gitlab, agents and the other fields of the project …
  research:
    writerSecret:
      name: agents.research-writer.ai-operator-postgres.credentials.postgresql.acid.zalan.do
    candidates:
      quick:
        - {engine: cursor, model: auto}
        - {engine: claude, model: claude-sonnet-5-5, effort: medium}
```

**A fixed agent**: `spec.agent: {engine: claude, model: sonnet}` runs every
run on Sonnet (an alias the project defines, or the full model name) and
never on another agent; the [guard](#choosing-agents) still applies, so the
run waits or is skipped while Sonnet's engine is busy.

## Feed sources

When `spec.sources` lists feeds, the runner (`internal/research/feeds`,
`internal/runner/research_feeds.go`) fetches them before it asks the agent
and puts the recent entries into the prompt as data. The agent groups,
weighs and summarises them, and may still search and fetch for details. A
failing feed is a warning on the run (and a "not available this run" line in
the prompt), never a failed run.

**Window.** Entries published since the previous run's start are kept, at
most 48 h back; without a previous run the window is 24 h. Entries without a
date are kept. URLs already in the previous result are dropped
(`blocks.URLHash`). A feed with nothing left is shown to the agent as having
no new entries.

**Caps.** At most 30 entries per feed and 200 in all (newest first). Titles
are cut to 200 characters, summaries to 400.

**Fetch.** https only; the dialer refuses loopback, private, link-local,
CGNAT, `0.0.0.0/8`, unspecified and multicast addresses (checked on the connected IP, so
DNS rebinding and redirects are covered). Per feed: 15 s timeout, body
capped at 4 MiB. At most 4 feeds in parallel, and all of them within the
first minute of the run. User agent:
`ai-operator-research/1 (+https://gitlab.com/jo.wiemers/ai-operator)`.

Example for a German politics flash:

```yaml
sources:
  - {name: tagesschau Inland, url: https://www.tagesschau.de/inland/index~rss2.xml}
  - {name: Deutschlandfunk Politikportal, url: https://www.deutschlandfunk.de/politikportal-100.rss}
  - {name: Bundestag Aktuelle Themen, url: https://www.bundestag.de/static/appdata/includes/rss/aktuellethemen.rss, topic: Bundestag}
  - {name: Bundesregierung, url: https://www.bundesregierung.de/service/rss/breg-de/1151244/feed.xml, topic: Bundesregierung}
```

## Choosing agents

The operator chooses the agent of each run when the run is due, before it
creates a pod (`internal/research/choose.go`), from the usage the
referenced AgentProject knows of (`status.engineUsage` and
`status.usageLimits`, fed by tasks and research runs alike) plus what the
research's last run reported.

**Candidates.** A research with `spec.agent` has that agent as its only
candidate. Without one, a run uses the candidates of its depth: the
project's `spec.research.candidates` if it lists any for the depth, else the
chart's `research.candidates`:

| Depth | Default candidates, in order |
|---|---|
| `quick` | `claude/claude-sonnet-5-5`, `cursor/auto` |
| `deep` | `claude/claude-opus-5-5` (effort `high`), `claude/claude-sonnet-5-5`, `cursor/auto` |

A candidate the project cannot run is skipped: an engine without any allowed
model of the project (`agents.models`, the profiles and `agents.implement`),
or a model the project does not allow. If no candidate is left at all, the
research is `Valid=False` with the reason `NoAgent`.

**The guard.** The candidates are tried in order; the first one runs unless

* its engine is limited (`status.usageLimits` of the project, or a limit the
  research's last run hit), or
* the fullest usage window of its engine's freshest reading that has not
  reset since is at or above `research.maxUsagePercent` (default 70). A
  window whose reset time has passed no longer counts.

The guard applies to every research run, a fixed `spec.agent` and one-offs
included: research never takes the last part of a subscription the factory
needs. The chosen effort is capped at the project's `agents.maxEffort`;
cursor runs get no effort.

**The reason.** `status.run.reason`, stored with the run as its choice
reason, names the agent and the candidates passed over, with their alias
where the project has one:

```text
chosen: cursor/auto (sonnet: 5-hour window at 85 %)
chosen: claude/claude-sonnet-5-5 (opus: 7-day window at 82 %)
chosen: cursor/auto (claude limited until 2026-11-13 00:00 UTC)
```

**When no candidate qualifies,** the run waits for the earliest window reset
or limit end among the blocked candidates, in `status.waiting`
(`{engine, until}`, shown in the UI as "waiting for `claude` until 14:00"):

* a **one-off** stays in phase `Waiting` and runs at `until` (choosing
  again then);
* a **scheduled** run is skipped: `status.skippedReason` lists every blocked
  candidate until when, e.g. "no candidate qualifies: sonnet: 5-hour window
  at 85 % until 2026-11-12 14:00 UTC; cursor limited until 2026-11-13 00:00
  UTC", and `status.nextRun` becomes `until` if that comes before the next
  scheduled time (else the next scheduled time).

Both are cleared when a run starts.

**A run that ends `limited`** (it hit a usage limit while running) is due
again at once, with its limit known: another candidate runs right away, or
the research waits or is skipped as above.

## Runs

The operator (`internal/controller/agentresearch_controller.go`) starts runs
and records their outcome:

* **When.** A research without a schedule runs once, right after it is
  created. A scheduled one runs at `status.nextRun`, the next time of
  `spec.schedule` in `spec.timeZone`; the first is the first scheduled time
  after the research was created. If the operator was down at a scheduled
  time, the run starts once when it is back, however many times were missed.
  After a change of the schedule the next time is counted again from the last
  run. A due run without an agent with usage left waits or is skipped, see
  [Choosing agents](#choosing-agents).
* **One run at a time.** A scheduled time that comes while the previous run
  is still going is skipped: `status.skippedReason` says "previous run still
  in progress" until the next run starts.
* **Start.** The operator writes `status.run` (a new UUID, the start time,
  the engine, model and effort the choice gave, and its reason) and then creates the pod
  `<research>-<first 8 characters of the run ID>`. The runner reads its agent
  from `status.run`.
* **End.** The runner records the outcome in the database and then in
  `status.run.outcome`. When the outcome is there, or the pod has ended or is
  gone, the operator finishes the run: without the runner's report the run
  failed, with a message such as "no report from the runner: pod
  DeadlineExceeded: …", in the database and the status. It then sets
  `finishedAt`, `lastRun`, `lastEngine`, `lastModel`, `lastResultID` (for
  `succeeded` and `degraded` runs) and the `phase` (`Scheduled` for a
  scheduled research, else `Succeeded` or `Failed`), and deletes the pod. A
  run that ended `limited` is due again at once. A
  pod that never starts is ended 5 minutes after its deadline.
* **Conditions.** `Valid` (`False` with the reason `ProjectNotFound`,
  `InvalidSchedule`, `InvalidAgent`, `NoAgent`, `NoAgentImage` or
  `WriterSecretMissing`; no run starts), `DatabaseReady` (`False` while the
  operator cannot reach or migrate the database; checked every 30 seconds),
  and `Running`.

A run's pod has a deadline of the depth's timeout plus 2 minutes and is never
restarted. Like a task's pod it has a `runner` and an `agent` container, but
no git, no GitLab token and no workspace volume (see
[design.md](design.md#pod-topology)). Deleting a research deletes the pod of
its current run.

## The agent's answer

The runner (`internal/runner/research.go`) asks the agent with the request,
any [feed entries](#feed-sources) it just fetched, the topics, the view, the
current date, and, for a news flash, the previous flash's item URLs. Its
system prompt holds the block schema, tells the agent to use the feed
entries first, then web search and fetch, and to treat web pages as data and
ignore any instructions found on them. On claude the agent may use only
WebSearch and WebFetch. Bash, Edit, Write and NotebookEdit are disallowed,
and the schema is also passed as the structured output schema. Cursor gets
the schema in the prompt and runs read-only.

The answer is validated against the block schema. If it fails, the agent gets
one repair round with the validation errors. It continues its session if it
has one, otherwise the runner sends the whole prompt again. If the repaired
answer is still invalid, or the repair call fails, the raw answer is kept as a
single `markdown` block inside a code fence, and the run is `degraded`, with
the validation errors among its warnings. A usage limit ends the run without a
result.

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
   run's deadline is the depth's timeout; recording the outcome gets another
   minute.
4. It records the outcome in the database, and then in `status.run`:
   `outcome`, `message`, `costUSD`, the engine's newest usage reading
   (`engineUsage`), any usage limit (`usageLimits`) and its engine calls
   (`calls`, seq 1..n, kind and role `research`). The operator then writes
   the calls to `agent_calls` (project = `spec.projectRef`, task = the
   research name, run 0, keyed `research/<UID>/<run ID>/<seq>`; the pod
   has no rights on that table), sets `finishedAt` and deletes the pod. A
   failed write is retried with the next reconcile.
   The project's next sync merges the reading and an active limit into the
   project's `status.engineUsage` and `status.usageLimits`, where task runs
   (and later research runs) see them.

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

Each run is a row in the database with its agent, the reason for that choice,
its outcome, cost and warnings. A run has one outcome: whichever of the
runner and the operator records it first wins. If the pod ends before the
runner gets to record the outcome, the operator records it instead. A later
finish, such as the operator noticing that the pod's deadline passed, does not
change it.

A run's result is stored with one row per news item, identified by its URL
(scheme and host lowercased, without the fragment). An item remembers when the
research first reported its URL. That date is what marks an item as **new**
in a news flash: the UI flags it in the run that first reported the URL
(`"new": true`), not in later runs. The prompt of the next run lists the item URLs of the latest
result (at most 200), so a news flash does not repeat itself.

## Block schema v1

The agent answers with one JSON object; the runner validates it
(`internal/research/blocks`) before anything is stored, and the UI validates
every stored result again before it sends it to the browser:

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

## Retention

Every run gets an expiry when it starts: its start time plus
`spec.retention` (default `720h`, 30 days). The operator, as the database's
owner, deletes expired runs with their results and items at its start and
then every hour. A changed `retention` applies to later runs only. Deleting
an `AgentResearch` does not delete its stored runs; they expire as usual (and
the UI no longer shows or searches them).

## Database

Research stores its runs in a Postgres database you provide; the operator
owns the schema. The chart is set up for the way the Zalando
postgres-operator provides a database: a host, and one Secret per login with
the keys `username` and `password` (other key names with `usernameKey` and
`passwordKey`). There are no connection URIs.

**Three logins.**

| Login | Secret | Where | Rights |
|-------|--------|-------|--------|
| owner | `research.database.ownerSecret` | the release's namespace | owns the database: migrations, grants, retention. Only the operator has it |
| reader | `research.database.readerSecret` | the release's namespace | read only; the UI's login. Its role is `readerRole` |
| writer | `AgentProject.spec.research.writerSecret` | the project's namespace | read, insert, update; the runner container of research runs. Its role must be one of `writerRoles` |
| chat writer | (wired with the Desk chat UI) | the release's namespace | select and insert user rows on `chat_messages`; its role is `chatWriterRole` |

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
  (and `REVOKE` on `history_events`, `chat_messages` and `agent_calls`);
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
NetworkPolicy `<agent role>-research-db` per agent namespace, which lets only
pods with the label `ai.wiemers.me/research` reach those pods on TCP
`port`. Task pods stay blocked. Details and the operator flags in
[configuration.md](configuration.md#research-database).

### Example: netcup01

The database `ai_operator` of the Zalando `postgresql` `ai-operator-postgres`
in `ai-operator-system`, with these logins:

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

Add [research rules](ui.md#research-rules) to `ui.authorization.rules` for
the users who should see the Research section.

## Troubleshooting

* **`DatabaseReady` is `False`** (reason `NotReady`, "waiting for the
  research database"): the operator could not connect, log in as the owner,
  migrate or grant. Its log says why ("research database not ready,
  retrying"); it retries with backoff from 5 seconds up to 5 minutes and
  keeps running the factory meanwhile. Check the host, the `ownerSecret`
  and its keys, `sslMode`, and that `readerRole` and every `writerRoles`
  entry exist as roles. No research runs until it is `True`.
* **`Valid` is `False` with `WriterSecretMissing`**: the project has no
  `spec.research.writerSecret`, or the Secret or one of its keys is missing
  in the project's namespace. The message names the Secret. With Zalando
  the Secret of a cross-namespace role appears in the agents' namespace once
  the role is in the `postgresql` resource. Checked again every minute.
* **Other `Valid` reasons**: `ProjectNotFound` (`projectRef`),
  `InvalidSchedule`, `InvalidAgent` (`spec.agent` is not an allowed model of
  the project), `NoAgent` (no candidate the project can run), `NoAgentImage`.
* **A run `failed`**: `status.run.message` and the run's error in the UI's
  run history say why. "no report from the runner: …" means the pod ended
  without recording an outcome (deadline, eviction, crash). A runner that
  cannot connect at all usually lacks the research-db NetworkPolicy
  (`networkPolicyPeer`); "permission denied" means the writer's role is not
  among `writerRoles`.
* **A run `degraded`**: the agent's answer failed validation, and again
  after the repair round. The raw answer is stored as one fenced markdown
  block, and the run's warnings list the validation errors (e.g. a limit of
  the [block schema](#block-schema-v1) such as more than 200 items).
* **Waiting or skipped**: no candidate qualifies; `status.waiting` and
  `status.skippedReason` say which agent is busy until when, see
  [Choosing agents](#choosing-agents).
