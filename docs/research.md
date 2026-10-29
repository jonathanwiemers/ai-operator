# Research

> **Status:** the `AgentResearch` resource, the `spec.research` field of
> `AgentProject` and the chart values exist, but nothing acts on them yet. The
> controller, the runner's research mode and the UI pages follow in the next
> releases (see the [spec](specs/research-platform.spec.md)).

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

The status shows the `phase` (`Scheduled`, `Running`, `Waiting`,
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

*To come with the agent choice:* candidates per depth, `maxUsagePercent`,
and what happens to scheduled and one-off runs when every candidate is
limited.

## Retention

*To come with the research controller.*

## Database

*To come with the operator's database connection:* the chart values
`research.database.*` (see the
[chart README](../charts/ai-operator/README.md#research)), migrations and
grants, and the network policy for research pods.
