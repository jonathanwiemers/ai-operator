# AgentResearch

An `AgentResearch` is a research question an agent answers once or on a
schedule, and `spec.research` on an
[`AgentProject`](../concepts/glossary.md#agentproject) holds the database
login and the agent choice of that project's research runs. This page lists
every field with its default, and the status the operator writes. How to
use them is in the [Research guide](../guides/research.md).

## Example

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
  sources:
    - {name: Kubernetes blog, url: https://kubernetes.io/feed.xml, topic: Kubernetes}
  view: newsflash
  depth: quick
  schedule: "0 7 * * *"
  timeZone: Europe/Berlin
  retention: 720h
```

`kubectl get agentresearches` (short name `ar`) lists the view, depth,
schedule, last and next run, and the model of the last run.

## `spec`

| Field | Meaning | Default |
|-------|---------|---------|
| `projectRef` | The `AgentProject` in the same namespace whose agent image, credentials, engines and usage the runs use. | required |
| `prompt` | The instructions, 1 to 16384 characters. | required |
| `topics` | Up to 20 topics (at most 100 characters each) the results are grouped by, such as the sections of a news flash. | – |
| `sources` | Up to 20 feeds (RSS 2.0 or Atom 1.0) read before each run: `url` (`https` only), optional `name` and `topic` (the topic of the feed's entries). See [Feed sources](../guides/research.md#feed-sources). | – |
| `view` | `newsflash`: the newest run's items grouped by topic, new items marked. `report`: the blocks as a document. | `report` |
| `depth` | `quick`: a run may take 20 minutes. `deep`: 2 hours. It also picks the [candidates](#candidates). | `quick` |
| `schedule` | A standard cron expression (5 fields, or `@daily` and the like). Without one the research runs once, right after it is created. | – |
| `timeZone` | IANA time zone of the schedule, such as `Europe/Berlin`. | `UTC` |
| `retention` | How long each run's result is kept, counted from the run's start. | `720h` (30 days) |
| `agent` | `{engine, model, effort}`: always run on this agent. The model must be one the project allows (`spec.agents.models`, the profiles and `implement`) or its alias; `effort` is for claude only. | chosen per run |

The Desk marks the researches it created with the annotation
`ai.wiemers.me/created-by`. The pods of a research's runs carry the label
`ai.wiemers.me/research`.

## `status`

The operator writes the status, and the runner of a run fills in parts of
`run`.

| Field | Meaning |
|-------|---------|
| `phase` | `Scheduled` (waiting for its next time), `Running`, `Waiting` (a one-off waiting for usage to reset), `Succeeded` or `Failed` (a one-off's last run). |
| `lastRun`, `nextRun` | When the last run started and when the next is due. The end of a run is `status.run.finishedAt`. |
| `lastResultID` | The run ID of the newest stored result (`succeeded` and `degraded` runs). |
| `lastEngine`, `lastModel` | The agent of the last run. |
| `skippedReason` | Why the last due run did not start: no candidate qualifies, or the previous run is still in progress. |
| `waiting` | `{engine, until}` while no candidate qualifies, see [Choose agents](../guides/research.md#choose-agents). |
| `runCount` | How many runs have started. |
| `run` | The current or last run, see below. |
| `conditions` | `Valid`, `DatabaseReady` and `Running`, see below. |

`status.run` has:

- `id` and `number` (from 1, counted by `runCount`);
- `startedAt` and `finishedAt`;
- `engine`, `model`, `effort` and `reason`: the agent and why it was
  chosen;
- `outcome`: `succeeded`, `degraded` (the answer failed the check and was
  stored as plain text), `failed` or `limited` (it hit a usage limit), and
  its `message`;
- `costUSD`, `engineUsage`, `usageLimits` and `calls`: what the run cost,
  the usage it saw and its engine calls.

### Conditions

| Condition | `False` means |
|-----------|---------------|
| `Valid` | No run starts. The reason is `ProjectNotFound`, `InvalidSchedule`, `InvalidAgent`, `NoAgent`, `NoAgentImage` or `WriterSecretMissing`. Checked again every minute. |
| `DatabaseReady` | Reason `NotReady`: the operator cannot reach or set up the research database. Checked again every 30 seconds. |
| `Running` | No run is in progress. |

What to do about each is in
[Troubleshooting](../guides/research.md#troubleshooting).

## `spec.research` on `AgentProject`

```yaml
spec:
  research:
    writerSecret:
      name: agents.research-writer.ai-operator-postgres.credentials.postgresql.acid.zalan.do
    candidates:
      quick:
        - {engine: cursor, model: auto}
        - {engine: claude, model: claude-sonnet-5-5, effort: medium}
```

| Field | Meaning | Default |
|-------|---------|---------|
| `writerSecret.name` | Secret in the project's namespace with the database login the runner of a research run stores its results with. Its role must be in the chart's `research.database.writerRoles`. | required for research runs |
| `writerSecret.usernameKey` | The key of the user name in the Secret. | `username` |
| `writerSecret.passwordKey` | The key of the password. | `password` |
| `candidates.quick`, `candidates.deep` | Up to 5 `{engine, model, effort}` per depth, tried in order for researches without `spec.agent`. | the chart's `research.candidates` |

Only the runner container of a research run gets the login; the agent
container gets no database credentials. The database's host, port, name and
`sslMode` come from the chart values `research.database.*`.

Without `writerSecret`, or when the Secret or one of its keys is missing,
every research of the project is `Valid=False` with the reason
`WriterSecretMissing`, and the message names the Secret.

## Candidates

The chart's `research.candidates`, used when the project lists none for the
depth:

| Depth | Default candidates, in order |
|-------|------------------------------|
| `quick` | `claude/claude-sonnet-5-5`, `cursor/auto` |
| `deep` | `claude/claude-opus-5-5` (effort `high`), `claude/claude-sonnet-5-5`, `cursor/auto` |

A candidate whose engine or model the project does not allow is skipped.
Each run uses a candidate only while its engine has no usage limit and has
used less than `research.maxUsagePercent` (default `70`) of every usage
window that has not reset yet. The effort is capped at the project's
`agents.maxEffort`; cursor runs get no effort.

The database values, the candidates, `research.maxUsagePercent`,
`research.historyRetention` and `ui.research.*` are in the
[chart README](../../charts/ai-operator/README.md#research).

## Next

- [Research](../guides/research.md): turn it on, examples, and what to do
  when a run fails.
- [AgentProject](agentproject.md): the other fields of the project.
- [Agents and models](agents-and-models.md): engines, models, aliases and
  `maxEffort`.
