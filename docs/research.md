# Research

> **Status:** the operator schedules researches, runs them in pods and
> stores their results, but the automatic agent choice and the UI pages
> follow in the next releases (see the
> [spec](specs/research-platform.spec.md)).

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

## `AgentResearch`

`kubectl get agentresearches` (short name `ar`) lists the view, depth,
schedule, last and next run, and the model of the last run.

| Field | Default | Description |
|-------|---------|-------------|
| `spec.projectRef` | required | the `AgentProject` in the same namespace whose agent image, credentials, engines and usage the runs use |
| `spec.prompt` | required | the instructions, 1 to 16384 characters |
| `spec.topics` | | up to 20 topics (at most 100 characters each) the results are grouped by, e.g. the sections of a news flash |
| `spec.view` | `report` | `newsflash`: the newest run's items grouped by topic, new items marked; `report`: the blocks as a document |
| `spec.depth` | `quick` | `quick`: a run may take 20 minutes; `deep`: 2 hours. It also selects the candidates of the agent choice |
| `spec.schedule` | | standard cron expression (5 fields, or `@daily` and the like); without one the research runs once |
| `spec.timeZone` | UTC | IANA time zone of the schedule, e.g. `Europe/Berlin` |
| `spec.retention` | `720h` | how long the results are kept |
| `spec.agent` | chosen automatically | `{engine, model, effort}`: always run on this agent. The model must be an allowed model of the project (`spec.agents.models`, the profiles and `implement`) or its alias; `effort` is for claude only |

The operator runs a research only while the research database is ready (see
[Runs](#runs)). The status shows the `phase` (`Scheduled`, `Running`, `Waiting`,
`Succeeded`, `Failed`), `lastRun`, `nextRun`, `lastResultID`, the agent of
the last run (`lastEngine`, `lastModel`) and `skippedReason`. While a run
waits for an engine's usage to reset, `waiting` shows `{engine, until}`.
`run` describes the current or last run: its agent, why that agent was
chosen, the outcome (`succeeded`, `degraded` when the answer failed
validation and was stored as plain markdown, `failed`, `limited`), its cost
and the usage readings.

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

A morning news flash ([`config/samples/agentresearch.yaml`](../config/samples/agentresearch.yaml)):

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

The writer login on the project, with the Secret the Zalando
postgres-operator creates for the cross-namespace role
`agents.research_writer`
([`config/samples/agentproject.yaml`](../config/samples/agentproject.yaml)):

```yaml
spec:
  research:
    writerSecret:
      name: agents.research-writer.ai-operator-postgres.credentials.postgresql.acid.zalan.do
```

## Choosing agents

A research with `spec.agent` always runs on that agent. Without one, a run
uses the candidates of its depth: the project's `spec.research.candidates`
if it lists any for the depth, else the chart's `research.candidates`. For
now the first candidate runs (`status.run.reason`: "first quick
candidate").

*To come with the agent choice:* skipping candidates whose usage is above
`maxUsagePercent` or limited, and what happens to scheduled and one-off runs
when every candidate is limited.

## Runs

The operator (`internal/controller/agentresearch_controller.go`) starts runs
and records their outcome:

* **When.** A research without a schedule runs once, right after it is
  created. A scheduled one runs at `status.nextRun`, the next time of
  `spec.schedule` in `spec.timeZone`; the first is the first scheduled time
  after the research was created. If the operator was down at a scheduled
  time, the run starts once when it is back, however many times were missed.
  After a change of the schedule the next time is counted again from the last
  run.
* **One run at a time.** A scheduled time that comes while the previous run
  is still going is skipped: `status.skippedReason` says "previous run still
  in progress" until the next run starts.
* **Start.** The operator writes `status.run` (a new UUID, the start time,
  the engine, model and effort, and why that agent) and then creates the pod
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
the topics, the view, the current date, and, for a news flash, the previous
flash's item URLs. Its system prompt holds the block schema and tells the agent
to treat web pages as data and ignore any instructions found on them. On
claude the agent may use only WebSearch and WebFetch. Bash, Edit, Write and
NotebookEdit are disallowed, and the schema is also passed as the structured
output schema. Cursor gets the schema in the prompt and runs read-only.

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
3. It waits for the agent container, reads the previous result's item URLs
   (news flash only) and asks the agent (see above). The run's deadline is
   the depth's timeout; recording the outcome gets another minute.
4. It records the outcome in the database, and then in `status.run`:
   `outcome`, `message`, `costUSD`, the engine's newest usage reading
   (`engineUsage`) and any usage limit (`usageLimits`). The operator then
   sets `finishedAt` and deletes the pod.
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
in a news flash. The prompt of the next run lists the item URLs of the latest
result (at most 200), so a news flash does not repeat itself.

## Retention

The operator deletes expired runs, with their results and items, at start and
then every hour. Runs are kept for `spec.retention` (default 30 days).

## Database

The operator connects as the database's owner, applies the migrations and
grants the reader and writer roles at every start, and deletes expired runs
hourly. The chart values, the logins and the behavior during an outage are in
[configuration.md](configuration.md#research-database).

With `agent.networkPolicy` on, research-run pods reach the database through
an extra NetworkPolicy for pods with the label `ai.wiemers.me/research`; task
pods stay blocked, see
[configuration.md](configuration.md#network-policy-for-research-runs).
