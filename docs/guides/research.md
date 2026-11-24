# Research

Besides milestones and issues, the agents can answer research questions. A
[research](../concepts/glossary.md#research) is a question that an agent answers on the web, either once or on
a schedule, and the Desk shows the result. This page shows what you can ask
for, how you turn research on, how the operator picks an agent for each
run, how long results are kept, and what to do when a run does not work.

There are two kinds:

- **Scheduled research**, such as "the latest news, ordered by topic" every
  morning. The Desk shows it as a **news flash**: the items grouped by
  topic, with the stories new since the last run marked.
- **One-off research**: a question goes in once, and a **report** comes
  out, with headings, text, tables, figures and links.

## Read results in the Desk

With research turned on and a [research rule](desk.md#research-rules) for
you, the [Desk](../concepts/glossary.md#desk) lists the researches under
*Research* in the rail (on a phone, in the bottom bar). Each card shows the
view (news flash or report), the schedule or *one-off*, the last and next
run and the agent.

A research's page shows its prompt, its feeds and the latest result:

- a **news flash** groups the items by topic, important and new ones first;
- a **report** shows its blocks in order.

*Run history* lists the stored runs with their outcome and cost, and opens
each one.

With `research: write` in your rule and `ui.research.allowCreate` in the
chart, *New research* starts a one-off research: type the question, choose
the depth, the view and optional topics, and, if you like, the project and
a fixed agent, then *Start research*. The Desk creates the research in
`ui.research.namespace` and opens its page. You may start 10 per hour and
delete the ones you created.

Scheduled researches are managed with `kubectl`, as below.

## Turn it on

Research needs the database from
[Add the database](../getting-started/install.md#add-the-database), and one
more login: the **writer**, which the runner of a research run uses to
store its results. The agent itself never gets a database login.

With the Zalando postgres-operator, add the writer to the `postgresql`
resource. A role with a namespace prefix gets its Secret in that namespace,
here the agents' namespace `agents`:

```yaml
spec:
  users:
    research_owner: []
    research_reader: []
    research_chat_writer: []
    agents.research_writer: []
```

Tell the chart about the writer role, and, with the agent network policy
on, where the database pods are, so research pods may reach them:

```yaml
research:
  database:
    host: ai-operator-postgres.ai-operator-system.svc.cluster.local
    name: ai_operator
    ownerSecret: {name: research-owner.ai-operator-postgres.credentials.postgresql.acid.zalan.do}
    readerSecret: {name: research-reader.ai-operator-postgres.credentials.postgresql.acid.zalan.do}
    readerRole: research_reader
    chatWriterRole: research_chat_writer
    writerRoles: [agents.research_writer]
    networkPolicyPeer:
      namespace: ai-operator-system
      podLabels: {cluster-name: ai-operator-postgres}
ui:
  research:
    allowCreate: true      # one-off researches from the Desk
    namespace: agents      # where they are created
```

Give each [`AgentProject`](../concepts/glossary.md#agentproject) that runs
researches the writer login:

```yaml
spec:
  research:
    writerSecret:
      name: agents.research-writer.ai-operator-postgres.credentials.postgresql.acid.zalan.do
```

Then add a [research rule](desk.md#research-rules) for the people who
should see the *Research* section, and create your first `AgentResearch`.

Research is off while `research.database.host` is empty. Without it, the
operator and the Desk work as they do without research: no research runs
and no *Research* section. With another Postgres, create the writer login
and its Secret yourself in the project's namespace. All database values are
in the [chart README](../../charts/ai-operator/README.md#research).

## Examples

**A morning news flash.** Every day at 07:00 Berlin time, the news of the
last day, grouped by the topics, with the stories new since the previous
flash marked:

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

```sh
kubectl apply -f morning-news.yaml
kubectl -n agents get agentresearches   # short name: ar
```

**A deep one-off report.** It runs once, right after you create it, for up
to 2 hours, on the deep candidates (Opus first), and is kept for 90 days.
*New research* in the Desk creates the same kind of object:

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

**Other agents for one project.** Quick runs of this project's researches
try cursor first and Sonnet second; deep runs keep the chart's candidates:

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

**A fixed agent.** `spec.agent: {engine: claude, model: sonnet}` runs every
run on Sonnet (an alias the project defines, or the full model name) and
never on another agent. The [guard](#leave-room-for-the-factory) still
applies, so the run waits or is skipped while Sonnet's engine is busy.

Every field and its default is in the
[AgentResearch reference](../reference/agentresearch.md).

## Feed sources

A research can list up to 20 feeds (RSS 2.0 or Atom 1.0) in
`spec.sources`. Before each run the runner fetches them and puts the recent
entries into the prompt. The agent groups, weighs and summarizes them, and
may still search the web for details. For a German politics flash:

```yaml
sources:
  - {name: tagesschau Inland, url: https://www.tagesschau.de/inland/index~rss2.xml}
  - {name: Deutschlandfunk Politikportal, url: https://www.deutschlandfunk.de/politikportal-100.rss}
  - {name: Bundestag Aktuelle Themen, url: https://www.bundestag.de/static/appdata/includes/rss/aktuellethemen.rss, topic: Bundestag}
  - {name: Bundesregierung, url: https://www.bundesregierung.de/service/rss/breg-de/1151244/feed.xml, topic: Bundesregierung}
```

- **Which entries:** those published since the previous run started, at
  most 48 hours back; on the first run, the last 24 hours. Entries without a
  date are kept. Links already in the previous result are left out.
- **How many:** at most 30 entries per feed and 200 in all, newest first.
  Titles are cut to 200 characters, summaries to 400.
- **Which feeds:** `https` only, and never addresses inside your network
  (loopback, private, link-local and the like). Each feed gets 15 seconds
  and 4 MiB.
- **When one fails:** the run goes on. The failure is a warning on the run,
  and the agent is told that the feed was not available.

`topic` puts all entries of a feed under that topic.

## Choose agents

Research runs use the agent image, credentials, engines and models of the
project in `spec.projectRef`. Their usage counts towards that project's, so
the issues and the researches share one view of the subscription's budget.
The operator picks the agent of each run when the run is due.

**Candidates.** Without `spec.agent`, a run uses the candidates of its
depth: the project's `spec.research.candidates` if it lists any for that
depth, else the chart's `research.candidates`. By default quick runs try
Sonnet, then cursor; deep runs try Opus, then Sonnet, then cursor. The
lists are in the [reference](../reference/agentresearch.md#candidates). A
candidate the project does not allow is skipped. If none is left, the
research cannot run (`NoAgent`, see [Troubleshooting](#troubleshooting)).

### Leave room for the factory

The candidates are tried in order. The first one runs unless

- its engine has hit a usage limit, or
- its engine has used `research.maxUsagePercent` or more of a usage window
  that has not reset yet.

This guard holds for every run, a fixed `spec.agent` and one-offs included:
research never takes the last part of a subscription that your milestones
need. The effort is capped at the project's `agents.maxEffort`.

The run's *reason* in the Desk and in `status.run.reason` names the agent
and the candidates it passed over:

```text
chosen: cursor/auto (sonnet: 5-hour window at 85 %)
chosen: claude/claude-sonnet-5-5 (opus: 7-day window at 82 %)
chosen: cursor/auto (claude limited until 2026-11-13 00:00 UTC)
```

**When no candidate qualifies,** the run waits for the first window or
limit that ends. The Desk shows "waiting for `claude` until 14:00", in your time zone (the
[pauses guide](pauses.md#in-the-desk) explains it).

- A one-off research waits (phase `Waiting`) and runs then.
- A scheduled run is skipped. Its *skipped* reason names each busy
  candidate and until when, and the next run comes at that time if it is
  before the next scheduled one.

A run that hits a usage limit while it runs ends `limited` and is due again
at once: another candidate runs right away, or it waits or is skipped as
above.

## Runs

- **When.** A research without a schedule runs once, right after you create
  it. A scheduled one runs at the times of its cron schedule, in its time
  zone. The schedule words name that zone, for example "daily at 07:15 UTC",
  because that is when it runs.
  The Desk shows the next run in your time zone. If the operator was down at
  a scheduled time, the run starts once when it is back, however many times
  were missed.
- **One at a time.** A scheduled time that comes while the previous run is
  still going is skipped ("previous run still in progress").
- **How long.** A quick run may take 20 minutes, a deep one 2 hours. Its pod
  is never restarted.
- **What the agent may do.** It may search and fetch web pages, and nothing
  else: no shell, no files, no git, no GitLab. It is told to treat web pages
  as data and to ignore instructions on them.
- **Its answer** is a fixed set of blocks: headings, text, news items,
  tables, figures and links. The runner checks the answer before it stores
  it, and the Desk shows it as text, never as HTML from the agent. If the
  check fails, the agent gets one chance to fix it.

Each run ends with one of these outcomes:

| Outcome | What happened | What is kept |
|---------|---------------|--------------|
| `succeeded` | The answer passed the check. | The result. |
| `degraded` | The answer failed the check, also after the repair. | The raw answer as one block of text, with the errors as warnings. |
| `limited` | The engine hit a usage limit. | Nothing; the run is due again at once. |
| `failed` | Anything else, such as the database or the agent being unreachable. | Nothing; the message says why. |

A news flash marks an item as **new** in the run that first reported its
link, and the next run is told which links the last result had, so it does
not repeat itself.

Deleting a research stops its current run. Its stored runs stay until they
expire.

## Retention

Each run's result is kept for `spec.retention` from the time the run
started (the default is in the
[`AgentResearch` reference](../reference/agentresearch.md#spec)). The operator deletes expired runs every hour. A
changed `retention` applies to later runs only. Deleting an `AgentResearch`
does not delete its stored runs: they expire as usual, and the Desk no
longer shows them.

What a run cost counts in the Desk's [Costs page and Spend
tab](budgets-and-spend.md), kept for `research.historyRetention` like the
rest of the spend, not for `spec.retention`.

## Troubleshooting

Look at the research's status first:

```sh
kubectl -n agents describe agentresearch morning-news
```

- **`DatabaseReady` is `False`** ("waiting for the research database"): the
  operator could not connect, log in as the owner or set up the tables. Its
  log says why. It keeps trying, from every few seconds up to every 5
  minutes, and keeps running your milestones meanwhile. Check the host, the
  `ownerSecret` and its keys, `sslMode`, and that `readerRole` and every
  entry of `writerRoles` exist as roles.
- **`Valid` is `False` with `WriterSecretMissing`**: the project has no
  `spec.research.writerSecret`, or the Secret or one of its keys is missing
  in the project's namespace. The message names the Secret. With Zalando,
  the Secret of a role with a namespace prefix appears once the role is in
  the `postgresql` resource. While a research is not valid, the operator
  checks it again every minute.
- **Other `Valid` reasons**: `ProjectNotFound` (check `projectRef`),
  `InvalidSchedule`, `InvalidAgent` (`spec.agent` is not a model the project
  allows), `NoAgent` (no candidate the project can run), `NoAgentImage`.
- **A run `failed`**: the message in the run history (or
  `status.run.message`) says why. "no report from the runner: …" means the
  pod ended before it stored an outcome: its time ran out, it was evicted,
  or it crashed. A run that cannot reach the database at all usually lacks
  `research.database.networkPolicyPeer`. "permission denied" means the
  writer's role is not in `writerRoles`.
- **A run `degraded`**: the agent's answer broke the rules for answers, for
  example with more than 200 items. The warnings list what was wrong.
- **Waiting or skipped**: no candidate qualifies. The Desk (or
  `status.waiting` and `status.skippedReason`) says which agent is busy
  until when, see [Choose agents](#choose-agents).

## Next

- [AgentResearch reference](../reference/agentresearch.md): every field,
  the status and the default candidates.
- [The Desk](desk.md#research): the *Research* section among the other
  views.
- [Budgets and spend](budgets-and-spend.md): where research costs show up.
